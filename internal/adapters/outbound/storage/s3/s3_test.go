package s3_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	pgstorage "dezuxk-gateway/internal/adapters/outbound/storage/postgres"
	s3storage "dezuxk-gateway/internal/adapters/outbound/storage/s3"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// TestS3_ConfigValidation kiểm tra xác thực cấu hình lưu trữ S3 / MinIO
func TestS3_ConfigValidation(t *testing.T) {
	// Thiếu bucket bắt buộc
	cfg := config.S3MediaConfig{
		Bucket:    "",
		Endpoint:  "http://localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}

	_, err := s3storage.NewS3StorageAdapter(cfg, "http://localhost:8080", nil)
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi khi cấu hình S3 thiếu bucket, nhưng khởi tạo thành công")
	}

	// Thiếu metadata repository
	cfgValid := config.S3MediaConfig{
		Bucket:    "my-bucket",
		Endpoint:  "http://localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	_, err = s3storage.NewS3StorageAdapter(cfgValid, "http://localhost:8080", nil)
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi khi metadata repository cho S3 nil, nhưng khởi tạo thành công")
	}
}

// TestS3_LiveIntegration chạy khi có cấu hình MinIO/S3 và PostgreSQL thực tế
func TestS3_LiveIntegration(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("Bỏ qua kiểm thử S3 thực tế vì TEST_S3_ENDPOINT không được thiết lập")
	}

	pgDSN := os.Getenv("TEST_POSTGRES_DSN")
	if pgDSN == "" {
		t.Fatalf("TEST_S3_ENDPOINT được thiết lập nhưng TEST_POSTGRES_DSN không có để lưu metadata")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Khởi tạo real PostgreSQL metadata repo
	pgCfg := config.PostgresConfig{
		DSN:      pgDSN,
		MaxConns: 5,
	}
	pool, err := pgstorage.NewPool(ctx, pgCfg)
	if err != nil {
		t.Fatalf("Kết nối PostgreSQL cho S3 metadata thất bại: %v", err)
	}
	defer pool.Close()

	if err := pgstorage.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("Chạy migrations PostgreSQL cho S3 thất bại: %v", err)
	}

	metaRepo, err := pgstorage.NewPostgresMediaMetadataRepository(pool)
	if err != nil {
		t.Fatalf("Khởi tạo PostgresMediaMetadataRepository thất bại: %v", err)
	}

	// 2. Khởi tạo S3/MinIO Adapter thật
	bucket := os.Getenv("TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "dezuxk-test"
	}
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "minioadmin"
	}
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if secretKey == "" {
		secretKey = "minioadmin"
	}

	cfg := config.S3MediaConfig{
		Bucket:       bucket,
		Endpoint:     endpoint,
		Region:       "us-east-1",
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		UsePathStyle: true,
	}

	adapter, err := s3storage.NewS3StorageAdapter(cfg, "http://localhost:8080", metaRepo)
	if err != nil {
		t.Fatalf("Khởi tạo S3 adapter thất bại: %v", err)
	}

	if err := adapter.Ping(ctx); err != nil {
		t.Fatalf("Ping MinIO bucket (%s) thất bại: %v", bucket, err)
	}

	// 3. SaveAsset cho Tenant A
	assetID_A := "live-asset-" + time.Now().Format("150405.000000")
	dataA := []byte("DEZUXK_MINIO_TEST_BINARY_PAYLOAD_FOR_TENANT_A")
	assetA := &domain.MediaAsset{
		ID:        assetID_A,
		TenantID:  "tenant-s3-a",
		FileName:  "diagram.png",
		Kind:      domain.MediaImagePNG,
		IsPublic:  false,
		CreatedAt: time.Now(),
	}

	if err := adapter.SaveAsset(ctx, assetA, bytes.NewReader(dataA)); err != nil {
		t.Fatalf("SaveAsset cho Tenant A thất bại: %v", err)
	}

	// 4. GetAsset đọc lại từ S3 + Postgres
	gotAssetA, streamA, err := adapter.GetAsset(ctx, assetID_A)
	if err != nil {
		t.Fatalf("GetAsset thất bại: %v", err)
	}
	defer streamA.Close()

	readBytesA, err := io.ReadAll(streamA)
	if err != nil {
		t.Fatalf("Đọc stream asset A thất bại: %v", err)
	}
	if !bytes.Equal(readBytesA, dataA) {
		t.Fatalf("Dữ liệu nhị phân đọc từ S3 không khớp nội dung gốc")
	}
	if gotAssetA.TenantID != "tenant-s3-a" || gotAssetA.Kind != domain.MediaImagePNG {
		t.Fatalf("Metadata đọc lại không khớp: tenant=%s, kind=%s", gotAssetA.TenantID, gotAssetA.Kind)
	}

	// 5. Tenant Isolation test: Tenant B lưu asset
	assetID_B := "live-asset-b-" + time.Now().Format("150405.000000")
	dataB := []byte("PRIVATE_TENANT_B_DATA")
	assetB := &domain.MediaAsset{
		ID:        assetID_B,
		TenantID:  "tenant-s3-b",
		FileName:  "private_report.png",
		Kind:      domain.MediaImagePNG,
		IsPublic:  false,
		CreatedAt: time.Now(),
	}
	if err := adapter.SaveAsset(ctx, assetB, bytes.NewReader(dataB)); err != nil {
		t.Fatalf("SaveAsset cho Tenant B thất bại: %v", err)
	}

	// Metadata của asset B phải phân tách độc lập
	gotAssetB, streamB, err := adapter.GetAsset(ctx, assetID_B)
	if err != nil {
		t.Fatalf("GetAsset B thất bại: %v", err)
	}
	defer streamB.Close()
	if gotAssetB.TenantID != "tenant-s3-b" {
		t.Fatalf("Vi phạm tenant isolation: assetB có tenant=%s", gotAssetB.TenantID)
	}

	// 6. ListAssets
	list, err := adapter.ListAssets(ctx, domain.MediaImagePNG)
	if err != nil {
		t.Fatalf("ListAssets thất bại: %v", err)
	}
	foundA := false
	for _, it := range list {
		if it.ID == assetID_A {
			foundA = true
			break
		}
	}
	if !foundA {
		t.Fatalf("ListAssets không chứa asset A vừa tạo")
	}

	// 7. DeleteAsset
	if err := adapter.DeleteAsset(ctx, assetID_A); err != nil {
		t.Fatalf("DeleteAsset thất bại: %v", err)
	}

	// Sau khi xóa, GetAsset phải trả về lỗi không tìm thấy
	_, _, err = adapter.GetAsset(ctx, assetID_A)
	if err == nil {
		t.Fatalf("Kỳ vọng lỗi khi GetAsset sau khi DeleteAsset, nhưng thành công")
	}
}
