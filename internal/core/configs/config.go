package configs

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/thekrauss/beto-shared/pkg/logger"
)

type GlobalConfig struct {
	ServiceName  string             `mapstructure:"service_name"`
	ProjectID    string             `mapstructure:"project_id"`
	Logger       logger.Config      `mapstructure:"logger"`
	Server       ServerConfig       `mapstructure:"server"`
	Database     DBConfig           `mapstructure:"database"`
	Redis        RedisConfig        `mapstructure:"redis"`
	RabbitMQ     RabbitConfig       `mapstructure:"rabbitmq"`
	Metrics      MetricsConfig      `mapstructure:"metrics"`
	Tracing      TracingConfig      `mapstructure:"tracing"`
	Temporal     TemporalConfig     `mapstructure:"temporal"`
	Mail         MailConfig         `mapstructure:"mail"`
	Swagger      SwaggerConfig      `mapstructure:"swagger"`
	JWT          JWTConfig          `mapstructure:"jwt"`
	Frontend     FrontendConfig     `mapstructure:"frontend"`
	Roles        RolesConfig        `mapstructure:"roles"`
	Kubernetes   KubernetesConfig   `mapstructure:"kubernetes"`
	Vps          Vps                `mapstructure:"vps"`
	Multitenancy MultitenancyConfig `mapstructure:"multitenancy"`
	Centrifugo   CentrifugoConfig   `mapstructure:"centrifugo"`
	Keycloak     KeycloakConfig     `mapstructure:"keycloak"`
	Audit        AuditConfig        `mapstructure:"audit"`
	RateLimit    RateLimitConfig    `mapstructure:"rate_limit"`
	ObjectStore  ObjectStoreConfig  `mapstructure:"object_store"`
	Sites        SitesConfig        `mapstructure:"sites"`
	IAM          IAMConfig          `mapstructure:"iam"`
}

// IAMConfig : secrets de l'IAM (docs/architecture/iam.md).
type IAMConfig struct {
	// APIKeyPepper : secret serveur des clés API v2 (HMAC). ≥ 32 octets, hors
	// base de données (env KUBEMANAGER_IAM_API_KEY_PEPPER en prod). Le changer
	// invalide toutes les clés existantes.
	APIKeyPepper string `mapstructure:"api_key_pepper"`
	// PlatformAdmins / PlatformSupport : emails du staff betorun. Source de
	// vérité des rôles roles/platform.admin et roles/platform.support : un
	// email retiré perd son rôle à la réconciliation suivante (≤ 10 min).
	PlatformAdmins  []string `mapstructure:"platform_admins"`
	PlatformSupport []string `mapstructure:"platform_support"`
	// STSIssuer : URL publique du STS (émetteur des jetons machine), c'est-à-
	// dire <URL publique de l'API>/kmanager/v1/iam. Le endpoint de jeton est
	// <STSIssuer>/token et les clés publiques <STSIssuer>/.well-known/jwks.json.
	STSIssuer string `mapstructure:"sts_issuer"`
	// STSKeyEncryptionKey : 32 octets en base64 ; chiffre (AES-256-GCM) les
	// clés de signature du STS stockées en base. Secret (env
	// KUBEMANAGER_IAM_STS_KEY_ENCRYPTION_KEY en prod), remplaçable par un KMS.
	STSKeyEncryptionKey string `mapstructure:"sts_key_encryption_key"`
	// MFAACRValues : valeurs du claim acr (Keycloak, mapping ACR → niveau
	// d'authentification du realm) qui attestent une double authentification,
	// ex. ["gold"]. Vide : MFA non configuré (pas de step-up, organisation
	// ne peut pas exiger le MFA).
	MFAACRValues []string `mapstructure:"mfa_acr_values"`
	// StepUpMaxAge : fraîcheur exigée de l'authentification MFA pour les
	// actions sensibles (défaut 10 min).
	StepUpMaxAge time.Duration `mapstructure:"step_up_max_age"`
}

// ObjectStoreConfig : stockage S3-compatible (SeaweedFS en dev) des sites statiques.
type ObjectStoreConfig struct {
	Endpoint  string `mapstructure:"endpoint"` // host:port, sans schéma
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	UseSSL    bool   `mapstructure:"use_ssl"`
}

// SitesConfig : hébergement de sites statiques.
type SitesConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// BaseDomain : chaque site est servi sur <subdomain>.<base_domain>.
	BaseDomain string `mapstructure:"base_domain"`
	// EdgeTarget : cible CNAME à donner aux utilisateurs pour un domaine perso.
	EdgeTarget string `mapstructure:"edge_target"`
	// Limites d'une archive de déploiement (0 = défaut du service).
	MaxArchiveBytes   int64 `mapstructure:"max_archive_bytes"`
	MaxExtractedBytes int64 `mapstructure:"max_extracted_bytes"`
	MaxFiles          int   `mapstructure:"max_files"`
}

// AuditConfig : contrôle le sampling des logs d'audit (fix P1).
// Les events de sécurité critiques (failed/revoked/permission_denied/creates)
// sont TOUJOURS loggés quel que soit le sampling.
type AuditConfig struct {
	// SampleRate : 1 sur N events "normaux" (success, apikey_used) est loggé.
	// 0 ou 1 → pas de sampling, tout est loggé (défaut en dev).
	// Ex : 100 → 1% des success. En prod haut trafic, recommandé : 50-1000.
	SampleRate int `mapstructure:"sample_rate"`
}

type KeycloakConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	IssuerURL         string        `mapstructure:"issuer_url"`
	JWKSURL           string        `mapstructure:"jwks_url"`
	Audience          string        `mapstructure:"audience"`
	ClientID          string        `mapstructure:"client_id"`
	ClientSecret      string        `mapstructure:"client_secret"`
	Realm             string        `mapstructure:"realm"`
	AdminBaseURL      string        `mapstructure:"admin_base_url"`
	AdminRealm        string        `mapstructure:"admin_realm"`
	AdminClientID     string        `mapstructure:"admin_client_id"`
	AdminClientSecret string        `mapstructure:"admin_client_secret"`
	StrictAudience    bool          `mapstructure:"strict_audience"`
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
	SocialRedirectURI string        `mapstructure:"social_redirect_uri"`
	// WebClientID : client public Keycloak (PKCE S256) utilisé pour les flux
	// Authorization Code (login hébergé + social). Vide = client admin.
	WebClientID string `mapstructure:"web_client_id"`
	// CLIClientID : client Keycloak public de la CLI, avec « OAuth 2.0 Device
	// Authorization Grant » activé. Vide : connexion de la CLI désactivée.
	CLIClientID string `mapstructure:"cli_client_id"`
	// AuthClientID / AuthClientSecret : client confidentiel réservé au
	// backend, qui vérifie les mots de passe (docs/architecture/auth.md §5).
	// Jamais exposé : le navigateur et la CLI ne parlent qu'à l'API betorun.
	AuthClientID     string `mapstructure:"auth_client_id"`
	AuthClientSecret string `mapstructure:"auth_client_secret"`
}

type JWTConfig struct {
	Secret            string        `mapstructure:"secret"`
	RefreshSecret     string        `mapstructure:"refresh_secret"`
	AccessExpiration  time.Duration `mapstructure:"access_expiration"`
	RefreshExpiration time.Duration `mapstructure:"refresh_expiration"`
}

type RateLimitConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	Limit   int           `mapstructure:"limit"` // max requests per Window
	Burst   int           `mapstructure:"burst"` // legacy, ignored (fixed window)
	Window  time.Duration `mapstructure:"window"`
}

type MailConfig struct {
	SMTPHost     string `mapstructure:"smtp_host"`
	SMTPPort     int    `mapstructure:"smtp_port"`
	SMTPUser     string `mapstructure:"smtp_user"`
	SMTPPassword string `mapstructure:"smtp_password"`
	FromEmail    string `mapstructure:"from_email"`
	FromName     string `mapstructure:"from_name"`
	Enabled      bool   `mapstructure:"enabled"`
}

type ServerConfig struct {
	GRPCPort        int           `mapstructure:"grpc_port"`
	HTTPPort        int           `mapstructure:"http_port"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	AllowedOrigins  []string      `mapstructure:"allowed_origins"`
	// TrustedProxies : adresses ou CIDR des proxies dont les en-têtes
	// X-Forwarded-For sont crus (ingress, BFF de la console). Vide : boucle
	// locale seulement. Sinon un client choisirait son IP et contournerait
	// les limites de tentatives.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

type DBConfig struct {
	Driver         string `mapstructure:"driver"`
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	Name           string `mapstructure:"name"`
	SSLMode        string `mapstructure:"sslmode"`
	MaxOpenConns   int    `mapstructure:"max_open_conns"`
	MaxIdleConns   int    `mapstructure:"max_idle_conns"`
	MigrationsPath string `mapstructure:"migrations_path"`
}

type CentrifugoConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	URL             string   `mapstructure:"url"`
	APIURL          string   `mapstructure:"api_url"`
	APIKey          string   `mapstructure:"api_key"`
	TokenHMACSecret string   `mapstructure:"token_hmac_secret"`
	AllowedOrigins  []string `mapstructure:"allowed_origins"`
	AdminEnabled    bool     `mapstructure:"admin_enabled"`
	AdminPassword   string   `mapstructure:"admin_password"`
	AdminSecret     string   `mapstructure:"admin_secret"`
}

type MultitenancyConfig struct {
	BaseDomain     string `mapstructure:"base_domain"`
	AllowCustomDom bool   `mapstructure:"allow_custom_dom"`
}

type RedisConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type SwaggerConfig struct {
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

type RabbitConfig struct {
	URL      string `mapstructure:"url"`
	Exchange string `mapstructure:"exchange"`
	Queue    string `mapstructure:"queue"`
}

type TemporalConfig struct {
	Host              string        `mapstructure:"host"`
	Namespace         string        `mapstructure:"namespace"`
	TaskQueue         string        `mapstructure:"task_queue"`
	ConnectionTimeout time.Duration `mapstructure:"connection_timeout"`
}

type MetricsConfig struct {
	PrometheusPort int `mapstructure:"prometheus_port"`
	// ListenAddr : si renseigné, /metrics est exposé sur un listener dédié
	// (ex: ":9100") au lieu de l'engine public. Permet une NetworkPolicy
	// serrée "monitoring namespace → :9100" sans exposer les metrics aux
	// utilisateurs finaux.
	ListenAddr string `mapstructure:"listen_addr"`
}

type TracingConfig struct {
	Exporter       string `mapstructure:"exporter"`
	JaegerEndpoint string `mapstructure:"jaeger_endpoint"`
	// SampleRatio : ratio de sampling OTEL (0.0 → 1.0). Défaut 1.0 en dev,
	// recommandé 0.01 (1%) en prod pour économiser Jaeger + CPU.
	SampleRatio float64 `mapstructure:"sample_ratio"`
}

type Vps struct {
	// AdressIp : IP publique du point d'entrée des conteneurs (URL
	// <release>-<projet>.<ip>.sslip.io). Clé vps.vps_ip (env
	// KUBEMANAGER_VPS_VPS_IP) ; vide : conteneurs sans URL publique.
	AdressIp string `mapstructure:"vps_ip"`
}

type FrontendConfig struct {
	BaseURL string `mapstructure:"url"`
}

type RolesConfig struct {
	PlatformAdmin string `mapstructure:"platform_admin"`
}

type KubernetesConfig struct {
	KubeConfigPath string `mapstructure:"kubeconfig_path"`
	// ClusterID : identifiant (celui de son watcher) du cluster où tournent
	// les workloads. Vide : identité des workloads désactivée.
	ClusterID string `mapstructure:"cluster_id"`
	// IngressClass : classe d'ingress des conteneurs exposés (défaut nginx ;
	// traefik sur un k3s/k3d nu).
	IngressClass string `mapstructure:"ingress_class"`
	// IngressNamespace : espace de noms du contrôleur d'ingress, seul autorisé
	// par la NetworkPolicy à joindre les conteneurs (défaut ingress-nginx ;
	// kube-system pour le traefik de k3s).
	IngressNamespace string `mapstructure:"ingress_namespace"`
}

// EnvPrefix est le préfixe utilisé pour les variables d'environnement (ex: KUBEMANAGER_DATABASE_HOST).
const EnvPrefix = "KUBEMANAGER"

var AppConfig GlobalConfig

func Load(path string) (*GlobalConfig, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath("./internal/core/configs")
		v.AddConfigPath(".")
		v.AddConfigPath("..")
		v.AddConfigPath("./config")
	}

	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	// AutomaticEnv ne sert à Unmarshal que pour les clés déjà connues de
	// viper (présentes dans le fichier) : sans liaison explicite, une
	// variable comme KUBEMANAGER_IAM_API_KEY_PEPPER serait ignorée si le
	// fichier n'a pas de section iam (cas de la prod, configurée par l'env).
	if err := bindEnvs(v, reflect.TypeOf(GlobalConfig{}), ""); err != nil {
		return nil, err
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("erreur de chargement du fichier de configuration: %w", err)
	}

	var config GlobalConfig
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("erreur d'analyse du fichier de configuration: %w", err)
	}

	AppConfig = config
	return &AppConfig, nil
}

// bindEnvs lie chaque clé de la configuration (tags mapstructure, récursif) à
// sa variable KUBEMANAGER_<CLÉ>.
func bindEnvs(v *viper.Viper, t reflect.Type, prefix string) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("mapstructure"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeOf(time.Duration(0)) && f.Type.PkgPath() != "time" {
			if err := bindEnvs(v, f.Type, key); err != nil {
				return err
			}
			continue
		}
		if err := v.BindEnv(key); err != nil {
			return fmt.Errorf("config: bind env %s: %w", key, err)
		}
	}
	return nil
}
