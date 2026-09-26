# Chức năng: Phân Nhánh & Chỉnh Sửa Tin Nhắn Cũ (Conversation Branching & Edit Turn)

Tài liệu này đặc tả cơ chế mạng (Wire Protocol) khi người dùng chỉnh sửa một tin nhắn đã gửi trước đó trong cuộc trò chuyện trên Google Gemini Web. Hệ thống không xóa tin nhắn cũ mà thực hiện phân nhánh đồ thị (Tree Forking), tạo ra một nhánh hội thoại mới bắt đầu từ điểm rẽ nhánh (Parent Response ID).

---

## 1. Cơ Chế Cây Hội Thoại Của Google Gemini (DAG Architecture)

Trong hệ thống Gemini Web:
* Mỗi cuộc trò chuyện (`c_<ID>`) là một cây có hướng (Directed Tree) chứa nhiều nút (Turns).
* Mỗi lượt trả lời của AI có một mã định danh duy nhất: `r_<RESPONSE_ID>` kèm phương án được chọn `rc_<CHOICE_ID>`.
* Khi người dùng chỉnh sửa câu hỏi tại lượt thứ $N$, client gửi một request `StreamGenerate` mới nhưng khai báo **nút cha trực tiếp** (`parent_response_id`) chính là phản hồi của trợ lý ngay trước câu hỏi được chỉnh sửa (hoặc chính `r_<ID>` của lượt đó).
* Máy chủ sinh ra nhánh con mới mà vẫn lưu giữ nguyên vẹn nhánh cũ, cho phép người dùng chuyển đổi qua lại giữa các phiên bản (vd: `1/2`, `2/2`).

---

## 2. Cấu Trúc Request Gửi Đi Khi Phân Nhánh (Forking Request Schema)

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?rt=c`
* **Form Fields:** `at=<SNlM0e_TOKEN>` và `f.req=[null, JSON.stringify(INNER_ARRAY)]`

### Cấu trúc mảng vị trí `INNER_ARRAY` khi Chỉnh Sửa / Tạo Nhánh:

```typescript
type ConversationBranchingPayload = [
  // [0] Câu hỏi MỚI đã được chỉnh sửa
  [
    [
      edited_prompt_text: string,               // Câu lệnh mới sau khi người dùng sửa
      input_type: 0,
      null,
      null,
      null,
      null,
      0
    ],
    [locale: string],                           // ["vi"]
    
    // [2] Ngữ cảnh chỉ định NÚT CHA để rẽ nhánh (Forking Anchor)
    [
      conversation_id: string,                  // "c_<EXISTING_CONVERSATION_ID>" (Giữ nguyên ID hội thoại)
      parent_response_id: string,               // "r_<PARENT_ID>" (Nút phản hồi của lượt liền trước điểm sửa)
      parent_choice_id: string,                 // "rc_<PARENT_ID>"
      null, null, null, null, null, null,
      context_blob: string                      // Chuỗi token đồng bộ trạng thái của nút cha
    ],
    
    null, null, null, [0], 1, null, null, 1, 0, null, null, null, null, null,
    [[0]], 0, null, null, null, null, null, null, null, null, 1, null, null,
    [4], null, null, null, null, null, null, null, null, null, null, [1],
    null, null, null, null, null, null, null, null, null, null, null, 0,
    null, null, null, null, null,
    
    // [46] UUID phiên gửi mới (Bắt buộc phải tạo UUID mới)
    new_client_request_uuid: string,            // UUID v4 mới phân biệt với lượt gửi cũ
    
    null, [], null, null, null, null, null, 0, 1, null, null, null, null, null,
    null, null, null, null, null,
    
    model_tier: 1, reasoning_mode: 0,
    null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, 0, null, 1
  ]
];
```

---

## 3. Cấu Trúc Dữ Liệu Nhận Về Từ Máy Chủ

Khi rẽ nhánh thành công, server trả về luồng `wrb.fr` chứa:
* Cùng mã `conversation_id` (`c_...`).
* Một mã phản hồi hoàn toàn mới `response_id` (`r_new_...`) và `choice_id` (`rc_new_...`).
* Cây lịch sử được server tự động cập nhật liên kết: `parent(r_new_...) = r_<PARENT_ID>`.

```json
[
  null,
  [
    "c_582910482910abcd",
    "r_new_938471928374def0"
  ],
  null,
  null,
  [
    [
      "rc_new_8472910394851234",
      [
        "Nội dung câu trả lời mới cho câu hỏi vừa được chỉnh sửa...",
        null, null, null, null, null, null
      ]
    ]
  ]
]
```

---

## 4. RPC Chuyển Đổi Qua Lại Giữa Các Nhánh (Branch Switching RPC)

Khi người dùng nhấn nút chuyển phiên bản (ví dụ từ nhánh `2` về nhánh `1` của cùng một câu hỏi), client gửi RPC thông báo cho server cập nhật nhánh đang hoạt động (Active Branch Pointer):

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/batchexecute?rpcids=wEb32b&rt=c`
* **Form Field:** `at=<SNlM0e_TOKEN>`
* **Form Field:** `f.req`

### Cấu trúc Payload:
```json
[
  [
    [
      "wEb32b",
      "[\"c_<CONVERSATION_ID>\", \"r_<TARGET_BRANCH_RESPONSE_ID>\", \"rc_<TARGET_BRANCH_CHOICE_ID>\"]",
      null,
      "generic"
    ]
  ]
]
```

### Phản hồi Server trả về:
```json
[1]
```
*(Xác nhận con trỏ ngữ cảnh hiện tại của phiên hội thoại đã chuyển sang nhánh được chỉ định)*.
