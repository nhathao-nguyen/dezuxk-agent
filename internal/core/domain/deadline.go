package domain

import (
	"context"
	"time"
)

// BoundContext giữ hạn chờ không dài hơn deadline của caller.
// limit <= 0 giữ nguyên parent. Parent đã ngắn hơn limit thì không nới thêm.
func BoundContext(parent context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if limit <= 0 {
		return parent, func() {}
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= limit {
		return parent, func() {}
	}
	return context.WithTimeout(parent, limit)
}
