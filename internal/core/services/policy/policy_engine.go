package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

var (
	ErrUnauthorizedTool    = errors.New("công cụ không được phép thực thi theo chính sách phân quyền")
	ErrDestructiveApproval = errors.New("công cụ có tính phá hủy bị từ chối phê duyệt")
	ErrWorkspaceEscape     = errors.New("đường dẫn truy cập nằm ngoài phạm vi workspace cho phép của tenant")
	ErrDangerousCommand    = errors.New("câu lệnh bị chặn bởi bộ lọc an ninh Policy Engine (phát hiện hành vi nguy hiểm)")
	ErrConcurrencyLimit    = errors.New("vượt quá giới hạn thực thi công cụ đồng thời của tenant")
	ErrToolTimeout         = errors.New("thời gian thực thi công cụ vượt quá hạn mức cho phép")
)

var (
	// Các mẫu lệnh nguy hiểm tuyệt đối cấm trong môi trường agent
	reDangerousShell = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:format|diskpart|mkfs)\b`),
		regexp.MustCompile(`(?i)\brm\s+-[rRfF]{1,3}\s+(?:/|~|/\w+|[a-zA-Z]:\\?$)`),
		regexp.MustCompile(`(?i)\brmdir\s+/[sS]\s+/[qQ]\s+[a-zA-Z]:\\?$`),
		regexp.MustCompile(`(?i)\b(?:curl|wget|iwr)\b.*\|\s*(?:bash|sh|powershell|pwsh|iex)\b`),
		regexp.MustCompile(`(?i)\biex\s*\(.*(?:iwr|curl|downloadstring).*\)`),
		regexp.MustCompile(`(?i)\b(?:shutdown|reboot|init\s+0|poweroff)\b`),
		regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\};:`), // Fork bomb
	}
)

// PolicyEngine triển khai cổng ports.ToolExecutionService
type PolicyEngine struct {
	mu          sync.Mutex
	toolReg     ports.ToolRegistry
	approval    ports.ApprovalProvider
	semaphores  map[string]chan struct{} // tenant_id -> semaphore channel
	maxToolRuns int
}

// NewPolicyEngine khởi tạo PolicyEngine
func NewPolicyEngine(toolReg ports.ToolRegistry, approval ports.ApprovalProvider) *PolicyEngine {
	return &PolicyEngine{
		toolReg:     toolReg,
		approval:    approval,
		semaphores:  make(map[string]chan struct{}),
		maxToolRuns: 5,
	}
}

var _ ports.ToolExecutionService = (*PolicyEngine)(nil)

// SetApprovalProvider cập nhật provider xác nhận hành động
func (p *PolicyEngine) SetApprovalProvider(app ports.ApprovalProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.approval = app
}

// ValidateToolExecution kiểm tra trước tính hợp lệ của lời gọi công cụ mà không thực thi
func (p *PolicyEngine) ValidateToolExecution(ctx context.Context, toolName string, argsJSON string) error {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return errors.New("tên công cụ không được để trống")
	}

	targetTool, exists := p.toolReg.GetTool(toolName)
	if !exists {
		return fmt.Errorf("công cụ %q không tồn tại trong danh mục khả dụng", toolName)
	}

	identity, ok := domain.TenantIdentityFromContext(ctx)
	if !ok {
		identity = domain.DefaultRestrictedIdentity()
	}

	// 1. Kiểm tra quyền của Tenant đối với công cụ này
	if !identity.IsToolAllowed(toolName) {
		return fmt.Errorf("%w: công cụ %q không nằm trong danh mục được phép hoặc thiếu scope", ErrUnauthorizedTool, toolName)
	}

	// 2. Kiểm tra Scope cụ thể cho Shell / Browser / Memory
	if toolName == "run_command" {
		if !identity.AllowShell && identity.Role != "admin" {
			return fmt.Errorf("%w: tenant %q không có quyền thực thi shell (allow_shell=false)", ErrUnauthorizedTool, identity.TenantID)
		}
		if !identity.HasScope(domain.ScopeShell) && identity.Role != "admin" {
			return fmt.Errorf("%w: thiếu scope 'shell' để thực thi run_command", ErrUnauthorizedTool)
		}
	}

	if strings.HasPrefix(toolName, "browser_") {
		if !identity.HasScope(domain.ScopeBrowser) && identity.Role != "admin" {
			return fmt.Errorf("%w: thiếu scope 'browser' để sử dụng công cụ duyệt web", ErrUnauthorizedTool)
		}
	}

	// 3. Phân tích các tham số đường dẫn (Path Sandboxing)
	if err := p.validatePaths(ctx, identity, toolName, argsJSON); err != nil {
		return err
	}

	// 4. Nếu là lệnh shell, quét các mẫu câu lệnh nguy hiểm
	if toolName == "run_command" {
		if err := p.validateShellCommand(argsJSON); err != nil {
			return err
		}
	}

	// 5. Kiểm tra quyền Destructive / Yêu cầu phê duyệt
	if targetTool.Permission().IsDestructiveOrRequiresApproval() && identity.RequireApproval {
		if p.approval == nil {
			return fmt.Errorf("%w: công cụ %q yêu cầu phê duyệt bảo mật nhưng chưa cấu hình ApprovalProvider", ErrDestructiveApproval, toolName)
		}
	}

	return nil
}

// ExecuteTool thực thi một công cụ thông qua toàn bộ hàng rào chính sách bảo mật
func (p *PolicyEngine) ExecuteTool(ctx context.Context, toolName string, argsJSON string) (string, error) {
	toolName = strings.TrimSpace(toolName)
	targetTool, exists := p.toolReg.GetTool(toolName)
	if !exists {
		return "", fmt.Errorf("công cụ %q không tồn tại trong danh mục khả dụng", toolName)
	}

	identity, ok := domain.TenantIdentityFromContext(ctx)
	if !ok {
		identity = domain.DefaultInternalIdentity()
	}

	// 1. Cơ chế phê duyệt Human-in-the-Loop cho công cụ Destructive / RequiresApproval nếu bật RequireApproval
	if targetTool.Permission().IsDestructiveOrRequiresApproval() && identity.RequireApproval && p.approval != nil {
		approvalReq := domain.ApprovalRequest{
			ToolName:    toolName,
			Arguments:   argsJSON,
			Description: targetTool.Description(),
			Permission:  targetTool.Permission(),
			RequestedAt: time.Now(),
		}

		approved, err := p.approval.RequestApproval(ctx, approvalReq)
		if err != nil || !approved {
			return fmt.Sprintf("[XÁC NHẬN BỊ TỪ CHỐI]: Người dùng đã từ chối cấp quyền thực thi công cụ %q với tham số: %s. Hãy đề xuất phương án an toàn khác hoặc hỏi lại.", toolName, argsJSON),
				fmt.Errorf("%w: người dùng hoặc chính sách đã từ chối phê duyệt công cụ %q", ErrDestructiveApproval, toolName)
		}
	}

	// 2. Kiểm tra chính sách an ninh và phân quyền thực thi
	if err := p.ValidateToolExecution(ctx, toolName, argsJSON); err != nil {
		return fmt.Sprintf("[LỖI CHÍNH SÁCH BẢO MẬT]: %v", err), err
	}

	// 3. Giới hạn số lượng tác vụ đồng thời của từng Tenant (Concurrency Limiter)
	sem := p.getTenantSemaphore(identity.TenantID, identity.MaxConcurrentRuns)
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("%w: tenant %q đạt giới hạn %d tác vụ đồng thời", ErrConcurrencyLimit, identity.TenantID, identity.MaxConcurrentRuns)
	}

	// 4. Chuẩn hóa và tự động sửa các lỗi JSON tham số nếu có
	argsJSON = RepairJSONArguments(argsJSON)

	// 5. Áp dụng giới hạn thời gian chạy tối đa theo loại công cụ (Per-Tool Timeout)
	timeout := p.resolveToolTimeout(toolName, identity.MaxToolRuntime)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 6. Thực thi công cụ
	output, err := targetTool.Execute(execCtx, argsJSON)
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: thời gian chạy công cụ vượt quá %v", ErrToolTimeout, timeout)
		}
		return output, err
	}

	return output, nil
}

func (p *PolicyEngine) resolveToolTimeout(toolName string, tenantMax time.Duration) time.Duration {
	if tenantMax > 0 {
		return tenantMax
	}
	switch {
	case toolName == "run_command":
		return 120 * time.Second
	case strings.HasPrefix(toolName, "browser_"):
		return 45 * time.Second
	case strings.HasPrefix(toolName, "read_") || strings.HasPrefix(toolName, "write_") || toolName == "replace_file_content":
		return 15 * time.Second
	case toolName == "grep_code" || strings.HasPrefix(toolName, "memory_"):
		return 30 * time.Second
	default:
		return 60 * time.Second
	}
}

func (p *PolicyEngine) getTenantSemaphore(tenantID string, maxRuns int) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	if maxRuns <= 0 {
		maxRuns = p.maxToolRuns
	}

	sem, exists := p.semaphores[tenantID]
	if !exists || cap(sem) != maxRuns {
		sem = make(chan struct{}, maxRuns)
		p.semaphores[tenantID] = sem
	}
	return sem
}

func (p *PolicyEngine) validatePaths(ctx context.Context, identity domain.TenantIdentity, toolName string, argsJSON string) error {
	if strings.TrimSpace(argsJSON) == "" {
		return nil
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		// Nếu JSON lỗi cú pháp, parser tool sẽ xử lý
		return nil
	}

	pathKeys := []string{"path", "cwd", "file", "target", "source", "destination", "directory"}
	for _, key := range pathKeys {
		val, exists := parsed[key]
		if !exists {
			continue
		}
		pathStr, ok := val.(string)
		if !ok || strings.TrimSpace(pathStr) == "" {
			continue
		}

		pathStr = strings.TrimSpace(pathStr)
		clean := filepath.Clean(pathStr)

		// 1. Kiểm tra dấu vết path traversal
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") || strings.Contains(clean, "\\..\\") {
			return fmt.Errorf("%w: phát hiện nỗ lực thoát workspace qua '..' trong trường %s=%q", ErrWorkspaceEscape, key, pathStr)
		}

		// 2. Đối soát với AllowedWorkspaceRoots của tenant
		if !identity.IsWorkspaceAllowed(clean) {
			return fmt.Errorf("%w: đường dẫn %q nằm ngoài phạm vi được phép của tenant %q", ErrWorkspaceEscape, pathStr, identity.TenantID)
		}
	}

	return nil
}

func (p *PolicyEngine) validateShellCommand(argsJSON string) error {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		return nil
	}

	cmd := strings.TrimSpace(parsed.Command)
	if cmd == "" {
		return nil
	}

	for _, re := range reDangerousShell {
		if re.MatchString(cmd) {
			return fmt.Errorf("%w: câu lệnh chứa mẫu cấm nguy hiểm: %q", ErrDangerousCommand, cmd)
		}
	}

	return nil
}

// ValidateVerificationCommand kiểm tra lệnh xác minh trong Graph Engine có thuộc các danh mục an toàn được phép không
func ValidateVerificationCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}

	// 1. Quét các lệnh cấm nguy hiểm
	for _, re := range reDangerousShell {
		if re.MatchString(cmd) {
			return fmt.Errorf("%w: câu lệnh xác minh chứa lệnh cấm nguy hại: %q", ErrDangerousCommand, cmd)
		}
	}

	// 2. Chặn các chuỗi pipe nguy hiểm
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "| rm") || strings.Contains(lower, "| del") || strings.Contains(lower, "curl") || strings.Contains(lower, "wget") {
		return fmt.Errorf("%w: câu lệnh xác minh không được chứa thao tác tải mạng hoặc xóa tệp: %q", ErrDangerousCommand, cmd)
	}

	return nil
}
