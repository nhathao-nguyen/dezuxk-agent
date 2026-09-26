# Chức năng: Dropdown Chọn Model & Điều hướng Mô hình (Model Selection)

Tài liệu này ghi nhận các mô hình thực tế xuất hiện trong menu chọn mô hình của Google Gemini Web và cách client chỉ định mô hình tương ứng lên server.

---

## 1. Danh sách Mô hình Thực tế trong Dropdown (Trích xuất từ Live Session)

Qua kiểm tra trực tiếp từ giao diện Gemini Web thông qua Chrome DevTools:

| Tên Model trên Menu | Tên mã DOM (`data-test-id`) | Mô tả chức năng | Tương ứng Backend Server |
| :--- | :--- | :--- | :--- |
| **3.5 Flash-Lite** | `bard-mode-option-8c46e95b1a07cecc` | *Réponses les plus rapides* (Phản hồi nhanh nhất) | Gemini 1.5/2.0 Flash-Lite |
| **3.8 Flash** | `bard-mode-option-56fdd199312815e2` | *Aide polyvalente* (Trợ thủ đa năng hằng ngày) | Gemini 1.5/2.0 Flash (Mặc định) |
| **3.1 Pro** | `bard-mode-option-e6fa609c3fa255c0` | *Raisonnement avancé* (Suy luận logic & lập trình sâu) | Gemini 1.5/2.0 Pro |
| **Raisonnement étendu** | *Menu option 4* | *Résolution de problèmes complexes* (Giải toán & bài toán khó) | Gemini Thinking Mode / Extended Reasoning |

---

## 2. Cách thức gửi Tham số Model lên Server

Trong request `StreamGenerate`, model không được truyền dưới dạng chuỗi tên text mà thông qua các cờ chỉ mục số nguyên trong mảng `f.req`:

* **Endpoint:** `POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate`
* **Vị trí chỉ mục trong payload:**
  * `index [0][70]`: Chỉ định nhóm mô hình cốt lõi:
    * Giá trị `1`: Chế độ Flash (Flash-Lite / Flash).
    * Giá trị `3`: Chế độ Pro (Chuyên sâu, Reasoning, Pro).
  * `index [0][100]`: Cờ kích hoạt suy luận mở rộng (Thinking Mode).

```json
[
  null,
  "[[\"Prompt của bạn\", 0, null, null, null, null, 0], [\"vi\"], [\"\", \"\", \"\"], \"!session_blob...\", ..., 3, 1, ...]"
]
```

---

## 3. Dữ liệu Server phản hồi tương ứng từng Model

Server trả về thông tin định danh mô hình thực tế đã xử lý lượt sinh trong khối metadata của `wrb.fr`:
* Với **Flash-Lite / Flash:** Thời gian phản hồi TTFT (Time to first token) cực nhanh (< 600ms), độ dài câu trả lời xúc tích, tập trung tốc độ.
* Với **Pro:** TTFT lâu hơn (1.2s - 2.5s), phân tích đa chiều, đi kèm các khối code Python hoặc trích dẫn bảng biểu chi tiết.
