package cache

import (
	"context"
	"fmt"
	"log"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

func NewAuthCacheFromConfig(ctx context.Context, cfg configs.RedisConfig) AuthCache {
	ristrettoCache, err := NewRistrettoAuthCache()
	if err != nil {
		log.Fatalf("failed to initialize ristretto cache: %v", err)
	}

	if !cfg.Enabled {
		return ristrettoCache
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	redisCache, err := NewRedisAuthCache(ctx, addr, cfg.Password, cfg.DB)
	if err != nil {
		log.Printf("redis enabled but connection failed, falling back to ristretto: %v", err)
		return ristrettoCache
	}

	return NewFallbackAuthCache(redisCache, ristrettoCache)
}
