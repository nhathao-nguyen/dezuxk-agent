# Chức năng: Cơ Chế Điều Khiển Camera Chuyển Động (Advanced Camera Movement Matrix)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) và ma trận tham số điều khiển góc quay camera không gian 3 chiều (Cinematic Camera Motion Controls) của mô hình Veo 3.1 trên Google Flow (`flow.google.com`).

---

## 1. Yêu Cầu Cơ Bản

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Cơ chế:** Nhúng đối tượng cấu hình `camera_motion` vào mảng cấu hình thế hệ video của `FlowCreationAgentService`.
* **Headers & Cookies Bắt Buộc:** Cặp cookie `OSID` / `__Secure-OSID` kèm CSRF token `at`.

---

## 2. Ma Trận Tham Số Chuyển Động Camera 3D (Camera Vector Matrix)

Google Veo 3.1 lượng hóa các chuyển động của camera bằng hệ tọa độ Descartes và vector góc Euler chuẩn hóa trong khoảng `[-1.0, 1.0]`:

| Tên Chuyển Động | Khóa Tham Số (Wire Key) | Miền Giá Trị (Range) | Chiều Hướng Vật Lý & Hiệu Ứng Điện Ảnh |
| :--- | :--- | :---: | :--- |
| **Quay ngang sang trái** | `pan_horizontal` | `[-1.0 .. 0.0)` | **Pan Left**: Ống kính xoay ngang từ phải sang trái quanh trục Y. |
| **Quay ngang sang phải** | `pan_horizontal` | `(0.0 .. 1.0]` | **Pan Right**: Ống kính xoay ngang từ trái sang phải quanh trục Y. |
| **Nghiêng ngước lên trên** | `tilt_vertical` | `(0.0 .. 1.0]` | **Tilt Up**: Ống kính hướng dần lên trời theo trục X. |
| **Nghiêng chúc xuống dưới** | `tilt_vertical` | `[-1.0 .. 0.0)` | **Tilt Down**: Ống kính hướng dần xuống mặt đất theo trục X. |
| **Thu phóng cận cảnh** | `zoom_depth` | `(0.0 .. 1.0]` | **Zoom In**: Phóng to tiêu cự làm chủ thể chiếm diện tích lớn hơn trong khung hình. |
| **Thu phóng góc rộng** | `zoom_depth` | `[-1.0 .. 0.0)` | **Zoom Out**: Thu nhỏ tiêu cự, mở rộng không gian bối cảnh xung quanh. |
| **Xoay quỹ đạo theo chiều kim đồng hồ**| `orbit_trajectory` | `(0.0 .. 1.0]` | **Orbit Clockwise**: Camera bay vòng tròn quanh tâm đối tượng từ trái qua phải. |
| **Xoay quỹ đạo ngược chiều kim đồng hồ**| `orbit_trajectory`| `[-1.0 .. 0.0)` | **Orbit Counter-Clockwise**: Camera bay vòng tròn quanh tâm từ phải qua trái. |
| **Trượt ngang (Truck)** | `truck_lateral` | `[-1.0 .. 1.0]` | Toàn bộ thân máy quay di chuyển tịnh tiến sang trái/phải trên ray trượt. |
| **Nâng hạ trục đứng (Pedestal)** | `pedestal_vertical` | `[-1.0 .. 1.0]` | Toàn bộ máy quay nâng cao hoặc hạ thấp theo phương thẳng đứng. |

---

## 3. Cấu Trúc Payload Request Gửi Lên Server (Request Schema)

Khối cấu hình `camera_motion` được đặt trong đối tượng thế hệ video:

```typescript
interface CameraMotionConfig {
  // Loại chuyển động chính
  motion_preset: 
    | "CUSTOM_VECTOR" 
    | "ORBIT" 
    | "DOLLY_ZOOM" 
    | "PAN_TILT" 
    | "STATIC";

  // Vector chuyển động chuẩn hóa
  motion_vector: {
    pan_horizontal: number;                     // -1.0 (Trái hết mức) đến 1.0 (Phải hết mức)
    tilt_vertical: number;                      // -1.0 (Chúc xuống) đến 1.0 (Ngước lên)
    zoom_depth: number;                         // -1.0 (Zoom Out) đến 1.0 (Zoom In)
    orbit_trajectory: number;                   // -1.0 (Ngược chiều kim) đến 1.0 (Cùng chiều kim)
    truck_lateral?: number;                     // Tịnh tiến ngang
    pedestal_vertical?: number;                 // Tịnh tiến dọc
  };

  // Hệ số tốc độ di chuyển
  speed_multiplier: number;                     // 0.5 (Chậm rãi, mượt) đến 2.0 (Nhanh, kịch tính)

  // Khóa ổn định chống rung (Stabilization)
  smoothness_factor: number;                    // 0.0 đến 1.0 (Mặc định 0.8)
}
```

### Vị trí lồng trong `f.req` của `FlowCreationAgentService/StreamChat`:
```json
[
  null,
  "[\"<REQUEST_UUID>\", [[[[ \"A drone shot flying through neon skyscrapers...\" ]]]], [\"projects/<PROJECT_UUID>\", null, [\"<SESSION_TOKEN>\", 1], {\"model_id\": \"veo_3_1_quality\", \"duration_seconds\": 8, \"aspect_ratio\": 2, \"camera_motion\": {\"motion_preset\": \"CUSTOM_VECTOR\", \"motion_vector\": {\"pan_horizontal\": 0.5, \"tilt_vertical\": -0.3, \"zoom_depth\": 0.8, \"orbit_trajectory\": 0.0}, \"speed_multiplier\": 1.0, \"smoothness_factor\": 0.85}}, null, 1]]"
]
```

---

## 4. Dữ Liệu Phản Hồi Xác Nhận Góc Quay Từ Máy Chủ

Trong luồng stream phản hồi, server xác nhận vector camera đã được phân giải sang đường đi nội suy 3D:

```json
{
  "task_status": "RENDERING",
  "camera_motion_acknowledged": {
    "preset_applied": "CUSTOM_VECTOR",
    "keyframe_trajectory": [
      { "time_sec": 0.0, "position": [0, 0, 0], "rotation": [0, 0, 0] },
      { "time_sec": 4.0, "position": [2.5, -1.2, 5.0], "rotation": [15.0, -10.0, 0] },
      { "time_sec": 8.0, "position": [5.0, -2.4, 10.0], "rotation": [30.0, -20.0, 0] }
    ]
  }
}
```
