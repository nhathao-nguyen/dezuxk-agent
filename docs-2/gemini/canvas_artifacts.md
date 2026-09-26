# Chức năng: Không Gian Làm Việc Trực Quan (Gemini Canvas & Artifacts)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của tính năng Gemini Canvas (Artifacts Workspace) trên Google Gemini Web. Tính năng này cho phép tạo ra các tài liệu độc lập (văn bản Markdown, mã nguồn Python, HTML/JS) chạy song song với cửa sổ chat, hỗ trợ cập nhật từng dòng (delta updates), sửa lỗi inline và xuất bản (publish) ra liên kết công khai.

---

## 1. Yêu Cầu Chung

* **Endpoint Batchexecute:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rt=c`
* **Headers Bắt Buộc:**
  ```http
  Content-Type: application/x-www-form-urlencoded;charset=UTF-8
  Origin: https://gemini.google.com
  Referer: https://gemini.google.com/app
  X-Same-Domain: 1
  ```
* **Cookies Bắt Buộc:** `__Secure-1PSID`, `__Secure-1PSIDTS`, `COMPASS`.

---

## 2. Các RPC Nghiệp Vụ Của Canvas

### 2.1. Khởi Tạo Tài Liệu Canvas Mới (RPC `tVk3Sc`)

Được kích hoạt khi người dùng yêu cầu tạo dự án mới trên Canvas hoặc khi Gemini tự động chuyển câu trả lời dài thành một Artifact.

* **Query Param:** `rpcids=tVk3Sc`
* **Cấu trúc Payload gửi đi trong `f.req`:**
```json
[
  [
    [
      "tVk3Sc",
      "[\"c_<CONVERSATION_ID>\", \"<TITLE_TÀI_LIỆU>\", \"<CONTENT_TYPE>\", \"<INITIAL_CONTENT_ESCAPED>\"]",
      null,
      "generic"
    ]
  ]
]
```
Trong đó:
* `CONTENT_TYPE`: `"MARKDOWN"` | `"PYTHON"` | `"HTML_JS"` | `"PLAIN_TEXT"`.
* `INITIAL_CONTENT_ESCAPED`: Nội dung văn bản hoặc code khởi đầu.

* **Dữ liệu Server trả về:**
```json
[
  "canvas_<CANVAS_UUID>",
  1,
  1790172500000
]
```
*(Cấp phát `canvas_id` và số hiệu phiên bản `version = 1`)*.

---

### 2.2. Cập Nhật / Chỉnh Sửa Từng Dòng Tài Liệu (Delta Update - RPC `sA4a8`)

Khi người dùng bôi đen một đoạn văn bản hoặc hàm trong Canvas và yêu cầu Gemini viết lại, hoặc người dùng tự sửa trực tiếp trong trình biên tập, client gửi bản cập nhật thay vì truyền lại toàn bộ văn bản.

* **Query Param:** `rpcids=sA4a8`
* **Cấu trúc Payload gửi đi trong `f.req`:**
```json
[
  [
    [
      "sA4a8",
      "[\"canvas_<CANVAS_UUID>\", <BASE_VERSION_NUMBER>, [<DIFF_OPERATIONS>]]",
      null,
      "generic"
    ]
  ]
]
```

#### Cấu trúc mảng `DIFF_OPERATIONS`:
```typescript
type DiffOperation = 
  | { op: "retain", count: number }              // Giữ nguyên số ký tự
  | { op: "delete", count: number }              // Xóa số ký tự
  | { op: "insert", text: string };              // Chèn chuỗi văn bản mới
```

* **Dữ liệu Server trả về:**
```json
[
  "canvas_<CANVAS_UUID>",
  2,
  "SUCCESS"
]
```
*(Xác nhận đã áp dụng diff và nâng `version = 2`)*.

---

### 2.3. Xuất Bản & Tạo Liên Kết Chia Sẻ (Publish Canvas - RPC `H8s0fe`)

Người dùng nhấn nút "Chia sẻ" hoặc "Publish" để biến Canvas thành một trang web công khai có thể xem mà không cần đăng nhập.

* **Query Param:** `rpcids=H8s0fe`
* **Cấu trúc Payload gửi đi trong `f.req`:**
```json
[
  [
    [
      "H8s0fe",
      "[\"canvas_<CANVAS_UUID>\", <VISIBILITY_CODE>, <ALLOW_FORK_BOOLEAN>]",
      null,
      "generic"
    ]
  ]
]
```
Trong đó:
* `VISIBILITY_CODE`: `1` (Chỉ người có liên kết / Unlisted) hoặc `2` (Công khai).
* `ALLOW_FORK_BOOLEAN`: `true` (Cho phép người khác nhân bản Canvas).

* **Dữ liệu Server trả về:**
```json
[
  "https://gemini.google.com/share/canvas/<PUBLIC_SHARE_HASH>",
  1790172550000
]
```

---

## 3. Lệnh cURL Mẫu Gọi Thao Tác Canvas Ngoài Trình Duyệt

```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=tVk3Sc&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"tVk3Sc\",\"[\\\"<CONVERSATION_ID>\\\",\\\"Kế hoạch dự án\\\",\\\"MARKDOWN\\\",\\\"# Tiêu đề\\\\nNội dung dự án...\\\"]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```
