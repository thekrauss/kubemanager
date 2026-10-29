// Package clusters — API pour les watchers K8s déployés dans chaque cluster géré.
//
// Protocole Restate-compatible (watcher existant pousse sur ces endpoints) :
//
//   POST /clusters/ClusterService/Enroll
//     → Enregistre le cluster auprès de kubemanager au démarrage du watcher.
//
//   GET  /clusters/ConfigService/{clusterID}/Get
//     → Retourne la config dynamique (dedup, batch, enabled_reasons) que
//       le watcher applique à chaud pour ajuster son comportement.
//
//   POST /clusters/ProjectService/{clusterID}/onPodEvent
//     → Reçoit un event pod (CrashLoopBackOff, OOMKilled, etc.), extrait
//       projectID/workloadID des labels K8s, met à jour la DB workload,
//       publie sur Centrifugo pour que le frontend reçoive en live.
//
// Authentification : API key de kind "cluster" (créée une fois par admin,
// injectée dans la config du watcher au deploy).
package clusters

import "time"

// ── Enroll ────────────────────────────────────────────────────────────────

type EnrollRequest struct {
	ClusterID string `json:"cluster_id" binding:"required" desc:"Identifiant unique du cluster (ex: prod-fr-north-1)"`
	Version   string `json:"version"    binding:"required" desc:"Version du binaire watcher (ex: v1.0.0)"`
}

type EnrollResponse struct {
	Status    string    `json:"status"` // "enrolled" | "updated"
	ClusterID string    `json:"cluster_id"`
	At        time.Time `json:"at"`
}

// ── RemoteConfig ──────────────────────────────────────────────────────────

// RemoteConfigResponse : shape EXACT attendu par watcher (cf restate/client.go:58).
// Les noms snake_case + unités (seconds/ms) sont contractuels — NE PAS modifier
// sans bump du protocole côté watcher.
type RemoteConfigResponse struct {
	// Fenêtre de dédup des events identiques (pod+container+reason).
	DedupCooldownSeconds int `json:"dedup_cooldown_seconds"`
	// Taille max d'un batch avant flush forcé.
	BatchSize int `json:"batch_size"`
	// Flush périodique du batch même si pas rempli.
	BatchFlushMs int `json:"batch_flush_ms"`
	// Reasons K8s qui déclenchent un notify (CrashLoopBackOff, OOMKilled, ...).
	EnabledReasons []string `json:"enabled_reasons"`
}

// ── PodEvent ──────────────────────────────────────────────────────────────

// PodEvent : shape EXACT envoyé par watcher (cf restate/client.go:34).
// Les champs `json:` doivent MATCHER EXACTEMENT ceux du watcher.
type PodEvent struct {
	EventID       string            `json:"event_id"`
	Type          string            `json:"type"`
	Namespace     string            `json:"namespace"`
	Name          string            `json:"name"`
	Phase         string            `json:"phase"`
	Reason        string            `json:"reason,omitempty"`
	Message       string            `json:"message,omitempty"`
	RestartCount  int               `json:"restart_count,omitempty"`
	ContainerName string            `json:"container_name,omitempty"`
	ProjectID     string            `json:"project_id,omitempty"`
	OwnerID       string            `json:"owner_id,omitempty"`
	WorkloadKind  string            `json:"workload_kind,omitempty"`
	WorkloadName  string            `json:"workload_name,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// PodEventAck : ACK envoyé au watcher. Intentionnellement minimal.
type PodEventAck struct {
	EventID  string `json:"event_id,omitempty"`
	Status   string `json:"status"` // "accepted" | "ignored" | "mapped"
	WorkloadID string `json:"workload_id,omitempty"`
}
