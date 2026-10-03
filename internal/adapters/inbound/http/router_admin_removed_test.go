package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func TestRouter_AdminWebDashboard(t *testing.T) {
	metrics := domain.NewContractMetrics()
	// Khởi tạo router với dependencies tối thiểu
	handler := BuildRouter(RouterDependencies{
		Config:        &config.Config{Server: config.ServerConfig{AllowedOrigins: []string{"*"}}},
		ModelRegistry: domain.NewModelRegistry(nil),
		Metrics:       metrics,
	})

	// 1. Kiểm tra /health vẫn hoạt động bình thường (200 OK)
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)

	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected /health to return %d, got %d", http.StatusOK, healthRec.Code)
	}

	// 2. Kiểm tra / điều hướng sang /admin/ (302 Found)
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)

	if rootRec.Code != http.StatusFound {
		t.Fatalf("expected / to return %d, got %d", http.StatusFound, rootRec.Code)
	}
	if loc := rootRec.Header().Get("Location"); loc != "/admin/" {
		t.Fatalf("expected Location /admin/, got %s", loc)
	}

	// 3. Kiểm tra /admin điều hướng sang /admin/ (302 Found)
	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRec := httptest.NewRecorder()
	handler.ServeHTTP(adminRec, adminReq)

	if adminRec.Code != http.StatusFound {
		t.Fatalf("expected /admin to return %d, got %d", http.StatusFound, adminRec.Code)
	}
	if loc := adminRec.Header().Get("Location"); loc != "/admin/" {
		t.Fatalf("expected Location /admin/, got %s", loc)
	}

	// 4. Kiểm tra /admin/ trả về 200 OK và chứa UI Dashboard
	adminSlashReq := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	adminSlashRec := httptest.NewRecorder()
	handler.ServeHTTP(adminSlashRec, adminSlashReq)

	if adminSlashRec.Code != http.StatusOK {
		t.Fatalf("expected /admin/ to return %d, got %d (body: %s)", http.StatusOK, adminSlashRec.Code, adminSlashRec.Body.String())
	}
	if !strings.Contains(adminSlashRec.Body.String(), "DEZUXK") {
		t.Fatalf("expected index.html to contain DEZUXK")
	}

	// 5. Kiểm tra file tĩnh /admin/style.css
	cssReq := httptest.NewRequest(http.MethodGet, "/admin/style.css", nil)
	cssRec := httptest.NewRecorder()
	handler.ServeHTTP(cssRec, cssReq)

	if cssRec.Code != http.StatusOK {
		t.Fatalf("expected /admin/style.css to return %d, got %d", http.StatusOK, cssRec.Code)
	}

	// 6. Kiểm tra file tĩnh /admin/app.js
	jsReq := httptest.NewRequest(http.MethodGet, "/admin/app.js", nil)
	jsRec := httptest.NewRecorder()
	handler.ServeHTTP(jsRec, jsReq)

	if jsRec.Code != http.StatusOK {
		t.Fatalf("expected /admin/app.js to return %d, got %d", http.StatusOK, jsRec.Code)
	}

	// 7. Xác nhận các request tĩnh, điều hướng, và /health không bị tính vào ContractMetrics (TotalRequests = 0)
	if snap := metrics.Snapshot(); snap.TotalRequests != 0 {
		t.Fatalf("expected TotalRequests to be 0 after admin and health checks, got %d", snap.TotalRequests)
	}

	// 8. Kiểm tra request API Gateway thực thụ (/v1/models) phải được ghi nhận vào TotalRequests (= 1)
	modelsReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsRec := httptest.NewRecorder()
	handler.ServeHTTP(modelsRec, modelsReq)

	if snap := metrics.Snapshot(); snap.TotalRequests != 1 {
		t.Fatalf("expected TotalRequests to be 1 after /v1/models, got %d", snap.TotalRequests)
	}
}
