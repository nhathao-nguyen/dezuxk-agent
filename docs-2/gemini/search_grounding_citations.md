# Chức năng: Tìm Kiếm Thời Gian Thực & Nguồn Trích Dẫn (Google Search Grounding & Citations)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của cơ chế Google Search Grounding trên Google Gemini Web. Khi kích hoạt, Gemini tự động hoặc theo chỉ định truy vấn máy chủ Google Tìm Kiếm, tích hợp thông tin thời gian thực vào câu trả lời và trả về danh mục nguồn trích dẫn kèm siêu dữ liệu.

---

## 1. Yêu Cầu Cơ Bản Của Request

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Headers Bắt Buộc:**
  ```http
  Origin: https://gemini.google.com
  Referer: https://gemini.google.com/app
  X-Same-Domain: 1
  User-Agent: <CLIENT_USER_AGENT>
  ```
* **Cookies Bắt Buộc:**
  ```http
  Cookie: __Secure-1PSID=<MASTER_ID>; __Secure-1PSIDTS=<TIME_TOKEN>; __Secure-1PSIDCC=<INTEGRITY_TOKEN>; COMPASS=<BOT_TOKEN>
  ```

---

## 2. Cấu Trúc Dữ Liệu Gửi Đi (Request Payload Schema)

Request sử dụng cơ chế Form Data URL-encoded với 2 trường `at` và `f.req`.

* **Form Fields:**
  * `at`: `<CSRF_TOKEN_SNlM0e>` (Chuỗi trích xuất từ `WIZ_global_data.SNlM0e`).
  * `f.req`: `[null, JSON.stringify(INNER_ARRAY)]`

### Cấu trúc mảng vị trí `INNER_ARRAY` kích hoạt Search Grounding:

```typescript
type SearchGroundingInnerPayload = [
  // [0] Nội dung yêu cầu tìm kiếm
  [
    [
      prompt_text: string,                      // Câu lệnh yêu cầu tin tức/thời gian thực
      input_type: 0,
      null,
      null,
      null,
      attachment_metadata: null,
      0
    ],
    [locale: string],                           // Ví dụ: ["vi"], ["en"]
    
    // [2] Ngữ cảnh hội thoại
    [
      conversation_id: string,                  // "c_<ID>" hoặc "" (nếu chat mới)
      response_id: string,                      // "r_<ID>" hoặc ""
      choice_id: string,                        // "rc_<ID>" hoặc ""
      null, null, null, null, null, null,
      context_blob: string                      // Khóa đồng bộ ngữ cảnh
    ],
    
    null, null, null, [0], 1, null, null, 1, 0, null, null, null, null, null,
    [[0]], 0, null, null, null, null, null, null, null, null, 1, null, null,
    
    // [27] Tool Selector / Capabilities Mask (Kích hoạt công cụ tìm kiếm)
    [
      search_tool_enabled: 1                    // 1: Bật Search Grounding / 0: Tắt
    ],
    
    null, null, null, null, null, null, null, null, null, null, [1],
    null, null, null, null, null, null, null, null, null, null, null, 0,
    null, null, null, null, null,
    
    // [46] Client Sequence UUID
    client_request_uuid: string,                // UUID v4 sinh ngẫu nhiên
    
    null, [], null, null, null, null, null, 0, 1, null, null, null, null, null,
    null, null, null, null, null,
    
    // [67] - [68] Model Tier
    model_tier_code: number,                    // 1: Flash / 3: Pro
    reasoning_mode: number,                     // 0 hoặc 1
    
    null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, 0, null, 1
  ]
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về (Response Wire Schema)

Phản hồi streaming dạng `wrb.fr` chứa các chunk văn bản kèm siêu dữ liệu Grounding trong mảng mở rộng của candidate draft.

### Định dạng Chunk Streaming `wrb.fr`:
```text
)]}'\n
<BYTE_LENGTH>\n
[["wrb.fr", null, "<INNER_RESPONSE_JSON_ESCAPED>", null, null, null, null, null, 0]]\n
```

### Cấu trúc JSON sau khi Parse `INNER_RESPONSE_JSON_ESCAPED`:

```typescript
interface GroundingInnerResponse {
  // [1] Cập nhật định danh phiên
  1: [
    conversation_id: string,                    // "c_<HASH>"
    response_id: string                         // "r_<HASH>"
  ];

  // [4] Danh sách kết quả phản hồi
  4: Array<[
    choice_id: string,                          // "rc_<HASH>"
    
    // [1] Khối văn bản và trích dẫn inline
    [
      markdown_text: string,                    // Văn bản chứa các thẻ đánh dấu [1], [2] tương ứng nguồn
      null, null, null, null, null, null
    ],
    
    null, null, null,
    
    // [12] Khối Siêu Dữ Liệu Grounding (GroundingMetadata Block)
    [
      // [0] Danh sách các từ khóa mà Gemini đã truy vấn lên Google Search
      search_queries: Array<string>,            // Ví dụ: ["thời tiết Hà Nội hôm nay", "nhiệt độ hiện tại"]

      null,

      // [2] Mảng chi tiết các Nguồn Trích Dẫn (Web Sources)
      grounding_sources: Array<[
        source_index: number,                   // Chỉ mục nguồn (1-based hoặc 0-based)
        [
          uri: string,                          // URL đích của bài viết
          title: string,                        // Tiêu đề trang web
          snippet: string,                      // Đoạn trích dẫn tóm tắt nội dung báo/trang
          favicon_url: string,                  // "https://www.google.com/s2/favicons?domain=..."
          domain_display: string                // "vnexpress.net", "tuoitre.vn",...
        ]
      ]>,

      // [3] Ánh xạ đoạn văn bản với nguồn trích dẫn (Span to Source Mapping)
      grounding_supports: Array<{
        segment: {
          start_index: number,                  // Vị trí ký tự bắt đầu trong markdown_text
          end_index: number,                    // Vị trí ký tự kết thúc
          text: string                          // Đoạn câu cần xác thực nguồn
        },
        grounding_chunk_indices: number[],      // Mảng trỏ đến index trong grounding_sources
        confidence_scores: number[]             // Độ tin cậy của đoạn trích dẫn (0.0 - 1.0)
      }>,

      // [4] Gợi ý tìm kiếm bổ sung (Search Suggestions Chips)
      search_entry_point: {
        rendered_content: string                // HTML/JSON chứa các nút tìm kiếm mở rộng trên Google
      }
    ]
  ]>;
}
```

---

## 4. Thuật Toán Trích Xuất Nguồn Trích Dẫn (Extraction Pipeline)

Để bóc tách danh mục nguồn từ luồng response, client thực hiện:

```typescript
function extractGroundingSources(innerJson: any) {
  const candidate = innerJson?.[4]?.[0];
  if (!candidate) return null;

  const rawText = candidate?.[1]?.[0] || "";
  const groundingMeta = candidate?.[12];

  if (!groundingMeta) {
    return { text: rawText, hasGrounding: false, sources: [] };
  }

  const queries = groundingMeta[0] || [];
  const rawSources = groundingMeta[2] || [];
  const supports = groundingMeta[3] || [];

  const sources = rawSources.map((item: any) => {
    const details = item[1] || [];
    return {
      sourceIndex: item[0],
      url: details[0],
      title: details[1],
      snippet: details[2],
      favicon: details[3],
      domain: details[4]
    };
  });

  return {
    text: rawText,
    hasGrounding: true,
    searchQueries: queries,
    sources: sources,
    citationsMapping: supports
  };
}
```
