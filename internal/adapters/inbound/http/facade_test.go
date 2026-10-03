package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func testRouter(metrics *domain.ContractMetrics) http.Handler {
	return BuildRouter(RouterDependencies{
		Config:        &config.Config{Server: config.ServerConfig{AllowedOrigins: []string{"*"}}},
		ModelRegistry: domain.NewModelRegistry(nil),
		Metrics:       metrics,
	})
}

func TestHealthIsPureLiveness(t *testing.T) {
	metrics := domain.NewContractMetrics()
	metrics.AddSchema()
	handler := testRouter(metrics)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("health status = %v", body["status"])
	}
	if _, ok := body["models_active"]; ok {
		t.Fatalf("/health must not leak models_active: %s", rec.Body.String())
	}
	if _, ok := body["contract"]; ok {
		t.Fatalf("/health must not leak contract metrics: %s", rec.Body.String())
	}
}

type countingChat struct {
	calls int
}

func (c *countingChat) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, streamWriter io.Writer, flusher func()) error {
	c.calls++
	return nil
}
func (c *countingChat) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	c.calls++
	return &domain.OpenAIChatResponse{ID: "should-not-run"}, nil
}

func TestDisabledOperationDoesNotCallOrigin(t *testing.T) {
	off := false
	chat := &countingChat{}
	handler := BuildRouter(RouterDependencies{
		Config: &config.Config{
			Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
			Operations: config.Operations{
				ChatCompletions: &off,
			},
		},
		ModelRegistry: domain.NewModelRegistry(domain.GetGeminiCatalog()),
		ChatUseCase:   chat,
		Metrics:       domain.NewContractMetrics(),
	})

	chatRec := httptest.NewRecorder()
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"hi"}]}`))
	handler.ServeHTTP(chatRec, chatReq)
	assertDisabled(t, chatRec, domain.OpChatCompletions)
	if strings.Contains(chatRec.Body.String(), "should-not-run") {
		t.Fatalf("chat body %s", chatRec.Body.String())
	}

	if chat.calls != 0 {
		t.Fatalf("origin calls chat=%d", chat.calls)
	}
}

func assertDisabled(t *testing.T, rec *httptest.ResponseRecorder, operation string) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%s status %d body %s", operation, rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	meta := body["meta"].(map[string]any)
	if meta["operation"] != operation || meta["retryable"] != false {
		t.Fatalf("%s meta %v", operation, meta)
	}
	errBody := body["error"].(map[string]any)
	if errBody["type"] != string(domain.ClassUpstreamRejected) || errBody["message"] != "operation đang tắt" {
		t.Fatalf("chat error %v", errBody)
	}
	if _, ok := body["choices"]; ok {
		t.Fatal("disabled chat returned an OpenAI success body")
	}
}
