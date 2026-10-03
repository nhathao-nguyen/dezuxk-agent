# Review: Dezuxk Gateway (Gemini Web → OpenAI/Agent)

Commit đã kiểm tra: `86b0f3c` (worktree sạch). `go vet ./...` **PASS**, `go test ./...` **PASS** (kết quả cached).

---

## 1. Hiểu biết của bạn có đúng không?

**Về cơ bản là đúng**, nhưng có 3 điểm cần chỉnh:

1. **Gemini Web không phải là "LLM API server"**. Nó là web app dành cho người dùng, có RPC nội bộ (`StreamGenerate`, `batchexecute`). Endpoint này không cho bạn dùng function calling gốc. Vì vậy gateway phải **giả lập tool calling bằng prompt**:
   - Chèn danh sách tool + quy tắc định dạng vào prompt ([tool_compiler.go](file:///d:/nhathao/AI/dezuxk/internal/core/domain/tool_compiler.go)).
   - Phân tích văn bản model trả về để tìm `<tool_call>{...}</tool_call>` ([tool_parser.go](file:///d:/nhathao/AI/dezuxk/internal/core/services/tool_parser.go)).
   - Kiểm tra tên tool và schema ([tool_normalizer.go](file:///d:/nhathao/AI/dezuxk/internal/core/services/tool_normalizer.go)), rồi trả về `tool_calls` theo chuẩn OpenAI.
2. **"Xử lý tool calling" có 2 chế độ khác nhau**, repo của bạn có cả hai:

   | Chế độ | Ai chạy tool? | Endpoint |
   | :--- | :--- | :--- |
   | **OpenAI-compatible (tool chạy ở client)** | Client (Cursor, Cline, Codex…) chạy tool. Gateway chỉ chuyển text → `tool_calls` | `/v1/chat/completions`, `/v1/responses` |
   | **Agent chạy trên server** | Gateway tự chạy vòng ReAct và tool (fs, shell, browser, MCP) | `/v1/agent/*` |

   Để "đa nền tảng agent" thì chế độ **1** là quan trọng nhất: độ tin cậy của phần parse/convert quyết định mọi thứ.
3. **Rủi ro nền tảng**: dùng Gemini Web qua cookie là giao thức không chính thức. Google có thể đổi wire format bất cứ lúc nào, và tài khoản có thể bị giới hạn hoặc khóa (có thể vi phạm ToS). Nếu cần độ ổn định cấp production, hãy giữ sẵn một adapter dự phòng dùng **Gemini API chính thức**, vốn có function calling gốc.

---

## 2. Checklist: server "ổn định + hỗ trợ đa nền tảng agent"

| Nhóm | Cần kiểm tra |
| :--- | :--- |
| **Tuân thủ giao thức** | Chat Completions sync/stream; `tool_calls` delta có `index/id/type/function`; `finish_reason`; `usage` + `stream_options.include_usage`; định dạng lỗi OpenAI; `/v1/models`; Responses API (Codex); *(tùy chọn)* Anthropic `/v1/messages` cho Claude Code |
| **Độ tin cậy tool calling** | Tỷ lệ parse đúng, tên tool hợp lệ, args khớp schema; nhiều tool trong 1 lượt; `tool_choice` (`auto/none/required/function`); vòng lặp 10–30 lượt; tool result lớn (≥50KB); tự sửa khi gọi sai |
| **Golden test theo client** | OpenAI SDK Python/Node, Cursor, Cline/Roo, Continue, Codex CLI, LangChain/LlamaIndex, Vercel AI SDK, n8n |
| **Chịu lỗi upstream** | Xoay vòng tài khoản, refresh cookie/`SNlM0e`, xử lý 429, circuit breaker, **phát hiện drift giao thức**, timeout, SSE keep-alive |
| **Tải & soak** | N request đồng thời trên mỗi tài khoản, p50/p95 latency, chạy 24h kiểm tra rò rỉ memory/goroutine |
| **Bảo mật** | Scope của API key, CORS, secret trong image/log, rate limit, sandbox cho tool phía server |
| **Observability** | Metrics theo client/model/tool-parse-failure, trace ID, cảnh báo |

---

## 3. Phát hiện (theo mức độ ưu tiên)

### 🔴 H1: Code block ```json có key `"name"` bị "nuốt" thành tool call (ĐÃ XÁC NHẬN)

- **Vị trí**: [tool_parser.go:194-210](file:///d:/nhathao/AI/dezuxk/internal/core/services/tool_parser.go#L194-L210), [chat_service.go:652](file:///d:/nhathao/AI/dezuxk/internal/core/services/chat_service.go#L652)
- **Điều kiện kích hoạt**: model trả lời có code block ```json chứa `"name"`. Rất hay gặp: `package.json`, ví dụ JSON, manifest…
- **Kết quả probe**: input `package.json` mẫu → `clean="Đây là package.json mẫu:\n\nChúc bạn thành công."`. **Code block bị xóa khỏi nội dung.**
- **Hậu quả**:
  - Chế độ sync **không có tools**: `ExtractToolCalls` vẫn chạy, nên người dùng mất nội dung.
  - Chế độ sync **có tools**: validate báo lỗi `công cụ "my-app" không nằm trong danh sách`, và **cả request thất bại**.
- **Cách sửa**:
  - Chỉ gọi `ExtractToolCalls` khi `len(req.Tools) > 0`.
  - Chỉ dùng fallback Markdown khi `name` nằm trong danh sách tool được phép.
  - Chỉ xóa block khỏi text khi tool call đó **đã được chấp nhận**.

### 🔴 H2: Một tool call sai làm hỏng cả lượt (sync), còn stream thì bỏ qua im lặng (ĐÃ XÁC NHẬN)

- **Vị trí**: [tool_normalizer.go:259-271](file:///d:/nhathao/AI/dezuxk/internal/core/services/tool_normalizer.go#L259-L271), [chat_service.go:656-659](file:///d:/nhathao/AI/dezuxk/internal/core/services/chat_service.go#L656-L659), [stream_tool_filter.go:197-200](file:///d:/nhathao/AI/dezuxk/internal/core/services/stream_tool_filter.go#L197-L200)
- **Kết quả probe**: 1 lệnh `read_file` hợp lệ + 1 lệnh `list_dir` (tool không tồn tại) → `normalized=0, err=...`. Lệnh hợp lệ cũng bị bỏ.
- **Hậu quả**:
  - Sync: client agent nhận HTTP error.
  - Stream: client nhận `finish_reason: "stop"` với nội dung rỗng, nên agent "đứng" không rõ lý do.
  - Hai chế độ hành xử khác nhau.
- **Cách sửa**:
  - Lọc từng call: giữ call hợp lệ, gom lỗi của call sai.
  - Nếu không còn call hợp lệ, chạy **1 vòng tự sửa** ("Tool X không tồn tại / thiếu tham số Y, hãy gọi lại"). Đây là cách tăng độ tin cậy hiệu quả nhất cho prompt-based tool calling.
  - Nếu vẫn thất bại, trả về text gốc thay vì lỗi.

### 🔴 H3: Responses API (Codex CLI) làm mất lịch sử `function_call` của assistant

- **Vị trí**: [chat_handler.go:331-376](file:///d:/nhathao/AI/dezuxk/internal/adapters/inbound/http/chat_handler.go#L331-L376) (đọc source, chưa chạy thử)
- Item `type: "function_call"` (và `reasoning`) không được xử lý riêng. Chúng thành message `user` rỗng, rồi bị `FlattenMessages` bỏ đi. Model chỉ thấy *kết quả* tool mà không biết mình đã gọi gì, nên vòng lặp nhiều lượt của Codex kém ổn định.
- **Bị bỏ qua**: `tool_choice`, `parallel_tool_calls`, `reasoning.effort`, `max_output_tokens`, `previous_response_id`. Tool `type: "custom"` (freeform, ví dụ `apply_patch`) cũng chưa được hỗ trợ.
- Model không tồn tại thì **âm thầm chuyển sang model đầu tiên** thay vì trả 404 `model_not_found`.
- Lỗi trả về bằng `http.Error`, tức là content-type `text/plain`, không đúng chuẩn.

### 🟠 M1: Không có giới hạn context, toàn bộ lịch sử bị gộp lại mỗi lượt
- [chat.go:412-531](file:///d:/nhathao/AI/dezuxk/internal/core/domain/chat.go#L412-L531): mọi message và tool result (không giới hạn kích thước) được nối thành 1 prompt mỗi lượt (trừ khi client gửi `conversation_id`). Agent chạy lâu → prompt phình to → chậm, dễ vượt giới hạn input của Gemini web, tốn quota.
- **Cách sửa**: đặt token budget; cắt hoặc tóm tắt tool result cũ; giữ nguyên system + N lượt gần nhất; ưu tiên tiếp tục hội thoại trên server qua `conversation_id`.

### 🟠 M2: `/v1/chat/completions` stream không có SSE keep-alive
- Chỉ Responses API có `startKeepAlive` ([chat_handler.go:516](file:///d:/nhathao/AI/dezuxk/internal/adapters/inbound/http/chat_handler.go#L516)). Khi bật thinking hoặc có tools (content bị buffer), kết nối có thể im lặng hàng chục giây, dẫn đến client/proxy timeout.
- **Cách sửa**: gửi `: keep-alive\n\n` mỗi 10–15 giây cho đến chunk đầu tiên.

### 🟠 M3: Không giới hạn số request đồng thời trên mỗi tài khoản Google
- [memory_repo.go:199](file:///d:/nhathao/AI/dezuxk/internal/adapters/outbound/session/memory_repo.go#L199) chỉ *đếm* `InFlightReqs` để chấm điểm, không có trần. Khi agent hoặc subagent bắn song song, nhiều request dồn vào cùng một cookie session → 429 hoặc bị gắn cờ.
- **Cách sửa**: thêm `max_inflight_per_account` (ví dụ 1–2) và hàng đợi có timeout.

### 🟠 M4: Phát hiện drift giao thức đang tắt trên thực tế
- `server.log`: `[Golden Job Standby] Chưa có tài khoản lab… Bỏ qua đối soát`, lặp lại mỗi 5 phút. Với giao thức không chính thức, đây là **cảnh báo sớm duy nhất** khi Google đổi format.
- **Cách sửa**: thêm 1 profile có ID chứa `lab` và bật webhook alert.

### 🟠 M5: Dockerfile không khớp phiên bản Go và đóng gói cả secret
- [Dockerfile:3](file:///d:/nhathao/AI/dezuxk/Dockerfile#L3) dùng `golang:1.24-alpine`, nhưng [go.mod](file:///d:/nhathao/AI/dezuxk/go.mod) yêu cầu `go 1.25.0`. Image golang chính thức đặt `GOTOOLCHAIN=local` nên build gần như chắc chắn lỗi (chưa chạy Docker để xác nhận).
- [Dockerfile:37](file:///d:/nhathao/AI/dezuxk/Dockerfile#L37) `COPY configs/` và [.dockerignore](file:///d:/nhathao/AI/dezuxk/.dockerignore) không loại `configs/config.yaml` (file secret cục bộ, đã nằm trong `.gitignore`).

### 🟡 M6: Tag tool cố định dễ bị chèn ngược (prompt injection / nhầm lẫn)
- Tool result được chèn nguyên văn. Nếu agent đọc file có chứa chuỗi `<tool_call>` (ví dụ chính source của repo này) và model trích dẫn lại, stream filter có thể parse nhầm.
- **Cách sửa**: dùng tag có nonce ngẫu nhiên cho từng request (ví dụ `<tool_call_7f3a>`), và bọc tool result trong delimiter.

### 🟡 Mức thấp
- **CORS** ([router.go:154](file:///d:/nhathao/AI/dezuxk/internal/adapters/inbound/http/router.go#L154)): thiếu `X-Stainless-*`, `OpenAI-Beta`, `X-Request-ID`. Client chạy trong trình duyệt dùng OpenAI JS SDK có thể fail preflight (cần kiểm chứng).
- **Auth fallback** ([router.go:303-307](file:///d:/nhathao/AI/dezuxk/internal/adapters/inbound/http/router.go#L303-L307)): so sánh key bằng `!=` (không constant-time), lỗi không theo định dạng OpenAI.
- **Rate limiter** áp lên cả `/health`, `/ready`, `/metrics`; `/metrics` không có auth.
- **ID chunk**: `"chatcmpl-"+conversationID` có thể rỗng ở các chunk đầu, và trùng nhau giữa các lượt cùng hội thoại ([stream_tool_filter.go:76](file:///d:/nhathao/AI/dezuxk/internal/core/services/stream_tool_filter.go#L76)).
- **Định danh model** ([chat.go:385-396](file:///d:/nhathao/AI/dezuxk/internal/core/domain/chat.go#L385-L396)): match chuỗi `"pro"` nên `gemini-2.5-pro` sẽ tự nhận là "3.1 Pro".

---

## 4. Điểm đã làm tốt
- Kiến trúc hexagonal rõ ràng (domain / ports / adapters), test phủ rộng, vet sạch.
- Có kiểm tra tên tool và JSON Schema, xử lý đủ `tool_choice`, cache không áp dụng cho request có tools.
- Chế độ production chặn admin token mặc định, có vault AES-GCM, circuit breaker, retry kèm jitter.

## 5. Thứ tự đề xuất
1. **H1 + H2**: ảnh hưởng trực tiếp độ tin cậy với mọi agent client.
2. **H3**: nếu bạn dùng Codex CLI.
3. **M2, M3, M4**: độ ổn định khi chạy lâu và có tải.
4. **M1**: agent chạy dài hơi.
5. **M5, M6** và nhóm mức thấp.
6. Viết **bộ golden test theo từng client** (mục 2) và chạy định kỳ.

## 6. Giới hạn của review
- **Chưa chạy**: server thật với Gemini, Docker build, client thật (Cursor/Cline/Codex), load test.
- **H1, H2 đã xác nhận** bằng probe test chạy qua `go test -overlay`, không sửa repo. File probe: [zz_review_probe_test.go](file:///C:/Users/PC/.gemini/antigravity-ide/brain/94c8748a-01dd-4602-84e1-c52db034ac97/scratch/zz_review_probe_test.go).
- **Chưa xác nhận bằng test**: H3 và các mục M chỉ dựa trên đọc source hiện tại.
