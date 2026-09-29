package alerts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestWebhookAlertDispatcher_Telegram(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookAlertConfig{
		Enabled:         boolPtr(true),
		Provider:        "telegram",
		URL:             server.URL,
		ChatID:          "12345678",
		MessageTemplate: "[ALERT] {error_type} | Account: {account_id} | Detail: {reason} | Action: {action_required}",
		MaxRetries:      1,
		RetryBackoff:    10 * time.Millisecond,
	}

	d := NewWebhookAlertDispatcher(cfg)
	defer d.Close()

	d.Dispatch(domain.AlertPayload{
		AccountID:      "acc_google_01",
		ErrorType:      "Google HTTP 401 (Cookie Revoked)",
		Reason:         "Cookie expired",
		ActionRequired: "Mở Chrome để đồng bộ lại CDP qua /v1/profiles/acc_google_01/sync",
		Timestamp:      time.Now(),
	})

	select {
	case payload := <-received:
		if payload["chat_id"] != "12345678" {
			t.Fatalf("expected chat_id 12345678, got %v", payload["chat_id"])
		}
		text, ok := payload["text"].(string)
		if !ok || !strings.Contains(text, "acc_google_01") || !strings.Contains(text, "Google HTTP 401") || !strings.Contains(text, "Mở Chrome để đồng bộ lại CDP") {
			t.Fatalf("unexpected message text: %s", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for telegram alert webhook")
	}
}

func TestWebhookAlertDispatcher_Discord(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookAlertConfig{
		Enabled:         boolPtr(true),
		Provider:        "discord",
		URL:             server.URL,
		MessageTemplate: "[DISCORD] {error_type} - {account_id}: {action_required}",
		MaxRetries:      1,
		RetryBackoff:    10 * time.Millisecond,
	}

	d := NewWebhookAlertDispatcher(cfg)
	defer d.Close()

	d.Dispatch(domain.AlertPayload{
		AccountID:      "acc_proxy_02",
		ErrorType:      "Proxy Connection Error",
		Reason:         "Connection refused to 127.0.0.1:1080",
		ActionRequired: "Kiểm tra Proxy URL hoặc mở Chrome CDP",
		Timestamp:      time.Now(),
	})

	select {
	case payload := <-received:
		content, ok := payload["content"].(string)
		if !ok || !strings.Contains(content, "Proxy Connection Error") || !strings.Contains(content, "acc_proxy_02") {
			t.Fatalf("unexpected discord content: %v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for discord alert webhook")
	}
}

func TestWebhookAlertDispatcher_Slack(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookAlertConfig{
		Enabled:         boolPtr(true),
		Provider:        "slack",
		URL:             server.URL,
		MessageTemplate: "[SLACK] Schema Drift: {reason}",
		MaxRetries:      1,
		RetryBackoff:    10 * time.Millisecond,
	}

	d := NewWebhookAlertDispatcher(cfg)
	defer d.Close()

	d.Dispatch(domain.AlertPayload{
		AccountID:      "lab_account",
		ErrorType:      "Schema Drift (nzlxg)",
		Reason:         "unmapped_fields count changed",
		ActionRequired: "Mở Chrome CDP đối soát spec",
		Timestamp:      time.Now(),
	})

	select {
	case payload := <-received:
		text, ok := payload["text"].(string)
		if !ok || !strings.Contains(text, "unmapped_fields count changed") {
			t.Fatalf("unexpected slack text: %v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for slack alert webhook")
	}
}

func TestWebhookAlertDispatcher_Generic(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookAlertConfig{
		Enabled:      boolPtr(true),
		Provider:     "generic",
		URL:          server.URL,
		MaxRetries:   1,
		RetryBackoff: 10 * time.Millisecond,
	}

	d := NewWebhookAlertDispatcher(cfg)
	defer d.Close()

	d.Dispatch(domain.AlertPayload{
		AccountID:      "acc_generic_01",
		ErrorType:      "Google HTTP 401",
		Reason:         "Token expired",
		ActionRequired: "Vui lòng mở Chrome đồng bộ lại CDP",
		Timestamp:      time.Now(),
	})

	select {
	case payload := <-received:
		if payload["account_id"] != "acc_generic_01" || payload["error_type"] != "Google HTTP 401" {
			t.Fatalf("unexpected generic payload: %v", payload)
		}
		if !strings.Contains(payload["action_required"].(string), "mở Chrome") {
			t.Fatalf("action_required missing chrome instruction: %v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for generic alert webhook")
	}
}

func TestWebhookAlertDispatcher_RetryBackoff(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		curr := atomic.AddInt32(&attempts, 1)
		if curr < 3 {
			http.Error(w, "temporary error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookAlertConfig{
		Enabled:      boolPtr(true),
		Provider:     "discord",
		URL:          server.URL,
		MaxRetries:   3,
		RetryBackoff: 20 * time.Millisecond,
	}

	d := NewWebhookAlertDispatcher(cfg)
	defer d.Close()

	d.Dispatch(domain.AlertPayload{
		AccountID: "acc_retry",
		ErrorType: "Test Retry",
		Timestamp: time.Now(),
	})

	// Wait enough for 3 attempts (initial + 20ms + 40ms)
	time.Sleep(300 * time.Millisecond)

	finalAttempts := atomic.LoadInt32(&attempts)
	if finalAttempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", finalAttempts)
	}
}

func TestWebhookAlertDispatcher_Disabled(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookAlertConfig{
		Enabled:  boolPtr(false),
		Provider: "telegram",
		URL:      server.URL,
	}

	d := NewWebhookAlertDispatcher(cfg)
	defer d.Close()

	d.Dispatch(domain.AlertPayload{
		AccountID: "acc_disabled",
		ErrorType: "Test Disabled",
	})

	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&attempts) != 0 {
		t.Fatalf("expected 0 attempts when disabled, got %d", atomic.LoadInt32(&attempts))
	}
}
