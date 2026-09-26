# Chức năng: Kích Hoạt Nâng Cấp Video Lên 4K (veo_3_1_upsampler_4k)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của cơ chế nâng cấp độ phân giải video (Super Resolution / Upscaling) từ video 720p tiêu chuẩn của Veo 3.1 lên chất lượng 4K Ultra HD (3840x2160) trên Google Flow (`flow.google.com`).

---

## 1. Yêu Cầu Cơ Bản & Chi Phí Hạn Mức

* **Chi phí Credit:** Khấu trừ **50 credits** từ số dư tài khoản (`nzlxg`).
* **Thời gian xử lý trung bình:** 45 - 90 giây (xử lý nền theo hàng đợi GPU chuyên dụng).
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=uW3g7e`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookies Bắt Buộc:**
  ```http
  Cookie: OSID=<FLOW_OSID>; __Secure-OSID=<SECURE_OSID>; __Secure-1PSID=<GOOGLE_ID>; __Secure-1PSIDTS=<TIME_TOKEN>
  ```

---

## 2. Cấu Trúc Request Kích Hoạt Nâng Cấp 4K (Request Schema)

Dữ liệu được đóng gói vào form param `f.req` và `at`:
* `at`: `<FLOW_CSRF_TOKEN>`
* `f.req`: `[[["uW3g7e", JSON.stringify(UPSAMPLE_PAYLOAD), null, "generic"]]]`

### Cấu trúc mảng `UPSAMPLE_PAYLOAD`:

```typescript
type VideoUpsample4KPayload = [
  // [0] Đường dẫn tài nguyên dự án
  project_resource_path: string,                // "projects/<PROJECT_UUID_V4>"

  // [1] Mã định danh tài nguyên video 720p nguồn
  source_asset_id: string,                      // UUID của video Veo 3.1 đã tạo thành công

  // [2] Cấu hình mô hình nâng cấp
  {
    target_resolution: "4K",                    // Chuẩn đầu ra 3840x2160
    upsampler_model_id: "veo_3_1_upsampler_4k", // Model ID bóc tách từ ma trận HTrJv
    enhancement_level: 1,                       // 1: Tiêu chuẩn / 2: Tăng cường chi tiết vi mô
    preserve_audio: boolean                     // true: Giữ nguyên âm thanh gốc
  },

  // [3] Khóa phiên tương tác (Session Lock từ RPC csbIsb)
  session_token: string,

  // [4] Client Tracking UUID
  client_request_guid: string
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về (Response Wire Schema)

Server trả về mã tiến trình tác vụ chạy dài LRO (Long Running Operation):

### Khung phản hồi Envelope `wrb.fr`:
```text
)]}'\n
<BYTE_LENGTH>\n
[["wrb.fr", "uW3g7e", "<INNER_UPSAMPLE_RESPONSE>", null, null, null, null, null, 0]]\n
```

### Cấu trúc JSON sau khi Parse `INNER_UPSAMPLE_RESPONSE`:

```typescript
interface UpsampleTaskResponse {
  // 1. Định danh tác vụ nâng cấp
  task_id: string;                              // "upsample_job_<UUID>"
  status: "QUEUED" | "PROCESSING" | "COMPLETED";

  // 2. Thông số tài nguyên đầu ra (khi hoàn tất)
  output_asset?: {
    asset_id: string;                           // UUID mới của video 4K
    url: string;                                // URL tải: "https://storage.googleapis.com/flow-rendered-videos/4k_<HASH>.mp4"
    resolution: "3840x2160";
    bitrate_mbps: number;                       // ~35 - 50 Mbps
    fps: 30;
    duration: number;
    size_bytes: number;
  };

  // 3. Khấu trừ hạn mức
  credit_deduction: {
    amount: 50;
    balance_after: number;                      // Số dư credit mới sau khi trừ 50
  };
}
```

---

## 4. Lệnh cURL Mẫu Gửi Trực Tiếp Từ Terminal

```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=uW3g7e" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<FLOW_AT_TOKEN>" \
  --data-urlencode 'f.req=[[["uW3g7e","[\"projects/<PROJECT_UUID>\",\"<SOURCE_ASSET_ID>\",{\"target_resolution\":\"4K\",\"upsampler_model_id\":\"veo_3_1_upsampler_4k\",\"enhancement_level\":1,\"preserve_audio\":true},\"<SESSION_LOCK_TOKEN>\",\"<CLIENT_GUID>\"]",null,"generic"]]]'
```
