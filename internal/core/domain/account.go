package domain

import (
	"math"
	"math/rand"
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
	ID                  string
	Email               string
	Jar                 *CookieJar
	GeminiSNlM0e        string
	UserAgent           string
	Tier                int // 1: Free, 2: Pro
	ActiveModeID        string // Mode ID hiện tại (RPC L5adhe)
	ProxyURL            string
	InFlightReqs        int64
	IsHealthy           bool
	LastRefresh         time.Time
	SuccessCount        int64
	FailureCount        int64
	ConsecutiveFailures int
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	HealthScore         float64 // Điểm số từ 0.0 đến 1.0 (mặc định 1.0)

	mu         sync.RWMutex
	geminiGate serviceGate
}

// GetHealthScore trả về điểm số sức khỏe của tài khoản (0.0 đến 1.0)
func (a *ManagedAccount) GetHealthScore() float64 {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.getHealthScoreLocked()
}

func (a *ManagedAccount) getHealthScoreLocked() float64 {
	if a.HealthScore <= 0 {
		if a.ConsecutiveFailures == 0 && a.IsHealthy {
			return 1.0
		}
		return 0.1
	}
	return a.HealthScore
}

// GetStats trả về các chỉ số thống kê của tài khoản một cách thread-safe
func (a *ManagedAccount) GetStats() (success int64, failure int64, consecutive int, health float64) {
	if a == nil {
		return 0, 0, 0, 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.SuccessCount, a.FailureCount, a.ConsecutiveFailures, a.getHealthScoreLocked()
}

// RecordSuccess ghi nhận một yêu cầu thành công (HTTP 200 OK)
func (a *ManagedAccount) RecordSuccess() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.SuccessCount++
	a.ConsecutiveFailures = 0
	a.LastSuccessAt = time.Now()
	a.IsHealthy = true

	// Hồi phục dần điểm sức khỏe theo thuật toán EWMA
	cur := a.getHealthScoreLocked()
	if cur < 1.0 {
		a.HealthScore = math.Min(1.0, cur*0.85+0.15)
	} else {
		a.HealthScore = 1.0
	}
}

// RecordFailure ghi nhận lỗi upstream (429, 503, 401,...) và phạt điểm sức khỏe
func (a *ManagedAccount) RecordFailure(class ErrorClass, statusCode int) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.FailureCount++
	a.ConsecutiveFailures++
	a.LastFailureAt = time.Now()

	cur := a.getHealthScoreLocked()
	switch {
	case class == ClassExpired || statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		a.HealthScore = 0.0
		a.IsHealthy = false
	case class == ClassRateLimited || statusCode == http.StatusTooManyRequests:
		// 429: Phạt 30% điểm, sàn tối thiểu 0.05
		a.HealthScore = math.Max(0.05, cur*0.70)
	case class == ClassUpstreamUnavailable || statusCode == http.StatusBadGateway || statusCode == http.StatusServiceUnavailable:
		// 502/503: Phạt 20% điểm, sàn tối thiểu 0.10
		a.HealthScore = math.Max(0.10, cur*0.80)
	default:
		// Lỗi mạng hoặc timeout khác: Phạt 15%
		a.HealthScore = math.Max(0.10, cur*0.85)
	}
}

// GetDynamicCooldown tính toán thời gian chờ thích ứng dựa trên số lần thất bại liên tiếp kèm Jitter (±20%)
func (a *ManagedAccount) GetDynamicCooldown(base time.Duration) time.Duration {
	if base <= 0 {
		base = 30 * time.Second
	}
	if a == nil {
		return base
	}
	a.mu.RLock()
	cf := a.ConsecutiveFailures
	a.mu.RUnlock()

	multiplier := 1.0
	if cf > 1 {
		// Tăng theo cấp số mũ 1.5^(cf-1), giới hạn tối đa 10x base cooldown
		multiplier = math.Min(10.0, math.Pow(1.5, float64(cf-1)))
	}

	// Áp dụng Jitter ngẫu nhiên trong khoảng [0.85, 1.20] để tránh thundering herd
	jitter := 0.85 + rand.Float64()*0.35
	cooldown := time.Duration(float64(base) * multiplier * jitter)
	// Giới hạn tối đa cooldown không quá 15 phút
	if cooldown > 15*time.Minute {
		cooldown = 15 * time.Minute
	}
	return cooldown
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
