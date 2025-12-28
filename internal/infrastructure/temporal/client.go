package temporal

import (
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/workflows/ping"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
)

func StartWorker(c client.Client, cfg *configs.GlobalConfig, logger *zap.SugaredLogger) {

	w := worker.New(c, cfg.Temporal.TaskQueue, worker.Options{})

	w.RegisterWorkflow(ping.PingWorkflow)
	w.RegisterActivity(ping.PingActivity)

	go func() {
		logger.Info("Starting Temporal Worker...")
		if err := w.Run(worker.InterruptCh()); err != nil {
			logger.Fatalw("Unable to start worker", "error", err)
		}
	}()
}
