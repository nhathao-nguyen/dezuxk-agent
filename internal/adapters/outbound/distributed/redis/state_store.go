package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"dezuxk-gateway/internal/core/ports"
)

// RedisSharedStateStore cung cấp lưu trữ key-value phân tán trên cụm Redis
type RedisSharedStateStore struct {
	client redis.UniversalClient
	prefix string
}

var _ ports.SharedStateStore = (*RedisSharedStateStore)(nil)

// NewRedisSharedStateStore tạo mới RedisSharedStateStore
func NewRedisSharedStateStore(client redis.UniversalClient, prefix ...string) *RedisSharedStateStore {
	p := "state:"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	return &RedisSharedStateStore{
		client: client,
		prefix: p,
	}
}

func (s *RedisSharedStateStore) formatKey(key string) string {
	return s.prefix + key
}

// Get đọc dữ liệu theo key
func (s *RedisSharedStateStore) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := s.client.Get(ctx, s.formatKey(key)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("lỗi đọc shared state (%s): %w", key, err)
	}
	return val, nil
}

// Set ghi dữ liệu kèm thời gian sống (TTL)
func (s *RedisSharedStateStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	err := s.client.Set(ctx, s.formatKey(key), val, ttl).Err()
	if err != nil {
		return fmt.Errorf("lỗi ghi shared state (%s): %w", key, err)
	}
	return nil
}

// Delete xóa dữ liệu theo key
func (s *RedisSharedStateStore) Delete(ctx context.Context, key string) error {
	err := s.client.Del(ctx, s.formatKey(key)).Err()
	if err != nil {
		return fmt.Errorf("lỗi xóa shared state (%s): %w", key, err)
	}
	return nil
}
