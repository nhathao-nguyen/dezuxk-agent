package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// EvidenceKind phân loại mức độ quan trọng và khả năng phục hồi của bằng chứng
type EvidenceKind string

const (
	// EvidenceCritical: Lỗi biên dịch, lỗi test, diff sửa đổi mã nguồn, cấu hình hệ thống
	EvidenceCritical EvidenceKind = "critical_evidence"
	// EvidenceUseful: Nội dung đọc mã nguồn, tài liệu, kết quả phân tích
	EvidenceUseful EvidenceKind = "useful_context"
	// EvidenceRefetchable: Kết quả grep/find/list dài có thể gọi tool lại để lấy
	EvidenceRefetchable EvidenceKind = "refetchable_data"
	// EvidenceLowValue: Dữ liệu nhiễu, log không chứa thông tin quan trọng
	EvidenceLowValue EvidenceKind = "low_value_output"
)

// EvidenceItem lưu giữ một mục bằng chứng thô kèm siêu dữ liệu định danh
type EvidenceItem struct {
	ID          string       `json:"id"`
	Kind        EvidenceKind `json:"kind"`
	SourceTool  string       `json:"source_tool"`
	Arguments   string       `json:"arguments"`
	Summary     string       `json:"summary"`
	FullContent string       `json:"full_content"`
	Importance  int          `json:"importance"` // 1 đến 10 (10 = sống còn)
	ReFetchable bool         `json:"refetchable"`
	StepIndex   int          `json:"step_index"`
	CreatedAt   time.Time    `json:"created_at"`
}

// EvidenceStore kho lưu trữ bằng chứng thô (L5 Raw Evidence Store) bảo đảm dữ liệu không bị biến mất khi nén
type EvidenceStore struct {
	mu    sync.RWMutex
	items map[string]*EvidenceItem
	order []string
}

// NewEvidenceStore khởi tạo kho lưu trữ bằng chứng
func NewEvidenceStore() *EvidenceStore {
	return &EvidenceStore{
		items: make(map[string]*EvidenceItem),
	}
}

// Store lưu trữ một mục bằng chứng thô vào kho
func (s *EvidenceStore) Store(item EvidenceItem) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if item.ID == "" {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d", item.SourceTool, item.Arguments, item.FullContent, time.Now().UnixNano())))
		item.ID = "ev_" + hex.EncodeToString(hash[:8])
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}

	itemCopy := item
	if _, exists := s.items[item.ID]; !exists {
		s.order = append(s.order, item.ID)
	}
	s.items[item.ID] = &itemCopy
	return item.ID
}

// Get truy xuất bằng chứng theo ID
func (s *EvidenceStore) Get(id string) (*EvidenceItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok {
		return nil, false
	}
	copy := *item
	return &copy, true
}

// ListCritical trả về danh sách tất cả các bằng chứng tối quan trọng
func (s *EvidenceStore) ListCritical() []EvidenceItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []EvidenceItem
	for _, id := range s.order {
		if item, ok := s.items[id]; ok && item.Kind == EvidenceCritical {
			result = append(result, *item)
		}
	}
	return result
}

// Search tìm kiếm bằng chứng theo từ khóa
func (s *EvidenceStore) Search(query string, topK int) []EvidenceItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := strings.ToLower(query)
	var matched []EvidenceItem
	for _, id := range s.order {
		item := s.items[id]
		if strings.Contains(strings.ToLower(item.FullContent), q) ||
			strings.Contains(strings.ToLower(item.Summary), q) ||
			strings.Contains(strings.ToLower(item.SourceTool), q) {
			matched = append(matched, *item)
			if topK > 0 && len(matched) >= topK {
				break
			}
		}
	}
	return matched
}

// ClassifyToolOutput phân loại thông minh đầu ra của tool theo Section 10
func ClassifyToolOutput(toolName, args, output string, isErr bool) (EvidenceKind, int, bool) {
	outLower := strings.ToLower(output)

	// A. Bằng chứng tối quan trọng: lỗi biên dịch, test fail, diff, sửa mã nguồn
	if isErr ||
		strings.Contains(outLower, "compiler error") ||
		strings.Contains(outLower, "syntaxerror") ||
		strings.Contains(outLower, "build failed") ||
		strings.Contains(outLower, "--- fail:") ||
		strings.Contains(outLower, "panic:") ||
		strings.Contains(outLower, "fatal error:") {
		return EvidenceCritical, 10, false
	}

	if toolName == "replace_file_content" || toolName == "write_file" || strings.Contains(outLower, "diff --git") {
		return EvidenceCritical, 9, false
	}

	// B. Dữ liệu có thể truy xuất lại (Re-fetchable): grep, list_dir, search
	if toolName == "grep_search" || toolName == "list_dir" || strings.Contains(toolName, "search") {
		return EvidenceRefetchable, 4, true
	}

	// C. Nội dung hữu ích (Useful Context): đọc file
	if toolName == "read_file" || toolName == "view_file" {
		return EvidenceUseful, 6, true
	}

	if len(output) > 2000 {
		return EvidenceUseful, 5, false
	}

	// D. Thông thường
	return EvidenceUseful, 5, false
}

// CompactToolOutputIfNeeded nén an toàn đầu ra của công cụ nếu quá dài, bảo toàn toàn bộ trong EvidenceStore
func CompactToolOutputIfNeeded(store *EvidenceStore, toolName, args, output string, stepIndex int, isErr bool) string {
	kind, importance, refetchable := ClassifyToolOutput(toolName, args, output, isErr)

	// Nếu là Critical Evidence hoặc đầu ra ngắn (< 1500 chars), giữ nguyên trong active prompt
	if len(output) <= 1500 || (kind == EvidenceCritical && len(output) <= 4000) {
		if store != nil {
			store.Store(EvidenceItem{
				Kind:        kind,
				SourceTool:  toolName,
				Arguments:   args,
				Summary:     extractSummarySnippet(output),
				FullContent: output,
				Importance:  importance,
				ReFetchable: refetchable,
				StepIndex:   stepIndex,
			})
		}
		return output
	}

	// Đầu ra quá dài: Lưu toàn bộ bản thô vào EvidenceStore và tạo bản tóm tắt có chỉ mục
	evidenceID := ""
	if store != nil {
		evidenceID = store.Store(EvidenceItem{
			Kind:        kind,
			SourceTool:  toolName,
			Arguments:   args,
			Summary:     fmt.Sprintf("Output of %s (%d chars)", toolName, len(output)),
			FullContent: output,
			Importance:  importance,
			ReFetchable: refetchable,
			StepIndex:   stepIndex,
		})
	}

	// Tạo snippet đầu và cuối
	head := output[:600]
	tail := output[len(output)-600:]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[TOOL OUTPUT TRUNCATED - %d characters total]\n", len(output)))
	if evidenceID != "" {
		sb.WriteString(fmt.Sprintf("[RAW EVIDENCE PERSISTED in EvidenceStore: ref=%s, kind=%s, refetchable=%v]\n", evidenceID, kind, refetchable))
	}
	sb.WriteString("--- BEGIN SNIPPET ---\n")
	sb.WriteString(head)
	sb.WriteString("\n\n... [intermediate output omitted for context efficiency, full content preserved in EvidenceStore] ...\n\n")
	sb.WriteString(tail)
	sb.WriteString("\n--- END SNIPPET ---")

	return sb.String()
}

func extractSummarySnippet(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return ""
	}
	if len(lines) == 1 {
		if len(lines[0]) > 120 {
			return lines[0][:120] + "..."
		}
		return lines[0]
	}
	return lines[0] + " (" + fmt.Sprintf("%d lines", len(lines)) + ")"
}
