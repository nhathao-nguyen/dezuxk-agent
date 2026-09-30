# Thiết Kế Kỹ Thuật: Cursor Autonomous Agent & Intelligence Engine

- **Mã thiết kế:** `SPEC-2026-09-30-CURSOR-AGENT`
- **Tác giả:** Dezuxk Core Team & Primary Coding Agent
- **Ngày lập:** 30/09/2026
- **Trạng thái:** Approved by User (Sẵn sàng lập kế hoạch triển khai)

---

## 1. Bối Cảnh & Vấn Đề (Context & Problem Statement)

Khi người dùng tích hợp Dezuxk AI Gateway vào **Cursor IDE** (chế độ Chat, Composer và Agent Mode) để lập trình, họ gặp phải tình trạng Agent hoạt động kém hiệu quả ("ngoo"):
1. **Thiếu Tool Calling / Function Calling (`tools` & `tool_calls`)**: Cursor Agent gửi danh mục công cụ (`read_file`, `write_file`, `run_terminal_command`). Vì Gateway chưa hỗ trợ cấu trúc này, Gemini chỉ trả lời bằng văn bản markdown thuần mà không thể tự động đọc, sửa file hay thực thi lệnh trên IDE.
2. **Thiếu `reasoning_content`**: Khối suy nghĩ ẩn của Gemini không được truyền tải vào trường `delta.reasoning_content`, khiến Cursor không hiển thị được khối tư duy "Thinking..." phục vụ phân tích thuật toán và tìm lỗi.
3. **Chưa hỗ trợ `role: "tool"`**: Khi Cursor thực thi xong công cụ và gửi lại kết quả, Gateway chưa có định dạng ngữ cảnh tối ưu để Gemini nắm bắt kết quả vừa chạy.
4. **Bộ phân giải Model cứng nhắc**: Gõ các tên biến thể phổ biến trong Cursor (`gemini-3.1-pro-thinking`, `gemini-2.5-flash`, `default`, `gpt-4o`) dẫn đến lỗi `HTTP 400 - không có mô hình này`.
5. **Ưu tiên mô hình thực tế**: Người dùng xác nhận dòng **Flash (`gemini-3.8-flash`)** có tốc độ phản hồi nhanh, ít lan man và theo sát chỉ dẫn lập trình tốt hơn dòng Pro trong các tác vụ coding hàng ngày, do đó cần đặt **Flash làm mô hình mặc định**.

---

## 2. Mục Tiêu Thiết Kế (Design Goals)

* **G-1 (Tool Calling Emulation)**: Hỗ trợ 100% chuẩn OpenAI Tool Calling (`tools`, `tool_choice`, `tool_calls`) cho cả chế độ Sync và Real-time SSE Streaming.
* **G-2 (Dual-Channel Thinking)**: Tự động trích xuất các khối suy luận CoT (Chain-of-Thought) của Gemini và stream vào trường `delta.reasoning_content` (chuẩn Cursor / DeepSeek-R1) và `message.reasoning_content`.
* **G-3 (Smart-by-Default)**: Mặc định luôn tự động kích hoạt **Extended Thinking** và **Google Search Grounding** trong mọi request mà không cần người dùng phải bật cờ thủ công.
* **G-4 (Flash-First & Resilient Model Routing)**: Đặt `gemini-3.8-flash` làm mô hình mặc định; hỗ trợ song song `gemini-3.1-pro`; tự động ánh xạ mọi biến thể tên mô hình từ Cursor về mô hình phù hợp và không bao giờ báo lỗi 400 do tên model lạ.
* **G-5 (Tool History Context)**: Tự động chuyển đổi các tin nhắn `role: "tool"` và `role: "function"` thành ngữ cảnh rõ ràng `[Tool Result (<id>)]: ...` để Gemini hiểu và tiếp tục giải quyết vấn đề.

---

## 3. Kiến Trúc Chi Tiết (Detailed Architecture)

```
Cursor IDE (Chat / Composer / Agent Mode)
    │
    │  [OpenAIChatRequest: messages, tools, tool_choice, stream=true, model="..."]
    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        DEZUXK AI GATEWAY                               │
│                                                                        │
│  [1. ModelRouter] ────────► Định tuyến model (Flash làm Default)        │
│                             Tự động bật Thinking & Search Grounding    │
│                                                                        │
│  [2. ToolCompiler] ───────► Dịch OpenAI `tools` JSON Schema            │
│                             thành định dạng XML <tools> & hướng dẫn    │
│                                                                        │
│  [3. ContextFlattener] ───► Xử lý `role: "tool"` & System Instructions │
│                             Đóng gói thành ngữ cảnh trực quan          │
│                                                                        │
│  [4. Gemini Upstream] ────► Gửi vector StreamGenerate lên Google       │
│                                                                        │
│  [5. Multi-Channel StreamDemuxer]                                      │
│       ├── Kênh 1: Thought Blocks  ──► delta.reasoning_content          │
│       ├── Kênh 2: <tool_call>     ──► delta.tool_calls                 │
│       └── Kênh 3: Text thường     ──► delta.content                    │
└────────────────────────────────────────────────────────────────────────┘
    │
    │  [OpenAI SSE Stream / JSON Response]
    ▼
Cursor IDE hiển thị khối "Thinking...", tự động gọi read_file, edit_file!
```

---

## 4. Các Thành Phần Cốt Lõi (Core Components)

### 4.1. Mở rộng Domain Models (`internal/core/domain/chat.go`)

Bổ sung các trường chuẩn OpenAI:

```go
type OpenAITool struct {
    Type     string             `json:"type"` // "function"
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
    Type     string                 `json:"type"` // "function"
    Function OpenAIFunctionCallData `json:"function"`
}

type OpenAIFunctionCallData struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"`
}

// Cập nhật OpenAIChatRequest
type OpenAIChatRequest struct {
    // ... các trường cũ ...
    Tools      []OpenAITool `json:"tools,omitempty"`
    ToolChoice any          `json:"tool_choice,omitempty"`
}

// Cập nhật OpenAIMessage
type OpenAIMessage struct {
    Role             string           `json:"role"`
    Content          string           `json:"content"`
    ReasoningContent string           `json:"reasoning_content,omitempty"`
    ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
    ToolCallID       string           `json:"tool_call_id,omitempty"`
    // ...
}

// Cập nhật OpenAIDelta
type OpenAIDelta struct {
    Role             string           `json:"role,omitempty"`
    Content          string           `json:"content,omitempty"`
    ReasoningContent string           `json:"reasoning_content,omitempty"`
    Thinking         string           `json:"thinking,omitempty"`
    ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
    // ...
}
```

### 4.2. Bộ Biên Dịch Công Cụ (`ToolCompiler`)

Khi `len(req.Tools) > 0`, `ToolCompiler` sẽ tạo ra một phần System Instruction chuyên biệt:

```text
# Available Tools
You have access to the following tools in this workspace:
<tools>
{"type":"function","function":{"name":"read_file","description":"Read file content","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}
...
</tools>

# Tool Calling Rules
1. To invoke a tool, output strictly in this XML format:
<tool_call>
{"name": "tool_name", "arguments": {"param1": "val1"}}
</tool_call>
2. You can provide explanations before or after a tool call.
3. If no tool is needed, respond directly with text.
```

### 4.3. Xử Lý Tin Nhắn Tool (`ContextFlattener`)

Trong `FlattenMessagesForModel`:
* Với `role == "tool"` hoặc `role == "function"`:
  ```text
  [Tool Result (call_id: <m.ToolCallID>)]:
  <m.Content>
  ```
* Với `m.ToolCalls != nil`:
  ```text
  Assistant: [Invoked Tool <tool_name> with arguments: <args>]
  ```

### 4.4. Bộ Giải Mã Đa Kênh (`StreamDemuxer` & `SyncDemuxer`)

Khi Gemini stream phản hồi:
1. **Khối Thought (`thought_content`)**:
   * Trích xuất nội dung suy nghĩ $\rightarrow$ gán vào `delta.ReasoningContent`.
   * Gửi chunk này về Cursor $\rightarrow$ Cursor hiển thị thanh Thinking.
2. **Khối Tool Call (`<tool_call>...</tool_call>`)**:
   * Sử dụng parser bóc tách thẻ `<tool_call>`.
   * Giải mã JSON: `name` và `arguments`.
   * Tạo `OpenAIToolCall` với ID ngẫu nhiên `call_<uuid>`.
   * Stream về Cursor với `delta.ToolCalls = [...]`.
   * Đồng thời đặt `FinishReason = "tool_calls"`.
3. **Khối Văn bản thường**:
   * Stream vào `delta.Content` như thông thường.

### 4.5. Bộ Định Tuyến Thông Minh (`ModelRouter`)

Triết lý **Flash-First & Smart-by-Default**:
1. Nếu `req.Thinking == nil` $\rightarrow$ Mặc định gán `true`.
2. Nếu `req.SearchGrounding == nil` $\rightarrow$ Mặc định gán `true`.
3. Phân giải tên mô hình:
   * Chứa `pro` $\rightarrow$ Chọn `gemini-3.1-pro` (Tier 3).
   * Mặc định (hoặc chứa `flash`, tên rỗng, `default`, `gpt-4o`, `cursor-small`...) $\rightarrow$ Chọn `gemini-3.8-flash` (Tier 1).
   * Cập nhật danh mục `/v1/models` hiển thị cả `gemini-3.8-flash`, `gemini-3.8-flash-thinking`, `gemini-3.1-pro`, `gemini-3.1-pro-thinking`.

---

## 5. Chiến Lược Kiểm Thử (Testing Strategy)

1. **Unit Tests**:
   * `TestToolCompiler_Format`: Kiểm tra biên dịch mảng `tools` thành XML `<tools>`.
   * `TestStreamDemuxer_ToolCallExtraction`: Kiểm tra bóc tách `<tool_call>` từ stream chunks (cả chunk nguyên vẹn lẫn chunk bị phân mảnh).
   * `TestStreamDemuxer_ReasoningContent`: Kiểm tra bóc tách `thought_content` vào `delta.ReasoningContent`.
   * `TestModelRouter_DefaultFlash`: Kiểm tra fallback thông minh về `gemini-3.8-flash`.
   * `TestFlattenMessages_ToolRole`: Kiểm tra định dạng `role: "tool"`.
2. **Integration Tests**:
   * Gửi request mô phỏng Cursor Agent (kèm `tools: [read_file]`) qua `/v1/chat/completions` (Sync và Stream SSE) và xác nhận output có `tool_calls` hợp lệ.
3. **Live System Verification**:
   * Kết nối Gateway với Cursor IDE thực tế để xác nhận Agent tự động đọc/ghi file và hiển thị Thinking.

---

## 6. Kế Hoạch Triển Khai (Next Steps)

Sau khi tài liệu thiết kế này được duyệt:
1. Lưu tài liệu thiết kế và commit vào repository.
2. Kích hoạt kỹ năng `writing-plans` để xây dựng kế hoạch thực thi từng bước (Implementation Plan).
3. Triển khai theo quy trình Test-Driven Development (TDD).
