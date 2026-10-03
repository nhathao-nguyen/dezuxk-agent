package ports

import (
	"context"
	"time"
)

// RateLimitIdentity đặc tả các chiều định danh cho Rate Limiting & Concurrency Limiting
type RateLimitIdentity struct {
	IP         string
	TenantID   string
	KeyID      string // Hash SHA-256 hoặc KeyID, tuyệt đối không bao giờ là raw API key
	Endpoint   string
	Model      string
	IsAgentRun bool
}

// RateLimitDecision kết quả phân định cho phép hay từ chối của Rate Limiter
type RateLimitDecision struct {
	Allowed       bool
	RetryAfterSec int
	Remaining     int
	Limit         int
	ResetAt       time.Time
	Reason        string
}

// RateLimiter giao diện kiểm soát tần suất và độ đồng thời chuẩn cho Gateway
type RateLimiter interface {
	// Allow kiểm tra tần suất (RPM / Window)
	Allow(ctx context.Context, id RateLimitIdentity) (RateLimitDecision, error)
	// AcquireConcurrency chiếm giữ 1 slot đồng thời, trả về hàm release khi hoàn tất
	AcquireConcurrency(ctx context.Context, id RateLimitIdentity) (release func(), allowed bool, err error)
}

// RedisRateLimiter giao diện mở rộng chuẩn bị sẵn cho Redis / Distributed Rate Limiter
type RedisRateLimiter interface {
	RateLimiter
	Ping(ctx context.Context) error
}
