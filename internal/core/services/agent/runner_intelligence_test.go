package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
)

func TestAgentIntelligence_SelfContinueOnCutOff(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Dưới đây là kế hoạch sửa đổi và đoạn mã khắc phục:\n```go\nfunc FixDeadlock() {", // unclosed code block
						},
					},
				},
			},
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "```go\nfunc FixDeadlock() {\n    mu.Lock()\n    defer mu.Unlock()\n}\n```\nĐã hoàn thành toàn bộ giải pháp!",
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	ctx := context.Background()

	state, err := runner.Run(ctx, "Giải thích giải pháp khắc phục lỗi deadlock", domain.AgentRunOptions{
		MaxSteps:   5,
		Workspace:  tempDir,
		Supervised: false,
	})

	if err != nil {
		t.Fatalf("Runner trả về lỗi: %v", err)
	}

	if !state.IsCompleted {
		t.Errorf("Agent phải tự động tiếp tục và hoàn tất khi gặp phản hồi cut-off")
	}
	if state.StopReason != domain.StopReasonCompleted {
		t.Errorf("StopReason kỳ vọng '%s', nhận được: %s", domain.StopReasonCompleted, state.StopReason)
	}
	if len(state.Steps) < 2 {
		t.Errorf("Kỳ vọng ít nhất 2 bước (1 cut-off + 1 hoàn thành), nhận được: %d", len(state.Steps))
	}
}

func TestAgentIntelligence_SelfContinueOnRequireAction(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "Tôi đã phân tích lỗi. Chỉ cần sửa dòng 10 thành if token == nil.", // Chỉ trả về text, không gọi tool sửa code
						},
					},
				},
			},
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_fix",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "write_file",
										Arguments: `{"path": "auth.go", "content": "package auth\nfunc Check() bool { return true }"}`,
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
							Role: "assistant",
							ToolCalls: []domain.OpenAIToolCall{
								{
									ID:   "call_verify",
									Type: "function",
									Function: domain.OpenAIFunctionCallData{
										Name:      "run_command",
										Arguments: `{"command": "echo PASS"}`,
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
							Content: "Đã sửa mã nguồn auth.go và xác thực kiểm thử thành công.",
						},
					},
				},
			},
		},
	}

	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	ctx := context.Background()

	state, err := runner.Run(ctx, "Fix bug in auth module and verify", domain.AgentRunOptions{
		MaxSteps:      6,
		Workspace:     tempDir,
		RequireAction: true,
	})

	if err != nil {
		t.Fatalf("Runner trả về lỗi: %v", err)
	}

	if !state.IsCompleted {
		t.Errorf("Agent phải hoàn tất sau khi được nhắc gọi tool")
	}

	// Kiểm tra xem message nhắc nhở ACTION REQUIRED có được gửi đi không
	var foundActionPrompt bool
	for _, msg := range state.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "ACTION REQUIRED") {
			foundActionPrompt = true
			break
		}
	}
	if !foundActionPrompt {
		t.Errorf("Kỳ vọng tìm thấy prompt [ACTION REQUIRED] trong lịch sử hội thoại")
	}
}

func TestAgentIntelligence_EvidenceStore_PreservesRawData(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	longOutput := strings.Repeat("Long log line: trace event at timestamp 2026-10-04\n", 80) // > 4000 chars

	store := NewEvidenceStore()
	compacted := CompactToolOutputIfNeeded(store, "run_command", "view_logs", longOutput, 1, false)

	if len(compacted) >= len(longOutput) {
		t.Errorf("Compacted output (%d) phải ngắn hơn raw output (%d)", len(compacted), len(longOutput))
	}
	if !strings.Contains(compacted, "TOOL OUTPUT TRUNCATED") {
		t.Errorf("Kỳ vọng marker truncation, nhận được:\n%s", compacted)
	}

	// Kiểm tra xem dữ liệu thô trong store có bảo toàn 100% không
	items := store.Search("trace event", 5)
	if len(items) == 0 {
		t.Fatalf("Không tìm thấy evidence trong store")
	}
	if items[0].FullContent != longOutput {
		t.Errorf("Dữ liệu thô trong EvidenceStore không khớp bản gốc")
	}
}

func TestAgentIntelligence_ContextPreservation_AfterCompaction(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Fix memory leak in subscriber.go. User constraint: NEVER remove metrics emission and preserve backwards compatibility."},
		{
			Role: "assistant",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID:   "c1",
					Type: "function",
					Function: domain.OpenAIFunctionCallData{
						Name:      "read_file",
						Arguments: `{"path": "subscriber.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "c1",
			Content:    "package sub\n// code here",
		},
		{
			Role: "assistant",
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID:   "c2",
					Type: "function",
					Function: domain.OpenAIFunctionCallData{
						Name:      "replace_file_content",
						Arguments: `{"target_file": "subscriber.go"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "c2",
			Content:    "Replaced successfully",
		},
	}

	summaryMsg := GenerateStructuredCompaction(context.Background(), messages, nil, "gemini-2.5-flash")
	content := summaryMsg.Content

	if !strings.Contains(content, "Auto-Compacted") {
		t.Errorf("Thiếu marker Auto-Compacted trong summary:\n%s", content)
	}
	if !strings.Contains(content, "NEVER remove metrics emission") {
		t.Errorf("Không bảo toàn User Constraint quan trọng:\n%s", content)
	}
	if !strings.Contains(content, "subscriber.go") {
		t.Errorf("Không bảo toàn tệp subscriber.go:\n%s", content)
	}
}

func TestAgentIntelligence_100TurnSyntheticCompaction(t *testing.T) {
	// Giả lập 100 turns trao đổi hội thoại
	var messages []domain.OpenAIMessage
	messages = append(messages, domain.OpenAIMessage{
		Role:    "system",
		Content: "You are Dezuxk Autonomous Engineering Agent.",
	})
	messages = append(messages, domain.OpenAIMessage{
		Role:    "user",
		Content: "CRITICAL USER CONSTRAINT: Do not delete existing audit tables in PostgreSQL under any circumstances.",
	})

	for i := 1; i <= 50; i++ {
		toolCallID := fmt.Sprintf("call_%d", i)
		messages = append(messages, domain.OpenAIMessage{
			Role:    "assistant",
			Content: fmt.Sprintf("Executing step %d to inspect subsystem %d", i, i),
			ToolCalls: []domain.OpenAIToolCall{
				{
					ID:   toolCallID,
					Type: "function",
					Function: domain.OpenAIFunctionCallData{
						Name:      "read_file",
						Arguments: fmt.Sprintf(`{"path": "pkg/subsystem_%d.go"}`, i),
					},
				},
			},
		})
		messages = append(messages, domain.OpenAIMessage{
			Role:       "tool",
			ToolCallID: toolCallID,
			Content:    fmt.Sprintf("// Subsystem %d source code with 50 lines of logic", i),
		})
	}

	if len(messages) != 102 {
		t.Fatalf("kỳ vọng 102 messages, nhận được %d", len(messages))
	}

	memMgr := NewMemoryManager(nil, nil, "gemini-2.5-pro", domain.CoreMemory{
		Scratchpad: "Refactoring all 50 subsystems",
	})

	// Thực hiện nén giữ lại 10 tin nhắn gần nhất
	compacted, err := memMgr.CompactConversation(context.Background(), messages, 10)
	if err != nil {
		t.Fatalf("CompactConversation thất bại trên chuỗi 100 turns: %v", err)
	}

	if len(compacted) >= len(messages) {
		t.Errorf("Compacted messages (%d) phải nhỏ hơn original (%d)", len(compacted), len(messages))
	}

	summaryContent := compacted[1].Content
	if !strings.Contains(summaryContent, "Do not delete existing audit tables") {
		t.Errorf("Rò rỉ/Mất User Constraint sau khi nén 100 turns:\n%s", summaryContent)
	}
	if !strings.Contains(summaryContent, "pkg/subsystem_1.go") {
		t.Errorf("Mất file đã kiểm tra pkg/subsystem_1.go trong structured summary")
	}
}

func TestAgentIntelligence_DynamicStepBudget_Extension(t *testing.T) {
	tempDir := t.TempDir()
	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	for i := 1; i <= 3; i++ {
		_ = os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("file_%d.txt", i)), []byte("sample content"), 0644)
	}

	var responses []*domain.OpenAIChatResponse
	// Bước 1 -> Bước 3: Đọc các file khác nhau (tiến triển tốt, không lặp)
	for i := 1; i <= 3; i++ {
		responses = append(responses, &domain.OpenAIChatResponse{
			Choices: []domain.OpenAIChoice{
				{
					Message: domain.OpenAIMessage{
						Role: "assistant",
						ToolCalls: []domain.OpenAIToolCall{
							{
								ID:   fmt.Sprintf("call_%d", i),
								Type: "function",
								Function: domain.OpenAIFunctionCallData{
									Name:      "read_file",
									Arguments: fmt.Sprintf(`{"path": "file_%d.txt"}`, i),
								},
							},
						},
					},
				},
			},
		})
	}
	// Bước 4: Hoàn tất
	responses = append(responses, &domain.OpenAIChatResponse{
		Choices: []domain.OpenAIChoice{
			{
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: "Đã hoàn thành toàn bộ giải thích hệ thống phân tán.",
				},
			},
		},
	})

	mockChat := &MockChatUseCase{responses: responses}
	runner := NewRunner(mockChat, reg, &MockApprovalProvider{ApproveAll: true})
	ctx := context.Background()

	// Đặt MaxSteps ban đầu = 2. Task 'deadlock' có ngân sách gia hạn, và khi tiến triển tốt ở bước 2 sẽ được mở rộng!
	state, err := runner.Run(ctx, "Explain distributed deadlock behavior across nodes", domain.AgentRunOptions{
		MaxSteps:  2,
		Workspace: tempDir,
	})

	if err != nil {
		t.Fatalf("Runner trả về lỗi: %v", err)
	}

	if !state.IsCompleted {
		t.Errorf("Agent phải hoàn tất thành công sau khi được gia hạn ngân sách bước, stop_reason: %s", state.StopReason)
	}
	if state.CurrentStep <= 2 {
		t.Errorf("Kỳ vọng Agent được gia hạn vượt quá 2 bước ban đầu, thực tế đã chạy %d bước", state.CurrentStep)
	}
}
