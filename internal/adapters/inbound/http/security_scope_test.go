package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/core/domain"
)

func TestRequireScope_Enforcement(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	chatScopedMiddleware := adaptersHTTP.RequireScope(domain.ScopeChat)(dummyHandler)
	agentScopedMiddleware := adaptersHTTP.RequireScope(domain.ScopeAgent)(dummyHandler)
	memoryScopedMiddleware := adaptersHTTP.RequireScope(domain.ScopeMemory)(dummyHandler)
	responsesScopedMiddleware := adaptersHTTP.RequireScope(domain.ScopeResponses)(dummyHandler)

	t.Run("ChatOnlyKey_AccessChat_Allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		id := domain.TenantIdentity{
			TenantID: "tenant-chat",
			Role:     "user",
			Scopes:   []string{domain.ScopeChat},
		}
		ctx := domain.ContextWithTenantIdentity(req.Context(), id)
		rec := httptest.NewRecorder()

		chatScopedMiddleware.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for chat scope, got %d", rec.Code)
		}
	})

	t.Run("ChatOnlyKey_AccessAgent_Forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/run", nil)
		id := domain.TenantIdentity{
			TenantID: "tenant-chat",
			Role:     "user",
			Scopes:   []string{domain.ScopeChat},
		}
		ctx := domain.ContextWithTenantIdentity(req.Context(), id)
		rec := httptest.NewRecorder()

		agentScopedMiddleware.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for missing agent scope, got %d", rec.Code)
		}
	})

	t.Run("ChatOnlyKey_AccessMemory_Forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/agent/memory/core", nil)
		id := domain.TenantIdentity{
			TenantID: "tenant-chat",
			Role:     "user",
			Scopes:   []string{domain.ScopeChat},
		}
		ctx := domain.ContextWithTenantIdentity(req.Context(), id)
		rec := httptest.NewRecorder()

		memoryScopedMiddleware.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for missing memory scope, got %d", rec.Code)
		}
	})

	t.Run("ChatOnlyKey_AccessResponses_Forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		id := domain.TenantIdentity{
			TenantID: "tenant-chat",
			Role:     "user",
			Scopes:   []string{domain.ScopeChat},
		}
		ctx := domain.ContextWithTenantIdentity(req.Context(), id)
		rec := httptest.NewRecorder()

		responsesScopedMiddleware.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for missing responses scope, got %d", rec.Code)
		}
	})

	t.Run("AdminKey_AccessAll_Allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/run", nil)
		adminID := domain.DefaultAdminIdentity()
		ctx := domain.ContextWithTenantIdentity(req.Context(), adminID)

		rec1 := httptest.NewRecorder()
		agentScopedMiddleware.ServeHTTP(rec1, req.WithContext(ctx))
		if rec1.Code != http.StatusOK {
			t.Fatalf("admin expected 200 on agent, got %d", rec1.Code)
		}

		rec2 := httptest.NewRecorder()
		memoryScopedMiddleware.ServeHTTP(rec2, req.WithContext(ctx))
		if rec2.Code != http.StatusOK {
			t.Fatalf("admin expected 200 on memory, got %d", rec2.Code)
		}
	})

	t.Run("MissingAuth_Unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()

		chatScopedMiddleware.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})
}

func TestRequireAdmin_Enforcement(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	adminMiddleware := adaptersHTTP.RequireAdmin(dummyHandler)

	t.Run("UserRole_Forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/overview", nil)
		id := domain.TenantIdentity{
			TenantID: "tenant-regular",
			Role:     "user",
			Scopes:   []string{domain.ScopeChat},
		}
		ctx := domain.ContextWithTenantIdentity(context.Background(), id)
		rec := httptest.NewRecorder()

		adminMiddleware.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for non-admin, got %d", rec.Code)
		}
	})

	t.Run("AdminRole_Allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/overview", nil)
		id := domain.DefaultAdminIdentity()
		ctx := domain.ContextWithTenantIdentity(context.Background(), id)
		rec := httptest.NewRecorder()

		adminMiddleware.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for admin, got %d", rec.Code)
		}
	})
}
