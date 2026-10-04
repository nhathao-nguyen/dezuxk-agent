package http

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
)

type AdminRuntimeHandler struct {
	mu           sync.Mutex
	lastRefresh  time.Time
	discoverySvc *services.ModelDiscoveryService
	catalogRepo  ports.ModelCatalogRepository
	sessionRepo  ports.SessionRepository
	registry     *domain.ModelRegistry
	metrics      *domain.ContractMetrics
}

func NewAdminRuntimeHandler(
	discoverySvc *services.ModelDiscoveryService,
	catalogRepo ports.ModelCatalogRepository,
	sessionRepo ports.SessionRepository,
	registry *domain.ModelRegistry,
	metrics *domain.ContractMetrics,
) *AdminRuntimeHandler {
	return &AdminRuntimeHandler{
		discoverySvc: discoverySvc,
		catalogRepo:  catalogRepo,
		sessionRepo:  sessionRepo,
		registry:     registry,
		metrics:      metrics,
	}
}

// HandleRefresh: POST /admin/runtime/models/refresh
func (h *AdminRuntimeHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.mu.TryLock() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "Một tiến trình làm mới danh mục mô hình đang được thực thi",
			"code":  "refresh_in_progress",
		})
		return
	}
	defer h.mu.Unlock()

	if time.Since(h.lastRefresh) < 3*time.Second {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "Yêu cầu làm mới danh mục mô hình quá nhanh, vui lòng chờ giây lát",
			"code":  "rate_limited",
		})
		return
	}
	h.lastRefresh = time.Now()

	if h.discoverySvc == nil {
		http.Error(w, `{"error": "model discovery service is not configured"}`, http.StatusNotImplemented)
		return
	}

	res, err := h.discoverySvc.DiscoverAndSync(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":            "Lỗi khi thực hiện discovery mô hình",
			"accounts_scanned": res.AccountsScanned,
			"models_seen":      res.ModelsSeen,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts_scanned":   res.AccountsScanned,
		"accounts_succeeded": res.AccountsSucceeded,
		"accounts_failed":    res.AccountsFailed,
		"models_seen":        res.ModelsSeen,
		"models_added":       res.ModelsAdded,
		"models_updated":     res.ModelsUpdated,
	})
}

// HandleListRuntimeModels: GET /admin/runtime/models
func (h *AdminRuntimeHandler) HandleListRuntimeModels(w http.ResponseWriter, r *http.Request) {
	var models []domain.ModelDescriptor
	if h.catalogRepo != nil {
		list, err := h.catalogRepo.ListModels(r.Context(), "")
		if err == nil {
			models = list
		}
	}
	if len(models) == 0 && h.registry != nil {
		models = h.registry.ListAll()
	}

	type cleanModel struct {
		ID           string                   `json:"id"`
		DisplayName  string                   `json:"display_name"`
		Service      string                   `json:"service"`
		UpstreamID   string                   `json:"upstream_id"`
		BackendID    string                   `json:"backend_id"`
		ModeID       string                   `json:"mode_id"`
		Tier         int                      `json:"tier"`
		Capabilities []domain.ModelCapability `json:"capabilities"`
		IsAvailable  bool                     `json:"is_available"`
		Source       string                   `json:"source"`
		FirstSeenAt  *time.Time               `json:"first_seen_at,omitempty"`
		LastSeenAt   *time.Time               `json:"last_seen_at,omitempty"`
		UpdatedAt    *time.Time               `json:"updated_at,omitempty"`
	}

	result := make([]cleanModel, 0, len(models))
	for _, m := range models {
		cm := cleanModel{
			ID:           m.ID,
			DisplayName:  m.DisplayName,
			Service:      string(m.TargetService),
			UpstreamID:   m.UpstreamModelID,
			BackendID:    m.InternalBackendID,
			ModeID:       m.ModeID,
			Tier:         m.ModelTierCode,
			Capabilities: m.Capabilities,
			IsAvailable:  m.IsActive,
			Source:       m.Source,
		}
		if cm.UpstreamID == "" {
			cm.UpstreamID = m.InternalBackendID
		}
		if !m.FirstSeenAt.IsZero() {
			cm.FirstSeenAt = &m.FirstSeenAt
		}
		if !m.LastSeenAt.IsZero() {
			cm.LastSeenAt = &m.LastSeenAt
		}
		if !m.UpdatedAt.IsZero() {
			cm.UpdatedAt = &m.UpdatedAt
		}
		result = append(result, cm)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"models": result,
		"count":  len(result),
	})
}

// HandleListRuntimeAccounts: GET /admin/runtime/accounts
// Tuyệt đối không trả token, cookies, PSID hay bất kỳ raw secret nào
func (h *AdminRuntimeHandler) HandleListRuntimeAccounts(w http.ResponseWriter, r *http.Request) {
	if h.sessionRepo == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": []any{}, "count": 0})
		return
	}

	rawAccounts := h.sessionRepo.ListAll(r.Context())
	type cleanAccount struct {
		ID              string              `json:"id"`
		Email           string              `json:"email"`
		HealthStatus    domain.HealthStatus `json:"health_status"`
		IsHealthy       bool                `json:"is_healthy"`
		HealthScore     float64             `json:"health_score"`
		Tier            int                 `json:"tier"`
		ActiveModeID    string              `json:"active_mode_id,omitempty"`
		SupportedModels []string            `json:"supported_models"`
		CooldownUntil   *time.Time          `json:"cooldown_until,omitempty"`
		InFlightReqs    int64               `json:"in_flight_reqs"`
	}

	result := make([]cleanAccount, 0, len(rawAccounts))
	for _, a := range rawAccounts {
		if a == nil {
			continue
		}
		ca := cleanAccount{
			ID:              a.ID,
			Email:           a.Email,
			HealthStatus:    a.GetHealthStatus(),
			IsHealthy:       a.IsHealthy,
			HealthScore:     a.HealthScore,
			Tier:            a.Tier,
			ActiveModeID:    a.ActiveModeID,
			SupportedModels: a.GetSupportedModels(),
			InFlightReqs:    a.InFlightReqs,
		}
		if !a.CooldownUntil.IsZero() {
			ca.CooldownUntil = &a.CooldownUntil
		}
		result = append(result, ca)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts": result,
		"count":    len(result),
	})
}

// HandleCatalogStatus: GET /admin/runtime/catalog/status
func (h *AdminRuntimeHandler) HandleCatalogStatus(w http.ResponseWriter, r *http.Request) {
	var totalModels int
	var activeModels int
	if h.registry != nil {
		activeModels = h.registry.Count()
		totalModels = len(h.registry.ListAll())
	}

	var totalAccounts int
	var healthyAccounts int
	if h.sessionRepo != nil {
		accs := h.sessionRepo.ListAll(r.Context())
		totalAccounts = len(accs)
		for _, a := range accs {
			if a != nil && a.IsHealthy && a.GetHealthStatus() == domain.HealthStatusHealthy {
				healthyAccounts++
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":                 "ok",
		"total_models":           totalModels,
		"active_models":          activeModels,
		"total_accounts":         totalAccounts,
		"healthy_accounts":       healthyAccounts,
		"discovery_enabled":      h.discoverySvc != nil,
		"has_usable_chat_models": activeModels > 0 && healthyAccounts > 0,
		"timestamp":              time.Now().UTC(),
	})
}
