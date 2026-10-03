package agent

import (
	"context"
	"io"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// MockChatUseCase giả lập ChatUseCase cho unit test
type MockChatUseCase struct {
	responses []*domain.OpenAIChatResponse
	callIndex int
}

var _ ports.ChatUseCase = (*MockChatUseCase)(nil)

func (m *MockChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, streamWriter io.Writer, flusher func()) error {
	return nil
}

func (m *MockChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return &domain.OpenAIChatResponse{
			Choices: []domain.OpenAIChoice{
				{Message: domain.OpenAIMessage{Role: "assistant", Content: "Done fallback"}},
			},
		}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

// MockApprovalProvider giả lập phê duyệt
type MockApprovalProvider struct {
	ApproveAll bool
}

func (m *MockApprovalProvider) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (bool, error) {
	return m.ApproveAll, nil
}

func TestAgentRunner_ReActLoop_Success(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	// Vòng 1: Mô hình gọi tool write_file
	// Vòng 2: Mô hình trả lời hoàn thành
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:             "assistant",
							Content:          "Tôi sẽ tạo file demo.",
							ReasoningContent: "Cần tạo file test trước.",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "write_file",
										Arguments: `{"path": "test.txt", "content": "Dezuxk Agent Working"}`,
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
							Content: "File test.txt đã được tạo thành công với nội dung chính xác.",
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	ctx := context.Background()

	state, err := runner.Run(ctx, "Hãy tạo file test.txt", domain.AgentRunOptions{
		MaxSteps:   5,
		Workspace:  tempDir,
		Supervised: false,
	})

	if err != nil {
		t.Fatalf("Runner trả về lỗi: %v", err)
	}

	if !state.IsCompleted {
		t.Errorf("Agent chưa đánh dấu hoàn thành")
	}
	if state.StopReason != "completed" {
		t.Errorf("StopReason kỳ vọng 'completed', nhận được: %s", state.StopReason)
	}
	if !strings.Contains(state.FinalAnswer, "File test.txt đã được tạo") {
		t.Errorf("FinalAnswer bất thường: %s", state.FinalAnswer)
	}
	if len(state.Steps) != 2 {
		t.Errorf("Kỳ vọng 2 bước thực thi, nhận được: %d", len(state.Steps))
	}
}

func TestAgentRunner_Supervised_Rejection(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	// Mô hình cố gọi lệnh shell phá hoại, user từ chối phê duyệt
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_danger",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "run_command",
										Arguments: `{"command": "rm -rf /"}`,
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
							Content: "Vì người dùng từ chối lệnh này, tôi đã dừng thao tác.",
						},
					},
				},
			},
		},
	}

	// ApproveAll = false
	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: false})
	ctx := context.Background()

	state, err := runner.Run(ctx, "Chạy lệnh nguy hiểm", domain.AgentRunOptions{
		MaxSteps:   5,
		Workspace:  tempDir,
		Supervised: true, // Chế độ Bán tự trị
	})

	if err != nil {
		t.Fatalf("Runner trả về lỗi: %v", err)
	}

	if !state.IsCompleted {
		t.Errorf("Agent phải hoàn thành sau khi tiếp nhận thông báo từ chối")
	}

	// Kiểm tra xem message tool result có phản ánh việc từ chối không
	var foundDenied bool
	for _, msg := range state.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "TỪ CHỐI") {
			foundDenied = true
			break
		}
	}
	if !foundDenied {
		t.Errorf("Không tìm thấy thông báo từ chối quyền trong lịch sử tool result")
	}
}

func TestAgentRunner_LoopDetection(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	// Mô hình liên tục gọi tool lặp lại cùng tham số
	loopResp := &domain.OpenAIChatResponse{
		Choices: []domain.OpenAIChoice{
			{
				Message: domain.OpenAIMessage{
					Role: "assistant",
					ToolCalls: []domain.OpenAIToolCall{
						{
							ID:   "call_loop",
							Type: "function",
							Function: domain.OpenAIFunctionCallData{
								Name:      "read_file",
								Arguments: `{"path": "nonexistent.txt"}`,
							},
						},
					},
				},
			},
		},
	}

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{loopResp, loopResp, loopResp, loopResp, loopResp},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	ctx := context.Background()

	state, err := runner.Run(ctx, "Test loop guardrail", domain.AgentRunOptions{
		MaxSteps:         10,
		MaxRepeatedCalls: 3,
		Workspace:        tempDir,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.StopReason != domain.StopReasonRepeatedToolLoop {
		t.Fatalf("expected StopReason %s, got %s", domain.StopReasonRepeatedToolLoop, state.StopReason)
	}
}

func TestAgentRunner_MaxToolCallsReached(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{ID: "c1", Type: "function", Function: domain.OpenAIFunctionCallData{Name: "read_file", Arguments: `{"path": "a.txt"}`}},
								{ID: "c2", Type: "function", Function: domain.OpenAIFunctionCallData{Name: "read_file", Arguments: `{"path": "b.txt"}`}},
								{ID: "c3", Type: "function", Function: domain.OpenAIFunctionCallData{Name: "read_file", Arguments: `{"path": "c.txt"}`}},
							},
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	ctx := context.Background()

	state, err := runner.Run(ctx, "Test max tool calls", domain.AgentRunOptions{
		MaxSteps:     5,
		MaxToolCalls: 2, // Đặt giới hạn 2 lời gọi
		Workspace:    tempDir,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.StopReason != domain.StopReasonMaxToolCallsReached {
		t.Fatalf("expected StopReason %s, got %s", domain.StopReasonMaxToolCallsReached, state.StopReason)
	}
}

