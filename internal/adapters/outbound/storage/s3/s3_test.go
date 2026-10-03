package s3_test

import (
	"context"
	"os"
	"testing"
	"time"

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

// TestS3_LiveIntegration chạy khi có cấu hình MinIO/S3 môi trường kiểm thử
func TestS3_LiveIntegration(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("Bỏ qua kiểm thử S3 thực tế vì TEST_S3_ENDPOINT không được thiết lập")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := config.S3MediaConfig{
		Bucket:       "dezuxk-test",
		Endpoint:     endpoint,
		Region:       "us-east-1",
		AccessKey:    "minioadmin",
		SecretKey:    "minioadmin",
		UsePathStyle: true,
	}

	adapter, err := s3storage.NewS3StorageAdapter(cfg, "http://localhost:8080", nil)
	if err != nil {
		t.Fatalf("Khởi tạo S3 adapter thất bại: %v", err)
	}

	list, err := adapter.ListAssets(ctx, domain.MediaImagePNG)
	if err != nil {
		t.Fatalf("ListAssets thất bại: %v", err)
	}
	t.Logf("Tìm thấy %d assets trong bucket test", len(list))
}
