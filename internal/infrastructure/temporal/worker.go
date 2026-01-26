package temporal

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	projectActivities "github.com/thekrauss/kubemanager/internal/modules/projects/activities"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	projectWorkflows "github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
	workloadActivities "github.com/thekrauss/kubemanager/internal/modules/workloads/activities"
	workloadWorkflows "github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
	metricsv1 "k8s.io/metrics/pkg/client/clientset/versioned"
)

type WorkerConfig struct {
	Client    client.Client
	Config    *configs.GlobalConfig
	Logger    *zap.SugaredLogger
	K8sClient *kubernetes.Clientset
	K8sConfig *rest.Config
	DB        *gorm.DB
	RBACSvc   *authSvc.RBACService
}

type WorkerManager struct {
	wrk WorkerConfig
}

func NewWorkerManager(cfg WorkerConfig) *WorkerManager {
	return &WorkerManager{wrk: cfg}
}

func (m *WorkerManager) Start() worker.Worker {
	w := worker.New(m.wrk.Client, m.wrk.Config.Temporal.TaskQueue, worker.Options{})

	metricsClient, err := metricsv1.NewForConfig(m.wrk.K8sConfig)
	if err != nil {
		m.wrk.Logger.Fatalf("Impossible de créer le client metrics: %v", err)
	}
	projDBActs := &projectActivities.ProjectDBActivities{
		Repo:   projectRepo.NewProjectRepository(m.wrk.DB),
		Logger: m.wrk.Logger,
	}

	projK8sActs := &projectActivities.ProjectK8sActivities{
		K8sClient:     m.wrk.K8sClient,
		MetricsClient: metricsClient,
		Logger:        m.wrk.Logger,
		Rbac:          *m.wrk.RBACSvc,
	}

	workloadDBActs := &workloadActivities.WorkloadDBActivities{
		DB:     m.wrk.DB,
		Logger: m.wrk.Logger,
	}

	helmActs := &workloadActivities.WorkloadActivities{
		K8sConfig: m.wrk.K8sConfig,
	}

	// register
	m.registerWorkflows(w)
	m.registerActivities(w, projDBActs, projK8sActs, workloadDBActs, helmActs)

	go m.run(w)

	return w
}

func (m *WorkerManager) registerWorkflows(w worker.Worker) {
	w.RegisterWorkflow(projectWorkflows.CreateProjectWorkflow)
	w.RegisterWorkflow(workloadWorkflows.DeployWorkloadWorkflow)
}

func (m *WorkerManager) registerActivities(w worker.Worker, acts ...interface{}) {
	for _, a := range acts {
		w.RegisterActivity(a)
	}
}

func (m *WorkerManager) run(w worker.Worker) {
	m.wrk.Logger.Info("temp Worker started: Modules [Projects, Workloads, RBAC] Active")
	if err := w.Run(worker.InterruptCh()); err != nil {
		m.wrk.Logger.Fatalw("Unable to start worker", "error", err)
	}
}
