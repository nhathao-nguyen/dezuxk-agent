package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/policy"

	_ "github.com/mattn/go-sqlite3"
)

// countingFakeRunner đếm số lần gọi Run bằng atomic counter
type countingFakeRunner struct {
	callCount int32
	delay     time.Duration
}

func (c *countingFakeRunner) Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error) {
	atomic.AddInt32(&c.callCount, 1)
	if c.delay > 0 {
		select {
		case <-ctx.Done():
			return &domain.AgentState{
				TaskID:      opts.TaskID,
				Goal:        goal,
				IsCompleted: false,
				StopReason:  domain.StopReasonCancelled,
				Error:       ctx.Err().Error(),
			}, ctx.Err()
		case <-time.After(c.delay):
		}
	}

	return &domain.AgentState{
		TaskID:      opts.TaskID,
		Goal:        goal,
		IsCompleted: true,
		StopReason:  domain.StopReasonCompleted,
		FinalAnswer: "Done: " + goal,
		CurrentStep: 1,
	}, nil
}

// Test 1: P0 Atomic Job Claim Integration
// 2 JobServices (Worker A và Worker B) cạnh tranh cùng 1 queued run.
// Runner chỉ được gọi ĐÚNG 1 LẦN. Worker thứ hai phải dừng ngay, không execute fallback.
func TestHardening_AtomicJobClaim(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()

	// Tạo 1 queued run trong repository
	runID := "run_atomic_claim_race"
	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  "tenant-race-claim",
		Goal:      "Atomic Claim Race Test",
		Status:    domain.RunStatusQueued,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
		MaxSteps:  5,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	fakeRunner := &countingFakeRunner{delay: 60 * time.Millisecond}

	// 2 worker instances riêng biệt với workerID khác nhau
	workerA := NewJobService(runRepo, fakeRunner)
	workerB := NewJobService(runRepo, fakeRunner)

	if workerA.workerID == workerB.workerID {
		t.Fatalf("workers must have different worker IDs, got same: %s", workerA.workerID)
	}

	var wg sync.WaitGroup
	startCh := make(chan struct{})

	// Cả 2 worker đồng thời tranh chấp chạy executeBackground
	opts := domain.AgentRunOptions{
		TaskID:    runID,
		Model:     run.Model,
		Workspace: run.Workspace,
		MaxSteps:  5,
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-startCh
		workerA.executeBackground(ctx, run, opts)
	}()
	go func() {
		defer wg.Done()
		<-startCh
		workerB.executeBackground(ctx, run, opts)
	}()

	close(startCh)
	wg.Wait()

	// Runner chỉ được gọi chính xác 1 lần duy nhất
	calls := atomic.LoadInt32(&fakeRunner.callCount)
	if calls != 1 {
		t.Fatalf("CRITICAL RACE: fakeRunner was executed %d times, expected exactly 1!", calls)
	}

	// Kiểm tra trạng thái hoàn thành trong DB
	finalRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("failed to get run from repo: %v", err)
	}
	if finalRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected completed status, got: %s", finalRun.Status)
	}
	if finalRun.WorkerID != workerA.workerID && finalRun.WorkerID != workerB.workerID {
		t.Fatalf("unexpected worker ID in DB: %s", finalRun.WorkerID)
	}
}

// Test 2: P0 Unify Agent Run ID & Checkpoint Task ID Integration
// JobService -> REAL Runner -> Checkpoint saved -> verify checkpoint TaskID == run.ID
func TestHardening_UnifyAgentRunIDAndCheckpointTaskID(t *testing.T) {
	tempDir := t.TempDir()
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Creating file",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_cp_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "write_file",
										Arguments: `{"path": "unify_test.txt", "content": "checking task id"}`,
									},
								},
							},
						},
					},
				},
			},
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Done writing file.",
						},
					},
				},
			},
		},
	}

	realRunner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	realRunner.SetCheckpointRepository(cpRepo)

	jobSvc := NewJobService(runRepo, realRunner)
	jobSvc.SetCheckpointRepository(cpRepo)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-unify-test",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	run, err := jobSvc.SubmitRun(ctx, "Unify Identity Goal", domain.AgentRunOptions{
		Workspace: tempDir,
		MaxSteps:  5,
	}, "")
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}

	// Đợi tác vụ thực thi xong
	var finalRun *domain.AgentRun
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		r, gErr := jobSvc.GetRun(ctx, run.ID)
		if gErr == nil && (r.Status == domain.RunStatusCompleted || r.Status == domain.RunStatusFailed) {
			finalRun = r
			break
		}
	}

	if finalRun == nil || finalRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected run completed, got: %+v", finalRun)
	}

	// Lấy Checkpoint từ repository bằng run.ID
	cp, err := cpRepo.GetLatestCheckpoint(ctx, run.ID)
	if err != nil {
		t.Fatalf("failed to get checkpoint with run.ID: %v", err)
	}
	if cp == nil {
		t.Fatalf("CRITICAL: Checkpoint was NOT found for run.ID (%s)!", run.ID)
	}

	// Xác nhận checkpoint.TaskID khớp với run.ID
	if cp.TaskID != run.ID {
		t.Fatalf("MISMATCH: checkpoint.TaskID (%s) != run.ID (%s)", cp.TaskID, run.ID)
	}
	if cp.TenantID != "tenant-unify-test" {
		t.Fatalf("checkpoint.TenantID (%s) != expected tenant-unify-test", cp.TenantID)
	}
}

// Test 3: P0 Real Runner Recovery from Saved Checkpoint
// Real runner lưu checkpoint -> giả lập server restart -> RecoverPendingRuns tìm thấy và resume thành công
func TestHardening_RealRunnerRecoveryAfterRestart(t *testing.T) {
	tempDir := t.TempDir()
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	// Runner 1: chạy bước 1 tạo file và lưu checkpoint, sau đó trả lỗi mô hình để dừng
	mockChat1 := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Step 1: creating file",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_recov_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "write_file",
										Arguments: `{"path": "recov_step1.txt", "content": "step 1 completed"}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	runner1 := NewRunner(mockChat1, reg, &MockApprovalProvider{ApproveAll: true})
	runner1.SetCheckpointRepository(cpRepo)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-recov-restart",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	// Thực thi bước 1 trực tiếp qua real runner để lưu checkpoint thật
	runID := "run_restart_recovery_real"
	opts := domain.AgentRunOptions{
		TaskID:    runID,
		Workspace: tempDir,
		MaxSteps:  5,
	}

	state1, _ := runner1.Run(ctx, "Multi-step task for recovery", opts)
	if state1 == nil || state1.CurrentStep < 1 {
		t.Fatalf("expected runner1 to complete at least step 1")
	}

	// Kiểm tra checkpoint thật đã được lưu trong DB với TaskID == runID
	cp, err := cpRepo.GetLatestCheckpoint(ctx, runID)
	if err != nil || cp == nil {
		t.Fatalf("real checkpoint was not saved: %v", err)
	}
	if cp.TaskID != runID {
		t.Fatalf("checkpoint TaskID %s != %s", cp.TaskID, runID)
	}

	// Giả lập server crash/restart: run đang ở trạng thái 'running' trong DB
	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  "tenant-recov-restart",
		Goal:      "Multi-step task for recovery",
		Status:    domain.RunStatusRunning,
		Model:     "gemini-3.8-flash",
		Workspace: tempDir,
		MaxSteps:  5,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to insert running run: %v", err)
	}

	// Khởi tạo Runner mới cho instance server mới sau restart
	mockChat2 := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Step 2: Task finished successfully after restart.",
						},
					},
				},
			},
		},
	}
	runner2 := NewRunner(mockChat2, reg, &MockApprovalProvider{ApproveAll: true})
	runner2.SetCheckpointRepository(cpRepo)

	jobSvc2 := NewJobService(runRepo, runner2)
	jobSvc2.SetCheckpointRepository(cpRepo)

	// Kích hoạt RecoverPendingRuns
	processed, err := jobSvc2.RecoverPendingRuns(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingRuns failed: %v", err)
	}
	if len(processed) == 0 {
		t.Fatalf("expected at least 1 recovered run, got 0")
	}

	// Đợi background worker của server mới chạy xong
	var finalRun *domain.AgentRun
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		r, gErr := runRepo.Get(ctx, runID)
		if gErr == nil && (r.Status == domain.RunStatusCompleted || r.Status == domain.RunStatusFailed) {
			finalRun = r
			break
		}
	}

	if finalRun == nil || finalRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected run to complete after recovery, got status: %v", finalRun)
	}

	// Xác nhận có sự kiện run_recovered trong audit log
	events, err := runRepo.GetEvents(ctx, runID, 0)
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}
	foundRecovEvent := false
	for _, ev := range events {
		if ev.Kind == "run_recovered" {
			foundRecovEvent = true
			break
		}
	}
	if !foundRecovEvent {
		t.Fatalf("expected run_recovered event in event stream")
	}
}

// Test 4: P0 Preserve Full Tenant Security Context Integration
// TenantIdentity với restrictions -> submit run -> restored trong worker -> PolicyEngine cấm run_command
func TestHardening_PreserveSecurityContextInWorker(t *testing.T) {
	tempDir := t.TempDir()
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	// Mô hình cố tình gọi run_command bị cấm
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Attempting shell command",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_sec_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "run_command",
										Arguments: `{"command": "whoami"}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	policyEng := policy.NewPolicyEngine(reg, &MockApprovalProvider{ApproveAll: true})
	realRunner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	realRunner.SetPolicyEngine(policyEng)

	jobSvc := NewJobService(runRepo, realRunner)

	// TenantIdentity bị giới hạn: KHÔNG có quyền shell (AllowShell=false), chỉ được dùng read_file
	restrictedIdentity := domain.TenantIdentity{
		TenantID:              "tenant-restricted-sec",
		KeyID:                 "key-sec-test-777",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent}, // KHÔNG có ScopeShell
		AllowedTools:          []string{"read_file"},      // KHÔNG có run_command
		AllowShell:            false,                      // Cấm shell
		RequireApproval:       false,
		AllowedWorkspaceRoots: []string{tempDir},
		MaxToolRuntime:        5 * time.Second,
	}

	ctx := domain.ContextWithTenantIdentity(context.Background(), restrictedIdentity)

	run, err := jobSvc.SubmitRun(ctx, "Restricted security test goal", domain.AgentRunOptions{
		Workspace: tempDir,
		MaxSteps:  3,
	}, "")
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}

	// Đợi background worker chạy
	time.Sleep(200 * time.Millisecond)

	// Đọc run từ DB kiểm tra SecurityContext đã được persist bền vững
	persistedRun, err := runRepo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}
	if persistedRun.SecurityContext == nil {
		t.Fatalf("CRITICAL SECURITY BUG: SecurityContext was NOT persisted in DB!")
	}
	if persistedRun.SecurityContext.AllowShell != false {
		t.Fatalf("persisted SecurityContext AllowShell must be false")
	}
	if len(persistedRun.SecurityContext.AllowedTools) != 1 || persistedRun.SecurityContext.AllowedTools[0] != "read_file" {
		t.Fatalf("persisted SecurityContext AllowedTools mismatch: %+v", persistedRun.SecurityContext.AllowedTools)
	}

	// Kiểm tra các sự kiện: run_command phải bị Policy Engine từ chối
	events, err := runRepo.GetEvents(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}

	foundDenied := false
	for _, ev := range events {
		if ev.Kind == "tool_end" || ev.Kind == "tool_error" || ev.Kind == "error" || ev.Kind == "thinking" {
			if strings.Contains(ev.Message, "không được phép") || strings.Contains(ev.Message, "chính sách") || strings.Contains(ev.Message, "LỖI THỰC THI") || strings.Contains(ev.Message, "LỖI CHÍNH SÁCH BẢO MẬT") {
				foundDenied = true
				break
			}
		}
	}
	if !foundDenied {
		t.Fatalf("expected tool execution error denying run_command in events, got: %+v", events)
	}
}

// Test 7: P0 Strict Checkpoint Cross-Tenant Isolation
// Tenant B không được phép đọc, liệt kê hoặc xóa checkpoint của Tenant A
func TestHardening_CheckpointStrictTenantIsolation(t *testing.T) {
	_, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	ctxA := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-owner-A",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})
	ctxB := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: "tenant-attacker-B",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	taskID := "task_isolated_123"

	// Tenant A lưu checkpoint
	err := cpRepo.SaveCheckpoint(ctxA, &domain.AgentCheckpoint{
		TenantID:  "tenant-owner-A",
		TaskID:    taskID,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      taskID,
			CurrentStep: 1,
			Goal:        "Confidential Data A",
		},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to save checkpoint: %v", err)
	}

	// 1. Tenant A đọc được checkpoint của mình
	cpA, err := cpRepo.GetLatestCheckpoint(ctxA, taskID)
	if err != nil || cpA == nil {
		t.Fatalf("tenant A should find own checkpoint, err: %v", err)
	}
	if cpA.StateSnapshot.Goal != "Confidential Data A" {
		t.Fatalf("unexpected goal in snapshot: %s", cpA.StateSnapshot.Goal)
	}

	// 2. Tenant B cố gắng đọc checkpoint của A -> PHẢI trả về nil / không thấy
	cpB, err := cpRepo.GetLatestCheckpoint(ctxB, taskID)
	if err == nil && cpB != nil {
		t.Fatalf("CRITICAL SECURITY BUG: Tenant B was able to read Tenant A's checkpoint! %+v", cpB)
	}

	// 3. Tenant B cố gắng List Checkpoints của A -> PHẢI trả về rỗng
	listB, err := cpRepo.ListCheckpoints(ctxB, taskID)
	if err == nil && len(listB) > 0 {
		t.Fatalf("CRITICAL SECURITY BUG: Tenant B was able to list Tenant A's checkpoints! count=%d", len(listB))
	}

	// 4. Tenant B cố gắng Delete Checkpoint của A -> không được ảnh hưởng đến A
	_ = cpRepo.DeleteCheckpoints(ctxB, taskID)

	// Tenant A vẫn phải đọc được checkpoint nguyên vẹn
	cpAAfter, err := cpRepo.GetLatestCheckpoint(ctxA, taskID)
	if err != nil || cpAAfter == nil {
		t.Fatalf("checkpoint of Tenant A was illegally deleted by Tenant B!")
	}
}

// Test 8: P1 Claim Lease Expiry
// Worker 1 giữ lease active -> Worker 2 không claim được. Lease hết hạn -> Worker 2 claim thành công.
func TestHardening_ClaimLeaseExpiry(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	runID := "run_lease_expiry_test"

	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  "tenant-lease",
		Goal:      "Test Lease Expiration",
		Status:    domain.RunStatusQueued,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
		MaxSteps:  5,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Worker 1 claim với lease ngắn 100ms
	claimed1, err := runRepo.ClaimRun(ctx, runID, "worker-1", 100*time.Millisecond)
	if err != nil || !claimed1 {
		t.Fatalf("worker-1 initial claim failed: %v", err)
	}

	// Ngay lập tức Worker 2 cố claim -> PHẢI thất bại vì lease còn hiệu lực
	claimed2, err := runRepo.ClaimRun(ctx, runID, "worker-2", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("worker-2 claim error: %v", err)
	}
	if claimed2 {
		t.Fatalf("worker-2 should NOT claim while lease is active")
	}

	// Đợi cho lease của worker-1 hết hạn (> 100ms)
	time.Sleep(150 * time.Millisecond)

	// Bây giờ Worker 2 cố claim lại -> PHẢI thành công vì lease đã expired
	claimed2After, err := runRepo.ClaimRun(ctx, runID, "worker-2", 1*time.Second)
	if err != nil || !claimed2After {
		t.Fatalf("worker-2 should successfully reclaim after lease expired: claimed=%v, err=%v", claimed2After, err)
	}

	// Kiểm tra worker_id hiện tại là worker-2
	current, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}
	if current.WorkerID != "worker-2" {
		t.Fatalf("expected current worker_id to be worker-2, got: %s", current.WorkerID)
	}
}

// Test 9: P1 Heartbeat Renewal Correctness
// Owner worker renew được lease; worker khác không renew được.
func TestHardening_HeartbeatRenewal(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	runID := "run_heartbeat_renewal_test"

	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  "tenant-hb",
		Goal:      "Test Heartbeat Renewal",
		Status:    domain.RunStatusQueued,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
		MaxSteps:  5,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Worker 1 claim
	claimed, err := runRepo.ClaimRun(ctx, runID, "worker-1", 500*time.Millisecond)
	if err != nil || !claimed {
		t.Fatalf("claim failed: %v", err)
	}

	// Worker 2 cố renew lease của Worker 1 -> PHẢI false
	renewedFake, err := runRepo.RenewLease(ctx, runID, "worker-impostor", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error on renew lease: %v", err)
	}
	if renewedFake {
		t.Fatalf("impostor worker should NOT be able to renew lease")
	}

	// Worker 1 renew lease của chính mình -> PHẢI true
	renewedOwner, err := runRepo.RenewLease(ctx, runID, "worker-1", 10*time.Second)
	if err != nil || !renewedOwner {
		t.Fatalf("owner worker failed to renew lease: %v", err)
	}

	// Kiểm tra lease_until trong DB đã được kéo dài
	inDB, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}
	if inDB.LeaseUntil == nil || inDB.LeaseUntil.Before(time.Now().Add(5*time.Second)) {
		t.Fatalf("lease was not properly extended: %v", inDB.LeaseUntil)
	}
}

// Test 10: P1 Graceful Shutdown Race Hardening
// Vòng lặp SubmitRun và Shutdown đồng thời liên tục không gây panic, WaitGroup misuse hoặc treo.
func TestHardening_GracefulShutdownRace(t *testing.T) {
	for iteration := 0; iteration < 5; iteration++ {
		runRepo, _, db := newTestSqliteRepo(t)

		runner := &dummyRunner{delay: 20 * time.Millisecond}
		svc := NewJobService(runRepo, runner)

		ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
			TenantID: fmt.Sprintf("tenant-shut-%d", iteration),
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		})

		var wg sync.WaitGroup
		const submitters = 10

		// Goroutines liên tục submit job
		for i := 0; i < submitters; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				for j := 0; j < 5; j++ {
					_, _ = svc.SubmitRun(ctx, fmt.Sprintf("Job %d-%d", idx, j), domain.AgentRunOptions{MaxSteps: 2}, "")
					time.Sleep(2 * time.Millisecond)
				}
			}(i)
		}

		// Kích hoạt shutdown sau 10ms
		time.Sleep(10 * time.Millisecond)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_ = svc.Shutdown(shutdownCtx)
		cancel()

		wg.Wait()
		db.Close()
	}
}
