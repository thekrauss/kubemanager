package main

import (
	"context"
	"log"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/router"
)

func main() {
	cfg, err := configs.Load("/app/internal/core/configs/config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	app := router.NewApp(cfg)
	if err := app.Run(context.Background()); err != nil {
		log.Fatalf("app run failed: %v", err)
	}
}
