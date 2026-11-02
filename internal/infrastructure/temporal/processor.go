package temporal

import (
	"fmt"
	"sync"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	idempotency "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/infrastructure/realtime"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	buildActivities "github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	buildWorkflows "github.com/thekrauss/kubemanager/internal/modules/builds/workflows"
	projectActivities "github.com/thekrauss/kubemanager/internal/modules/projects/activities"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectWorkflows "github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
	siteActivities "github.com/thekrauss/kubemanager/internal/modules/sites/activities"
	siteRepository "github.com/thekrauss/kubemanager/internal/modules/sites/repository"
	siteWorkflows "github.com/thekrauss/kubemanager/internal/modules/sites/workflows"
	workloadActivities "github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
	workloadRepository "github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	workloadWorkflows "github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

type WorkerConfig struct {
	Client       client.Client
	Config       *configs.GlobalConfig
	Logger       *zap.SugaredLogger
	K8sClient    *kubernetes.Clientset
	K8sConfig    *rest.Config
	DB           *gorm.DB
	RBACSvc      authSvc.IRBACService
	Idempotency  idempotency.AtomicManager
	Publisher    realtime.Publisher
	ObjectStore  objectstore.Store                // nil si sites statiques désactivés
	Builds       *buildActivities.Activities      // nil si builds désactivés
	RegistryPull *workloadActivities.RegistryPull // nil si builds désactivés
	Variables    workloadRepository.VariableStore
}

type TemporalProcessor struct {
	cfg WorkerConfig

	mu      sync.Mutex
	worker  worker.Worker
	started bool
	errCh   chan error
}

func NewTemporalProcessor(cfg WorkerConfig) *TemporalProcessor {
	return &TemporalProcessor{cfg: cfg}
}

func NewWorkerManager(cfg WorkerConfig) *TemporalProcessor {
	return NewTemporalProcessor(cfg)
}

func (p *TemporalProcessor) Start() (<-chan error, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return p.errCh, fmt.Errorf("temporal processor already started")
	}

	tc := p.cfg.Config.Temporal.WithWorkerDefaults()
	// Réglage global du SDK, à poser avant le premier worker.
	worker.SetStickyWorkflowCacheSize(tc.StickyCacheSize)
	w := worker.New(p.cfg.Client, tc.TaskQueue, workerOptions(tc))

	metricsClient, err := metricsv1.NewForConfig(p.cfg.K8sConfig)
	if err != nil {
		return nil, fmt.Errorf("metrics client: %w", err)
	}

	projDBActs := &projectActivities.ProjectDBActivities{
		Repo:         projectRepo.NewProjectRepository(p.cfg.DB),
		WorkloadRepo: workloadRepository.NewWorkloadRepository(p.cfg.DB),
		Logger:       p.cfg.Logger,
	}
	projK8sActs := &projectActivities.ProjectK8sActivities{
		K8sClient:     p.cfg.K8sClient,
		MetricsClient: metricsClient,
		Logger:        p.cfg.Logger,
		Rbac:          p.cfg.RBACSvc,
		Idempotency:   p.cfg.Idempotency,
	}
	workloadDBActs := &workloadActivities.WorkloadDBActivities{
		DB:        p.cfg.DB,
		Logger:    p.cfg.Logger,
		Repo:      workloadRepository.NewWorkloadRepository(p.cfg.DB),
		Publisher: p.cfg.Publisher,
	}
	helmActs := &workloadActivities.WorkloadActivities{
		K8sConfig:   p.cfg.K8sConfig,
		K8sClient:   p.cfg.K8sClient,
		Idempotency: p.cfg.Idempotency,
		Registry:    p.cfg.RegistryPull,
		Variables:   p.cfg.Variables,
	}

	p.registerWorkflows(w)
	p.registerActivities(w, projDBActs, projK8sActs, workloadDBActs, helmActs)

	// Toujours enregistrées : DeleteProjectWorkflow appelle
	// DeleteProjectSiteObjects même quand les sites sont désactivés
	// (ObjectStore nil, gérée par l'activité).
	{
		sc := p.cfg.Config.Sites
		w.RegisterWorkflow(siteWorkflows.DeploySiteWorkflow)
		w.RegisterWorkflow(siteWorkflows.DeleteSiteWorkflow)
		p.registerActivities(w, &siteActivities.SiteActivities{
			Repo:  siteRepository.NewSiteRepository(p.cfg.DB),
			Store: p.cfg.ObjectStore,
			Limits: siteActivities.Limits{
				MaxArchiveBytes:   sc.MaxArchiveBytes,
				MaxExtractedBytes: sc.MaxExtractedBytes,
				MaxFiles:          sc.MaxFiles,
			},
			Logger: p.cfg.Logger,
		})
	}

	if p.cfg.Builds != nil {
		w.RegisterWorkflow(buildWorkflows.BuildImageWorkflow)
		p.registerActivities(w, p.cfg.Builds)
	}

	p.worker = w
	p.errCh = make(chan error, 1)
	p.started = true

	go func() {
		p.cfg.Logger.Infow("temporal worker starting",
			"queue", p.cfg.Config.Temporal.TaskQueue,
			"workflows", []string{projectWorkflows.CreateProjectWorkflowName, workloadWorkflows.DeployWorkloadWorkflowName},
		)

		if err := w.Run(nil); err != nil {
			p.cfg.Logger.Errorw("temporal worker exited with error", "error", err)
			p.errCh <- err
			return
		}
		p.cfg.Logger.Infow("temporal worker exited cleanly")
		p.errCh <- nil
	}()

	return p.errCh, nil
}

// workerOptions : bornes du worker sur une petite machine ; à l'arrêt, les
// activités en cours ont WorkerStopTimeout pour finir (à garder sous le délai
// d'arrêt du conteneur, stop_grace_period).
func workerOptions(tc configs.TemporalConfig) worker.Options {
	return worker.Options{
		MaxConcurrentActivityExecutionSize:     tc.MaxConcurrentActivities,
		MaxConcurrentWorkflowTaskExecutionSize: tc.MaxConcurrentWorkflowTasks,
		WorkerStopTimeout:                      tc.WorkerStopTimeout,
	}
}

func (p *TemporalProcessor) Shutdown() {
	p.mu.Lock()
	w := p.worker
	started := p.started
	p.mu.Unlock()

	if !started || w == nil {
		return
	}
	w.Stop()
	p.cfg.Logger.Infow("temporal worker stopped")
}

func (p *TemporalProcessor) Worker() worker.Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.worker
}

func (p *TemporalProcessor) registerWorkflows(w worker.Worker) {
	w.RegisterWorkflow(projectWorkflows.CreateProjectWorkflow)
	w.RegisterWorkflow(projectWorkflows.DeleteProjectWorkflow)
	w.RegisterWorkflow(workloadWorkflows.DeployWorkloadWorkflow)
	w.RegisterWorkflow(workloadWorkflows.DeleteWorkloadWorkflow)
}

func (p *TemporalProcessor) registerActivities(w worker.Worker, acts ...interface{}) {
	for _, a := range acts {
		w.RegisterActivity(a)
	}
}
