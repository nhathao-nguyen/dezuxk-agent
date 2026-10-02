package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// SubagentTool triển khai domain.AgentTool cho phép gọi sub-agent biệt lập
type SubagentTool struct {
	supervisor ports.SubagentSupervisor
}

// NewSubagentTool khởi tạo SubagentTool
func NewSubagentTool(supervisor ports.SubagentSupervisor) *SubagentTool {
	return &SubagentTool{supervisor: supervisor}
}

var _ domain.AgentTool = (*SubagentTool)(nil)

func (t *SubagentTool) Name() string { return "invoke_subagent" }
func (t *SubagentTool) Description() string {
	return "Khởi chạy một sub-agent biệt lập (researcher, coder, reviewer, general) để thực hiện một nhiệm vụ chuyên biệt trong ngữ cảnh độc lập mà không làm đầy bộ nhớ context của agent chính."
}
func (t *SubagentTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *SubagentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"role": {
				"type": "string",
				"enum": ["researcher", "coder", "reviewer", "general"],
				"description": "Vai trò của sub-agent: 'researcher' (tra cứu code/tài liệu read-only), 'coder' (sửa code/chạy test), 'reviewer' (đánh giá code/bảo mật), 'general' (đa nhiệm)."
			},
			"prompt": {
				"type": "string",
				"description": "Mục tiêu cụ thể và chi tiết mà sub-agent cần thực hiện và báo cáo kết quả."
			},
			"context": {
				"type": "string",
				"description": "Thông tin ngữ cảnh bổ sung từ tác vụ cha (tùy chọn)."
			}
		},
		"required": ["role", "prompt"]
	}`)
}

func (t *SubagentTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Role    string `json:"role"`
		Prompt  string `json:"prompt"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("lỗi đọc tham số invoke_subagent: %w", err)
	}

	if args.Role == "" {
		args.Role = "general"
	}
	if args.Prompt == "" {
		return "", fmt.Errorf("tham số 'prompt' không được để trống")
	}

	result, err := t.supervisor.InvokeSubagent(ctx, domain.SubagentTask{
		Role:    domain.SubagentRole(args.Role),
		Prompt:  args.Prompt,
		Context: args.Context,
	})
	if err != nil {
		return "", fmt.Errorf("thực thi sub-agent [%s] thất bại: %w", args.Role, err)
	}

	resJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result.Summary, nil
	}

	return string(resJSON), nil
}

// RegisterSubagentTool đăng ký invoke_subagent vào ToolRegistry
func RegisterSubagentTool(registry ports.ToolRegistry, supervisor ports.SubagentSupervisor) {
	registry.RegisterTool(NewSubagentTool(supervisor))
}
