package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
)

// DestructiveCounterTool mô phỏng công cụ nhạy cảm có side-effect ghi/tăng biến đếm ngoại vi
type DestructiveCounterTool struct {
	counter   *atomic.Int64
	startedCh chan struct{}
	releaseCh chan struct{}
}

func (d *DestructiveCounterTool) Name() string {
	return "destructive_increment"
}

func (d *DestructiveCounterTool) Description() string {
	return "Tăng biến đếm ngoại vi có side-effect thực tế"
}

func (d *DestructiveCounterTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (d *DestructiveCounterTool) Permission() domain.PermissionLevel {
	return domain.PermissionDestructive
}

func (d *DestructiveCounterTool) ExecutionSemantics() domain.ToolExecutionSemantics {
	return domain.ToolExecutionSemantics{
		ReadOnly:   false,
		Idempotent: false,
	}
}

func (d *DestructiveCounterTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if d.startedCh != nil {
		d.startedCh <- struct{}{}
	}
	if d.releaseCh != nil {
		select {
		case <-d.releaseCh:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	val := d.counter.Add(1)
	return fmt.Sprintf(`{"counter": %d}`, val), nil
}

// TestToolFencing_01_ZombieWorkerPlannedRejected kiểm tra:
// Node A (gen=10), Node B giành quyền (gen=11). Node A cố ghi RecordPlannedOwned (gen=10) -> BỊ TỪ CHỐI, tool không chạy.
func TestToolFencing_01_ZombieWorkerPlannedRejected(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	ctx := context.Background()

	runID := "run_fencing_01"
	tenantID := "tenant_fencing_01"
	now := time.Now()
	leaseEnd := now.Add(10 * time.Second)

	// Khởi tạo run với worker B đã giành quyền gen=11
	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-b",
		ClaimGeneration: 11,
		LeaseUntil:      &leaseEnd,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create run failed: %v", err)
	}

	var counter atomic.Int64
	destructiveTool := &DestructiveCounterTool{
		counter: &counter,
	}

	reg := tools.NewToolRegistry()
	reg.RegisterTool(destructiveTool)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_inc_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "destructive_increment",
										Arguments: `{}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	runner.SetToolExecutionLedger(repo)
	runner.SetAgentRunRepository(repo)

	// Gán context sở hữu của Zombie Worker A với gen=10 (cũ)
	zombieCtx := domain.ContextWithExecutionOwnership(ctx, domain.ExecutionOwnership{
		RunID:           runID,
		WorkerID:        "worker-a",
		ClaimGeneration: 10,
	})
	zombieCtx = domain.ContextWithTenantIdentity(zombieCtx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	state, err := runner.Run(zombieCtx, "Tăng biến đếm", domain.AgentRunOptions{
		MaxSteps: 3,
		TaskID:   runID,
	})

	if err != nil {
		t.Fatalf("runner.Run không nên trả runtime error: %v", err)
	}

	if state.StopReason != domain.StopReasonVerificationFailed {
		t.Fatalf("Kỳ vọng stop reason %s nhưng nhận %s", domain.StopReasonVerificationFailed, state.StopReason)
	}

	// Xác nhận biến đếm tuyệt đối KHÔNG được thực thi
	if counter.Load() != 0 {
		t.Fatalf("Zombie worker A đã kích hoạt side-effect! counter=%d, kỳ vọng 0", counter.Load())
	}
}

// TestToolFencing_02_ZombieWorkerFinishedRejected kiểm tra:
// Node A đang chạy tool chậm, lease hết hạn, Node B claim gen=11.
// Node A tool trả về, gọi RecordFinishedOwned gen=10 -> BỊ TỪ CHỐI, kết quả bị discard, state không commit.
func TestToolFencing_02_ZombieWorkerFinishedRejected(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	ctx := context.Background()

	runID := "run_fencing_02"
	tenantID := "tenant_fencing_02"
	now := time.Now()
	leaseEnd := now.Add(1 * time.Second)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-a",
		ClaimGeneration: 10,
		LeaseUntil:      &leaseEnd,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create run failed: %v", err)
	}

	var counter atomic.Int64
	startedCh := make(chan struct{}, 1)
	releaseCh := make(chan struct{}, 1)

	slowTool := &DestructiveCounterTool{
		counter:   &counter,
		startedCh: startedCh,
		releaseCh: releaseCh,
	}

	reg := tools.NewToolRegistry()
	reg.RegisterTool(slowTool)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_slow_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "destructive_increment",
										Arguments: `{}`,
									},
								},
							},
						},
					},
				},
			},
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Thành công hoàn tất",
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	runner.SetToolExecutionLedger(repo)
	// Lưu ý: Không set runRepo để không tự gia hạn lease tự động trong kịch bản này
	// nhằm mô phỏng mạng bị trễ hoặc worker bị pause garbage collection

	workerACtx := domain.ContextWithExecutionOwnership(ctx, domain.ExecutionOwnership{
		RunID:           runID,
		WorkerID:        "worker-a",
		ClaimGeneration: 10,
	})
	workerACtx = domain.ContextWithTenantIdentity(workerACtx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	doneCh := make(chan *domain.AgentState, 1)
	go func() {
		state, _ := runner.Run(workerACtx, "Thực thi tác vụ chậm", domain.AgentRunOptions{
			MaxSteps: 3,
			TaskID:   runID,
		})
		doneCh <- state
	}()

	// Chờ tool bắt đầu chạy
	<-startedCh

	// Node B cướp quyền với gen=11 trong khi worker A đang bận tính toán
	newLease := time.Now().Add(30 * time.Second)
	run.WorkerID = "worker-b"
	run.ClaimGeneration = 11
	run.LeaseUntil = &newLease
	if err := repo.Update(ctx, run); err != nil {
		t.Fatalf("Node B chiếm quyền thất bại: %v", err)
	}

	// Cho phép tool worker A hoàn thành
	releaseCh <- struct{}{}

	// Worker A phải dừng ngay và không ghi kết quả
	state := <-doneCh

	if state.StopReason != domain.StopReasonVerificationFailed {
		t.Fatalf("Kỳ vọng stop reason %s nhưng nhận %s", domain.StopReasonVerificationFailed, state.StopReason)
	}

	// Kiểm tra xem message "tool" có bị đưa vào state không (phải bị loại bỏ hoàn toàn)
	for _, msg := range state.Messages {
		if msg.Role == "tool" {
			t.Fatalf("Kết quả tool của zombie worker A bị commit trái phép vào state.Messages!")
		}
	}
}

// TestToolFencing_03_DestructiveCounterExactOnce kiểm tra:
// Công cụ ghi side-effect ngoại vi chỉ được tăng chính xác 1 lần (counter == 1, không phải 2)
// khi có worker cạnh tranh bằng kênh đồng bộ deterministic
func TestToolFencing_03_DestructiveCounterExactOnce(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	ctx := context.Background()

	runID := "run_fencing_03"
	tenantID := "tenant_fencing_03"
	now := time.Now()
	leaseEnd := now.Add(10 * time.Second)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-legit",
		ClaimGeneration: 1,
		LeaseUntil:      &leaseEnd,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create run failed: %v", err)
	}

	var counter atomic.Int64
	toolInstance := &DestructiveCounterTool{
		counter: &counter,
	}

	reg := tools.NewToolRegistry()
	reg.RegisterTool(toolInstance)

	mockChat1 := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_idempotent_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "destructive_increment",
										Arguments: `{}`,
									},
								},
							},
						},
					},
				},
			},
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Hoàn tất tăng biến đếm",
						},
					},
				},
			},
		},
	}

	runner1 := NewRunner(mockChat1, reg, &MockApprovalProvider{ApproveAll: true})
	runner1.SetToolExecutionLedger(repo)

	// 1. Worker hợp lệ chạy thành công
	legitCtx := domain.ContextWithExecutionOwnership(ctx, domain.ExecutionOwnership{
		RunID:           runID,
		WorkerID:        "worker-legit",
		ClaimGeneration: 1,
	})
	legitCtx = domain.ContextWithTenantIdentity(legitCtx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	state1, err1 := runner1.Run(legitCtx, "Chạy lần 1", domain.AgentRunOptions{
		MaxSteps: 3,
		TaskID:   runID,
	})
	if err1 != nil || state1.StopReason != domain.StopReasonCompleted {
		t.Fatalf("Worker hợp lệ chạy thất bại: %v, reason: %s", err1, state1.StopReason)
	}

	if counter.Load() != 1 {
		t.Fatalf("Kỳ vọng counter == 1 sau lần chạy đầu, nhận %d", counter.Load())
	}

	// 2. Worker Zombie chạy cùng runID nhưng mang token thế hệ cũ gen=0
	mockChat2 := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_idempotent_2",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "destructive_increment",
										Arguments: `{}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	runner2 := NewRunner(mockChat2, reg, &MockApprovalProvider{ApproveAll: true})
	runner2.SetToolExecutionLedger(repo)

	zombieCtx := domain.ContextWithExecutionOwnership(ctx, domain.ExecutionOwnership{
		RunID:           runID,
		WorkerID:        "worker-zombie",
		ClaimGeneration: 0, // Cũ hơn
	})
	zombieCtx = domain.ContextWithTenantIdentity(zombieCtx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	state2, _ := runner2.Run(zombieCtx, "Zombie cố chạy lại", domain.AgentRunOptions{
		MaxSteps: 3,
		TaskID:   runID,
	})

	if state2.StopReason != domain.StopReasonVerificationFailed {
		t.Fatalf("Kỳ vọng zombie bị từ chối với StopReasonVerificationFailed, nhận %s", state2.StopReason)
	}

	// Biến đếm TUYỆT ĐỐI vẫn là 1, không bị tăng lần 2!
	if counter.Load() != 1 {
		t.Fatalf("Vi phạm an toàn! Counter bị tăng lên %d (kỳ vọng chính xác 1)", counter.Load())
	}
}

// TestToolFencing_04_UnknownAfterCrash kiểm tra:
// Khi tool đang planned/running mà worker crash trước khi ghi finished.
// Lần phục hồi sau phát hiện tool nhạy cảm destructive -> KHÔNG tự động replay, dừng an toàn.
func TestToolFencing_04_UnknownAfterCrash(t *testing.T) {
	repo := session.NewMemoryAgentRunRepository()
	ctx := context.Background()

	runID := "run_fencing_04"
	tenantID := "tenant_fencing_04"
	now := time.Now()
	leaseEnd := now.Add(10 * time.Second)

	run := &domain.AgentRun{
		ID:              runID,
		TenantID:        tenantID,
		Status:          domain.RunStatusRunning,
		WorkerID:        "worker-b",
		ClaimGeneration: 2,
		LeaseUntil:      &leaseEnd,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("Create run failed: %v", err)
	}

	// Mô phỏng trước crash: Worker A đã ghi status 'running' vào ledger
	err := repo.RecordPlannedOrRunning(ctx, &domain.ToolExecutionRecord{
		TenantID:   tenantID,
		RunID:      runID,
		ToolCallID: "call_crash_1",
		ToolName:   "destructive_increment",
		Status:     domain.ToolExecutionRunning,
		StartedAt:  &now,
	})
	if err != nil {
		t.Fatalf("RecordPlannedOrRunning failed: %v", err)
	}

	var counter atomic.Int64
	toolInstance := &DestructiveCounterTool{
		counter: &counter,
	}

	reg := tools.NewToolRegistry()
	reg.RegisterTool(toolInstance)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_crash_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "destructive_increment",
										Arguments: `{}`,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	runner.SetToolExecutionLedger(repo)

	recoveryCtx := domain.ContextWithExecutionOwnership(ctx, domain.ExecutionOwnership{
		RunID:           runID,
		WorkerID:        "worker-b",
		ClaimGeneration: 2,
	})
	recoveryCtx = domain.ContextWithTenantIdentity(recoveryCtx, domain.TenantIdentity{
		TenantID: tenantID,
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
	})

	state, _ := runner.Run(recoveryCtx, "Phục hồi sau crash", domain.AgentRunOptions{
		MaxSteps: 3,
		TaskID:   runID,
	})

	if state.StopReason != domain.StopReasonVerificationFailed {
		t.Fatalf("Kỳ vọng stop reason %s nhưng nhận %s", domain.StopReasonVerificationFailed, state.StopReason)
	}

	// Biến đếm TUYỆT ĐỐI không được chạy tự động replay
	if counter.Load() != 0 {
		t.Fatalf("Tool destructive tự ý replay sau crash! counter=%d", counter.Load())
	}

	// Ledger phải được cập nhật thành unknown_after_restart
	exec, err := repo.GetExecution(ctx, tenantID, runID, "call_crash_1")
	if err != nil || exec == nil {
		t.Fatalf("Không tìm thấy ledger record: %v", err)
	}
	if exec.Status != domain.ToolExecutionUnknownAfterRestart {
		t.Fatalf("Kỳ vọng status %s nhưng nhận %s", domain.ToolExecutionUnknownAfterRestart, exec.Status)
	}
}
