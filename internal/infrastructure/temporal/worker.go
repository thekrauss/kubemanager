package temporal

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectActivities "github.com/thekrauss/kubemanager/internal/modules/projects/activities"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectWorkflows "github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
	workloadActivities "github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
	workloadWorkflows "github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

type WorkerConfig struct {
	Client    client.Client
	Config    *configs.GlobalConfig
	Logger    *zap.SugaredLogger
	K8sClient *kubernetes.Clientset
	K8sConfig *rest.Config
	DB        *gorm.DB
	RBACSvc   authSvc.IRBACService
}

func NewWorkerManager(cfg WorkerConfig) *WorkerConfig {
	return &cfg
}

func (m *WorkerConfig) Start() worker.Worker {
	w := worker.New(m.Client, m.Config.Temporal.TaskQueue, worker.Options{})

	metricsClient, err := metricsv1.NewForConfig(m.K8sConfig)
	if err != nil {
		m.Logger.Fatalf("Impossible de créer le client metrics: %v", err)
	}

	projDBActs := &projectActivities.ProjectDBActivities{
		Repo:   projectRepo.NewProjectRepository(m.DB),
		Logger: m.Logger,
	}

	projK8sActs := &projectActivities.ProjectK8sActivities{
		K8sClient:     m.K8sClient,
		MetricsClient: metricsClient,
		Logger:        m.Logger,
		Rbac:          m.RBACSvc,
	}

	workloadDBActs := &workloadActivities.WorkloadDBActivities{
		DB:     m.DB,
		Logger: m.Logger,
	}

	helmActs := &workloadActivities.WorkloadActivities{
		K8sConfig: m.K8sConfig,
		K8sClient: m.K8sClient,
	}

	m.registerWorkflows(w)
	m.registerActivities(w, projDBActs, projK8sActs, workloadDBActs, helmActs)

	go m.run(w)

	return w
}

func (m *WorkerConfig) registerWorkflows(w worker.Worker) {
	w.RegisterWorkflow(projectWorkflows.CreateProjectWorkflow)
	w.RegisterWorkflow(workloadWorkflows.DeployWorkloadWorkflow)
}

func (m *WorkerConfig) registerActivities(w worker.Worker, acts ...interface{}) {
	for _, a := range acts {
		w.RegisterActivity(a)
	}
}

func (m *WorkerConfig) run(w worker.Worker) {
	m.Logger.Infow("Temporal Worker started",
		"queue", m.Config.Temporal.TaskQueue,
		"modules", []string{"Projects", "Workloads", "RBAC"},
	)

	if err := w.Run(worker.InterruptCh()); err != nil {
		m.Logger.Fatalw("Unable to start worker", "error", err)
	}
}
