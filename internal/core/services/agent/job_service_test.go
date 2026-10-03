package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"

	_ "dezuxk-gateway/internal/pkg/sqlite"
)

type dummyRunner struct {
	delay time.Duration
}

func (d *dummyRunner) Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error) {
	if opts.OnProgress != nil {
		opts.OnProgress(1, "thinking", "Analyzing goal...")
		opts.OnProgress(2, "completed", "Goal achieved!")
	}
	if d.delay > 0 {
		select {
		case <-ctx.Done():
			return &domain.AgentState{
				Goal:        goal,
				IsCompleted: false,
				StopReason:  domain.StopReasonCancelled,
				Error:       ctx.Err().Error(),
			}, ctx.Err()
		case <-time.After(d.delay):
		}
	}

	state := &domain.AgentState{
		Goal:        goal,
		IsCompleted: true,
		StopReason:  domain.StopReasonCompleted,
		FinalAnswer: "Task done: " + goal,
		CurrentStep: 2,
	}
	if opts.InitialState != nil {
		state.Messages = append(state.Messages, opts.InitialState.Messages...)
		state.Steps = append(state.Steps, opts.InitialState.Steps...)
	}
	return state, nil
}

func newTestSqliteRepo(t *testing.T) (*session.SqliteAgentRunRepository, *session.SqliteCheckpointRepository, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "job_service_test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	runRepo, err := session.NewSqliteAgentRunRepository(db)
	if err != nil {
		t.Fatalf("failed to create run repo: %v", err)
	}
	cpRepo, err := session.NewSqliteCheckpointRepository(db)
	if err != nil {
		t.Fatalf("failed to create cp repo: %v", err)
	}
	return runRepo, cpRepo, db
}

func TestJobService_SubmitAndIdempotency(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	runner := &dummyRunner{delay: 10 * time.Millisecond}
	svc := NewJobService(repo, runner)

	ctx := context.Background()
	idempKey := "idem-key-12345"

	// 1. Submit run lần đầu
	run1, err := svc.SubmitRun(ctx, "Build gateway API", domain.AgentRunOptions{MaxSteps: 5}, idempKey)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	// 2. Submit lại với cùng Idempotency Key -> phải trả về cùng run ID
	run2, err := svc.SubmitRun(ctx, "Build gateway API duplicate", domain.AgentRunOptions{MaxSteps: 5}, idempKey)
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}

	if run1.ID != run2.ID {
		t.Fatalf("idempotency failed: expected same run ID %s, got %s", run1.ID, run2.ID)
	}

	time.Sleep(50 * time.Millisecond)

	finalRun, err := svc.GetRun(ctx, run1.ID)
	if err != nil {
		t.Fatalf("get run failed: %v", err)
	}
	if finalRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected run status completed, got: %s", finalRun.Status)
	}
	if !strings.Contains(finalRun.FinalAnswer, "Task done") {
		t.Fatalf("unexpected final answer: %s", finalRun.FinalAnswer)
	}
}

func TestJobService_IdempotencyConcurrency20Goroutines(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	runner := &dummyRunner{delay: 30 * time.Millisecond}
	svc := NewJobService(runRepo, runner)

	const concurrency = 25
	idempKey := "idem-race-25-workers"
	tenantID := "tenant-concurrency-test"

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	var wg sync.WaitGroup
	startCh := make(chan struct{})
	results := make([]string, concurrency)
	errorsList := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh

			run, err := svc.SubmitRun(ctx, "Concurrent Task Goal", domain.AgentRunOptions{MaxSteps: 5}, idempKey)
			if err != nil {
				errorsList[idx] = err
				return
			}
			results[idx] = run.ID
		}(i)
	}

	close(startCh)
	wg.Wait()

	// Không được có goroutine nào gặp lỗi
	for i, err := range errorsList {
		if err != nil {
			t.Fatalf("goroutine %d failed submit: %v", i, err)
		}
	}

	// Tất cả 25 goroutine phải nhận được đúng 1 Run ID duy nhất
	expectedID := results[0]
	if expectedID == "" {
		t.Fatalf("expected non-empty run ID")
	}
	for i, id := range results {
		if id != expectedID {
			t.Fatalf("goroutine %d got run ID %s, expected %s", i, id, expectedID)
		}
	}

	// Kiểm tra Database chỉ có đúng 1 row duy nhất
	var rowCount int
	err := db.QueryRow("SELECT COUNT(1) FROM agent_runs WHERE tenant_id = ? AND idempotency_key = ?", tenantID, idempKey).Scan(&rowCount)
	if err != nil {
		t.Fatalf("query count failed: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("database has %d rows, expected exactly 1", rowCount)
	}
}

func TestJobService_CancelConsistencyRace(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	runner := &dummyRunner{delay: 150 * time.Millisecond}
	svc := NewJobService(runRepo, runner)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-cancel-race",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	run, err := svc.SubmitRun(ctx, "Long Running Task to Cancel", domain.AgentRunOptions{MaxSteps: 5}, "")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Đợi worker chạy
	time.Sleep(30 * time.Millisecond)

	// User cancel
	if err := svc.CancelRunForTenant(ctx, "tenant-cancel-race", run.ID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	// Đợi cho worker hoàn thành
	time.Sleep(200 * time.Millisecond)

	finalRun, err := svc.GetRunForTenant(ctx, "tenant-cancel-race", run.ID)
	if err != nil {
		t.Fatalf("get run failed: %v", err)
	}

	if finalRun.Status != domain.RunStatusCancelled {
		t.Fatalf("cancel consistency violated: expected cancelled, got: %s", finalRun.Status)
	}
}

func TestJobService_ShutdownLifecycle(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	runner := &dummyRunner{delay: 80 * time.Millisecond}
	svc := NewJobService(runRepo, runner)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-shutdown",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	run, err := svc.SubmitRun(ctx, "Task before shutdown", domain.AgentRunOptions{MaxSteps: 5}, "")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Shutdown với timeout 500ms
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := svc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Cố submit sau khi đã shutdown -> phải bị từ chối
	_, err = svc.SubmitRun(ctx, "Task after shutdown", domain.AgentRunOptions{MaxSteps: 5}, "")
	if err == nil {
		t.Fatalf("submit after shutdown should be rejected!")
	}

	// Run trước đó đã hoàn tất hoặc bị hủy sạch sẽ
	finalRun, err := svc.GetRunForTenant(ctx, "tenant-shutdown", run.ID)
	if err != nil {
		t.Fatalf("get run failed: %v", err)
	}
	if finalRun.Status != domain.RunStatusCompleted && finalRun.Status != domain.RunStatusCancelled {
		t.Fatalf("expected completed or cancelled after shutdown, got: %s", finalRun.Status)
	}
}

func TestJobService_RecoverPendingRuns(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	runner := &dummyRunner{delay: 20 * time.Millisecond}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)

	ctx := context.Background()

	secCtx := domain.SecurityContextFromTenantIdentity(domain.TenantIdentity{
		TenantID: "tenant-recov",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	// 1. Tác vụ queued -> mong đợi requeue
	runQueued := &domain.AgentRun{
		ID:              "run_queued_restart",
		TenantID:        "tenant-recov",
		Goal:            "Requeue Task",
		Status:          domain.RunStatusQueued,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		SecurityContext: secCtx,
	}
	_ = runRepo.Create(ctx, runQueued)

	// 2. Tác vụ running có checkpoint -> mong đợi recovering
	runWithCP := &domain.AgentRun{
		ID:              "run_running_checkpoint",
		TenantID:        "tenant-recov",
		Goal:            "Recoverable Task",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		SecurityContext: secCtx,
	}
	_ = runRepo.Create(ctx, runWithCP)
	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-recov",
		TaskID:    runWithCP.ID,
		StepIndex: 2,
		StateSnapshot: domain.AgentState{
			TaskID:      runWithCP.ID,
			CurrentStep: 2,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Recoverable Task"},
			},
		},
	})

	// 3. Tác vụ running KHÔNG có checkpoint -> mong đợi interrupted (server_restart)
	runNoCP := &domain.AgentRun{
		ID:              "run_running_no_checkpoint",
		TenantID:        "tenant-recov",
		Goal:            "Unrecoverable Task",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		SecurityContext: secCtx,
	}
	_ = runRepo.Create(ctx, runNoCP)

	// 4. Tác vụ waiting_for_approval -> giữ nguyên
	runWaiting := &domain.AgentRun{
		ID:              "run_waiting_approval",
		TenantID:        "tenant-recov",
		Goal:            "Waiting Approval Task",
		Status:          domain.RunStatusWaitingForApproval,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		SecurityContext: secCtx,
	}
	_ = runRepo.Create(ctx, runWaiting)

	// 5. Tác vụ Legacy KHÔNG CÓ SecurityContext -> Bắt buộc Fail Closed (reauthorization_required)
	runLegacy := &domain.AgentRun{
		ID:        "run_legacy_no_secctx",
		TenantID:  "tenant-recov",
		Goal:      "Legacy Run without SecurityContext",
		Status:    domain.RunStatusRunning,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
		MaxSteps:  5,
	}
	_ = runRepo.Create(ctx, runLegacy)

	// Kích hoạt phục hồi
	processed, err := svc.RecoverPendingRuns(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingRuns failed: %v", err)
	}

	if len(processed) < 5 {
		t.Fatalf("expected at least 5 processed runs, got: %d", len(processed))
	}

	// Đợi các worker nền chạy
	time.Sleep(80 * time.Millisecond)

	// Kiểm tra runNoCP phải là interrupted với StopReasonServerRestart
	resNoCP, err := runRepo.Get(ctx, runNoCP.ID)
	if err != nil {
		t.Fatalf("get runNoCP failed: %v", err)
	}
	if resNoCP.Status != domain.RunStatusInterrupted || resNoCP.StopReason != domain.StopReasonServerRestart {
		t.Fatalf("expected interrupted with server_restart, got status=%s, reason=%s", resNoCP.Status, resNoCP.StopReason)
	}

	// Kiểm tra runLegacy phải là interrupted với StopReasonReauthorizationRequired (ISSUE 5)
	resLegacy, err := runRepo.Get(ctx, runLegacy.ID)
	if err != nil {
		t.Fatalf("get runLegacy failed: %v", err)
	}
	if resLegacy.Status != domain.RunStatusInterrupted || resLegacy.StopReason != domain.StopReasonReauthorizationRequired {
		t.Fatalf("expected legacy run to be interrupted with reauthorization_required, got status=%s, reason=%s", resLegacy.Status, resLegacy.StopReason)
	}

	// Kiểm tra runWaiting vẫn là waiting_for_approval
	resWaiting, err := runRepo.Get(ctx, runWaiting.ID)
	if err != nil {
		t.Fatalf("get runWaiting failed: %v", err)
	}
	if resWaiting.Status != domain.RunStatusWaitingForApproval {
		t.Fatalf("expected waiting_for_approval, got: %s", resWaiting.Status)
	}

	// Kiểm tra sự kiện server_restart_detected được ghi
	eventsNoCP, _ := runRepo.GetEvents(ctx, runNoCP.ID, 0)
	foundRestartEv := false
	for _, ev := range eventsNoCP {
		if ev.Kind == "server_restart_detected" {
			foundRestartEv = true
			break
		}
	}
	if !foundRestartEv {
		t.Fatalf("expected server_restart_detected event for runNoCP")
	}
}

func TestJobService_RealResumeWithCheckpoint(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	runner := &dummyRunner{delay: 20 * time.Millisecond}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-resume",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	// Tạo một tác vụ đã bị interrupted hoặc cancelled
	origRun := &domain.AgentRun{
		ID:        "orig_run_123",
		TenantID:  "tenant-resume",
		Goal:      "Refactor Auth Module",
		Status:    domain.RunStatusCancelled,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
		MaxSteps:  10,
	}
	_ = runRepo.Create(ctx, origRun)

	// Lưu Checkpoint đại diện cho trạng thái của run
	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-resume",
		TaskID:    origRun.ID,
		StepIndex: 3,
		StateSnapshot: domain.AgentState{
			TaskID:      origRun.ID,
			CurrentStep: 3,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Refactor Auth Module"},
				{Role: "assistant", Content: "Inspecting files..."},
			},
		},
	})

	// Thực hiện Resume thật
	resumedRun, err := svc.ResumeRunForTenant(ctx, "tenant-resume", origRun.ID, "Please fix remaining linter errors")
	if err != nil {
		t.Fatalf("ResumeRunForTenant failed: %v", err)
	}

	// Kiểm tra liên kết cha - con
	if resumedRun.ParentRunID != origRun.ID || resumedRun.ResumeFromRunID != origRun.ID {
		t.Fatalf("expected ParentRunID and ResumeFromRunID == %s, got parent=%s, resume=%s",
			origRun.ID, resumedRun.ParentRunID, resumedRun.ResumeFromRunID)
	}
	if resumedRun.TenantID != "tenant-resume" {
		t.Fatalf("expected tenant-resume, got: %s", resumedRun.TenantID)
	}

	time.Sleep(60 * time.Millisecond)

	finalRun, err := svc.GetRunForTenant(ctx, "tenant-resume", resumedRun.ID)
	if err != nil {
		t.Fatalf("get resumed run failed: %v", err)
	}
	if finalRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected completed status, got: %s", finalRun.Status)
	}
}

func TestJobService_ResumeWithoutCheckpointFails(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	runner := &dummyRunner{}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-no-cp",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	origRun := &domain.AgentRun{
		ID:        "orig_run_no_cp",
		TenantID:  "tenant-no-cp",
		Goal:      "Deploy k8s",
		Status:    domain.RunStatusFailed,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
	}
	_ = runRepo.Create(ctx, origRun)

	// Thử resume nhưng không có checkpoint trong DB -> phải trả về run_not_resumable
	_, err := svc.ResumeRunForTenant(ctx, "tenant-no-cp", origRun.ID, "Try again")
	if err == nil {
		t.Fatalf("resume without checkpoint should fail")
	}
	if !strings.Contains(err.Error(), "run_not_resumable") {
		t.Fatalf("expected error containing 'run_not_resumable', got: %v", err)
	}
}

func TestJobService_TenantIsolation(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	runner := &dummyRunner{delay: 50 * time.Millisecond}
	svc := NewJobService(repo, runner)

	ctxA := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-A",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	ctxB := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-B",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	runA, err := svc.SubmitRun(ctxA, "Run of Tenant A", domain.AgentRunOptions{MaxSteps: 5}, "")
	if err != nil {
		t.Fatalf("submit A failed: %v", err)
	}

	// 1. Tenant A đọc được
	gotA, err := svc.GetRunForTenant(ctxA, "tenant-A", runA.ID)
	if err != nil || gotA == nil {
		t.Fatalf("tenant A should get own run: %v", err)
	}

	// 2. Tenant B đọc run A -> từ chối
	gotB, err := svc.GetRunForTenant(ctxB, "tenant-B", runA.ID)
	if err == nil || gotB != nil {
		t.Fatalf("tenant B should NOT get run A")
	}

	// 3. Tenant B cancel run A -> từ chối
	err = svc.CancelRunForTenant(ctxB, "tenant-B", runA.ID)
	if err == nil {
		t.Fatalf("tenant B should NOT cancel run A")
	}

	// 4. Tenant B resume run A -> từ chối
	_, err = svc.ResumeRunForTenant(ctxB, "tenant-B", runA.ID, "feedback")
	if err == nil {
		t.Fatalf("tenant B should NOT resume run A")
	}

	// 5. Tenant B stream events run A -> từ chối
	_, _, err = svc.SubscribeEventsForTenant(ctxB, "tenant-B", runA.ID)
	if err == nil {
		t.Fatalf("tenant B should NOT subscribe events of run A")
	}
}
