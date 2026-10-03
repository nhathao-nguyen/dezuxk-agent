package google

import (
	"errors"
	"sync"
	"time"
)

type CircuitState string

const (
	StateClosed   CircuitState = "closed"
	StateHalfOpen CircuitState = "half_open"
	StateOpen     CircuitState = "open"
)

var ErrCircuitOpen = errors.New("circuit breaker is open: upstream service is temporarily unavailable")

// CircuitBreakerConfig cấu hình bộ ngắt mạch Circuit Breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           // Số lần lỗi liên tiếp trước khi ngắt mạch (mặc định 5)
	ResetTimeout     time.Duration // Thời gian giữ trạng thái Open trước khi chuyển sang Half-Open (mặc định 30s)
	HalfOpenSuccess  int           // Số lần thành công liên tiếp ở Half-Open để đóng mạch hoàn toàn (mặc định 2)
}

func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		ResetTimeout:     30 * time.Second,
		HalfOpenSuccess:  2,
	}
}

// CircuitBreaker triển khai mô hình bảo vệ ngắt mạch theo 3 trạng thái
type CircuitBreaker struct {
	mu           sync.RWMutex
	cfg          CircuitBreakerConfig
	state        CircuitState
	failures     int
	successes    int
	lastFailure  time.Time
	openedAt     time.Time
	stateChanges int
}

func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenSuccess <= 0 {
		cfg.HalfOpenSuccess = 2
	}
	return &CircuitBreaker{
		cfg:   cfg,
		state: StateClosed,
	}
}

// Allow kiểm tra xem request có được phép thực thi không
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(cb.openedAt) >= cb.cfg.ResetTimeout {
			cb.state = StateHalfOpen
			cb.successes = 0
			cb.failures = 0
			cb.stateChanges++
			return true
		}
		return false
	case StateHalfOpen:
		// Cho phép thăm dò trong half-open
		return true
	default:
		return true
	}
}

// RecordSuccess ghi nhận một lần gọi thành công
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateHalfOpen:
		cb.successes++
		if cb.successes >= cb.cfg.HalfOpenSuccess {
			cb.state = StateClosed
			cb.failures = 0
			cb.successes = 0
			cb.stateChanges++
		}
	case StateClosed:
		cb.failures = 0
	}
}

// RecordFailure ghi nhận một lần gọi thất bại
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()
	switch cb.state {
	case StateClosed:
		cb.failures++
		if cb.failures >= cb.cfg.FailureThreshold {
			cb.state = StateOpen
			cb.openedAt = time.Now()
			cb.stateChanges++
		}
	case StateHalfOpen:
		// Thất bại ngay lập tức khi đang thăm dò -> Mở lại mạch
		cb.state = StateOpen
		cb.openedAt = time.Now()
		cb.failures++
		cb.stateChanges++
	}
}

// State trả về trạng thái hiện tại của Circuit Breaker
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset hoàn trả Circuit Breaker về trạng thái Closed ban đầu
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
}
