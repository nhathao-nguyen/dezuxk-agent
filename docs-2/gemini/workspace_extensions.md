# Chức năng: Tiện Ích Mở Rộng Google Workspace (@Extensions Tooling)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) khi kích hoạt và gọi các tiện ích mở rộng hệ sinh thái Google (Extensions) trên Gemini Web thông qua cú pháp `@Gmail`, `@Google Drive`, `@YouTube`, `@Google Maps`. Khi được gọi, máy chủ Gemini thực hiện ủy quyền truy cập dữ liệu người dùng (OAuth Scopes ngầm), gọi API dịch vụ tương ứng và tổng hợp câu trả lời kèm siêu dữ liệu trích dẫn.

---

## 1. Yêu Cầu Cơ Bản

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookies Bắt Buộc:** `__Secure-1PSID`, `__Secure-1PSIDTS` (bắt buộc phải thuộc tài khoản đã cấp quyền truy cập Gmail/Drive tương ứng).

---

## 2. Cấu Trúc Request Kích Hoạt Extension (Request Schema)

Khi người dùng nhập tiền tố `@` hoặc khi câu lệnh tự động yêu cầu truy xuất dữ liệu cá nhân, mảng nội bộ `INNER_ARRAY` của `f.req` được gắn thêm cờ định tuyến Extension.

### Cấu trúc mảng vị trí `INNER_ARRAY`:

```typescript
type WorkspaceExtensionInnerPayload = [
  // [0] Câu lệnh chứa tiền tố hoặc chỉ thị
  [
    [
      prompt_text: string,                      // Ví dụ: "@Gmail Tìm các email gửi từ sếp tuần này" hoặc "@YouTube tìm video học Go"
      input_type: 0,
      null, null, null, null, 0
    ],
    [locale: string],                           // ["vi"]
    
    // [2] Ngữ cảnh hội thoại
    [conversation_id: string, response_id: string, choice_id: string, null, null, null, null, null, null, ""],
    
    null, null, null, [0], 1, null, null, 1, 0, null, null, null, null, null,
    [[0]], 0, null, null, null, null, null, null, null, null, 1, null, null,
    
    // [27] Tool Call Routing
    [
      tool_mask: 1                              // 1: Bật xử lý Tools
    ],
    
    null, null,
    
    // [30] Khối Chỉ Định Danh Mục Tiện Ích Mở Rộng (Extension Selection Block)
    [
      // Mảng các extension được phép kích hoạt trong lượt chat này
      Array<{
        extension_identifier: 
          | "workspace_gmail"                   // Dịch vụ hộp thư Gmail
          | "workspace_drive"                   // Dịch vụ Google Drive / Docs / Sheets
          | "workspace_youtube"                 // Dịch vụ YouTube Video & Subtitles
          | "workspace_maps"                    // Dịch vụ Google Maps & Địa điểm
          | "workspace_flights_hotels",         // Dịch vụ Google Flights / Chuyến bay
        
        is_explicitly_tagged: boolean,          // true nếu người dùng gõ trực tiếp cú pháp @...
        permission_grant_token: string | null   // Token cấp quyền nội bộ (nếu có)
      }>
    ],
    
    null, null, null, null, null, null, null, null, [1],
    null, null, null, null, null, null, null, null, null, null, null, 0,
    null, null, null, null, null,
    
    client_request_uuid: string,
    
    null, [], null, null, null, null, null, 0, 1, null, null, null, null, null,
    null, null, null, null, null,
    1, 0, null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, 0, null, 1
  ]
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về (Response Wire Schema)

Khi Extension được thực thi, phản hồi `wrb.fr` trả về một khối trung gian mô tả dữ liệu truy vấn từ hệ sinh thái Google trước khi trả về câu trả lời tổng hợp.

### Cấu trúc JSON sau khi Parse Envelope `wrb.fr`:

```typescript
interface ExtensionInnerResponse {
  // [4] Mảng phản hồi của Trợ lý
  4: Array<[
    choice_id: string,
    
    // [1] Khối văn bản trả lời tổng hợp
    [
      final_answer_markdown: string,            // Lời giải thích tổng hợp từ email hoặc video tìm được
      null, null, null, null, null, null
    ],

    null, null, null, null, null, null, null,

    // [14] Khối Siêu Dữ Liệu Extension (Extension Invocation Result)
    Array<{
      extension_type: "workspace_gmail" | "workspace_drive" | "workspace_youtube" | "workspace_maps",
      execution_status: "SUCCESS" | "PERMISSION_DENIED" | "NO_RESULTS",
      
      // Dữ liệu thô bóc tách từ dịch vụ Google
      raw_payload: {
        // Trường hợp Gmail:
        emails?: Array<{
          message_id: string,
          thread_id: string,
          sender_name: string,
          sender_email: string,
          subject: string,
          date_timestamp_ms: number,
          snippet: string,
          direct_url: string                    // "https://mail.google.com/mail/u/0/#inbox/..."
        }>,

        // Trường hợp Google Drive:
        drive_files?: Array<{
          file_id: string,
          file_title: string,
          mime_type: string,                    // "application/vnd.google-apps.document",...
          last_modified_by: string,
          direct_url: string                    // "https://docs.google.com/document/d/..."
        }>,

        // Trường hợp YouTube:
        youtube_videos?: Array<{
          video_id: string,
          title: string,
          channel_title: string,
          thumbnail_url: string,
          timestamp_anchor_seconds?: number,    // Vị trí giây mà câu hỏi nhắc tới
          direct_url: string                    // "https://www.youtube.com/watch?v=..."
        }>,

        // Trường hợp Google Maps:
        locations?: Array<{
          place_id: string,
          place_name: string,
          address: string,
          latitude: number,
          longitude: number,
          rating: number,
          photos: string[]
        }>
      }
    }>
  ]>;
}
```

---

## 4. Xử Lý Khi Chưa Cấp Quyền (Permission Challenge)

Nếu tài khoản Google chưa bật extension trong cài đặt hoặc chưa đồng ý cấp quyền:
* `execution_status`: `"PERMISSION_DENIED"`
* Server trả về URL ủy quyền nhanh:
  ```json
  {
    "auth_consent_required": true,
    "consent_url": "https://myaccount.google.com/connections/services/..."
  }
  ```
Client cần hướng dẫn người dùng hoàn tất phê duyệt trên Google Accounts trước khi replay request.
