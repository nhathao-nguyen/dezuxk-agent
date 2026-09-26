# Chức năng: Hệ Thống Âm Nhạc & Âm Thanh Nền (Flow Music & Audio Engine - MusicFX)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của hệ thống tạo nhạc nền (Background Music), âm thanh môi trường (SFX) và đồng bộ âm thanh tự động của Google Flow (`flow.google.com`) tích hợp cùng hạ tầng mô hình MusicFX của Google Labs.

---

## 1. Yêu Cầu Cơ Bản & Chi Phí Hạn Mức

* **Chi phí Credit:** Khấu trừ **5 - 10 credits** cho mỗi bản nhạc hoàn chỉnh (đồng bộ 4s - 30s).
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mX9w1`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Cookies Bắt Buộc:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`.

---

## 2. Cấu Trúc Request Tạo Nhạc Nền Đồng Bộ (Request Payload Schema)

Client gửi chỉ thị âm nhạc, thể loại, tốc độ nhịp điệu (BPM) và định danh video Veo mục tiêu để máy chủ căn chỉnh thời lượng khớp từng mili-giây.

* **Form Fields:** `at=<FLOW_AT_TOKEN>` và `f.req`

### Cấu trúc mảng `MUSIC_GEN_PAYLOAD`:

```typescript
type FlowMusicGenerationPayload = [
  // [0] Đường dẫn tài nguyên dự án
  project_resource_path: string,                // "projects/<PROJECT_UUID_V4>"

  // [1] Cấu hình âm nhạc MusicFX
  {
    // Mô tả phong cách âm nhạc
    music_prompt: string,                       // Ví dụ: "Cyberpunk synthwave electronic beat with dark bassline"
    genre: "ELECTRONIC" | "CINEMATIC" | "AMBIENT" | "LOFI" | "ORCHESTRAL",
    mood: "DRAMATIC" | "ENERGETIC" | "MELANCHOLY" | "UPLIFTING",
    tempo_bpm: number,                          // Nhịp điệu: 60 - 180 BPM
    
    // Đồng bộ với video
    sync_to_video_asset_id?: string,            // UUID của video Veo cần lồng nhạc
    target_duration_seconds: number,            // 4, 6, 8, 10 hoặc tự động khớp video
    
    // Cấu hình âm thanh đầu ra
    audio_format: "WAV" | "MP3",
    sample_rate_hz: 44100 | 48000,
    channels: 2                                 // Stereo
  },

  // [2] Khóa phiên tương tác
  session_token: string,

  // [3] Client Request Tracking GUID
  client_request_guid: string
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về (Response Wire Schema)

Phản hồi trả về chuỗi JSON chứa siêu dữ liệu tệp âm thanh hoàn tất:

### Cấu trúc JSON sau khi Parse Envelope `wrb.fr`:

```typescript
interface MusicFXGenerationResponse {
  task_status: "COMPLETED";
  audio_asset: {
    asset_id: string;                           // UUID tài nguyên âm thanh: "audio_asset_uuid_777"
    url: string;                                // URL tải tệp: "https://storage.googleapis.com/flow-rendered-audio/music_777.wav"
    mime_type: "audio/wav";
    duration_seconds: number;                   // Thời lượng thực tế (khớp video)
    sample_rate: 44100;
    bitrate_kbps: 1411;
    file_size_bytes: number;
    
    // Cột mốc Beat Markers (Dùng để tự động cắt ghép video theo nhịp nhạc)
    beat_timestamps: number[];                  // [0.0, 0.5, 1.0, 1.5, 2.0,...]
  };
  credits_deducted: 5;
  remaining_credits: number;
}
```

---

## 4. Lệnh cURL Mẫu Gửi Trực Tiếp Từ Terminal

```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=mX9w1" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<FLOW_AT_TOKEN>" \
  --data-urlencode 'f.req=[[["mX9w1","[\"projects/<PROJECT_UUID>\",{\"music_prompt\":\"<PROMPT_TEXT>\",\"genre\":\"CINEMATIC\",\"mood\":\"DRAMATIC\",\"tempo_bpm\":120,\"target_duration_seconds\":8,\"audio_format\":\"WAV\",\"sample_rate_hz\":44100,\"channels\":2},\"<SESSION_LOCK_TOKEN>\",\"<CLIENT_GUID>\"]",null,"generic"]]]'
```
