// Package observability wires Temporal SDK metrics into the Prometheus
// default registry via a tally scope.
//
// Flow:
//
//	Temporal SDK (client + worker)
//	  → sdktally.NewMetricsHandler(scope)
//	    → tally v4 scope
//	      → tally/v4/prometheus reporter
//	        → prometheus.DefaultRegisterer (scraped by /metrics)
//
// The reporter deduplicates metric registrations, so the same registry can be
// used for gin-prometheus + GORM spans + Temporal + custom business metrics.
package observability

import (
	"io"
	"time"

	tally "github.com/uber-go/tally/v4"
	tallyprom "github.com/uber-go/tally/v4/prometheus"
	"go.temporal.io/sdk/client"
	sdktally "go.temporal.io/sdk/contrib/tally"
)

// NewTemporalMetricsHandler builds a Temporal MetricsHandler backed by a tally
// Prometheus reporter. The returned Closer MUST be called on shutdown to flush
// buffered metric samples.
//
// `prefix` is prepended to every emitted metric (default "temporal"). Useful
// to avoid collisions with other sources that use the same tally scope names
// (e.g., if another tally reporter is ever wired).
func NewTemporalMetricsHandler(prefix string) (client.MetricsHandler, io.Closer) {
	if prefix == "" {
		prefix = "temporal"
	}

	reporter := tallyprom.NewReporter(tallyprom.Options{
		OnRegisterError: func(err error) {
			// Duplicate metric registrations are expected across reloads;
			// don't fatal on them. Other errors surface via the SDK logger.
			_ = err
		},
	})

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter:  reporter,
		Separator:       tallyprom.DefaultSeparator,
		Prefix:          prefix,
		SanitizeOptions: &sdktally.PrometheusSanitizeOptions,
	}, time.Second)

	return sdktally.NewMetricsHandler(scope), closer
}
