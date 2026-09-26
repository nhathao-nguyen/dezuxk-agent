# Chức năng: Chế độ Tư duy Mở rộng (Extended Thinking Mode)

Chế độ Tư duy Mở rộng (*Raisonnement étendu* / *Extended Reasoning*) là tính năng cho phép Gemini kích hoạt chuỗi suy nghĩ nội tâm (Chain-of-Thought / Thinking Process) trước khi tạo câu trả lời cuối cùng, phục vụ giải toán, gỡ lỗi logic, khoa học và lập trình phức tạp.

---

## 1. Vị trí & Cách kích hoạt trên Giao diện

* Trên dropdown chọn model của Gemini Web, lựa chọn thứ tư: **"Raisonnement étendu | Résolution de problèmes complexes"**.
* Khi được kích hoạt, giao diện sẽ xuất hiện khối hộp thoại có thể mở rộng/thu gọn hiển thị nội dung *"Thinking process"* (Quá trình tư duy).

---

## 2. Dữ liệu khi gửi Request lên Server

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate`
* **Cookie cần thiết:** `__Secure-1PSID`, `__Secure-1PSIDTS`.
* **Cấu trúc cờ tham số trong `f.req`:**
  Khi chọn Extended Thinking, client gửi thêm các cờ số học đặc thù ở cuối mảng `f.req`:

```json
[
  null,
  "[[\"Tại sao bầu trời màu xanh?\", 0, null, null, null, null, 0], [\"fr\"], [\"\", \"\", \"\"], \"!context_blob...\", ..., 3, 1, null, null, null, null, null, null, null, null, null, null, 0, null, null, null, null, 1, null, 1]"
]
```

### Điểm khác biệt so với Chat thông thường:
1. `mode_tier = 3`: Chỉ định nhóm mô hình Pro / Reasoning cao cấp.
2. `extended_thinking_flag = 1`: Kích hoạt bộ nhớ suy nghĩ trung gian (Internal CoT Token Budget).
3. Cho phép model sử dụng ngân sách token ẩn (hidden reasoning tokens) trước khi trả về văn bản đầu ra hiển thị cho người dùng.

---

## 3. Cấu trúc Dữ liệu Server trả về

Server truyền luồng dữ liệu phân đoạn với 2 khối nội dung độc lập:

### 3.1. Khối Tư duy Suy nghĩ (Thinking Tokens)
Các đoạn suy nghĩ được gắn cờ thẻ nội bộ `thought`:
```json
[
  "rc_choice_id",
  null,
  null,
  null,
  null,
  [
    {
      "thought_content": "1. Phân tích hiện tượng tán xạ ánh sáng Rayleigh.\n2. Bước sóng ánh sáng xanh ngắn hơn ánh sáng đỏ.\n3. Khí quyển Trái Đất gồm N2 và O2 tán xạ bước sóng ngắn mạnh hơn theo tỷ lệ 1/lambda^4...",
      "is_thinking": true
    }
  ]
]
```

### 3.2. Khối Phản hồi Chính thức (Final Answer)
Sau khi khối tư duy hoàn tất, server tiếp tục truyền luồng các chunk chứa câu trả lời hoàn thiện để render ra giao diện chính.

---

## 4. Hạn mức & Quản lý Tài nguyên
* Chế độ tư duy mở rộng tiêu thụ nhiều tài nguyên GPU TPU hơn thông thường.
* Máy chủ kiểm soát nghiêm ngặt hạn mức 5 giờ (`quota5h`). Nếu người dùng kích hoạt quá nhiều lượt tư duy mở rộng trong thời gian ngắn, server sẽ tự động hạ cấp xuống Flash hoặc yêu cầu chờ reset hạn ngạch.
