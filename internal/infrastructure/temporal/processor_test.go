package temporal

import (
	"testing"
	"time"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

func TestWorkerOptions_Bounded(t *testing.T) {
	opts := workerOptions(configs.TemporalConfig{}.WithWorkerDefaults())
	if opts.MaxConcurrentActivityExecutionSize != configs.DefaultMaxConcurrentActivities ||
		opts.MaxConcurrentWorkflowTaskExecutionSize != configs.DefaultMaxConcurrentWorkflowTasks ||
		opts.WorkerStopTimeout != configs.DefaultWorkerStopTimeout {
		t.Fatalf("défauts : %+v", opts)
	}
	opts = workerOptions(configs.TemporalConfig{MaxConcurrentActivities: 2, WorkerStopTimeout: time.Minute}.WithWorkerDefaults())
	if opts.MaxConcurrentActivityExecutionSize != 2 || opts.WorkerStopTimeout != time.Minute {
		t.Fatalf("config ignorée : %+v", opts)
	}
}
