package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dezuxk-gateway/internal/config"
)

func TestAdminAuthMiddleware_Methods(t *testing.T) {
	enabled := true
	adminCfg := config.AdminConfig{
		Enabled:      &enabled,
		Username:     "admin_test",
		Password:     "secret_pass_123",
		SessionToken: "token_abc_xyz_999",
	}

	protectedHandler := AdminAuthMiddleware(adminCfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("protected_ok"))
	}))

	// 1. Không có auth -> 401 Unauthorized
	req1 := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	w1 := httptest.NewRecorder()
	protectedHandler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w1.Code)
	}

	// 2. Cookie dezuxk_admin_token -> 200 OK
	req2 := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	req2.AddCookie(&http.Cookie{
		Name:  "dezuxk_admin_token",
		Value: "token_abc_xyz_999",
	})
	w2 := httptest.NewRecorder()
	protectedHandler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK with valid cookie, got %d", w2.Code)
	}

	// 3. Bearer Token -> 200 OK
	req3 := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	req3.Header.Set("Authorization", "Bearer token_abc_xyz_999")
	w3 := httptest.NewRecorder()
	protectedHandler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 OK with Bearer token, got %d", w3.Code)
	}

	// 4. Basic Auth -> 200 OK
	req4 := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	basicVal := base64.StdEncoding.EncodeToString([]byte("admin_test:secret_pass_123"))
	req4.Header.Set("Authorization", "Basic "+basicVal)
	w4 := httptest.NewRecorder()
	protectedHandler.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("expected 200 OK with Basic auth, got %d", w4.Code)
	}

	// 5. Sai mật khẩu Basic Auth -> 401
	req5 := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	wrongBasic := base64.StdEncoding.EncodeToString([]byte("admin_test:wrong_pass"))
	req5.Header.Set("Authorization", "Basic "+wrongBasic)
	w5 := httptest.NewRecorder()
	protectedHandler.ServeHTTP(w5, req5)
	if w5.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong Basic auth, got %d", w5.Code)
	}
}

func TestAdminHandler_LoginAndLogout(t *testing.T) {
	enabled := true
	adminCfg := config.AdminConfig{
		Enabled:      &enabled,
		Username:     "myadmin",
		Password:     "mypassword",
		SessionToken: "my_static_token",
	}

	handler := NewAdminHandler(nil, nil, nil, nil, &adminCfg)

	// 1. Đăng nhập sai
	badBody := []byte(`{"username":"myadmin","password":"wrong"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/admin/auth/login", bytes.NewReader(badBody))
	w1 := httptest.NewRecorder()
	handler.HandleLogin(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad login, got %d", w1.Code)
	}

	// 2. Đăng nhập đúng username/password
	goodBody := []byte(`{"username":"myadmin","password":"mypassword"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/admin/auth/login", bytes.NewReader(goodBody))
	w2 := httptest.NewRecorder()
	handler.HandleLogin(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for good login, got %d", w2.Code)
	}

	// Kiểm tra cookie trả về
	cookies := w2.Result().Cookies()
	var adminCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "dezuxk_admin_token" {
			adminCookie = c
			break
		}
	}
	if adminCookie == nil || adminCookie.Value != "my_static_token" {
		t.Fatalf("expected admin cookie with value my_static_token, got %v", adminCookie)
	}

	// 3. Đăng nhập bằng Token trực tiếp
	tokenBody := []byte(`{"token":"my_static_token"}`)
	req3 := httptest.NewRequest(http.MethodPost, "/v1/admin/auth/login", bytes.NewReader(tokenBody))
	w3 := httptest.NewRecorder()
	handler.HandleLogin(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 for token login, got %d", w3.Code)
	}

	// 4. Đăng xuất
	req4 := httptest.NewRequest(http.MethodPost, "/v1/admin/auth/logout", nil)
	w4 := httptest.NewRecorder()
	handler.HandleLogout(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("expected 200 for logout, got %d", w4.Code)
	}
	logoutCookies := w4.Result().Cookies()
	for _, c := range logoutCookies {
		if c.Name == "dezuxk_admin_token" && c.MaxAge != -1 {
			t.Errorf("expected logout cookie to have MaxAge -1, got %d", c.MaxAge)
		}
	}
	_ = json.Marshal
}
