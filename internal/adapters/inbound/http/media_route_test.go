package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gatewayHttp "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/storage"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestMediaRouteAndTenantIsolation(t *testing.T) {
	tempDir := t.TempDir()
	mediaAdapter, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create media storage: %v", err)
	}

	keyRepo := session.NewMemoryKeyRepository()
	keyService := services.NewKeyService(keyRepo, "master-admin-key")

	ctx := context.Background()

	// 1. Tạo 2 virtual key cho Tenant A và Tenant B
	keyTenantA, err := keyService.CreateKey(ctx, domain.CreateKeyRequest{
		Name:     "Tenant A Key",
		TenantID: "tenant-alpha",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("failed to create key for tenant A: %v", err)
	}

	keyTenantB, err := keyService.CreateKey(ctx, domain.CreateKeyRequest{
		Name:     "Tenant B Key",
		TenantID: "tenant-beta",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("failed to create key for tenant B: %v", err)
	}

	cfg := &config.Config{
		Server: config.ServerConfig{
			AllowedOrigins: []string{"*"},
		},
	}

	router := gatewayHttp.BuildRouter(gatewayHttp.RouterDependencies{
		Config:        cfg,
		KeyUseCase:    keyService,
		MediaStorage:  mediaAdapter,
		ModelRegistry: domain.NewModelRegistry(nil),
	})

	// 2. Lưu private media asset thuộc Tenant A
	privateAssetA := &domain.MediaAsset{
		ID:        "media-asset-private-a",
		FileName:  "private_a.png",
		Kind:      domain.MediaImagePNG,
		TenantID:  "tenant-alpha",
		IsPublic:  false,
		CreatedAt: time.Now(),
	}
	if err := mediaAdapter.SaveAsset(ctx, privateAssetA, bytes.NewReader([]byte("png-content-of-tenant-a"))); err != nil {
		t.Fatalf("failed to save private asset A: %v", err)
	}

	// Lưu public media asset
	publicAsset := &domain.MediaAsset{
		ID:        "media-asset-public",
		FileName:  "public.png",
		Kind:      domain.MediaImagePNG,
		IsPublic:  true,
		CreatedAt: time.Now(),
	}
	if err := mediaAdapter.SaveAsset(ctx, publicAsset, bytes.NewReader([]byte("png-content-public"))); err != nil {
		t.Fatalf("failed to save public asset: %v", err)
	}

	// 3. Test: GET /v1/media/{id} phục vụ đúng route (không bị 404 mismatch)
	// Tenant A truy cập media của chính mình -> 200 OK
	reqA := httptest.NewRequest(http.MethodGet, "/v1/media/media-asset-private-a", nil)
	reqA.Header.Set("Authorization", "Bearer "+keyTenantA.RawKey)
	recA := httptest.NewRecorder()
	router.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for tenant A accessing own media on /v1/media/{id}, got: %d", recA.Code)
	}
	// Kiểm tra Cache-Control cho private asset
	cacheControl := recA.Header().Get("Cache-Control")
	if !bytes.Contains([]byte(cacheControl), []byte("private")) {
		t.Errorf("expected Cache-Control private for private media asset, got: %s", cacheControl)
	}

	// 4. Test: GET /media/{id} (legacy route removed for security, must return 404)
	reqRoot := httptest.NewRequest(http.MethodGet, "/media/media-asset-public", nil)
	recRoot := httptest.NewRecorder()
	router.ServeHTTP(recRoot, reqRoot)
	if recRoot.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for legacy unauthenticated /media/{id}, got: %d", recRoot.Code)
	}

	// 4b. Test: GET /v1/media/{id} canonical route for public media
	reqV1Pub := httptest.NewRequest(http.MethodGet, "/v1/media/media-asset-public", nil)
	reqV1Pub.Header.Set("Authorization", "Bearer "+keyTenantA.RawKey)
	recV1Pub := httptest.NewRecorder()
	router.ServeHTTP(recV1Pub, reqV1Pub)
	if recV1Pub.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for public media on canonical /v1/media/{id}, got: %d", recV1Pub.Code)
	}
	pubCache := recV1Pub.Header().Get("Cache-Control")
	if !bytes.Contains([]byte(pubCache), []byte("public")) {
		t.Errorf("expected Cache-Control public for public media asset, got: %s", pubCache)
	}

	// 5. Test: Tenant B cố tình đọc private media của Tenant A -> 403 Forbidden (Tenant Isolation)
	reqB := httptest.NewRequest(http.MethodGet, "/v1/media/media-asset-private-a", nil)
	reqB.Header.Set("Authorization", "Bearer "+keyTenantB.RawKey)
	recB := httptest.NewRecorder()
	router.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusForbidden {
		t.Fatalf("SECURITY VIOLATION: expected 403 Forbidden when tenant B reads tenant A's media, got: %d", recB.Code)
	}
	var errBody map[string]any
	if err := json.Unmarshal(recB.Body.Bytes(), &errBody); err == nil {
		if errObj, ok := errBody["error"].(map[string]any); ok {
			if errObj["code"] != "tenant_isolation_violation" {
				t.Errorf("expected error code 'tenant_isolation_violation', got: %v", errObj["code"])
			}
		}
	}
}
