# Bảng operation và quyết định protocol

Mức hiện tại của gateway: **Production Gateway (Mức 4)**. Facade đã hoàn tất các hợp đồng cốt lõi:
- **Chuẩn OpenAI & Core**: `/v1/models`, `/v1/chat/completions`, `/v1/flow/credits` và `/v1/profiles*` (hỗ trợ tạo profile, cấu hình proxy per-profile `PUT /v1/profiles/{id}/proxy`, mã hóa Secret Vault AES-256-GCM `enc:v1:`).
- **Gemini Advanced RPCs**: Hoàn thiện toàn bộ endpoints `/v1/gemini/*` (Lịch sử hội thoại `MaZiqc`/`cZOhpc`/`PCck7e`/`VxUbXb`/`wEb32b`, Hạn ngạch `/usage` & Tier `I4z33b`, SCOTTY Resumable Upload `upload_handshake`, Canvas Artifacts `tVk3Sc`/`sA4a8`/`H8s0fe`, Feedback `uP80Sb`).
- **Flow Studio Suite**: Đã mount và phục vụ `/v1/flow/projects*`, `/v1/flow/trash*`, `/v1/flow/voices`, `/v1/flow/audio/generate`, `/v1/flow/videos/extend`, `/v1/flow/videos/upsample-4k`, `/v1/flow/gallery`.
- **Hạ tầng tự động**: `NzlxgGoldenJob` đối soát schema drift định kỳ trên tài khoản lab; `startProactiveKeepAliveRunner` chạy ngầm mỗi 4 giờ gửi request đọc nhẹ (`/usage`, `nzlxg`) để làm tươi `__Secure-1PSIDTS` chống hết hạn cookie Google; `startFlowModelMatrixSyncRunner` đồng bộ cụm GPU online qua `HTrJv`/`yBhWQ`; Rate limiter kiểm tra Trusted Proxies trước khi tin cậy `X-Forwarded-For`.

`nzlxg` và handshake dùng `upstream_short_timeout` (mặc định 20 giây). `StreamGenerate` và `StreamChat` dùng `upstream_stream_timeout`, hoặc `read_timeout` khi khóa đó thiếu. Hạn chờ stream không dài hơn deadline của caller. Counter lệch hợp đồng ghi theo tên operation. Golden job đọc số dư nzlxg (`NzlxgGoldenJob`) chạy định kỳ trong background theo cấu hình `golden_job.interval` độc quyền trên tài khoản lab (`lab*`), tự động ghi nhận cảnh báo drift và cập nhật metric schema nếu số trường `unmapped_fields` thay đổi so với baseline spec (`5`). Nếu không có tài khoản lab nạp vào, job ghi log standby và bỏ qua an toàn để bảo vệ tài khoản cá nhân của người dùng.

Spec version của số dư Flow: `2026-09-23`, gắn với hình phản hồi trong `docs-2/TEST_VERIFICATION_REPORT.md` ([1050, 1, 2, 2, null, 1050]). Đổi hình phản hồi là spec mới.

## Bảng operation toàn diện (DefaultRpcRegistry và docs-2)

Quy tắc phân loại:
- **Hoạt động / Production**: Các RPC đã có mã Go đầy đủ, gắn hợp đồng Facade, có route HTTP tại `/v1/*` hoặc worker nền chạy định kỳ.
- **Nghiên cứu**: Các RPC chưa có đặc tả hoặc endpoint mạng nội bộ chưa kích hoạt mã gọi.

### 1. Thao tác sống và RPC Flow cốt lõi

| RPC gốc | Dịch vụ & Operation nội bộ | Hàm Go | Mức hiện tại | Đọc / Ghi | Idempotent? | Tín hiệu hết hạn đã thấy | Ghi chú / Trạng thái |
|---|---|---|---|---|---|---|---|
| `nzlxg` | Flow (`flow.get_credits`) | `FlowClientAdapter.GetCreditsBalance` | Hoạt động (Production) | Đọc | Có | HTTP 401 hoặc redirect `accounts.google.com` | **ĐÃ HOÀN TẤT & ĐÓNG CASE (6/6 Tiêu chí Mục 12)**. Đọc số dư nguyên, đếm unmapped, retry 1 lần khi `expired`. Đã có `NzlxgGoldenJob` chạy runtime trên tài khoản lab. Đồng thời tích hợp trong `startProactiveKeepAliveRunner`. |
| `StreamGenerate` | Gemini (`chat.completions`) | `ChatService.ExecuteChatSync`, `ExecuteChatStream` | Hoạt động (Production) | Ghi hội thoại | Không | HTTP 401 hoặc handshake mất token `SNlM0e` | Hoạt động đầy đủ. Tích hợp `TryWriteLease(account, ServiceGemini)` - request thứ 2 cùng account nhận lỗi `conflict` (HTTP 409). Điền slot động vector new-chat & thinking. Phản hồi SSE. Hỗ trợ Proxy riêng biệt per-account. |
| `csbIsb` | Flow (`flow.session_lock`) | `FlowClientAdapter.RegisterSessionLock` | Hoạt động (Production) | Ghi khóa phiên | Chưa có bằng chứng | HTTP 401 | Tự động gọi trước khi thực thi tác vụ media để bảo vệ trạng thái phiên và tránh âm credit. |
| `jHPbke` | Flow (`flow.create_project`) | `FlowClientAdapter.CreateProject` | Hoạt động (Production) | Ghi tạo dự án | Không | HTTP 401 | Đã pack inner JSON, chỉ nhận UUID. Phục vụ tự động tạo project khi tài khoản chưa có workspace Flow. |
| `StreamChat` | Flow (`images.generations`, `videos.generations`) | `MediaService.GenerateImage`, `GenerateVideo` | Hoạt động (Production) | Ghi tạo media | Không | HTTP 401 hoặc thiếu session token | Cùng khung vector `StreamChat`. Đã mở rộng bảng mã Aspect Ratio đa dạng: `1:1` (3), `16:9` (2), `9:16` (1), `4:3` (4), `3:4` (5). Cờ kiểm soát qua `images_generations` và `videos_generations` trong config. |

### 2. RPC Gemini trong DefaultRpcRegistry (Đã triển khai đầy đủ trên HTTP `/v1/gemini/*`)

| RPC gốc | Dịch vụ & Mô tả chức năng | Hàm Go | Mức hiện tại | Đọc / Ghi | Idempotent? | Tín hiệu hết hạn đã thấy | Ghi chú / Trạng thái |
|---|---|---|---|---|---|---|---|
| `batchexecute_gemini` | Gemini: Cổng batchexecute tổng | `GoogleTransportAdapter.DoRequest` | Hoạt động (Production) | Vừa đọc vừa ghi | Tùy inner RPC | HTTP 401 | Cổng transport giao thức batchexecute bọc header FdrF9c. |
| `usage` | Gemini: Tra cứu hạn ngạch 5h / tuần | `GeminiQuotaService.GetQuota` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Đã mount `/v1/gemini/usage`. Được gọi định kỳ bởi `startProactiveKeepAliveRunner` để làm tươi `__Secure-1PSIDTS`. |
| `MaZiqc` | Gemini: Danh sách cuộc trò chuyện | `GeminiHistoryService.ListConversations` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Đã mount `/v1/gemini/conversations`. Trả về danh sách cây phân nhánh chat kèm pagination token. |
| `cZOhpc` | Gemini: Chi tiết tin nhắn hội thoại | `GeminiHistoryService.GetConversationDetail` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Đã mount `/v1/gemini/conversations/{id}`. Trả về cấu trúc cây DAG hội thoại đầy đủ. |
| `PCck7e` | Gemini: Đổi tên tiêu đề hội thoại | `GeminiHistoryService.RenameConversation` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `/v1/gemini/conversations/{id}/rename`. |
| `VxUbXb` | Gemini: Xóa cuộc trò chuyện | `GeminiHistoryService.DeleteConversation` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `DELETE /v1/gemini/conversations/{id}`. |
| `uP80Sb` | Gemini: Đánh giá Thumbs Up / Down | `GeminiFeedbackService.SendFeedback` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `/v1/gemini/feedback`. Gửi phản hồi chất lượng câu trả lời. |
| `wEb32b` | Gemini: Chuyển đổi con trỏ nhánh rẽ DAG hội thoại | `GeminiHistoryService.SwitchBranch` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `/v1/gemini/conversations/{id}/branch`. |
| `tVk3Sc` | Gemini: Khởi tạo tài liệu Canvas Artifacts mới | `GeminiCanvasService.CreateCanvas` | Hoạt động (Production) | Ghi | Không | HTTP 401 | Đã mount `/v1/gemini/canvas`. Tạo artifact văn bản/mã nguồn mới. |
| `sA4a8` | Gemini: Cập nhật diff delta tài liệu Canvas Artifacts | `GeminiCanvasService.UpdateDelta` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `/v1/gemini/canvas/{id}/delta`. Áp dụng các phép biến đổi diff theo base version. |
| `H8s0fe` | Gemini: Xuất bản và tạo liên kết chia sẻ công khai Canvas Artifacts | `GeminiCanvasService.Publish` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `/v1/gemini/canvas/{id}/publish`. |
| `whPPme` | Gemini: Tổng hợp giọng đọc Text-to-Speech audio WAV | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Ghi | Có | Chưa có bằng chứng | Nghiên cứu TTS; không có hàm gọi mạng, router không mount. |
| `I4z33b` | Gemini: Tra cứu cấp độ bản quyền tài khoản và context window | `GeminiQuotaService.GetAccountTier` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Đã mount `/v1/gemini/account-tier`. Trả về thông tin Tier (Free/Pro/Ultra) và giới hạn context. |
| `GPRiHf` | Gemini: Xóa toàn bộ lịch sử cuộc trò chuyện của tài khoản | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Ghi | Có | Chưa có bằng chứng | Nghiên cứu bulk delete; không có hàm gọi mạng, router không mount. |
| `upload_handshake` | Gemini: Khởi tạo phiên tải lên tệp đa phương thức Resumable SCOTTY | `GeminiUploadService.UploadFile` | Hoạt động (Production) | Ghi | Không | HTTP 401 | Đã mount `/v1/gemini/upload`. Gửi trực tiếp qua SCOTTY protocol push.clients6. |

### 3. RPC Flow Studio trong DefaultRpcRegistry (Đã triển khai đầy đủ trên HTTP `/v1/flow/*`)

| RPC gốc | Dịch vụ & Mô tả chức năng | Hàm Go | Mức hiện tại | Đọc / Ghi | Idempotent? | Tín hiệu hết hạn đã thấy | Ghi chú / Trạng thái |
|---|---|---|---|---|---|---|---|
| `batchexecute_flow` | Flow: Cổng batchexecute tổng | `GoogleTransportAdapter.DoRequest` | Hoạt động (Production) | Vừa đọc vừa ghi | Tùy inner RPC | HTTP 401 | Cổng batchexecute cho toàn bộ các RPC Flow. |
| `cPZSdc` | Flow: Gói cước & bonus credits ngày | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Đọc | Có | Chưa có bằng chứng | Nghiên cứu; không có hàm gọi mạng. |
| `HTrJv` | Flow: Ma trận model Veo & biểu phí | `FlowClientAdapter.GetActiveModels` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Tích hợp trong `startFlowModelMatrixSyncRunner` định kỳ cập nhật trạng thái model. |
| `yBhWQ` | Flow: Danh sách GPU online | `FlowClientAdapter.GetActiveModels` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Tích hợp trong `startFlowModelMatrixSyncRunner` kiểm tra tính khả dụng GPU thực tế. |
| `tRARke` | Flow: Thư viện 68 quy trình mẫu | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Đọc | Có | Chưa có bằng chứng | Nghiên cứu Workflow Templates; không có hàm gọi mạng. |
| `UpteDb` | Flow: Lịch sử & danh sách dự án | `MediaService.ListProjects` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Đã mount `/v1/flow/projects`. |
| `ngNC2` | Flow: Sơ đồ đồ thị PINHOLE | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Đọc | Có | Chưa có bằng chứng | Nghiên cứu mutation graph; không có hàm gọi mạng. |
| `Zzl0ze` | Flow: 30 voice mẫu & file WAV | `MediaService.ListVoicePersonas` | Hoạt động (Production) | Đọc | Có | Không | Đã mount `/v1/flow/voices`. Trả catalog tĩnh 30 personas chuẩn. |
| `kF8z7b` | Flow: Thêm nút mới vào đồ thị PINHOLE Node Graph | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Ghi | Không | Chưa có bằng chứng | Docs-2 graph mutation; không có hàm gọi mạng. |
| `jE2m9c` | Flow: Nối chân Pins liên kết giữa các nút đồ thị PINHOLE | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Ghi | Không | Chưa có bằng chứng | Docs-2 graph mutation; không có hàm gọi mạng. |
| `dL5p2` | Flow: Xóa nút hoặc cạnh nối trong đồ thị PINHOLE | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Ghi | Có | Chưa có bằng chứng | Docs-2 graph mutation; không có hàm gọi mạng. |
| `dK3x9` | Flow: Chuyển dự án vào thùng rác (Soft Delete) | `MediaService.MoveProjectToTrash` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `DELETE /v1/flow/projects/{id}`. |
| `tB6q8` | Flow: Lấy danh sách dự án trong thùng rác | `MediaService.ListTrash` | Hoạt động (Production) | Đọc | Có | HTTP 401 | Đã mount `/v1/flow/trash`. |
| `rS4y1` | Flow: Khôi phục dự án khỏi thùng rác | `MediaService.RestoreProject` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `/v1/flow/trash/{id}/restore`. |
| `mrlkwd` | Flow: Xóa vĩnh viễn dự án khỏi hệ thống | `MediaService.DeleteProjectPermanently` | Hoạt động (Production) | Ghi | Có | HTTP 401 | Đã mount `DELETE /v1/flow/trash/{id}`. |
| `mX9w1` | Flow: Tạo nhạc nền và SFX MusicFX | `MediaService.GenerateAudio` | Hoạt động (Production) | Ghi | Không | HTTP 401 | Đã mount `/v1/flow/audio/generate`. |
| `uW3g7e` | Flow: Nâng cấp video Veo lên 4K | `MediaService.UpsampleVideo4K` | Hoạt động (Production) | Ghi | Không | HTTP 401 | Đã mount `/v1/flow/videos/upsample-4k`. |

### 4. RPC nghiên cứu bổ sung trong docs-2 (Chưa vào registry Go / Không có hàm Go gọi mạng)

| RPC gốc / Tên | Dịch vụ & Mô tả chức năng | Hàm Go | Mức hiện tại | Đọc / Ghi | Idempotent? | Tín hiệu hết hạn đã thấy | Ghi chú / Trạng thái |
|---|---|---|---|---|---|---|---|
| `L5adhe` | Gemini: Kiểm tra tiến độ tác vụ sinh video | Không có hàm Go | Nghiên cứu (chưa có code gọi) | Đọc | Có | Chưa có bằng chứng | Docs-2 video status polling; không có hàm gọi mạng, router không mount. |

---

## Hợp đồng `nzlxg`

Bất biến trong spec `2026-09-23`:

- Host `flow.google.com`, path lấy từ registry.
- Vỏ form `f.req` = `[[[ "nzlxg", "[]", null, "generic" ]]]`.
- Phần tử 0 của payload trong là số dư nguyên, không âm.

Tính lại mỗi lần gọi: `at`, cookie, User-Agent của profile.

Field lạ trong payload: đếm `unmapped_fields`, vẫn trả số dư. Thiếu phần tử đầu, hoặc phần tử đầu không phải số nguyên không âm: `schema_unexpected`, không gán mặc định.

Vector nội bộ đã che secret: `[1050, 1, 2, 2, null, 1050]`. Số dư là `1050`. Năm phần tử còn lại là `unmapped` (chưa có spec chứng minh, không được tự ý gán nghĩa gói cước / phân hạng tài khoản).

## Hợp đồng chat, ảnh, video

Chat Gemini lấy phần bất biến từ vector new-chat trong `docs-2/gemini/API_REQUESTS_REFERENCE.md`. Mỗi lần gọi tính lại prompt, locale, bộ context, client uuid, tier và cờ thinking. `at` và cookie không nằm trong `f.req`.

Ảnh và video Flow dùng `FlowCreationAgentService/StreamChat`. Khung bất biến là:

`[request_id, [[[[prompt]]]], ["projects/"+project_id, null, [session_token, 1], options, null, 1]]`

`request_id`, prompt, project id và session state token tính lại mỗi lần. Video điền `options` bằng `model_id`, `duration_seconds`, `aspect_ratio`. Ảnh chỉ điền `model_id`. Ô options để null khi không có các field đó, đúng curl cơ bản.

Session state token không suy ra từ `SNlM0e`. Account chưa có token thì facade trả `unauthenticated` và không gọi `StreamChat`. Project id thiếu thì tạo một lần qua `jHPbke`, rồi khóa phiên `csbIsb`, rồi mới gửi media. Số dư đọc trước; thấp hơn giá mô hình thì dừng.

URL kết quả chỉ nhận HTTPS trên `lh3.googleusercontent.com`, `storage.googleapis.com`, `video.google.com`. Field lạ được đếm. Không có URL sau khi hết stream thì `schema_unexpected`. Ảnh Gemini và video Gemini vẫn đi qua `chat.completions`: URL hợp lệ được gắn vào nội dung trả về.

Một account chỉ có một tác vụ ghi ảnh hoặc video tại một thời điểm. Tác vụ thứ hai nhận `conflict`. Hết phiên thì xoay `SNlM0e` một lần. Timeout không tự gửi lại.

## Lớp lỗi

Một chỗ phân loại, `ClassifyUpstreamStatus` và `EnsureGateway`. Caller thấy `class` và câu an toàn. `meta` có `origin_operation`, `origin_status`, `retryable`, `correlation_id`. Thân phản hồi gốc không đi vào lỗi, log facade, hay metric.

| Tín hiệu đã thấy | Lớp | Retry |
|---|---|---|
| HTTP 401 | `expired` | Một lần xoay bí mật dẫn xuất, không gọi lại sau đó |
| Handshake dừng ở `accounts.google.com` | `expired` | Như trên |
| Trang 200 mà không có `SNlM0e` | `expired` | Như trên |
| HTTP 403 | `upstream_rejected` | Không |
| HTTP 429 | `rate_limited` | Cờ `retryable` bật với thao tác đọc. Chưa tự gọi lại. Nghỉ cục bộ 60 giây trên đúng service. Request trong lúc nghỉ nhận lại `rate_limited` |
| HTTP 408, 425, 5xx, lỗi mạng, hết hạn chờ | `upstream_unavailable` | Cờ bật với thao tác đọc. Chưa tự gọi lại. Xoay bí mật thất bại vì mạng thì phiên về `ready` và nghỉ 60 giây. Request trong lúc nghỉ nhận `upstream_unavailable` |
| Số dư không đúng hợp đồng | `schema_unexpected` | Không. Không mở Chrome, không đổi trạng thái phiên |

Chưa quan sát được tín hiệu captcha hay khóa tài khoản. Không gán hai lớp đó.

`schema_unexpected` và `upstream_unavailable` không bị coi là hết hạn mức.

## Session

Khóa logic đang dùng: `(account_id, service)`. Gemini và Flow mỗi bên một trạng thái.

| Trạng thái | Được gọi nghiệp vụ |
|---|---|
| `empty` | Không |
| `authenticating` | Không |
| `ready` | Có, nếu hết cooldown |
| `refreshing` | Không. Request khác chờ đúng flight đó |
| `invalid` | Không. Hết một lần xoay là dừng |
| `quarantined` | Không. Chưa có đường tự đưa vào trạng thái này |

`Save` một account mới đưa service có cookie tương ứng lên `ready`. `Save` lại đúng con trỏ đã `invalid` không hồi sinh phiên. Đăng nhập lại tạo account mới thì lên `ready`.

Chưa chứng minh Google cho hai peer ghi cùng lúc trên một account. Đọc được chồng lên nhau. Mọi lần xoay bí mật của một `(account, service)` đi một flight. Cả chat (`StreamGenerate`) và media (`StreamChat`) đều chiếm giữ lease ghi `TryWriteLease(account, service)` trên state machine. Request ghi thứ hai trên cùng một account nhận ngay lỗi `conflict` (HTTP 409). Round-robin chỉ chọn session `ready`.

`ScanAndDiscover` chỉ nạp `session.json` lên `ready` và bật danh mục cục bộ. Khởi động không đọc số dư và không xoay `SNlM0e`. Lần đọc số dư đầu tiên xảy ra khi có request.

Bí mật gốc là phiên Chrome do người dùng đăng nhập. Bí mật dẫn xuất là cookie và `SNlM0e`. Chúng nằm trong bộ nhớ và trong `session.json` của profile. Chưa có vault. Lỗi và facade không in các giá trị đó.

Công tắc nằm trong config, khóa `flow_get_credits`, `chat_completions`, `images_generations`, `videos_generations`. Hiện tại hai cờ ghi media (`images_generations: false`, `videos_generations: false`) đã bị khóa dứt điểm. Tắt một operation không gỡ route và không tắt ba operation còn lại. Số dư, ảnh và video vẫn trả `data` / `error` / `meta`. Chat thành công vẫn là thân OpenAI. Lỗi chat vẫn có `class` trong `error.type` và `meta`.

User-Agent gắn với profile đã đăng nhập. Chưa có pool IP. Đổi IP giữa chừng chưa được coi là an toàn.

Tài khoản lab cho lần đối chiếu định kỳ phải tách hoàn toàn khỏi tài khoản dùng hàng ngày. `NzlxgGoldenJob` đã được gắn runtime trong daemon và server chính, chạy định kỳ và chỉ quét tài khoản lab (`lab*`), nếu không có tài khoản lab thì bỏ qua an toàn và ghi log standby. Đạt chuẩn 6/6 tiêu chí Mục 12 để chính thức đóng case `nzlxg`.
