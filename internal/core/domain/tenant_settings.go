package domain

import (
	"time"
)

// TenantRuntimeSettings chứa cấu hình động cho từng tenant trong thời gian chạy.
// Tuyệt đối không cho phép tenant ghi đè các bất biến an ninh (fencing, master key, DB driver).
type TenantRuntimeSettings struct {
	TenantID            string            `json:"tenant_id"`
	PreferredModel      string            `json:"preferred_model,omitempty"`
	ModelPolicy         string            `json:"model_policy,omitempty"` // "fast", "balanced", "best"
	StrictModel         bool              `json:"strict_model,omitempty"`
	Persona             string            `json:"persona,omitempty"`
	ProjectContext      string            `json:"project_context,omitempty"`
	AllowedCapabilities []ModelCapability `json:"allowed_capabilities,omitempty"`
	AllowedTools        []string          `json:"allowed_tools,omitempty"`
	Settings            map[string]any    `json:"settings,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at"`
}
