package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

// ISSUE 6: GET /health không được rò rỉ alerts, tài khoản, proxy, hay cookie của hệ thống
func TestHealthEndpointDoesNotLeakAlerts(t *testing.T) {
	sessionRepo := session.NewMemorySessionRepository(nil)

	// Thêm cảnh báo nội bộ chứa thông tin nhạy cảm
	sessionRepo.AddAlert(domain.SessionAlert{
		AccountID:      "acc-sensitive-007",
		Service:        domain.ServiceGemini,
		Reason:         "Proxy connection failed via socks5://secret-user:secret-pass@10.0.0.1:1080 for admin-secret@corp.internal",
		ActionRequired: "reauth_cookies",
		CreatedAt:      time.Now(),
	})

	cfg := &config.Config{
		Server: config.ServerConfig{
			AllowedOrigins: []string{"*"},
		},
	}

	readiness := NewReadinessManager()
	readiness.SetReady(true)

	models := domain.NewModelRegistry(nil)

	router := BuildRouter(RouterDependencies{
		Config:           cfg,
		SessionRepo:      sessionRepo,
		ModelRegistry:    models,
		ReadinessManager: readiness,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec.Code)
	}

	rawBody := rec.Body.String()

	// 1. Tuyệt đối không được chứa dữ liệu nhạy cảm
	if strings.Contains(rawBody, "admin-secret@corp.internal") {
		t.Fatalf("SECURITY LEAK: /health leaked email!")
	}
	if strings.Contains(rawBody, "secret-pass") || strings.Contains(rawBody, "socks5://") {
		t.Fatalf("SECURITY LEAK: /health leaked proxy credentials!")
	}
	if strings.Contains(rawBody, "acc-sensitive-007") {
		t.Fatalf("SECURITY LEAK: /health leaked account ID!")
	}

	// 2. Parse payload và kiểm tra safe operational data
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response from /health: %v", err)
	}

	if _, ok := payload["alerts"]; ok {
		t.Fatalf("SECURITY LEAK: /health exposed 'alerts' array to unauthenticated callers!")
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status 'ok', got: %v", payload["status"])
	}
	if payload["ready"] != true {
		t.Fatalf("expected ready=true, got: %v", payload["ready"])
	}
	if _, ok := payload["models_active"]; !ok {
		t.Fatalf("missing models_active in /health payload")
	}
	if _, ok := payload["timestamp"]; !ok {
		t.Fatalf("missing timestamp in /health payload")
	}
}

// ISSUE 6: GET /v1/alerts và POST /v1/alerts/clear bắt buộc quyền Admin (RequireAdmin)
func TestAlertsRequireAdmin(t *testing.T) {
	keyRepo := session.NewMemoryKeyRepository()
	keyService := services.NewKeyService(keyRepo, "master-super-secret-key")

	sessionRepo := session.NewMemorySessionRepository(nil)
	sessionRepo.AddAlert(domain.SessionAlert{
		AccountID:      "acc-internal",
		Service:        domain.ServiceGemini,
		Reason:         "Test alert for internal@corp.com",
		ActionRequired: "reauth",
		CreatedAt:      time.Now(),
	})

	cfg := &config.Config{
		Server: config.ServerConfig{
			AllowedOrigins: []string{"*"},
			APIKey:         "master-super-secret-key",
		},
	}

	router := BuildRouter(RouterDependencies{
		Config:        cfg,
		KeyUseCase:    keyService,
		SessionRepo:   sessionRepo,
		ModelRegistry: domain.NewModelRegistry(nil),
	})

	ctx := context.Background()

	// 1. Tạo Regular User Key
	userKeyCreated, err := keyService.CreateKey(ctx, domain.CreateKeyRequest{
		Name:               "Normal User Key",
		Role:               "user",
		RateLimitRPM:       60,
		DailyQuotaRequests: 100,
	})
	if err != nil {
		t.Fatalf("failed to create user key: %v", err)
	}

	// 2. User Key gọi GET /v1/alerts -> Bắt buộc bị từ chối 403 Forbidden
	recUser := httptest.NewRecorder()
	reqUser := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	reqUser.Header.Set("Authorization", "Bearer "+userKeyCreated.RawKey)
	router.ServeHTTP(recUser, reqUser)
	if recUser.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin on GET /v1/alerts, got: %d (body: %s)", recUser.Code, recUser.Body.String())
	}

	// 3. User Key gọi POST /v1/alerts/clear -> Bắt buộc bị từ chối 403 Forbidden
	recUserClear := httptest.NewRecorder()
	reqUserClear := httptest.NewRequest(http.MethodPost, "/v1/alerts/clear", nil)
	reqUserClear.Header.Set("Authorization", "Bearer "+userKeyCreated.RawKey)
	router.ServeHTTP(recUserClear, reqUserClear)
	if recUserClear.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin on POST /v1/alerts/clear, got: %d", recUserClear.Code)
	}

	// 4. Admin Key gọi GET /v1/alerts -> 200 OK
	recAdmin := httptest.NewRecorder()
	reqAdmin := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	reqAdmin.Header.Set("Authorization", "Bearer master-super-secret-key")
	router.ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin on GET /v1/alerts, got: %d", recAdmin.Code)
	}

	// 5. Admin Key gọi POST /v1/alerts/clear -> 200 OK
	recAdminClear := httptest.NewRecorder()
	reqAdminClear := httptest.NewRequest(http.MethodPost, "/v1/alerts/clear", nil)
	reqAdminClear.Header.Set("Authorization", "Bearer master-super-secret-key")
	router.ServeHTTP(recAdminClear, reqAdminClear)
	if recAdminClear.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin on POST /v1/alerts/clear, got: %d", recAdminClear.Code)
	}
}

// HTTP Model Auth: Request payload lớn (2MB+) chứa model cấm không được bypass kiểm tra quyền
func TestLargeBodyCannotBypassAllowedModels(t *testing.T) {
	keyRepo := session.NewMemoryKeyRepository()
	keyService := services.NewKeyService(keyRepo, "master-admin-key")

	cfg := &config.Config{
		Server: config.ServerConfig{
			AllowedOrigins: []string{"*"},
		},
	}

	router := BuildRouter(RouterDependencies{
		Config:        cfg,
		KeyUseCase:    keyService,
		ModelRegistry: domain.NewModelRegistry(nil),
	})

	ctx := context.Background()

	// Tạo key bị giới hạn model: CHỈ ĐƯỢC PHÉP DÙNG 'gemini-1.5-pro'
	restrictedKey, err := keyService.CreateKey(ctx, domain.CreateKeyRequest{
		Name:          "Restricted Pro Key",
		Role:          "user",
		AllowedModels: []string{"gemini-1.5-pro"},
	})
	if err != nil {
		t.Fatalf("failed to create restricted key: %v", err)
	}

	// 1. Tạo JSON payload lớn (2MB) với messages độn nhiều ký tự và "model": "gemini-1.5-flash" (bị cấm)
	largePadding := strings.Repeat("x", 2*1024*1024) // 2MB padding
	forbiddenBody := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": largePadding},
		},
		"model": "gemini-1.5-flash", // MODEL BỊ CẤM
	}
	bodyBytes, _ := json.Marshal(forbiddenBody)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+restrictedKey.RawKey)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	// Phải bị từ chối 403 Forbidden với mã model_not_allowed
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for forbidden model in large body, got: %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_not_allowed") {
		t.Fatalf("expected error code model_not_allowed, got: %s", rec.Body.String())
	}

	// 2. Request không chỉ định model gửi tới /v1/chat/completions trên restricted key -> Phải bị từ chối 403
	noModelBody := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "Hello without model"},
		},
	}
	noModelBytes, _ := json.Marshal(noModelBody)
	recNoModel := httptest.NewRecorder()
	reqNoModel := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(noModelBytes))
	reqNoModel.Header.Set("Authorization", "Bearer "+restrictedKey.RawKey)
	reqNoModel.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recNoModel, reqNoModel)

	if recNoModel.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when model is missing for restricted key, got: %d", recNoModel.Code)
	}
}
