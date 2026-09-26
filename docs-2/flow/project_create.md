# Chức năng: Tạo Dự án Mới (Create Project)

Chức năng tạo một không gian làm việc (Project Workspace) mới trên máy chủ đám mây của Google Flow.

---

## 1. Thông tin Endpoint

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jHPbke`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookie cần thiết:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`, `__Secure-1PSIDTS`.

---

## 2. Cấu trúc Dữ liệu Gửi lên Server (Request Payload)

* **Tham số form:**
  * `at`: CSRF token phiên làm việc.
  * `f.req`: Cấu trúc mảng RPC chuẩn:

```json
[
  [
    [
      "jHPbke",
      "[\"projects/*\", [null, [\"sept. 23 - 19:48\"]], [null, 22]]",
      null,
      "generic"
    ]
  ]
]
```

### Ý nghĩa các trường gửi đi:
1. `"projects/*"`: Tiền tố không gian dự án trên hệ thống Google Cloud.
2. `[null, ["Tên dự án"]]`: Tên khởi tạo của dự án (mặc định lấy theo tháng ngày giờ hiện tại, ví dụ: `"sept. 23 - 19:48"`).
3. `[null, 22]`: Mã phân vùng định danh dịch vụ AI Sandbox.

---

## 3. Cấu trúc Dữ liệu Server Xử lý & Phản hồi

Server tạo bản ghi dự án mới trong cơ sở dữ liệu và trả về đối tượng JSON trong `wrb.fr`:

```json
[
  "1cfeebb6-4d61-4373-aa21-dd52fd93679f",
  [
    "sept. 23 - 19:48"
  ]
]
```

### Dữ liệu nghiệp vụ nhận từ Server:
* `project_id`: Chuỗi UUID v4 duy nhất do server cấp phát (ví dụ: `1cfeebb6-4d61-4373-aa21-dd52fd93679f`).
* `project_name`: Tên dự án đã được lưu trên máy chủ.
* Client sử dụng UUID này để điều hướng URL sang: `https://flow.google.com/project/<project_id>`.
