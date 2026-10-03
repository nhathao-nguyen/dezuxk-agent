package ports

import (
	"context"
	"time"
)

// LockHandle đại diện cho quyền sở hữu khóa đã chiếm được, chứa token bí mật để giải phóng an toàn
type LockHandle interface {
	Release(ctx context.Context) error
	Renew(ctx context.Context, ttl time.Duration) error
}

// DistributedLocker giao diện phân phối khóa an toàn giữa nhiều node gateway
type DistributedLocker interface {
	AcquireLock(ctx context.Context, key string, ttl time.Duration) (LockHandle, bool, error)
}

// SharedRateLimiter giao diện kiểm soát tần suất dùng chung cho toàn bộ cụm cluster
type SharedRateLimiter interface {
	RateLimiter
	Ping(ctx context.Context) error
}

// EventBus giao diện xuất bản và đăng ký nhận sự kiện phân tán giữa các tiến trình
type EventBus interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Subscribe(ctx context.Context, topic string) (events <-chan []byte, unsubscribe func(), err error)
}

// SharedStateStore giao diện lưu trữ key-value phân tán giữa nhiều node gateway
type SharedStateStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// DistributedClusterClient giao diện kiểm tra sức khỏe và quản lý vòng đời kết nối cụm phân tán
type DistributedClusterClient interface {
	Ping(ctx context.Context) error
	Close() error
}
