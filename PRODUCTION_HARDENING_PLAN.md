# Production Hardening Plan: Dezuxk AI Agent Gateway

> **Tài liệu Kế hoạch Triển khai Nâng cấp Gateway thành Production-Grade Multi-Platform AI Agent Gateway**  
> Kiến trúc: Clean Architecture / Hexagonal Architecture (Ports & Adapters)  
> Mục tiêu hỗ trợ ổn định: OpenAI SDK, Python/Node.js, Cursor, Cline, Roo Code, Codex CLI, Antigravity, Agent Frameworks.

---

## 1. BẢN ĐỒ VẤN ĐỀ HIỆN TẠI TỪ SOURCE AUDIT

| STT | Vấn đề phát hiện | File liên quan | Mức độ | Rủi ro phá vỡ (Breaking Risk) |
| :---: | :--- | :--- | :---: | :--- |
| **1** | **Thiếu lớp Upstream Resilience tập trung:** Retry và failover nằm rải rác trong `chat_service.go`, thiếu exponential backoff có jitter, thiếu retry budget, thiếu circuit breaker khi Google lỗi hàng loạt. | `internal/core/services/chat_service.go`, `internal/adapters/outbound/google/transport.go` | **P0** | **Không** (Bọc ngoài outbound transport / service) |
| **2** | **Session / Account Health & Selection cứng nhắc:** Trạng thái tài khoản chưa phân loại đủ 7 trạng thái chuẩn (`healthy`, `degraded`, `cooldown`, `auth_expired`, `quota_exhausted`, `rate_limited`, `unavailable`), thuật toán chọn tài khoản bị hardcode trong `sqlite_repo.go` mà không có Strategy pattern. | `internal/core/domain/account.go`, `internal/core/domain/session_state.go`, `internal/adapters/outbound/session/sqlite_repo.go`, `memory_repo.go` | **P0** | **Không** (Giữ API SessionRepository, mở rộng selection strategy) |
| **3** | **Lỗ hổng bảo mật Auth trong Production:** `Config.Validate()` không kiểm tra `server.api_key` trong production; `router.go` tự động fallback sang `DefaultInternalIdentity` (đầy đủ quyền admin) nếu không có auth; thiếu cờ `server.environment: production`. | `internal/config/config.go`, `internal/adapters/inbound/http/router.go`, `internal/adapters/inbound/http/auth_middleware.go`, `internal/core/services/agent/runner.go` | **P0** | **Thấp** (Chỉ tác động khi chạy cấu hình production sai) |
| **4** | **CORS Hardening lỏng lẻo:** `AllowedOrigins: ["*"]` kết hợp với `AllowCredentials: true` vi phạm tiêu chuẩn bảo mật trình duyệt; production không được phép wildcard CORS. | `internal/adapters/inbound/http/router.go`, `internal/config/config.go` | **P0** | **Không** (Cho phép cấu hình whitelist origins) |
| **5** | **Agent Loop thiếu Guardrails kỹ thuật:** Chưa có loop detection (gọi cùng tool + same arguments N lần), chưa giới hạn tổng số tool calls toàn run, chưa giới hạn thời gian thực thi tối đa, thiếu phân loại stop reasons chuẩn (`max_tool_calls_reached`, `repeated_tool_loop`, `timeout`, `cancelled`, `upstream_unavailable`, `policy_denied`, `verification_failed`). | `internal/core/services/agent/runner.go`, `internal/core/domain/agent.go` | **P0** | **Không** (Giữ nguyên cấu trúc AgentState, bổ sung trường & stop reasons) |
| **6** | **Thiếu Durable Agent Job System:** Agent chỉ chạy synchronous request HTTP dài (`/v1/agent/run` và `/v1/agent/run/stream`), nếu client rớt mạng thì mất kiểm soát tác vụ; thiếu async Job API (`/v1/agent/runs`). | `internal/adapters/inbound/http/agent_handler.go`, `internal/core/ports/agent_ports.go`, `internal/core/domain/agent.go` | **P1** | **Không** (Bổ sung endpoint mới, giữ nguyên `/v1/agent/run`) |
| **7** | **Thiếu Persistent Agent Run State Abstraction:** Trạng thái agent run chưa được lưu trữ bền vững độc lập với graph checkpoints; cần interface `AgentRunRepository` hỗ trợ SQLite. | `internal/core/ports/agent_ports.go`, `internal/adapters/outbound/session/sqlite_agent_repo.go` | **P1** | **Không** (Mở rộng ports/adapters) |
| **8** | **Multi-Instance Readiness chưa phân tách:** Rate limiter, account health, in-flight state nằm hoàn toàn trong RAM đơn tiến trình mà không có interface phân biệt local state vs shared distributed state. | `internal/core/ports/`, `internal/adapters/outbound/session/` | **P1** | **Không** (Thiết kế Ports/Adapters abstraction) |
| **9** | **Rate Limit & Concurrency Limit đa chiều:** `IPRateLimiter` chỉ giới hạn theo IP thô; thiếu rate limiting theo tenant_id, api_key_id, endpoint, model và giới hạn concurrent agent runs. | `internal/adapters/inbound/http/ratelimit.go`, `internal/adapters/inbound/http/router.go` | **P1** | **Không** (Tương thích ngược) |
| **10** | **Tool Calling Normalization & Canonical Schema:** Cần chuẩn hóa dữ liệu ToolCall/ToolResult nội bộ thành Canonical representation, chống duplicate tool_call_id, xử lý malformed JSON và kiểm định JSON Schema trước khi thực thi. | `internal/core/domain/chat.go`, `internal/core/services/tool_normalizer.go`, `internal/core/services/tool_parser.go` | **P1** | **Không** (Cải tiến pipeline parsing) |
| **11** | **Tool Execution Policy thống nhất:** Toàn bộ công cụ phải qua một policy layer duy nhất với timeout từng tool, phân loại quyền (read-only, write, shell, network, destructive, privileged). | `internal/core/services/policy/policy_engine.go` | **P1** | **Không** (Hoàn thiện logic hiện có) |
| **12** | **Idempotency Key:** Thiếu hỗ trợ `Idempotency-Key` header cho các tác vụ tạo Agent Run/Job để chống duplicate execution khi retry mạng. | `internal/adapters/inbound/http/agent_handler.go`, `internal/core/ports/` | **P1** | **Không** (Header tùy chọn) |
| **13** | **Request Tracing & Context Propagation:** Thiếu `request_id`, `trace_id`, `tenant_id`, `key_id`, masked account ID xuyên suốt từ HTTP -> Agent -> Chat -> Upstream -> Tool. | `internal/core/domain/`, `internal/adapters/inbound/http/` | **P1** | **Không** (Truyền qua Go context) |
| **14** | **Observability (Prometheus Metrics & Structured Logging):** Chưa có endpoint `/metrics` chuẩn Prometheus cho gateway/gemini/agent; logs còn lẫn text thô, cần structured logger bảo vệ thông tin nhạy cảm. | `internal/adapters/inbound/http/router.go`, `internal/core/domain/metrics.go` | **P2** | **Không** (Bổ sung router endpoint) |
| **15** | **Health & Readiness Endpoints:** Tách bạch rõ `/health` (liveness) và `/ready` (readiness: DB, healthy accounts, model registry). | `internal/adapters/inbound/http/router.go` | **P2** | **Không** (Đã có sẵn /health và /ready, cần làm chặt điều kiện) |
| **16** | **Graceful Shutdown & Config Validation:** Cần đảm bảo khi nhận SIGTERM sẽ ngưng nhận traffic, flush state agent, đóng DB; validate config fail-fast khi startup. | `internal/app/daemon/daemon.go`, `internal/config/config.go` | **P2** | **Không** |

---

## 2. LỘ TRÌNH TRIỂN KHAI THEO PHÂN KỲ

### Giai đoạn P0 — Khắc Phục Lỗ Hổng Sống Còn & Nâng Cao Sức Chịu Đựng (Upstream & Auth Resilience)
1. **P0.1: Resilient Upstream Client & Circuit Breaker**
   - Tạo `ResilientUpstreamClient` đóng gói `UpstreamGoogleTransport`.
   - Hỗ trợ: Exponential backoff (`250ms -> 500ms -> 1s -> 2s`), random jitter (±20%), retry budget, tôn trọng `Retry-After`.
   - Phân loại lỗi chính xác: Retryable (429, 500, 502, 503, 504, timeout, connection reset, broken pipe) vs Non-retryable (400, 401, 403, 404, invalid auth, schema error).
   - Tích hợp Circuit Breaker theo host/service với 3 trạng thái: `Closed`, `Open`, `Half-Open`.
2. **P0.2: Gemini Account & Session Health Management & Selection Strategy**
   - Mở rộng domain entity `ManagedAccount` với 7 trạng thái chuẩn: `healthy`, `degraded`, `cooldown`, `auth_expired`, `quota_exhausted`, `rate_limited`, `unavailable`.
   - Thêm metric tracking: `consecutive_failures`, `last_success_at`, `last_failure_at`, `count_429`, `count_403`, `avg_latency_ms`, `cooldown_until`.
   - Tạo interface `AccountSelectionStrategy` với các triển khai:
     - `WeightedHealthScoreStrategy` (mặc định)
     - `RoundRobinStrategy`
     - `LeastFailuresStrategy`
     - `LeastLatencyStrategy`
   - Đảm bảo khi một account bị lỗi tạm thời sẽ tự động chuyển cooldown và failover sang account khác ngay lập tức, không retry mù vào cùng một account.
3. **P0.3: Production Auth Security & Masked Logging**
   - Cập nhật `Config.Validate()`: Khi `Environment == "production"` hoặc `server.production == true`, bắt buộc `Server.APIKey` phải có giá trị và không dùng secret mặc định.
   - Xóa bỏ fallback `DefaultInternalIdentity` trong `router.go` đối với request bên ngoài khi chạy production.
   - Thêm filter chặn log credentials: Cấm log Authorization header, Gemini cookies (`__Secure-1PSID`, `__Secure-1PSIDTS`, `OSID`), CSRF token (`SNlM0e`), proxy passwords. Chỉ cho phép log hashed/masked identifiers (e.g. `acc_***a1b2`).
4. **P0.4: CORS Hardening**
   - Trong production: Tuyệt đối không cho phép `AllowedOrigins: ["*"]` khi `AllowCredentials: true`.
   - Đọc danh sách whitelist từ `server.allowed_origins`. Nếu production mà config `*` thì báo lỗi cấu hình khi khởi động.
5. **P0.5: Agent Loop Safety & Loop Detection**
   - Bổ sung vào `Runner` và `AgentRunOptions`:
     - `MaxToolCalls` (giới hạn tổng tool calls)
     - `MaxRepeatedCalls` (phát hiện loop: cùng tool + cùng args lặp lại > N lần)
     - `MaxExecutionDuration` (timeout tổng cho agent run)
     - `ConsecutiveFailureLimit` (dừng khi tool fail liên tiếp quá N lần)
   - Cập nhật các Stop Reasons rõ ràng: `completed`, `max_steps_reached`, `max_tool_calls_reached`, `repeated_tool_loop`, `timeout`, `cancelled`, `upstream_unavailable`, `policy_denied`, `verification_failed`.

### Giai đoạn P1 — Durable Agent System, Multi-Instance & Protocol Conformance
1. **P1.1: Durable Agent Job System (Async Job API)**
   - Thêm endpoints:
     - `POST /v1/agent/runs` (tạo run async, trả về ngay `run_id` và status `queued`)
     - `GET /v1/agent/runs/{id}` (tra cứu trạng thái tiến trình)
     - `POST /v1/agent/runs/{id}/cancel` (hủy bỏ tác vụ đang chạy)
     - `GET /v1/agent/runs/{id}/events` (stream SSE hoặc danh sách events)
     - `POST /v1/agent/runs/{id}/resume` (tiếp tục tác vụ bị ngắt hoặc chờ duyệt)
   - Bảo toàn tương thích ngược 100% với `POST /v1/agent/run` và `POST /v1/agent/run/stream`.
2. **P1.2: Persistent Agent State & Checkpoint Repository**
   - Thiết kế abstraction `AgentRunRepository` độc lập:
     - `CreateRun`, `UpdateRun`, `GetRun`, `ListRuns`, `AppendEvent`, `GetEvents`, `CancelRun`.
   - Triển khai SQLite `SqliteAgentRunRepository` (sử dụng WAL mode).
   - In-memory implementation cho unit tests.
3. **P1.3: Multi-Instance Ready Abstractions**
   - Tách biệt rõ `LocalProcessState` vs `DistributedState`.
   - Định nghĩa Ports cho Distributed Lock (`DistributedLocker`), Shared Rate Limiter, Shared Account Pool.
4. **P1.4: Rate Limiting & Concurrency Control đa chiều**
   - Mở rộng rate limiter kiểm soát theo `TenantID`, `KeyID`, `Endpoint`, `Model`.
   - Kiểm soát `MaxConcurrentAgentRuns` theo Tenant.
   - Trả về HTTP 429 và header `Retry-After` chuẩn.
5. **P1.5: Canonical Tool Calling Normalization**
   - Định nghĩa `CanonicalToolCall` và `CanonicalToolResult` trong Domain.
   - Chặn duplicate `tool_call_id`.
   - Tự động sửa chữa JSON arguments malformed trước khi validate với JSON Schema.
6. **P1.6: Tool Execution Policy Hardening**
   - Mọi thao tác thực thi tool bắt buộc qua Policy Engine.
   - Phân loại rõ: `ReadOnly`, `Network`, `Write`, `Shell`, `Destructive`, `Privileged`.
   - Cấu hình execution timeout độc lập cho từng loại tool.
7. **P1.7: Idempotency Key Support**
   - Hỗ trợ header `Idempotency-Key` với cache lưu trữ TTL để chống trùng lặp job khi client retry mạng.
8. **P1.8: Request Trace & Context Propagation**
   - Tạo struct `TraceContext` mang `RequestID`, `TraceID`, `TenantID`, `KeyID`, `Model`, `MaskedAccountID`.
   - Truyền qua Go `context.Context` xuyên suốt toàn bộ các tầng.

### Giai đoạn P2 — Giám Sát, Khởi Động Sạch, Kiểm Thử Toàn Diện
1. **P2.1: Prometheus Observability & Health/Ready Separation**
   - Endpoint `GET /metrics` xuất định dạng Prometheus text format.
   - Endpoint `GET /health` (liveness) và `GET /ready` (readiness: DB ping, active accounts > 0, models > 0).
2. **P2.2: Structured Logging**
   - Structured logger với context fields (`request_id`, `trace_id`, `tenant_id`, `duration_ms`, `status`).
   - Tự động che dấu dữ liệu nhạy cảm.
3. **P2.3: Graceful Shutdown & Strict Config Validation**
   - Dừng nhận traffic mới khi có SIGINT/SIGTERM, hoàn tất các jobs đang chạy dở với deadline 15s.
   - Fail-fast khi config sai lúc khởi động.
4. **P2.4: Comprehensive Unit, Integration, Agent & Race Testing**
   - Test retry, backoff, circuit breaker, account selection.
   - Integration tests mock Gemini upstream (429, 500, 503, disconnect, slow response).
   - Test agent loop guardrails (loop detection, timeout, cancellation, approval).
   - Chạy kiểm tra không có race condition (`go test -race ./...`).

---

## 3. CHECKLIST KIỂM SOÁT TÍNH TƯƠNG THÍCH (BACKWARD COMPATIBILITY)
- [ ] Giữ nguyên 100% chữ ký của `POST /v1/chat/completions` (sync và stream).
- [ ] Giữ nguyên `POST /v1/responses` tương thích OpenAI Codex CLI.
- [ ] Giữ nguyên `GET /v1/models`.
- [ ] Giữ nguyên `POST /v1/agent/run` và `POST /v1/agent/run/stream`.
- [ ] Giữ nguyên các use cases và interfaces hiện hành trong `ports/`.
- [ ] Tất cả các test hiện có (`go test ./...` và `go test -race ./...`) phải tiếp tục PASS.
