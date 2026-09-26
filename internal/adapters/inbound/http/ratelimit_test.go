package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
