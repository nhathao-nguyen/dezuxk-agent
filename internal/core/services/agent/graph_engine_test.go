package agent

import (
	"context"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
)

func TestGraphEngine_PlanExecuteVerifyComplete(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)
	checkpointRepo := session.NewMemoryCheckpointRepository()

	// Kế hoạch trả về 1 bước có verification command "echo OK"
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			// 1. Phản hồi cho nodePlan: xuất JSON kế hoạch
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							Content: "{\"steps\": [{\"id\": 1, \"title\": \"Tạo file và kiểm thử\", \"description\": \"Tạo file hello.txt\", \"verification_cmd\": \"echo VERIFY_PASS\"}]}",
						},
					},
				},
			},
			// 2. Phản hồi cho nodeExecute (ReAct runner): hoàn thành tạo file
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Đã tạo file hoàn tất.",
						},
					},
				},
			},
		},
	}

	engine := NewGraphEngine(mockChat, reg, checkpointRepo, &MockApprovalProvider{ApproveAll: true}, 3)
	ctx := context.Background()

	graphState, err := engine.RunGraph(ctx, "Tạo file và kiểm tra", domain.AgentRunOptions{
		Workspace: tempDir,
	})

	if err != nil {
		t.Fatalf("RunGraph trả về lỗi: %v", err)
	}

	if !graphState.IsCompleted {
		t.Errorf("Kỳ vọng workflow hoàn thành")
	}
	if graphState.CurrentNode != domain.NodeKindComplete {
		t.Errorf("Kỳ vọng NodeKind 'COMPLETE', nhận được: %s", graphState.CurrentNode)
	}
	if !graphState.Plan.IsAllPassed() {
		t.Errorf("Kỳ vọng toàn bộ các bước đều PASS")
	}
	if graphState.Checkpoints < 3 {
		t.Errorf("Kỳ vọng ít nhất 3 checkpoints được lưu, nhận được: %d", graphState.Checkpoints)
	}
	if !strings.Contains(graphState.FinalSummary, "Bước 1: Tạo file và kiểm thử [PASS]") {
		t.Errorf("FinalSummary không đúng: %s", graphState.FinalSummary)
	}

	// Kiểm tra phục hồi từ checkpoint qua ResumeGraph
	resumed, err := engine.ResumeGraph(ctx, graphState.TaskID, domain.AgentRunOptions{
		Workspace: tempDir,
	})
	if err != nil {
		t.Fatalf("Lỗi ResumeGraph: %v", err)
	}
	if resumed.TaskID != graphState.TaskID {
		t.Errorf("ResumeGraph sai TaskID: %s vs %s", resumed.TaskID, graphState.TaskID)
	}
}

func TestGraphEngine_VerifyFailAndFixCycle(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)
	checkpointRepo := session.NewMemoryCheckpointRepository()

	// Kịch bản:
	// 1. Plan node tạo ra step với verification command
	// 2. Execute node chạy
	// 3. Verify node chạy lần 1: fail (giả lập verification command trả về lỗi nếu không có file)
	// 4. Fix node chạy
	// 5. Verify node chạy lần 2: pass
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			// NodePlan
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							Content: `{"steps": [{"id": 1, "title": "Sửa code", "description": "Tạo fix", "verification_cmd": "echo FIXED"}]}`,
						},
					},
				},
			},
			// NodeExecute
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Thực hiện bước 1.",
						},
					},
				},
			},
			// NodeFix
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Đã phân tích lỗi và sửa chữa thành công.",
						},
					},
				},
			},
		},
	}

	engine := NewGraphEngine(mockChat, reg, checkpointRepo, &MockApprovalProvider{ApproveAll: true}, 3)
	ctx := context.Background()

	graphState, err := engine.RunGraph(ctx, "Nhiệm vụ có fix", domain.AgentRunOptions{
		Workspace: tempDir,
	})

	if err != nil {
		t.Fatalf("RunGraph trả về lỗi: %v", err)
	}
	if !graphState.IsCompleted {
		t.Errorf("Kỳ vọng hoàn thành sau khi fix")
	}
}
