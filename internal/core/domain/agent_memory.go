package domain

import (
	"fmt"
	"strings"
	"time"
)

// -------------------------------------------------------------
// TIER 1: Working / Core Memory (Luôn hiện diện trong Prompt)
// -------------------------------------------------------------

// CoreMemory lưu trữ các khối thông tin nền tảng cốt lõi của Agent (Letta-style)
type CoreMemory struct {
	TenantID       string    `json:"tenant_id,omitempty"`
	ProjectID      string    `json:"project_id,omitempty"`
	AgentID        string    `json:"agent_id,omitempty"`
	Persona        string    `json:"persona"`         // Định danh, phong cách và vai trò của Agent
	HumanProfile   string    `json:"human_profile"`   // Sở thích, quy ước coding của người dùng
	ProjectContext string    `json:"project_context"` // Quy tắc kiến trúc, quy ước của dự án
	Scratchpad     string    `json:"scratchpad"`      // Bảng nháp ghi chú tạm thời cho tác vụ hiện tại
	UpdatedAt      time.Time `json:"updated_at"`
}

// FormatPrompt render CoreMemory thành khối văn bản chèn vào System Prompt
func (c *CoreMemory) FormatPrompt() string {
	var sb strings.Builder
	sb.WriteString("## Working Memory (Core Context)\n")
	if strings.TrimSpace(c.Persona) != "" {
		sb.WriteString(fmt.Sprintf("### Persona\n%s\n\n", strings.TrimSpace(c.Persona)))
	}
	if strings.TrimSpace(c.HumanProfile) != "" {
		sb.WriteString(fmt.Sprintf("### User Conventions & Preferences\n%s\n\n", strings.TrimSpace(c.HumanProfile)))
	}
	if strings.TrimSpace(c.ProjectContext) != "" {
		sb.WriteString(fmt.Sprintf("### Project Architectural Rules\n%s\n\n", strings.TrimSpace(c.ProjectContext)))
	}
	if strings.TrimSpace(c.Scratchpad) != "" {
		sb.WriteString(fmt.Sprintf("### Current Scratchpad\n%s\n\n", strings.TrimSpace(c.Scratchpad)))
	}
	return sb.String()
}

// -------------------------------------------------------------
// TIER 2: Recall Memory (Lịch sử hội thoại có nén / Auto-Summarizer)
// -------------------------------------------------------------

// RecallMemorySummary bản tóm tắt một đoạn hội thoại quá khứ
type RecallMemorySummary struct {
	FromStep  int       `json:"from_step"`
	ToStep    int       `json:"to_step"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// -------------------------------------------------------------
// TIER 3: Archival Memory (Bộ nhớ dài hạn vô hạn: Hybrid FTS5 + Vector)
// -------------------------------------------------------------

// ArchivalMemoryItem một bản ghi kiến thức/kinh nghiệm bền vững trong cơ sở dữ liệu
type ArchivalMemoryItem struct {
	ID        int64     `json:"id"`
	TenantID  string    `json:"tenant_id,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	Key       string    `json:"key"`     // Khóa định danh hoặc chủ đề (ví dụ: "hexagonal_rules", "bug_fix_429")
	Content   string    `json:"content"` // Nội dung kiến thức chi tiết
	Tags      []string  `json:"tags"`    // Nhãn phân loại (ví dụ: ["architecture", "go", "security"])
	Embedding []float32 `json:"embedding,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MemorySearchResult kết quả tìm kiếm bộ nhớ kết hợp (Hybrid Search)
type MemorySearchResult struct {
	Item      ArchivalMemoryItem `json:"item"`
	Score     float32            `json:"score"`      // Điểm xếp hạng tổng hợp (RRF score)
	MatchType string             `json:"match_type"` // "hybrid" | "fts" | "vector"
}
