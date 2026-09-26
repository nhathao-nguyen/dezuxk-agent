# Phân tích Cookie, Vòng đời Dữ liệu & Cơ chế Phòng chống Bot trên Google Flow

Tài liệu này phân tích chi tiết cơ chế bảo mật, định danh, vòng đời cookie và các biện pháp chống bot/spam của máy chủ Google Flow (`flow.google.com`) được thu thập từ phiên Chrome thực tế qua Chrome DevTools Protocol.

---

## 1. Danh mục Cookie & Cơ chế Định danh Riêng biệt của Flow

Khác với các dịch vụ thông thường, Google Flow sử dụng cơ chế bảo mật **Origin-Bound Session Isolation** (cô lập phiên theo tên miền nguồn) với các cookie độc quyền:

| Tên Cookie | Domain | Nguồn gốc tạo | Thời hạn (Lifecycle) | Tần suất Refresh | Mục đích bảo mật & Nhận diện |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `OSID` | `flow.google.com` | **Server** | Dài hạn (~1 - 2 năm) | Ổn định | **Origin Session ID:** Định danh phiên làm việc gắn chặt riêng cho tên miền `flow.google.com`. Bắt buộc phải có để truy cập AI Sandbox. |
| `__Secure-OSID` | `flow.google.com` | **Server** | Dài hạn (~1 - 2 năm) | Ổn định | Biến thể bảo mật cao của `OSID` (`HttpOnly`, `Secure`, `SameSite=None`). |
| `__Secure-1PSID` | `.google.com` | **Server** | Dài hạn (~1 - 2 năm) | Ổn định | Cookie phiên đăng nhập Google gốc cấp quyền truy cập tài nguyên cloud. |
| `__Secure-1PSIDTS` | `.google.com` | **Server** | Trung hạn (~1 năm) | **Từng giờ / Request** | Time-stamped Security Token chống replay request. |
| `NID` | `.google.com` | **Server** | ~6 tháng | Định kỳ | Cookie điều hướng hạ tầng máy chủ Google ESF (Edge Server Frontend). |
| `_ga`, `_ga_X2GNH8R5NS` | `.flow.google.com` | **Client (JS)** | 1 - 2 năm | Khi có sự kiện | Cookie Google Analytics phân tích hành vi tương tác studio của client. |

---

## 2. Vòng đời Cookie: Client-set vs Server-set & Cơ chế Refresh

1. **Dữ liệu được tạo từ Server:**
   * `OSID`, `__Secure-OSID`: Do endpoint xác thực của Google cấp phát khi người dùng truy cập `https://flow.google.com/?pli=1`. Nếu thiếu 2 cookie này, request sẽ bị chuyển hướng (Redirect HTTP 302) về trang đăng nhập Google Accounts.
   * `__Secure-1PSIDTS`: Server cập nhật liên tục thông qua tiến trình xoay token bảo mật ngầm.
2. **Dữ liệu được tạo từ Client:**
   * Toàn bộ mã định danh phiên UUID v4 của request `FlowCreationAgentService/StreamChat` và RPC `csbIsb`.
   * Các cookie đo lường phân tích hành vi: `_ga`, `_ga_X2GNH8R5NS`.
3. **Mức độ lưu trữ:**
   * Lưu trữ lâu dài: `__Secure-1PSID`, `OSID`, `__Secure-OSID` (duy trì phiên đăng nhập không cần nhập lại mật khẩu).
   * Refresh liên tục: `__Secure-1PSIDTS`, `SIDCC` (được làm mới liên tục để bảo vệ an toàn cho hạn mức credit).

---

## 3. Cơ chế Khóa Phiên Tương tác & Chống Spam/Lạm dụng Credit (Anti-Abuse)

### 3.1. RPC Khóa Phiên Dự án (`csbIsb`)
Trước mỗi thao tác gọi Agent hoặc tạo nội dung tốn credit, client bắt buộc phải gửi một request khóa phiên:
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=csbIsb`
* **Cấu trúc:** `["project_uuid_v4", null, "client_session_guid"]`
* **Mục đích:** Khóa phiên làm việc trên server, ngăn chặn việc gọi API đồng thời từ nhiều nguồn làm âm số dư credit của tài khoản.

### 3.2. Chữ ký CSRF `at`
* Được cấp phát động qua biến môi trường frontend của `AiSandboxAngularFrontend`.
* Mọi request đến `batchexecute` và `FlowCreationAgentService` đều phải đính kèm tham số form `at=<token>`.

### 3.3. Bộ Headers Bảo vệ Bắt buộc
```http
Content-Type: application/x-www-form-urlencoded;charset=UTF-8
Origin: https://flow.google.com
Referer: https://flow.google.com/
Sec-Fetch-Dest: empty
Sec-Fetch-Mode: cors
Sec-Fetch-Site: same-origin
X-Same-Domain: 1
```
> **Lưu ý chống Bot:** Nếu gọi API từ bên ngoài mà không có cookie `OSID` và `__Secure-OSID` gắn với domain `flow.google.com`, server sẽ từ chối kết nối ngay lập tức với mã lỗi HTTP 401 Unauthorized.
