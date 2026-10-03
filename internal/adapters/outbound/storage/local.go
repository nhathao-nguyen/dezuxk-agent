package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

type LocalStorageAdapter struct {
	mu         sync.RWMutex
	storageDir string
	baseURL    string
	assets     map[string]*domain.MediaAsset
	httpClient *http.Client
}

func NewLocalStorageAdapter(storageDir, baseURL string) (*LocalStorageAdapter, error) {
	if storageDir == "" {
		storageDir = filepath.Join("storage", "media")
	}
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("không thể khởi tạo thư mục lưu trữ media %s: %w", storageDir, err)
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &LocalStorageAdapter{
		storageDir: storageDir,
		baseURL:    baseURL,
		assets:     make(map[string]*domain.MediaAsset),
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func generateAssetID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *LocalStorageAdapter) SaveAsset(ctx context.Context, asset *domain.MediaAsset, content io.Reader) error {
	if asset == nil {
		return fmt.Errorf("asset không được để nil")
	}
	if asset.ID == "" {
		asset.ID = generateAssetID()
	}
	if asset.TenantID == "" {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
			asset.TenantID = id.TenantID
		}
	}
	if asset.FileName == "" {
		ext := ".bin"
		if strings.Contains(string(asset.Kind), "png") {
			ext = ".png"
		} else if strings.Contains(string(asset.Kind), "mp4") {
			ext = ".mp4"
		} else if strings.Contains(string(asset.Kind), "wav") {
			ext = ".wav"
		}
		asset.FileName = asset.ID + ext
	}

	targetPath := filepath.Join(s.storageDir, asset.FileName)
	f, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("lỗi tạo file media %s: %w", targetPath, err)
	}
	defer f.Close()

	n, err := io.Copy(f, content)
	if err != nil {
		return fmt.Errorf("lỗi ghi nội dung media: %w", err)
	}

	asset.FilePath = targetPath
	asset.SizeBytes = n
	asset.IsReady = true
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now()
	}
	if s.baseURL != "" {
		asset.LocalURL = fmt.Sprintf("%s/v1/media/%s", s.baseURL, asset.ID)
	}

	s.mu.Lock()
	s.assets[asset.ID] = asset
	s.mu.Unlock()
	return nil
}

func (s *LocalStorageAdapter) GetAsset(ctx context.Context, assetID string) (*domain.MediaAsset, io.ReadCloser, error) {
	s.mu.RLock()
	asset, exists := s.assets[assetID]
	s.mu.RUnlock()

	if !exists {
		// Fallback kiểm tra file trên đĩa
		matches, err := filepath.Glob(filepath.Join(s.storageDir, assetID+".*"))
		if err == nil && len(matches) > 0 {
			f, fErr := os.Open(matches[0])
			if fErr == nil {
				stat, _ := f.Stat()
				asset = &domain.MediaAsset{
					ID:        assetID,
					FileName:  filepath.Base(matches[0]),
					FilePath:  matches[0],
					SizeBytes: stat.Size(),
					IsReady:   true,
					CreatedAt: stat.ModTime(),
				}
				s.mu.Lock()
				s.assets[assetID] = asset
				s.mu.Unlock()
				return asset, f, nil
			}
		}
		return nil, nil, fmt.Errorf("không tìm thấy asset media với ID %s", assetID)
	}

	f, err := os.Open(asset.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("không thể mở file asset %s: %w", asset.FilePath, err)
	}
	return asset, f, nil
}

func (s *LocalStorageAdapter) ListAssets(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*domain.MediaAsset
	for _, a := range s.assets {
		if kind == "" || a.Kind == kind {
			list = append(list, a)
		}
	}
	return list, nil
}

func (s *LocalStorageAdapter) DeleteExpired(ctx context.Context, maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	for id, a := range s.assets {
		if a.CreatedAt.Before(cutoff) {
			_ = os.Remove(a.FilePath)
			delete(s.assets, id)
			deleted++
		}
	}
	return deleted, nil
}

func (s *LocalStorageAdapter) DownloadAndCache(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string) (*domain.MediaAsset, error) {
	return s.DownloadAndCacheWithAuth(ctx, remoteURL, kind, prompt, model, "", "")
}

func (s *LocalStorageAdapter) DownloadAndCacheWithAuth(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string, cookies string, userAgent string) (*domain.MediaAsset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return nil, err
	}
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lỗi tải media từ %s: %w", remoteURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tải media thất bại với HTTP status: %d", resp.StatusCode)
	}

	asset := &domain.MediaAsset{
		ID:          generateAssetID(),
		OriginalURL: remoteURL,
		Kind:        kind,
		Prompt:      prompt,
		Model:       model,
		CreatedAt:   time.Now(),
	}
	if err := s.SaveAsset(ctx, asset, resp.Body); err != nil {
		return nil, err
	}
	return asset, nil
}

func (s *LocalStorageAdapter) ServeAssetHTTP(w http.ResponseWriter, r *http.Request, assetID string) error {
	asset, reader, err := s.GetAsset(r.Context(), assetID)
	if err != nil {
		http.NotFound(w, r)
		return err
	}
	defer reader.Close()

	// Tenant ownership verification for private media assets
	if !asset.IsPublic && asset.TenantID != "" {
		callerID, ok := domain.TenantIdentityFromContext(r.Context())
		if !ok || (callerID.Role != "admin" && callerID.TenantID != asset.TenantID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Forbidden: unauthorized access to private media asset",
					"type":    "permission_denied",
					"code":    "tenant_isolation_violation",
				},
			})
			return fmt.Errorf("forbidden: unauthorized access to private media asset %s", assetID)
		}
	}

	if string(asset.Kind) != "" {
		w.Header().Set("Content-Type", string(asset.Kind))
	}
	if asset.IsPublic {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache, no-store, must-revalidate")
	}
	if file, ok := reader.(*os.File); ok {
		http.ServeContent(w, r, asset.FileName, asset.CreatedAt, file)
		return nil
	}
	_, err = io.Copy(w, reader)
	return err
}
