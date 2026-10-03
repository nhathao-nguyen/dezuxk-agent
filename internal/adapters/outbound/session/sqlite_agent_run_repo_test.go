package session

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"

	_ "dezuxk-gateway/internal/pkg/sqlite"
)

func newTestSqliteAgentRunRepo(t *testing.T) (*SqliteAgentRunRepository, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_runs.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	repo, err := NewSqliteAgentRunRepository(db)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	return repo, db
}

func TestSqliteAgentRunRepository_IdempotencyConcurrency(t *testing.T) {
	repo, db := newTestSqliteAgentRunRepo(t)
	defer db.Close()

	ctx := context.Background()
	const concurrency = 25
	idempKey := "idem-concurrent-key-999"
	tenantID := "tenant-alpha"

	var wg sync.WaitGroup
	startCh := make(chan struct{})
	runIDs := make([]string, concurrency)
	errorsList := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh

			// Mô phỏng logic Submit: Tìm trước -> Chèn -> nếu dính UNIQUE conflict thì Tìm lại
			existing, err := repo.FindByTenantAndIdempotencyKey(ctx, tenantID, idempKey)
			if err == nil && existing != nil {
				runIDs[idx] = existing.ID
				return
			}

			run := &domain.AgentRun{
				ID:             fmt.Sprintf("run_%d_%d", time.Now().UnixNano(), idx),
				TenantID:       tenantID,
				IdempotencyKey: idempKey,
				Goal:           "Concurrent Task Submission",
				Status:         domain.RunStatusQueued,
				Model:          "gemini-3.8-flash",
				Workspace:      ".",
				MaxSteps:       10,
			}

			insertErr := repo.Create(ctx, run)
			if insertErr != nil {
				// Nếu vi phạm UNIQUE constraint, lấy lại bản ghi của goroutine khác đã chèn thành công
				if insertErr == ErrIdempotencyConflict {
					found, findErr := repo.FindByTenantAndIdempotencyKey(ctx, tenantID, idempKey)
					if findErr == nil && found != nil {
						runIDs[idx] = found.ID
						return
					}
					errorsList[idx] = findErr
					return
				}
				errorsList[idx] = insertErr
				return
			}

			runIDs[idx] = run.ID
		}(i)
	}

	close(startCh)
	wg.Wait()

	// 1. Kiểm tra không có lỗi không mong muốn
	for i, err := range errorsList {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}

	// 2. Tất cả goroutine phải nhận về cùng 1 run ID duy nhất
	firstID := runIDs[0]
	if firstID == "" {
		t.Fatalf("first run ID is empty")
	}
	for i, id := range runIDs {
		if id != firstID {
			t.Fatalf("concurrency mismatch: goroutine %d got run ID %s, expected %s", i, id, firstID)
		}
	}

	// 3. Database chỉ được có đúng 1 row duy nhất cho idempotency key này
	var count int
	err := db.QueryRow("SELECT COUNT(1) FROM agent_runs WHERE tenant_id = ? AND idempotency_key = ?", tenantID, idempKey).Scan(&count)
	if err != nil {
		t.Fatalf("query count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row in database, got %d", count)
	}
}

func TestSqliteAgentRunRepository_TenantIsolation(t *testing.T) {
	repo, db := newTestSqliteAgentRunRepo(t)
	defer db.Close()

	ctx := context.Background()

	runA := &domain.AgentRun{
		ID:        "run_tenant_A",
		TenantID:  "tenant_A",
		Goal:      "Secret goal for tenant A",
		Status:    domain.RunStatusRunning,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
	}
	if err := repo.Create(ctx, runA); err != nil {
		t.Fatalf("create run A failed: %v", err)
	}

	eventA := &domain.AgentRunEvent{
		TenantID: "tenant_A",
		RunID:    runA.ID,
		Step:     1,
		Kind:     "thinking",
		Message:  "Private thinking for A",
	}
	if err := repo.AppendEvent(ctx, eventA); err != nil {
		t.Fatalf("append event A failed: %v", err)
	}

	// 1. Tenant A đọc được run của chính mình
	fetchedA, err := repo.GetForTenant(ctx, "tenant_A", runA.ID)
	if err != nil || fetchedA == nil {
		t.Fatalf("tenant A should be able to get run A: %v", err)
	}

	// 2. Tenant B đọc run của Tenant A -> phải lỗi không tìm thấy (404/not found)
	fetchedB, err := repo.GetForTenant(ctx, "tenant_B", runA.ID)
	if err == nil || fetchedB != nil {
		t.Fatalf("tenant B must NOT be able to get run of tenant A! got: %v", fetchedB)
	}

	// 3. Tenant B cố gắng đọc event của Tenant A -> phải rỗng hoặc lỗi
	eventsB, err := repo.GetEventsForTenant(ctx, "tenant_B", runA.ID, 0)
	if err == nil && len(eventsB) > 0 {
		t.Fatalf("tenant B must NOT see events of tenant A! got %d events", len(eventsB))
	}

	// 4. Tenant B cố gắng Cancel run của Tenant A -> bị từ chối
	err = repo.CancelForTenant(ctx, "tenant_B", runA.ID)
	if err == nil {
		t.Fatalf("tenant B must NOT be able to cancel run of tenant A")
	}

	// Kiểm tra lại run A vẫn đang running (không bị B hủy)
	checkA, _ := repo.GetForTenant(ctx, "tenant_A", runA.ID)
	if checkA.Status != domain.RunStatusRunning {
		t.Fatalf("run A should still be running, got: %s", checkA.Status)
	}

	// 5. Tenant A hủy run của chính mình -> thành công
	err = repo.CancelForTenant(ctx, "tenant_A", runA.ID)
	if err != nil {
		t.Fatalf("tenant A cancel should succeed: %v", err)
	}

	checkA2, _ := repo.GetForTenant(ctx, "tenant_A", runA.ID)
	if checkA2.Status != domain.RunStatusCancelled {
		t.Fatalf("run A should be cancelled, got: %s", checkA2.Status)
	}
}

func TestSqliteAgentRunRepository_CancelConsistency(t *testing.T) {
	repo, db := newTestSqliteAgentRunRepo(t)
	defer db.Close()

	ctx := context.Background()

	run := &domain.AgentRun{
		ID:        "run_race_cancel",
		TenantID:  "tenant_x",
		Goal:      "Test Cancel Consistency",
		Status:    domain.RunStatusRunning,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Người dùng hủy tác vụ
	if err := repo.Cancel(ctx, run.ID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	// Kiểm tra trạng thái hiện tại trong DB là cancelled
	inDB, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if inDB.Status != domain.RunStatusCancelled {
		t.Fatalf("expected cancelled, got %s", inDB.Status)
	}

	// Worker kết thúc sau đó và cố ghi đè thành completed
	run.Status = domain.RunStatusCompleted
	run.FinalAnswer = "Overwritten completed answer"
	if err := repo.Update(ctx, run); err != nil {
		t.Fatalf("update call failed: %v", err)
	}

	// Trạng thái trong DB TUYỆT ĐỐI vẫn phải là cancelled!
	finalDB, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if finalDB.Status != domain.RunStatusCancelled {
		t.Fatalf("CRITICAL BUG: cancelled status was overwritten with %s!", finalDB.Status)
	}
}

func TestSqliteAgentRunRepository_ClaimRun(t *testing.T) {
	repo, db := newTestSqliteAgentRunRepo(t)
	defer db.Close()

	ctx := context.Background()

	run := &domain.AgentRun{
		ID:        "run_claim_test",
		TenantID:  "tenant_y",
		Goal:      "Test Claim",
		Status:    domain.RunStatusQueued,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Worker 1 claim run
	claimed1, err := repo.ClaimRun(ctx, run.ID, "worker-1", 1*time.Minute)
	if err != nil || !claimed1 {
		t.Fatalf("worker-1 claim failed: %v", err)
	}

	// Worker 2 cố claim cùng run -> phải thất bại vì run đã running và lease chưa hết hạn
	claimed2, err := repo.ClaimRun(ctx, run.ID, "worker-2", 1*time.Minute)
	if err != nil {
		t.Fatalf("worker-2 claim error: %v", err)
	}
	if claimed2 {
		t.Fatalf("worker-2 should NOT be able to claim already claimed run")
	}

	// Kiểm tra worker_id trong DB
	inDB, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if inDB.WorkerID != "worker-1" {
		t.Fatalf("expected worker-1, got %s", inDB.WorkerID)
	}
}
