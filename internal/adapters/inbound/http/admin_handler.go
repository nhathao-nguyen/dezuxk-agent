package http

import (
	"encoding/json"
	"net/http"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
)

type AdminHandler struct {
	sessionRepo   ports.SessionRepository
	modelRegistry *domain.ModelRegistry
	metrics       *domain.ContractMetrics
	cache         *services.ResponseCache
	adminCfg      *config.AdminConfig
	keyUseCase    ports.KeyUseCase
	profileMgr    ports.ProfileUseCase
}

func NewAdminHandler(
	sr ports.SessionRepository,
	mr *domain.ModelRegistry,
	metrics *domain.ContractMetrics,
	cache *services.ResponseCache,
	adminCfg *config.AdminConfig,
) *AdminHandler {
	return &AdminHandler{
		sessionRepo:   sr,
		modelRegistry: mr,
		metrics:       metrics,
		cache:         cache,
		adminCfg:      adminCfg,
	}
}

func (h *AdminHandler) SetKeyUseCase(k ports.KeyUseCase) {
	h.keyUseCase = k
}

func (h *AdminHandler) SetProfileUseCase(pm ports.ProfileUseCase) {
	h.profileMgr = pm
}

// AccountSummary tóm tắt trạng thái tài khoản cho Dashboard
type AccountSummary struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	Tier         string  `json:"tier"`
	IsHealthy    bool    `json:"is_healthy"`
	Proxy        string  `json:"proxy"`
	GeminiStatus string  `json:"gemini_status"`
	HasGemini    bool    `json:"has_gemini"`
	LastRefresh  string  `json:"last_refresh"`
	HealthScore  float64 `json:"health_score"`
	SuccessCount int64   `json:"success_count"`
	FailureCount int64   `json:"failure_count"`
}

// HandleOverview trả về tổng hợp tài khoản, metrics RPM, cache stats, drift alerts và GPU models
func (h *AdminHandler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Danh sách tài khoản: Kết hợp Session Repo & Profile Manager
	var accounts []AccountSummary
	seen := make(map[string]bool)

	if h.sessionRepo != nil {
		allAccs := h.sessionRepo.ListAll(ctx)
		for _, acc := range allAccs {
			seen[acc.ID] = true
			tierStr := "Free"
			if acc.Tier == 2 {
				tierStr = "Pro"
			} else if acc.Tier == 3 {
				tierStr = "Ultra"
			}

			proxyVal := acc.GetProxy()
			hasGemini := false
			if acc.Jar != nil {
				hasGemini = acc.Jar.HasKey("__Secure-1PSID")
			}

			accounts = append(accounts, AccountSummary{
				ID:           acc.ID,
				Email:        acc.Email,
				Tier:         tierStr,
				IsHealthy:    acc.IsHealthy,
				Proxy:        proxyVal,
				GeminiStatus: string(acc.ServiceState(domain.ServiceGemini)),
				HasGemini:    hasGemini,
				LastRefresh:  acc.LastRefresh.Format(time.RFC3339),
				HealthScore:  acc.GetHealthScore(),
				SuccessCount: acc.SuccessCount,
				FailureCount: acc.FailureCount,
			})
		}
	}

	if h.profileMgr != nil {
		allProfiles := h.profileMgr.ListActiveProfiles()
		for _, p := range allProfiles {
			if !seen[p.ID] {
				seen[p.ID] = true
				accounts = append(accounts, AccountSummary{
					ID:           p.ID,
					Email:        "Chưa đăng nhập",
					Tier:         "Chờ nạp",
					IsHealthy:    false,
					Proxy:        p.Proxy,
					GeminiStatus: "Chờ đồng bộ CDP",
					HasGemini:    false,
					LastRefresh:  p.LastActive.Format(time.RFC3339),
					HealthScore:  0,
					SuccessCount: 0,
					FailureCount: 0,
				})
			}
		}
	}

	// 2. Metrics & RPM Snapshot
	var metricsSnapshot domain.ContractSnapshot
	if h.metrics != nil {
		metricsSnapshot = h.metrics.Snapshot()
	}

	// 3. Cache Stats
	var cacheStats services.CacheStats
	if h.cache != nil {
		cacheStats = h.cache.Stats()
	}

	// 4. Danh sách mô hình GPU online
	var models []domain.ModelDescriptor
	if h.modelRegistry != nil {
		models = h.modelRegistry.List()
	}

	// 5. Cảnh báo phiên (Alerts)
	var alertsList []domain.SessionAlert
	if h.sessionRepo != nil {
		alertsList = h.sessionRepo.GetAlerts()
	}

	// 6. Thống kê sử dụng Token toàn hệ thống
	var systemUsage []domain.KeyTokenUsage
	if h.keyUseCase != nil {
		if u, err := h.keyUseCase.GetSystemTokenUsageHistory(ctx, 7); err == nil {
			systemUsage = u
		}
	}
	if systemUsage == nil {
		systemUsage = make([]domain.KeyTokenUsage, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"timestamp":   time.Now().Format(time.RFC3339),
		"accounts":    accounts,
		"metrics":     metricsSnapshot,
		"cache":       cacheStats,
		"models":      models,
		"alerts":      alertsList,
		"token_usage": systemUsage,
	})
}

type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

// HandleLogin xử lý đăng nhập Admin cấp session token và cookie
func (h *AdminHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if h.adminCfg == nil || !h.adminCfg.IsEnabled() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin dashboard bị vô hiệu hóa trong cấu hình"})
		return
	}

	var req AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "dữ liệu yêu cầu không hợp lệ"})
		return
	}

	expectedUser := h.adminCfg.GetUsername()
	expectedPass := h.adminCfg.GetPassword()
	expectedToken := h.adminCfg.GetSessionToken()

	authorized := false
	if req.Token != "" && req.Token == expectedToken {
		authorized = true
	} else if req.Username == expectedUser && req.Password == expectedPass {
		authorized = true
	}

	if !authorized {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tên đăng nhập, mật khẩu hoặc token không chính xác"})
		return
	}

	// Thiết lập Session Cookie cho Admin UI
	http.SetCookie(w, &http.Cookie{
		Name:     "dezuxk_admin_token",
		Value:    expectedToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7, // 7 ngày
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"token":   expectedToken,
		"message": "Đăng nhập trang quản trị thành công.",
	})
}

// HandleLogout xử lý đăng xuất xóa cookie
func (h *AdminHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "dezuxk_admin_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Đã đăng xuất khỏi phiên quản trị.",
	})
}
