// Package service : connexion GitHub. Toute vérification de propriété passe
// par le projet : une installation n'est utilisable que depuis un projet où
// elle a été liée par quelqu'un qui a prouvé y avoir accès.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/iam"
	buildActs "github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	buildsSvc "github.com/thekrauss/kubemanager/internal/modules/builds/service"
	"github.com/thekrauss/kubemanager/internal/modules/github/client"
	"github.com/thekrauss/kubemanager/internal/modules/github/domain"
	"github.com/thekrauss/kubemanager/internal/modules/github/repository"
	workloadsDomain "github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

const (
	stateTTL       = 10 * time.Minute
	statePrefix    = "github:install:"
	stateBytes     = 32
	defaultPerPage = 30
	maxPerPage     = 100
	// searchPages : GitHub n'offre pas de recherche sur les dépôts d'une
	// installation ; elle se fait ici, sur 1 000 dépôts au plus.
	searchPages  = 10
	maxBranches  = 100
	refsHeads    = "refs/heads/"
	eventPush    = "push"
	eventInstall = "installation"
	eventRepos   = "installation_repositories"
)

// StateStore : état de l'installation (Redis : partagé entre les instances).
type StateStore interface {
	SetValue(ctx context.Context, key, value string, ttl time.Duration) error
	GetValue(ctx context.Context, key string) (string, error)
	TakeValue(ctx context.Context, key string) (string, error)
}

// Authorizer : décision IAM.
type Authorizer interface {
	Check(ctx context.Context, p iam.Principal, perm iam.Permission, r iam.ResourceName) (iam.Decision, error)
}

// Builds : démarrage des builds et lecture de leur historique.
type Builds interface {
	FromGitHub(ctx context.Context, req buildsSvc.GitHubBuild) (*buildsDomain.Build, error)
	LatestForWorkload(ctx context.Context, workload uuid.UUID) (*buildsDomain.Build, error)
	LatestDeployed(ctx context.Context, workload uuid.UUID) (*buildsDomain.Build, error)
}

// Workloads : conteneurs.
type Workloads interface {
	Get(ctx context.Context, id uuid.UUID) (*workloadsDomain.Workload, error)
	CreateAwaitingBuild(ctx context.Context, in *workloadsSvc.WorkloadServiceRequest) (*workloadsDomain.Workload, error)
	MarkAwaitingBuild(ctx context.Context, w *workloadsDomain.Workload) error
	MarkBuildFailed(ctx context.Context, project, id uuid.UUID) error
	Remove(ctx context.Context, id uuid.UUID) error
}

// Config : secret des webhooks.
type Config struct {
	WebhookSecret string
}

// Service : logique de la connexion GitHub. Client nil : fonction désactivée.
type Service struct {
	Client    *client.Client
	Repo      repository.Repository
	State     StateStore
	Authz     Authorizer
	Builds    Builds
	Workloads Workloads
	Cfg       Config
	Logger    *zap.SugaredLogger
}

// Enabled : application configurée.
func (s *Service) Enabled() bool { return s != nil && s.Client != nil }

func (s *Service) ready() error {
	if !s.Enabled() {
		return domain.ErrDisabled
	}
	return nil
}

// apiError : erreurs de GitHub traduites en erreurs de l'API.
func apiError(err error, notAccessible error) error {
	switch {
	case errors.Is(err, client.ErrNotAccessible):
		return notAccessible
	case errors.Is(err, client.ErrUnavailable):
		return domain.ErrUnavailable
	}
	return err
}

// ── Installation ────────────────────────────────────────────────────────────

type installState struct {
	User    uuid.UUID `json:"user"`
	Project uuid.UUID `json:"project"`
}

func stateKey(state string) string {
	sum := sha256.Sum256([]byte(state))
	return statePrefix + hex.EncodeToString(sum[:])
}

// InstallURL : page d'installation de GitHub. L'état, aléatoire et à durée
// courte, est gardé côté serveur et lié à cette personne et à ce projet.
func (s *Service) InstallURL(ctx context.Context, project, user uuid.UUID) (*domain.URLResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	raw := make([]byte, stateBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	value, err := json.Marshal(installState{User: user, Project: project})
	if err != nil {
		return nil, err
	}
	if err := s.State.SetValue(ctx, stateKey(state), string(value), stateTTL); err != nil {
		return nil, domain.ErrUnavailable
	}
	return &domain.URLResponse{URL: s.Client.InstallURL(state)}, nil
}

// Callback : retour de GitHub. installation_id vient du navigateur et ne prouve
// rien : sans la vérification qui suit, n'importe qui lierait l'installation
// d'un autre et lirait ses dépôts privés. Il faut un état à usage unique émis
// pour cette personne, puis son propre jeton GitHub (issu du code) doit lister
// cette installation. Le jeton n'est pas conservé.
func (s *Service) Callback(ctx context.Context, user uuid.UUID, caller iam.Principal, in domain.CallbackRequest) (*domain.CallbackResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	key := stateKey(in.State)
	raw, err := s.State.GetValue(ctx, key)
	if err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return nil, domain.ErrStateInvalid
		}
		return nil, domain.ErrUnavailable
	}
	var st installState
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.User != user {
		// L'état d'un autre n'est pas consommé : il reste valable pour lui.
		return nil, domain.ErrStateInvalid
	}
	if _, err := s.State.TakeValue(ctx, key); err != nil {
		return nil, domain.ErrStateInvalid // déjà utilisé entre-temps
	}
	// Droit perdu depuis la demande.
	decision, err := s.Authz.Check(ctx, caller, iam.WorkloadsGitHubManage, iam.ProjectName(st.Project))
	if err != nil {
		return nil, err
	}
	if !decision.Allowed {
		return nil, domain.ErrInstallationDenied
	}
	res := &domain.CallbackResponse{ProjectID: st.Project.String()}
	// Organisation : la personne a demandé l'installation à un propriétaire.
	if in.SetupAction == "request" && in.InstallationID == 0 {
		res.Status = domain.CallbackRequested
		return res, nil
	}
	if in.InstallationID <= 0 {
		return nil, domain.ErrInstallationDenied
	}
	if in.Code == "" {
		return nil, domain.ErrCodeRequired
	}
	userToken, err := s.Client.ExchangeCode(ctx, in.Code)
	if err != nil {
		if errors.Is(err, client.ErrUnavailable) {
			return nil, domain.ErrUnavailable
		}
		return nil, domain.ErrCodeRequired
	}
	found, err := s.Client.UserInstallation(ctx, userToken, in.InstallationID)
	if err != nil {
		return nil, apiError(err, domain.ErrInstallationDenied)
	}
	if found == nil {
		s.Logger.Warnw("github installation refused: not accessible to this account", "installation", in.InstallationID, "user", user)
		return nil, domain.ErrInstallationDenied
	}
	installation := &domain.Installation{ProjectID: st.Project, InstallationID: found.ID, AccountLogin: found.Account.Login,
		AccountType: found.Account.Type, CreatedBy: &user}
	if err := s.Repo.UpsertInstallation(ctx, installation); err != nil {
		return nil, err
	}
	res.Status, res.Installation = domain.CallbackLinked, installation
	return res, nil
}

// ListInstallations : installations liées au projet.
func (s *Service) ListInstallations(ctx context.Context, project uuid.UUID) ([]domain.Installation, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.Repo.ListInstallations(ctx, project)
}

// DeleteInstallation : délie l'installation. Les liens des conteneurs du projet
// disparaissent, les conteneurs continuent de tourner.
func (s *Service) DeleteInstallation(ctx context.Context, project uuid.UUID, installationID int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.Repo.DeleteInstallation(ctx, project, installationID)
}

// ListRepositories : dépôts d'une installation liée au projet.
func (s *Service) ListRepositories(ctx context.Context, project uuid.UUID, in domain.ListReposRequest) (*domain.RepositoriesResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.Repo.GetInstallation(ctx, project, in.InstallationID); err != nil {
		return nil, err
	}
	token, err := s.Client.InstallationToken(ctx, in.InstallationID, client.ListScope())
	if err != nil {
		return nil, apiError(err, domain.ErrInstallationNotLinked)
	}
	page, perPage := max(in.Page, 1), in.PerPage
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	perPage = min(perPage, maxPerPage)
	query := strings.ToLower(strings.TrimSpace(in.Query))
	out := &domain.RepositoriesResponse{Repositories: []domain.Repository{}}
	if query == "" {
		repos, total, err := s.Client.Repositories(ctx, token, page, perPage)
		if err != nil {
			return nil, apiError(err, domain.ErrInstallationNotLinked)
		}
		for i := range repos {
			out.Repositories = append(out.Repositories, viewRepo(&repos[i]))
		}
		out.HasMore = page*perPage < total
		return out, nil
	}
	skip := (page - 1) * perPage
	for p := 1; p <= searchPages; p++ {
		repos, _, err := s.Client.Repositories(ctx, token, p, maxPerPage)
		if err != nil {
			return nil, apiError(err, domain.ErrInstallationNotLinked)
		}
		for i := range repos {
			if !strings.Contains(strings.ToLower(repos[i].FullName), query) {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			if len(out.Repositories) == perPage {
				out.HasMore = true
				return out, nil
			}
			out.Repositories = append(out.Repositories, viewRepo(&repos[i]))
		}
		if len(repos) < maxPerPage {
			break
		}
	}
	return out, nil
}

func viewRepo(r *client.Repository) domain.Repository {
	return domain.Repository{ID: r.ID, FullName: r.FullName, Private: r.Private, DefaultBranch: r.DefaultBranch}
}

// repository : jeton restreint au dépôt et dépôt à jour. Le refus de GitHub de
// restreindre le jeton à ce dépôt prouve qu'il n'appartient pas à l'installation.
func (s *Service) repository(ctx context.Context, installationID, repoID int64) (string, *client.Repository, error) {
	token, err := s.Client.InstallationToken(ctx, installationID, client.CloneScope(repoID))
	if err != nil {
		return "", nil, apiError(err, domain.ErrRepositoryInaccessible)
	}
	repo, err := s.Client.RepositoryByID(ctx, token, repoID)
	if err != nil {
		return "", nil, apiError(err, domain.ErrRepositoryInaccessible)
	}
	return token, repo, nil
}

// ListBranches : branches d'un dépôt d'une installation du projet.
func (s *Service) ListBranches(ctx context.Context, project uuid.UUID, in domain.ListBranchesRequest) (*domain.BranchesResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.Repo.GetInstallation(ctx, project, in.InstallationID); err != nil {
		return nil, err
	}
	token, repo, err := s.repository(ctx, in.InstallationID, in.RepoID)
	if err != nil {
		return nil, err
	}
	branches, err := s.Client.Branches(ctx, token, repo.FullName, 1, maxBranches)
	if err != nil {
		return nil, apiError(err, domain.ErrRepositoryInaccessible)
	}
	out := &domain.BranchesResponse{Branches: make([]string, 0, len(branches)), Default: repo.DefaultBranch}
	for i := range branches {
		out.Branches = append(out.Branches, branches[i].Name)
	}
	return out, nil
}

// ── Liens ───────────────────────────────────────────────────────────────────

// resolveSource : l'installation est liée au projet, le dépôt lui appartient,
// la branche existe. Renvoie le lien prêt à enregistrer et le dernier commit.
func (s *Service) resolveSource(ctx context.Context, project uuid.UUID, src domain.LinkSource) (*domain.Link, string, error) {
	if err := buildsDomain.ValidateGitRef(src.Branch); err != nil {
		return nil, "", err
	}
	if err := buildsDomain.ValidatePath(src.ContextDir); err != nil {
		return nil, "", err
	}
	if err := buildsDomain.ValidatePath(src.Dockerfile); err != nil {
		return nil, "", err
	}
	installation, err := s.Repo.GetInstallation(ctx, project, src.InstallationID)
	if err != nil {
		return nil, "", err
	}
	if installation.Suspended {
		return nil, "", domain.ErrLinkDisabled
	}
	token, repo, err := s.repository(ctx, src.InstallationID, src.RepoID)
	if err != nil {
		return nil, "", err
	}
	head, err := s.Client.BranchHead(ctx, token, repo.FullName, src.Branch)
	if err != nil {
		return nil, "", apiError(err, domain.ErrBranchNotFound)
	}
	auto := true
	if src.AutoDeploy != nil {
		auto = *src.AutoDeploy
	}
	return &domain.Link{ProjectID: project, InstallationID: src.InstallationID, RepoID: repo.ID, RepoFullName: repo.FullName,
		RepoPrivate: repo.Private, Branch: src.Branch, ContextDir: src.ContextDir, Dockerfile: src.Dockerfile, AutoDeploy: auto}, head.Commit.SHA, nil
}

func (s *Service) startBuild(ctx context.Context, w *workloadsDomain.Workload, link *domain.Link, commit, trigger string, by *uuid.UUID, record bool) (*buildsDomain.Build, error) {
	return s.Builds.FromGitHub(ctx, buildsSvc.GitHubBuild{
		Project: link.ProjectID, Workload: w.ID, Name: w.Name, InstallationID: link.InstallationID, RepoID: link.RepoID,
		CloneURL: s.Client.CloneURL(link.RepoFullName), Branch: link.Branch, Commit: commit, ContextDir: link.ContextDir,
		Dockerfile: link.Dockerfile, Trigger: trigger, By: by, RecordRejection: record,
	})
}

// CreateWorkload : le conteneur, le lien et le premier build en une action. Le
// conteneur réserve son quota tout de suite et reste « en construction » : le
// build le déploie à sa réussite, même si la page est quittée entre-temps.
func (s *Service) CreateWorkload(ctx context.Context, caller iam.Principal, project uuid.UUID, by *uuid.UUID, in domain.CreateWorkloadRequest) (*domain.CreateWorkloadResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	decision, err := s.Authz.Check(ctx, caller, iam.WorkloadsCreate, iam.ProjectName(project))
	if err != nil {
		return nil, err
	}
	if !decision.Allowed {
		return nil, iam.ErrEscalation
	}
	if err := buildsDomain.ValidateName(in.Name); err != nil {
		return nil, err
	}
	link, commit, err := s.resolveSource(ctx, project, in.LinkSource)
	if err != nil {
		return nil, err
	}
	w, err := s.Workloads.CreateAwaitingBuild(ctx, &workloadsSvc.WorkloadServiceRequest{Caller: caller, CreateWorkloadRequest: workloadsDomain.CreateWorkloadRequest{
		ProjectID: project.String(), Name: in.Name, Replicas: in.Replicas, CPURequest: in.CPURequest, MemoryRequest: in.MemoryRequest,
		CPULimit: in.CPULimit, MemoryLimit: in.MemoryLimit, TargetPort: in.TargetPort, ServiceType: in.ServiceType, MountPath: in.MountPath,
		PersistenceEnabled: in.PersistenceEnabled, StorageSize: in.StorageSize, StorageClass: in.StorageClass, EnvVars: in.EnvVars,
		SecretData: in.SecretData, PDBEnabled: in.PDBEnabled, TLSEnabled: in.TLSEnabled, HealthPath: in.HealthPath,
		ReadinessPath: in.ReadinessPath, ServiceAccount: in.ServiceAccount,
	}})
	if err != nil {
		return nil, err
	}
	link.WorkloadID, link.CreatedBy = w.ID, by
	if err := s.Repo.SaveLink(ctx, link); err != nil {
		_ = s.Workloads.Remove(ctx, w.ID)
		return nil, err
	}
	build, err := s.startBuild(ctx, w, link, commit, buildsDomain.TriggerManual, by, true)
	if errors.Is(err, buildsDomain.ErrTooManyBuilds) && build != nil {
		// Trace visible, le conteneur n'attend plus rien : même état qu'un échec.
		if err := s.Workloads.MarkBuildFailed(ctx, project, w.ID); err != nil {
			s.Logger.Warnw("github workload not marked as failed", "workload", w.ID, "error", err)
		}
		return &domain.CreateWorkloadResponse{WorkloadID: w.ID.String(), Status: "FAILED", Build: build}, nil
	}
	if err != nil {
		if markErr := s.Workloads.MarkBuildFailed(ctx, project, w.ID); markErr != nil {
			s.Logger.Warnw("github workload not marked as failed", "workload", w.ID, "error", markErr)
		}
		return nil, err
	}
	return &domain.CreateWorkloadResponse{WorkloadID: w.ID.String(), Status: w.Status, Build: build}, nil
}

func (s *Service) workload(ctx context.Context, id uuid.UUID) (*workloadsDomain.Workload, error) {
	w, err := s.Workloads.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrLinkNotFound
	}
	return w, err
}

// GetLink : lien du conteneur et dernières opérations. Répond aussi quand la
// fonction est désactivée : la console masque alors simplement le panneau.
func (s *Service) GetLink(ctx context.Context, id uuid.UUID) (*domain.LinkView, error) {
	if !s.Enabled() {
		return &domain.LinkView{}, nil
	}
	view := &domain.LinkView{Enabled: true}
	link, err := s.Repo.GetLink(ctx, id)
	if errors.Is(err, domain.ErrLinkNotFound) {
		return view, nil
	}
	if err != nil {
		return nil, err
	}
	view.Linked, view.Link, view.RepositoryURL = true, link, s.Client.HTMLURL(link.RepoFullName)
	if view.LastBuild, err = s.Builds.LatestForWorkload(ctx, id); err != nil {
		return nil, err
	}
	deployed, err := s.Builds.LatestDeployed(ctx, id)
	if err != nil {
		return nil, err
	}
	if deployed != nil {
		view.DeployedCommit, view.DeployedAt = deployed.GitCommit, deployed.FinishedAt
	}
	return view, nil
}

// PutLink : relie le conteneur à un dépôt (nouveau lien, ou lien désactivé
// réparé). Le conteneur doit appartenir au projet de l'installation : le projet
// vient toujours du conteneur, jamais de la requête.
func (s *Service) PutLink(ctx context.Context, id uuid.UUID, by *uuid.UUID, in domain.LinkSource) (*domain.LinkView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	w, err := s.workload(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := buildsDomain.ValidateName(w.Name); err != nil {
		return nil, err
	}
	link, _, err := s.resolveSource(ctx, w.ProjectID, in)
	if err != nil {
		return nil, err
	}
	link.WorkloadID, link.CreatedBy = id, by
	if existing, err := s.Repo.GetLink(ctx, id); err == nil {
		link.CreatedAt, link.CreatedBy = existing.CreatedAt, existing.CreatedBy
	}
	if err := s.Repo.SaveLink(ctx, link); err != nil {
		return nil, err
	}
	return s.GetLink(ctx, id)
}

// PatchLink : branche, dossier, Dockerfile, déploiement à chaque push.
func (s *Service) PatchLink(ctx context.Context, id uuid.UUID, in domain.PatchLinkRequest) (*domain.LinkView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	link, err := s.Repo.GetLink(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.ContextDir != nil {
		if err := buildsDomain.ValidatePath(*in.ContextDir); err != nil {
			return nil, err
		}
		link.ContextDir = *in.ContextDir
	}
	if in.Dockerfile != nil {
		if err := buildsDomain.ValidatePath(*in.Dockerfile); err != nil {
			return nil, err
		}
		link.Dockerfile = *in.Dockerfile
	}
	if in.Branch != nil && *in.Branch != link.Branch {
		if err := buildsDomain.ValidateGitRef(*in.Branch); err != nil || *in.Branch == "" {
			return nil, buildsDomain.ErrInvalidGitRef
		}
		if link.DisabledReason != "" {
			return nil, domain.ErrLinkDisabled
		}
		token, repo, err := s.repository(ctx, link.InstallationID, link.RepoID)
		if err != nil {
			return nil, err
		}
		if _, err := s.Client.BranchHead(ctx, token, repo.FullName, *in.Branch); err != nil {
			return nil, apiError(err, domain.ErrBranchNotFound)
		}
		link.Branch, link.RepoFullName = *in.Branch, repo.FullName
	}
	if in.AutoDeploy != nil {
		link.AutoDeploy = *in.AutoDeploy
	}
	if err := s.Repo.SaveLink(ctx, link); err != nil {
		return nil, err
	}
	return s.GetLink(ctx, id)
}

// DeleteLink : le conteneur ne suit plus de dépôt ; il continue de tourner.
func (s *Service) DeleteLink(ctx context.Context, id uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.Repo.DeleteLink(ctx, id)
}

// DeployNow : construit et déploie le dernier commit de la branche suivie.
func (s *Service) DeployNow(ctx context.Context, id uuid.UUID, by *uuid.UUID) (*buildsDomain.Build, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	link, err := s.Repo.GetLink(ctx, id)
	if err != nil {
		return nil, err
	}
	if link.DisabledReason != "" {
		return nil, domain.ErrLinkDisabled
	}
	if _, err := s.Repo.GetInstallation(ctx, link.ProjectID, link.InstallationID); err != nil {
		return nil, err
	}
	w, err := s.workload(ctx, id)
	if err != nil {
		return nil, err
	}
	token, repo, err := s.repository(ctx, link.InstallationID, link.RepoID)
	if err != nil {
		return nil, err
	}
	head, err := s.Client.BranchHead(ctx, token, repo.FullName, link.Branch)
	if err != nil {
		return nil, apiError(err, domain.ErrBranchNotFound)
	}
	link.RepoFullName, link.RepoPrivate = repo.FullName, repo.Private
	if err := s.Repo.SaveLink(ctx, link); err != nil {
		return nil, err
	}
	if err := s.Workloads.MarkAwaitingBuild(ctx, w); err != nil {
		return nil, err
	}
	return s.startBuild(ctx, w, link, head.Commit.SHA, buildsDomain.TriggerManual, by, false)
}

// CloneAccess : jeton de clonage, généré par l'activité au moment de créer le
// Job (builds/activities.GitAccess). L'installation doit toujours être liée au
// projet du build. Le nom du dépôt est relu : il a pu être renommé.
func (s *Service) CloneAccess(ctx context.Context, project uuid.UUID, installationID, repoID int64) (*buildActs.CloneAccess, error) {
	if !s.Enabled() {
		return nil, buildsDomain.ErrGitAccessLost
	}
	if _, err := s.Repo.GetInstallation(ctx, project, installationID); err != nil {
		if errors.Is(err, domain.ErrInstallationNotLinked) {
			return nil, buildsDomain.ErrGitAccessLost
		}
		return nil, err
	}
	token, repo, err := s.repository(ctx, installationID, repoID)
	if errors.Is(err, domain.ErrRepositoryInaccessible) {
		return nil, buildsDomain.ErrGitAccessLost
	}
	if err != nil {
		return nil, err
	}
	return &buildActs.CloneAccess{Token: token, CloneURL: s.Client.CloneURL(repo.FullName)}, nil
}

// ── Webhook ─────────────────────────────────────────────────────────────────

// Livraison : résultat du traitement, renvoyé à GitHub (visible dans son
// historique des livraisons).
const (
	WebhookAccepted  = "accepted"
	WebhookIgnored   = "ignored"
	WebhookDuplicate = "duplicate"
)

// HandleWebhook : corps déjà authentifié (signature vérifiée par le contrôleur).
// Répond vite : démarre des workflows, ne construit rien ici.
func (s *Service) HandleWebhook(ctx context.Context, delivery, event string, body []byte) (string, error) {
	first, err := s.Repo.ClaimDelivery(ctx, delivery, event)
	if err != nil {
		return "", err
	}
	if !first {
		return WebhookDuplicate, nil
	}
	status, err := s.dispatch(ctx, event, body)
	if err != nil {
		// Échec du traitement : un nouvel envoi depuis GitHub doit pouvoir réussir.
		if relErr := s.Repo.ReleaseDelivery(ctx, delivery); relErr != nil {
			s.Logger.Warnw("github delivery not released", "delivery", delivery, "error", relErr)
		}
		return "", err
	}
	return status, nil
}

func (s *Service) dispatch(ctx context.Context, event string, body []byte) (string, error) {
	switch event {
	case eventPush:
		var e domain.PushEvent
		if err := json.Unmarshal(body, &e); err != nil {
			return WebhookIgnored, nil
		}
		return s.onPush(ctx, &e)
	case eventInstall:
		var e domain.InstallationEvent
		if err := json.Unmarshal(body, &e); err != nil {
			return WebhookIgnored, nil
		}
		return s.onInstallation(ctx, &e)
	case eventRepos:
		var e domain.InstallationReposEvent
		if err := json.Unmarshal(body, &e); err != nil {
			return WebhookIgnored, nil
		}
		return s.onRepositories(ctx, &e)
	}
	return WebhookIgnored, nil
}

// onPush : un build par conteneur qui suit cette branche. Le dépôt est reconnu
// par son identifiant et par l'installation émettrice, jamais par son nom.
func (s *Service) onPush(ctx context.Context, e *domain.PushEvent) (string, error) {
	branch, isBranch := strings.CutPrefix(e.Ref, refsHeads)
	if !isBranch || e.Deleted || !buildsDomain.ValidCommit(e.After) || strings.Trim(e.After, "0") == "" {
		return WebhookIgnored, nil
	}
	if buildsDomain.ValidateGitRef(branch) != nil || e.Repository.ID == 0 || e.Installation.ID == 0 {
		return WebhookIgnored, nil
	}
	links, err := s.Repo.LinksForPush(ctx, e.Installation.ID, e.Repository.ID, branch)
	if err != nil {
		return "", err
	}
	if len(links) == 0 {
		return WebhookIgnored, nil
	}
	if e.Repository.FullName != "" {
		if err := s.Repo.RenameRepo(ctx, e.Repository.ID, e.Repository.FullName); err != nil {
			s.Logger.Warnw("github repository name not updated", "repo", e.Repository.ID, "error", err)
		} else {
			for i := range links {
				links[i].RepoFullName = e.Repository.FullName
			}
		}
	}
	for i := range links {
		s.buildForPush(ctx, &links[i], e.After)
	}
	return WebhookAccepted, nil
}

// buildForPush : l'échec d'un conteneur ne prive pas les autres de leur build,
// et ne doit pas faire rejouer la livraison pour tous. Un quota atteint laisse
// un build FAILED dans l'historique : pas d'échec silencieux.
func (s *Service) buildForPush(ctx context.Context, link *domain.Link, commit string) {
	w, err := s.Workloads.Get(ctx, link.WorkloadID)
	if err != nil {
		s.Logger.Errorw("github push: container unavailable", "workload", link.WorkloadID, "error", err)
		return
	}
	if err := s.Workloads.MarkAwaitingBuild(ctx, w); err != nil {
		s.Logger.Errorw("github push: container not updated", "workload", link.WorkloadID, "error", err)
	}
	if _, err := s.startBuild(ctx, w, link, commit, buildsDomain.TriggerPush, nil, true); err != nil {
		if errors.Is(err, buildsDomain.ErrTooManyBuilds) {
			if markErr := s.Workloads.MarkBuildFailed(ctx, link.ProjectID, w.ID); markErr != nil {
				s.Logger.Warnw("github workload not marked as failed", "workload", w.ID, "error", markErr)
			}
			return
		}
		s.Logger.Errorw("github push: build not started", "workload", link.WorkloadID, "error", err)
	}
}

func (s *Service) onInstallation(ctx context.Context, e *domain.InstallationEvent) (string, error) {
	id := e.Installation.ID
	var err error
	switch e.Action {
	case "deleted":
		err = s.Repo.InstallationGone(ctx, id)
	case "suspend":
		err = s.Repo.SetSuspended(ctx, id, true)
	case "unsuspend":
		err = s.Repo.SetSuspended(ctx, id, false)
	default:
		return WebhookIgnored, nil
	}
	if err != nil {
		return "", err
	}
	return WebhookAccepted, nil
}

func (s *Service) onRepositories(ctx context.Context, e *domain.InstallationReposEvent) (string, error) {
	ids := func(in []struct {
		ID int64 `json:"id"`
	}) []int64 {
		out := make([]int64, 0, len(in))
		for _, r := range in {
			out = append(out, r.ID)
		}
		return out
	}
	var err error
	switch e.Action {
	case "removed":
		err = s.Repo.DisableRepos(ctx, e.Installation.ID, ids(e.RepositoriesRemoved))
	case "added":
		err = s.Repo.EnableRepos(ctx, e.Installation.ID, ids(e.RepositoriesAdded))
	default:
		return WebhookIgnored, nil
	}
	if err != nil {
		return "", err
	}
	return WebhookAccepted, nil
}
