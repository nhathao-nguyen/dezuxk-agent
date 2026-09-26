# Chức năng: Trò chuyện & Suy luận Trực tuyến (Chat & StreamGenerate)

Chức năng xử lý prompt hội thoại, suy luận ngôn ngữ, trích dẫn tìm kiếm và tạo câu trả lời theo thời gian thực của Google Gemini.

---

## 1. Thông tin Endpoint & Giao thức

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Query Parameters:**
  * `_reqid`: Số nguyên ngẫu nhiên tăng dần (ví dụ: `2871382`).
  * `rt`: `c` (Giao thức Streaming HTTP Chunked).
  * `bl`: Build label của server Google (ví dụ: `boq_assistant-bard-web-server_20260921.20_p0`).
  * `f.sid`: Session ID của phiên tương tác.
  * `hl`: Ngôn ngữ yêu cầu (ví dụ: `vi`, `fr`, `en`).

---

## 2. Dữ liệu khi gửi Request lên Server

### 2.1. Yêu cầu Cookie
* `__Secure-1PSID`: Định danh tài khoản.
* `__Secure-1PSIDTS`: Token bảo mật dấu thời gian.
* `__Secure-1PSIDCC`: Token xác thực phiên đang hoạt động.
* `COMPASS`: Token giám sát phiên làm việc của Gemini.

### 2.2. Dữ liệu Form Payload (`f.req` & `at`)
* `at`: CSRF token (`SNlM0e`).
* `f.req`: Cấu trúc mảng 2 phần tử `[null, "<JSON_string>"]`:

```json
[
  null,
  "[[\"<PROMPT_TEXT>\", 0, null, null, null, null, 0], [\"<LOCALE>\"], [\"<CONVERSATION_ID>\", \"<RESPONSE_ID>\", \"<CHOICE_ID>\", null, null, null, null, null, null, \"\"], \"!<CONTEXT_SESSION_BLOB>\", \"<REQ_HASH_ID>\", null, [0], 1, null, null, 1, 0, null, null, null, null, null, [[0]], 0, null, null, null, null, null, null, null, null, 1, null, null, [4], null, null, null, null, null, null, null, null, null, null, [1], null, null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, null, \"<CLIENT_SESSION_UUID>\", null, [], null, null, null, null, null, 0, 1, null, null, null, null, null, null, null, null, null, null, 1, 1, null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, 0, null, 1]"
]
```

### Ý nghĩa các trường gửi đi:
* `index [0][0]`: Nội dung prompt người dùng nhập vào.
* `index [0][1]`: Mã ngôn ngữ (`vi`).
* `index [0][2]`: Bộ 3 context ID của phiên cũ:
  * `c_...`: Conversation ID.
  * `r_...`: Response ID gần nhất.
  * `rc_...`: Choice ID gần nhất.
* `index [0][3]`: Context Session Blob do server cấp từ lượt phản hồi trước để đảm bảo tính liên tục của bộ nhớ model.
* `index [0][58]`: Client Session UUID v4 theo dõi phía frontend.
* `index [0][70]`: Cờ chọn mô hình: `1` cho Flash, `3` cho Pro/Thinking.

---

## 3. Dữ liệu Server Google xử lý & Phản hồi

Dữ liệu trả về qua kết nối chunked streaming theo định dạng Google WRB:

```text
)]}'

<độ_dài_byte>
[["wrb.fr", "assistant.lamda.BardFrontendService", "<JSON_kết_quả_suy_luận>"]]
```

### Các thông tin nghiệp vụ server trả về:
1. **Mã nhận diện:** Cấp mới `c_<id>` (nếu là chat mới), `r_<id>` (lượt trả lời), `rc_<id>` (phương án trả lời).
2. **Text Generation Chunks:** Từng phần câu trả lời văn bản nối tiếp nhau.
3. **Google Search Grounding:**
   * Các truy vấn tìm kiếm mà server đã thực hiện ngầm để tra cứu sự thật.
   * Danh sách đường dẫn URL, tiêu đề web và đoạn trích (snippets) dùng làm nguồn tham chiếu.
4. **Code Execution:** Mã nguồn do model viết và kết quả thực thi thực tế trong môi trường sandbox của Google.

---

## 4. Dữ liệu phòng chống Bot & Chặn

* Bắt buộc có header `Origin: https://gemini.google.com` và `X-Same-Domain: 1`.
* CSRF `at` phải trùng khớp với phiên đăng nhập trong cookie `__Secure-1PSID`.
* Giữ User-Agent nhất quán giữa các request.
