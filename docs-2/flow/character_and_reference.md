# Chức năng: Nhân vật Nhất quán & Hình ảnh Tham chiếu (Character & Reference)

Google Flow hỗ trợ tính năng duy trì sự nhất quán của nhân vật (Character Consistency), phong cách hình ảnh tham chiếu (Style Reference), và thư viện giọng đọc đa dạng (Voice Personas).

---

## 1. Cơ chế Tra cứu Nhân vật & Giọng đọc AI (RPC `Zzl0ze`)

* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=Zzl0ze&bl=boq_labs-ai-sandbox-frontend_20260922.00_p0&rt=c`
* **Cookie bắt buộc:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`, `__Secure-1PSIDTS`.
* **Headers bắt buộc:**
  ```http
  Content-Type: application/x-www-form-urlencoded;charset=UTF-8
  Origin: https://flow.google.com
  Referer: https://flow.google.com/
  X-Same-Domain: 1
  ```

### Quy tắc Tham số Đã Xác Minh Thực Tế:
> **LƯU Ý QUAN TRỌNG:** RPC `Zzl0ze` **không chấp nhận** wildcard `projects/*`. Request bắt buộc phải truyền mã dự án cụ thể dạng `projects/<project_uuid>`.

### Cấu trúc Payload Form `f.req`:
```json
[
  [
    [
      "Zzl0ze",
      "[\"projects/1cfeebb6-4d61-4373-aa21-dd52fd93679f\", null, null, null, [1]]",
      null,
      "generic"
    ]
  ]
]
```

### Lệnh cURL Mẫu Gửi Trực Tiếp từ Terminal:
```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=Zzl0ze&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "f.req=[[[\"Zzl0ze\",\"[\\\"projects/1cfeebb6-4d61-4373-aa21-dd52fd93679f\\\",null,null,null,[1]]\",null,\"generic\"]]]" \
  --data-urlencode "at=<FLOW_AT_TOKEN>"
```

---

## 2. Thư viện 30 Nhân vật & Giọng đọc AI (Trích xuất từ Server)

Dữ liệu mảng `index [3]` từ phản hồi RPC `Zzl0ze` trả về danh mục 30 nhân vật mẫu lồng tiếng:

| Tên Nhân vật | ID Server | Giới tính & Đặc trưng Giọng | Đường dẫn Mẫu Âm thanh Server |
| :--- | :--- | :--- | :--- |
| **Achernar** | `achernar` | Nữ, nhẹ nhàng, cao độ thanh mảnh (*Female, soft, high pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Achernar.wav` |
| **Achird** | `achird` | Nam, thân thiện, cao độ trung bình (*Male, friendly, mid pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Achird.wav` |
| **Algenib** | `algenib` | Nam, trầm khàn, cao độ thấp (*Male, gravelly, low pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Algenib.wav` |
| **Algieba** | `algieba` | Nam, thoải mái, cao độ trung-trầm (*Male, easy-going, mid-low pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Algieba.wav` |
| **Alnilam** | `alnilam` | Nam, đanh thép, dứt khoát (*Male, firm, mid-low pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Alnilam.wav` |
| **Aoede** | `aoede` | Nữ, trong trẻo, tự nhiên (*Female, breezy, mid pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Aoede.wav` |
| **Autonoe** | `autonoe` | Nữ, tươi sáng, giàu năng lượng (*Female, bright, mid pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Autonoe.wav` |
| **Callirrhoe** | `callirrhoe` | Nữ, trầm ấm, gần gũi (*Female, easy-going, mid pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Callirrhoe.wav` |
| **Charon** | `charon` | Nam, truyền cảm, tin tức thời sự (*Male, informative, lower pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Charon.wav` |
| **Despina** | `despina` | Nữ, mượt mà, cuốn hút (*Female, smooth, mid pitch*) | `https://gstatic.com/aitestkitchen/voices/samples/Despina.wav` |
*(Và 20 nhân vật khác trong mảng danh mục Zzl0ze của server)*.

---

## 3. Cơ chế Hình ảnh Tham chiếu (Reference Images & Ingredients)

Trong Flow, người dùng kéo thả các "thành phần" (Ingredients) vào prompt để máy chủ định hướng tạo hình:

1. **Khung hình bắt đầu (Start Image - `i2v_s`):**
   * Video sẽ bắt đầu chính xác từ bức ảnh này rồi chuyển động dần theo prompt.
2. **Khung hình đầu và cuối (First & Last Frame Interpolation - `i2v_s_fl`):**
   * Người dùng cung cấp 2 ảnh. Server Google Veo sẽ tự động tạo chuyển động trung gian kết nối mượt mà giữa khung hình đầu và khung hình cuối.
3. **Hình ảnh tham chiếu phong cách & Nhân vật (`r2v` - Reference to Video):**
   * Tách khuôn mặt nhân vật hoặc bảng màu phong cách và duy trì sự đồng nhất qua nhiều cảnh quay khác nhau.
4. **Vùng chỉnh sửa (Inpainting Mask - `abra_edit`):**
   * Cung cấp ảnh gốc + mặt nạ vùng chọn (mask) để chèn hoặc xóa nhân vật khỏi khung cảnh.

---

## 4. Cấu trúc Payload gửi lên Agent khi kích hoạt Nhân vật
Khi chọn một persona nhân vật cho video, client đưa mã định danh của nhân vật đó vào mảng tham số audio của `FlowCreationAgentService/StreamChat`:
```json
{
  "voice_persona_id": "achernar",
  "lip_sync_enabled": true,
  "dialogue_text": "Chào mừng bạn đến với thế giới sáng tạo Google Flow."
}
```
Máy chủ sẽ đồng bộ chuyển động môi của nhân vật trong video (Lip-sync) khớp hoàn hảo với nhịp điệu phát âm của tệp âm thanh WAV được tổng hợp.
