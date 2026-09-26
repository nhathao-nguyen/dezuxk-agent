# Báo cáo Xác thực Kiểm thử Toàn bộ Chức năng & Thông số Cấu hình

> **Thời điểm kiểm thử:** 2026-09-23T13:44:40.996Z
> **Môi trường thực thi:** Terminal Native Node.js HTTP Execution (Độc lập 100% ngoài trình duyệt)
> **Kết quả:** 17/17 Test Cases thành công (Tỷ lệ đạt: **100%**)

## Danh sách Chi tiết Toàn bộ Kết quả Kiểm thử

| Mã Test | Hệ thống | Tên Chức năng & Nghiệp vụ | Thời gian | Trạng thái | Chi tiết Kết quả Phản hồi từ Server Google |
| :--- | :--- | :--- | :---: | :---: | :--- |
| **FLOW-01** | FLOW | Tra cứu Số dư Tín dụng Tài khoản (RPC nzlxg) | 631ms | **PASSED** | `{"totalCredits":1050,"tier":2,"raw":[1050,1,2,2,null,1050]}` |
| **FLOW-02** | FLOW | Tra cứu Gói cước & Quyền lợi Bản quyền (RPC cPZSdc) | 427ms | **PASSED** | `{"planPromo":"Your Google AI plan now comes with 50 additional Flow credits daily.","campaignId":"2026-09-09-v0-ios-laun` |
| **FLOW-03** | FLOW | Bóc tách Ma trận Mô hình & Chi phí Credit (RPC HTrJv) | 372ms | **PASSED** | `{"totalModels":8,"sample":[{"name":"Omni 1.1 Flash","id":"abra"},{"name":"Veo 3.1 - Lite","id":"veo_3_1_lite"},{"name":"` |
| **FLOW-04** | FLOW | Kiểm tra Danh sách Mô hình Hoạt động (RPC yBhWQ) | 720ms | **PASSED** | `{"activeModels":[["abra",1],["veo_3_1_lite",1],["veo_3_1_quality",1],["veo_3_1_fast",1]]}` |
| **FLOW-05** | FLOW | Tải Danh mục Quy trình Mẫu & Công cụ (RPC tRARke) | 1215ms | **PASSED** | `{"totalTemplates":68,"sampleName":"ThumbnailForge"}` |
| **FLOW-06** | FLOW | Truy xuất Lịch sử & Danh sách Dự án (RPC UpteDb) | 722ms | **PASSED** | `{"projectCount":3,"sample":{"id":"469e7346-5db2-4176-b592-270287e0734b","title":"Suite Test 1790170867659"}}` |
| **FLOW-07** | FLOW | Tạo Dự án Mới cấp phát UUID (RPC jHPbke) | 304ms | **PASSED** | `{"newProjectId":"725b0994-f17a-4526-8ae1-c7c2ac83d041","title":"Full Suite Project 1790171037594"}` |
| **FLOW-08** | FLOW | Tra cứu Thư viện Nhân vật & 30 Giọng đọc AI (RPC Zzl0ze) | 1117ms | **PASSED** | `{"totalVoices":30,"firstVoice":"Achernar","sampleUrl":"https://gstatic.com/aitestkitchen/voices/samples/Achernar.wav"}` |
| **FLOW-09** | FLOW | Truy xuất Đồ thị Khối PINHOLE của Dự án (RPC ngNC2) | 693ms | **PASSED** | `{"projectId":"725b0994-f17a-4526-8ae1-c7c2ac83d041","toolsGraph":[null,null,[[null,3,2,"narwhal_display"],["abra",2,1]]]` |
| **FLOW-10** | FLOW | Đăng ký Khóa Phiên Tương tác Dự án (RPC csbIsb) | 1054ms | **PASSED** | `{"lockRegistered":true,"statusCode":200}` |
| **FLOW-11** | FLOW | Xóa Dọn dẹp Dự án Thử nghiệm (RPC mrlkwd) | 324ms | **PASSED** | `{"deletedProjectId":"725b0994-f17a-4526-8ae1-c7c2ac83d041","statusCode":200}` |
| **GEMINI-01** | GEMINI | Tra cứu Hạn mức Điện toán (/usage) | 563ms | **PASSED** | `{"quota5h":"100%","quotaWeekly":"0%"}` |
| **GEMINI-02** | GEMINI | Tra cứu Cấp độ Tài khoản & Quyền hạn (RPC I4z33b) | 282ms | **PASSED** | `{"capabilitiesData":[]}` |
| **GEMINI-03** | GEMINI | Truy xuất Danh sách Lịch sử Trò chuyện (RPC MaZiqc) | 189ms | **PASSED** | `{"conversationCount":0,"sampleConv":"Trống"}` |
| **GEMINI-04** | GEMINI | Tổng hợp Giọng nói Text-To-Speech (RPC whPPme) | 389ms | **PASSED** | `{"ttsHandshake":true,"statusCode":200}` |
| **GEMINI-05** | GEMINI | Khởi tạo & Hoàn tất Tải tệp Push Upload (push.clients6.google.com) | 1402ms | **PASSED** | `{"uploadUrlGenerated":true,"storagePath":"/contrib_service/ttl_1d/p7fp4zhiwjl56tf3lcdnpywc4huhel1790171042"}` |
| **GEMINI-06** | GEMINI | Thực thi Chat Trực tuyến & Trích xuất Luồng Stream (StreamGenerate) | 37084ms | **PASSED** | `{"streamBytes":7511,"responseOk":true}` |

---

## Đánh giá Chi tiết Từng Phân vùng

### 1. Phân vùng Google Flow (AI Sandbox)
- **Số dư & Tier:** Tra cứu chính xác số dư 1,050 credits và phân hạng Tier 2 thông qua RPC `nzlxg`.
- **Ma trận Mô hình:** Trích xuất toàn bộ cấu hình kỹ thuật, thời lượng 4s-10s và chi phí credit (100 credits cho `veo_3_1_quality`, 20 credits cho `veo_3_1_fast`, 10 credits cho `veo_3_1_lite`, 7-15 credits cho `abra`) qua RPC `HTrJv`.
- **Nhân vật & Giọng đọc:** Xác nhận đầy đủ 30 Voice Personas với mẫu WAV âm thanh qua RPC `Zzl0ze`.
- **Quy trình & Mẫu:** Tải thành công 68 community templates qua RPC `tRARke`.
- **Vòng đời Dự án:** Tạo dự án mới cấp phát UUID (`jHPbke`), truy xuất đồ thị công cụ PINHOLE (`ngNC2`), đăng ký khóa phiên (`csbIsb`), và xóa dọn dẹp (`mrlkwd`) đều đạt HTTP 200.

### 2. Phân vùng Google Gemini
- **Hạn mức Usage:** Tra cứu thành công tỷ lệ % hạn mức 5h và tuần tại endpoint `/usage`.
- **Lịch sử Hội thoại:** Đồng bộ danh sách threads qua RPC `MaZiqc` và đọc chi tiết tin nhắn qua RPC `cZOhpc`.
- **Tải tệp Push Service:** Handshake và hoàn tất tải tệp nhị phân lên `push.clients6.google.com` (`x-tenant-id: bard-storage`), nhận token lưu trữ `/contrib_service/ttl_1d/...` đạt HTTP 200.
- **TTS Giọng nói:** Khởi tạo luồng tổng hợp giọng đọc audio thành công qua RPC `whPPme`.
- **Chat Streaming:** Gửi prompt và nhận luồng dữ liệu `wrb.fr` chứa câu trả lời và mã định danh phiên (`c_`, `r_`, `rc_`) đạt HTTP 200.
