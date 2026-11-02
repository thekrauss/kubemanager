// Package domain : builds d'images depuis le code.
package domain

import (
	"errors"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// États d'un build.
const (
	StatusQueued    = "QUEUED"
	StatusBuilding  = "BUILDING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
	StatusCancelled = "CANCELLED"
)

// Causes d'échec : codes stables, traduits par les clients.
const (
	FailureBuild    = "BUILD_FAILED" // erreur du Dockerfile ou de la source : voir les journaux
	FailureTimeout  = "TIMEOUT"
	FailureDisk     = "DISK_LIMIT"
	FailureInternal = "INTERNAL"
	FailureNoPlan   = "NO_BUILD_PLAN" // pas de Dockerfile, type de projet non reconnu
	// FailureGitAccess : l'accès au dépôt a été retiré entre le push et le clonage.
	FailureGitAccess = "GIT_ACCESS_LOST"
	// FailureTooMany : trop de builds en cours quand le push est arrivé.
	FailureTooMany = "TOO_MANY_BUILDS"
	// FailureSuperseded : annulé au profit d'un commit plus récent de la branche.
	FailureSuperseded = "SUPERSEDED"
)

// Origines du code.
const (
	SourceUpload = "upload"
	SourceGit    = "git"
	// SourceGitHub : dépôt d'une installation GitHub (privé possible).
	SourceGitHub = "github"
)

// Déclencheurs d'un build.
const (
	TriggerManual = "manual"
	TriggerPush   = "push"
)

// États du déploiement enchaîné par le serveur à la fin d'un build. Le
// déploiement lui-même se suit ensuite sur le conteneur.
const (
	DeployPending    = "PENDING"
	DeployStarted    = "DEPLOYED"
	DeployBusy       = "BUSY"       // un déploiement occupait encore le conteneur, après plusieurs essais
	DeployFailed     = "FAILED"     // refusé (quota, image…)
	DeployStopped    = "STOPPED"    // conteneur arrêté : image prête, non déployée
	DeployGone       = "GONE"       // conteneur supprimé entre-temps
	DeploySuperseded = "SUPERSEDED" // un build plus récent prend le relais
	DeployNotBuilt   = "NOT_BUILT"  // build en échec ou annulé : rien n'a changé
)

// DefaultDockerfile : Dockerfile à la racine du code.
const DefaultDockerfile = "Dockerfile"

// ArchiveFormField : champ multipart de l'archive (.tar.gz).
const ArchiveFormField = "archive"

// Build : construction d'une image.
type Build struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	ProjectID  uuid.UUID `gorm:"type:uuid;not null" json:"project_id"`
	Name       string    `gorm:"not null" json:"name"`
	SourceType string    `gorm:"not null" json:"source_type"`
	GitURL     string    `gorm:"not null" json:"git_url,omitempty"`
	GitRef     string    `gorm:"not null" json:"git_ref,omitempty"`
	ContextDir string    `gorm:"not null" json:"context_dir,omitempty"`
	Dockerfile string    `gorm:"not null" json:"dockerfile"`
	Status     string    `gorm:"not null" json:"status"`
	Failure    string    `gorm:"not null" json:"failure,omitempty"`
	Image      string    `gorm:"not null" json:"image"`
	Digest     string    `gorm:"not null" json:"digest,omitempty"`
	// Detected : type de projet détecté sans Dockerfile (next, node, fastapi…).
	Detected string `gorm:"not null" json:"detected,omitempty"`
	// GitCommit : commit exact construit (voie GitHub).
	GitCommit string `gorm:"not null" json:"git_commit,omitempty"`
	// GitRepoID, GitInstallationID : dépôt GitHub, désigné par son identifiant
	// (un nom change et peut être repris). Le jeton de clonage en dépend.
	GitRepoID         *int64 `json:"git_repo_id,omitempty"`
	GitInstallationID *int64 `json:"git_installation_id,omitempty"`
	// DeployWorkloadID : le serveur met ce conteneur à jour si le build réussit.
	DeployWorkloadID *uuid.UUID `gorm:"type:uuid" json:"deploy_workload_id,omitempty"`
	Trigger          string     `gorm:"column:trigger;not null" json:"trigger"`
	DeployState      string     `gorm:"not null" json:"deploy_state,omitempty"`

	CreatedBy  *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (Build) TableName() string { return "builds" }

// Finished : le build n'évoluera plus.
func (b *Build) Finished() bool {
	return b.Status == StatusSucceeded || b.Status == StatusFailed || b.Status == StatusCancelled
}

// CreateGitBuildRequest : build d'un dépôt Git public.
type CreateGitBuildRequest struct {
	ProjectID  string `path:"id" binding:"required" json:"-"`
	Name       string `json:"name" binding:"required" description:"Nom de l'image (souvent celui du conteneur)"`
	GitURL     string `json:"git_url" binding:"required" description:"Dépôt public en https"`
	GitRef     string `json:"git_ref" description:"Branche ou tag (défaut : branche par défaut du dépôt)"`
	ContextDir string `json:"context_dir" description:"Sous-dossier à construire"`
	Dockerfile string `json:"dockerfile" description:"Chemin du Dockerfile (défaut : Dockerfile)"`
}

// UploadPath : paramètres de l'envoi d'une archive.
type UploadPath struct {
	ProjectID string `path:"id" binding:"required"`
}

// BuildPath : un build du projet.
type BuildPath struct {
	ProjectID string `path:"id" binding:"required"`
	BuildID   string `path:"buildID" binding:"required"`
}

// ListBuildsRequest : builds récents.
type ListBuildsRequest struct {
	ProjectID string `path:"id" binding:"required"`
	Name      string `query:"name" description:"Uniquement les builds de cette image"`
}

// LogsRequest : journal d'un build.
type LogsRequest struct {
	ProjectID string `path:"id" binding:"required"`
	BuildID   string `path:"buildID" binding:"required"`
	Follow    bool   `query:"follow" description:"Suivre jusqu'à la fin du build"`
}

// SourcePath : téléchargement de l'archive par le Job de build (URL signée).
type SourcePath struct {
	BuildID string `path:"buildID" binding:"required"`
	Expires int64  `query:"expires" binding:"required"`
	Sig     string `query:"sig" binding:"required"`
}

// TokenRequest : protocole d'authentification du registre (Docker token auth).
type TokenRequest struct {
	Service string `query:"service"`
	Scope   string `query:"scope"`
}

// TokenResponse : jeton du registre.
type TokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

var (
	ErrInvalidName       = errors.New("invalid image name: 2-63 lowercase letters, digits and hyphens")
	ErrInvalidGitURL     = errors.New("invalid Git URL: use a public https repository, without credentials")
	ErrInvalidGitRef     = errors.New("invalid Git branch or tag")
	ErrInvalidPath       = errors.New("invalid path: it must stay inside the source")
	ErrInvalidArchive    = errors.New("the archive must be a .tar.gz file")
	ErrTooManyBuilds     = errors.New("too many builds in progress for this project: wait for one to finish")
	ErrBuildNotFound     = errors.New("build not found")
	ErrBuildFinished     = errors.New("this build is already finished")
	ErrBuildsDisabled    = errors.New("builds are not enabled on this platform")
	ErrForeignImage      = errors.New("this image belongs to another project")
	ErrRegistryForbidden = errors.New("invalid registry credentials")

	// Déploiement enchaîné par le serveur : réponses du service des conteneurs.
	ErrWorkloadBusy    = errors.New("a deployment is already in progress on this container")
	ErrWorkloadStopped = errors.New("this container is stopped")
	ErrWorkloadGone    = errors.New("this container no longer exists")
	// ErrGitAccessLost : jeton de clonage refusé par GitHub ou installation déliée.
	ErrGitAccessLost = errors.New("betorun no longer has access to this repository")
)

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	gitRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	pathRe   = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]{0,199}$`)
	shaRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ValidCommit : empreinte SHA-1 complète d'un commit Git.
func ValidCommit(sha string) bool { return shaRe.MatchString(sha) }

// ValidateName : devient un segment du nom de l'image.
func ValidateName(name string) error {
	if len(name) < 2 || !nameRe.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// ValidatePath : chemin relatif à la source (Dockerfile, sous-dossier).
func ValidatePath(p string) error {
	if p == "" {
		return nil
	}
	if !pathRe.MatchString(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return ErrInvalidPath
	}
	return nil
}

// ValidateGitRef : branche ou tag ; vide = branche par défaut.
func ValidateGitRef(ref string) error {
	if ref != "" && (!gitRefRe.MatchString(ref) || strings.Contains(ref, "..")) {
		return ErrInvalidGitRef
	}
	return nil
}

// NormalizeGitURL : https uniquement, sans identifiants ni adresse IP. Le
// réseau du Job de build refuse de toute façon les adresses internes ; ce
// contrôle donne une erreur claire plutôt qu'un échec de clone. Le suffixe
// .git est ce qui fait reconnaître un dépôt à BuildKit.
func NormalizeGitURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrInvalidGitURL
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", ErrInvalidGitURL
	}
	p := strings.TrimSuffix(strings.TrimRight(u.EscapedPath(), "/"), ".git")
	if strings.Count(p, "/") < 2 {
		return "", ErrInvalidGitURL
	}
	return "https://" + u.Host + p + ".git", nil
}
