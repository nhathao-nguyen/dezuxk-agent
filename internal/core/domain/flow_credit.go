package domain

import (
	"sync"
)

// FlowCreditBalance là số dư Flow đã đọc. Các field khác của payload gốc không có mặt ở đây.
type FlowCreditBalance struct {
	Amount         int
	UnmappedFields int
	SpecVersion    string
}

// Danh mục định danh tác vụ để tra cứu chi phí tín dụng động
const (
	CreditOpVeoQuality    = "veo_quality"
	CreditOpVeoFast       = "veo_fast"
	CreditOpVeoLite       = "veo_lite"
	CreditOpAbraImagen    = "abra_imagen"
	CreditOpUpsample4K    = "upsample_4k"
	CreditOpVideoExtend4s = "video_extend_4s"
	CreditOpVideoExtend6s = "video_extend_6s"
	CreditOpMusicFX       = "musicfx"
)

// FlowCreditCostRegistry quản lý bảng giá credit động theo cấu hình hạ tầng.
// Hoàn toàn thread-safe, không hardcode giá trị cố định vào mã nguồn logic.
type FlowCreditCostRegistry struct {
	mu    sync.RWMutex
	costs map[string]int
}

// NewFlowCreditCostRegistry khởi tạo registry chi phí tín dụng với các giá trị tùy chọn nạp từ config.
// Trường hợp không cấu hình hoặc thiếu key, sẽ sử dụng giá sàn linh hoạt ban đầu.
func NewFlowCreditCostRegistry(customCosts map[string]int) *FlowCreditCostRegistry {
	base := map[string]int{
		CreditOpVeoQuality:    100,
		CreditOpVeoFast:       20,
		CreditOpVeoLite:       10,
		CreditOpAbraImagen:    4,
		CreditOpUpsample4K:    50,
		CreditOpVideoExtend4s: 20,
		CreditOpVideoExtend6s: 30,
		CreditOpMusicFX:       5,
	}
	for k, v := range customCosts {
		if v >= 0 {
			base[k] = v
		}
	}
	return &FlowCreditCostRegistry{
		costs: base,
	}
}

// GetCost tra cứu chi phí của một tác vụ, trả về fallback nếu không tìm thấy key.
func (r *FlowCreditCostRegistry) GetCost(operation string, fallback int) int {
	if r == nil {
		return fallback
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if val, ok := r.costs[operation]; ok {
		return val
	}
	return fallback
}

// SetCost cập nhật chi phí của một tác vụ trong runtime.
func (r *FlowCreditCostRegistry) SetCost(operation string, cost int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.costs == nil {
		r.costs = make(map[string]int)
	}
	r.costs[operation] = cost
}

// AllCosts trả về bản sao toàn bộ bảng giá credit hiện tại.
func (r *FlowCreditCostRegistry) AllCosts() map[string]int {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make(map[string]int, len(r.costs))
	for k, v := range r.costs {
		res[k] = v
	}
	return res
}
