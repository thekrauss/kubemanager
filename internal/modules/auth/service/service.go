package service

import (
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/cache"
	"github.com/thekrauss/kubemanager/internal/middleware/security"

	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"go.uber.org/zap"
)

type AuthService struct {
	AuthRepo repository.AuthRepository
	Cache    cache.CacheRedis
	JWT      security.JWTManager
	Logger   *zap.SugaredLogger
	Config   *configs.GlobalConfig
	Hasher   security.PasswordHasher
}

func NewAuthService(
	cfg *configs.GlobalConfig,
	repo repository.AuthRepository,
	jwtMgr security.JWTManager,
	cacheRepo cache.CacheRedis,
	logger *zap.SugaredLogger,
	hasher security.PasswordHasher,
) *AuthService {
	return &AuthService{
		AuthRepo: repo,
		Cache:    cacheRepo,
		JWT:      jwtMgr,
		Logger:   logger.With("service", "AuthService"),
		Config:   cfg,
		Hasher:   hasher,
	}
}
