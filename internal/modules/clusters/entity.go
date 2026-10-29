package clusters

import (
	"time"

	"github.com/google/uuid"
)

// Cluster : registre des clusters K3s gérés par kubemanager.
//
// Alimenté par `Enroll` (au démarrage du watcher) et touché à chaque
// `onPodEvent` via `LastSeenAt`. L'absence de `LastSeenAt` récent peut
// indiquer un watcher en panne → base pour des alertes futures.
type Cluster struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// ClusterID : identifiant logique choisi par l'admin (ex: prod-fr-north-1).
	// Unique — un seul watcher par cluster logique. Utilisé dans les URLs
	// /ConfigService/:clusterID/Get et /ProjectService/:clusterID/onPodEvent.
	ClusterID string `gorm:"type:varchar(100);uniqueIndex;not null"`

	// Version : version du binaire watcher (issue de son Enroll).
	Version string `gorm:"type:varchar(50)"`

	// LastSeenAt : heure du dernier échange réussi avec ce cluster.
	// Base pour les alertes staleness (watcher down > 5min → warning).
	LastSeenAt *time.Time

	// EnrolledAt : premier Enroll réussi. Immutable (set au INSERT initial).
	EnrolledAt time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}
