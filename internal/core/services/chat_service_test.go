package services_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type mockSessionRepo struct {
	refreshes int
	denyLease bool
	releases  int
}

func (m *mockSessionRepo) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
	return &domain.ManagedAccount{
		ID:           "test",
		GeminiSNlM0e: "fake-sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "fake"}),
	}, nil
}
func (m *mockSessionRepo) Release(account *domain.ManagedAccount, err error) {
	m.releases++
}
func (m *mockSessionRepo) ListAll(ctx context.Context) []*domain.ManagedAccount           { return nil }
func (m *mockSessionRepo) Save(ctx context.Context, account *domain.ManagedAccount) error { return nil }
func (m *mockSessionRepo) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	return nil, nil
}
func (m *mockSessionRepo) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	m.refreshes++
	return nil
}
func (m *mockSessionRepo) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {}
func (m *mockSessionRepo) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	if m.denyLease {
		return false
	}
	return true
}
func (m *mockSessionRepo) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
}
func (m *mockSessionRepo) GetAlerts() []domain.SessionAlert   { return nil }
func (m *mockSessionRepo) AddAlert(alert domain.SessionAlert) {}
func (m *mockSessionRepo) ClearAlerts(accountID string)       {}

type secretTransport struct{}

func (secretTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (secretTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (secretTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader("SUPERSECRET_COOKIE")),
		Header:     make(http.Header),
	}, nil
}

func TestChatService_RejectUnknownModelInChat(t *testing.T) {
	mr := domain.NewModelRegistry(nil)
	sr := &mockSessionRepo{}

	chatService := services.NewChatService(mr, sr, nil, nil, nil)

	req := &domain.OpenAIChatRequest{
		Model: "unknown-model",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	_, err := chatService.ExecuteChatSync(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when sending unknown model to Chat completions, got nil")
	}
}

func TestChatService_UpstreamBodyIsNotReturned(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	chatService := services.NewChatService(mr, repo, secretTransport{}, google.NewWireAdapter(nil), nil)
	_, err := chatService.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Hello"},
		},
	})
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if strings.Contains(err.Error(), "SUPERSECRET_COOKIE") {
		t.Fatalf("secret leaked: %s", err.Error())
	}
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassExpired {
		t.Fatalf("error = %v", err)
	}
	if repo.refreshes != 1 {
		t.Fatalf("refreshes = %d", repo.refreshes)
	}
}

func TestChatService_EmptyStreamIsSchemaError(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	transport := emptyTransport{}
	metrics := domain.NewContractMetrics()
	chatService := services.NewChatService(mr, repo, transport, google.NewWireAdapter(nil), metrics)
	_, err := chatService.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{
		Model:    "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{{Role: "user", Content: "Hello"}},
	})
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassSchemaUnexpected {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("secret leaked: %s", err.Error())
	}
	op := metrics.Snapshot().Operations[domain.OpChatCompletions]
	if op.SchemaUnexpected != 1 {
		t.Fatalf("chat schema = %+v", metrics.Snapshot())
	}
}

type unavailableTransport struct {
	calls int
}

func (u *unavailableTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (u *unavailableTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (u *unavailableTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("down")), Header: make(http.Header)}, nil
}

func TestChatService_UnavailableIsNotResent(t *testing.T) {
	transport := &unavailableTransport{}
	chatService := services.NewChatService(domain.NewModelRegistry(domain.GetGeminiCatalog()), &mockSessionRepo{}, transport, google.NewWireAdapter(nil), nil)
	_, err := chatService.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{
		Model:    "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if transport.calls != 1 {
		t.Fatalf("calls = %d", transport.calls)
	}
}

type emptyTransport struct{}

func (emptyTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (emptyTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (emptyTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func TestChatService_ConcurrentChatOnSameAccountConflicts(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{denyLease: true}
	chatService := services.NewChatService(mr, repo, nil, nil, nil)

	_, err := chatService.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{
		Model:    "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected conflict error when write lease is denied")
	}

	ge, ok := domain.AsGatewayError(err)
	if !ok {
		t.Fatalf("expected GatewayError, got %T: %v", err, err)
	}
	if ge.Class != domain.ClassConflict {
		t.Fatalf("expected ClassConflict, got %v", ge.Class)
	}
	if ge.HTTPStatus() != http.StatusConflict {
		t.Fatalf("expected HTTP 409, got %d", ge.HTTPStatus())
	}
	if ge.OriginOperation != domain.OriginStreamGenerate {
		t.Fatalf("expected OriginStreamGenerate, got %s", ge.OriginOperation)
	}
	if repo.releases != 1 {
		t.Fatalf("expected account to be released upon lease denial, got %d releases", repo.releases)
	}
}

type mockMediaRepo struct {
	calledWithURL string
}

func (m *mockMediaRepo) SaveAsset(ctx context.Context, asset *domain.MediaAsset, content io.Reader) error {
	return nil
}
func (m *mockMediaRepo) GetAsset(ctx context.Context, assetID string) (*domain.MediaAsset, io.ReadCloser, error) {
	return nil, nil, nil
}
func (m *mockMediaRepo) ListAssets(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	return nil, nil
}
func (m *mockMediaRepo) DeleteExpired(ctx context.Context, maxAgeDays int) (int, error) {
	return 0, nil
}
func (m *mockMediaRepo) DownloadAndCache(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string) (*domain.MediaAsset, error) {
	return m.DownloadAndCacheWithAuth(ctx, remoteURL, kind, prompt, model, "", "")
}
func (m *mockMediaRepo) DownloadAndCacheWithAuth(ctx context.Context, remoteURL string, kind domain.MediaKind, prompt string, model string, cookies string, userAgent string) (*domain.MediaAsset, error) {
	m.calledWithURL = remoteURL
	return &domain.MediaAsset{
		ID:       "hash123",
		LocalURL: "http://localhost:8080/media/hash123.png",
		IsReady:  true,
	}, nil
}
func (m *mockMediaRepo) ServeAssetHTTP(w http.ResponseWriter, r *http.Request, assetID string) error {
	return nil
}

type mediaChatCodec struct{}

func (mediaChatCodec) MaterializeChat(account *domain.ManagedAccount, payload domain.GeminiPayloadBuilder) (domain.OutboundAttempt, error) {
	return domain.OutboundAttempt{
		Path:        "/test",
		Body:        "test",
		ContentType: "application/x-www-form-urlencoded",
	}, nil
}
func (mediaChatCodec) DematerializeChat(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error) {
	return domain.GeminiReply{
		Text:           "Đây là ảnh mèo:\n![Hình ảnh](https://lh3.googleusercontent.com/rd-gg-dl/cat123)",
		MediaURLs:      []string{"https://lh3.googleusercontent.com/rd-gg-dl/cat123"},
		ConversationID: "c_123",
	}, nil
}

func TestChatService_CachesMediaURLs(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	repo := &mockSessionRepo{}
	metrics := domain.NewContractMetrics()
	storageMock := &mockMediaRepo{}

	chatService := services.NewChatService(mr, repo, emptyTransport{}, mediaChatCodec{}, metrics)
	chatService.SetStorage(storageMock)

	resp, err := chatService.ExecuteChatSync(context.Background(), &domain.OpenAIChatRequest{
		Model:    "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{{Role: "user", Content: "Vẽ con mèo"}},
	})
	if err != nil {
		t.Fatalf("ExecuteChatSync failed: %v", err)
	}

	if storageMock.calledWithURL != "https://lh3.googleusercontent.com/rd-gg-dl/cat123" {
		t.Errorf("Expected storage to be called with remote URL, got %s", storageMock.calledWithURL)
	}

	if len(resp.MediaURLs) != 1 || resp.MediaURLs[0] != "http://localhost:8080/media/hash123.png" {
		t.Errorf("Expected resp.MediaURLs to be local facade URL, got %v", resp.MediaURLs)
	}

	expectedText := "Đây là ảnh mèo:\n![Hình ảnh](http://localhost:8080/media/hash123.png)"
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != expectedText {
		t.Errorf("Expected content with local URL replaced, got %s", resp.Choices[0].Message.Content)
	}
}
