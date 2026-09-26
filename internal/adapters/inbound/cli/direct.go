package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
)

// DirectServices encapsulates direct DB, Vault, and Domain services for offline CLI operations.
type DirectServices struct {
	Config               *config.Config
	SessionRepo          ports.SessionRepository
	Vault                *session.Vault
	ProfileManager       ports.ProfileUseCase
	KeyService           ports.KeyUseCase
	ModelRegistry        *domain.ModelRegistry
	ResponseCache        *services.ResponseCache
	ChatService          ports.ChatUseCase
	FlowService          ports.FlowUseCase
	FlowCreditService    ports.FlowCreditUseCase
	GeminiHistoryService ports.GeminiHistoryUseCase
	GeminiQuotaService   ports.GeminiQuotaUseCase

	sqliteRepo *session.SqliteSessionRepository
}

// InitDirectServices loads configuration, opens SQLite and Vault, and wires up core services.
func InitDirectServices(configPath string) (*DirectServices, error) {
	if configPath == "" {
		configPath = "configs/config.yaml"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("không thể khởi tạo DirectServices do lỗi cấu hình: %w", err)
	}

	_ = domain.LoadModelsFile("configs/models.yaml")

	modelRegistry := domain.NewModelRegistry(nil)
	rpcRegistry := domain.DefaultRpcRegistry()
	for id, rpcCfg := range cfg.Rpcs {
		rpcRegistry.Override(id, rpcCfg.PathPattern, rpcCfg.TargetHost, rpcCfg.RequiresAt, rpcCfg.Description)
	}

	tokenExtractor := google.NewGoogleTokenExtractorAdapter(cfg.Server.ShortTimeout())

	masterKey := session.ResolveMasterKey(cfg.Security.MasterKey)
	vault := session.NewVault(masterKey)

	dbPath := cfg.Storage.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join("storage", "gateway.db")
	}

	refresher := google.NewDerivedSecretRefresher(tokenExtractor)
	sqliteRepo, err := session.NewSqliteSessionRepository(dbPath, refresher, vault)
	if err != nil {
		return nil, fmt.Errorf("không thể mở SQLite session repository: %w", err)
	}

	keyRepo, err := session.NewSqliteKeyRepository(sqliteRepo.DB())
	if err != nil {
		_ = sqliteRepo.Close()
		return nil, fmt.Errorf("không thể khởi tạo SqliteKeyRepository: %w", err)
	}
	masterAdminKey := cfg.Server.APIKey
	if masterAdminKey == "" && cfg.Admin.IsEnabled() {
		masterAdminKey = cfg.Admin.GetSessionToken()
	}
	keyService := services.NewKeyService(keyRepo, masterAdminKey)

	metrics := domain.NewContractMetrics()
	upstreamTransport := google.NewGoogleTransportAdapter(cfg)
	flowClient := google.NewFlowClientAdapter(upstreamTransport, rpcRegistry, metrics)

	profileManager, err := session.NewProfileManager(cfg, sqliteRepo, modelRegistry, tokenExtractor, flowClient, vault)
	if err != nil {
		_ = sqliteRepo.Close()
		return nil, fmt.Errorf("lỗi khởi tạo ProfileManager: %w", err)
	}
	_, _ = profileManager.ScanAndDiscover(context.Background())

	responseCache := services.NewResponseCache(cfg.Cache)

	wire := google.NewWireAdapter(rpcRegistry)
	chatService := services.NewChatService(modelRegistry, sqliteRepo, upstreamTransport, wire, metrics)
	flowService := services.NewMediaService(modelRegistry, sqliteRepo, upstreamTransport, wire, flowClient, profileManager, metrics)
	flowCreditService := services.NewFlowCreditService(sqliteRepo, flowClient, metrics)
	geminiHistoryService := services.NewGeminiHistoryService(sqliteRepo, upstreamTransport, rpcRegistry, metrics)
	geminiQuotaService := services.NewGeminiQuotaService(sqliteRepo, upstreamTransport, rpcRegistry)

	return &DirectServices{
		Config:               cfg,
		SessionRepo:          sqliteRepo,
		Vault:                vault,
		ProfileManager:       profileManager,
		KeyService:           keyService,
		ModelRegistry:        modelRegistry,
		ResponseCache:        responseCache,
		ChatService:          chatService,
		FlowService:          flowService,
		FlowCreditService:    flowCreditService,
		GeminiHistoryService: geminiHistoryService,
		GeminiQuotaService:   geminiQuotaService,
		sqliteRepo:           sqliteRepo,
	}, nil
}

// Close gracefully closes database connections and associated resources.
func (d *DirectServices) Close() error {
	if d == nil {
		return nil
	}
	if d.sqliteRepo != nil {
		return d.sqliteRepo.Close()
	}
	if closer, ok := d.SessionRepo.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
