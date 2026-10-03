package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	_ "github.com/mattn/go-sqlite3"
)

// Test 5: P0 Fix SQLite Legacy Migration Order
// 1. Tạo schema cũ giống commit trước (thiếu tenant_id trong events, thiếu worker/lease/security_context trong runs)
// 2. Chèn dữ liệu legacy
// 3. Chạy NewSqliteAgentRunRepository
// 4. Migration phải pass
// 5. tenant_id, worker_id, lease_until, heartbeat_at, security_context phải tồn tại
// 6. Index phải tồn tại
// 7. Dữ liệu cũ không mất
func TestHardening_LegacySqliteMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy_migration_test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// 1. Tạo schema cũ (legacy)
	// Bảng agent_runs cũ: không có security_context, worker_id, lease_until, heartbeat_at
	oldRunsTable := `
	CREATE TABLE agent_runs (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		goal TEXT NOT NULL,
		status TEXT NOT NULL,
		model TEXT NOT NULL,
		workspace TEXT NOT NULL,
		current_step INTEGER NOT NULL DEFAULT 0,
		max_steps INTEGER NOT NULL DEFAULT 25,
		total_tool_calls INTEGER NOT NULL DEFAULT 0,
		stop_reason TEXT,
		final_answer TEXT,
		error TEXT,
		git_diff TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		finished_at DATETIME
	);`
	if _, err := db.Exec(oldRunsTable); err != nil {
		t.Fatalf("failed to create legacy agent_runs table: %v", err)
	}

	// Bảng agent_run_events cũ: KHÔNG có cột tenant_id
	oldEventsTable := `
	CREATE TABLE agent_run_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		step INTEGER NOT NULL,
		kind TEXT NOT NULL,
		message TEXT NOT NULL,
		timestamp DATETIME NOT NULL
	);`
	if _, err := db.Exec(oldEventsTable); err != nil {
		t.Fatalf("failed to create legacy agent_run_events table: %v", err)
	}

	// 2. Chèn dữ liệu legacy trước khi migrate
	insertOldRun := `
	INSERT INTO agent_runs (id, tenant_id, goal, status, model, workspace, created_at, updated_at)
	VALUES ('legacy_run_001', 'legacy_tenant_x', 'Legacy Goal', 'completed', 'gemini-3.8-flash', '.', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`
	if _, err := db.Exec(insertOldRun); err != nil {
		t.Fatalf("failed to insert legacy run: %v", err)
	}

	insertOldEvent := `
	INSERT INTO agent_run_events (run_id, step, kind, message, timestamp)
	VALUES ('legacy_run_001', 1, 'thinking', 'Legacy event message', CURRENT_TIMESTAMP);
	`
	if _, err := db.Exec(insertOldEvent); err != nil {
		t.Fatalf("failed to insert legacy event: %v", err)
	}

	// 3. Khởi tạo SqliteAgentRunRepository để kích hoạt migration mới
	repo, err := NewSqliteAgentRunRepository(db)
	if err != nil {
		t.Fatalf("CRITICAL: NewSqliteAgentRunRepository failed on legacy schema: %v", err)
	}

	// 4 & 5. Kiểm tra các cột mới đã được thêm thành công
	colsRuns, err := getTableColumns(db, "agent_runs")
	if err != nil {
		t.Fatalf("failed to get columns for agent_runs: %v", err)
	}
	expectedRunCols := []string{"tenant_id", "idempotency_key", "worker_id", "lease_until", "heartbeat_at", "security_context"}
	for _, col := range expectedRunCols {
		if !colsRuns[col] {
			t.Fatalf("column %q was NOT added to agent_runs by migration!", col)
		}
	}

	colsEvents, err := getTableColumns(db, "agent_run_events")
	if err != nil {
		t.Fatalf("failed to get columns for agent_run_events: %v", err)
	}
	if !colsEvents["tenant_id"] {
		t.Fatalf("column 'tenant_id' was NOT added to agent_run_events by migration!")
	}

	// 6. Kiểm tra các indexes bắt buộc tồn tại
	var indexCount int
	err = db.QueryRow(`
		SELECT COUNT(1) FROM sqlite_master 
		WHERE type = 'index' AND name IN (
			'idx_agent_runs_tenant_status',
			'idx_agent_runs_status',
			'idx_agent_run_events_tenant_run'
		);
	`).Scan(&indexCount)
	if err != nil {
		t.Fatalf("failed to query indexes: %v", err)
	}
	if indexCount < 3 {
		t.Fatalf("expected at least 3 required indexes, found: %d", indexCount)
	}

	// 7. Xác nhận dữ liệu cũ hoàn toàn nguyên vẹn
	ctx := context.Background()
	legacyRun, err := repo.Get(ctx, "legacy_run_001")
	if err != nil {
		t.Fatalf("failed to get legacy run through migrated repo: %v", err)
	}
	if legacyRun.Goal != "Legacy Goal" || legacyRun.TenantID != "legacy_tenant_x" {
		t.Fatalf("legacy run data corrupted after migration: %+v", legacyRun)
	}

	// Kiểm tra sự kiện cũ
	events, err := repo.GetEvents(ctx, "legacy_run_001", 0)
	if err != nil {
		t.Fatalf("failed to get legacy events: %v", err)
	}
	if len(events) != 1 || events[0].Message != "Legacy event message" {
		t.Fatalf("legacy events lost or corrupted after migration: %+v", events)
	}
}

// Test 6: P0 Remove "all" Tenant Magic Bypass
// role=user với tenant_id="all" KHÔNG được đọc hoặc hủy run của tenant khác
func TestHardening_NoAllTenantMagicBypass(t *testing.T) {
	// Kiểm tra cả SQLite và Memory Repository
	t.Run("SqliteRepo", func(t *testing.T) {
		repo, db := newTestSqliteAgentRunRepo(t)
		defer db.Close()
		testAllTenantBypass(t, repo)
	})

	t.Run("MemoryRepo", func(t *testing.T) {
		repo := NewMemoryAgentRunRepository()
		testAllTenantBypass(t, repo)
	})
}

func testAllTenantBypass(t *testing.T, repo ports.AgentRunRepository) {
	ctx := context.Background()

	// Tenant A tạo 1 run
	runA := &domain.AgentRun{
		ID:        "run_belonging_to_tenant_a",
		TenantID:  "tenant-alpha",
		Goal:      "Confidential Alpha Goal",
		Status:    domain.RunStatusRunning,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
	}
	if err := repo.Create(ctx, runA); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Người dùng có tenant_id = "all" cố tình truy vấn run của tenant-alpha
	// 1. GetForTenant("all", runA.ID) PHẢI trả về not found
	got, err := repo.GetForTenant(ctx, "all", runA.ID)
	if err == nil && got != nil {
		t.Fatalf("CRITICAL SECURITY BYPASS: tenant_id='all' was able to get run of tenant-alpha!")
	}

	// 2. List("all", 10, 0) KHÔNG được chứa run của tenant-alpha
	runs, err := repo.List(ctx, "all", 10, 0)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for _, r := range runs {
		if r.TenantID == "tenant-alpha" {
			t.Fatalf("CRITICAL SECURITY BYPASS: List('all') returned run belonging to tenant-alpha!")
		}
	}

	// 3. CancelForTenant("all", runA.ID) PHẢI thất bại
	err = repo.CancelForTenant(ctx, "all", runA.ID)
	if err == nil {
		t.Fatalf("CRITICAL SECURITY BYPASS: tenant_id='all' was able to cancel run of tenant-alpha!")
	}

	// Run A vẫn phải giữ nguyên trạng thái running
	checkA, err := repo.GetForTenant(ctx, "tenant-alpha", runA.ID)
	if err != nil {
		t.Fatalf("failed to get run A: %v", err)
	}
	if checkA.Status != domain.RunStatusRunning {
		t.Fatalf("run A status changed unexpectedly: %s", checkA.Status)
	}
}

// Test 11: P1 Fix Cancel State Machine & Terminal State Rejection
// Không cho phép hủy tác vụ đã ở trạng thái kết thúc (completed, failed, cancelled).
// Phải trả về ErrInvalidStatusTransition (hoặc 409 conflict).
func TestHardening_CancelStateMachineParity(t *testing.T) {
	ctx := context.Background()

	testCases := []domain.AgentRunStatus{
		domain.RunStatusCompleted,
		domain.RunStatusFailed,
		domain.RunStatusCancelled,
	}

	t.Run("SqliteRepo", func(t *testing.T) {
		repo, db := newTestSqliteAgentRunRepo(t)
		defer db.Close()

		for _, terminalStatus := range testCases {
			runID := "run_term_" + string(terminalStatus)
			run := &domain.AgentRun{
				ID:        runID,
				TenantID:  "tenant-term",
				Goal:      "Terminal state cancel test",
				Status:    terminalStatus,
				Model:     "gemini-3.8-flash",
				Workspace: ".",
			}
			_ = repo.Create(ctx, run)

			// 1. Cancel() trên run đã kết thúc phải trả ErrInvalidStatusTransition
			err := repo.Cancel(ctx, runID)
			if err == nil {
				t.Fatalf("Cancel() on terminal status %s should return error", terminalStatus)
			}
			if !errors.Is(err, ErrInvalidStatusTransition) {
				t.Fatalf("expected ErrInvalidStatusTransition, got: %v", err)
			}

			// 2. CancelForTenant() trên run đã kết thúc phải trả ErrInvalidStatusTransition
			err = repo.CancelForTenant(ctx, "tenant-term", runID)
			if err == nil {
				t.Fatalf("CancelForTenant() on terminal status %s should return error", terminalStatus)
			}
			if !errors.Is(err, ErrInvalidStatusTransition) {
				t.Fatalf("expected ErrInvalidStatusTransition, got: %v", err)
			}
		}
	})

	t.Run("MemoryRepo", func(t *testing.T) {
		repo := NewMemoryAgentRunRepository()

		for _, terminalStatus := range testCases {
			runID := "run_mem_term_" + string(terminalStatus)
			run := &domain.AgentRun{
				ID:        runID,
				TenantID:  "tenant-term",
				Goal:      "Terminal state cancel test",
				Status:    terminalStatus,
				Model:     "gemini-3.8-flash",
				Workspace: ".",
			}
			_ = repo.Create(ctx, run)

			// 1. Cancel() trên run đã kết thúc phải trả ErrInvalidStatusTransition
			err := repo.Cancel(ctx, runID)
			if err == nil {
				t.Fatalf("Cancel() on terminal status %s should return error in memory repo", terminalStatus)
			}
			if !errors.Is(err, ErrInvalidStatusTransition) {
				t.Fatalf("expected ErrInvalidStatusTransition, got: %v", err)
			}

			// 2. CancelForTenant() trên run đã kết thúc phải trả ErrInvalidStatusTransition
			err = repo.CancelForTenant(ctx, "tenant-term", runID)
			if err == nil {
				t.Fatalf("CancelForTenant() on terminal status %s should return error in memory repo", terminalStatus)
			}
			if !errors.Is(err, ErrInvalidStatusTransition) {
				t.Fatalf("expected ErrInvalidStatusTransition, got: %v", err)
			}
		}
	})
}
