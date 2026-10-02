package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type ServiceKind string

const (
	ServiceGemini ServiceKind = "gemini"
	ServiceFlow   ServiceKind = "flow"
)

type ModelCapability string

const (
	CapChat  ModelCapability = "chat"
	CapImage ModelCapability = "image"
	CapVideo ModelCapability = "video"
	CapAudio ModelCapability = "audio"
)

// ModelDescriptor định nghĩa một mô hình động, chỉ được kích hoạt sau khi login thành công
type ModelDescriptor struct {
	ID                 string            `json:"id" yaml:"id"`
	DisplayName        string            `json:"display_name" yaml:"display_name"`
	TargetService      ServiceKind       `json:"target_service" yaml:"target_service"`
	Capabilities       []ModelCapability `json:"capabilities" yaml:"capabilities"`
	InternalBackendID  string            `json:"internal_backend_id" yaml:"internal_backend_id"`
	ModeID             string            `json:"mode_id" yaml:"mode_id"`                 // RPC L5adhe mode ID
	ModelTierCode      int               `json:"model_tier_code" yaml:"model_tier_code"` // 1: Flash, 3: Pro
	CreditCostPerUnit  int               `json:"credit_cost_per_unit" yaml:"credit_cost_per_unit"`
	SupportedDurations []int             `json:"supported_durations" yaml:"supported_durations"`
	SupportedAspects   []string          `json:"supported_aspects" yaml:"supported_aspects"`
	IsActive           bool              `json:"is_active" yaml:"is_active"`
}

// ModelRegistry quản lý danh mục mô hình trong thời gian chạy. Khi server khởi động, registry này rỗng hoàn toàn.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]ModelDescriptor
}

func NewModelRegistry(initial []ModelDescriptor) *ModelRegistry {
	r := &ModelRegistry{
		models: make(map[string]ModelDescriptor),
	}
	for _, m := range initial {
		r.models[m.ID] = m
	}
	return r
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

func (r *ModelRegistry) Register(m ModelDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[m.ID] = m
}

func (r *ModelRegistry) ActivateServiceModels(service ServiceKind, models []ModelDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range models {
		if m.TargetService == service {
			m.IsActive = true
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

// ResolveGeminiModel phân giải linh hoạt tên mô hình đầu vào thành ModelDescriptor Gemini khả dụng.
// Áp dụng triết lý Flash-First:
// 1. Tìm chính xác theo ID (hoặc nếu là ServiceGemini).
// 2. Nếu tên chứa "pro", thử chuyển sang "gemini-3.1-pro".
// 3. Mọi alias/tên khác hoặc rỗng -> chuyển sang "gemini-3.8-flash".
// 4. Nếu không có, chọn mô hình Gemini bất kỳ đang active trong registry.
func (r *ModelRegistry) ResolveGeminiModel(name string) (ModelDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Tìm chính xác
	if m, ok := r.models[name]; ok && m.TargetService == ServiceGemini {
		return m, true
	}

	modelInput := strings.ToLower(strings.TrimSpace(name))

	// 2. Chứa "pro" -> gemini-3.1-pro
	if strings.Contains(modelInput, "pro") {
		if m, ok := r.models["gemini-3.1-pro"]; ok && m.TargetService == ServiceGemini {
			return m, true
		}
	}

	// 3. Mặc định Flash-First -> gemini-3.8-flash
	if m, ok := r.models["gemini-3.8-flash"]; ok && m.TargetService == ServiceGemini {
		return m, true
	}

	// 4. Bất kỳ Gemini nào có trong registry
	for _, m := range r.models {
		if m.TargetService == ServiceGemini {
			return m, true
		}
	}

	return ModelDescriptor{}, false
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
