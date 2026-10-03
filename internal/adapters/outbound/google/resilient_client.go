package google

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// MaxRetryBodyBytes là kích thước tối đa của request body được đệm trong bộ nhớ để phục vụ retry an toàn (10MB)
const MaxRetryBodyBytes = 10 * 1024 * 1024

// ErrRequestBodyTooLarge báo lỗi khi kích thước request body vượt quá giới hạn bộ đệm retry
var ErrRequestBodyTooLarge = errors.New("request_body_too_large: request body exceeds max retry buffer limit")

// ResilientConfig cấu hình cho ResilientUpstreamClient
type ResilientConfig struct {
	MaxRetries        int           // Số lần retry tối đa (mặc định 3)
	InitialBackoff    time.Duration // Thời gian chờ ban đầu (mặc định 250ms)
	MaxBackoff        time.Duration // Thời gian chờ tối đa (mặc định 2s)
	BackoffMultiplier float64       // Hệ số nhân lũy thừa (mặc định 2.0)
	JitterFraction    float64       // Tỷ lệ jitter ngẫu nhiên [0.0..1.0] (mặc định 0.20)
	RetryBudgetRatio  float64       // Tỷ lệ tối đa các request được phép retry (mặc định 0.25 tức 25%)
	CircuitBreaker    CircuitBreakerConfig
}

func DefaultResilientConfig() ResilientConfig {
	return ResilientConfig{
		MaxRetries:        3,
		InitialBackoff:    250 * time.Millisecond,
		MaxBackoff:        2 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.20,
		RetryBudgetRatio:  0.25,
		CircuitBreaker:    DefaultCircuitBreakerConfig(),
	}
}

// ResilientUpstreamClient bọc UpstreamGoogleTransport với đầy đủ cơ chế resilience:
// Exponential backoff, random jitter, circuit breaker, retry budget, phân loại lỗi và Retry-After.
type ResilientUpstreamClient struct {
	underlying ports.UpstreamGoogleTransport
	cfg        ResilientConfig
	breaker    *CircuitBreaker

	// Thống kê & Retry Budget
	totalRequests  int64
	totalRetries   int64
	totalFailures  int64
	consecutive429 int64
}

// NewResilientUpstreamClient khởi tạo một ResilientUpstreamClient
func NewResilientUpstreamClient(underlying ports.UpstreamGoogleTransport, cfg ...ResilientConfig) *ResilientUpstreamClient {
	c := DefaultResilientConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = 250 * time.Millisecond
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 2 * time.Second
	}
	if c.BackoffMultiplier <= 1.0 {
		c.BackoffMultiplier = 2.0
	}
	if c.JitterFraction <= 0 || c.JitterFraction > 1.0 {
		c.JitterFraction = 0.20
	}
	if c.RetryBudgetRatio <= 0 || c.RetryBudgetRatio > 1.0 {
		c.RetryBudgetRatio = 0.25
	}

	return &ResilientUpstreamClient{
		underlying: underlying,
		cfg:        c,
		breaker:    NewCircuitBreaker(c.CircuitBreaker),
	}
}

var _ ports.UpstreamGoogleTransport = (*ResilientUpstreamClient)(nil)

func (c *ResilientUpstreamClient) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.underlying != nil {
		return c.underlying.BoundShort(ctx)
	}
	return domain.BoundContext(ctx, 20*time.Second)
}

func (c *ResilientUpstreamClient) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.underlying != nil {
		return c.underlying.BoundStream(ctx)
	}
	return domain.BoundContext(ctx, 300*time.Second)
}

// DoRequest thực thi request với đầy đủ bảo vệ resilience
func (c *ResilientUpstreamClient) DoRequest(
	ctx context.Context,
	account *domain.ManagedAccount,
	service domain.ServiceKind,
	method string,
	path string,
	body io.Reader,
	contentType string,
) (*http.Response, error) {
	atomic.AddInt64(&c.totalRequests, 1)

	// 1. Kiểm tra Circuit Breaker trước khi gửi
	if !c.breaker.Allow() {
		return nil, domain.UpstreamUnavailable(domain.OpSession, "circuit_breaker", service, "máy chủ Google tạm thời không khả dụng (Circuit Breaker OPEN)")
	}

	// 2. Buffer lại thân request nếu có để có thể retry an toàn
	var bodyBytes []byte
	if body != nil {
		var readErr error
		// Đọc tối đa MaxRetryBodyBytes + 1 để phát hiện kích thước vượt quá giới hạn, tuyệt đối không truncate ngầm
		bodyBytes, readErr = io.ReadAll(io.LimitReader(body, MaxRetryBodyBytes+1))
		if readErr != nil {
			return nil, fmt.Errorf("read request body error: %w", readErr)
		}
		if len(bodyBytes) > MaxRetryBodyBytes {
			return nil, ErrRequestBodyTooLarge
		}
	}

	maxRetries := c.cfg.MaxRetries
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Kiểm tra context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Chuẩn bị reader cho lượt gọi này
		var attemptBody io.Reader
		if bodyBytes != nil {
			attemptBody = bytes.NewReader(bodyBytes)
		}

		startCall := time.Now()
		resp, err := c.underlying.DoRequest(ctx, account, service, method, path, attemptBody, contentType)
		duration := time.Since(startCall)
		_ = duration

		// A. Trường hợp lỗi kết nối mạng (Network Error / Timeout)
		if err != nil {
			lastErr = err
			c.breaker.RecordFailure()
			atomic.AddInt64(&c.totalFailures, 1)

			if !isRetryableNetworkError(err) || attempt >= maxRetries || !c.canRetryBudget() {
				return nil, err
			}

			// Tính thời gian chờ exponential backoff có jitter
			atomic.AddInt64(&c.totalRetries, 1)
			backoff := c.calculateBackoff(attempt, 0)
			if waitErr := sleepContext(ctx, backoff); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		// B. Nhận được phản hồi HTTP
		if resp != nil {
			// Kiểm tra mã trạng thái HTTP có retry được không
			if isRetryableStatusCode(resp.StatusCode) {
				c.breaker.RecordFailure()
				atomic.AddInt64(&c.totalFailures, 1)
				if resp.StatusCode == http.StatusTooManyRequests {
					atomic.AddInt64(&c.consecutive429, 1)
				}

				if attempt < maxRetries && c.canRetryBudget() {
					// Đọc header Retry-After nếu có
					retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))

					// Đóng body của lượt gọi lỗi trước khi thử lại
					if resp.Body != nil {
						_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
						_ = resp.Body.Close()
					}

					atomic.AddInt64(&c.totalRetries, 1)
					backoff := c.calculateBackoff(attempt, retryAfter)
					if waitErr := sleepContext(ctx, backoff); waitErr != nil {
						return nil, waitErr
					}
					continue
				}

				// Hết lượt retry hoặc budget không cho phép: trả lại response để caller xử lý (hoặc failover account)
				return resp, nil
			}

			// Phản hồi thành công (2xx..3xx) hoặc lỗi client (4xx non-retryable)
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				c.breaker.RecordSuccess()
				atomic.StoreInt64(&c.consecutive429, 0)
			}
			return resp, nil
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// canRetryBudget kiểm tra xem tỷ lệ retry hiện tại có vượt quá ngân sách cho phép không
func (c *ResilientUpstreamClient) canRetryBudget() bool {
	reqs := atomic.LoadInt64(&c.totalRequests)
	if reqs <= 5 {
		// Trong 5 request đầu tiên cho phép retry bình thường
		return true
	}
	retries := atomic.LoadInt64(&c.totalRetries)
	ratio := float64(retries) / float64(reqs)
	return ratio < c.cfg.RetryBudgetRatio
}

// calculateBackoff tính toán thời gian chờ lũy thừa kèm jitter và tôn trọng Retry-After
func (c *ResilientUpstreamClient) calculateBackoff(attempt int, retryAfter time.Duration) time.Duration {
	base := float64(c.cfg.InitialBackoff) * math.Pow(c.cfg.BackoffMultiplier, float64(attempt))
	if base > float64(c.cfg.MaxBackoff) {
		base = float64(c.cfg.MaxBackoff)
	}

	// Thêm jitter ngẫu nhiên trong khoảng [1 - JitterFraction, 1 + JitterFraction]
	jitter := 1.0 - c.cfg.JitterFraction + (rand.Float64() * 2.0 * c.cfg.JitterFraction)
	computed := time.Duration(base * jitter)

	// Nếu upstream trả về Retry-After cụ thể và lớn hơn, tôn trọng Retry-After
	if retryAfter > computed {
		// Không chờ quá 15 giây để tránh treo request của client
		if retryAfter > 15*time.Second {
			return 15 * time.Second
		}
		return retryAfter
	}
	return computed
}

// sleepContext dừng chờ an toàn với context cancellation
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// isRetryableStatusCode phân loại mã HTTP có thể retry
func isRetryableStatusCode(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// isRetryableNetworkError phân loại lỗi mạng có thể retry
func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "stream error") ||
		strings.Contains(errStr, "temporary failure in name resolution") ||
		strings.Contains(errStr, "tls handshake timeout") ||
		strings.Contains(errStr, "server closed") ||
		strings.Contains(errStr, "unexpected eof")
}

// parseRetryAfter chuyển đổi header Retry-After sang time.Duration
func parseRetryAfter(headerVal string) time.Duration {
	headerVal = strings.TrimSpace(headerVal)
	if headerVal == "" {
		return 0
	}
	// Dạng số giây: e.g. "5"
	if secs, err := strconv.Atoi(headerVal); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// Dạng HTTP Date: e.g. "Fri, 31 Dec 2026 23:59:59 GMT"
	if t, err := http.ParseTime(headerVal); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// CircuitState trả về trạng thái Circuit Breaker hiện tại
func (c *ResilientUpstreamClient) CircuitState() CircuitState {
	return c.breaker.State()
}

// GetStats trả về thống kê resilience
func (c *ResilientUpstreamClient) GetStats() (requests, retries, failures int64, circuit CircuitState) {
	return atomic.LoadInt64(&c.totalRequests), atomic.LoadInt64(&c.totalRetries), atomic.LoadInt64(&c.totalFailures), c.breaker.State()
}
