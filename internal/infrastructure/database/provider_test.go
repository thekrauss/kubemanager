package database

import (
	"testing"
	"time"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

func TestPoolSettings(t *testing.T) {
	open, idle, life, idleTime := poolSettings(configs.DBConfig{})
	if open != DefaultMaxOpenConns || idle != DefaultMaxIdleConns || life != DefaultConnMaxLifetime || idleTime != DefaultConnMaxIdleTime {
		t.Fatalf("défauts : %d %d %v %v", open, idle, life, idleTime)
	}
	open, idle, life, idleTime = poolSettings(configs.DBConfig{MaxOpenConns: 7, MaxIdleConns: 2, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Minute})
	if open != 7 || idle != 2 || life != time.Hour || idleTime != time.Minute {
		t.Fatalf("config ignorée : %d %d %v %v", open, idle, life, idleTime)
	}
	// Pool ouvert plus petit que le défaut des connexions en attente.
	if _, idle, _, _ = poolSettings(configs.DBConfig{MaxOpenConns: 3}); idle != 3 {
		t.Fatalf("idle = %d", idle)
	}
}
