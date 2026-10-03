package redis_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	redisadapter "dezuxk-gateway/internal/adapters/outbound/distributed/redis"
	"dezuxk-gateway/internal/core/ports"
)

func setupTestRedis(t *testing.T) (*miniredis.Miniredis, goredis.UniversalClient) {
	t.Helper()
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("khởi tạo miniredis thất bại: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
	})

	rdb := goredis.NewClient(&goredis.Options{
		Addr: s.Addr(),
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	return s, rdb
}

func TestRedisDistributedLocker(t *testing.T) {
	_, rdb := setupTestRedis(t)
	locker := redisadapter.NewRedisDistributedLocker(rdb)
	ctx := context.Background()

	// 1. Acquire lock thành công
	handle1, ok1, err := locker.AcquireLock(ctx, "resource-1", 5*time.Second)
	if err != nil || !ok1 || handle1 == nil {
		t.Fatalf("kỳ vọng acquire lock thành công, err: %v, ok: %v", err, ok1)
	}

	// 2. Gia hạn lock thành công
	if err := handle1.Renew(ctx, 10*time.Second); err != nil {
		t.Fatalf("kỳ vọng gia hạn lock thành công: %v", err)
	}

	// 3. Node thứ 2 acquire cùng key sẽ bị từ chối
	handle2, ok2, err := locker.AcquireLock(ctx, "resource-1", 5*time.Second)
	if err != nil || ok2 || handle2 != nil {
		t.Fatalf("kỳ vọng acquire lock bị từ chối, err: %v, ok: %v", err, ok2)
	}

	// 4. Node 1 release lock thành công
	if err := handle1.Release(ctx); err != nil {
		t.Fatalf("kỳ vọng release lock thành công: %v", err)
	}

	// 5. Node 2 giờ có thể acquire được
	handle3, ok3, err := locker.AcquireLock(ctx, "resource-1", 5*time.Second)
	if err != nil || !ok3 || handle3 == nil {
		t.Fatalf("kỳ vọng node 2 acquire lock thành công sau khi release: %v", err)
	}
	_ = handle3.Release(ctx)
}

func TestRedisSharedRateLimiter(t *testing.T) {
	_, rdb := setupTestRedis(t)
	limiter := redisadapter.NewRedisSharedRateLimiter(rdb, 5, 1*time.Minute)
	limiter.SetLimits(2, 2)
	ctx := context.Background()

	id := ports.RateLimitIdentity{
		TenantID: "tenant-a",
		KeyID:    "key-123",
		Endpoint: "/v1/chat/completions",
		Model:    "gemini-2.5-pro",
	}

	// 5 requests đầu phải được phép
	for i := 0; i < 5; i++ {
		dec, err := limiter.Allow(ctx, id)
		if err != nil {
			t.Fatalf("lỗi allow: %v", err)
		}
		if !dec.Allowed {
			t.Fatalf("kỳ vọng request %d được phép", i+1)
		}
	}

	// Request thứ 6 phải bị chặn
	dec, err := limiter.Allow(ctx, id)
	if err != nil {
		t.Fatalf("lỗi allow: %v", err)
	}
	if dec.Allowed {
		t.Fatalf("kỳ vọng request 6 bị từ chối do vượt rate limit")
	}
	if dec.RetryAfterSec <= 0 {
		t.Fatalf("kỳ vọng RetryAfterSec > 0, got %d", dec.RetryAfterSec)
	}

	// Test Concurrency
	rel1, ok1, err := limiter.AcquireConcurrency(ctx, id)
	if err != nil || !ok1 || rel1 == nil {
		t.Fatalf("kỳ vọng acquire concurrency 1 thành công: %v", err)
	}

	rel2, ok2, err := limiter.AcquireConcurrency(ctx, id)
	if err != nil || !ok2 || rel2 == nil {
		t.Fatalf("kỳ vọng acquire concurrency 2 thành công: %v", err)
	}

	// Concurrency thứ 3 vượt giới hạn maxConcurrent=2
	rel3, ok3, err := limiter.AcquireConcurrency(ctx, id)
	if err != nil || ok3 || rel3 != nil {
		t.Fatalf("kỳ vọng concurrency 3 bị từ chối")
	}

	// Release 1 slot
	rel1()

	// Bây giờ có thể acquire lại
	rel4, ok4, err := limiter.AcquireConcurrency(ctx, id)
	if err != nil || !ok4 || rel4 == nil {
		t.Fatalf("kỳ vọng acquire concurrency sau khi release thành công")
	}
	rel2()
	rel4()
}

func TestRedisEventBus(t *testing.T) {
	_, rdb := setupTestRedis(t)
	bus := redisadapter.NewRedisEventBus(rdb)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, unsubscribe, err := bus.Subscribe(ctx, "agent:events:run-123")
	if err != nil {
		t.Fatalf("subscribe thất bại: %v", err)
	}
	defer unsubscribe()

	msg := []byte(`{"event_id":1,"type":"started"}`)
	var wg sync.WaitGroup
	wg.Add(1)

	var received []byte
	go func() {
		defer wg.Done()
		select {
		case ev := <-events:
			received = ev
		case <-time.After(2 * time.Second):
		}
	}()

	time.Sleep(50 * time.Millisecond)
	if err := bus.Publish(ctx, "agent:events:run-123", msg); err != nil {
		t.Fatalf("publish thất bại: %v", err)
	}

	wg.Wait()
	if string(received) != string(msg) {
		t.Fatalf("kỳ vọng nhận tin '%s', nhận được '%s'", string(msg), string(received))
	}
}

func TestRedisSharedStateStore(t *testing.T) {
	_, rdb := setupTestRedis(t)
	store := redisadapter.NewRedisSharedStateStore(rdb, "test:")
	ctx := context.Background()

	// Set & Get
	err := store.Set(ctx, "foo", []byte("bar"), 5*time.Second)
	if err != nil {
		t.Fatalf("set thất bại: %v", err)
	}

	val, err := store.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("get thất bại: %v", err)
	}
	if string(val) != "bar" {
		t.Fatalf("kỳ vọng 'bar', got '%s'", string(val))
	}

	// Delete
	err = store.Delete(ctx, "foo")
	if err != nil {
		t.Fatalf("delete thất bại: %v", err)
	}

	valAfter, err := store.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("get after delete thất bại: %v", err)
	}
	if valAfter != nil {
		t.Fatalf("kỳ vọng nil sau khi xóa, got %v", valAfter)
	}
}
