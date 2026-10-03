package domain

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Danh sách các scopes chuẩn trong hệ thống Dezuxk
const (
	ScopeChat      = "chat"
	ScopeResponses = "responses"
	ScopeAgent     = "agent"
	ScopeMemory    = "memory"
	ScopeBrowser   = "browser"
	ScopeShell     = "shell"
	ScopeAdmin     = "admin"
)

// NetworkPolicy cấu hình chính sách mạng cho từng tenant
type NetworkPolicy struct {
	AllowOutbound  bool     `json:"allow_outbound"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

// IsURLAllowed kiểm tra xem địa chỉ URL có được phép truy cập theo NetworkPolicy không
func (np NetworkPolicy) IsURLAllowed(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}

	// Chặn các endpoint nhạy cảm (cloud metadata, localhost) để chống SSRF
	if host == "169.254.169.254" || host == "metadata.google.internal" || host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return false
	}

	for _, blocked := range np.BlockedDomains {
		b := strings.ToLower(strings.TrimSpace(blocked))
		if b != "" && (host == b || strings.HasSuffix(host, "."+b)) {
			return false
		}
	}

	if len(np.AllowedDomains) > 0 {
		for _, allowed := range np.AllowedDomains {
			a := strings.ToLower(strings.TrimSpace(allowed))
			if a == "*" || host == a || strings.HasSuffix(host, "."+a) {
				return true
			}
		}
		return false
	}

	return np.AllowOutbound
}

// TenantIdentity đại diện cho đối tượng định danh tin cậy nội bộ của từng yêu cầu
type TenantIdentity struct {
	TenantID              string        `json:"tenant_id"`
	KeyID                 string        `json:"key_id"`
	Role                  string        `json:"role"` // "admin" hoặc "user"
	Scopes                []string      `json:"scopes"`
	AllowedModels         []string      `json:"allowed_models"`
	AllowedTools          []string      `json:"allowed_tools"`
	AllowedWorkspaceRoots []string      `json:"allowed_workspace_roots"`
	MaxAgentSteps         int           `json:"max_agent_steps"`
	MaxConcurrentRuns     int           `json:"max_concurrent_runs"`
	MaxToolRuntime        time.Duration `json:"max_tool_runtime"`
	RequireApproval       bool          `json:"require_approval"`
	AllowShell            bool          `json:"allow_shell"`
	EnforceSandbox        bool          `json:"enforce_sandbox"`
	AutoMergeAllowed      bool          `json:"auto_merge_allowed"`
	NetworkPolicy         NetworkPolicy `json:"network_policy"`
}

// AgentSecurityContext lưu trữ snapshot phân quyền hiệu lực của TenantIdentity gắn liền với AgentRun
type AgentSecurityContext struct {
	TenantID              string        `json:"tenant_id"`
	KeyID                 string        `json:"key_id"`
	Role                  string        `json:"role"`
	Scopes                []string      `json:"scopes"`
	AllowedModels         []string      `json:"allowed_models"`
	AllowedTools          []string      `json:"allowed_tools"`
	AllowedWorkspaceRoots []string      `json:"allowed_workspace_roots"`
	MaxAgentSteps         int           `json:"max_agent_steps"`
	MaxConcurrentRuns     int           `json:"max_concurrent_runs"`
	MaxToolRuntime        time.Duration `json:"max_tool_runtime"`
	RequireApproval       bool          `json:"require_approval"`
	AllowShell            bool          `json:"allow_shell"`
	EnforceSandbox        bool          `json:"enforce_sandbox"`
	AutoMergeAllowed      bool          `json:"auto_merge_allowed"`
	NetworkPolicy         NetworkPolicy `json:"network_policy"`
}

// SecurityContextFromTenantIdentity tạo snapshot an toàn các chính sách ủy quyền từ TenantIdentity
func SecurityContextFromTenantIdentity(id TenantIdentity) *AgentSecurityContext {
	return &AgentSecurityContext{
		TenantID:              id.TenantID,
		KeyID:                 id.KeyID,
		Role:                  id.Role,
		Scopes:                append([]string(nil), id.Scopes...),
		AllowedModels:         append([]string(nil), id.AllowedModels...),
		AllowedTools:          append([]string(nil), id.AllowedTools...),
		AllowedWorkspaceRoots: append([]string(nil), id.AllowedWorkspaceRoots...),
		MaxAgentSteps:         id.MaxAgentSteps,
		MaxConcurrentRuns:     id.MaxConcurrentRuns,
		MaxToolRuntime:        id.MaxToolRuntime,
		RequireApproval:       id.RequireApproval,
		AllowShell:            id.AllowShell,
		EnforceSandbox:        id.EnforceSandbox,
		AutoMergeAllowed:      id.AutoMergeAllowed,
		NetworkPolicy:         id.NetworkPolicy,
	}
}

// ToTenantIdentity khôi phục đầy đủ ngữ cảnh TenantIdentity từ AgentSecurityContext
func (s *AgentSecurityContext) ToTenantIdentity() TenantIdentity {
	if s == nil {
		return TenantIdentity{
			TenantID: "default",
			Role:     "user",
			Scopes:   []string{ScopeAgent},
		}
	}
	return TenantIdentity{
		TenantID:              s.TenantID,
		KeyID:                 s.KeyID,
		Role:                  s.Role,
		Scopes:                append([]string(nil), s.Scopes...),
		AllowedModels:         append([]string(nil), s.AllowedModels...),
		AllowedTools:          append([]string(nil), s.AllowedTools...),
		AllowedWorkspaceRoots: append([]string(nil), s.AllowedWorkspaceRoots...),
		MaxAgentSteps:         s.MaxAgentSteps,
		MaxConcurrentRuns:     s.MaxConcurrentRuns,
		MaxToolRuntime:        s.MaxToolRuntime,
		RequireApproval:       s.RequireApproval,
		AllowShell:            s.AllowShell,
		EnforceSandbox:        s.EnforceSandbox,
		AutoMergeAllowed:      s.AutoMergeAllowed,
		NetworkPolicy:         s.NetworkPolicy,
	}
}

// HasScope kiểm tra xem identity có scope yêu cầu hay không
func (id TenantIdentity) HasScope(scope string) bool {
	if id.Role == "admin" {
		return true
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	for _, s := range id.Scopes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "*" || s == scope {
			return true
		}
	}
	return false
}

// IsToolAllowed kiểm tra xem một tool cụ thể có được phép thực thi không
func (id TenantIdentity) IsToolAllowed(toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return false
	}

	// 1. Kiểm tra shell tool cần quyền shell
	if toolName == "run_command" && !id.AllowShell && id.Role != "admin" {
		return false
	}

	// 2. Kiểm tra browser tools cần scope browser
	if strings.HasPrefix(toolName, "browser_") && !id.HasScope(ScopeBrowser) && id.Role != "admin" {
		return false
	}

	// 3. Nếu danh sách AllowedTools rỗng
	if len(id.AllowedTools) == 0 {
		// Mặc định cho phép các công cụ an toàn, trừ phi bị hạn chế scope
		return true
	}

	for _, pattern := range id.AllowedTools {
		pattern = strings.TrimSpace(pattern)
		if pattern == "*" || pattern == toolName {
			return true
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(toolName, prefix) {
				return true
			}
		}
	}
	return false
}

// IsWorkspaceAllowed kiểm tra xem workspace chỉ định có nằm trong phạm vi cho phép của tenant hay không
func (id TenantIdentity) IsWorkspaceAllowed(targetPath string) bool {
	if id.Role == "admin" {
		return true
	}

	cleanTarget := filepath.Clean(targetPath)
	absTarget, err := filepath.Abs(cleanTarget)
	if err != nil {
		return false
	}

	// Nếu tenant có danh sách allowed roots cụ thể
	if len(id.AllowedWorkspaceRoots) > 0 {
		for _, root := range id.AllowedWorkspaceRoots {
			absRoot, err := filepath.Abs(filepath.Clean(root))
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(absRoot, absTarget)
			if err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				return true
			}
		}
		return false
	}

	// Mặc định mỗi tenant chỉ được truy cập thư mục con trong workspaces/<tenant_id> hoặc .dezuxk/workspaces/<tenant_id>
	tenantDefaultRoot := filepath.Clean(filepath.Join("workspaces", id.TenantID))
	absTenantRoot, _ := filepath.Abs(tenantDefaultRoot)
	rel, err := filepath.Rel(absTenantRoot, absTarget)
	if err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return true
	}

	// Cho phép đường dẫn tương đối hiện tại nếu là "." và chưa có thư mục riêng
	if cleanTarget == "." || cleanTarget == "" {
		return true
	}

	return false
}

// IsURLAllowed kiểm tra xem URL có được phép truy cập theo chính sách mạng của tenant không
func (id TenantIdentity) IsURLAllowed(rawURL string) bool {
	if id.Role == "admin" {
		return true
	}
	return id.NetworkPolicy.IsURLAllowed(rawURL)
}

// EffectiveMaxSteps trả về số bước thực thi tối đa sau khi áp dụng chính sách tenant
func (id TenantIdentity) EffectiveMaxSteps(requested int) int {
	maxAllowed := id.MaxAgentSteps
	if maxAllowed <= 0 {
		maxAllowed = 25
	}
	if requested <= 0 || requested > maxAllowed {
		return maxAllowed
	}
	return requested
}

type tenantIdentityContextKey struct{}

// ContextWithTenantIdentity lưu TenantIdentity vào context
func ContextWithTenantIdentity(ctx context.Context, id TenantIdentity) context.Context {
	return context.WithValue(ctx, tenantIdentityContextKey{}, id)
}

// TenantIdentityFromContext trích xuất TenantIdentity từ context
func TenantIdentityFromContext(ctx context.Context) (TenantIdentity, bool) {
	if ctx == nil {
		return TenantIdentity{}, false
	}
	val := ctx.Value(tenantIdentityContextKey{})
	if id, ok := val.(TenantIdentity); ok {
		return id, true
	}
	return TenantIdentity{}, false
}

// DefaultAdminIdentity trả về identity đầy đủ quyền cho quản trị viên
func DefaultAdminIdentity() TenantIdentity {
	return DefaultInternalIdentity()
}

// DefaultInternalIdentity trả về identity đầy đủ quyền cho các tác vụ nội bộ/local/tests khi không qua HTTP gateway
func DefaultInternalIdentity() TenantIdentity {
	return TenantIdentity{
		TenantID:              "internal-local",
		KeyID:                 "internal",
		Role:                  "admin",
		Scopes:                []string{ScopeChat, ScopeResponses, ScopeAgent, ScopeMemory, ScopeBrowser, ScopeShell, ScopeAdmin},
		AllowedTools:          nil,
		AllowedWorkspaceRoots: nil,
		MaxAgentSteps:         50,
		MaxConcurrentRuns:     10,
		MaxToolRuntime:        120 * time.Second,
		RequireApproval:       false,
		AllowShell:            true,
		EnforceSandbox:        false,
		AutoMergeAllowed:      true,
	}
}

// DefaultRestrictedIdentity trả về identity giới hạn mặc định cho các yêu cầu không xác thực
func DefaultRestrictedIdentity() TenantIdentity {
	return TenantIdentity{
		TenantID:        "anonymous",
		KeyID:           "anon",
		Role:            "user",
		Scopes:          []string{ScopeChat, ScopeResponses},
		MaxAgentSteps:   10,
		MaxToolRuntime:  30 * time.Second,
		RequireApproval: true,
		AllowShell:      false,
		EnforceSandbox:  true,
	}
}

// MemoryNamespace xác định phân cấp định danh bộ nhớ: tenant_id -> project_id -> agent_id
type MemoryNamespace struct {
	TenantID  string `json:"tenant_id"`
	ProjectID string `json:"project_id"`
	AgentID   string `json:"agent_id"`
}

func (m MemoryNamespace) Key() string {
	tid := strings.TrimSpace(m.TenantID)
	if tid == "" {
		tid = "default"
	}
	pid := strings.TrimSpace(m.ProjectID)
	if pid == "" {
		pid = "default"
	}
	aid := strings.TrimSpace(m.AgentID)
	if aid == "" {
		aid = "default"
	}
	return tid + "/" + pid + "/" + aid
}

type memoryNamespaceContextKey struct{}

// ContextWithMemoryNamespace lưu MemoryNamespace vào context
func ContextWithMemoryNamespace(ctx context.Context, ns MemoryNamespace) context.Context {
	return context.WithValue(ctx, memoryNamespaceContextKey{}, ns)
}

// MemoryNamespaceFromContext trích xuất MemoryNamespace từ context
func MemoryNamespaceFromContext(ctx context.Context) MemoryNamespace {
	if ctx != nil {
		if ns, ok := ctx.Value(memoryNamespaceContextKey{}).(MemoryNamespace); ok {
			return ns
		}
		if id, ok := TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
			return MemoryNamespace{
				TenantID:  id.TenantID,
				ProjectID: "default",
				AgentID:   "default",
			}
		}
	}
	return MemoryNamespace{
		TenantID:  "default",
		ProjectID: "default",
		AgentID:   "default",
	}
}
