package idempotency

import (
	"context"
	"time"
)

type WorkerExecutionStatus string

const (
	WorkerExecuted   WorkerExecutionStatus = "executed"
	WorkerCached     WorkerExecutionStatus = "cached"
	WorkerInProgress WorkerExecutionStatus = "in_progress"
)

type WorkerExecutionOptions struct {
	ProcessingTTL   time.Duration
	OnFinalizeError func(stage string, err error)
}

func ExecuteWorker[T any](ctx context.Context, mgr Manager, key string, payloadHash string, opts WorkerExecutionOptions, work func() (T, error)) (T, WorkerExecutionStatus, error) {
	var zero T
	if mgr == nil || key == "" {
		result, err := work()
		if err != nil {
			return zero, "", err
		}
		return result, WorkerExecuted, nil
	}

	if atomic, ok := mgr.(AtomicManager); ok {
		var cached T
		beginStatus, err := atomic.Begin(ctx, key, payloadHash, opts.ProcessingTTL, &cached)
		if err != nil {
			return zero, "", err
		}
		switch beginStatus {
		case BeginCompleted:
			return cached, WorkerCached, nil
		case BeginInProgress:
			return zero, WorkerInProgress, nil
		}

		result, err := work()
		if err != nil {
			if cleanupErr := atomic.Fail(ctx, key, payloadHash); cleanupErr != nil && opts.OnFinalizeError != nil {
				opts.OnFinalizeError("fail", cleanupErr)
			}
			return zero, "", err
		}
		if completeErr := atomic.Complete(ctx, key, payloadHash, result); completeErr != nil && opts.OnFinalizeError != nil {
			opts.OnFinalizeError("complete", completeErr)
		}
		return result, WorkerExecuted, nil
	}

	var cached T
	found, err := mgr.Lookup(ctx, key, payloadHash, &cached)
	if err != nil {
		if err == ErrInProgress {
			return zero, WorkerInProgress, nil
		}
		return zero, "", err
	}
	if found {
		return cached, WorkerCached, nil
	}

	result, err := work()
	if err != nil {
		return zero, "", err
	}
	if storeErr := mgr.Store(ctx, key, payloadHash, result); storeErr != nil && opts.OnFinalizeError != nil {
		opts.OnFinalizeError("store", storeErr)
	}
	return result, WorkerExecuted, nil
}
