package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func TestRouter_AdminWebRemoved(t *testing.T) {
	// Khởi tạo router với dependencies tối thiểu
	handler := BuildRouter(RouterDependencies{
		Config:        &config.Config{Server: config.ServerConfig{AllowedOrigins: []string{"*"}}},
		ModelRegistry: domain.NewModelRegistry(nil),
		Metrics:       domain.NewContractMetrics(),
	})

	// 1. Kiểm tra /health vẫn hoạt động bình thường (200 OK)
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)

	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected /health to return %d, got %d", http.StatusOK, healthRec.Code)
	}

	// 2. Kiểm tra /admin đã bị gỡ bỏ (404 Not Found)
	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRec := httptest.NewRecorder()
	handler.ServeHTTP(adminRec, adminReq)

	if adminRec.Code != http.StatusNotFound {
		t.Fatalf("expected /admin to return %d, got %d (body: %s)", http.StatusNotFound, adminRec.Code, adminRec.Body.String())
	}

	// 3. Kiểm tra /admin/ cũng trả về 404 Not Found
	adminSlashReq := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	adminSlashRec := httptest.NewRecorder()
	handler.ServeHTTP(adminSlashRec, adminSlashReq)

	if adminSlashRec.Code != http.StatusNotFound {
		t.Fatalf("expected /admin/ to return %d, got %d (body: %s)", http.StatusNotFound, adminSlashRec.Code, adminSlashRec.Body.String())
	}
}
