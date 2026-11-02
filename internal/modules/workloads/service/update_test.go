package service

import (
	"context"
	"encoding/json"
	projectDomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	projectRepo "github.com/thekrauss/kubemanager/internal/modules/projects/repository"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	started    []string
	running    map[string]bool
}

func (f *fakeDistributor) IsRunning(_ context.Context, workflowID string) (bool, error) {
	return f.running[workflowID], nil
}

func (f *fakeDistributor) TerminateWorkflow(_ context.Context, workflowID, _, _ string) error {
	f.terminated = append(f.terminated, workflowID)
	return nil
}

func (f *fakeDistributor) ExecuteWorkflow(_ context.Context, opts temporal.ExecuteOptions, _ any, args ...any) (temporal.WorkflowHandle, error) {
	f.args = args
	f.started = append(f.started, opts.WorkflowID)
	return temporal.WorkflowHandle{}, nil
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
}

func (f *fakeRepo) UpdateStatus(context.Context, uuid.UUID, string, string) error { return nil }

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
	return f.usedCPU, f.usedMem, f.usedStorage, nil
}
func (f *fakeRepo) UpdateSpec(_ context.Context, _ uuid.UUID, image, disk, url string, spec []byte) error {
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

// Suppression : les déploiements en cours sont arrêtés avant la
// désinstallation (sinon réinstallés par leurs nouvelles tentatives).
func TestDelete_StopsRunningDeployFirst(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{w: domain.Workload{ID: id, ProjectID: uuid.New(), Name: "crash", Namespace: "km-x"}}
	dist := &fakeDistributor{}
	s := &WorkloadService{Repo: repo, Distributor: dist, Variables: &fakeVariables{}}
	require.NoError(t, s.Delete(context.Background(), id))
	assert.Equal(t, []string{"workload-deploy-" + id.String(), "workload-update-" + id.String()}, dist.terminated)
	assert.Equal(t, []string{"workload-delete-" + id.String()}, dist.started, "suppression lancée ensuite")
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
