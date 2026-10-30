package activities

import (
	"context"
	"errors"
	"fmt"
	"github.com/thekrauss/kubemanager/internal/infrastructure/helm/charts"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/storage/driver"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	idempotency "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
)

// WorkloadActivities : activities Temporal pour déployer des workloads via Helm.
// Idempotency (optionnel) déduplique InstallChart / EnsureSecret sur retry.
type WorkloadActivities struct {
	K8sConfig   *rest.Config
	K8sClient   *kubernetes.Clientset
	Idempotency idempotency.AtomicManager
}

// WorkloadIdentity : le workload s'exécute comme un compte de service ; ses
// pods reçoivent un jeton Kubernetes projeté (audience = STS) à échanger.
type WorkloadIdentity struct {
	ServiceAccount string // "serviceAccount:<uuid>"
	Audience       string // émetteur du STS
	TokenURI       string
}

type InstallWorkloadInput struct {
	Identity *WorkloadIdentity

	ProjectID    string
	WorkloadID   string
	ReleaseName  string
	Namespace    string
	ImageRepo    string
	ImageTag     string
	ExternalURL  string
	IngressClass string // vide : nginx
	// IngressNamespace : espace du contrôleur d'ingress (vide : ingress-nginx).
	IngressNamespace string
	ServiceType      string // "ClusterIP" ou "LoadBalancer"

	Env     map[string]string
	Secrets map[string]string

	PersistenceEnabled bool
	StorageSize        string
	StorageClass       string
	MountPath          string

	Replicas   int
	TargetPort int

	// Resources
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string

	// Security overrides (0 = chart defaults 1000).
	RunAsUser  int64
	RunAsGroup int64

	// ReadOnlyRootFS : opt-in. Si true, tmpfs montés sur /tmp et /var/run.
	ReadOnlyRootFS bool

	// Autoscaling
	AutoscalingEnabled      bool
	AutoscalingMin          int32
	AutoscalingMax          int32
	AutoscalingTargetCPU    int32
	AutoscalingTargetMemory int32

	// Disruption budget
	PDBEnabled bool

	// TLS
	TLSEnabled bool

	// Probes
	HealthPath    string
	ReadinessPath string
}

func (a *WorkloadActivities) InstallChart(ctx context.Context, input InstallWorkloadInput) error {
	_, _, err := idempotency.ExecuteActivity(ctx, a.Idempotency, input,
		idempotency.ActivityOptions{ProcessingTTL: 10 * time.Minute},
		func() (struct{}, error) {
			settings := cli.New()

			actionConfig := new(action.Configuration)
			if err := actionConfig.Init(settings.RESTClientGetter(), input.Namespace, "secret", func(format string, v ...interface{}) {
				fmt.Printf(format, v...)
			}); err != nil {
				return struct{}{}, err
			}

			// Chart embarqué dans le binaire (internal/infrastructure/helm/charts).
			chart, err := charts.StandardApp()
			if err != nil {
				return struct{}{}, fmt.Errorf("failed to load chart: %w", err)
			}
			vals := buildHelmValues(input)

			if err := upgradeOrInstall(actionConfig, input.Namespace, input.ReleaseName, chart, vals); err != nil {
				return struct{}{}, fmt.Errorf("helm release failed: %w", err)
			}
			return struct{}{}, nil
		},
	)
	return err
}

// upgradeOrInstall : équivalent de `helm upgrade --install`. Dans le SDK,
// Upgrade.Install n'a aucun effet (la logique est dans la commande helm) :
// sans ce choix explicite, le premier déploiement d'un conteneur échouait
// toujours (« has no deployed releases »).
func upgradeOrInstall(cfg *action.Configuration, namespace, name string, ch *chart.Chart, vals map[string]any) error {
	history := action.NewHistory(cfg)
	history.Max = 1
	releases, err := history.Run(name)
	switch {
	case errors.Is(err, driver.ErrReleaseNotFound) || (err == nil && len(releases) == 0):
		return install(cfg, namespace, name, ch, vals, false)
	case err != nil:
		return err
	}
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = namespace
	upgrade.Wait = true
	upgrade.Timeout = 5 * time.Minute
	_, err = upgrade.Run(name, ch, vals)
	if err != nil && strings.Contains(err.Error(), "has no deployed releases") {
		// Seulement des tentatives en échec : on réinstalle par-dessus.
		return install(cfg, namespace, name, ch, vals, true)
	}
	return err
}

func install(cfg *action.Configuration, namespace, name string, ch *chart.Chart, vals map[string]any, replace bool) error {
	in := action.NewInstall(cfg)
	in.Namespace = namespace
	in.ReleaseName = name
	in.Wait = true
	in.Timeout = 5 * time.Minute
	in.Replace = replace
	_, err := in.Run(ch, vals)
	return err
}

// UninstallChart : helm uninstall de la release (Saga Delete).
// Idempotent : si la release n'existe pas, on retourne nil (déjà uninstall).
func (a *WorkloadActivities) UninstallChart(ctx context.Context, namespace, releaseName string) error {
	payload := struct{ NS, Release string }{namespace, releaseName}
	_, _, err := idempotency.ExecuteActivity(ctx, a.Idempotency, payload,
		idempotency.ActivityOptions{ProcessingTTL: 5 * time.Minute},
		func() (struct{}, error) {
			settings := cli.New()
			actionConfig := new(action.Configuration)
			if err := actionConfig.Init(settings.RESTClientGetter(), namespace, "secret", func(format string, v ...interface{}) {
				fmt.Printf(format, v...)
			}); err != nil {
				return struct{}{}, err
			}
			u := action.NewUninstall(actionConfig)
			u.Wait = true
			u.Timeout = 3 * time.Minute
			if _, err := u.Run(releaseName); err != nil {
				// "release: not found" → considéré idempotent (déjà supprimée).
				if strings.Contains(err.Error(), "not found") {
					return struct{}{}, nil
				}
				return struct{}{}, fmt.Errorf("helm uninstall: %w", err)
			}
			return struct{}{}, nil
		},
	)
	return err
}

// buildHelmValues construit l'arbre `values` envoyé à Helm upgrade.
// Centralisé ici pour que InstallChart reste lisible + pour pouvoir tester
// le mapping Go → Helm values (unit-testable sans K8s).
//
// Règles de mapping :
//   - commonLabels.projectID/workloadID → labels K8s km-project-id/km-workload-id
//   - RunAsUser/Group = 0 → laisse les defaults du chart (1000)
//   - Autoscaling désactivé → HPA pas déployé, replicas fixe dans Deployment
//   - PDB activé uniquement si replicas >= 2 (sinon chart no-op)
func buildHelmValues(input InstallWorkloadInput) map[string]any {
	// Resources avec fallbacks sur les defaults restrictifs.
	resources := map[string]any{
		"requests": map[string]any{
			"cpu":    fallback(input.CPURequest, "100m"),
			"memory": fallback(input.MemoryRequest, "128Mi"),
		},
		"limits": map[string]any{
			"cpu":    fallback(input.CPULimit, "500m"),
			"memory": fallback(input.MemoryLimit, "512Mi"),
		},
	}

	// podSecurityContext : override RunAsUser/Group si fournis (images non-1000).
	podSec := map[string]any{
		"runAsNonRoot": true,
		"seccompProfile": map[string]any{
			"type": "RuntimeDefault",
		},
	}
	containerSec := map[string]any{
		"allowPrivilegeEscalation": false,
		"readOnlyRootFilesystem":   input.ReadOnlyRootFS,
		"runAsNonRoot":             true,
		"capabilities": map[string]any{
			"drop": []string{"ALL"},
		},
	}
	if input.RunAsUser > 0 {
		podSec["runAsUser"] = input.RunAsUser
		containerSec["runAsUser"] = input.RunAsUser
	}
	if input.RunAsGroup > 0 {
		podSec["runAsGroup"] = input.RunAsGroup
		podSec["fsGroup"] = input.RunAsGroup
	}

	// Probes : HealthPath sert pour liveness + startup par défaut.
	healthPath := fallback(input.HealthPath, "/")
	readinessPath := fallback(input.ReadinessPath, healthPath)

	vals := map[string]any{
		"replicaCount": input.Replicas,
		"commonLabels": map[string]any{
			"projectID":  input.ProjectID,
			"workloadID": input.WorkloadID,
		},
		"image": map[string]any{
			"repository": input.ImageRepo,
			"tag":        input.ImageTag,
		},
		"service": map[string]any{
			"type":       fallback(input.ServiceType, "ClusterIP"),
			"port":       80,
			"targetPort": input.TargetPort,
			"protocol":   "TCP",
		},
		"ingress": map[string]any{
			"enabled":   input.ExternalURL != "", // sans URL publique : pas d'ingress
			"className": fallback(input.IngressClass, "nginx"),
			"host":      input.ExternalURL,
			"tls": map[string]any{
				"enabled":       input.TLSEnabled,
				"clusterIssuer": "letsencrypt-prod",
			},
		},
		"resources":                resources,
		"podSecurityContext":       podSec,
		"containerSecurityContext": containerSec,
		"serviceAccount": map[string]any{
			"create":                       true,
			"automountServiceAccountToken": false,
			// Nom fixe : le STS retrouve le workload par
			// system:serviceaccount:<namespace>:<release>.
			"name": input.ReleaseName,
		},
		"betorunIdentity": identityValues(input.Identity),
		"persistence": map[string]any{
			"enabled":      input.PersistenceEnabled,
			"size":         input.StorageSize,
			"storageClass": input.StorageClass,
			"mountPath":    input.MountPath,
		},
		"autoscaling": map[string]any{
			"enabled":                           input.AutoscalingEnabled,
			"minReplicas":                       fallbackInt32(input.AutoscalingMin, 1),
			"maxReplicas":                       fallbackInt32(input.AutoscalingMax, 10),
			"targetCPUUtilizationPercentage":    fallbackInt32(input.AutoscalingTargetCPU, 75),
			"targetMemoryUtilizationPercentage": input.AutoscalingTargetMemory,
		},
		"pdb": map[string]any{
			"enabled":      input.PDBEnabled,
			"minAvailable": "50%",
		},
		"probes": map[string]any{
			"startup": map[string]any{
				"enabled":          true,
				"httpGet":          map[string]any{"path": healthPath, "port": "http"},
				"periodSeconds":    5,
				"failureThreshold": 30,
			},
			"readiness": map[string]any{
				"enabled":             true,
				"httpGet":             map[string]any{"path": readinessPath, "port": "http"},
				"initialDelaySeconds": 5,
				"periodSeconds":       10,
				"timeoutSeconds":      3,
				"failureThreshold":    3,
			},
			"liveness": map[string]any{
				"enabled":             true,
				"httpGet":             map[string]any{"path": healthPath, "port": "http"},
				"initialDelaySeconds": 30,
				"periodSeconds":       20,
				"timeoutSeconds":      5,
				"failureThreshold":    3,
			},
		},
		"networkPolicy": map[string]any{
			"enabled":             true,
			"ingressNSLabelKey":   "kubernetes.io/metadata.name",
			"ingressNSLabelValue": fallback(input.IngressNamespace, "ingress-nginx"),
			"egress": map[string]any{
				"enabled":     true,
				"allowDNS":    true,
				"publicPorts": defaultEgressPublicPorts,
			},
		},
		// tmpfs uniquement si rootfs read-only : sinon un emptyDir sur /var/run
		// masquerait le contenu de l'image sans rien apporter.
		"tmpfs": map[string]any{
			"enabled": input.ReadOnlyRootFS,
			"mounts": []map[string]any{
				{"name": "tmp", "path": "/tmp", "sizeLimit": "64Mi"},
				{"name": "run", "path": "/var/run", "sizeLimit": "16Mi"},
			},
		},
		"envSecretName": input.ReleaseName + "-env",
	}

	if len(input.Env) > 0 {
		vals["envVars"] = input.Env
	}

	return vals
}

// defaultEgressPublicPorts : ports TCP sortants autorisés vers Internet
// (subnets privés exclus par le chart). Couvre HTTP(S), SMTP submission et
// les bases managées courantes. Le port 25 reste fermé (anti-spam).
var defaultEgressPublicPorts = []int{80, 443, 465, 587, 3306, 5432, 6379, 27017}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func fallbackInt32(v, def int32) int32 {
	if v == 0 {
		return def
	}
	return v
}

func (a *WorkloadActivities) EnsureSecret(ctx context.Context, nsNam, releaseName, projectID string, data map[string]string) error {
	payload := struct {
		NS, Release, ProjectID string
		Data                   map[string]string
	}{nsNam, releaseName, projectID, data}

	_, _, err := idempotency.ExecuteActivity(ctx, a.Idempotency, payload,
		idempotency.ActivityOptions{ProcessingTTL: 2 * time.Minute},
		func() (struct{}, error) {
			secretName := releaseName + "-env"
			_, err := a.K8sClient.CoreV1().Secrets(nsNam).Get(ctx, secretName, metav1.GetOptions{})

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: nsNam,
					Labels:    map[string]string{"km-project-id": projectID},
				},
				StringData: data,
			}
			if err != nil {
				_, err = a.K8sClient.CoreV1().Secrets(nsNam).Create(ctx, secret, metav1.CreateOptions{})
			} else {
				_, err = a.K8sClient.CoreV1().Secrets(nsNam).Update(ctx, secret, metav1.UpdateOptions{})
			}
			return struct{}{}, err
		},
	)
	return err
}

func identityValues(id *WorkloadIdentity) map[string]any {
	if id == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{"enabled": true, "serviceAccount": id.ServiceAccount, "audience": id.Audience,
		"tokenURI": id.TokenURI}
}
