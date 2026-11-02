package configs

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Mode production (KUBEMANAGER_ENV=production) : réglages par défaut prudents,
// puis refus de démarrer d'une configuration incomplète. Hors de ce mode, rien
// de ce fichier ne s'applique.

const (
	envKey        = "env"
	envProduction = "production"

	// minSecretLen : secrets opérés par betorun (jetons, pepper, clés).
	minSecretLen = 32
	// minPasswordLen : mots de passe des services internes.
	minPasswordLen = 16
	// stsKEKBytes : clé de chiffrement du STS, en base64.
	stsKEKBytes = 32
	// minProxyPrefixV4/V6 : au-delà, un proxy « de confiance » couvre trop d'hôtes.
	minProxyPrefixV4 = 16
	minProxyPrefixV6 = 48

	// Rate limit par défaut (par IP). Assez haut pour une connexion partagée :
	// les opérateurs mobiles placent beaucoup d'abonnés derrière une même IP.
	defaultRateLimit       = 600
	defaultRateLimitWindow = time.Minute
	// defaultMetricsListenAddr : port non publié par le compose de production.
	defaultMetricsListenAddr = ":9100"
)

func isProductionEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), envProduction)
}

// IsProduction : KUBEMANAGER_ENV=production.
func (c *GlobalConfig) IsProduction() bool { return isProductionEnv(c.Env) }

// applyProductionDefaults : ce qui doit être actif sans configuration.
func (c *GlobalConfig) applyProductionDefaults() {
	if !c.RateLimit.Enabled && c.RateLimit.Limit == 0 {
		c.RateLimit.Enabled = true
		c.RateLimit.Limit = defaultRateLimit
	}
	if c.RateLimit.Window == 0 {
		c.RateLimit.Window = defaultRateLimitWindow
	}
	if c.Metrics.ListenAddr == "" {
		c.Metrics.ListenAddr = defaultMetricsListenAddr
	}
}

// ProductionConfigError : tout ce qui empêche de démarrer, en une fois.
type ProductionConfigError struct {
	Problems []string
}

func (e *ProductionConfigError) Error() string {
	return fmt.Sprintf("configuration de production invalide (%d problème(s)) :\n  - %s",
		len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// configCheck accumule les problèmes. Les messages citent la clé et sa variable
// d'environnement, jamais la valeur d'un secret.
type configCheck struct{ problems []string }

func envName(key string) string {
	return EnvPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func (v *configCheck) fail(key, format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf("%s (%s) : %s", key, envName(key), fmt.Sprintf(format, args...)))
}

func (v *configCheck) required(key, value string) bool {
	if strings.TrimSpace(value) == "" {
		v.fail(key, "obligatoire")
		return false
	}
	return true
}

// host : nom ou adresse joignable depuis un autre conteneur ou serveur.
func (v *configCheck) host(key, value string) {
	if v.required(key, value) && isLocalHost(value) {
		v.fail(key, "valeur locale refusée (%s)", value)
	}
}

// internalURL : URL d'un service interne (http accepté), jamais locale.
func (v *configCheck) internalURL(key, value string) {
	if !v.required(key, value) {
		return
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		v.fail(key, "URL invalide (%s)", value)
		return
	}
	if isLocalHost(u.Hostname()) {
		v.fail(key, "valeur locale refusée (%s)", value)
	}
}

// publicURL : URL vue depuis l'extérieur, forcément en HTTPS.
func (v *configCheck) publicURL(key, value string) {
	if !v.required(key, value) {
		return
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		v.fail(key, "URL invalide (%s)", value)
		return
	}
	if u.Scheme != "https" {
		v.fail(key, "HTTPS obligatoire (%s)", value)
	}
	if isLocalHost(u.Hostname()) {
		v.fail(key, "valeur locale refusée (%s)", value)
	}
}

// secret : présent, assez long, pas une valeur d'exemple ou de développement.
func (v *configCheck) secret(key, value string, minLen int) {
	if !v.required(key, value) {
		return
	}
	if isPlaceholderSecret(value) {
		v.fail(key, "valeur d'exemple ou de développement refusée")
		return
	}
	if len(value) < minLen {
		v.fail(key, "trop court (%d caractères minimum)", minLen)
	}
}

func (v *configCheck) file(key, value string) {
	if !v.required(key, value) {
		return
	}
	// Ouverture réelle : un fichier présent mais réservé à root échoue ici, avec
	// un message clair, plutôt qu'au premier usage.
	f, err := os.Open(value)
	if err != nil {
		v.fail(key, "fichier illisible (%s)", value)
		return
	}
	_ = f.Close()
}

// isLocalHost : hôte (éventuellement avec port) de la machine, d'un réseau de
// développement ou réservé à la documentation.
func isLocalHost(value string) bool {
	host := strings.ToLower(strings.TrimSpace(value))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSuffix(host, "."), "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	switch host {
	case "localhost", "host.docker.internal", "mailhog", "example.com", "example.org", "example.net":
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".test", ".example", ".invalid", ".example.com", ".example.org", ".example.net"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func isPlaceholderSecret(value string) bool {
	v := strings.ToLower(value)
	if strings.Contains(v, "change-me") || strings.Contains(v, "changeme") || strings.Contains(v, "dev-secret") {
		return true
	}
	switch v {
	case "admin", "password", "secret", "test":
		return true
	}
	return false
}

// ValidateProduction contrôle la configuration et liste TOUT ce qui ne va pas.
func (c *GlobalConfig) ValidateProduction() error {
	v := &configCheck{}
	c.checkServer(v)
	c.checkStores(v)
	c.checkIdentity(v)
	c.checkPublicServices(v)
	c.checkKubernetes(v)
	c.checkOptional(v)
	if len(v.problems) == 0 {
		return nil
	}
	return &ProductionConfigError{Problems: v.problems}
}

func (c *GlobalConfig) checkServer(v *configCheck) {
	v.required("service_name", c.ServiceName)
	if c.Server.HTTPPort <= 0 {
		v.fail("server.http_port", "obligatoire")
	}
	if len(c.Server.AllowedOrigins) == 0 {
		v.fail("server.allowed_origins", "obligatoire (origine HTTPS de la console)")
	}
	for _, origin := range c.Server.AllowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			v.fail("server.allowed_origins", "« * » refusé")
			continue
		}
		v.publicURL("server.allowed_origins", origin)
	}
	checkTrustedProxies(v, c.Server.TrustedProxies)
	v.publicURL("frontend.url", c.Frontend.BaseURL)

	if !c.RateLimit.Enabled || c.RateLimit.Limit <= 0 {
		v.fail("rate_limit.enabled", "le rate limit doit rester actif en production (rate_limit.limit > 0)")
	}
	if metricsPort := portOf(c.Metrics.ListenAddr); metricsPort != 0 && metricsPort == c.Server.HTTPPort {
		v.fail("metrics.listen_addr", "doit être distinct du port public de l'API (%d)", c.Server.HTTPPort)
	}
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func checkTrustedProxies(v *configCheck, proxies []string) {
	const key = "server.trusted_proxies"
	if len(proxies) == 0 {
		v.fail(key, "obligatoire : IP fixe du proxy (sinon tous les clients partagent la même IP et les mêmes limites)")
		return
	}
	for _, proxy := range proxies {
		proxy = strings.TrimSpace(proxy)
		if ip := net.ParseIP(proxy); ip != nil {
			if ip.IsUnspecified() {
				v.fail(key, "%s refusé", proxy)
			}
			continue
		}
		_, network, err := net.ParseCIDR(proxy)
		if err != nil {
			v.fail(key, "ni adresse ni CIDR (%s)", proxy)
			continue
		}
		ones, bits := network.Mask.Size()
		if (bits == 32 && ones < minProxyPrefixV4) || (bits == 128 && ones < minProxyPrefixV6) {
			v.fail(key, "plage trop large (%s) : un client pourrait choisir son IP", proxy)
		}
	}
}

func (c *GlobalConfig) checkStores(v *configCheck) {
	v.required("database.driver", c.Database.Driver)
	v.host("database.host", c.Database.Host)
	if c.Database.Port <= 0 {
		v.fail("database.port", "obligatoire")
	}
	v.required("database.user", c.Database.User)
	v.required("database.name", c.Database.Name)
	v.required("database.sslmode", c.Database.SSLMode)
	v.secret("database.password", c.Database.Password, minPasswordLen)
	// beto-shared/pkg/db construit le DSN sans échappement.
	if strings.ContainsAny(c.Database.Password, " /@:?#%'\"\\") {
		v.fail("database.password", "caractères interdits : le DSN n'est pas échappé (utiliser de l'hexadécimal)")
	}

	v.host("redis.host", c.Redis.Host)
	if c.Redis.Port <= 0 {
		v.fail("redis.port", "obligatoire")
	}
	v.secret("redis.password", c.Redis.Password, minPasswordLen)

	v.host("temporal.host", c.Temporal.Host)
	v.required("temporal.namespace", c.Temporal.Namespace)
	v.required("temporal.task_queue", c.Temporal.TaskQueue)
}

func (c *GlobalConfig) checkIdentity(v *configCheck) {
	if !c.Keycloak.Enabled {
		v.fail("keycloak.enabled", "doit être true")
	}
	v.internalURL("keycloak.issuer_url", c.Keycloak.IssuerURL)
	v.internalURL("keycloak.admin_base_url", c.Keycloak.AdminBaseURL)
	v.required("keycloak.realm", c.Keycloak.Realm)
	v.required("keycloak.admin_realm", c.Keycloak.AdminRealm)
	v.required("keycloak.client_id", c.Keycloak.ClientID)
	v.required("keycloak.admin_client_id", c.Keycloak.AdminClientID)
	v.secret("keycloak.admin_client_secret", c.Keycloak.AdminClientSecret, minPasswordLen)
	v.required("keycloak.auth_client_id", c.Keycloak.AuthClientID)
	v.secret("keycloak.auth_client_secret", c.Keycloak.AuthClientSecret, minPasswordLen)
	if c.Keycloak.SocialRedirectURI != "" {
		v.publicURL("keycloak.social_redirect_uri", c.Keycloak.SocialRedirectURI)
	}

	v.publicURL("iam.sts_issuer", c.IAM.STSIssuer)
	v.secret("iam.api_key_pepper", c.IAM.APIKeyPepper, minSecretLen)
	if v.required("iam.sts_key_encryption_key", c.IAM.STSKeyEncryptionKey) {
		kek, err := base64.StdEncoding.DecodeString(c.IAM.STSKeyEncryptionKey)
		if err != nil || len(kek) != stsKEKBytes {
			v.fail("iam.sts_key_encryption_key", "%d octets en base64 attendus", stsKEKBytes)
		}
	}
	if len(c.IAM.PlatformAdmins) == 0 {
		v.fail("iam.platform_admins", "obligatoire (au moins un email)")
	}

	if !c.Mail.Enabled {
		v.fail("mail.enabled", "doit être true : inscription, vérification d'email et mot de passe oublié en dépendent")
	}
	v.host("mail.smtp_host", c.Mail.SMTPHost)
	if c.Mail.SMTPPort <= 0 {
		v.fail("mail.smtp_port", "obligatoire")
	}
	if v.required("mail.from_email", c.Mail.FromEmail) {
		if at := strings.LastIndex(c.Mail.FromEmail, "@"); at < 0 || isLocalHost(c.Mail.FromEmail[at+1:]) {
			v.fail("mail.from_email", "adresse locale ou invalide (%s)", c.Mail.FromEmail)
		}
	}
}

func (c *GlobalConfig) checkPublicServices(v *configCheck) {
	if c.Centrifugo.Enabled {
		v.publicURL("centrifugo.url", c.Centrifugo.URL)
		v.internalURL("centrifugo.api_url", c.Centrifugo.APIURL)
		v.secret("centrifugo.api_key", c.Centrifugo.APIKey, minSecretLen)
		v.secret("centrifugo.token_hmac_secret", c.Centrifugo.TokenHMACSecret, minSecretLen)
		if c.Centrifugo.AdminEnabled {
			v.fail("centrifugo.admin_enabled", "doit être false")
		}
	}

	if c.Sites.Enabled {
		if v.required("sites.base_domain", c.Sites.BaseDomain) && isLocalHost(c.Sites.BaseDomain) {
			v.fail("sites.base_domain", "valeur locale refusée (%s)", c.Sites.BaseDomain)
		}
		v.host("sites.edge_target", c.Sites.EdgeTarget)
		v.host("object_store.endpoint", c.ObjectStore.Endpoint)
		v.required("object_store.access_key", c.ObjectStore.AccessKey)
		v.secret("object_store.secret_key", c.ObjectStore.SecretKey, minSecretLen)
		v.required("object_store.bucket", c.ObjectStore.Bucket)
		v.required("object_store.region", c.ObjectStore.Region)
	}

	if c.Builds.Enabled {
		if !c.Sites.Enabled {
			v.fail("builds.enabled", "exige sites.enabled : le stockage objet est partagé")
		}
		v.host("builds.registry_host", c.Builds.RegistryHost)
		if c.Builds.RegistryInsecure {
			v.fail("builds.registry_insecure", "doit être false")
		}
		v.publicURL("builds.public_api_url", c.Builds.PublicAPIURL)
		v.secret("builds.secret", c.Builds.Secret, minSecretLen)
		v.file("builds.token_cert_file", c.Builds.TokenCertFile)
		v.file("builds.token_key_file", c.Builds.TokenKeyFile)
	}
}

func (c *GlobalConfig) checkKubernetes(v *configCheck) {
	v.file("kubernetes.kubeconfig_path", c.Kubernetes.KubeConfigPath)
	// Sans ces deux réglages, le chart vise nginx : sous k3s, aucun conteneur
	// ne serait joignable.
	v.required("kubernetes.ingress_class", c.Kubernetes.IngressClass)
	v.required("kubernetes.ingress_namespace", c.Kubernetes.IngressNamespace)
	if c.Kubernetes.CustomDomainInsecure {
		v.fail("kubernetes.custom_domain_insecure", "doit être false")
	}
	v.host("vps.vps_ip", c.Vps.AdressIp)
}

func (c *GlobalConfig) checkOptional(v *configCheck) {
	// Swagger : facultatif, mais jamais à moitié configuré.
	if c.Swagger.User != "" || c.Swagger.Password != "" {
		v.required("swagger.user", c.Swagger.User)
		v.secret("swagger.password", c.Swagger.Password, minPasswordLen)
	}
	if gh := c.GitHubApp; gh != (GitHubAppConfig{}) && !gh.Complete() {
		v.fail("github_app.app_id", "application GitHub incomplète (app_id, slug, private_key_file, webhook_secret, client_id, client_secret)")
	}
	if c.GitHubApp.Complete() {
		v.file("github_app.private_key_file", c.GitHubApp.PrivateKeyFile)
	}
	for name, provider := range map[string]SocialProviderConfig{"google": c.Social.Google, "github": c.Social.GitHub} {
		if provider.ClientID != "" && provider.ClientSecret == "" {
			v.fail("social."+name+".client_secret", "obligatoire si client_id est renseigné")
		}
	}
}
