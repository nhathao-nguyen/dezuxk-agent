package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/inbound/cli"
	"dezuxk-gateway/internal/core/domain"
)

func TestCLI_AIOps_Registration(t *testing.T) {
	root := cli.NewRootCmd()

	expectedCommands := []string{"chat", "flow", "gemini", "cache", "alerts"}
	for _, name := range expectedCommands {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd == nil || cmd.Name() != name {
			t.Fatalf("expected subcommand %q to be registered on RootCmd", name)
		}
	}

	// Verify chat flags
	chatCmd, _, _ := root.Find([]string{"chat"})
	for _, flagName := range []string{"prompt", "model", "stream", "system", "image", "temp"} {
		if chatCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("chat command missing flag: --%s", flagName)
		}
	}

	// Verify flow subcommands
	flowCmd, _, _ := root.Find([]string{"flow"})
	for _, sub := range []string{"credits", "projects", "voices", "audio"} {
		subCmd, _, err := flowCmd.Find([]string{sub})
		if err != nil || subCmd == nil || subCmd.Name() != sub {
			t.Errorf("flow command missing subcommand: %s", sub)
		}
	}

	// Verify flow projects subcommands
	projectsCmd, _, _ := flowCmd.Find([]string{"projects"})
	for _, pSub := range []string{"list", "create", "trash", "restore", "delete"} {
		subCmd, _, err := projectsCmd.Find([]string{pSub})
		if err != nil || subCmd == nil || subCmd.Name() != pSub {
			t.Errorf("flow projects missing subcommand: %s", pSub)
		}
	}

	// Verify gemini subcommands
	geminiCmd, _, _ := root.Find([]string{"gemini"})
	for _, sub := range []string{"usage", "conversations"} {
		subCmd, _, err := geminiCmd.Find([]string{sub})
		if err != nil || subCmd == nil || subCmd.Name() != sub {
			t.Errorf("gemini command missing subcommand: %s", sub)
		}
	}

	// Verify gemini conversations subcommands
	convCmd, _, _ := geminiCmd.Find([]string{"conversations"})
	for _, cSub := range []string{"list", "get", "rename", "delete"} {
		subCmd, _, err := convCmd.Find([]string{cSub})
		if err != nil || subCmd == nil || subCmd.Name() != cSub {
			t.Errorf("gemini conversations missing subcommand: %s", cSub)
		}
	}

	// Verify cache subcommands
	cacheCmd, _, _ := root.Find([]string{"cache"})
	for _, sub := range []string{"status", "purge"} {
		subCmd, _, err := cacheCmd.Find([]string{sub})
		if err != nil || subCmd == nil || subCmd.Name() != sub {
			t.Errorf("cache command missing subcommand: %s", sub)
		}
	}

	// Verify alerts subcommands
	alertsCmd, _, _ := root.Find([]string{"alerts"})
	for _, sub := range []string{"list", "clear"} {
		subCmd, _, err := alertsCmd.Find([]string{sub})
		if err != nil || subCmd == nil || subCmd.Name() != sub {
			t.Errorf("alerts command missing subcommand: %s", sub)
		}
	}
}

func TestCLI_AIOps_CacheOffline(t *testing.T) {
	cfgPath, _ := createTestEnvironment(t)

	// Pre-populate response cache in offline direct DB mode
	direct, err := cli.InitDirectServices(cfgPath)
	if err != nil {
		t.Fatalf("InitDirectServices failed: %v", err)
	}
	direct.ResponseCache.Set("test-key", []byte("cached-payload"), "application/json", nil)
	_ = direct.Close()

	t.Run("cache status table format offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"cache", "status", "--config", cfgPath, "--offline", "--format", "table"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("cache status table failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "ENTRIES") && !strings.Contains(out, "Entries") {
			t.Errorf("expected table output to have entries header, got: %s", out)
		}
	})

	t.Run("cache status json format offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"cache", "status", "--config", cfgPath, "--offline", "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("cache status json failed: %v", err)
		}

		var stats map[string]any
		if err := json.Unmarshal(buf.Bytes(), &stats); err != nil {
			t.Fatalf("failed to parse cache status json: %v, raw: %s", err, buf.String())
		}
		if _, ok := stats["total_entries"]; !ok {
			t.Errorf("expected total_entries in json stats, got: %v", stats)
		}
	})

	t.Run("cache purge offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"cache", "purge", "--config", cfgPath, "--offline"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("cache purge failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(strings.ToLower(out), "purge") && !strings.Contains(strings.ToLower(out), "xóa") && !strings.Contains(strings.ToLower(out), "clear") {
			t.Errorf("expected purge confirmation message, got: %s", out)
		}
	})
}

func TestCLI_AIOps_AlertsOffline(t *testing.T) {
	cfgPath, _ := createTestEnvironment(t)

	// Pre-populate alert in direct DB
	direct, err := cli.InitDirectServices(cfgPath)
	if err != nil {
		t.Fatalf("InitDirectServices failed: %v", err)
	}
	alert := domain.SessionAlert{
		AccountID:      "acc_alert_1",
		Service:        domain.ServiceGemini,
		Reason:         "Token expired or unauthorized",
		StatusCode:     401,
		ActionRequired: "Re-login via Chrome",
		CreatedAt:      time.Now(),
	}
	direct.SessionRepo.AddAlert(alert)
	_ = direct.Close()

	t.Run("alerts list table format offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"alerts", "list", "--config", cfgPath, "--offline", "--format", "table"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("alerts list table failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "acc_alert_1") {
			t.Errorf("expected output to contain acc_alert_1, got: %s", out)
		}
		if !strings.Contains(out, "Token expired") {
			t.Errorf("expected output to contain alert reason, got: %s", out)
		}
	})

	t.Run("alerts list json format offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"alerts", "list", "--config", cfgPath, "--offline", "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("alerts list json failed: %v", err)
		}

		var alerts []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &alerts); err != nil {
			t.Fatalf("failed to parse alerts json: %v, raw: %s", err, buf.String())
		}
		if len(alerts) == 0 {
			t.Fatalf("expected at least 1 alert, got 0")
		}
		if alerts[0]["account_id"] != "acc_alert_1" {
			t.Errorf("expected account_id acc_alert_1, got %v", alerts[0]["account_id"])
		}
	})

	t.Run("alerts clear offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"alerts", "clear", "--config", cfgPath, "--offline"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("alerts clear failed: %v", err)
		}

		// Verify list is empty
		bufList := new(bytes.Buffer)
		rootList := cli.NewRootCmd()
		rootList.SetOut(bufList)
		rootList.SetErr(bufList)
		rootList.SetArgs([]string{"alerts", "list", "--config", cfgPath, "--offline", "--format", "json"})
		if err := rootList.Execute(); err != nil {
			t.Fatalf("alerts list after clear failed: %v", err)
		}

		var alertsAfter []map[string]any
		_ = json.Unmarshal(bufList.Bytes(), &alertsAfter)
		if len(alertsAfter) != 0 {
			t.Errorf("expected 0 alerts after clear, got %d", len(alertsAfter))
		}
	})
}

func TestCLI_AIOps_FlowOffline(t *testing.T) {
	cfgPath, _ := createTestEnvironment(t)

	t.Run("flow voices table offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"flow", "voices", "--config", cfgPath, "--offline", "--format", "table"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("flow voices offline failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "Achernar") && !strings.Contains(out, "achernar") {
			t.Errorf("expected flow voices to list default voices, got: %s", out)
		}
	})

	t.Run("flow voices json offline", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"flow", "voices", "--config", cfgPath, "--offline", "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("flow voices json offline failed: %v", err)
		}

		var voices []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &voices); err != nil {
			t.Fatalf("failed to parse json voices: %v, raw: %s", err, buf.String())
		}
		if len(voices) < 5 {
			t.Errorf("expected at least 5 voices, got %d", len(voices))
		}
	})
}

func TestCLI_AIOps_OnlineEndpoints(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/ready":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ready":true}`))

		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			stream, _ := req["stream"].(bool)
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, ok := w.(http.Flusher)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"Hello"}}]}`)
				if ok {
					flusher.Flush()
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":" World!"}}]}`)
				if ok {
					flusher.Flush()
				}
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				if ok {
					flusher.Flush()
				}
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "chatcmpl-test",
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role":    "assistant",
							"content": "Hello Non-Stream World!",
						},
					},
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/flow/credits":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"account_id":      "acc_flow_test",
					"credits_balance": 350,
					"tier":            "Pro",
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/flow/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":         "proj_123",
						"title":      "My Test Project",
						"updated_at": "2026-09-26T12:00:00Z",
						"is_active":  true,
					},
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/v1/flow/projects":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			title, _ := body["title"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":    "proj_new_999",
					"title": title,
				},
			})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/flow/projects/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v1/flow/trash/") && strings.HasSuffix(r.URL.Path, "/restore"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/flow/trash/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/flow/voices":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "achernar", "name": "Achernar", "gender": "Nữ", "description": "Nhẹ nhàng"},
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/v1/flow/audio/generate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"asset_id":         "audio_123",
					"url":              "http://media/audio_123.wav",
					"duration_seconds": 8.0,
					"credits_deducted": 5,
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/gemini/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"quota_5h":      15.5,
					"quota_weekly":  45.0,
					"rpm_limit":     60,
					"reset_time_5h": "2026-09-26T18:00:00Z",
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/gemini/conversations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"conversations": []map[string]any{
						{"id": "conv_123", "title": "Chat Session 1", "updated_at": "2026-09-26T10:00:00Z"},
					},
				},
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/gemini/conversations/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"conversation_id": "conv_123",
					"title":           "Chat Session 1",
					"turns": []map[string]any{
						{
							"turn_id":     "t1",
							"user_prompt": "Hello AI",
							"choices":     []map[string]any{{"choice_id": "c1", "content": "Hi there!"}},
						},
					},
				},
			})

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v1/gemini/conversations/") && strings.HasSuffix(r.URL.Path, "/rename"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/gemini/conversations/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/alerts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"alerts": []map[string]any{
					{
						"account_id": "acc_online_alert",
						"reason":     "Rate limited",
						"created_at": "2026-09-26T12:00:00Z",
					},
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/v1/alerts/clear":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/overview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"cache": map[string]any{
					"hits":          100,
					"misses":        20,
					"total_entries": 42,
					"max_entries":   10000,
					"hit_ratio":     83.33,
				},
			})

		case r.Method == http.MethodPost && (r.URL.Path == "/v1/admin/cache/purge" || r.URL.Path == "/v1/cache/purge"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "Purged"})

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	t.Run("chat streaming online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"chat", "--url", ts.URL, "-p", "Say hello"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("chat streaming failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "Hello World!") {
			t.Errorf("expected streamed output to contain Hello World!, got: %s", out)
		}
	})

	t.Run("chat non-streaming with stdin online", func(t *testing.T) {
		root := cli.NewRootCmd()
		bufOut := new(bytes.Buffer)
		stdinBuf := bytes.NewBufferString("Prompt from stdin")
		root.SetIn(stdinBuf)
		root.SetOut(bufOut)
		root.SetErr(bufOut)
		root.SetArgs([]string{"chat", "--url", ts.URL, "--stream=false"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("chat non-streaming failed: %v", err)
		}

		out := bufOut.String()
		if !strings.Contains(out, "Hello Non-Stream World!") {
			t.Errorf("expected output to contain non-stream content, got: %s", out)
		}
	})

	t.Run("flow credits online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"flow", "credits", "--url", ts.URL, "--format", "json"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("flow credits failed: %v", err)
		}

		var res map[string]any
		if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse flow credits json: %v", err)
		}
		if !strings.Contains(buf.String(), "350") {
			t.Errorf("expected credits balance 350, got: %s", buf.String())
		}
	})

	t.Run("flow projects lifecycle online", func(t *testing.T) {
		rootList := cli.NewRootCmd()
		bufList := new(bytes.Buffer)
		rootList.SetOut(bufList)
		rootList.SetErr(bufList)
		rootList.SetArgs([]string{"flow", "projects", "list", "--url", ts.URL, "--format", "table"})
		if err := rootList.Execute(); err != nil {
			t.Fatalf("flow projects list failed: %v", err)
		}
		if !strings.Contains(bufList.String(), "proj_123") {
			t.Errorf("expected proj_123 in projects list, got: %s", bufList.String())
		}

		rootCreate := cli.NewRootCmd()
		bufCreate := new(bytes.Buffer)
		rootCreate.SetOut(bufCreate)
		rootCreate.SetErr(bufCreate)
		rootCreate.SetArgs([]string{"flow", "projects", "create", "New Demo", "--url", ts.URL})
		if err := rootCreate.Execute(); err != nil {
			t.Fatalf("flow projects create failed: %v", err)
		}
		if !strings.Contains(bufCreate.String(), "proj_new_999") {
			t.Errorf("expected proj_new_999 in create output, got: %s", bufCreate.String())
		}

		rootTrash := cli.NewRootCmd()
		bufTrash := new(bytes.Buffer)
		rootTrash.SetOut(bufTrash)
		rootTrash.SetErr(bufTrash)
		rootTrash.SetArgs([]string{"flow", "projects", "trash", "proj_new_999", "--url", ts.URL})
		if err := rootTrash.Execute(); err != nil {
			t.Fatalf("flow projects trash failed: %v", err)
		}

		rootRestore := cli.NewRootCmd()
		bufRestore := new(bytes.Buffer)
		rootRestore.SetOut(bufRestore)
		rootRestore.SetErr(bufRestore)
		rootRestore.SetArgs([]string{"flow", "projects", "restore", "proj_new_999", "--url", ts.URL})
		if err := rootRestore.Execute(); err != nil {
			t.Fatalf("flow projects restore failed: %v", err)
		}

		rootDelete := cli.NewRootCmd()
		bufDelete := new(bytes.Buffer)
		rootDelete.SetOut(bufDelete)
		rootDelete.SetErr(bufDelete)
		rootDelete.SetArgs([]string{"flow", "projects", "delete", "proj_new_999", "--url", ts.URL})
		if err := rootDelete.Execute(); err != nil {
			t.Fatalf("flow projects delete failed: %v", err)
		}
	})

	t.Run("flow audio online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"flow", "audio", "--prompt", "cyberpunk beat", "--duration", "8", "--url", ts.URL, "--format", "json"})

		if err := root.Execute(); err != nil {
			t.Fatalf("flow audio failed: %v", err)
		}
		if !strings.Contains(buf.String(), "audio_123") {
			t.Errorf("expected audio_123 in audio generate output, got: %s", buf.String())
		}
	})

	t.Run("gemini usage online", func(t *testing.T) {
		root := cli.NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"gemini", "usage", "--url", ts.URL, "--format", "table"})

		if err := root.Execute(); err != nil {
			t.Fatalf("gemini usage failed: %v", err)
		}
		if !strings.Contains(buf.String(), "15.5") {
			t.Errorf("expected 15.5%% quota in usage output, got: %s", buf.String())
		}
	})

	t.Run("gemini conversations lifecycle online", func(t *testing.T) {
		rootList := cli.NewRootCmd()
		bufList := new(bytes.Buffer)
		rootList.SetOut(bufList)
		rootList.SetErr(bufList)
		rootList.SetArgs([]string{"gemini", "conversations", "list", "--url", ts.URL, "--format", "table"})
		if err := rootList.Execute(); err != nil {
			t.Fatalf("gemini conversations list failed: %v", err)
		}
		if !strings.Contains(bufList.String(), "conv_123") {
			t.Errorf("expected conv_123 in conversations list, got: %s", bufList.String())
		}

		rootGet := cli.NewRootCmd()
		bufGet := new(bytes.Buffer)
		rootGet.SetOut(bufGet)
		rootGet.SetErr(bufGet)
		rootGet.SetArgs([]string{"gemini", "conversations", "get", "conv_123", "--url", ts.URL, "--format", "table"})
		if err := rootGet.Execute(); err != nil {
			t.Fatalf("gemini conversations get failed: %v", err)
		}
		if !strings.Contains(bufGet.String(), "Hello AI") {
			t.Errorf("expected Hello AI in conversation turns, got: %s", bufGet.String())
		}

		rootRename := cli.NewRootCmd()
		bufRename := new(bytes.Buffer)
		rootRename.SetOut(bufRename)
		rootRename.SetErr(bufRename)
		rootRename.SetArgs([]string{"gemini", "conversations", "rename", "conv_123", "New Name", "--url", ts.URL})
		if err := rootRename.Execute(); err != nil {
			t.Fatalf("gemini conversations rename failed: %v", err)
		}

		rootDel := cli.NewRootCmd()
		bufDel := new(bytes.Buffer)
		rootDel.SetOut(bufDel)
		rootDel.SetErr(bufDel)
		rootDel.SetArgs([]string{"gemini", "conversations", "delete", "conv_123", "--url", ts.URL})
		if err := rootDel.Execute(); err != nil {
			t.Fatalf("gemini conversations delete failed: %v", err)
		}
	})

	t.Run("alerts online", func(t *testing.T) {
		rootList := cli.NewRootCmd()
		bufList := new(bytes.Buffer)
		rootList.SetOut(bufList)
		rootList.SetErr(bufList)
		rootList.SetArgs([]string{"alerts", "list", "--url", ts.URL, "--format", "table"})
		if err := rootList.Execute(); err != nil {
			t.Fatalf("alerts list online failed: %v", err)
		}
		if !strings.Contains(bufList.String(), "acc_online_alert") {
			t.Errorf("expected acc_online_alert in alerts list, got: %s", bufList.String())
		}

		rootClear := cli.NewRootCmd()
		bufClear := new(bytes.Buffer)
		rootClear.SetOut(bufClear)
		rootClear.SetErr(bufClear)
		rootClear.SetArgs([]string{"alerts", "clear", "--url", ts.URL})
		if err := rootClear.Execute(); err != nil {
			t.Fatalf("alerts clear online failed: %v", err)
		}
	})

	t.Run("cache status and purge online", func(t *testing.T) {
		rootStatus := cli.NewRootCmd()
		bufStatus := new(bytes.Buffer)
		rootStatus.SetOut(bufStatus)
		rootStatus.SetErr(bufStatus)
		rootStatus.SetArgs([]string{"cache", "status", "--url", ts.URL, "--format", "json"})
		if err := rootStatus.Execute(); err != nil {
			t.Fatalf("cache status online failed: %v", err)
		}
		if !strings.Contains(bufStatus.String(), "42") {
			t.Errorf("expected 42 entries in cache status, got: %s", bufStatus.String())
		}

		rootPurge := cli.NewRootCmd()
		bufPurge := new(bytes.Buffer)
		rootPurge.SetOut(bufPurge)
		rootPurge.SetErr(bufPurge)
		rootPurge.SetArgs([]string{"cache", "purge", "--url", ts.URL})
		if err := rootPurge.Execute(); err != nil {
			t.Fatalf("cache purge online failed: %v", err)
		}
	})
}
