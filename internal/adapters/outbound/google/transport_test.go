package google_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

func TestGoogleTransportAdapter_DynamicTargetHost(t *testing.T) {
	var capturedReq *http.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	adapter := google.NewGoogleTransportAdapter(nil)
	acc := &domain.ManagedAccount{
		ID:        "acc-1",
		UserAgent: "CustomUA/1.0",
		Jar:       domain.NewCookieJar(map[string]string{"OSID": "osid-val", "SID": "sid-val"}),
	}

	t.Run("Absolute URL in path overrides target host", func(t *testing.T) {
		capturedReq = nil
		resp, err := adapter.DoRequest(
			context.Background(),
			acc,
			domain.ServiceFlow,
			http.MethodPost,
			ts.URL+"/custom/path",
			strings.NewReader("sample-body"),
			"application/json",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if capturedReq == nil {
			t.Fatal("request was not captured")
		}
		if capturedReq.URL.Path != "/custom/path" {
			t.Errorf("expected path /custom/path, got %s", capturedReq.URL.Path)
		}
		if capturedReq.Header.Get("User-Agent") != "CustomUA/1.0" {
			t.Errorf("expected CustomUA/1.0, got %s", capturedReq.Header.Get("User-Agent"))
		}
		if capturedReq.Header.Get("Origin") != ts.URL {
			t.Errorf("expected Origin %s, got %s", ts.URL, capturedReq.Header.Get("Origin"))
		}
	})

	t.Run("TargetHost from context overrides default domain", func(t *testing.T) {
		capturedReq = nil
		ctx := domain.WithTargetHost(context.Background(), ts.URL)
		resp, err := adapter.DoRequest(
			ctx,
			acc,
			domain.ServiceGemini,
			http.MethodGet,
			"/relative/endpoint",
			nil,
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if capturedReq == nil {
			t.Fatal("request was not captured")
		}
		if capturedReq.URL.Path != "/relative/endpoint" {
			t.Errorf("expected path /relative/endpoint, got %s", capturedReq.URL.Path)
		}
		if capturedReq.Header.Get("Origin") != ts.URL {
			t.Errorf("expected Origin %s, got %s", ts.URL, capturedReq.Header.Get("Origin"))
		}
	})
}

func TestGoogleTransportAdapter_DirectAndProxyParity(t *testing.T) {
	adapter := google.NewGoogleTransportAdapter(nil)
	googleAdapter, ok := adapter.(*google.GoogleTransportAdapter)
	if !ok {
		t.Fatalf("expected *GoogleTransportAdapter")
	}

	directClient := googleAdapter.ClientForAccount(nil)
	if directClient.Timeout != 0 {
		t.Errorf("expected direct client.Timeout = 0 for streaming resilience, got %v", directClient.Timeout)
	}

	directTr, ok := directClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected direct Transport to be *http.Transport")
	}
	if directTr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("expected direct ResponseHeaderTimeout 30s, got %v", directTr.ResponseHeaderTimeout)
	}
	if directTr.IdleConnTimeout != 90*time.Second {
		t.Errorf("expected direct IdleConnTimeout 90s, got %v", directTr.IdleConnTimeout)
	}
	if directTr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("expected direct TLSHandshakeTimeout 10s, got %v", directTr.TLSHandshakeTimeout)
	}

	// Proxy client
	proxyAcc := &domain.ManagedAccount{
		ID:       "acc-proxy",
		ProxyURL: "http://127.0.0.1:8888",
	}
	proxyClient := googleAdapter.ClientForAccount(proxyAcc)
	if proxyClient.Timeout != 0 {
		t.Errorf("expected proxy client.Timeout = 0 for streaming resilience, got %v", proxyClient.Timeout)
	}

	proxyTr, ok := proxyClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected proxy Transport to be *http.Transport")
	}
	if proxyTr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("expected proxy ResponseHeaderTimeout 30s, got %v", proxyTr.ResponseHeaderTimeout)
	}
	if proxyTr.IdleConnTimeout != 90*time.Second {
		t.Errorf("expected proxy IdleConnTimeout 90s, got %v", proxyTr.IdleConnTimeout)
	}
	if proxyTr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("expected proxy TLSHandshakeTimeout 10s, got %v", proxyTr.TLSHandshakeTimeout)
	}
}
