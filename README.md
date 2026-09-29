# Dezuxk AI Gateway (Google Gemini $\rightarrow$ OpenAI API)

<p align="center">
  <b>Hệ thống AI Gateway hiệu năng cao, chuyển đổi Google Gemini Web thành chuẩn OpenAI API (`/v1/chat/completions`) tương thích 100% với NextChat, OpenWebUI, Cursor, Chatbox và Continue.dev.</b>
</p>

---

## 🌟 Tính Năng Nổi Bật

* **Tương thích chuẩn OpenAI API (`/v1`)**:
  * Hỗ trợ đầy đủ `/v1/chat/completions` (cả chế độ **Sync** và **Real-time SSE Streaming**).
  * Danh mục mô hình động `/v1/models` đồng bộ tự động theo quyền hạn tài khoản.
* **Các tính năng Gemini độc quyền**:
  * 🧠 **Extended Thinking Mode (Suy luận sâu)**: Bật cờ `"thinking": true` để Gemini suy nghĩ từng bước trước khi trả lời.
  * 🌐 **Search Grounding (Truy vấn Web thời gian thực)**: Bật cờ `"grounding": true` để Gemini tự động tìm kiếm Google Search và trả về trích dẫn nguồn (`sources`, `favicon`, `domain`).
  * 🐍 **Python Code Interpreter (Sandbox thực thi mã)**: Bật cờ `"code_interpreter": true` để Gemini tự động viết và chạy mã Python ngầm trong sandbox của Google.
  * 👁️ **Multimodal Vision (Phân tích hình ảnh)**: Tự động phân giải ảnh Base64 (`data:image/...`) hoặc Image URL trong `messages` và upload qua Google SCOTTY protocol.
* **Bộ nhớ đệm phản hồi siêu tốc (< 10ms)**:
  * Tích hợp **In-Memory LRU Caching + TTL**. Các câu hỏi lặp lại được phản hồi tức thì từ RAM, trả về header `X-Cache: HIT`.
* **Quản lý đa tài khoản & Khả năng chịu lỗi cao (Resilience & Failover)**:
  * **Tự động chuyển tài khoản (Next-Account Failover)**: Khi tài khoản hiện tại chạm ngưỡng giới hạn (429 Rate Limit hoặc Quota), hệ thống tự động đưa tài khoản vào hàng chờ Cooldown và chuyển ngay sang tài khoản khả dụng tiếp theo.
  * **Khóa chống nghẽn (Write-Lease)**: Bảo đảm mỗi tài khoản chỉ xử lý 1 tác vụ ghi tại một thời điểm, loại bỏ nguy cơ bị Google WAF đánh cờ vi phạm.
* **Trích xuất Cookie tự động qua Chrome CDP**:
  * Quản lý mỗi tài khoản Google trong một thư mục Profile Chrome riêng biệt (`profiles/`).
  * Tự động kết nối WebSocket CDP (`Network.getAllCookies`) để trích xuất cookie trực tiếp từ RAM trình duyệt mà không cần cài extension.
  * Tự động bắt tay lấy CSRF Token `SNlM0e` và tự xoay vòng token `__Secure-1PSIDTS`.
* **Bảo mật cấp doanh nghiệp**:
  * **Mã hóa đối xứng AES-256-GCM (Encryption at Rest)** cho toàn bộ Cookie và thông tin đăng nhập trong SQLite và đĩa cứng qua module Vault.
  * **Virtual API Keys đa người dùng**: Hỗ trợ phân quyền `admin` / `user`, giới hạn tốc độ (Rate Limit RPM) và hạn ngạch ngày (Daily Quota) cho từng client.

---

## 🚀 Hướng Dẫn Bắt Đầu Nhanh (3 Phút)

### Bước 1: Khởi động Server Gateway

Chỉ cần nhấp đúp vào file `run.bat` (trên Windows) hoặc chạy qua terminal:

```bash
# Cách 1: Dùng script có sẵn
.\run.ps1        # (PowerShell)
run.bat          # (Command Prompt)

# Cách 2: Chạy trực tiếp binary
./dezuxk.exe --config configs/config.yaml

# Cách 3: Chạy từ source code Golang
go run main.go --config configs/config.yaml
```

Khi server khởi động thành công, màn hình sẽ hiển thị:
```text
[Server] Dezuxk Gateway đang lắng nghe tại: http://127.0.0.1:8080
[Server] Admin Overview API: http://127.0.0.1:8080/v1/admin/overview
[Server] OpenAI API Base: http://127.0.0.1:8080/v1
```

---

### Bước 2: Thiết lập Tài khoản Google (1-Click Onboarding)

Mở một cửa sổ terminal mới và chạy công cụ thiết lập tự động:

```powershell
# Chạy script thiết lập 1-click
powershell -ExecutionPolicy Bypass -File scripts/setup_profile.ps1

# Hoặc nhấp đúp vào:
scripts\setup_profile.bat
```

Quy trình tự động hóa sẽ diễn ra như sau:
1. Nhập tên Profile đại diện (ví dụ: `my_account`).
2. Script sẽ tự động mở một cửa sổ Chrome riêng biệt.
3. Bạn đăng nhập tài khoản Google trên Chrome và vào trang [gemini.google.com/app](https://gemini.google.com/app).
4. Quay lại terminal và nhấn **Enter**. Hệ thống sẽ tự động đồng bộ Cookie và tạo sẵn một **Virtual API Key** (dạng `sk-dezuxk-...`).

---

### Bước 3: Cấu hình trên các Ứng Dụng Chat Khách

Bạn có thể cắm ngay **Dezuxk AI Gateway** vào bất kỳ ứng dụng hỗ trợ OpenAI API nào:

#### 1. NextChat (ChatGPT-Next-Web)
* **Custom Model (Mô hình tùy chỉnh)**: `-all,+gemini-3.8-flash,+gemini-3.1-pro,+gemini-3.0-ultra`
* **OpenAI API Key**: Nhập key được tạo ở Bước 2 (hoặc key quản trị: `dezuxk_admin_secret_key_2026`).
* **OpenAI Endpoint (Địa chỉ API)**: `http://127.0.0.1:8080` (hoặc `http://127.0.0.1:8080/v1`).

#### 2. OpenWebUI / LibreChat
* **Base URL**: `http://127.0.0.1:8080/v1`
* **API Key**: Key được tạo từ Bước 2.

#### 3. Cursor IDE / VSCode Continue.dev / Roo Code
* **Provider**: `OpenAI Compatible`
* **Base URL**: `http://127.0.0.1:8080/v1`
* **Model**: `gemini-3.8-flash` hoặc `gemini-3.1-pro`

#### 4. Chatbox AI
* **AI Provider**: `OpenAI API`
* **API Host**: `http://127.0.0.1:8080`
* **API Key**: Key được tạo từ Bước 2.

---

## 🤖 Danh Mục Mô Hình Hỗ Trợ

Các model được định nghĩa linh hoạt qua file [configs/models.yaml](file:///d:/nhathao/AI/dezuxk/configs/models.yaml):

| Tên Model (ID) | Mô tả | Chế độ khuyến nghị |
| :--- | :--- | :--- |
| `gemini-3.8-flash` | Siêu tốc độ, phản hồi gần như tức thì | Chat thông thường, Coding nhanh, Tóm tắt |
| `gemini-3.1-pro` | Mô hình lập luận mạnh mẽ, cân bằng | Lập trình phức tạp, Phân tích văn bản dài |
| `gemini-3.0-ultra` | Sức mạnh cao cấp nhất | Giải toán chuyên sâu, Suy luận logic bậc cao |
| `gemini-2.5-pro` | Phiên bản 2.5 Pro ổn định | Kiểm thử đối sánh |

---

## 📡 Ví Dụ Gọi API Bằng cURL

### 1. Chat Completion Thông Thường (Sync)

```bash
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer <API_KEY_CỦA_BẠN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "messages": [
      {"role": "user", "content": "Thủ đô của Việt Nam là gì?"}
    ]
  }'
```

### 2. Kích Hoạt Thinking Mode (Suy luận sâu)

```bash
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer <API_KEY_CỦA_BẠN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "thinking": true,
    "messages": [
      {"role": "user", "content": "Một người nông dân có 17 con cừu, tất cả trừ 9 con chạy mất. Hỏi còn lại bao nhiêu con cừu?"}
    ]
  }'
```

### 3. Kích Hoạt Search Grounding (Tìm kiếm Web)

```bash
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer <API_KEY_CỦA_BẠN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "grounding": true,
    "messages": [
      {"role": "user", "content": "Giá vàng hôm nay tại Việt Nam thế nào?"}
    ]
  }'
```

---

## 🛠️ Cấu Trúc Dự Án

```text
dezuxk-gateway/
├── cmd/
│   ├── test_live_gateway/        # Bộ kiểm thử E2E 13 tính năng toàn diện
│   └── test_real_gemini/         # Test kết nối trực tiếp Google
├── configs/
│   ├── config.yaml               # File cấu hình chính của Gateway
│   └── models.yaml               # Cấu hình danh mục model động
├── internal/
│   ├── adapters/
│   │   ├── inbound/http/         # Chi router, OpenAI API handlers, rate limiting
│   │   └── outbound/
│   │       ├── alerts/           # Webhook thông báo Telegram/Discord/Slack
│   │       ├── google/           # Protocol wire codec, wrb.fr stream parser, SCOTTY
│   │       ├── session/          # SQLite WAL repository, AES-256-GCM Vault, Chrome CDP
│   │       └── storage/          # Local media storage với Byte-Range streaming
│   ├── app/daemon/               # Khởi động dịch vụ Headless Gateway
│   ├── config/                   # Loader cấu hình YAML
│   └── core/
│       ├── domain/               # Thực thể nghiệp vụ thuần (Zero 3rd-party)
│       ├── ports/                # Giao diện Interfaces Inbound/Outbound
│       └── services/             # ChatService, TokenCounter, ResponseCache, Failover
├── profiles/                     # Thư mục chứa Chrome profiles & Cookie mã hóa
├── scripts/
│   ├── setup_profile.ps1         # Tool Onboarding Profile tự động (PowerShell)
│   └── setup_profile.bat         # Wrapper nhấp đúp chuột cho Windows
├── storage/                      # Database gateway.db SQLite & Media assets
├── main.go                       # Entrypoint khởi động Gateway
├── run.bat                       # Script chạy server 1-click cho Windows
└── run.ps1                       # Script chạy server 1-click cho PowerShell
```

---

## 🛡️ Bảo Mật & Lưu Ý Vận Hành

1. **Khóa Master Key (AES-256-GCM)**:
   Để bảo vệ cookie an toàn tuyệt đối trong môi trường sản xuất, bạn nên cấu hình biến môi trường:
   ```bash
   set DEZUXK_MASTER_KEY=your_very_secret_32_bytes_passphrase_here
   ```
2. **Khóa Admin Token**:
   Mặc định token quản trị là `dezuxk_secure_admin_session_token_2026`. Bạn nên thay đổi trong file `configs/config.yaml` tại mục `admin.session_token`.
3. **Chạy ngầm dưới dạng Windows Service**:
   Bạn có thể dùng tiện ích [NSSM (Non-Sucking Service Manager)](https://nssm.cc/) để cài `dezuxk.exe` thành Windows Service tự động khởi động cùng hệ điều hành:
   ```cmd
   nssm install DezuxkGateway "D:\path\to\dezuxk.exe" "--config D:\path\to\configs\config.yaml"
   nssm start DezuxkGateway
   ```
