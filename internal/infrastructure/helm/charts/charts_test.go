package charts

import (
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

func TestStandardApp_Loads(t *testing.T) {
	c, err := StandardApp()
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "standard-app" || len(c.Templates) == 0 {
		t.Fatalf("chart incomplet : %s, %d templates", c.Name(), len(c.Templates))
	}
}

// render : rendu du chart embarqué, avec les valeurs posées par l'API.
func render(t *testing.T, values map[string]any) map[string]string {
	t.Helper()
	c, err := StandardApp()
	if err != nil {
		t.Fatal(err)
	}
	return renderChart(t, c, values)
}

func renderChart(t *testing.T, c *chart.Chart, values map[string]any) map[string]string {
	t.Helper()
	vals, err := chartutil.ToRenderValues(c, values, chartutil.ReleaseOptions{Name: "web", Namespace: "proj-1", IsInstall: true}, chartutil.DefaultCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	out, err := engine.Render(c, vals)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func manifest(t *testing.T, out map[string]string, name string) string {
	t.Helper()
	body, ok := out["standard-app/templates/"+name]
	if !ok {
		t.Fatalf("template %s absent", name)
	}
	return body
}

func ingressValues(class string, tls bool) map[string]any {
	return map[string]any{"ingress": map[string]any{
		"enabled": true, "className": class, "host": "web.example.org",
		"tls": map[string]any{"enabled": tls, "clusterIssuer": "letsencrypt-prod"},
	}}
}

const (
	redirectRef = "proj-1-web-https-redirect@kubernetescrd"
	bodyRef     = "proj-1-web-body-limit@kubernetescrd"
)

func TestIngress_Traefik_TLS(t *testing.T) {
	out := render(t, ingressValues("traefik", true))
	ing := manifest(t, out, "ingress.yaml")
	want := `traefik.ingress.kubernetes.io/router.middlewares: "` + redirectRef + `"`
	if !strings.Contains(ing, want) {
		t.Fatalf("annotation absente :\n%s", ing)
	}
	if strings.Contains(ing, "nginx.ingress") {
		t.Fatalf("annotation nginx sous traefik :\n%s", ing)
	}
	if !strings.Contains(ing, "ingressClassName: traefik") || !strings.Contains(ing, "cert-manager.io/cluster-issuer") {
		t.Fatalf("classe ou émetteur absents :\n%s", ing)
	}
	// router.tls=true rendrait le port 80 muet : plus de redirection.
	if strings.Contains(ing, "router.tls") {
		t.Fatalf("router.tls posé :\n%s", ing)
	}
	mw := manifest(t, out, "traefik-middlewares.yaml")
	for _, s := range []string{"apiVersion: traefik.io/v1alpha1", "name: web-https-redirect", "scheme: https"} {
		if !strings.Contains(mw, s) {
			t.Errorf("%q absent :\n%s", s, mw)
		}
	}
	// Le middleware buffering retient les réponses : jamais par défaut.
	if strings.Contains(mw, "buffering") || strings.Contains(ing, bodyRef) {
		t.Fatalf("plafond actif par défaut :\n%s\n%s", ing, mw)
	}
}

func TestIngress_Traefik_NoTLS(t *testing.T) {
	out := render(t, ingressValues("traefik", false))
	ing := manifest(t, out, "ingress.yaml")
	if strings.Contains(ing, "router.middlewares") {
		t.Fatalf("sans TLS ni plafond, aucun middleware :\n%s", ing)
	}
	if mw := strings.TrimSpace(manifest(t, out, "traefik-middlewares.yaml")); mw != "" {
		t.Fatalf("middleware rendu :\n%s", mw)
	}
}

func TestIngress_Traefik_BodyLimitOnDemand(t *testing.T) {
	values := ingressValues("traefik", true)
	values["ingress"].(map[string]any)["traefik"] = map[string]any{"maxBodyBytes": 52428800}
	out := render(t, values)
	want := `"` + redirectRef + `,` + bodyRef + `"`
	if ing := manifest(t, out, "ingress.yaml"); !strings.Contains(ing, want) {
		t.Fatalf("plafond demandé :\n%s", ing)
	}
	if mw := manifest(t, out, "traefik-middlewares.yaml"); !strings.Contains(mw, "maxRequestBodyBytes: 52428800") {
		t.Fatalf("middleware de plafond absent :\n%s", mw)
	}
}

// Comportement historique inchangé sous nginx, sans CRD Traefik.
func TestIngress_Nginx(t *testing.T) {
	out := render(t, ingressValues("nginx", true))
	ing := manifest(t, out, "ingress.yaml")
	for _, s := range []string{`nginx.ingress.kubernetes.io/proxy-body-size: "50m"`, `nginx.ingress.kubernetes.io/ssl-redirect: "true"`, "ingressClassName: nginx"} {
		if !strings.Contains(ing, s) {
			t.Errorf("%q absent :\n%s", s, ing)
		}
	}
	if strings.Contains(ing, "traefik") {
		t.Fatalf("traefik sous nginx :\n%s", ing)
	}
	if mw := strings.TrimSpace(manifest(t, out, "traefik-middlewares.yaml")); mw != "" {
		t.Fatalf("CRD Traefik rendue sous nginx :\n%s", mw)
	}
}

func TestCustomDomains_Traefik(t *testing.T) {
	values := map[string]any{
		"ingress":         map[string]any{"enabled": false, "className": "traefik"},
		"customDomains":   []any{"app.client.org"},
		"customDomainTLS": true,
	}
	out := render(t, values)
	cd := manifest(t, out, "custom-domains.yaml")
	if !strings.Contains(cd, redirectRef) || strings.Contains(cd, "nginx.ingress") {
		t.Fatalf("domaine personnalisé :\n%s", cd)
	}
	if mw := manifest(t, out, "traefik-middlewares.yaml"); !strings.Contains(mw, "redirectScheme") {
		t.Fatalf("middleware absent alors que l'ingress principal est désactivé :\n%s", mw)
	}
}

func TestCustomDomains_Traefik_Insecure(t *testing.T) {
	values := map[string]any{
		"ingress":         map[string]any{"enabled": false, "className": "traefik"},
		"customDomains":   []any{"app.client.org"},
		"customDomainTLS": false,
	}
	out := render(t, values)
	if cd := manifest(t, out, "custom-domains.yaml"); strings.Contains(cd, redirectRef) || strings.Contains(cd, "cert-manager.io") {
		t.Fatalf("redirection sans certificat :\n%s", cd)
	}
}

// La NetworkPolicy ne vise que les pods du workload : le pod du solveur HTTP-01
// (labels acme.cert-manager.io/*, sans « app ») n'est pas sélectionné, donc pas
// filtré. L'espace du contrôleur est bien celui donné par l'API.
func TestNetworkPolicy_SolverNotSelected(t *testing.T) {
	values := ingressValues("traefik", true)
	values["networkPolicy"] = map[string]any{"ingressNSLabelValue": "kube-system"}
	np := manifest(t, render(t, values), "network-policy.yaml")
	if !strings.Contains(np, "kubernetes.io/metadata.name: kube-system") {
		t.Fatalf("espace du contrôleur :\n%s", np)
	}
	selector := np[strings.Index(np, "podSelector:"):strings.Index(np, "policyTypes:")]
	if !strings.Contains(selector, "app: web") || strings.Contains(selector, "acme") {
		t.Fatalf("sélecteur :\n%s", selector)
	}
	if strings.Count(selector, "\n") != 3 {
		t.Fatalf("le sélecteur doit rester limité au label app :\n%s", selector)
	}
}
