# OpenAI Compatibility & Capability Matrix

Tài liệu này xác định chi tiết ma trận tương thích giữa Dezuxk Agent Gateway và chuẩn giao thức OpenAI API (OpenAI Python SDK, Node.js SDK, Cursor, Cline, Roo Code, Codex CLI).

## 1. Endpoints Matrix

| Endpoint | Method | Hỗ trợ | Ghi chú tương thích |
| :--- | :--- | :---: | :--- |
| `/v1/chat/completions` | POST | **100%** | Hỗ trợ cả Synchronous và SSE Streaming (`text/event-stream`). |
| `/v1/responses` | POST | **100%** | Hỗ trợ OpenAI Responses API (dùng cho OpenAI Codex CLI). |
| `/v1/models` | GET | **100%** | Trả về danh sách mô hình dạng OpenAI standard. |
| `/ready` | GET | **100%** | Endpoint kiểm tra sức khỏe phụ thuộc (Database, Gemini Session). |

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
| **Error Format** | `{"error": {"message": ..., "type": ..., "code": ...}}` | Trả về đúng HTTP status code và payload chuẩn OpenAI SDK |

---

## 4. Documented Compatibility Fallbacks

- **OpenAI Codex CLI Fallback**: Khi `Accept` header chứa `text/event-stream` hoặc `User-Agent` chứa `codex`, gateway tự động kích hoạt chế độ streaming event translator tương thích chuẩn Responses API.
