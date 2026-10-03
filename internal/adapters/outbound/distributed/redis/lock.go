package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"dezuxk-gateway/internal/core/ports"
)

// luaReleaseLock script giải phóng khóa nguyên tử chỉ khi giá trị token khớp với người sở hữu
const luaReleaseLock = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`

// luaRenewLock script gia hạn khóa nguyên tử chỉ khi giá trị token khớp với người sở hữu
const luaRenewLock = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    local ms = tonumber(ARGV[2])
    return redis.call("pexpire", KEYS[1], ms)
else
    return 0
end
`

// RedisDistributedLocker phân phối khóa an toàn giữa nhiều node gateway
type RedisDistributedLocker struct {
	client redis.UniversalClient
}

var _ ports.DistributedLocker = (*RedisDistributedLocker)(nil)

// NewRedisDistributedLocker tạo mới RedisDistributedLocker
func NewRedisDistributedLocker(client redis.UniversalClient) *RedisDistributedLocker {
	return &RedisDistributedLocker{client: client}
}

// redisLockHandle giữ token sở hữu khóa
type redisLockHandle struct {
	client redis.UniversalClient
	key    string
	token  string
}

var _ ports.LockHandle = (*redisLockHandle)(nil)

// Release giải phóng khóa nguyên tử bằng Lua script
func (h *redisLockHandle) Release(ctx context.Context) error {
	res, err := h.client.Eval(ctx, luaReleaseLock, []string{h.key}, h.token).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return fmt.Errorf("lỗi khi giải phóng lock (%s): %w", h.key, err)
	}
	// res = 1 nghĩa là đã xóa khóa, 0 nghĩa là khóa đã hết hạn hoặc bị node khác chiếm
	_ = res
	return nil
}

// Renew gia hạn thời gian sống (TTL) của khóa một cách an toàn
func (h *redisLockHandle) Renew(ctx context.Context, ttl time.Duration) error {
	ttlMs := int64(ttl / time.Millisecond)
	res, err := h.client.Eval(ctx, luaRenewLock, []string{h.key}, h.token, ttlMs).Int64()
	if err != nil {
		return fmt.Errorf("lỗi khi gia hạn lock (%s): %w", h.key, err)
	}
	if res == 0 {
		return errors.New("không thể gia hạn: lock đã hết hạn hoặc bị tiến trình khác chiếm")
	}
	return nil
}

// AcquireLock cố gắng chiếm khóa với thời gian sống (TTL)
func (l *RedisDistributedLocker) AcquireLock(ctx context.Context, key string, ttl time.Duration) (ports.LockHandle, bool, error) {
	if key == "" {
		return nil, false, errors.New("lock key không được để trống")
	}
	if ttl <= 0 {
		ttl = 10 * time.Second
	}

	// Tạo token định danh ngẫu nhiên cho handle này
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, false, fmt.Errorf("lỗi tạo token ngẫu nhiên: %w", err)
	}
	token := hex.EncodeToString(b)
	lockKey := "lock:" + key

	ok, err := l.client.SetNX(ctx, lockKey, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("lỗi redis khi chiếm lock (%s): %w", key, err)
	}
	if !ok {
		return nil, false, nil
	}

	return &redisLockHandle{
		client: l.client,
		key:    lockKey,
		token:  token,
	}, true, nil
}
