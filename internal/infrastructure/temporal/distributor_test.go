package temporal

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

// Une exécution déjà en cours fait échouer le démarrage : sinon la demande s'y
// rattache en silence et son travail n'a jamais lieu.
func TestExecuteWorkflow_ErrorWhenAlreadyStarted(t *testing.T) {
	c := mocks.NewClient(t)
	run := mocks.NewWorkflowRun(t)
	run.On("GetID").Return("wf")
	run.On("GetRunID").Return("run")
	c.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(o client.StartWorkflowOptions) bool {
		return o.WorkflowExecutionErrorWhenAlreadyStarted && o.ID == "wf" && o.TaskQueue == "q"
	}), "Workflow").Return(run, nil)

	h, err := NewDistributor(c, "q").ExecuteWorkflow(context.Background(), ExecuteOptions{WorkflowID: "wf"}, "Workflow")
	require.NoError(t, err)
	assert.Equal(t, "run", h.RunID)
}

func TestIsAlreadyStarted(t *testing.T) {
	err := fmt.Errorf("execute workflow: %w", serviceerror.NewWorkflowExecutionAlreadyStarted("started", "", "run"))
	assert.True(t, IsAlreadyStarted(err))
	assert.False(t, IsAlreadyStarted(fmt.Errorf("other")))
	assert.False(t, IsAlreadyStarted(nil))
}
