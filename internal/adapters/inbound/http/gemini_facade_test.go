package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"github.com/go-chi/chi/v5"
)

type fakeGeminiHistory struct {
	summaries []domain.ConversationSummary
	tree      *domain.ConversationTree
	err       error
}

func (f *fakeGeminiHistory) ListConversations(ctx context.Context, limit int) ([]domain.ConversationSummary, string, error) {
	return f.summaries, "", f.err
}
func (f *fakeGeminiHistory) GetConversationDetail(ctx context.Context, convID string) (*domain.ConversationTree, error) {
	return f.tree, f.err
}
func (f *fakeGeminiHistory) RenameConversation(ctx context.Context, convID, newTitle string) error {
	return f.err
}
func (f *fakeGeminiHistory) DeleteConversation(ctx context.Context, convID string) error {
	return f.err
}
func (f *fakeGeminiHistory) SwitchBranch(ctx context.Context, convID, respID, choiceID string) error {
	return f.err
}

type fakeGeminiUpload struct {
	token string
	err   error
}

func (f *fakeGeminiUpload) UploadFile(ctx context.Context, fileName, mimeType string, content io.Reader, size int64) (string, error) {
	return f.token, f.err
}
func (f *fakeGeminiUpload) UploadFileWithAccount(ctx context.Context, account *domain.ManagedAccount, fileName, mimeType string, content io.Reader, size int64) (string, error) {
	return f.token, f.err
}

type fakeGeminiCanvas struct {
	artifact *domain.CanvasArtifact
	shareURL string
	err      error
}

func (f *fakeGeminiCanvas) CreateCanvas(ctx context.Context, convID, title, contentType, initialContent string) (*domain.CanvasArtifact, error) {
	return f.artifact, f.err
}
func (f *fakeGeminiCanvas) UpdateDelta(ctx context.Context, canvasID string, baseVersion int, diffOps []domain.CanvasDiffOp) (*domain.CanvasArtifact, error) {
	return f.artifact, f.err
}
func (f *fakeGeminiCanvas) Publish(ctx context.Context, canvasID string, visibilityCode int, allowFork bool) (string, error) {
	return f.shareURL, f.err
}

type fakeGeminiQuota struct {
	info *domain.QuotaInfo
	tier *domain.AccountTierInfo
	err  error
}

func (f *fakeGeminiQuota) GetQuota(ctx context.Context) (*domain.QuotaInfo, error) {
	return f.info, f.err
}
func (f *fakeGeminiQuota) GetAccountTier(ctx context.Context) (*domain.AccountTierInfo, error) {
	return f.tier, f.err
}
func (f *fakeGeminiQuota) GetQuotaForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.QuotaInfo, error) {
	return f.info, f.err
}
func (f *fakeGeminiQuota) GetAccountTierForAccount(ctx context.Context, account *domain.ManagedAccount) (*domain.AccountTierInfo, error) {
	return f.tier, f.err
}

type fakeGeminiFeedback struct {
	err error
}

func (f *fakeGeminiFeedback) SendFeedback(ctx context.Context, req *domain.FeedbackRequest) error {
	return f.err
}

func TestGeminiFacadeRoutes(t *testing.T) {
	metrics := domain.NewContractMetrics()

	history := &fakeGeminiHistory{
		summaries: []domain.ConversationSummary{
			{ID: "c_1", Title: "Hội thoại 1"},
		},
		tree: &domain.ConversationTree{
			ConversationID: "c_1",
			Title:          "Hội thoại 1",
			Turns: []domain.HistoryTurn{
				{TurnID: "r_1", UserPrompt: "Hi"},
			},
		},
	}
	upload := &fakeGeminiUpload{token: "/contrib_service/ttl_1d/token123"}
	canvas := &fakeGeminiCanvas{
		artifact: &domain.CanvasArtifact{CanvasID: "canvas_abc", Version: 1},
		shareURL: "https://gemini.google.com/share/canvas/test",
	}
	quota := &fakeGeminiQuota{
		info: &domain.QuotaInfo{Quota5h: 10, QuotaWeekly: 20},
		tier: &domain.AccountTierInfo{TierCode: "GOOGLE_ONE_AI_PREMIUM"},
	}
	feedback := &fakeGeminiFeedback{}

	geminiHandler := NewGeminiHandler(history, upload, canvas, quota, feedback, metrics)
	r := chi.NewRouter()
	r.Route("/v1", func(v1 chi.Router) {
		v1.Get("/gemini/conversations", geminiHandler.HandleListConversations)
		v1.Get("/gemini/conversations/{id}", geminiHandler.HandleGetConversation)
		v1.Post("/gemini/conversations/{id}/rename", geminiHandler.HandleRenameConversation)
		v1.Delete("/gemini/conversations/{id}", geminiHandler.HandleDeleteConversation)
		v1.Post("/gemini/conversations/{id}/branch", geminiHandler.HandleSwitchBranch)
		v1.Post("/gemini/upload", geminiHandler.HandleUpload)
		v1.Get("/gemini/usage", geminiHandler.HandleGetUsage)
		v1.Get("/gemini/account-tier", geminiHandler.HandleGetAccountTier)
		v1.Post("/gemini/feedback", geminiHandler.HandleFeedback)
		v1.Post("/gemini/canvas", geminiHandler.HandleCreateCanvas)
		v1.Post("/gemini/canvas/{id}/delta", geminiHandler.HandleUpdateCanvasDelta)
		v1.Post("/gemini/canvas/{id}/publish", geminiHandler.HandlePublishCanvas)
	})
	handler := r

	// 1. GET /v1/gemini/conversations
	req := httptest.NewRequest(http.MethodGet, "/v1/gemini/conversations", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/gemini/conversations code=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Hội thoại 1") {
		t.Errorf("GET conversations missing title: %s", rr.Body.String())
	}

	// 2. GET /v1/gemini/conversations/c_1
	req = httptest.NewRequest(http.MethodGet, "/v1/gemini/conversations/c_1", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/gemini/conversations/c_1 code=%d", rr.Code)
	}

	// 3. POST /v1/gemini/conversations/c_1/rename
	renameBody := `{"title":"Tên mới"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/gemini/conversations/c_1/rename", strings.NewReader(renameBody))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST rename code=%d", rr.Code)
	}

	// 4. POST /v1/gemini/upload (Base64)
	uploadBody := `{"file_name":"test.png","mime_type":"image/png","data_base64":"iVBORw0KGgo="}`
	req = httptest.NewRequest(http.MethodPost, "/v1/gemini/upload", strings.NewReader(uploadBody))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST upload code=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "token123") {
		t.Errorf("upload token missing: %s", rr.Body.String())
	}

	// 5. GET /v1/gemini/usage
	req = httptest.NewRequest(http.MethodGet, "/v1/gemini/usage", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET usage code=%d", rr.Code)
	}

	// 6. POST /v1/gemini/feedback
	fbBody := `{"conversation_id":"c_1","response_id":"r_1","choice_id":"rc_1","rating":1}`
	req = httptest.NewRequest(http.MethodPost, "/v1/gemini/feedback", strings.NewReader(fbBody))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST feedback code=%d", rr.Code)
	}

	// 7. POST /v1/gemini/canvas
	canvasBody := `{"conversation_id":"c_1","title":"Canvas Test","content_type":"MARKDOWN","content":"# Test"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/gemini/canvas", bytes.NewReader([]byte(canvasBody)))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST canvas code=%d", rr.Code)
	}
	var cResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &cResp)
	if cResp["data"] == nil {
		t.Errorf("canvas data missing: %s", rr.Body.String())
	}
}
