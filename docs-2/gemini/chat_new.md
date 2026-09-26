# Chức năng: Khởi tạo Cuộc trò chuyện Mới (Chat Mới / New Chat)

Chức năng xóa trạng thái ngữ cảnh cũ và bắt đầu một phiên trò chuyện hoàn toàn mới với Gemini.

---

## 1. Cơ chế Nghiệp vụ

Khi người dùng bấm nút "Cuộc trò chuyện mới" (New Chat) trên Gemini Web:
1. Client **không** gửi ngay một request tạo chat rỗng lên server.
2. Client xóa sạch bộ nhớ ngữ cảnh cục bộ: reset `conversation_id`, `response_id`, `choice_id` về giá trị rỗng `["", "", ""]`.
3. Khi người dùng nhập tin nhắn đầu tiên của cuộc trò chuyện mới, request `StreamGenerate` sẽ được gửi lên với cấu trúc rỗng này.
4. Server Google sẽ tự động cấp phát một `conversation_id` mới (tiền tố `c_...`) và trả về trong phản hồi chunk đầu tiên.
5. Sau đó, server kích hoạt RPC ngầm `PCck7e` để liên kết và tự động tạo tiêu đề tóm tắt cho cuộc trò chuyện mới.

---

## 2. Dữ liệu khi gửi Request khởi tạo Chat Mới

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate`
* **Cookie cần thiết:** `__Secure-1PSID`, `__Secure-1PSIDTS`, `__Secure-1PSIDCC`.
* **Cấu trúc trường ngữ cảnh trong `f.req`:**
  ```json
  [
    null,
    "[[\"Tin nhắn mở đầu cuộc trò chuyện mới\", 0, null, null, null, null, 0], [\"vi\"], [\"\", \"\", \"\", null, null, null, null, null, null, \"\"], null, ...]"
  ]
  ```
  * Chú ý mảng ngữ cảnh: `["", "", ""]` thể hiện cuộc trò chuyện chưa có lịch sử trước đó.
  * Context Blob: `null` (không có session blob trước đó).

---

## 3. Đồng bộ & Tự động đặt tiêu đề (RPC `PCck7e`)

Ngay sau khi lượt stream đầu tiên hoàn tất, client gửi request đến `batchexecute`:
* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=PCck7e`
* **Cấu trúc Payload:**
  ```json
  [
    [
      [
        "PCck7e",
        "[\"r_conversation_reference_id\"]",
        null,
        "generic"
      ]
    ]
  ]
  ```
* **Dữ liệu Server xử lý:**
  * Server phân tích nội dung câu hỏi đầu tiên của người dùng.
  * Tự động sinh tiêu đề ngắn gọn (khoảng 3 - 6 từ) và lưu vào danh sách lịch sử hội thoại trên đám mây.
