package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// -------------------------------------------------------------
// MemoryStoreTool: Lưu kiến thức vào Archival Memory
// -------------------------------------------------------------
type MemoryStoreTool struct {
	memorySvc ports.MemoryService
}

func NewMemoryStoreTool(memorySvc ports.MemoryService) *MemoryStoreTool {
	return &MemoryStoreTool{memorySvc: memorySvc}
}

func (t *MemoryStoreTool) Name() string { return "memory_store" }
func (t *MemoryStoreTool) Description() string {
	return "Lưu trữ vĩnh viễn một kiến thức, nguyên tắc kiến trúc hoặc kinh nghiệm sửa lỗi vào Bộ nhớ dài hạn (Archival Memory) với cơ chế tìm kiếm kết hợp FTS5 và Semantic Vector."
}
func (t *MemoryStoreTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *MemoryStoreTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"key": {
				"type": "string",
				"description": "Khóa định danh ngắn gọn duy nhất cho mẩu kiến thức (ví dụ: 'clean_arch_rules', 'fix_timeout_429')"
			},
			"content": {
				"type": "string",
				"description": "Nội dung kiến thức, quyết định thiết kế hoặc bài học kinh nghiệm cần ghi nhớ"
			},
			"tags": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Danh sách các nhãn phân loại (ví dụ: [\"architecture\", \"golang\", \"security\"])"
			}
		},
		"required": ["key", "content"]
	}`)
}

type MemoryStoreArgs struct {
	Key     string   `json:"key"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

func (t *MemoryStoreTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t.memorySvc == nil {
		return "", fmt.Errorf("MemoryService chưa được kích hoạt")
	}

	var args MemoryStoreArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho memory_store: %w", err)
	}

	if strings.TrimSpace(args.Key) == "" || strings.TrimSpace(args.Content) == "" {
		return "", fmt.Errorf("key và content không được để trống")
	}

	if err := t.memorySvc.StoreArchival(ctx, args.Key, args.Content, args.Tags); err != nil {
		return "", fmt.Errorf("lỗi khi lưu vào Archival Memory: %w", err)
	}

	return fmt.Sprintf("✓ Đã lưu thành công vào Archival Memory với key %q (Nhãn: %v)", args.Key, args.Tags), nil
}

// -------------------------------------------------------------
// MemorySearchTool: Tìm kiếm kiến thức trong Archival Memory
// -------------------------------------------------------------
type MemorySearchTool struct {
	memorySvc ports.MemoryService
}

func NewMemorySearchTool(memorySvc ports.MemoryService) *MemorySearchTool {
	return &MemorySearchTool{memorySvc: memorySvc}
}

func (t *MemorySearchTool) Name() string { return "memory_search" }
func (t *MemorySearchTool) Description() string {
	return "Tìm kiếm thông tin trong Bộ nhớ dài hạn (Archival Memory) bằng cơ chế Hybrid Search (kết hợp tìm kiếm từ khóa FTS5 và tương đồng ngữ nghĩa Semantic Vector)."
}
func (t *MemorySearchTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *MemorySearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Truy vấn tìm kiếm kiến thức hoặc từ khóa ngữ nghĩa"
			},
			"top_k": {
				"type": "integer",
				"description": "Số lượng kết quả tối đa cần trả về (mặc định: 5)"
			}
		},
		"required": ["query"]
	}`)
}

type MemorySearchArgs struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
}

func (t *MemorySearchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t.memorySvc == nil {
		return "", fmt.Errorf("MemoryService chưa được kích hoạt")
	}

	var args MemorySearchArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho memory_search: %w", err)
	}

	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("truy vấn query không được để trống")
	}

	topK := args.TopK
	if topK <= 0 {
		topK = 5
	}

	results, err := t.memorySvc.SearchArchival(ctx, args.Query, topK)
	if err != nil {
		return "", fmt.Errorf("lỗi khi tìm kiếm Archival Memory: %w", err)
	}

	if len(results) == 0 {
		return fmt.Sprintf("Không tìm thấy kết quả nào trong Archival Memory khớp với truy vấn: %q", args.Query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== KẾT QUẢ TÌM KIẾM BỘ NHỚ HYBRID CHO %q (%d kết quả) ===\n", args.Query, len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("\n[%d] Key: %s (Độ khớp: %.4f | Kiểu: %s)\n", i+1, r.Item.Key, r.Score, r.MatchType))
		if len(r.Item.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("    Tags: %s\n", strings.Join(r.Item.Tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("    Nội dung: %s\n", r.Item.Content))
	}

	return sb.String(), nil
}

// -------------------------------------------------------------
// MemoryUpdateCoreTool: Cập nhật Working/Core Memory
// -------------------------------------------------------------
type MemoryUpdateCoreTool struct {
	memorySvc ports.MemoryService
}

func NewMemoryUpdateCoreTool(memorySvc ports.MemoryService) *MemoryUpdateCoreTool {
	return &MemoryUpdateCoreTool{memorySvc: memorySvc}
}

func (t *MemoryUpdateCoreTool) Name() string { return "memory_update_core" }
func (t *MemoryUpdateCoreTool) Description() string {
	return "Cập nhật Bộ nhớ làm việc tức thời (Core / Working Memory). Dùng để cập nhật bảng nháp (scratchpad), ghi nhận quy ước của người dùng hoặc quy tắc dự án."
}
func (t *MemoryUpdateCoreTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *MemoryUpdateCoreTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"enum": ["scratchpad", "human_profile", "project_context", "persona"],
				"description": "Khối bộ nhớ cần cập nhật"
			},
			"content": {
				"type": "string",
				"description": "Nội dung mới cần cập nhật hoặc ghi đè vào khối bộ nhớ"
			}
		},
		"required": ["target", "content"]
	}`)
}

type MemoryUpdateCoreArgs struct {
	Target  string `json:"target"`
	Content string `json:"content"`
}

func (t *MemoryUpdateCoreTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t.memorySvc == nil {
		return "", fmt.Errorf("MemoryService chưa được kích hoạt")
	}

	var args MemoryUpdateCoreArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(args.Target)) {
	case "scratchpad":
		t.memorySvc.UpdateCoreMemory(func(core *domain.CoreMemory) {
			core.Scratchpad = args.Content
		})
	case "human_profile":
		t.memorySvc.UpdateCoreMemory(func(core *domain.CoreMemory) {
			core.HumanProfile = args.Content
		})
	case "project_context":
		t.memorySvc.UpdateCoreMemory(func(core *domain.CoreMemory) {
			core.ProjectContext = args.Content
		})
	case "persona":
		t.memorySvc.UpdateCoreMemory(func(core *domain.CoreMemory) {
			core.Persona = args.Content
		})
	default:
		return "", fmt.Errorf("target không hợp lệ: %q (chỉ chấp nhận: scratchpad, human_profile, project_context, persona)", args.Target)
	}

	return fmt.Sprintf("✓ Đã cập nhật thành công khối Core Memory: %s", args.Target), nil
}

// RegisterMemoryTools đăng ký các công cụ bộ nhớ vào ToolRegistry
func RegisterMemoryTools(registry ports.ToolRegistry, memorySvc ports.MemoryService) {
	if memorySvc == nil {
		return
	}
	registry.RegisterTool(NewMemoryStoreTool(memorySvc))
	registry.RegisterTool(NewMemorySearchTool(memorySvc))
	registry.RegisterTool(NewMemoryUpdateCoreTool(memorySvc))
}
