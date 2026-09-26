# Chức năng: Quản Lý Thùng Rác & Khôi Phục Dự Án (Trash & Restore RPCs)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của cơ chế xóa tạm thời (Soft Delete / Move to Trash), truy xuất danh sách dự án trong thùng rác, khôi phục dự án (Restore) và dọn sạch thùng rác vĩnh viễn trên Google Flow (`flow.google.com`).

---

## 1. Yêu Cầu Cơ Bản

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rt=c`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookies Bắt Buộc:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`, `__Secure-1PSIDTS`.

---

## 2. Danh Mục Các RPC Quản Lý Thùng Rác

### 2.1. Chuyển Dự Án Vào Thùng Rác (Soft Delete - RPC `dK3x9`)

Khi người dùng chọn "Xóa dự án" trên giao diện danh sách hoặc trong menu dự án, hệ thống không xóa ngay lập tức mà chuyển cờ trạng thái dự án sang thùng rác (Soft Deleted) để lưu trữ dự phòng trong 30 ngày.

* **Query Param:** `rpcids=dK3x9`
* **Cấu trúc Payload gửi đi:**
```json
[
  [
    [
      "dK3x9",
      "[\"projects/<PROJECT_UUID_V4>\"]",
      null,
      "generic"
    ]
  ]
]
```

* **Dữ liệu Server trả về:**
```json
[
  1,
  1790172700000
]
```
*(Số nguyên `1` xác nhận dự án đã được chuyển vào thùng rác an toàn)*.

---

### 2.2. Lấy Danh Sách Dự Án Trong Thùng Rác (List Trash - RPC `tB6q8`)

Truy xuất toàn bộ danh mục các dự án đang nằm trong thùng rác kèm thời điểm bị xóa và thời gian còn lại trước khi tự động dọn sạch.

* **Query Param:** `rpcids=tB6q8`
* **Cấu trúc Payload gửi đi:**
```json
[
  [
    [
      "tB6q8",
      "[]",
      null,
      "generic"
    ]
  ]
]
```

* **Dữ liệu Server trả về:**
```typescript
type TrashListResponse = Array<[
  project_uuid: string,                         // "<PROJECT_UUID>"
  project_title: string,                        // "<PROJECT_TITLE>"
  deleted_timestamp_ms: number,                 // Thời điểm chuyển vào thùng rác
  auto_purge_days_remaining: number,            // Số ngày còn lại trước khi xóa vĩnh viễn (ví dụ: 29)
  assets_count: number                          // Tổng số ảnh và video bên trong
]>;
```

---

### 2.3. Khôi Phục Dự Án Khỏi Thùng Rác (Restore Project - RPC `rS4y1`)

Đưa dự án trong thùng rác trở lại danh sách dự án đang hoạt động (`UpteDb`).

* **Query Param:** `rpcids=rS4y1`
* **Cấu trúc Payload gửi đi:**
```json
[
  [
    [
      "rS4y1",
      "[\"projects/<PROJECT_UUID_V4>\"]",
      null,
      "generic"
    ]
  ]
]
```

* **Dữ liệu Server trả về:**
```json
[
  1,
  "RESTORED"
]
```
*(Dự án được phục hồi nguyên vẹn cùng toàn bộ đồ thị PINHOLE và các tài sản truyền thông đã render)*.

---

### 2.4. Dọn Rác Vĩnh Viễn / Xóa Ngay Lập Tức (Hard Purge - RPC `mrlkwd`)

Xóa vĩnh viễn dự án khỏi máy chủ và giải phóng toàn bộ file video/ảnh trên Google Cloud Storage.

* **Query Param:** `rpcids=mrlkwd`
* **Cấu trúc Payload gửi đi:**
```json
[
  [
    [
      "mrlkwd",
      "[\"projects/<PROJECT_UUID_V4>\"]",
      null,
      "generic"
    ]
  ]
]
```
* **Dữ liệu Server trả về:** `[1]`

---

## 3. Lệnh cURL Mẫu Khôi Phục Dự Án Ngoài Trình Duyệt

```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=rS4y1&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<FLOW_AT_TOKEN>" \
  --data-urlencode 'f.req=[[["rS4y1","[\"projects/<PROJECT_UUID>\"]",null,"generic"]]]'
```
