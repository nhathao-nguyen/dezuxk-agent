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

type fakeSessionRepo struct {
	account *domain.ManagedAccount
	err     error
}

func (f *fakeSessionRepo) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.account, nil
}
func (f *fakeSessionRepo) Release(account *domain.ManagedAccount, err error) {}
func (f *fakeSessionRepo) ListAll(ctx context.Context) []*domain.ManagedAccount {
	return nil
}
func (f *fakeSessionRepo) Save(ctx context.Context, account *domain.ManagedAccount) error {
	return nil
}
func (f *fakeSessionRepo) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	return f.account, nil
}
func (f *fakeSessionRepo) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	return nil
}
func (f *fakeSessionRepo) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {}
func (f *fakeSessionRepo) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	return true
}
func (f *fakeSessionRepo) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
}
func (f *fakeSessionRepo) GetAlerts() []domain.SessionAlert   { return nil }
func (f *fakeSessionRepo) AddAlert(alert domain.SessionAlert) {}
func (f *fakeSessionRepo) ClearAlerts(accountID string)       {}

func testRouter(metrics *domain.ContractMetrics) http.Handler {
	return BuildRouter(RouterDependencies{
		Config:        &config.Config{Server: config.ServerConfig{AllowedOrigins: []string{"*"}}},
		ModelRegistry: domain.NewModelRegistry(nil),
		Metrics:       metrics,
	})
}

func TestHealthKeepsModelCount(t *testing.T) {
	metrics := domain.NewContractMetrics()
	metrics.AddSchema()
	handler := testRouter(metrics)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["models_active"].(float64) != 0 {
		t.Fatalf("health = %s", rec.Body.String())
	}
	contract := body["contract"].(map[string]any)
	if contract["schema_unexpected"].(float64) != 1 {
		t.Fatalf("contract = %v", contract)
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
