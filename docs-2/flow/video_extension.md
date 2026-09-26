# Chức năng: Mở Rộng Thời Lượng Video (Video Extension Mode - veo_3_1_extend)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của cơ chế nối dài video (Video Extension) trên Google Flow (`flow.google.com`). Tính năng này cho phép lấy một đoạn video 4 giây hoặc 6 giây đã tạo trước đó, trích xuất khung hình cuối cùng (Last Keyframe) làm điều kiện biên, và tạo tiếp 4 giây hoặc 6 giây tiếp theo với chuyển động mượt mà, bảo đảm tính nhất quán về nhân vật, ánh sáng và bối cảnh.

---

## 1. Yêu Cầu Cơ Bản & Chi Phí Hạn Mức

* **Model ID:** `veo_3_1_extend` (hoặc `veo_3_1_quality` kèm cờ extension).
* **Chi phí Credit:**
  * Mở rộng thêm 4 giây: **20 credits** (Chất lượng Fast) / **50 credits** (Chất lượng Quality).
  * Mở rộng thêm 6 giây: **30 credits** (Fast) / **75 credits** (Quality).
* **Endpoint:** `POST https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat`
* **Cookies Bắt Buộc:** `OSID`, `__Secure-OSID`, `__Secure-1PSID`.

---

## 2. Cấu Trúc Request Nối Dài Video (Request Payload Schema)

Khác với việc tạo mới từ đầu, request nối dài video **bắt buộc phải truyền `parent_asset_id`** và cờ nghiệp vụ `extension_mode: true`.

### Cấu trúc JSON bên trong `f.req`:

```typescript
type FlowVideoExtensionPayload = [
  // [0] Client Turn UUID mới
  extension_request_uuid: string,

  // [1] Prompt miêu tả diễn biến tiếp theo của câu chuyện
  [
    [
      [
        [
          continuation_prompt_text: string      // Ví dụ: "Tiếp tục máy bay lượn vòng qua tòa tháp và hạ cánh xuống sân đỗ"
        ]
      ]
    ]
  ],

  // [2] Ngữ cảnh dự án và tham chiếu video gốc
  [
    project_resource_path: string,              // "projects/<PROJECT_UUID_V4>"

    // Mảng thành phần tham chiếu (Reference Ingredients)
    [
      {
        ingredient_type: 4,                     // 4: Video Extension Anchor (Điểm tựa nối dài)
        source_video_asset_id: string,          // UUID của video gốc: "video_asset_uuid_12345"
        cut_at_timestamp_seconds?: number       // Vị trí cắt nối (mặc định lấy khung hình cuối cùng)
      }
    ],

    // Khóa phiên tương tác (Session Lock từ csbIsb)
    [
      session_token: string,
      is_active: 1
    ],

    // Cấu hình thế hệ mở rộng
    {
      model_id: "veo_3_1_extend",
      extension_duration_seconds: 4 | 6,        // Thời lượng nối dài thêm
      preserve_physics: true,                   // Bảo toàn quán tính vật lý và quỹ đạo chuyển động
      camera_motion_continuation: "MAINTAIN"    // Tiếp nối góc quay cũ hoặc áp dụng vector mới
    },

    null,
    1
  ]
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về Từ Máy Chủ

Server trả về luồng HTTP chunked stream báo cáo quá trình ghép nối và sinh video:

### Khối Hoàn Tất Nối Dài (Completion Chunk):

```typescript
interface ExtendedVideoResult {
  task_status: "COMPLETED";
  output_assets: [
    {
      asset_id: string;                         // UUID mới của video đã nối dài hoàn chỉnh
      url: string;                              // URL tải MP4 hoàn chỉnh: "https://storage.googleapis.com/..."
      media_type: "video/mp4";
      resolution: "1280x720";
      
      // Thời lượng tổng hợp sau khi ghép
      total_duration_seconds: number;           // Ví dụ: video gốc 6s + nối 4s = 10s
      
      // Khung thời gian phân đoạn
      segments_timeline: [
        { segment_id: 1, duration: 6.0, source: "parent_asset" },
        { segment_id: 2, duration: 4.0, source: "extension_render" }
      ]
    }
  ];
  credits_deducted: number;                     // 20 credits
  remaining_credits: number;
}
```

---

## 4. Lệnh cURL Mẫu Gửi Trực Tiếp Từ Terminal

```bash
curl -X POST "https://flow.google.com/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://flow.google.com" \
  -H "Referer: https://flow.google.com/" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: OSID=<COOKIE>; __Secure-OSID=<COOKIE>; __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<FLOW_AT_TOKEN>" \
  --data-urlencode 'f.req=[null,"[\"<EXT_REQUEST_UUID>\",[[[[\"<CONTINUATION_PROMPT_TEXT>\"]]]],[\"projects/<PROJECT_UUID>\",[{\"ingredient_type\":4,\"source_video_asset_id\":\"<SOURCE_ASSET_ID>\"}],[\"<SESSION_LOCK_TOKEN>\",1],{\"model_id\":\"veo_3_1_extend\",\"extension_duration_seconds\":4,\"preserve_physics\":true},null,1]]"]'
```
