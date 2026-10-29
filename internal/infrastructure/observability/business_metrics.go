// Package observability exposes KubeManager business-level Prometheus metrics.
//
// These metrics sit alongside gin_*, temporal_*, go_* (process) and are the
// signals our alerts in PrometheusRule match on (see chart/templates/prometheusrule.yaml).
//
// Cardinality contract — no unbounded label. Allowed labels:
//   - `status`  : finite enum (STARTING / RUNNING / FAILED / DELETE_FAILED …).
//   - `cluster_id` : bounded (one per enrolled cluster, < 100 expected).
//   - `phase`   : finite enum from the workload lifecycle.
//
// Forbidden labels (will blow up Prometheus): user_id, workload_id, request_id,
// project_id. Those belong in OTEL traces, not metrics.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// WorkloadStatusTransitions counts every status/phase change written by
// `workloads/activities/db.go:UpdateWorkloadStatus`. Used by the
// `WorkloadSagaDeleteFailures` alert.
//
// Example series:
//
//	kubemanager_workload_status_transitions_total{status="RUNNING",phase="DEPLOYED"} 42
//	kubemanager_workload_status_transitions_total{status="DELETE_FAILED",phase="HELM_UNINSTALL_ERROR"} 3
var WorkloadStatusTransitions = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "kubemanager",
		Subsystem: "workload",
		Name:      "status_transitions_total",
		Help:      "Total workload status transitions, partitioned by status and phase.",
	},
	[]string{"status", "phase"},
)

// WorkloadDeploymentsTotal tracks successful deploy submissions.
// Incremented by workloads.service.Deploy when the DB row is created + the
// Temporal workflow is accepted.
var WorkloadDeploymentsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "kubemanager",
		Subsystem: "workload",
		Name:      "deployments_total",
		Help:      "Total workloads successfully submitted to the deploy workflow.",
	},
	[]string{"outcome"}, // "accepted" | "quota_exceeded" | "rejected"
)

// ClusterLastSeenTimestamp exposes the unix timestamp of the last onPodEvent
// received from each enrolled cluster. The `time() - X > 300` alert in
// PrometheusRule uses this gauge for staleness detection.
var ClusterLastSeenTimestamp = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "kubemanager",
		Subsystem: "cluster",
		Name:      "last_seen_timestamp_seconds",
		Help:      "Unix timestamp of the last pod event received from a cluster watcher.",
	},
	[]string{"cluster_id"},
)

// ClusterEnrolled is a small gauge = 1 per enrolled cluster. Set on Enroll,
// cleared on cluster removal. Useful for dashboards "fleet size over time".
var ClusterEnrolled = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "kubemanager",
		Subsystem: "cluster",
		Name:      "enrolled",
		Help:      "1 per cluster currently enrolled, cleared on delete.",
	},
	[]string{"cluster_id"},
)

// PodEventsIngested counts every pod event processed by OnPodEvent. Partitioned
// by status (mapped / accepted / ignored) + the K8s reason label.
var PodEventsIngested = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "kubemanager",
		Subsystem: "cluster",
		Name:      "pod_events_ingested_total",
		Help:      "Total pod events ingested from cluster watchers.",
	},
	[]string{"cluster_id", "status"},
)
