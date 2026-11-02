// Package helm : accès aux releases Helm du cluster de la plateforme.
package helm

import (
	"errors"

	"helm.sh/helm/v3/pkg/action"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	// storageDriver : état des releases dans des Secrets Kubernetes.
	storageDriver = "secret"
	// discoveryBurst : la découverte de l'API enchaîne les requêtes.
	discoveryBurst = 100
)

// ErrNoCluster : aucune configuration Kubernetes n'est chargée.
var ErrNoCluster = errors.New("no kubernetes configuration available for helm")

// NewActionConfig : configuration Helm construite depuis le rest.Config de la
// plateforme (kubernetes.kube_config_path ou in-cluster), le même que client-go.
// Le chargement par défaut de Helm (cli.New) lit KUBECONFIG ou ~/.kube/config :
// sur un serveur séparé du cluster, il échouait ou visait un autre cluster.
func NewActionConfig(cfg *rest.Config, namespace string, log action.DebugLog) (*action.Configuration, error) {
	if cfg == nil {
		return nil, ErrNoCluster
	}
	out := new(action.Configuration)
	if err := out.Init(&restClientGetter{cfg: cfg, namespace: namespace}, namespace, storageDriver, log); err != nil {
		return nil, err
	}
	return out, nil
}

// restClientGetter : genericclioptions.RESTClientGetter sans aucun fichier.
type restClientGetter struct {
	cfg       *rest.Config
	namespace string
}

func (g *restClientGetter) ToRESTConfig() (*rest.Config, error) {
	return rest.CopyConfig(g.cfg), nil
}

func (g *restClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	cfg := rest.CopyConfig(g.cfg)
	cfg.Burst = discoveryBurst
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(dc), nil
}

func (g *restClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	dc, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(dc)
	return restmapper.NewShortcutExpander(mapper, dc, nil), nil
}

func (g *restClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return &staticClientConfig{cfg: g.cfg, namespace: g.namespace}
}

// staticClientConfig : clientcmd.ClientConfig figé sur le rest.Config. Helm n'en
// lit que l'espace de noms et la configuration ; RawConfig reste vide.
type staticClientConfig struct {
	cfg       *rest.Config
	namespace string
}

func (c *staticClientConfig) RawConfig() (clientcmdapi.Config, error) {
	return clientcmdapi.Config{}, nil
}

func (c *staticClientConfig) ClientConfig() (*rest.Config, error) {
	return rest.CopyConfig(c.cfg), nil
}

func (c *staticClientConfig) Namespace() (string, bool, error) {
	return c.namespace, c.namespace != "", nil
}

func (c *staticClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return clientcmd.NewDefaultClientConfigLoadingRules()
}
