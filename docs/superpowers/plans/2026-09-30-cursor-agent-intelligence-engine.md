# Cursor Autonomous Agent & Intelligence Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Trang bị khả năng Autonomous Tool Calling (Function Calling chuẩn OpenAI), luồng tư duy sâu (`reasoning_content`), triết lý Smart-by-Default (luôn bật Thinking & Search Grounding), và định tuyến ưu tiên dòng Flash cho Cursor IDE.

**Architecture:** Mở rộng domain OpenAI chat với các struct công cụ (`OpenAITool`, `OpenAIToolCall`). Xây dựng `ToolCompiler` để dịch JSON Schema sang chỉ dẫn XML `<tools>` cho Gemini. Nâng cấp `FlattenMessagesForModel` để định dạng kết quả từ `role: "tool"`. Triển khai `StreamDemuxer` bóc tách 3 luồng dữ liệu song song (`reasoning_content`, `tool_calls`, `content`). Tối ưu `ModelRouter` với Flash làm mô hình mặc định và fallback thông minh.

**Tech Stack:** Go 1.22+, Chi Router, Gorilla WebSocket, SSE Stream, Google lamda Frontend Service RPC.

**Spec:** `docs/superpowers/specs/2026-09-30-cursor-agent-intelligence-engine-design.md`

## Global Constraints
- Tuân thủ 100% chuẩn OpenAI Chat Completions API (`/v1/chat/completions`).
- Giữ nguyên cơ chế mã hóa AES-256-GCM Vault cho phiên lưu trữ trên đĩa.
- Zero-latency: Không tạo thêm pass LLM phụ, xử lý bóc tách trực tiếp trên stream pipeline.
- Flash-First: Mọi tên model không xác định hoặc mặc định đều điều hướng về `gemini-3.8-flash`.

---

### Task 1: Mở Rộng Domain Models Cho Tool Calling & Reasoning Content

**Files:**
- Modify: `internal/core/domain/chat.go:60-230`
- Test: `internal/core/domain/chat_test.go`

**Interfaces:**
- Produces:
  ```go
  type OpenAITool struct {
      Type     string            `json:"type"`
      Function OpenAIFunctionDef `json:"function"`
  }
  type OpenAIFunctionDef struct {
      Name        string          `json:"name"`
      Description string          `json:"description,omitempty"`
      Parameters  json.RawMessage `json:"parameters,omitempty"`
  }
  type OpenAIToolCall struct {
      Index    int                    `json:"index,omitempty"`
      ID       string                 `json:"id"`
      Type     string                 `json:"type"`
      Function OpenAIFunctionCallData `json:"function"`
  }
  type OpenAIFunctionCallData struct {
      Name      string `json:"name"`
      Arguments string `json:"arguments"`
  }
  ```
  Thêm `Tools []OpenAITool`, `ToolChoice any` vào `OpenAIChatRequest`.
  Thêm `ReasoningContent string`, `ToolCalls []OpenAIToolCall`, `ToolCallID string` vào `OpenAIMessage` và `OpenAIDelta`.

- [ ] **Step 1: Viết test kiểm tra serialization/deserialization của Tool structs**
  Viết test `TestOpenAIChatRequest_ToolSerialization` trong `internal/core/domain/chat_test.go` xác nhận parse được JSON có `tools`, `tool_calls`, `reasoning_content`.

- [ ] **Step 2: Chạy test để xác nhận test FAIL (chưa có trường)**
  Chạy: `go test -v -run TestOpenAIChatRequest_ToolSerialization ./internal/core/domain`
  Kỳ vọng: Compile error (trường chưa tồn tại).

- [ ] **Step 3: Cập nhật `internal/core/domain/chat.go` với các struct mới**
  Bổ sung `OpenAITool`, `OpenAIFunctionDef`, `OpenAIToolCall`, `OpenAIFunctionCallData`, cập nhật `OpenAIChatRequest`, `OpenAIMessage`, `OpenAIDelta`, `OpenAIChoice`.

- [ ] **Step 4: Chạy test để xác nhận test PASS**
  Chạy: `go test -v -run TestOpenAIChatRequest_ToolSerialization ./internal/core/domain`
  Kỳ vọng: PASS.

- [ ] **Step 5: Commit Task 1**
  ```bash
  git add internal/core/domain/chat.go internal/core/domain/chat_test.go
  git commit -m "feat(domain): add tool calling and reasoning_content fields to chat models"
  ```

---

### Task 2: Bộ Biên Dịch Công Cụ (`ToolCompiler`)

**Files:**
- Create: `internal/core/domain/tool_compiler.go`
- Create: `internal/core/domain/tool_compiler_test.go`

**Interfaces:**
- Produces:
  ```go
  func CompileToolsInstruction(tools []OpenAITool) string
  ```
  Nhận danh sách `tools` và xuất ra chỉ dẫn hệ thống chứa khối `<tools>...</tools>` kèm quy tắc xuất `<tool_call>{"name":"...","arguments":{...}}</tool_call>`.

- [ ] **Step 1: Viết failing test cho `CompileToolsInstruction`**
  Viết `TestCompileToolsInstruction` trong `internal/core/domain/tool_compiler_test.go` kiểm tra định dạng XML `<tools>` và quy tắc xuất `<tool_call>`.

- [ ] **Step 2: Chạy test để xác nhận FAIL**
  Chạy: `go test -v -run TestCompileToolsInstruction ./internal/core/domain`
  Kỳ vọng: Compile error (chưa có function).

- [ ] **Step 3: Cài đặt `internal/core/domain/tool_compiler.go`**
  Thực hiện hàm `CompileToolsInstruction(tools []OpenAITool) string` đóng gói JSON Schema của từng function vào XML tags.

- [ ] **Step 4: Chạy test để xác nhận PASS**
  Chạy: `go test -v -run TestCompileToolsInstruction ./internal/core/domain`
  Kỳ vọng: PASS.

- [ ] **Step 5: Commit Task 2**
  ```bash
  git add internal/core/domain/tool_compiler.go internal/core/domain/tool_compiler_test.go
  git commit -m "feat(domain): implement ToolCompiler for Gemini system instruction injection"
  ```

---

### Task 3: Nâng Cấp Ngữ Cảnh Hội Thoại Cho `role: "tool"` & Tool Calls

**Files:**
- Modify: `internal/core/domain/chat.go:280-350`
- Test: `internal/core/domain/chat_test.go`

**Interfaces:**
- Consumes: `FlattenMessagesForModel(messages []OpenAIMessage, modelID string) (system string, prompt string)`
- Produces: Nhận diện `role == "tool"` hoặc `role == "function"`, định dạng thành `[Tool Result (call_id)]: <content>`. Ghi nhận `Assistant: [Invoked Tool <name>]` khi `m.ToolCalls` xuất hiện trong lịch sử.

- [ ] **Step 1: Viết test cho `FlattenMessagesForModel` với tool messages**
  Viết `TestFlattenMessages_WithToolCallsAndResults` trong `internal/core/domain/chat_test.go`.

- [ ] **Step 2: Chạy test để xác nhận FAIL**
  Chạy: `go test -v -run TestFlattenMessages_WithToolCallsAndResults ./internal/core/domain`
  Kỳ vọng: FAIL (định dạng cũ chưa có nhãn `[Tool Result ...]`).

- [ ] **Step 3: Cập nhật hàm `FlattenMessagesForModel` trong `internal/core/domain/chat.go`**
  Xử lý case `role == "tool"` và `role == "function"` kèm `ToolCallID`.

- [ ] **Step 4: Chạy test để xác nhận PASS**
  Chạy: `go test -v -run TestFlattenMessages_WithToolCallsAndResults ./internal/core/domain`
  Kỳ vọng: PASS.

- [ ] **Step 5: Commit Task 3**
  ```bash
  git add internal/core/domain/chat.go internal/core/domain/chat_test.go
  git commit -m "feat(domain): format tool role messages and assistant tool invocations in context flattener"
  ```

---

### Task 4: Bộ Giải Mã Đa Kênh Stream & Sync (`StreamDemuxer`)

**Files:**
- Create: `internal/core/services/tool_parser.go`
- Create: `internal/core/services/tool_parser_test.go`
- Modify: `internal/core/services/chat_service.go:300-470`
- Test: `internal/core/services/chat_service_test.go`

**Interfaces:**
- Produces:
  ```go
  type ParsedStreamChunk struct {
      Content          string
      ReasoningContent string
      ToolCalls        []domain.OpenAIToolCall
      HasToolCall      bool
  }
  func ExtractToolCalls(rawText string) (cleanContent string, toolCalls []domain.OpenAIToolCall)
  ```
  Xử lý thẻ `<tool_call>{"name": "...", "arguments": {...}}</tool_call>` và bóc tách `thought_content` vào `delta.ReasoningContent`.

- [ ] **Step 1: Viết test cho `ExtractToolCalls` bóc tách tool call và text thông thường**
  Viết `TestExtractToolCalls` trong `internal/core/services/tool_parser_test.go`.

- [ ] **Step 2: Chạy test để xác nhận FAIL**
  Chạy: `go test -v -run TestExtractToolCalls ./internal/core/services`
  Kỳ vọng: FAIL.

- [ ] **Step 3: Cài đặt `internal/core/services/tool_parser.go`**
  Thực hiện regex và JSON decoder để bóc tách thẻ `<tool_call>`.

- [ ] **Step 4: Chạy test parser PASS**
  Chạy: `go test -v -run TestExtractToolCalls ./internal/core/services`
  Kỳ vọng: PASS.

- [ ] **Step 5: Tích hợp vào `streamRound` và `syncRound` trong `chat_service.go`**
  - Đẩy khối suy nghĩ của Gemini vào `delta.ReasoningContent`.
  - Đẩy tool call vào `delta.ToolCalls`.
  - Nếu có `ToolCalls`, đặt `finish_reason = "tool_calls"`.

- [ ] **Step 6: Chạy kiểm thử toàn bộ `chat_service`**
  Chạy: `go test -v -run TestChatService ./internal/core/services`
  Kỳ vọng: PASS.

- [ ] **Step 7: Commit Task 4**
  ```bash
  git add internal/core/services/tool_parser.go internal/core/services/tool_parser_test.go internal/core/services/chat_service.go
  git commit -m "feat(services): implement tool parsing and multi-channel reasoning stream demuxer"
  ```

---

### Task 5: Triết Lý Smart-by-Default & Bộ Định Tuyến Flash-First

**Files:**
- Modify: `internal/core/services/chat_service.go:120-135`
- Modify: `internal/core/domain/catalog.go:1-50`
- Modify: `internal/core/domain/gemini_template.go:1-40`
- Test: `internal/core/services/chat_service_test.go`

**Interfaces:**
- Cập nhật `prepareModel`:
  - Luôn bật `EnableThinking = true` và `EnableSearchGrounding = true` trừ khi có cờ tắt tường minh.
  - Phân giải:
    - Nếu tên chứa `pro` $\rightarrow$ `gemini-3.1-pro`.
    - Mặc định mọi tên khác (`default`, `gpt-4o`, `flash`, `cursor-small`...) $\rightarrow$ `gemini-3.8-flash`.
  - Cập nhật danh mục `/v1/models` hiển thị:
    - `gemini-3.8-flash` (Primary Default)
    - `gemini-3.8-flash-thinking`
    - `gemini-3.1-pro`
    - `gemini-3.1-pro-thinking`

- [ ] **Step 1: Viết test cho Smart-by-Default và Flash-First routing**
  Viết `TestPrepareModel_FlashFirstAndSmartByDefault` trong `internal/core/services/chat_service_test.go`.

- [ ] **Step 2: Chạy test xác nhận FAIL**
  Chạy: `go test -v -run TestPrepareModel_FlashFirstAndSmartByDefault ./internal/core/services`
  Kỳ vọng: FAIL (tên lạ đang báo lỗi 400).

- [ ] **Step 3: Cập nhật `prepareModel` trong `chat_service.go` và catalog trong `catalog.go`**
  Thực hiện fallback thông minh về Flash và bật mặc định Thinking + Search Grounding.

- [ ] **Step 4: Chạy test xác nhận PASS**
  Chạy: `go test -v -run TestPrepareModel_FlashFirstAndSmartByDefault ./internal/core/services`
  Kỳ vọng: PASS.

- [ ] **Step 5: Commit Task 5**
  ```bash
  git add internal/core/services/chat_service.go internal/core/domain/catalog.go internal/core/domain/gemini_template.go internal/core/services/chat_service_test.go
  git commit -m "feat(routing): enforce Flash-First default routing and smart-by-default capabilities"
  ```

---

### Task 6: Kiểm Thử Tích Hợp Toàn Diện & Live Cursor Simulation

**Files:**
- Modify: `cmd/test_live_gateway/main.go`

- [ ] **Step 1: Cập nhật `cmd/test_live_gateway/main.go`**
  Thêm kịch bản test:
  1. Gửi request kèm `tools: [read_file]` đến `gemini-3.8-flash` với yêu cầu "Hãy dùng công cụ read_file để đọc file config.yaml".
  2. Xác minh Gateway trả về `tool_calls` hợp lệ với `name: "read_file"`.
  3. Gửi câu hỏi phức tạp yêu cầu tư duy, xác minh trường `reasoning_content` có dữ liệu.
  4. Gửi tên model lạ `cursor-test-model`, xác minh fallback về `gemini-3.8-flash` thành công.

- [ ] **Step 2: Chạy toàn bộ test suite dự án**
  Chạy: `go test -count=1 ./...`
  Kỳ vọng: PASS 100%.

- [ ] **Step 3: Build binary `dezuxk.exe` mới nhất**
  Chạy: `go build -v -o dezuxk.exe main.go`
  Kỳ vọng: Biên dịch thành công mã sạch.

- [ ] **Step 4: Commit & Push lên GitHub**
  ```bash
  git add cmd/test_live_gateway/main.go
  git commit -m "test(live): add integration verification for cursor tool calling and reasoning"
  git push origin main
  ```
