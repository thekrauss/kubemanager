package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/router"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgPath := os.Getenv("KUBEMANAGER_CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "/app/internal/core/configs/config.yaml"
	}

	cfg, err := configs.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	app := router.NewApp(cfg)
	if err := app.Init(ctx); err != nil {
		log.Fatalf("app init failed: %v", err)
	}

	mode := router.ResolveMode()
	log.Printf("starting kubemanager in mode=%s", mode)

	switch mode {
	case router.ModeWorker:
		if err := app.RunWorker(ctx); err != nil {
			log.Fatalf("worker exited with error: %v", err)
		}
	default:
		if err := app.RunServer(ctx); err != nil {
			log.Fatalf("server exited with error: %v", err)
		}
	}
}
