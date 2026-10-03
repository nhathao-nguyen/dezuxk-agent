package leader_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	redisadapter "dezuxk-gateway/internal/adapters/outbound/distributed/redis"
	"dezuxk-gateway/internal/core/services/leader"
)

func TestLeaderCoordinatorFailover(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("khởi tạo miniredis thất bại: %v", err)
	}
	defer s.Close()

	rdb1 := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer rdb1.Close()
	locker1 := redisadapter.NewRedisDistributedLocker(rdb1)

	rdb2 := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer rdb2.Close()
	locker2 := redisadapter.NewRedisDistributedLocker(rdb2)

	coord1 := leader.NewCoordinator(locker1, "node-1", 1*time.Second)
	coord2 := leader.NewCoordinator(locker2, "node-2", 1*time.Second)

	var node1Runs, node2Runs atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	coord1.RegisterJob("golden-job", func(leaderCtx context.Context) {
		node1Runs.Add(1)
		<-leaderCtx.Done()
	})

	coord2.RegisterJob("golden-job", func(leaderCtx context.Context) {
		node2Runs.Add(1)
		<-leaderCtx.Done()
	})

	coord1.Start(ctx)
	coord2.Start(ctx)

	// Đợi node 1 chiếm quyền leader
	time.Sleep(100 * time.Millisecond)

	if !coord1.IsLeader("golden-job") && !coord2.IsLeader("golden-job") {
		t.Fatalf("kỳ vọng ít nhất 1 node trở thành leader")
	}

	// Chỉ duy nhất 1 node là leader
	if coord1.IsLeader("golden-job") && coord2.IsLeader("golden-job") {
		t.Fatalf("lỗi: cả 2 node đều là leader cùng lúc")
	}

	// Giả lập node 1 chết/dừng lại
	coord1.Stop()

	// Chờ node 2 tiếp quản (failover takeover)
	time.Sleep(500 * time.Millisecond)

	if !coord2.IsLeader("golden-job") {
		// Thử thêm 500ms nữa nếu lock TTL chưa hết
		time.Sleep(800 * time.Millisecond)
	}

	if !coord2.IsLeader("golden-job") {
		t.Fatalf("kỳ vọng node 2 tiếp quản quyền leader sau khi node 1 dừng")
	}

	coord2.Stop()
}
