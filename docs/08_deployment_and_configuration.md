# Module 08: Cấu Hình, Biên Dịch & Vận Hành Hệ Thống (Deployment & Configuration)

> Tài liệu đặc tả cấu trúc tệp cấu hình `config.yaml`, quy trình biên dịch nhị phân độc lập trên Windows (Wails Desktop `.exe` & Headless CLI Daemon) và tối ưu hóa hiệu năng mạng.

---

## 1. Lược Đồ Cấu Hình Hệ Thống (`config.yaml`)

File cấu hình được đặt cùng cấp với file nhị phân thực thi:

```yaml
# Cấu hình Cổng Mạng Gateway
server:
  host: "127.0.0.1"          # Đổi thành "0.0.0.0" nếu muốn chia sẻ qua mạng nội bộ LAN
  port: 8080                 # Cổng tiếp nhận chuẩn OpenAI API
  api_key: "sk-dezuxk-local"  # Mật khẩu xác thực Client (nếu để trống: không yêu cầu auth)
  cors_allowed_origins:
    - "*"
  read_timeout_seconds: 300   # 5 phút (phù hợp với các lượt render video Veo dài)
  write_timeout_seconds: 300

# Quản Lý Bộ Nhớ Đệm Media Cục Bộ
media:
  storage_dir: "./storage/media"
  base_url: "http://localhost:8080"
  max_disk_gigabytes: 15     # Tự động dọn dẹp khi vượt quá 15GB
  retention_days: 7          # Xóa các tệp tải về quá 7 ngày

# Hồ Tài Khoản Google (Multi-Account Pool)
accounts:
  - id: "acc_primary"
    email: "user@gmail.com"
    user_agent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36"
    cookies:
      OSID: "YOUR_FLOW_OSID_COOKIE"
      __Secure-OSID: "YOUR_FLOW_SECURE_OSID_COOKIE"
      __Secure-1PSID: "YOUR_GOOGLE_1PSID_COOKIE"
      __Secure-1PSIDTS: "YOUR_GOOGLE_1PSIDTS_COOKIE"
      __Secure-1PSIDCC: "YOUR_GOOGLE_1PSIDCC_COOKIE"
      SIDCC: "YOUR_GOOGLE_SIDCC_COOKIE"
      COMPASS: "YOUR_COMPASS_COOKIE"

# Cấu Hình Kết Nối Lên Máy Chủ Google (Upstream)
upstream:
  flow_origin: "https://flow.google.com"
  gemini_origin: "https://gemini.google.com"
  handshake_interval_hours: 6 # Định kỳ 6 giờ làm mới SNlM0e một lần
  proxy_url: ""               # Tùy chọn proxy (http://ip:port hoặc socks5://...) nếu cần đổi IP
```

---

## 2. Quy Trình Biên Dịch Ứng Dụng (Build & Compilation)

### 2.1. Yêu Cầu Môi Trường (Prerequisites)
1. **Golang:** Phiên bản `>= 1.22`.
2. **Node.js:** Phiên bản `>= 18.x` (để build frontend Wails).
3. **Wails v2 CLI:** Cài đặt bằng lệnh:
   ```bash
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```
4. **WebView2:** Đã tích hợp sẵn trên Windows 10/11.

---

### 2.2. Biên Dịch Chế Độ Desktop GUI (Wails Windows App)

1. **Chế độ phát triển (Live Development / Hot Reload):**
   ```bash
   wails dev
   ```
   * Tự động khởi động Go server và frontend Vite/Webview với tính năng Live Reload khi sửa code.

2. **Biên dịch bản thương mại độc lập (Production Build):**
   ```bash
   wails build -platform windows/amd64 -clean -upx=false
   ```
   * File đầu ra: `build/bin/dezuxk-gateway.exe`
   * Đặc điểm: File chạy duy nhất (Single Portable Executable), nhúng toàn bộ HTML/CSS/JS bên trong, không cần cài đặt thêm runtime.

---

### 2.3. Biên Dịch Chế Độ Headless CLI Daemon (Không Cần Giao Diện)

Dành cho kịch bản chạy trên máy chủ VPS, Docker hoặc chạy ngầm làm Windows Service:

```bash
go build -ldflags="-s -w" -o dezuxk-daemon.exe ./cmd/daemon
```

* **Khởi chạy dòng lệnh:**
  ```bash
  # Chạy với file config mặc định
  ./dezuxk-daemon.exe

  # Hoặc chỉ định cổng và file config riêng biệt
  ./dezuxk-daemon.exe -config C:\configs\dezuxk.yaml -port 9090
  ```

---

## 3. Tối Ưu Hóa Mạng & Hiệu Năng Golang (`http.Transport`)

Để Gateway chịu tải cao và không bị nghẽn socket khi nhiều ứng dụng cùng gọi tới:

```go
package server

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// NewOptimizedTransport cấu hình connection pool tối ưu cho upstream Google
func NewOptimizedTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true, // Hỗ trợ HTTP/2 multiplexing khi gọi sang Google
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}
```

---

## 4. Xử Lý Sự Cố Thường Gặp (Troubleshooting Guide)

| Hiện Tượng / Báo Lỗi | Nguyên Nhân Gốc Rễ | Cách Khắc Phục Chuẩn |
| :--- | :--- | :--- |
| **`HTTP 401 Unauthorized`** | Cookie `OSID` chưa được nạp hoặc đã bị đăng xuất khỏi trình duyệt. | Đồng bộ lại cookie từ Chrome hoặc copy cặp `OSID` mới từ `flow.google.com`. |
| **`HTTP 403 Forbidden`** | Request bị chặn WAF do lệch `Origin`, `Referer` hoặc User-Agent. | Đảm bảo middleware tự động inject đủ bộ 6 Headers tiêu chuẩn mô tả trong Module 01. |
| **Windows Defender chặn Port** | Tường lửa Windows hỏi quyền truy cập mạng cục bộ. | Bấm "Allow access" cho `dezuxk-gateway.exe` trên Private Network. |
| **Video tải về bị hỏng (0 bytes)** | Pre-signed URL của Google đã quá hạn 24 giờ trước khi hoàn tất tải. | Kích hoạt Goroutine tải ngầm ngay trong lượt streaming đầu tiên (Module 06). |
| **Stream chữ bị đứt đoạn** | Client proxy (như Nginx) dồn buffer. | Thêm header `X-Accel-Buffering: no` vào response SSE. |
