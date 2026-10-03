package daemon

import (
	"context"
	"database/sql"
	"errors"
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
	"dezuxk-gateway/internal/adapters/outbound/mcp"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/storage"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
	"dezuxk-gateway/internal/core/services/agent"
	"dezuxk-gateway/internal/core/services/policy"
)

// ValidateInfrastructureAdapters kiểm tra tính sẵn sàng thực tế của các adapter hạ tầng (Fail Fast)
func ValidateInfrastructureAdapters(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Storage.Driver), "postgres") {
		return errors.New("postgres storage driver configured but adapter is not implemented")
	}
	if cfg.Distributed.Enabled {
		return errors.New("distributed mode configured but Redis adapters are not implemented")
	}
	return nil
}

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

	if err := ValidateInfrastructureAdapters(cfg); err != nil {
		return fmt.Errorf("không thể khởi động Gateway: %w", err)
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
			if !cfg.Storage.AllowMemoryFallback {
				return fmt.Errorf("khởi tạo SqliteKeyRepository thất bại: %w", err)
			}
			log.Printf("[Key Database Warning] Không thể khởi tạo SqliteKeyRepository: %v, dùng bộ nhớ RAM", err)
			keyRepo = session.NewMemoryKeyRepository()
		} else {
			keyRepo = kr
		}
	} else {
		keyRepo = session.NewMemoryKeyRepository()
	}
	masterAdminKey := cfg.Server.APIKey
	var additionalAdminTokens []string
	if cfg.Admin.IsEnabled() && cfg.Admin.GetSessionToken() != "" {
		additionalAdminTokens = append(additionalAdminTokens, cfg.Admin.GetSessionToken())
	}
	if masterAdminKey == "" && len(additionalAdminTokens) > 0 {
		masterAdminKey = additionalAdminTokens[0]
	}
	keyService := services.NewKeyService(keyRepo, masterAdminKey, additionalAdminTokens...)

	metrics := domain.NewContractMetrics()

	// 4. Khởi tạo Outbound Adapters với lớp bọc Upstream Resilience (Circuit Breaker, Backoff, Jitter, Retry-After)
	rawUpstream := google.NewGoogleTransportAdapter(cfg)
	upstreamTransport := google.NewResilientUpstreamClient(rawUpstream)

	// 5. Khởi tạo Profile Manager (Mỗi tài khoản Google 1 folder riêng, lưu cookies & cấu hình)
	profileManager, err := session.NewProfileManager(cfg, sessionRepo, modelRegistry, tokenExtractor, vault)
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
	profileManager.SetQuotaUseCase(geminiQuotaService)
	geminiUploadService := services.NewGeminiUploadService(sessionRepo, upstreamTransport, rpcRegistry)
	geminiCanvasService := services.NewGeminiCanvasService(sessionRepo, upstreamTransport, rpcRegistry)
	geminiFeedbackService := services.NewGeminiFeedbackService(sessionRepo, upstreamTransport, rpcRegistry)

	// Khởi tạo VisionResolver & TokenCounter & ChatService
	visionResolver := services.NewVisionResolver(cfg.Vision, geminiUploadService)
	tokenCounter := services.NewTokenCounter(cfg.Tokens)

	chatService := services.NewChatService(modelRegistry, sessionRepo, upstreamTransport, wire, metrics)
	chatService.SetRpcRegistry(rpcRegistry)
	if mediaStorage != nil {
		chatService.SetStorage(mediaStorage)
	}
	chatService.SetVisionResolver(visionResolver)
	chatService.SetTokenCounter(tokenCounter)
	chatService.SetFailoverConfig(cfg.Failover)
	if keyService != nil {
		chatService.SetKeyUseCase(keyService)
	}
	chatService.SetChatDefaults(cfg.ChatDefaults)

	if sr, ok := sessionRepo.(interface{ SetCoolingDuration(time.Duration) }); ok {
		sr.SetCoolingDuration(cfg.Failover.GetCoolingDuration())
	}

	log.Printf("[Vision] Kích hoạt Vision Resolver (Upload Method: %s, Max Size: %d bytes)",
		cfg.Vision.GetUploadMethod(), cfg.Vision.GetMaxImageSizeBytes())
	log.Printf("[Tokens] Kích hoạt Token Counter (Encoding: %s)", cfg.Tokens.GetEncoding())
	log.Printf("[Failover] Kích hoạt Next-Account Failover (Max Attempts: %d, Cooldown: %v)",
		cfg.Failover.GetMaxAttempts(), cfg.Failover.GetCoolingDuration())

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

	// Khởi tạo Autonomous Agent Tool Registry, Checkpoint Repo & Runners
	toolRegistry := tools.NewToolRegistry()
	tools.RegisterDefaultTools(toolRegistry, ".")
	policyEngine := policy.NewPolicyEngine(toolRegistry, nil)
	agentRunner := agent.NewRunner(chatService, toolRegistry, nil)
	agentRunner.SetPolicyEngine(policyEngine)
	agentRunner.SetKeyUseCase(keyService)

	var db *sql.DB
	if sqliteRepo != nil {
		db = sqliteRepo.DB()
	}
	checkpointRepo, memoryRepo, agentRunRepo, repoErr := InitCriticalRepositories(cfg, db)
	if repoErr != nil {
		return repoErr
	}
	agentRunner.SetCheckpointRepository(checkpointRepo)
	if ledger, ok := agentRunRepo.(ports.ToolExecutionLedger); ok {
		agentRunner.SetToolExecutionLedger(ledger)
	}

	agentJobService := agent.NewJobService(agentRunRepo, agentRunner)
	agentJobService.SetCheckpointRepository(checkpointRepo)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()
	_ = agentJobService.Start(serverCtx)

	readinessManager := adaptersHTTP.NewReadinessManager()
	readinessManager.SetReady(false)

	// Khởi tạo 3-Tier Memory Manager (Working, Recall, Archival)
	initialCore := domain.CoreMemory{
		Persona:        "Dezuxk Autonomous Engineering Agent (Tự trị • Kiểm chứng • Chuẩn chỉ)",
		ProjectContext: "Hexagonal Clean Architecture, Zero Hardcoding, SQLite WAL persistence, Extended Thinking",
	}
	memoryService := agent.NewMemoryManager(memoryRepo, chatService, "gemini-3.8-flash", initialCore)
	agentRunner.SetMemoryService(memoryService)
	tools.RegisterMemoryTools(toolRegistry, memoryService)

	// Đăng ký Chrome CDP Browser Tools (Web Automation)
	tools.RegisterBrowserTools(toolRegistry, 9222, filepath.Join(".", ".dezuxk", "screenshots"))

	// Khởi tạo Isolated Sub-agents Supervisor & đăng ký invoke_subagent tool
	subagentSupervisor := agent.NewSubagentSupervisor(chatService, toolRegistry, nil, memoryService)
	tools.RegisterSubagentTool(toolRegistry, subagentSupervisor)

	// Tự động kết nối và nạp các công cụ từ danh mục MCP Servers (Model Context Protocol)
	if len(cfg.MCPServers) > 0 {
		for sName, sCfg := range cfg.MCPServers {
			if strings.TrimSpace(sCfg.Command) == "" {
				continue
			}
			mcpClient, err := mcp.StartStdioMCPClient(context.Background(), sName, sCfg.Command, sCfg.Args...)
			if err != nil {
				log.Printf("[MCP] Không thể kết nối MCP Server %s: %v", sName, err)
				continue
			}
			mcpTools, err := mcpClient.ListTools(context.Background())
			if err != nil {
				log.Printf("[MCP] Không thể lấy danh sách tools từ MCP Server %s: %v", sName, err)
				continue
			}
			for _, def := range mcpTools {
				adapter := mcp.NewMCPToolAdapter(mcpClient, def, domain.PermissionSafe)
				toolRegistry.RegisterTool(adapter)
				log.Printf("[MCP] Đã đăng ký tool: %s (%s)", adapter.Name(), adapter.Description())
			}
		}
	}

	graphRunner := agent.NewGraphEngine(chatService, toolRegistry, checkpointRepo, nil, 3)
	graphRunner.SetPolicyEngine(policyEngine)

	httpRouter := adaptersHTTP.BuildRouter(adaptersHTTP.RouterDependencies{
		Config:                cfg,
		ModelRegistry:         modelRegistry,
		ChatUseCase:           chatService,
		ProfileUseCase:        profileManager,
		MediaStorage:          mediaStorage,
		Metrics:               metrics,
		SessionRepo:           sessionRepo,
		RateLimiter:           rateLimiter,
		GeminiHistoryUseCase:  geminiHistoryService,
		GeminiQuotaUseCase:    geminiQuotaService,
		GeminiCanvasUseCase:   geminiCanvasService,
		GeminiUploadUseCase:   geminiUploadService,
		GeminiFeedbackUseCase: geminiFeedbackService,
		KeyUseCase:            keyService,
		AlertDispatcher:       alertDispatcher,
		ResponseCache:         responseCache,
		AgentRunner:           agentRunner,
		GraphRunner:           graphRunner,
		ToolRegistry:          toolRegistry,
		CheckpointRepo:        checkpointRepo,
		MemoryService:         memoryService,
		SubagentSupervisor:    subagentSupervisor,
		AgentJobService:       agentJobService,
		AgentRunRepo:          agentRunRepo,
		ReadinessManager:      readinessManager,
	})

	// 8. Khởi động GeminiChatGoldenJob và Proactive Session Keep-Alive Worker
	goldenCtx, goldenCancel := context.WithCancel(context.Background())
	defer goldenCancel()
	startGeminiChatGoldenRunner(goldenCtx, wire, upstreamTransport, sessionRepo, metrics, cfg.GoldenJob, alertDispatcher)
	startProactiveKeepAliveRunner(goldenCtx, sessionRepo, geminiQuotaService, cfg.KeepAlive)
	startStartupTierDiscovery(goldenCtx, sessionRepo, geminiQuotaService)

	// Phục hồi an toàn các tác vụ Agent dở dang trước khi mở sẵn sàng
	log.Println("[Agent Recovery] Đang quét và khôi phục các tác vụ Agent dở dang...")
	if recovered, err := agentJobService.RecoverPendingRuns(serverCtx); err != nil {
		log.Printf("[Agent Recovery Warning] Lỗi khi khôi phục pending runs: %v", err)
	} else if len(recovered) > 0 {
		log.Printf("[Agent Recovery] Đã khôi phục và xử lý an toàn %d tác vụ dở dang sau khởi động lại.", len(recovered))
	}
	readinessManager.SetReady(true)

	serverAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:              serverAddr,
		Handler:           httpRouter,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
	}

	// 9. Chạy Server trong Goroutine
	go func() {
		log.Printf("[Server] Dezuxk Gateway đang lắng nghe tại: http://%s", serverAddr)
		log.Printf("[Server] Web Dashboard UI:  http://%s/admin/", serverAddr)
		log.Printf("[Server] Admin Overview API: http://%s/v1/admin/overview", serverAddr)
		log.Printf("[Server] OpenAI API Base:    http://%s/v1", serverAddr)
		log.Printf("[Server] Agent Engine API:   http://%s/v1/agent/run", serverAddr)
		log.Printf("[Server] Agent Memory API:   http://%s/v1/agent/memory/core", serverAddr)
		log.Printf("[Server] Profiles API:       http://%s/v1/profiles", serverAddr)
		log.Printf("[Server] Health API:         http://%s/health", serverAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Server] Fatal error: %v", err)
		}
	}()

	// 10. Graceful Shutdown khi nhận tín hiệu kết thúc từ hệ điều hành
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Server] Bắt đầu quy trình tắt máy chủ an toàn (Graceful Shutdown)...")
	// 1. Chuyển /ready sang 503 ngay lập tức trước khi drain
	readinessManager.SetReady(false)

	totalShutdown := cfg.Server.GetShutdownTimeout()
	if totalShutdown <= 0 {
		totalShutdown = 30 * time.Second
	}

	httpBudget := totalShutdown / 3
	if httpBudget > 10*time.Second {
		httpBudget = 10 * time.Second
	}
	agentBudget := totalShutdown - httpBudget

	// 2. Dừng nhận HTTP và drain kết nối
	log.Printf("[Server] Dừng tiếp nhận HTTP và drain kết nối (budget: %v)...", httpBudget)
	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), httpBudget)
	if err := httpServer.Shutdown(httpCtx); err != nil {
		log.Printf("[Server Warning] Lỗi khi dừng HTTP server: %v", err)
	}
	cancelHTTP()

	// 3. Dừng tiếp nhận Agent Runs mới và drain / cancel JobService workers
	log.Printf("[Agent] Dừng tiếp nhận Agent Runs mới và drain workers (budget: %v)...", agentBudget)
	agentCtx, cancelAgent := context.WithTimeout(context.Background(), agentBudget)
	agentErr := agentJobService.Shutdown(agentCtx)
	cancelAgent()
	if agentErr != nil {
		log.Printf("[Agent Alert] Timeout khi chờ Agent Job Service shutdown: %v", agentErr)
	}

	// 4. Đóng kết nối cơ sở dữ liệu SQLite sau khi tất cả worker đã hoàn tất ghi trạng thái
	// TUYỆT ĐỐI không close SQLite nếu JobService workers chưa dừng!
	if sqliteRepo != nil {
		if agentErr == nil {
			log.Println("[Database] Đóng cơ sở dữ liệu SQLite...")
			_ = sqliteRepo.Close()
		} else {
			log.Printf("[Database Protection] Bỏ qua đóng SQLite vì JobService workers chưa dừng hẳn (tránh database write after close).")
		}
	}

	log.Println("[Server] Gateway đã dừng hoàn toàn sạch sẽ.")
	return nil
}

// startGeminiChatGoldenRunner vận hành chu kỳ đối soát hợp đồng StreamGenerate định kỳ trên tài khoản lab
func startGeminiChatGoldenRunner(
	ctx context.Context,
	wire ports.WireCodec,
	transport ports.UpstreamGoogleTransport,
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

	job := google.NewGeminiChatGoldenJob(wire, transport, metrics, func(report google.ChatGoldenReport) {
		if report.DriftDetected {
			log.Printf("[Golden Job Alert] SCHEMA DRIFT StreamGenerate: %s", report.AlertMessage)
			if alertDispatcher != nil {
				alertDispatcher.Dispatch(domain.AlertPayload{
					AccountID:      report.AccountID,
					ErrorType:      "Schema Drift Detected (StreamGenerate)",
					Service:        domain.ServiceGemini,
					Reason:         fmt.Sprintf("Hợp đồng StreamGenerate phát hiện Schema Drift: %s", report.AlertMessage),
					ActionRequired: "Vui lòng mở Google Chrome đối soát lại CDP qua /v1/profiles/" + report.AccountID + "/sync và cập nhật baseline spec",
					Timestamp:      time.Now(),
				})
			}
		} else if !report.Passed {
			log.Printf("[Golden Job Alert] CONTRACT FAIL StreamGenerate: %s", report.AlertMessage)
			if alertDispatcher != nil {
				alertDispatcher.Dispatch(domain.AlertPayload{
					AccountID:      report.AccountID,
					ErrorType:      "Contract Failure (StreamGenerate)",
					Service:        domain.ServiceGemini,
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
		loggedStandby := false
		allowAny := os.Getenv("DEZUXK_GOLDEN_ALLOW_ANY_ACCOUNT") == "true"
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
				if labAccount == nil && allowAny && len(accounts) > 0 {
					labAccount = accounts[0]
				}
				if labAccount == nil {
					if !loggedStandby {
						log.Printf("[Golden Job Standby] Chưa có tài khoản lab (ID chứa '%s'). Bỏ qua đối soát StreamGenerate (thiết lập DEZUXK_GOLDEN_ALLOW_ANY_ACCOUNT=true để dùng tài khoản hiện có).", labPrefix)
						loggedStandby = true
					}
					continue
				}
				loggedStandby = false
				log.Printf("[Golden Job] Bắt đầu đối soát hợp đồng StreamGenerate trên tài khoản lab: %s", labAccount.ID)
				report, err := job.Run(ctx, labAccount, "ping")
				if err != nil {
					log.Printf("[Golden Job Error] Thất bại trên tài khoản lab %s: %v", labAccount.ID, err)
				} else if report.DriftDetected {
					log.Printf("[Golden Job Warning] Drift detected trên %s: %s", labAccount.ID, report.AlertMessage)
				} else {
					log.Printf("[Golden Job Success] Đối soát StreamGenerate thành công trên %s (passed=%v)", labAccount.ID, report.Passed)
				}
			}
		}
	}()
}

// startProactiveKeepAliveRunner chạy worker ngầm định kỳ gửi request đọc nhẹ (/usage) để duy trì tính tươi mới của __Secure-1PSIDTS
func startProactiveKeepAliveRunner(
	ctx context.Context,
	repo ports.SessionRepository,
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

			// Duy trì Gemini session (__Secure-1PSIDTS) qua endpoint /usage
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

// startStartupTierDiscovery tự động truy vấn Google RPC otAQ7b để xác thực và cập nhật chính xác cấp độ Tier (Pro/Ultra/Free) cho toàn bộ tài khoản ngay khi khởi động
func startStartupTierDiscovery(
	ctx context.Context,
	repo ports.SessionRepository,
	geminiQuota ports.GeminiQuotaUseCase,
) {
	if geminiQuota == nil || repo == nil {
		return
	}
	go func() {
		// Chờ 2 giây để HTTP Server và các cấu phần hoàn tất lắng nghe
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}

		accounts := repo.ListAll(ctx)
		if len(accounts) == 0 {
			return
		}

		log.Printf("[Startup Tier Discovery] Bắt đầu tự động quét và xác thực cấp độ gói cho %d tài khoản từ Google...", len(accounts))
		for _, acc := range accounts {
			if !acc.IsHealthy || !acc.ServiceReady(domain.ServiceGemini) {
				continue
			}
			quotaCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			tierInfo, err := geminiQuota.GetAccountTierForAccount(quotaCtx, acc)
			cancel()
			if err != nil {
				log.Printf("[Startup Tier Discovery Warning] Không thể tra cứu Tier cho %s: %v", acc.ID, err)
			} else {
				log.Printf("[Startup Tier Discovery Success] Tài khoản %s đã được xác thực từ Google: %s (Tier %d)", acc.ID, tierInfo.TierCode, acc.Tier)
			}
		}
	}()
}

// InitCriticalRepositories khởi tạo các repository tác vụ bền vững (Checkpoint, Memory, AgentRun)
// Tuân thủ triệt để: trong môi trường production (allow_memory_fallback = false), nếu SQLite migration/init lỗi thì buộc phải báo lỗi startup fail.
func InitCriticalRepositories(cfg *config.Config, db *sql.DB) (ports.CheckpointRepository, ports.MemoryRepository, ports.AgentRunRepository, error) {
	var checkpointRepo ports.CheckpointRepository
	var memoryRepo ports.MemoryRepository
	var agentRunRepo ports.AgentRunRepository

	allowFallback := cfg != nil && cfg.Storage.AllowMemoryFallback

	if db != nil {
		cpRepo, err := session.NewSqliteCheckpointRepository(db)
		if err != nil {
			if !allowFallback {
				return nil, nil, nil, fmt.Errorf("khởi tạo SqliteCheckpointRepository thất bại: %w", err)
			}
			log.Printf("[Storage Warning] Khởi tạo SqliteCheckpointRepository thất bại, fallback sang MemoryCheckpointRepository: %v", err)
		} else {
			checkpointRepo = cpRepo
		}

		mRepo, err := session.NewSqliteMemoryRepository(db)
		if err != nil {
			if !allowFallback {
				return nil, nil, nil, fmt.Errorf("khởi tạo SqliteMemoryRepository thất bại: %w", err)
			}
			log.Printf("[Storage Warning] Khởi tạo SqliteMemoryRepository thất bại, fallback sang MemoryMemoryRepository: %v", err)
		} else {
			memoryRepo = mRepo
		}

		arRepo, err := session.NewSqliteAgentRunRepository(db)
		if err != nil {
			if !allowFallback {
				return nil, nil, nil, fmt.Errorf("khởi tạo SqliteAgentRunRepository thất bại (migration fail): %w", err)
			}
			log.Printf("[Storage Warning] Khởi tạo SqliteAgentRunRepository thất bại, fallback sang MemoryAgentRunRepository: %v", err)
		} else {
			agentRunRepo = arRepo
		}
	}

	if checkpointRepo == nil {
		checkpointRepo = session.NewMemoryCheckpointRepository()
	}
	if memoryRepo == nil {
		memoryRepo = session.NewMemoryMemoryRepository()
	}
	if agentRunRepo == nil {
		agentRunRepo = session.NewMemoryAgentRunRepository()
	}

	return checkpointRepo, memoryRepo, agentRunRepo, nil
}
