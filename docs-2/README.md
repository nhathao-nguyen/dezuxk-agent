# Tổng Quan Hệ Thống Tài Liệu Kỹ Thuật Đảo Ngược (Reverse Engineering): Gemini & Flow

> **Phiên bản:** v2.0.0 (Bổ sung đầy đủ 12 module chức năng chuyên sâu theo Wire Protocol thực tế)  
> **Trạng thái kiểm thử:** 17/17 Core Test Cases đạt **100%** thành công trên môi trường Terminal Native ngoài trình duyệt.  
> **Báo cáo chi tiết kiểm thử:** Xem tại [TEST_VERIFICATION_REPORT.md](TEST_VERIFICATION_REPORT.md).  
> ⚠️ **Sổ tay lưu ý quan trọng, bẫy lỗi & phòng chống WAF:** Xem tại [CRITICAL_INTEGRATION_GUIDE.md](CRITICAL_INTEGRATION_GUIDE.md).

> ### ⚠️ TỔNG HỢP CÁC LƯU Ý QUAN TRỌNG ĐỂ KHÔNG BỊ LỖI KHI GỌI API:
> 1. **Header `X-Same-Domain: 1` và `Origin`**: Bắt buộc phải có trong mọi request để vượt qua lớp kiểm tra CORS/WAF của Google.
> 2. **Quy tắc riêng của từng RPC**: Bám sát mục *"Quy tắc Tham số Đã Xác Minh Thực Tế"* trong từng file docs. Ví dụ:
>    * Với RPC `Zzl0ze`, phải truyền đúng format `projects/<project_uuid>`, không được truyền `projects/*`.
>    * Với Gemini Chat Mới, bộ 3 context ID phải là `["", "", ""]` (mảng chuỗi rỗng) chứ không được để `null`.
>    * Với Flow, thiếu cookie `OSID` (hoặc `__Secure-OSID`) sẽ bị báo lỗi `401 Unauthorized` ngay lập tức.
> 3. **Giải mã luồng Streaming (`wrb.fr`)**: Phản hồi của Google luôn có chuỗi bảo vệ `)]}'\n` ở đầu; chỉ cần bỏ chuỗi này đi là parse JSON bình thường.
> 4. **Đóng gói hai lớp JSON**: Form field `f.req` bắt buộc phải là mảng lồng chuỗi JSON 2 tầng: `[null, JSON_STRING(inner_array)]`.
> 5. **Tải ngay file Media về máy**: URL ảnh `lh3.googleusercontent.com` và video `storage.googleapis.com` là Pre-signed URL tạm thời, sẽ hết hạn sau 12 - 24 giờ.

---

## 1. Hai Bảng Tra Cứu Toàn Bộ Request API (Master Request References)

Để tra cứu nhanh, kiểm tra hoặc copy-paste trực tiếp các lệnh cURL kèm đầy đủ Headers, Cookies định danh, Endpoint và Payload tham số cho từng chức năng:

* 📘 **[Google Gemini - Master Request Reference](gemini/API_REQUESTS_REFERENCE.md)**
  * Gửi tin nhắn & Streaming (`StreamGenerate`)
  * Khởi tạo Chat Mới (Reset Context ID & null blob)
  * Chế độ Tư duy Mở rộng (Extended Thinking / Raisonnement étendu)
  * Tìm kiếm Thời gian thực & Nguồn trích dẫn (Google Search Grounding & Citations)
  * Thực thi mã Python trong Sandbox (Code Interpreter)
  * Phân nhánh cây hội thoại & Sửa câu hỏi cũ (Conversation Branching / Edit Turn)
  * Không gian làm việc trực quan (Canvas & Artifacts RPCs)
  * Tiện ích mở rộng Google Workspace (@Gmail, @Drive, @YouTube, @Maps)
  * Đánh giá phản hồi trợ lý (Feedback RPC `uP80Sb`)
  * Tạo ảnh Imagen 3 qua Prompt
  * Tạo video Veo qua Prompt
  * Tải lên tệp Resumable qua Google Push Service (`push.clients6.google.com`, `x-tenant-id: bard-storage`)
  * Lựa chọn Model Dropdown (`Flash-Lite`, `Flash`, `Pro`)
  * Tra cứu hạn mức điện toán (`/usage`)
  * Quản lý Lịch sử Chat (`MaZiqc`, `cZOhpc`, `PCck7e`, `VxUbXb`)
  * Tổng hợp giọng nói Text-To-Speech (`whPPme`)
  * Tra cứu cấp độ tài khoản (`I4z33b`)

* 🎨 **[Google Flow - Master Request Reference](flow/API_REQUESTS_REFERENCE.md)**
  * Tra cứu số dư Credit tổng quát (`nzlxg`)
  * Quyền lợi gói cước & Credit tặng thêm mỗi ngày (`cPZSdc`)
  * Ma trận 8 mô hình phần cứng & biểu phí Credit (`HTrJv`)
  * Danh sách mô hình GPU đang hoạt động (`yBhWQ`)
  * Thư viện 68 Quy trình Mẫu / Templates (`tRARke`)
  * Lịch sử & Danh sách Dự án (`UpteDb`)
  * Tạo Dự án Mới & cấp phát UUID (`jHPbke`)
  * Đăng ký Khóa phiên Tương tác đa tab (`csbIsb`)
  * Lấy Sơ đồ Đồ thị PINHOLE & Công cụ (`ngNC2`)
  * Thư viện 30 Nhân vật & Giọng đọc AI mẫu WAV (`Zzl0ze`)
  * Nâng cấp Video lên chất lượng 4K (`veo_3_1_upsampler_4k` qua `uW3g7e`)
  * Điều khiển Camera chuyển động 3D nâng cao (`camera_motion` vector matrix)
  * Mở rộng nối dài thời lượng video (`veo_3_1_extend` qua `StreamChat`)
  * Hệ thống âm nhạc & SFX đồng bộ (`mX9w1` / MusicFX)
  * Thao tác trực tiếp lên đồ thị PINHOLE (Thêm nút `kF8z7b`, Nối chân `jE2m9c`, Xóa `dL5p2`)
  * Quản lý Thùng rác & Khôi phục dự án (Xóa tạm `dK3x9`, Danh sách rác `tB6q8`, Khôi phục `rS4y1`, Xóa vĩnh viễn `mrlkwd`)
  * Tạo Video Veo 3.1 (`Quality`, `Fast`, `Lite` qua `StreamChat`)
  * Tạo Ảnh Studio Abra & Inpainting (`abra_edit` qua `StreamChat`)
  * Tác nhân Sáng tạo AI Lập kế hoạch & mở rộng Prompt (`CreationAgent`)

---

## 2. Danh Mục Tài Liệu Chi Tiết Của Google Gemini (`docs-2/gemini/`)

| STT | Tên Tài Liệu Chức Năng | Tệp Tin Liên Kết | Tóm Tắt Kỹ Thuật Đã Xác Minh |
| :---: | :--- | :--- | :--- |
| 1 | **Cookies, Vòng đời & Bảo mật WAF** | [cookies_and_security.md](gemini/cookies_and_security.md) | Vòng đời `__Secure-1PSID`, `__Secure-1PSIDTS`, `COMPASS`, cơ chế chống bot và bypass WAF. |
| 2 | **Trò chuyện & Suy luận Trực tuyến** | [chat.md](gemini/chat.md) | Endpoint `StreamGenerate`, giải mã định dạng luồng `wrb.fr`, trích xuất `c_`, `r_`, `rc_`. |
| 3 | **Khởi tạo Hội thoại Mới** | [chat_new.md](gemini/chat_new.md) | Cơ chế reset context `["", "", ""]`, khởi tạo nhánh hội thoại mới và sync tiêu đề. |
| 4 | **Tìm Kiếm & Nguồn Trích Dẫn** | [search_grounding_citations.md](gemini/search_grounding_citations.md) | Cờ kích hoạt Search Grounding `[27]`, bóc tách mảng `grounding_sources`, favicon, domain và thẻ `grounding_supports`. |
| 5 | **Thực Thi Code Python Trong Hộp Cát** | [code_interpreter.md](gemini/code_interpreter.md) | Hộp cát thực thi mã Python, trích xuất `stdout`, `stderr`, ảnh biểu đồ Base64/PNG từ khối `[10]`. |
| 6 | **Phân Nhánh & Sửa Câu Hỏi Cũ** | [conversation_branching.md](gemini/conversation_branching.md) | Cấu trúc rẽ nhánh cây DAG gắn kèm `parent_response_id`, tạo nhánh mới không làm mất lịch sử cũ, RPC `wEb32b`. |
| 7 | **Không Gian Làm Việc Trực Quan (Canvas)**| [canvas_artifacts.md](gemini/canvas_artifacts.md) | Bộ RPC tạo tài liệu độc lập (`tVk3Sc`), cập nhật từng dòng theo diff delta (`sA4a8`), và xuất bản công khai (`H8s0fe`). |
| 8 | **Tiện Ích Mở Rộng Workspace** | [workspace_extensions.md](gemini/workspace_extensions.md) | Điều hướng công cụ `@Gmail`, `@Drive`, `@YouTube`, `@Maps` trong `inner[30]`, bóc tách kết quả email/tài liệu. |
| 9 | **Đánh Giá & Phản Hồi Trợ Lý** | [feedback.md](gemini/feedback.md) | RPC `uP80Sb` gửi đánh giá Thumbs Up / Thumbs Down kèm mã lý do (Inaccurate, Harmful,...) và nhận xét. |
| 10 | **Tải lên Tệp Đa Phương Thức** | [uploads.md](gemini/uploads.md) | Hạ tầng `push.clients6.google.com`, `x-tenant-id: bard-storage`, cấp token `/contrib_service/ttl_1d/...`. |
| 11 | **Tạo Hình Ảnh Tự Nhiên (Imagen 3)** | [image.md](gemini/image.md) | Tự động định tuyến intent tạo ảnh trong chat prompt, cấu trúc mảng URL kết quả trên Google CDN. |
| 12 | **Tạo Video Tự Nhiên (Veo Video)** | [video.md](gemini/video.md) | Intent video trong chat prompt, cơ chế phân luồng tác vụ dài LRO và streaming trả về MP4. |
| 13 | **Các Mô hình Trong Dropdown** | [model.md](gemini/model.md) | Cờ tham số nội bộ: `Gemini 3.5 Flash-Lite`, `Gemini 3.8 Flash`, `Gemini 3.1 Pro`. |
| 14 | **Chế Độ Tư Duy Mở Rộng** | [extended_thinking.md](gemini/extended_thinking.md) | Kích hoạt cờ Thinking `3, 1`, nhận và hiển thị các khối suy luận ẩn (`thought_content`). |
| 15 | **Hạn Mức Điện Toán & Quota** | [quota.md](gemini/quota.md) | Tra cứu qua `/usage`, kiểm soát hạn mức xoay vòng 5 giờ và hạn mức cố định theo tuần. |
| 16 | **Quản Lý Lịch Sử Trò Chuyện** | [chat_history.md](gemini/chat_history.md) | Toàn bộ RPC quản lý: Lấy danh sách (`MaZiqc`), đọc chi tiết (`cZOhpc`), đổi tên (`PCck7e`), xóa (`VxUbXb`). |

---

## 3. Danh Mục Tài Liệu Chi Tiết Của Google Flow (`docs-2/flow/`)

| STT | Tên Tài Liệu Chức Năng | Tệp Tin Liên Kết | Tóm Tắt Kỹ Thuật Đã Xác Minh |
| :---: | :--- | :--- | :--- |
| 1 | **Cookies, Phân Lập Tên Miền & Bảo Mật** | [cookies_and_security.md](flow/cookies_and_security.md) | Yêu cầu bắt buộc cặp cookie origin-bound `OSID` & `__Secure-OSID`, khóa phiên `csbIsb`. |
| 2 | **Tạo Dự Án Mới** | [project_create.md](flow/project_create.md) | RPC `jHPbke`, cấp phát UUID v4 duy nhất cho từng workspace của người dùng. |
| 3 | **Lịch Sử Dự Án & Sơ Đồ PINHOLE** | [history.md](flow/history.md) | Truy xuất danh sách (`UpteDb`), tải đồ thị node PINHOLE (`ngNC2`), và dọn dẹp. |
| 4 | **Nâng Cấp Video Lên 4K** | [upsampler_4k.md](flow/upsampler_4k.md) | RPC `uW3g7e` nâng cấp video Veo 720p lên 4K Ultra HD (3840x2160), khấu trừ 50 credits. |
| 5 | **Ma Trận Điều Khiển Camera 3D** | [camera_movement.md](flow/camera_movement.md) | Ma trận vector camera 3 chiều: `pan_horizontal`, `tilt_vertical`, `zoom_depth`, `orbit_trajectory`. |
| 6 | **Mở Rộng Nối Dài Video** | [video_extension.md](flow/video_extension.md) | Kỹ thuật nối dài thêm 4s/6s từ video có sẵn (`veo_3_1_extend`) dùng khung hình cuối làm điều kiện biên. |
| 7 | **Hệ Thống Âm Nhạc & SFX (MusicFX)**| [music_audio_engine.md](flow/music_audio_engine.md) | RPC `mX9w1` tạo bài nhạc nền đồng bộ thời lượng và nhịp điệu (BPM) với video Veo. |
| 8 | **Thao Tác Trực Tiếp Lên Đồ Thị Node** | [pinhole_graph_mutation.md](flow/pinhole_graph_mutation.md) | Bộ RPC tạo nút (`kF8z7b`), nối các chân pin giữa Abra và Veo (`jE2m9c`), và xóa nút (`dL5p2`). |
| 9 | **Quản Lý Thùng Rác & Khôi Phục Dự Án**| [trash_and_restore.md](flow/trash_and_restore.md) | Các RPC xóa tạm (`dK3x9`), danh sách thùng rác (`tB6q8`), phục hồi dự án (`rS4y1`), xóa vĩnh viễn (`mrlkwd`). |
| 10 | **Tạo Hình Ảnh Trong Studio (Abra)** | [image_generation.md](flow/image_generation.md) | Mô hình Abra (Omni 1.1 Flash / Imagen 3), các tỷ lệ khung hình, chỉnh sửa inpainting `abra_edit`. |
| 11 | **Tạo Video Điện Ảnh (Veo 3.1)** | [video_generation.md](flow/video_generation.md) | Các model `veo_3_1_quality`, `veo_3_1_fast`, `veo_3_1_lite`, thời lượng 4s-10s. |
| 12 | **Ma Trận Mô Hình Phần Cứng** | [models_image_video.md](flow/models_image_video.md) | Phân tích phần cứng từ RPC `HTrJv` và danh sách mô hình đang online qua RPC `yBhWQ`. |
| 13 | **Nhân Vật Nhất Quán & Giọng Đọc** | [character_and_reference.md](flow/character_and_reference.md) | RPC `Zzl0ze` (yêu cầu path `projects/<uuid>`), 30 nhân vật lồng tiếng kèm file audio mẫu WAV. |
| 14 | **Chi Phí Credit & Hạn Mức Tiêu Tốn** | [credits_and_consumption.md](flow/credits_and_consumption.md) | Bảng chi phí credit thực tế (Veo Quality: 100, Fast: 10-20, Lite: 5-10, Abra: 4-20, Upsample 4K: 50) & tra cứu số dư `nzlxg`. |
| 15 | **Tác Nhân Sáng Tạo & Lập Kế Hoạch** | [agent_and_capabilities.md](flow/agent_and_capabilities.md) | Dịch vụ `FlowCreationAgentService/StreamChat`, mở rộng prompt, tự động sắp xếp node PINHOLE. |
