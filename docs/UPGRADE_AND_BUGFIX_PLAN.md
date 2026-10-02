# KẾ HOẠCH NÂNG CẤP VÀ KHẮC PHỤC LỖI TOÀN DIỆN (DEZUXK AI SERVER AGENT)
> **Mục tiêu**: Khắc phục triệt để các lỗi logic, loại bỏ mã cứng (hardcoded), nâng cấp kiến trúc Server Agent đạt chuẩn công nghiệp (State Machine, Workspace Sandbox, SSE Streaming, MCP Integration) và xác minh bằng dữ liệu/yêu cầu thực tế.

---

## 1. TỔNG QUAN & MỤC TIÊU DỰ ÁN

### 1.1. Hiện Trạng Dự Án
- **Phân hệ AI Gateway (`/v1/chat/completions`)**: Đang hoạt động tốt với Google Gemini thật (StreamGenerate, Thinking, Search Grounding, Code Interpreter, Vision SCOTTY Upload, SQLite Secret Vault WAL).
- **Phân hệ Agent ReAct**: Đang hoạt động tốt trong việc tạo dự án và chạy lệnh shell biên dịch cơ bản.
- **Phân hệ Agent Graph (`PLAN ➔ EXECUTE ➔ VERIFY ➔ FIX`)**: **Bị hư hỏng thực tế** do lỗi nhân đôi đường dẫn workspace (`Path Doubling`) và vòng lặp sửa lỗi mù (`Blind Fix Loop`).
- **Phân hệ Quản lý Workspace**: Bị gán cứng `.` trong daemon server, làm mất tính cô lập đa người dùng / đa dự án.
- **Phân hệ MCP Client**: Mã nguồn đã viết nhưng chưa được tích hợp vào runtime server.
- **Phân hệ Bộ Nhớ 3 Tầng**: Có nguy cơ lỗi cấu trúc giao thức khi nén ngữ cảnh hội thoại có chứa Tool Calling.

### 1.2. Mục Tiêu Nâng Cấp
1. **Khắc phục 100% các lỗi logic và điểm nghẽn**:
   - Sửa dứt điểm lỗi nhân đôi đường dẫn workspace trong `GraphEngine`.
   - Chặn đứng vòng lặp vô tận trong `nodeFix` khi mô hình từ chối hoặc không gọi công cụ sửa lỗi.
   - Động hóa toàn diện `WorkspaceContext` cho từng tác vụ Agent.
   - Bảo toàn tính hợp lệ cấu trúc tin nhắn (Message Sequence Validation) khi nén ngữ cảnh hội thoại dài.
2. **Loại bỏ mã viết cứng ("Hardcore Code")**:
   - Chuyển đổi các cờ "Smart Defaults" (`isThinking`, `isGrounding`, `isCodeInterpreter`) sang cấu hình linh hoạt.
   - Dọn dẹp triệt để tàn dư của dịch vụ cũ (`Flow`) khỏi database và domain model.
   - Tối ưu hóa kết nối Chrome CDP bằng cơ chế Pooling thay vì mở/đóng liên tục.
3. **Nâng cấp kiến trúc Server Agent theo chuẩn công nghiệp (Industry-Standard Architecture)**:
   - Bổ sung giao thức **Server-Sent Events (SSE) Real-Time Streaming** cho Agent (`/v1/agent/run/stream`).
   - Tích hợp chính thức **Model Context Protocol (MCP)** đa máy chủ (Multi-Server MCP Provider).
   - Cơ chế **Workspace Sandboxing** (kiểm tra an toàn đường dẫn, chống Path Traversal).
4. **Quy trình nghiệm thu thực nghiệm**: Mọi tính năng sau khi sửa phải được kiểm chứng bằng HTTP Request thật và dự án thực tế được tạo ra trên đĩa cứng.

---

## 2. QUY ĐỊNH KỸ THUẬT & NGUYÊN TẮC THIẾT KẾ

1. **Tuân thủ Clean Hexagonal Architecture**:
   - Tầng `domain` và `ports` không phụ thuộc vào `adapters` bên ngoài hay framework HTTP.
   - Mọi thay đổi logic nghiệp vụ của Agent phải nằm trong `internal/core/services/agent/`.
   - Mọi giao tiếp hạ tầng (Filesystem, Shell, CDP, MCP) phải nằm trong `internal/adapters/outbound/`.
2. **Nguyên tắc Workspace Context**:
   - Không được phép hardcode thư mục `.` ở bất kỳ tầng nào trong hệ thống Agent.
   - Mọi đường dẫn truyền vào công cụ tệp tin hoặc lệnh shell đều phải được phân giải dựa trên `WorkspaceContext` của phiên làm việc đó.
   - Tuyệt đối ngăn chặn Path Traversal (`../..`) vượt ra ngoài phạm vi thư mục được chỉ định trừ khi có cờ cho phép rõ ràng.
3. **Nguyên tắc State Machine Bền Vững (Resilient State Machine)**:
   - Một bước trong State Machine không được phép kết luận "hoàn thành" nếu không có bằng chứng thực thi (Evidence).
   - `nodeFix` bắt buộc phải có hành động sửa đổi tệp (thực thi tool) hoặc trả về lỗi cụ thể; không được phép ngộ nhận hoàn thành khi LLM chỉ trả về văn bản từ chối.
4. **Nguyên tắc Xác Minh Bằng Thực Tế (No Fake/Mock Testing)**:
   - Không dùng các file test giả lập (mock unit test) để khẳng định hệ thống chạy được.
   - Mọi tiêu chí hoàn thành phải được kiểm tra qua: Khởi động server thật, gửi HTTP request thật, thao tác file thật, biên dịch mã thật và chạy binary thật.

---

## 3. CHI TIẾT CÁC ĐIỂM CẦN SỬA & NÂNG CẤP

### GIAI ĐOẠN 1: KHẮC PHỤC CÁC LỖI LOGIC NGHIÊM TRỌNG

#### 3.1. Sửa Lỗi Nhân Đôi Đường Dẫn (Path Doubling) trong `GraphEngine`
- **File cần sửa**: `internal/core/services/agent/graph_engine.go` (các hàm `nodePlan` và `nodeVerify`).
- **Hiện trạng lỗi**:
  - `nodePlan` đưa `Workspace: storage/...` vào prompt ➔ LLM sinh lệnh `verification_cmd: "ls storage/.../file.go"`.
  - `nodeVerify` truyền `cwd = opts.Workspace` vào `run_command` ➔ PowerShell chạy `ls storage/...` bên trong `storage/...` làm đường dẫn bị nối đôi ➔ Lỗi không tìm thấy file.
- **Phương án khắc phục**:
  1. Trong System Prompt của `nodePlan`: Chỉ định rõ quy ước: *"All file paths and verification commands MUST be relative to the Workspace root (e.g. use 'ls file.go' or 'go test ./...', DO NOT prefix with the workspace folder name)"*.
  2. Trong `nodeVerify`: Xây dựng hàm chuẩn hóa `SanitizeVerificationCommand(cmd string, workspace string) string`:
     - Nếu câu lệnh chứa tiền tố lặp lại của `workspace` (ví dụ `ls storage/demo/file.go` trong khi `cwd` là `storage/demo`), tự động loại bỏ tiền tố thừa để trở thành `ls file.go`.
     - Loại bỏ các lệnh `cd <workspace> &&` thừa nếu `cwd` đã được trỏ thẳng tới thư mục đó.

#### 3.2. Sửa Vòng Lặp Sửa Lỗi Mù (Blind Fix Loop) trong `nodeFix` & `Runner`
- **File cần sửa**:
  - `internal/core/services/agent/graph_engine.go` (`nodeFix`).
  - `internal/core/services/agent/runner.go` (`Run`).
- **Hiện trạng lỗi**:
  - Khi `nodeVerify` báo lỗi, prompt đưa vào `nodeFix` bằng tiếng Việt khiến mô hình Gemini từ chối trả lời ("Tôi chỉ là mô hình ngôn ngữ...").
  - `runner.Run` thấy không có `tool_calls` nên kết luận `is_completed = true`.
  - `GraphEngine` quay lại `nodeVerify` mà thực chất chưa có file nào được sửa ➔ Lặp vô tận 4 lần rồi fail.
- **Phương án khắc phục**:
  1. Trong `runner.Run`: Bổ sung kiểm tra cờ `RequireToolCall` hoặc ngữ cảnh nhiệm vụ sửa lỗi. Nếu mục tiêu là `nodeFix` mà mô hình không gọi công cụ sửa tệp (`replace_file_content` hoặc `write_file`), không được đánh dấu `is_completed = true`, mà phải chuyển sang trạng thái cảnh báo hoặc yêu cầu mô hình thử lại với chỉ dẫn kỹ thuật bằng tiếng Anh rõ ràng hơn.
  2. Trong `nodeFix`: Cải tiến System Prompt và User Prompt:
     - Giữ System Instruction bằng tiếng Anh chuẩn kỹ thuật (tránh kích hoạt bộ lọc từ chối của Gemini).
     - Định dạng rõ ràng: File bị lỗi, nội dung lỗi từ compiler/test runner, và danh sách các file đang có trong workspace.
     - Bắt buộc mô hình: *"You MUST use replace_file_content or write_file to patch the error. Plain text explanations without tool calls are strictly prohibited."*

#### 3.3. Động Hóa Workspace Cho Từng Request & Tách Khỏi Daemon Hardcode
- **File cần sửa**:
  - `internal/adapters/outbound/tools/fs_tools.go`
  - `internal/adapters/outbound/tools/shell_tools.go`
  - `internal/adapters/inbound/http/agent_handler.go`
  - `internal/app/daemon/daemon.go`
- **Hiện trạng lỗi**:
  - `daemon.go` gọi `tools.RegisterDefaultTools(toolRegistry, ".")` làm các tool file system bị gắn cứng thư mục `.` vĩnh viễn.
  - Tham số `"workspace"` gửi lên từ client bị bỏ qua.
- **Phương án khắc phục**:
  1. Cấu trúc lại các tool: Chuyển sang cơ chế `ScopedToolRegistry` hoặc truyền `WorkspaceContext` qua `context.Context` (ví dụ `domain.WithWorkspaceContext(ctx, wsPath)`).
  2. Trong `resolvePath(workspace, userPath)`: Ưu tiên lấy workspace từ `context.Context` của từng request trước khi dùng fallback của registry.
  3. Bổ sung cơ chế bảo vệ (Path Sandboxing): Đảm bảo đường dẫn sau khi resolve luôn nằm bên trong thư mục workspace được cấp quyền, chặn đứng các nguy cơ Path Traversal độc hại.

#### 3.4. Bảo Toàn Tính Hợp Lệ Của Cặp Message Tool Calling Trong `CompactConversation`
- **File cần sửa**: `internal/core/services/agent/memory_service.go` (`CompactConversation`).
- **Hiện trạng lỗi**:
  - Cắt cứng `messages[1 : len(messages)-keepRecent]` có thể chém ngang giữa tin nhắn `assistant (tool_calls)` và tin nhắn `tool (result)`.
- **Phương án khắc phục**:
  1. Xây dựng hàm tìm điểm cắt an toàn `findSafeCompactionBoundary(messages []domain.OpenAIMessage, targetIdx int) int`:
     - Nếu `messages[targetIdx]` là tin nhắn có `role == "tool"`, dịch chuyển điểm cắt lùi về trước tin nhắn `assistant` chứa `tool_calls` tương ứng.
     - Nếu `messages[targetIdx]` là tin nhắn `assistant` có `tool_calls` mà các kết quả `tool` nằm sau đó, dịch chuyển điểm cắt tiến lên sau khi toàn bộ kết quả của tool đã hoàn tất.
  2. Đảm bảo mảng tin nhắn mới luôn giữ nguyên tính toàn vẹn của cặp `assistant.tool_calls ➔ tool.tool_call_id`.

#### 3.5. Tăng Cường Bộ Phân Giải JSON Kế Hoạch Trong `nodePlan`
- **File cần sửa**: `internal/core/services/agent/graph_engine.go` (`nodePlan`).
- **Phương án khắc phục**:
  1. Sử dụng các hàm tiền xử lý đã có trong `tool_parser.go`:
     ```go
     sanitized := sanitizeJSONStringLiterals(rawJSON)
     repaired := repairMalformedJSON(sanitized)
     ```
  2. Bổ sung fallback giải mã linh hoạt: Nếu mô hình trả về mảng các bước trực tiếp thay vì bọc trong `{"steps": [...]}` thì tự động chuyển đổi phù hợp, tránh rơi về 1-step fallback ngoài ý muốn.

---

### GIAI ĐOẠN 2: TỐI ƯU HÓA TÍNH LINH HOẠT & LOẠI BỎ HARDCODED

#### 3.6. Cấu Hình Hóa Smart Defaults Trong `ChatService`
- **File cần sửa**:
  - `internal/config/config.go`
  - `internal/core/services/chat_service.go`
  - `configs/config.yaml`
- **Phương án khắc phục**:
  1. Bổ sung mục cấu hình vào `configs/config.yaml`:
     ```yaml
     chat_defaults:
       enable_thinking: true
       enable_search_grounding: false # Mặc định tắt, chỉ bật khi request yêu cầu
       enable_code_interpreter: false  # Mặc định tắt, chỉ bật khi request yêu cầu
     ```
  2. Trong `chat_service.go`: Đọc từ cấu hình thay vì gán cứng `true` cho mọi request. Điều này giúp giảm độ trễ phản hồi của Gateway từ 8s xuống ~1s đối với các câu hỏi chat thông thường.

#### 3.7. Dọn Dẹp Mã Nguồn Rác & Schema Dịch Vụ Cũ (`Flow`)
- **File cần sửa**:
  - `internal/adapters/outbound/session/sqlite_repo.go`
  - `internal/core/domain/model.go`
  - `internal/core/domain/session_state.go`
- **Phương án khắc phục**:
  1. Xóa bỏ các cột thừa trong database SQLite: `flow_sn_token`, `flow_project_id`, `flow_session_token`.
  2. Xóa các hằng số `ServiceFlow`, `DefaultFlowOrigin`, dọn sạch các câu lệnh `DELETE FROM session_alerts WHERE service = 'flow'`.

#### 3.8. Nâng Cấp Quản Lý Kết Nối Trình Duyệt Chrome CDP
- **File cần sửa**: `internal/adapters/outbound/tools/browser_tools.go`.
- **Phương án khắc phục**:
  1. Xây dựng cơ chế `PersistentCDPSession`: Duy trì một kết nối WebSocket duy nhất cho mỗi cổng CDP với cơ chế tự động kết nối lại (Auto-reconnect).
  2. Dùng Mutex bảo vệ luồng gửi/nhận JSON-RPC, tránh mở và đóng kết nối TCP/WebSocket liên tục cho từng hành động `navigate`, `evaluate`, `screenshot`.

---

### GIAI ĐOẠN 3: NÂNG CẤP KIẾN TRÚC SERVER AGENT CHUẨN CÔNG NGHIỆP

#### 3.9. Xây Dựng Endpoint SSE Real-Time Event Streaming (`/v1/agent/run/stream`)
- **File cần sửa/tạo mới**:
  - `internal/adapters/inbound/http/agent_handler.go`
  - `internal/adapters/inbound/http/router.go`
  - `internal/core/ports/agent_ports.go`
- **Phương án thiết kế**:
  1. Thêm endpoint `POST /v1/agent/run/stream` hỗ trợ Server-Sent Events (SSE).
  2. Thiết lập header HTTP: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`.
  3. Định nghĩa các kiểu sự kiện chuẩn theo triết lý LangGraph:
     - `event: run_start` (gửi task_id, model, workspace).
     - `event: node_change` (chuyển đổi giữa PLAN, EXECUTE, VERIFY, FIX, COMPLETE).
     - `event: thinking` (luồng suy luận từng token của mô hình).
     - `event: tool_start` (tên công cụ và tham số gọi).
     - `event: tool_end` (kết quả thực thi công cụ hoặc kiểm thử).
     - `event: checkpoint` (snapshot trạng thái lưu vào SQLite).
     - `event: run_complete` (kết quả cuối cùng và tổng kết).

#### 3.10. Tích Hợp Chính Thức Model Context Protocol (MCP) Vào Runtime
- **File cần sửa/tạo mới**:
  - `internal/config/config.go`
  - `internal/app/daemon/daemon.go`
  - `internal/adapters/outbound/mcp/client.go`
  - `configs/config.yaml`
- **Phương án thiết kế**:
  1. Bổ sung cấu hình `mcp_servers` vào YAML:
     ```yaml
     mcp_servers:
       filesystem:
         enabled: true
         command: "npx"
         args: ["-y", "@modelcontextprotocol/server-filesystem", "./storage"]
     ```
  2. Khi khởi động `daemon.go`: Duyệt danh sách cấu hình, gọi `StartStdioMCPClient` khởi chạy tiến trình con, nạp danh sách công cụ qua `client.ListTools(ctx)` và bọc thành `domain.AgentTool` đăng ký vào `ToolRegistry`.

---

## 4. KẾ HOẠCH NGHIỆM THU & BÀI TEST THỰC TẾ (REAL-FLOW VERIFICATION PROTOCOL)

> [!IMPORTANT]
> **Toàn bộ quá trình kiểm tra sau khi sửa lỗi phải chạy trên SERVER THẬT (`http://127.0.0.1:8080`), tương tác với GOOGLE GEMINI THẬT, tạo THƯ MỤC & FILE THẬT, biên dịch BINARY THẬT.**

### Kịch Bản Nghiệm Thu Chi Tiết:

#### Bài Test 1: Xác Minh Sửa Lỗi Nhân Đôi Đường Dẫn (Graph Workflow Pass 100%)
- **Mục tiêu**: Chứng minh `GraphEngine` hoàn thành trọn vẹn chu trình `PLAN ➔ EXECUTE ➔ VERIFY ➔ COMPLETE` mà không bị lỗi nhân đôi đường dẫn.
- **Hành động thực tế**:
  1. Gửi request thật: `POST http://127.0.0.1:8080/v1/agent/run` với:
     ```json
     {
       "goal": "Tạo module Go kiểm thử toán học trong thư mục storage/real_math_app gồm file math.go chứa hàm Multiply(a, b int) int và math_test.go. Chạy go test -v ./... để chứng minh test pass.",
       "workflow": "graph",
       "workspace": "storage/real_math_app",
       "model": "gemini-3.8-flash"
     }
     ```
  2. **Tiêu chí thành công**:
     - `nodePlan` tạo kế hoạch đúng chuẩn.
     - `nodeExecute` tạo thành công `math.go`, `math_test.go`, `go.mod`.
     - `nodeVerify` chạy lệnh kiểm thử chính xác bên trong `storage/real_math_app`, nhận `Exit Code: 0` và bằng chứng `PASS: TestMultiply`.
     - Trạng thái kết thúc: `success: true`, `current_node: "COMPLETE"`.

#### Bài Test 2: Xác Minh Khả Năng Tự Sửa Lỗi Có Kiểm Soát (Resilient Fix Node)
- **Mục tiêu**: Chứng minh khi gặp lỗi biên dịch thật, `nodeFix` tự động đọc lỗi, gọi công cụ `replace_file_content` hoặc `write_file` để sửa code và verify lại thành công.
- **Hành động thực tế**:
  1. Tạo sẵn một file Go bị lỗi cú pháp trong `storage/broken_app/main.go` (ví dụ cố tình thiếu dấu ngoặc).
  2. Gửi request thật: `POST /v1/agent/run` với `workflow: "graph"` yêu cầu: *"Sửa lỗi biên dịch trong storage/broken_app/main.go và chạy go build để chứng minh thành công."*
  3. **Tiêu chí thành công**:
     - `nodeVerify` lần 1 phát hiện lỗi biên dịch ➔ Chuyển `nodeFix`.
     - `nodeFix` gọi `read_file` đọc code lỗi, gọi `replace_file_content` sửa lỗi.
     - `nodeVerify` lần 2 chạy lại `go build` thành công `Exit Code: 0`.
     - `GraphEngine` chuyển sang `COMPLETE`.

#### Bài Test 3: Xác Minh Tính Cô Lập Đa Workspace (Multi-Workspace Isolation)
- **Mục tiêu**: Chứng minh hai tác vụ với hai workspace khác nhau không bị ghi đè hay lẫn lộn thư mục.
- **Hành động thực tế**:
  1. Gửi request 1 với `"workspace": "storage/project_alpha"`, tạo file `version.txt` ghi `"ALPHA_V1"`.
  2. Gửi request 2 với `"workspace": "storage/project_beta"`, tạo file `version.txt` ghi `"BETA_V1"`.
  3. Kiểm tra trực tiếp trên ổ cứng: Cả hai file đều nằm đúng thư mục riêng biệt, không có file nào rơi ra thư mục gốc `.`.

#### Bài Test 4: Xác Minh Real-time Event Streaming (`/v1/agent/run/stream`)
- **Mục tiêu**: Kiểm tra luồng SSE stream nhận sự kiện liên tục.
- **Hành động thực tế**:
  1. Dùng lệnh `curl -N` hoặc PowerShell StreamReader kết nối tới `/v1/agent/run/stream`.
  2. Ghi nhận các event: `node_change`, `tool_start`, `tool_end` được truyền về tức thời theo thời gian thực trước khi tác vụ kết thúc.

---

## 5. LỘ TRÌNH THỰC THI (ACTIONABLE ROADMAP)

```
[BƯỚC 1]: Sửa GraphEngine Path Sanitization & Fix Loop Logic
    ├── Sửa workspace path doubling trong nodePlan & nodeVerify
    ├── Thêm bộ lọc repairMalformedJSON cho Plan parsing
    └── Chặn ngộ nhận hoàn thành trong nodeFix & Runner
          ▼
[BƯỚC 2]: Cấu Trúc Lại Filesystem Tools Theo Scoped Workspace
    ├── Hỗ trợ WorkspaceContext động từ HTTP request
    └── Đặt chốt chặn bảo vệ Path Sandboxing
          ▼
[BƯỚC 3]: Tối Ưu Hóa Memory Compaction & Loại Bỏ Hardcoded
    ├── Đảm bảo ranh giới an toàn cho tool messages khi tóm tắt
    ├── Dọn dẹp tàn dư ServiceFlow
    └── Chuyển Smart Defaults của ChatService sang config YAML
          ▼
[BƯỚC 4]: Nâng Cấp Tính Năng Mới (SSE Streaming & MCP Integration)
    ├── Xây dựng endpoint /v1/agent/run/stream
    └── Kích hoạt StartStdioMCPClient trong Daemon
          ▼
[BƯỚC 5]: Nghiệm Thu Bằng Toàn Bộ Bài Test Thực Tế
    ├── Test Graph Workflow trên dự án thật
    ├── Test Tự sửa lỗi (Fix node) trên lỗi biên dịch thật
    └── Test Đa workspace và SSE Stream
```
