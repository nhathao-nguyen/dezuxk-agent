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

// GetCookieHeader xuất chuỗi Header Cookie cho Gemini
func (cj *CookieJar) GetCookieHeader(isFlow ...bool) string {
	cj.mu.RLock()
	defer cj.mu.RUnlock()

	var sb strings.Builder
	for name, val := range cj.cookies {
		if val == "" || name == "SNlM0e" || name == "cfb2h" {
			continue
		}
		// Bỏ OSID và __Secure-OSID nếu không yêu cầu
		if (name == "OSID" || name == "__Secure-OSID") && (len(isFlow) == 0 || !isFlow[0]) {
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

// ManagedAccount đại diện cho một danh tính người dùng Google Gemini
type ManagedAccount struct {
	ID           string
	Email        string
	Jar          *CookieJar
	GeminiSNlM0e string
	UserAgent    string
	Tier         int // 1: Free, 2: Pro
	ActiveModeID string // Mode ID hiện tại (RPC L5adhe)
	ProxyURL     string
	InFlightReqs int64
	IsHealthy    bool
	LastRefresh  time.Time

	mu         sync.RWMutex
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

func (a *ManagedAccount) GetActiveModeID() string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ActiveModeID
}

func (a *ManagedAccount) SetActiveModeID(modeID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ActiveModeID = modeID
}

func (a *ManagedAccount) IsAvailable() bool {
	return a.ServiceReady(ServiceGemini)
}

func (a *ManagedAccount) GetAtToken(service ...ServiceKind) string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.GeminiSNlM0e
}

func (a *ManagedAccount) SetAtToken(service ServiceKind, token string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.GeminiSNlM0e = token
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
