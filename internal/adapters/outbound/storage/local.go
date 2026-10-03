package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/security/netguard"
)

type mediaAssetMetadata struct {
	AssetID     string           `json:"asset_id"`
	TenantID    string           `json:"tenant_id"`
	IsPublic    bool             `json:"is_public"`
	FileName    string           `json:"filename"`
	Kind        domain.MediaKind `json:"kind"`
	CreatedAt   time.Time        `json:"created_at"`
	SizeBytes   int64            `json:"size"`
	OriginalURL string           `json:"original_url,omitempty"`
	Prompt      string           `json:"prompt,omitempty"`
	Model       string           `json:"model,omitempty"`
}

type LocalStorageAdapter struct {
	mu         sync.RWMutex
	storageDir string
	baseURL    string
	assets     map[string]*domain.MediaAsset
	httpClient *http.Client
	urlGuard   netguard.URLGuard
}

func sanitizeExtension(rawExt string, kind domain.MediaKind) string {
	ext := strings.ToLower(strings.TrimSpace(rawExt))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp4", ".webm", ".wav", ".mp3", ".ogg", ".bin":
		return ext
	}
	switch {
	case strings.Contains(string(kind), "png"):
		return ".png"
	case strings.Contains(string(kind), "jpeg") || strings.Contains(string(kind), "jpg"):
		return ".jpg"
	case strings.Contains(string(kind), "webp"):
		return ".webp"
	case strings.Contains(string(kind), "gif"):
		return ".gif"
	case strings.Contains(string(kind), "mp4"):
		return ".mp4"
	case strings.Contains(string(kind), "webm"):
		return ".webm"
	case strings.Contains(string(kind), "wav"):
		return ".wav"
	case strings.Contains(string(kind), "ogg"):
		return ".ogg"
	case strings.Contains(string(kind), "mp3"):
		return ".mp3"
	default:
		return ".bin"
	}
}

func isTrustedGoogleHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	trustedDomains := []string{
		"google.com",
		"googleapis.com",
		"googleusercontent.com",
		"gstatic.com",
		"deepmind.com",
		"gemini.google.com",
	}
	for _, d := range trustedDomains {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

func validateSafeMediaURL(rawURL string) (*url.URL, error) {
	guard := netguard.NewDefaultGuard(false)
	return guard.Validate(context.Background(), rawURL)
}

func NewLocalStorageAdapter(storageDir, baseURL string) (*LocalStorageAdapter, error) {
	if storageDir == "" {
		storageDir = filepath.Join("storage", "media")
	}
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("không thể khởi tạo thư mục lưu trữ media %s: %w", storageDir, err)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	guard := netguard.NewDefaultGuard(false)
	adapter := &LocalStorageAdapter{
		storageDir: storageDir,
		baseURL:    baseURL,
		assets:     make(map[string]*domain.MediaAsset),
		urlGuard:   guard,
		httpClient: netguard.NewSafeHTTPClient(netguard.SafeHTTPConfig{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if !isTrustedGoogleHost(req.URL.Hostname()) {
					req.Header.Del("Cookie")
					req.Header.Del("Authorization")
				}
				return nil
			},
		}),
	}

	// Nạp trước các asset metadata bền vững đã lưu từ trước (sidecar metadata)
	entries, err := os.ReadDir(storageDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".metadata.json") {
				continue
			}
			metaPath := filepath.Join(storageDir, entry.Name())
			data, readErr := os.ReadFile(metaPath)
			if readErr != nil {
				continue
			}
			var meta mediaAssetMetadata
			if jsonErr := json.Unmarshal(data, &meta); jsonErr != nil || meta.AssetID == "" {
				continue
			}
			filePath := filepath.Join(storageDir, meta.FileName)
			if _, statErr := os.Stat(filePath); statErr == nil {
				asset := &domain.MediaAsset{
					ID:          meta.AssetID,
					TenantID:    meta.TenantID,
					IsPublic:    meta.IsPublic,
					FileName:    meta.FileName,
					FilePath:    filePath,
					Kind:        meta.Kind,
					SizeBytes:   meta.SizeBytes,
					IsReady:     true,
					CreatedAt:   meta.CreatedAt,
					OriginalURL: meta.OriginalURL,
					Prompt:      meta.Prompt,
					Model:       meta.Model,
				}
				if baseURL != "" {
					asset.LocalURL = fmt.Sprintf("%s/v1/media/%s", baseURL, asset.ID)
				}
				adapter.assets[asset.ID] = asset
			}
		}
	}

	return adapter, nil
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
	// Sanitize asset.ID: chỉ cho phép ký tự an toàn
	cleanID := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return -1
	}, asset.ID)
	if cleanID == "" {
		cleanID = generateAssetID()
	}
	asset.ID = cleanID

	if asset.TenantID == "" {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
			asset.TenantID = id.TenantID
		}
	}

	// Tạo tên file an toàn từ ID + phần mở rộng chuẩn hóa (chống path traversal triệt để)
	safeExt := sanitizeExtension(filepath.Ext(asset.FileName), asset.Kind)
	asset.FileName = asset.ID + safeExt

	cleanStorageDir := filepath.Clean(s.storageDir)
	targetPath := filepath.Clean(filepath.Join(cleanStorageDir, asset.FileName))
	rel, err := filepath.Rel(cleanStorageDir, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("path traversal attempt detected: invalid filename %s", asset.FileName)
	}

	f, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("lỗi tạo file media %s: %w", targetPath, err)
	}

	n, copyErr := io.Copy(f, content)
	if copyErr != nil {
		_ = f.Close()
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi ghi nội dung media: %w", copyErr)
	}

	if syncErr := f.Sync(); syncErr != nil {
		_ = f.Close()
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi fsync file media %s: %w", targetPath, syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi đóng file media %s: %w", targetPath, closeErr)
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

	// Lưu sidecar metadata bền vững và nguyên tử (atomic persistence) ra đĩa
	meta := mediaAssetMetadata{
		AssetID:     asset.ID,
		TenantID:    asset.TenantID,
		IsPublic:    asset.IsPublic,
		FileName:    asset.FileName,
		Kind:        asset.Kind,
		CreatedAt:   asset.CreatedAt,
		SizeBytes:   n,
		OriginalURL: asset.OriginalURL,
		Prompt:      asset.Prompt,
		Model:       asset.Model,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi marshal metadata cho asset %s: %w", asset.ID, err)
	}

	tmpMetaPath := filepath.Join(cleanStorageDir, asset.ID+".metadata.json.tmp")
	finalMetaPath := filepath.Join(cleanStorageDir, asset.ID+".metadata.json")

	fMeta, err := os.OpenFile(tmpMetaPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi tạo file tạm metadata %s: %w", tmpMetaPath, err)
	}

	if _, err := fMeta.Write(metaBytes); err != nil {
		_ = fMeta.Close()
		_ = os.Remove(tmpMetaPath)
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi ghi nội dung metadata tạm: %w", err)
	}

	if err := fMeta.Sync(); err != nil {
		_ = fMeta.Close()
		_ = os.Remove(tmpMetaPath)
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi fsync metadata tạm: %w", err)
	}

	if err := fMeta.Close(); err != nil {
		_ = os.Remove(tmpMetaPath)
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi đóng file metadata tạm: %w", err)
	}

	if err := os.Rename(tmpMetaPath, finalMetaPath); err != nil {
		_ = os.Remove(tmpMetaPath)
		_ = os.Remove(targetPath)
		return fmt.Errorf("lỗi atomic rename metadata %s -> %s: %w", tmpMetaPath, finalMetaPath, err)
	}

	// Đồng bộ thư mục cha trên các hệ thống tệp hỗ trợ
	if dirF, err := os.Open(cleanStorageDir); err == nil {
		_ = dirF.Sync()
		_ = dirF.Close()
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
		// Kiểm tra sidecar metadata trên đĩa (khôi phục sau restart)
		metaPath := filepath.Join(s.storageDir, assetID+".metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			// Nguyên tắc bảo mật: missing security metadata => FAIL CLOSED!
			return nil, nil, fmt.Errorf("không tìm thấy asset media hoặc thiếu metadata bảo mật cho ID %s (fail closed): %w", assetID, err)
		}
		var meta mediaAssetMetadata
		if jsonErr := json.Unmarshal(data, &meta); jsonErr != nil || meta.AssetID != assetID || meta.FileName == "" {
			// Nguyên tắc bảo mật: corrupted metadata => FAIL CLOSED!
			return nil, nil, fmt.Errorf("metadata bảo mật bị hỏng hoặc không hợp lệ cho ID %s (fail closed)", assetID)
		}
		filePath := filepath.Join(s.storageDir, meta.FileName)
		f, fErr := os.Open(filePath)
		if fErr != nil {
			return nil, nil, fmt.Errorf("không thể mở file asset tương ứng với metadata %s: %w", filePath, fErr)
		}
		asset = &domain.MediaAsset{
			ID:          meta.AssetID,
			TenantID:    meta.TenantID,
			IsPublic:    meta.IsPublic,
			FileName:    meta.FileName,
			FilePath:    filePath,
			Kind:        meta.Kind,
			SizeBytes:   meta.SizeBytes,
			IsReady:     true,
			CreatedAt:   meta.CreatedAt,
			OriginalURL: meta.OriginalURL,
			Prompt:      meta.Prompt,
			Model:       meta.Model,
		}
		if s.baseURL != "" {
			asset.LocalURL = fmt.Sprintf("%s/v1/media/%s", s.baseURL, asset.ID)
		}
		s.mu.Lock()
		s.assets[assetID] = asset
		s.mu.Unlock()
		return asset, f, nil
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
			_ = os.Remove(filepath.Join(s.storageDir, id+".metadata.json"))
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
	u, err := validateSafeMediaURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("chặn tải media do rủi ro bảo mật (SSRF): %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	// Chỉ gửi Gemini/Google cookies tới các host tin cậy thuộc allowlist của Google
	if cookies != "" && isTrustedGoogleHost(u.Hostname()) {
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
	// Nguyên tắc bảo mật: Không có metadata hoặc TenantID rỗng trên private asset => FAIL CLOSED!
	if !asset.IsPublic {
		callerID, ok := domain.TenantIdentityFromContext(r.Context())
		if !ok || callerID.TenantID == "" || (callerID.Role != "admin" && callerID.TenantID != asset.TenantID) {
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
