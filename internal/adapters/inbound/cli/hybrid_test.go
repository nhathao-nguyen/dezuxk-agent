package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/adapters/inbound/cli"

	"github.com/spf13/cobra"
)

func TestProbeDaemon(t *testing.T) {
	t.Run("returns true when daemon /ready responds 200", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ready" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ready":true}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		online := cli.ProbeDaemon(ts.URL, 500)
		if !online {
			t.Errorf("expected ProbeDaemon to return true, got false")
		}
	})

	t.Run("returns false when daemon /ready responds 500", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		online := cli.ProbeDaemon(ts.URL, 500)
		if online {
			t.Errorf("expected ProbeDaemon to return false, got true")
		}
	})

	t.Run("returns false when daemon is unreachable", func(t *testing.T) {
		online := cli.ProbeDaemon("http://127.0.0.1:59999", 100)
		if online {
			t.Errorf("expected ProbeDaemon to return false, got true")
		}
	})
}

func TestGatewayClient_APIs(t *testing.T) {
	var receivedAuth string
	var lastMethod string
	var lastPath string
	var lastBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		lastMethod = r.Method
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")

		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&lastBody)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/overview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        "healthy",
				"active_models": 2,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/profiles":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"profiles": []map[string]any{
					{"id": "user1", "status": "active"},
				},
				"count": 1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/profiles":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "created"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/profiles/user1/launch":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "launched"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/profiles/user1/sync":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "activated"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/profiles/user1/ingest":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "activated"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/profiles/user1/proxy":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "updated"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{"id": "key1", "name": "test-key"},
				},
				"count": 1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/keys":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":  "key_new",
				"key": "sk-dez-123456",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/admin/keys/key1":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/flow/credits":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"total_credits": 100},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/gemini/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"tier": "advanced"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/alerts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"alerts": []map[string]any{{"type": "warning"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/alerts/clear":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := cli.NewGatewayClient(ts.URL, "secret_token_123")
	ctx := context.Background()

	// 1. GetOverview
	overview, err := client.GetOverview(ctx)
	if err != nil {
		t.Fatalf("GetOverview failed: %v", err)
	}
	if overview["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got %v", overview["status"])
	}
	if receivedAuth != "Bearer secret_token_123" {
		t.Errorf("expected auth 'Bearer secret_token_123', got %q", receivedAuth)
	}

	// 2. ListProfiles
	profiles, err := client.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("ListProfiles failed: %v", err)
	}
	if len(profiles) != 1 || profiles[0]["id"] != "user1" {
		t.Errorf("unexpected profiles: %+v", profiles)
	}

	// 3. CreateProfile
	err = client.CreateProfile(ctx, "user2")
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}
	if lastMethod != http.MethodPost || lastPath != "/v1/profiles" {
		t.Errorf("expected POST /v1/profiles, got %s %s", lastMethod, lastPath)
	}

	// 4. LaunchChrome
	err = client.LaunchChrome(ctx, "user1", 9222, false)
	if err != nil {
		t.Fatalf("LaunchChrome failed: %v", err)
	}
	if lastPath != "/v1/profiles/user1/launch" {
		t.Errorf("expected /v1/profiles/user1/launch, got %s", lastPath)
	}

	// 5. SyncCDP
	err = client.SyncCDP(ctx, "user1")
	if err != nil {
		t.Fatalf("SyncCDP failed: %v", err)
	}
	if lastPath != "/v1/profiles/user1/sync" {
		t.Errorf("expected /v1/profiles/user1/sync, got %s", lastPath)
	}

	// 6. IngestCookies
	err = client.IngestCookies(ctx, "user1", []byte(`{"OSID":"abc"}`))
	if err != nil {
		t.Fatalf("IngestCookies failed: %v", err)
	}
	if lastPath != "/v1/profiles/user1/ingest" {
		t.Errorf("expected /v1/profiles/user1/ingest, got %s", lastPath)
	}

	// 7. SetProxy
	err = client.SetProxy(ctx, "user1", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("SetProxy failed: %v", err)
	}
	if lastPath != "/v1/profiles/user1/proxy" {
		t.Errorf("expected /v1/profiles/user1/proxy, got %s", lastPath)
	}

	// 8. ListKeys
	keys, err := client.ListKeys(ctx)
	if err != nil {
		t.Fatalf("ListKeys failed: %v", err)
	}
	if len(keys) != 1 || keys[0]["id"] != "key1" {
		t.Errorf("unexpected keys: %+v", keys)
	}

	// 9. CreateKey
	createdKey, err := client.CreateKey(ctx, "test-key", 60, true)
	if err != nil {
		t.Fatalf("CreateKey failed: %v", err)
	}
	if createdKey["key"] != "sk-dez-123456" {
		t.Errorf("expected key 'sk-dez-123456', got %v", createdKey["key"])
	}

	// 10. RevokeKey
	err = client.RevokeKey(ctx, "key1")
	if err != nil {
		t.Fatalf("RevokeKey failed: %v", err)
	}
	if lastPath != "/v1/admin/keys/key1" {
		t.Errorf("expected /v1/admin/keys/key1, got %s", lastPath)
	}

	// 11. GetFlowCredits
	credits, err := client.GetFlowCredits(ctx, "acc1")
	if err != nil {
		t.Fatalf("GetFlowCredits failed: %v", err)
	}
	if credits["data"] == nil {
		t.Errorf("expected data field in credits response")
	}

	// 12. GetGeminiUsage
	usage, err := client.GetGeminiUsage(ctx, "acc1")
	if err != nil {
		t.Fatalf("GetGeminiUsage failed: %v", err)
	}
	if usage["data"] == nil {
		t.Errorf("expected data field in usage response")
	}

	// 13. GetAlerts
	alerts, err := client.GetAlerts(ctx)
	if err != nil {
		t.Fatalf("GetAlerts failed: %v", err)
	}
	if len(alerts) != 1 {
		t.Errorf("expected 1 alert, got %d", len(alerts))
	}

	// 14. ClearAlerts
	err = client.ClearAlerts(ctx, "acc1")
	if err != nil {
		t.Fatalf("ClearAlerts failed: %v", err)
	}
	if lastPath != "/v1/alerts/clear" {
		t.Errorf("expected /v1/alerts/clear, got %s", lastPath)
	}
}

func helperCreateTestConfig(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	profileDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatalf("failed to create profile dir: %v", err)
	}

	cfgContent := `
server:
  host: "127.0.0.1"
  port: 8080
profiles:
  base_dir: "` + filepath.ToSlash(profileDir) + `"
storage:
  database_path: "` + filepath.ToSlash(dbPath) + `"
security:
  master_key: "01234567890123456789012345678901"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	return cfgPath
}

func TestDirectServices_InitAndClose(t *testing.T) {
	cfgPath := helperCreateTestConfig(t)

	direct, err := cli.InitDirectServices(cfgPath)
	if err != nil {
		t.Fatalf("InitDirectServices failed: %v", err)
	}
	if direct == nil {
		t.Fatalf("expected direct to be non-nil")
	}

	if direct.Config == nil {
		t.Error("expected direct.Config to be non-nil")
	}
	if direct.SessionRepo == nil {
		t.Error("expected direct.SessionRepo to be non-nil")
	}
	if direct.Vault == nil {
		t.Error("expected direct.Vault to be non-nil")
	}
	if direct.ProfileManager == nil {
		t.Error("expected direct.ProfileManager to be non-nil")
	}
	if direct.KeyService == nil {
		t.Error("expected direct.KeyService to be non-nil")
	}
	if direct.ModelRegistry == nil {
		t.Error("expected direct.ModelRegistry to be non-nil")
	}
	if direct.ResponseCache == nil {
		t.Error("expected direct.ResponseCache to be non-nil")
	}

	err = direct.Close()
	if err != nil {
		t.Errorf("direct.Close() returned error: %v", err)
	}
}

func TestGetExecutionContext(t *testing.T) {
	cfgPath := helperCreateTestConfig(t)

	t.Run("online mode when server probe succeeds", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ready" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ready":true}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		cmd := &cobra.Command{}
		cmd.Flags().String("config", cfgPath, "")
		cmd.Flags().String("format", "json", "")
		cmd.Flags().String("url", ts.URL, "")
		cmd.Flags().Bool("offline", false, "")
		cmd.Flags().String("token", "admin_tok", "")

		execCtx, err := cli.GetExecutionContext(cmd)
		if err != nil {
			t.Fatalf("GetExecutionContext failed: %v", err)
		}
		if !execCtx.IsOnline {
			t.Errorf("expected IsOnline to be true")
		}
		if execCtx.Format != "json" {
			t.Errorf("expected Format 'json', got %q", execCtx.Format)
		}
		if execCtx.Client == nil {
			t.Error("expected Client to be non-nil")
		}
		if execCtx.Direct != nil {
			t.Error("expected Direct to be nil in online mode")
		}
	})

	t.Run("offline mode when offline flag is true", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("config", cfgPath, "")
		cmd.Flags().String("format", "table", "")
		cmd.Flags().String("url", "http://127.0.0.1:8080", "")
		cmd.Flags().Bool("offline", true, "")
		cmd.Flags().String("token", "", "")

		execCtx, err := cli.GetExecutionContext(cmd)
		if err != nil {
			t.Fatalf("GetExecutionContext failed: %v", err)
		}
		if execCtx.IsOnline {
			t.Errorf("expected IsOnline to be false")
		}
		if execCtx.Format != "table" {
			t.Errorf("expected Format 'table', got %q", execCtx.Format)
		}
		if execCtx.Client != nil {
			t.Error("expected Client to be nil in offline mode")
		}
		if execCtx.Direct == nil {
			t.Error("expected Direct to be non-nil in offline mode")
		}

		err = execCtx.Close()
		if err != nil {
			t.Errorf("execCtx.Close() returned error: %v", err)
		}
	})

	t.Run("fallback to direct mode when server probe fails", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("config", cfgPath, "")
		cmd.Flags().String("format", "table", "")
		cmd.Flags().String("url", "http://127.0.0.1:59998", "")
		cmd.Flags().Bool("offline", false, "")
		cmd.Flags().String("token", "", "")

		execCtx, err := cli.GetExecutionContext(cmd)
		if err != nil {
			t.Fatalf("GetExecutionContext failed: %v", err)
		}
		if execCtx.IsOnline {
			t.Errorf("expected IsOnline to be false on probe failure")
		}
		if execCtx.Client != nil {
			t.Error("expected Client to be nil on fallback")
		}
		if execCtx.Direct == nil {
			t.Error("expected Direct to be non-nil on fallback")
		}

		err = execCtx.Close()
		if err != nil {
			t.Errorf("execCtx.Close() returned error: %v", err)
		}
	})
}
