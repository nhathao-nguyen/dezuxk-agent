# Module 03: Chuẩn Hóa API Tương Thích OpenAI (OpenAI Compatibility Layer)

> Tài liệu đặc tả các cổng giao tiếp chuẩn hóa theo chuẩn OpenAI API (`/v1/*`), giúp tích hợp trực tiếp vào mọi thư viện AI, Extension, và ứng dụng Client (NextChat, Cursor, LibreChat, Python `openai` SDK...).

---

## 1. Danh Mục Mô Hình (`GET /v1/models`)

Cung cấp danh sách các mô hình ảo mà Gateway hỗ trợ, ánh xạ tương ứng vào các backend Gemini và Flow:

```http
GET /v1/models HTTP/1.1
Host: localhost:8080
Authorization: Bearer <GATEWAY_API_KEY>
```

### Phản Hồi Chuẩn OpenAI:
```json
{
  "object": "list",
  "data": [
    {
      "id": "gemini-3.8-flash",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google",
      "permission": [],
      "root": "gemini-3.8-flash",
      "parent": null
    },
    {
      "id": "gemini-3.5-flash-lite",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google"
    },
    {
      "id": "gemini-3.1-pro",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google"
    },
    {
      "id": "gemini-thinking",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google"
    },
    {
      "id": "veo-3.1-quality",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google-flow"
    },
    {
      "id": "veo-3.1-fast",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google-flow"
    },
    {
      "id": "veo-3.1-lite",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google-flow"
    },
    {
      "id": "abra-imagen-3",
      "object": "model",
      "created": 1790170000,
      "owned_by": "google-flow"
    }
  ]
}
```

---

## 2. Trò Chuyện & Suy Luận (`POST /v1/chat/completions`)

### 2.1. Cấu Trúc Yêu Cầu Đầu Vào (Request Payload)

```json
{
  "model": "gemini-3.8-flash",
  "messages": [
    {
      "role": "system",
      "content": "Bạn là chuyên gia phân tích dữ liệu cao cấp."
    },
    {
      "role": "user",
      "content": "Hãy tóm tắt ưu điểm của kiến trúc Event-Driven."
    }
  ],
  "stream": true,
  "temperature": 0.7
}
```

### 2.2. Ánh Xạ Tham Số OpenAI Sang Google Gemini Payload

Trong Golang, Gateway chuyển đổi `messages` sang mảng 2 tầng Google:

```go
package handlers

import (
	"strings"
)

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature"`
}

// ConvertMessagesToPrompt tổng hợp system prompt và lịch sử hội thoại thành chuỗi prompt gửi lên Gemini
func ConvertMessagesToPrompt(messages []OpenAIMessage) (systemPrompt string, lastUserPrompt string, conversationHistory string) {
	var historyBuilder strings.Builder
	for i, msg := range messages {
		switch msg.Role {
		case "system":
			systemPrompt = msg.Content
		case "user":
			if i == len(messages)-1 {
				lastUserPrompt = msg.Content
			} else {
				historyBuilder.WriteString("User: " + msg.Content + "\n")
			}
		case "assistant":
			historyBuilder.WriteString("Assistant: " + msg.Content + "\n")
		}
	}

	// Ghép System Prompt vào đầu câu hỏi nếu có
	if systemPrompt != "" {
		lastUserPrompt = "[System Instruction: " + systemPrompt + "]\n\n" + lastUserPrompt
	}

	return systemPrompt, lastUserPrompt, historyBuilder.String()
}
```

### 2.3. Định Dạng Luồng Phản Hồi Streaming (SSE - `stream: true`)

Khi `stream: true`, Gateway trả về Header:
```http
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

Mỗi chunk được định dạng chuẩn JSON của OpenAI:
```text
data: {"id":"chatcmpl-c37442807c1368987","object":"chat.completion.chunk","created":1790171200,"model":"gemini-3.8-flash","choices":[{"index":0,"delta":{"content":"Ưu "},"finish_reason":null}]}

data: {"id":"chatcmpl-c37442807c1368987","object":"chat.completion.chunk","created":1790171200,"model":"gemini-3.8-flash","choices":[{"index":0,"delta":{"content":"điểm "},"finish_reason":null}]}

data: {"id":"chatcmpl-c37442807c1368987","object":"chat.completion.chunk","created":1790171200,"model":"gemini-3.8-flash","choices":[{"index":0,"delta":{"content":"cốt lõi..." },"finish_reason":null}]}

data: {"id":"chatcmpl-c37442807c1368987","object":"chat.completion.chunk","created":1790171200,"model":"gemini-3.8-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

```

### 2.4. Phản Hồi Non-Streaming (`stream: false`)

Khi `stream: false`, Gateway dồn toàn bộ các chunk text từ luồng `wrb.fr` cho đến khi kết thúc và trả về JSON:

```json
{
  "id": "chatcmpl-c37442807c1368987",
  "object": "chat.completion",
  "created": 1790171200,
  "model": "gemini-3.8-flash",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Kiến trúc Event-Driven sở hữu các ưu điểm chính gồm: 1. Khả năng mở rộng độc lập (Decoupled Scalability)..."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 45,
    "completion_tokens": 128,
    "total_tokens": 173
  }
}
```

---

## 3. Tạo Hình Ảnh Tương Thích DALL-E (`POST /v1/images/generations`)

### 3.1. Cấu Trúc Yêu Cầu

```json
{
  "model": "abra-imagen-3",
  "prompt": "A futuristic sports car neon reflections rainy road octane render",
  "n": 1,
  "size": "1024x1024",
  "response_format": "url"
}
```

### 3.2. Luồng Xử Lý Nội Bộ

1. Gateway nhận request và định tuyến tới mô hình **Abra** trên Flow Studio (hoặc Gemini Imagen 3).
2. Gọi hàm thực thi qua RPC `FlowCreationAgentService/StreamChat` với payload đã được kiểm chứng trong `docs-2/flow/image_generation.md`.
3. Trích xuất Pre-signed URL `https://lh3.googleusercontent.com/ai-sandbox/...`.
4. Kích hoạt module **Media Storage**: Tải file ảnh về đĩa cứng nội bộ và tạo mã asset ID duy nhất (ví dụ: `img_8f29d10e`).
5. Trả về cho client đường dẫn vĩnh viễn trên Gateway:

```json
{
  "created": 1790171300,
  "data": [
    {
      "url": "http://localhost:8080/v1/media/img_8f29d10e.png"
    }
  ]
}
```

---

## 4. Tổng Hợp Giọng Đọc Tương Thích Audio (`POST /v1/audio/speech`)

### 4.1. Cấu Trúc Yêu Cầu

```json
{
  "model": "gemini-tts",
  "input": "Xin chào, đây là giọng đọc tổng hợp chất lượng cao qua Google Gateway.",
  "voice": "Achernar",
  "response_format": "mp3"
}
```

### 4.2. Ánh Xạ Giọng Đọc (Voice Mapping)

Gateway hỗ trợ:
1. **Giọng chuẩn Gemini:** Sử dụng RPC `whPPme` với ngôn ngữ `vi`, `en`.
2. **30 Giọng Đọc Flow Personas:** Ánh xạ trường `voice` sang danh sách 30 nhân vật đã xác minh (`Achernar`, `Altair`, `Alioth`, `Fenrir`...) lấy từ RPC `Zzl0ze`.
3. Dữ liệu âm thanh được stream trực tiếp về client với `Content-Type: audio/mpeg` hoặc `audio/wav`.
