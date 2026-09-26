package services_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type mockHistoryTransport struct {
	status   int
	response string
	lastPath string
	lastBody string
}

func (m *mockHistoryTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (m *mockHistoryTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (m *mockHistoryTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	m.lastPath = path
	if body != nil {
		raw, _ := io.ReadAll(body)
		m.lastBody = string(raw)
	}
	status := m.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(m.response)),
		Header:     make(http.Header),
	}, nil
}

func geminiAccount() *domain.ManagedAccount {
	account := &domain.ManagedAccount{
		ID:           "test_gemini",
		IsHealthy:    true,
		GeminiSNlM0e: "test-gemini-at",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "test_psid"}),
	}
	_ = account.MoveService(domain.ServiceGemini, domain.StateReady)
	return account
}

func newGeminiRepo(t *testing.T) *session.MemorySessionRepository {
	t.Helper()
	repo := session.NewMemorySessionRepository(nil)
	if err := repo.Save(context.Background(), geminiAccount()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestGeminiHistoryService_ListConversations(t *testing.T) {
	// Giả lập phản hồi batchexecute MaZiqc
	fixture := `)]}'
[["wrb.fr","MaZiqc","[[[\"c_123\",\"Cuộc trò chuyện thử nghiệm\",null,null,1700000000000]]]",null,null,null,"generic"]]`
	transport := &mockHistoryTransport{response: fixture}
	repo := newGeminiRepo(t)
	svc := services.NewGeminiHistoryService(repo, transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())

	convs, token, err := svc.ListConversations(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListConversations lỗi: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("kỳ vọng 1 cuộc hội thoại, nhận được %d", len(convs))
	}
	if convs[0].ID != "c_123" || convs[0].Title != "Cuộc trò chuyện thử nghiệm" {
		t.Errorf("dữ liệu hội thoại không khớp: %+v", convs[0])
	}
	if token != "" {
		t.Errorf("kỳ vọng next token rỗng, nhận %s", token)
	}
	if !strings.Contains(transport.lastPath, "MaZiqc") {
		t.Errorf("path không chứa MaZiqc: %s", transport.lastPath)
	}
}

func TestGeminiHistoryService_GetConversationDetail(t *testing.T) {
	// Giả lập phản hồi batchexecute cZOhpc
	fixture := `)]}'
[["wrb.fr","cZOhpc","[[\"r_turn_1\",[\"Xin chào\"],[[\"rc_choice_1\",[\"Chào bạn! Tôi có thể giúp gì?\"]]]]]",null,null,null,"generic"]]`
	transport := &mockHistoryTransport{response: fixture}
	repo := newGeminiRepo(t)
	svc := services.NewGeminiHistoryService(repo, transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())

	tree, err := svc.GetConversationDetail(context.Background(), "c_123")
	if err != nil {
		t.Fatalf("GetConversationDetail lỗi: %v", err)
	}
	if tree.ConversationID != "c_123" {
		t.Errorf("conversation_id không khớp: %s", tree.ConversationID)
	}
	if len(tree.Turns) != 1 {
		t.Fatalf("kỳ vọng 1 turn, nhận %d", len(tree.Turns))
	}
	if tree.Turns[0].UserPrompt != "Xin chào" {
		t.Errorf("user prompt không khớp: %s", tree.Turns[0].UserPrompt)
	}
	if len(tree.Turns[0].Choices) != 1 || tree.Turns[0].Choices[0].Content != "Chào bạn! Tôi có thể giúp gì?" {
		t.Errorf("choices không khớp: %+v", tree.Turns[0].Choices)
	}
}

func TestGeminiHistoryService_RenameConversation(t *testing.T) {
	fixture := `)]}'
[["wrb.fr","PCck7e","[1]",null,null,null,"generic"]]`
	transport := &mockHistoryTransport{response: fixture}
	repo := newGeminiRepo(t)
	svc := services.NewGeminiHistoryService(repo, transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())

	err := svc.RenameConversation(context.Background(), "c_123", "Tiêu đề mới")
	if err != nil {
		t.Fatalf("RenameConversation lỗi: %v", err)
	}
	if !strings.Contains(transport.lastPath, "PCck7e") {
		t.Errorf("path không chứa PCck7e: %s", transport.lastPath)
	}
}

func TestGeminiHistoryService_DeleteConversation(t *testing.T) {
	fixture := `)]}'
[["wrb.fr","VxUbXb","[1]",null,null,null,"generic"]]`
	transport := &mockHistoryTransport{response: fixture}
	repo := newGeminiRepo(t)
	svc := services.NewGeminiHistoryService(repo, transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())

	err := svc.DeleteConversation(context.Background(), "c_123")
	if err != nil {
		t.Fatalf("DeleteConversation lỗi: %v", err)
	}
	if !strings.Contains(transport.lastPath, "VxUbXb") {
		t.Errorf("path không chứa VxUbXb: %s", transport.lastPath)
	}
}

func TestGeminiHistoryService_SwitchBranch(t *testing.T) {
	fixture := `)]}'
[["wrb.fr","wEb32b","[1]",null,null,null,"generic"]]`
	transport := &mockHistoryTransport{response: fixture}
	repo := newGeminiRepo(t)
	svc := services.NewGeminiHistoryService(repo, transport, domain.DefaultRpcRegistry(), domain.NewContractMetrics())

	err := svc.SwitchBranch(context.Background(), "c_123", "r_123", "rc_456")
	if err != nil {
		t.Fatalf("SwitchBranch lỗi: %v", err)
	}
	if !strings.Contains(transport.lastPath, "wEb32b") {
		t.Errorf("path không chứa wEb32b: %s", transport.lastPath)
	}
}
