package domain

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// CookieJar quản lý các cookie và phân lập domain
type CookieJar struct {
	mu      sync.RWMutex
	cookies map[string]string
}

func NewCookieJar(raw map[string]string) *CookieJar {
	cj := &CookieJar{cookies: make(map[string]string)}
	for k, v := range raw {
		cj.cookies[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return cj
}

// IngestResponseCookies cập nhật các cookie Google xoay vòng (Rolling Cookies)
func (cj *CookieJar) IngestResponseCookies(cookies []*http.Cookie) bool {
	cj.mu.Lock()
	defer cj.mu.Unlock()

	updated := false
	for _, c := range cookies {
		switch c.Name {
		case "__Secure-1PSIDTS", "__Secure-1PSIDCC", "SIDCC", "COMPASS", "OSID", "__Secure-OSID":
			if c.Value != "" && cj.cookies[c.Name] != c.Value {
				cj.cookies[c.Name] = c.Value
				updated = true
			}
		}
	}
	return updated
}

// GetCookieHeader xuất chuỗi Header Cookie phân lập theo Service Target
func (cj *CookieJar) GetCookieHeader(isFlow bool) string {
	cj.mu.RLock()
	defer cj.mu.RUnlock()

	var sb strings.Builder
	for name, val := range cj.cookies {
		if val == "" || name == "SNlM0e" || name == "cfb2h" {
			continue
		}
		// Chỉ đưa OSID và __Secure-OSID vào request Flow
		if (name == "OSID" || name == "__Secure-OSID") && !isFlow {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(name)
		sb.WriteString("=")
		sb.WriteString(val)
	}
	return sb.String()
}

func (cj *CookieJar) HasKey(name string) bool {
	cj.mu.RLock()
	defer cj.mu.RUnlock()
	v, ok := cj.cookies[name]
	return ok && v != ""
}

func (cj *CookieJar) Get(name string) string {
	cj.mu.RLock()
	defer cj.mu.RUnlock()
	return cj.cookies[name]
}

func (cj *CookieJar) GetAll() map[string]string {
	cj.mu.RLock()
	defer cj.mu.RUnlock()
	res := make(map[string]string, len(cj.cookies))
	for k, v := range cj.cookies {
		res[k] = v
	}
	return res
}

// ManagedAccount đại diện cho một danh tính người dùng Google
type ManagedAccount struct {
	ID               string
	Email            string
	Jar              *CookieJar
	FlowSNlM0e       string
	GeminiSNlM0e     string
	FlowProjectID    string
	FlowSessionToken string
	UserAgent        string
	CreditsBalance   int
	Tier             int // 1: Free, 2: Pro
	ProxyURL         string
	InFlightReqs     int64
	IsHealthy        bool
	LastRefresh      time.Time

	mu         sync.RWMutex
	flowGate   serviceGate
	geminiGate serviceGate
}

func (a *ManagedAccount) GetProxy() string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ProxyURL
}

func (a *ManagedAccount) SetProxy(proxyURL string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ProxyURL = proxyURL
}

func (a *ManagedAccount) IsAvailable() bool {
	return a.ServiceReady(ServiceGemini) || a.ServiceReady(ServiceFlow)
}

func (a *ManagedAccount) GetAtToken(service ServiceKind) string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if service == ServiceFlow {
		return a.FlowSNlM0e
	}
	return a.GeminiSNlM0e
}

func (a *ManagedAccount) SetAtToken(service ServiceKind, token string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if service == ServiceFlow {
		a.FlowSNlM0e = token
		return
	}
	a.GeminiSNlM0e = token
}

func (a *ManagedAccount) GetFlowMediaSecrets() (projectID, sessionToken string) {
	if a == nil {
		return "", ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.FlowProjectID, a.FlowSessionToken
}

func (a *ManagedAccount) SetFlowProjectID(projectID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.FlowProjectID = projectID
	a.mu.Unlock()
}

func (a *ManagedAccount) SetFlowSessionToken(token string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.FlowSessionToken = token
	a.mu.Unlock()
}

func (cj *CookieJar) ToMap() map[string]string {
	if cj == nil {
		return nil
	}
	cj.mu.RLock()
	defer cj.mu.RUnlock()
	res := make(map[string]string, len(cj.cookies))
	for k, v := range cj.cookies {
		res[k] = v
	}
	return res
}

// SessionAlert ghi nhận cảnh báo phiên tài khoản bị lỗi, hết hạn hoặc thu hồi
type SessionAlert struct {
	AccountID      string      `json:"account_id"`
	Service        ServiceKind `json:"service"`
	Reason         string      `json:"reason"`
	StatusCode     int         `json:"status_code,omitempty"`
	ActionRequired string      `json:"action_required"`
	CreatedAt      time.Time   `json:"created_at"`
}
