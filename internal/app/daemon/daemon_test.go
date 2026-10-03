package daemon_test

import (
	"database/sql"
	"testing"

	"dezuxk-gateway/internal/app/daemon"
	"dezuxk-gateway/internal/config"

	_ "github.com/mattn/go-sqlite3"
)

func TestCheckpointMigrationFailure_ProductionStartupFails(t *testing.T) {
	// Mở db rồi đóng ngay để các truy vấn migration thất bại
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.Close()

	cfg := &config.Config{
		Storage: config.StorageConfig{
			AllowMemoryFallback: false, // Production default: không được silent fallback
		},
	}

	_, _, _, err = daemon.InitCriticalRepositories(cfg, db)
	if err == nil {
		t.Fatalf("expected error on closed db with allow_memory_fallback=false, got nil")
	}
}

func TestCheckpointMigrationFailure_DevModeMayFallback(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.Close()

	cfg := &config.Config{
		Storage: config.StorageConfig{
			AllowMemoryFallback: true, // Explicit Dev Mode fallback
		},
	}

	cpRepo, mRepo, arRepo, err := daemon.InitCriticalRepositories(cfg, db)
	if err != nil {
		t.Fatalf("expected nil error on dev mode fallback, got: %v", err)
	}
	if cpRepo == nil || mRepo == nil || arRepo == nil {
		t.Fatalf("expected all in-memory fallback repositories to be initialized")
	}
}

func TestCheckpointRepoFailureProductionFailsStartup(t *testing.T) {
	TestCheckpointMigrationFailure_ProductionStartupFails(t)
}

