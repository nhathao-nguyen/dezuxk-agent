# Thiết Kế Kiến Trúc Giai Đoạn 3: Embedded Admin Dashboard & In-Memory Response Caching

- **Dự án**: dezuxk-gateway
- **Trạng thái**: Đã phê duyệt (Approved)
- **Ngày lập**: 2026-09-26
- **Kỹ sư Trưởng**: Antigravity AI Gateway Lead Engineer

---

## 1. Mục Tiêu & Phạm Vi (Goals & Scope)

Giai đoạn 3 bổ sung hai thành phần cốt lõi nhằm nâng cao tính tiện dụng trong quản trị vận hành và tối ưu hóa hiệu năng, chi phí tài nguyên cho `dezuxk-gateway`:
1. **In-Memory Response Caching (Tầng Bộ nhớ đệm Phản hồi Thông minh)**:
   - Lưu trữ phản hồi câu trả lời Chat (`/v1/chat/completions`) và số dư tín dụng (`/v1/flow/credits`) trong RAM với thuật toán băm SHA-256 từ `model + messages + temperature + system_prompt`.
   - Trả lời trong < 5ms đối với các prompt trùng lặp, kèm header `X-Cache: HIT`, giảm tối đa số lượt gọi lên Google backend và bảo toàn quota cho tài khoản Google.
   - Hỗ trợ LRU eviction khi đạt ngưỡng `max_entries` và tự động hết hạn theo `ttl_seconds`.
2. **Embedded Admin Dashboard (Giao diện Quản trị nhúng Single Page Application)**:
   - Sử dụng cơ chế Go `embed.FS` để nhúng toàn bộ HTML, CSS, JavaScript vào binary mà không cần Node.js, npm hay external CDN khi chạy daemon.
   - Giám sát Account Pool: Hiển thị danh sách tài khoản (Email, Tier, Trạng thái Sống/Chết, Proxy đã gán, Flow Credits, Gemini Quota).
   - Điều khiển Chrome 1-Click: Nút bấm gọi `POST /v1/profiles/{id}/launch` để mở Chrome thực tế, và `POST /v1/profiles/{id}/sync` để cào Cookie qua CDP trực tiếp trên giao diện web.
   - Cấu hình Proxy nhanh: Modal cho phép cập nhật HTTP/SOCKS5 proxy per-profile và mã hóa lưu vào Secret Vault qua `PUT /v1/profiles/{id}/proxy`.
   - Theo dõi Metric: Hiển thị RPM, tổng số request, Contract Drift Alerts (`schema_unexpected`, `unmapped_fields`) và trạng thái GPU online (`HTrJv` / `yBhWQ`).
   - Bảo mật trang quản trị qua cấu hình `admin` trong `config.yaml` (hỗ trợ Admin Token / Session Cookie / Basic Auth).

---

## 2. Kiến Trúc Kỹ Thuật Chi Tiết (Technical Architecture)

### 2.1. Cấu hình Hạ tầng (`config.yaml` & `internal/config/config.go`)

Không hardcode bất kỳ tham số nào. Thêm hai khối cấu hình mới vào `config.yaml`:

```yaml
# Tầng Bộ nhớ đệm Phản hồi Thông minh (In-Memory Response Caching)
cache:
  enabled: true
  max_entries: 10000
  ttl_seconds: 3600
  methods:
    - "chat"
    - "credits"

# Quản trị viên & Giao diện Dashboard nhúng
admin:
  enabled: true
  username: "admin"
  password: "dezuxk_admin_secret_pass"
  session_token: "dezuxk_secure_admin_session_token_2026"
```

Cấu trúc Go tương ứng trong `internal/config/config.go`:
- `CacheConfig`:
  - `Enabled *bool`: Cờ bật/tắt cache.
  - `MaxEntries int`: Giới hạn số mục trong RAM (mặc định 10000).
  - `TTLSeconds int`: Thời gian sống của cache tính bằng giây (mặc định 3600s).
  - `Methods []string`: Danh sách phương thức cho phép cache (ví dụ: `chat`, `credits`).
- `AdminConfig`:
  - `Enabled *bool`: Cờ bật/tắt quản trị.
  - `Username string`: Tên đăng nhập Admin.
  - `Password string`: Mật khẩu đăng nhập Admin.
  - `SessionToken string`: Token phiên quản trị bảo mật (cung cấp qua Header hoặc Cookie).

### 2.2. In-Memory Response Caching Engine (`internal/core/services/response_cache.go`)

- **Cấu trúc dữ liệu**:
  - `CacheItem`: `Key string`, `Value []byte`, `ContentType string`, `Headers map[string]string`, `ExpiresAt time.Time`, `AccessedAt time.Time`.
  - `ResponseCache`:
    - `mu sync.RWMutex`: Đảm bảo an toàn đa luồng.
    - `entries map[string]*list.Element`: Bảng băm tra cứu O(1).
    - `evictList *list.List`: Danh sách liên kết đôi cho thuật toán LRU (Least Recently Used).
    - `maxEntries int`: Ngưỡng mục tối đa.
    - `ttl time.Duration`: Thời gian sống.
    - `methods map[string]bool`: Tập phương thức được bật cache.
    - `hits uint64`, `misses uint64`: Thống kê hoạt động.

- **Thuật toán Khóa Băm (Key Generation)**:
  - Hàm `GenerateChatKey(model string, systemPrompt string, messages []domain.ChatMessage, temperature float64) string`:
    Chuẩn hóa dữ liệu theo định dạng xác định:
    `chat:{sha256(model + "\n" + systemPrompt + "\n" + serialize(messages) + "\n" + fmt.Sprintf("%.4f", temperature))}`.
  - Hàm `GenerateCreditsKey(accountID string) string`:
    `credits:{sha256(accountID)}`.

- **Quy trình Xử lý Request Chat / Credits**:
  1. Kiểm tra cấu hình `cache.enabled` và phương thức tương ứng có trong `cache.methods` không. Nếu không, chuyển tiếp trực tiếp sang upstream.
  2. Tạo khóa băm SHA-256 từ request.
  3. Tra cứu cache (`Get(key)`):
     - **Nếu HIT**:
       - Cập nhật thời gian truy cập cho LRU và tăng counter `hits`.
       - Đặt response header: `X-Cache: HIT`, `X-Cache-TTL: <remaining_seconds>`.
       - Ghi trực tiếp `Value` và `ContentType` ra `http.ResponseWriter`. Thời gian phản hồi < 5ms.
     - **Nếu MISS**:
       - Tăng counter `misses`.
       - Đặt response header: `X-Cache: MISS`.
       - Thực thi tác vụ upstream qua `ChatUseCase` / `FlowCreditUseCase`.
       - Nếu kết quả trả về HTTP 200 OK và không phải luồng SSE dở dang, lưu vào cache qua `Set(key, payload, contentType, headers)`.

### 2.3. Tích Hợp Thống Kê & Metrics (`internal/core/domain/contract_metrics.go`)

Mở rộng `ContractMetrics` để thu thập các số liệu vận hành thời gian thực phục vụ Dashboard:
- Thống kê RPM (Requests Per Minute): Cửa sổ trượt ghi nhận số lượng request trong 60 giây gần nhất.
- Thống kê Cache: `CacheHits`, `CacheMisses`, `CacheHitRatio` (phần trăm).
- Trạng thái Contract Drift: `SchemaUnexpected`, `UnmappedFields`, phân loại lỗi theo từng RPC.
- Danh sách GPU online từ `ModelRegistry.List()` và `backendStatus` thu thập qua `HTrJv` / `yBhWQ`.

### 2.4. Embedded Admin Web UI (`internal/adapters/inbound/web/`)

- Sử dụng cơ chế nhúng:
  ```go
  package web

  import "embed"

  //go:embed static/*
  var StaticFS embed.FS
  ```
- File cấu trúc:
  - `static/index.html`: SPA Dashboard giao diện hiện đại phong cách Clean Cyberpunk / Dark Engineering.
  - `static/style.css`: Toàn bộ CSS tự viết thuần túy (Flexbox, CSS Grid, Badges, Modals, Responsive), không phụ thuộc internet/CDN ngoài.
  - `static/app.js`: Quản lý trạng thái giao diện:
    - Tab 1: **Account Pool**: Bảng các tài khoản Google với nút 1-click Chrome và CDP Sync, nút mở modal cấu hình Proxy.
    - Tab 2: **System Metrics**: Biểu đồ Canvas RPM, thẻ Cache Performance (<5ms indicator), Bảng cảnh báo Drift Alerts.
    - Tab 3: **GPU Cluster Status**: Trạng thái các cụm GPU và models Veo/Imagen từ `yBhWQ` / `HTrJv`.
    - Quản lý phiên: Lưu session token vào `localStorage` / cookie, tự động đăng xuất khi phiên hết hạn.

### 2.5. Admin Handlers & Bảo Mật Tuyến Đường (`internal/adapters/inbound/http/`)

- **Middleware Xác thực Admin (`AdminAuthMiddleware`)**:
  - Kiểm tra Cookie `dezuxk_admin_token` hoặc Header `Authorization: Bearer <token>` hoặc HTTP Basic Auth.
  - Nếu không hợp lệ: Trả về HTTP 401 Unauthorized (hoặc chuyển hướng sang màn hình đăng nhập nếu truy cập web).
- **Endpoint Quản Trị**:
  - `POST /v1/admin/auth/login`: Xác thực username/password hoặc token, trả về session cookie.
  - `POST /v1/admin/auth/logout`: Xóa session cookie.
  - `GET /v1/admin/overview`: Trả về dữ liệu tổng hợp cho Dashboard (Account Pool, Cache Stats, Metrics Snapshot, GPU status) trong một lần gọi nhanh.
  - Mount `/admin/*` trỏ tới `http.FileServer(http.FS(subFS))` để phục vụ Single-Page App tĩnh.

---

## 3. Kế Hoạch Kiểm Thử & Nghiệm Thu (Verification Plan)

1. **Unit Tests**:
   - `internal/core/services/response_cache_test.go`:
     - Test băm khóa SHA-256 nhất quán.
     - Test LRU eviction khi vượt quá `max_entries`.
     - Test TTL expiration.
     - Test an toàn đa luồng (`go test -race`).
   - `internal/adapters/inbound/http/cache_handler_test.go`:
     - Test header `X-Cache: MISS` ở lần gọi 1 và `X-Cache: HIT` (<5ms) ở lần gọi 2.
   - `internal/adapters/inbound/http/admin_auth_test.go`:
     - Test từ chối truy cập không có token / sai mật khẩu.
     - Test đăng nhập thành công với đúng cấu hình.
   - `internal/adapters/inbound/web/embed_test.go`:
     - Kiểm tra `StaticFS` đọc được `index.html`, `style.css`, `app.js`.
2. **Regression & Full Suite**:
   - Chạy `go test -count=1 ./...` bảo đảm 100% PASS tất cả các package.
   - Chạy `go build ./...` biên dịch sạch sẽ không cảnh báo.
3. **E2E Automation Verification**:
   - Sử dụng Chrome DevTools MCP để mở và kiểm tra giao diện Dashboard, kiểm tra console log sạch (không có JS error).
