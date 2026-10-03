package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// TrustedProxyChecker kiểm tra xem IP kết nối trực tiếp có thuộc danh sách proxy tin cậy hay không
type TrustedProxyChecker struct {
	trustedIPs  []net.IP
	trustedNets []*net.IPNet
	trustAll    bool
}

// NewTrustedProxyChecker phân tích danh sách IP và CIDR thành bộ kiểm tra proxy tin cậy
func NewTrustedProxyChecker(proxies []string) *TrustedProxyChecker {
	tpc := &TrustedProxyChecker{}
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			tpc.trustAll = true
			continue
		}
		if strings.Contains(p, "/") {
			_, ipNet, err := net.ParseCIDR(p)
			if err == nil {
				tpc.trustedNets = append(tpc.trustedNets, ipNet)
			}
		} else {
			ip := net.ParseIP(p)
			if ip != nil {
				tpc.trustedIPs = append(tpc.trustedIPs, ip)
			}
		}
	}
	return tpc
}

// IsTrusted kiểm tra IP có nằm trong danh sách tin cậy không
func (c *TrustedProxyChecker) IsTrusted(ipStr string) bool {
	if c == nil || (len(c.trustedIPs) == 0 && len(c.trustedNets) == 0 && !c.trustAll) {
		return false
	}
	if c.trustAll {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, tip := range c.trustedIPs {
		if tip.Equal(ip) {
			return true
		}
	}
	for _, tnet := range c.trustedNets {
		if tnet.Contains(ip) {
			return true
		}
	}
	return false
}

// HashKeyID băm cryptographic SHA-256 khóa API để làm định danh, tuyệt đối không lưu hay dùng prefix của raw key
func HashKeyID(rawKey string) string {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return "anonymous"
	}
	h := sha256.Sum256([]byte(rawKey))
	return "key_" + hex.EncodeToString(h[:12])
}

type visitorBucket struct {
	tokens     int
	lastRefill time.Time
}

// LocalRateLimiter triển khai ports.RateLimiter hỗ trợ đầy đủ các chiều:
// IP, tenant_id, key_id, endpoint, model, agent runs và concurrency.
type LocalRateLimiter struct {
	mu             sync.Mutex
	buckets        map[string]*visitorBucket
	inFlight       map[string]int
	defaultRPM     int
	window         time.Duration
	maxConcurrent  int
	maxAgentRuns   int
	cleanupFreq    time.Duration
	trustedChecker *TrustedProxyChecker
}

var _ ports.RateLimiter = (*LocalRateLimiter)(nil)

// NewLocalRateLimiter khởi tạo LocalRateLimiter chuẩn production
func NewLocalRateLimiter(rate int, window time.Duration, trustedProxies ...[]string) *LocalRateLimiter {
	var checker *TrustedProxyChecker
	if len(trustedProxies) > 0 && len(trustedProxies[0]) > 0 {
		checker = NewTrustedProxyChecker(trustedProxies[0])
	}
	if rate <= 0 {
		rate = 120
	}
	if window <= 0 {
		window = 1 * time.Minute
	}

	l := &LocalRateLimiter{
		buckets:        make(map[string]*visitorBucket),
		inFlight:       make(map[string]int),
		defaultRPM:     rate,
		window:         window,
		maxConcurrent:  30,
		maxAgentRuns:   5,
		cleanupFreq:    1 * time.Minute,
		trustedChecker: checker,
	}
	go l.cleanupLoop()
	return l
}

// ExtractClientIP trích xuất IP client thực từ request, bảo vệ chống giả mạo header
func (l *LocalRateLimiter) ExtractClientIP(r *http.Request) string {
	peerIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peerIP = host
	}

	if l.trustedChecker != nil && l.trustedChecker.IsTrusted(peerIP) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			clientIP := strings.TrimSpace(parts[0])
			if net.ParseIP(clientIP) != nil {
				return clientIP
			}
		}
		if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
			if net.ParseIP(xrip) != nil {
				return xrip
			}
		}
	}

	return peerIP
}

func (l *LocalRateLimiter) buildBucketKey(id ports.RateLimitIdentity) string {
	// Post-auth rate limit bằng: tenant_id, key_id, endpoint, model
	// Định dạng canonical key: tenant:{tenant}:key:{key}:endpoint:{endpoint}:model:{model}
	if id.TenantID != "" || id.KeyID != "" {
		tenant := id.TenantID
		if tenant == "" {
			tenant = "default"
		}
		key := id.KeyID
		if key == "" {
			key = "anonymous"
		}
		endpoint := id.Endpoint
		if endpoint == "" {
			endpoint = "default"
		}
		model := id.Model
		if model == "" {
			model = "default"
		}
		return fmt.Sprintf("tenant:%s:key:%s:endpoint:%s:model:%s", tenant, key, endpoint, model)
	}
	// Pre-auth rate limit chỉ bằng client IP
	if id.IP != "" {
		return "ip:" + id.IP
	}
	return "global:anonymous"
}

// Allow kiểm tra tần suất yêu cầu (Rate Limit) theo danh tính đa chiều
func (l *LocalRateLimiter) Allow(ctx context.Context, id ports.RateLimitIdentity) (ports.RateLimitDecision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	key := l.buildBucketKey(id)

	limit := l.defaultRPM
	v, exists := l.buckets[key]
	if !exists {
		l.buckets[key] = &visitorBucket{
			tokens:     limit - 1,
			lastRefill: now,
		}
		return ports.RateLimitDecision{
			Allowed:       true,
			RetryAfterSec: 0,
			Remaining:     limit - 1,
			Limit:         limit,
			ResetAt:       now.Add(l.window),
		}, nil
	}

	elapsed := now.Sub(v.lastRefill)
	if elapsed >= l.window {
		v.tokens = limit
		v.lastRefill = now
		elapsed = 0
	}

	if v.tokens > 0 {
		v.tokens--
		return ports.RateLimitDecision{
			Allowed:       true,
			RetryAfterSec: 0,
			Remaining:     v.tokens,
			Limit:         limit,
			ResetAt:       v.lastRefill.Add(l.window),
		}, nil
	}

	retryAfter := int((l.window - elapsed).Seconds())
	if retryAfter <= 0 {
		retryAfter = 1
	}

	return ports.RateLimitDecision{
		Allowed:       false,
		RetryAfterSec: retryAfter,
		Remaining:     0,
		Limit:         limit,
		ResetAt:       v.lastRefill.Add(l.window),
		Reason:        "rate_limit_exceeded",
	}, nil
}

// AcquireConcurrency kiểm tra và chiếm giữ 1 slot đồng thời
// Phân tách riêng biệt giữa normal chat concurrency và agent run concurrency
func (l *LocalRateLimiter) AcquireConcurrency(ctx context.Context, id ports.RateLimitIdentity) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

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
		max = l.maxAgentRuns
	} else {
		concurrencyKey = fmt.Sprintf("concurrency:chat:tenant:%s:key:%s", tenant, key)
		max = l.maxConcurrent
	}

	current := l.inFlight[concurrencyKey]
	if max > 0 && current >= max {
		return nil, false, nil
	}

	l.inFlight[concurrencyKey] = current + 1

	released := false
	release := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		released = true
		cur := l.inFlight[concurrencyKey]
		if cur <= 1 {
			delete(l.inFlight, concurrencyKey)
		} else {
			l.inFlight[concurrencyKey] = cur - 1
		}
	}

	return release, true, nil
}

func (l *LocalRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(l.cleanupFreq)
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for key, v := range l.buckets {
			if now.Sub(v.lastRefill) > 2*l.window {
				delete(l.buckets, key)
			}
		}
		l.mu.Unlock()
	}
}

// SetMaxConcurrent thiết lập số lượng request đồng thời tối đa
func (l *LocalRateLimiter) SetMaxConcurrent(max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if max > 0 {
		l.maxConcurrent = max
	}
}

// SetMaxAgentRuns thiết lập số lượng agent run đồng thời tối đa
func (l *LocalRateLimiter) SetMaxAgentRuns(max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if max > 0 {
		l.maxAgentRuns = max
	}
}

// -------------------------------------------------------------------------
// HTTP Middlewares
// -------------------------------------------------------------------------

// PreAuthIPRateLimitMiddleware tạo middleware Rate Limiting chạy TRƯỚC authentication (Layer A)
// Chỉ sử dụng client IP để chống brute force, flood, abuse. Tuyệt đối không dùng API key hoặc tenant ở layer này.
func PreAuthIPRateLimitMiddleware(limiter ports.RateLimiter, extractIP func(*http.Request) string, metrics ...*domain.ContractMetrics) func(http.Handler) http.Handler {
	var m *domain.ContractMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Bỏ qua rate limit cho endpoint sức khỏe nội bộ
			if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			clientIP := r.RemoteAddr
			if extractIP != nil {
				clientIP = extractIP(r)
			}

			// Layer A: Chỉ sử dụng client IP, không kiểm tra API key hoặc tenant
			identity := ports.RateLimitIdentity{
				IP: clientIP,
			}

			decision, err := limiter.Allow(r.Context(), identity)
			if err == nil && !decision.Allowed {
				if m != nil {
					m.RecordRateLimitRejection("ip_limit")
					m.RecordClusterRateLimitRejection("ip_limit")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", decision.RetryAfterSec))
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": fmt.Sprintf("IP rate limit exceeded. Please wait %d seconds before retrying.", decision.RetryAfterSec),
						"type":    "rate_limit_error",
						"code":    "ip_rate_limit_exceeded",
					},
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AuthenticatedRateLimitMiddleware tạo middleware Rate Limit & Concurrency Limit chạy SAU Authentication (Layer B)
func AuthenticatedRateLimitMiddleware(limiter ports.RateLimiter, extractIP func(*http.Request) string, metrics ...*domain.ContractMetrics) func(http.Handler) http.Handler {
	var m *domain.ContractMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Bỏ qua rate limit cho endpoint sức khỏe nội bộ
			if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			// 1. Phân giải định danh an toàn (TenantID / KeyID / Hash) từ Context đã xác thực
			identity := ports.RateLimitIdentity{
				Endpoint: r.URL.Path,
				Model:    "default",
			}
			if extractIP != nil {
				identity.IP = extractIP(r)
			}

			if id, ok := domain.TenantIdentityFromContext(r.Context()); ok {
				identity.TenantID = id.TenantID
				identity.KeyID = id.KeyID
			} else if vKey := domain.VirtualKeyFromContext(r.Context()); vKey != nil {
				identity.TenantID = vKey.TenantID
				identity.KeyID = vKey.ID
			}

			// Nếu chưa có KeyID nhưng có raw Authorization header, băm cryptographic SHA-256 (tuyệt đối không dùng prefix)
			if identity.KeyID == "" {
				if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
					identity.KeyID = HashKeyID(strings.TrimPrefix(auth, "Bearer "))
				} else if xKey := r.Header.Get("x-api-key"); xKey != "" {
					identity.KeyID = HashKeyID(xKey)
				}
			}

			// 1b. Trích xuất Model: ưu tiên Context, sau đó Query param, sau đó Body JSON an toàn (fallback: default)
			if modelName, ok := domain.ModelFromContext(r.Context()); ok && modelName != "" {
				identity.Model = modelName
			} else if modelName := r.URL.Query().Get("model"); modelName != "" {
				identity.Model = strings.TrimSpace(modelName)
				r = r.WithContext(domain.ContextWithModel(r.Context(), identity.Model))
			} else if modelName := extractTargetModel(r); modelName != "" {
				identity.Model = modelName
				r = r.WithContext(domain.ContextWithModel(r.Context(), identity.Model))
			}
			if identity.Model == "" {
				identity.Model = "default"
			}

			if strings.Contains(r.URL.Path, "/agent/") || strings.Contains(r.URL.Path, "/runs") {
				identity.IsAgentRun = true
			}

			// 2. Kiểm tra Rate Limit (RPM)
			decision, err := limiter.Allow(r.Context(), identity)
			if err == nil && !decision.Allowed {
				if m != nil {
					m.RecordRateLimitRejection("rate_limit_exceeded")
					m.RecordClusterRateLimitRejection("rate_limit_exceeded")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", decision.RetryAfterSec))
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": fmt.Sprintf("Rate limit exceeded. Please wait %d seconds before retrying.", decision.RetryAfterSec),
						"type":    "rate_limit_error",
						"code":    "rate_limit_exceeded",
					},
				})
				return
			}

			// 3. Kiểm tra Concurrency Limit
			release, allowed, err := limiter.AcquireConcurrency(r.Context(), identity)
			if err == nil && !allowed {
				if m != nil {
					m.RecordConcurrencyRejection("concurrency_limit_exceeded")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "Too many concurrent requests. Please wait for previous requests to finish.",
						"type":    "rate_limit_error",
						"code":    "concurrency_limit_exceeded",
					},
				})
				return
			}
			if release != nil {
				defer release()
			}

			next.ServeHTTP(w, r)
		})
	}
}

// IPRateLimiter giữ tương thích ngược 100% cho cấu trúc và test cũ, đồng thời hỗ trợ ủy quyền sang cụm Redis
type IPRateLimiter struct {
	*LocalRateLimiter
	shared ports.RateLimiter
}

// NewIPRateLimiter khởi tạo IPRateLimiter bọc LocalRateLimiter
func NewIPRateLimiter(rate int, window time.Duration, trustedProxies ...[]string) *IPRateLimiter {
	local := NewLocalRateLimiter(rate, window, trustedProxies...)
	return &IPRateLimiter{LocalRateLimiter: local}
}

// SetSharedRateLimiter cấu hình rate limiter phân tán dùng chung cụm Redis
func (lim *IPRateLimiter) SetSharedRateLimiter(s ports.RateLimiter) {
	lim.shared = s
}

// Allow kiểm tra rate limit thông qua shared limiter nếu có hoặc local limiter
func (lim *IPRateLimiter) Allow(ctx context.Context, id ports.RateLimitIdentity) (ports.RateLimitDecision, error) {
	if lim.shared != nil {
		return lim.shared.Allow(ctx, id)
	}
	return lim.LocalRateLimiter.Allow(ctx, id)
}

// AcquireConcurrency chiếm giữ slot đồng thời thông qua shared limiter nếu có hoặc local limiter
func (lim *IPRateLimiter) AcquireConcurrency(ctx context.Context, id ports.RateLimitIdentity) (func(), bool, error) {
	if lim.shared != nil {
		return lim.shared.AcquireConcurrency(ctx, id)
	}
	return lim.LocalRateLimiter.AcquireConcurrency(ctx, id)
}

// Middleware cung cấp tương thích ngược cho Chi router
func (lim *IPRateLimiter) Middleware(metrics ...*domain.ContractMetrics) func(http.Handler) http.Handler {
	return PreAuthIPRateLimitMiddleware(lim, lim.LocalRateLimiter.ExtractClientIP, metrics...)
}

// MaxBodySizeMiddleware giới hạn kích thước tối đa của request body để chống DoS
func MaxBodySizeMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecureRealIPMiddleware thay thế middleware.RealIP của Chi bằng cơ chế kiểm tra Trusted Proxies an toàn
func SecureRealIPMiddleware(checker *TrustedProxyChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if checker != nil {
				peerIP := r.RemoteAddr
				if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
					peerIP = host
				}
				if checker.IsTrusted(peerIP) {
					if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
						parts := strings.Split(xff, ",")
						clientIP := strings.TrimSpace(parts[0])
						if net.ParseIP(clientIP) != nil {
							r.RemoteAddr = net.JoinHostPort(clientIP, "0")
						}
					} else if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
						if net.ParseIP(xrip) != nil {
							r.RemoteAddr = net.JoinHostPort(xrip, "0")
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
