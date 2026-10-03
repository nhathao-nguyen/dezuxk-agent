package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/ports"
)

// RedisClient bọc redis.UniversalClient hỗ trợ Standalone, Sentinel và Cluster
type RedisClient struct {
	client redis.UniversalClient
	cfg    config.RedisConfig
}

var _ ports.DistributedClusterClient = (*RedisClient)(nil)

// NewRedisClient khởi tạo client kết nối Redis dựa theo cấu hình
func NewRedisClient(cfg config.RedisConfig) (*RedisClient, error) {
	addrs := cfg.GetAddrs()
	if len(addrs) == 0 {
		return nil, fmt.Errorf("cấu hình redis thiếu địa chỉ kết nối (addrs hoặc addr)")
	}

	mode := cfg.GetMode()
	var client redis.UniversalClient

	switch mode {
	case "cluster":
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        addrs,
			Username:     cfg.Username,
			Password:     cfg.Password,
			PoolSize:     cfg.GetPoolSize(),
			MinIdleConns: cfg.GetMinIdleConns(),
			DialTimeout:  cfg.GetDialTimeout(),
			ReadTimeout:  cfg.GetReadTimeout(),
			WriteTimeout: cfg.GetWriteTimeout(),
		})
	case "sentinel":
		client = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: addrs,
			Username:      cfg.Username,
			Password:      cfg.Password,
			DB:            cfg.DB,
			PoolSize:      cfg.GetPoolSize(),
			MinIdleConns:  cfg.GetMinIdleConns(),
			DialTimeout:   cfg.GetDialTimeout(),
			ReadTimeout:   cfg.GetReadTimeout(),
			WriteTimeout:  cfg.GetWriteTimeout(),
		})
	default: // standalone
		addr := addrs[0]
		client = redis.NewClient(&redis.Options{
			Addr:         addr,
			Username:     cfg.Username,
			Password:     cfg.Password,
			DB:           cfg.DB,
			PoolSize:     cfg.GetPoolSize(),
			MinIdleConns: cfg.GetMinIdleConns(),
			DialTimeout:  cfg.GetDialTimeout(),
			ReadTimeout:  cfg.GetReadTimeout(),
			WriteTimeout: cfg.GetWriteTimeout(),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.GetDialTimeout())
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("không thể kết nối Redis (%s): %w", addrs[0], err)
	}

	return &RedisClient{
		client: client,
		cfg:    cfg,
	}, nil
}

// UniversalClient trả về client gốc của go-redis
func (r *RedisClient) UniversalClient() redis.UniversalClient {
	return r.client
}

// Ping kiểm tra kết nối với Redis
func (r *RedisClient) Ping(ctx context.Context) error {
	if r.client == nil {
		return fmt.Errorf("redis client chưa được khởi tạo")
	}
	return r.client.Ping(ctx).Err()
}

// Close đóng kết nối Redis an toàn
func (r *RedisClient) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}
