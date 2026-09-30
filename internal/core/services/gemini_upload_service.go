package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type GeminiUploadService struct {
	sessionRepo ports.SessionRepository
	upstream    ports.UpstreamGoogleTransport
	rpcs        *domain.RpcRegistry
	client      *http.Client
}

func NewGeminiUploadService(
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	rpcs *domain.RpcRegistry,
) ports.GeminiUploadUseCase {
	if rpcs == nil {
		rpcs = domain.DefaultRpcRegistry()
	}
	return &GeminiUploadService{
		sessionRepo: sr,
		upstream:    up,
		rpcs:        rpcs,
		client:      &http.Client{Timeout: 120 * time.Second},
	}
}

// UploadFile thực thi giao thức 2 bước SCOTTY Push Upload lên Google Gemini (docs-2/gemini/uploads.md)
func (s *GeminiUploadService) UploadFile(
	ctx context.Context,
	fileName, mimeType string,
	content io.Reader,
	size int64,
) (string, error) {
	if content == nil || size <= 0 {
		return "", domain.InvalidRequest("UploadFile", "", domain.ServiceGemini, "tệp tải lên rỗng")
	}

	account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil {
		return "", domain.Unauthenticated("UploadFile", "", domain.ServiceGemini, fmt.Sprintf("không có tài khoản Gemini khả dụng: %v", err))
	}
	defer s.sessionRepo.Release(account, nil)

	return s.UploadFileWithAccount(ctx, account, fileName, mimeType, content, size)
}

// UploadFileWithAccount thực thi SCOTTY upload với tài khoản chỉ định cụ thể
func (s *GeminiUploadService) UploadFileWithAccount(
	ctx context.Context,
	account *domain.ManagedAccount,
	fileName, mimeType string,
	content io.Reader,
	size int64,
) (string, error) {
	if content == nil || size <= 0 {
		return "", domain.InvalidRequest("UploadFile", "", domain.ServiceGemini, "tệp tải lên rỗng")
	}
	if account == nil {
		return "", domain.Unauthenticated("UploadFile", "", domain.ServiceGemini, "tài khoản chỉ định rỗng")
	}

	cookieHeader := ""
	if account.Jar != nil {
		cookieHeader = account.Jar.GetCookieHeader(false)
	}
	if cookieHeader == "" {
		return "", domain.Unauthenticated("UploadFile", "", domain.ServiceGemini, "phiên tài khoản chưa có cookie Gemini")
	}

	// -------------------------------------------------------------
	// BƯỚC 1: Khởi tạo phiên tải lên (Start Handshake)
	// -------------------------------------------------------------
	handshakeURL := "https://push.clients6.google.com/upload/"
	if ep, ok := s.rpcs.Get("UploadHandshake"); ok && ep.PathPattern != "" {
		handshakeURL = ep.PathPattern
		if ep.TargetHost != "" && !strings.HasPrefix(handshakeURL, "http") {
			handshakeURL = ep.TargetHost + handshakeURL
		}
	}

	req1, err := http.NewRequestWithContext(ctx, http.MethodPost, handshakeURL, nil)
	if err != nil {
		return "", fmt.Errorf("tạo handshake request thất bại: %w", err)
	}

	req1.Header.Set("X-Tenant-Id", "bard-storage")
	req1.Header.Set("Push-Id", "feeds/mcudyrk2a4khkz")
	req1.Header.Set("X-Goog-Upload-Command", "start")
	req1.Header.Set("X-Goog-Upload-Protocol", "resumable")
	req1.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.FormatInt(size, 10))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req1.Header.Set("Origin", "https://gemini.google.com")
	req1.Header.Set("Referer", "https://gemini.google.com/")
	req1.Header.Set("Cookie", cookieHeader)
	if account.UserAgent != "" {
		req1.Header.Set("User-Agent", account.UserAgent)
	}

	client := s.httpClientForAccount(account)
	resp1, err := client.Do(req1)
	if err != nil {
		return "", domain.CodecTransport("UploadHandshake", domain.ServiceGemini, err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		body1, _ := io.ReadAll(resp1.Body)
		return "", domain.UpstreamRejected("UploadHandshake", "", domain.ServiceGemini, fmt.Sprintf("handshake thất bại HTTP %d: %s", resp1.StatusCode, string(body1)))
	}

	uploadURL := resp1.Header.Get("X-Goog-Upload-URL")
	if uploadURL == "" {
		return "", domain.CodecSchema("UploadHandshake", domain.ServiceGemini, "Google không trả về header X-Goog-Upload-URL")
	}

	// -------------------------------------------------------------
	// BƯỚC 2: Tải lên nhị phân và chốt tệp (Upload & Finalize)
	// -------------------------------------------------------------
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, content)
	if err != nil {
		return "", fmt.Errorf("tạo upload binary request thất bại: %w", err)
	}

	req2.Header.Set("X-Tenant-Id", "bard-storage")
	req2.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	req2.Header.Set("X-Goog-Upload-Offset", "0")
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req2.Header.Set("Referer", "https://gemini.google.com/")
	req2.Header.Set("Cookie", cookieHeader)
	if account.UserAgent != "" {
		req2.Header.Set("User-Agent", account.UserAgent)
	}

	resp2, err := client.Do(req2)
	if err != nil {
		return "", domain.CodecTransport("UploadBinary", domain.ServiceGemini, err)
	}
	defer resp2.Body.Close()

	bodyBytes, err := io.ReadAll(resp2.Body)
	if err != nil {
		return "", domain.CodecTransport("UploadBinary", domain.ServiceGemini, err)
	}

	if resp2.StatusCode != http.StatusOK {
		return "", domain.UpstreamRejected("UploadBinary", "", domain.ServiceGemini, fmt.Sprintf("upload thất bại HTTP %d: %s", resp2.StatusCode, string(bodyBytes)))
	}

	storageToken := strings.TrimSpace(string(bodyBytes))
	if storageToken == "" {
		return "", domain.CodecSchema("UploadBinary", domain.ServiceGemini, "phản hồi token tệp rỗng")
	}

	return storageToken, nil
}

func (s *GeminiUploadService) httpClientForAccount(account *domain.ManagedAccount) *http.Client {
	if s.upstream != nil {
		if provider, ok := s.upstream.(interface{ ClientForAccount(account *domain.ManagedAccount) *http.Client }); ok {
			return provider.ClientForAccount(account)
		}
	}
	if s.client != nil {
		return s.client
	}
	return &http.Client{Timeout: 120 * time.Second}
}
