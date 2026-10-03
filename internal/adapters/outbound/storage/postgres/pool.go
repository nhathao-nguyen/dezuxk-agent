package postgres

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/config"
)

// NewPool khởi tạo kết nối connection pool chuẩn production tới PostgreSQL
func NewPool(ctx context.Context, cfg config.PostgresConfig) (*pgxpool.Pool, error) {
	dsn := cfg.BuildDSN()
	if dsn == "" {
		return nil, fmt.Errorf("cấu hình DSN PostgreSQL không hợp lệ hoặc thiếu thông tin kết nối")
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("lỗi phân tích cấu hình pgxpool DSN: %w", err)
	}

	poolConfig.MaxConns = cfg.GetMaxConns()
	poolConfig.MinConns = cfg.GetMinConns()
	poolConfig.MaxConnLifetime = cfg.GetMaxConnLifetime()
	poolConfig.MaxConnIdleTime = cfg.GetMaxConnIdleTime()
	poolConfig.HealthCheckPeriod = cfg.GetHealthCheckPeriod()
	if cfg.GetConnectTimeout() > 0 {
		poolConfig.ConnConfig.ConnectTimeout = cfg.GetConnectTimeout()
	}

	connectCtx, cancel := context.WithTimeout(ctx, cfg.GetConnectTimeout()+5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("không thể kết nối tới cụm PostgreSQL (%s): %w", cfg.SanitizedDSN(), err)
	}

	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping kiểm tra sẵn sàng PostgreSQL thất bại (%s): %w", cfg.SanitizedDSN(), err)
	}

	log.Printf("[PostgreSQL] Đã kết nối thành công tới %s (Pool: %d-%d conns, max lifetime: %v)",
		cfg.SanitizedDSN(), poolConfig.MinConns, poolConfig.MaxConns, poolConfig.MaxConnLifetime)

	return pool, nil
}
