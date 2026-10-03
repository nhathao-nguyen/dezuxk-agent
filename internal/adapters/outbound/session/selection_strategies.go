package session

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

var ErrNoCandidates = errors.New("không có tài khoản nào khả dụng để lựa chọn")

// WeightedHealthScoreStrategy chọn tài khoản dựa trên điểm sức khỏe, số request in-flight và độ trễ
type WeightedHealthScoreStrategy struct{}

func NewWeightedHealthScoreStrategy() *WeightedHealthScoreStrategy {
	return &WeightedHealthScoreStrategy{}
}

func (s *WeightedHealthScoreStrategy) Name() string {
	return "weighted_health_score"
}

func (s *WeightedHealthScoreStrategy) Select(ctx context.Context, candidates []*domain.ManagedAccount) (*domain.ManagedAccount, error) {
	if len(candidates) == 0 {
		return nil, ErrNoCandidates
	}

	var bestAcc *domain.ManagedAccount
	var maxScore = -1e9

	for _, acc := range candidates {
		if acc == nil {
			continue
		}
		inFlight := acc.InFlightReqs
		health := acc.GetHealthScore()

		// Điểm số: HealthScore [0..1] trừ đi phạt in-flight (0.25 mỗi request) và độ trễ
		score := health - (0.25 * float64(inFlight))
		if acc.AvgLatencyMs > 0 {
			// Phạt nhẹ độ trễ cao: 1000ms giảm 0.05 điểm
			score -= math.Min(0.2, (acc.AvgLatencyMs/1000.0)*0.05)
		}

		if score > maxScore {
			maxScore = score
			bestAcc = acc
		} else if score == maxScore && bestAcc != nil && inFlight < bestAcc.InFlightReqs {
			bestAcc = acc
		}
	}

	if bestAcc == nil {
		return nil, ErrNoCandidates
	}
	return bestAcc, nil
}

// RoundRobinStrategy chọn tài khoản lần lượt theo vòng tròn
type RoundRobinStrategy struct {
	counter uint64
}

func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{}
}

func (s *RoundRobinStrategy) Name() string {
	return "round_robin"
}

func (s *RoundRobinStrategy) Select(ctx context.Context, candidates []*domain.ManagedAccount) (*domain.ManagedAccount, error) {
	n := len(candidates)
	if n == 0 {
		return nil, ErrNoCandidates
	}
	idx := atomic.AddUint64(&s.counter, 1) - 1
	return candidates[idx%uint64(n)], nil
}

// LeastFailuresStrategy ưu tiên tài khoản có số lần lỗi liên tiếp và tổng lỗi thấp nhất
type LeastFailuresStrategy struct{}

func NewLeastFailuresStrategy() *LeastFailuresStrategy {
	return &LeastFailuresStrategy{}
}

func (s *LeastFailuresStrategy) Name() string {
	return "least_failures"
}

func (s *LeastFailuresStrategy) Select(ctx context.Context, candidates []*domain.ManagedAccount) (*domain.ManagedAccount, error) {
	if len(candidates) == 0 {
		return nil, ErrNoCandidates
	}

	var bestAcc *domain.ManagedAccount
	minConsecutive := math.MaxInt32
	var minTotalFailures int64 = math.MaxInt64

	for _, acc := range candidates {
		if acc == nil {
			continue
		}
		if acc.ConsecutiveFailures < minConsecutive {
			minConsecutive = acc.ConsecutiveFailures
			minTotalFailures = acc.FailureCount
			bestAcc = acc
		} else if acc.ConsecutiveFailures == minConsecutive {
			if acc.FailureCount < minTotalFailures {
				minTotalFailures = acc.FailureCount
				bestAcc = acc
			} else if acc.FailureCount == minTotalFailures && bestAcc != nil && acc.InFlightReqs < bestAcc.InFlightReqs {
				bestAcc = acc
			}
		}
	}

	if bestAcc == nil {
		return nil, ErrNoCandidates
	}
	return bestAcc, nil
}

// LeastLatencyStrategy ưu tiên tài khoản có thời gian phản hồi trung bình thấp nhất
type LeastLatencyStrategy struct {
	mu sync.Mutex
}

func NewLeastLatencyStrategy() *LeastLatencyStrategy {
	return &LeastLatencyStrategy{}
}

func (s *LeastLatencyStrategy) Name() string {
	return "least_latency"
}

func (s *LeastLatencyStrategy) Select(ctx context.Context, candidates []*domain.ManagedAccount) (*domain.ManagedAccount, error) {
	if len(candidates) == 0 {
		return nil, ErrNoCandidates
	}

	var bestAcc *domain.ManagedAccount
	minLatency := math.MaxFloat64

	// Ưu tiên tài khoản đã có latency đo đạc
	for _, acc := range candidates {
		if acc == nil {
			continue
		}
		lat := acc.AvgLatencyMs
		if lat <= 0 {
			// Nếu chưa đo, gán giá trị mặc định dựa theo in-flight
			lat = 500.0 + float64(acc.InFlightReqs)*200.0
		} else {
			// Thêm penalty cho in-flight
			lat += float64(acc.InFlightReqs) * 150.0
		}
		if lat < minLatency {
			minLatency = lat
			bestAcc = acc
		}
	}

	if bestAcc == nil {
		return candidates[0], nil
	}
	return bestAcc, nil
}

var _ ports.AccountSelectionStrategy = (*WeightedHealthScoreStrategy)(nil)
var _ ports.AccountSelectionStrategy = (*RoundRobinStrategy)(nil)
var _ ports.AccountSelectionStrategy = (*LeastFailuresStrategy)(nil)
var _ ports.AccountSelectionStrategy = (*LeastLatencyStrategy)(nil)
