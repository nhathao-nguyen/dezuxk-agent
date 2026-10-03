package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

func generateRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run_%d", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(b)
}

// JobService triển khai ports.AgentJobService quản lý tác vụ Agent chạy nền bất đồng bộ với vòng đời và khôi phục bền vững
type JobService struct {
	repo           ports.AgentRunRepository
	runner         ports.AgentRunner
	checkpointRepo ports.CheckpointRepository
	workerID       string

	mu          sync.RWMutex
	accepting   bool
	cancels     map[string]context.CancelFunc
	subsMu      sync.RWMutex
	subscribers map[string][]chan domain.AgentRunEvent

	recoveryInterval time.Duration
	recoveryCancel   context.CancelFunc
	recoveryStopCh   chan struct{}
	recoveryWg       sync.WaitGroup

	wg         sync.WaitGroup
	rootCtx    context.Context
	cancelRoot context.CancelFunc
}

// NewJobService khởi tạo service quản lý Job Agent
func NewJobService(repo ports.AgentRunRepository, runner ports.AgentRunner) *JobService {
	rootCtx, cancel := context.WithCancel(context.Background())
	js := &JobService{
		repo:             repo,
		runner:           runner,
		workerID:         fmt.Sprintf("worker-%s", generateRunID()[:10]),
		cancels:          make(map[string]context.CancelFunc),
		subscribers:      make(map[string][]chan domain.AgentRunEvent),
		rootCtx:          rootCtx,
		cancelRoot:       cancel,
		accepting:        true,
		recoveryInterval: 15 * time.Second,
	}
	return js
}

// SetCheckpointRepository cấu hình checkpoint repository phục vụ phục hồi và resume
func (s *JobService) SetCheckpointRepository(cp ports.CheckpointRepository) {
	s.checkpointRepo = cp
}

// SetRecoveryInterval cấu hình chu kỳ quét phục hồi của Sweeper (phục vụ test hoặc cấu hình tùy biến)
func (s *JobService) SetRecoveryInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recoveryInterval = d
	if s.accepting && s.rootCtx != nil {
		s.startRecoveryLoopLocked(s.rootCtx, d)
	}
}

var _ ports.AgentJobService = (*JobService)(nil)

// Start khởi chạy JobService với context gốc của máy chủ và kích hoạt background recovery sweeper
func (s *JobService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelRoot != nil {
		s.cancelRoot()
	}
	s.rootCtx, s.cancelRoot = context.WithCancel(ctx)
	s.accepting = true

	interval := s.recoveryInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	s.startRecoveryLoopLocked(s.rootCtx, interval)
	return nil
}

// StartRecoveryLoop khởi chạy vòng lặp Sweeper định kỳ quét và thu hồi các lease hết hạn
func (s *JobService) StartRecoveryLoop(ctx context.Context, interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startRecoveryLoopLocked(ctx, interval)
}

func (s *JobService) startRecoveryLoopLocked(ctx context.Context, interval time.Duration) {
	if s.recoveryCancel != nil {
		s.recoveryCancel()
	}
	if s.recoveryStopCh != nil {
		select {
		case <-s.recoveryStopCh:
		default:
			close(s.recoveryStopCh)
		}
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	s.recoveryInterval = interval

	loopCtx, cancel := context.WithCancel(ctx)
	s.recoveryCancel = cancel
	stopCh := make(chan struct{})
	s.recoveryStopCh = stopCh

	s.recoveryWg.Add(1)
	go func() {
		defer s.recoveryWg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = s.ScanRecoverableRuns(loopCtx)
			}
		}
	}()
}

// Shutdown dừng tiếp nhận job mới, dừng recovery sweeper, hủy/drain các worker đang chạy và đợi hoàn tất
func (s *JobService) Shutdown(ctx context.Context) error {
	// 1. Dừng nhận job mới và hủy các worker dưới lock
	s.mu.Lock()
	s.accepting = false
	if s.recoveryCancel != nil {
		s.recoveryCancel()
	}
	if s.recoveryStopCh != nil {
		select {
		case <-s.recoveryStopCh:
		default:
			close(s.recoveryStopCh)
		}
	}
	for _, cancel := range s.cancels {
		if cancel != nil {
			cancel()
		}
	}
	s.mu.Unlock()

	if s.cancelRoot != nil {
		s.cancelRoot()
	}

	// 2. Chờ background recovery sweeper thoát an toàn
	s.recoveryWg.Wait()

	// 3. Đợi các worker kết thúc có thời hạn bảo vệ
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("job service shutdown timeout hoặc bị hủy: %w", ctx.Err())
	}

	// 4. Đóng tất cả kênh SSE subscribers
	s.subsMu.Lock()
	for _, subs := range s.subscribers {
		for _, ch := range subs {
			close(ch)
		}
	}
	s.subscribers = make(map[string][]chan domain.AgentRunEvent)
	s.subsMu.Unlock()

	return nil
}

func reconstructOptions(run *domain.AgentRun, initialCheckpoint *domain.AgentCheckpoint) domain.AgentRunOptions {
	opts := domain.AgentRunOptions{
		TaskID:    run.ID,
		Model:     run.Model,
		Workspace: run.Workspace,
		MaxSteps:  run.MaxSteps,
	}
	if run.ExecutionConfig != nil {
		if run.ExecutionConfig.Model != "" {
			opts.Model = run.ExecutionConfig.Model
		}
		if run.ExecutionConfig.Workspace != "" {
			opts.Workspace = run.ExecutionConfig.Workspace
		}
		if run.ExecutionConfig.MaxSteps > 0 {
			opts.MaxSteps = run.ExecutionConfig.MaxSteps
		}
		opts.MaxToolCalls = run.ExecutionConfig.MaxToolCalls
		opts.MaxRepeatedCalls = run.ExecutionConfig.MaxRepeatedCalls
		opts.MaxExecutionDuration = run.ExecutionConfig.MaxExecutionDuration
		opts.MaxConsecutiveFailures = run.ExecutionConfig.MaxConsecutiveFailures
		opts.Supervised = run.ExecutionConfig.Supervised
		opts.RequireAction = run.ExecutionConfig.RequireAction
		opts.UseSandbox = run.ExecutionConfig.UseSandbox
		opts.AutoMerge = run.ExecutionConfig.AutoMerge
		opts.CustomPrompt = run.ExecutionConfig.CustomPrompt
	}
	if opts.Model == "" {
		opts.Model = run.Model
	}
	if opts.Workspace == "" {
		opts.Workspace = run.Workspace
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = run.MaxSteps
	}
	if initialCheckpoint != nil {
		resumedState := initialCheckpoint.StateSnapshot
		resumedState.TaskID = run.ID
		opts.InitialState = &resumedState
	}
	return opts
}

// RecoverPendingRuns quét và khôi phục các tác vụ dở dang từ SQLite sau khi server khởi động lại (Startup Recovery)
func (s *JobService) RecoverPendingRuns(ctx context.Context) ([]*domain.AgentRun, error) {
	if s.repo == nil {
		return nil, nil
	}

	pending, err := s.repo.ListPendingRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("không thể truy vấn tác vụ pending để phục hồi: %w", err)
	}

	var processed []*domain.AgentRun

	for _, run := range pending {
		// ISSUE 5: Legacy run thiếu SecurityContext -> Bắt buộc FAIL CLOSED, không tự động phục hồi cấp quyền admin!
		if run.SecurityContext == nil {
			now := time.Now()
			run.Status = domain.RunStatusInterrupted
			run.StopReason = domain.StopReasonReauthorizationRequired
			run.Error = "legacy_security_context_missing: Tác vụ thiếu SecurityContext hợp lệ từ phiên bản trước; yêu cầu người dùng xác thực và resume lại"
			run.UpdatedAt = now
			run.FinishedAt = &now
			_ = s.repo.Update(ctx, run)

			interEv := domain.AgentRunEvent{
				TenantID:  run.TenantID,
				RunID:     run.ID,
				Step:      run.CurrentStep,
				Kind:      "run_interrupted",
				Message:   "Tác vụ bị gián đoạn do thiếu SecurityContext (Stop reason: reauthorization_required)",
				Timestamp: now,
			}
			_ = s.repo.AppendEvent(ctx, &interEv)
			processed = append(processed, run)
			continue
		}

		restartEv := domain.AgentRunEvent{
			TenantID:  run.TenantID,
			RunID:     run.ID,
			Step:      run.CurrentStep,
			Kind:      "server_restart_detected",
			Message:   fmt.Sprintf("Máy chủ phát hiện tác vụ ở trạng thái '%s' sau khi khởi động lại", run.Status),
			Timestamp: time.Now(),
		}
		_ = s.repo.AppendEvent(ctx, &restartEv)

		switch run.Status {
		case domain.RunStatusQueued:
			recovEv := domain.AgentRunEvent{
				TenantID:  run.TenantID,
				RunID:     run.ID,
				Step:      run.CurrentStep,
				Kind:      "run_recovered",
				Message:   "Tác vụ queued được tự động xếp hàng thực thi lại sau restart",
				Timestamp: time.Now(),
			}
			_ = s.repo.AppendEvent(ctx, &recovEv)

			opts := reconstructOptions(run, nil)
			_ = s.launchBackgroundWorker(run, opts)
			processed = append(processed, run)

		case domain.RunStatusRunning, domain.RunStatusRecovering, domain.RunStatusWaitingForTool:
			now := time.Now()
			// 1. Nếu worker khác đang giữ lease active -> BỎ QUA (skip)
			if run.WorkerID != "" && run.WorkerID != s.workerID && run.LeaseUntil != nil && run.LeaseUntil.After(now) {
				continue
			}

			// 2. Lease đã hết hạn hoặc không có -> tra cứu checkpoint khả dụng
			var cp *domain.AgentCheckpoint
			if s.checkpointRepo != nil {
				cpCtx := domain.ContextWithTenantIdentity(ctx, run.SecurityContext.ToTenantIdentity())
				cp, _ = s.checkpointRepo.GetLatestCheckpoint(cpCtx, run.ID)
			}

			claimed, cErr := s.repo.ClaimRun(ctx, run.ID, s.workerID, 60*time.Second)
			if cErr != nil || !claimed {
				continue
			}

			claimedRun, _ := s.repo.Get(ctx, run.ID)
			claimGen := run.ClaimGeneration
			if claimedRun != nil {
				claimGen = claimedRun.ClaimGeneration
				run.ClaimGeneration = claimGen
				run.WorkerID = claimedRun.WorkerID
			}

			if cp != nil && len(cp.StateSnapshot.Messages) > 0 {
				run.Status = domain.RunStatusRecovering
				run.UpdatedAt = time.Now()
				_, _ = s.repo.UpdateOwned(ctx, run, s.workerID, claimGen)

				recovEv := domain.AgentRunEvent{
					TenantID:  run.TenantID,
					RunID:     run.ID,
					Step:      cp.StepIndex,
					Kind:      "run_recovered",
					Message:   fmt.Sprintf("Tác vụ được phục hồi thành công từ Checkpoint #%d sau restart", cp.StepIndex),
					Timestamp: time.Now(),
				}
				_ = s.repo.AppendEvent(ctx, &recovEv)

				opts := reconstructOptions(run, cp)
				_ = s.launchBackgroundWorker(run, opts)
				processed = append(processed, run)
			} else {
				// Không có checkpoint an toàn -> đánh dấu interrupted với stop reason server_restart
				now := time.Now()
				run.Status = domain.RunStatusInterrupted
				run.StopReason = domain.StopReasonServerRestart
				run.Error = "Server khởi động lại và tác vụ chưa kịp lưu checkpoint an toàn để tiếp tục"
				run.UpdatedAt = now
				run.FinishedAt = &now
				_, _ = s.repo.UpdateOwned(ctx, run, s.workerID, claimGen)

				interEv := domain.AgentRunEvent{
					TenantID:  run.TenantID,
					RunID:     run.ID,
					Step:      run.CurrentStep,
					Kind:      "run_interrupted",
					Message:   "Tác vụ bị gián đoạn do server khởi động lại (Stop reason: server_restart)",
					Timestamp: now,
				}
				_ = s.repo.AppendEvent(ctx, &interEv)
				processed = append(processed, run)
			}

		case domain.RunStatusWaitingForApproval:
			// Giữ nguyên trạng thái chờ người dùng phê duyệt
			processed = append(processed, run)
		}
	}

	return processed, nil
}

// ScanRecoverableRuns quét định kỳ (Recovery Sweeper) để reclaim các tác vụ bị treo do worker crash và lease đã hết hạn
func (s *JobService) ScanRecoverableRuns(ctx context.Context) ([]*domain.AgentRun, error) {
	if s.repo == nil {
		return nil, nil
	}

	pending, err := s.repo.ListPendingRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("không thể quét tác vụ để thu hồi lease hết hạn: %w", err)
	}

	var processed []*domain.AgentRun
	now := time.Now()

	for _, run := range pending {
		// 1. Kiểm tra Fail Closed nếu thiếu SecurityContext
		if run.SecurityContext == nil {
			runNow := time.Now()
			run.Status = domain.RunStatusInterrupted
			run.StopReason = domain.StopReasonReauthorizationRequired
			run.Error = "legacy_security_context_missing: Tác vụ thiếu SecurityContext hợp lệ; yêu cầu người dùng xác thực và resume lại"
			run.UpdatedAt = runNow
			run.FinishedAt = &runNow
			_ = s.repo.Update(ctx, run)
			processed = append(processed, run)
			continue
		}

		// 2. Không can thiệp nếu tác vụ đang chạy trên node hiện tại
		s.mu.RLock()
		_, isRunningLocally := s.cancels[run.ID]
		s.mu.RUnlock()
		if isRunningLocally {
			continue
		}

		// 3. Chỉ reclaim các trạng thái running / recovering / waiting_for_tool mà LEASE ĐÃ HẾT HẠN
		if run.Status == domain.RunStatusRunning || run.Status == domain.RunStatusRecovering || run.Status == domain.RunStatusWaitingForTool {
			// TUYỆT ĐỐI KHÔNG ĐỤNG JOB CÓ LEASE ACTIVE
			if run.WorkerID != "" && run.WorkerID != s.workerID && run.LeaseUntil != nil && run.LeaseUntil.After(now) {
				continue
			}

			// Atomic claim: cố gắng giành quyền sở hữu run
			claimed, cErr := s.repo.ClaimRun(ctx, run.ID, s.workerID, 60*time.Second)
			if cErr != nil || !claimed {
				continue
			}

			// Đọc lại để lấy claim_generation mới nhất
			claimedRun, _ := s.repo.Get(ctx, run.ID)
			claimGen := run.ClaimGeneration
			if claimedRun != nil {
				claimGen = claimedRun.ClaimGeneration
				run.ClaimGeneration = claimGen
				run.WorkerID = claimedRun.WorkerID
			}

			// Tra cứu checkpoint khả dụng
			var cp *domain.AgentCheckpoint
			if s.checkpointRepo != nil {
				cpCtx := domain.ContextWithTenantIdentity(ctx, run.SecurityContext.ToTenantIdentity())
				cp, _ = s.checkpointRepo.GetLatestCheckpoint(cpCtx, run.ID)
			}

			if cp != nil && len(cp.StateSnapshot.Messages) > 0 {
				run.Status = domain.RunStatusRecovering
				run.UpdatedAt = time.Now()
				_, _ = s.repo.UpdateOwned(ctx, run, s.workerID, claimGen)

				recovEv := domain.AgentRunEvent{
					TenantID:  run.TenantID,
					RunID:     run.ID,
					Step:      cp.StepIndex,
					Kind:      "run_reclaimed",
					Message:   fmt.Sprintf("Sweeper phát hiện lease hết hạn và đã phục hồi tác vụ thành công từ Checkpoint #%d", cp.StepIndex),
					Timestamp: time.Now(),
				}
				_ = s.repo.AppendEvent(ctx, &recovEv)

				opts := reconstructOptions(run, cp)
				_ = s.launchBackgroundWorker(run, opts)
				processed = append(processed, run)
			} else {
				staleNow := time.Now()
				run.Status = domain.RunStatusInterrupted
				run.StopReason = domain.StopReasonServerRestart
				run.Error = "Worker cũ mất kết nối và tác vụ không có checkpoint hợp lệ để tiếp tục"
				run.UpdatedAt = staleNow
				run.FinishedAt = &staleNow
				_, _ = s.repo.UpdateOwned(ctx, run, s.workerID, claimGen)

				interEv := domain.AgentRunEvent{
					TenantID:  run.TenantID,
					RunID:     run.ID,
					Step:      run.CurrentStep,
					Kind:      "run_interrupted",
					Message:   "Sweeper thu hồi tác vụ lease hết hạn không có checkpoint (Stop reason: server_restart)",
					Timestamp: staleNow,
				}
				_ = s.repo.AppendEvent(ctx, &interEv)
				processed = append(processed, run)
			}
		}
	}

	return processed, nil
}

func (s *JobService) SubmitRun(ctx context.Context, goal string, opts domain.AgentRunOptions, idempotencyKey string) (*domain.AgentRun, error) {
	s.mu.RLock()
	accepting := s.accepting
	s.mu.RUnlock()
	if !accepting {
		return nil, errors.New("máy chủ đang trong quá trình tắt, từ chối nhận thêm tác vụ mới")
	}

	tenantID := "default"
	var secCtx *domain.AgentSecurityContext
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
		secCtx = domain.SecurityContextFromTenantIdentity(id)
	} else {
		secCtx = domain.SecurityContextFromTenantIdentity(domain.TenantIdentity{
			TenantID: tenantID,
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		})
	}

	// 1. Kiểm tra Idempotency để tránh nhân đôi tác vụ khi client retry
	cleanIdempKey := strings.TrimSpace(idempotencyKey)
	if cleanIdempKey != "" {
		existing, err := s.repo.FindByTenantAndIdempotencyKey(ctx, tenantID, cleanIdempKey)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	now := time.Now()
	runID := generateRunID()

	var billingKeyID string
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.KeyID != "" {
		billingKeyID = id.KeyID
	}
	if bId, ok := domain.BillingIdentityFromContext(ctx); ok && bId.KeyID != "" {
		billingKeyID = bId.KeyID
	}

	execConfig := &domain.AgentExecutionConfig{
		Model:                  opts.Model,
		Workspace:              opts.Workspace,
		MaxSteps:               opts.MaxSteps,
		MaxToolCalls:           opts.MaxToolCalls,
		MaxRepeatedCalls:       opts.MaxRepeatedCalls,
		MaxExecutionDuration:   opts.MaxExecutionDuration,
		MaxConsecutiveFailures: opts.MaxConsecutiveFailures,
		Supervised:             opts.Supervised,
		RequireAction:          opts.RequireAction,
		UseSandbox:             opts.UseSandbox,
		AutoMerge:              opts.AutoMerge,
		CustomPrompt:           opts.CustomPrompt,
	}

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		IdempotencyKey:  cleanIdempKey,
		Goal:            goal,
		Status:          domain.RunStatusQueued,
		Model:           opts.Model,
		Workspace:       opts.Workspace,
		MaxSteps:        opts.MaxSteps,
		SecurityContext: secCtx,
		BillingKeyID:    billingKeyID,
		ExecutionConfig: execConfig,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Chèn vào Database với cơ chế xử lý Race Condition từ UNIQUE constraint
	if err := s.repo.Create(ctx, run); err != nil {
		if errors.Is(err, session.ErrIdempotencyConflict) || strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "constraint") {
			// Đã có một request đồng thời tạo thành công -> lấy bản ghi đã có
			if cleanIdempKey != "" {
				existing, findErr := s.repo.FindByTenantAndIdempotencyKey(ctx, tenantID, cleanIdempKey)
				if findErr == nil && existing != nil {
					return existing, nil
				}
			}
		}
		return nil, fmt.Errorf("không thể tạo bản ghi agent run: %w", err)
	}

	// 2. Khởi chạy goroutine nền kế thừa từ root context của JobService
	if err := s.launchBackgroundWorker(run, opts); err != nil {
		return nil, err
	}

	return run, nil
}

func (s *JobService) launchBackgroundWorker(run *domain.AgentRun, opts domain.AgentRunOptions) error {
	s.mu.Lock()
	if !s.accepting {
		s.mu.Unlock()
		return errors.New("máy chủ đang trong quá trình tắt, từ chối nhận thêm tác vụ mới")
	}

	baseCtx := s.rootCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	runCtx, cancel := context.WithCancel(baseCtx)

	// Đính kèm định danh Tenant chính xác từ SecurityContext đã snapshot
	var identity domain.TenantIdentity
	if run.SecurityContext != nil {
		identity = run.SecurityContext.ToTenantIdentity()
	} else {
		identity = domain.TenantIdentity{
			TenantID: run.TenantID,
			Role:     "user",
			Scopes:   []string{domain.ScopeAgent},
		}
	}
	runCtx = domain.ContextWithTenantIdentity(runCtx, identity)
	if run.BillingKeyID != "" {
		runCtx = domain.ContextWithBillingIdentity(runCtx, domain.BillingIdentity{
			TenantID: run.TenantID,
			KeyID:    run.BillingKeyID,
		})
	}

	s.cancels[run.ID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()

	// Thống nhất danh tính: AgentRun.ID == opts.TaskID
	opts.TaskID = run.ID
	if opts.InitialState != nil {
		opts.InitialState.TaskID = run.ID
	}

	runCopy := *run
	go func() {
		defer s.wg.Done()
		s.executeBackground(runCtx, &runCopy, opts)
	}()

	return nil
}

func (s *JobService) executeBackground(ctx context.Context, run *domain.AgentRun, opts domain.AgentRunOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, run.ID)
		s.mu.Unlock()
	}()

	leaseDuration := 60 * time.Second
	heartbeatInterval := 20 * time.Second

	// Atomic claim: nếu không claim được, worker khác đang sở hữu -> DỪNG NGAY
	claimed, err := s.repo.ClaimRun(ctx, run.ID, s.workerID, leaseDuration)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			now := time.Now()
			run.Status = domain.RunStatusCancelled
			run.StopReason = domain.StopReasonCancelled
			run.Error = "Tác vụ bị hủy do máy chủ tắt trong lúc đang nhận việc"
			run.UpdatedAt = now
			run.FinishedAt = &now
			_ = s.repo.Update(context.Background(), run)
		} else {
			log.Printf("[JobService] Lỗi khi claim run %s: %v", run.ID, err)
		}
		return
	}
	if !claimed {
		// Worker khác đang sở hữu run hoặc run không thể claim -> STOP
		return
	}

	// Đọc lại để lấy claim_generation mới nhất được sinh ra từ ClaimRun
	claimedRun, gErr := s.repo.Get(context.Background(), run.ID)
	workerClaimGen := run.ClaimGeneration
	if gErr == nil && claimedRun != nil {
		workerClaimGen = claimedRun.ClaimGeneration
		run.ClaimGeneration = workerClaimGen
		run.WorkerID = claimedRun.WorkerID
	}

	// Thiết lập context thực thi và goroutine heartbeat gia hạn lease
	execCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()

	heartbeatStop := make(chan struct{})
	defer close(heartbeatStop)

	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatStop:
				return
			case <-execCtx.Done():
				return
			case <-ticker.C:
				renewed, rErr := s.repo.RenewLease(execCtx, run.ID, s.workerID, leaseDuration)
				if rErr != nil || !renewed {
					log.Printf("[JobService] Mất quyền lease cho run %s (renewed=%v, err=%v), dừng thực thi an toàn", run.ID, renewed, rErr)
					cancelExec()
					return
				}
			}
		}
	}()

	// Thiết lập Progress Callback để stream sự kiện vào DB & Subscribers
	userProgress := opts.OnProgress
	opts.OnProgress = func(step int, kind string, message string) {
		ev := &domain.AgentRunEvent{
			TenantID:  run.TenantID,
			RunID:     run.ID,
			Step:      step,
			Kind:      kind,
			Message:   message,
			Timestamp: time.Now(),
		}
		_ = s.repo.AppendEvent(context.Background(), ev)
		s.broadcastEvent(run.ID, *ev)

		if userProgress != nil {
			userProgress(step, kind, message)
		}
	}

	state, runErr := s.runner.Run(execCtx, run.Goal, opts)

	now := time.Now()
	run.FinishedAt = &now
	run.UpdatedAt = now

	// Cancel consistency: Kiểm tra xem user có bấm Cancel trong lúc worker đang xử lý không
	currentRun, _ := s.repo.Get(context.Background(), run.ID)
	isCancelled := (currentRun != nil && currentRun.Status == domain.RunStatusCancelled) || ctx.Err() != nil

	if isCancelled {
		run.Status = domain.RunStatusCancelled
		run.StopReason = domain.StopReasonCancelled
		if ctx.Err() != nil && run.Error == "" {
			run.Error = "Tác vụ bị hủy bởi người dùng hoặc hệ thống"
		}
	} else if state != nil {
		run.CurrentStep = state.CurrentStep
		run.TotalToolCalls = state.TotalToolCalls
		run.StopReason = state.StopReason
		run.FinalAnswer = state.FinalAnswer
		run.GitDiff = state.GitDiff
		run.Error = state.Error

		if state.IsCompleted && (state.StopReason == domain.StopReasonCompleted || state.StopReason == "") {
			run.Status = domain.RunStatusCompleted
		} else if state.StopReason == domain.StopReasonCancelled {
			run.Status = domain.RunStatusCancelled
		} else {
			run.Status = domain.RunStatusFailed
		}
	} else if runErr != nil {
		run.Status = domain.RunStatusFailed
		run.Error = runErr.Error()
		if errors.Is(runErr, context.Canceled) {
			run.Status = domain.RunStatusCancelled
			run.StopReason = domain.StopReasonCancelled
		}
	} else {
		run.Status = domain.RunStatusCompleted
	}

	// ISSUE 2: Fencing - Chỉ cho phép ghi kết quả cuối nếu worker vẫn giữ quyền sở hữu (worker_id + claim_generation)
	updated, uErr := s.repo.UpdateOwned(context.Background(), run, s.workerID, workerClaimGen)
	if uErr != nil || !updated {
		log.Printf("[JobService] executeBackground: worker %s mất quyền sở hữu run %s (claim_gen=%d), từ chối ghi đè kết quả", s.workerID, run.ID, workerClaimGen)
		return
	}

	finalEv := domain.AgentRunEvent{
		TenantID:  run.TenantID,
		RunID:     run.ID,
		Step:      run.CurrentStep,
		Kind:      string(run.Status),
		Message:   fmt.Sprintf("Tác vụ kết thúc với trạng thái: %s (Stop reason: %s)", run.Status, run.StopReason),
		Timestamp: now,
	}
	_ = s.repo.AppendEvent(context.Background(), &finalEv)
	s.broadcastEvent(run.ID, finalEv)
	s.closeSubscribers(run.ID)
}

func (s *JobService) GetRun(ctx context.Context, runID string) (*domain.AgentRun, error) {
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.Role != "admin" && id.TenantID != "" {
		return s.GetRunForTenant(ctx, id.TenantID, runID)
	}
	return s.repo.Get(ctx, runID)
}

func (s *JobService) GetRunForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error) {
	return s.repo.GetForTenant(ctx, tenantID, runID)
}

func (s *JobService) CancelRun(ctx context.Context, runID string) error {
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.Role != "admin" && id.TenantID != "" {
		return s.CancelRunForTenant(ctx, id.TenantID, runID)
	}

	s.mu.RLock()
	cancel, ok := s.cancels[runID]
	s.mu.RUnlock()

	if ok && cancel != nil {
		cancel()
	}

	return s.repo.Cancel(ctx, runID)
}

func (s *JobService) CancelRunForTenant(ctx context.Context, tenantID, runID string) error {
	// Kiểm tra quyền sở hữu Tenant trước khi hủy
	if _, err := s.repo.GetForTenant(ctx, tenantID, runID); err != nil {
		return err
	}

	s.mu.RLock()
	cancel, ok := s.cancels[runID]
	s.mu.RUnlock()

	if ok && cancel != nil {
		cancel()
	}

	return s.repo.CancelForTenant(ctx, tenantID, runID)
}

func (s *JobService) ResumeRun(ctx context.Context, runID string, feedback string) (*domain.AgentRun, error) {
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.Role != "admin" && id.TenantID != "" {
		return s.ResumeRunForTenant(ctx, id.TenantID, runID, feedback)
	}

	run, err := s.repo.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	return s.ResumeRunForTenant(ctx, run.TenantID, runID, feedback)
}

func (s *JobService) ResumeRunForTenant(ctx context.Context, tenantID, runID string, feedback string) (*domain.AgentRun, error) {
	run, err := s.repo.GetForTenant(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}

	if run.Status != domain.RunStatusFailed && run.Status != domain.RunStatusCancelled &&
		run.Status != domain.RunStatusWaitingForApproval && run.Status != domain.RunStatusInterrupted {
		return nil, fmt.Errorf("không thể resume tác vụ ở trạng thái: %s", run.Status)
	}

	// Xác thực caller hiện tại từ context
	callerIdentity, hasCaller := domain.TenantIdentityFromContext(ctx)
	if !hasCaller || callerIdentity.TenantID == "" {
		return nil, errors.New("resume_reauthorization_failed: thiếu thông tin xác thực của caller")
	}
	if callerIdentity.Role != "admin" && callerIdentity.TenantID != tenantID {
		return nil, errors.New("resume_reauthorization_failed: không có quyền resume tác vụ của tenant khác")
	}
	if !callerIdentity.HasScope(domain.ScopeAgent) {
		return nil, errors.New("resume_reauthorization_failed: caller thiếu scope 'agent'")
	}

	// ISSUE 3 & ISSUE 5: Re-authorization và intersection
	var effectiveSecCtx *domain.AgentSecurityContext
	if run.SecurityContext == nil {
		// Legacy run missing SecurityContext -> Fail closed nếu không rõ chủ sở hữu
		if callerIdentity.Role != "admin" && callerIdentity.TenantID != run.TenantID {
			return nil, errors.New("resume_reauthorization_failed: legacy run không xác định được chủ sở hữu tenant an toàn")
		}
		effectiveSecCtx = domain.SecurityContextFromTenantIdentity(callerIdentity)
	} else {
		var reauthErr error
		effectiveSecCtx, reauthErr = domain.IntersectSecurityContext(run.SecurityContext, callerIdentity)
		if reauthErr != nil {
			return nil, fmt.Errorf("resume_reauthorization_failed: %w", reauthErr)
		}
	}

	// Kiểm tra workspace restrictions của caller
	if len(callerIdentity.AllowedWorkspaceRoots) > 0 && run.Workspace != "" {
		matched := false
		for _, root := range callerIdentity.AllowedWorkspaceRoots {
			if strings.HasPrefix(run.Workspace, root) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, errors.New("resume_reauthorization_failed: caller không có quyền truy cập workspace của tác vụ")
		}
	}

	// 1. Phục hồi từ Checkpoint thật
	if s.checkpointRepo == nil {
		return nil, fmt.Errorf("run_not_resumable: hệ thống CheckpointRepository chưa được khởi tạo")
	}

	cpCtx := domain.ContextWithTenantIdentity(ctx, effectiveSecCtx.ToTenantIdentity())
	latestCP, err := s.checkpointRepo.GetLatestCheckpoint(cpCtx, runID)
	if err != nil || latestCP == nil {
		return nil, fmt.Errorf("run_not_resumable: không tìm thấy checkpoint khả dụng cho run %s: %v", runID, err)
	}

	now := time.Now()
	newRunID := generateRunID()

	// 2. Chuẩn bị ngữ cảnh và kế thừa quan hệ cha - con (ParentRunID / ResumeFromRunID)
	resumedState := latestCP.StateSnapshot
	resumedState.TaskID = newRunID
	if feedback != "" {
		resumedState.Messages = append(resumedState.Messages, domain.OpenAIMessage{
			Role:    "user",
			Content: fmt.Sprintf("[USER RESUME FEEDBACK]: %s", feedback),
		})
	}

	opts := reconstructOptions(run, latestCP)
	opts.TaskID = newRunID
	opts.InitialState = &resumedState

	// Áp dụng các giới hạn an toàn từ effectiveSecCtx lên opts
	if effectiveSecCtx.MaxAgentSteps > 0 && (opts.MaxSteps <= 0 || opts.MaxSteps > effectiveSecCtx.MaxAgentSteps) {
		opts.MaxSteps = effectiveSecCtx.MaxAgentSteps
	}
	opts.RequireAction = opts.RequireAction || effectiveSecCtx.RequireApproval
	opts.UseSandbox = opts.UseSandbox || effectiveSecCtx.EnforceSandbox
	opts.AutoMerge = opts.AutoMerge && effectiveSecCtx.AutoMergeAllowed

	newExecConfig := run.ExecutionConfig
	if newExecConfig == nil {
		newExecConfig = &domain.AgentExecutionConfig{
			Model:                  opts.Model,
			Workspace:              opts.Workspace,
			MaxSteps:               opts.MaxSteps,
			MaxToolCalls:           opts.MaxToolCalls,
			MaxRepeatedCalls:       opts.MaxRepeatedCalls,
			MaxExecutionDuration:   opts.MaxExecutionDuration,
			MaxConsecutiveFailures: opts.MaxConsecutiveFailures,
			Supervised:             opts.Supervised,
			RequireAction:          opts.RequireAction,
			UseSandbox:             opts.UseSandbox,
			AutoMerge:              opts.AutoMerge,
			CustomPrompt:           opts.CustomPrompt,
		}
	} else {
		copied := *newExecConfig
		copied.MaxSteps = opts.MaxSteps
		copied.RequireAction = opts.RequireAction
		copied.UseSandbox = opts.UseSandbox
		copied.AutoMerge = opts.AutoMerge
		newExecConfig = &copied
	}

	newRun := &domain.AgentRun{
		ID:              newRunID,
		TenantID:        tenantID,
		Goal:            run.Goal,
		Status:          domain.RunStatusQueued,
		Model:           opts.Model,
		Workspace:       opts.Workspace,
		CurrentStep:     latestCP.StepIndex,
		MaxSteps:        opts.MaxSteps,
		ParentRunID:     run.ID,
		ResumeFromRunID: run.ID,
		SecurityContext: effectiveSecCtx,
		BillingKeyID:    callerIdentity.KeyID,
		ExecutionConfig: newExecConfig,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.Create(ctx, newRun); err != nil {
		return nil, fmt.Errorf("không thể tạo bản ghi resume agent run: %w", err)
	}

	resumeEv := domain.AgentRunEvent{
		TenantID:  tenantID,
		RunID:     newRun.ID,
		Step:      newRun.CurrentStep,
		Kind:      "run_resumed",
		Message:   fmt.Sprintf("Tác vụ được phục hồi thành công từ checkpoint của tác vụ gốc %s với quyền hạn re-authorized", run.ID),
		Timestamp: now,
	}
	_ = s.repo.AppendEvent(ctx, &resumeEv)

	_ = s.launchBackgroundWorker(newRun, opts)
	return newRun, nil
}

func (s *JobService) SubscribeEvents(ctx context.Context, runID string) (<-chan domain.AgentRunEvent, func(), error) {
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.Role != "admin" && id.TenantID != "" {
		return s.SubscribeEventsForTenant(ctx, id.TenantID, runID)
	}

	return s.registerSubscriber(runID)
}

func (s *JobService) SubscribeEventsForTenant(ctx context.Context, tenantID, runID string) (<-chan domain.AgentRunEvent, func(), error) {
	// Kiểm tra quyền truy cập của Tenant trước khi cho phép subscribe
	if _, err := s.repo.GetForTenant(ctx, tenantID, runID); err != nil {
		return nil, nil, err
	}

	return s.registerSubscriber(runID)
}

func (s *JobService) registerSubscriber(runID string) (<-chan domain.AgentRunEvent, func(), error) {
	ch := make(chan domain.AgentRunEvent, 50)

	s.subsMu.Lock()
	s.subscribers[runID] = append(s.subscribers[runID], ch)
	s.subsMu.Unlock()

	unsubscribe := func() {
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		subs := s.subscribers[runID]
		for i, sub := range subs {
			if sub == ch {
				s.subscribers[runID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				break
			}
		}
	}

	return ch, unsubscribe, nil
}

func (s *JobService) broadcastEvent(runID string, event domain.AgentRunEvent) {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()

	subs := s.subscribers[runID]
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// Tránh làm block worker nếu subscriber đọc chậm
		}
	}
}

func (s *JobService) closeSubscribers(runID string) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()

	subs := s.subscribers[runID]
	for _, ch := range subs {
		close(ch)
	}
	delete(s.subscribers, runID)
}
