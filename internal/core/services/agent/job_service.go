package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

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

// JobService triển khai ports.AgentJobService quản lý tác vụ Agent chạy nền bất đồng bộ
type JobService struct {
	repo          ports.AgentRunRepository
	runner        ports.AgentRunner
	mu            sync.RWMutex
	cancels       map[string]context.CancelFunc
	subsMu        sync.RWMutex
	subscribers   map[string][]chan domain.AgentRunEvent
}

// NewJobService khởi tạo service quản lý Job Agent
func NewJobService(repo ports.AgentRunRepository, runner ports.AgentRunner) *JobService {
	return &JobService{
		repo:        repo,
		runner:      runner,
		cancels:     make(map[string]context.CancelFunc),
		subscribers: make(map[string][]chan domain.AgentRunEvent),
	}
}

var _ ports.AgentJobService = (*JobService)(nil)

func (s *JobService) SubmitRun(ctx context.Context, goal string, opts domain.AgentRunOptions, idempotencyKey string) (*domain.AgentRun, error) {
	tenantID := "default"
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
	}

	// 1. Kiểm tra Idempotency để tránh nhân đôi tác vụ khi client retry
	if idempotencyKey != "" {
		existing, err := s.repo.FindByTenantAndIdempotencyKey(ctx, tenantID, idempotencyKey)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	now := time.Now()
	runID := generateRunID()

	run := &domain.AgentRun{
		ID:             runID,
		TenantID:       tenantID,
		IdempotencyKey: idempotencyKey,
		Goal:           goal,
		Status:         domain.RunStatusQueued,
		Model:          opts.Model,
		Workspace:      opts.Workspace,
		MaxSteps:       opts.MaxSteps,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.repo.Create(ctx, run); err != nil {
		return nil, fmt.Errorf("không thể tạo bản ghi agent run: %w", err)
	}

	// 2. Khởi chạy goroutine nền độc lập với HTTP request context của client
	runCtx, cancel := context.WithCancel(context.Background())
	// Sao chép tenant identity sang context chạy nền
	if id, ok := domain.TenantIdentityFromContext(ctx); ok {
		runCtx = domain.ContextWithTenantIdentity(runCtx, id)
	}

	s.mu.Lock()
	s.cancels[runID] = cancel
	s.mu.Unlock()

	go s.executeBackground(runCtx, run, opts)

	return run, nil
}

func (s *JobService) executeBackground(ctx context.Context, run *domain.AgentRun, opts domain.AgentRunOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, run.ID)
		s.mu.Unlock()
	}()

	// Đổi trạng thái sang running
	run.Status = domain.RunStatusRunning
	run.UpdatedAt = time.Now()
	_ = s.repo.Update(context.Background(), run)

	// Thiết lập Progress Callback để stream sự kiện vào DB & Subscribers
	userProgress := opts.OnProgress
	opts.OnProgress = func(step int, kind string, message string) {
		ev := &domain.AgentRunEvent{
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

	state, err := s.runner.Run(ctx, run.Goal, opts)

	now := time.Now()
	run.FinishedAt = &now
	run.UpdatedAt = now

	if state != nil {
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
	} else if err != nil {
		run.Status = domain.RunStatusFailed
		run.Error = err.Error()
		if errors.Is(err, context.Canceled) {
			run.Status = domain.RunStatusCancelled
			run.StopReason = domain.StopReasonCancelled
		}
	} else {
		run.Status = domain.RunStatusCompleted
	}

	_ = s.repo.Update(context.Background(), run)

	finalEv := domain.AgentRunEvent{
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
	return s.repo.Get(ctx, runID)
}

func (s *JobService) CancelRun(ctx context.Context, runID string) error {
	s.mu.RLock()
	cancel, ok := s.cancels[runID]
	s.mu.RUnlock()

	if ok && cancel != nil {
		cancel()
	}

	return s.repo.Cancel(ctx, runID)
}

func (s *JobService) ResumeRun(ctx context.Context, runID string, feedback string) (*domain.AgentRun, error) {
	run, err := s.repo.Get(ctx, runID)
	if err != nil {
		return nil, err
	}

	if run.Status != domain.RunStatusFailed && run.Status != domain.RunStatusCancelled && run.Status != domain.RunStatusWaitingForApproval {
		return nil, fmt.Errorf("không thể resume tác vụ ở trạng thái: %s", run.Status)
	}

	newGoal := run.Goal
	if feedback != "" {
		newGoal = fmt.Sprintf("%s\n\n[USER RESUME FEEDBACK]: %s", run.Goal, feedback)
	}

	opts := domain.AgentRunOptions{
		Model:     run.Model,
		Workspace: run.Workspace,
		MaxSteps:  run.MaxSteps,
	}

	return s.SubmitRun(ctx, newGoal, opts, "")
}

func (s *JobService) SubscribeEvents(ctx context.Context, runID string) (<-chan domain.AgentRunEvent, func(), error) {
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
			// Tránh làm block worker nếu channel bị nghẽn
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
