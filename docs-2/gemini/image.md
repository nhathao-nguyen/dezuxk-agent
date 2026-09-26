# Chức năng: Tạo Hình ảnh Trực tiếp qua Chat Prompt (Image Generation)

Trên Google Gemini Web, người dùng **không cần phải chọn công cụ tạo ảnh riêng biệt**. Thay vào đó, máy chủ tự động phân tích ý định (Intent Recognition) từ nội dung prompt và điều hướng lệnh sang mô hình **Imagen 3** nội bộ.

---

## 1. Cơ chế Điều hướng (Internal Intent Routing)

Khi người dùng nhập các prompt như:
* *"Vẽ một bức tranh sơn dầu chú mèo lông vàng bên cửa sổ"*
* *"Generate an image of a futuristic cyberpunk city at night"*

Client vẫn gửi request đến cùng một endpoint duy nhất:
* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate`
* **Cookie cần thiết:** `__Secure-1PSID`, `__Secure-1PSIDTS`, `__Secure-1PSIDCC`.

---

## 2. Cấu trúc Payload gửi lên Server

Payload `f.req` hoàn toàn tuân theo định dạng chuẩn của `StreamGenerate`:

```json
[
  null,
  "[[\"<IMAGE_PROMPT_TEXT>\", 0, null, null, null, null, 0], [\"<LOCALE>\"], [\"<CONVERSATION_ID>\", \"<RESPONSE_ID>\", \"<CHOICE_ID>\", null, null, null, null, null, null, \"\"], \"!<CONTEXT_BLOB>\", ...]"
]
```
* **Không cần cờ đặc biệt:** Server LLM của Gemini sẽ tự phát hiện đây là tác vụ sinh hình ảnh (Text-to-Image Generation).
* LLM tự động dịch prompt sang tiếng Anh tối ưu hóa cho mô hình diffusion Imagen 3 (nếu người dùng nhập tiếng Việt).

---

## 3. Dữ liệu Server xử lý và Phản hồi

Khác với câu trả lời thuần văn bản, khi kích hoạt Imagen 3, phản hồi streaming của server chứa các khối dữ liệu hình ảnh đặc thù:

### 3.1. Cấu trúc phản hồi thực tế (Xác minh từ Wire Protocol thực tế):
Trong luồng `wrb.fr`, Google trả về 2 phần song hành:

1. **Phần văn bản (`candidate[1][0]`)**: Chứa chuỗi placeholder nội bộ để định vị ảnh:
   ```text
   \n\nhttp://googleusercontent.com/image_generation_content/0_624\n\n
   ```

2. **Phần siêu dữ liệu tạo ảnh (`candidate[12][7][0]`)**: Chứa đối tượng ảnh kèm ánh xạ với placeholder:
   ```json
   [
     [
       [
         null, null, null,
         [
           null,
           1,
           "watermarked_img_15836108994116051645.png",
           "https://lh3.googleusercontent.com/gg-dl/AAQ_wbF8yNvZMEL5rfopm2K9oVzan_cPxjiyfYK9x05kCvwfHvbZGPpR92Fj3Zyyt2xKWXXOm1qq24CAeL2Z0mJywh_4MblKH_q0qxGpkEPR1CKvUT84si3ALrzc3g2r9X_WndVMgO5Btmd3Mfn5HuaHjI-072arq6FyBmjtEF7OK-tuTzU-ZA",
           null,
           "$Aesyi1...",
           null, null, null,
           [1790308899, 482767390],
           null,
           "image/png",
           null, null, null,
           [1408, 768, 2389823]
         ]
       ],
       [
         "http://googleusercontent.com/image_generation_content/0_624"
       ],
       null,
       [20, ...],
       null, null, null, null,
       "im_60801523e5f376fd"
     ]
   ]
   ```

3. **Cấu trúc Imagen cổ điển (`candidate[4]`)**:
   ```json
   [
     [
       "IMAGE_OUTPUT_ID_12345",
       "https://lh3.googleusercontent.com/gg-bard-images/...",
       null,
       1024,
       1024,
       "image/jpeg",
       "A golden-furred cat sitting by a sunlit wooden window",
       "https://lh3.googleusercontent.com/gg-bard-images/...=s512"
     ]
   ]
   ```

### 3.2. Quy tắc Xử lý & Ánh xạ Chuẩn của Gateway:
1. **Trích xuất ảnh (`ExtractGeneratedImages`)**: Quét `candidate[12][7]` và `candidate[4]`, lấy URL tải thật trên `https://lh3.googleusercontent.com/...`, tên file, kích thước pixel (`1408x768`), kích thước bytes và MIME type (`image/png`).
2. **Giải mã Placeholder**: Thay thế chuỗi định vị nội bộ `http://googleusercontent.com/image_generation_content/...` bằng định dạng Markdown Image `![Hình ảnh](<URL_LH3>)`.
3. **Phơi ra Facade**: Trả mảng `MediaURLs` sạch sẽ trong `OpenAIChatResponse.media_urls` và `RichChatResponseDTO.media_urls` để UI hiển thị Media Card tương tác (Phóng to, Tải về, Sao chép liên kết).

---

## 4. Phòng chống Bot & Giới hạn Lạm dụng Tạo ảnh
* Google giới hạn số lượng ảnh có thể sinh liên tục trong một khoảng thời gian ngắn (Burst limit: thường là 4 - 8 ảnh / 5 phút).
* Nếu vượt quá ngưỡng, server trả về thông báo lỗi dạng chuỗi: *"Bạn đã đạt giới hạn tạo hình ảnh trong thời điểm này. Vui lòng thử lại sau ít phút."*

---

## 5. Cơ chế Bảo vệ Cookie Google CDN & Giải pháp Gateway Media Cache (Khắc phục HTTP 403 Forbidden)

### 5.1. Nguyên nhân Gốc rễ Lỗi 403 Forbidden:
* Các đường link ảnh sinh ra bởi Imagen 3 (`lh3.googleusercontent.com/rd-gg-dl/...` hoặc `work.fife.usercontent.google.com/...`) là **Cookie-bound Signed URLs**.
* Google CDN bắt buộc phải có Cookie phiên Google (`__Secure-1PSID`, `__Secure-3PSID`...) cùng header `Origin: https://gemini.google.com` và `Referer: https://gemini.google.com/`.
* Khi client hoặc trình duyệt ngoài (như Chrome tab mới hoặc WebView2 `<img>`) truy cập trực tiếp URL này mà không mang cookie tài khoản Google tương ứng, Google CDN sẽ chặn lại với mã lỗi:
  > **403. That's an error.**  
  > *Your client does not have permission to get URL /rd-gg-dl/... from this server. (Client IP address: ...) Forbidden That's all we know.*

### 5.2. Kiến trúc Khắc phục Triệt để (Gateway Media Cache Facade):
Tuân thủ nghiêm ngặt **docs-2/CRITICAL_INTEGRATION_GUIDE.md Section 6** và **gateway-goi-thang-server-kien-truc.md**:
1. **Tải ngay lập tức với Cookie phiên (`DownloadAndCacheWithAuth`)**:
   - Khi Gemini trả về `MediaURLs`, Gateway `ChatService` và `MediaService` sử dụng `LocalStorageAdapter` để lập tức tải dữ liệu nhị phân về ổ đĩa `./storage/media/<hashID>.png`.
   - Client HTTP của Gateway có `CheckRedirect` chuyên dụng để **bảo toàn Cookie và User-Agent** qua các bước nhảy Redirect 302 của Google CDN sang `work.fife.usercontent.google.com`.
2. **Thay thế thành URL Facade Cục bộ (`/media/<hashID>.png`)**:
   - Cả trong mảng `resp.MediaURLs` và trong nội dung văn bản Markdown (`![Hình ảnh](http://localhost:8080/media/<hashID>.png)`), URL thô `lh3.googleusercontent.com` được thay thế hoàn toàn bằng URL Facade an toàn.
   - Endpoint máy chủ `GET /media/{id}` hỗ trợ CORS (`Access-Control-Allow-Origin: *`), Byte-Range streaming (HTTP 206), và nhận diện chuẩn xác MIME type.
3. **Lợi ích Vượt trội**:
   - **Không bao giờ bị lỗi 403**: Trình duyệt, desktop app, hoặc OpenAI API caller đều load ảnh trơn tru không cần cookie Google.
   - **Vĩnh viễn không hết hạn**: Phá bỏ giới hạn TTL 12–24h của link CDN Google.
   - **Hỗ trợ đầy đủ**: Mở tab mới, tải về máy, hoặc hiển thị phóng to (Lightbox) hoạt động 100%.


