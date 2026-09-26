package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestAdminOverview_Success(t *testing.T) {
	metrics := domain.NewContractMetrics()
	// Giả lập 5 requests
	for i := 0; i < 5; i++ {
		metrics.RecordRequest()
	}

	mr := domain.NewModelRegistry([]domain.ModelDescriptor{
		{
			ID:            "gemini-3.8-flash",
			DisplayName:   "Gemini 3.8 Flash",
			TargetService: domain.ServiceGemini,
			IsActive:      true,
		},
		{
			ID:                "veo-2.0",
			DisplayName:       "Veo 2.0 Video",
			TargetService:     domain.ServiceFlow,
			InternalBackendID: "veo-2.0-backend",
			IsActive:          true,
		},
	})

	enabled := true
	cfg := config.CacheConfig{
		Enabled:    &enabled,
		MaxEntries: 100,
		TTLSeconds: 3600,
		Methods:    []string{"chat"},
	}
	respCache := services.NewResponseCache(cfg)
	respCache.Set("test-key", []byte("test-payload"), "application/json", nil)

	sessionRepo := session.NewMemorySessionRepository(nil)
	acc := &domain.ManagedAccount{
		ID:             "profile_test_1",
		Email:          "test@example.com",
		ProxyURL:       "http://127.0.0.1:8888",
		CreditsBalance: 1200,
		IsHealthy:      true,
		Tier:           3,
		LastRefresh:    time.Now(),
	}
	acc.CommitServiceState(domain.ServiceFlow, domain.StateReady)
	acc.CommitServiceState(domain.ServiceGemini, domain.StateReady)
	_ = sessionRepo.Save(context.Background(), acc)


	adminHandler := NewAdminHandler(sessionRepo, mr, metrics, respCache, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	w := httptest.NewRecorder()
	adminHandler.HandleOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var overview map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &overview); err != nil {
		t.Fatalf("failed to decode overview response: %v", err)
	}

	// Kiểm tra accounts
	accs, ok := overview["accounts"].([]any)
	if !ok || len(accs) != 1 {
		t.Fatalf("expected 1 account in overview, got %v", overview["accounts"])
	}
	accMap := accs[0].(map[string]any)
	if accMap["email"] != "test@example.com" {
		t.Errorf("expected email test@example.com, got %v", accMap["email"])
	}
	if accMap["tier"] != "Ultra" {
		t.Errorf("expected tier Ultra, got %v", accMap["tier"])
	}
	if accMap["credits"] != float64(1200) {
		t.Errorf("expected credits 1200, got %v", accMap["credits"])
	}

	// Kiểm tra metrics
	metricsMap, ok := overview["metrics"].(map[string]any)
	if !ok {
		t.Fatal("expected metrics object in overview")
	}
	if metricsMap["total_requests"] != float64(5) {
		t.Errorf("expected 5 total_requests, got %v", metricsMap["total_requests"])
	}
	if metricsMap["rpm"] != float64(5) {
		t.Errorf("expected 5 rpm, got %v", metricsMap["rpm"])
	}

	// Kiểm tra cache
	cacheMap, ok := overview["cache"].(map[string]any)
	if !ok {
		t.Fatal("expected cache object in overview")
	}
	if cacheMap["total_entries"] != float64(1) {
		t.Errorf("expected 1 total entry, got %v", cacheMap["total_entries"])
	}

	// Kiểm tra models
	modelsList, ok := overview["models"].([]any)
	if !ok || len(modelsList) != 2 {
		t.Errorf("expected 2 models in overview, got %v", overview["models"])
	}
}
