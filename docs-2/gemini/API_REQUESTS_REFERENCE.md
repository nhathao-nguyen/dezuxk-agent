# Bảng Tra Cứu Toàn Bộ Request API: Google Gemini Web (Master Request Reference)

Tài liệu này tổng hợp **toàn bộ các HTTP Request thực tế** của tất cả các chức năng chính trên Google Gemini Web (`gemini.google.com`). Mỗi chức năng bao gồm: Endpoint, Method, Headers, Cookies yêu cầu, Cấu trúc Payload và Lệnh cURL mẫu có thể copy-paste chạy trực tiếp từ Terminal.

---

## 1. Yêu Cầu Cơ Bản Cho Mọi Request Gemini

### Headers Chuẩn Bắt Buộc (Anti-Bot & WAF)
```http
Content-Type: application/x-www-form-urlencoded;charset=UTF-8
Origin: https://gemini.google.com
Referer: https://gemini.google.com/app
X-Same-Domain: 1
Sec-Fetch-Site: same-origin
Sec-Fetch-Mode: cors
Sec-Fetch-Dest: empty
User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36
```

### Cookies Bắt Buộc
* `__Secure-1PSID`: Định danh tài khoản Google chính.
* `__Secure-1PSIDTS`: Token bảo mật dấu thời gian (xoay vòng liên tục).
* `__Secure-1PSIDCC` & `SIDCC`: Token xác thực phiên đang hoạt động.
* `COMPASS`: Token giám sát hành vi riêng của Gemini.

---

## 2. Danh Mục Các Request Chức Năng Chính

### 2.1. Gửi Tin Nhắn & Trò Chuyện Trực Tuyến (StreamGenerate)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Mục đích:** Gửi prompt, suy luận mô hình và nhận luồng văn bản phản hồi theo thời gian thực.
* **Cấu trúc Tham số Form:**
  * `at`: CSRF token (`SNlM0e`).
  * `f.req`: Chuỗi lồng JSON 2 tầng: `[null, "<JSON_ARRAY>"]`.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>; __Secure-1PSIDCC=<COOKIE>; COMPASS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[[[\"Xin chào Gemini, hôm nay thời tiết thế nào?\",0,null,null,null,null,0],[\"vi\"],[\"<CONVERSATION_ID>\",\"<RESPONSE_ID>\",\"<CHOICE_ID>\",null,null,null,null,null,null,\"\"],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,null,\"<CLIENT_UUID>\",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,1,1,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,0,null,1]]"]'
```

---

### 2.2. Khởi Tạo Chat Mới (New Chat)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Điểm khác biệt:** Reset toàn bộ context ID về mảng rỗng `["", "", ""]` và Context Blob về `null`. Server sẽ tự cấp `c_...` mới trong phản hồi.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[[[\"Câu hỏi mở đầu cho cuộc trò chuyện mới\",0,null,null,null,null,0],[\"vi\"],[\"\",\"\",\"\",null,null,null,null,null,null,\"\"],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,null,\"NEW_CHAT_UUID\",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,1,1,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,0,null,1]]"]'
```

---

### 2.3. Chế Độ Tư Duy Mở Rộng (Extended Thinking Mode / Raisonnement étendu)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Tham số kích hoạt:** Đổi cờ model sang Pro (`3`) và cờ thinking sang kích hoạt (`1, null, 1` ở cuối payload).

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[[[\"Hãy chứng minh định lý Fermat nhỏ và giải thích từng bước tư duy\",0,null,null,null,null,0],[\"vi\"],[\"\",\"\",\"\",null,null,null,null,null,null,\"\"],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,null,\"THINKING_UUID\",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,3,1,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,1,null,1]]"]'
```
* **Dữ liệu trả về:** Phản hồi chứa các block `thought_content` mô tả các bước suy luận ngầm trước khi xuất câu trả lời.

---

### 2.4. Tạo Hình Ảnh qua Prompt (Imagen 3)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Cơ chế:** Gửi prompt mô tả hình ảnh tự nhiên, server tự định tuyến sang Imagen 3.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[[[\"Vẽ một bức tranh sơn dầu chú mèo vàng ngồi bên cửa sổ đón nắng\",0,null,null,null,null,0],[\"vi\"],[\"\",\"\",\"\",null,null,null,null,null,null,\"\"],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,null,\"IMAGE_GEN_UUID\",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,1,1,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,0,null,1]]"]'
```
* **Dữ liệu trả về:** JSON chứa mảng `https://lh3.googleusercontent.com/gg-bard-images/...` kích thước 1024x1024.

---

### 2.5. Tải Lên Tệp Nhị Phân (Push Resumable Upload)

Quy trình 2 bước chuẩn xác trên hệ thống `push.clients6.google.com`:

#### Bước 1: Khởi tạo phiên Upload (Start Handshake)
```bash
curl -i -X POST "https://push.clients6.google.com/upload/" \
  -H "x-tenant-id: bard-storage" \
  -H "push-id: feeds/mcudyrk2a4khkz" \
  -H "x-goog-upload-command: start" \
  -H "x-goog-upload-protocol: resumable" \
  -H "x-goog-upload-header-content-length: 1024" \
  -H "content-type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/"
```
* **Lấy header phản hồi:** `x-goog-upload-url` (chứa URL đẩy tệp).

#### Bước 2: Tải lên dữ liệu nhị phân & Chốt tệp (Upload & Finalize)
```bash
curl -X POST "<URL_LẤY_TỪ_HEADER_X_GOOG_UPLOAD_URL>" \
  -H "x-tenant-id: bard-storage" \
  -H "push-id: feeds/mcudyrk2a4khkz" \
  -H "x-goog-upload-command: upload, finalize" \
  -H "x-goog-upload-offset: 0" \
  -H "content-type: application/x-www-form-urlencoded;charset=utf-8" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  -H "Referer: https://gemini.google.com/" \
  --data-binary "@/duong/dan/anh_hoac_tai_lieu.png"
```
* **Dữ liệu trả về:** Chuỗi lưu trữ nội bộ `/contrib_service/ttl_1d/pjsmt5...` dùng để đính kèm vào `StreamGenerate`.

---

### 2.6. Lấy Lịch Sử Hội Thoại (RPC `MaZiqc`)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=MaZiqc&rt=c`

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=MaZiqc&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"MaZiqc\",\"[25,null,[1,null,1]]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```
* **Dữ liệu trả về:** Mảng danh sách các cuộc trò chuyện gồm `c_conversation_id`, tiêu đề và timestamps.

---

### 2.7. Đọc Chi Tiết Tin Nhắn Cuộc Trò Chuyện (RPC `cZOhpc`)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=cZOhpc&rt=c`

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=cZOhpc&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"cZOhpc\",\"[\\\"<CONVERSATION_ID>\\\"]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

---

### 2.8. Tự Động / Đồng Bộ Tiêu Đề Hội Thoại (RPC `PCck7e`)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=PCck7e&rt=c`

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=PCck7e&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"PCck7e\",\"[\\\"<RESPONSE_ID>\\\"]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

---

### 2.9. Xóa Cuộc Trò Chuyện (RPC `VxUbXb`)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=VxUbXb&rt=c`

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=VxUbXb&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"VxUbXb\",\"[\\\"<CONVERSATION_ID>\\\"]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

---

### 2.10. Tra Cứu Hạn Mức Điện Toán (Usage)

* **Endpoint:** `GET https://gemini.google.com/usage`

#### Lệnh cURL Mẫu:
```bash
curl -X GET "https://gemini.google.com/usage" \
  -H "Accept: text/html,application/xhtml+xml,application/xml" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  -H "User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"
```
* **Dữ liệu trả về:** HTML/JSON chứa `%` hạn mức 5h (`quota5h`) và tuần (`quotaWeekly`).

---

### 2.11. Tổng Hợp Giọng Đọc Audio (TTS RPC `whPPme`)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=whPPme&rt=c`

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=whPPme&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"whPPme\",\"[\\\"vi\\\",null,[4]]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

---

### 2.12. Tìm Kiếm Thời Gian Thực & Nguồn Trích Dẫn (Search Grounding & Citations)

* **Tài liệu chi tiết:** [search_grounding_citations.md](search_grounding_citations.md)
* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Tham số kích hoạt:** Đặt cờ công cụ `inner[27]` thành `[1]`.
* **Dữ liệu trả về:** Khung `wrb.fr` chứa mảng `candidate[12]` với danh sách `search_queries`, `grounding_sources` (URL, tiêu đề, favicon, domain), và `grounding_supports`.

---

### 2.13. Thực Thi Mã Python Trong Hộp Cát (Code Interpreter Sandbox)

* **Tài liệu chi tiết:** [code_interpreter.md](code_interpreter.md)
* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Dữ liệu trả về:** Khung `wrb.fr` chứa khối `candidate[10]` gồm `code_block` (Python), `execution_result` (`exit_code`, `stdout`, `stderr`), và mảng hình ảnh đồ thị `output_images` (Base64/PNG).

---

### 2.14. Phân Nhánh Hội Thoại & Sửa Tin Nhắn Cũ (Conversation Branching - RPC `wEb32b`)

* **Tài liệu chi tiết:** [conversation_branching.md](conversation_branching.md)
* **Tạo nhánh mới:** Gửi `StreamGenerate` với `inner[2][1] = "r_<PARENT_ID>"` và `inner[46] = "<NEW_UUID>"`.
* **Chuyển đổi nhánh (RPC `wEb32b`):**
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=wEb32b&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["wEb32b","[\"<CONVERSATION_ID>\",\"<PARENT_RESPONSE_ID>\",\"<PARENT_CHOICE_ID>\"]",null,"generic"]]]'
```

---

### 2.15. Không Gian Làm Việc Trực Quan (Gemini Canvas & Artifacts)

* **Tài liệu chi tiết:** [canvas_artifacts.md](canvas_artifacts.md)
* **Khởi tạo Canvas mới (RPC `tVk3Sc`):**
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=tVk3Sc&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["tVk3Sc","[\"<CONVERSATION_ID>\",\"<CANVAS_TITLE>\",\"MARKDOWN\",\"# Nội dung khởi tạo...\"]",null,"generic"]]]'
```
* **Cập nhật từng dòng (RPC `sA4a8`):** Truyền mảng diff delta operations `[retain, delete, insert]`.
* **Xuất bản chia sẻ liên kết (RPC `H8s0fe`):** Trả về URL `https://gemini.google.com/share/canvas/...`.

---

### 2.16. Tiện Ích Mở Rộng Google Workspace (@Extensions)

* **Tài liệu chi tiết:** [workspace_extensions.md](workspace_extensions.md)
* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Tham số:** Kích hoạt danh mục extension trong `inner[30]` (`workspace_gmail`, `workspace_drive`, `workspace_youtube`, `workspace_maps`).
* **Dữ liệu trả về:** Mảng `candidate[14]` chứa raw payload các email Gmail, tài liệu Drive hoặc video YouTube được trích xuất.

---

### 2.17. Đánh Giá & Phản Hồi Trợ Lý (Feedback RPC `uP80Sb`)

* **Tài liệu chi tiết:** [feedback.md](feedback.md)
* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=uP80Sb&rt=c`
```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=uP80Sb&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["uP80Sb","[\"<CONVERSATION_ID>\",\"<RESPONSE_ID>\",\"<CHOICE_ID>\",1,[],\"Rất hữu ích\",[\"vi\",null]]",null,"generic"]]]'
```

