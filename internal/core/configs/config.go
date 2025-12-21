package configs

import (
	"fmt"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"github.com/thekrauss/beto-shared/pkg/logger"
)

type GlobalConfig struct {
	ServiceName string         `mapstructure:"service_name"`
	ProjectID   string         `mapstructure:"project_id"`
	Logger      logger.Config  `mapstructure:"logger"`
	Server      ServerConfig   `mapstructure:"server"`
	Database    DBConfig       `mapstructure:"database"`
	Redis       RedisConfig    `mapstructure:"redis"`
	RabbitMQ    RabbitConfig   `mapstructure:"rabbitmq"`
	Metrics     MetricsConfig  `mapstructure:"metrics"`
	Tracing     TracingConfig  `mapstructure:"tracing"`
	Temporal    TemporalConfig `mapstructure:"temporal"`
	Mail        MailConfig     `mapstructure:"mail"`
	Swagger     SwaggerConfig  `mapstructure:"swagger"`
	JWT         JWTConfig      `mapstructure:"jwt"`
	Frontend    FrontendConfig `mapstructure:"frontend"`
	Roles       RolesConfig    `mapstructure:"roles"`
}

type JWTConfig struct {
	Secret            string        `mapstructure:"secret"`
	RefreshSecret     string        `mapstructure:"refresh_secret"`
	AccessExpiration  time.Duration `mapstructure:"access_expiration"`
	RefreshExpiration time.Duration `mapstructure:"refresh_expiration"`
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
}

type DBConfig struct {
	Driver       string `mapstructure:"driver"`
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Name         string `mapstructure:"name"`
	SSLMode      string `mapstructure:"sslmode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

type RedisConfig struct {
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
}

type TracingConfig struct {
	Exporter       string `mapstructure:"exporter"`
	JaegerEndpoint string `mapstructure:"jaeger_endpoint"`
}

type FrontendConfig struct {
	BaseURL string `mapstructure:"url"`
}

type RolesConfig struct {
	PlatformAdmin string `mapstructure:"platform_admin"`
}

var AppConfig GlobalConfig

func Load(path string) (*GlobalConfig, error) {

	viper.SetConfigType("yaml")

	if path != "" {
		viper.SetConfigFile(path)
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath("./internal/core/configs")
		viper.AddConfigPath(".")
		viper.AddConfigPath("..")
		viper.AddConfigPath("./config")
	}

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("erreur de chargement du fichier de configuration: %w", err)
	}

	// viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	// viper.AutomaticEnv()

	var config GlobalConfig
	err := viper.Unmarshal(&config, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		),
	))
	if err != nil {
		return nil, fmt.Errorf("configuration file parsing error: %w", err)
	}

	AppConfig = config
	return &AppConfig, nil
}
