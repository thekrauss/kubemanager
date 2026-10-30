package idempotency

import (
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

func TestActivityKey_PerRun(t *testing.T) {
	info := func(runID string, attempt int32) activity.Info {
		return activity.Info{
			WorkflowExecution: workflow.Execution{ID: "workload-update-w1", RunID: runID},
			ActivityType:      activity.Type{Name: "InstallChart"},
			ActivityID:        "8",
			Attempt:           attempt,
		}
	}
	if activityKey(info("run-1", 1)) != activityKey(info("run-1", 3)) {
		t.Fatal("les tentatives d'une même exécution doivent partager la clé")
	}
	if activityKey(info("run-1", 1)) == activityKey(info("run-2", 1)) {
		t.Fatal("une nouvelle exécution du même workflow (nouvelle version) doit avoir sa propre clé")
	}
}
