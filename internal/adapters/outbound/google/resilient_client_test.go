package google

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type mockRawTransport struct {
	doRequestFn func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error)
	calls       int64
}

var _ ports.UpstreamGoogleTransport = (*mockRawTransport)(nil)

func (m *mockRawTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	atomic.AddInt64(&m.calls, 1)
	return m.doRequestFn(ctx, account, service, method, path, body, contentType)
}

func (m *mockRawTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}

func (m *mockRawTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}

func TestResilientUpstreamClient_RetriesOn503ThenSucceeds(t *testing.T) {
	var attempts int64
	mock := &mockRawTransport{
		doRequestFn: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			curr := atomic.AddInt64(&attempts, 1)
			if curr < 3 {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("upstream busy")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok response")),
				Header:     make(http.Header),
			}, nil
		},
	}

	cfg := DefaultResilientConfig()
	cfg.InitialBackoff = 5 * time.Millisecond
	cfg.MaxBackoff = 20 * time.Millisecond

	client := NewResilientUpstreamClient(mock, cfg)

	resp, err := client.DoRequest(context.Background(), nil, domain.ServiceGemini, http.MethodPost, "/test", strings.NewReader("body content"), "text/plain")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got: %d", resp.StatusCode)
	}

	if atomic.LoadInt64(&attempts) != 3 {
		t.Fatalf("expected 3 attempts, got: %d", attempts)
	}
}

func TestResilientUpstreamClient_NonRetryable400(t *testing.T) {
	var attempts int64
	mock := &mockRawTransport{
		doRequestFn: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			atomic.AddInt64(&attempts, 1)
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader("invalid payload")),
				Header:     make(http.Header),
			}, nil
		},
	}

	cfg := DefaultResilientConfig()
	cfg.InitialBackoff = 5 * time.Millisecond
	client := NewResilientUpstreamClient(mock, cfg)

	resp, err := client.DoRequest(context.Background(), nil, domain.ServiceGemini, http.MethodPost, "/test", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got: %d", resp.StatusCode)
	}

	if atomic.LoadInt64(&attempts) != 1 {
		t.Fatalf("expected exactly 1 attempt without retry for 400, got: %d", attempts)
	}
}

func TestResilientUpstreamClient_CircuitBreakerTrip(t *testing.T) {
	mock := &mockRawTransport{
		doRequestFn: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return nil, errors.New("connection reset by peer")
		},
	}

	cfg := DefaultResilientConfig()
	cfg.InitialBackoff = 1 * time.Millisecond
	cfg.MaxBackoff = 5 * time.Millisecond
	cfg.CircuitBreaker.FailureThreshold = 3
	cfg.CircuitBreaker.ResetTimeout = 100 * time.Millisecond

	client := NewResilientUpstreamClient(mock, cfg)

	// Gửi request cho tới khi trip circuit breaker
	for i := 0; i < 3; i++ {
		_, _ = client.DoRequest(context.Background(), nil, domain.ServiceGemini, http.MethodPost, "/test", nil, "")
	}

	if client.breaker.State() != StateOpen {
		t.Fatalf("expected Circuit Breaker to be OPEN, got: %v", client.breaker.State())
	}

	// Request tiếp theo phải bị chặn ngay lập tức mà không gọi xuống mock
	beforeCalls := atomic.LoadInt64(&mock.calls)
	_, err := client.DoRequest(context.Background(), nil, domain.ServiceGemini, http.MethodPost, "/test", nil, "")

	if err == nil || !strings.Contains(err.Error(), "Circuit Breaker OPEN") {
		t.Fatalf("expected Circuit Breaker OPEN error, got: %v", err)
	}

	if atomic.LoadInt64(&mock.calls) != beforeCalls {
		t.Fatalf("circuit breaker open should not execute raw transport")
	}
}

func TestResilientUpstreamClient_BodyExceedsMaxBuffer_ExplicitError(t *testing.T) {
	mock := &mockRawTransport{
		doRequestFn: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}

	client := NewResilientUpstreamClient(mock, DefaultResilientConfig())

	// 1. Body = MaxRetryBodyBytes + 1 -> Phải trả về ErrRequestBodyTooLarge rõ ràng, không được gửi xuống transport
	largeBody := io.LimitReader(strings.NewReader(strings.Repeat("a", MaxRetryBodyBytes+1)), int64(MaxRetryBodyBytes+1))
	_, err := client.DoRequest(context.Background(), nil, domain.ServiceGemini, http.MethodPost, "/test", largeBody, "application/json")
	if !errors.Is(err, ErrRequestBodyTooLarge) {
		t.Fatalf("expected ErrRequestBodyTooLarge, got: %v", err)
	}
	if atomic.LoadInt64(&mock.calls) != 0 {
		t.Fatalf("expected 0 calls to raw transport when body exceeds MaxRetryBodyBytes, got: %d", mock.calls)
	}

	// 2. Body hợp lệ nhỏ hơn MaxRetryBodyBytes -> Thành công và gọi tới raw transport
	smallBody := strings.NewReader("hello world")
	resp, err := client.DoRequest(context.Background(), nil, domain.ServiceGemini, http.MethodPost, "/test", smallBody, "application/json")
	if err != nil {
		t.Fatalf("unexpected error for small body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", resp.StatusCode)
	}
	if atomic.LoadInt64(&mock.calls) != 1 {
		t.Fatalf("expected exactly 1 call to raw transport, got: %d", mock.calls)
	}
}

func TestResilientUpstreamClient_AccountHealthNotDoubleCounted(t *testing.T) {
	mock := &mockRawTransport{
		doRequestFn: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}

	client := NewResilientUpstreamClient(mock, DefaultResilientConfig())
	acc := &domain.ManagedAccount{
		ID:        "test-acc",
		Email:     "email@example.com",
		ProxyURL:  "proxy",
		IsHealthy: true,
	}

	// Upstream call thành công
	_, err := client.DoRequest(context.Background(), acc, domain.ServiceGemini, http.MethodPost, "/test", strings.NewReader("test"), "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ResilientUpstreamClient KHÔNG ĐƯỢC ghi nhận thành công/thất bại trực tiếp lên ManagedAccount
	// (SessionRepository.Release là Single Source of Truth duy nhất)
	success, failure, _, _ := acc.GetStats()
	if success != 0 || failure != 0 {
		t.Fatalf("expected 0 requests recorded by transport layer on account, got success=%d, failure=%d", success, failure)
	}
}
