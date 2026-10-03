package daemon_test

import (
	"database/sql"
	"strings"
	"testing"

	"dezuxk-gateway/internal/app/daemon"
	"dezuxk-gateway/internal/config"

	_ "dezuxk-gateway/internal/pkg/sqlite"
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

func TestProductionMode_DurabilityRequirementCannotBeOverridden(t *testing.T) {
	// Production rule: ngay cả khi cấu hình vô tình bật allow_memory_fallback = true,
	// môi trường production TUYỆT ĐỐI không cho phép silent fallback sang RAM.
	cfg := &config.Config{
		Environment: "production",
		Storage: config.StorageConfig{
			AllowMemoryFallback: true, // Không được phép ghi đè durability requirement
		},
	}

	// 1. Khi db == nil
	_, _, _, err := daemon.InitCriticalRepositories(cfg, nil)
	if err == nil {
		t.Fatalf("expected error in production when db is nil even if allow_memory_fallback=true, got nil")
	}

	// 2. Khi db bị lỗi kết nối/đã đóng
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.Close()

	_, _, _, err = daemon.InitCriticalRepositories(cfg, db)
	if err == nil {
		t.Fatalf("expected error in production on closed db, got nil")
	}
}

func TestDevelopmentMode_MemoryFallback(t *testing.T) {
	// 1. Dev mode cho phép fallback khi allow_memory_fallback = true
	cfgDevAllowed := &config.Config{
		Environment: "development",
		Storage: config.StorageConfig{
			AllowMemoryFallback: true,
		},
	}
	cp, m, ar, err := daemon.InitCriticalRepositories(cfgDevAllowed, nil)
	if err != nil {
		t.Fatalf("expected nil error on dev mode fallback, got: %v", err)
	}
	if cp == nil || m == nil || ar == nil {
		t.Fatalf("expected in-memory fallback repos to be created")
	}

	// 2. Dev mode fail closed khi allow_memory_fallback = false
	cfgDevBlocked := &config.Config{
		Environment: "development",
		Storage: config.StorageConfig{
			AllowMemoryFallback: false,
		},
	}
	_, _, _, err = daemon.InitCriticalRepositories(cfgDevBlocked, nil)
	if err == nil {
		t.Fatalf("expected error in dev mode when allow_memory_fallback=false, got nil")
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

func TestValidateInfrastructureAdapters_FailFast(t *testing.T) {
	// 1. Postgres storage driver must fail fast with truthful message
	cfgPostgres := &config.Config{
		Storage: config.StorageConfig{
			Driver: "postgres",
		},
	}
	err := daemon.ValidateInfrastructureAdapters(cfgPostgres)
	if err == nil || !strings.Contains(err.Error(), "postgres storage driver configured but adapter is not implemented") {
		t.Fatalf("expected fail fast error for postgres, got: %v", err)
	}

	// 2. Distributed enabled must fail fast with truthful message
	cfgDistributed := &config.Config{
		Distributed: config.DistributedConfig{
			Enabled: true,
			Driver:  "redis",
		},
	}
	err = daemon.ValidateInfrastructureAdapters(cfgDistributed)
	if err == nil || !strings.Contains(err.Error(), "distributed mode configured but Redis adapters are not implemented") {
		t.Fatalf("expected fail fast error for distributed redis, got: %v", err)
	}

	// 3. SQLite and non-distributed config must pass
	cfgValid := &config.Config{
		Storage: config.StorageConfig{
			Driver: "sqlite",
		},
		Distributed: config.DistributedConfig{
			Enabled: false,
		},
	}
	if err := daemon.ValidateInfrastructureAdapters(cfgValid); err != nil {
		t.Fatalf("expected nil error for valid sqlite standalone config, got: %v", err)
	}
}
