package domain

import (
	"context"
	"errors"
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

// BillingIdentity đại diện cho thông tin tính cước và hạn ngạch của tác vụ Agent chạy nền
type BillingIdentity struct {
	TenantID string `json:"tenant_id"`
	KeyID    string `json:"key_id"`
}

type billingIdentityContextKey struct{}

// ContextWithBillingIdentity gắn BillingIdentity vào context
func ContextWithBillingIdentity(ctx context.Context, b BillingIdentity) context.Context {
	return context.WithValue(ctx, billingIdentityContextKey{}, b)
}

// BillingIdentityFromContext trích xuất BillingIdentity từ context
func BillingIdentityFromContext(ctx context.Context) (BillingIdentity, bool) {
	if ctx == nil {
		return BillingIdentity{}, false
	}
	if b, ok := ctx.Value(billingIdentityContextKey{}).(BillingIdentity); ok {
		return b, true
	}
	return BillingIdentity{}, false
}

// IntersectSecurityContext tính toán chính sách phân quyền an toàn khi resume một run.
// Quy tắc bắt buộc: new permissions <= old permissions AND new permissions <= current caller permissions.
// Tuyệt đối không bao giờ thực hiện phép UNION quyền (đặc quyền thừa kế).
func IntersectSecurityContext(old *AgentSecurityContext, current TenantIdentity) (*AgentSecurityContext, error) {
	if old == nil {
		return nil, errors.New("legacy_security_context_missing: tác vụ cũ thiếu SecurityContext hợp lệ để đối soát an toàn")
	}

	// 1. Kiểm tra tính tương thích TenantID
	tenantID := old.TenantID
	if current.Role != "admin" && current.TenantID != "" && current.TenantID != old.TenantID {
		return nil, errors.New("tenant_mismatch: người gọi không có quyền truy cập tenant của tác vụ gốc")
	}

	// 2. Vai trò hiệu lực: chỉ thành admin nếu cả cũ và hiện tại đều là admin
	effectiveRole := "user"
	if old.Role == "admin" && current.Role == "admin" {
		effectiveRole = "admin"
	}

	// 3. Quyền Shell: chỉ cho phép nếu CẢ HAI bên cùng cho phép
	allowShell := old.AllowShell && current.AllowShell

	// 4. RequireApproval: chọn phương án an toàn/chặt chẽ hơn (chỉ cần 1 bên yêu cầu phê duyệt thì phải phê duyệt)
	requireApproval := old.RequireApproval || current.RequireApproval

	// 5. EnforceSandbox: chọn phương án chặt chẽ hơn (bắt buộc sandbox nếu 1 trong 2 yêu cầu)
	enforceSandbox := old.EnforceSandbox || current.EnforceSandbox

	// 6. AutoMergeAllowed: chỉ cho phép nếu CẢ HAI bên cùng đồng ý
	autoMergeAllowed := old.AutoMergeAllowed && current.AutoMergeAllowed

	// 7. MaxAgentSteps: min(old, current)
	maxSteps := current.MaxAgentSteps
	if maxSteps <= 0 {
		maxSteps = 25
	}
	if old.MaxAgentSteps > 0 && old.MaxAgentSteps < maxSteps {
		maxSteps = old.MaxAgentSteps
	}

	// 8. MaxConcurrentRuns: min(old, current)
	maxConcurrent := current.MaxConcurrentRuns
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	if old.MaxConcurrentRuns > 0 && old.MaxConcurrentRuns < maxConcurrent {
		maxConcurrent = old.MaxConcurrentRuns
	}

	// 9. MaxToolRuntime: min(old, current)
	maxRuntime := current.MaxToolRuntime
	if maxRuntime <= 0 {
		maxRuntime = 60 * time.Second
	}
	if old.MaxToolRuntime > 0 && old.MaxToolRuntime < maxRuntime {
		maxRuntime = old.MaxToolRuntime
	}

	// 10. Giao các Scopes (Intersection)
	hasWildcardScope := func(scopes []string) bool {
		for _, s := range scopes {
			if strings.TrimSpace(s) == "*" {
				return true
			}
		}
		return false
	}

	var effectiveScopes []string
	if hasWildcardScope(old.Scopes) && hasWildcardScope(current.Scopes) {
		effectiveScopes = []string{"*"}
	} else if hasWildcardScope(old.Scopes) {
		effectiveScopes = append([]string(nil), current.Scopes...)
	} else if hasWildcardScope(current.Scopes) {
		effectiveScopes = append([]string(nil), old.Scopes...)
	} else {
		currScopeMap := make(map[string]bool)
		for _, s := range current.Scopes {
			currScopeMap[strings.ToLower(strings.TrimSpace(s))] = true
		}
		for _, s := range old.Scopes {
			norm := strings.ToLower(strings.TrimSpace(s))
			if currScopeMap[norm] {
				effectiveScopes = append(effectiveScopes, norm)
			}
		}
	}

	// Kiểm tra bắt buộc phải còn quyền ScopeAgent sau khi intersect
	hasAgentScope := false
	for _, s := range effectiveScopes {
		if s == "*" || s == ScopeAgent {
			hasAgentScope = true
			break
		}
	}
	if !hasAgentScope && effectiveRole != "admin" {
		return nil, errors.New("resume_reauthorization_failed: người gọi hiện tại không có scope 'agent'")
	}

	// 11. Giao các AllowedTools
	hasWildcardTool := func(tools []string) bool {
		if len(tools) == 0 {
			return true
		}
		for _, t := range tools {
			if strings.TrimSpace(t) == "*" {
				return true
			}
		}
		return false
	}

	var effectiveTools []string
	if hasWildcardTool(old.AllowedTools) && hasWildcardTool(current.AllowedTools) {
		effectiveTools = nil // Cho phép các tool mặc định theo allowShell
	} else if hasWildcardTool(old.AllowedTools) {
		effectiveTools = append([]string(nil), current.AllowedTools...)
	} else if hasWildcardTool(current.AllowedTools) {
		effectiveTools = append([]string(nil), old.AllowedTools...)
	} else {
		currToolMap := make(map[string]bool)
		for _, t := range current.AllowedTools {
			currToolMap[strings.TrimSpace(t)] = true
		}
		for _, t := range old.AllowedTools {
			trim := strings.TrimSpace(t)
			if currToolMap[trim] {
				effectiveTools = append(effectiveTools, trim)
			}
		}
	}

	// Nếu không có quyền Shell, đảm bảo loại bỏ run_command
	if !allowShell && effectiveRole != "admin" {
		var filtered []string
		for _, t := range effectiveTools {
			if t != "run_command" && t != "*" {
				filtered = append(filtered, t)
			}
		}
		effectiveTools = filtered
	}

	// 12. Giao các AllowedModels
	hasWildcardModel := func(models []string) bool {
		if len(models) == 0 {
			return true
		}
		for _, m := range models {
			if strings.TrimSpace(m) == "*" {
				return true
			}
		}
		return false
	}

	var effectiveModels []string
	if hasWildcardModel(old.AllowedModels) && hasWildcardModel(current.AllowedModels) {
		effectiveModels = []string{"*"}
	} else if hasWildcardModel(old.AllowedModels) {
		effectiveModels = append([]string(nil), current.AllowedModels...)
	} else if hasWildcardModel(current.AllowedModels) {
		effectiveModels = append([]string(nil), old.AllowedModels...)
	} else {
		currModelMap := make(map[string]bool)
		for _, m := range current.AllowedModels {
			currModelMap[strings.ToLower(strings.TrimSpace(m))] = true
		}
		for _, m := range old.AllowedModels {
			norm := strings.ToLower(strings.TrimSpace(m))
			if currModelMap[norm] {
				effectiveModels = append(effectiveModels, norm)
			}
		}
	}

	// 13. Giao NetworkPolicy (Restrictive Intersection)
	allowOutbound := old.NetworkPolicy.AllowOutbound && current.NetworkPolicy.AllowOutbound

	// Allowed domains: intersection
	var allowedDomains []string
	if len(old.NetworkPolicy.AllowedDomains) == 0 {
		allowedDomains = append([]string(nil), current.NetworkPolicy.AllowedDomains...)
	} else if len(current.NetworkPolicy.AllowedDomains) == 0 {
		allowedDomains = append([]string(nil), old.NetworkPolicy.AllowedDomains...)
	} else {
		currDomainMap := make(map[string]bool)
		for _, d := range current.NetworkPolicy.AllowedDomains {
			currDomainMap[strings.ToLower(strings.TrimSpace(d))] = true
		}
		for _, d := range old.NetworkPolicy.AllowedDomains {
			norm := strings.ToLower(strings.TrimSpace(d))
			if currDomainMap[norm] {
				allowedDomains = append(allowedDomains, norm)
			}
		}
	}

	// Blocked domains: union
	blockedDomainMap := make(map[string]bool)
	var blockedDomains []string
	for _, d := range old.NetworkPolicy.BlockedDomains {
		norm := strings.ToLower(strings.TrimSpace(d))
		if norm != "" && !blockedDomainMap[norm] {
			blockedDomainMap[norm] = true
			blockedDomains = append(blockedDomains, norm)
		}
	}
	for _, d := range current.NetworkPolicy.BlockedDomains {
		norm := strings.ToLower(strings.TrimSpace(d))
		if norm != "" && !blockedDomainMap[norm] {
			blockedDomainMap[norm] = true
			blockedDomains = append(blockedDomains, norm)
		}
	}

	return &AgentSecurityContext{
		TenantID:              tenantID,
		KeyID:                 current.KeyID, // Gắn KeyID của người gọi hiện tại cho billing và attribution
		Role:                  effectiveRole,
		Scopes:                effectiveScopes,
		AllowedModels:         effectiveModels,
		AllowedTools:          effectiveTools,
		AllowedWorkspaceRoots: append([]string(nil), current.AllowedWorkspaceRoots...),
		MaxAgentSteps:         maxSteps,
		MaxConcurrentRuns:     maxConcurrent,
		MaxToolRuntime:        maxRuntime,
		RequireApproval:       requireApproval,
		AllowShell:            allowShell,
		EnforceSandbox:        enforceSandbox,
		AutoMergeAllowed:      autoMergeAllowed,
		NetworkPolicy: NetworkPolicy{
			AllowOutbound:  allowOutbound,
			AllowedDomains: allowedDomains,
			BlockedDomains: blockedDomains,
		},
	}, nil
}
