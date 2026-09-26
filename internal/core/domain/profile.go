package domain

import (
	"time"
)

// Profile đại diện cho một thư mục Chrome Profile độc lập của một tài khoản Google
type Profile struct {
	ID          string    `json:"id"`           // Tên định danh (tên thư mục con, ví dụ: "acc_thao", "work")
	Dir         string    `json:"dir"`          // Đường dẫn đầy đủ trên đĩa tới thư mục Chrome Profile
	Email       string    `json:"email"`        // Email tài khoản (phát hiện sau khi login)
	CDPPort     int       `json:"cdp_port"`     // Cổng Remote Debugging gán riêng cho Profile này
	IsLoggedIn  bool      `json:"is_logged_in"` // Đã đăng nhập Google thành công chưa
	HasGemini   bool      `json:"has_gemini"`   // Đã có cookie __Secure-1PSID và __Secure-1PSIDTS
	HasFlow     bool      `json:"has_flow"`     // Đã có cookie OSID và __Secure-OSID
	GeminiQuota string    `json:"gemini_quota"` // Hạn mức quota của Gemini (ví dụ: "100%")
	FlowCredits int       `json:"flow_credits"` // Số dư credit đọc từ Google Flow sau khi login
	Proxy       string    `json:"proxy,omitempty"` // Proxy gán riêng cho Profile này (http://... hoặc socks5://...)
	LastActive  time.Time `json:"last_active"`
}

// ServiceActivationEvent sự kiện kích hoạt các dịch vụ sau khi đăng nhập thành công
type ServiceActivationEvent struct {
	ProfileID string
	Email     string
	HasGemini bool
	HasFlow   bool
}
