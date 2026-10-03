package redis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"dezuxk-gateway/internal/core/ports"
)

// RedisEventBus chuyển tiếp sự kiện phân tán giữa các node gateway qua Redis Pub/Sub
type RedisEventBus struct {
	client redis.UniversalClient
}

var _ ports.EventBus = (*RedisEventBus)(nil)

// NewRedisEventBus tạo mới RedisEventBus
func NewRedisEventBus(client redis.UniversalClient) *RedisEventBus {
	return &RedisEventBus{client: client}
}

// Publish xuất bản dữ liệu sự kiện lên topic xác định
func (b *RedisEventBus) Publish(ctx context.Context, topic string, payload []byte) error {
	if topic == "" {
		return fmt.Errorf("topic không được để trống")
	}
	return b.client.Publish(ctx, topic, payload).Err()
}

// Subscribe đăng ký lắng nghe sự kiện từ topic xác định
func (b *RedisEventBus) Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error) {
	if topic == "" {
		return nil, nil, fmt.Errorf("topic không được để trống")
	}

	pubsub := b.client.Subscribe(ctx, topic)

	// Đảm bảo subscription đã được thiết lập thành công
	_, err := pubsub.ReceiveTimeout(ctx, 3*time.Second)
	if err != nil {
		_ = pubsub.Close()
		return nil, nil, fmt.Errorf("lỗi khởi tạo subscription redis cho topic %s: %w", topic, err)
	}

	out := make(chan []byte, 128)
	var once sync.Once
	done := make(chan struct{})

	unsubscribe := func() {
		once.Do(func() {
			close(done)
			_ = pubsub.Close()
		})
	}

	go func() {
		defer close(out)
		ch := pubsub.Channel()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if msg == nil {
					continue
				}
				payload := []byte(msg.Payload)
				select {
				case out <- payload:
				case <-done:
					return
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, unsubscribe, nil
}
