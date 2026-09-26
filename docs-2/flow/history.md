# Chức năng: Lịch sử Dự án & Quản lý Không gian làm việc (History & Projects)

Chức năng truy xuất danh sách dự án đã lưu, tải đồ thị các node xử lý của dự án và xóa dự án trên máy chủ Google Flow.

---

## 1. Lấy danh sách Dự án (RPC `UpteDb`)

Tải toàn bộ các dự án mà người dùng đã tạo trên tài khoản.

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=UpteDb`
* **Cookie cần thiết:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`.
* **Cấu trúc dữ liệu gửi lên:**
  ```json
  [
    [
      [
        "UpteDb",
        "[\"projects/*\", 21, null, null, null, null, [1]]",
        null,
        "generic"
      ]
    ]
  ]
  ```
  * `21`: Số lượng dự án trên một trang (Limit).
  * `[1]`: Cờ lấy các dự án đang hoạt động (Active Projects).

### Dữ liệu server phản hồi:
```json
[
  [
    [
      "1cfeebb6-4d61-4373-aa21-dd52fd93679f",
      [
        "sept. 23 - 19:48",
        null,
        [1790167687, 193446000]
      ]
    ]
  ]
]
```
* Mảng danh sách chứa: `project_uuid`, tiêu đề dự án, và mảng mốc thời gian Unix (giây, nano giây).

---

## 2. Lấy Chi tiết Đồ thị Node & Công cụ PINHOLE (RPC `ngNC2`)

Khi người dùng mở một dự án, client gọi RPC này để server trả về toàn bộ sơ đồ các khối công cụ, video và ảnh đã tạo.

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=ngNC2`
* **Cấu trúc dữ liệu gửi lên:**
  ```json
  [
    [
      [
        "ngNC2",
        "[\"tools/PINHOLE/projects/1cfeebb6-4d61-4373-aa21-dd52fd93679f\"]",
        null,
        "generic"
      ]
    ]
  ]
  ```

### Dữ liệu server phản hồi:
* Danh sách các khối công cụ PINHOLE (`narwhal_display`, `abra`).
* Tọa độ, liên kết đầu vào/đầu ra giữa các node.
* Danh mục các video/ảnh đã render thuộc dự án này kèm URL tải về.

---

## 3. Xóa Dự án (RPC `mrlkwd`)

Xóa hoàn toàn một dự án khỏi cơ sở dữ liệu cloud.

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mrlkwd`
* **Cấu trúc dữ liệu gửi lên:**
  ```json
  [
    [
      [
        "mrlkwd",
        "[\"1cfeebb6-4d61-4373-aa21-dd52fd93679f\"]",
        null,
        "generic"
      ]
    ]
  ]
  ```
* **Dữ liệu server trả về:** Mã HTTP 200 xác nhận bản ghi dự án đã được dọn sạch khỏi hệ thống.
