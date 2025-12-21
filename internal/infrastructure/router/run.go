package router

import (
	"context"
	"net/http"

	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	"github.com/thekrauss/beto-shared/pkg/logger"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/cache"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

type App struct {
	Config         *configs.GlobalConfig
	JWTManager     security.JWTManager
	DB             *gorm.DB
	Logger         *zap.SugaredLogger
	GRPCServer     *grpc.Server
	HTTPServer     *http.Server
	Cache          cache.CacheRedis
	Tracer         trace.Tracer
	TracerShutdown func()
}

func NewApp(cfg *configs.GlobalConfig) *App {
	log := logger.InitLogger(cfg.Logger)
	return &App{Config: cfg, Logger: log}
}

func (a *App) Run(ctx context.Context) error {
	return nil
}
