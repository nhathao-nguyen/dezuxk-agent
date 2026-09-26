package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
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
func (f *fakeSessionRepo) GetAlerts() []domain.SessionAlert { return nil }
func (f *fakeSessionRepo) AddAlert(alert domain.SessionAlert) {}
func (f *fakeSessionRepo) ClearAlerts(accountID string)     {}

type fakeFlow struct {
	balance domain.FlowCreditBalance
	err     error
	calls   int
}

func (f *fakeFlow) GetCreditsBalance(ctx context.Context, account *domain.ManagedAccount) (domain.FlowCreditBalance, error) {
	f.calls++
	return f.balance, f.err
}
func (f *fakeFlow) RegisterSessionLock(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *fakeFlow) CreateProject(ctx context.Context, account *domain.ManagedAccount, title string) (string, error) {
	return "", nil
}
func (f *fakeFlow) ListProjects(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowProject, error) {
	return nil, nil
}
func (f *fakeFlow) MoveProjectToTrash(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *fakeFlow) ListTrash(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowTrashProject, error) {
	return nil, nil
}
func (f *fakeFlow) RestoreProject(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *fakeFlow) DeleteProjectPermanently(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *fakeFlow) GetActiveModels(ctx context.Context, account *domain.ManagedAccount) (map[string]bool, error) {
	return map[string]bool{"abra": true}, nil
}
func (f *fakeFlow) ListVoicePersonas(ctx context.Context, account *domain.ManagedAccount, projectUUID string) ([]domain.VoicePersona, error) {
	return domain.DefaultVoicePersonas(), nil
}
type fakeFlowCreditBridge struct {
	repo ports.SessionRepository
	flow ports.FlowClient
}

func (b *fakeFlowCreditBridge) GetCredits(ctx context.Context) (string, domain.FlowCreditBalance, error) {
	acc, err := b.repo.GetAvailable(ctx, domain.ServiceFlow, 0)
	if err != nil {
		return "", domain.FlowCreditBalance{}, err
	}
	defer b.repo.Release(acc, err)
	bal, err := b.flow.GetCreditsBalance(ctx, acc)
	if err != nil {
		return "", domain.FlowCreditBalance{}, err
	}
	return acc.ID, bal, nil
}

func testRouter(repo ports.SessionRepository, flow ports.FlowClient, metrics *domain.ContractMetrics) http.Handler {
	return testRouterWithMedia(repo, flow, nil, metrics)
}

func testRouterWithMedia(repo ports.SessionRepository, flow ports.FlowClient, media ports.MediaUseCase, metrics *domain.ContractMetrics) http.Handler {
	return BuildRouter(RouterDependencies{
		Config:            &config.Config{Server: config.ServerConfig{AllowedOrigins: []string{"*"}}},
		ModelRegistry:     domain.NewModelRegistry(nil),
		FlowCreditUseCase: &fakeFlowCreditBridge{repo: repo, flow: flow},
		MediaUseCase:      media,
		Metrics:           metrics,
	})
}

func TestHealthKeepsModelCount(t *testing.T) {
	metrics := domain.NewContractMetrics()
	metrics.AddSchema()
	handler := testRouter(&fakeSessionRepo{}, &fakeFlow{}, metrics)
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

func TestFlowCreditsFacade(t *testing.T) {
	repo := &fakeSessionRepo{account: &domain.ManagedAccount{ID: "lab"}}
	flow := &fakeFlow{balance: domain.FlowCreditBalance{Amount: 1050, UnmappedFields: 5, SpecVersion: domain.FlowCreditSpecVersion}}
	handler := testRouter(repo, flow, domain.NewContractMetrics())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/flow/credits", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	data := body["data"].(map[string]any)
	if data["total_credits"].(float64) != 1050 || data["account_id"] != "lab" {
		t.Fatalf("data = %v", data)
	}
	if _, ok := data["tier"]; ok {
		t.Fatal("tier must not be invented from nzlxg")
	}
	meta := body["meta"].(map[string]any)
	if meta["operation"] != domain.OpFlowGetCredits || meta["spec_version"] != domain.FlowCreditSpecVersion {
		t.Fatalf("meta = %v", meta)
	}
	if meta["unmapped_fields"].(float64) != 5 {
		t.Fatalf("meta = %v", meta)
	}
	if meta["correlation_id"] == "" {
		t.Fatal("missing correlation id")
	}
}

func TestFlowCreditsFacade_HidesRawError(t *testing.T) {
	repo := &fakeSessionRepo{account: &domain.ManagedAccount{ID: "lab"}}
	flow := &fakeFlow{err: fmt.Errorf("cookie SUPERSECRET")}
	handler := testRouter(repo, flow, domain.NewContractMetrics())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/flow/credits", nil))
	if strings.Contains(rec.Body.String(), "SUPERSECRET") {
		t.Fatalf("secret leaked: %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	errBody := body["error"].(map[string]any)
	if errBody["class"] != string(domain.ClassUpstreamRejected) {
		t.Fatalf("error = %v", errBody)
	}
	meta := body["meta"].(map[string]any)
	if _, ok := meta["origin_operation"]; ok {
		t.Fatal("origin_operation must not be exposed at top-level meta")
	}
	if _, ok := meta["origin_status"]; ok {
		t.Fatal("origin_status must not be exposed at top-level meta")
	}
	if debug, ok := meta["debug"].(map[string]any); ok {
		if debug["origin_operation"] != "" && debug["origin_operation"] != domain.OriginNzlxg {
			t.Fatalf("debug origin_operation = %v", debug["origin_operation"])
		}
	}
}

type fakeMedia struct {
	result      *domain.ImageGenerationResult
	videoResult *domain.VideoGenerationResult
	err         error
	calls       int
}

func (f *fakeMedia) GenerateImage(ctx context.Context, req *domain.ImageGenerationRequest) (*domain.ImageGenerationResult, error) {
	f.calls++
	return f.result, f.err
}
func (f *fakeMedia) GenerateVideo(ctx context.Context, req *domain.VideoGenerationRequest) (*domain.VideoGenerationResult, error) {
	f.calls++
	return f.videoResult, f.err
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

func TestImageFacade(t *testing.T) {
	media := &fakeMedia{result: &domain.ImageGenerationResult{
		URL: "https://lh3.googleusercontent.com/ai-sandbox/output_777.png", SpecVersion: domain.FlowMediaSpecVersion,
	}}
	handler := testRouterWithMedia(&fakeSessionRepo{}, &fakeFlow{}, media, domain.NewContractMetrics())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"abra-imagen-3","prompt":"a cat"}`))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "output_777.png") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	media.err = fmt.Errorf("cookie SUPERSECRET")
	media.result = nil
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"abra-imagen-3","prompt":"a cat"}`))
	handler.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "SUPERSECRET") || rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestDisabledOperationDoesNotCallOrigin(t *testing.T) {
	off := false
	flow := &fakeFlow{balance: domain.FlowCreditBalance{Amount: 1050, SpecVersion: domain.FlowCreditSpecVersion}}
	media := &fakeMedia{result: &domain.ImageGenerationResult{URL: "https://lh3.googleusercontent.com/x.png"}}
	chat := &countingChat{}
	handler := BuildRouter(RouterDependencies{
		Config: &config.Config{
			Server: config.ServerConfig{AllowedOrigins: []string{"*"}},
			Operations: config.Operations{
				FlowGetCredits:    &off,
				ChatCompletions:   &off,
				ImagesGenerations: &off,
				VideosGenerations: &off,
			},
		},
		ModelRegistry:     domain.NewModelRegistry(domain.GetGeminiCatalog()),
		ChatUseCase:       chat,
		FlowCreditUseCase: &fakeFlowCreditBridge{repo: &fakeSessionRepo{account: &domain.ManagedAccount{ID: "lab"}}, flow: flow},
		MediaUseCase:      media,
		Metrics:           domain.NewContractMetrics(),
	})

	credits := httptest.NewRecorder()
	handler.ServeHTTP(credits, httptest.NewRequest(http.MethodGet, "/v1/flow/credits", nil))
	assertDisabled(t, credits, domain.OpFlowGetCredits, false)

	chatRec := httptest.NewRecorder()
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"hi"}]}`))
	handler.ServeHTTP(chatRec, chatReq)
	assertDisabled(t, chatRec, domain.OpChatCompletions, true)
	if strings.Contains(chatRec.Body.String(), "should-not-run") {
		t.Fatalf("chat body %s", chatRec.Body.String())
	}

	image := httptest.NewRecorder()
	handler.ServeHTTP(image, httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"abra-imagen-3","prompt":"a cat"}`)))
	assertDisabled(t, image, domain.OpImages, false)

	video := httptest.NewRecorder()
	handler.ServeHTTP(video, httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"model":"veo-3.1-fast","prompt":"a river"}`)))
	assertDisabled(t, video, domain.OpVideos, false)

	if flow.calls != 0 || media.calls != 0 || chat.calls != 0 {
		t.Fatalf("origin calls flow=%d media=%d chat=%d", flow.calls, media.calls, chat.calls)
	}
}

func assertDisabled(t *testing.T, rec *httptest.ResponseRecorder, operation string, chat bool) {
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
	if chat {
		errBody := body["error"].(map[string]any)
		if errBody["type"] != string(domain.ClassUpstreamRejected) || errBody["message"] != "operation đang tắt" {
			t.Fatalf("chat error %v", errBody)
		}
		if _, ok := body["choices"]; ok {
			t.Fatal("disabled chat returned an OpenAI success body")
		}
		return
	}
	errBody := body["error"].(map[string]any)
	if errBody["class"] != string(domain.ClassUpstreamRejected) || errBody["message"] != "operation đang tắt" || body["data"] != nil {
		t.Fatalf("%s body %v", operation, body)
	}
}

func TestVideoFacade_WithCreditsBalance(t *testing.T) {
	bal := 1030
	media := &fakeMedia{videoResult: &domain.VideoGenerationResult{
		URL:            "https://storage.googleapis.com/test.mp4",
		SpecVersion:    domain.FlowMediaSpecVersion,
		CreditsBalance: &bal,
	}}
	handler := testRouterWithMedia(&fakeSessionRepo{}, &fakeFlow{}, media, domain.NewContractMetrics())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{"model":"veo-3.1-quality","prompt":"a river","duration":8,"aspect_ratio":"16:9"}`))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	credits, ok := body["credits"].(map[string]any)
	if !ok || credits["total_credits"].(float64) != 1030 {
		t.Fatalf("credits = %v", body["credits"])
	}
	meta := body["meta"].(map[string]any)
	if meta["credits_balance"].(float64) != 1030 {
		t.Fatalf("meta = %v", meta)
	}
}
