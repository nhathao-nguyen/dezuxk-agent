package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/inbound/cli"
	"dezuxk-gateway/internal/core/domain"
)

func createTestEnvironment(t *testing.T) (string, string) {
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
	return cfgPath, dbPath
}

func TestCLI_StatusOffline(t *testing.T) {
	cfgPath, _ := createTestEnvironment(t)

	t.Run("status offline table format", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"status", "--config", cfgPath, "--offline", "--format", "table"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected status command to succeed, got: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "Offline") {
			t.Errorf("expected output to mention Offline, got: %s", out)
		}
		if !strings.Contains(out, "MODELS") && !strings.Contains(out, "Models") {
			t.Errorf("expected output to have models column/info, got: %s", out)
		}
	})

	t.Run("status offline json format", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"status", "--config", cfgPath, "--offline", "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected status command to succeed, got: %v", err)
		}

		var res map[string]any
		if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse json output: %v, raw: %s", err, buf.String())
		}
		if res["server_status"] != "Offline" {
			t.Errorf("expected server_status Offline, got %v", res["server_status"])
		}
	})
}

func TestCLI_ProfileOffline(t *testing.T) {
	cfgPath, _ := createTestEnvironment(t)

	// Pre-populate an account in direct DB
	direct, err := cli.InitDirectServices(cfgPath)
	if err != nil {
		t.Fatalf("InitDirectServices failed: %v", err)
	}
	acc := &domain.ManagedAccount{
		ID:             "acc_test_1",
		Email:          "test1@gmail.com",
		Tier:           2,
		IsHealthy:      true,
		CreditsBalance: 150,
		ProxyURL:       "http://proxy.local:8080",
	}
	jar := domain.NewCookieJar(map[string]string{
		"OSID":           "osid_val",
		"__Secure-1PSID": "psid_val",
	})
	acc.Jar = jar
	if err := direct.SessionRepo.Save(context.Background(), acc); err != nil {
		t.Fatalf("Save account failed: %v", err)
	}
	_ = direct.Close()

	t.Run("profile list table format", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"profile", "list", "--config", cfgPath, "--offline", "--format", "table"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected profile list command to succeed, got: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "acc_test_1") {
			t.Errorf("expected output to contain acc_test_1, got: %s", out)
		}
		if !strings.Contains(out, "test1@gmail.com") {
			t.Errorf("expected output to contain test1@gmail.com, got: %s", out)
		}
		if !strings.Contains(out, "Pro") {
			t.Errorf("expected output to contain Pro tier, got: %s", out)
		}
	})

	t.Run("profile list json format", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"profile", "list", "--config", cfgPath, "--offline", "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected profile list command to succeed, got: %v", err)
		}

		var res []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse json output: %v, raw: %s", err, buf.String())
		}
		if len(res) == 0 {
			t.Fatalf("expected at least 1 profile in json output, got 0")
		}
		if res[0]["id"] != "acc_test_1" {
			t.Errorf("expected id acc_test_1, got %v", res[0]["id"])
		}
	})

	t.Run("profile create offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"profile", "create", "new_prof", "--config", cfgPath, "--offline"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected profile create to succeed, got: %v", err)
		}
		if !strings.Contains(buf.String(), "new_prof") {
			t.Errorf("expected output to mention new_prof, got: %s", buf.String())
		}
	})

	t.Run("profile proxy offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"profile", "proxy", "new_prof", "--url", "http://newproxy:8080", "--config", cfgPath, "--offline"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected profile proxy to succeed, got: %v", err)
		}
		if !strings.Contains(buf.String(), "newproxy:8080") {
			t.Errorf("expected output to mention proxy url, got: %s", buf.String())
		}
	})

	t.Run("profile ingest offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		cookieJSON := `{"OSID":"osid_123","__Secure-OSID":"sec_osid_123"}`
		root.SetArgs([]string{"profile", "ingest", "new_prof", "--json", cookieJSON, "--config", cfgPath, "--offline"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected profile ingest to succeed, got: %v", err)
		}
		if !strings.Contains(buf.String(), "new_prof") {
			t.Errorf("expected output to mention new_prof, got: %s", buf.String())
		}
	})
}

func TestCLI_KeyOffline(t *testing.T) {
	cfgPath, _ := createTestEnvironment(t)

	t.Run("key create offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"key", "create", "--name", "worker-key", "--rpm", "120", "--admin", "--config", cfgPath, "--offline"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected key create to succeed, got: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "sk-dez-") {
			t.Errorf("expected output to display generated secret key, got: %s", out)
		}
		if !strings.Contains(out, "worker-key") {
			t.Errorf("expected output to display key name, got: %s", out)
		}
	})

	t.Run("key list table format offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"key", "list", "--config", cfgPath, "--offline", "--format", "table"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected key list to succeed, got: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "worker-key") {
			t.Errorf("expected key list to contain worker-key, got: %s", out)
		}
		if !strings.Contains(out, "KEY_ID") {
			t.Errorf("expected key list to have KEY_ID header, got: %s", out)
		}
	})

	t.Run("key list json format offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"key", "list", "--config", cfgPath, "--offline", "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected key list to succeed, got: %v", err)
		}

		var res []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse json output: %v, raw: %s", err, buf.String())
		}
		if len(res) == 0 {
			t.Fatalf("expected at least 1 key in json output, got 0")
		}
		if res[0]["name"] != "worker-key" {
			t.Errorf("expected name worker-key, got %v", res[0]["name"])
		}

		keyID := res[0]["key_id"].(string)

		// Test key revoke
		bufRevoke := new(bytes.Buffer)
		rootRevoke := cli.NewRootCmd()
		rootRevoke.SetOut(bufRevoke)
		rootRevoke.SetErr(bufRevoke)
		rootRevoke.SetArgs([]string{"key", "revoke", keyID, "--config", cfgPath, "--offline"})

		err = rootRevoke.Execute()
		if err != nil {
			t.Fatalf("expected key revoke to succeed, got: %v", err)
		}
		if !strings.Contains(bufRevoke.String(), keyID) {
			t.Errorf("expected revoke output to mention key ID, got: %s", bufRevoke.String())
		}
	})
}

func TestCLI_StartCommand(t *testing.T) {
	var capturedConfig string
	var capturedPort int

	cli.DaemonRunner = func(configPath string, portOverride int) error {
		capturedConfig = configPath
		capturedPort = portOverride
		return nil
	}

	root := cli.NewRootCmd()
	root.SetArgs([]string{"start", "-c", "custom/config.yaml", "-p", "9090"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected start command to succeed, got: %v", err)
	}

	if capturedConfig != "custom/config.yaml" {
		t.Errorf("expected capturedConfig custom/config.yaml, got %s", capturedConfig)
	}
	if capturedPort != 9090 {
		t.Errorf("expected capturedPort 9090, got %d", capturedPort)
	}

	// Test alias 'run'
	rootRun := cli.NewRootCmd()
	rootRun.SetArgs([]string{"run", "-p", "8888"})
	err = rootRun.Execute()
	if err != nil {
		t.Fatalf("expected run alias to succeed, got: %v", err)
	}
	if capturedPort != 8888 {
		t.Errorf("expected capturedPort 8888, got %d", capturedPort)
	}
}

func TestCLI_OnlineCommands(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/ready":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ready":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/overview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"accounts": []map[string]any{
					{"id": "online_acc", "is_healthy": true},
				},
				"models": []map[string]any{
					{"id": "gemini-2.5"},
				},
				"alerts": []any{},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/profiles":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"profiles": []map[string]any{
					{
						"id":           "prof_online",
						"email":        "online@test.com",
						"tier":         "Pro",
						"is_logged_in": true,
						"proxy":        "http://proxy:8080",
						"flow_credits": 200,
						"has_flow":     true,
						"has_gemini":   true,
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/keys":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "vk_online123",
				"name":           "remote-key",
				"key":            "sk-dez-online12345",
				"key_prefix":     "sk-dez-onli",
				"role":           "admin",
				"rate_limit_rpm": 300,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"id":             "vk_online123",
						"name":           "remote-key",
						"key_prefix":     "sk-dez-onli",
						"role":           "admin",
						"rate_limit_rpm": 300,
					},
				},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/admin/keys/vk_online123":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	t.Run("status online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"status", "--url", ts.URL, "--format", "json"})

		if err := root.Execute(); err != nil {
			t.Fatalf("status online failed: %v", err)
		}

		var res map[string]any
		if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}
		if res["server_status"] != "Online" {
			t.Errorf("expected server_status Online, got %v", res["server_status"])
		}
	})

	t.Run("profile list online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"profile", "list", "--url", ts.URL, "--format", "json"})

		if err := root.Execute(); err != nil {
			t.Fatalf("profile list online failed: %v", err)
		}

		var res []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}
		if len(res) == 0 || res[0]["id"] != "prof_online" {
			t.Errorf("unexpected profile result: %v", res)
		}
	})

	t.Run("key lifecycle online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"key", "create", "--name", "remote-key", "--rpm", "300", "--admin", "--url", ts.URL})

		if err := root.Execute(); err != nil {
			t.Fatalf("key create online failed: %v", err)
		}
		if !strings.Contains(buf.String(), "sk-dez-online12345") {
			t.Errorf("expected created key in output, got: %s", buf.String())
		}

		bufList := new(bytes.Buffer)
		rootList := cli.NewRootCmd()
		rootList.SetOut(bufList)
		rootList.SetErr(bufList)
		rootList.SetArgs([]string{"key", "list", "--url", ts.URL, "--format", "table"})
		if err := rootList.Execute(); err != nil {
			t.Fatalf("key list online failed: %v", err)
		}
		if !strings.Contains(bufList.String(), "remote-key") {
			t.Errorf("expected remote-key in list, got: %s", bufList.String())
		}

		bufRevoke := new(bytes.Buffer)
		rootRevoke := cli.NewRootCmd()
		rootRevoke.SetOut(bufRevoke)
		rootRevoke.SetErr(bufRevoke)
		rootRevoke.SetArgs([]string{"key", "revoke", "vk_online123", "--url", ts.URL})
		if err := rootRevoke.Execute(); err != nil {
			t.Fatalf("key revoke online failed: %v", err)
		}
	})
}
