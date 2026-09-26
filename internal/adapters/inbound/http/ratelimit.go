package http

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
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

// IPRateLimiter quản lý giới hạn tần suất request theo IP (Sliding Window / Token Bucket)
type IPRateLimiter struct {
	mu             sync.Mutex
	visitors       map[string]*visitor
	rate           int           // Số request tối đa trong window
	window         time.Duration // Độ dài cửa sổ thời gian
	cleanupFreq    time.Duration
	trustedChecker *TrustedProxyChecker
}

type visitor struct {
	tokens     int
	lastRefill time.Time
}

// NewIPRateLimiter khởi tạo một Rate Limiter cho HTTP Router
func NewIPRateLimiter(rate int, window time.Duration, trustedProxies ...[]string) *IPRateLimiter {
	var checker *TrustedProxyChecker
	if len(trustedProxies) > 0 && len(trustedProxies[0]) > 0 {
		checker = NewTrustedProxyChecker(trustedProxies[0])
	}

	limiter := &IPRateLimiter{
		visitors:       make(map[string]*visitor),
		rate:           rate,
		window:         window,
		cleanupFreq:    1 * time.Minute,
		trustedChecker: checker,
	}
	go limiter.cleanupLoop()
	return limiter
}

// ExtractClientIP trích xuất IP của client thực sự, bảo vệ chống giả mạo header X-Forwarded-For
func (lim *IPRateLimiter) ExtractClientIP(r *http.Request) string {
	peerIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peerIP = host
	}

	// Chỉ đọc X-Forwarded-For hoặc X-Real-IP nếu peerIP thuộc danh sách trusted_proxies
	if lim.trustedChecker != nil && lim.trustedChecker.IsTrusted(peerIP) {
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

func (lim *IPRateLimiter) allow(ip string) bool {
	lim.mu.Lock()
	defer lim.mu.Unlock()

	now := time.Now()
	v, exists := lim.visitors[ip]
	if !exists {
		lim.visitors[ip] = &visitor{
			tokens:     lim.rate - 1,
			lastRefill: now,
		}
		return true
	}

	// Refill tokens dựa trên thời gian trôi qua
	elapsed := now.Sub(v.lastRefill)
	if elapsed >= lim.window {
		v.tokens = lim.rate
		v.lastRefill = now
	}

	if v.tokens > 0 {
		v.tokens--
		return true
	}

	return false
}

func (lim *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(lim.cleanupFreq)
	for range ticker.C {
		lim.mu.Lock()
		now := time.Now()
		for ip, v := range lim.visitors {
			if now.Sub(v.lastRefill) > 2*lim.window {
				delete(lim.visitors, ip)
			}
		}
		lim.mu.Unlock()
	}
}

// Middleware trả về hàm middleware của Chi
func (lim *IPRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bỏ qua rate limit cho endpoint sức khỏe nội bộ
			if r.URL.Path == "/health" || r.URL.Path == "/ready" {
				next.ServeHTTP(w, r)
				return
			}

			ip := lim.ExtractClientIP(r)
			if !lim.allow(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "Rate limit exceeded. Please slow down your requests to protect upstream account sessions.",
						"type":    "rate_limit_error",
						"code":    "rate_limit_exceeded",
					},
				})
				return
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
