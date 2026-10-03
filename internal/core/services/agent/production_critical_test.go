package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

// ISSUE 1: Recovery Sweeper phát hiện và khôi phục tác vụ khi lease hết hạn
func TestRecoverySweeper_ExpiredLeaseEventuallyRecovered(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	secCtx := domain.SecurityContextFromTenantIdentity(domain.TenantIdentity{
		TenantID: "tenant-sweeper",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	runID := "run_sweeper_lease_expired"
	activeLease := now.Add(5 * time.Minute)

	// 1. Tạo tác vụ đang chạy bởi Worker A với lease còn hiệu lực
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-sweeper",
		Goal:            "Sweeper Recovery Task",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		WorkerID:        "worker-A-crashed",
		LeaseUntil:      &activeLease,
		SecurityContext: secCtx,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Lưu checkpoint hợp lệ
	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-sweeper",
		TaskID:    runID,
		StepIndex: 2,
		StateSnapshot: domain.AgentState{
			TaskID:      runID,
			CurrentStep: 2,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Task in progress"},
			},
		},
	})

	runner := &dummyRunner{delay: 10 * time.Millisecond}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)

	// 2. Recovery Pass #1: Lease còn hiệu lực -> Bắt buộc SKIP, không đụng vào
	processed1, err := svc.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}
	for _, p := range processed1 {
		if p.ID == runID {
			t.Fatalf("Pass #1 must skip run with active lease, but got processed: %s", runID)
		}
	}

	// 3. Expire lease: cập nhật trực tiếp DB giả lập lease quá hạn
	expiredLease := time.Now().Add(-10 * time.Second)
	_, err = db.Exec("UPDATE agent_runs SET lease_until = ? WHERE id = ?", expiredLease, runID)
	if err != nil {
		t.Fatalf("failed to expire lease in db: %v", err)
	}

	// 4. Recovery Pass #2: Lease đã hết hạn -> Sweeper phải reclaim và khôi phục tác vụ
	processed2, err := svc.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns pass #2 failed: %v", err)
	}
	found := false
	for _, p := range processed2 {
		if p.ID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Pass #2 expected to reclaim expired lease run %s", runID)
	}

	// Đợi background worker thực thi hoàn tất
	time.Sleep(100 * time.Millisecond)

	finalRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("failed to get reclaimed run: %v", err)
	}
	if finalRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected reclaimed run to complete, got status: %s", finalRun.Status)
	}
}

// ISSUE 2: Fencing token - Stale worker write bị từ chối, không overwrite canonical state
func TestFencing_StaleWorkerCannotFinalize(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	secCtx := domain.SecurityContextFromTenantIdentity(domain.TenantIdentity{
		TenantID: "tenant-fence",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	runID := "run_fencing_test"
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-fence",
		Goal:            "Fencing Test Task",
		Status:          domain.RunStatusQueued,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		SecurityContext: secCtx,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// 1. Worker A claim run: nhận generation 1
	claimedA, err := runRepo.ClaimRun(ctx, runID, "worker-A", 100*time.Millisecond)
	if err != nil || !claimedA {
		t.Fatalf("worker-A claim failed: %v", err)
	}
	runA, _ := runRepo.Get(ctx, runID)
	if runA.ClaimGeneration != 1 || runA.WorkerID != "worker-A" {
		t.Fatalf("expected gen 1 worker-A, got gen %d worker %s", runA.ClaimGeneration, runA.WorkerID)
	}

	// 2. Giả lập Worker A bị lag mạng/treo, lease hết hạn. Worker B claim lại run: nhận generation 2
	_, _ = db.Exec("UPDATE agent_runs SET lease_until = ? WHERE id = ?", time.Now().Add(-10*time.Second), runID)
	claimedB, err := runRepo.ClaimRun(ctx, runID, "worker-B", 60*time.Second)
	if err != nil || !claimedB {
		t.Fatalf("worker-B claim failed: %v", err)
	}
	runB, _ := runRepo.Get(ctx, runID)
	if runB.ClaimGeneration != 2 || runB.WorkerID != "worker-B" {
		t.Fatalf("expected gen 2 worker-B, got gen %d worker %s", runB.ClaimGeneration, runB.WorkerID)
	}

	// 3. Worker A thức dậy và cố gắng ghi kết quả cuối cùng với generation cũ (1)
	staleRunA := *runA
	staleRunA.Status = domain.RunStatusCompleted
	staleRunA.FinalAnswer = "STALE OVERWRITE FROM WORKER A"
	updatedA, errA := runRepo.UpdateOwned(ctx, &staleRunA, "worker-A", 1)
	if errA != nil {
		t.Fatalf("unexpected db error on stale write: %v", errA)
	}
	if updatedA {
		t.Fatalf("FENCING VIOLATION: Stale Worker A write was accepted! RowsAffected must be 0")
	}

	// Kiểm tra dữ liệu DB: kết quả của Worker A KHÔNG ĐƯỢC ghi đè
	currentRun, _ := runRepo.Get(ctx, runID)
	if currentRun.FinalAnswer == "STALE OVERWRITE FROM WORKER A" {
		t.Fatalf("FENCING VIOLATION: Database was overwritten by stale Worker A!")
	}

	// 4. Worker B hoàn thành công việc hợp lệ với generation 2 -> Cho phép ghi
	validRunB := *runB
	validRunB.Status = domain.RunStatusCompleted
	validRunB.FinalAnswer = "CANONICAL RESULT FROM WORKER B"
	updatedB, errB := runRepo.UpdateOwned(ctx, &validRunB, "worker-B", 2)
	if errB != nil || !updatedB {
		t.Fatalf("Worker B valid write was rejected: %v, updated=%v", errB, updatedB)
	}

	finalRun, _ := runRepo.Get(ctx, runID)
	if finalRun.Status != domain.RunStatusCompleted || finalRun.FinalAnswer != "CANONICAL RESULT FROM WORKER B" {
		t.Fatalf("expected Worker B canonical result in db, got: %v", finalRun)
	}
}

// ISSUE 3: Resume không được kế thừa quyền admin cũ, bắt buộc re-authorize theo caller hiện tại
func TestResume_CannotEscalatePrivileges(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	// 1. Admin tạo tác vụ với đầy đủ quyền tối cao
	adminSecCtx := &domain.AgentSecurityContext{
		TenantID:        "tenant-multi",
		Role:            "admin",
		Scopes:          []string{domain.ScopeAgent, domain.ScopeShell, domain.ScopeAdmin},
		AllowedModels:   []string{"*"},
		AllowedTools:    []string{"*"},
		AllowShell:      true,
		RequireApproval: false,
		EnforceSandbox:  false,
		AutoMergeAllowed: true,
	}

	runID := "run_admin_escalation_target"
	adminRun := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-multi",
		Goal:            "Admin Sensitive Task",
		Status:          domain.RunStatusFailed,
		Model:           "gemini-1.5-pro",
		Workspace:       "/data/projects",
		SecurityContext: adminSecCtx,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, adminRun); err != nil {
		t.Fatalf("failed to create admin run: %v", err)
	}

	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-multi",
		TaskID:    runID,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      runID,
			CurrentStep: 1,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Run privileged command"},
			},
		},
	})

	runner := &dummyRunner{delay: 10 * time.Millisecond}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)

	// 2. Caller thường với quyền hạn bị hạn chế (Restricted Key) gọi Resume
	restrictedCaller := domain.TenantIdentity{
		TenantID:              "tenant-multi",
		KeyID:                 "vk-restricted-001",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedModels:         []string{"gemini-1.5-pro"},
		AllowedTools:          []string{"read_file"},
		AllowedWorkspaceRoots: []string{"/data/projects"},
		AllowShell:            false,
		RequireApproval:       true,
		EnforceSandbox:        true,
		AutoMergeAllowed:      false,
	}
	callerCtx := domain.ContextWithTenantIdentity(ctx, restrictedCaller)

	resumedRun, err := svc.ResumeRunForTenant(callerCtx, "tenant-multi", runID, "Try resuming safely")
	if err != nil {
		t.Fatalf("ResumeRunForTenant failed: %v", err)
	}

	// 3. Kiểm tra SecurityContext của resumed run: Phải là phép giao (Intersection), TUYỆT ĐỐI không kế thừa quyền admin
	resSec := resumedRun.SecurityContext
	if resSec == nil {
		t.Fatalf("resumed run must have security context")
	}

	if resSec.Role != "user" {
		t.Fatalf("PRIVILEGE ESCALATION: resumed run has role=%s, expected 'user'", resSec.Role)
	}
	if resSec.AllowShell != false {
		t.Fatalf("PRIVILEGE ESCALATION: resumed run has AllowShell=true, expected false")
	}
	if resSec.RequireApproval != true {
		t.Fatalf("PRIVILEGE ESCALATION: resumed run has RequireApproval=false, expected true")
	}
	if resSec.EnforceSandbox != true {
		t.Fatalf("PRIVILEGE ESCALATION: resumed run has EnforceSandbox=false, expected true")
	}
	if resSec.AutoMergeAllowed != false {
		t.Fatalf("PRIVILEGE ESCALATION: resumed run has AutoMergeAllowed=true, expected false")
	}

	// AllowedTools phải là ["read_file"], không phải wildcard "*"
	if len(resSec.AllowedTools) != 1 || resSec.AllowedTools[0] != "read_file" {
		t.Fatalf("PRIVILEGE ESCALATION: resumed run has AllowedTools=%v, expected ['read_file']", resSec.AllowedTools)
	}

	// Billing attribution phải ghi nhận đúng KeyID của caller hiện tại
	if resumedRun.BillingKeyID != "vk-restricted-001" {
		t.Fatalf("expected BillingKeyID to be caller's key 'vk-restricted-001', got: %s", resumedRun.BillingKeyID)
	}
}

// ISSUE 4: Async Agent Token Accounting & Quota Enforcement
func TestAsyncAgent_QuotaStopsFurtherModelRounds(t *testing.T) {
	keyRepo := session.NewMemoryKeyRepository()
	keySvc := services.NewKeyService(keyRepo, "")

	ctx := context.Background()

	// 1. Tạo Virtual API Key với hạn ngạch token thấp (MaxTokenQuota = 100)
	createdKey, err := keySvc.CreateKey(ctx, domain.CreateKeyRequest{
		TenantID:           "tenant-quota",
		Name:               "Low Quota Key",
		Role:               "user",
		Scopes:             []string{domain.ScopeAgent, domain.ScopeChat},
		DailyQuotaRequests: 1000,
		MaxTokenQuota:      100, // Chỉ cho phép tối đa 100 tokens
	})
	if err != nil {
		t.Fatalf("failed to create virtual key: %v", err)
	}

	runner := NewRunner(nil, nil, nil)
	runner.SetKeyUseCase(keySvc)

	// Mock: Đã tiêu thụ 120 tokens trên key (vượt hạn mức 100)
	err = keyRepo.RecordTokenUsage(ctx, createdKey.ID, 60, 60)
	if err != nil {
		t.Fatalf("failed to record token usage: %v", err)
	}

	// Chuẩn bị context có BillingIdentity gắn với key này
	runCtx := domain.ContextWithBillingIdentity(ctx, domain.BillingIdentity{
		TenantID: "tenant-quota",
		KeyID:    createdKey.ID,
	})

	// Thực thi qua Runner.Run: Bắt buộc dừng và trả về ErrTokenQuotaExceeded
	state, runErr := runner.Run(runCtx, "Perform round 2", domain.AgentRunOptions{
		TaskID: "task-quota-test",
	})

	if runErr == nil {
		t.Fatalf("expected Run to return error when quota exceeded, got nil")
	}
	if !errors.Is(runErr, domain.ErrTokenQuotaExceeded) {
		t.Fatalf("expected ErrTokenQuotaExceeded, got: %v", runErr)
	}
	if state.StopReason != domain.StopReasonQuotaExceeded {
		t.Fatalf("expected StopReasonQuotaExceeded, got: %s", state.StopReason)
	}
}

// ISSUE 5: Legacy run không có SecurityContext bắt buộc Fail Closed (reauthorization_required)
func TestLegacyRunWithoutSecurityContextFailsClosed(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	runID := "legacy_run_no_secctx_test"

	// Tạo run với SecurityContext = nil
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-legacy",
		Goal:            "Legacy Run Task",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-3.8-flash",
		Workspace:       ".",
		MaxSteps:        5,
		SecurityContext: nil, // KHÔNG CÓ SECURITY CONTEXT
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create legacy run: %v", err)
	}

	// Checkpoint có tồn tại
	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-legacy",
		TaskID:    runID,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      runID,
			CurrentStep: 1,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Legacy goal"},
			},
		},
	})

	runner := &dummyRunner{delay: 10 * time.Millisecond}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)

	// Quét recovery sweeper
	processed, err := svc.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}

	found := false
	for _, p := range processed {
		if p.ID == runID {
			found = true
			if p.Status != domain.RunStatusInterrupted {
				t.Fatalf("FAIL CLOSED VIOLATION: legacy run status is %s, expected interrupted", p.Status)
			}
			if p.StopReason != domain.StopReasonReauthorizationRequired {
				t.Fatalf("FAIL CLOSED VIOLATION: legacy run stop_reason is %s, expected reauthorization_required", p.StopReason)
			}
		}
	}
	if !found {
		t.Fatalf("expected legacy run to be processed and fail-closed")
	}

	// Kiểm tra trong DB
	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("failed to get run from db: %v", err)
	}
	if dbRun.Status != domain.RunStatusInterrupted || dbRun.StopReason != domain.StopReasonReauthorizationRequired {
		t.Fatalf("FAIL CLOSED VIOLATION in DB: status=%s, reason=%s", dbRun.Status, dbRun.StopReason)
	}
}

// Bổ sung: Bảo toàn ExecutionConfig trong suốt vòng đời và qua Recovery
func TestRecoveryPreservesExecutionOptions(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()
	runner := &dummyRunner{delay: 10 * time.Millisecond}
	svc := NewJobService(runRepo, runner)

	secCtx := domain.SecurityContextFromTenantIdentity(domain.TenantIdentity{
		TenantID: "tenant-exec",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})
	callerCtx := domain.ContextWithTenantIdentity(ctx, secCtx.ToTenantIdentity())

	opts := domain.AgentRunOptions{
		Model:                  "gemini-1.5-pro",
		Workspace:              "/var/workspace",
		MaxSteps:               18,
		MaxToolCalls:           25,
		MaxRepeatedCalls:       3,
		MaxExecutionDuration:   5 * time.Minute,
		MaxConsecutiveFailures: 4,
		Supervised:             true,
		RequireAction:          true,
		UseSandbox:             true,
		AutoMerge:              false,
		CustomPrompt:           "Custom system prompt instruction",
	}

	run, err := svc.SubmitRun(callerCtx, "Goal with custom options", opts, "")
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}

	// Đọc lại từ DB để kiểm tra tính bền bỉ của ExecutionConfig
	savedRun, err := runRepo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get run failed: %v", err)
	}

	if savedRun.ExecutionConfig == nil {
		t.Fatalf("ExecutionConfig was not persisted in database!")
	}
	if savedRun.ExecutionConfig.MaxToolCalls != 25 {
		t.Fatalf("expected MaxToolCalls=25, got %d", savedRun.ExecutionConfig.MaxToolCalls)
	}
	if !savedRun.ExecutionConfig.UseSandbox {
		t.Fatalf("expected UseSandbox=true, got false")
	}
	if savedRun.ExecutionConfig.CustomPrompt != "Custom system prompt instruction" {
		t.Fatalf("custom prompt mismatch")
	}

	// Khôi phục options
	reconstructed := reconstructOptions(savedRun, nil)
	if reconstructed.MaxToolCalls != 25 || !reconstructed.UseSandbox || reconstructed.CustomPrompt != "Custom system prompt instruction" {
		t.Fatalf("reconstructed options failed to preserve custom settings: %+v", reconstructed)
	}
}
