package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
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
	return &domain.AgentState{
		Goal:        goal,
		IsCompleted: true,
		StopReason:  domain.StopReasonCompleted,
		FinalAnswer: "Task done: " + goal,
	}, nil
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

	// Chờ hoàn thành
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

func TestJobService_CancelRun(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	runner := &dummyRunner{delay: 200 * time.Millisecond}
	svc := NewJobService(repo, runner)

	ctx := context.Background()
	run, err := svc.SubmitRun(ctx, "Long running job", domain.AgentRunOptions{MaxSteps: 10}, "")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Đợi worker chuyển sang running
	time.Sleep(20 * time.Millisecond)

	// Hủy job
	if err := svc.CancelRun(ctx, run.ID); err != nil {
		t.Fatalf("cancel run failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	cancelledRun, err := svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run failed: %v", err)
	}

	if cancelledRun.Status != domain.RunStatusCancelled {
		t.Fatalf("expected status cancelled, got: %s", cancelledRun.Status)
	}
}
