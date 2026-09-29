package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func main() {
	fmt.Println("================================================================")
	fmt.Println("🚀 BẮT ĐẦU KIỂM THỬ THỰC TẾ PHÂN HỆ GEMINI (REAL-FLOW VERIFICATION)")
	fmt.Println("================================================================")

	// 1. Nạp Config
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("❌ Lỗi nạp cấu hình: %v", err)
	}

	// 2. Khởi tạo Dependencies
	ctx := context.Background()
	modelRegistry := domain.NewModelRegistry(nil)
	rpcRegistry := domain.DefaultRpcRegistry()
	tokenExtractor := google.NewGoogleTokenExtractorAdapter(cfg.Server.ShortTimeout())
	sessionRepo := session.NewMemorySessionRepository(google.NewDerivedSecretRefresher(tokenExtractor))
	metrics := domain.NewContractMetrics()
	upstreamTransport := google.NewGoogleTransportAdapter(cfg)
	vault := session.NewVault(session.ResolveMasterKey(cfg.Security.MasterKey))

	profileManager, err := session.NewProfileManager(cfg, sessionRepo, modelRegistry, tokenExtractor, vault)
	if err != nil {
		log.Fatalf("❌ Lỗi khởi tạo ProfileManager: %v", err)
	}

	// 3. Quét Profile Ngoquang41296
	profiles, err := profileManager.ScanAndDiscover(ctx)
	if err != nil {
		log.Fatalf("❌ Lỗi quét profiles: %v", err)
	}

	fmt.Printf("🔍 Đã quét được %d profile(s)\n", len(profiles))
	var activeProf *domain.Profile
	for _, p := range profiles {
		fmt.Printf("  • Profile ID: %s | LoggedIn: %v | HasGemini: %v | Email: %s\n", p.ID, p.IsLoggedIn, p.HasGemini, p.Email)
		if p.HasGemini {
			activeProf = p
		}
	}

	if activeProf == nil {
		log.Fatalf("❌ Không tìm thấy profile nào có phiên Gemini hợp lệ trong ./profiles/")
	}

	// Kiểm tra Account trong SessionRepo
	acc, err := sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil {
		log.Fatalf("❌ Không thể lấy account Gemini: %v", err)
	}
	fmt.Printf("✅ Đã kết nối phiên Gemini tài khoản: %s (SNlM0e len: %d)\n", acc.Email, len(acc.GeminiSNlM0e))

	// 4. Khởi tạo Services
	wire := google.NewWireAdapter(rpcRegistry)
	chatService := services.NewChatService(modelRegistry, sessionRepo, upstreamTransport, wire, metrics)
	geminiQuotaService := services.NewGeminiQuotaService(sessionRepo, upstreamTransport, rpcRegistry)
	geminiHistoryService := services.NewGeminiHistoryService(sessionRepo, upstreamTransport, rpcRegistry, metrics)
	geminiUploadService := services.NewGeminiUploadService(sessionRepo, upstreamTransport, rpcRegistry)

	// -------------------------------------------------------------------------
	// TEST 1: Tra cứu Hạn ngạch Quota (/usage & I4z33b)
	// -------------------------------------------------------------------------
	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("🧪 TEST 1: Tra cứu Hạn mức Sử dụng Google Gemini (/usage & I4z33b)")
	fmt.Println("----------------------------------------------------------------")
	t0 := time.Now()
	tier, err := geminiQuotaService.GetAccountTier(ctx)
	if err == nil && tier != nil {
		fmt.Printf("✅ Tra cứu Account Tier thành công (RPC I4z33b):\n")
		fmt.Printf("  • Gói tài khoản (Tier): %s\n", tier.TierCode)
		fmt.Printf("  • Context Window: %d tokens\n", tier.ContextWindowSize)
		fmt.Printf("  • Capabilities: %v\n", tier.Capabilities)
	}

	quota, err := geminiQuotaService.GetQuota(ctx)
	latencyQuota := time.Since(t0).Milliseconds()
	if err != nil {
		fmt.Printf("⚠️ Lỗi đọc Quota (/usage có thể yêu cầu web cookie): %v\n", err)
	} else {
		fmt.Printf("✅ Quota thành công (%d ms):\n", latencyQuota)
		fmt.Printf("  • Hạn mức 5 giờ: %.1f%%\n", quota.Quota5h*100)
		fmt.Printf("  • Hạn mức tuần: %.1f%%\n", quota.QuotaWeekly*100)
		fmt.Printf("  • Thời gian reset 5h: %s\n", quota.ResetTime5h)
	}

	// -------------------------------------------------------------------------
	// TEST 2: Lấy Danh sách Lịch sử Hội thoại (MaZiqc)
	// -------------------------------------------------------------------------
	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("🧪 TEST 2: Đọc Danh Sách Hội Thoại Lịch Sử từ Google (RPC MaZiqc)")
	fmt.Println("----------------------------------------------------------------")
	t0 = time.Now()
	convs, nextToken, err := geminiHistoryService.ListConversations(ctx, 5)
	latencyHistory := time.Since(t0).Milliseconds()
	if err != nil {
		fmt.Printf("⚠️ Lỗi gọi MaZiqc: %v\n", err)
	} else {
		fmt.Printf("✅ Đọc lịch sử thành công (%d ms, tổng: %d cuộc trò chuyện):\n", latencyHistory, len(convs))
		for i, c := range convs {
			fmt.Printf("  [%d] ID: %s | Tiêu đề: %s\n", i+1, c.ID, c.Title)
		}
		if nextToken != "" {
			fmt.Printf("  • Next Page Token: %s\n", nextToken)
		}
	}

	// -------------------------------------------------------------------------
	// TEST 3: Chat Thực Tế với Thinking Mode
	// -------------------------------------------------------------------------
	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("🧪 TEST 3: Gửi Chat Thực Tế Kèm Suy Luận Sâu (Thinking Mode)")
	fmt.Println("----------------------------------------------------------------")
	enableThinking := true
	promptThinking := "Giải thích ngắn gọn trong 2 câu: Nguyên lý hoạt động của kiến trúc Transformer là gì?"
	reqThinking := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: promptThinking},
		},
		Thinking: &enableThinking,
		Stream:   false,
	}

	t0 = time.Now()
	respThinking, err := chatService.ExecuteChatSync(ctx, reqThinking)
	latencyChat := time.Since(t0).Milliseconds()
	if err != nil {
		fmt.Printf("❌ Lỗi gọi Chat Thinking: %v\n", err)
	} else {
		fmt.Printf("✅ Chat Thinking thành công (%d ms):\n", latencyChat)
		fmt.Printf("  • Conversation ID: %s\n", respThinking.ConversationID)
		fmt.Printf("  • Response ID: %s\n", respThinking.ResponseID)
		fmt.Printf("  • Choice ID: %s\n", respThinking.ChoiceID)
		if len(respThinking.Thinking) > 0 {
			fmt.Printf("  • Khối suy luận Thinking (%d blocks):\n", len(respThinking.Thinking))
			for i, th := range respThinking.Thinking {
				preview := th.Content
				if len(preview) > 120 {
					preview = preview[:120] + "..."
				}
				fmt.Printf("    [%d] %s\n", i+1, preview)
			}
		} else {
			fmt.Println("  • (Không có thinking blocks riêng, model đã trả lời trực tiếp)")
		}
		if len(respThinking.Choices) > 0 {
			answer := respThinking.Choices[0].Message.Content
			if len(answer) > 200 {
				answer = answer[:200] + "..."
			}
			fmt.Printf("  • Nội dung câu trả lời: %s\n", answer)
		}
	}

	// -------------------------------------------------------------------------
	// TEST 4: Chat Thực Tế với Tìm Kiếm Web (Search Grounding)
	// -------------------------------------------------------------------------
	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("🧪 TEST 4: Gửi Chat Kèm Tìm Kiếm Web Thời Gian Thực (Search Grounding)")
	fmt.Println("----------------------------------------------------------------")
	enableGrounding := true
	promptGrounding := "Thời tiết Hà Nội hôm nay thế nào?"
	reqGrounding := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: promptGrounding},
		},
		SearchGrounding: &enableGrounding,
		Stream:          false,
	}

	t0 = time.Now()
	respGrounding, err := chatService.ExecuteChatSync(ctx, reqGrounding)
	latencyGrounding := time.Since(t0).Milliseconds()
	if err != nil {
		fmt.Printf("❌ Lỗi gọi Chat Search Grounding: %v\n", err)
	} else {
		fmt.Printf("✅ Search Grounding thành công (%d ms):\n", latencyGrounding)
		if respGrounding.Grounding != nil {
			fmt.Printf("  • Trích xuất được %d nguồn tham khảo (Sources):\n", len(respGrounding.Grounding.Sources))
			for i, src := range respGrounding.Grounding.Sources {
				fmt.Printf("    [%d] Domain: %s | Title: %s | URL: %s\n", i+1, src.Domain, src.Title, src.URL)
			}
			if len(respGrounding.Grounding.SearchQueries) > 0 {
				fmt.Printf("  • Từ khóa truy vấn Google: %v\n", respGrounding.Grounding.SearchQueries)
			}
		} else {
			fmt.Println("  • (Mô hình trả lời trực tiếp mà không cần trigger truy vấn ngoài)")
		}
		if len(respGrounding.Choices) > 0 {
			answer := respGrounding.Choices[0].Message.Content
			if len(answer) > 160 {
				answer = answer[:160] + "..."
			}
			fmt.Printf("  • Nội dung: %s\n", answer)
		}
	}

	// -------------------------------------------------------------------------
	// TEST 5: Chat Thực Tế với Python Sandbox / Code Interpreter
	// -------------------------------------------------------------------------
	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("🧪 TEST 5: Gửi Chat Thực Thi Mã Python Sandbox (Code Interpreter)")
	fmt.Println("----------------------------------------------------------------")
	enableCode := true
	promptCode := "Dùng Python tính tổng các số từ 1 đến 100 và in ra kết quả."
	reqCode := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: promptCode},
		},
		CodeInterpreter: &enableCode,
		Stream:          false,
	}

	t0 = time.Now()
	respCode, err := chatService.ExecuteChatSync(ctx, reqCode)
	latencyCode := time.Since(t0).Milliseconds()
	if err != nil {
		fmt.Printf("❌ Lỗi gọi Chat Code Interpreter: %v\n", err)
	} else {
		fmt.Printf("✅ Code Interpreter thành công (%d ms):\n", latencyCode)
		if len(respCode.CodeExecutions) > 0 {
			fmt.Printf("  • Tìm thấy %d khối thực thi mã Python:\n", len(respCode.CodeExecutions))
			for i, ce := range respCode.CodeExecutions {
				fmt.Printf("    [%d] Code: %s\n", i+1, ce.Code)
				if ce.Stdout != "" {
					fmt.Printf("        Stdout: %s\n", ce.Stdout)
				}
				if ce.Stderr != "" {
					fmt.Printf("        Stderr: %s\n", ce.Stderr)
				}
				if len(ce.Images) > 0 {
					fmt.Printf("        Images: %d ảnh PNG được vẽ\n", len(ce.Images))
				}
			}
		} else {
			fmt.Println("  • (Mô hình trả kết quả trực tiếp)")
		}
		if len(respCode.Choices) > 0 {
			answer := respCode.Choices[0].Message.Content
			if len(answer) > 160 {
				answer = answer[:160] + "..."
			}
			fmt.Printf("  • Nội dung: %s\n", answer)
		}
	}

	// -------------------------------------------------------------------------
	// TEST 6: Multimodal Upload qua Push Clients6 Service
	// -------------------------------------------------------------------------
	fmt.Println("\n----------------------------------------------------------------")
	fmt.Println("🧪 TEST 6: Tải Lên Tệp Đa Phương Thức (Google Push Storage)")
	fmt.Println("----------------------------------------------------------------")
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: 37, G: 99, B: 235, A: 255})
		}
	}
	var imgBuf bytes.Buffer
	_ = png.Encode(&imgBuf, img)

	t0 = time.Now()
	storageToken, err := geminiUploadService.UploadFile(ctx, "test_blue_pixel.png", "image/png", &imgBuf, int64(imgBuf.Len()))
	latencyUpload := time.Since(t0).Milliseconds()
	if err != nil {
		fmt.Printf("⚠️ Upload trả lời: %v\n", err)
	} else {
		fmt.Printf("✅ Upload tệp thành công (%d ms)!\n", latencyUpload)
		fmt.Printf("  • SCOTTY Storage Token: %s\n", storageToken)
	}

	fmt.Println("\n================================================================")
	fmt.Println("🎉 HOÀN TẤT KIỂM THỬ THỰC TẾ HỆ THỐNG GEMINI!")
	fmt.Println("================================================================")
}
