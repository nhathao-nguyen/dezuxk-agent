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

		// Google Flow Endpoints
		{
			ID:          "batchexecute_flow",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Cổng thực thi lệnh hàng loạt của Google Flow",
		},
		{
			ID:          "nzlxg",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=nzlxg",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Tra cứu số dư tín dụng Credit tổng quát và phân hạng tài khoản",
		},
		{
			ID:          "cPZSdc",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=cPZSdc",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Tra cứu gói cước và hạn ngạch tín dụng được tặng thêm mỗi ngày",
		},
		{
			ID:          "HTrJv",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=HTrJv",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Bóc tách ma trận mô hình phần cứng Veo 3.1 & Abra kèm biểu phí Credit",
		},
		{
			ID:          "yBhWQ",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=yBhWQ",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Danh sách các mô hình GPU đang hoạt động trực tuyến",
		},
		{
			ID:          "tRARke",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=tRARke",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Thư viện 68 quy trình mẫu (Workflow Templates)",
		},
		{
			ID:          "UpteDb",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=UpteDb",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Truy xuất danh sách và lịch sử các dự án người dùng",
		},
		{
			ID:          "jHPbke",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jHPbke",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Tạo dự án mới và cấp phát UUID duy nhất",
		},
		{
			ID:          "csbIsb",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=csbIsb",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Đăng ký khóa phiên tương tác tránh trừ âm credit",
		},
		{
			ID:          "ngNC2",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=ngNC2",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Lấy sơ đồ đồ thị PINHOLE và công cụ dự án",
		},
		{
			ID:          "Zzl0ze",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=Zzl0ze",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Thư viện 30 nhân vật lồng tiếng và file audio mẫu WAV",
		},
		{
			ID:          "kF8z7b",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=kF8z7b",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Thêm nút mới vào đồ thị PINHOLE Node Graph",
		},
		{
			ID:          "jE2m9c",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jE2m9c&rt=c",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Nối chân Pins liên kết giữa các nút trong đồ thị PINHOLE",
		},
		{
			ID:          "dL5p2",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=dL5p2&rt=c",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Xóa nút hoặc cạnh nối trong đồ thị PINHOLE",
		},
		{
			ID:          "dK3x9",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=dK3x9",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Chuyển dự án vào thùng rác (Soft Delete)",
		},
		{
			ID:          "tB6q8",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=tB6q8",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Lấy danh sách các dự án đang nằm trong thùng rác",
		},
		{
			ID:          "rS4y1",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=rS4y1&rt=c",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Khôi phục dự án khỏi thùng rác",
		},
		{
			ID:          "mrlkwd",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mrlkwd",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Xóa vĩnh viễn dự án khỏi hệ thống",
		},
		{
			ID:          "mX9w1",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mX9w1",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Tạo nhạc nền và âm thanh SFX đồng bộ qua hạ tầng MusicFX",
		},
		{
			ID:          "uW3g7e",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=uW3g7e",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Nâng cấp video Veo lên độ phân giải 4K Ultra HD",
		},
		{
			ID:          "StreamChat",
			TargetHost:  "https://flow.google.com",
			PathPattern: "/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat?rt=c",
			Service:     ServiceFlow,
			RequiresAt:  true,
			Description: "Tạo video Veo 3.1, mở rộng video, tạo ảnh Abra và điều khiển camera 3D",
		},
	})
}
