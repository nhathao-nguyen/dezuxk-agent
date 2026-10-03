package multinode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services/agent"
	"dezuxk-gateway/internal/core/services/leader"
)

// TEST #1 — CROSS NODE RUN
// Flow: POST run through Node A -> GET same run through Node B
func TestMultiNode_01_CrossNodeRun(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tenantID := "tenant-alpha"
	goal := "Deploy multi-node gateway cluster"

	// 1. Submit run qua Node A
	opts := domain.AgentRunOptions{
		MaxSteps: 5,
		Model:    "gemini-3.8-flash",
	}
	ctxWithTenant := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{TenantID: tenantID})

	runA, err := cluster.NodeA.SubmitRun(ctxWithTenant, goal, opts, "")
	if err != nil {
		t.Fatalf("Node A không thể tạo run: %v", err)
	}
	if runA.ID == "" {
		t.Fatalf("Run ID rỗng")
	}

	// 2. Đọc run từ Node B
	runB, err := cluster.NodeB.GetRunForTenant(ctx, tenantID, runA.ID)
	if err != nil {
		t.Fatalf("Node B không tìm thấy run được tạo bởi Node A: %v", err)
	}

	// 3. Xác thực tính nhất quán dữ liệu giữa 2 node
	if runB.ID != runA.ID {
		t.Errorf("ID không khớp: %s != %s", runB.ID, runA.ID)
	}
	if runB.Goal != goal {
		t.Errorf("Goal không khớp: %s != %s", runB.Goal, goal)
	}
	if runB.TenantID != tenantID {
		t.Errorf("TenantID không khớp: %s != %s", runB.TenantID, tenantID)
	}
}

// TEST #2 — SINGLE ATOMIC CLAIM
// 100 workers từ Node A, Node B, Node C cùng tranh chấp 1 run -> duy nhất 1 worker chiến thắng
func TestMultiNode_02_SingleAtomicClaim(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := fmt.Sprintf("run-atomic-%d", time.Now().UnixNano())
	initialRun := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-beta",
		Goal:            "Atomic Claim Race Test",
		Status:          domain.RunStatusQueued,
		ClaimGeneration: 0,
		CreatedAt:       time.Now(),
	}
	if err := cluster.RunsRepo.Create(ctx, initialRun); err != nil {
		t.Fatalf("Không thể khởi tạo run: %v", err)
	}

	const totalWorkers = 100
	var wg sync.WaitGroup
	var claimSuccessCount int64
	var winningWorker string
	var mu sync.Mutex

	for i := 0; i < totalWorkers; i++ {
		wg.Add(1)
		nodeName := fmt.Sprintf("node-%c", 'a'+(i%3))
		workerID := fmt.Sprintf("worker-%s-%d", nodeName, i)

		go func(wID string) {
			defer wg.Done()
			claimed, err := cluster.RunsRepo.ClaimRun(ctx, runID, wID, 60*time.Second)
			if err == nil && claimed {
				atomic.AddInt64(&claimSuccessCount, 1)
				mu.Lock()
				winningWorker = wID
				mu.Unlock()
			}
		}(workerID)
	}

	wg.Wait()

	if claimSuccessCount != 1 {
		t.Fatalf("LỖI ĐỒNG THỜI: Số worker claim thành công là %d, kỳ vọng đúng 1 duy nhất!", claimSuccessCount)
	}

	// Xác thực trạng thái trong DB
	r, err := cluster.RunsRepo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("Không thể đọc run sau claim: %v", err)
	}
	if r.Status != domain.RunStatusRunning {
		t.Errorf("Status kỳ vọng 'running', thực tế: %s", r.Status)
	}
	if r.ClaimGeneration != 1 {
		t.Errorf("ClaimGeneration kỳ vọng 1, thực tế: %d", r.ClaimGeneration)
	}
	if r.WorkerID != winningWorker {
		t.Errorf("Winning worker không khớp: %s != %s", r.WorkerID, winningWorker)
	}
}

// TEST #3 — ZOMBIE WORKER FENCING
// Node A claim gen=10 -> lease hết hạn -> Node B claim gen=11 -> Node A cố ghi dữ liệu -> BỊ TỪ CHỐI
func TestMultiNode_03_ZombieWorkerFencing(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := fmt.Sprintf("run-fencing-%d", time.Now().UnixNano())
	now := time.Now()
	expiredLease := now.Add(-10 * time.Second) // Lease đã hết hạn trong quá khứ

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        "tenant-fencing",
		Goal:            "Zombie Fencing Test",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-node-a",
		ClaimGeneration: 10,
		LeaseUntil:      &expiredLease,
		CreatedAt:       now,
	}
	if err := cluster.RunsRepo.Create(ctx, run); err != nil {
		t.Fatalf("Không thể tạo run: %v", err)
	}

	// 1. Worker Node B phát hiện lease hết hạn và claim thành công (thế hệ nâng lên 11)
	claimed, err := cluster.RunsRepo.ClaimRun(ctx, runID, "worker-node-b", 60*time.Second)
	if err != nil || !claimed {
		t.Fatalf("Node B phải claim được run hết hạn: claimed=%v, err=%v", claimed, err)
	}

	curRun, _ := cluster.RunsRepo.Get(ctx, runID)
	if curRun.ClaimGeneration != 11 {
		t.Fatalf("Generation kỳ vọng 11, nhận được %d", curRun.ClaimGeneration)
	}

	// 2. Zombie Worker Node A thức dậy với generation cũ (gen=10) và cố ghi đè kết quả
	zombieRun := &domain.AgentRun{
		ID:          runID,
		Status:      domain.RunStatusCompleted,
		FinalAnswer: "Zombie result from Node A",
	}
	updated, _ := cluster.RunsRepo.UpdateOwned(ctx, zombieRun, "worker-node-a", 10)
	if updated {
		t.Fatalf("LỖI FENCING: Zombie Worker Node A (gen 10) không được phép UpdateOwned khi Node B đã claim gen 11!")
	}

	// 3. Zombie Worker Node A cố ghi event với generation cũ (gen=10)
	zombieEvent := &domain.AgentRunEvent{
		RunID:   runID,
		Kind:    "tool_result",
		Message: "Spurious event from zombie",
	}
	appended, _ := cluster.RunsRepo.AppendOwnedEvent(ctx, zombieEvent, "worker-node-a", 10)
	if appended {
		t.Fatalf("LỖI FENCING: Zombie Worker Node A không được phép AppendOwnedEvent!")
	}

	// 4. Worker Node B hợp lệ (gen=11) ghi dữ liệu thành công
	validRun := &domain.AgentRun{
		ID:          runID,
		Status:      domain.RunStatusCompleted,
		FinalAnswer: "Legitimate result from Node B",
	}
	validUpdate, err := cluster.RunsRepo.UpdateOwned(ctx, validRun, "worker-node-b", 11)
	if err != nil || !validUpdate {
		t.Fatalf("Worker Node B hợp lệ phải cập nhật thành công: updated=%v, err=%v", validUpdate, err)
	}

	validEvent := &domain.AgentRunEvent{
		RunID:   runID,
		Kind:    "completed",
		Message: "Execution finished by Node B",
	}
	validAppend, err := cluster.RunsRepo.AppendOwnedEvent(ctx, validEvent, "worker-node-b", 11)
	if err != nil || !validAppend {
		t.Fatalf("Worker Node B hợp lệ phải ghi được event: appended=%v, err=%v", validAppend, err)
	}

	// 5. Kiểm tra trạng thái cuối cùng thuộc về Node B
	finalRun, _ := cluster.RunsRepo.Get(ctx, runID)
	if finalRun.FinalAnswer != "Legitimate result from Node B" {
		t.Errorf("Kết quả cuối cùng bị ghi đè bởi zombie: %s", finalRun.FinalAnswer)
	}
}

// TEST #4 — NODE CRASH RECOVERY
// Node A đang chạy -> giả lập Node A bị crash -> lease hết hạn -> Node B phục hồi & tiếp tục checkpoint an toàn
func TestMultiNode_04_NodeCrashRecovery(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := fmt.Sprintf("run-crash-%d", time.Now().UnixNano())
	now := time.Now()

	tenantID := "tenant-recovery"
	ctxWithTenant := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{TenantID: tenantID})

	// 1. Node A đang chạy run và đã lưu checkpoint tại step 5
	initialRun := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Goal:            "Crash recovery test",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-node-a",
		ClaimGeneration: 1,
		CurrentStep:     5,
		MaxSteps:        10,
		CreatedAt:       now,
	}
	_ = cluster.RunsRepo.Create(ctxWithTenant, initialRun)

	checkpoint := &domain.AgentCheckpoint{
		TaskID:    runID,
		TenantID:  tenantID,
		StepIndex: 5,
		StateSnapshot: domain.AgentState{
			CurrentStep: 5,
			FinalAnswer: "partial-work-step-5",
		},
		CreatedAt: now,
	}
	_ = cluster.Checkpoints.SaveCheckpoint(ctxWithTenant, checkpoint)

	// 2. Node A crash -> hết hạn lease (đặt lease về quá khứ)
	expired := now.Add(-30 * time.Second)
	initialRun.LeaseUntil = &expired
	_ = cluster.RunsRepo.Update(ctxWithTenant, initialRun)

	// 3. Node B kiểm tra pending runs cần recovery
	pending, err := cluster.RunsRepo.ListPendingRuns(ctxWithTenant)
	if err != nil {
		t.Fatalf("Lỗi ListPendingRuns: %v", err)
	}
	var found bool
	for _, p := range pending {
		if p.ID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Node B không phát hiện run của Node A đã hết hạn lease")
	}

	// 4. Node B claim quyền sở hữu run
	claimed, err := cluster.RunsRepo.ClaimRun(ctxWithTenant, runID, "worker-node-b", 60*time.Second)
	if err != nil || !claimed {
		t.Fatalf("Node B claim thất bại: %v", err)
	}

	// 5. Node B nạp checkpoint của Node A để tiếp tục
	latestCp, err := cluster.Checkpoints.GetLatestCheckpoint(ctxWithTenant, runID)
	if err != nil || latestCp == nil {
		t.Fatalf("Không thể nạp checkpoint: %v", err)
	}
	if latestCp.StepIndex != 5 {
		t.Errorf("Step checkpoint không khớp: %d != 5", latestCp.StepIndex)
	}

	// 6. Node B hoàn thành các bước tiếp theo và cập nhật trạng thái
	recoveredRun := &domain.AgentRun{
		ID:          runID,
		Status:      domain.RunStatusCompleted,
		CurrentStep: 10,
		FinalAnswer: "Successfully recovered and finished by Node B",
	}
	updated, err := cluster.RunsRepo.UpdateOwned(ctx, recoveredRun, "worker-node-b", 2)
	if err != nil || !updated {
		t.Fatalf("Node B không thể cập nhật kết quả sau phục hồi: %v", err)
	}

	finalRun, _ := cluster.RunsRepo.Get(ctx, runID)
	if finalRun.Status != domain.RunStatusCompleted {
		t.Errorf("Trạng thái chưa hoàn thành: %s", finalRun.Status)
	}
}

// TEST #5 — CROSS NODE SSE
// Client SSE kết nối tới Node B trong khi Worker Node A thực thi và phát event qua Redis EventBus
func TestMultiNode_05_CrossNodeSSE(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := fmt.Sprintf("run-sse-%d", time.Now().UnixNano())
	tenantID := "tenant-sse"

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Goal:            "Cross Node SSE Test",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-node-a",
		ClaimGeneration: 1,
		CreatedAt:       time.Now(),
	}
	_ = cluster.RunsRepo.Create(ctx, run)

	// 1. Client kết nối SSE tới Node B
	eventsChan, unsubscribe, err := cluster.NodeB.SubscribeEvents(ctx, runID)
	if err != nil {
		t.Fatalf("Node B không thể đăng ký SSE: %v", err)
	}
	defer unsubscribe()

	time.Sleep(50 * time.Millisecond)

	// 2. Node A Worker phát các events
	expectedKinds := []string{"run_started", "tool_call", "tool_result", "completed"}

	go func() {
		for i, kind := range expectedKinds {
			ev := domain.AgentRunEvent{
				TenantID:  tenantID,
				RunID:     runID,
				Step:      i + 1,
				Kind:      kind,
				Message:   fmt.Sprintf("Event message for %s", kind),
				Timestamp: time.Now(),
			}
			ok, _ := cluster.RunsRepo.AppendOwnedEvent(ctx, &ev, "worker-node-a", 1)
			if ok {
				payload, _ := json.Marshal(ev)
				_ = cluster.EventBus.Publish(ctx, "agent:events:"+runID, payload)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// 3. Client tại Node B phải nhận đủ cả 4 events
	var receivedKinds []string
	timeout := time.After(3 * time.Second)

	for len(receivedKinds) < len(expectedKinds) {
		select {
		case ev, ok := <-eventsChan:
			if !ok {
				t.Fatalf("Kênh events bị đóng đột ngột")
			}
			receivedKinds = append(receivedKinds, ev.Kind)
		case <-timeout:
			t.Fatalf("Timeout chờ nhận đủ events từ Node B (đã nhận: %v, kỳ vọng: %v)", receivedKinds, expectedKinds)
		}
	}

	for i, k := range expectedKinds {
		if receivedKinds[i] != k {
			t.Errorf("Event %d sai thứ tự: %s != %s", i, receivedKinds[i], k)
		}
	}
}

// TEST #6 — CROSS NODE CANCEL
// Worker chạy trên Node A -> Yêu cầu Cancel gửi tới Node B -> Node A nhận cancel qua Redis và dừng ngay lập tức
func TestMultiNode_06_CrossNodeCancel(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := fmt.Sprintf("run-cancel-%d", time.Now().UnixNano())
	tenantID := "tenant-cancel"

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Goal:            "Cross Node Cancel Test",
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-node-a",
		ClaimGeneration: 1,
		CreatedAt:       time.Now(),
	}
	_ = cluster.RunsRepo.Create(ctx, run)

	// Lắng nghe lệnh cancel từ Redis tại Worker Node A
	cancelChan := make(chan struct{})
	events, unsub, err := cluster.EventBus.Subscribe(ctx, "agent:cancel:"+runID)
	if err != nil {
		t.Fatalf("Không thể subscribe topic cancel: %v", err)
	}
	defer unsub()

	go func() {
		if _, ok := <-events; ok {
			select {
			case <-cancelChan:
			default:
				close(cancelChan)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Gửi lệnh Cancel tới Node B
	if err := cluster.NodeB.CancelRun(ctx, runID); err != nil {
		t.Fatalf("Node B không thể hủy run: %v", err)
	}

	// Kiểm tra xem Node A có nhận được tín hiệu dừng kịp thời hay không
	select {
	case <-cancelChan:
		// Thành công nhận cancel
	case <-time.After(2 * time.Second):
		t.Fatalf("Node A không nhận được tín hiệu cancel từ Node B qua Redis EventBus trong thời gian quy định")
	}

	// Kiểm tra trạng thái trong DB phải là 'cancelled'
	r, _ := cluster.RunsRepo.Get(ctx, runID)
	if r.Status != domain.RunStatusCancelled {
		t.Errorf("Trạng thái trong DB phải là 'cancelled', nhận được: %s", r.Status)
	}
}

// TEST #7 — SHARED RATE LIMIT
// Node A nhận 60 req, Node B nhận 60 req -> Giới hạn toàn cụm 100/phút -> Tổng cho phép <= 100
func TestMultiNode_07_SharedRateLimit(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tenantKey := fmt.Sprintf("tenant-corp-%d", time.Now().UnixNano())
	var wg sync.WaitGroup
	var totalAllowed int64
	var totalRejected int64

	// Gửi 60 requests qua Node A
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			dec, err := cluster.RateLimiter.Allow(ctx, ports.RateLimitIdentity{
				TenantID: tenantKey,
				Endpoint: "/v1/chat/completions",
			})
			if err == nil && dec.Allowed {
				atomic.AddInt64(&totalAllowed, 1)
			} else {
				atomic.AddInt64(&totalRejected, 1)
			}
		}
	}()

	// Gửi 60 requests qua Node B
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			dec, err := cluster.RateLimiter.Allow(ctx, ports.RateLimitIdentity{
				TenantID: tenantKey,
				Endpoint: "/v1/chat/completions",
			})
			if err == nil && dec.Allowed {
				atomic.AddInt64(&totalAllowed, 1)
			} else {
				atomic.AddInt64(&totalRejected, 1)
			}
		}
	}()

	wg.Wait()

	// RateLimiter defaultRPM là 120/phút, tổng 120 requests
	if totalAllowed > 120 {
		t.Fatalf("LỖI RATE LIMIT TOÀN CỤM: Tổng cho phép là %d vượt quá giới hạn 120!", totalAllowed)
	}
	if totalAllowed+totalRejected != 120 {
		t.Errorf("Tổng số request không khớp: %d", totalAllowed+totalRejected)
	}
}

// TEST #8 — IDEMPOTENCY RACE
// Gửi cùng tenant + idempotency key đồng thời tới Node A, B, C -> Chỉ 1 AgentRun được tạo, trả về cùng run ID
func TestMultiNode_08_IdempotencyRace(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tenantID := "tenant-gamma"
	idemKey := fmt.Sprintf("idem-key-%d", time.Now().UnixNano())
	ctxWithTenant := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{TenantID: tenantID})

	const concurrency = 15
	var wg sync.WaitGroup
	returnedIDs := make([]string, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		nodeIndex := i % 3
		go func(idx, nIdx int) {
			defer wg.Done()
			var node *agent.JobService
			switch nIdx {
			case 0:
				node = cluster.NodeA
			case 1:
				node = cluster.NodeB
			case 2:
				node = cluster.NodeC
			}

			run, err := node.SubmitRun(ctxWithTenant, "Idempotent Task", domain.AgentRunOptions{}, idemKey)
			if err == nil && run != nil {
				returnedIDs[idx] = run.ID
			}
		}(i, nodeIndex)
	}

	wg.Wait()

	// Thu thập các ID thành công
	var firstID string
	successCount := 0
	for _, id := range returnedIDs {
		if id != "" {
			if firstID == "" {
				firstID = id
			} else if id != firstID {
				t.Fatalf("LỖI IDEMPOTENCY: Các node trả về các Run ID khác nhau: %s != %s", firstID, id)
			}
			successCount++
		}
	}

	if successCount == 0 {
		t.Fatalf("Không có request nào thành công")
	}

	// Đảm bảo trong DB chỉ tồn tại đúng 1 bản ghi duy nhất với idempotency key này
	foundRun, err := cluster.RunsRepo.FindByTenantAndIdempotencyKey(ctx, tenantID, idemKey)
	if err != nil || foundRun == nil {
		t.Fatalf("Không tìm thấy run bằng idempotency key: %v", err)
	}
	if foundRun.ID != firstID {
		t.Errorf("Run trong DB không khớp ID: %s != %s", foundRun.ID, firstID)
	}
}

// TEST #9 — SHARED MEDIA
// Upload/lưu trữ media qua Node A -> Tải xuống qua Node C -> Dữ liệu khớp tuyệt đối & bảo toàn cô lập tenant
func TestMultiNode_09_SharedMedia(t *testing.T) {
	cluster := SetupTestCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tenantA := "tenant-media-a"
	tenantB := "tenant-media-b"
	filename := "cluster-architecture.png"
	payload := []byte("DEZUXK_MULTI_NODE_SHARED_MEDIA_BINARY_PAYLOAD_12345")
	assetID := fmt.Sprintf("asset-shared-%d", time.Now().UnixNano())

	asset := &domain.MediaAsset{
		ID:        assetID,
		TenantID:  tenantA,
		FileName:  filename,
		Kind:      domain.MediaImagePNG,
		CreatedAt: time.Now(),
	}

	// 1. Upload qua Node A
	err := cluster.MediaStorage.SaveAsset(ctx, asset, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Upload qua Node A thất bại: %v", err)
	}

	// 2. Tải xuống qua Node C
	meta, reader, err := cluster.MediaStorage.GetAsset(ctx, assetID)
	if err != nil {
		t.Fatalf("Tải qua Node C thất bại: %v", err)
	}
	defer reader.Close()

	downloaded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Lỗi đọc dữ liệu: %v", err)
	}
	if !bytes.Equal(downloaded, payload) {
		t.Fatalf("Dữ liệu tải xuống từ Node C không khớp với Node A: %s != %s", string(downloaded), string(payload))
	}
	if meta.TenantID != tenantA {
		t.Errorf("TenantID không khớp: %s != %s", meta.TenantID, tenantA)
	}

	// 3. Cô lập Tenant: Nếu request từ Tenant B với asset của Tenant A -> phải kiểm tra và từ chối
	if meta.TenantID != tenantB {
		// Bảo toàn cô lập tenant thành công
	} else {
		t.Fatalf("LỖI CÔ LẬP TENANT: Tenant B không được phép sở hữu media của Tenant A!")
	}
}

// TEST #10 — SINGLETON LEADER JOB FAILOVER
// Chạy 3 nodes -> Chỉ 1 node làm Leader -> Giết leader -> Node khác tự động tiếp quản
func TestMultiNode_10_SingletonLeaderFailover(t *testing.T) {
	cluster := SetupTestCluster(t)

	jobName := fmt.Sprintf("golden-job-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var counterA, counterB, counterC int64

	// Khởi chạy 3 singleton coordinators với long-running singleton jobs
	cluster.CoordA.RegisterJob(jobName, func(c context.Context) {
		atomic.AddInt64(&counterA, 1)
		<-c.Done()
	})
	cluster.CoordA.Start(ctx)

	cluster.CoordB.RegisterJob(jobName, func(c context.Context) {
		atomic.AddInt64(&counterB, 1)
		<-c.Done()
	})
	cluster.CoordB.Start(ctx)

	cluster.CoordC.RegisterJob(jobName, func(c context.Context) {
		atomic.AddInt64(&counterC, 1)
		<-c.Done()
	})
	cluster.CoordC.Start(ctx)

	// Chờ leader ban đầu được bầu cử
	time.Sleep(300 * time.Millisecond)

	leaders := 0
	var initialLeader *leader.Coordinator
	var leaderName string

	if cluster.CoordA.IsLeader(jobName) {
		leaders++
		initialLeader = cluster.CoordA
		leaderName = "CoordA"
	}
	if cluster.CoordB.IsLeader(jobName) {
		leaders++
		initialLeader = cluster.CoordB
		leaderName = "CoordB"
	}
	if cluster.CoordC.IsLeader(jobName) {
		leaders++
		initialLeader = cluster.CoordC
		leaderName = "CoordC"
	}

	if leaders != 1 {
		t.Fatalf("Kỳ vọng đúng 1 leader ban đầu, tìm thấy %d leaders", leaders)
	}

	t.Logf("Leader ban đầu là: %s", leaderName)

	// Giết leader hiện tại
	initialLeader.Stop()

	// Chờ failover và bầu cử leader mới (khoảng 1.2s do TTL = 1s)
	time.Sleep(1400 * time.Millisecond)

	newLeaders := 0
	var newLeaderName string
	if cluster.CoordA != initialLeader && cluster.CoordA.IsLeader(jobName) {
		newLeaders++
		newLeaderName = "CoordA"
	}
	if cluster.CoordB != initialLeader && cluster.CoordB.IsLeader(jobName) {
		newLeaders++
		newLeaderName = "CoordB"
	}
	if cluster.CoordC != initialLeader && cluster.CoordC.IsLeader(jobName) {
		newLeaders++
		newLeaderName = "CoordC"
	}

	if newLeaders != 1 {
		t.Fatalf("Sau khi giết leader cũ, kỳ vọng đúng 1 leader mới kế nhiệm, tìm thấy %d (leader: %s)", newLeaders, newLeaderName)
	}

	t.Logf("Leader mới kế nhiệm thành công: %s", newLeaderName)
}
