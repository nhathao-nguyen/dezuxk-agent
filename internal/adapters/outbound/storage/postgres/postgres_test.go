package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	pgstorage "dezuxk-gateway/internal/adapters/outbound/storage/postgres"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// TestPostgres_ConfigValidation kiểm tra cấu hình kết nối PostgreSQL
func TestPostgres_ConfigValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Cấu hình không thể kết nối tới cổng không tồn tại -> phải trả về lỗi kết nối nhanh (fail-fast)
	cfg := config.PostgresConfig{
		Host:           "127.0.0.1",
		Port:           59999, // Cổng không có service lắng nghe
		User:           "invalid_user",
		Password:       "invalid_pass",
		DBName:         "invalid_db",
		SSLMode:        "disable",
		ConnectTimeout: 500 * time.Millisecond,
	}

	_, err := pgstorage.NewPool(ctx, cfg)
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi khi kết nối tới PostgreSQL không tồn tại, nhưng thành công")
	}
}

// TestPostgres_LiveIntegration chạy khi có biến môi trường TEST_POSTGRES_DSN
func TestPostgres_LiveIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("Bỏ qua kiểm thử Postgres thực tế vì TEST_POSTGRES_DSN không được thiết lập")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := config.PostgresConfig{
		Host:     "localhost",
		Port:     5432,
		MaxConns: 5,
	}
	pool, err := pgstorage.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("Không thể kết nối PostgreSQL thực tế: %v", err)
	}
	defer pool.Close()

	if err := pgstorage.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("Chạy migrations thất bại: %v", err)
	}

	repo, err := pgstorage.NewPostgresAgentRunRepository(pool)
	if err != nil {
		t.Fatalf("Khởi tạo AgentRunRepo thất bại: %v", err)
	}

	runID := "pg-test-" + time.Now().Format("150405.000")
	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  "test-tenant",
		Goal:      "Live PG integration test",
		Status:    domain.RunStatusQueued,
		CreatedAt: time.Now(),
	}

	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create run thất bại: %v", err)
	}

	claimed, err := repo.ClaimRun(ctx, runID, "worker-test", 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("ClaimRun thất bại: claimed=%v, err=%v", claimed, err)
	}
}
