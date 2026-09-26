# Chức năng: Thao Tác Trực Tiếp Lên Đồ Thị PINHOLE (Graph Mutation RPCs)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của các RPC đột biến đồ thị (Graph Mutation) trong Google Flow Studio. Cho phép thêm nút (Add Node), nối các chân (Connect Pins / Create Edges), và xóa nút trên sơ đồ PINHOLE trực tiếp thông qua API mà không cần tương tác giao diện kéo thả bằng chuột.

---

## 1. Kiến Trúc Đồ Thị Node PINHOLE

Trong Google Flow:
* Workspace của một dự án được mô hình hóa thành một đồ thị có hướng (Directed Acyclic Graph - DAG) gọi là **PINHOLE Graph**.
* Các nút (Nodes) đại diện cho các công cụ:
  * `Node_Abra`: Bộ sinh/chỉnh sửa hình ảnh Imagen 3.
  * `Node_Veo`: Bộ sinh video điện ảnh Veo 3.1.
  * `Node_Prompt`: Hộp nhập câu lệnh văn bản.
  * `Node_MediaInput`: Hộp chứa hình ảnh/video tham chiếu tải lên.
  * `Node_Display`: Bảng hiển thị kết quả đầu ra (Narwhal Display Panel).
* Các chân (Pins) đại diện cho cổng đầu vào/đầu ra dữ liệu:
  * `image_output_pin` (Cổng ảnh ra của Abra).
  * `first_frame_input_pin` (Cổng nhận khung hình bắt đầu của Veo).
  * `prompt_input_pin` (Cổng nhận chuỗi câu lệnh).

---

## 2. Các RPC Đột Biến Đồ Thị (Graph Mutation RPCs)

Mọi thao tác đều thực thi qua endpoint `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute`.

---

### 2.1. Thêm Nút Mới Vào Đồ Thị (Add Node - RPC `kF8z7b`)

* **Query Param:** `rpcids=kF8z7b`
* **Cấu trúc Payload gửi đi:**
```json
[
  [
    [
      "kF8z7b",
      "[\"projects/<PROJECT_UUID>\", \"<NODE_TYPE>\", {\"pos_x\": <X_COORD>, \"pos_y\": <Y_COORD>}, \"<NODE_TITLE>\"]",
      null,
      "generic"
    ]
  ]
]
```
Trong đó:
* `NODE_TYPE`: `"abra"` | `"veo_3_1_quality"` | `"veo_3_1_fast"` | `"prompt_box"` | `"narwhal_display"`.
* `pos_x`, `pos_y`: Tọa độ vị trí đặt khối trên canvas (ví dụ: `{ "pos_x": 450, "pos_y": 200 }`).

* **Phản hồi Server trả về:**
```json
[
  "node_id_<NEW_NODE_UUID>",
  "SUCCESS",
  1790172600000
]
```

---

### 2.2. Nối Các Chân Giữa 2 Nút (Connect Pins / Create Edge - RPC `jE2m9c`)

Được dùng để liên kết tự động kết quả sinh ảnh của Abra làm khung hình đầu vào cho video Veo.

* **Query Param:** `rpcids=jE2m9c`
* **Cấu trúc Payload gửi đi:**
```typescript
type ConnectPinsPayload = [
  project_resource_path: string,                // "projects/<PROJECT_UUID>"
  
  // Khối thông tin kết nối (Edge Descriptor)
  {
    // Nút phát (Source Node)
    source_node_id: string,                     // Ví dụ: "node_abra_uuid_111"
    source_pin_id: "image_out" | "text_out",

    // Nút nhận (Target Node)
    target_node_id: string,                     // Ví dụ: "node_veo_uuid_222"
    target_pin_id: "first_frame_in" | "prompt_in" | "style_ref_in"
  },

  // Khóa phiên tương tác
  session_lock_token: string
];
```

* **Chuỗi Payload URL-encoded gửi trong `f.req`:**
```json
[
  [
    [
      "jE2m9c",
      "[\"projects/<PROJECT_UUID>\",{\"source_node_id\":\"<SOURCE_NODE_ID>\",\"source_pin_id\":\"image_out\",\"target_node_id\":\"<TARGET_NODE_ID>\",\"target_pin_id\":\"first_frame_in\"},\"<SESSION_LOCK_TOKEN>\"]",
      null,
      "generic"
    ]
  ]
]
```

* **Phản hồi Server trả về:**
```json
[
  "edge_id_<EDGE_UUID>",
  1
]
```
*(Xác nhận đường dây kết nối giữa 2 khối đã được thiết lập thành công trên máy chủ)*.

---

### 2.3. Xóa Nút Hoặc Đường Nối (Delete Node/Edge - RPC `dL5p2`)

* **Query Param:** `rpcids=dL5p2`
* **Cấu trúc Payload gửi đi:**
```json
[
  [
    [
      "dL5p2",
      "[\"projects/<PROJECT_UUID>\", [\"node_abra_111\"], [\"edge_id_333\"]]",
      null,
      "generic"
    ]
  ]
]
```
* **Phản hồi Server trả về:**
```json
[1]
```

---

## 3. Lệnh cURL Mẫu Kết Nối Tự Động 2 Node Ngoài Trình Duyệt

```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jE2m9c&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<FLOW_AT_TOKEN>" \
  --data-urlencode 'f.req=[[["jE2m9c","[\"projects/<PROJECT_UUID>\",{\"source_node_id\":\"<SOURCE_NODE_ID>\",\"source_pin_id\":\"image_out\",\"target_node_id\":\"<TARGET_NODE_ID>\",\"target_pin_id\":\"first_frame_in\"},\"<SESSION_LOCK_TOKEN>\"]",null,"generic"]]]'
```
