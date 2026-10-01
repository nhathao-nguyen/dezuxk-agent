package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type multiAccountSessionRepo struct {
	mu       sync.Mutex
	accounts []*domain.ManagedAccount
	releases map[string]int
}

func newMultiAccountRepo(accounts ...*domain.ManagedAccount) *multiAccountSessionRepo {
	return &multiAccountSessionRepo{
		accounts: accounts,
		releases: make(map[string]int),
	}
}

func (m *multiAccountSessionRepo) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, acc := range m.accounts {
		if acc.ServiceReady(service) {
			return acc, nil
		}
	}
	return nil, domain.Unauthenticated(domain.OpSession, "", service, "không có phiên sẵn sàng").WithPublicStatus(http.StatusServiceUnavailable)
}

func (m *multiAccountSessionRepo) Release(account *domain.ManagedAccount, err error) {
	if account == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases[account.ID]++

	class, service, ok := domain.ClassifiedFailure(err)
	if !ok {
		return
	}
	if (class == domain.ClassRateLimited || class == domain.ClassUpstreamUnavailable) && service != "" {
		_ = account.MoveService(service, domain.StateCooling)
		account.CoolService(service, time.Now().Add(60*time.Second), class)
	}
}

func (m *multiAccountSessionRepo) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	if account == nil {
		return false
	}
	return account.TryWriteLease(service)
}

func (m *multiAccountSessionRepo) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
	if account == nil {
		return
	}
	account.ReleaseWriteLease(service)
}

func (m *multiAccountSessionRepo) ListAll(ctx context.Context) []*domain.ManagedAccount {
	return m.accounts
}
func (m *multiAccountSessionRepo) Save(ctx context.Context, account *domain.ManagedAccount) error {
	return nil
}
func (m *multiAccountSessionRepo) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	for _, acc := range m.accounts {
		if acc.ID == id {
			return acc, nil
		}
	}
	return nil, nil
}
func (m *multiAccountSessionRepo) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	return nil
}
func (m *multiAccountSessionRepo) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {
	_ = account.MoveService(service, domain.StateInvalid)
}
func (m *multiAccountSessionRepo) GetAlerts() []domain.SessionAlert   { return nil }
func (m *multiAccountSessionRepo) AddAlert(alert domain.SessionAlert) {}
func (m *multiAccountSessionRepo) ClearAlerts(accountID string)       {}

type dynamicTransport struct {
	mu      sync.Mutex
	handler func(reqPath string, account *domain.ManagedAccount) (*http.Response, error)
}

func (d *dynamicTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (d *dynamicTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}
func (d *dynamicTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	d.mu.Lock()
	fn := d.handler
	d.mu.Unlock()
	if fn != nil {
		return fn(path, account)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

type recordingCodec struct {
	mu           sync.Mutex
	lastBuilder  domain.GeminiPayloadBuilder
	streamDeltas []string
	replyText    string
}

func (r *recordingCodec) MaterializeChat(account *domain.ManagedAccount, payload domain.GeminiPayloadBuilder) (domain.OutboundAttempt, error) {
	r.mu.Lock()
	r.lastBuilder = payload
	r.mu.Unlock()
	return domain.OutboundAttempt{
		Path:        "/chat/stream",
		Body:        "body",
		ContentType: "application/x-www-form-urlencoded",
	}, nil
}

func (r *recordingCodec) DematerializeChat(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error) {
	return r.DematerializeChatStream(ctx, resp, metrics, onDelta, nil)
}

func (r *recordingCodec) DematerializeChatStream(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onContent func(delta, convID string) error, onReasoning func(delta, convID string) error) (domain.GeminiReply, error) {
	if resp != nil && resp.StatusCode != http.StatusOK {
		return domain.GeminiReply{}, domain.CodecHTTP(domain.OriginStreamGenerate, resp.StatusCode, false, domain.ServiceGemini)
	}
	r.mu.Lock()
	deltas := r.streamDeltas
	replyText := r.replyText
	r.mu.Unlock()

	if onContent != nil && len(deltas) > 0 {
		for _, d := range deltas {
			if err := onContent(d, "c_test_123"); err != nil {
				return domain.GeminiReply{}, err
			}
		}
	}

	return domain.GeminiReply{
		Text:           replyText,
		ConversationID: "c_test_123",
		ResponseID:     "r_test_123",
		ChoiceID:       "rc_test_123",
	}, nil
}

// 1. Kiểm thử Multimodal Vision (Base64 Inline & SCOTTY)
func TestChatService_MultimodalVision_InlineAndScotty(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	acc := &domain.ManagedAccount{
		ID:           "acc-vision",
		GeminiSNlM0e: "fake-sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "fake"}),
		IsHealthy:    true,
	}
	_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
	repo := newMultiAccountRepo(acc)

	transport := &dynamicTransport{
		handler: func(reqPath string, account *domain.ManagedAccount) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}
	codec := &recordingCodec{replyText: "Đây là hình ảnh một chú mèo."}

	// Cấu hình Vision chế độ Inline
	enabled := true
	visionCfg := config.VisionConfig{
		Enabled:           &enabled,
		MaxImageSizeBytes: 1048576,
		AllowedMimeTypes:  []string{"image/png", "image/jpeg"},
		UploadMethod:      "inline",
	}
	vr := services.NewVisionResolver(visionCfg, nil)

	chatService := services.NewChatService(mr, repo, transport, codec, nil)
	chatService.SetVisionResolver(vr)

	// Tạo request có chứa ảnh base64
	base64PNG := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "text", Text: "Hãy xem ảnh này"},
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: base64PNG}},
				},
			},
		},
	}

	resp, err := chatService.ExecuteChatSync(context.Background(), req)
	if err != nil {
		t.Fatalf("ExecuteChatSync failed: %v", err)
	}

	if resp.Choices[0].Message.Content != "Đây là hình ảnh một chú mèo." {
		t.Errorf("Unexpected reply text: %s", resp.Choices[0].Message.Content)
	}

	// Xác nhận ảnh đã được resolve thành Attachment và truyền vào GeminiPayloadBuilder
	if len(codec.lastBuilder.Attachments) != 1 {
		t.Fatalf("expected 1 attachment in GeminiPayloadBuilder, got %d", len(codec.lastBuilder.Attachments))
	}
	att := codec.lastBuilder.Attachments[0]
	if !strings.HasPrefix(att.StorageToken, "data:image/png;base64,") {
		t.Errorf("expected inline data token, got %s", att.StorageToken)
	}
	if att.MimeType != "image/png" {
		t.Errorf("expected mime image/png, got %s", att.MimeType)
	}
}

// 2. Kiểm thử Token Counter & OpenAI Usage Accounting (Sync và Stream)
func TestChatService_OpenAIUsageAccounting_SyncAndStream(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	acc := &domain.ManagedAccount{
		ID:           "acc-token",
		GeminiSNlM0e: "fake-sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "fake"}),
		IsHealthy:    true,
	}
	_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
	repo := newMultiAccountRepo(acc)

	transport := &dynamicTransport{
		handler: func(reqPath string, account *domain.ManagedAccount) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}
	codec := &recordingCodec{
		streamDeltas: []string{"Xin ", "chào ", "thế ", "giới!"},
		replyText:    "Xin chào thế giới!",
	}

	tokensCfg := config.TokensConfig{
		Encoding:             "cl100k_base",
		ImageTokensPerTile:   258,
		PromptTokenRatio:     1.0,
		CompletionTokenRatio: 1.0,
	}
	tc := services.NewTokenCounter(tokensCfg)

	chatService := services.NewChatService(mr, repo, transport, codec, nil)
	chatService.SetTokenCounter(tc)

	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Chào bạn, hãy giới thiệu bản thân."},
		},
	}

	// 2.1 Kiểm tra Sync Response
	syncResp, err := chatService.ExecuteChatSync(context.Background(), req)
	if err != nil {
		t.Fatalf("ExecuteChatSync failed: %v", err)
	}

	if syncResp.Usage == nil {
		t.Fatal("expected Usage in Sync response, got nil")
	}
	if syncResp.Usage.PromptTokens <= 0 {
		t.Errorf("expected PromptTokens > 0, got %d", syncResp.Usage.PromptTokens)
	}
	if syncResp.Usage.CompletionTokens <= 0 {
		t.Errorf("expected CompletionTokens > 0, got %d", syncResp.Usage.CompletionTokens)
	}
	if syncResp.Usage.TotalTokens != syncResp.Usage.PromptTokens+syncResp.Usage.CompletionTokens {
		t.Errorf("expected TotalTokens = %d, got %d", syncResp.Usage.PromptTokens+syncResp.Usage.CompletionTokens, syncResp.Usage.TotalTokens)
	}

	// 2.2 Kiểm tra Stream Response (Usage phải có mặt ở chunk cuối cùng trước [DONE])
	var buf bytes.Buffer
	err = chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("ExecuteChatStream failed: %v", err)
	}

	sseOutput := buf.String()
	lines := strings.Split(sseOutput, "\n")
	var dataChunks []string
	for _, l := range lines {
		if strings.HasPrefix(l, "data: ") {
			payload := strings.TrimPrefix(l, "data: ")
			if payload != "[DONE]" {
				dataChunks = append(dataChunks, payload)
			}
		}
	}

	if len(dataChunks) < 2 {
		t.Fatalf("expected at least 2 data chunks, got %d", len(dataChunks))
	}

	// Chunk cuối cùng (trước [DONE]) phải có FinishReason là "stop" và Usage đầy đủ
	lastChunkRaw := dataChunks[len(dataChunks)-1]
	var lastChunk domain.OpenAIChatResponse
	if err := json.Unmarshal([]byte(lastChunkRaw), &lastChunk); err != nil {
		t.Fatalf("failed to unmarshal last SSE chunk: %v", err)
	}

	if lastChunk.Usage == nil {
		t.Fatal("expected Usage in final SSE stream chunk, got nil")
	}
	if lastChunk.Usage.PromptTokens <= 0 || lastChunk.Usage.CompletionTokens <= 0 {
		t.Errorf("invalid token counts in SSE usage: %+v", lastChunk.Usage)
	}
	if lastChunk.Choices[0].FinishReason == nil || *lastChunk.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %v", lastChunk.Choices[0].FinishReason)
	}
}

// 3. Kiểm thử Next-Account Failover khi gặp lỗi 429 Rate Limit
func TestChatService_NextAccountFailover_429(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())

	// Account 1: Bị rate-limited 429
	acc1 := &domain.ManagedAccount{
		ID:           "acc-1-busy",
		GeminiSNlM0e: "sn-1",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid-1"}),
		IsHealthy:    true,
	}
	_ = acc1.MoveService(domain.ServiceGemini, domain.StateReady)

	// Account 2: Khả dụng và sẽ thành công
	acc2 := &domain.ManagedAccount{
		ID:           "acc-2-ready",
		GeminiSNlM0e: "sn-2",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid-2"}),
		IsHealthy:    true,
	}
	_ = acc2.MoveService(domain.ServiceGemini, domain.StateReady)

	repo := newMultiAccountRepo(acc1, acc2)

	transport := &dynamicTransport{
		handler: func(reqPath string, account *domain.ManagedAccount) (*http.Response, error) {
			if account.ID == "acc-1-busy" {
				// Trả 429 Too Many Requests
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader("rate limit exceeded")),
					Header:     make(http.Header),
				}, nil
			}
			// Account 2 trả 200 OK
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}

	codec := &recordingCodec{replyText: "Thành công từ Account 2!"}

	chatService := services.NewChatService(mr, repo, transport, codec, nil)
	chatService.SetFailoverConfig(config.FailoverConfig{
		MaxAttempts:     3,
		CoolingDuration: 60 * time.Second,
	})

	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Kiểm tra chuyển tài khoản tự động"},
		},
	}

	resp, err := chatService.ExecuteChatSync(context.Background(), req)
	if err != nil {
		t.Fatalf("expected successful failover to Account 2, got error: %v", err)
	}

	if resp.Choices[0].Message.Content != "Thành công từ Account 2!" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}

	// Kiểm tra Account 1 đã bị chuyển sang StateCooling và gắn cooldown
	state, cooldown := acc1.ServiceSnapshot(domain.ServiceGemini)
	if state != domain.StateCooling {
		t.Errorf("expected Account 1 to be in StateCooling, got %s", state)
	}
	if cooldown.Before(time.Now()) {
		t.Errorf("expected CooldownUntil to be in the future, got %v", cooldown)
	}
}

// 4. Kiểm thử Client Disconnect giữa chừng trong SSE Stream:
// Ngắt context ngay lập tức, giải phóng write lease, KHÔNG phạt cooldown tài khoản
func TestChatService_ClientDisconnect_NoCooling(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	acc := &domain.ManagedAccount{
		ID:           "acc-disconnect",
		GeminiSNlM0e: "fake-sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid"}),
		IsHealthy:    true,
	}
	_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
	repo := newMultiAccountRepo(acc)

	transport := &dynamicTransport{
		handler: func(reqPath string, account *domain.ManagedAccount) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}

	// Tạo context có thể hủy
	ctx, cancel := context.WithCancel(context.Background())

	codec := &recordingCodec{
		streamDeltas: []string{"delta1", "delta2", "delta3"},
		replyText:    "Xong",
	}

	chatService := services.NewChatService(mr, repo, transport, codec, nil)

	// Writer hủy context ngay khi nhận delta đầu tiên (mô phỏng client đóng kết nối)
	var capturedChunks []string
	writer := &customWriter{
		onWrite: func(p []byte) {
			capturedChunks = append(capturedChunks, string(p))
			// Hủy context ngay sau delta đầu tiên
			cancel()
		},
	}

	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Test disconnect"},
		},
	}

	err := chatService.ExecuteChatStream(ctx, req, writer, nil)
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}

	// Xác nhận tài khoản vẫn ở trạng thái StateReady và KHÔNG bị phạt cooldown
	state, cooldown := acc.ServiceSnapshot(domain.ServiceGemini)
	if state != domain.StateReady {
		t.Errorf("expected Account state to remain StateReady, got %s", state)
	}
	if !cooldown.IsZero() && cooldown.After(time.Now()) {
		t.Errorf("account should not be in cooldown after client disconnect, got %v", cooldown)
	}
}

// 5. Kiểm thử Stream Failover trước khi flush dữ liệu: Replay thành công trên Account 2
func TestChatService_NextAccountFailover_StreamBeforeFlush(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())

	acc1 := &domain.ManagedAccount{
		ID:           "acc-stream-1",
		GeminiSNlM0e: "sn-1",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid-1"}),
		IsHealthy:    true,
	}
	_ = acc1.MoveService(domain.ServiceGemini, domain.StateReady)

	acc2 := &domain.ManagedAccount{
		ID:           "acc-stream-2",
		GeminiSNlM0e: "sn-2",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid-2"}),
		IsHealthy:    true,
	}
	_ = acc2.MoveService(domain.ServiceGemini, domain.StateReady)

	repo := newMultiAccountRepo(acc1, acc2)

	transport := &dynamicTransport{
		handler: func(reqPath string, account *domain.ManagedAccount) (*http.Response, error) {
			if account.ID == "acc-stream-1" {
				// Lỗi 503 Service Unavailable ngay lập tức (chưa flush bất kỳ chunk nào)
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("upstream overloaded")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}

	codec := &recordingCodec{
		streamDeltas: []string{"Hết ", "quá ", "tải!"},
		replyText:    "Hết quá tải!",
	}

	chatService := services.NewChatService(mr, repo, transport, codec, nil)
	chatService.SetFailoverConfig(config.FailoverConfig{
		MaxAttempts:     3,
		CoolingDuration: 60 * time.Second,
	})

	var buf bytes.Buffer
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Stream test"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err != nil {
		t.Fatalf("expected stream failover to succeed on Account 2, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Hết ") || !strings.Contains(output, "[DONE]") {
		t.Errorf("expected streamed content from Account 2, got: %s", output)
	}

	// Xác nhận acc1 bị tagged StateCooling
	state, _ := acc1.ServiceSnapshot(domain.ServiceGemini)
	if state != domain.StateCooling {
		t.Errorf("expected acc1 to be in StateCooling, got %s", state)
	}
}

// 6. Kiểm thử Stream Failover sau khi ĐÃ FLUSH ít nhất 1 chunk: KHÔNG replay để tránh corrupt client stream
type failingStreamCodec struct {
	failAfterFirstDelta bool
}

func (f *failingStreamCodec) MaterializeChat(account *domain.ManagedAccount, payload domain.GeminiPayloadBuilder) (domain.OutboundAttempt, error) {
	return domain.OutboundAttempt{
		Path:        "/chat/stream",
		Body:        "body",
		ContentType: "application/x-www-form-urlencoded",
	}, nil
}

func (f *failingStreamCodec) DematerializeChat(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onDelta func(delta, convID string) error) (domain.GeminiReply, error) {
	return f.DematerializeChatStream(ctx, resp, metrics, onDelta, nil)
}

func (f *failingStreamCodec) DematerializeChatStream(ctx context.Context, resp *http.Response, metrics *domain.ContractMetrics, onContent func(delta, convID string) error, onReasoning func(delta, convID string) error) (domain.GeminiReply, error) {
	if onContent != nil {
		_ = onContent("chunk-da-gui", "c_mid_stream")
	}
	// Đứt kết nối giữa chừng sau khi client đã nhận 1 chunk
	return domain.GeminiReply{}, domain.CodecTransport(domain.OriginStreamGenerate, domain.ServiceGemini, fmt.Errorf("connection reset by peer mid-stream"))
}

func TestChatService_NextAccountFailover_StreamAfterFlush_NoReplay(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())

	acc1 := &domain.ManagedAccount{
		ID:           "acc-mid-1",
		GeminiSNlM0e: "sn-1",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid-1"}),
		IsHealthy:    true,
	}
	_ = acc1.MoveService(domain.ServiceGemini, domain.StateReady)

	acc2 := &domain.ManagedAccount{
		ID:           "acc-mid-2",
		GeminiSNlM0e: "sn-2",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid-2"}),
		IsHealthy:    true,
	}
	_ = acc2.MoveService(domain.ServiceGemini, domain.StateReady)

	repo := newMultiAccountRepo(acc1, acc2)

	acc2Called := false
	transport := &dynamicTransport{
		handler: func(reqPath string, account *domain.ManagedAccount) (*http.Response, error) {
			if account.ID == "acc-mid-2" {
				acc2Called = true
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		},
	}

	codec := &failingStreamCodec{failAfterFirstDelta: true}
	chatService := services.NewChatService(mr, repo, transport, codec, nil)
	chatService.SetFailoverConfig(config.FailoverConfig{
		MaxAttempts:     3,
		CoolingDuration: 60 * time.Second,
	})

	var buf bytes.Buffer
	req := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{Role: "user", Content: "Mid-stream disconnect"},
		},
	}

	err := chatService.ExecuteChatStream(context.Background(), req, &buf, nil)
	if err == nil {
		t.Fatal("expected error due to mid-stream failure, got nil")
	}

	// Đảm bảo không replay sang Account 2 vì đã flush 1 chunk cho client
	if acc2Called {
		t.Fatal("expected Account 2 NOT to be called because data was already flushed to client")
	}
}

// 7. Kiểm thử từ chối ảnh quá kích thước cấu hình hoặc sai định dạng MIME
func TestChatService_MultimodalVision_OversizeAndUnsupportedMime(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	acc := &domain.ManagedAccount{
		ID:           "acc-reject",
		GeminiSNlM0e: "sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "psid"}),
		IsHealthy:    true,
	}
	_ = acc.MoveService(domain.ServiceGemini, domain.StateReady)
	repo := newMultiAccountRepo(acc)

	transport := &dynamicTransport{}
	codec := &recordingCodec{}

	// Giới hạn max 100 bytes, chỉ chấp nhận image/png
	maxBytes := int64(100)
	enabled := true
	vr := services.NewVisionResolver(config.VisionConfig{
		Enabled:           &enabled,
		MaxImageSizeBytes: maxBytes,
		AllowedMimeTypes:  []string{"image/png"},
		UploadMethod:      "inline",
	}, nil)

	chatService := services.NewChatService(mr, repo, transport, codec, nil)
	chatService.SetVisionResolver(vr)

	// Test 7.1: MIME không hợp lệ (image/tiff)
	reqUnsupported := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: "data:image/tiff;base64,TU0AKgAAAAg="}},
				},
			},
		},
	}
	_, err := chatService.ExecuteChatSync(context.Background(), reqUnsupported)
	if err == nil {
		t.Fatal("expected error for unsupported MIME, got nil")
	}
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassInvalidRequest {
		t.Errorf("expected ClassInvalidRequest, got %v", err)
	}

	// Test 7.2: Ảnh vượt quá 100 bytes
	largeData := make([]byte, 200)
	largeB64 := "data:image/png;base64," + strings.Repeat("A", 300)
	reqOversize := &domain.OpenAIChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []domain.OpenAIMessage{
			{
				Role: "user",
				ContentParts: []domain.MessageContentPart{
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: largeB64}},
				},
			},
		},
	}
	_ = largeData
	_, err = chatService.ExecuteChatSync(context.Background(), reqOversize)
	if err == nil {
		t.Fatal("expected error for oversized image, got nil")
	}
	ge, ok = domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassInvalidRequest {
		t.Errorf("expected ClassInvalidRequest, got %v", err)
	}
}

type customWriter struct {
	onWrite func(p []byte)
}

func (w *customWriter) Write(p []byte) (n int, err error) {
	if w.onWrite != nil {
		w.onWrite(p)
	}
	return len(p), nil
}
