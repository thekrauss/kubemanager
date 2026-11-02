package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	projectDomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/repository"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

type fakeDistributor struct {
	temporal.TaskDistributor
	args       []any
	terminated []string
	cancelled  []string
	started    []string
	running    map[string]bool
	// stuck : l'annulation ne produit aucun effet (worker tombé).
	stuck    bool
	startErr error
}

func (f *fakeDistributor) IsRunning(_ context.Context, workflowID string) (bool, error) {
	return f.running[workflowID], nil
}

func (f *fakeDistributor) TerminateWorkflow(_ context.Context, workflowID, _, _ string) error {
	f.terminated = append(f.terminated, workflowID)
	delete(f.running, workflowID)
	return nil
}

func (f *fakeDistributor) CancelWorkflow(_ context.Context, workflowID, _ string) error {
	f.cancelled = append(f.cancelled, workflowID)
	if !f.stuck {
		delete(f.running, workflowID)
	}
	return nil
}

func (f *fakeDistributor) ExecuteWorkflow(_ context.Context, opts temporal.ExecuteOptions, _ any, args ...any) (temporal.WorkflowHandle, error) {
	if f.startErr != nil {
		return temporal.WorkflowHandle{}, f.startErr
	}
	f.args = args
	f.started = append(f.started, opts.WorkflowID)
	return temporal.WorkflowHandle{}, nil
}

// cancelOnStart : la requête s'interrompt juste après le lancement du workflow.
type cancelOnStart struct {
	*fakeDistributor
	cancel func()
}

func (c cancelOnStart) ExecuteWorkflow(ctx context.Context, o temporal.ExecuteOptions, w any, a ...any) (temporal.WorkflowHandle, error) {
	h, err := c.fakeDistributor.ExecuteWorkflow(ctx, o, w, a...)
	c.cancel()
	return h, err
}

// fakeVariables : magasin en mémoire, avec les règles réelles du contrat.
type fakeVariables struct {
	repository.VariableStore
	revision int64
	values   map[string]string
	secrets  map[string]bool
	imported map[string]string
	applied  []domain.VariableChange
}

func (f *fakeVariables) Import(_ context.Context, _ uuid.UUID, v map[string]string) (int64, error) {
	f.imported = v
	if f.revision == 0 {
		f.revision = 1
	}
	return f.revision, nil
}

func (f *fakeVariables) Apply(_ context.Context, _ uuid.UUID, c domain.VariableChange) (int64, bool, error) {
	f.applied = append(f.applied, c)
	changed := len(c.Env)+len(c.Secrets)+len(c.Unset) > 0
	if changed || f.revision == 0 {
		f.revision++
	}
	return f.revision, changed, nil
}

func (f *fakeVariables) List(context.Context, uuid.UUID) ([]domain.VariableView, error) {
	return []domain.VariableView{{Name: "A"}}, nil
}

type fakeRepo struct {
	repository.WorkloadRepository
	w                            domain.Workload
	specImage, specDisk, specURL string
	spec                         []byte

	runtimeCalls                  int
	replicas, port                int
	cpu, mem                      string
	tls                           bool
	usedCPU, usedMem, usedStorage int64

	// statuses : statuts écrits. statusErr : refus de l'écriture conditionnelle.
	statuses  []string
	statusErr error
	deleted   int
	created   int
	usageErr  error
	// onCreate : appelé après l'écriture de la ligne (requête interrompue...).
	onCreate func()
}

func (f *fakeRepo) Create(_ context.Context, w *domain.Workload) error {
	f.created++
	if f.onCreate != nil {
		f.onCreate()
	}
	f.w = *w
	return nil
}

func (f *fakeRepo) ListByProject(context.Context, uuid.UUID) ([]domain.Workload, error) {
	return nil, nil
}

func (f *fakeRepo) UpdateStatus(ctx context.Context, _ uuid.UUID, status, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.statusErr != nil {
		return f.statusErr
	}
	f.statuses = append(f.statuses, status)
	return nil
}

func (f *fakeRepo) Delete(ctx context.Context, _ uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.deleted++
	return nil
}

func (f *fakeRepo) GetByID(context.Context, uuid.UUID) (*domain.Workload, error) {
	w := f.w
	return &w, nil
}
func (f *fakeRepo) UpdateRuntime(_ context.Context, _ uuid.UUID, replicas int, cpu, mem string, port int, tls bool) error {
	f.runtimeCalls++
	f.replicas, f.cpu, f.mem, f.port, f.tls = replicas, cpu, mem, port, tls
	return nil
}
func (f *fakeRepo) GetTotalUsageByProject(context.Context, uuid.UUID) (int64, int64, int64, error) {
	if f.usageErr != nil {
		return 0, 0, 0, f.usageErr
	}
	return f.usedCPU, f.usedMem, f.usedStorage, nil
}
func (f *fakeRepo) UpdateSpec(ctx context.Context, _ uuid.UUID, image, disk, url string, spec []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.specImage, f.specDisk, f.specURL, f.spec = image, disk, url, spec
	return nil
}

// Nouvelle version : seule l'image change, le reste de la configuration
// enregistrée est repris (avant : port, ressources, réplicas, TLS et URL
// perdus, image jamais enregistrée).
func TestUpdate_KeepsSavedConfiguration(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"

	project := uuid.MustParse("37f62891-bdce-40b1-be27-833c1fea931c")
	repo := &fakeRepo{w: domain.Workload{
		ID: uuid.New(), ProjectID: project, Name: "web", Namespace: "km-sunupay",
		Image: "nginx:1.26", Replicas: 2, TargetPort: 3000, TLSEnabled: true,
		CPULimit: "500m", MemoryLimit: "512Mi", CPURequest: "100m", MemoryRequest: "128Mi",
		StorageSize: "1Gi", StorageClass: "local-path",
	}}
	dist := &fakeDistributor{}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}

	require.NoError(t, s.Update(context.Background(), repo.w.ID.String(), domain.UpdateWorkloadRequest{Image: "nginx:1.27"}))

	assert.Equal(t, "nginx:1.27", repo.specImage, "nouvelle image enregistrée")
	assert.Equal(t, "1Gi", repo.specDisk)
	assert.Equal(t, "https://web-37f628.5.196.223.196.sslip.io", repo.specURL, "URL enregistrée")
	require.Len(t, dist.args, 1)
	in := dist.args[0].(workflows.DeployWorkloadInput)
	assert.Equal(t, "nginx:1.27", in.Image)
	assert.Equal(t, "web", in.ReleaseName)
	assert.Equal(t, 2, in.Replicas)
	assert.Equal(t, 3000, in.TargetPort)
	assert.Equal(t, "500m", in.CPULimit)
	assert.Equal(t, "512Mi", in.MemoryLimit)
	assert.True(t, in.TLSEnabled)
	assert.Equal(t, "web-37f628.5.196.223.196.sslip.io", in.ExternalHost, "URL publique conservée")

	err := s.Update(context.Background(), repo.w.ID.String(), domain.UpdateWorkloadRequest{StorageSize: "512Mi"})
	assert.Error(t, err, "disque réduit refusé")
	assert.Error(t, s.Update(context.Background(), "pas-un-uuid", domain.UpdateWorkloadRequest{}))
}

// Spec enregistrée : les réglages sans colonne en base (autoscaling, sondes,
// type de service, point de montage, sécurité) survivent à une mise à jour.
func TestUpdate_KeepsDeclarativeSpec(t *testing.T) {
	spec := domain.CreateWorkloadRequest{Name: "api", Image: "ghcr.io/sunupay/api:1.0", Replicas: 2, TargetPort: 3000,
		ServiceType: "LoadBalancer", MountPath: "/var/lib/app", HealthPath: "/healthz", ReadinessPath: "/ready",
		RunAsUser: 101, ReadOnlyRootFS: true, PDBEnabled: true,
		Autoscaling: &domain.AutoscalingConfig{Enabled: true, MinReplicas: 2, MaxReplicas: 6, TargetCPUUtilizationPercentage: 70}}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	repo := &fakeRepo{w: domain.Workload{ID: uuid.New(), ProjectID: uuid.New(), Name: "api", Namespace: "km-x",
		Image: "ghcr.io/sunupay/api:1.0", Spec: raw}}
	dist := &fakeDistributor{}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}

	require.NoError(t, s.Update(context.Background(), repo.w.ID.String(), domain.UpdateWorkloadRequest{Image: "ghcr.io/sunupay/api:1.1"}))
	in := dist.args[0].(workflows.DeployWorkloadInput)
	assert.Equal(t, "ghcr.io/sunupay/api:1.1", in.Image)
	assert.Equal(t, "LoadBalancer", in.ServiceType)
	assert.Equal(t, "/var/lib/app", in.MountPath)
	assert.Equal(t, "/healthz", in.HealthPath)
	assert.Equal(t, "/ready", in.ReadinessPath)
	assert.Equal(t, int64(101), in.RunAsUser)
	assert.True(t, in.ReadOnlyRootFS)
	assert.True(t, in.PDBEnabled)
	assert.True(t, in.AutoscalingEnabled)
	assert.Equal(t, int32(6), in.AutoscalingMax)

	var saved domain.CreateWorkloadRequest
	require.NoError(t, json.Unmarshal(repo.spec, &saved))
	assert.Equal(t, "ghcr.io/sunupay/api:1.1", saved.Image, "nouvelle image enregistrée dans la spec")
	assert.Nil(t, saved.EnvVars, "jamais de variables dans la spec")
}

// Suppression : les déploiements en cours sont annulés et terminés avant la
// désinstallation (sinon réinstallés par leurs nouvelles tentatives).
func TestDelete_StopsRunningDeployFirst(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "crash", Namespace: "km-x"}}
	dist := &fakeDistributor{running: map[string]bool{"workload-deploy-" + id.String(): true, "workload-update-" + id.String(): true}}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}
	require.NoError(t, s.Delete(context.Background(), id))
	assert.Equal(t, []string{"workload-deploy-" + id.String(), "workload-update-" + id.String()}, dist.cancelled)
	assert.Empty(t, dist.terminated, "annulation entendue : pas d'arrêt brutal")
	assert.Equal(t, []string{"workload-delete-" + id.String()}, dist.started, "suppression lancée ensuite")
	assert.Equal(t, []string{"DELETING"}, repo.statuses)
}

// La désinstallation attend vraiment la fin du déploiement annulé ; si rien ne
// répond, l'arrêt brutal évite de bloquer la suppression.
func TestDelete_TerminatesStuckDeploy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		id := uuid.New()
		deploy := "workload-deploy-" + id.String()
		repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "crash", Namespace: "km-x"}}
		dist := &fakeDistributor{running: map[string]bool{deploy: true}, stuck: true}
		s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}
		start := time.Now()
		require.NoError(t, s.Delete(context.Background(), id))
		assert.GreaterOrEqual(t, time.Since(start), deployStopWait)
		assert.Equal(t, []string{deploy}, dist.cancelled)
		assert.Equal(t, []string{deploy}, dist.terminated)
		assert.Equal(t, []string{"workload-delete-" + id.String()}, dist.started)
	})
}

// Rien en cours : ni annulation ni attente.
func TestDelete_NothingRunning(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x"}}
	dist := &fakeDistributor{}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}
	require.NoError(t, s.Delete(context.Background(), id))
	assert.Empty(t, dist.cancelled)
	assert.Equal(t, []string{"workload-delete-" + id.String()}, dist.started)
}

// Deux suppressions : la seconde ne relance rien et ne renvoie pas d'erreur,
// qu'elle voie le workflow en cours ou qu'elle arrive en même temps que lui.
func TestDelete_AlreadyRunning(t *testing.T) {
	id := uuid.New()
	deleteID := "workload-delete-" + id.String()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Status: "DELETING"}}
	dist := &fakeDistributor{running: map[string]bool{deleteID: true}}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}
	require.NoError(t, s.Delete(context.Background(), id))
	assert.Empty(t, dist.started)
	assert.Empty(t, repo.statuses)

	raced := &fakeDistributor{startErr: serviceerror.NewWorkflowExecutionAlreadyStarted("started", "", "run")}
	s.Distributor = raced
	require.NoError(t, s.Delete(context.Background(), id))
}

// Temporal indisponible : DELETE_FAILED, et non DELETING pour toujours, même si
// la requête est déjà interrompue.
func TestDelete_StartFailure(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Status: "DELETING"}}
	down := errors.New("temporal down")
	s := &WorkloadService{Repo: repo, Distributor: &fakeDistributor{startErr: down}, Variables: &fakeVariables{}}
	require.ErrorIs(t, s.Delete(context.Background(), id), down)
	assert.Equal(t, []string{"DELETING", "DELETE_FAILED"}, repo.statuses)
}

func stuckUpdate(t *testing.T, startErr error) (*WorkloadService, *fakeRepo, uuid.UUID) {
	t.Helper()
	saved := configs.AppConfig
	t.Cleanup(func() { configs.AppConfig = saved })
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1",
		Status: "RUNNING", CurrentPhase: "DEPLOYED", Replicas: 1, TargetPort: 8080, CPULimit: "100m", MemoryLimit: "64Mi"},
		usedCPU: 100, usedMem: 64}
	s := &WorkloadService{Repo: repo, Distributor: &fakeDistributor{startErr: startErr}, Variables: &fakeVariables{},
		ProjectRepo: fakeProjects{p: &projectDomain.Project{CpuLimit: "2000m", MemoryLimit: "2048Mi", StorageLimit: "10Gi"}}}
	return s, repo, id
}

// Deux mises à jour concurrentes passent le contrôle préalable : la seconde est
// refusée au démarrage, et la base n'annonce pas son image (avant : rattachée en
// silence à l'exécution en cours, image enregistrée mais jamais déployée).
func TestUpdate_LosesRaceWithoutWritingSpec(t *testing.T) {
	s, repo, id := stuckUpdate(t, fmt.Errorf("execute workflow: %w",
		serviceerror.NewWorkflowExecutionAlreadyStarted("started", "", "run")))
	err := s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2", Replicas: 2})
	require.ErrorIs(t, err, domain.ErrDeployInProgress)
	assert.Nil(t, repo.spec, "spec non écrite")
	assert.Zero(t, repo.runtimeCalls)
	assert.Equal(t, []string{"STARTING", "RUNNING"}, repo.statuses)
}

// Temporal indisponible : ni nouvelle spec ni STARTING resté en base.
func TestUpdate_StartFailureRestoresStatus(t *testing.T) {
	down := errors.New("temporal down")
	s, repo, id := stuckUpdate(t, down)
	err := s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"})
	require.ErrorIs(t, err, down)
	assert.Nil(t, repo.spec)
	assert.Equal(t, []string{"STARTING", "RUNNING"}, repo.statuses)
}

// Un STARTING déjà orphelin ne se rétablit pas : aucun workflow ne le terminerait.
func TestUpdate_StartFailureOnStartingFails(t *testing.T) {
	s, repo, id := stuckUpdate(t, errors.New("temporal down"))
	repo.w.Status = "STARTING"
	require.Error(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"}))
	assert.Equal(t, []string{"STARTING", "FAILED"}, repo.statuses)
}

// Le workflow lancé, la nouvelle version s'enregistre même si le client est parti.
func TestUpdate_SpecSavedAfterCanceledRequest(t *testing.T) {
	s, repo, id := stuckUpdate(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Distributor = cancelOnStart{&fakeDistributor{}, cancel}
	require.NoError(t, s.Update(ctx, id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"}))
	assert.Equal(t, "nginx:2", repo.specImage)
}

// Un conteneur en suppression refuse toute opération, sans rien écrire ni lancer.
func TestDeletingWorkloadRefusesEverything(t *testing.T) {
	for _, status := range []string{"DELETING", "DELETED"} {
		t.Run(status, func(t *testing.T) {
			s, store, _, dist, id := domainService(t)
			repo := s.Repo.(*fakeRepo)
			repo.w.Status = status
			d := &domain.WorkloadDomain{ID: uuid.New(), WorkloadID: id, Hostname: "a.exemple.sn", Token: "t", Verified: true}
			store.rows[d.ID] = d
			ctx := context.Background()

			ops := map[string]func() error{
				"Update": func() error { return s.Update(ctx, id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"}) },
				"PatchVariables": func() error {
					_, err := s.PatchVariables(ctx, id, domain.PatchVariablesRequest{Env: map[string]string{"A": "b"}})
					return err
				},
				"AddDomain": func() error {
					_, err := s.AddDomain(ctx, id, "b.exemple.sn")
					return err
				},
				"VerifyDomain": func() error {
					_, err := s.VerifyDomain(ctx, id, d.ID)
					return err
				},
				"RemoveDomain": func() error { return s.RemoveDomain(ctx, id, d.ID) },
				"Stop":         func() error { return s.Stop(ctx, id) },
				"Start":        func() error { return s.Start(ctx, id) },
				"Restart":      func() error { return s.Restart(ctx, id) },
				"Rollback":     func() error { return s.Rollback(ctx, id, 0) },
			}
			for name, op := range ops {
				assert.ErrorIs(t, op(), domain.ErrWorkloadDeleting, name)
			}
			assert.Empty(t, dist.started)
			assert.Empty(t, repo.statuses)
			assert.Contains(t, store.rows, d.ID, "domaine conservé")
		})
	}
}

// Workflow de suppression en cours mais statut déjà réécrit : le contrôle le voit.
func TestDeleteWorkflowRunningRefusesUpdate(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1"}}
	dist := &fakeDistributor{running: map[string]bool{workflows.WorkflowIDDeletePrefix + id.String(): true}}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}
	assert.ErrorIs(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"}), domain.ErrWorkloadDeleting)
}

// Écriture conditionnelle refusée (suppression lancée entre le contrôle et
// l'écriture) : le workflow ne démarre pas.
func TestUpdate_ConditionalWriteRefused(t *testing.T) {
	s, repo, id := stuckUpdate(t, nil)
	repo.statusErr = domain.ErrWorkloadDeleting
	err := s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"})
	require.ErrorIs(t, err, domain.ErrWorkloadDeleting)
	assert.Empty(t, s.Distributor.(*fakeDistributor).started)
	assert.Nil(t, repo.spec)
}

// Un build terminé ne déploie pas sur un conteneur en suppression.
func TestDeployBuiltImage_DeletingRace(t *testing.T) {
	s, repo, id := stuckUpdate(t, nil)
	repo.statusErr = domain.ErrWorkloadDeleting
	assert.ErrorIs(t, s.DeployBuiltImage(context.Background(), repo.w.ProjectID, id, "nginx:2"), buildsDomain.ErrWorkloadGone)
}

// Une image du registre de la plateforme n'est déployable que par son projet :
// sinon, un nœud qui l'a en cache la lancerait sans contrôle du registre.
func TestCheckImage_RefusesOtherProject(t *testing.T) {
	const host = "registry.betorun.app"
	project := uuid.New()
	s := &WorkloadService{Registry: &RegistrySettings{Host: host}}

	assert.NoError(t, s.checkImage(project, "nginx:1.27"))
	assert.NoError(t, s.checkImage(project, host+"/"+project.String()+"/web:b-1"))
	assert.ErrorIs(t, s.checkImage(project, host+"/"+uuid.NewString()+"/web:b-1"), buildsDomain.ErrForeignImage)
	assert.ErrorIs(t, s.checkImage(project, "REGISTRY.BETORUN.APP/"+uuid.NewString()+"/web:b-1"), buildsDomain.ErrForeignImage)

	assert.NoError(t, (&WorkloadService{}).checkImage(project, host+"/"+uuid.NewString()+"/web:b-1"), "builds désactivés")
}

type fakeProjects struct {
	projectRepo.ProjectRepository
	p *projectDomain.Project
}

func (f fakeProjects) GetProjectByID(context.Context, string) (*projectDomain.Project, error) {
	return f.p, nil
}

// Une mise à jour ne dépasse pas les quotas : sans ce contrôle, créer petit
// puis monter les réplicas contournait la limite du projet.
func TestUpdate_RuntimeWithinQuota(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"
	project := uuid.New()
	id := uuid.New()
	mk := func() (*WorkloadService, *fakeRepo) {
		repo := &fakeRepo{usedCPU: 500, usedMem: 256, w: domain.Workload{
			ID: id, ProjectID: project, Name: "web", Namespace: "km-x", Image: "nginx:1", Replicas: 1, TargetPort: 8080,
			CPULimit: "500m", MemoryLimit: "256Mi", StorageSize: "1Gi",
		}}
		return &WorkloadService{Distributor: &fakeDistributor{}, Repo: repo, Variables: &fakeVariables{},
			ProjectRepo: fakeProjects{p: &projectDomain.Project{CpuLimit: "2000m", MemoryLimit: "2048Mi", StorageLimit: "10Gi"}}}, repo
	}

	s, repo := mk()
	tls := true
	require.NoError(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Replicas: 3, CPULimit: "600m", TargetPort: 3000, TLSEnabled: &tls, EnvVars: map[string]string{}}))
	assert.Equal(t, 1, repo.runtimeCalls)
	assert.Equal(t, 3, repo.replicas)
	assert.Equal(t, "600m", repo.cpu)
	assert.Equal(t, 3000, repo.port)
	assert.True(t, repo.tls)
	var spec domain.CreateWorkloadRequest
	require.NoError(t, json.Unmarshal(repo.spec, &spec))
	assert.Equal(t, 3, spec.Replicas, "la spec suit : un déploiement suivant ne repart pas à l'ancienne valeur")

	s, repo = mk()
	err := s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Replicas: 5, EnvVars: map[string]string{}})
	require.Error(t, err, "5 × 500m > 2000m")
	assert.Contains(t, err.Error(), "quota")
	assert.Zero(t, repo.runtimeCalls, "rien n'est enregistré")
	assert.Nil(t, repo.spec)

	s, _ = mk()
	err = s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{MemoryLimit: "8Gi", EnvVars: map[string]string{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota")

	s, _ = mk()
	assert.Error(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{TargetPort: 22, EnvVars: map[string]string{}}))
	s, _ = mk()
	assert.Error(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Replicas: -1, EnvVars: map[string]string{}}))

	// Sans changement de ressources : pas de contrôle, rien de plus enregistré.
	s, repo = mk()
	repo.usedCPU = 999999
	require.NoError(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2", EnvVars: map[string]string{}}))
	assert.Zero(t, repo.runtimeCalls)
}

// L'historique Temporal conserve tout ce qu'un workflow reçoit : l'entrée ne
// doit contenir aucune valeur, seulement la révision.
func TestUpdate_NoValueInWorkflowInput(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1", Replicas: 1, TargetPort: 8080, StorageSize: "1Gi"}}
	dist := &fakeDistributor{}
	vars := &fakeVariables{revision: 4}
	repo.w.EnvRevision = 4
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: vars}

	const plain, secret = "valeur-visible-1234", "s3cret-ne-doit-pas-fuiter"
	require.NoError(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{
		EnvVars: map[string]string{"A": plain}, SecretData: map[string]string{"B": secret},
	}))
	require.Len(t, dist.args, 1)
	raw, err := json.Marshal(dist.args)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), plain)
	assert.NotContains(t, string(raw), secret)
	assert.Contains(t, string(raw), `"EnvRevision":5`)
	require.Len(t, vars.applied, 1)
	assert.True(t, vars.applied[0].ReplaceEnv, "env_vars remplace les variables, pas les secrets")
	assert.Equal(t, map[string]string{"B": secret}, vars.applied[0].Secrets)
	var spec domain.CreateWorkloadRequest
	require.NoError(t, json.Unmarshal(repo.spec, &spec))
	assert.Nil(t, spec.SecretData)
}

// Sans env_vars : les variables en place ne sont pas touchées.
func TestUpdate_KeepsVariablesWhenAbsent(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1", Replicas: 1, TargetPort: 8080, EnvRevision: 2}}
	vars := &fakeVariables{revision: 2}
	s := &WorkloadService{Repo: repo, Distributor: &fakeDistributor{}, Variables: vars}
	require.NoError(t, s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"}))
	assert.False(t, vars.applied[0].ReplaceEnv)
	assert.EqualValues(t, 2, vars.revision)
}

// Un déploiement en cours : refus clair, rien n'est modifié (Temporal aurait
// rattaché la demande à l'exécution en cours sans rien dire).
func TestUpdate_RefusedWhileDeploying(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1"}}
	dist := &fakeDistributor{running: map[string]bool{workflows.WorkflowIDUpdatePrefix + id.String(): true}}
	vars := &fakeVariables{}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: vars}

	err := s.Update(context.Background(), id.String(), domain.UpdateWorkloadRequest{Image: "nginx:2"})
	assert.ErrorIs(t, err, domain.ErrDeployInProgress)
	assert.Empty(t, dist.started)
	assert.Nil(t, repo.spec)

	_, err = s.PatchVariables(context.Background(), id, domain.PatchVariablesRequest{Secrets: map[string]string{"A": "b"}})
	assert.ErrorIs(t, err, domain.ErrDeployInProgress)
	assert.Empty(t, vars.applied)
}

func TestPatchVariables(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"
	id := uuid.New()
	mk := func(status string) (*WorkloadService, *fakeDistributor, *fakeVariables) {
		repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1", Status: status, EnvRevision: 1}}
		d, v := &fakeDistributor{}, &fakeVariables{revision: 1}
		return &WorkloadService{Repo: repo, Distributor: d, Variables: v}, d, v
	}

	s, d, _ := mk("RUNNING")
	res, err := s.PatchVariables(context.Background(), id, domain.PatchVariablesRequest{Secrets: map[string]string{"A": "b"}})
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.Len(t, d.started, 1, "une valeur change : redéploiement")

	s, d, _ = mk("RUNNING")
	res, err = s.PatchVariables(context.Background(), id, domain.PatchVariablesRequest{})
	require.NoError(t, err)
	assert.False(t, res.Changed)
	assert.Empty(t, d.started, "rien ne change : pas de redéploiement")

	s, d, v := mk("STOPPED")
	_, err = s.PatchVariables(context.Background(), id, domain.PatchVariablesRequest{Env: map[string]string{"A": "b"}})
	assert.ErrorIs(t, err, domain.ErrWorkloadStopped)
	assert.Empty(t, d.started)
	assert.Empty(t, v.applied)

	s, _, _ = mk("RUNNING")
	_, err = s.PatchVariables(context.Background(), id, domain.PatchVariablesRequest{Env: map[string]string{"1 mauvais": "b"}})
	assert.ErrorIs(t, err, domain.ErrVariableName)
}

// createService : projet de 1000m / 1024Mi, sans rien d'autre en cours.
func createService(t *testing.T) (*WorkloadService, *fakeRepo, *fakeDistributor) {
	t.Helper()
	repo := &fakeRepo{}
	dist := &fakeDistributor{}
	return &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{},
		ProjectRepo: fakeProjects{p: &projectDomain.Project{Name: "demo", CpuLimit: "1000m", MemoryLimit: "1024Mi", StorageLimit: "10Gi"}}}, repo, dist
}

func createRequest(mutate func(*domain.CreateWorkloadRequest)) *WorkloadServiceRequest {
	req := domain.CreateWorkloadRequest{ProjectID: uuid.NewString(), Name: "web", Image: "nginx:1", TargetPort: 8080, Replicas: 1}
	if mutate != nil {
		mutate(&req)
	}
	return &WorkloadServiceRequest{CreateWorkloadRequest: req}
}

// Sans limites : une seule valeur par défaut, vue par le quota, la base et Helm
// (avant : 0 au contrôle, 200m en base, 500m dans Helm).
func TestCreate_DefaultLimitsAreCounted(t *testing.T) {
	s, _, dist := createService(t)
	w, err := s.Deploy(context.Background(), createRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "500m", w.CPULimit)
	assert.Equal(t, "512Mi", w.MemoryLimit)
	in := dist.args[0].(workflows.DeployWorkloadInput)
	assert.Equal(t, "500m", in.CPULimit)
	assert.Equal(t, "512Mi", in.MemoryLimit)
	var spec domain.CreateWorkloadRequest
	require.NoError(t, json.Unmarshal(w.Spec, &spec))
	assert.Equal(t, "500m", spec.CPULimit, "spec des déploiements suivants")

	// Plus assez de place pour la valeur par défaut : refusé.
	s, repo, _ := createService(t)
	repo.usedCPU = 600
	_, err = s.Deploy(context.Background(), createRequest(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota")
}

func TestCreate_InvalidLimitsRefused(t *testing.T) {
	cases := map[string]func(*domain.CreateWorkloadRequest){
		"cpu illisible":     func(r *domain.CreateWorkloadRequest) { r.CPULimit = "beaucoup" },
		"cpu nul":           func(r *domain.CreateWorkloadRequest) { r.CPULimit = "0" },
		"cpu négatif":       func(r *domain.CreateWorkloadRequest) { r.CPULimit = "-1" },
		"mémoire illisible": func(r *domain.CreateWorkloadRequest) { r.MemoryLimit = "beaucoup" },
		"disque illisible":  func(r *domain.CreateWorkloadRequest) { r.PersistenceEnabled, r.StorageSize = true, "beaucoup" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, dist := createService(t)
			_, err := s.Deploy(context.Background(), createRequest(mutate))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid")
			assert.Empty(t, dist.started)
		})
	}
}

// Disque sans taille : compté au quota comme n'importe quel autre.
func TestCreate_DefaultStorageCounted(t *testing.T) {
	s, repo, _ := createService(t)
	repo.usedStorage = 10 * 1024
	_, err := s.Deploy(context.Background(), createRequest(func(r *domain.CreateWorkloadRequest) { r.PersistenceEnabled = true }))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Storage")
}

// Panne de la lecture de l'usage : erreur, pas un quota vu vide.
func TestCreate_UsageErrorRefused(t *testing.T) {
	s, repo, dist := createService(t)
	down := errors.New("db down")
	repo.usageErr = down
	_, err := s.Deploy(context.Background(), createRequest(nil))
	require.ErrorIs(t, err, down)
	assert.Empty(t, dist.started)
	assert.Zero(t, repo.created)
}

// Conteneur antérieur, spec sans limites : Helm reçoit celles de la base.
func TestDeployInput_FallsBackToSavedLimits(t *testing.T) {
	s := &WorkloadService{}
	in := s.deployInput(&domain.Workload{ID: uuid.New(), ProjectID: uuid.New(), Name: "web", CPULimit: "200m", MemoryLimit: "256Mi"}, domain.CreateWorkloadRequest{})
	assert.Equal(t, "200m", in.CPULimit)
	assert.Equal(t, "256Mi", in.MemoryLimit)
}

// Workflow non lancé à la création : la ligne ne garde ni le nom ni le quota,
// même si la requête est déjà interrompue.
func TestDeploy_StartFailureRemovesRow(t *testing.T) {
	s, repo, dist := createService(t)
	down := errors.New("temporal down")
	dist.startErr = down
	ctx, cancel := context.WithCancel(context.Background())
	repo.onCreate = cancel
	defer cancel()
	w, err := s.Deploy(ctx, createRequest(nil))
	require.ErrorIs(t, err, down)
	assert.Nil(t, w)
	assert.Equal(t, 1, repo.deleted)
}

// fakeK8s : Deployment renvoyé par une API Kubernetes de test.
func fakeK8s(t *testing.T, status int, body string) *kubernetes.Clientset {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	return c
}

func deploymentAtRevision(revision string) string {
	return `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"web"},"spec":{"template":{"metadata":{"annotations":{"checksum/env":"` + revision + `"}}}}}`
}

// Mêmes valeurs renvoyées alors que la révision n'a jamais été déployée (échec
// au lancement) : le redéploiement a lieu. Sinon, rien à faire.
func TestPatchVariables_RedeploysWhenRevisionBehind(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	configs.AppConfig.Vps.AdressIp = "5.196.223.196"
	notFound := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
	cases := []struct {
		name    string
		status  int
		body    string
		restart bool
	}{
		{"révision en retard", http.StatusOK, deploymentAtRevision("1"), true},
		{"jamais installé", http.StatusNotFound, notFound, true},
		{"à jour", http.StatusOK, deploymentAtRevision("2"), false},
		{"cluster injoignable", http.StatusInternalServerError, `{}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := uuid.New()
			repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "web", Namespace: "km-x", Image: "nginx:1", Status: "RUNNING", EnvRevision: 2}}
			dist := &fakeDistributor{}
			s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &noChangeVariables{fakeVariables{revision: 2}}, K8sClient: fakeK8s(t, c.status, c.body)}
			res, err := s.PatchVariables(context.Background(), id, domain.PatchVariablesRequest{Env: map[string]string{"A": "b"}})
			require.NoError(t, err)
			assert.False(t, res.Changed)
			if c.restart {
				assert.Len(t, dist.started, 1)
			} else {
				assert.Empty(t, dist.started)
			}
		})
	}
}

// noChangeVariables : les valeurs reçues sont déjà celles en base.
type noChangeVariables struct{ fakeVariables }

func (f *noChangeVariables) Apply(context.Context, uuid.UUID, domain.VariableChange) (int64, bool, error) {
	return f.revision, false, nil
}
