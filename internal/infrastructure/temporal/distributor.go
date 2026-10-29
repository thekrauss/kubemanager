
package temporal

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// TaskDistributor : interface exposée aux services métier.
type TaskDistributor interface {
	// ExecuteWorkflow démarre un workflow et retourne le WorkflowID + RunID.
	ExecuteWorkflow(ctx context.Context, opts ExecuteOptions, workflow any, args ...any) (WorkflowHandle, error)

	// SignalWorkflow envoie un signal à un workflow en cours.
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, payload any) error

	// QueryWorkflow interroge l'état d'un workflow (synchrone).
	QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...any) (converter.EncodedValue, error)

	// CancelWorkflow annule un workflow en cours (graceful).
	CancelWorkflow(ctx context.Context, workflowID, runID string) error

	// TerminateWorkflow arrête immédiatement un workflow (hard kill).
	TerminateWorkflow(ctx context.Context, workflowID, runID, reason string) error
}

// ExecuteOptions : paramètres par invocation (override les defaults).
type ExecuteOptions struct {
	WorkflowID          string        // défaut = auto-généré si vide
	TaskQueue           string        // défaut = config.Temporal.TaskQueue
	WorkflowRunTimeout  time.Duration // défaut = 0 (pas de timeout)
	WorkflowTaskTimeout time.Duration // défaut = 0 (SDK default)
}

// WorkflowHandle : identifiants retournés après ExecuteWorkflow.
type WorkflowHandle struct {
	WorkflowID string
	RunID      string
}

// TemporalDistributor : implémentation concrète.
type TemporalDistributor struct {
	client           client.Client
	defaultTaskQueue string
}

// NewDistributor construit un distributor qui délègue au client Temporal.
func NewDistributor(c client.Client, defaultTaskQueue string) *TemporalDistributor {
	return &TemporalDistributor{
		client:           c,
		defaultTaskQueue: defaultTaskQueue,
	}
}

func (d *TemporalDistributor) ExecuteWorkflow(
	ctx context.Context,
	opts ExecuteOptions,
	workflow any,
	args ...any,
) (WorkflowHandle, error) {
	if d == nil || d.client == nil {
		return WorkflowHandle{}, fmt.Errorf("temporal distributor not initialized")
	}

	queue := opts.TaskQueue
	if queue == "" {
		queue = d.defaultTaskQueue
	}

	startOpts := client.StartWorkflowOptions{
		ID:                       opts.WorkflowID,
		TaskQueue:                queue,
		WorkflowExecutionTimeout: opts.WorkflowRunTimeout,
		WorkflowTaskTimeout:      opts.WorkflowTaskTimeout,
	}

	run, err := d.client.ExecuteWorkflow(ctx, startOpts, workflow, args...)
	if err != nil {
		return WorkflowHandle{}, fmt.Errorf("execute workflow: %w", err)
	}
	return WorkflowHandle{WorkflowID: run.GetID(), RunID: run.GetRunID()}, nil
}

func (d *TemporalDistributor) SignalWorkflow(
	ctx context.Context,
	workflowID, runID, signalName string,
	payload any,
) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("temporal distributor not initialized")
	}
	return d.client.SignalWorkflow(ctx, workflowID, runID, signalName, payload)
}

func (d *TemporalDistributor) QueryWorkflow(
	ctx context.Context,
	workflowID, runID, queryType string,
	args ...any,
) (converter.EncodedValue, error) {
	if d == nil || d.client == nil {
		return nil, fmt.Errorf("temporal distributor not initialized")
	}
	return d.client.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
}

func (d *TemporalDistributor) CancelWorkflow(ctx context.Context, workflowID, runID string) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("temporal distributor not initialized")
	}
	return d.client.CancelWorkflow(ctx, workflowID, runID)
}

func (d *TemporalDistributor) TerminateWorkflow(ctx context.Context, workflowID, runID, reason string) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("temporal distributor not initialized")
	}
	return d.client.TerminateWorkflow(ctx, workflowID, runID, reason)
}
