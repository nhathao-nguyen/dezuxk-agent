package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
	cancels     map[string]context.CancelFunc
	subsMu      sync.RWMutex
	subscribers map[string][]chan domain.AgentRunEvent

	wg            sync.WaitGroup
	rootCtx       context.Context
	cancelRoot    context.CancelFunc
	acceptingJobs atomic.Bool
}

// NewJobService khởi tạo service quản lý Job Agent
func NewJobService(repo ports.AgentRunRepository, runner ports.AgentRunner) *JobService {
	rootCtx, cancel := context.WithCancel(context.Background())
	js := &JobService{
		repo:        repo,
		runner:      runner,
		workerID:    fmt.Sprintf("worker-%s", generateRunID()[:10]),
		cancels:     make(map[string]context.CancelFunc),
		subscribers: make(map[string][]chan domain.AgentRunEvent),
		rootCtx:     rootCtx,
		cancelRoot:  cancel,
	}
	js.acceptingJobs.Store(true)
	return js
}

// SetCheckpointRepository cấu hình checkpoint repository phục vụ phục hồi và resume
func (s *JobService) SetCheckpointRepository(cp ports.CheckpointRepository) {
	s.checkpointRepo = cp
}

var _ ports.AgentJobService = (*JobService)(nil)

// Start khởi chạy JobService với context gốc của máy chủ
func (s *JobService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelRoot != nil {
		s.cancelRoot()
	}
	s.rootCtx, s.cancelRoot = context.WithCancel(ctx)
	s.acceptingJobs.Store(true)
	return nil
}

// Shutdown dừng tiếp nhận job mới, hủy/drain các worker đang chạy và đợi hoàn tất
func (s *JobService) Shutdown(ctx context.Context) error {
	// 1. Dừng nhận job mới
	s.acceptingJobs.Store(false)

	// 2. Hủy các tác vụ đang thực thi
	s.mu.Lock()
	for _, cancel := range s.cancels {
		if cancel != nil {
			cancel()
		}
	}
	s.mu.Unlock()

	if s.cancelRoot != nil {
		s.cancelRoot()
	}

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

// RecoverPendingRuns quét và khôi phục các tác vụ dở dang từ SQLite sau khi server khởi động lại
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
			// Requeue tác vụ và chạy nền
			recovEv := domain.AgentRunEvent{
				TenantID:  run.TenantID,
				RunID:     run.ID,
				Step:      run.CurrentStep,
				Kind:      "run_recovered",
				Message:   "Tác vụ queued được tự động xếp hàng thực thi lại sau restart",
				Timestamp: time.Now(),
			}
			_ = s.repo.AppendEvent(ctx, &recovEv)

			opts := domain.AgentRunOptions{
				Model:     run.Model,
				Workspace: run.Workspace,
				MaxSteps:  run.MaxSteps,
			}
			s.launchBackgroundWorker(run, opts)
			processed = append(processed, run)

		case domain.RunStatusRunning:
			// Quá trình trước đã dừng đột ngột
			var cp *domain.AgentCheckpoint
			if s.checkpointRepo != nil {
				cp, _ = s.checkpointRepo.GetLatestCheckpoint(ctx, run.ID)
			}

			if cp != nil && len(cp.StateSnapshot.Messages) > 0 {
				run.Status = domain.RunStatusRecovering
				run.UpdatedAt = time.Now()
				_ = s.repo.Update(ctx, run)

				recovEv := domain.AgentRunEvent{
					TenantID:  run.TenantID,
					RunID:     run.ID,
					Step:      cp.StepIndex,
					Kind:      "run_recovered",
					Message:   fmt.Sprintf("Tác vụ được phục hồi thành công từ Checkpoint #%d sau restart", cp.StepIndex),
					Timestamp: time.Now(),
				}
				_ = s.repo.AppendEvent(ctx, &recovEv)

				opts := domain.AgentRunOptions{
					Model:        run.Model,
					Workspace:    run.Workspace,
					MaxSteps:     run.MaxSteps,
					InitialState: &cp.StateSnapshot,
				}
				s.launchBackgroundWorker(run, opts)
				processed = append(processed, run)
			} else {
				// Không có checkpoint an toàn -> đánh dấu interrupted với stop reason server_restart
				now := time.Now()
				run.Status = domain.RunStatusInterrupted
				run.StopReason = domain.StopReasonServerRestart
				run.Error = "Server khởi động lại và tác vụ chưa kịp lưu checkpoint an toàn để tiếp tục"
				run.UpdatedAt = now
				run.FinishedAt = &now
				_ = s.repo.Update(ctx, run)

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

		case domain.RunStatusWaitingForTool:
			var cp *domain.AgentCheckpoint
			if s.checkpointRepo != nil {
				cp, _ = s.checkpointRepo.GetLatestCheckpoint(ctx, run.ID)
			}
			if cp != nil {
				run.Status = domain.RunStatusRecovering
				run.UpdatedAt = time.Now()
				_ = s.repo.Update(ctx, run)

				opts := domain.AgentRunOptions{
					Model:        run.Model,
					Workspace:    run.Workspace,
					MaxSteps:     run.MaxSteps,
					InitialState: &cp.StateSnapshot,
				}
				s.launchBackgroundWorker(run, opts)
				processed = append(processed, run)
			} else {
				now := time.Now()
				run.Status = domain.RunStatusInterrupted
				run.StopReason = domain.StopReasonServerRestart
				run.Error = "Tác vụ chờ công cụ bị gián đoạn do restart và thiếu checkpoint"
				run.UpdatedAt = now
				run.FinishedAt = &now
				_ = s.repo.Update(ctx, run)

				interEv := domain.AgentRunEvent{
					TenantID:  run.TenantID,
					RunID:     run.ID,
					Step:      run.CurrentStep,
					Kind:      "run_interrupted",
					Message:   "Tác vụ chờ công cụ bị gián đoạn do server restart",
					Timestamp: now,
				}
				_ = s.repo.AppendEvent(ctx, &interEv)
				processed = append(processed, run)
			}
		}
	}

	return processed, nil
}

func (s *JobService) SubmitRun(ctx context.Context, goal string, opts domain.AgentRunOptions, idempotencyKey string) (*domain.AgentRun, error) {
	if !s.acceptingJobs.Load() {
		return nil, errors.New("máy chủ đang trong quá trình tắt, từ chối nhận thêm tác vụ mới")
	}

	tenantID := "default"
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
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

	run := &domain.AgentRun{
		ID:             runID,
		TenantID:       tenantID,
		IdempotencyKey: cleanIdempKey,
		Goal:           goal,
		Status:         domain.RunStatusQueued,
		Model:          opts.Model,
		Workspace:      opts.Workspace,
		MaxSteps:       opts.MaxSteps,
		CreatedAt:      now,
		UpdatedAt:      now,
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
	s.launchBackgroundWorker(run, opts)

	return run, nil
}

func (s *JobService) launchBackgroundWorker(run *domain.AgentRun, opts domain.AgentRunOptions) {
	baseCtx := s.rootCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	runCtx, cancel := context.WithCancel(baseCtx)
	// Đính kèm định danh Tenant vào Context chạy nền
	identity := domain.TenantIdentity{
		TenantID: run.TenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	}
	runCtx = domain.ContextWithTenantIdentity(runCtx, identity)

	s.mu.Lock()
	s.cancels[run.ID] = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.executeBackground(runCtx, run, opts)
	}()
}

func (s *JobService) executeBackground(ctx context.Context, run *domain.AgentRun, opts domain.AgentRunOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, run.ID)
		s.mu.Unlock()
	}()

	// Atomic claim hoặc chuyển trạng thái sang running
	claimed, err := s.repo.ClaimRun(ctx, run.ID, s.workerID, 10*time.Minute)
	if err != nil || !claimed {
		// Thử cập nhật trạng thái nếu ClaimRun không áp dụng
		run.Status = domain.RunStatusRunning
		run.UpdatedAt = time.Now()
		_ = s.repo.Update(context.Background(), run)
	}

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

	state, runErr := s.runner.Run(ctx, run.Goal, opts)

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

	_ = s.repo.Update(context.Background(), run)

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

	// 1. Phục hồi từ Checkpoint thật
	if s.checkpointRepo == nil {
		return nil, fmt.Errorf("run_not_resumable: hệ thống CheckpointRepository chưa được khởi tạo")
	}

	latestCP, err := s.checkpointRepo.GetLatestCheckpoint(ctx, runID)
	if err != nil || latestCP == nil {
		return nil, fmt.Errorf("run_not_resumable: không tìm thấy checkpoint khả dụng cho run %s: %v", runID, err)
	}

	// 2. Chuẩn bị ngữ cảnh và kế thừa quan hệ cha - con (ParentRunID / ResumeFromRunID)
	resumedState := latestCP.StateSnapshot
	if feedback != "" {
		resumedState.Messages = append(resumedState.Messages, domain.OpenAIMessage{
			Role:    "user",
			Content: fmt.Sprintf("[USER RESUME FEEDBACK]: %s", feedback),
		})
	}

	opts := domain.AgentRunOptions{
		Model:        run.Model,
		Workspace:    run.Workspace,
		MaxSteps:     run.MaxSteps,
		InitialState: &resumedState,
	}

	now := time.Now()
	newRunID := generateRunID()

	newRun := &domain.AgentRun{
		ID:              newRunID,
		TenantID:        tenantID,
		Goal:            run.Goal,
		Status:          domain.RunStatusQueued,
		Model:           run.Model,
		Workspace:       run.Workspace,
		CurrentStep:     latestCP.StepIndex,
		MaxSteps:        run.MaxSteps,
		ParentRunID:     run.ID,
		ResumeFromRunID: run.ID,
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
		Message:   fmt.Sprintf("Tác vụ được phục hồi thành công từ checkpoint của tác vụ gốc %s", run.ID),
		Timestamp: now,
	}
	_ = s.repo.AppendEvent(ctx, &resumeEv)

	s.launchBackgroundWorker(newRun, opts)
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
