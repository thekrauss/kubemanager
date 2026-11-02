// Package activities : étapes du workflow de build.
package activities

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/thekrauss/kubemanager/internal/infrastructure/heartbeat"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	"github.com/thekrauss/kubemanager/internal/modules/builds/registry"
	"github.com/thekrauss/kubemanager/internal/modules/builds/repository"
)

const pollEvery = 3 * time.Second

// maxSavedLogBytes : taille maximale du journal conservé d'un build.
const maxSavedLogBytes = 32 << 20

// savedLogCutMarker : première ligne d'un journal dont le début a été retiré.
const savedLogCutMarker = "[…]\n"

var (
	digestRe   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	detectedRe = regexp.MustCompile(`^[a-z-]{1,30}$`)
)

const (
	// noBuildPlanMarker : écrit par build.sh quand le projet n'est pas reconnu.
	noBuildPlanMarker = "betorun:no-build-plan"
	kindDockerfile    = "dockerfile"
)

// ObjectPrefix : préfixe des objets des builds d'un projet.
func ObjectPrefix(project string) string { return "builds/" + project + "/" }

// SourceKey : archive envoyée pour un build.
func SourceKey(project, build string) string { return ObjectPrefix(project) + build + "/source.tar.gz" }

// LogKey : journal conservé après le build.
func LogKey(project, build string) string { return ObjectPrefix(project) + build + "/build.log" }

// PodSelector : pods d'un build.
func PodSelector(build string) string { return labelBuildID + "=" + build }

// ContainerName : conteneur à lire pour le journal.
const ContainerName = containerName

// BuildRef : référence d'un build dans le workflow (rien de secret : tout
// passe dans l'historique Temporal).
type BuildRef struct {
	BuildID   string
	ProjectID string
}

// BuildResult : résultat du Job.
type BuildResult struct {
	Digest   string
	Detected string
}

// FinishInput : état final d'un build.
type FinishInput struct {
	BuildRef
	Status, Failure, Digest, Detected string
}

// NotDeployedInput : le build n'a pas abouti, rien n'est déployé.
type NotDeployedInput struct {
	BuildRef
	Status string
}

// DeployFailedInput : le déploiement n'a pas pu commencer.
type DeployFailedInput struct {
	BuildRef
	State string
}

// CloneAccess : de quoi cloner un dépôt privé. Jamais dans une entrée ou une
// sortie d'activité : tout ce qui y passe reste dans l'historique du workflow.
type CloneAccess struct {
	Token    string
	CloneURL string
}

// GitAccess : jeton de clonage à la demande (voie GitHub). Renvoie
// domain.ErrGitAccessLost si le dépôt n'est plus accessible à ce projet.
type GitAccess interface {
	CloneAccess(ctx context.Context, project uuid.UUID, installationID, repoID int64) (*CloneAccess, error)
}

// Deployer : mise à jour d'un conteneur par le serveur (implémenté par le
// service des conteneurs). Le projet est vérifié : un build ne peut viser
// qu'un conteneur de son projet.
type Deployer interface {
	// DeployBuiltImage : erreurs domain.ErrWorkloadBusy, ErrWorkloadStopped et
	// ErrWorkloadGone pour les cas attendus.
	DeployBuiltImage(ctx context.Context, project, workload uuid.UUID, image string) error
	// MarkBuildFailed : conteneur jamais déployé dont la première image a échoué.
	MarkBuildFailed(ctx context.Context, project, workload uuid.UUID) error
}

// Activities : dépendances des étapes.
type Activities struct {
	Repo       repository.BuildRepository
	Store      objectstore.Store
	K8s        kubernetes.Interface
	Job        JobConfig
	Creds      registry.Credentials
	RegHost    string
	SourceBase string    // URL de l'API vue depuis les pods
	Git        GitAccess // nil : connexion GitHub désactivée
	Deployer   Deployer
	Logger     *zap.SugaredLogger
}

// MarkBuilding : le build démarre.
func (a *Activities) MarkBuilding(ctx context.Context, ref BuildRef) error {
	id, err := uuid.Parse(ref.BuildID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid build id", domain.FailureInternal, err)
	}
	return a.Repo.MarkBuilding(ctx, id)
}

// RunBuild : lance le Job (s'il n'existe pas déjà : réessai après un
// redémarrage du worker) et le suit jusqu'à la fin. Annulé, il supprime le Job.
func (a *Activities) RunBuild(ctx context.Context, ref BuildRef) (*BuildResult, error) {
	// Avec un cluster distant lent, ces appels dépassent le délai de heartbeat :
	// sans signal, toutes les tentatives s'épuiseraient ici.
	if err := heartbeat.While(ctx, pollEvery, func() error {
		if err := a.ensureEnvironment(ctx); err != nil {
			return err
		}
		return a.ensureJob(ctx, ref)
	}); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		activity.RecordHeartbeat(ctx)
		job, err := a.K8s.BatchV1().Jobs(a.Job.Namespace).Get(ctx, jobName(ref.BuildID), metav1.GetOptions{})
		if err != nil {
			if ctx.Err() == nil && !apierrors.IsNotFound(err) {
				a.Logger.Warnw("build job status unavailable", "build", ref.BuildID, "error", err)
			} else if apierrors.IsNotFound(err) {
				return nil, temporal.NewNonRetryableApplicationError("job disappeared", domain.FailureInternal, err)
			}
		} else if done, res, err := a.jobOutcome(ctx, job, ref); done {
			return res, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *Activities) jobOutcome(ctx context.Context, job *batchv1.Job, ref BuildRef) (bool, *BuildResult, error) {
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete:
			digest, detected := a.readResult(ctx, ref)
			return true, &BuildResult{Digest: digest, Detected: detected}, nil
		case batchv1.JobFailed:
			failure := domain.FailureBuild
			if c.Reason == batchv1.JobReasonDeadlineExceeded {
				failure = domain.FailureTimeout
			} else if a.evicted(ctx, ref) {
				failure = domain.FailureDisk
			} else if a.terminationMessage(ctx, ref) == noBuildPlanMarker {
				failure = domain.FailureNoPlan
			}
			return true, nil, temporal.NewNonRetryableApplicationError("build failed", failure, nil)
		}
	}
	return false, nil, nil
}

func (a *Activities) pod(ctx context.Context, build string) (*corev1.Pod, error) {
	pods, err := a.K8s.CoreV1().Pods(a.Job.Namespace).List(ctx, metav1.ListOptions{LabelSelector: PodSelector(build)})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, nil
	}
	return &pods.Items[0], nil
}

// terminationMessage : message écrit par le script du Job.
func (a *Activities) terminationMessage(ctx context.Context, ref BuildRef) string {
	pod, err := a.pod(ctx, ref.BuildID)
	if err != nil || pod == nil {
		return ""
	}
	for _, st := range pod.Status.ContainerStatuses {
		if t := st.State.Terminated; t != nil {
			return strings.TrimSpace(t.Message)
		}
	}
	return ""
}

// readResult : digest et type de projet. Écrits par un pod qui a exécuté du
// code du client : valeurs filtrées, jamais utilisées pour une décision.
func (a *Activities) readResult(ctx context.Context, ref BuildRef) (digest, detected string) {
	digest, detected, _ = strings.Cut(a.terminationMessage(ctx, ref), "\n")
	if !digestRe.MatchString(digest) {
		digest = ""
	}
	if detected = strings.TrimSpace(detected); !detectedRe.MatchString(detected) || detected == kindDockerfile {
		detected = ""
	}
	return digest, detected
}

func (a *Activities) evicted(ctx context.Context, ref BuildRef) bool {
	pod, err := a.pod(ctx, ref.BuildID)
	return err == nil && pod != nil && pod.Status.Reason == "Evicted"
}

// ensureEnvironment : namespace et réseau des builds.
func (a *Activities) ensureEnvironment(ctx context.Context) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: a.Job.Namespace}}
	if _, err := a.K8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	want := newNetworkPolicy(a.Job)
	api := a.K8s.NetworkingV1().NetworkPolicies(a.Job.Namespace)
	if _, err := api.Create(ctx, want, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if _, err := api.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (a *Activities) ensureJob(ctx context.Context, ref BuildRef) error {
	id, err := uuid.Parse(ref.BuildID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid build id", domain.FailureInternal, err)
	}
	// Déjà créé (réessai après un redémarrage) : surtout pas de nouveau jeton.
	if _, err := a.K8s.BatchV1().Jobs(a.Job.Namespace).Get(ctx, jobName(ref.BuildID), metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	b, err := a.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	deadline := time.Duration(a.Job.DeadlineSecond) * time.Second
	expires := time.Now().Add(deadline + time.Minute)

	user, password := a.Creds.ForBuild(b.ProjectID, expires)
	auth, err := registry.DockerConfigJSON(a.RegHost, user, password)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName(ref.BuildID), Namespace: a.Job.Namespace,
			Labels: map[string]string{labelRole: roleBuilder, labelBuildID: ref.BuildID}},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: auth},
	}
	if _, err := a.K8s.CoreV1().Secrets(a.Job.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	spec := JobSpec{
		BuildID: ref.BuildID, ProjectID: ref.ProjectID, GitURL: b.GitURL, GitRef: b.GitRef, ContextDir: b.ContextDir, Dockerfile: b.Dockerfile,
		Image: b.Image, CacheRef: registry.ImageName(a.RegHost, b.ProjectID, b.Name) + ":buildcache", AuthSecret: secret.Name,
	}
	if b.SourceType == domain.SourceGitHub {
		if err := a.prepareGitHubSource(ctx, ref, b, &spec); err != nil {
			return err
		}
	}
	if b.SourceType == domain.SourceUpload {
		exp := expires.Unix()
		spec.SourceURL = fmt.Sprintf("%s/builds/%s/source?expires=%d&sig=%s", strings.TrimRight(a.SourceBase, "/"), b.ID, exp, a.Creds.SourceSignature(b.ID, exp))
	}
	previous, err := a.Repo.List(ctx, b.ProjectID, b.Name)
	if err != nil {
		return err
	}
	for i := range previous {
		if previous[i].Status == domain.StatusSucceeded {
			spec.ImportCache = "1"
			break
		}
	}
	job := newJob(a.Job, spec)
	if _, err := a.K8s.BatchV1().Jobs(a.Job.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// prepareGitHubSource : jeton de clonage généré ici, à la création du Job, et
// remis par un Secret : il ne passe ni par l'historique du workflow, ni par la
// base, ni par la spec du Job, ni par le journal.
//
// Ce jeton vit une heure et le code du client peut le lire : le Dockerfile et
// le build tournent à côté dans le même conteneur, sans isolation de processus
// (voir newJob). C'est acceptable uniquement parce qu'il est restreint au seul
// dépôt construit et à contents:read : il ne donne rien que ce build n'ait déjà.
// Ne jamais élargir cette restriction sans revoir ce point.
func (a *Activities) prepareGitHubSource(ctx context.Context, ref BuildRef, b *domain.Build, spec *JobSpec) error {
	if a.Git == nil || b.GitRepoID == nil || b.GitInstallationID == nil {
		return temporal.NewNonRetryableApplicationError("repository access unavailable", domain.FailureGitAccess, nil)
	}
	access, err := a.Git.CloneAccess(ctx, b.ProjectID, *b.GitInstallationID, *b.GitRepoID)
	if errors.Is(err, domain.ErrGitAccessLost) {
		return temporal.NewNonRetryableApplicationError("repository access lost", domain.FailureGitAccess, err)
	}
	if err != nil {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: gitSecretName(ref.BuildID), Namespace: a.Job.Namespace,
			Labels: map[string]string{labelRole: roleBuilder, labelBuildID: ref.BuildID}},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{gitTokenKey: []byte(access.Token)},
	}
	api := a.K8s.CoreV1().Secrets(a.Job.Namespace)
	if _, err := api.Create(ctx, secret, metav1.CreateOptions{}); apierrors.IsAlreadyExists(err) {
		// Réessai avant la création du Job : le jeton déjà remis est périmé ou perdu.
		if _, err := api.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	spec.GitURL, spec.GitTokenSecret, spec.GitCommit = access.CloneURL, secret.Name, b.GitCommit
	return nil
}

// DeployBuilt : le build a réussi, le conteneur passe à la nouvelle image. Un
// déploiement encore en cours renvoie une erreur réessayée par le workflow
// (backoff borné) ; tout autre refus est définitif et reste visible sur le build.
func (a *Activities) DeployBuilt(ctx context.Context, ref BuildRef) error {
	id, err := uuid.Parse(ref.BuildID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid build id", domain.FailureInternal, err)
	}
	b, err := a.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if b.DeployWorkloadID == nil {
		return a.Repo.SetDeployState(ctx, id, domain.DeployGone)
	}
	// Annulé entre-temps (le workflow l'ignorait encore) : pas de déploiement.
	if b.Status != domain.StatusSucceeded {
		return a.Repo.SetDeployState(ctx, id, domain.DeployNotBuilt)
	}
	newer, err := a.Repo.HasNewerForWorkload(ctx, *b.DeployWorkloadID, b.CreatedAt)
	if err != nil {
		return err
	}
	if newer {
		return a.Repo.SetDeployState(ctx, id, domain.DeploySuperseded)
	}
	err = a.Deployer.DeployBuiltImage(ctx, b.ProjectID, *b.DeployWorkloadID, b.Image)
	switch {
	case err == nil:
		return a.Repo.SetDeployState(ctx, id, domain.DeployStarted)
	case errors.Is(err, domain.ErrWorkloadBusy):
		return temporal.NewApplicationError("deployment in progress", domain.DeployBusy)
	case errors.Is(err, domain.ErrWorkloadStopped):
		return a.Repo.SetDeployState(ctx, id, domain.DeployStopped)
	case errors.Is(err, domain.ErrWorkloadGone):
		return a.Repo.SetDeployState(ctx, id, domain.DeployGone)
	default:
		a.Logger.Warnw("automatic deployment refused", "build", ref.BuildID, "error", err)
		return temporal.NewNonRetryableApplicationError("deployment refused", domain.DeployFailed, nil)
	}
}

// RecordDeployFailure : le déploiement n'a pas pu commencer après les essais.
func (a *Activities) RecordDeployFailure(ctx context.Context, in DeployFailedInput) error {
	id, err := uuid.Parse(in.BuildID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid build id", domain.FailureInternal, err)
	}
	return a.Repo.SetDeployState(ctx, id, in.State)
}

// BuildNotDeployed : build en échec ou annulé. L'ancienne version reste en
// service ; un conteneur qui n'a encore jamais tourné passe en échec, sauf si
// un build plus récent prend le relais.
func (a *Activities) BuildNotDeployed(ctx context.Context, in NotDeployedInput) error {
	id, err := uuid.Parse(in.BuildID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid build id", domain.FailureInternal, err)
	}
	b, err := a.Repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if b.DeployWorkloadID == nil {
		return a.Repo.SetDeployState(ctx, id, domain.DeployGone)
	}
	newer, err := a.Repo.HasNewerForWorkload(ctx, *b.DeployWorkloadID, b.CreatedAt)
	if err != nil {
		return err
	}
	if newer {
		return a.Repo.SetDeployState(ctx, id, domain.DeploySuperseded)
	}
	if err := a.Deployer.MarkBuildFailed(ctx, b.ProjectID, *b.DeployWorkloadID); err != nil {
		return err
	}
	return a.Repo.SetDeployState(ctx, id, domain.DeployNotBuilt)
}

// Finish : journal conservé, état final, puis ménage. Le journal est écrit
// avant l'état : un client qui voit le build terminé peut le lire.
func (a *Activities) Finish(ctx context.Context, in FinishInput) error {
	id, err := uuid.Parse(in.BuildID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid build id", domain.FailureInternal, err)
	}
	a.saveLog(ctx, in.BuildRef)
	if err := a.Repo.Finish(ctx, id, in.Status, in.Failure, in.Digest, in.Detected); err != nil {
		return err
	}
	a.cleanup(ctx, in.BuildRef)
	return nil
}

// savedLogOptions : le journal est lu en entier : logTail n'en garde que la fin.
func savedLogOptions() *corev1.PodLogOptions {
	return &corev1.PodLogOptions{Container: containerName}
}

// logTail garde la fin du journal, où se trouve souvent l'erreur. Il est tenu en
// mémoire par le worker : au plus limit + limit/8 octets.
type logTail struct {
	buf   []byte
	limit int
	cut   bool
}

func (t *logTail) Write(p []byte) (int, error) {
	n := len(p)
	if n > t.limit {
		p = p[n-t.limit:]
		t.buf = t.buf[:0]
		t.cut = true
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit+t.limit/8 {
		t.buf = t.buf[:copy(t.buf, t.buf[len(t.buf)-t.limit:])]
		t.cut = true
	}
	return n, nil
}

// Bytes : journal conservé. S'il a été coupé, il commence par savedLogCutMarker,
// puis à la première ligne entière.
func (t *logTail) Bytes() []byte {
	tail := t.buf
	cut := t.cut
	if len(tail) > t.limit {
		tail = tail[len(tail)-t.limit:]
		cut = true
	}
	if !cut {
		return tail
	}
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return append([]byte(savedLogCutMarker), tail...)
}

func (a *Activities) saveLog(ctx context.Context, ref BuildRef) {
	pod, err := a.pod(ctx, ref.BuildID)
	if err != nil || pod == nil {
		return
	}
	stream, err := a.K8s.CoreV1().Pods(a.Job.Namespace).GetLogs(pod.Name, savedLogOptions()).Stream(ctx)
	if err != nil {
		a.Logger.Warnw("build log unavailable", "build", ref.BuildID, "error", err)
		return
	}
	defer stream.Close()
	tail := logTail{limit: maxSavedLogBytes}
	if _, err := io.Copy(&tail, stream); err != nil && !errors.Is(err, io.EOF) {
		a.Logger.Warnw("build log truncated", "build", ref.BuildID, "error", err)
	}
	saved := tail.Bytes()
	if err := a.Store.Put(ctx, LogKey(ref.ProjectID, ref.BuildID), bytes.NewReader(saved), int64(len(saved)), "text/plain; charset=utf-8"); err != nil {
		a.Logger.Warnw("build log not saved", "build", ref.BuildID, "error", err)
	}
}

func (a *Activities) cleanup(ctx context.Context, ref BuildRef) {
	propagation := metav1.DeletePropagationBackground
	if err := a.K8s.BatchV1().Jobs(a.Job.Namespace).Delete(ctx, jobName(ref.BuildID), metav1.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
		a.Logger.Warnw("build job not deleted", "build", ref.BuildID, "error", err)
	}
	if err := a.K8s.CoreV1().Secrets(a.Job.Namespace).Delete(ctx, secretName(ref.BuildID), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		a.Logger.Warnw("build secret not deleted", "build", ref.BuildID, "error", err)
	}
	if err := a.K8s.CoreV1().Secrets(a.Job.Namespace).Delete(ctx, gitSecretName(ref.BuildID), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		a.Logger.Warnw("build git secret not deleted", "build", ref.BuildID, "error", err)
	}
	if err := a.Store.Delete(ctx, SourceKey(ref.ProjectID, ref.BuildID)); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
		a.Logger.Warnw("build source not deleted", "build", ref.BuildID, "error", err)
	}
}
