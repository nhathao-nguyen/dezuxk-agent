package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ServiceKind string

const (
	ServiceGemini ServiceKind = "gemini"
	ServiceFlow   ServiceKind = "flow"
)

type ModelCapability string

const (
	CapChat     ModelCapability = "chat"
	CapImage    ModelCapability = "image"
	CapVideo    ModelCapability = "video"
	CapAudio    ModelCapability = "audio"
	CapThinking ModelCapability = "thinking"
	CapCode     ModelCapability = "code"
	CapToolUse  ModelCapability = "tool_use"
)

type ModelPreference string

const (
	PrefFast           ModelPreference = "fast"
	PrefLowestLatency  ModelPreference = "lowest_latency"
	PrefBalanced       ModelPreference = "balanced"
	PrefBest           ModelPreference = "best"
	PrefHighestQuality ModelPreference = "highest_quality"
)

// ModelRequirement xác định tiêu chí yêu cầu khi tự động lựa chọn mô hình
type ModelRequirement struct {
	Capability  ModelCapability `json:"capability"`
	Preference  ModelPreference `json:"preference"`
	ExactModel  string          `json:"exact_model,omitempty"`
	TenantID    string          `json:"tenant_id,omitempty"`
	StrictModel bool            `json:"strict_model,omitempty"`
}

// ModelAliasRule định nghĩa quy tắc ánh xạ alias/nhãn mô hình
type ModelAliasRule struct {
	TargetModel string          `json:"target_model,omitempty" yaml:"target_model,omitempty"`
	Capability  ModelCapability `json:"capability,omitempty" yaml:"capability,omitempty"`
	Preference  ModelPreference `json:"preference,omitempty" yaml:"preference,omitempty"`
}

// ModelSelector là giao diện lựa chọn mô hình tương thích dựa trên yêu cầu
type ModelSelector interface {
	SelectDefault(ctx context.Context, requirement ModelRequirement) (ModelDescriptor, error)
}

// ModelDescriptor định nghĩa một mô hình động trong hệ thống
type ModelDescriptor struct {
	ID                 string            `json:"id" yaml:"id"`
	DisplayName        string            `json:"display_name" yaml:"display_name"`
	TargetService      ServiceKind       `json:"target_service" yaml:"target_service"`
	Capabilities       []ModelCapability `json:"capabilities" yaml:"capabilities"`
	InternalBackendID  string            `json:"internal_backend_id" yaml:"internal_backend_id"`
	UpstreamModelID    string            `json:"upstream_model_id,omitempty" yaml:"upstream_model_id,omitempty"`
	ModeID             string            `json:"mode_id" yaml:"mode_id"`                 // RPC L5adhe mode ID
	ModelTierCode      int               `json:"model_tier_code" yaml:"model_tier_code"` // 1: Flash/Free, 2/3: Pro/Ultra
	CreditCostPerUnit  int               `json:"credit_cost_per_unit" yaml:"credit_cost_per_unit"`
	SupportedDurations []int             `json:"supported_durations,omitempty" yaml:"supported_durations,omitempty"`
	SupportedAspects   []string          `json:"supported_aspects,omitempty" yaml:"supported_aspects,omitempty"`
	IsActive           bool              `json:"is_active" yaml:"is_active"`
	Source             string            `json:"source,omitempty" yaml:"source,omitempty"` // "upstream_discovery", "seed", "admin"
	FirstSeenAt        time.Time         `json:"first_seen_at,omitempty" yaml:"first_seen_at,omitempty"`
	LastSeenAt         time.Time         `json:"last_seen_at,omitempty" yaml:"last_seen_at,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	Metadata           map[string]any    `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// CanonicalModelID chuẩn hóa chuỗi mô hình đầu vào thành canonical gateway ID duy nhất, ổn định
func CanonicalModelID(rawID string, service ServiceKind) string {
	s := strings.TrimSpace(rawID)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)

	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '_' || c == '/' || c == ':' {
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "-") {
				sb.WriteByte('-')
			}
		} else if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-' {
			sb.WriteByte(c)
		}
	}
	res := strings.Trim(sb.String(), "-")

	prefix := string(service) + "-"
	if service != "" && !strings.HasPrefix(res, prefix) {
		res = prefix + res
	}
	return res
}

// ModelRegistry quản lý danh mục mô hình trong bộ nhớ đệm thời gian chạy (in-memory cache).
// PostgreSQL và upstream discovery là nguồn chân lý bền vững (source of truth).
type ModelRegistry struct {
	mu                 sync.RWMutex
	models             map[string]ModelDescriptor
	aliases            map[string]ModelAliasRule
	capabilityRegistry *ModelCapabilityRegistry
}

func DefaultModelAliases() map[string]ModelAliasRule {
	return map[string]ModelAliasRule{
		"fast":     {Capability: CapChat, Preference: PrefFast},
		"flash":    {Capability: CapChat, Preference: PrefFast},
		"pro":      {Capability: CapChat, Preference: PrefBest},
		"best":     {Capability: CapChat, Preference: PrefBest},
		"thinking": {Capability: CapThinking, Preference: PrefBest},
		"default":  {Capability: CapChat, Preference: PrefBalanced},
	}
}

func NewModelRegistry(initial []ModelDescriptor) *ModelRegistry {
	r := &ModelRegistry{
		models:             make(map[string]ModelDescriptor),
		aliases:            DefaultModelAliases(),
		capabilityRegistry: NewModelCapabilityRegistry(),
	}
	now := time.Now()
	for _, m := range initial {
		if m.FirstSeenAt.IsZero() {
			m.FirstSeenAt = now
		}
		if m.LastSeenAt.IsZero() {
			m.LastSeenAt = now
		}
		if m.UpdatedAt.IsZero() {
			m.UpdatedAt = now
		}
		r.models[m.ID] = m
	}
	return r
}

func (r *ModelRegistry) CapabilityRegistry() *ModelCapabilityRegistry {
	if r == nil {
		return NewModelCapabilityRegistry()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.capabilityRegistry == nil {
		return NewModelCapabilityRegistry()
	}
	return r.capabilityRegistry
}

func (r *ModelRegistry) GetCapabilities(modelID string) ModelCapabilities {
	if r == nil {
		return DefaultFallbackCapabilities(modelID, nil)
	}
	r.mu.RLock()
	capReg := r.capabilityRegistry
	desc, ok := r.models[modelID]
	r.mu.RUnlock()

	var descPtr *ModelDescriptor
	if ok {
		descPtr = &desc
	}
	if capReg == nil {
		return DefaultFallbackCapabilities(modelID, descPtr)
	}
	return capReg.Resolve(modelID, descPtr)
}

func (r *ModelRegistry) SetAliases(aliases map[string]ModelAliasRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range aliases {
		r.aliases[strings.ToLower(strings.TrimSpace(k))] = v
	}
}

func (r *ModelRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.models)
}

func (r *ModelRegistry) Get(id string) (ModelDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

func (r *ModelRegistry) List() []ModelDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]ModelDescriptor, 0, len(r.models))
	for _, m := range r.models {
		if m.IsActive {
			list = append(list, m)
		}
	}
	return list
}

func (r *ModelRegistry) ListActive() []ModelDescriptor {
	return r.List()
}

func (r *ModelRegistry) ListAll() []ModelDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]ModelDescriptor, 0, len(r.models))
	for _, m := range r.models {
		list = append(list, m)
	}
	return list
}

func (r *ModelRegistry) Register(m ModelDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if m.FirstSeenAt.IsZero() {
		m.FirstSeenAt = now
	}
	m.LastSeenAt = now
	m.UpdatedAt = now
	r.models[m.ID] = m
}

func (r *ModelRegistry) Upsert(m ModelDescriptor) {
	r.Register(m)
}

func (r *ModelRegistry) ReplaceAll(models []ModelDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	newMap := make(map[string]ModelDescriptor, len(models))
	for _, m := range models {
		newMap[m.ID] = m
	}
	r.models = newMap
}

func (r *ModelRegistry) ActivateServiceModels(service ServiceKind, models []ModelDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, m := range models {
		if m.TargetService == service {
			m.IsActive = true
			if m.FirstSeenAt.IsZero() {
				m.FirstSeenAt = now
			}
			m.LastSeenAt = now
			m.UpdatedAt = now
			r.models[m.ID] = m
		}
	}
}

func (r *ModelRegistry) DeactivateServiceModels(service ServiceKind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, m := range r.models {
		if m.TargetService == service {
			delete(r.models, k)
		}
	}
}

func (r *ModelRegistry) MustFind(id string) (*ModelDescriptor, error) {
	m, ok := r.Get(id)
	if !ok {
		return nil, fmt.Errorf("mô hình %q chưa khả dụng hoặc chưa có tài khoản Google nào đăng nhập dịch vụ này", id)
	}
	return &m, nil
}

// HasCapability kiểm tra xem mô hình có hỗ trợ năng lực chỉ định không
func (m *ModelDescriptor) HasCapability(cap ModelCapability) bool {
	if cap == "" {
		return true
	}
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// CapabilitiesProfile trả về ModelCapabilities phân giải từ ModelDescriptor
func (m *ModelDescriptor) CapabilitiesProfile() ModelCapabilities {
	if m == nil {
		return DefaultFallbackCapabilities("", nil)
	}
	return GetGlobalCapabilityRegistry().Resolve(m.ID, m)
}

// ResolveGeminiModel phân giải tên mô hình cho dịch vụ Gemini
func (r *ModelRegistry) ResolveGeminiModel(name string) (ModelDescriptor, bool) {
	return r.ResolveModelForService(name, ServiceGemini, ModelRequirement{
		Capability: CapChat,
		Preference: PrefBalanced,
	})
}

// ResolveModel phân giải tên mô hình động không giới hạn dịch vụ
func (r *ModelRegistry) ResolveModel(name string, req ...ModelRequirement) (ModelDescriptor, bool) {
	return r.ResolveModelForService(name, "", req...)
}

// ResolveModelForService phân giải tên mô hình động theo thứ bậc chuẩn và phân lập theo dịch vụ:
// 1. Tìm chính xác theo Canonical ID hoặc ID trong active models
// 2. Tìm theo Alias cấu hình
// 3. Tìm theo tiêu chuẩn Dynamic Default Selector (Capability + Preference)
func (r *ModelRegistry) ResolveModelForService(name string, service ServiceKind, req ...ModelRequirement) (ModelDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cleaned := strings.TrimSpace(name)
	lower := strings.ToLower(cleaned)

	var requirement ModelRequirement
	if len(req) > 0 {
		requirement = req[0]
	}

	matchesService := func(m ModelDescriptor) bool {
		if service == "" {
			return true
		}
		return m.TargetService == service
	}

	// 1. Tìm chính xác theo ID trực tiếp nếu có chỉ định name
	if cleaned != "" {
		if m, ok := r.models[cleaned]; ok && m.IsActive && matchesService(m) {
			return m, true
		}
		// Thử tìm theo canonical ID nếu là ServiceGemini
		if service == ServiceGemini || service == "" {
			canonical := CanonicalModelID(cleaned, ServiceGemini)
			if m, ok := r.models[canonical]; ok && m.IsActive && matchesService(m) {
				return m, true
			}
		}
		// Thử tìm theo DisplayName hoặc InternalBackendID
		for _, m := range r.models {
			if m.IsActive && matchesService(m) {
				if strings.EqualFold(m.DisplayName, cleaned) || strings.EqualFold(m.InternalBackendID, cleaned) {
					return m, true
				}
			}
		}

		// 2. Ánh xạ Alias cấu hình nếu có
		var matchedRule *ModelAliasRule
		if rule, ok := r.aliases[lower]; ok {
			matchedRule = &rule
		} else {
			for aliasKey, rule := range r.aliases {
				if strings.Contains(lower, aliasKey) {
					ruleCopy := rule
					matchedRule = &ruleCopy
					break
				}
			}
		}

		if matchedRule != nil {
			if matchedRule.TargetModel != "" {
				if m, found := r.models[matchedRule.TargetModel]; found && m.IsActive && matchesService(m) {
					return m, true
				}
			}
			if matchedRule.Capability != "" {
				requirement.Capability = matchedRule.Capability
			}
			if matchedRule.Preference != "" {
				requirement.Preference = matchedRule.Preference
			}
		} else if strings.HasPrefix(lower, "cursor") || lower == "auto" || lower == "default" {
			// Nhận diện client alias chung (Cursor/Cline) mà không định danh model cụ thể
			// Tiếp tục sang Dynamic Default Selector
		} else {
			// Model name được chỉ định cụ thể nhưng không tồn tại trong catalog hay alias
			return ModelDescriptor{}, false
		}
	}

	// 3. Dynamic Default Model Selector dựa trên năng lực và tiêu chí ưu tiên
	if requirement.Capability == "" {
		requirement.Capability = CapChat
	}

	var candidates []ModelDescriptor
	for _, m := range r.models {
		if m.IsActive && matchesService(m) && m.HasCapability(requirement.Capability) {
			candidates = append(candidates, m)
		}
	}

	if len(candidates) == 0 {
		return ModelDescriptor{}, false
	}

	// Lọc và xếp hạng ứng viên theo Preference một cách tất định (deterministic)
	score := func(m ModelDescriptor) int {
		s := 0
		idLower := strings.ToLower(m.ID)
		nameLower := strings.ToLower(m.DisplayName)
		isLite := strings.Contains(idLower, "lite") || strings.Contains(nameLower, "lite")
		isDefaultName := strings.Contains(nameLower, "default")
		hasThinking := m.HasCapability(CapThinking)

		switch requirement.Preference {
		case PrefFast, PrefLowestLatency:
			// Ưu tiên độ trễ thấp nhất: lite trước, tier thấp trước, tránh thinking
			s = 100 - m.ModelTierCode*20
			if isLite {
				s += 50
			}
			if hasThinking && requirement.Capability != CapThinking {
				s -= 60
			}
		case PrefBest, PrefHighestQuality:
			// Ưu tiên chất lượng cao nhất: tier cao nhất (Pro/Ultra), tránh lite
			s = m.ModelTierCode * 50
			if requirement.Capability == CapThinking {
				if hasThinking {
					s += 40
				}
			} else {
				if hasThinking {
					s -= 20 // Không yêu cầu thinking thì ưu tiên bản Pro chuẩn
				}
			}
			if isLite {
				s -= 50
			}
		default: // Balanced / Default
			// Cân bằng chuẩn: tier 1 chuẩn (Flash), không phải lite, không phải thinking trừ khi yêu cầu
			if m.ModelTierCode == 1 {
				s += 100
			} else if m.ModelTierCode == 2 {
				s += 60
			} else if m.ModelTierCode == 3 {
				s += 30
			}
			if isLite {
				s -= 40
			}
			if hasThinking && requirement.Capability != CapThinking {
				s -= 30
			}
			if isDefaultName {
				s += 20
			}
		}
		return s
	}

	best := candidates[0]
	bestScore := score(best)
	for _, c := range candidates[1:] {
		cScore := score(c)
		if cScore > bestScore {
			best = c
			bestScore = cScore
		} else if cScore == bestScore {
			// Tie-breaker tất định dựa trên ID để tránh phụ thuộc ngẫu nhiên vào thứ tự map Go
			if c.ID < best.ID {
				best = c
				bestScore = cScore
			}
		}
	}
	return best, true
}

// SelectDefault triển khai giao diện ModelSelector trên ModelRegistry
func (r *ModelRegistry) SelectDefault(ctx context.Context, requirement ModelRequirement) (ModelDescriptor, error) {
	desc, ok := r.ResolveModel("", requirement)
	if !ok {
		return ModelDescriptor{}, fmt.Errorf("không tìm thấy mô hình mặc định nào đáp ứng yêu cầu (capability=%s, preference=%s)", requirement.Capability, requirement.Preference)
	}
	return desc, nil
}

// UpdateBackendStatus cập nhật trạng thái IsActive của mô hình theo kết quả trả về từ yBhWQ
func (r *ModelRegistry) UpdateBackendStatus(service ServiceKind, backendStatus map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, m := range r.models {
		if m.TargetService == service {
			if active, ok := backendStatus[m.InternalBackendID]; ok {
				m.IsActive = active
				r.models[k] = m
			}
		}
	}
}

// ParseActiveModelsResponse phân tích phản hồi RPC yBhWQ (danh sách mô hình GPU online)
func ParseActiveModelsResponse(raw string, metrics *ContractMetrics) (map[string]bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return map[string]bool{}, nil
	}
	var outer []any
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("yBhWQ", ServiceGemini, "phản hồi yBhWQ không đúng hợp đồng")
	}

	result := make(map[string]bool)
	var scanList func(node any)
	scanList = func(node any) {
		arr, ok := node.([]any)
		if !ok {
			return
		}
		if len(arr) >= 2 {
			if idStr, ok1 := arr[0].(string); ok1 && idStr != "" {
				if statusNum, ok2 := arr[1].(float64); ok2 {
					result[idStr] = (statusNum == 1)
					return
				}
			}
		}
		for _, child := range arr {
			scanList(child)
		}
	}
	scanList(outer)
	return result, nil
}
