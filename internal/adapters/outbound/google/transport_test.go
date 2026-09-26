package google_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
