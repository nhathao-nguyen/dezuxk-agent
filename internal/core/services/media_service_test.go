package services_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type mediaTransport struct {
	mu       sync.Mutex
	calls    int
	body     string
	status   int
	response string
	entered  chan struct{}
	hold     chan struct{}
}

func (m *mediaTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (m *mediaTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (m *mediaTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	m.mu.Lock()
	m.calls++
	if body != nil {
		raw, _ := io.ReadAll(body)
		m.body = string(raw)
	}
	m.mu.Unlock()
	if m.entered != nil {
		select {
		case <-m.entered:
		default:
			close(m.entered)
		}
	}
	if m.hold != nil {
		<-m.hold
	}
	status := m.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(m.response)), Header: make(http.Header)}, nil
}

type mediaFlow struct {
	credits   int
	calls     int
	lastTitle string
}

func (f *mediaFlow) GetCreditsBalance(ctx context.Context, account *domain.ManagedAccount) (domain.FlowCreditBalance, error) {
	f.calls++
	return domain.FlowCreditBalance{Amount: f.credits, SpecVersion: domain.FlowCreditSpecVersion}, nil
}
func (f *mediaFlow) RegisterSessionLock(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *mediaFlow) CreateProject(ctx context.Context, account *domain.ManagedAccount, title string) (string, error) {
	f.lastTitle = title
	return "1cfeebb6-4d61-4373-aa21-dd52fd93679f", nil
}
func (f *mediaFlow) ListProjects(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowProject, error) {
	return []domain.FlowProject{{ID: "proj-1", Title: "Dự án 1"}}, nil
}
func (f *mediaFlow) MoveProjectToTrash(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *mediaFlow) ListTrash(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowTrashProject, error) {
	return []domain.FlowTrashProject{{ID: "trash-1", Title: "Dự án rác"}}, nil
}
func (f *mediaFlow) RestoreProject(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *mediaFlow) DeleteProjectPermanently(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	return nil
}
func (f *mediaFlow) GetActiveModels(ctx context.Context, account *domain.ManagedAccount) (map[string]bool, error) {
	return map[string]bool{"abra": true}, nil
}
func (f *mediaFlow) ListVoicePersonas(ctx context.Context, account *domain.ManagedAccount, projectUUID string) ([]domain.VoicePersona, error) {
	return domain.DefaultVoicePersonas(), nil
}

func mediaAccount() *domain.ManagedAccount {
	account := &domain.ManagedAccount{
		ID:         "lab",
		IsHealthy:  true,
		FlowSNlM0e: "flow-at",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "osid"}),
	}
	account.SetFlowProjectID("725b0994-f17a-4526-8ae1-c7c2ac83d041")
	account.SetFlowSessionToken("session-token")
	return account
}

func newMediaRepo(t *testing.T) *session.MemorySessionRepository {
	t.Helper()
	repo := session.NewMemorySessionRepository(nil)
	if err := repo.Save(context.Background(), mediaAccount()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestGenerateVideo_PacksKnownFields(t *testing.T) {
	transport := &mediaTransport{response: `{"status":"COMPLETED","video_asset":{"url":"https://storage.googleapis.com/flow-rendered-videos/output_999.mp4","mime_type":"video/mp4"}}`}
	flow := &mediaFlow{credits: 5000}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), newMediaRepo(t), transport, google.NewWireAdapter(nil), flow, nil, domain.NewContractMetrics())
	result, err := svc.GenerateVideo(context.Background(), &domain.VideoGenerationRequest{
		Model:       "veo-3.1-quality",
		Prompt:      "a river",
		Duration:    8,
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://storage.googleapis.com/flow-rendered-videos/output_999.mp4" || result.MimeType != "video/mp4" {
		t.Fatalf("result = %+v", result)
	}
	values, err := url.ParseQuery(transport.body)
	if err != nil {
		t.Fatal(err)
	}
	freq := values.Get("f.req")
	if !strings.Contains(freq, "veo_3_1_quality") || !strings.Contains(freq, "a river") || !strings.Contains(freq, "session-token") {
		t.Fatalf("freq = %s", freq)
	}
	if strings.Contains(freq, "flow-at") {
		t.Fatal("at token leaked into f.req")
	}
}

func TestGenerateVideo_MissingSessionTokenDoesNotCallOrigin(t *testing.T) {
	transport := &mediaTransport{}
	repo := session.NewMemorySessionRepository(nil)
	account := mediaAccount()
	account.SetFlowSessionToken("")
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), repo, transport, google.NewWireAdapter(nil), &mediaFlow{credits: 5000}, nil, nil)
	_, err := svc.GenerateVideo(context.Background(), &domain.VideoGenerationRequest{
		Model: "veo-3.1-quality", Prompt: "a river", Duration: 8, AspectRatio: "16:9",
	})
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassUnauthenticated {
		t.Fatalf("error = %v", err)
	}
	if transport.calls != 0 {
		t.Fatalf("origin calls = %d", transport.calls)
	}
}

func TestGenerateVideo_InsufficientCredits(t *testing.T) {
	transport := &mediaTransport{}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), newMediaRepo(t), transport, google.NewWireAdapter(nil), &mediaFlow{credits: 1}, nil, nil)
	_, err := svc.GenerateVideo(context.Background(), &domain.VideoGenerationRequest{
		Model: "veo-3.1-quality", Prompt: "a river", Duration: 8, AspectRatio: "16:9",
	})
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassInvalidRequest {
		t.Fatalf("error = %v", err)
	}
	if transport.calls != 0 {
		t.Fatal("origin was called")
	}
}

func TestGenerateImage_HidesSecretBody(t *testing.T) {
	transport := &mediaTransport{status: http.StatusOK, response: `{"note":"SUPERSECRET"}`}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), newMediaRepo(t), transport, google.NewWireAdapter(nil), &mediaFlow{credits: 100}, nil, domain.NewContractMetrics())
	_, err := svc.GenerateImage(context.Background(), &domain.ImageGenerationRequest{Model: "abra-imagen-3", Prompt: "a cat"})
	if err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("error = %v", err)
	}
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassSchemaUnexpected {
		t.Fatalf("error = %v", err)
	}
}

func TestGenerateVideo_UnavailableIsNotResent(t *testing.T) {
	transport := &mediaTransport{status: http.StatusServiceUnavailable, response: "down"}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), newMediaRepo(t), transport, google.NewWireAdapter(nil), &mediaFlow{credits: 5000}, nil, nil)
	_, err := svc.GenerateVideo(context.Background(), &domain.VideoGenerationRequest{
		Model: "veo-3.1-lite", Prompt: "a river", Duration: 4, AspectRatio: "16:9",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if transport.calls != 1 {
		t.Fatalf("calls = %d", transport.calls)
	}
}

func TestGenerateVideo_SecondWriteConflicts(t *testing.T) {
	transport := &mediaTransport{
		entered:  make(chan struct{}),
		hold:     make(chan struct{}),
		response: `{"status":"COMPLETED","video_asset":{"url":"https://storage.googleapis.com/flow-rendered-videos/output_999.mp4"}}`,
	}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), newMediaRepo(t), transport, google.NewWireAdapter(nil), &mediaFlow{credits: 5000}, nil, nil)
	req := &domain.VideoGenerationRequest{Model: "veo-3.1-lite", Prompt: "a river", Duration: 4, AspectRatio: "16:9"}
	first := make(chan error, 1)
	go func() {
		_, err := svc.GenerateVideo(context.Background(), req)
		first <- err
	}()
	<-transport.entered
	_, err := svc.GenerateVideo(context.Background(), req)
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassConflict {
		t.Fatalf("second = %v", err)
	}
	close(transport.hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestGenerateVideo_DynamicProjectTitle(t *testing.T) {
	transport := &mediaTransport{response: `{"status":"COMPLETED","video_asset":{"url":"https://storage.googleapis.com/flow-rendered-videos/output_999.mp4"}}`}
	flow := &mediaFlow{credits: 5000}
	repo := session.NewMemorySessionRepository(nil)
	account := &domain.ManagedAccount{
		ID:         "user-alpha",
		IsHealthy:  true,
		FlowSNlM0e: "flow-at",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "osid"}),
	}
	account.SetFlowSessionToken("session-token")
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), repo, transport, google.NewWireAdapter(nil), flow, nil, domain.NewContractMetrics())
	svc.SetProjectTitlePrefix("CustomStudio")

	_, err := svc.GenerateVideo(context.Background(), &domain.VideoGenerationRequest{
		Model:       "veo-3.1-quality",
		Prompt:      "a flowing stream",
		Duration:    8,
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}

	if flow.lastTitle == "" {
		t.Fatal("expected CreateProject to be called")
	}
	if strings.Contains(flow.lastTitle, "dezuxk") {
		t.Fatalf("project title must NOT contain hardcoded dezuxk, got: %s", flow.lastTitle)
	}
	if !strings.HasPrefix(flow.lastTitle, "CustomStudio-user-alpha-") {
		t.Fatalf("expected title to start with CustomStudio-user-alpha-, got: %s", flow.lastTitle)
	}
}

func TestMediaService_FlowProjectManagement(t *testing.T) {
	repo := newMediaRepo(t)
	transport := &mediaTransport{}
	flow := &mediaFlow{}
	svc := services.NewMediaService(domain.NewModelRegistry(domain.GetFlowCatalog()), repo, transport, google.NewWireAdapter(nil), flow, nil, domain.NewContractMetrics())

	// 1. ListProjects
	projects, err := svc.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "proj-1" {
		t.Fatalf("unexpected projects: %+v", projects)
	}

	// 2. MoveProjectToTrash
	if err := svc.MoveProjectToTrash(context.Background(), "proj-1"); err != nil {
		t.Fatalf("MoveProjectToTrash failed: %v", err)
	}

	// 3. ListTrash
	trash, err := svc.ListTrash(context.Background())
	if err != nil {
		t.Fatalf("ListTrash failed: %v", err)
	}
	if len(trash) != 1 || trash[0].ID != "trash-1" {
		t.Fatalf("unexpected trash: %+v", trash)
	}

	// 4. RestoreProject
	if err := svc.RestoreProject(context.Background(), "trash-1"); err != nil {
		t.Fatalf("RestoreProject failed: %v", err)
	}

	// 5. DeleteProjectPermanently
	if err := svc.DeleteProjectPermanently(context.Background(), "trash-1"); err != nil {
		t.Fatalf("DeleteProjectPermanently failed: %v", err)
	}
}

