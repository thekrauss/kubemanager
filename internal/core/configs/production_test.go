package configs

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testSecret   = "0123456789abcdef0123456789abcdef0123456789abcdef"
	testPassword = "0123456789abcdef0123"
)

// validProductionConfig : configuration complète, modifiée ensuite par chaque cas.
func validProductionConfig(t *testing.T) *GlobalConfig {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fichier")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &GlobalConfig{Env: "production", ServiceName: "kubemanager"}
	c.Server.HTTPPort = 8080
	c.Server.AllowedOrigins = []string{"https://app.betorun.app"}
	c.Server.TrustedProxies = []string{"172.30.0.10", "172.30.0.11/32"}
	c.Frontend.BaseURL = "https://app.betorun.app"
	c.Database = DBConfig{Driver: "postgres", Host: "k8sdb", Port: 5432, User: "betorun", Password: testPassword, Name: "betorun", SSLMode: "disable"}
	c.Redis = RedisConfig{Host: "redis", Port: 6379, Password: testPassword}
	c.Temporal = TemporalConfig{Host: "temporal:7233", Namespace: "default", TaskQueue: "kubemanager"}
	c.Keycloak = KeycloakConfig{Enabled: true, IssuerURL: "http://keycloak:8080/realms/kubemanager", AdminBaseURL: "http://keycloak:8080",
		Realm: "kubemanager", AdminRealm: "kubemanager", ClientID: "kubemanager-api", AdminClientID: "kubemanager-admin",
		AdminClientSecret: testSecret, AuthClientID: "betorun-auth", AuthClientSecret: testSecret}
	c.IAM = IAMConfig{STSIssuer: "https://api.betorun.app/kmanager/v1/iam", APIKeyPepper: testSecret,
		STSKeyEncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), PlatformAdmins: []string{"ops@betorun.app"}}
	c.Mail = MailConfig{Enabled: true, SMTPHost: "smtp.mailer.net", SMTPPort: 587, FromEmail: "no-reply@betorun.app"}
	c.Centrifugo = CentrifugoConfig{Enabled: true, URL: "https://realtime.betorun.app", APIURL: "http://centrifugo:8000", APIKey: testSecret, TokenHMACSecret: testSecret}
	c.Sites = SitesConfig{Enabled: true, BaseDomain: "sites.betorun.app", EdgeTarget: "edge.betorun.app"}
	c.ObjectStore = ObjectStoreConfig{Endpoint: "seaweedfs:8333", AccessKey: "platform", SecretKey: testSecret, Bucket: "betorun-sites", Region: "us-east-1"}
	c.Builds = BuildsConfig{Enabled: true, RegistryHost: "registry.betorun.app", PublicAPIURL: "https://api.betorun.app/kmanager/v1", Secret: testSecret, TokenCertFile: file, TokenKeyFile: file}
	c.Kubernetes = KubernetesConfig{KubeConfigPath: file, IngressClass: "traefik", IngressNamespace: "kube-system"}
	c.Vps.AdressIp = "203.0.113.20"
	c.applyProductionDefaults()
	return c
}

func TestValidateProduction_Complete(t *testing.T) {
	if err := validProductionConfig(t).ValidateProduction(); err != nil {
		t.Fatal(err)
	}
}

// Chaque défaut doit citer la clé fautive.
func TestValidateProduction_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*GlobalConfig)
		want   string
	}{
		{"mail absent", func(c *GlobalConfig) { c.Mail = MailConfig{} }, "mail.enabled"},
		{"mailhog", func(c *GlobalConfig) { c.Mail.SMTPHost = "mailhog" }, "mail.smtp_host"},
		{"expéditeur .local", func(c *GlobalConfig) { c.Mail.FromEmail = "no-reply@kubemanager.local" }, "mail.from_email"},
		{"frontend absent", func(c *GlobalConfig) { c.Frontend.BaseURL = "" }, "frontend.url"},
		{"frontend localhost", func(c *GlobalConfig) { c.Frontend.BaseURL = "https://localhost:3000" }, "frontend.url"},
		{"frontend http", func(c *GlobalConfig) { c.Frontend.BaseURL = "http://app.betorun.app" }, "frontend.url"},
		{"sts_issuer absent", func(c *GlobalConfig) { c.IAM.STSIssuer = "" }, "iam.sts_issuer"},
		{"sites.localhost", func(c *GlobalConfig) { c.Sites.BaseDomain = "sites.localhost" }, "sites.base_domain"},
		{"registry_host local", func(c *GlobalConfig) { c.Builds.RegistryHost = "127.0.0.1:5000" }, "builds.registry_host"},
		{"registre http", func(c *GlobalConfig) { c.Builds.RegistryInsecure = true }, "builds.registry_insecure"},
		{"public_api_url absent", func(c *GlobalConfig) { c.Builds.PublicAPIURL = "" }, "builds.public_api_url"},
		{"certificat absent", func(c *GlobalConfig) { c.Builds.TokenKeyFile = "/inexistant" }, "builds.token_key_file"},
		{"builds sans sites", func(c *GlobalConfig) { c.Sites.Enabled = false }, "builds.enabled"},
		{"proxies absents", func(c *GlobalConfig) { c.Server.TrustedProxies = nil }, "server.trusted_proxies"},
		{"proxy 0.0.0.0/0", func(c *GlobalConfig) { c.Server.TrustedProxies = []string{"0.0.0.0/0"} }, "server.trusted_proxies"},
		{"proxy ::/0", func(c *GlobalConfig) { c.Server.TrustedProxies = []string{"::/0"} }, "server.trusted_proxies"},
		{"proxy trop large", func(c *GlobalConfig) { c.Server.TrustedProxies = []string{"172.16.0.0/12"} }, "server.trusted_proxies"},
		{"proxy invalide", func(c *GlobalConfig) { c.Server.TrustedProxies = []string{"caddy"} }, "server.trusted_proxies"},
		{"swagger sans mot de passe", func(c *GlobalConfig) { c.Swagger.User = "admin" }, "swagger.password"},
		{"swagger sans utilisateur", func(c *GlobalConfig) { c.Swagger.Password = testPassword }, "swagger.user"},
		{"pepper court", func(c *GlobalConfig) { c.IAM.APIKeyPepper = "court" }, "iam.api_key_pepper"},
		{"kek hexadécimale", func(c *GlobalConfig) { c.IAM.STSKeyEncryptionKey = testSecret }, "iam.sts_key_encryption_key"},
		{"mot de passe d'exemple", func(c *GlobalConfig) { c.Database.Password = "change-me-change-me-change-me" }, "database.password"},
		{"secret de dev", func(c *GlobalConfig) { c.ObjectStore.SecretKey = "edge-dev-secret-edge-dev-secret-x" }, "object_store.secret_key"},
		{"mot de passe avec /", func(c *GlobalConfig) { c.Database.Password = "abcdefgh/abcdefgh@abcdefgh" }, "database.password"},
		{"origine *", func(c *GlobalConfig) { c.Server.AllowedOrigins = []string{"*"} }, "server.allowed_origins"},
		{"admin centrifugo", func(c *GlobalConfig) { c.Centrifugo.AdminEnabled = true }, "centrifugo.admin_enabled"},
		{"centrifugo http", func(c *GlobalConfig) { c.Centrifugo.URL = "http://realtime.betorun.app" }, "centrifugo.url"},
		{"base locale", func(c *GlobalConfig) { c.Database.Host = "127.0.0.1" }, "database.host"},
		{"classe d'ingress absente", func(c *GlobalConfig) { c.Kubernetes.IngressClass = "" }, "kubernetes.ingress_class"},
		{"domaines sans TLS", func(c *GlobalConfig) { c.Kubernetes.CustomDomainInsecure = true }, "kubernetes.custom_domain_insecure"},
		{"rate limit désactivé", func(c *GlobalConfig) { c.RateLimit.Enabled = false }, "rate_limit.enabled"},
		{"métriques sur le port public", func(c *GlobalConfig) { c.Metrics.ListenAddr = ":8080" }, "metrics.listen_addr"},
		{"domaine d'exemple", func(c *GlobalConfig) { c.Frontend.BaseURL = "https://app.example.com" }, "frontend.url"},
		{"github incomplet", func(c *GlobalConfig) { c.GitHubApp.AppID = "1" }, "github_app"},
		{"social incomplet", func(c *GlobalConfig) { c.Social.Google.ClientID = "x" }, "social.google.client_secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validProductionConfig(t)
			tc.mutate(c)
			err := c.ValidateProduction()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("attendu %q, reçu : %v", tc.want, err)
			}
		})
	}
}

// Tout est listé d'un coup, avec la variable à renseigner, sans aucun secret.
func TestValidateProduction_ListsEverythingWithoutSecrets(t *testing.T) {
	c := &GlobalConfig{Env: "production"}
	c.Database.Password = "secret-trop-court"
	c.applyProductionDefaults()
	err := c.ValidateProduction()
	var pce *ProductionConfigError
	if !errors.As(err, &pce) || len(pce.Problems) < 15 {
		t.Fatalf("erreur = %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"KUBEMANAGER_FRONTEND_URL", "KUBEMANAGER_SERVER_TRUSTED_PROXIES", "KUBEMANAGER_IAM_STS_ISSUER", "KUBEMANAGER_MAIL_ENABLED", "KUBEMANAGER_KUBERNETES_INGRESS_CLASS"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s absent de :\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "secret-trop-court") {
		t.Errorf("secret divulgué :\n%s", msg)
	}
}

func TestApplyProductionDefaults(t *testing.T) {
	c := &GlobalConfig{Env: "production"}
	c.applyProductionDefaults()
	if !c.RateLimit.Enabled || c.RateLimit.Limit != defaultRateLimit || c.RateLimit.Window != defaultRateLimitWindow {
		t.Fatalf("rate limit = %+v", c.RateLimit)
	}
	if c.Metrics.ListenAddr != defaultMetricsListenAddr {
		t.Fatalf("metrics = %q", c.Metrics.ListenAddr)
	}
}

func TestLoad_ProductionWithoutFile(t *testing.T) {
	t.Setenv("KUBEMANAGER_ENV", "production")
	// Fichier absent toléré : l'erreur vient de la validation, pas du fichier.
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	var pce *ProductionConfigError
	if !errors.As(err, &pce) {
		t.Fatalf("erreur = %v", err)
	}
}

func TestLoad_DevelopmentUnchanged(t *testing.T) {
	// Sans le mode production, un fichier absent reste une erreur.
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("fichier absent accepté hors production")
	}
	// Et une configuration vide démarre sans contrôle ni rate limit imposé.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("service_name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.Enabled || cfg.Metrics.ListenAddr != "" {
		t.Fatalf("réglages de production appliqués : %+v %+v", cfg.RateLimit, cfg.Metrics)
	}
	if cfg.Server.ShutdownTimeout != DefaultShutdownTimeout {
		t.Fatalf("shutdown = %v", cfg.Server.ShutdownTimeout)
	}
}
