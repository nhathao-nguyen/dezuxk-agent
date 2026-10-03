package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

// Multi-Instance Concurrency Test: Nhiều worker và sweeper tranh chấp đồng thời trên SQLite
func TestMultiInstance_ConcurrentClaimsAndSweeperRecovery(t *testing.T) {
	runRepo, cpRepo, db := newTestSqliteRepo(t)
	defer db.Close()

	ctx := context.Background()

	const numRuns = 20
	const numWorkers = 8

	secCtx := domain.SecurityContextFromTenantIdentity(domain.TenantIdentity{
		TenantID: "tenant-multi-stress",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	// 1. Tạo 20 runs ở trạng thái queued
	for i := 0; i < numRuns; i++ {
		runID := fmt.Sprintf("stress_run_%03d", i)
		run := &domain.AgentRun{
			ID:              runID,
			TenantID:        "tenant-multi-stress",
			Goal:            fmt.Sprintf("Concurrent task %d", i),
			Status:          domain.RunStatusQueued,
			Model:           "gemini-3.8-flash",
			Workspace:       ".",
			MaxSteps:        5,
			SecurityContext: secCtx,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}
		if err := runRepo.Create(ctx, run); err != nil {
			t.Fatalf("failed to create run %s: %v", runID, err)
		}

		// Lưu checkpoint cho từng run
		_ = cpRepo.SaveCheckpoint(ctx, &domain.AgentCheckpoint{
			TenantID:  "tenant-multi-stress",
			TaskID:    runID,
			StepIndex: 1,
			StateSnapshot: domain.AgentState{
				TaskID:      runID,
				CurrentStep: 1,
				Messages: []domain.OpenAIMessage{
					{Role: "user", Content: "Start"},
				},
			},
		})
	}

	var totalCompleted atomic.Int64
	var totalFencingRejections atomic.Int64

	var wg sync.WaitGroup

	// 2. Chạy 8 worker giả lập tranh chấp claim và thực thi
	for w := 0; w < numWorkers; w++ {
		workerID := fmt.Sprintf("worker-instance-%d", w)
		wg.Add(1)
		go func(wid string) {
			defer wg.Done()

			for i := 0; i < numRuns; i++ {
				runID := fmt.Sprintf("stress_run_%03d", i)

				// Cố gắng claim với lease ngắn 80ms
				claimed, err := runRepo.ClaimRun(ctx, runID, wid, 80*time.Millisecond)
				if err != nil || !claimed {
					continue
				}

				// Đọc lại để lấy generation
				claimedRun, gErr := runRepo.Get(ctx, runID)
				if gErr != nil || claimedRun == nil {
					continue
				}
				claimGen := claimedRun.ClaimGeneration

				// Giả lập xử lý ngắn
				time.Sleep(10 * time.Millisecond)

				// Ghi nhận hoàn thành bằng UpdateOwned có fencing
				claimedRun.Status = domain.RunStatusCompleted
				claimedRun.FinalAnswer = fmt.Sprintf("Completed by %s at gen %d", wid, claimGen)
				updated, uErr := runRepo.UpdateOwned(ctx, claimedRun, wid, claimGen)
				if uErr == nil && updated {
					totalCompleted.Add(1)
				} else {
					totalFencingRejections.Add(1)
				}
			}
		}(workerID)
	}

	// 3. Chạy song song sweeper recovery trên instance riêng
	sweeperInstance := NewJobService(runRepo, &dummyRunner{delay: 5 * time.Millisecond})
	sweeperInstance.SetCheckpointRepository(cpRepo)

	stopSweeper := make(chan struct{})
	var sweeperWg sync.WaitGroup
	sweeperWg.Add(1)
	go func() {
		defer sweeperWg.Done()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSweeper:
				return
			case <-ticker.C:
				_, _ = sweeperInstance.ScanRecoverableRuns(ctx)
			}
		}
	}()

	// Đợi tất cả workers hoàn tất
	wg.Wait()

	close(stopSweeper)
	sweeperWg.Wait()

	// 4. Cho phép sweeper dọn dẹp nốt các run còn sót lại nếu có lease hết hạn
	time.Sleep(120 * time.Millisecond)
	_, _ = sweeperInstance.ScanRecoverableRuns(ctx)
	time.Sleep(100 * time.Millisecond)

	// 5. Kiểm tra toàn bộ runs trong database:
	// Mọi run phải kết thúc ở trạng thái completed hoặc interrupted, tuyệt đối KHÔNG có run nào bị kẹt treo vĩnh viễn ở running/queued mà không ai xử lý!
	runs, err := runRepo.List(ctx, "tenant-multi-stress", 100, 0)
	if err != nil {
		t.Fatalf("failed to list runs: %v", err)
	}

	for _, r := range runs {
		if r.Status == domain.RunStatusQueued {
			t.Fatalf("run %s is still queued!", r.ID)
		}
		// Nếu ở trạng thái running, kiểm tra xem lease đã hết hạn chưa
		if r.Status == domain.RunStatusRunning && (r.LeaseUntil == nil || r.LeaseUntil.Before(time.Now())) {
			t.Fatalf("run %s is stuck running with expired lease without being recovered!", r.ID)
		}
	}
}
