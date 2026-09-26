# Chức năng: Tạo Video Chuyên nghiệp (Video Generation - Veo 3.1)

Chức năng tạo video AI điện ảnh trên Google Flow thông qua gia đình mô hình **Google Veo 3.1**.

---

## 1. Thông tin Endpoint & Giao thức

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookie cần thiết:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`, `__Secure-1PSIDTS`.

---

## 2. Các Tùy chọn (Options) khi Tạo Video

Dữ liệu trích xuất từ cấu hình phần cứng mô hình Veo trên server Google:

| Tùy chọn | Các giá trị hỗ trợ trên Server | Mô tả |
| :--- | :--- | :--- |
| **Model Video** | `veo_3_1_quality`, `veo_3_1_fast`, `veo_3_1_lite`, `veo_3_1_lite_low_priority` | Các biến thể mô hình Veo từ tốc độ cao đến chất lượng điện ảnh tối đa. |
| **Thời lượng (Duration)** | `4s`, `6s`, `8s`, `10s` | Độ dài đoạn video trong một lần render (ví dụ: `veo_3_1_t2v_fast_4s`, `veo_3_1_t2v_quality_6s`). |
| **Độ phân giải (Resolution)** | `360p`, `720p`, `1080p` (Upsampler 1080P), `4K` (Upsampler 4K) | Cho phép render nhanh ở 720p rồi upscale lên 1080p (miễn phí) hoặc 4K (50 credits). |
| **Tỷ lệ khung hình** | `Landscape` (16:9 ngang), `Portrait` (9:16 dọc / Shorts / TikTok) | Định cấu hình khung hình vật lý của video. |
| **Điều khiển Camera (Camera Control)** | `veo_2_camera_control` | Điều khiển quỹ đạo chuyển động của camera (Pan trái/phải, Tilt lên/xuống, Zoom in/out, Dolly). |
| **Kéo dài Video (Video Extension)** | `veo_3_1_extend_fast_4s_relaxed`, `veo_3_1_extend_fast_6s_relaxed` | Mở rộng đoạn video đã có thêm 4 giây hoặc 6 giây dựa trên khung hình cuối. |
| **Tái quay cảnh (Reshoot)** | `veo_3_0_reshoot_landscape`, `veo_3_0_reshoot_portrait` | Giữ nguyên chuyển động nhân vật nhưng thay đổi góc quay hoặc ánh sáng. |
| **Nội suy khung hình (Interpolation)** | `veo_2_1_fast_d_15_with_start_image_and_end_image_interpolation` | Nhận ảnh đầu và ảnh cuối rồi tự động sinh video nối liền mượt mà giữa 2 ảnh. |

---

## 3. Cấu trúc Payload gửi lên Server

```json
[
  null,
  "[\"request_uuid_v4\", [[[[ \"A drone shot descending over misty pine forest with a winding river during golden hour\" ]]]], [\"projects/project_uuid_v4\", null, [\"client_session_state_token\", 1], null, null, 1]]"
]
```

### Các trường cấu hình trong mảng điều khiển video:
* `model_identifier`: Chọn `veo_3_1_quality`, `veo_3_1_fast` hoặc `veo_3_1_lite`.
* `duration_code`: `4`, `6`, `8`, hoặc `10`.
* `aspect_code`: `1` (Portrait) hoặc `2` (Landscape).
* `start_image_token`: Token ảnh bắt đầu (nếu dùng chế độ Image-to-Video `i2v`).
* `end_image_token`: Token ảnh kết thúc (nếu dùng chế độ First & Last Frame Interpolation).

---

## 4. Dữ liệu Server xử lý và Trả về

* **Khối tiến độ (Progress Stream):** Server cập nhật tỷ lệ hoàn thành render trên GPU cluster (`25%`, `50%`, `75%`, `100%`).
* **Khối hoàn thành (Asset Output):**
  ```json
  {
    "status": "COMPLETED",
    "video_asset": {
      "asset_id": "video_asset_uuid_67890",
      "url": "https://storage.googleapis.com/flow-rendered-videos/output_67890.mp4",
      "mime_type": "video/mp4",
      "resolution": "1280x720",
      "duration": 6.0,
      "fps": 24
    },
    "credits_deducted": 20
  }
  ```
