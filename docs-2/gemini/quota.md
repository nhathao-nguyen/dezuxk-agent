# Chức năng: Kiểm tra Hạn mức Tính toán & Quota (Quota & Limits)

Chức năng kiểm tra dung lượng tài nguyên suy luận AI còn lại của tài khoản, hạn mức RPM và cấp độ gói dịch vụ Google.

---

## 1. Endpoint Tra cứu Hạn mức Sử dụng Trực tiếp

* **Endpoint:** `GET https://gemini.google.com/usage`
* **Xác thực:** Cookies `__Secure-1PSID`, `__Secure-1PSIDTS`.
* **Headers:**
  ```http
  Accept: text/html,application/xhtml+xml,application/xml
  User-Agent: <Trình duyệt Chromium chuẩn>
  ```

---

## 2. Dữ liệu Server xử lý và Trả về

Server tính toán và trả về các chỉ số đo lường tài nguyên thực tế:

| Chỉ số | Mô tả nghiệp vụ | Giá trị điển hình |
| :--- | :--- | :--- |
| `quota5h` | Tỷ lệ phần trăm tài nguyên đã tiêu thụ trong chu kỳ 5 giờ gần nhất | `0%` đến `100%` |
| `quotaWeekly` | Tỷ lệ phần trăm tài nguyên đã tiêu thụ trong chu kỳ 7 ngày | `0%` đến `100%` |
| `rpmLimit` | Giới hạn số lượng request tối đa trong mỗi phút | `60` requests/phút |
| `resetTime5h` | Thời điểm chính xác chu kỳ 5 giờ sẽ được hoàn trả dung lượng | Timestamp ISO-8601 |

---

## 3. RPC Tra cứu Cấp độ Bản quyền Tài khoản (RPC `I4z33b`)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=I4z33b`
* **Dữ liệu gửi lên:** `[]`
* **Dữ liệu server trả về:**
  * Thông tin gói thuê bao: `FREE_USER`, `WORKSPACE_ENTERPRISE`, hoặc `GOOGLE_ONE_AI_PREMIUM`.
  * Hạn mức dung lượng cửa sổ ngữ cảnh (Context Window Size: ví dụ 1,000,000 tokens cho gói Pro).
  * Quyền truy cập các tính năng thử nghiệm sớm (Early Access Features).
