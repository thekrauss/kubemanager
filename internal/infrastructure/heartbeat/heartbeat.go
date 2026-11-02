// Package heartbeat : signaler une activité Temporal vivante pendant une
// opération qui n'en émet pas elle-même.
package heartbeat

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
)

// While : heartbeat immédiat puis régulier pendant op. Temporal détecte ainsi un
// worker mort, et ne transmet une annulation qu'en réponse à un heartbeat.
func While(ctx context.Context, every time.Duration, op func() error) error {
	return beatWhile(ctx, every, func() { activity.RecordHeartbeat(ctx) }, op)
}

func beatWhile(ctx context.Context, every time.Duration, beat func(), op func() error) error {
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			beat()
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		close(stop)
		<-stopped
	}()
	return op()
}
