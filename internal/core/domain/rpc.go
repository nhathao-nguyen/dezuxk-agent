package domain

import (
	"context"
	"fmt"
	"sync"
)

type targetHostContextKey struct{}

// WithTargetHost gắn TargetHost trực tiếp vào context để transport sử dụng
func WithTargetHost(ctx context.Context, host string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, targetHostContextKey{}, host)
}

// TargetHostFromContext lấy TargetHost từ context nếu có
func TargetHostFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if host, ok := ctx.Value(targetHostContextKey{}).(string); ok {
		return host
	}
	return ""
}

// RpcEndpoint định nghĩa một RPC hoặc API Endpoint động
type RpcEndpoint struct {
	ID          string      `json:"id" yaml:"id"`                     // Mã định danh (ví dụ: "nzlxg", "Zzl0ze", "StreamGenerate")
	TargetHost  string      `json:"target_host" yaml:"target_host"`   // "flow.google.com" hoặc "gemini.google.com"
	PathPattern string      `json:"path_pattern" yaml:"path_pattern"` // Path URL
	Service     ServiceKind `json:"service" yaml:"service"`
	RequiresAt  bool        `json:"requires_at" yaml:"requires_at"` // Cần CSRF token SNlM0e không
	Description string      `json:"description" yaml:"description"`
}

// RpcRegistry quản lý tập trung toàn bộ danh bạ RPC
type RpcRegistry struct {
	mu   sync.RWMutex
	rpcs map[string]RpcEndpoint
}

func NewRpcRegistry(initial []RpcEndpoint) *RpcRegistry {
	r := &RpcRegistry{
		rpcs: make(map[string]RpcEndpoint),
	}
	for _, ep := range initial {
		r.rpcs[ep.ID] = ep
	}
	return r
}

func (r *RpcRegistry) Get(id string) (RpcEndpoint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ep, ok := r.rpcs[id]
	return ep, ok
}

func (r *RpcRegistry) MustFind(id string) (*RpcEndpoint, error) {
	ep, ok := r.Get(id)
	if !ok {
		return nil, fmt.Errorf("rpc endpoint %q not found in registry", id)
	}
	return &ep, nil
}

// Override ghi đè hoặc bổ sung một RPC Endpoint vào Registry từ cấu hình động
func (r *RpcRegistry) Override(id string, pathPattern, targetHost string, requiresAt *bool, description string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ep, ok := r.rpcs[id]
	if !ok {
		reqAt := true
		if requiresAt != nil {
			reqAt = *requiresAt
		}
		r.rpcs[id] = RpcEndpoint{
			ID:          id,
			TargetHost:  targetHost,
			PathPattern: pathPattern,
			RequiresAt:  reqAt,
			Description: description,
		}
		return
	}
	if pathPattern != "" {
		ep.PathPattern = pathPattern
	}
	if targetHost != "" {
		ep.TargetHost = targetHost
	}
	if requiresAt != nil {
		ep.RequiresAt = *requiresAt
	}
	if description != "" {
		ep.Description = description
	}
	r.rpcs[id] = ep
}

// All trả về bản sao danh sách toàn bộ các RPC đang có trong Registry
func (r *RpcRegistry) All() map[string]RpcEndpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make(map[string]RpcEndpoint, len(r.rpcs))
	for k, v := range r.rpcs {
		res[k] = v
	}
	return res
}

// DefaultRpcRegistry trả về danh bạ toàn bộ các RPC chính thức đã xác thực trong docs-2
func DefaultRpcRegistry() *RpcRegistry {
	return NewRpcRegistry([]RpcEndpoint{
		// Google Gemini Endpoints
		{
			ID:          "StreamGenerate",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Gửi prompt trò chuyện và nhận luồng dữ liệu streaming thời gian thực",
		},
		{
			ID:          "batchexecute_gemini",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Cổng thực thi lệnh hàng loạt của Gemini",
		},
		{
			ID:          "usage",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/usage",
			Service:     ServiceGemini,
			RequiresAt:  false,
			Description: "Tra cứu hạn ngạch điện toán 5 giờ và hàng tuần",
		},
		{
			ID:          "I4z33b",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=I4z33b",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Tra cứu gói thuê bao và cấp độ tài khoản",
		},
		{
			ID:          "MaZiqc",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=MaZiqc",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Truy xuất danh sách các cuộc trò chuyện",
		},
		{
			ID:          "cZOhpc",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=cZOhpc",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Đọc chi tiết toàn bộ tin nhắn trong cuộc trò chuyện",
		},
		{
			ID:          "PCck7e",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=PCck7e",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Đổi tên tiêu đề cuộc trò chuyện",
		},
		{
			ID:          "VxUbXb",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=VxUbXb",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Xóa cuộc trò chuyện",
		},
		{
			ID:          "uP80Sb",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=uP80Sb",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Gửi đánh giá Thumbs Up / Down phản hồi trợ lý",
		},
		{
			ID:          "wEb32b",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=wEb32b",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Chuyển đổi con trỏ nhánh rẽ DAG hội thoại đang hoạt động",
		},
		{
			ID:          "tVk3Sc",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=tVk3Sc",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Khởi tạo tài liệu Canvas Artifacts mới",
		},
		{
			ID:          "sA4a8",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=sA4a8",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Cập nhật diff delta tài liệu Canvas Artifacts",
		},
		{
			ID:          "H8s0fe",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=H8s0fe",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Xuất bản và tạo liên kết chia sẻ công khai Canvas Artifacts",
		},
		{
			ID:          "whPPme",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=whPPme",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Tổng hợp giọng đọc Text-to-Speech audio WAV",
		},
		{
			ID:          "I4z33b",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=I4z33b",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Tra cứu cấp độ bản quyền tài khoản và context window",
		},
		{
			ID:          "GPRiHf",
			TargetHost:  "https://gemini.google.com",
			PathPattern: "/_/BardChatUi/data/batchexecute?rpcids=GPRiHf",
			Service:     ServiceGemini,
			RequiresAt:  true,
			Description: "Xóa toàn bộ lịch sử cuộc trò chuyện của tài khoản",
		},
		{
			ID:          "upload_handshake",
			TargetHost:  "https://push.clients6.google.com",
			PathPattern: "/upload/",
			Service:     ServiceGemini,
			RequiresAt:  false,
			Description: "Khởi tạo phiên tải lên tệp đa phương thức Resumable SCOTTY",
		},
	})
}
