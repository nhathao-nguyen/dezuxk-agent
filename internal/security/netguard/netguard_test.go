package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNetGuard_SSRFBlockedTargets(t *testing.T) {
	guard := NewDefaultGuard(false)

	blockedURLs := []string{
		"http://127.0.0.1",
		"http://127.0.0.1:8080/secret",
		"http://localhost",
		"http://localhost:3000",
		"http://test.localhost",
		"http://[::1]",
		"http://[::1]:9090",
		"http://169.254.169.254",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1",
		"http://10.255.255.254",
		"http://172.16.0.1",
		"http://172.31.255.255",
		"http://192.168.1.1",
		"http://192.168.0.254",
		"http://100.64.0.1",
		"http://100.127.255.255",
		"http://metadata.google.internal",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://api.internal",
		"http://cluster.local",
		"ftp://example.com/file.png",
		"file:///etc/passwd",
		"gopher://127.0.0.1:70",
	}

	for _, rawURL := range blockedURLs {
		t.Run(rawURL, func(t *testing.T) {
			ctx := context.Background()
			u, err := guard.Validate(ctx, rawURL)
			if err == nil {
				t.Fatalf("expected error for blocked URL %s, got allowed URL: %v", rawURL, u)
			}
		})
	}
}

func TestNetGuard_ValidPublicURL(t *testing.T) {
	guard := NewDefaultGuard(false)

	validURLs := []string{
		"https://example.com/image.png",
		"http://public.service.org/test",
		"https://images.unsplash.com/photo-12345",
	}

	for _, rawURL := range validURLs {
		t.Run(rawURL, func(t *testing.T) {
			ctx := context.Background()
			u, err := guard.Validate(ctx, rawURL)
			if err != nil {
				t.Fatalf("expected valid URL for %s, got error: %v", rawURL, err)
			}
			if u == nil || u.Host == "" {
				t.Fatalf("expected parsed URL, got nil or empty host")
			}
		})
	}
}

func TestNetGuard_DNSRebindingProtection(t *testing.T) {
	// Giả lập DNS resolver trả về IP nội bộ nguy hiểm (DNS Rebinding attack)
	fakeResolver := func(ctx context.Context, network, host string) ([]net.IP, error) {
		if host == "rebind.attacker.com" {
			// DNS trả về loopback IP
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		if host == "metadata.attacker.com" {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		}
		if host == "cgnat.attacker.com" {
			return []net.IP{net.ParseIP("100.64.0.1")}, nil
		}
		return nil, errors.New("unknown host")
	}

	client := NewSafeHTTPClient(SafeHTTPConfig{
		Timeout:        2 * time.Second,
		CustomResolver: fakeResolver,
	})

	testCases := []string{
		"http://rebind.attacker.com/leak",
		"http://metadata.attacker.com/secret",
		"http://cgnat.attacker.com/admin",
	}

	for _, u := range testCases {
		t.Run(u, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			_, err = client.Do(req)
			if err == nil {
				t.Fatalf("expected connection to be blocked by DNS rebinding guard, but request succeeded!")
			}
		})
	}
}

func TestNetGuard_RedirectSSRFProtection(t *testing.T) {
	// Tạo target server nội bộ
	internalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("sensitive internal data"))
	}))
	defer internalServer.Close()

	// Tạo server chuyển hướng trỏ về target nội bộ
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internalServer.URL, http.StatusFound)
	}))
	defer redirectServer.Close()

	// Client cho phép loopback để gọi redirectServer ban đầu, nhưng cấm chuyển hướng đến IP bị cấm
	// Ta test cơ chế CheckRedirect bằng cách dùng URL guard bình thường
	guard := NewDefaultGuard(false)

	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if _, err := guard.Validate(req.Context(), req.URL.String()); err != nil {
				return fmt.Errorf("ssrf redirect blocked: %w", err)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, redirectServer.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	_, err = client.Do(req)
	if err == nil {
		t.Fatalf("expected redirect to loopback to be blocked by SSRF redirect guard")
	}
}

func TestNetGuard_AllowLoopbackForTestingMode(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok from loopback test"))
	}))
	defer testServer.Close()

	client := NewSafeHTTPClient(SafeHTTPConfig{
		Timeout:                 2 * time.Second,
		AllowLoopbackForTesting: true,
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, testServer.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected request to loopback to succeed in test mode: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", resp.StatusCode)
	}
}
