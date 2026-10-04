package google

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const (
	DefaultFlowOrigin   = "https://flow.google.com"
	DefaultGeminiOrigin = "https://gemini.google.com"
)

// ResolveUserAgent ưu tiên lấy User-Agent đã lưu trong tài khoản, tránh bị WAF chặn do lệch User-Agent (docs-2 mục 1.2)
func ResolveUserAgent(account *domain.ManagedAccount, fallback string) string {
	if account != nil && account.UserAgent != "" {
		return account.UserAgent
	}
	if fallback != "" {
		return fallback
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
}

type GoogleTransportAdapter struct {
	client        *http.Client
	flowHost      string
	geminiHost    string
	defaultUA     string
	shortTimeout  time.Duration
	streamTimeout time.Duration

	proxyMu      sync.RWMutex
	proxyClients map[string]*http.Client
	proxyAccess  map[string]time.Time
}

func NewGoogleTransportAdapter(cfg *config.Config) ports.UpstreamGoogleTransport {
	short := 20 * time.Second
	stream := 1800 * time.Second
	connect := 10 * time.Second
	if cfg != nil {
		short = cfg.Server.ShortTimeout()
		stream = cfg.Server.StreamTimeout()
		connect = cfg.Server.ConnectTimeout()
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   connect,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	return &GoogleTransportAdapter{
		client: &http.Client{
			Transport: transport,
			Timeout:   0, // Timeout = 0 để tránh Client.Timeout hủy ngang stream đọc dữ liệu dài; timeout được kiểm soát bởi context và stream idle detection
		},
		flowHost:      DefaultFlowOrigin,
		geminiHost:    DefaultGeminiOrigin,
		defaultUA:     ResolveUserAgent(nil, ""),
		shortTimeout:  short,
		streamTimeout: stream,
		proxyClients:  make(map[string]*http.Client),
		proxyAccess:   make(map[string]time.Time),
	}
}

func (a *GoogleTransportAdapter) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	limit := 20 * time.Second
	if a != nil && a.shortTimeout > 0 {
		limit = a.shortTimeout
	}
	return domain.BoundContext(ctx, limit)
}

func (a *GoogleTransportAdapter) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	limit := 1800 * time.Second
	if a != nil && a.streamTimeout > 0 {
		limit = a.streamTimeout
	}
	return domain.BoundContext(ctx, limit)
}

func (a *GoogleTransportAdapter) DoRequest(
	ctx context.Context,
	account *domain.ManagedAccount,
	service domain.ServiceKind,
	method string,
	path string,
	body io.Reader,
	contentType string,
) (*http.Response, error) {
	var host string
	var fullURL string

	// Phân giải target host động:
	// 1. URL tuyệt đối truyền trực tiếp trong path (http:// hoặc https://)
	// 2. TargetHost truyền qua context (domain.WithTargetHost)
	// 3. Fallback theo ServiceKind (flow.google.com / gemini.google.com)
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		fullURL = path
		if u, err := url.Parse(path); err == nil && u.Host != "" {
			host = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
		}
	} else if ctxHost := domain.TargetHostFromContext(ctx); ctxHost != "" {
		host = ctxHost
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "https://" + host
		}
		fullURL = host + path
	}

	if host == "" {
		if service == domain.ServiceFlow {
			host = a.flowHost
		} else {
			host = a.geminiHost
		}
		fullURL = host + path
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("create upstream request error: %w", err)
	}

	// Xác định dịch vụ và origin/referer
	isFlow := (service == domain.ServiceFlow) || strings.Contains(host, "flow.google.com")

	origin := host
	referer := host + "/"
	if !isFlow {
		if strings.Contains(host, "gemini.google.com") {
			referer = host + "/app"
		}
	}

	// 1. Bộ Headers WAF & Anti-Bot bắt buộc đã kiểm chứng
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// Trường hợp Push Upload SCOTTY (push.clients6.google.com), Origin & Referer xuất phát từ Gemini Web
	if strings.Contains(host, "push.clients6.google.com") {
		origin = a.geminiHost
		referer = a.geminiHost + "/"
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	} else {
		req.Header.Set("X-Same-Domain", "1")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}

	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", referer)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")

	ua := a.defaultUA
	if account != nil && account.UserAgent != "" {
		ua = account.UserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 2. Phân lập và gắn Cookie Jar
	if account != nil && account.Jar != nil {
		req.Header.Set("Cookie", account.Jar.GetCookieHeader(isFlow))
	}

	client := a.getClient(account)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute upstream call error: %w", err)
	}

	// 3. Tự động bắt Set-Cookie để xoay vòng token thời gian __Secure-1PSIDTS
	if account != nil && account.Jar != nil && len(resp.Cookies()) > 0 {
		account.Jar.IngestResponseCookies(resp.Cookies())
	}

	return resp, nil
}

func (a *GoogleTransportAdapter) getClient(account *domain.ManagedAccount) *http.Client {
	if account == nil || account.GetProxy() == "" {
		return a.client
	}
	proxyStr := account.GetProxy()

	a.proxyMu.RLock()
	c, ok := a.proxyClients[proxyStr]
	a.proxyMu.RUnlock()
	if ok {
		a.proxyMu.Lock()
		a.proxyAccess[proxyStr] = time.Now()
		a.proxyMu.Unlock()
		return c
	}

	a.proxyMu.Lock()
	defer a.proxyMu.Unlock()
	if c, ok := a.proxyClients[proxyStr]; ok {
		a.proxyAccess[proxyStr] = time.Now()
		return c
	}

	// Cơ chế giới hạn LRU tối đa 50 proxy clients: đóng idle connections khi loại bỏ
	const maxCachedProxies = 50
	if len(a.proxyClients) >= maxCachedProxies {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, t := range a.proxyAccess {
			if first || t.Before(oldestTime) {
				oldestKey = k
				oldestTime = t
				first = false
			}
		}
		if oldestKey != "" {
			if oldClient, exists := a.proxyClients[oldestKey]; exists {
				if tr, ok := oldClient.Transport.(*http.Transport); ok {
					tr.CloseIdleConnections()
				}
				delete(a.proxyClients, oldestKey)
				delete(a.proxyAccess, oldestKey)
			}
		}
	}

	proxyURL, err := url.Parse(proxyStr)
	if err != nil {
		log.Printf("[Proxy Warning] Lỗi phân tích Proxy URL %q: %v, sử dụng kết nối mặc định", proxyStr, err)
		return a.client
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   0,
	}
	a.proxyClients[proxyStr] = client
	a.proxyAccess[proxyStr] = time.Now()
	return client
}

// ClientForAccount trả về http.Client được cấu hình proxy tương ứng với tài khoản
func (a *GoogleTransportAdapter) ClientForAccount(account *domain.ManagedAccount) *http.Client {
	return a.getClient(account)
}
