// Package service : logique des builds et du registre d'images.
package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/builds/activities"
	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	"github.com/thekrauss/kubemanager/internal/modules/builds/registry"
	"github.com/thekrauss/kubemanager/internal/modules/builds/repository"
	"github.com/thekrauss/kubemanager/internal/modules/builds/workflows"
)

// Attente du pod quand on suit un journal dès le lancement du build.
const (
	logWaitEvery = time.Second
	logWaitMax   = 2 * time.Minute

	// imageTagLength : caractères de l'identifiant du build dans le tag.
	imageTagLength = 12
	// compensationTimeout : écritures détachées de la requête.
	compensationTimeout = 10 * time.Second
)

var gzipMagic = []byte{0x1f, 0x8b}

// Config : réglages du service.
type Config struct {
	Namespace       string
	RegistryHost    string
	MaxArchiveBytes int64
	MaxConcurrent   int64
	Deadline        time.Duration
}

// Service : API des builds.
type Service struct {
	Repo        repository.BuildRepository
	Store       objectstore.Store
	Distributor temporal.TaskDistributor
	K8s         kubernetes.Interface
	Creds       registry.Credentials
	Issuer      *registry.Issuer
	Cfg         Config
	Logger      *zap.SugaredLogger
}

// MaxArchiveBytes : taille maximale d'une archive.
func (s *Service) MaxArchiveBytes() int64 { return s.Cfg.MaxArchiveBytes }

// RegistryHost : registre de la plateforme.
func (s *Service) RegistryHost() string { return s.Cfg.RegistryHost }

func (s *Service) newBuild(ctx context.Context, project uuid.UUID, name, dockerfile string, by *uuid.UUID) (*domain.Build, error) {
	if err := domain.ValidateName(name); err != nil {
		return nil, err
	}
	if dockerfile == "" {
		dockerfile = domain.DefaultDockerfile
	}
	if err := domain.ValidatePath(dockerfile); err != nil {
		return nil, err
	}
	active, err := s.Repo.CountActive(ctx, project)
	if err != nil {
		return nil, err
	}
	if active >= s.Cfg.MaxConcurrent {
		return nil, domain.ErrTooManyBuilds
	}
	id := uuid.New()
	return &domain.Build{
		ID: id, ProjectID: project, Name: name, Dockerfile: dockerfile, Status: domain.StatusQueued, CreatedBy: by,
		Trigger: domain.TriggerManual,
		Image:   registry.ImageName(s.Cfg.RegistryHost, project, name) + ":b-" + id.String()[:imageTagLength],
	}, nil
}

// FromGit : build d'un dépôt public.
func (s *Service) FromGit(ctx context.Context, project uuid.UUID, req domain.CreateGitBuildRequest, by *uuid.UUID) (*domain.Build, error) {
	gitURL, err := domain.NormalizeGitURL(req.GitURL)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateGitRef(req.GitRef); err != nil {
		return nil, err
	}
	if err := domain.ValidatePath(req.ContextDir); err != nil {
		return nil, err
	}
	b, err := s.newBuild(ctx, project, req.Name, req.Dockerfile, by)
	if err != nil {
		return nil, err
	}
	b.SourceType, b.GitURL, b.GitRef, b.ContextDir = domain.SourceGit, gitURL, req.GitRef, req.ContextDir
	return b, s.start(ctx, b)
}

// GitHubBuild : build d'un commit précis d'un dépôt GitHub, déployé par le
// serveur en cas de réussite.
type GitHubBuild struct {
	Project, Workload uuid.UUID
	Name              string
	InstallationID    int64
	RepoID            int64
	CloneURL          string
	Branch, Commit    string
	ContextDir        string
	Dockerfile        string
	Trigger           string
	By                *uuid.UUID
	// RecordRejection : quota atteint, garder une trace (build FAILED) au lieu
	// de seulement renvoyer l'erreur. Un push n'a personne à qui répondre.
	RecordRejection bool
}

// FromGitHub : le dernier commit gagne. Les builds encore en cours du même
// conteneur sont annulés d'abord : ils libèrent leur place dans le quota, et
// leur annulation est enregistrée tout de suite pour ne pas la compter.
func (s *Service) FromGitHub(ctx context.Context, req GitHubBuild) (*domain.Build, error) {
	if err := domain.ValidateGitRef(req.Branch); err != nil || req.Branch == "" {
		return nil, domain.ErrInvalidGitRef
	}
	if !domain.ValidCommit(req.Commit) {
		return nil, domain.ErrInvalidGitRef
	}
	if err := domain.ValidatePath(req.ContextDir); err != nil {
		return nil, err
	}
	if err := s.supersede(ctx, req.Workload); err != nil {
		return nil, err
	}
	b, err := s.newBuild(ctx, req.Project, req.Name, req.Dockerfile, req.By)
	rejected := errors.Is(err, domain.ErrTooManyBuilds)
	if rejected && req.RecordRejection {
		id := uuid.New()
		b, err = &domain.Build{ID: id, ProjectID: req.Project, Name: req.Name, Dockerfile: req.Dockerfile, CreatedBy: req.By,
			Image: registry.ImageName(s.Cfg.RegistryHost, req.Project, req.Name) + ":b-" + id.String()[:imageTagLength]}, nil
	}
	if err != nil {
		return nil, err
	}
	if b.Dockerfile == "" {
		b.Dockerfile = domain.DefaultDockerfile
	}
	workload := req.Workload
	b.SourceType, b.GitURL, b.GitRef, b.GitCommit, b.ContextDir = domain.SourceGitHub, req.CloneURL, req.Branch, req.Commit, req.ContextDir
	b.GitRepoID, b.GitInstallationID, b.DeployWorkloadID = &req.RepoID, &req.InstallationID, &workload
	b.Trigger, b.DeployState = req.Trigger, domain.DeployPending
	if rejected {
		now := time.Now()
		b.Status, b.Failure, b.DeployState, b.FinishedAt = domain.StatusFailed, domain.FailureTooMany, domain.DeployNotBuilt, &now
		if err := s.Repo.Create(ctx, b); err != nil {
			return nil, err
		}
		return b, domain.ErrTooManyBuilds
	}
	return b, s.start(ctx, b)
}

// supersede : annule les builds en cours de ce conteneur.
func (s *Service) supersede(ctx context.Context, workload uuid.UUID) error {
	active, err := s.Repo.ActiveForWorkload(ctx, workload)
	if err != nil {
		return err
	}
	for i := range active {
		id := active[i].ID
		if err := s.Repo.Finish(ctx, id, domain.StatusCancelled, domain.FailureSuperseded, "", ""); err != nil {
			return err
		}
		if err := s.Repo.SetDeployState(ctx, id, domain.DeploySuperseded); err != nil {
			return err
		}
		// Déjà terminé : rien à annuler. Le workflow supprime le Job et garde le journal.
		if err := s.Distributor.CancelWorkflow(ctx, workflows.WorkflowIDPrefix+id.String(), ""); err != nil {
			s.Logger.Warnw("superseded build not cancelled", "build", id, "error", err)
		}
	}
	return nil
}

// LatestForWorkload : dernier build destiné à ce conteneur.
func (s *Service) LatestForWorkload(ctx context.Context, workload uuid.UUID) (*domain.Build, error) {
	return s.Repo.LatestForWorkload(ctx, workload)
}

// LatestDeployed : dernier build envoyé sur ce conteneur.
func (s *Service) LatestDeployed(ctx context.Context, workload uuid.UUID) (*domain.Build, error) {
	return s.Repo.LatestDeployed(ctx, workload)
}

// FromArchive : build d'une archive .tar.gz.
func (s *Service) FromArchive(ctx context.Context, project uuid.UUID, name, dockerfile string, by *uuid.UUID, archive io.Reader, size int64) (*domain.Build, error) {
	r := bufio.NewReader(archive)
	head, err := r.Peek(len(gzipMagic))
	if err != nil || head[0] != gzipMagic[0] || head[1] != gzipMagic[1] {
		return nil, domain.ErrInvalidArchive
	}
	b, err := s.newBuild(ctx, project, name, dockerfile, by)
	if err != nil {
		return nil, err
	}
	b.SourceType = domain.SourceUpload
	key := activities.SourceKey(project.String(), b.ID.String())
	if err := s.Store.Put(ctx, key, r, size, "application/gzip"); err != nil {
		return nil, err
	}
	if err := s.start(ctx, b); err != nil {
		// Aucun workflow ne lira ni ne supprimera cette archive.
		s.compensate(ctx, "orphan build archive not removed", b.ID, func(ctx context.Context) error {
			return s.Store.Delete(ctx, key)
		})
		return nil, err
	}
	return b, nil
}

// compensate : annule une écriture dont la suite a échoué, y compris si la
// requête a été interrompue. Erreur journalisée : celle de l'appelant prime.
func (s *Service) compensate(ctx context.Context, msg string, id uuid.UUID, undo func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
	defer cancel()
	if err := undo(ctx); err != nil && s.Logger != nil {
		s.Logger.Warnw(msg, "build", id, "error", err)
	}
}

func (s *Service) start(ctx context.Context, b *domain.Build) error {
	if err := s.Repo.Create(ctx, b); err != nil {
		return err
	}
	in := workflows.BuildInput{
		BuildRef: activities.BuildRef{BuildID: b.ID.String(), ProjectID: b.ProjectID.String()},
		Deadline: s.Cfg.Deadline,
		Deploy:   b.DeployWorkloadID != nil,
	}
	_, err := s.Distributor.ExecuteWorkflow(ctx, temporal.ExecuteOptions{WorkflowID: workflows.WorkflowIDPrefix + b.ID.String()},
		workflows.BuildImageWorkflow, in)
	if err != nil {
		// Sinon QUEUED à vie : il compterait pour toujours dans ErrTooManyBuilds.
		s.compensate(ctx, "build failure not recorded", b.ID, func(ctx context.Context) error {
			return s.Repo.Finish(ctx, b.ID, domain.StatusFailed, domain.FailureInternal, "", "")
		})
		return err
	}
	return nil
}

// Get : un build du projet.
func (s *Service) Get(ctx context.Context, project, id uuid.UUID) (*domain.Build, error) {
	return s.Repo.Get(ctx, project, id)
}

// List : builds récents du projet.
func (s *Service) List(ctx context.Context, project uuid.UUID, name string) ([]domain.Build, error) {
	return s.Repo.List(ctx, project, name)
}

// Cancel : arrête un build en cours (le workflow supprime le Job).
func (s *Service) Cancel(ctx context.Context, project, id uuid.UUID) error {
	b, err := s.Repo.Get(ctx, project, id)
	if err != nil {
		return err
	}
	if b.Finished() {
		return domain.ErrBuildFinished
	}
	return s.Distributor.CancelWorkflow(ctx, workflows.WorkflowIDPrefix+id.String(), "")
}

// Logs : journal du pod pendant le build, journal conservé ensuite.
func (s *Service) Logs(ctx context.Context, project, id uuid.UUID, follow bool) (io.ReadCloser, error) {
	b, err := s.Repo.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if !b.Finished() {
		if stream := s.podLogs(ctx, id, follow); stream != nil {
			return stream, nil
		}
		if b, err = s.Repo.Get(ctx, project, id); err != nil {
			return nil, err
		}
	}
	obj, _, err := s.Store.Get(ctx, activities.LogKey(project.String(), id.String()))
	if errors.Is(err, objectstore.ErrNotFound) {
		return io.NopCloser(emptyReader{}), nil
	}
	return obj, err
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// podLogs : flux du pod, en attendant qu'il démarre si on suit le build.
func (s *Service) podLogs(ctx context.Context, id uuid.UUID, follow bool) io.ReadCloser {
	deadline := time.Now().Add(logWaitMax)
	for {
		pods, err := s.K8s.CoreV1().Pods(s.Cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: activities.PodSelector(id.String())})
		if err == nil && len(pods.Items) > 0 && pods.Items[0].Status.Phase != corev1.PodPending {
			stream, err := s.K8s.CoreV1().Pods(s.Cfg.Namespace).GetLogs(pods.Items[0].Name,
				&corev1.PodLogOptions{Container: activities.ContainerName, Follow: follow}).Stream(ctx)
			if err == nil {
				return stream
			}
		}
		if !follow || time.Now().After(deadline) {
			return nil
		}
		// Le build a pu se terminer (ou échouer) sans que le pod soit visible.
		if b, err := s.Repo.GetByID(ctx, id); err == nil && b.Finished() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(logWaitEvery):
		}
	}
}

// Source : archive d'un build en cours, pour son Job (URL signée).
func (s *Service) Source(ctx context.Context, id uuid.UUID, expires int64, sig string) (io.ReadCloser, int64, error) {
	if !s.Creds.VerifySource(id, expires, sig, time.Now()) {
		return nil, 0, domain.ErrBuildNotFound
	}
	b, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if b.Finished() {
		return nil, 0, domain.ErrBuildNotFound
	}
	obj, info, err := s.Store.Get(ctx, activities.SourceKey(b.ProjectID.String(), id.String()))
	if err != nil {
		return nil, 0, err
	}
	return obj, info.Size, nil
}

// RegistryToken : jeton du registre pour un identifiant.
func (s *Service) RegistryToken(user, password, service string, scopes []string) (*domain.TokenResponse, error) {
	if service != s.Issuer.Service {
		return nil, domain.ErrRegistryForbidden
	}
	now := time.Now().UTC()
	project, granted, err := s.Creds.Verify(user, password, now)
	if err != nil {
		return nil, domain.ErrRegistryForbidden
	}
	token, ttl, err := s.Issuer.Issue(user, project, granted, scopes, now)
	if err != nil {
		return nil, err
	}
	return &domain.TokenResponse{Token: token, AccessToken: token, ExpiresIn: ttl, IssuedAt: now.Format(time.RFC3339)}, nil
}
