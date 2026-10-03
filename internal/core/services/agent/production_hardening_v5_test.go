package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

// ISSUE 1: Sweeper không được reclaim active lease, dù là cùng worker
func TestRecoverySweeper_DoesNotReclaimActiveLeaseSameWorker(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runner := &dummyRunner{}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)
	defer svc.Shutdown(ctx)

	runID := "run-sweeper-same-worker-active"
	activeLease := now.Add(10 * time.Minute)
	initialGen := int64(1)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Active Lease Same Worker",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-1.5-flash",
		WorkerID:        svc.workerID, // Cùng worker với sweeper
		ClaimGeneration: initialGen,
		LeaseUntil:      &activeLease,
		SecurityContext: &domain.AgentSecurityContext{
			TenantID: "tenant-v5",
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	processed, err := svc.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}
	for _, p := range processed {
		if p.ID == runID {
			t.Fatalf("BUG ISSUE 1: Sweeper đã reclaim tác vụ có lease đang active của cùng worker!")
		}
	}

	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dbRun.ClaimGeneration != initialGen {
		t.Fatalf("ClaimGeneration bị tăng sai: expected %d, got %d", initialGen, dbRun.ClaimGeneration)
	}
	if dbRun.Status != domain.RunStatusRunning {
		t.Fatalf("Status bị đổi sai: expected %s, got %s", domain.RunStatusRunning, dbRun.Status)
	}
}

func TestRecoverySweeper_DoesNotReclaimActiveLeaseOtherWorker(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runner := &dummyRunner{}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)
	defer svc.Shutdown(ctx)

	runID := "run-sweeper-other-worker-active"
	activeLease := now.Add(10 * time.Minute)
	initialGen := int64(1)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Active Lease Other Worker",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-1.5-flash",
		WorkerID:        "worker-foreign-node",
		ClaimGeneration: initialGen,
		LeaseUntil:      &activeLease,
		SecurityContext: &domain.AgentSecurityContext{
			TenantID: "tenant-v5",
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	processed, err := svc.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}
	for _, p := range processed {
		if p.ID == runID {
			t.Fatalf("Sweeper không được phép reclaim active lease của node khác")
		}
	}

	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dbRun.ClaimGeneration != initialGen {
		t.Fatalf("ClaimGeneration bị thay đổi: expected %d, got %d", initialGen, dbRun.ClaimGeneration)
	}
	if dbRun.WorkerID != "worker-foreign-node" {
		t.Fatalf("WorkerID bị thay đổi trái phép: %s", dbRun.WorkerID)
	}
}

func TestRecoverySweeper_ReclaimsExpiredLease(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runner := &dummyRunner{delay: 10 * time.Millisecond}
	svc := NewJobService(runRepo, runner)
	svc.SetCheckpointRepository(cpRepo)
	defer svc.Shutdown(ctx)

	runID := "run-sweeper-expired-lease"
	expiredLease := now.Add(-10 * time.Second)
	initialGen := int64(1)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Expired Lease Recovery",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-1.5-flash",
		WorkerID:        "worker-crashed",
		ClaimGeneration: initialGen,
		LeaseUntil:      &expiredLease,
		SecurityContext: &domain.AgentSecurityContext{
			TenantID: "tenant-v5",
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Lưu checkpoint hợp lệ để sweeper có thể tiếp tục
	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-v5",
		TaskID:    runID,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      runID,
			CurrentStep: 1,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Expired Lease Recovery"},
			},
		},
	})

	processed, err := svc.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}
	if len(processed) != 1 || processed[0].ID != runID {
		t.Fatalf("expected run %s to be reclaimed, got: %v", runID, processed)
	}

	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dbRun.ClaimGeneration != 2 {
		t.Fatalf("expected claim generation incremented to 2, got %d", dbRun.ClaimGeneration)
	}
	if dbRun.WorkerID != svc.workerID {
		t.Fatalf("expected workerID to be %s, got %s", svc.workerID, dbRun.WorkerID)
	}
}

func TestClaimRun_ActiveLeaseCannotIncrementGeneration(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runID := "run-claim-active-lease"
	activeLease := now.Add(5 * time.Minute)
	initialGen := int64(1)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Active Lease Protection",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-1.5-flash",
		WorkerID:        "worker-A",
		ClaimGeneration: initialGen,
		LeaseUntil:      &activeLease,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// 1. Worker A (same worker) cố gắng claim lại trên active lease -> BỊ TỪ CHỐI
	claimedSame, err := runRepo.ClaimRun(ctx, runID, "worker-A", 1*time.Minute)
	if err != nil {
		t.Fatalf("ClaimRun error: %v", err)
	}
	if claimedSame {
		t.Fatalf("ClaimRun must return false when lease is active, even for same worker")
	}

	// 2. Worker B (other worker) cố gắng claim trên active lease -> BỊ TỪ CHỐI
	claimedOther, err := runRepo.ClaimRun(ctx, runID, "worker-B", 1*time.Minute)
	if err != nil {
		t.Fatalf("ClaimRun error: %v", err)
	}
	if claimedOther {
		t.Fatalf("ClaimRun must return false when lease is active for other worker")
	}

	// 3. Kiểm tra DB: ClaimGeneration TUYỆT ĐỐI không bị increment
	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dbRun.ClaimGeneration != initialGen {
		t.Fatalf("ClaimGeneration must remain %d, got %d", initialGen, dbRun.ClaimGeneration)
	}
}

// ISSUE 2: Fencing RenewLease với claim_generation
func TestFencing_StaleGenerationCannotRenewLease(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runID := "run-fencing-renew-lease"
	activeLease := now.Add(1 * time.Minute)
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Fencing Renew Lease",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-B",
		ClaimGeneration: 2, // Hiện tại thuộc worker B thế hệ 2
		LeaseUntil:      &activeLease,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Stale worker A với generation 1 gọi RenewLease -> PHẢI THẤT BẠI
	renewedA, err := runRepo.RenewLease(ctx, runID, "worker-A", 1, 1*time.Minute)
	if err != nil {
		t.Fatalf("RenewLease error: %v", err)
	}
	if renewedA {
		t.Fatalf("Stale worker A (gen 1) must NOT be able to renew lease")
	}

	// Worker B nhưng gửi nhầm generation cũ 1 -> PHẢI THẤT BẠI
	renewedBStale, err := runRepo.RenewLease(ctx, runID, "worker-B", 1, 1*time.Minute)
	if err != nil {
		t.Fatalf("RenewLease error: %v", err)
	}
	if renewedBStale {
		t.Fatalf("Worker B with stale gen 1 must NOT be able to renew lease")
	}

	// Worker B với generation 2 hợp lệ -> THÀNH CÔNG
	renewedBValid, err := runRepo.RenewLease(ctx, runID, "worker-B", 2, 1*time.Minute)
	if err != nil {
		t.Fatalf("RenewLease error: %v", err)
	}
	if !renewedBValid {
		t.Fatalf("Current worker B with valid generation 2 must renew lease successfully")
	}
}

// ISSUE 2: Fencing AppendOwnedEvent
func TestFencing_StaleGenerationCannotAppendWorkerEvent(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runID := "run-fencing-append-event"
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Fencing Append Event",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-B",
		ClaimGeneration: 2,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	staleEv := &domain.AgentRunEvent{
		TenantID:  "tenant-v5",
		RunID:     runID,
		Step:      1,
		Kind:      "thinking",
		Message:   "Stale worker thinking...",
		Timestamp: now,
	}

	// Stale worker A gen 1 -> TỪ CHỐI
	appendedA, err := runRepo.AppendOwnedEvent(ctx, staleEv, "worker-A", 1)
	if err != nil {
		t.Fatalf("AppendOwnedEvent error: %v", err)
	}
	if appendedA {
		t.Fatalf("Stale worker A gen 1 event must be rejected")
	}

	// Worker B với stale gen 1 -> TỪ CHỐI
	appendedBStale, err := runRepo.AppendOwnedEvent(ctx, staleEv, "worker-B", 1)
	if err != nil {
		t.Fatalf("AppendOwnedEvent error: %v", err)
	}
	if appendedBStale {
		t.Fatalf("Worker B with stale gen 1 event must be rejected")
	}

	// Worker B với valid gen 2 -> THÀNH CÔNG
	validEv := &domain.AgentRunEvent{
		TenantID:  "tenant-v5",
		RunID:     runID,
		Step:      1,
		Kind:      "thinking",
		Message:   "Valid worker B thinking...",
		Timestamp: now,
	}
	appendedBValid, err := runRepo.AppendOwnedEvent(ctx, validEv, "worker-B", 2)
	if err != nil {
		t.Fatalf("AppendOwnedEvent error: %v", err)
	}
	if !appendedBValid {
		t.Fatalf("Valid worker B gen 2 event must be accepted")
	}

	// Kiểm tra sự kiện lưu trong DB: Chỉ duy nhất 1 event của Worker B
	events, err := runRepo.GetEvents(ctx, runID, 0)
	if err != nil {
		t.Fatalf("GetEvents error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event in db, got %d", len(events))
	}
	if events[0].Message != "Valid worker B thinking..." {
		t.Fatalf("unexpected event in db: %s", events[0].Message)
	}
}

// ISSUE 2: Fencing UpdateOwned
func TestFencing_StaleGenerationCannotFinalize(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runID := "run-fencing-finalize"
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-v5",
		Goal:            "Fencing Finalize",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-B",
		ClaimGeneration: 2,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	staleRun := *run
	staleRun.Status = domain.RunStatusCompleted
	staleRun.FinalAnswer = "STALE FINAL ANSWER FROM WORKER A"

	// Worker A gen 1 -> TỪ CHỐI
	updatedA, err := runRepo.UpdateOwned(ctx, &staleRun, "worker-A", 1)
	if err != nil {
		t.Fatalf("UpdateOwned error: %v", err)
	}
	if updatedA {
		t.Fatalf("Stale worker A gen 1 must NOT be allowed to finalize run")
	}

	validRun := *run
	validRun.Status = domain.RunStatusCompleted
	validRun.FinalAnswer = "CANONICAL FINAL ANSWER FROM WORKER B"

	// Worker B gen 2 -> THÀNH CÔNG
	updatedB, err := runRepo.UpdateOwned(ctx, &validRun, "worker-B", 2)
	if err != nil {
		t.Fatalf("UpdateOwned error: %v", err)
	}
	if !updatedB {
		t.Fatalf("Valid worker B gen 2 must finalize run successfully")
	}

	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dbRun.FinalAnswer != "CANONICAL FINAL ANSWER FROM WORKER B" {
		t.Fatalf("unexpected final answer in db: %s", dbRun.FinalAnswer)
	}
}

// ISSUE 3: Resume Security Context Intersection
func TestResume_WorkspaceIntersectionCannotExpand(t *testing.T) {
	oldCtx := &domain.AgentSecurityContext{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects/A")},
	}
	callerExpand := domain.TenantIdentity{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects/A"), filepath.Clean("/projects/B")},
	}
	intersected, err := domain.IntersectSecurityContext(oldCtx, callerExpand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intersected.AllowedWorkspaceRoots) != 1 {
		t.Fatalf("expected 1 root, got: %v", intersected.AllowedWorkspaceRoots)
	}

	// Disjoint workspace must fail reauthorization
	callerDisjoint := domain.TenantIdentity{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/other/unrelated")},
	}
	_, errDisjoint := domain.IntersectSecurityContext(oldCtx, callerDisjoint)
	if errDisjoint == nil {
		t.Fatalf("expected ErrResumeReauthRequired for disjoint workspace roots, got nil")
	}
}

func TestResume_NetworkPolicyCannotExpand(t *testing.T) {
	oldDenied := &domain.AgentSecurityContext{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  false,
			AllowedDomains: []string{"example.com"},
		},
	}
	callerAllowed := domain.TenantIdentity{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  true,
			AllowedDomains: []string{"example.com", "other.com"},
		},
	}
	res, err := domain.IntersectSecurityContext(oldDenied, callerAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.NetworkPolicy.AllowOutbound {
		t.Fatalf("expected AllowOutbound=false when old policy had AllowOutbound=false")
	}
	if len(res.NetworkPolicy.AllowedDomains) != 0 {
		t.Fatalf("expected empty AllowedDomains when AllowOutbound is false")
	}
	if res.NetworkPolicy.IsURLAllowed("https://example.com") {
		t.Fatalf("IsURLAllowed must be false when AllowOutbound is false")
	}
}

func TestResume_ToolsCannotExpand(t *testing.T) {
	oldCtx := &domain.AgentSecurityContext{
		TenantID:     "tenant-1",
		Role:         "user",
		Scopes:       []string{domain.ScopeAgent},
		AllowedTools: []string{"read_file"},
	}
	callerExpanded := domain.TenantIdentity{
		TenantID:     "tenant-1",
		Role:         "user",
		Scopes:       []string{domain.ScopeAgent},
		AllowedTools: []string{"read_file", "write_file", "run_command"},
	}
	res, err := domain.IntersectSecurityContext(oldCtx, callerExpanded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.AllowedTools) != 1 || res.AllowedTools[0] != "read_file" {
		t.Fatalf("expected only read_file, got: %v", res.AllowedTools)
	}
}

func TestResume_ModelsCannotExpand(t *testing.T) {
	oldCtx := &domain.AgentSecurityContext{
		TenantID:      "tenant-1",
		Role:          "user",
		Scopes:        []string{domain.ScopeAgent},
		AllowedModels: []string{"gemini-1.5-flash"},
	}
	callerExpanded := domain.TenantIdentity{
		TenantID:      "tenant-1",
		Role:          "user",
		Scopes:        []string{domain.ScopeAgent},
		AllowedModels: []string{"gemini-1.5-flash", "gemini-1.5-pro"},
	}
	res, err := domain.IntersectSecurityContext(oldCtx, callerExpanded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.AllowedModels) != 1 || res.AllowedModels[0] != "gemini-1.5-flash" {
		t.Fatalf("expected only gemini-1.5-flash, got: %v", res.AllowedModels)
	}
}

// ISSUE 6: Async Agent respects RPM
func TestAsyncAgent_RespectsRPM(t *testing.T) {
	keyRepo := session.NewMemoryKeyRepository()
	keySvc := services.NewKeyService(keyRepo, "")
	ctx := context.Background()

	createdKey, err := keySvc.CreateKey(ctx, domain.CreateKeyRequest{
		TenantID:           "tenant-rpm",
		Name:               "RPM Limited Key",
		Role:               "user",
		Scopes:             []string{domain.ScopeAgent, domain.ScopeChat},
		DailyQuotaRequests: 1000,
		MaxTokenQuota:      100000,
		RateLimitRPM:       2, // Cho phép tối đa 2 requests mỗi phút
	})
	if err != nil {
		t.Fatalf("failed to create key: %v", err)
	}

	// Cuộc gọi 1: OK
	_, err1 := keySvc.ValidateKeyByID(ctx, createdKey.ID, "gemini-1.5-flash")
	if err1 != nil {
		t.Fatalf("call 1 failed: %v", err1)
	}

	// Cuộc gọi 2: OK
	_, err2 := keySvc.ValidateKeyByID(ctx, createdKey.ID, "gemini-1.5-flash")
	if err2 != nil {
		t.Fatalf("call 2 failed: %v", err2)
	}

	// Cuộc gọi 3: BỊ GIỚI HẠN RPM
	_, err3 := keySvc.ValidateKeyByID(ctx, createdKey.ID, "gemini-1.5-flash")
	if err3 == nil {
		t.Fatalf("call 3 expected ErrRateLimitRPMExceeded, got nil")
	}
	if !errors.Is(err3, domain.ErrRateLimitRPMExceeded) {
		t.Fatalf("expected ErrRateLimitRPMExceeded, got: %v", err3)
	}

	// Xác nhận Runner kiểm tra billing identity và dừng với StopReasonRateLimited
	runner := NewRunner(nil, nil, nil)
	runner.SetKeyUseCase(keySvc)
	runCtx := domain.ContextWithBillingIdentity(ctx, domain.BillingIdentity{
		TenantID: "tenant-rpm",
		KeyID:    createdKey.ID,
	})

	state, runErr := runner.Run(runCtx, "Goal with rate limit", domain.AgentRunOptions{
		TaskID: "task-rpm-check",
	})
	if runErr == nil || !errors.Is(runErr, domain.ErrRateLimitRPMExceeded) {
		t.Fatalf("expected runner to return ErrRateLimitRPMExceeded, got: %v", runErr)
	}
	if state.StopReason != domain.StopReasonRateLimited {
		t.Fatalf("expected state.StopReason to be StopReasonRateLimited, got: %s", state.StopReason)
	}
}

// ISSUE 7: Submit Shutdown race - không để lại orphan runnable job
func TestSubmitShutdownRace_NoRunnableOrphan(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()

	runner := &dummyRunner{delay: 20 * time.Millisecond}
	svc := NewJobService(runRepo, runner)

	// Đóng JobService
	_ = svc.Shutdown(ctx)

	// Thử submit run sau khi shutdown
	opts := domain.AgentRunOptions{
		Model: "gemini-1.5-flash",
	}
	callerCtx := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{
		TenantID: "tenant-shutdown",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	_, err := svc.SubmitRun(callerCtx, "Goal during shutdown", opts, "")
	if err == nil {
		t.Fatalf("SubmitRun after shutdown must be rejected")
	}

	// Đảm bảo không có bất kỳ run nào ở trạng thái queued trong DB
	runs, err := runRepo.List(ctx, "tenant-shutdown", 100, 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, r := range runs {
		if r.Status == domain.RunStatusQueued {
			t.Fatalf("Found orphan queued run in DB after shutdown: %s", r.ID)
		}
	}
}

// ISSUE 5: Tool Execution Ledger tests
func TestToolLedger_SucceededToolNotReplayed(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()

	reg := tools.NewToolRegistry()
	tempDir := t.TempDir()
	tools.RegisterDefaultTools(reg, tempDir)

	runID := "task-ledger-succeeded"
	tenantID := "tenant-ledger"
	toolCallID := "call_cached_1"

	// Lưu sẵn kết quả thành công vào Ledger bền vững
	now := time.Now()
	err := runRepo.RecordPlannedOrRunning(ctx, &domain.ToolExecutionRecord{
		TenantID:   tenantID,
		RunID:      runID,
		ToolCallID: toolCallID,
		ToolName:   "write_file",
		Status:     domain.ToolExecutionRunning,
		StartedAt:  &now,
	})
	if err != nil {
		t.Fatalf("RecordPlannedOrRunning failed: %v", err)
	}

	err = runRepo.RecordFinished(ctx, tenantID, runID, toolCallID, domain.ToolExecutionSucceeded, "{\"status\": \"cached_success\"}", "")
	if err != nil {
		t.Fatalf("RecordFinished failed: %v", err)
	}

	// MockChat gọi lại chính toolCallID này
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   toolCallID,
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "write_file",
										Arguments: `{"path": "never_written.txt", "content": "should not exist"}`,
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
							Content: "Hoàn tất sau khi tái sử dụng cache.",
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	runner.SetToolExecutionLedger(runRepo)

	runCtx := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	state, err := runner.Run(runCtx, "Write file", domain.AgentRunOptions{
		TaskID:    runID,
		MaxSteps:  3,
		Workspace: tempDir,
	})
	if err != nil {
		t.Fatalf("runner.Run failed: %v", err)
	}
	if !state.IsCompleted {
		t.Fatalf("expected run to complete, stop_reason: %s", state.StopReason)
	}

	// Đảm bảo file never_written.txt không bao giờ được tạo thực sự trên đĩa
	filePath := filepath.Join(tempDir, "never_written.txt")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("CRITICAL BUG: Succeeded tool was executed again on disk!")
	}
}

func TestToolLedger_DestructiveRunningToolBecomesUnknownAfterRestart(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()

	reg := tools.NewToolRegistry()
	tempDir := t.TempDir()
	tools.RegisterDefaultTools(reg, tempDir)

	runID := "task-ledger-destructive-running"
	tenantID := "tenant-ledger"
	toolCallID := "call_destructive_running"

	// Giả lập trạng thái trước crash: công cụ destructive đang chạy
	now := time.Now()
	err := runRepo.RecordPlannedOrRunning(ctx, &domain.ToolExecutionRecord{
		TenantID:   tenantID,
		RunID:      runID,
		ToolCallID: toolCallID,
		ToolName:   "write_file",
		Status:     domain.ToolExecutionRunning,
		StartedAt:  &now,
	})
	if err != nil {
		t.Fatalf("RecordPlannedOrRunning failed: %v", err)
	}

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   toolCallID,
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "write_file",
										Arguments: `{"path": "crash_file.txt", "content": "crash content"}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	runner.SetToolExecutionLedger(runRepo)

	runCtx := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	state, err := runner.Run(runCtx, "Write crash file", domain.AgentRunOptions{
		TaskID:    runID,
		MaxSteps:  3,
		Workspace: tempDir,
	})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	// Phải dừng với StopReasonVerificationFailed để ngăn replay side-effect
	if state.StopReason != domain.StopReasonVerificationFailed {
		t.Fatalf("expected StopReasonVerificationFailed, got: %s", state.StopReason)
	}

	// Ledger trong DB phải được cập nhật thành unknown_after_restart
	rec, err := runRepo.GetExecution(ctx, tenantID, runID, toolCallID)
	if err != nil {
		t.Fatalf("GetExecution error: %v", err)
	}
	if rec.Status != domain.ToolExecutionUnknownAfterRestart {
		t.Fatalf("expected ledger status to be unknown_after_restart, got: %s", rec.Status)
	}
}

func TestToolLedger_ReadOnlyToolMayRetry(t *testing.T) {
	runRepo, _, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()

	reg := tools.NewToolRegistry()
	tempDir := t.TempDir()
	tools.RegisterDefaultTools(reg, tempDir)

	// Tạo sẵn file để đọc
	readmePath := filepath.Join(tempDir, "README.md")
	_ = os.WriteFile(readmePath, []byte("Dezuxk Read Only Content"), 0644)

	runID := "task-ledger-readonly-running"
	tenantID := "tenant-ledger"
	toolCallID := "call_readonly_running"

	// Giả lập trạng thái trước crash: công cụ read_file đang chạy
	now := time.Now()
	err := runRepo.RecordPlannedOrRunning(ctx, &domain.ToolExecutionRecord{
		TenantID:   tenantID,
		RunID:      runID,
		ToolCallID: toolCallID,
		ToolName:   "read_file",
		Status:     domain.ToolExecutionRunning,
		StartedAt:  &now,
	})
	if err != nil {
		t.Fatalf("RecordPlannedOrRunning failed: %v", err)
	}

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   toolCallID,
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "read_file",
										Arguments: `{"path": "README.md"}`,
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
							Content: "Đã đọc xong nội dung README.md.",
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	runner.SetToolExecutionLedger(runRepo)

	runCtx := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{
		TenantID:              tenantID,
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{tempDir},
	})

	state, err := runner.Run(runCtx, "Read file", domain.AgentRunOptions{
		TaskID:    runID,
		MaxSteps:  3,
		Workspace: tempDir,
	})
	if err != nil {
		t.Fatalf("runner error: %v", err)
	}
	if !state.IsCompleted {
		t.Fatalf("expected read-only tool to complete successfully after retry, stop_reason: %s", state.StopReason)
	}

	// Ledger trong DB phải chuyển thành succeeded
	rec, err := runRepo.GetExecution(ctx, tenantID, runID, toolCallID)
	if err != nil {
		t.Fatalf("GetExecution error: %v", err)
	}
	t.Logf("ledger rec: status=%s, error=%q, result=%q", rec.Status, rec.Error, rec.ResultJSON)
	if rec.Status != domain.ToolExecutionSucceeded {
		t.Fatalf("expected ledger status to be succeeded, got: %s (err: %s)", rec.Status, rec.Error)
	}
}

// MULTI-INSTANCE SCENARIO TEST:
// Instance A & Instance B trên cùng SQLite DB
// Lease active -> Sweeper B bỏ qua -> Lease expire -> Sweeper B takeover gen 2 -> A bị từ chối mọi thao tác -> B finalize thành công
func TestMultiInstance_LeaseExpiry_Takeover_FencingRace(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()

	runnerA := &dummyRunner{delay: 10 * time.Millisecond}
	runnerB := &dummyRunner{delay: 150 * time.Millisecond}

	svcA := NewJobService(runRepo, runnerA)
	svcA.SetCheckpointRepository(cpRepo)
	defer svcA.Shutdown(ctx)

	svcB := NewJobService(runRepo, runnerB)
	svcB.SetCheckpointRepository(cpRepo)
	defer svcB.Shutdown(ctx)

	runID := "run-multi-instance-race"
	activeLease := now.Add(5 * time.Minute)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-multi",
		Goal:            "Multi-instance Takeover & Fencing Race",
		Status:          domain.RunStatusRunning,
		Model:           "gemini-1.5-flash",
		WorkerID:        svcA.workerID,
		ClaimGeneration: 1,
		LeaseUntil:      &activeLease,
		SecurityContext: &domain.AgentSecurityContext{
			TenantID: "tenant-multi",
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Checkpoint hợp lệ cho recovery
	_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
		TenantID:  "tenant-multi",
		TaskID:    runID,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      runID,
			CurrentStep: 1,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Start step"},
			},
		},
	})

	// 1. Sweeper B quét khi lease của A còn active -> B BỎ QUA
	reclaimedByB, err := svcB.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}
	for _, r := range reclaimedByB {
		if r.ID == runID {
			t.Fatalf("Sweeper B must NOT reclaim run when A's lease is active")
		}
	}

	// 2. Giả lập node A crash và lease hết hạn
	expiredTime := time.Now().Add(-10 * time.Second)
	_, err = db.Exec("UPDATE agent_runs SET lease_until = ? WHERE id = ?", expiredTime, runID)
	if err != nil {
		t.Fatalf("failed to expire lease in db: %v", err)
	}

	// 3. Sweeper B quét lại -> B reclaim thành công!
	reclaimedByB2, err := svcB.ScanRecoverableRuns(ctx)
	if err != nil {
		t.Fatalf("ScanRecoverableRuns failed: %v", err)
	}
	if len(reclaimedByB2) != 1 || reclaimedByB2[0].ID != runID {
		t.Fatalf("expected Sweeper B to reclaim expired run %s", runID)
	}

	// Kiểm tra thế hệ trong DB đã tăng lên 2 và thuộc sở hữu của B
	dbRun, err := runRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if dbRun.ClaimGeneration != 2 {
		t.Fatalf("expected claim generation to be 2, got %d", dbRun.ClaimGeneration)
	}
	if dbRun.WorkerID != svcB.workerID {
		t.Fatalf("expected workerID to be %s, got %s", svcB.workerID, dbRun.WorkerID)
	}

	// 4. Stale Worker A cố gắng thực hiện các thao tác:
	// a) Heartbeat / RenewLease -> BỊ TỪ CHỐI
	renewA, err := runRepo.RenewLease(ctx, runID, svcA.workerID, 1, 1*time.Minute)
	if err != nil {
		t.Fatalf("RenewLease error: %v", err)
	}
	if renewA {
		t.Fatalf("Stale Worker A RenewLease must return false")
	}

	// b) AppendOwnedEvent -> BỊ TỪ CHỐI
	evA := &domain.AgentRunEvent{
		TenantID:  "tenant-multi",
		RunID:     runID,
		Step:      2,
		Kind:      "thinking",
		Message:   "Stale Worker A thinking...",
		Timestamp: time.Now(),
	}
	appendedA, err := runRepo.AppendOwnedEvent(ctx, evA, svcA.workerID, 1)
	if err != nil {
		t.Fatalf("AppendOwnedEvent error: %v", err)
	}
	if appendedA {
		t.Fatalf("Stale Worker A AppendOwnedEvent must return false")
	}

	// c) UpdateOwned (Finalize) -> BỊ TỪ CHỐI
	finalRunA := *dbRun
	finalRunA.Status = domain.RunStatusCompleted
	finalRunA.FinalAnswer = "STALE ANSWER FROM A"
	updatedA, err := runRepo.UpdateOwned(ctx, &finalRunA, svcA.workerID, 1)
	if err != nil {
		t.Fatalf("UpdateOwned error: %v", err)
	}
	if updatedA {
		t.Fatalf("Stale Worker A UpdateOwned must return false")
	}

	// 5. Active Worker B thực hiện các thao tác thành công:
	// a) Heartbeat -> THÀNH CÔNG (khi tác vụ đang recovering/running)
	renewB, err := runRepo.RenewLease(ctx, runID, svcB.workerID, 2, 1*time.Minute)
	if err != nil || !renewB {
		t.Fatalf("Worker B RenewLease failed: %v, renewed=%v", err, renewB)
	}

	// b) Event -> THÀNH CÔNG
	evB := &domain.AgentRunEvent{
		TenantID:  "tenant-multi",
		RunID:     runID,
		Step:      2,
		Kind:      "thinking",
		Message:   "Active Worker B thinking...",
		Timestamp: time.Now(),
	}
	appendedB, err := runRepo.AppendOwnedEvent(ctx, evB, svcB.workerID, 2)
	if err != nil || !appendedB {
		t.Fatalf("Worker B AppendOwnedEvent failed: %v, appended=%v", err, appendedB)
	}

	// 6. Chờ Worker B hoàn tất thực thi tác vụ
	var finalDBRun *domain.AgentRun
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		finalDBRun, _ = runRepo.Get(ctx, runID)
		if finalDBRun != nil && (finalDBRun.Status == domain.RunStatusCompleted || finalDBRun.Status == domain.RunStatusFailed) {
			break
		}
	}
	if finalDBRun == nil || finalDBRun.Status != domain.RunStatusCompleted {
		t.Fatalf("expected completed status from Worker B, got: %v", finalDBRun)
	}
	if finalDBRun.ClaimGeneration != 2 {
		t.Fatalf("expected claim generation to be 2, got %d", finalDBRun.ClaimGeneration)
	}
	if finalDBRun.WorkerID != svcB.workerID {
		t.Fatalf("expected final workerID to be %s, got %s", svcB.workerID, finalDBRun.WorkerID)
	}
}
