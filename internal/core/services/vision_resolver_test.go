package services_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
	"dezuxk-gateway/internal/security/netguard"
)

type mockUploadService struct {
	lastAccount  *domain.ManagedAccount
	lastFileName string
	lastMime     string
	lastSize     int64
	returnToken  string
	returnErr    error
}

func (m *mockUploadService) UploadFile(ctx context.Context, fileName, mimeType string, content io.Reader, size int64) (string, error) {
	return m.returnToken, m.returnErr
}

func (m *mockUploadService) UploadFileWithAccount(ctx context.Context, account *domain.ManagedAccount, fileName, mimeType string, content io.Reader, size int64) (string, error) {
	m.lastAccount = account
	m.lastFileName = fileName
	m.lastMime = mimeType
	m.lastSize = size
	return m.returnToken, m.returnErr
}

func TestVisionResolver_Base64DataURL(t *testing.T) {
	cfg := config.VisionConfig{
		MaxImageSizeBytes: 1024 * 1024,
		AllowedMimeTypes:  []string{"image/png", "image/jpeg"},
		UploadMethod:      "scotty",
	}
	mockUpload := &mockUploadService{returnToken: "/contrib_service/ttl_1d/sample_scotty_123"}
	vr := services.NewVisionResolver(cfg, mockUpload)

	acc := &domain.ManagedAccount{ID: "acc-1"}

	// Valid PNG base64
	tinyPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	dataURL := "data:image/png;base64," + tinyPNG

	req := &domain.OpenAIChatRequest{
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: dataURL}},
				},
			},
		},
	}

	attachments, err := vr.ProcessRequestAttachments(context.Background(), acc, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].StorageToken != "/contrib_service/ttl_1d/sample_scotty_123" {
		t.Errorf("expected scotty token, got %s", attachments[0].StorageToken)
	}
	if mockUpload.lastAccount != acc {
		t.Error("expected mockUpload to receive the specified account")
	}
}

func TestVisionResolver_InlineMethod(t *testing.T) {
	cfg := config.VisionConfig{
		MaxImageSizeBytes: 1024 * 1024,
		AllowedMimeTypes:  []string{"image/png", "image/jpeg"},
		UploadMethod:      "inline",
	}
	vr := services.NewVisionResolver(cfg, nil)

	acc := &domain.ManagedAccount{ID: "acc-1"}
	tinyPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	dataURL := "data:image/png;base64," + tinyPNG

	req := &domain.OpenAIChatRequest{
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: dataURL}},
				},
			},
		},
	}

	attachments, err := vr.ProcessRequestAttachments(context.Background(), acc, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if !strings.HasPrefix(attachments[0].StorageToken, "data:image/png;base64,") {
		t.Errorf("expected inline data URL token, got %s", attachments[0].StorageToken)
	}
}

func TestVisionResolver_RejectsUnsupportedMime(t *testing.T) {
	cfg := config.VisionConfig{
		MaxImageSizeBytes: 1024 * 1024,
		AllowedMimeTypes:  []string{"image/png"},
	}
	vr := services.NewVisionResolver(cfg, nil)
	acc := &domain.ManagedAccount{ID: "acc-1"}

	dataURL := "data:image/bmp;base64," + base64.StdEncoding.EncodeToString([]byte("fake-bmp-data"))
	req := &domain.OpenAIChatRequest{
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: dataURL}},
				},
			},
		},
	}

	_, err := vr.ProcessRequestAttachments(context.Background(), acc, req)
	if err == nil {
		t.Fatal("expected error for unsupported MIME type, got nil")
	}
}

func TestVisionResolver_RejectsOversize(t *testing.T) {
	cfg := config.VisionConfig{
		MaxImageSizeBytes: 10, // Very small limit (10 bytes)
		AllowedMimeTypes:  []string{"image/png"},
	}
	vr := services.NewVisionResolver(cfg, nil)
	acc := &domain.ManagedAccount{ID: "acc-1"}

	oversizeData := make([]byte, 100)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(oversizeData)
	req := &domain.OpenAIChatRequest{
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: dataURL}},
				},
			},
		},
	}

	_, err := vr.ProcessRequestAttachments(context.Background(), acc, req)
	if err == nil {
		t.Fatal("expected error for oversize image, got nil")
	}
}

func TestVisionResolver_HTTPURLDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg-image-bytes"))
	}))
	defer server.Close()

	cfg := config.VisionConfig{
		MaxImageSizeBytes:   1024 * 1024,
		AllowedMimeTypes:    []string{"image/jpeg", "image/png"},
		HTTPDownloadTimeout: 5 * time.Second,
		UploadMethod:        "scotty",
	}
	mockUpload := &mockUploadService{returnToken: "/contrib_service/ttl_1d/http_image_token"}
	vr := services.NewVisionResolver(cfg, mockUpload)
	// Cho phép loopback trong test với httptest server cục bộ
	vr.SetURLGuard(netguard.NewDefaultGuard(true))
	vr.SetHTTPClient(netguard.NewSafeHTTPClient(netguard.SafeHTTPConfig{
		Timeout:                 5 * time.Second,
		AllowLoopbackForTesting: true,
	}))
	acc := &domain.ManagedAccount{ID: "acc-1"}

	req := &domain.OpenAIChatRequest{
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: server.URL + "/photo.jpg"}},
				},
			},
		},
	}

	attachments, err := vr.ProcessRequestAttachments(context.Background(), acc, req)
	if err != nil {
		t.Fatalf("unexpected error downloading image: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].StorageToken != "/contrib_service/ttl_1d/http_image_token" {
		t.Errorf("expected storage token from http download, got %s", attachments[0].StorageToken)
	}
}

func TestVisionResolver_SSRFProtection(t *testing.T) {
	cfg := config.VisionConfig{
		MaxImageSizeBytes:   1024 * 1024,
		AllowedMimeTypes:    []string{"image/jpeg", "image/png"},
		HTTPDownloadTimeout: 5 * time.Second,
		UploadMethod:        "scotty",
	}
	mockUpload := &mockUploadService{returnToken: "token"}
	vr := services.NewVisionResolver(cfg, mockUpload) // default production config

	blockedTargets := []string{
		"http://127.0.0.1/evil.png",
		"http://127.0.0.1:8080/image.jpg",
		"http://localhost/image.png",
		"http://sub.localhost/image.png",
		"http://[::1]/image.png",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/image.png",
		"http://172.16.0.1/image.png",
		"http://192.168.1.1/image.png",
		"http://100.64.0.1/image.png",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://vault.internal/secret.png",
	}

	for _, target := range blockedTargets {
		t.Run(target, func(t *testing.T) {
			_, err := vr.ResolveImage(context.Background(), target, 0)
			if err == nil {
				t.Fatalf("expected SSRF error for %s, but ResolveImage succeeded", target)
			}
			if !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "restricted") {
				t.Fatalf("expected SSRF error message for %s, got: %v", target, err)
			}
		})
	}
}

func TestAttachmentResolver_PDFAndTextDocuments(t *testing.T) {
	cfg := config.VisionConfig{
		MaxImageSizeBytes: 10 * 1024 * 1024,
		AllowedMimeTypes:  []string{"image/jpeg", "image/png", "application/pdf", "text/plain", "text/csv", "audio/mp3"},
		UploadMethod:      "scotty",
	}
	mockUpload := &mockUploadService{returnToken: "/contrib_service/ttl_1d/pdf_token"}
	vr := services.NewVisionResolver(cfg, mockUpload)
	acc := &domain.ManagedAccount{ID: "acc-1"}

	// PDF base64
	fakePDF := base64.StdEncoding.EncodeToString([]byte("%PDF-1.5 fake pdf content"))
	pdfURL := "data:application/pdf;base64," + fakePDF

	// CSV base64
	fakeCSV := base64.StdEncoding.EncodeToString([]byte("id,name,score\n1,Alice,100\n2,Bob,95"))
	csvURL := "data:text/csv;base64," + fakeCSV

	req := &domain.OpenAIChatRequest{
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "document", Document: &domain.MessageImageURL{URL: pdfURL}},
					{Type: "file", File: &domain.MessageImageURL{URL: csvURL}},
				},
			},
		},
	}

	attachments, err := vr.ProcessRequestAttachments(context.Background(), acc, req)
	if err != nil {
		t.Fatalf("unexpected error resolving documents: %v", err)
	}
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(attachments))
	}
	if attachments[0].FileName != "upload_doc_1.pdf" {
		t.Errorf("expected upload_doc_1.pdf, got %s", attachments[0].FileName)
	}
	if attachments[1].FileName != "upload_doc_2.csv" {
		t.Errorf("expected upload_doc_2.csv, got %s", attachments[1].FileName)
	}
}
