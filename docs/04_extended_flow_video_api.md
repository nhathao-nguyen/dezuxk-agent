# Module 04: Bộ API Mở Rộng Cho Google Flow & Veo Video (Extended Flow Video API)

> Tài liệu đặc tả các endpoint mở rộng độc quyền của Gateway cho việc khai thác tối đa sức mạnh của Google Flow Studio: Render video Veo 3.1, Nối dài thời lượng, Camera chuyển động 3D, Nâng cấp 4K, 30 giọng đọc nhân vật và thao tác sơ đồ đồ thị PINHOLE.

---

## 1. Danh Mục Các Endpoint Mở Rộng (Extended Endpoints)

| Phương Thức | Endpoint URI | Mô Tả Nghiệp Vụ | Chi Phí Tín Dụng |
| :---: | :--- | :--- | :---: |
| `POST` | `/v1/video/generations` | Tạo video mới qua Text-to-Video hoặc Image-to-Video | 10 - 100 credits |
| `POST` | `/v1/video/extend` | Mở rộng nối dài thời lượng video có sẵn (thêm 4s/6s) | 20 - 50 credits |
| `POST` | `/v1/video/upsample-4k` | Nâng cấp video lên độ phân giải 4K Ultra HD | 50 credits |
| `POST` | `/v1/video/camera-motion` | Tạo video kèm ma trận vector camera 3 chiều | 20 - 100 credits |
| `GET` | `/v1/flow/credits` | Tra cứu số dư credit và phân hạng gói tài khoản | 0 credits |
| `GET` | `/v1/flow/voices` | Lấy danh mục 30 nhân vật giọng đọc AI kèm mẫu WAV | 0 credits |
| `POST` | `/v1/flow/music` | Tạo nhạc nền đồng bộ nhịp điệu BPM (MusicFX) | 5 credits |
| `GET` | `/v1/flow/templates` | Tải thư viện 68 quy trình sáng tạo mẫu | 0 credits |
| `GET` | `/v1/flow/projects` | Lấy danh sách lịch sử dự án của tài khoản | 0 credits |
| `POST` | `/v1/flow/projects` | Khởi tạo dự án mới và cấp phát UUID | 0 credits |
| `DELETE`| `/v1/flow/projects/{id}` | Xóa vĩnh viễn dự án và giải phóng đồ thị | 0 credits |

---

## 2. Tạo Video Chuyên Nghiệp (`POST /v1/video/generations`)

### 2.1. Cấu Trúc Yêu Cầu Đầu Vào (Request Payload)

```json
{
  "model": "veo-3.1-quality",
  "prompt": "A cinematic wide angle drone shot flying over a futuristic neon city in rain at night, 8k, photorealistic",
  "image_url": "http://localhost:8080/v1/media/init_frame.png",
  "duration": 8,
  "aspect_ratio": "16:9",
  "stream": true
}
```

* **Tham số chi tiết:**
  * `model`: `"veo-3.1-quality"` (100 credits) | `"veo-3.1-fast"` (20 credits) | `"veo-3.1-lite"` (10 credits).
  * `prompt`: Câu lệnh mô tả cảnh quay bằng tiếng Anh hoặc tiếng Việt.
  * `image_url` *(Tùy chọn)*: Nếu truyền vào, Gateway tự động chuyển sang chế độ **Image-to-Video** (I2V), sử dụng hình ảnh làm khung hình bắt đầu (First Frame).
  * `duration`: `4`, `6`, `8`, hoặc `10` (giây).
  * `aspect_ratio`: `"16:9"` (Landscape) hoặc `"9:16"` (Portrait).

### 2.2. Luồng Xử Lý Nội Bộ Trên Gateway

1. **Pre-flight Check:** Gọi RPC `nzlxg` kiểm tra số dư khả dụng (`availableCredits >= requiredCredits`). Nếu không đủ, trả về `402 Payment Required`.
2. **Project Allocation:** Tự động lấy dự án mặc định hoặc sinh dự án mới qua RPC `jHPbke`.
3. **Session Lock:** Gọi RPC `csbIsb` với tham số `["projects/<UUID>"]` để làm chủ phiên tránh xung đột render.
4. **Trigger Generation:** Gửi request đến `FlowCreationAgentService/StreamChat`.
5. **Streaming Progress to Client:** Đọc luồng chunked từ Google và stream tiến độ về cho client theo chuẩn SSE:

```text
data: {"status":"PROCESSING","progress":15,"message":"Initializing render cluster"}

data: {"status":"PROCESSING","progress":50,"message":"Synthesizing frames"}

data: {"status":"PROCESSING","progress":85,"message":"Encoding H.264 video stream"}

data: {"status":"COMPLETED","progress":100,"video_url":"http://localhost:8080/v1/media/vid_91a0c4f2.mp4","original_url":"https://storage.googleapis.com/...","duration":8.0,"resolution":"1280x720","credits_deducted":20}

data: [DONE]

```

---

## 3. Mở Rộng Nối Dài Video (`POST /v1/video/extend`)

Cho phép kéo dài thêm 4 giây hoặc 6 giây từ video Veo có sẵn bằng cách sử dụng khung hình cuối cùng làm điều kiện biên:

```json
{
  "source_video_asset_id": "vid_91a0c4f2",
  "prompt": "The camera pans down to reveal a crowded night street market with steam rising",
  "extension_duration": 4
}
```

* **Giao thức backend:** Gateway gán đối tượng `ingredient_type: 4` trỏ đến asset ID gốc và cấu hình model `veo_3_1_extend`.

---

## 4. Điều Khiển Camera Chuyển Động 3D (`POST /v1/video/camera-motion`)

Cho phép can thiệp trực tiếp vào vector camera chuyển động không gian của mô hình Veo 3.1:

```json
{
  "model": "veo-3.1-quality",
  "prompt": "A medieval knight standing in front of a burning dragon fortress",
  "duration": 6,
  "camera": {
    "pan_horizontal": 0.5,
    "tilt_vertical": -0.3,
    "zoom_depth": 0.8,
    "orbit_trajectory": 0.4
  }
}
```

* **Bảng chỉ số vector camera (-1.0 đến 1.0):**
  * `pan_horizontal`: `-1.0` (Lướt sang trái) ── `+1.0` (Lướt sang phải).
  * `tilt_vertical`: `-1.0` (Ngửa lên trời) ── `+1.0` (Cúi xuống đất).
  * `zoom_depth`: `-1.0` (Zoom xa lùi lại) ── `+1.0` (Zoom sâu tiến tới).
  * `orbit_trajectory`: `-1.0` (Xoay tròn ngược chiều kim đồng hồ) ── `+1.0` (Xoay tròn thuận chiều).

---

## 5. Nâng Cấp Video Lên 4K Ultra HD (`POST /v1/video/upsample-4k`)

Kích hoạt mô hình siêu phân giải `veo_3_1_upsampler_4k` qua RPC `uW3g7e`:

```json
{
  "video_asset_id": "vid_91a0c4f2",
  "preserve_audio": true
}
```

* **Chi phí:** Khấu trừ 50 credits trên máy chủ Google.
* **Đầu ra:** Video phân giải 3840x2160, giữ nguyên nhịp khung hình và đường âm thanh đồng bộ.

---

## 6. Thư Viện 30 Giọng Đọc & Nhân Vật AI (`GET /v1/flow/voices`)

Truy xuất danh sách 30 nhân vật giọng đọc mẫu đã được xác minh qua RPC `Zzl0ze`:

### Phản Hồi:
```json
{
  "total": 30,
  "voices": [
    {
      "id": "achernar",
      "name": "Achernar",
      "gender": "male",
      "sample_audio_url": "https://gstatic.com/aitestkitchen/voices/samples/Achernar.wav"
    },
    {
      "id": "altair",
      "name": "Altair",
      "gender": "male",
      "sample_audio_url": "https://gstatic.com/aitestkitchen/voices/samples/Altair.wav"
    },
    {
      "id": "aoede",
      "name": "Aoede",
      "gender": "female",
      "sample_audio_url": "https://gstatic.com/aitestkitchen/voices/samples/Aoede.wav"
    },
    {
      "id": "despina",
      "name": "Despina",
      "gender": "female",
      "sample_audio_url": "https://gstatic.com/aitestkitchen/voices/samples/Despina.wav"
    }
  ]
}
```

---

## 7. Tra Cứu Tín Dụng & Cấp Bậc Gói Cước (`GET /v1/flow/credits`)

Gọi trực tiếp RPC `nzlxg` và trả về thông tin minh bạch cho client:

```json
{
  "total_credits": 592,
  "daily_active": true,
  "daily_grant": 50,
  "tier_level": 2,
  "tier_name": "Google AI Pro / Premium",
  "pricing_matrix": {
    "veo_3_1_quality": 100,
    "veo_3_1_fast": 20,
    "veo_3_1_lite": 10,
    "abra_image": 7,
    "upsampler_4k": 50
  }
}
```
