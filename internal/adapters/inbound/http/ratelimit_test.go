package http

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

func TestIPRateLimiter(t *testing.T) {
	limiter := NewIPRateLimiter(3, 100*time.Millisecond)

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	// 3 request đầu tiên được cho phép
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d mong đợi 200, nhận %d", i+1, w.Code)
		}
	}

	// Request thứ 4 phải bị 429 Too Many Requests
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("request thứ 4 mong đợi 429, nhận %d", w.Code)
	}

	// Chờ window trôi qua (110ms)
	time.Sleep(120 * time.Millisecond)

	// Sau đó phải được phép trở lại
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sau khi refill mong đợi 200, nhận %d", w.Code)
	}
}

func TestIPRateLimiter_TrustedProxies(t *testing.T) {
	trustedProxies := []string{"127.0.0.1", "10.0.0.0/8"}
	limiter := NewIPRateLimiter(2, 500*time.Millisecond, trustedProxies)

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Case 1: Untrusted peer (203.0.113.50) tries to spoof X-Forwarded-For
	// Header must be ignored, limited on peer IP
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
		req.RemoteAddr = "203.0.113.50:4321"
		req.Header.Set("X-Forwarded-For", "8.8.8.8")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("untrusted req %d expected 200, got %d", i+1, w.Code)
		}
	}

	// Third request from same peer but spoofing different IP -> must be blocked (429)
	reqSpoof := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	reqSpoof.RemoteAddr = "203.0.113.50:4321"
	reqSpoof.Header.Set("X-Forwarded-For", "9.9.9.9")
	wSpoof := httptest.NewRecorder()
	handler.ServeHTTP(wSpoof, reqSpoof)
	if wSpoof.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofing attempt should be blocked with 429, got %d", wSpoof.Code)
	}

	// Case 2: Trusted peer (10.1.2.3) forwarding legitimate client IP
	req1 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req1.RemoteAddr = "10.1.2.3:5555"
	req1.Header.Set("X-Forwarded-For", "198.51.100.1")
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("trusted proxy req1 expected 200, got %d", w1.Code)
	}

	// Different forwarded client through same proxy -> allowed
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req2.RemoteAddr = "10.1.2.3:5555"
	req2.Header.Set("X-Forwarded-For", "198.51.100.2")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("trusted proxy req2 (different client) expected 200, got %d", w2.Code)
	}
}

func TestLocalRateLimiter_MultiTierAndHashing(t *testing.T) {
	limiter := NewLocalRateLimiter(2, 200*time.Millisecond)
	limiter.SetMaxConcurrent(2)
	limiter.SetMaxAgentRuns(1)

	// 1. Kiểm tra SHA-256 Key Hashing
	rawKey1 := "sk-prod-test-key-abcdef-123456"
	rawKey2 := "sk-prod-test-key-abcdef-999999" // Same prefix, different token
	hash1 := HashKeyID(rawKey1)
	hash2 := HashKeyID(rawKey2)
	if hash1 == hash2 {
		t.Fatalf("Key hash collision for different tokens: %s == %s", hash1, hash2)
	}
	if strings.Contains(hash1, "sk-prod") {
		t.Fatalf("Key hash must NOT leak raw token prefix, got: %s", hash1)
	}

	// 2. Multi-tier isolation: Tenant A vs Tenant B
	idA := ports.RateLimitIdentity{TenantID: "tenant-A", KeyID: hash1}
	idB := ports.RateLimitIdentity{TenantID: "tenant-B", KeyID: hash2}

	// Tenant A uses 2 requests
	for i := 0; i < 2; i++ {
		d, err := limiter.Allow(t.Context(), idA)
		if err != nil || !d.Allowed {
			t.Fatalf("Tenant A req %d should be allowed: %+v, err: %v", i+1, d, err)
		}
	}

	// Tenant A 3rd request -> rate limited with Retry-After
	dA, err := limiter.Allow(t.Context(), idA)
	if err != nil {
		t.Fatalf("Tenant A check error: %v", err)
	}
	if dA.Allowed {
		t.Fatalf("Tenant A 3rd request should be blocked")
	}
	if dA.RetryAfterSec <= 0 {
		t.Fatalf("Expected RetryAfterSec > 0, got %d", dA.RetryAfterSec)
	}

	// Tenant B is isolated and must still be allowed
	dB, err := limiter.Allow(t.Context(), idB)
	if err != nil || !dB.Allowed {
		t.Fatalf("Tenant B should be allowed regardless of Tenant A: %+v, err: %v", dB, err)
	}

	// 3. Concurrency Limiter: Agent Runs MaxConcurrent = 1
	idAgent := ports.RateLimitIdentity{TenantID: "tenant-A", KeyID: hash1, IsAgentRun: true}
	release1, ok1, err := limiter.AcquireConcurrency(t.Context(), idAgent)
	if err != nil || !ok1 {
		t.Fatalf("First concurrent agent run should succeed")
	}

	// Second concurrent agent run -> blocked
	_, ok2, _ := limiter.AcquireConcurrency(t.Context(), idAgent)
	if ok2 {
		t.Fatalf("Second concurrent agent run should be rejected by maxAgentRuns limit")
	}

	// Release first agent run -> subsequent can acquire
	release1()
	release3, ok3, err := limiter.AcquireConcurrency(t.Context(), idAgent)
	if err != nil || !ok3 {
		t.Fatalf("Agent run after release should succeed")
	}
	release3()
}

func TestAuthenticatedRateLimitMiddleware_ModelAware(t *testing.T) {
	limiter := NewLocalRateLimiter(2, 500*time.Millisecond)

	var lastReceivedBody string
	handler := AuthenticatedRateLimitMiddleware(limiter, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		lastReceivedBody = string(bodyBytes)
		model, _ := domain.ModelFromContext(r.Context())
		w.Header().Set("X-Model", model)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Same tenant/key/endpoint, but model A
	reqA1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model": "gemini-3.8-flash", "messages": []}`))
	reqA1.Header.Set("Content-Type", "application/json")
	reqA1.Header.Set("Authorization", "Bearer sk-test-key-12345")
	recA1 := httptest.NewRecorder()
	handler.ServeHTTP(recA1, reqA1)
	if recA1.Code != http.StatusOK {
		t.Fatalf("reqA1 expected 200, got %d", recA1.Code)
	}
	if recA1.Header().Get("X-Model") != "gemini-3.8-flash" {
		t.Fatalf("expected model gemini-3.8-flash in context, got %s", recA1.Header().Get("X-Model"))
	}
	if !strings.Contains(lastReceivedBody, "gemini-3.8-flash") {
		t.Fatalf("handler downstream body was lost or corrupted: %s", lastReceivedBody)
	}

	// 2nd request for model A -> allowed (limit is 2)
	reqA2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model": "gemini-3.8-flash"}`))
	reqA2.Header.Set("Content-Type", "application/json")
	reqA2.Header.Set("Authorization", "Bearer sk-test-key-12345")
	recA2 := httptest.NewRecorder()
	handler.ServeHTTP(recA2, reqA2)
	if recA2.Code != http.StatusOK {
		t.Fatalf("reqA2 expected 200, got %d", recA2.Code)
	}

	// 3rd request for model A -> 429 Too Many Requests
	reqA3 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model": "gemini-3.8-flash"}`))
	reqA3.Header.Set("Content-Type", "application/json")
	reqA3.Header.Set("Authorization", "Bearer sk-test-key-12345")
	recA3 := httptest.NewRecorder()
	handler.ServeHTTP(recA3, reqA3)
	if recA3.Code != http.StatusTooManyRequests {
		t.Fatalf("reqA3 expected 429 for model A, got %d", recA3.Code)
	}

	// Request for model B with SAME tenant/key/endpoint -> MUST BE ALLOWED (separate bucket)
	reqB1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model": "gemini-2.5-pro"}`))
	reqB1.Header.Set("Content-Type", "application/json")
	reqB1.Header.Set("Authorization", "Bearer sk-test-key-12345")
	recB1 := httptest.NewRecorder()
	handler.ServeHTTP(recB1, reqB1)
	if recB1.Code != http.StatusOK {
		t.Fatalf("reqB1 for model B expected 200 (isolated bucket), got %d: %s", recB1.Code, recB1.Body.String())
	}
	if recB1.Header().Get("X-Model") != "gemini-2.5-pro" {
		t.Fatalf("expected model gemini-2.5-pro in context, got %s", recB1.Header().Get("X-Model"))
	}
}
