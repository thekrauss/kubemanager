// Package domain : connexion GitHub des conteneurs (installations de
// l'application par projet, liens dépôt ↔ conteneur, webhooks).
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"

	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
)

// Pourquoi un lien ne fonctionne plus : codes stables, traduits par les clients.
const (
	DisabledInstallationDeleted   = "INSTALLATION_DELETED"
	DisabledInstallationSuspended = "INSTALLATION_SUSPENDED"
	DisabledRepositoryRemoved     = "REPOSITORY_REMOVED"
)

// Résultat du retour de GitHub.
const (
	CallbackLinked    = "linked"
	CallbackRequested = "requested" // demande envoyée au propriétaire de l'organisation
)

var (
	ErrDisabled           = errors.New("the GitHub connection is not enabled on this platform")
	ErrStateInvalid       = errors.New("this GitHub connection link is invalid or has expired: start again from the console")
	ErrCodeRequired       = errors.New("GitHub did not confirm your identity: start again and accept the authorization")
	ErrInstallationDenied = errors.New("your GitHub account does not have access to this installation")
	// ErrInstallationPartial : membre qui ne voit qu'une partie des dépôts.
	ErrInstallationPartial    = errors.New("you do not have access to every repository of this GitHub installation: ask an owner of the account to connect betorun")
	ErrInstallationNotLinked  = errors.New("this GitHub account is not connected to this project")
	ErrRepositoryInaccessible = errors.New("this repository is not accessible to the GitHub installation")
	ErrBranchNotFound         = errors.New("this branch does not exist in the repository")
	ErrLinkNotFound           = errors.New("this container is not linked to a repository")
	ErrLinkDisabled           = errors.New("access to this repository was removed: reconnect it from the console")
	ErrUnavailable            = errors.New("GitHub is unavailable right now: try again in a moment")
)

// Installation : installation de l'application, liée à un projet. Le jeton
// utilisateur qui a prouvé l'accès n'est pas conservé.
type Installation struct {
	ProjectID      uuid.UUID  `gorm:"type:uuid;primaryKey" json:"project_id"`
	InstallationID int64      `gorm:"primaryKey;autoIncrement:false" json:"installation_id"`
	AccountLogin   string     `gorm:"not null" json:"account_login"`
	AccountType    string     `gorm:"not null" json:"account_type"`
	Suspended      bool       `gorm:"not null" json:"suspended"`
	CreatedBy      *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (Installation) TableName() string { return "github_installations" }

// Link : lien dépôt ↔ conteneur. Le dépôt est identifié par RepoID ; un nom
// change et peut être repris par quelqu'un d'autre.
type Link struct {
	WorkloadID     uuid.UUID  `gorm:"type:uuid;primaryKey" json:"workload_id"`
	ProjectID      uuid.UUID  `gorm:"type:uuid;not null" json:"project_id"`
	InstallationID int64      `gorm:"not null" json:"installation_id"`
	RepoID         int64      `gorm:"not null" json:"repo_id"`
	RepoFullName   string     `gorm:"not null" json:"repo_full_name"`
	RepoPrivate    bool       `gorm:"not null" json:"repo_private"`
	Branch         string     `gorm:"not null" json:"branch"`
	ContextDir     string     `gorm:"not null" json:"context_dir,omitempty"`
	Dockerfile     string     `gorm:"not null" json:"dockerfile,omitempty"`
	AutoDeploy     bool       `gorm:"not null" json:"auto_deploy"`
	DisabledReason string     `gorm:"not null" json:"disabled_reason,omitempty"`
	CreatedBy      *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (Link) TableName() string { return "workload_git_links" }

// Delivery : livraison de webhook déjà reçue.
type Delivery struct {
	DeliveryID string `gorm:"primaryKey"`
	Event      string `gorm:"not null"`
	ReceivedAt time.Time
}

func (Delivery) TableName() string { return "github_webhook_deliveries" }

// ── Requêtes et réponses ────────────────────────────────────────────────────

// StatusResponse : la fonction est-elle disponible ?
type StatusResponse struct {
	Enabled bool `json:"enabled" description:"Connexion GitHub disponible sur cette plateforme"`
}

// ProjectPath : projet.
type ProjectPath struct {
	ProjectID string `path:"id" binding:"required"`
}

// URLResponse : page de GitHub.
type URLResponse struct {
	URL string `json:"url"`
}

// CallbackRequest : paramètres du retour de GitHub. installation_id vient du
// navigateur : ce n'est pas une preuve, voir service.Callback.
type CallbackRequest struct {
	State          string `json:"state" binding:"required" description:"Valeur remise par la page d'installation"`
	InstallationID int64  `json:"installation_id" description:"Installation désignée par GitHub"`
	Code           string `json:"code" description:"Code d'autorisation de GitHub"`
	SetupAction    string `json:"setup_action" description:"install, update ou request"`
}

// CallbackResponse : résultat du retour de GitHub.
type CallbackResponse struct {
	Status       string        `json:"status"`
	ProjectID    string        `json:"project_id"`
	Installation *Installation `json:"installation,omitempty"`
}

// InstallationPath : installation d'un projet.
type InstallationPath struct {
	ProjectID      string `path:"id" binding:"required"`
	InstallationID int64  `path:"installationID" binding:"required"`
}

// ListReposRequest : dépôts d'une installation.
type ListReposRequest struct {
	ProjectID      string `path:"id" binding:"required"`
	InstallationID int64  `path:"installationID" binding:"required"`
	Query          string `query:"q" description:"Recherche dans le nom du dépôt"`
	Page           int    `query:"page" description:"Page (à partir de 1)"`
	PerPage        int    `query:"per_page" description:"Dépôts par page (30 par défaut, 100 au plus)"`
}

// Repository : dépôt proposé à la sélection.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// RepositoriesResponse : une page de dépôts.
type RepositoriesResponse struct {
	Repositories []Repository `json:"repositories"`
	HasMore      bool         `json:"has_more"`
}

// ListBranchesRequest : branches d'un dépôt.
type ListBranchesRequest struct {
	ProjectID      string `path:"id" binding:"required"`
	InstallationID int64  `path:"installationID" binding:"required"`
	RepoID         int64  `path:"repoID" binding:"required"`
}

// BranchesResponse : branches d'un dépôt.
type BranchesResponse struct {
	Branches []string `json:"branches"`
	Default  string   `json:"default"`
}

// LinkSource : dépôt à suivre.
type LinkSource struct {
	InstallationID int64  `json:"installation_id" binding:"required" description:"Installation liée au projet"`
	RepoID         int64  `json:"repo_id" binding:"required" description:"Identifiant du dépôt"`
	Branch         string `json:"branch" binding:"required" description:"Branche suivie"`
	ContextDir     string `json:"context_dir" description:"Sous-dossier à construire"`
	Dockerfile     string `json:"dockerfile" description:"Chemin du Dockerfile (défaut : Dockerfile, ou détection automatique)"`
	AutoDeploy     *bool  `json:"auto_deploy" description:"Déployer à chaque push (oui par défaut)"`
}

// CreateWorkloadRequest : conteneur construit depuis un dépôt. Mêmes réglages
// que POST /workloads, sans image : c'est le build qui la produit.
type CreateWorkloadRequest struct {
	ProjectID string `path:"id" binding:"required" json:"-"`
	LinkSource
	Name               string            `json:"name" binding:"required,min=3,max=30" description:"Nom du conteneur"`
	Replicas           int               `json:"replicas"`
	CPURequest         string            `json:"cpu_request"`
	MemoryRequest      string            `json:"memory_request"`
	CPULimit           string            `json:"cpu_limit"`
	MemoryLimit        string            `json:"memory_limit"`
	TargetPort         int               `json:"target_port"`
	ServiceType        string            `json:"service_type"`
	MountPath          string            `json:"mount_path"`
	PersistenceEnabled bool              `json:"persistence_enabled"`
	StorageSize        string            `json:"storage_size"`
	StorageClass       string            `json:"storage_class"`
	EnvVars            map[string]string `json:"env_vars"`
	SecretData         map[string]string `json:"secret_data"`
	PDBEnabled         bool              `json:"pdb_enabled"`
	TLSEnabled         bool              `json:"tls_enabled"`
	HealthPath         string            `json:"health_path"`
	ReadinessPath      string            `json:"readiness_path"`
	ServiceAccount     string            `json:"service_account,omitempty"`
}

// CreateWorkloadResponse : conteneur créé, première construction lancée.
type CreateWorkloadResponse struct {
	WorkloadID string              `json:"workload_id"`
	Status     string              `json:"status"`
	Build      *buildsDomain.Build `json:"build"`
}

// WorkloadPath : conteneur.
type WorkloadPath struct {
	ID string `path:"id" binding:"required"`
}

// PutLinkRequest : créer ou remplacer le lien d'un conteneur.
type PutLinkRequest struct {
	ID string `path:"id" binding:"required" json:"-"`
	LinkSource
}

// PatchLinkRequest : modifier le lien d'un conteneur.
type PatchLinkRequest struct {
	ID         string  `path:"id" binding:"required" json:"-"`
	Branch     *string `json:"branch" description:"Branche suivie"`
	ContextDir *string `json:"context_dir" description:"Sous-dossier à construire"`
	Dockerfile *string `json:"dockerfile" description:"Chemin du Dockerfile"`
	AutoDeploy *bool   `json:"auto_deploy" description:"Déployer à chaque push"`
}

// LinkView : lien d'un conteneur et dernières opérations.
type LinkView struct {
	Linked         bool                `json:"linked"`
	Enabled        bool                `json:"enabled" description:"Connexion GitHub disponible sur cette plateforme"`
	Link           *Link               `json:"link,omitempty"`
	RepositoryURL  string              `json:"repository_url,omitempty"`
	DeployedCommit string              `json:"deployed_commit,omitempty"`
	DeployedAt     *time.Time          `json:"deployed_at,omitempty"`
	LastBuild      *buildsDomain.Build `json:"last_build,omitempty"`
}

// ── Charges utiles des webhooks (champs utilisés uniquement) ────────────────

// PushEvent : événement push.
type PushEvent struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// InstallationEvent : installation supprimée, suspendue, réactivée.
type InstallationEvent struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// InstallationReposEvent : dépôts retirés ou ajoutés à une installation.
type InstallationReposEvent struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	RepositoriesAdded []struct {
		ID int64 `json:"id"`
	} `json:"repositories_added"`
	RepositoriesRemoved []struct {
		ID int64 `json:"id"`
	} `json:"repositories_removed"`
}
