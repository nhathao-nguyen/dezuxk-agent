package daemon

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/adapters/outbound/alerts"
	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/storage"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
)

// Run khởi động toàn bộ hạ tầng Dezuxk AI Gateway ở chế độ Headless Daemon
func Run(configPath string, portOverride int) error {
	log.Println("=================================================================")
	log.Println("   DEZUXK AI SERVER GATEWAY - HOÀN TOÀN SẠCH (HEADLESS DAEMON)   ")
	log.Println("=================================================================")

	// 1. Nạp cấu hình hạ tầng thuần túy
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("không thể khởi động Gateway do lỗi cấu hình: %w", err)
	}

	if portOverride > 0 {
		cfg.Server.Port = portOverride
	}

	// Nạp danh mục mô hình động từ configs/models.yaml
	if err := domain.LoadModelsFile("configs/models.yaml"); err != nil {
		log.Printf("[Config Warning] Không thể đọc configs/models.yaml (%v), dùng danh mục mặc định", err)
	} else {
		log.Println("[Config] Đã nạp danh mục mô hình động từ configs/models.yaml")
	}

	// 2. Khởi tạo Domain Registries
	modelRegistry := domain.NewModelRegistry(nil)
	rpcRegistry := domain.DefaultRpcRegistry()
	for id, rpcCfg := range cfg.Rpcs {
		rpcRegistry.Override(id, rpcCfg.PathPattern, rpcCfg.TargetHost, rpcCfg.RequiresAt, rpcCfg.Description)
	}
	if len(cfg.Rpcs) > 0 {
		log.Printf("[Config] Đã áp dụng %d cấu hình RPC tùy biến/ghi đè từ YAML.", len(cfg.Rpcs))
	}
	tokenExtractor := google.NewGoogleTokenExtractorAdapter(cfg.Server.ShortTimeout())

	// 3. Khởi tạo Persistent SQLite Session Repository (WAL mode) với Secret Vault (AES-256-GCM)
	masterKey := session.ResolveMasterKey(cfg.Security.MasterKey)
	vault := session.NewVault(masterKey)

	dbPath := cfg.Storage.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join("storage", "gateway.db")
	}
	refresher := google.NewDerivedSecretRefresher(tokenExtractor)
	var sessionRepo ports.SessionRepository

	sqliteRepo, err := session.NewSqliteSessionRepository(dbPath, refresher, vault)
	if err != nil {
		log.Printf("[Database Warning] Không thể mở SQLite (%v), dùng bộ nhớ RAM MemorySessionRepository", err)
		sessionRepo = session.NewMemorySessionRepository(refresher)
	} else {
		log.Printf("[Database] Đã kích hoạt lưu trữ bền vững SQLite tại %s (WAL Mode, Vault AES-256-GCM)", dbPath)
		sessionRepo = sqliteRepo
		defer sqliteRepo.Close()
	}

	// Khởi tạo Webhook Alert Dispatcher (Telegram / Discord / Slack / Generic)
	alertDispatcher := alerts.NewWebhookAlertDispatcher(cfg.Alerts.Webhook)
	defer alertDispatcher.Close()
	if cfg.Alerts.Webhook.IsEnabled() {
		log.Printf("[Alerts] Webhook Alert Dispatcher đã kích hoạt (Provider: %s)", cfg.Alerts.Webhook.GetProvider())
	}
	if sa, ok := sessionRepo.(ports.SessionAlertNotifier); ok {
		sa.SetAlertDispatcher(alertDispatcher)
	}

	// Khởi tạo Key Repository & Key Service (Virtual API Keys đa người dùng)
	var keyRepo ports.KeyRepository
	if sqliteRepo != nil {
		kr, err := session.NewSqliteKeyRepository(sqliteRepo.DB())
		if err != nil {
			log.Printf("[Key Database Warning] Không thể khởi tạo SqliteKeyRepository: %v, dùng bộ nhớ RAM", err)
			keyRepo = session.NewMemoryKeyRepository()
		} else {
			keyRepo = kr
		}
	} else {
		keyRepo = session.NewMemoryKeyRepository()
	}
	keyService := services.NewKeyService(keyRepo, cfg.Server.APIKey)

	metrics := domain.NewContractMetrics()

	// 4. Khởi tạo Outbound Adapters
	upstreamTransport := google.NewGoogleTransportAdapter(cfg)
	flowClient := google.NewFlowClientAdapter(upstreamTransport, rpcRegistry, metrics)

	// 5. Khởi tạo Profile Manager (Mỗi tài khoản Google 1 folder riêng, lưu cookies & cấu hình)
	profileManager, err := session.NewProfileManager(cfg, sessionRepo, modelRegistry, tokenExtractor, flowClient, vault)
	if err != nil {
		return fmt.Errorf("lỗi khởi tạo Profile Manager: %w", err)
	}

	// 6. Quét các thư mục profile đã có trong profiles/
	ctx := context.Background()
	discovered, err := profileManager.ScanAndDiscover(ctx)
	if err != nil {
		log.Printf("[Profile] Cảnh báo khi quét profiles: %v", err)
	} else {
		log.Printf("[Profile] Đã quét %d thư mục profile trong %s", len(discovered), cfg.Profiles.BaseDir)
	}

	activeCount := modelRegistry.Count()
	if activeCount == 0 {
		log.Println("[Gateway Standby] Chưa có tài khoản Google nào đăng nhập. Server ở trạng thái chờ (0 models).")
		log.Println("[Gateway Standby] Dữ liệu mô hình sẽ tự động kích hoạt ngay sau khi nạp Cookie qua API /v1/profiles.")
	} else {
		log.Printf("[Gateway Ready] Đã kích hoạt %d mô hình từ các profile đã đăng nhập.", activeCount)
	}

	// 7. Khởi tạo Core Services & Inbound HTTP Router
	wire := google.NewWireAdapter(rpcRegistry)
	mediaStorage, err := storage.NewLocalStorageAdapter(cfg.Media.StorageDir, cfg.Media.BaseURL)
	if err != nil {
		log.Printf("[Storage Warning] Không thể khởi tạo LocalStorageAdapter: %v", err)
	}

	// Khởi tạo các Gemini Services nâng cao (Production)
	geminiHistoryService := services.NewGeminiHistoryService(sessionRepo, upstreamTransport, rpcRegistry, metrics)
	geminiQuotaService := services.NewGeminiQuotaService(sessionRepo, upstreamTransport, rpcRegistry)
	geminiUploadService := services.NewGeminiUploadService(sessionRepo, upstreamTransport, rpcRegistry)
	geminiCanvasService := services.NewGeminiCanvasService(sessionRepo, upstreamTransport, rpcRegistry)
	geminiFeedbackService := services.NewGeminiFeedbackService(sessionRepo, upstreamTransport, rpcRegistry)

	// Khởi tạo VisionResolver & TokenCounter & ChatService
	visionResolver := services.NewVisionResolver(cfg.Vision, geminiUploadService)
	tokenCounter := services.NewTokenCounter(cfg.Tokens)

	chatService := services.NewChatService(modelRegistry, sessionRepo, upstreamTransport, wire, metrics)
	if mediaStorage != nil {
		chatService.SetStorage(mediaStorage)
	}
	chatService.SetVisionResolver(visionResolver)
	chatService.SetTokenCounter(tokenCounter)
	chatService.SetFailoverConfig(cfg.Failover)

	if sr, ok := sessionRepo.(interface{ SetCoolingDuration(time.Duration) }); ok {
		sr.SetCoolingDuration(cfg.Failover.GetCoolingDuration())
	}

	log.Printf("[Vision] Kích hoạt Vision Resolver (Upload Method: %s, Max Size: %d bytes)",
		cfg.Vision.GetUploadMethod(), cfg.Vision.GetMaxImageSizeBytes())
	log.Printf("[Tokens] Kích hoạt Token Counter (Encoding: %s)", cfg.Tokens.GetEncoding())
	log.Printf("[Failover] Kích hoạt Next-Account Failover (Max Attempts: %d, Cooldown: %v)",
		cfg.Failover.GetMaxAttempts(), cfg.Failover.GetCoolingDuration())

	mediaService := services.NewMediaService(modelRegistry, sessionRepo, upstreamTransport, wire, flowClient, profileManager, metrics)
	if mediaStorage != nil {
		mediaService.SetStorage(mediaStorage)
	}
	mediaService.SetCreditCosts(domain.NewFlowCreditCostRegistry(cfg.Flow.CreditCosts))
	if cfg.Flow.ProjectTitlePrefix != "" {
		mediaService.SetProjectTitlePrefix(cfg.Flow.ProjectTitlePrefix)
	}

	flowCreditService := services.NewFlowCreditService(sessionRepo, flowClient, metrics)

	// Khởi tạo In-Memory Response Caching (Giai đoạn 3: Phản hồi < 5ms)
	responseCache := services.NewResponseCache(cfg.Cache)
	if cfg.Cache.IsEnabled() {
		log.Printf("[Cache] Kích hoạt In-Memory Response Caching (Max Entries: %d, TTL: %v, Methods: %v)",
			cfg.Cache.GetMaxEntries(), cfg.Cache.GetTTL(), cfg.Cache.Methods)
	}

	// Khởi tạo Rate Limiter bảo vệ Gateway với cấu hình động và Trusted Proxies
	rateLimitCfg := cfg.Server.RateLimit
	maxReqs := rateLimitCfg.MaxRequests
	if maxReqs <= 0 {
		maxReqs = 120
	}
	windowSecs := rateLimitCfg.WindowSeconds
	if windowSecs <= 0 {
		windowSecs = 60
	}
	rateLimiter := adaptersHTTP.NewIPRateLimiter(maxReqs, time.Duration(windowSecs)*time.Second, cfg.Server.TrustedProxies)

	httpRouter := adaptersHTTP.BuildRouter(adaptersHTTP.RouterDependencies{
		Config:                 cfg,
		ModelRegistry:          modelRegistry,
		ChatUseCase:            chatService,
		ProfileUseCase:         profileManager,
		FlowCreditUseCase:      flowCreditService,
		MediaUseCase:           mediaService,
		FlowUseCase:            mediaService,
		MediaStorage:           mediaStorage,
		Metrics:                metrics,
		SessionRepo:            sessionRepo,
		RateLimiter:            rateLimiter,
		GeminiHistoryUseCase:   geminiHistoryService,
		GeminiQuotaUseCase:     geminiQuotaService,
		GeminiCanvasUseCase:    geminiCanvasService,
		GeminiUploadUseCase:    geminiUploadService,
		GeminiFeedbackUseCase:  geminiFeedbackService,
		KeyUseCase:             keyService,
		AlertDispatcher:        alertDispatcher,
		ResponseCache:          responseCache,
	})

	// 8. Khởi động NzlxgGoldenJob, Proactive Session Keep-Alive Worker và đồng bộ động ma trận mô hình
	goldenCtx, goldenCancel := context.WithCancel(context.Background())
	defer goldenCancel()
	startNzlxgGoldenRunner(goldenCtx, flowClient, sessionRepo, metrics, cfg.GoldenJob, alertDispatcher)
	startProactiveKeepAliveRunner(goldenCtx, sessionRepo, flowClient, geminiQuotaService, cfg.KeepAlive)
	startFlowModelMatrixSyncRunner(goldenCtx, flowClient, sessionRepo, modelRegistry)

	serverAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:           serverAddr,
		Handler:        httpRouter,
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	// 9. Chạy Server trong Goroutine
	go func() {
		log.Printf("[Server] Dezuxk Gateway đang lắng nghe tại: http://%s", serverAddr)
		log.Printf("[Server] Admin Dashboard UI: http://%s/admin", serverAddr)
		log.Printf("[Server] OpenAI API Base: http://%s/v1", serverAddr)
		log.Printf("[Server] Profiles API:   http://%s/v1/profiles", serverAddr)

		log.Printf("[Server] Health API:     http://%s/health", serverAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Server] Fatal error: %v", err)
		}
	}()

	// 10. Graceful Shutdown khi nhận tín hiệu kết thúc từ hệ điều hành
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Server] Shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("force shutdown error: %w", err)
	}

	log.Println("[Server] Gateway đã dừng hoàn toàn sạch sẽ.")
	return nil
}

// startNzlxgGoldenRunner vận hành chu kỳ đối soát hợp đồng nzlxg định kỳ trên tài khoản lab
func startNzlxgGoldenRunner(
	ctx context.Context,
	client ports.FlowClient,
	repo ports.SessionRepository,
	metrics *domain.ContractMetrics,
	cfg config.GoldenJobConfig,
	alertDispatcher ports.AlertDispatcher,
) {
	if !cfg.IsEnabled() {
		log.Println("[Golden Job] Đã tắt theo cấu hình (enabled: false).")
		return
	}

	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	labPrefix := strings.ToLower(strings.TrimSpace(cfg.LabAccountPrefix))
	if labPrefix == "" {
		labPrefix = "lab"
	}

	job := google.NewNzlxgGoldenJob(client, metrics, func(report google.GoldenReport) {
		if report.DriftDetected {
			log.Printf("[Golden Job Alert] SCHEMA DRIFT nzlxg: %s (unmapped=%d)", report.AlertMessage, report.UnmappedFields)
			if alertDispatcher != nil {
				alertDispatcher.Dispatch(domain.AlertPayload{
					AccountID:      report.AccountID,
					ErrorType:      "Schema Drift Detected (nzlxg)",
					Service:        domain.ServiceFlow,
					Reason:         fmt.Sprintf("Hợp đồng nzlxg phát hiện Schema Drift (unmapped_fields = %d): %s", report.UnmappedFields, report.AlertMessage),
					ActionRequired: "Vui lòng mở Google Chrome đối soát lại CDP qua /v1/profiles/" + report.AccountID + "/sync và cập nhật baseline spec",
					Timestamp:      time.Now(),
				})
			}
		} else if !report.Passed {
			log.Printf("[Golden Job Alert] CONTRACT FAIL nzlxg: %s", report.AlertMessage)
			if alertDispatcher != nil {
				alertDispatcher.Dispatch(domain.AlertPayload{
					AccountID:      report.AccountID,
					ErrorType:      "Contract Failure (nzlxg)",
					Service:        domain.ServiceFlow,
					Reason:         report.AlertMessage,
					ActionRequired: "Vui lòng mở Google Chrome kiểm tra lại trạng thái session qua /v1/profiles/" + report.AccountID + "/launch",
					Timestamp:      time.Now(),
				})
			}
		}
	})

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				accounts := repo.ListAll(ctx)
				var labAccount *domain.ManagedAccount
				for _, acc := range accounts {
					lower := strings.ToLower(acc.ID)
					if strings.HasPrefix(lower, labPrefix) || strings.Contains(lower, "_"+labPrefix) || strings.Contains(lower, "-"+labPrefix) {
						labAccount = acc
						break
					}
				}
				if labAccount == nil {
					log.Printf("[Golden Job Standby] Chưa có tài khoản lab (ID chứa '%s'). Bỏ qua đối soát nzlxg để đảm bảo an toàn tài khoản người dùng.", labPrefix)
					continue
				}
				log.Printf("[Golden Job] Bắt đầu đối soát hợp đồng nzlxg trên tài khoản lab: %s", labAccount.ID)
				report, err := job.Run(ctx, labAccount)
				if err != nil {
					log.Printf("[Golden Job Error] Thất bại trên tài khoản lab %s: %v", labAccount.ID, err)
				} else if report.DriftDetected {
					log.Printf("[Golden Job Warning] Drift detected trên %s: unmapped=%d", labAccount.ID, report.UnmappedFields)
				} else {
					log.Printf("[Golden Job Success] Đối soát nzlxg thành công trên %s: balance=%d unmapped=%d", labAccount.ID, report.Amount, report.UnmappedFields)
				}
			}
		}
	}()
}

// startProactiveKeepAliveRunner chạy worker ngầm định kỳ gửi request đọc nhẹ (/usage, nzlxg) để duy trì tính tươi mới của __Secure-1PSIDTS
func startProactiveKeepAliveRunner(
	ctx context.Context,
	repo ports.SessionRepository,
	flowClient ports.FlowClient,
	geminiQuota ports.GeminiQuotaUseCase,
	cfg config.KeepAliveConfig,
) {
	if !cfg.IsEnabled() {
		log.Println("[Session Keep-Alive] Đã tắt theo cấu hình (enabled: false).")
		return
	}

	interval := cfg.Interval
	if interval <= 0 {
		interval = 4 * time.Hour
	}

	log.Printf("[Session Keep-Alive] Đã kích hoạt worker duy trì độ tươi Cookie (chu kỳ %v).", interval)

	runKeepAlive := func() {
		accounts := repo.ListAll(ctx)
		if len(accounts) == 0 {
			return
		}

		for _, acc := range accounts {
			if !acc.IsHealthy {
				continue
			}

			// 1. Duy trì Gemini session (__Secure-1PSIDTS) qua endpoint /usage
			if acc.ServiceReady(domain.ServiceGemini) && geminiQuota != nil {
				quotaCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
				_, err := geminiQuota.GetQuotaForAccount(quotaCtx, acc)
				cancel()
				if err != nil {
					log.Printf("[Session Keep-Alive Warning] Gemini /usage keep-alive thất bại trên %s: %v", acc.ID, err)
				} else {
					log.Printf("[Session Keep-Alive] Gemini /usage keep-alive thành công trên %s. __Secure-1PSIDTS đã được làm tươi.", acc.ID)
					_ = repo.Save(ctx, acc)
				}
			}

			// 2. Duy trì Flow session qua nzlxg
			if acc.ServiceReady(domain.ServiceFlow) && flowClient != nil {
				flowCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
				balance, err := flowClient.GetCreditsBalance(flowCtx, acc)
				cancel()
				if err != nil {
					log.Printf("[Session Keep-Alive Warning] Flow nzlxg keep-alive thất bại trên %s: %v", acc.ID, err)
				} else {
					acc.CreditsBalance = balance.Amount
					log.Printf("[Session Keep-Alive] Flow nzlxg keep-alive thành công trên %s (balance=%d). Cookies đã được cập nhật.", acc.ID, balance.Amount)
					_ = repo.Save(ctx, acc)
				}
			}
		}
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runKeepAlive()
			}
		}
	}()
}

// startFlowModelMatrixSyncRunner định kỳ gọi HTrJv và yBhWQ cập nhật trạng thái online/offline của cụm GPU mô hình Flow vào ModelRegistry
func startFlowModelMatrixSyncRunner(ctx context.Context, client ports.FlowClient, repo ports.SessionRepository, registry *domain.ModelRegistry) {
	syncModels := func() {
		accounts := repo.ListAll(ctx)
		var flowAcc *domain.ManagedAccount
		for _, acc := range accounts {
			if acc.IsHealthy && acc.ServiceReady(domain.ServiceFlow) {
				flowAcc = acc
				break
			}
		}
		if flowAcc == nil {
			return
		}
		activeMap, err := client.GetActiveModels(ctx, flowAcc)
		if err != nil {
			log.Printf("[Model Registry Sync Warning] Lỗi đồng bộ ma trận mô hình Flow (HTrJv/yBhWQ): %v", err)
			return
		}
		if len(activeMap) > 0 {
			registry.UpdateBackendStatus(domain.ServiceFlow, activeMap)
			log.Printf("[Model Registry Sync] Đã cập nhật ma trận mô hình Flow qua HTrJv/yBhWQ: %d mô hình online", len(activeMap))
		}
	}

	go syncModels()

	ticker := time.NewTicker(3 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncModels()
			}
		}
	}()
}
