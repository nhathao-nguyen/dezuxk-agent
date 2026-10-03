package agent_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	redisadapter "dezuxk-gateway/internal/adapters/outbound/distributed/redis"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"
)

type dummySlowRunner struct {
	started chan struct{}
}

func (d *dummySlowRunner) Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error) {
	if d.started != nil {
		select {
		case d.started <- struct{}{}:
		default:
		}
	}
	if opts.OnProgress != nil {
		opts.OnProgress(1, "thinking", "đang suy nghĩ...")
		opts.OnProgress(2, "action", "đang chạy công cụ...")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Second):
		return &domain.AgentState{
			IsCompleted: true,
			CurrentStep: 2,
			StopReason:  domain.StopReasonCompleted,
			FinalAnswer: "hoàn thành",
		}, nil
	}
}

func TestJobService_CrossNodeSSEFanOut(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis error: %v", err)
	}
	defer s.Close()

	rdbA := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer rdbA.Close()
	busA := redisadapter.NewRedisEventBus(rdbA)

	rdbB := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer rdbB.Close()
	busB := redisadapter.NewRedisEventBus(rdbB)

	memRepo := session.NewMemoryAgentRunRepository()
	runner := &dummySlowRunner{started: make(chan struct{}, 1)}

	// Node A có worker thực thi
	jobServiceA := agent.NewJobService(memRepo, runner)
	jobServiceA.SetEventBus(busA)
	jobServiceA.SetWorkerID("node-a-worker")

	// Node B chỉ kết nối client SSE
	jobServiceB := agent.NewJobService(memRepo, nil)
	jobServiceB.SetEventBus(busB)
	jobServiceB.SetWorkerID("node-b-api")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run, err := jobServiceA.SubmitRun(ctx, "kiểm tra cross-node SSE", domain.AgentRunOptions{}, "")
	if err != nil {
		t.Fatalf("submit run thất bại: %v", err)
	}

	// Đăng ký nhận SSE trên Node B
	eventsCh, unsub, err := jobServiceB.SubscribeEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("subscribe trên Node B thất bại: %v", err)
	}
	defer unsub()

	var receivedKinds []string
	var mu sync.Mutex

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventsCh:
				if !ok {
					return
				}
				mu.Lock()
				receivedKinds = append(receivedKinds, ev.Kind)
				if len(receivedKinds) >= 2 {
					mu.Unlock()
					return
				}
				mu.Unlock()
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("hết thời gian chờ nhận sự kiện qua Node B")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(receivedKinds) < 2 {
		t.Fatalf("kỳ vọng nhận ít nhất 2 sự kiện qua Node B, nhận được: %v", receivedKinds)
	}
}

func TestJobService_CrossNodeCancellation(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis error: %v", err)
	}
	defer s.Close()

	rdbA := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer rdbA.Close()
	busA := redisadapter.NewRedisEventBus(rdbA)

	rdbB := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer rdbB.Close()
	busB := redisadapter.NewRedisEventBus(rdbB)

	memRepo := session.NewMemoryAgentRunRepository()
	startedCh := make(chan struct{}, 1)
	runner := &dummySlowRunner{started: startedCh}

	// Node A: chạy tác vụ
	jobServiceA := agent.NewJobService(memRepo, runner)
	jobServiceA.SetEventBus(busA)
	jobServiceA.SetWorkerID("node-a-worker")

	// Node B: nhận request cancel từ HTTP
	jobServiceB := agent.NewJobService(memRepo, nil)
	jobServiceB.SetEventBus(busB)
	jobServiceB.SetWorkerID("node-b-api")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run, err := jobServiceA.SubmitRun(ctx, "kiểm tra cross-node cancel", domain.AgentRunOptions{}, "")
	if err != nil {
		t.Fatalf("submit run thất bại: %v", err)
	}

	// Chờ worker trên Node A bắt đầu
	select {
	case <-startedCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("worker không bắt đầu đúng hạn")
	}

	// Yêu cầu Cancel gửi tới Node B
	if err := jobServiceB.CancelRun(ctx, run.ID); err != nil {
		t.Fatalf("cancel run qua Node B thất bại: %v", err)
	}

	// Chờ một chút để tín hiệu cancel truyền qua Redis tới worker Node A
	time.Sleep(200 * time.Millisecond)

	cancelledRun, err := memRepo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("lấy run thất bại: %v", err)
	}
	if cancelledRun.Status != domain.RunStatusCancelled {
		t.Fatalf("kỳ vọng run trạng thái cancelled, got: %s", cancelledRun.Status)
	}
}
