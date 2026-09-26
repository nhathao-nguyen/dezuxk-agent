package google

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

var (
	reSNlM0e = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	reCFB2H  = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
)

type TokenExtractorAdapter struct {
	client       *http.Client
	flowHost     string
	geminiHost   string
	defaultUA    string
	short        time.Duration
	proxyMu      sync.RWMutex
	proxyClients map[string]*http.Client
}

func NewGoogleTokenExtractorAdapter(short time.Duration) ports.TokenExtractor {
	return newTokenExtractor(DefaultFlowOrigin, DefaultGeminiOrigin, ResolveUserAgent(nil, ""), short)
}

func NewTokenExtractorAdapter(flowHost, geminiHost, defaultUA string) ports.TokenExtractor {
	return newTokenExtractor(flowHost, geminiHost, defaultUA, 20*time.Second)
}

func newTokenExtractor(flowHost, geminiHost, defaultUA string, short time.Duration) *TokenExtractorAdapter {
	if short <= 0 {
		short = 20 * time.Second
	}
	client := &http.Client{
		Timeout: short,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("quá 10 lần chuyển hướng")
			}
			if len(via) > 0 {
				req.Header.Set("Cookie", via[0].Header.Get("Cookie"))
				req.Header.Set("User-Agent", via[0].Header.Get("User-Agent"))
			}
			return nil
		},
	}
	return &TokenExtractorAdapter{
		client:       client,
		flowHost:     flowHost,
		geminiHost:   geminiHost,
		defaultUA:    defaultUA,
		short:        short,
		proxyClients: make(map[string]*http.Client),
	}
}

func (e *TokenExtractorAdapter) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	limit := 20 * time.Second
	if e != nil && e.short > 0 {
		limit = e.short
	}
	return domain.BoundContext(ctx, limit)
}

func (e *TokenExtractorAdapter) ExtractTokens(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) (string, string, error) {
	ctx, cancel := e.bound(ctx)
	defer cancel()

	var targetURL string
	var isFlow bool

	if service == domain.ServiceFlow {
		targetURL = e.flowHost + "/"
		isFlow = true
	} else {
		targetURL = e.geminiHost + "/app"
		isFlow = false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", "", domain.CodecTransport(domain.OriginHandshake, service, err)
	}

	ua := e.defaultUA
	if account.UserAgent != "" {
		ua = account.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Cookie", account.Jar.GetCookieHeader(isFlow))
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := e.getClient(account)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", domain.CodecTransport(domain.OriginHandshake, service, err)
	}
	defer resp.Body.Close()

	if resp.Request != nil && resp.Request.URL != nil {
		host := resp.Request.URL.Hostname()
		if host == "accounts.google.com" || strings.HasSuffix(host, ".accounts.google.com") {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			return "", "", domain.CodecExpired(domain.OriginHandshake, service, "phiên gốc hết hạn")
		}
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", StatusError(resp, domain.OriginHandshake, false, service)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", domain.CodecTransport(domain.OriginHandshake, service, err)
	}
	bodyStr := string(bodyBytes)

	matchSN := reSNlM0e.FindStringSubmatch(bodyStr)
	if len(matchSN) < 2 {
		return "", "", domain.CodecExpired(domain.OriginHandshake, service, "không lấy lại được bí mật dẫn xuất")
	}

	cfb2h := ""
	matchCFB := reCFB2H.FindStringSubmatch(bodyStr)
	if len(matchCFB) >= 2 {
		cfb2h = matchCFB[1]
	}

	return matchSN[1], cfb2h, nil
}

func (e *TokenExtractorAdapter) getClient(account *domain.ManagedAccount) *http.Client {
	if account == nil || account.GetProxy() == "" {
		return e.client
	}
	proxyStr := account.GetProxy()

	e.proxyMu.RLock()
	c, ok := e.proxyClients[proxyStr]
	e.proxyMu.RUnlock()
	if ok {
		return c
	}

	e.proxyMu.Lock()
	defer e.proxyMu.Unlock()
	if c, ok := e.proxyClients[proxyStr]; ok {
		return c
	}

	proxyURL, err := url.Parse(proxyStr)
	if err != nil {
		return e.client
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   e.short,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("quá 10 lần chuyển hướng")
			}
			if len(via) > 0 {
				req.Header.Set("Cookie", via[0].Header.Get("Cookie"))
				req.Header.Set("User-Agent", via[0].Header.Get("User-Agent"))
			}
			return nil
		},
	}
	e.proxyClients[proxyStr] = client
	return client
}
