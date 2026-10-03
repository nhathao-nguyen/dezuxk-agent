package leader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/ports"
)

// SingletonJob hàm công việc chỉ được chạy duy nhất bởi 1 node leader trong cluster
type SingletonJob func(ctx context.Context)

// Coordinator điều phối bầu chọn Leader và chạy các tác vụ singleton
type Coordinator struct {
	mu            sync.RWMutex
	locker        ports.DistributedLocker
	nodeID        string
	lockTTL       time.Duration
	retryInterval time.Duration
	jobs          map[string]SingletonJob
	isLeaderMap   map[string]bool
	cancelFuncs   map[string]context.CancelFunc
	running       bool
}

// NewCoordinator khởi tạo Leader Coordinator
func NewCoordinator(locker ports.DistributedLocker, nodeID string, lockTTL ...time.Duration) *Coordinator {
	ttl := 10 * time.Second
	if len(lockTTL) > 0 && lockTTL[0] > 0 {
		ttl = lockTTL[0]
	}
	retry := ttl / 3
	if retry <= 0 {
		retry = 200 * time.Millisecond
	} else if retry > 2*time.Second {
		retry = 2 * time.Second
	}
	return &Coordinator{
		locker:        locker,
		nodeID:        nodeID,
		lockTTL:       ttl,
		retryInterval: retry,
		jobs:          make(map[string]SingletonJob),
		isLeaderMap:   make(map[string]bool),
		cancelFuncs:   make(map[string]context.CancelFunc),
	}
}

// SetRetryInterval cấu hình chu kỳ thử lại khi tranh cử leader
func (c *Coordinator) SetRetryInterval(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.retryInterval = d
	}
}

// RegisterJob đăng ký một tác vụ singleton cần bầu leader
func (c *Coordinator) RegisterJob(role string, job SingletonJob) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs[role] = job
}

// IsLeader kiểm tra xem node hiện tại có đang là leader của role hay không
func (c *Coordinator) IsLeader(role string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isLeaderMap[role]
}

// Start khởi chạy vòng lặp tranh cử leader cho mọi role đã đăng ký
func (c *Coordinator) Start(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true

	for role, job := range c.jobs {
		jobRole := role
		jobFunc := job
		jobCtx, cancel := context.WithCancel(ctx)
		c.cancelFuncs[jobRole] = cancel

		go c.campaignLoop(jobCtx, jobRole, jobFunc)
	}
	c.mu.Unlock()
}

// Stop dừng việc tranh cử và giải phóng các khóa leader đang giữ
func (c *Coordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	for _, cancel := range c.cancelFuncs {
		cancel()
	}
	c.cancelFuncs = make(map[string]context.CancelFunc)
}

func (c *Coordinator) campaignLoop(ctx context.Context, role string, job SingletonJob) {
	key := fmt.Sprintf("singleton:%s", role)
	renewInterval := c.lockTTL / 3
	if renewInterval <= 0 {
		renewInterval = 2 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if c.locker == nil {
			// Single-node mode không có distributed locker: tự động chạy như leader
			c.setLeaderStatus(role, true)
			job(ctx)
			return
		}

		handle, acquired, err := c.locker.AcquireLock(ctx, key, c.lockTTL)
		if err != nil {
			slog.Debug("lỗi acquire leader lock", "role", role, "error", err)
			time.Sleep(c.retryInterval)
			continue
		}

		if !acquired || handle == nil {
			c.setLeaderStatus(role, false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.retryInterval):
				continue
			}
		}

		// Đã chiếm được vị trí Leader!
		c.setLeaderStatus(role, true)
		slog.Info("node đã trở thành leader cho singleton job", "node_id", c.nodeID, "role", role)

		leaderCtx, cancelLeader := context.WithCancel(ctx)
		jobDone := make(chan struct{})

		// Chạy job trong goroutine
		go func() {
			defer close(jobDone)
			job(leaderCtx)
		}()

		// Vòng lặp gia hạn (Heartbeat renewal loop)
		ticker := time.NewTicker(renewInterval)
		lost := false

		for !lost {
			select {
			case <-ctx.Done():
				lost = true
			case <-jobDone:
				// Job hoàn thành sớm
				lost = true
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(context.Background(), 3*time.Second)
				renewErr := handle.Renew(renewCtx, c.lockTTL)
				renewCancel()
				if renewErr != nil {
					slog.Warn("mất quyền leader do gia hạn thất bại", "node_id", c.nodeID, "role", role, "error", renewErr)
					lost = true
				}
			}
		}

		ticker.Stop()
		cancelLeader()
		<-jobDone

		c.setLeaderStatus(role, false)

		relCtx, relCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = handle.Release(relCtx)
		relCancel()

		select {
		case <-ctx.Done():
			return
		case <-time.After(c.retryInterval):
		}
	}
}

func (c *Coordinator) setLeaderStatus(role string, isLeader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isLeaderMap[role] = isLeader
}
