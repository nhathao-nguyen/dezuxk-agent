# Bảng Tra Cứu Toàn Bộ Request API: Google Flow (Master Request Reference)

Tài liệu này tổng hợp **toàn bộ các HTTP Request thực tế** của tất cả các chức năng chính trên Google Flow (`flow.google.com`). Mỗi chức năng bao gồm: Endpoint, Method, Headers, Cookies yêu cầu, Cấu trúc Payload và Lệnh cURL mẫu có thể copy-paste chạy trực tiếp từ Terminal ngoài trình duyệt (Node.js/cURL/Python).

---

## 1. Yêu Cầu Cơ Bản Cho Mọi Request Flow

### 1.1. Headers Chuẩn Bắt Buộc (Bảo Vệ WAF & Định Tuyến)
```http
Content-Type: application/x-www-form-urlencoded;charset=UTF-8
Origin: https://flow.google.com
Referer: https://flow.google.com/
X-Same-Domain: 1
Sec-Fetch-Site: same-origin
Sec-Fetch-Mode: cors
Sec-Fetch-Dest: empty
User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36
```

### 1.2. Cookies Bắt Buộc (Origin-Bound Tokens)
Khác với Gemini (chỉ cần các cookie trên miền `google.com`), Google Flow bắt buộc phải có cặp cookie định danh theo miền riêng:
* `OSID`: Cookie nhận diện phiên đăng nhập độc quyền trên domain `flow.google.com`. Thiếu cookie này server trả về HTTP 401 hoặc chuyển hướng về `accounts.google.com`.
* `__Secure-OSID`: Phiên bản bảo mật truyền tải qua HTTPS.
* `__Secure-1PSID`: Token định danh tài khoản Google Master.
* `__Secure-1PSIDTS`: Token thời gian bảo mật (xoay vòng liên tục).
* `SIDCC` / `__Secure-1PSIDCC`: Token kiểm soát toàn vẹn phiên.

### 1.3. Cấu Trúc Form Data Chuẩn (`batchexecute`)
Tất cả các RPC của Flow chạy qua cơ chế Google Batchexecute:
* `f.req`: `[[["<RPC_ID>", "<INNER_JSON_PAYLOAD_ESCAPED>", null, "generic"]]]`
* `at`: CSRF token trích xuất từ `window.WIZ_global_data.SNlM0e` hoặc `window.WIZ_global_data.at` trong trang chủ Flow.

---

## 2. Danh Mục Các Request Chức Năng Chính

### 2.1. Tra Cứu Số Dư Tín Dụng Tài Khoản (Credits Balance - RPC `nzlxg`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=nzlxg`
* **Mục đích:** Kiểm tra tổng số credit còn lại, credit hàng ngày và phân hạng tài khoản (Tier).

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=nzlxg" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"nzlxg\",\"[]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
[1050, 1, 2, 2, null, 1050]
```
* `Index 0` (`1050`): Tổng số credit khả dụng (phần tử bắt buộc duy nhất theo spec 2026-09-23).
* `Index 1-5` (`1, 2, 2, null, 1050`): Chưa có spec (unmapped), gateway đếm unmapped fields và không tự ý gán tier hay gói cước.

---

### 2.2. Tra Cứu Gói Cước & Quyền Lợi Hàng Ngày (Plan Promo - RPC `cPZSdc`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=cPZSdc`
* **Mục đích:** Lấy thông tin banner khuyến mãi và lượng credit được tặng mỗi ngày.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=cPZSdc" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"cPZSdc\",\"[]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
["Your Google AI plan now comes with 50 additional Flow credits daily.", "2026-09-09-v0-ios-launch-banner"]
```

---

### 2.3. Bóc Tách Ma Trận Mô Hình, Tùy Chọn & Bảng Giá Credit (Model Matrix - RPC `HTrJv`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=HTrJv`
* **Mục đích:** Tải toàn bộ danh mục mô hình Veo 3.1 & Abra từ máy chủ kèm thời lượng hỗ trợ và chi phí credit cho từng cấu hình.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=HTrJv" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"HTrJv\",\"[]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
Bảng giá và thông số kỹ thuật 8 mô hình:
* `veo_3_1_quality`: 100 credits (Tối đa chất lượng điện ảnh, 4s/6s/8s/10s).
* `veo_3_1_fast`: 20 credits (Chuẩn 8s) / 10 credits (4s/6s Fast).
* `veo_3_1_lite`: 10 credits (Chuẩn 8s) / 5 credits (4s/6s Lite).
* `veo_3_1_lite_low_priority`: 5 credits (Hàng đợi off-peak).
* `abra` (Omni 1.1 Flash / Imagen 3): 7-15 credits (720p HD), 4-7 credits (360p Preview).
* `abra_edit` (Inpainting chỉnh sửa): 20 credits (720p), 10 credits (360p).
* `veo_3_1_upsampler_1080p`: 0 credits (Miễn phí cho gói Pro).
* `veo_3_1_upsampler_4k`: 50 credits.

---

### 2.4. Danh Sách Mô Hình Đang Trực Tuyến Khả Dụng (Active Models - RPC `yBhWQ`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=yBhWQ`
* **Mục đích:** Xác minh trạng thái online/offline của cụm GPU phục vụ từng mô hình.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=yBhWQ" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"yBhWQ\",\"[]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
[["abra", 1], ["veo_3_1_lite", 1], ["veo_3_1_quality", 1], ["veo_3_1_fast", 1]]
```

---

### 2.5. Tải Danh Mục 68 Mẫu Quy Trình Sáng Tạo (Templates - RPC `tRARke`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=tRARke`
* **Mục đích:** Tải toàn bộ thư viện Workflow Templates cộng đồng và của Google.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=tRARke" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"tRARke\",\"[]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
Mảng chứa 68 templates (ví dụ: `ThumbnailForge`, `StoryScene`, `CinematicTrailer`, v.v.) kèm cấu trúc node PINHOLE dựng sẵn.

---

### 2.6. Lấy Lịch Sử & Danh Sách Dự Án (Projects History - RPC `UpteDb`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=UpteDb`
* **Mục đích:** Lấy toàn bộ danh sách dự án của người dùng gồm UUID, tên dự án, thời gian cập nhật.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=UpteDb" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"UpteDb\",\"[]\",null,\"generic\"]]]" \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
[
  [
    ["469e7346-5db2-4176-b592-270287e0734b", "Dự án Cyberpunk 2026", 1790170867659, null, 1],
    ["725b0994-f17a-4526-8ae1-c7c2ac83d041", "Full Suite Project", 1790171037594, null, 1]
  ]
]
```

---

### 2.7. Khởi Tạo Dự Án Mới Cấp Phát UUID (Create Project - RPC `jHPbke`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jHPbke`
* **Mục đích:** Yêu cầu server tạo không gian dự án mới và sinh mã định danh UUID v4 duy nhất.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jHPbke" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode 'f.req=[[["jHPbke","[\"Dự án Phim Hoạt Hình Mới\"]",null,"generic"]]]' \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
["<PROJECT_UUID>", "Dự án Phim Hoạt Hình Mới", 1790171200000]
```

---

### 2.8. Đăng Ký Khóa Phiên Tương Tác Dự Án (Session Lock - RPC `csbIsb`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=csbIsb`
* **Mục đích:** Đăng ký quyền làm chủ phiên (Session Lock) cho một dự án cụ thể, ngăn chặn xung đột chỉnh sửa từ nhiều tab hoặc nhiều phiên.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=csbIsb" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode 'f.req=[[["csbIsb","[\"projects/<PROJECT_UUID>\"]",null,"generic"]]]' \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
[1]
```

---

### 2.9. Truy Xuất Đồ Thị Khối & Công Cụ PINHOLE (PINHOLE Graph - RPC `ngNC2`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=ngNC2`
* **Mục đích:** Lấy toàn bộ sơ đồ đồ thị node bên trong Studio của dự án (các node render ảnh Abra, node video Veo, display panel, reference media).

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=ngNC2" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode 'f.req=[[["ngNC2","[\"projects/<PROJECT_UUID>\"]",null,"generic"]]]' \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
[null, null, [[null, 3, 2, "narwhal_display"], ["abra", 2, 1], ["veo_3_1_fast", 1, 1]]]
```

---

### 2.10. Thư Viện 30 Nhân Vật & Mẫu Giọng Đọc AI (Character Personas - RPC `Zzl0ze`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=Zzl0ze`
* **Mục đích:** Lấy danh sách 30 nhân vật giọng đọc AI sẵn có kèm mẫu âm thanh WAV.
* **Quy tắc Kỹ thuật Đặc Biệt Đã Xác Minh:** RPC này **bắt buộc** phải truyền đúng mã định danh dự án cụ thể `["projects/<PROJECT_UUID>", null, null, null, [1]]`. Nếu truyền wildcard `projects/*` server sẽ trả về mảng rỗng `[]`.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=Zzl0ze" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode 'f.req=[[["Zzl0ze","[\"projects/<PROJECT_UUID>\",null,null,null,[1]]",null,"generic"]]]' \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
Mảng gồm 30 Voice Personas với cấu trúc:
```json
[
  "Achernar",
  "Achird",
  "Algenib",
  "Algorab",
  "Alioth",
  "Alkaid",
  "Alshain",
  "Altair",
  "Amalthea",
  "Ankaa"
]
```
Kèm đường dẫn mẫu WAV trực tiếp: `https://gstatic.com/aitestkitchen/voices/samples/<Tên_Nhân_Vật>.wav`.

---

### 2.11. Tạo Video Chuyên Nghiệp (Veo 3.1 StreamChat)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Mục đích:** Gửi prompt, kích hoạt cụm máy chủ render mô hình Veo 3.1 và nhận luồng dữ liệu tiến độ.
* **Cấu hình Tùy chọn Hỗ trợ:**
  * Model: `veo_3_1_quality`, `veo_3_1_fast`, `veo_3_1_lite`.
  * Thời lượng: `4s`, `6s`, `8s`, `10s`.
  * Tỷ lệ khung hình: `1` (Portrait 9:16) hoặc `2` (Landscape 16:9).
  * Chế độ nâng cao: `Camera Control`, `Interpolation` (nối giữa ảnh bắt đầu và ảnh kết thúc), `Extension` (kéo dài thêm 4s/6s).

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[\"VEO_REQ_UUID_001\",[[[[\"A cinematic wide angle drone shot flying over a futuristic neon city in rain at night, 8k, photorealistic\"]]]],[\"projects/<PROJECT_UUID>\",null,[\"SESSION_STATE_TOKEN\",1],null,null,1]]"]'
```

#### Dữ liệu Server Trả Về (HTTP Chunked Stream):
* Khối cập nhật tiến độ (Progress): `{"progress": 25}`, `{"progress": 75}`.
* Khối thành phẩm hoàn tất (Completed Video):
  ```json
  {
    "status": "COMPLETED",
    "video_asset": {
      "asset_id": "video_asset_uuid_999",
      "url": "https://storage.googleapis.com/flow-rendered-videos/output_999.mp4",
      "resolution": "1280x720",
      "duration": 6.0
    },
    "credits_deducted": 20
  }
  ```

---

### 2.12. Tạo Hình Ảnh Studio & Inpainting (Imagen 3 / Abra StreamChat)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Mục đích:** Sinh ảnh độ nét cao qua mô hình Abra (Omni 1.1 Flash / Imagen 3) hoặc chỉnh sửa inpainting ảnh có sẵn.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[\"ABRA_REQ_UUID_002\",[[[[\"A studio portrait of a futuristic astronaut in reflective visor, octane render, soft studio light\"]]]],[\"projects/<PROJECT_UUID>\",null,[\"SESSION_STATE_TOKEN\",1],null,null,1]]"]'
```

#### Dữ liệu Server Trả Về:
```json
{
  "task_status": "COMPLETED",
  "model_used": "abra",
  "output_assets": [
    {
      "asset_id": "image_asset_uuid_777",
      "url": "https://lh3.googleusercontent.com/ai-sandbox/output_777.png",
      "mime_type": "image/png",
      "width": 1024,
      "height": 1024,
      "seed": 847291039
    }
  ],
  "credits_deducted": 7
}
```

---

### 2.13. Tác Nhân Sáng Tạo & Lập Kế Hoạch Đa Bước (Creation Agent Planning)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Mục đích:** Yêu cầu Flow Creation Agent phân tích ý tưởng phức tạp, tự động mở rộng prompt (Prompt Expansion), phân cảnh kịch bản và đề xuất chuỗi node PINHOLE tương ứng.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[null,"[\"AGENT_PLAN_003\",[[[[\"Hãy lập kế hoạch tạo một video giới thiệu sản phẩm xe thể thao điện tử, gồm 3 phân cảnh từ góc rộng đến cận cảnh\"]]]],[\"projects/<PROJECT_UUID>\",null,[\"SESSION_STATE_TOKEN\",1],null,null,1]]"]'
```

#### Dữ liệu Server Trả Về:
Phản hồi stream trả về khối JSON cấu trúc kế hoạch gồm:
* `expanded_prompts`: Danh sách prompt chi tiết được tối ưu cho mô hình Veo 3.1.
* `camera_moves`: Chỉ định góc quay (`orbit`, `pan`, `dolly`).
* `suggested_models`: Đề xuất `veo_3_1_quality` cho cảnh quan trọng và `veo_3_1_fast` cho cảnh lướt.
* `node_insertion_plan`: Hướng dẫn tự động chèn node vào đồ thị PINHOLE của dự án.

---

### 2.14. Xóa Dọn Dẹp Dự Án (Delete Project - RPC `mrlkwd`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mrlkwd`
* **Mục đích:** Xóa vĩnh viễn dự án và giải phóng tài nguyên đồ thị liên quan trên máy chủ.

#### Lệnh cURL Mẫu:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mrlkwd" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode 'f.req=[[["mrlkwd","[\"projects/<PROJECT_UUID>\"]",null,"generic"]]]' \
  --data-urlencode "at=<SNlM0e_TOKEN>"
```

#### Dữ liệu Server Trả Về:
```json
[1]
```
*(HTTP 200 kèm phản hồi `[1]` xác nhận dự án đã được xóa khỏi hệ thống)*

---

### 2.15. Kích Hoạt Nâng Cấp Video Lên 4K (RPC `uW3g7e`)

* **Tài liệu chi tiết:** [upsampler_4k.md](upsampler_4k.md)
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=uW3g7e`
* **Chi phí:** Khấu trừ 50 credits.
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=uW3g7e" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["uW3g7e","[\"projects/<PROJECT_UUID>\",\"asset_720p_uuid\",{\"target_resolution\":\"4K\",\"upsampler_model_id\":\"veo_3_1_upsampler_4k\",\"preserve_audio\":true},\"session_lock_token\",\"guid_abc\"]",null,"generic"]]]'
```

---

### 2.16. Điều Khiển Camera Chuyển Động 3D Nâng Cao (Camera Motion Controls)

* **Tài liệu chi tiết:** [camera_movement.md](camera_movement.md)
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Tham số Vector:** `pan_horizontal` (-1.0 đến 1.0), `tilt_vertical` (-1.0 đến 1.0), `zoom_depth` (-1.0 đến 1.0), `orbit_trajectory` (-1.0 đến 1.0).

---

### 2.17. Mở Rộng Nối Dài Thời Lượng Video (Video Extension Mode - `veo_3_1_extend`)

* **Tài liệu chi tiết:** [video_extension.md](video_extension.md)
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Chi phí:** 20 - 50 credits (nối thêm 4s/6s).
* **Cơ chế:** Nhúng đối tượng `ingredient_type: 4` trỏ đến `source_video_asset_id` của video gốc và đặt `model_id: "veo_3_1_extend"`.

---

### 2.18. Hệ Thống Âm Nhạc & SFX Đồng Bộ (MusicFX - RPC `mX9w1`)

* **Tài liệu chi tiết:** [music_audio_engine.md](music_audio_engine.md)
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mX9w1`
* **Chi phí:** 5 credits.
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mX9w1" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["mX9w1","[\"projects/<PROJECT_UUID>\",{\"music_prompt\":\"Cyberpunk synthwave electronic\",\"genre\":\"ELECTRONIC\",\"tempo_bpm\":120,\"target_duration_seconds\":8,\"audio_format\":\"WAV\"},\"session_lock_token\",\"guid_123\"]",null,"generic"]]]'
```

---

### 2.19. Thao Tác Trực Tiếp Lên Đồ Thị Node PINHOLE

* **Tài liệu chi tiết:** [pinhole_graph_mutation.md](pinhole_graph_mutation.md)
* **Thêm Node (RPC `kF8z7b`):** `["projects/<ID>", "<NODE_TYPE>", {"pos_x": 450, "pos_y": 200}, "<TITLE>"]`
* **Nối chân Pins (RPC `jE2m9c`):**
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=jE2m9c&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["jE2m9c","[\"projects/<PROJECT_UUID>\",{\"source_node_id\":\"node_abra_111\",\"source_pin_id\":\"image_out\",\"target_node_id\":\"node_veo_222\",\"target_pin_id\":\"first_frame_in\"},\"lock_token_999\"]",null,"generic"]]]'
```
* **Xóa Node/Edge (RPC `dL5p2`):** `["projects/<ID>", ["node_id"], ["edge_id"]]`

---

### 2.20. Quản Lý Thùng Rác & Khôi Phục Dự Án (Trash & Restore)

* **Tài liệu chi tiết:** [trash_and_restore.md](trash_and_restore.md)
* **Xóa tạm vào thùng rác (RPC `dK3x9`):** `["projects/<PROJECT_UUID>"]`
* **Lấy danh sách thùng rác (RPC `tB6q8`):** `[]`
* **Khôi phục dự án (RPC `rS4y1`):** `["projects/<PROJECT_UUID>"]`
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=rS4y1&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["rS4y1","[\"projects/<PROJECT_UUID>\"]",null,"generic"]]]'
```

