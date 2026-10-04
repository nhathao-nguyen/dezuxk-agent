package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// mockSessionRepoForHTTP implements minimal ports.SessionRepository for HTTP testing
type mockSessionRepoForHTTP struct {
	accounts []*domain.ManagedAccount
	mu       sync.Mutex
}

func (m *mockSessionRepoForHTTP) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
	return m.GetAvailableForModel(ctx, service, "", minCredits)
}

func (m *mockSessionRepoForHTTP) GetAvailableForModel(ctx context.Context, service domain.ServiceKind, modelID string, minCredits int) (*domain.ManagedAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, acc := range m.accounts {
		if acc.ServiceReady(service) && (modelID == "" || acc.SupportsModel(modelID)) {
			return acc, nil
		}
	}
	return nil, errors.New("không có phiên sẵn sàng")
}

func (m *mockSessionRepoForHTTP) Release(account *domain.ManagedAccount, err error) {}
func (m *mockSessionRepoForHTTP) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	return true
}
func (m *mockSessionRepoForHTTP) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
}
func (m *mockSessionRepoForHTTP) ListAll(ctx context.Context) []*domain.ManagedAccount {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accounts
}
func (m *mockSessionRepoForHTTP) Save(ctx context.Context, account *domain.ManagedAccount) error {
	return nil
}
func (m *mockSessionRepoForHTTP) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, acc := range m.accounts {
		if acc.ID == id {
			return acc, nil
		}
	}
	return nil, nil
}
func (m *mockSessionRepoForHTTP) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	return nil
}
func (m *mockSessionRepoForHTTP) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {
}
func (m *mockSessionRepoForHTTP) GetAlerts() []domain.SessionAlert   { return nil }
func (m *mockSessionRepoForHTTP) AddAlert(alert domain.SessionAlert) {}
func (m *mockSessionRepoForHTTP) ClearAlerts(accountID string)       {}

// TestProductionHardening_AdminDisabledRoutesInaccessible kiểm tra:
// Khi admin.enabled = false, các route admin runtime không được public mà phải trả về 404 (không mount).
func TestProductionHardening_AdminDisabledRoutesInaccessible(t *testing.T) {
	disabled := false
	cfg := &config.Config{
		Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
		Admin: config.AdminConfig{
			Enabled: &disabled,
		},
	}
	mr := domain.NewModelRegistry(nil)
	handler := BuildRouter(RouterDependencies{
		Config:        cfg,
		ModelRegistry: mr,
		SessionRepo:   &mockSessionRepoForHTTP{},
		Metrics:       domain.NewContractMetrics(),
	})

	testRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/runtime/models"},
		{http.MethodPost, "/admin/runtime/models/refresh"},
		{http.MethodGet, "/admin/runtime/accounts"},
		{http.MethodGet, "/admin/runtime/catalog/status"},
		{http.MethodGet, "/v1/admin/runtime/models"},
		{http.MethodGet, "/v1/admin/overview"},
	}

	for _, tc := range testRoutes {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("[%s %s] Expected 404 Not Found when admin is disabled, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

// TestProductionHardening_AdminEnabledAuth kiểm tra:
// Khi admin.enabled = true:
// - Không có auth -> 401 Unauthorized
// - Có auth hợp lệ -> 200 OK
// - Payload không được leak cookie, SNlM0e hoặc bí mật
func TestProductionHardening_AdminEnabledAuth(t *testing.T) {
	enabled := true
	sessionToken := "admin_secret_token_12345"
	cfg := &config.Config{
		Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
		Admin: config.AdminConfig{
			Enabled:      &enabled,
			Username:     "admin",
			Password:     "pass",
			SessionToken: sessionToken,
		},
	}

	acc := &domain.ManagedAccount{
		ID:           "acc-secret-test",
		GeminiSNlM0e: "super_secret_snlm0e_xyz",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "secret_cookie_val_123"}),
		IsHealthy:    true,
	}
	_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)

	sessionRepo := &mockSessionRepoForHTTP{accounts: []*domain.ManagedAccount{acc}}
	mr := domain.NewModelRegistry([]domain.ModelDescriptor{
		{
			ID:            "gemini-3.8-flash",
			DisplayName:   "Flash 3.8",
			TargetService: domain.ServiceGemini,
			Capabilities:  []domain.ModelCapability{domain.CapChat},
			IsActive:      true,
		},
	})

	handler := BuildRouter(RouterDependencies{
		Config:        cfg,
		ModelRegistry: mr,
		SessionRepo:   sessionRepo,
		Metrics:       domain.NewContractMetrics(),
	})

	// 1. Không có credentials -> 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/admin/runtime/models", nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized without credentials, got %d", unauthRec.Code)
	}

	// 2. Có bearer token -> 200
	authReq := httptest.NewRequest(http.MethodGet, "/admin/runtime/models", nil)
	authReq.Header.Set("Authorization", "Bearer "+sessionToken)
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with valid bearer token, got %d", authRec.Code)
	}

	// 3. GET /admin/runtime/accounts với auth -> 200, nhưng KHÔNG được chứa bí mật
	accReq := httptest.NewRequest(http.MethodGet, "/admin/runtime/accounts", nil)
	accReq.Header.Set("Authorization", "Bearer "+sessionToken)
	accRec := httptest.NewRecorder()
	handler.ServeHTTP(accRec, accReq)
	if accRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /admin/runtime/accounts, got %d", accRec.Code)
	}

	body := accRec.Body.String()
	if strings.Contains(body, "super_secret_snlm0e_xyz") {
		t.Fatalf("LEAK: /admin/runtime/accounts leaked raw SNlM0e secret in response!")
	}
	if strings.Contains(body, "secret_cookie_val_123") {
		t.Fatalf("LEAK: /admin/runtime/accounts leaked raw cookie secret in response!")
	}
}

// TestProductionHardening_AdminRefreshRateLimit kiểm tra:
// Endpoint POST /admin/runtime/models/refresh bị giới hạn tần suất (rate limit 429) khi gọi liên tục.
func TestProductionHardening_AdminRefreshRateLimit(t *testing.T) {
	enabled := true
	sessionToken := "admin_secret_token_12345"
	cfg := &config.Config{
		Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
		Admin: config.AdminConfig{
			Enabled:      &enabled,
			SessionToken: sessionToken,
		},
	}
	handler := BuildRouter(RouterDependencies{
		Config:        cfg,
		ModelRegistry: domain.NewModelRegistry(nil),
		SessionRepo:   &mockSessionRepoForHTTP{},
		Metrics:       domain.NewContractMetrics(),
	})

	// Lần 1: Có thể 200 (hoặc hoàn tất thành công)
	req1 := httptest.NewRequest(http.MethodPost, "/admin/runtime/models/refresh", nil)
	req1.Header.Set("Authorization", "Bearer "+sessionToken)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Lần 2 ngay lập tức: Phải bị 429 Too Many Requests (hoặc 409 Conflict)
	req2 := httptest.NewRequest(http.MethodPost, "/admin/runtime/models/refresh", nil)
	req2.Header.Set("Authorization", "Bearer "+sessionToken)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests && rec2.Code != http.StatusConflict {
		t.Fatalf("Expected 429 or 409 for rapid refresh endpoint, got %d", rec2.Code)
	}
}

// TestProductionHardening_ReadinessUnifiedEligibility kiểm tra:
// /ready endpoint sử dụng unified eligibility logic:
// Phải có ít nhất 1 active chat model M VÀ ít nhất 1 account A thỏa mãn:
// A is healthy, not auth_expired, not in cooldown, and SupportsModel(M.ID).
func TestProductionHardening_ReadinessUnifiedEligibility(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
	}
	modelID := "gemini-3.8-flash"
	activeModel := domain.ModelDescriptor{
		ID:            modelID,
		DisplayName:   "Flash 3.8",
		TargetService: domain.ServiceGemini,
		Capabilities:  []domain.ModelCapability{domain.CapChat},
		IsActive:      true,
	}

	// Case 1: Không có active chat model nào -> 503
	{
		mr := domain.NewModelRegistry(nil)
		sessionRepo := &mockSessionRepoForHTTP{}
		handler := BuildRouter(RouterDependencies{
			Config:        cfg,
			ModelRegistry: mr,
			SessionRepo:   sessionRepo,
			Metrics:       domain.NewContractMetrics(),
		})

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("Case 1: Expected 503 when no active models, got %d", rec.Code)
		}
	}

	// Case 2: Có active model, nhưng account bị IsHealthy=false -> 503
	{
		mr := domain.NewModelRegistry([]domain.ModelDescriptor{activeModel})
		acc := &domain.ManagedAccount{
			ID:              "acc-1",
			IsHealthy:       false,
			SupportedModels: []string{modelID},
		}
		_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
		sessionRepo := &mockSessionRepoForHTTP{accounts: []*domain.ManagedAccount{acc}}
		handler := BuildRouter(RouterDependencies{
			Config:        cfg,
			ModelRegistry: mr,
			SessionRepo:   sessionRepo,
			Metrics:       domain.NewContractMetrics(),
		})

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("Case 2: Expected 503 when account unhealthy, got %d", rec.Code)
		}
	}

	// Case 3: Có active model, account healthy nhưng đang bị cooldown -> 503
	{
		mr := domain.NewModelRegistry([]domain.ModelDescriptor{activeModel})
		acc := &domain.ManagedAccount{
			ID:              "acc-1",
			IsHealthy:       true,
			CooldownUntil:   time.Now().Add(5 * time.Minute),
			SupportedModels: []string{modelID},
		}
		_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
		sessionRepo := &mockSessionRepoForHTTP{accounts: []*domain.ManagedAccount{acc}}
		handler := BuildRouter(RouterDependencies{
			Config:        cfg,
			ModelRegistry: mr,
			SessionRepo:   sessionRepo,
			Metrics:       domain.NewContractMetrics(),
		})

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("Case 3: Expected 503 when account in cooldown, got %d", rec.Code)
		}
	}

	// Case 4: Có active model, account healthy nhưng KHÔNG support model đó -> 503
	{
		mr := domain.NewModelRegistry([]domain.ModelDescriptor{activeModel})
		acc := &domain.ManagedAccount{
			ID:              "acc-1",
			IsHealthy:       true,
			SupportedModels: []string{"gemini-other-model"},
		}
		_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
		sessionRepo := &mockSessionRepoForHTTP{accounts: []*domain.ManagedAccount{acc}}
		handler := BuildRouter(RouterDependencies{
			Config:        cfg,
			ModelRegistry: mr,
			SessionRepo:   sessionRepo,
			Metrics:       domain.NewContractMetrics(),
		})

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("Case 4: Expected 503 when account does not support active model, got %d", rec.Code)
		}
	}

	// Case 5: Có active chat model M VÀ có account A healthy, không cooldown, eligible cho M -> 200 OK
	{
		mr := domain.NewModelRegistry([]domain.ModelDescriptor{activeModel})
		acc := &domain.ManagedAccount{
			ID:              "acc-1",
			IsHealthy:       true,
			Jar:             domain.NewCookieJar(map[string]string{"__Secure-1PSID": "fake-sid"}),
			SupportedModels: []string{modelID},
		}
		_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
		sessionRepo := &mockSessionRepoForHTTP{accounts: []*domain.ManagedAccount{acc}}
		handler := BuildRouter(RouterDependencies{
			Config:        cfg,
			ModelRegistry: mr,
			SessionRepo:   sessionRepo,
			Metrics:       domain.NewContractMetrics(),
		})

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Case 5: Expected 200 OK when active model + eligible account pair exist, got %d", rec.Code)
		}
	}
}

// TestProductionHardening_ProfileControlPlaneNodeOwnership kiểm tra:
// Node follower (ControlPlaneEnabled = false) từ chối thao tác /v1/profiles bằng 403 Forbidden.
// Node control-plane (ControlPlaneEnabled = true) cho phép thao tác.
func TestProductionHardening_ProfileControlPlaneNodeOwnership(t *testing.T) {
	// 1. Follower node (ControlPlaneEnabled = false)
	followerFalse := false
	followerCfg := &config.Config{
		Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
		Profiles: config.ProfilesConfig{
			ControlPlaneEnabled: &followerFalse,
		},
	}
	followerHandler := BuildRouter(RouterDependencies{
		Config:        followerCfg,
		ModelRegistry: domain.NewModelRegistry(nil),
		SessionRepo:   &mockSessionRepoForHTTP{},
		Metrics:       domain.NewContractMetrics(),
	})

	reqFollower := httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	recFollower := httptest.NewRecorder()
	followerHandler.ServeHTTP(recFollower, reqFollower)

	if recFollower.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden on follower node without profile control plane ownership, got %d", recFollower.Code)
	}

	// 2. Control plane node (ControlPlaneEnabled = true)
	leaderTrue := true
	leaderCfg := &config.Config{
		Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
		Profiles: config.ProfilesConfig{
			ControlPlaneEnabled: &leaderTrue,
		},
	}
	leaderHandler := BuildRouter(RouterDependencies{
		Config:         leaderCfg,
		ModelRegistry:  domain.NewModelRegistry(nil),
		SessionRepo:    &mockSessionRepoForHTTP{},
		ProfileUseCase: nil,
		Metrics:        domain.NewContractMetrics(),
	})

	reqLeader := httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	recLeader := httptest.NewRecorder()
	leaderHandler.ServeHTTP(recLeader, reqLeader)

	// Since ProfileMgr is nil, it may return 500 or 200/empty, but definitely NOT 403 Forbidden
	if recLeader.Code == http.StatusForbidden {
		t.Fatalf("Expected non-403 on control plane node, got 403 Forbidden")
	}
}
