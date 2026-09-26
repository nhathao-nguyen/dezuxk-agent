package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// ChatGoldenReport chứa kết quả đối soát hợp đồng định kỳ cho StreamGenerate.
// Tuyệt đối không chứa cookie, CSRF token hay thân HTTP thô.
type ChatGoldenReport struct {
	Timestamp      time.Time `json:"timestamp"`
	AccountID      string    `json:"account_id"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Passed         bool      `json:"passed"`
	TextExtracted  bool      `json:"text_extracted"`
	DriftDetected  bool      `json:"drift_detected"`
	AlertMessage   string    `json:"alert_message,omitempty"`
}

// GeminiChatGoldenJob thực hiện đối soát hợp đồng StreamGenerate định kỳ trên tài khoản lab.
type GeminiChatGoldenJob struct {
	wire        ports.WireCodec
	transport   ports.UpstreamGoogleTransport
	metrics     *domain.ContractMetrics
	alertNotify func(report ChatGoldenReport)
}

func NewGeminiChatGoldenJob(
	wire ports.WireCodec,
	transport ports.UpstreamGoogleTransport,
	metrics *domain.ContractMetrics,
	alertNotify func(report ChatGoldenReport),
) *GeminiChatGoldenJob {
	return &GeminiChatGoldenJob{
		wire:        wire,
		transport:   transport,
		metrics:     metrics,
		alertNotify: alertNotify,
	}
}

// Run thực thi một vòng đối soát hợp đồng StreamGenerate.
// Bắt buộc tài khoản phải thuộc phân vùng lab (ID chứa 'lab').
func (j *GeminiChatGoldenJob) Run(ctx context.Context, labAccount *domain.ManagedAccount, prompt string) (ChatGoldenReport, error) {
	if labAccount == nil {
		return ChatGoldenReport{}, errors.New("chat golden job: tài khoản kiểm thử rỗng")
	}
	if !isLabAccount(labAccount.ID) {
		return ChatGoldenReport{}, fmt.Errorf("chat golden job vi phạm quy tắc cách ly: tài khoản %q không phải tài khoản lab", labAccount.ID)
	}

	report := ChatGoldenReport{
		Timestamp: time.Now(),
		AccountID: labAccount.ID,
	}

	if prompt == "" {
		prompt = "Xin chào Gemini"
	}

	builder := domain.GeminiPayloadBuilder{
		UserPrompt: prompt,
		Locale:     "en",
		ModelTier:  1,
		ClientUUID: fmt.Sprintf("golden_%d", time.Now().UnixNano()),
	}

	attempt, err := j.wire.MaterializeChat(labAccount, builder)
	if err != nil {
		report.Passed = false
		report.AlertMessage = fmt.Sprintf("Không đóng gói được yêu cầu StreamGenerate: %v", err)
		j.emitAlert(report)
		return report, err
	}

	reqPath := attempt.Path
	if attempt.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = attempt.TargetHost + reqPath
	}

	resp, err := j.transport.DoRequest(
		ctx,
		labAccount,
		domain.ServiceGemini,
		http.MethodPost,
		reqPath,
		strings.NewReader(attempt.Body),
		attempt.ContentType,
	)
	if err != nil {
		report.Passed = false
		report.AlertMessage = fmt.Sprintf("Lỗi kết nối StreamGenerate: %v", err)
		j.emitAlert(report)
		return report, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		report.Passed = false
		report.AlertMessage = fmt.Sprintf("StreamGenerate trả mã trạng thái %d", resp.StatusCode)
		j.emitAlert(report)
		return report, fmt.Errorf("upstream status: %d", resp.StatusCode)
	}

	reply, err := j.wire.DematerializeChat(ctx, resp, j.metrics.Bind(domain.OpChatCompletions), nil)
	if err != nil {
		report.Passed = false
		report.AlertMessage = fmt.Sprintf("Lệch hợp đồng StreamGenerate: %v", err)
		if j.metrics != nil {
			j.metrics.Bind(domain.OpChatCompletions).AddSchema()
		}
		j.emitAlert(report)
		return report, err
	}

	report.ConversationID = reply.ConversationID
	report.TextExtracted = strings.TrimSpace(reply.Text) != ""
	report.Passed = true

	// Kiểm tra tính toàn vẹn: stream hoàn tất phải có ConversationID c_... và text
	if !strings.HasPrefix(reply.ConversationID, "c_") {
		report.DriftDetected = true
		report.AlertMessage = fmt.Sprintf("Phát hiện trôi định dạng: ConversationID %q không bắt đầu bằng 'c_'", reply.ConversationID)
		j.emitAlert(report)
	}

	return report, nil
}

func (j *GeminiChatGoldenJob) emitAlert(report ChatGoldenReport) {
	if j.alertNotify != nil {
		j.alertNotify(report)
	}
}
