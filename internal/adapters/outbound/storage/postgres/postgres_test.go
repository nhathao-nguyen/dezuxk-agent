package postgres_test

import (
	"context"
	"os"
	"sync"
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := config.PostgresConfig{
		DSN:      dsn,
		MaxConns: 10,
	}
	pool, err := pgstorage.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("Không thể kết nối PostgreSQL thực tế qua DSN: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping PostgreSQL thực tế thất bại: %v", err)
	}

	if err := pgstorage.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("Chạy migrations thất bại: %v", err)
	}

	repo, err := pgstorage.NewPostgresAgentRunRepository(pool)
	if err != nil {
		t.Fatalf("Khởi tạo AgentRunRepo thất bại: %v", err)
	}

	tenantID := "test-tenant-live"
	runID := "pg-live-" + time.Now().Format("150405.000000")
	now := time.Now()
	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  tenantID,
		Goal:      "Live PG comprehensive integration test",
		Status:    domain.RunStatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 1. Create run
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create run thất bại: %v", err)
	}

	// 2. Claim run (worker-node-a nhận quyền gen=1)
	claimed, err := repo.ClaimRun(ctx, runID, "worker-node-a", 10*time.Second)
	if err != nil || !claimed {
		t.Fatalf("ClaimRun thất bại: claimed=%v, err=%v", claimed, err)
	}

	rClaimed, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get run sau claim thất bại: %v", err)
	}
	if rClaimed.ClaimGeneration != 1 || rClaimed.WorkerID != "worker-node-a" {
		t.Fatalf("ClaimGeneration (%d) hoặc WorkerID (%s) không đúng", rClaimed.ClaimGeneration, rClaimed.WorkerID)
	}
	if rClaimed.LeaseUntil == nil || rClaimed.LeaseUntil.Before(time.Now()) {
		t.Fatalf("LeaseUntil không hợp lệ: %v", rClaimed.LeaseUntil)
	}

	// 3. Renew lease hợp lệ
	renewed, err := repo.RenewLease(ctx, runID, "worker-node-a", 1, 20*time.Second)
	if err != nil || !renewed {
		t.Fatalf("RenewLease hợp lệ thất bại: renewed=%v, err=%v", renewed, err)
	}

	// 4. UpdateOwned hợp lệ
	run.Status = domain.RunStatusRunning
	run.CurrentStep = 1
	run.TotalToolCalls = 1
	updated, err := repo.UpdateOwned(ctx, run, "worker-node-a", 1)
	if err != nil || !updated {
		t.Fatalf("UpdateOwned hợp lệ thất bại: updated=%v, err=%v", updated, err)
	}

	// 5. AppendOwnedEvent hợp lệ
	event := &domain.AgentRunEvent{
		TenantID:  tenantID,
		RunID:     runID,
		Step:      1,
		Kind:      "step_progress",
		Message:   "Executing tool in postgres live test",
		Timestamp: time.Now(),
	}
	appended, err := repo.AppendOwnedEvent(ctx, event, "worker-node-a", 1)
	if err != nil || !appended {
		t.Fatalf("AppendOwnedEvent hợp lệ thất bại: appended=%v, err=%v", appended, err)
	}

	// 6. ValidateOwnership
	validOwner, err := repo.ValidateOwnership(ctx, runID, "worker-node-a", 1)
	if err != nil || !validOwner {
		t.Fatalf("ValidateOwnership hợp lệ thất bại: valid=%v, err=%v", validOwner, err)
	}

	// 7. Fencing rejects stale generation
	staleRenew, _ := repo.RenewLease(ctx, runID, "worker-node-a", 0, 10*time.Second)
	if staleRenew {
		t.Fatalf("Fencing thất bại: RenewLease chấp thuận stale generation 0!")
	}
	staleUpdate, _ := repo.UpdateOwned(ctx, run, "worker-node-a", 0)
	if staleUpdate {
		t.Fatalf("Fencing thất bại: UpdateOwned chấp thuận stale generation 0!")
	}
	staleAppend, _ := repo.AppendOwnedEvent(ctx, event, "worker-node-a", 0)
	if staleAppend {
		t.Fatalf("Fencing thất bại: AppendOwnedEvent chấp thuận stale generation 0!")
	}
	staleOwner, _ := repo.ValidateOwnership(ctx, runID, "worker-node-a", 0)
	if staleOwner {
		t.Fatalf("Fencing thất bại: ValidateOwnership chấp thuận stale generation 0!")
	}
	diffWorkerOwner, _ := repo.ValidateOwnership(ctx, runID, "worker-zombie", 1)
	if diffWorkerOwner {
		t.Fatalf("Fencing thất bại: ValidateOwnership chấp thuận worker_id sai!")
	}

	// 8. Tool ledger owned writes & fencing
	toolCallID := "call_live_pg_1"
	start := time.Now()
	// Stale tool planned
	stalePlanned, _ := repo.RecordPlannedOrRunningOwned(ctx, &domain.ToolExecutionRecord{
		TenantID:   tenantID,
		RunID:      runID,
		ToolCallID: toolCallID,
		ToolName:   "db_write",
		Status:     domain.ToolExecutionRunning,
		StartedAt:  &start,
	}, "worker-node-a", 0)
	if stalePlanned {
		t.Fatalf("Fencing thất bại: RecordPlannedOrRunningOwned chấp thuận stale generation 0!")
	}

	// Valid tool planned
	validPlanned, err := repo.RecordPlannedOrRunningOwned(ctx, &domain.ToolExecutionRecord{
		TenantID:   tenantID,
		RunID:      runID,
		ToolCallID: toolCallID,
		ToolName:   "db_write",
		Status:     domain.ToolExecutionRunning,
		StartedAt:  &start,
	}, "worker-node-a", 1)
	if err != nil || !validPlanned {
		t.Fatalf("RecordPlannedOrRunningOwned hợp lệ thất bại: valid=%v, err=%v", validPlanned, err)
	}

	// Stale tool finished
	staleFinished, _ := repo.RecordFinishedOwned(ctx, tenantID, runID, toolCallID, domain.ToolExecutionSucceeded, `{"ok":true}`, "", "worker-node-a", 0)
	if staleFinished {
		t.Fatalf("Fencing thất bại: RecordFinishedOwned chấp thuận stale generation 0!")
	}

	// Valid tool finished
	validFinished, err := repo.RecordFinishedOwned(ctx, tenantID, runID, toolCallID, domain.ToolExecutionSucceeded, `{"ok":true}`, "", "worker-node-a", 1)
	if err != nil || !validFinished {
		t.Fatalf("RecordFinishedOwned hợp lệ thất bại: valid=%v, err=%v", validFinished, err)
	}

	// 9. Read back and verify
	readRun, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get run thất bại: %v", err)
	}
	if readRun.CurrentStep != 1 || readRun.TotalToolCalls != 1 {
		t.Fatalf("Trạng thái run đọc lại không khớp: step=%d, toolCalls=%d", readRun.CurrentStep, readRun.TotalToolCalls)
	}

	events, err := repo.GetEvents(ctx, runID, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("GetEvents thất bại hoặc không có event: %v", err)
	}
	if events[0].Message != "Executing tool in postgres live test" {
		t.Fatalf("Nội dung event không khớp: %s", events[0].Message)
	}

	execRec, err := repo.GetExecution(ctx, tenantID, runID, toolCallID)
	if err != nil || execRec == nil {
		t.Fatalf("GetExecution thất bại: %v", err)
	}
	if execRec.Status != domain.ToolExecutionSucceeded || execRec.ResultJSON != `{"ok":true}` {
		t.Fatalf("Trạng thái tool execution không khớp: status=%s, result=%s", execRec.Status, execRec.ResultJSON)
	}
}

// TestPostgres_MigrationConcurrency mô phỏng Node A, Node B, Node C đồng thời chạy RunMigrations()
func TestPostgres_MigrationConcurrency(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("Bỏ qua kiểm thử Postgres migration concurrency vì TEST_POSTGRES_DSN không được thiết lập")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cfg := config.PostgresConfig{
		DSN:      dsn,
		MaxConns: 10,
	}
	pool, err := pgstorage.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("Không thể kết nối PostgreSQL: %v", err)
	}
	defer pool.Close()

	const concurrentNodes = 5
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentNodes)

	startGate := make(chan struct{})

	for i := 0; i < concurrentNodes; i++ {
		wg.Add(1)
		go func(nodeIndex int) {
			defer wg.Done()
			<-startGate // Đồng loạt xuất phát

			nodePool, err := pgstorage.NewPool(ctx, cfg)
			if err != nil {
				errCh <- err
				return
			}
			defer nodePool.Close()

			if mErr := pgstorage.RunMigrations(ctx, nodePool); mErr != nil {
				errCh <- mErr
			}
		}(i)
	}

	// Mở cổng đồng loạt
	close(startGate)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("Migration concurrency phát sinh lỗi: %v", err)
		}
	}

	// Xác nhận schema đúng đắn và không có bản ghi trùng lặp
	var totalMigrations, distinctVersions int
	err = pool.QueryRow(ctx, "SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations;").Scan(&totalMigrations, &distinctVersions)
	if err != nil {
		t.Fatalf("Truy vấn schema_migrations thất bại: %v", err)
	}
	if totalMigrations != distinctVersions || totalMigrations == 0 {
		t.Fatalf("schema_migrations có sự trùng lặp hoặc rỗng: total=%d, distinct=%d", totalMigrations, distinctVersions)
	}
}
