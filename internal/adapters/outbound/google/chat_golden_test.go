package google_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

type mockTransport struct {
	doRequestFunc func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error)
}

func (m *mockTransport) DoRequest(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
	if m.doRequestFunc != nil {
		return m.doRequestFunc(ctx, account, service, method, path, body, contentType)
	}
	return nil, nil
}

func (m *mockTransport) BoundShort(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

func (m *mockTransport) BoundStream(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

func TestGeminiChatGoldenJob_VectorPass(t *testing.T) {
	mockChunk := `)]}'

128
[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_123456789abc\",\"r_987654321xyz\"],null,null,[[\"rc_111222\",[\"Xin chào! Tôi là trợ lý ảo Gemini.\"]]]]"]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockChunk))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	wire := google.NewWireAdapter(domain.DefaultRpcRegistry())

	alerts := 0
	job := google.NewGeminiChatGoldenJob(wire, transport, metrics, func(report google.ChatGoldenReport) {
		alerts++
	})

	labAcc := &domain.ManagedAccount{
		ID:           "lab_gemini_tester",
		GeminiSNlM0e: "fake-sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "fake-psid"}),
	}

	report, err := job.Run(context.Background(), labAcc, "Hello")
	if err != nil {
		t.Fatalf("chat golden job failed: %v", err)
	}

	if !report.Passed {
		t.Fatal("expected report to pass")
	}
	if !report.TextExtracted {
		t.Fatal("expected text to be extracted")
	}
	if report.ConversationID != "c_123456789abc" {
		t.Fatalf("expected conversation ID c_123456789abc, got %s", report.ConversationID)
	}
	if report.DriftDetected || alerts != 0 {
		t.Fatalf("unexpected drift or alerts: drift=%v alerts=%d", report.DriftDetected, alerts)
	}
}

func TestGeminiChatGoldenJob_DetectsFormatDrift(t *testing.T) {
	// Google trả về Conversation ID không theo định dạng c_...
	mockChunk := `)]}'

128
[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"invalid_id_format\",\"r_123\"],null,null,[[\"rc_111\",[\"Xin chào!\"]]]]"]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockChunk))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	wire := google.NewWireAdapter(domain.DefaultRpcRegistry())

	var alertReceived google.ChatGoldenReport
	alerts := 0
	job := google.NewGeminiChatGoldenJob(wire, transport, metrics, func(report google.ChatGoldenReport) {
		alerts++
		alertReceived = report
	})

	labAcc := &domain.ManagedAccount{
		ID:           "lab_gemini_tester",
		GeminiSNlM0e: "fake-sn",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "fake-psid"}),
	}

	report, err := job.Run(context.Background(), labAcc, "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.DriftDetected || alerts != 1 {
		t.Fatalf("expected drift detected: drift=%v alerts=%d", report.DriftDetected, alerts)
	}
	if !strings.Contains(alertReceived.AlertMessage, "không bắt đầu bằng 'c_'") {
		t.Fatalf("unexpected alert: %s", alertReceived.AlertMessage)
	}
}

func TestGeminiChatGoldenJob_RejectsProductionAccount(t *testing.T) {
	wire := google.NewWireAdapter(domain.DefaultRpcRegistry())
	job := google.NewGeminiChatGoldenJob(wire, nil, nil, nil)

	prodAcc := &domain.ManagedAccount{
		ID:           "prod_user_account",
		GeminiSNlM0e: "prod-sn",
	}

	_, err := job.Run(context.Background(), prodAcc, "Hello")
	if err == nil {
		t.Fatal("expected rejection of non-lab account")
	}
	if !strings.Contains(err.Error(), "không phải tài khoản lab") {
		t.Fatalf("expected isolation error: %v", err)
	}
}

func TestGeminiChatGoldenJob_LiveLabAccount(t *testing.T) {
	cookie := os.Getenv("GEMINI_LAB_COOKIE")
	if cookie == "" {
		cookie = os.Getenv("DEZUXK_LAB_COOKIE")
	}
	if cookie == "" {
		t.Skip("bỏ qua live chat lab test: không có biến môi trường GEMINI_LAB_COOKIE / DEZUXK_LAB_COOKIE")
	}

	cookieMap := make(map[string]string)
	for _, pair := range strings.Split(cookie, ";") {
		pair = strings.TrimSpace(pair)
		if idx := strings.Index(pair, "="); idx != -1 {
			k := strings.TrimSpace(pair[:idx])
			v := strings.TrimSpace(pair[idx+1:])
			if k != "" {
				cookieMap[k] = v
			}
		}
	}

	labAcc := &domain.ManagedAccount{
		ID:  "lab_gemini_tester",
		Jar: domain.NewCookieJar(cookieMap),
	}

	extractor := google.NewTokenExtractorAdapter("", "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	sn, _, err := extractor.ExtractTokens(ctx, labAcc, domain.ServiceGemini)
	if err != nil {
		t.Fatalf("[CLASSIFICATION: CREDENTIAL_EXPIRED] Không thể trích xuất SNlM0e từ cookie lab: %v", err)
	}
	labAcc.GeminiSNlM0e = sn

	transport := google.NewGoogleTransportAdapter(nil)
	wire := google.NewWireAdapter(domain.DefaultRpcRegistry())
	metrics := domain.NewContractMetrics()

	job := google.NewGeminiChatGoldenJob(wire, transport, metrics, nil)
	report, err := job.Run(ctx, labAcc, "Hello from Gemini Live Smoke Test")
	if err != nil {
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") || strings.Contains(strings.ToLower(err.Error()), "auth") {
			t.Fatalf("[CLASSIFICATION: CREDENTIAL_EXPIRED] Live test bị từ chối xác thực tài khoản lab: %v", err)
		}
		t.Fatalf("[CLASSIFICATION: UPSTREAM_CONTRACT_DRIFT] Lỗi giao thức kết nối upstream: %v", err)
	}

	if report.DriftDetected {
		t.Fatalf("[CLASSIFICATION: UPSTREAM_CONTRACT_DRIFT] Phát hiện schema/protocol drift: %s", report.AlertMessage)
	}
	if !report.Passed || !report.TextExtracted {
		t.Fatalf("[CLASSIFICATION: UPSTREAM_CONTRACT_DRIFT] Live test không trích xuất được phản hồi hợp lệ")
	}
}
