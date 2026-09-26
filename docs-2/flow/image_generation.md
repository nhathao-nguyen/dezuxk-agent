# Chức năng: Tạo Hình ảnh trong Studio (Image Generation)

Chức năng tạo hình ảnh chất lượng cao trên Google Flow thông qua các mô hình tạo ảnh được khám phá động từ máy chủ.

---

> [!IMPORTANT]
> **Nguyên tắc Phân giải Model Động:**
> Mã định danh mô hình (`model_id`) không được gán cứng cố định mà phải được lấy động từ danh mục mô hình tạo ảnh (`root[5]`) thông qua RPC `HTrJv` hoặc giá trị mặc định theo Tier tài khoản (`root[2]`).

---

## 1. Thông tin Endpoint & Phương thức

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookie cần thiết:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`, `__Secure-1PSIDTS`.

---

## 2. Các Tùy chọn (Options) khi Tạo Ảnh

Từ ma trận kỹ thuật trích xuất động từ máy chủ Google:

| Tùy chọn (Option) | Các giá trị hỗ trợ trên Server | Mô tả |
| :--- | :--- | :--- |
| **Model** | `dynamic_model_id` (Lấy từ `root[5]` qua `HTrJv`) | Mã định danh backend của mô hình tạo ảnh đang được chọn. |
| **Tỷ lệ khung hình (Aspect Ratio)** | `1:1` (Vuông), `16:9` (Ngang), `9:16` (Dọc), `4:3`, `3:4` | Quy định tỷ lệ khung hình ảnh thành phẩm. |
| **Độ phân giải (Resolution)** | `360p`, `720p`, `Upsample 2K`, `Upsample 4K` | Hỗ trợ nâng cấp độ phân giải qua các model Upsampler riêng trong `root[5]`. |
| **Chế độ chỉnh sửa (Inpainting / Edit)** | Biến thể `edit` tương ứng | Chỉnh sửa vùng chọn trên ảnh gốc dựa trên prompt. |
| **Số lượng sinh (Count)** | `1`, `2`, `3`, `4` ảnh | Số lượng biến thể sinh ra trong một lượt chạy. |

---

## 3. Cấu trúc Payload gửi lên Server

```json
[
  null,
  "[\"request_uuid_v4\", [[[[ \"A hyper-realistic cinematic portrait of a cybernetic warrior, 8k resolution, volumetric lighting\" ]]]], [\"projects/project_uuid_v4\", null, [\"client_session_state_token\", 1], null, null, 1]]"
]
```

### Nếu có hình ảnh tham chiếu (Image-to-Image / Inpainting):
Payload đính kèm thêm mảng tham chiếu tài nguyên đã upload:
```json
[
  "media_reference_token",
  "aspect_ratio_enum_code",
  "style_preset_id"
]
```

---

## 4. Dữ liệu Server xử lý và Phản hồi

Server gửi về luồng sự kiện phân đoạn HTTP Chunked:
```json
{
  "task_status": "COMPLETED",
  "model_used": "dynamic_model_id",
  "output_assets": [
    {
      "asset_id": "image_asset_uuid_12345",
      "url": "https://lh3.googleusercontent.com/ai-sandbox/output_12345.png",
      "mime_type": "image/png",
      "width": 1024,
      "height": 1024,
      "seed": 984572183
    }
  ],
  "credits_deducted": 0
}
```
* Số credit khấu trừ được cập nhật theo đúng chính sách định giá động của model và Tier tài khoản.
