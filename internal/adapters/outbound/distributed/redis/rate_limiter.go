package redis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"dezuxk-gateway/internal/core/ports"
)

const luaTokenBucket = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local consume = tonumber(ARGV[4])

local data = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])

if not tokens or not last_refill then
    tokens = limit
    last_refill = now
else
    local elapsed = now - last_refill
    if elapsed >= window then
        tokens = limit
        last_refill = now
    end
end

if tokens >= consume then
    tokens = tokens - consume
    redis.call('HMSET', key, 'tokens', tokens, 'last_refill', last_refill)
    redis.call('PEXPIRE', key, window * 2)
    local resetAfterSec = math.ceil((last_refill + window - now) / 1000)
    if resetAfterSec < 0 then resetAfterSec = 0 end
    return {1, tokens, 0, resetAfterSec}
else
    local elapsed = now - last_refill
    local retryAfter = math.ceil((window - elapsed) / 1000)
    if retryAfter <= 0 then retryAfter = 1 end
    return {0, 0, retryAfter, retryAfter}
end
`

const luaAcquireConcurrency = `
local key = KEYS[1]
local max = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])

local current = redis.call('get', key)
local count = 0
if current then
    count = tonumber(current)
end

if max > 0 and count >= max then
    return 0
end

redis.call('incr', key)
redis.call('expire', key, ttl)
return 1
`

const luaReleaseConcurrency = `
local key = KEYS[1]
local current = redis.call('decr', key)
if current <= 0 then
    redis.call('del', key)
end
return current
`

// RedisSharedRateLimiter kiểm soát tần suất và độ đồng thời dùng chung cụm cluster qua Redis
type RedisSharedRateLimiter struct {
	client        redis.UniversalClient
	defaultRPM    int
	window        time.Duration
	maxConcurrent int
	maxAgentRuns  int
}

var _ ports.SharedRateLimiter = (*RedisSharedRateLimiter)(nil)

// NewRedisSharedRateLimiter tạo mới RedisSharedRateLimiter
func NewRedisSharedRateLimiter(client redis.UniversalClient, rate int, window time.Duration) *RedisSharedRateLimiter {
	if rate <= 0 {
		rate = 120
	}
	if window <= 0 {
		window = 1 * time.Minute
	}
	return &RedisSharedRateLimiter{
		client:        client,
		defaultRPM:    rate,
		window:        window,
		maxConcurrent: 30,
		maxAgentRuns:  5,
	}
}

// SetLimits tùy chỉnh giới hạn đồng thời cho chat và agent runs
func (r *RedisSharedRateLimiter) SetLimits(maxConcurrent, maxAgentRuns int) {
	if maxConcurrent > 0 {
		r.maxConcurrent = maxConcurrent
	}
	if maxAgentRuns > 0 {
		r.maxAgentRuns = maxAgentRuns
	}
}

func (r *RedisSharedRateLimiter) buildBucketKey(id ports.RateLimitIdentity) string {
	tenant := id.TenantID
	if tenant == "" {
		tenant = "default"
	}
	key := id.KeyID
	endpoint := id.Endpoint
	model := id.Model

	if id.IsAgentRun {
		if key == "" {
			key = "default"
		}
		return fmt.Sprintf("ratelimit:tenant:%s:key:%s:agent:run", tenant, key)
	}

	if key != "" {
		if endpoint == "" {
			endpoint = "default"
		}
		if model == "" {
			model = "default"
		}
		return fmt.Sprintf("ratelimit:tenant:%s:key:%s:endpoint:%s:model:%s", tenant, key, endpoint, model)
	}

	if id.IP != "" {
		return "ratelimit:ip:" + id.IP
	}
	return "ratelimit:global:anonymous"
}

// Allow kiểm tra tần suất yêu cầu (Rate Limit) theo danh tính đa chiều
func (r *RedisSharedRateLimiter) Allow(ctx context.Context, id ports.RateLimitIdentity) (ports.RateLimitDecision, error) {
	key := r.buildBucketKey(id)
	now := time.Now()
	nowMs := now.UnixNano() / int64(time.Millisecond)
	windowMs := int64(r.window / time.Millisecond)
	limit := r.defaultRPM

	res, err := r.client.Eval(ctx, luaTokenBucket, []string{key}, limit, windowMs, nowMs, 1).Slice()
	if err != nil {
		return ports.RateLimitDecision{
			Allowed:       false,
			RetryAfterSec: 5,
			Reason:        "redis_rate_limit_error",
		}, fmt.Errorf("lỗi kiểm tra redis rate limit: %w", err)
	}

	allowedInt, _ := res[0].(int64)
	remaining, _ := res[1].(int64)
	retryAfter, _ := res[2].(int64)
	resetAfterSec, _ := res[3].(int64)

	allowed := allowedInt == 1
	resetAt := now.Add(time.Duration(resetAfterSec) * time.Second)

	if !allowed {
		return ports.RateLimitDecision{
			Allowed:       false,
			RetryAfterSec: int(retryAfter),
			Remaining:     0,
			Limit:         limit,
			ResetAt:       resetAt,
			Reason:        "rate_limit_exceeded",
		}, nil
	}

	return ports.RateLimitDecision{
		Allowed:       true,
		RetryAfterSec: 0,
		Remaining:     int(remaining),
		Limit:         limit,
		ResetAt:       resetAt,
	}, nil
}

// AcquireConcurrency kiểm tra và chiếm giữ 1 slot đồng thời trên toàn cụm Redis
func (r *RedisSharedRateLimiter) AcquireConcurrency(ctx context.Context, id ports.RateLimitIdentity) (func(), bool, error) {
	tenant := id.TenantID
	if tenant == "" {
		tenant = "default"
	}
	key := id.KeyID
	if key == "" {
		if id.IP != "" {
			key = id.IP
		} else {
			key = "anonymous"
		}
	}

	var concurrencyKey string
	var max int
	if id.IsAgentRun {
		concurrencyKey = fmt.Sprintf("concurrency:agent:tenant:%s:key:%s", tenant, key)
		max = r.maxAgentRuns
	} else {
		concurrencyKey = fmt.Sprintf("concurrency:chat:tenant:%s:key:%s", tenant, key)
		max = r.maxConcurrent
	}

	safetyTTLSeconds := 300 // 5 phút đề phòng node bị crash
	res, err := r.client.Eval(ctx, luaAcquireConcurrency, []string{concurrencyKey}, max, safetyTTLSeconds).Int64()
	if err != nil {
		return nil, false, fmt.Errorf("lỗi kiểm tra redis concurrency: %w", err)
	}
	if res == 0 {
		return nil, false, nil
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			relCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = r.client.Eval(relCtx, luaReleaseConcurrency, []string{concurrencyKey}).Result()
		})
	}

	return release, true, nil
}

// Ping kiểm tra kết nối với Redis
func (r *RedisSharedRateLimiter) Ping(ctx context.Context) error {
	if r.client == nil {
		return fmt.Errorf("redis client chưa sẵn sàng")
	}
	return r.client.Ping(ctx).Err()
}
