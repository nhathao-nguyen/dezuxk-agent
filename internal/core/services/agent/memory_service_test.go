package agent

import (
	"context"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
)

func TestMemoryManager_WorkingMemoryAndCompaction(t *testing.T) {
	memRepo := session.NewMemoryMemoryRepository()
	mockChat := &MockChatUseCase{
		responses: []*domain.OpenAIChatResponse{
			{
				Choices: []domain.OpenAIChoice{
					{
						Message: domain.OpenAIMessage{
							Role:    "assistant",
							Content: "User requested creating a project. Architecture rules set to Hexagonal.",
						},
					},
				},
			},
		},
	}

	initialCore := domain.CoreMemory{
		Persona:        "Expert Go Engineer",
		ProjectContext: "Always follow Clean Architecture",
	}

	mgr := NewMemoryManager(memRepo, mockChat, "gemini-3.8-flash", initialCore)

	// 1. Kiểm tra Working Memory
	core := mgr.GetCoreMemory()
	if core.Persona != "Expert Go Engineer" {
		t.Errorf("Persona không đúng: %s", core.Persona)
	}

	prompt := core.FormatPrompt()
	if !strings.Contains(prompt, "Expert Go Engineer") || !strings.Contains(prompt, "Clean Architecture") {
		t.Errorf("FormatPrompt thiếu nội dung: %s", prompt)
	}

	// Update Scratchpad
	mgr.UpdateCoreMemory(func(c *domain.CoreMemory) {
		c.Scratchpad = "Investigating memory leak"
	})
	if mgr.GetCoreMemory().Scratchpad != "Investigating memory leak" {
		t.Errorf("Scratchpad chưa được cập nhật")
	}

	// 2. Kiểm tra Recall Memory Auto-Compaction
	ctx := context.Background()
	var longMessages []domain.OpenAIMessage
	longMessages = append(longMessages, domain.OpenAIMessage{Role: "system", Content: "System prompt"})
	for i := 1; i <= 15; i++ {
		longMessages = append(longMessages, domain.OpenAIMessage{Role: "user", Content: "Question " + string(rune('0'+i))})
		longMessages = append(longMessages, domain.OpenAIMessage{Role: "assistant", Content: "Answer " + string(rune('0'+i))})
	}

	compacted, err := mgr.CompactConversation(ctx, longMessages, 10)
	if err != nil {
		t.Fatalf("CompactConversation lỗi: %v", err)
	}

	if len(compacted) >= len(longMessages) {
		t.Errorf("Kỳ vọng compacted messages (%d) < original messages (%d)", len(compacted), len(longMessages))
	}
	if !strings.Contains(compacted[1].Content, "Auto-Compacted") {
		t.Errorf("Compacted messages thiếu block summary: %s", compacted[1].Content)
	}

	// 3. Kiểm tra Archival Memory Store & Search
	if err := mgr.StoreArchival(ctx, "jwt_auth", "JWT token expiry is set to 24h with refresh rotation.", []string{"auth", "security"}); err != nil {
		t.Fatalf("Lỗi StoreArchival: %v", err)
	}

	results, err := mgr.SearchArchival(ctx, "JWT token expiration", 5)
	if err != nil {
		t.Fatalf("Lỗi SearchArchival: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("SearchArchival không tìm thấy kết quả")
	}
	if results[0].Item.Key != "jwt_auth" {
		t.Errorf("Kỳ vọng key 'jwt_auth', nhận được: %s", results[0].Item.Key)
	}
}

func TestFindSafeCompactionBoundaryWithToolCalls(t *testing.T) {
	messages := []domain.OpenAIMessage{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Run calculation"},
		{Role: "assistant", Content: "I will call tools", ToolCalls: []domain.OpenAIToolCall{
			{ID: "call_1", Function: domain.OpenAIFunctionCallData{Name: "calc"}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "result: 42"},
		{Role: "assistant", Content: "Result is 42"},
		{Role: "user", Content: "Next question"},
		{Role: "assistant", Content: "Answer"},
	}

	// keepRecent = 4: len(messages)=7, 7 - 4 = 3 (trúng messages[3] là role: "tool")
	// Phải lùi về messages[2] (assistant có tool_calls)
	b := findSafeCompactionBoundary(messages, 4)
	if b != 2 {
		t.Errorf("Kỳ vọng boundary lùi về 2 (assistant), nhận được: %d", b)
	}

	// recentMessages bắt đầu từ messages[2]: assistant và tool message đi cùng nhau
	recent := messages[b:]
	if recent[0].Role != "assistant" || len(recent[0].ToolCalls) == 0 {
		t.Errorf("Tin nhắn đầu tiên của recentMessages phải là assistant có tool_calls")
	}
	if recent[1].Role != "tool" || recent[1].ToolCallID != "call_1" {
		t.Errorf("Tin nhắn thứ hai của recentMessages phải là tool tương ứng")
	}
}
