package google_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

type mockTransport struct {
	doRequestFunc func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error)
}

func (m *mockTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	return m.doRequestFunc(ctx, account, service, method, path, body, contentType)
}
func (m *mockTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (m *mockTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

func TestFlowClient_GetCreditsBalance(t *testing.T) {
	mockResp := `)]}'

35
[[["wrb.fr","nzlxg","[1050, 1, 2, 2, null, 1050]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			if service != domain.ServiceFlow {
				t.Errorf("expected service Flow, got %v", service)
			}
			if !strings.Contains(path, "nzlxg") {
				t.Errorf("expected path to contain nzlxg, got %s", path)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(mockResp)),
			}, nil
		},
	}

	rpcReg := domain.DefaultRpcRegistry()
	metrics := domain.NewContractMetrics()
	client := google.NewFlowClientAdapter(transport, rpcReg, metrics)

	acc := &domain.ManagedAccount{
		ID:         "test",
		FlowSNlM0e: "fake-sn-token",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "fake-osid"}),
	}

	balance, err := client.GetCreditsBalance(context.Background(), acc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if balance.Amount != 1050 {
		t.Errorf("expected 1050 credits, got %d", balance.Amount)
	}
	if balance.UnmappedFields != 5 {
		t.Errorf("expected 5 unmapped fields, got %d", balance.UnmappedFields)
	}
	if balance.SpecVersion != domain.FlowCreditSpecVersion {
		t.Errorf("expected spec %s, got %s", domain.FlowCreditSpecVersion, balance.SpecVersion)
	}
	if acc.Tier != 0 {
		t.Errorf("tier must stay unset, got %d", acc.Tier)
	}
	if acc.CreditsBalance != 1050 {
		t.Errorf("expected stored balance 1050, got %d", acc.CreditsBalance)
	}
	snap := metrics.Snapshot()
	if snap.UnmappedFields != 5 || snap.SchemaUnexpected != 0 {
		t.Errorf("metrics = %+v", snap)
	}
	if snap.Operations[domain.OpFlowGetCredits].UnmappedFields != 5 {
		t.Fatalf("operation metrics = %+v", snap.Operations)
	}
}

func TestGetCreditsBalance_RetriesUnavailableOnce(t *testing.T) {
	calls := 0
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			calls++
			if calls == 1 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("down"))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(")]}'\n\n12\n[[[\"wrb.fr\",\"nzlxg\",\"[1050]\"]]]\n"))}, nil
		},
	}
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())
	balance, err := client.GetCreditsBalance(context.Background(), &domain.ManagedAccount{ID: "lab", FlowSNlM0e: "at"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || balance.Amount != 1050 {
		t.Fatalf("calls=%d balance=%+v", calls, balance)
	}
}

func TestCreateProject_DoesNotRetryUnavailable(t *testing.T) {
	calls := 0
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("down"))}, nil
		},
	}
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), nil)
	_, err := client.CreateProject(context.Background(), &domain.ManagedAccount{ID: "lab", FlowSNlM0e: "at"}, "dezuxk")
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestFlowClient_CreateProjectUsesVector(t *testing.T) {
	var captured string
	mockResp := `)]}'

40
[[["wrb.fr","jHPbke","[\"1cfeebb6-4d61-4373-aa21-dd52fd93679f\",[\"dezuxk\"]]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			raw, _ := io.ReadAll(body)
			captured = string(raw)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockResp))}, nil
		},
	}
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), nil)
	id, err := client.CreateProject(context.Background(), &domain.ManagedAccount{FlowSNlM0e: "at-token"}, "dezuxk")
	if err != nil {
		t.Fatal(err)
	}
	if id != "1cfeebb6-4d61-4373-aa21-dd52fd93679f" {
		t.Fatalf("id = %s", id)
	}
	decoded, _ := url.QueryUnescape(captured)
	if !strings.Contains(decoded, `projects/*`) || !strings.Contains(decoded, "dezuxk") {
		t.Fatalf("body = %s", decoded)
	}
}

func TestFlowClient_RegisterSessionLock(t *testing.T) {
	var capturedBody string
	mockResp := `)]}'

25
[[["wrb.fr","csbIsb","[true]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			bytes, _ := io.ReadAll(body)
			capturedBody = string(bytes)
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(mockResp)),
			}, nil
		},
	}

	rpcReg := domain.DefaultRpcRegistry()
	client := google.NewFlowClientAdapter(transport, rpcReg, nil)

	acc := &domain.ManagedAccount{
		ID:         "test",
		FlowSNlM0e: "fake-sn-token",
	}

	projectUUID := "12345678-abcd-ef01-2345-6789abcdef01"
	err := client.RegisterSessionLock(context.Background(), acc, projectUUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// URL decode để kiểm tra nội dung bên trong form URL-encoded
	decodedBody, _ := url.QueryUnescape(capturedBody)
	expectedSnippet := fmt.Sprintf("projects/%s", projectUUID)
	if !strings.Contains(decodedBody, expectedSnippet) {
		t.Errorf("expected decodedBody to contain %q, got %s", expectedSnippet, decodedBody)
	}
}

func TestFlowClient_ListVoicePersonas(t *testing.T) {
	var capturedBody string
	mockResp := `)]}'

45
[[["wrb.fr","Zzl0ze","[[\"Aoede\", \"Charon\", \"Fenrir\"]]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			bytes, _ := io.ReadAll(body)
			capturedBody = string(bytes)
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(mockResp)),
			}, nil
		},
	}

	rpcReg := domain.DefaultRpcRegistry()
	client := google.NewFlowClientAdapter(transport, rpcReg, domain.NewContractMetrics())

	acc := &domain.ManagedAccount{
		ID:         "test",
		FlowSNlM0e: "fake-sn-token",
	}

	projectUUID := "80721e35-f1bc-4239-ba89-c524342f4fde"
	personas, err := client.ListVoicePersonas(context.Background(), acc, projectUUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(personas) != 3 {
		t.Fatalf("expected 3 personas, got %d", len(personas))
	}
	if personas[0].Name != "Aoede" || personas[1].Name != "Charon" || personas[2].Name != "Fenrir" {
		t.Errorf("unexpected personas names: %+v", personas)
	}

	// Verify projectUUID was properly serialized into Batchexecute payload
	decodedBody, _ := url.QueryUnescape(capturedBody)
	expectedProjectSnippet := fmt.Sprintf("projects/%s", projectUUID)
	if !strings.Contains(decodedBody, expectedProjectSnippet) {
		t.Errorf("expected payload to contain %q, got %s", expectedProjectSnippet, decodedBody)
	}
}

