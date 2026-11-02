package activities

import (
	_ "embed"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	labelBuildID = "betorun.dev/build-id"
	labelProject = "betorun.dev/project-id"
	labelRole    = "betorun.dev/role"
	roleBuilder  = "builder"

	containerName = "build"
	stateVolume   = "state"
	authVolume    = "registry-auth"
	gitTokenKey   = "token"
	statePath     = "/home/user/.local/share/buildkit"
	authPath      = "/home/user/.docker"
	builderUID    = int64(1000)

	jobTTLSeconds  = int32(600)
	terminationLog = "/dev/termination-log"
	policyName     = "builders"
)

// Script du Job : récupère la source, utilise son Dockerfile ou en génère un
// (detect.sh), construit et publie l'image. Les valeurs choisies par
// l'utilisateur arrivent par des variables d'environnement, jamais interpolées
// dans le script. La couche de cache est propre au dépôt de l'image : un Job
// neuf ne repart pas de zéro.
//
//go:embed detect.sh
var detectScript string

//go:embed build.sh
var mainScript string

var buildScript = detectScript + "\n" + mainScript

// JobConfig : réglages des Jobs de build.
type JobConfig struct {
	Namespace      string
	Image          string
	RegistryOpts   string // ",registry.insecure=true" en dev
	CPURequest     string
	CPULimit       string
	MemoryRequest  string
	MemoryLimit    string
	DiskLimit      string
	DeadlineSecond int64
	RuntimeClass   string
	NodeSelector   map[string]string
	EgressAllow    []string
}

// JobSpec : ce qu'un build doit construire.
type JobSpec struct {
	BuildID, ProjectID string
	// SourceURL (archive signée) ou GitURL.
	SourceURL, GitURL, GitRef, ContextDir string
	Dockerfile                            string
	Image                                 string
	CacheRef                              string
	// ImportCache : non vide si un build précédent a publié un cache.
	ImportCache string
	AuthSecret  string
	// GitCommit : commit exact à récupérer (voie GitHub), à la place du bout de branche.
	GitCommit string
	// GitTokenSecret : Secret du jeton de clonage (dépôt privé), jamais sa valeur.
	GitTokenSecret string
}

func jobName(buildID string) string    { return "build-" + buildID }
func secretName(buildID string) string { return "build-auth-" + buildID }

// gitSecretName : Secret du jeton de clonage d'un dépôt privé.
func gitSecretName(buildID string) string { return "build-git-" + buildID }

// newJob : pod jetable, sans jeton Kubernetes ni variables de service, avec
// des limites strictes. BuildKit sans root a besoin de seccomp/AppArmor non
// confinés (montages dans son espace utilisateur) : l'isolation vient de
// l'utilisateur non privilégié, du réseau (newNetworkPolicy) et, en
// production, d'un runtime dédié (RuntimeClass, nœuds séparés).
func newJob(c JobConfig, s JobSpec) *batchv1.Job {
	labels := map[string]string{labelRole: roleBuilder, labelBuildID: s.BuildID, labelProject: s.ProjectID}
	disk := resource.MustParse(c.DiskLimit)
	mode := int32(0o440)
	backoff, ttl, deadline := int32(0), jobTTLSeconds, c.DeadlineSecond
	falseVal, trueVal := false, true
	uid := builderUID

	pod := corev1.PodSpec{
		RestartPolicy:                 corev1.RestartPolicyNever,
		AutomountServiceAccountToken:  &falseVal,
		EnableServiceLinks:            &falseVal,
		TerminationGracePeriodSeconds: ptr(int64(10)),
		NodeSelector:                  c.NodeSelector,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, RunAsNonRoot: &trueVal,
			SeccompProfile:  &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined},
			AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined},
		},
		Containers: []corev1.Container{{
			Name:    containerName,
			Image:   c.Image,
			Command: []string{"sh", "-c", buildScript},
			Env: []corev1.EnvVar{
				{Name: "BUILDKITD_FLAGS", Value: "--oci-worker-no-process-sandbox"},
				{Name: "DOCKER_CONFIG", Value: authPath},
				{Name: "BUILD_SOURCE_URL", Value: s.SourceURL},
				{Name: "BUILD_GIT_URL", Value: s.GitURL},
				{Name: "BUILD_GIT_REF", Value: s.GitRef},
				{Name: "BUILD_CONTEXT_DIR", Value: s.ContextDir},
				{Name: "BUILD_DOCKERFILE", Value: s.Dockerfile},
				{Name: "BUILD_IMAGE", Value: s.Image},
				{Name: "BUILD_CACHE", Value: s.CacheRef},
				{Name: "BUILD_IMPORT_CACHE", Value: s.ImportCache},
				{Name: "BUILD_REGISTRY_OPTS", Value: c.RegistryOpts},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(c.CPURequest),
					corev1.ResourceMemory: resource.MustParse(c.MemoryRequest),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse(c.CPULimit),
					corev1.ResourceMemory:           resource.MustParse(c.MemoryLimit),
					corev1.ResourceEphemeralStorage: disk,
				},
			},
			TerminationMessagePath:   terminationLog,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			VolumeMounts: []corev1.VolumeMount{
				{Name: stateVolume, MountPath: statePath},
				{Name: authVolume, MountPath: authPath, ReadOnly: true},
			},
		}},
		Volumes: []corev1.Volume{
			{Name: stateVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &disk}}},
			{Name: authVolume, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: s.AuthSecret, DefaultMode: &mode,
				Items: []corev1.KeyToPath{{Key: corev1.DockerConfigJsonKey, Path: "config.json"}},
			}}},
		},
	}
	pod.Containers[0].Env = append(pod.Containers[0].Env, corev1.EnvVar{Name: "BUILD_GIT_COMMIT", Value: s.GitCommit})
	if s.GitTokenSecret != "" {
		// Le jeton reste dans le Secret : la spec du Job n'en porte que la référence.
		pod.Containers[0].Env = append(pod.Containers[0].Env, corev1.EnvVar{Name: "BUILD_GIT_TOKEN", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: s.GitTokenSecret}, Key: gitTokenKey},
		}})
	}
	if c.RuntimeClass != "" {
		pod.RuntimeClassName = &c.RuntimeClass
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName(s.BuildID), Namespace: c.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template:                corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: pod},
		},
	}
}

// privateRanges : adresses qu'un build ne doit jamais joindre (cluster, nœuds,
// réseau interne, métadonnées du cloud).
var privateRanges = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8",
}

// newNetworkPolicy : un build atteint Internet (images de base, paquets, Git)
// et le DNS, rien d'interne hormis les adresses explicitement autorisées
// (registre et API quand ils sont sur un réseau privé).
func newNetworkPolicy(c JobConfig) *networkingv1.NetworkPolicy {
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	dns := intstr.FromInt32(53)
	egress := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
			}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
		},
		{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: privateRanges}}}},
	}
	for _, cidr := range c.EgressAllow {
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: cidr}}},
		})
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: c.Namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{labelRole: roleBuilder}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}

func ptr[T any](v T) *T { return &v }
