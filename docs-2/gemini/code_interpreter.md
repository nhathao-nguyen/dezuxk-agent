# Chức năng: Thực Thi Code Python Trong Hộp Cát (Code Interpreter Sandbox)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của tính năng Code Interpreter (Hộp cát chạy mã Python) trên Google Gemini Web. Khi kích hoạt hoặc khi câu hỏi yêu cầu tính toán/vẽ đồ thị phức tạp, Gemini tự sinh mã Python, gửi tới môi trường gVisor/Borg sandbox trên máy chủ Google, thực thi và trả về kết quả thời gian thực gồm mã lệnh, đầu ra chuỗi và hình ảnh trực quan hóa.

---

## 1. Yêu Cầu Cơ Bản Của Request

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Headers Bắt Buộc:**
  ```http
  Origin: https://gemini.google.com
  Referer: https://gemini.google.com/app
  X-Same-Domain: 1
  User-Agent: <CLIENT_USER_AGENT>
  ```
* **Cookies Bắt Buộc:**
  ```http
  Cookie: __Secure-1PSID=<MASTER_ID>; __Secure-1PSIDTS=<TIME_TOKEN>; __Secure-1PSIDCC=<INTEGRITY_TOKEN>; COMPASS=<BOT_TOKEN>
  ```

---

## 2. Cấu Trúc Dữ Liệu Gửi Đi (Request Payload Schema)

* **Form Fields:**
  * `at`: `<CSRF_TOKEN_SNlM0e>`
  * `f.req`: `[null, JSON.stringify(INNER_ARRAY)]`

### Cấu trúc mảng vị trí `INNER_ARRAY` cho Code Interpreter:

```typescript
type CodeInterpreterInnerPayload = [
  // [0] Câu hỏi yêu cầu tính toán / phân tích dữ liệu / vẽ biểu đồ
  [
    [
      prompt_text: string,                      // Ví dụ: "Vẽ biểu đồ hình sin và tính tích phân từ 0 đến pi"
      input_type: 0,
      null,
      null,
      null,
      attachment_metadata: Array<[              // Tệp dữ liệu CSV/Excel tải lên (nếu có)
        [
          storage_token: string,                // Token trả về từ push.clients6.google.com
          mime_type: string                     // "text/csv", "application/vnd.ms-excel",...
        ],
        file_name: string
      ]> | null,
      0
    ],
    [locale: string],                           // ["vi"] hoặc ["en"]
    
    // [2] Ngữ cảnh hội thoại
    [
      conversation_id: string,                  // "c_<ID>"
      response_id: string,                      // "r_<ID>"
      choice_id: string,                        // "rc_<ID>"
      null, null, null, null, null, null,
      context_blob: string
    ],
    
    null, null, null, [0], 1, null, null, 1, 0, null, null, null, null, null,
    [[0]], 0, null, null, null, null, null, null, null, null, 1, null, null,
    
    // [27] Cờ Kích hoạt Công cụ Thực thi Mã Lệnh (Code Execution Sandbox Flag)
    [
      code_execution_enabled: 1                 // 1: Bắt buộc bật hộp cát Python / 0: Tự động
    ],
    
    null, null, null, null, null, null, null, null, null, null, [1],
    null, null, null, null, null, null, null, null, null, null, null, 0,
    null, null, null, null, null,
    
    // [46] Định danh phiên Client
    client_request_uuid: string,
    
    null, [], null, null, null, null, null, 0, 1, null, null, null, null, null,
    null, null, null, null, null,
    
    // [67] - [68] Model Tier
    model_tier_code: number,                    // 1: Flash / 3: Pro
    reasoning_mode: number,
    
    null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, 0, null, 1
  ]
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về (Response Wire Schema)

Khi mã Python được thực thi trong hộp cát, server trả về một khối cấu trúc đặc biệt phân định rõ giữa mã nguồn, kết quả in (`stdout/stderr`) và hình ảnh đầu ra từ matplotlib/seaborn.

### Cấu trúc JSON sau khi Parse Envelope `wrb.fr`:

```typescript
interface CodeInterpreterInnerResponse {
  // [1] State IDs
  1: [
    conversation_id: string,
    response_id: string
  ];

  // [4] Mảng phản hồi của Trợ lý
  4: Array<[
    choice_id: string,

    // [1] Khối nội dung chính
    [
      explanatory_text: string,                 // Lời giải thích tổng kết
      null, null, null, null, null, null
    ],

    null, null, null, null, null,

    // [10] Khối Thực Thi Mã Lệnh Sandbox (Code Execution Block)
    Array<{
      // 1. Mã nguồn Python được sinh ra
      code_block: {
        language: "python",
        code: string                            // Nội dung script Python (VD: "import matplotlib.pyplot as plt...")
      },

      // 2. Trạng thái và Kết quả Thực thi
      execution_result: {
        exit_code: number,                      // 0: Thành công, khác 0: Lỗi runtime
        execution_duration_ms: number,          // Thời gian thực thi trong container
        
        // Luồng xuất chuẩn văn bản
        stdout: string,                         # Chuỗi in ra từ print() hoặc kết quả expression
        stderr: string | null,                  # Thông báo lỗi (nếu có)
        
        // 3. Danh sách các biểu đồ / file hình ảnh do code tạo ra
        output_images: Array<{
          mime_type: "image/png" | "image/jpeg" | "image/svg+xml",
          image_format: "base64" | "cdn_url",
          data: string,                         // Chuỗi base64 hoặc URL: "https://lh3.googleusercontent.com/..."
          width?: number,
          height?: number
        }> | null,

        // 4. Các file dữ liệu phát sinh (CSV/Excel output)
        output_files: Array<{
          file_name: string,
          download_url: string,
          size_bytes: number
        }> | null
      }
    }>
  ]>;
}
```

---

## 4. Quy Trình Bóc Tách Thực Thi Mã Lệnh (Client Parser Algorithm)

Dưới đây là hàm xử lý trích xuất kết quả chạy Python từ luồng phản hồi nhị phân:

```typescript
function parseCodeExecutionPayload(innerJson: any) {
  const candidate = innerJson?.[4]?.[0];
  if (!candidate) return null;

  const executionContainer = candidate?.[10];
  if (!Array.isArray(executionContainer) || executionContainer.length === 0) {
    return { hasCodeExecution: false, text: candidate?.[1]?.[0] || "" };
  }

  const executions = executionContainer.map((item: any) => {
    const code = item?.code_block?.code || "";
    const res = item?.execution_result || {};
    
    return {
      language: item?.code_block?.language || "python",
      code: code,
      exitCode: res.exit_code,
      stdout: res.stdout || "",
      stderr: res.stderr || "",
      images: (res.output_images || []).map((img: any) => ({
        mimeType: img.mime_type,
        data: img.data,
        isBase64: img.image_format === "base64"
      })),
      files: res.output_files || []
    };
  });

  return {
    hasCodeExecution: true,
    executions: executions,
    finalText: candidate?.[1]?.[0] || ""
  };
}
```
