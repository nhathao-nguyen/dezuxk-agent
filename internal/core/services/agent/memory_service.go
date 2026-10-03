package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// MemoryManager quản lý toàn diện 3 tầng bộ nhớ của Agent (Working, Recall, Archival)
type MemoryManager struct {
	mu          sync.RWMutex
	tenantCores map[string]*domain.CoreMemory
	archival    ports.MemoryRepository
	chatUseCase ports.ChatUseCase
	model       string
	initialCore domain.CoreMemory
}

// NewMemoryManager khởi tạo MemoryManager
func NewMemoryManager(
	archival ports.MemoryRepository,
	chatUseCase ports.ChatUseCase,
	model string,
	initialCore domain.CoreMemory,
) *MemoryManager {
	if initialCore.UpdatedAt.IsZero() {
		initialCore.UpdatedAt = time.Now()
	}

	cores := make(map[string]*domain.CoreMemory)
	defaultCopy := initialCore
	cores["default/default/default"] = &defaultCopy

	return &MemoryManager{
		tenantCores: cores,
		archival:    archival,
		chatUseCase: chatUseCase,
		model:       model,
		initialCore: initialCore,
	}
}

var _ ports.MemoryService = (*MemoryManager)(nil)

func (m *MemoryManager) GetCoreMemory() *domain.CoreMemory {
	return m.GetCoreMemoryForContext(context.Background())
}

func (m *MemoryManager) GetCoreMemoryForContext(ctx context.Context) *domain.CoreMemory {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns := domain.MemoryNamespaceFromContext(ctx)
	key := ns.Key()
	core, exists := m.tenantCores[key]
	if !exists {
		fresh := m.initialCore
		fresh.TenantID = ns.TenantID
		fresh.ProjectID = ns.ProjectID
		fresh.AgentID = ns.AgentID
		fresh.UpdatedAt = time.Now()
		m.tenantCores[key] = &fresh
		core = &fresh
	}
	copy := *core
	return &copy
}

func (m *MemoryManager) UpdateCoreMemory(update func(core *domain.CoreMemory)) {
	m.UpdateCoreMemoryForContext(context.Background(), update)
}

func (m *MemoryManager) UpdateCoreMemoryForContext(ctx context.Context, update func(core *domain.CoreMemory)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns := domain.MemoryNamespaceFromContext(ctx)
	key := ns.Key()
	core, exists := m.tenantCores[key]
	if !exists {
		fresh := m.initialCore
		fresh.TenantID = ns.TenantID
		fresh.ProjectID = ns.ProjectID
		fresh.AgentID = ns.AgentID
		core = &fresh
		m.tenantCores[key] = core
	}
	update(core)
	core.UpdatedAt = time.Now()
}

func (m *MemoryManager) StoreArchival(ctx context.Context, key, content string, tags []string) error {
	if m.archival == nil {
		return fmt.Errorf("kho lưu trữ Archival Memory chưa được kích hoạt")
	}

	ns := domain.MemoryNamespaceFromContext(ctx)
	item := &domain.ArchivalMemoryItem{
		TenantID:  ns.TenantID,
		ProjectID: ns.ProjectID,
		AgentID:   ns.AgentID,
		Key:       key,
		Content:   content,
		Tags:      tags,
	}
	return m.archival.Store(ctx, item)
}

func (m *MemoryManager) SearchArchival(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	if m.archival == nil {
		return nil, fmt.Errorf("kho lưu trữ Archival Memory chưa được kích hoạt")
	}
	return m.archival.SearchHybrid(ctx, query, topK)
}

// CompactConversation nén lịch sử hội thoại dài bằng Gemini Flash (Tier 2 Recall Memory)
func (m *MemoryManager) CompactConversation(
	ctx context.Context,
	messages []domain.OpenAIMessage,
	threshold int,
) ([]domain.OpenAIMessage, error) {
	if threshold <= 0 {
		threshold = 12
	}

	// Nếu số lượng tin nhắn chưa vượt ngưỡng, giữ nguyên
	if len(messages) <= threshold {
		return messages, nil
	}

	keepRecent := 4
	boundary := findSafeCompactionBoundary(messages, keepRecent)
	if boundary <= 1 {
		return messages, nil
	}

	systemMsg := messages[0]
	middleMessages := messages[1:boundary]
	recentMessages := messages[boundary:]

	var sb strings.Builder
	for _, msg := range middleMessages {
		content := strings.TrimSpace(msg.Content)
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, content))
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				sb.WriteString(fmt.Sprintf("[Tool Call]: %s(%s)\n", tc.Function.Name, tc.Function.Arguments))
			}
		}
	}

	prompt := fmt.Sprintf(`You are a conversation summarizer for an AI agent.
Summarize the following past conversation turns into a dense technical context summary (max 3-5 sentences).
Preserve all key decisions, files modified, tools executed, and user constraints:

%s`, sb.String())

	summaryText := "[Lịch sử hội thoại trước đó đã được tóm tắt]"
	if m.chatUseCase != nil {
		resp, err := m.chatUseCase.ExecuteChatSync(ctx, &domain.OpenAIChatRequest{
			Model: m.model,
			Messages: []domain.OpenAIMessage{
				{Role: "system", Content: "You are a concise technical summarizer. Output only the summary without pleasantries."},
				{Role: "user", Content: prompt},
			},
		})
		if err == nil && len(resp.Choices) > 0 && resp.Choices[0].Message.Content != "" {
			summaryText = resp.Choices[0].Message.Content
		}
	}

	summaryMsg := domain.OpenAIMessage{
		Role:    "system",
		Content: fmt.Sprintf("## Previous Conversation Context Summary (Auto-Compacted)\n%s", strings.TrimSpace(summaryText)),
	}

	compacted := make([]domain.OpenAIMessage, 0, 2+len(recentMessages))
	compacted = append(compacted, systemMsg)
	compacted = append(compacted, summaryMsg)
	compacted = append(compacted, recentMessages...)

	return compacted, nil
}

// findSafeCompactionBoundary tìm vị trí cắt an toàn không làm vỡ cặp assistant (tool_calls) - tool (result)
func findSafeCompactionBoundary(messages []domain.OpenAIMessage, keepRecent int) int {
	if len(messages) <= keepRecent+2 {
		return -1
	}

	boundary := len(messages) - keepRecent

	// Nếu boundary trúng vào tin nhắn role "tool":
	// 1. Dịch lùi lại trước tin nhắn assistant chứa tool_calls tương ứng để giữ trọn vẹn cả cặp trong recentMessages
	if messages[boundary].Role == "tool" {
		for boundary > 1 && messages[boundary].Role == "tool" {
			boundary--
		}
		// Nếu lùi quá sâu về đầu hội thoại (boundary <= 1), chuyển sang phương án dịch tiến lên
		// cho tới khi toàn bộ chuỗi phản hồi tool hoàn tất
		if boundary <= 1 {
			f := len(messages) - keepRecent
			for f < len(messages) && messages[f].Role == "tool" {
				f++
			}
			if f < len(messages) {
				return f
			}
			return -1
		}
	}

	if boundary <= 1 {
		return -1
	}

	return boundary
}
