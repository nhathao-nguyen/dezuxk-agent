package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrBlockedScheme báo lỗi scheme không hợp lệ
	ErrBlockedScheme = errors.New("ssrf: only http and https schemes are permitted")
	// ErrMissingHost báo lỗi thiếu hostname
	ErrMissingHost = errors.New("ssrf: missing hostname in request URL")
	// ErrBlockedHost báo lỗi hostname bị cấm (localhost, cloud metadata, internal)
	ErrBlockedHost = errors.New("ssrf: target host is restricted")
	// ErrBlockedIP báo lỗi IP thuộc dải nội bộ, link-local, loopback hoặc CGNAT
	ErrBlockedIP = errors.New("ssrf: target IP address is restricted")
	// ErrTooManyRedirects báo lỗi vượt quá số lần chuyển hướng
	ErrTooManyRedirects = errors.New("ssrf: too many redirects")
)

// URLGuard định nghĩa interface kiểm tra tính an toàn của URL trước và trong khi gửi request
type URLGuard interface {
	Validate(ctx context.Context, rawURL string) (*url.URL, error)
	IsRestrictedIP(ip net.IP) bool
	IsRestrictedHost(host string) bool
}

// DefaultGuard cài đặt mặc định của URLGuard với chính sách bảo mật SSRF nghiêm ngặt
type DefaultGuard struct {
	allowLoopbackForTesting bool
}

// NewDefaultGuard tạo mới DefaultGuard
func NewDefaultGuard(allowLoopbackForTesting bool) *DefaultGuard {
	return &DefaultGuard{
		allowLoopbackForTesting: allowLoopbackForTesting,
	}
}

// IsRestrictedHost kiểm tra hostname có nằm trong danh sách cấm hay không
func (g *DefaultGuard) IsRestrictedHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return true
	}
	// Xóa port nếu có
	if strings.Contains(h, ":") {
		if sh, _, err := net.SplitHostPort(h); err == nil {
			h = sh
		}
	}
	h = strings.TrimSuffix(h, ".")

	if !g.allowLoopbackForTesting {
		if h == "localhost" || strings.HasSuffix(h, ".localhost") {
			return true
		}
	}

	// Chặn cloud metadata endpoints và tên miền nội bộ
	if h == "metadata.google.internal" || strings.HasSuffix(h, ".internal") ||
		h == "instance-data" || strings.HasSuffix(h, ".local") {
		return true
	}

	if h == "169.254.169.254" {
		return true
	}

	return false
}

// IsRestrictedIP kiểm tra IP có thuộc các dải nhạy cảm hay không
func (g *DefaultGuard) IsRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Cho phép loopback trong môi trường test nếu được cấu hình rõ ràng
	if g.allowLoopbackForTesting && ip.IsLoopback() {
		return false
	}

	// 1. Loopback (127.0.0.0/8, ::1)
	if ip.IsLoopback() {
		return true
	}

	// 2. Private IPv4 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) và Private IPv6 (fc00::/7)
	if ip.IsPrivate() {
		return true
	}

	// 3. Link-Local (169.254.0.0/16, fe80::/10)
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// 4. Multicast & Unspecified (0.0.0.0, ::)
	if ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() {
		return true
	}

	// 5. Kiểm tra chi tiết dải IPv4 (bao gồm IPv4-mapped IPv6)
	if ipv4 := ip.To4(); ipv4 != nil {
		// 0.0.0.0/8 (Current network)
		if ipv4[0] == 0 {
			return true
		}
		// 169.254.0.0/16 (Link-local / Cloud metadata 169.254.169.254)
		if ipv4[0] == 169 && ipv4[1] == 254 {
			return true
		}
		// 100.64.0.0/10 (CGNAT / Shared Address Space RFC 6598)
		if ipv4[0] == 100 && (ipv4[1] >= 64 && ipv4[1] <= 127) {
			return true
		}
		// 192.0.0.0/24 (IETF Protocol Assignments)
		if ipv4[0] == 192 && ipv4[1] == 0 && ipv4[2] == 0 {
			return true
		}
		// 198.18.0.0/15 (Benchmarking tests RFC 2544)
		if ipv4[0] == 198 && (ipv4[1] == 18 || ipv4[1] == 19) {
			return true
		}
		// 255.255.255.255 (Broadcast)
		if ipv4[0] == 255 && ipv4[1] == 255 && ipv4[2] == 255 && ipv4[3] == 255 {
			return true
		}
	}

	return false
}

// Validate kiểm tra cú pháp URL, scheme và đối soát IP tĩnh hoặc hostname
func (g *DefaultGuard) Validate(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, ErrBlockedScheme
	}

	hostname := strings.ToLower(u.Hostname())
	if hostname == "" {
		return nil, ErrMissingHost
	}

	if g.IsRestrictedHost(hostname) {
		return nil, fmt.Errorf("%w: %s", ErrBlockedHost, hostname)
	}

	// Nếu hostname là IP trực tiếp (ví dụ http://127.0.0.1 hoặc http://[::1])
	if ip := net.ParseIP(hostname); ip != nil {
		if g.IsRestrictedIP(ip) {
			return nil, fmt.Errorf("%w: %s", ErrBlockedIP, ip.String())
		}
		return u, nil
	}

	return u, nil
}

// SafeHTTPConfig cấu hình khởi tạo HTTP Client an toàn
type SafeHTTPConfig struct {
	Timeout                 time.Duration
	AllowLoopbackForTesting bool
	CheckRedirect           func(req *http.Request, via []*http.Request) error
	CustomResolver          func(ctx context.Context, network, host string) ([]net.IP, error)
	CustomDialContext       func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewSafeHTTPClient tạo http.Client được bảo vệ chống SSRF, DNS Rebinding và Redirect Hijacking
func NewSafeHTTPClient(cfg SafeHTTPConfig) *http.Client {
	guard := NewDefaultGuard(cfg.AllowLoopbackForTesting)

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	dialContext := cfg.CustomDialContext
	if dialContext == nil {
		dialer := &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}

		dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("ssrf dial split failed: %w", err)
			}

			if guard.IsRestrictedHost(host) {
				return nil, fmt.Errorf("%w: %s", ErrBlockedHost, host)
			}

			var ips []net.IP
			if ip := net.ParseIP(host); ip != nil {
				ips = []net.IP{ip}
			} else if cfg.CustomResolver != nil {
				resolved, err := cfg.CustomResolver(ctx, network, host)
				if err != nil {
					return nil, fmt.Errorf("ssrf dns lookup failed for %s: %w", host, err)
				}
				ips = resolved
			} else {
				resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
				if err != nil {
					return nil, fmt.Errorf("ssrf dns lookup failed for %s: %w", host, err)
				}
				ips = resolved
			}

			if len(ips) == 0 {
				return nil, fmt.Errorf("ssrf dns: no IP resolved for %s", host)
			}

			// Chống DNS Rebinding & TOCTOU:
			// Nếu BẤT KỲ IP nào được phân giải thuộc dải hạn chế/nội bộ -> từ chối kết nối ngay lập tức!
			var safeIP net.IP
			for _, ip := range ips {
				if guard.IsRestrictedIP(ip) {
					return nil, fmt.Errorf("%w: %s resolved to restricted IP %s", ErrBlockedIP, host, ip.String())
				}
				if safeIP == nil {
					safeIP = ip
				}
			}

			// Kết nối trực tiếp tới IP đã được thẩm định an toàn
			// Đối với HTTPS: http.Transport vẫn giữ nguyên TLS ServerName ban đầu từ Request URL,
			// do đó chứng chỉ TLS vẫn được xác thực nghiêm ngặt theo đúng hostname.
			safeAddr := net.JoinHostPort(safeIP.String(), port)
			return dialer.DialContext(ctx, network, safeAddr)
		}
	}

	transport := &http.Transport{
		DialContext:           dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return ErrTooManyRedirects
			}

			// Validate URL chuyển hướng mới
			if _, err := guard.Validate(req.Context(), req.URL.String()); err != nil {
				return fmt.Errorf("ssrf redirect blocked: %w", err)
			}

			// Cho phép hook kiểm tra redirect mở rộng (ví dụ xóa cookie nhạy cảm khi chuyển hướng)
			if cfg.CheckRedirect != nil {
				return cfg.CheckRedirect(req, via)
			}

			return nil
		},
	}

	return client
}
