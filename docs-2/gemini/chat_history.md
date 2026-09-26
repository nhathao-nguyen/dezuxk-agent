# Chức năng: Lịch sử Trò chuyện & Quản trị Phiên (Chat History)

Toàn bộ lịch sử trò chuyện của người dùng trên Gemini được đồng bộ lên hạ tầng đám mây của Google và quản lý thông qua các lời gọi RPC trong kênh `batchexecute`.

---

## 1. Endpoint chung

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute`
* **Cookie cần thiết:** `__Secure-1PSID`, `__Secure-1PSIDTS`.
* **Headers:** `Content-Type: application/x-www-form-urlencoded;charset=UTF-8`, `X-Same-Domain: 1`.

---

## 2. Chi tiết từng Tác vụ Nghiệp vụ trên Server

### 2.1. Lấy danh sách lịch sử cuộc trò chuyện (RPC `MaZiqc`)
Tải danh sách các cuộc trò chuyện gần nhất kèm phân trang.

* **RPC ID:** `MaZiqc`
* **Dữ liệu gửi lên server:**
  ```json
  [25, null, [1, null, 1]]
  ```
  * `25`: Số lượng cuộc trò chuyện tối đa cần lấy.
* **Dữ liệu server trả về:**
  * Mảng các cuộc trò chuyện gồm: `c_conversation_id`, tiêu đề hiển thị, thời gian tạo, thời gian sửa đổi gần nhất.
  * Token phân trang (Next Page Token) để tải tiếp các trang cũ hơn.

---

### 2.2. Đọc toàn bộ tin nhắn của một cuộc trò chuyện (RPC `cZOhpc`)
Tải chi tiết toàn bộ các lượt hỏi đáp trong một phiên trò chuyện cụ thể.

* **RPC ID:** `cZOhpc`
* **Dữ liệu gửi lên server:**
  ```json
  ["c_conversation_id"]
  ```
* **Dữ liệu server trả về:**
  * Toàn bộ lịch sử prompt và câu trả lời.
  * Các hình ảnh, tài liệu đã tải lên trong phiên đó.
  * Các mã định danh nhánh câu trả lời (`rc_choice_id`).

---

### 2.3. Đổi tên cuộc trò chuyện (RPC `PCck7e`)
Cập nhật tiêu đề tùy chỉnh cho cuộc hội thoại do người dùng tự đặt.

* **RPC ID:** `PCck7e`
* **Dữ liệu gửi lên server:**
  ```json
  ["c_conversation_id", "Tiêu đề mới do người dùng đặt"]
  ```
* **Dữ liệu server trả về:**
  * Xác nhận cập nhật thành công vào cơ sở dữ liệu server.

---

### 2.4. Xóa một cuộc trò chuyện (RPC `VxUbXb`)
Xóa vĩnh viễn cuộc trò chuyện khỏi cơ sở dữ liệu của Google.

* **RPC ID:** `VxUbXb`
* **Dữ liệu gửi lên server:**
  ```json
  ["c_conversation_id"]
  ```
* **Dữ liệu server trả về:**
  * Xác nhận xóa thành công.

---

### 2.5. Xóa sạch toàn bộ lịch sử (RPC `GPRiHf`)
Xóa trắng toàn bộ dữ liệu lịch sử của tài khoản trên Gemini.

* **RPC ID:** `GPRiHf`
* **Dữ liệu gửi lên server:**
  ```json
  []
  ```
* **Dữ liệu server trả về:**
  * Xác nhận hoàn tất xóa toàn bộ dữ liệu hội thoại của người dùng.
