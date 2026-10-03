# Dezuxk AI Gateway (Google Gemini $\rightarrow$ OpenAI API)

<p align="center">
  <b>Hệ thống AI Gateway hiệu năng cao, chuyển đổi Google Gemini Web thành chuẩn OpenAI API (`/v1/chat/completions`) tương thích 100% với NextChat, OpenWebUI, Cursor, Chatbox và Continue.dev.</b>
</p>

---

## 🌟 Tính Năng Nổi Bật

* **Tương thích chuẩn OpenAI API (`/v1`) & Multi-Platform Client Support**:
  * Hỗ trợ đầy đủ `/v1/chat/completions` (cả chế độ **Sync** và **Real-time SSE Streaming**), `/v1/responses` (OpenAI Codex CLI), và `/v1/models`.
  * Hoạt động ổn định với **OpenAI Python / Node.js SDK**, **Cursor**, **Cline**, **Roo Code**, **Codex CLI**, **Antigravity**, và các Agent Framework bên thứ ba.
* **Danh Mục Mô Hình & Cấu Hình Động Thời Gian Chạy (Dynamic Runtime Architecture)**:
  * Tự động phát hiện mô hình mới từ upstream Google RPC (`otAQ7b`), không cần cập nhật mã nguồn hay file YAML khi Google ra mắt mô hình mới.
  * Lưu trữ bền vững vào **PostgreSQL** và lan truyền tức thì qua **Redis EventBus** giữa các Gateway Node A/B/C.
  * Bộ chọn mô hình mặc định thông minh (**Dynamic Model Selector**) dựa trên năng lực (`chat`, `thinking`, `code`, `vision`) và ưu tiên (`fast`, `balanced`, `best`).
  * Quản lý phân quyền và cấu hình động phân lập theo Tenant (**Tenant Runtime Settings**: Persona, Project Context, Preferred Model).
* **Hệ Thống Tác Vụ Tự Trị Bền Vững (Durable Agent Job System)**:
  * Khởi tạo tác vụ chạy nền bất đồng bộ qua `POST /v1/agent/runs`, tra cứu `GET /v1/agent/runs/{id}`, hủy `POST /v1/agent/runs/{id}/cancel`, resume `POST /v1/agent/runs/{id}/resume`, và stream tiến trình thời gian thực qua SSE `GET /v1/agent/runs/{id}/events`.
  * Hỗ trợ header `Idempotency-Key` ngăn chặn trùng lặp tác vụ khi client thử lại request.
  * Lưu trữ và phục hồi Snapshot trạng thái bền vững trên cơ sở dữ liệu SQLite (WAL Mode).
* **Khả Năng Chịu Lỗi Cấp Sản Xuất (Upstream Resilience & Failover)**:
  * **Resilient Upstream Client**: Tự động thử lại (Retry) có giới hạn với thuật toán **Exponential Backoff** (250ms -> 2s) và **Random Jitter (±20%)**, tôn trọng header `Retry-After` từ máy chủ Google.
  * **Circuit Breaker 3 trạng thái (`Closed`, `Half-Open`, `Open`)**: Tự động ngắt mạch khi lỗi dồn dập bảo vệ gateway không bị tắc nghẽn.
  * **Quản lý sức khỏe phiên Google (7 trạng thái)**: Theo dõi tỷ lệ lỗi, 429 count, 403 count, EWMA Average Latency, và hỗ trợ 4 thuật toán xoay vòng linh hoạt (`WeightedHealthScore`, `RoundRobin`, `LeastFailures`, `LeastLatency`).
* **Hàng Rào Bảo Vệ An Toàn Cho Agent (Loop Detection & Policy Engine)**:
  * Phát hiện và tự động ngắt các vòng lặp vô hạn (**Loop Detection**) khi agent gọi trùng lặp công cụ.
  * Kiểm soát phân quyền thực thi qua **Policy Layer** duy nhất: sandboxing đường dẫn tệp, giới hạn thời gian chạy cho từng công cụ (Per-tool Timeout), và xác nhận Human-in-the-Loop đối với các lệnh phá hủy hệ thống.
* **Giám Sát & Vận Hành Chuẩn Cloud-Native (Observability)**:
  * Xuất chỉ số giám sát chuẩn Prometheus qua endpoint `GET /metrics`.
  * Phân tách rành mạch **Liveness Probe** (`GET /health`) và **Readiness Probe** (`GET /ready`).
  * Truyền vết định danh toàn trình qua headers `X-Request-ID` và `X-Trace-ID`.
  * Cơ chế tắt máy chủ an toàn (**Graceful Shutdown** với thời gian xả 15 giây).
* **Các tính năng Gemini độc quyền**:
  * 🧠 **Extended Thinking Mode (Suy luận sâu)**: Bật cờ `"thinking": true` để Gemini suy nghĩ từng bước trước khi trả lời.
  * 🌐 **Search Grounding (Truy vấn Web thời gian thực)**: Bật cờ `"grounding": true` để Gemini tự động tìm kiếm Google Search và trả về trích dẫn nguồn (`sources`, `favicon`, `domain`).
  * 🐍 **Python Code Interpreter (Sandbox thực thi mã)**: Bật cờ `"code_interpreter": true` để Gemini tự động viết và chạy mã Python ngầm trong sandbox của Google.
  * 👁️ **Multimodal Vision (Phân tích hình ảnh)**: Tự động phân giải ảnh Base64 (`data:image/...`) hoặc Image URL trong `messages` và upload qua Google SCOTTY protocol.
* **Bộ nhớ đệm phản hồi siêu tốc (< 10ms)**:
  * Tích hợp **In-Memory LRU Caching + TTL**. Các câu hỏi lặp lại được phản hồi tức thì từ RAM, trả về header `X-Cache: HIT`.
* **Trích xuất Cookie tự động qua Chrome CDP**:
  * Quản lý mỗi tài khoản Google trong một thư mục Profile Chrome riêng biệt (`profiles/`).
  * Tự động kết nối WebSocket CDP (`Network.getAllCookies`) để trích xuất cookie trực tiếp từ RAM trình duyệt mà không cần cài extension.
  * Tự động bắt tay lấy CSRF Token `SNlM0e` và tự xoay vòng token `__Secure-1PSIDTS`.
* **Bảo mật cấp doanh nghiệp**:
  * **Mã hóa đối xứng AES-256-GCM (Encryption at Rest)** cho toàn bộ Cookie và thông tin đăng nhập trong SQLite và đĩa cứng qua module Vault.
  * **Zero Raw Secret Logging**: Tuyệt đối không ghi nhật ký cookie, Authorization header, CSRF token hay master key.
  * **Virtual API Keys đa người dùng**: Hỗ trợ phân quyền `admin` / `user`, giới hạn tốc độ đa tầng (Rate Limit RPM) và giới hạn kết nối đồng thời (Concurrency Limit).

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
* **OpenAI API Key**: Nhập key được tạo ở Bước 2 (hoặc key quản trị: `dezuxk_secure_admin_session_token_2026`).
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

## 🌐 Mô hình Vận hành: Development/CI vs Production Real Data

Dezuxk AI Gateway phân tách rõ ràng giữa môi trường kiểm thử và môi trường triển khai sản xuất với dữ liệu thật:

### 1. Môi trường Phát triển & Kiểm thử CI (Development / CI Test Mode)
- **Mục đích**: Chạy unit tests, kiểm thử tích hợp multi-node, chaos testing và CI/CD pipelines.
- **Cấu hình**: Sử dụng `docker-compose.multinode.yml` với cờ `DEZUXK_TEST_MODE=true` và các secret giả lập (fake credentials).
- **Khởi động**:
  ```bash
  docker compose -f docker-compose.multinode.yml up -d --build
  ```
- *Lưu ý*: File này được thiết kế riêng cho CI tự động, TUYỆT ĐỐI không dùng cho môi trường production chứa dữ liệu thật.

### 2. Môi trường Triển khai Sản xuất (Production Real Data Deployment)
- **Mục đích**: Vận hành cụm Gateway phân tán chịu tải cao, sử dụng tài khoản Google/Gemini thật và lưu trữ dữ liệu thật.
- **Kiến trúc**: 3 Gateway Nodes (A/B/C) cân bằng tải qua Nginx Load Balancer, dùng chung PostgreSQL (source-of-truth), Redis (distributed coordination) và S3/MinIO (shared media).
- **Bảo mật**:
  - Không hardcode secret vào compose hay git repository.
  - Sử dụng file .env.production và configs/config.production.yaml (cả 2 đều được gitignore).
  - Khóa DEZUXK_MASTER_KEY đồng nhất giữa các node để mã hóa AES-256-GCM toàn bộ Google session/cookie vào PostgreSQL.
  - Ngăn chặn tuyệt đối cờ DEZUXK_TEST_MODE=true trong production (fail-fast startup validation).
- **Phân định Quyền sở hữu Profile**: Gateway Node A đảm nhiệm độc quyền Profile/CDP Control Plane (Nginx route ^~ /v1/profiles về Node A; thư mục mount riêng biệt profiles/gateway-a, profiles/gateway-b, profiles/gateway-c). AI/Chat data-plane vẫn HA round-robin trên toàn cụm.
- **Quy trình Khởi động Production (First-Boot)**:
  `ash
  # Bước 1: Sao chép file cấu hình mẫu và điền secret thực tế
  cp configs/production.env.example .env.production
  cp configs/config.production.example.yaml configs/config.production.yaml
  mkdir -p profiles/gateway-a profiles/gateway-b profiles/gateway-c

  # Bước 2: Chạy kiểm tra tiền trạm cấu hình (config-only)
  ./scripts/production-preflight.sh --skip-infra

  # Bước 3: Khởi động tầng dữ liệu (Postgres, Redis, MinIO và minio-init tạo bucket tự động)
  docker compose \
    --env-file .env.production \
    -f docker-compose.production.yml \
    --profile self-hosted \
    up -d postgres redis minio minio-init

  # Bước 4: Chạy kiểm tra tiền trạm toàn diện hạ tầng
  ./scripts/production-preflight.sh

  # Bước 5: Khởi động toàn bộ cụm Gateways và Nginx Load Balancer
  docker compose \
    --env-file .env.production \
    -f docker-compose.production.yml \
    --profile self-hosted \
    up -d --build
  `
- **Tài liệu Chi tiết**:
  - [Hướng Dẫn Triển Khai Production Real Data](file:///d:/nhathao/Vibe/dezuxk-agent/docs/PRODUCTION_REAL_DATA_SETUP.md)
  - [Kiến trúc Phân tán Multi-Node](file:///d:/nhathao/Vibe/dezuxk-agent/docs/MULTI_NODE_ARCHITECTURE.md)
  - [Quy trình Di chuyển Schema PostgreSQL](file:///d:/nhathao/Vibe/dezuxk-agent/docs/POSTGRES_MIGRATION.md)

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

---

## 🔒 CI Pipeline & GitHub Branch Protection

Để bảo vệ nhánh `main` / `master` khỏi các mã nguồn lỗi hoặc phá vỡ an toàn sản xuất, hãy cấu hình Branch Protection Rule trên GitHub Repository:

1. Truy cập: **Settings** → **Branches** (hoặc **Rulesets**) → **Add branch ruleset** / **Add rule**.
2. **Branch name pattern**: `main` (hoặc `master`).
3. Tích chọn: **Require status checks to pass before merging**.
4. Tìm kiếm và chọn chính xác tên status check sau:
   * **`Production Verification Gate / Lint, Test & Race Verification`**
5. Tích chọn: **Require branches to be up to date before merging**.
6. Nhấn **Save changes** để kích hoạt.

> **Lưu ý**: CI Pipeline (`.github/workflows/ci.yml`) tự động đồng bộ toolchain từ `go.mod` và thực thi chuỗi kiểm tra bắt buộc:
> `go mod verify` $\rightarrow$ `gofmt` $\rightarrow$ `go vet` $\rightarrow$ `staticcheck` $\rightarrow$ `govulncheck` $\rightarrow$ `go test -v` $\rightarrow$ `go test -race`.
