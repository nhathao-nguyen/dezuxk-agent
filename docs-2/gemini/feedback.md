# Chức năng: Đánh Giá & Phản Hồi Câu Trả Lời (Feedback & Rating RPC)

Tài liệu này đặc tả giao thức mạng (Wire Protocol) của chức năng gửi phản hồi chất lượng câu trả lời (Thumbs Up / Thumbs Down, gắn cờ nội dung không chính xác hoặc nguy hại) trên Google Gemini Web.

---

## 1. Yêu Cầu Cơ Bản

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=uP80Sb&rt=c`
* **Method:** `POST`
* **Content-Type:** `application/x-www-form-urlencoded;charset=UTF-8`
* **Headers Bắt Buộc:**
  ```http
  Origin: https://gemini.google.com
  Referer: https://gemini.google.com/app
  X-Same-Domain: 1
  User-Agent: <CLIENT_USER_AGENT>
  ```
* **Cookies Bắt Buộc:** `__Secure-1PSID`, `__Secure-1PSIDTS`.

---

## 2. Cấu Trúc Payload Gửi Đi (Request Schema)

Dữ liệu gửi qua form field `f.req` và `at`:
* `at`: `<CSRF_TOKEN_SNlM0e>`
* `f.req`: `[[["uP80Sb", JSON.stringify(FEEDBACK_PAYLOAD), null, "generic"]]]`

### Cấu trúc mảng `FEEDBACK_PAYLOAD`:

```typescript
type GeminiFeedbackPayload = [
  // [0] Định danh cuộc hội thoại và câu trả lời được đánh giá
  conversation_id: string,                      // "c_<ID>"
  response_id: string,                          // "r_<ID>"
  choice_id: string,                            // "rc_<ID>"

  // [3] Loại đánh giá (Sentiment Code)
  rating_type: 1 | 2,                           // 1: Thích (Thumbs Up / Good) / 2: Không thích (Thumbs Down / Bad)

  // [4] Danh mục lý do chi tiết (Tags / Reason Codes) - Áp dụng khi rating_type = 2
  feedback_reasons: Array<
    | 1                                         // Thông tin không chính xác (Factually incorrect)
    | 2                                         // Không làm theo hướng dẫn (Didn't follow instructions)
    | 3                                         // Phản hồi nguy hại / xúc phạm (Offensive / Harmful)
    | 4                                         // Không hữu ích (Not helpful)
    | 5                                         // Từ chối trả lời không cần thiết (Refusal)
    | 6                                         // Trùng lặp / Lặp từ (Repetitive)
  > | [],

  // [5] Ý kiến đóng góp tự do của người dùng
  user_comment: string | null,                  // Nội dung nhận xét bằng văn bản

  // [6] Siêu dữ liệu ngữ cảnh thiết bị
  client_metadata: [
    locale: string,                             // "vi"
    app_version_hash: string | null
  ]
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về Từ Máy Chủ

Khi gửi đánh giá thành công, server trả về xác nhận HTTP 200 kèm chunk `wrb.fr`:

```text
)]}'\n
<BYTE_LENGTH>\n
[["wrb.fr", "uP80Sb", "[1]", null, null, null, null, null, 0]]\n
```

### Phản hồi JSON giải mã:
```json
[1]
```
*(Số nguyên `1` biểu thị phản hồi đánh giá chất lượng đã được lưu vào hệ thống RLHF của Google)*.

---

## 4. Lệnh cURL Mẫu Chạy Ngoài Trình Duyệt

```bash
curl -X POST "https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=uP80Sb&rt=c" \
  -H "Content-Type: application/x-www-form-urlencoded;charset=UTF-8" \
  -H "Origin: https://gemini.google.com" \
  -H "Referer: https://gemini.google.com/app" \
  -H "X-Same-Domain: 1" \
  -H "Cookie: __Secure-1PSID=<COOKIE>; __Secure-1PSIDTS=<COOKIE>" \
  --data-urlencode "at=<SNlM0e_TOKEN>" \
  --data-urlencode 'f.req=[[["uP80Sb","[\"<CONVERSATION_ID>\",\"<RESPONSE_ID>\",\"<CHOICE_ID>\",2,[1,4],\"Câu trả lời chưa đúng số liệu thực tế\",[\"vi\",null]]",null,"generic"]]]'
```
