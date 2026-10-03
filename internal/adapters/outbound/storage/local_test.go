package storage_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/storage"
	"dezuxk-gateway/internal/core/domain"
)

func TestLocalStorageAdapter_PathTraversalDefense(t *testing.T) {
	tempDir := t.TempDir()
	adapter, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create storage adapter: %v", err)
	}

	ctx := context.Background()

	// 1. Attempt path traversal in FileName
	asset := &domain.MediaAsset{
		ID:       "asset-traversal-1",
		FileName: "../../../etc/passwd",
		Kind:     domain.MediaImagePNG,
		TenantID: "tenant-a",
	}

	err = adapter.SaveAsset(ctx, asset, bytes.NewReader([]byte("dummy content")))
	if err != nil {
		t.Fatalf("unexpected error saving asset: %v", err)
	}

	// Verify that the saved file is strictly inside tempDir and has safe filename
	if !strings.HasPrefix(asset.FilePath, filepath.Clean(tempDir)) {
		t.Fatalf("SECURITY VIOLATION: asset file path escaped storageDir: %s", asset.FilePath)
	}
	if strings.Contains(asset.FileName, "..") || strings.Contains(asset.FileName, "/") || strings.Contains(asset.FileName, "\\") {
		t.Fatalf("SECURITY VIOLATION: asset FileName contains traversal characters: %s", asset.FileName)
	}
	if asset.FileName != "asset-traversal-1.png" {
		t.Fatalf("expected safe filename 'asset-traversal-1.png', got: %s", asset.FileName)
	}
}

func TestLocalStorageAdapter_MetadataPersistenceAndRestart(t *testing.T) {
	tempDir := t.TempDir()

	ctx := context.Background()

	// 1. Adapter Instance 1: Save private media asset for Tenant A
	adapter1, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create adapter1: %v", err)
	}

	assetA := &domain.MediaAsset{
		ID:        "media-asset-tenant-a",
		Kind:      domain.MediaImagePNG,
		TenantID:  "tenant-alpha",
		IsPublic:  false,
		CreatedAt: time.Now(),
	}
	contentA := []byte("secret-image-content-for-tenant-alpha")
	if err := adapter1.SaveAsset(ctx, assetA, bytes.NewReader(contentA)); err != nil {
		t.Fatalf("failed to save asset in adapter1: %v", err)
	}

	// Verify sidecar metadata file was created on disk
	metaFile := filepath.Join(tempDir, "media-asset-tenant-a.metadata.json")
	if _, err := os.Stat(metaFile); err != nil {
		t.Fatalf("expected sidecar metadata file to exist on disk: %v", err)
	}

	// 2. Restart Simulation: Create Adapter Instance 2 pointing to the same storage dir
	adapter2, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to recreate adapter2 (restart): %v", err)
	}

	// 3. Tenant B (tenant-beta) tries to access Tenant A's private asset -> 403 Forbidden
	reqB := httptest.NewRequest(http.MethodGet, "/v1/media/media-asset-tenant-a", nil)
	ctxB := domain.ContextWithTenantIdentity(reqB.Context(), domain.TenantIdentity{
		TenantID: "tenant-beta",
		Role:     "user",
	})
	reqB = reqB.WithContext(ctxB)
	recB := httptest.NewRecorder()

	errB := adapter2.ServeAssetHTTP(recB, reqB, "media-asset-tenant-a")
	if errB == nil || recB.Code != http.StatusForbidden {
		t.Fatalf("SECURITY VIOLATION after restart: expected 403 Forbidden for Tenant B, got %d (err: %v)", recB.Code, errB)
	}

	// 4. Tenant A (tenant-alpha) accesses own private asset -> 200 OK
	reqA := httptest.NewRequest(http.MethodGet, "/v1/media/media-asset-tenant-a", nil)
	ctxA := domain.ContextWithTenantIdentity(reqA.Context(), domain.TenantIdentity{
		TenantID: "tenant-alpha",
		Role:     "user",
	})
	reqA = reqA.WithContext(ctxA)
	recA := httptest.NewRecorder()

	errA := adapter2.ServeAssetHTTP(recA, reqA, "media-asset-tenant-a")
	if errA != nil || recA.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Tenant A after restart, got %d (err: %v)", recA.Code, errA)
	}
	if recA.Body.String() != string(contentA) {
		t.Fatalf("content mismatch after restart: expected %s, got %s", string(contentA), recA.Body.String())
	}
}

func TestLocalStorageAdapter_FailClosedWhenMetadataMissing(t *testing.T) {
	tempDir := t.TempDir()

	// Create a raw file directly on disk without sidecar metadata
	orphanID := "orphan-raw-file"
	rawPath := filepath.Join(tempDir, orphanID+".png")
	if err := os.WriteFile(rawPath, []byte("raw-untracked-content"), 0644); err != nil {
		t.Fatalf("failed to create orphan raw file: %v", err)
	}

	adapter, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create storage adapter: %v", err)
	}

	// Any tenant trying to request this orphan file lacking metadata must FAIL CLOSED
	req := httptest.NewRequest(http.MethodGet, "/v1/media/"+orphanID, nil)
	ctx := domain.ContextWithTenantIdentity(req.Context(), domain.TenantIdentity{
		TenantID: "any-tenant",
		Role:     "user",
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	err = adapter.ServeAssetHTTP(rec, req, orphanID)
	// Must fail closed with 404 Not Found (or error) and never serve the file
	if err == nil || rec.Code == http.StatusOK {
		t.Fatalf("SECURITY VIOLATION: orphan file without metadata was served! code: %d, err: %v", rec.Code, err)
	}
}

func TestLocalStorageAdapter_SSRFProtection(t *testing.T) {
	tempDir := t.TempDir()
	adapter, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create storage adapter: %v", err)
	}

	ctx := context.Background()

	// 1. Loopback SSRF
	_, err = adapter.DownloadAndCacheWithAuth(ctx, "http://127.0.0.1:8080/secret.png", domain.MediaImagePNG, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected SSRF error for loopback 127.0.0.1, got: %v", err)
	}

	// 2. Localhost SSRF
	_, err = adapter.DownloadAndCacheWithAuth(ctx, "http://localhost:9090/secret.png", domain.MediaImagePNG, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected SSRF error for localhost, got: %v", err)
	}

	// 3. Cloud Metadata SSRF
	_, err = adapter.DownloadAndCacheWithAuth(ctx, "http://169.254.169.254/latest/meta-data/", domain.MediaImagePNG, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected SSRF error for link-local metadata, got: %v", err)
	}

	// 4. Non-http/https protocol
	_, err = adapter.DownloadAndCacheWithAuth(ctx, "file:///etc/passwd", domain.MediaImagePNG, "", "", "", "")
	if err == nil {
		t.Fatalf("expected error for file:// protocol, got nil")
	}
}

func TestLocalStorageAdapter_CorruptedMetadataFailsClosed(t *testing.T) {
	tempDir := t.TempDir()

	assetID := "corrupted-meta-asset"
	mediaPath := filepath.Join(tempDir, assetID+".png")
	if err := os.WriteFile(mediaPath, []byte("valid-image-bytes"), 0644); err != nil {
		t.Fatalf("failed to create media file: %v", err)
	}

	// Write corrupted JSON metadata
	metaPath := filepath.Join(tempDir, assetID+".metadata.json")
	if err := os.WriteFile(metaPath, []byte("{ this is not valid json! }"), 0644); err != nil {
		t.Fatalf("failed to write corrupted metadata: %v", err)
	}

	adapter, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to initialize adapter: %v", err)
	}

	// 1. GetAsset must fail closed
	_, _, err = adapter.GetAsset(context.Background(), assetID)
	if err == nil {
		t.Fatalf("expected error on corrupted metadata, got nil")
	}
	if !strings.Contains(err.Error(), "fail closed") && !strings.Contains(err.Error(), "hỏng") {
		t.Fatalf("expected fail closed error message, got: %v", err)
	}

	// 2. ServeAssetHTTP must fail closed (404 Not Found)
	req := httptest.NewRequest(http.MethodGet, "/v1/media/"+assetID, nil)
	rec := httptest.NewRecorder()
	err = adapter.ServeAssetHTTP(rec, req, assetID)
	if err == nil || rec.Code == http.StatusOK {
		t.Fatalf("SECURITY VIOLATION: served file with corrupted metadata! code: %d, err: %v", rec.Code, err)
	}
}

func TestLocalStorageAdapter_MetadataWriteFailureCleansUpMediaFile(t *testing.T) {
	tempDir := t.TempDir()
	adapter, err := storage.NewLocalStorageAdapter(tempDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	assetID := "fail-write-metadata-asset"

	// Block metadata writing by creating a directory where the temp metadata file is supposed to be created
	tmpMetaDir := filepath.Join(tempDir, assetID+".metadata.json.tmp")
	if err := os.Mkdir(tmpMetaDir, 0755); err != nil {
		t.Fatalf("failed to create blocking directory: %v", err)
	}

	asset := &domain.MediaAsset{
		ID:        assetID,
		FileName:  assetID + ".png",
		Kind:      domain.MediaImagePNG,
		TenantID:  "tenant-alpha",
		IsPublic:  false,
		CreatedAt: time.Now(),
	}

	content := []byte("image-data-that-should-be-cleaned-up")
	err = adapter.SaveAsset(context.Background(), asset, bytes.NewReader(content))
	if err == nil {
		t.Fatalf("expected SaveAsset to fail due to blocked metadata file, got nil")
	}

	// Verify that NO orphan media binary file was left on disk
	mediaFile := filepath.Join(tempDir, asset.FileName)
	if _, statErr := os.Stat(mediaFile); statErr == nil {
		t.Fatalf("SECURITY VIOLATION: orphan media file %s was left on disk after metadata write failure!", mediaFile)
	}
}
