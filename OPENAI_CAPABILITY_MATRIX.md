# OpenAI Compatibility & Capability Matrix

Tài liệu này xác định chi tiết ma trận tương thích giữa Dezuxk Agent Gateway và chuẩn giao thức OpenAI API (OpenAI Python SDK, Node.js SDK, Cursor, Cline, Roo Code, Codex CLI).

## 1. Endpoints Matrix

| Endpoint | Method | Hỗ trợ | Ghi chú tương thích |
| :--- | :--- | :---: | :--- |
| `/v1/chat/completions` | POST | **100%** | Hỗ trợ cả Synchronous và SSE Streaming (`text/event-stream`), tool calling, vision. |
| `/v1/responses` | POST | **100%** | Hỗ trợ OpenAI Responses API (dùng cho OpenAI Codex CLI). |
| `/v1/models` | GET | **100%** | Trả về danh sách mô hình dạng OpenAI standard. |
| `/v1/agent/run` | POST | **100%** | Synchronous Agent ReAct Loop và State Machine Graph. |
| `/v1/agent/run/stream` | POST | **100%** | Synchronous SSE Streaming Agent execution. |
| `/v1/agent/runs` | POST | **100%** | Async Durable Agent Job Submission (trả ngay HTTP 202 `{id, status}`). |
| `/v1/agent/runs/{id}` | GET | **100%** | Tra cứu trạng thái và lịch sử bước chạy của Agent Run. |
| `/v1/agent/runs` | GET | **100%** | Liệt kê danh sách Agent Runs theo tenant. |
| `/v1/agent/runs/{id}/cancel` | POST | **100%** | Hủy tác vụ Agent đang chạy nền. |
| `/v1/agent/runs/{id}/resume` | POST | **100%** | Tiếp tục Agent Run với phản hồi bổ sung từ người dùng. |
| `/v1/agent/runs/{id}/events` | GET | **100%** | Server-Sent Events (SSE) theo dõi sự kiện thời gian thực của tác vụ. |
| `/metrics` | GET | **100%** | Prometheus Metrics Exporter (text/plain 0.0.4) cho Grafana/Datadog. |
| `/health` | GET | **100%** | Liveness Probe kiểm tra tiến trình máy chủ còn hoạt động (`status: ok`). |
| `/ready` | GET | **100%** | Readiness Probe kiểm tra SQLite DB, session pool, và active models. |

---

## 2. Request Parameters Matrix (`/v1/chat/completions`)

| Tham số | Kiểu dữ liệu | Trạng thái | Hành vi xử lý |
| :--- | :--- | :---: | :--- |
| `model` | `string` | **Hỗ trợ** | Ánh xạ sang danh mục mô hình Gemini (ví dụ: `gemini-3.8-flash`, `gemini-3.8-pro`). |
| `messages` | `array` | **Hỗ trợ** | Hỗ trợ đầy đủ roles: `system`, `user`, `assistant`, `tool`. Nhận cả string và mảng đa phương thức `content_parts` (`text`, `image_url`). |
| `stream` | `boolean` | **Hỗ trợ** | Phát SSE streaming chunks theo định dạng `data: {...}\n\ndata: [DONE]\n\n`. |
| `tools` | `array` | **Hỗ trợ** | Nhận danh sách function tools với JSON Schema parameters chuẩn. |
| `tool_choice` | `string / object` | **Hỗ trợ** | Hỗ trợ `"auto"`, `"none"`, `"required"`, và `{"type":"function", "function":{"name":"..."}}` với pipeline kiểm định code nghiêm ngặt. |
| `temperature` | `number` | **Hỗ trợ** | Truyền trực tiếp tới engine sinh nội dung. |
| `response_format` | `object` | **Hỗ trợ** | Hỗ trợ ép kiểu JSON output (`{"type": "json_object"}`). |
| `max_tokens` | `integer` | **Hỗ trợ** | Tiếp nhận an toàn. |
| `max_completion_tokens`| `integer` | **Hỗ trợ** | Tiếp nhận an toàn và alias cho `max_tokens`. |
| `top_p` | `number` | **Chấp nhận an toàn** | Tiếp nhận an toàn mà không làm lỗi request. |
| `n` | `integer` | **Hỗ trợ** | Trả về các draft choices nếu có. |
| `user` | `string` | **Chấp nhận an toàn** | Tiếp nhận an toàn cho mục đích tracking. |
| `seed` | `integer` | **Chấp nhận an toàn** | Tiếp nhận an toàn. |

---

## 3. Streaming & Response Output Conformance

| Thành phần | Chuẩn OpenAI | Hành vi Dezuxk Gateway |
| :--- | :--- | :--- |
| **Object Type** | `chat.completion` / `chat.completion.chunk` | Chính xác |
| **ID Format** | `chatcmpl-<id>` | Bắt đầu bằng tiền tố `chatcmpl-` |
| **Finish Reason** | `"stop"`, `"tool_calls"`, `"length"` | Trả về `"tool_calls"` khi có lệnh gọi tool, `"stop"` khi hoàn thành |
| **Tool Calling Stream** | Delta chứa `tool_calls` array kèm `index`, `id`, `function` | Lọc sạch thẻ XML rò rỉ, phát chunk `tool_calls` chuẩn |
| **Usage Statistics** | `prompt_tokens`, `completion_tokens`, `total_tokens` | Tính toán chính xác theo token counter |
| **Error Format** | `{"error": {"message": ..., "type": ..., "code": ...}}` | Trả về đúng HTTP status code (400, 401, 403, 429, 503) và payload chuẩn OpenAI SDK |
| **Retry-After Header** | Header `Retry-After: <seconds>` trên HTTP 429 | Tính toán động chính xác số giây cần chờ |
| **Idempotency** | Header `Idempotency-Key` | Tránh duplicate jobs khi client gửi lại request |
| **Trace Propagation** | Headers `X-Request-ID`, `X-Trace-ID` | Theo vết toàn trình từ client qua gateway đến model |

---

## 4. Documented Compatibility Fallbacks

- **OpenAI Codex CLI Fallback**: Khi `Accept` header chứa `text/event-stream` hoặc `User-Agent` chứa `codex`, gateway tự động kích hoạt chế độ streaming event translator tương thích chuẩn Responses API.
- **Malformed Tool Arguments Repair**: Gateway tự động sửa cú pháp JSON bị lỗi từ mô hình (loại bỏ markdown fence, sửa trailing comma, đóng ngoặc bị cắt ngắn) trước khi chuyển sang lớp thực thi công cụ.

