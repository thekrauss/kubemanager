package temporal

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/workflows/ping"
	"github.com/thekrauss/kubemanager/internal/modules/projects/activities"
	"github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"github.com/thekrauss/kubemanager/internal/modules/projects/workflows"
)

func StartWorker(c client.Client, cfg *configs.GlobalConfig, logger *zap.SugaredLogger, k8s *kubernetes.Clientset, db *gorm.DB) worker.Worker {

	w := worker.New(c, cfg.Temporal.TaskQueue, worker.Options{})

	projDBActs := &activities.ProjectDBActivities{
		Repo: repository.NewProjectRepository(db),
	}
	projK8sActs := &activities.ProjectK8sActivities{
		K8sClient: k8s,
	}

	w.RegisterWorkflow(ping.PingWorkflow)
	w.RegisterWorkflow(workflows.CreateProjectWorkflow)

	w.RegisterActivity(ping.PingActivity)
	w.RegisterActivity(projDBActs.CreateProjectInDB)
	w.RegisterActivity(projK8sActs.CreateNamespace)
	w.RegisterActivity(projDBActs.DeleteProjectDBActivity)

	go func() {
		logger.Info("Starting Temporal Worker with Project & K8s activities...")
		if err := w.Run(worker.InterruptCh()); err != nil {
			logger.Fatalw("Unable to start worker", "error", err)
		}
	}()

	return w
}
