package ports

import (
	"context"
	"time"
)

// DistributedLocker giao diện phân phối khóa an toàn giữa nhiều node gateway
type DistributedLocker interface {
	AcquireLock(ctx context.Context, key string, ttl time.Duration) (release func() error, acquired bool, err error)
}

// SharedRateLimiter giao diện kiểm soát tần suất dùng chung cho toàn bộ cụm cluster
type SharedRateLimiter interface {
	RateLimiter
}

// LeaseManager giao diện quản lý lease và fencing token cho worker nodes trong môi trường phân tán
type LeaseManager interface {
	AcquireLease(ctx context.Context, resourceID, workerID string, ttl time.Duration) (generation int64, acquired bool, err error)
	RenewLease(ctx context.Context, resourceID, workerID string, generation int64, ttl time.Duration) (renewed bool, err error)
	ReleaseLease(ctx context.Context, resourceID, workerID string, generation int64) error
}

// EventBus giao diện xuất bản và đăng ký nhận sự kiện phân tán giữa các tiến trình
type EventBus interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Subscribe(ctx context.Context, topic string) (events <-chan []byte, unsubscribe func(), err error)
}

// DistributedClusterClient giao diện kiểm tra sức khỏe và quản lý vòng đời kết nối cụm phân tán
type DistributedClusterClient interface {
	Ping(ctx context.Context) error
	Close() error
}
