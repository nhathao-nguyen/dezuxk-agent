package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func TestPrometheusMetricsExporter(t *testing.T) {
	metrics := domain.NewContractMetrics()
	metrics.RecordRequest()
	metrics.AddSchema()

	exporter := NewPrometheusMetricsExporter(nil, nil, nil, metrics)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	exporter.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", contentType)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "gateway_requests_total 1") {
		t.Errorf("expected gateway_requests_total 1 in body, got:\n%s", body)
	}
	if !strings.Contains(body, "gateway_errors_total 1") {
		t.Errorf("expected gateway_errors_total 1 in body, got:\n%s", body)
	}
}

func TestRouter_MetricsAuthHardening_Production(t *testing.T) {
	cfg := &config.Config{
		Environment: "production",
		Server: config.ServerConfig{
			APIKey:         "master-server-key",
			MetricsToken:   "metrics-scrape-secret-token",
			AllowedOrigins: []string{"https://admin.example.com"},
		},
		Admin: config.AdminConfig{
			Password:     "super-secret-admin-pass-999",
			SessionToken: "admin-session-token-xyz",
		},
	}

	router := BuildRouter(RouterDependencies{
		Config:        cfg,
		ModelRegistry: domain.NewModelRegistry(nil),
		Metrics:       domain.NewContractMetrics(),
	})

	// 1. Untrusted remote peer with NO token -> 401 Unauthorized
	req1 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req1.RemoteAddr = "198.51.100.22:54321"
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated public peer, got %d", rec1.Code)
	}

	// 2. Untrusted remote peer trying to use raw Admin Password as Bearer token -> MUST FAIL (401)
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.RemoteAddr = "198.51.100.22:54321"
	req2.Header.Set("Authorization", "Bearer super-secret-admin-pass-999")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("SECURITY VIOLATION: raw admin password should NOT be accepted as Bearer token for /metrics, got: %d", rec2.Code)
	}

	// 3. Untrusted remote peer using dedicated MetricsToken -> 200 OK
	req3 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req3.RemoteAddr = "198.51.100.22:54321"
	req3.Header.Set("Authorization", "Bearer metrics-scrape-secret-token")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with valid dedicated metrics_token, got %d", rec3.Code)
	}

	// 4. Localhost access (internal scraping) without token -> 200 OK
	req4 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req4.RemoteAddr = "127.0.0.1:41234"
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for localhost internal scraper, got %d", rec4.Code)
	}
}
