package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/adapters/outbound/alerts"
	redisadapter "dezuxk-gateway/internal/adapters/outbound/distributed/redis"
	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/storage"
	pgstorage "dezuxk-gateway/internal/adapters/outbound/storage/postgres"
	s3storage "dezuxk-gateway/internal/adapters/outbound/storage/s3"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services/leader"
)

// Infrastructure gói hợp nhất toàn bộ tài nguyên hạ tầng (Persistence, Distributed, Media, Singleton Coordinator)
type Infrastructure struct {
	NodeID          string
	Sessions        ports.SessionRepository
	Keys            ports.KeyRepository
	Runs            ports.AgentRunRepository
	Checkpoints     ports.CheckpointRepository
	Memory          ports.MemoryRepository
	SharedLimiter   ports.SharedRateLimiter
	EventBus        ports.EventBus
	Locker          ports.DistributedLocker
	Media           ports.MediaRepository
	LeaderCoord     *leader.Coordinator
	ClusterClient   ports.DistributedClusterClient
	PostgresPool    *pgxpool.Pool
	SqliteDB        *sql.DB
	SqliteRepo      *session.SqliteSessionRepository
	AlertDispatcher ports.AlertDispatcher
	ModelCatalog    ports.ModelCatalogRepository
	TenantSettings  ports.TenantRuntimeSettingsRepository
}

// Close giải phóng tài nguyên hạ tầng theo đúng thứ tự an toàn
func (inf *Infrastructure) Close() error {
	if inf.LeaderCoord != nil {
		inf.LeaderCoord.Stop()
	}
	if inf.ClusterClient != nil {
		_ = inf.ClusterClient.Close()
	}
	if inf.PostgresPool != nil {
		inf.PostgresPool.Close()
	}
	if inf.SqliteRepo != nil {
		_ = inf.SqliteRepo.Close()
	}
	if inf.AlertDispatcher != nil {
		_ = inf.AlertDispatcher.Close()
	}
	return nil
}

// ResolveNodeID xác định định danh node ổn định (node_id từ config hoặc <hostname>-<random>)
func ResolveNodeID(cfg *config.Config) string {
	if cfg != nil && cfg.Cluster.NodeID != "" {
		return strings.TrimSpace(cfg.Cluster.NodeID)
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "gateway"
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", hostname, hex.EncodeToString(b))
}

// BuildInfrastructure khởi tạo toàn bộ hạ tầng dựa theo cấu hình (hỗ trợ cả Single-Node và Multi-Node Cluster)
func BuildInfrastructure(ctx context.Context, cfg *config.Config, tokenExtractor ports.TokenExtractor) (*Infrastructure, error) {
	nodeID := ResolveNodeID(cfg)
	log.Printf("[Cluster] Node Identity: %s (Cluster Enabled: %v)", nodeID, cfg.Cluster.IsEnabled())

	masterKey := session.ResolveMasterKey(cfg.Security.MasterKey)
	if cfg.IsProduction() && (strings.TrimSpace(masterKey) == "" || masterKey == "CHANGE_ME") {
		return nil, errors.New("security.master_key bắt buộc phải được cấu hình hợp lệ trong môi trường production")
	}
	vault := session.NewVault(masterKey)
	log.Printf("[Security Vault] Đã khởi tạo AES-256-GCM Vault (vault_key_fingerprint=%s)", vault.Fingerprint())
	refresher := google.NewDerivedSecretRefresher(tokenExtractor)

	infra := &Infrastructure{
		NodeID: nodeID,
	}

	// 1. Khởi tạo Webhook Alert Dispatcher
	infra.AlertDispatcher = alerts.NewWebhookAlertDispatcher(cfg.Alerts.Webhook)
	if cfg.Alerts.Webhook.IsEnabled() {
		log.Printf("[Alerts] Webhook Alert Dispatcher đã kích hoạt (Provider: %s)", cfg.Alerts.Webhook.GetProvider())
	}

	// 2. Khởi tạo Storage Layer (PostgreSQL hoặc SQLite)
	storageDriver := strings.ToLower(strings.TrimSpace(cfg.Storage.Driver))
	var metaRepo *pgstorage.PostgresMediaMetadataRepository

	if storageDriver == "postgres" {
		log.Println("[Persistence] Đang kết nối tới PostgreSQL Pool...")
		pool, err := pgstorage.NewPool(ctx, cfg.Storage.Postgres)
		if err != nil {
			return nil, fmt.Errorf("không thể kết nối tới PostgreSQL trong multi-node production (fail closed): %w", err)
		}
		infra.PostgresPool = pool

		// Chạy migrations với advisory lock an toàn
		log.Println("[Persistence] Đang kiểm tra và áp dụng PostgreSQL schema migrations...")
		if err := pgstorage.RunMigrations(ctx, pool); err != nil {
			pool.Close()
			return nil, fmt.Errorf("lỗi thực thi PostgreSQL migrations: %w", err)
		}
		log.Println("[Persistence] PostgreSQL schema migrations hoàn tất thành công.")

		// Session Repository
		pgSessionRepo, err := pgstorage.NewPostgresSessionRepository(ctx, pool, refresher, vault)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("khởi tạo PostgresSessionRepository thất bại: %w", err)
		}
		infra.Sessions = pgSessionRepo

		// Key Repository
		kr, err := pgstorage.NewPostgresKeyRepository(pool)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("khởi tạo PostgresKeyRepository thất bại: %w", err)
		}
		infra.Keys = kr

		// Agent Run Repository & Tool Execution Ledger
		ar, err := pgstorage.NewPostgresAgentRunRepository(pool)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("khởi tạo PostgresAgentRunRepository thất bại: %w", err)
		}
		infra.Runs = ar

		// Checkpoint Repository
		cp, err := pgstorage.NewPostgresCheckpointRepository(pool)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("khởi tạo PostgresCheckpointRepository thất bại: %w", err)
		}
		infra.Checkpoints = cp

		// Memory Repository (Hybrid tsvector)
		mem, err := pgstorage.NewPostgresMemoryRepository(pool)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("khởi tạo PostgresMemoryRepository thất bại: %w", err)
		}
		infra.Memory = mem

		// Media Metadata Repository
		metaRepo, err = pgstorage.NewPostgresMediaMetadataRepository(pool)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("khởi tạo PostgresMediaMetadataRepository thất bại: %w", err)
		}

		infra.ModelCatalog = pgstorage.NewPostgresModelCatalogRepository(pool)
		infra.TenantSettings = pgstorage.NewPostgresTenantRuntimeSettingsRepository(pool)

		log.Printf("[Persistence] Đã kích hoạt PostgreSQL Adapter thật sự tại %s/%s (MinConns=%d, MaxConns=%d)",
			cfg.Storage.Postgres.Host, cfg.Storage.Postgres.DBName,
			cfg.Storage.Postgres.GetMinConns(), cfg.Storage.Postgres.GetMaxConns())

	} else {
		// SQLite Mode (Single-node production / local dev)
		dbPath := cfg.Storage.DatabasePath
		if dbPath == "" {
			dbPath = filepath.Join("storage", "gateway.db")
		}

		sqliteRepo, err := session.NewSqliteSessionRepository(dbPath, refresher, vault)
		if err != nil {
			if cfg.IsProduction() || !cfg.Storage.AllowMemoryFallback {
				return nil, fmt.Errorf("không thể khởi tạo SqliteSessionRepository: %w", err)
			}
			log.Printf("[Database Warning] Không thể mở SQLite (%v), dùng bộ nhớ RAM (Development Mode)", err)
			infra.Sessions = session.NewMemorySessionRepository(refresher)
		} else {
			infra.SqliteRepo = sqliteRepo
			infra.SqliteDB = sqliteRepo.DB()
			infra.Sessions = sqliteRepo
			log.Printf("[Database] Đã kích hoạt SQLite tại %s (WAL Mode, Vault AES-256-GCM)", dbPath)
		}

		if infra.SqliteDB != nil {
			kr, err := session.NewSqliteKeyRepository(infra.SqliteDB)
			if err != nil {
				return nil, fmt.Errorf("khởi tạo SqliteKeyRepository thất bại: %w", err)
			}
			infra.Keys = kr

			cp, err := session.NewSqliteCheckpointRepository(infra.SqliteDB)
			if err != nil {
				return nil, fmt.Errorf("khởi tạo SqliteCheckpointRepository thất bại: %w", err)
			}
			infra.Checkpoints = cp

			mem, err := session.NewSqliteMemoryRepository(infra.SqliteDB)
			if err != nil {
				return nil, fmt.Errorf("khởi tạo SqliteMemoryRepository thất bại: %w", err)
			}
			infra.Memory = mem

			runRepo, err := session.NewSqliteAgentRunRepository(infra.SqliteDB)
			if err != nil {
				return nil, fmt.Errorf("khởi tạo SqliteAgentRunRepository thất bại: %w", err)
			}
			infra.Runs = runRepo
		} else {
			infra.Keys = session.NewMemoryKeyRepository()
			infra.Checkpoints = session.NewMemoryCheckpointRepository()
			infra.Memory = session.NewMemoryMemoryRepository()
			infra.Runs = session.NewMemoryAgentRunRepository()
		}
	}

	if infra.ModelCatalog == nil {
		infra.ModelCatalog = session.NewMemoryModelCatalogRepository()
	}
	if infra.TenantSettings == nil {
		infra.TenantSettings = session.NewMemoryTenantRuntimeSettingsRepository()
	}

	if sa, ok := infra.Sessions.(ports.SessionAlertNotifier); ok && infra.AlertDispatcher != nil {
		sa.SetAlertDispatcher(infra.AlertDispatcher)
	}

	// 3. Khởi tạo Distributed Layer (Redis)
	if cfg.Distributed.Enabled {
		log.Println("[Distributed] Đang khởi tạo kết nối Redis Cluster/Standalone...")
		redisClient, err := redisadapter.NewRedisClient(cfg.Distributed.Redis)
		if err != nil {
			_ = infra.Close()
			return nil, fmt.Errorf("không thể kết nối Redis trong multi-node cluster (fail closed): %w", err)
		}
		infra.ClusterClient = redisClient

		rdb := redisClient.UniversalClient()
		infra.Locker = redisadapter.NewRedisDistributedLocker(rdb)

		rateLimitCfg := cfg.Server.RateLimit
		maxReqs := rateLimitCfg.MaxRequests
		if maxReqs <= 0 {
			maxReqs = 120
		}
		windowSecs := rateLimitCfg.WindowSeconds
		if windowSecs <= 0 {
			windowSecs = 60
		}
		infra.SharedLimiter = redisadapter.NewRedisSharedRateLimiter(rdb, maxReqs, time.Duration(windowSecs)*time.Second)
		infra.EventBus = redisadapter.NewRedisEventBus(rdb)

		log.Printf("[Distributed] Đã kích hoạt Redis Adapter (%s, Mode: %s, Pool: %d)",
			cfg.Distributed.Redis.GetAddrs()[0], cfg.Distributed.Redis.GetMode(), cfg.Distributed.Redis.GetPoolSize())
	}

	// 4. Khởi tạo Media Storage Layer (S3/MinIO hoặc Local Disk)
	mediaDriver := cfg.Media.GetDriver()
	if mediaDriver == "s3" {
		if metaRepo == nil {
			_ = infra.Close()
			return nil, errors.New("cấu hình media driver là 's3' yêu cầu storage driver 'postgres' để quản lý siêu dữ liệu đa node")
		}
		log.Printf("[Media] Đang khởi tạo S3/MinIO Media Adapter (Bucket: %s, Endpoint: %s)...", cfg.Media.S3.Bucket, cfg.Media.S3.Endpoint)
		s3Adapter, err := s3storage.NewS3StorageAdapter(cfg.Media.S3, cfg.Media.BaseURL, metaRepo)
		if err != nil {
			_ = infra.Close()
			return nil, fmt.Errorf("khởi tạo S3StorageAdapter thất bại: %w", err)
		}
		if cfg.IsProduction() {
			if err := s3Adapter.Ping(ctx); err != nil {
				_ = infra.Close()
				return nil, fmt.Errorf("không thể kết nối S3/MinIO bucket (%s): %w", cfg.Media.S3.Bucket, err)
			}
		} else {
			for i := 0; i < 5; i++ {
				if err := s3Adapter.EnsureBucketExists(ctx); err == nil {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		infra.Media = s3Adapter
		log.Println("[Media] Đã kích hoạt S3/MinIO Shared Media Adapter.")
	} else {
		localAdapter, err := storage.NewLocalStorageAdapter(cfg.Media.StorageDir, cfg.Media.BaseURL)
		if err != nil {
			log.Printf("[Media Warning] Không thể khởi tạo LocalStorageAdapter: %v", err)
		} else {
			infra.Media = localAdapter
			log.Printf("[Media] Đã kích hoạt Local Storage Media Adapter tại %s", cfg.Media.StorageDir)
		}
	}

	// 5. Khởi tạo Leader Election Coordinator
	infra.LeaderCoord = leader.NewCoordinator(infra.Locker, nodeID, 10*time.Second)

	return infra, nil
}
