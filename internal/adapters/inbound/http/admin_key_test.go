package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestAdminKeyEndpoints_And_ClientAuth(t *testing.T) {
	keyRepo := session.NewMemoryKeyRepository()
	keyService := services.NewKeyService(keyRepo, "master-admin-key-pass")

	cfg := &config.Config{
		Server: config.ServerConfig{
			AllowedOrigins: []string{"*"},
			APIKey:         "master-admin-key-pass",
		},
	}

	router := BuildRouter(RouterDependencies{
		Config:        cfg,
		KeyUseCase:    keyService,
		ModelRegistry: domain.NewModelRegistry(nil),
	})

	// 1. Gọi /v1/admin/keys không có Authorization header -> 401 Unauthorized
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/keys", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when no auth, got %d", rec.Code)
	}

	// 2. Tạo Virtual Key mới bằng Master Admin Key (POST /v1/admin/keys)
	createBody := map[string]any{
		"name":                 "Client Mobile App",
		"role":                 "user",
		"rate_limit_rpm":       10,
		"daily_quota_requests": 2,
		"allowed_models":       []string{"gemini-2.5-flash"},
	}
	bodyBytes, _ := json.Marshal(createBody)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/keys", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer master-admin-key-pass")
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var created domain.VirtualKeyCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to parse created key response: %v", err)
	}
	if created.Key == "" || created.ID == "" {
		t.Fatalf("invalid created key response: %+v", created)
	}
	clientRawKey := created.Key
	clientKeyID := created.ID

	// 3. Liệt kê các key đang hoạt động (GET /v1/admin/keys) bằng Master Admin
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/keys", nil)
	req.Header.Set("Authorization", "Bearer master-admin-key-pass")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var listResp struct {
		Keys  []*domain.VirtualKey `json:"keys"`
		Count int                  `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Count != 1 || listResp.Keys[0].ID != clientKeyID {
		t.Fatalf("expected 1 active key with id %s, got %+v", clientKeyID, listResp)
	}

	// 4. Client với role 'user' gọi endpoint admin -> 403 Forbidden
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/keys", nil)
	req.Header.Set("Authorization", "Bearer "+clientRawKey)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin key on admin route, got %d", rec.Code)
	}

	// 5. Kiểm tra phân quyền Model (Allowed Models)
	// Request model không được phép ("veo-2.0-generate-001") -> 403 Forbidden
	reqChatDisallowed := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"veo-2.0-generate-001","messages":[{"role":"user","content":"hi"}]}`)),
	)
	reqChatDisallowed.Header.Set("Authorization", "Bearer "+clientRawKey)
	reqChatDisallowed.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, reqChatDisallowed)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for disallowed model, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 5.1. Gọi GET /v1/models không tiêu thụ hạn ngạch ngày
	reqModels := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	reqModels.Header.Set("Authorization", "Bearer "+clientRawKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, reqModels)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /v1/models, got %d", rec.Code)
	}

	// 6. Kiểm tra Daily Quota (Hạn ngạch 2 request)
	// Lần 1: Gọi model hợp lệ ("gemini-2.5-flash")
	reqChat1 := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)),
	)
	reqChat1.Header.Set("Authorization", "Bearer "+clientRawKey)
	reqChat1.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, reqChat1)
	// ChatUseCase là nil trong test này nên nếu vượt qua Auth middleware sẽ tới handler (trả về 503 hoặc 404 tùy handler)
	// Nhưng mã trạng thái TUYỆT ĐỐI không phải là 401/403/429!
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusTooManyRequests {
		t.Fatalf("unexpected auth error on request 1: code %d, body: %s", rec.Code, rec.Body.String())
	}

	// Lần 2: Tiếp tục thành công
	reqChat2 := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)),
	)
	reqChat2.Header.Set("Authorization", "Bearer "+clientRawKey)
	reqChat2.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, reqChat2)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusTooManyRequests {
		t.Fatalf("unexpected auth error on request 2: code %d, body: %s", rec.Code, rec.Body.String())
	}

	// Lần 3: Đã dùng hết 2/2 lượt quota trong ngày -> Phải nhận HTTP 429 Too Many Requests
	reqChat3 := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)),
	)
	reqChat3.Header.Set("Authorization", "Bearer "+clientRawKey)
	reqChat3.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, reqChat3)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests when daily quota exceeded, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 7. Thu hồi key qua DELETE /v1/admin/keys/{id}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/admin/keys/"+clientKeyID, nil)
	req.Header.Set("Authorization", "Bearer master-admin-key-pass")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on revoke, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 8. Sau khi thu hồi, gọi lại bằng key này phải nhận 401 Unauthorized
	reqAfterRevoke := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)),
	)
	reqAfterRevoke.Header.Set("Authorization", "Bearer "+clientRawKey)
	reqAfterRevoke.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, reqAfterRevoke)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized after key revocation, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestAdminKeyEndpoints_RevokeNotFound(t *testing.T) {
	keyRepo := session.NewMemoryKeyRepository()
	keyService := services.NewKeyService(keyRepo, "master-admin")

	router := BuildRouter(RouterDependencies{
		KeyUseCase: keyService,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/admin/keys/vk_non_existent", nil)
	req.Header.Set("Authorization", "Bearer master-admin")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", rec.Code)
	}
}
