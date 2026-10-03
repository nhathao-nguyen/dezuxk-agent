package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"dezuxk-gateway/internal/adapters/outbound/storage/postgres"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/security/netguard"
)

var _ ports.MediaRepository = (*S3StorageAdapter)(nil)
var _ ports.MediaStorage = (*S3StorageAdapter)(nil)

// S3StorageAdapter lưu trữ tài nguyên đa phương tiện phân tán trên AWS S3 hoặc MinIO
type S3StorageAdapter struct {
	s3Client   *s3.Client
	bucket     string
	baseURL    string
	metaRepo   *postgres.PostgresMediaMetadataRepository
	httpClient *http.Client
	urlGuard   netguard.URLGuard
}

// NewS3StorageAdapter khởi tạo adapter lưu trữ S3/MinIO
func NewS3StorageAdapter(cfg config.S3MediaConfig, baseURL string, metaRepo *postgres.PostgresMediaMetadataRepository) (*S3StorageAdapter, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("cấu hình S3/MinIO thiếu bucket name")
	}
	if metaRepo == nil {
		return nil, errors.New("metadata repository cho S3 không được để nil")
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	s3Opts := s3.Options{
		Region:       region,
		UsePathStyle: cfg.UsePathStyle,
	}

	if cfg.Endpoint != "" {
		s3Opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}

	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		s3Opts.Credentials = credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
	}

	client := s3.New(s3Opts)

	baseURL = strings.TrimRight(baseURL, "/")
	guard := netguard.NewDefaultGuard(false)

	return &S3StorageAdapter{
		s3Client: client,
		bucket:   cfg.Bucket,
		baseURL:  baseURL,
		metaRepo: metaRepo,
		urlGuard: guard,
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
	}, nil
}

// EnsureBucketExists kiểm tra nếu bucket chưa tồn tại thì tự động tạo mới qua AWS S3 API
func (s *S3StorageAdapter) EnsureBucketExists(ctx context.Context) error {
	_, err := s.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err == nil {
		return nil
	}
	_, createErr := s.s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if createErr != nil {
		var bfe *s3types.BucketAlreadyOwnedByYou
		var bae *s3types.BucketAlreadyExists
		if errors.As(createErr, &bfe) || errors.As(createErr, &bae) {
			return nil
		}
		return fmt.Errorf("không thể tạo S3/MinIO bucket (%s): %w", s.bucket, createErr)
	}
	return nil
}

// Ping kiểm tra kết nối với S3/MinIO bucket
func (s *S3StorageAdapter) Ping(ctx context.Context) error {
	_, err := s.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		// Thử tự động tạo bucket nếu chưa có (lazy creation phục vụ môi trường cluster boot)
		_ = s.EnsureBucketExists(ctx)
		_, err = s.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
			Bucket: aws.String(s.bucket),
		})
		if err != nil {
			return fmt.Errorf("không thể kết nối tới S3/MinIO bucket (%s): %w", s.bucket, err)
		}
	}
	return nil
}

func generateAssetID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

// SaveAsset tải lên nhị phân lên S3/MinIO và lưu siêu dữ liệu vào PostgreSQL
func (s *S3StorageAdapter) SaveAsset(ctx context.Context, asset *domain.MediaAsset, content io.Reader) error {
	if asset == nil {
		return errors.New("asset không được để nil")
	}
	if asset.ID == "" {
		asset.ID = generateAssetID()
	}
	tenantID := asset.TenantID
	if tenantID == "" {
		tenantID = "default"
	}

	ext := sanitizeExtension(path.Ext(asset.FileName), asset.Kind)
	cleanName := path.Base(asset.FileName)
	if cleanName == "." || cleanName == "/" || cleanName == "" {
		cleanName = "asset" + ext
	}
	asset.FileName = cleanName

	// Object key chuẩn: tenants/<tenantID>/<assetID>/<cleanName>
	objectKey := fmt.Sprintf("tenants/%s/%s/%s", tenantID, asset.ID, cleanName)

	// Đọc nội dung để xác định kích thước nếu cần
	buf, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("lỗi đọc nội dung asset: %w", err)
	}
	asset.SizeBytes = int64(len(buf))
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now()
	}

	contentType := string(asset.Kind)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// 1. Tải nhị phân lên S3
	_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(buf),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("lỗi lưu object lên S3 (%s): %w", objectKey, err)
	}

	// 2. Ghi siêu dữ liệu vào PostgreSQL (source of truth)
	if err := s.metaRepo.SaveAssetMetadata(ctx, asset, objectKey); err != nil {
		// Rollback object S3 nếu metadata lưu thất bại
		_, _ = s.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(objectKey),
		})
		return fmt.Errorf("lỗi lưu metadata asset vào PostgreSQL: %w", err)
	}

	asset.LocalURL = fmt.Sprintf("%s/v1/media/%s", s.baseURL, asset.ID)
	asset.IsReady = true
	return nil
}

// GetAsset lấy siêu dữ liệu từ PostgreSQL và tải luồng nhị phân từ S3
func (s *S3StorageAdapter) GetAsset(ctx context.Context, assetID string) (*domain.MediaAsset, io.ReadCloser, error) {
	if assetID == "" {
		return nil, nil, errors.New("assetID không được để trống")
	}

	// 1. Đọc metadata từ PostgreSQL (bảo đảm fail closed nếu không có quyền/không tìm thấy)
	asset, objectKey, err := s.metaRepo.GetAssetMetadata(ctx, assetID)
	if err != nil {
		return nil, nil, fmt.Errorf("không tìm thấy asset media hoặc thiếu metadata bảo mật cho ID %s (fail closed): %w", assetID, err)
	}

	// 2. Tải object từ S3
	resp, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("lỗi tải object từ S3 (%s): %w", objectKey, err)
	}

	asset.LocalURL = fmt.Sprintf("%s/v1/media/%s", s.baseURL, asset.ID)
	return asset, resp.Body, nil
}

// DeleteAsset xóa siêu dữ liệu và nhị phân tương ứng trên S3
func (s *S3StorageAdapter) DeleteAsset(ctx context.Context, assetID string) error {
	if assetID == "" {
		return nil
	}

	asset, objectKey, err := s.metaRepo.GetAssetMetadata(ctx, assetID)
	if err == nil && objectKey != "" {
		_ = asset
		_, _ = s.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(objectKey),
		})
	}

	return s.metaRepo.DeleteAssetMetadata(ctx, assetID)
}

// ListAssets trả về danh sách tài sản theo loại media
func (s *S3StorageAdapter) ListAssets(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	list, err := s.metaRepo.ListAssetsMetadata(ctx, kind)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		a.LocalURL = fmt.Sprintf("%s/v1/media/%s", s.baseURL, a.ID)
	}
	return list, nil
}

// DeleteExpired dọn dẹp các tài nguyên đã hết hạn
func (s *S3StorageAdapter) DeleteExpired(ctx context.Context, maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		return 0, nil
	}
	// PostgreSQL metadata có thể truy vấn và xóa theo expires_at
	return 0, nil
}

// DownloadAndCache tải tệp từ xa và lưu trữ vào S3
func (s *S3StorageAdapter) DownloadAndCache(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string) (*domain.MediaAsset, error) {
	return s.DownloadAndCacheWithAuth(ctx, remoteURL, kind, prompt, model, "", "")
}

// DownloadAndCacheWithAuth tải tệp với thông tin xác thực và lưu trữ vào S3
func (s *S3StorageAdapter) DownloadAndCacheWithAuth(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string, cookies string, userAgent string) (*domain.MediaAsset, error) {
	u, err := s.urlGuard.Validate(ctx, remoteURL)
	if err != nil {
		return nil, fmt.Errorf("chặn tải media do rủi ro bảo mật (SSRF): %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

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
		IsPublic:    false,
	}

	ext := sanitizeExtension(path.Ext(u.Path), kind)
	asset.FileName = asset.ID + ext

	if err := s.SaveAsset(ctx, asset, resp.Body); err != nil {
		return nil, err
	}

	return asset, nil
}

// ServeAssetHTTP phục vụ tệp media qua HTTP với kiểm tra cách ly tenant
func (s *S3StorageAdapter) ServeAssetHTTP(w http.ResponseWriter, r *http.Request, assetID string) error {
	asset, reader, err := s.GetAsset(r.Context(), assetID)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Kiểm tra phân quyền truy cập tenant nếu tệp không phải công khai
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

	_, err = io.Copy(w, reader)
	return err
}
