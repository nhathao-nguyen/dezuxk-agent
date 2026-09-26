package google

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// MediaGoldenReport chứa kết quả đối soát hợp đồng định kỳ cho Flow Media và csbIsb.
type MediaGoldenReport struct {
	Timestamp     time.Time `json:"timestamp"`
	AccountID     string    `json:"account_id"`
	Passed        bool      `json:"passed"`
	LockVerified  bool      `json:"lock_verified"`
	MediaVerified bool      `json:"media_verified"`
	DriftDetected bool      `json:"drift_detected"`
	AlertMessage  string    `json:"alert_message,omitempty"`
}

// FlowMediaGoldenJob thực hiện đối soát hợp đồng StreamChat và csbIsb trên tài khoản lab.
type FlowMediaGoldenJob struct {
	client      ports.FlowClient
	wire        ports.WireCodec
	metrics     *domain.ContractMetrics
	alertNotify func(report MediaGoldenReport)
}

func NewFlowMediaGoldenJob(
	client ports.FlowClient,
	wire ports.WireCodec,
	metrics *domain.ContractMetrics,
	alertNotify func(report MediaGoldenReport),
) *FlowMediaGoldenJob {
	return &FlowMediaGoldenJob{
		client:      client,
		wire:        wire,
		metrics:     metrics,
		alertNotify: alertNotify,
	}
}

// Run thực thi một vòng đối soát hợp đồng Flow Media.
// Bắt buộc tài khoản phải thuộc phân vùng lab (ID chứa 'lab').
func (j *FlowMediaGoldenJob) Run(ctx context.Context, labAccount *domain.ManagedAccount, projectUUID string) (MediaGoldenReport, error) {
	if labAccount == nil {
		return MediaGoldenReport{}, errors.New("media golden job: tài khoản kiểm thử rỗng")
	}
	if !isLabAccount(labAccount.ID) {
		return MediaGoldenReport{}, fmt.Errorf("media golden job vi phạm quy tắc cách ly: tài khoản %q không phải tài khoản lab", labAccount.ID)
	}

	report := MediaGoldenReport{
		Timestamp: time.Now(),
		AccountID: labAccount.ID,
	}

	if projectUUID == "" {
		projectUUID = "00000000-0000-0000-0000-000000000001"
	}

	// 1. Đối soát hợp đồng khóa phiên csbIsb
	if j.client != nil {
		if err := j.client.RegisterSessionLock(ctx, labAccount, projectUUID); err != nil {
			report.Passed = false
			report.AlertMessage = fmt.Sprintf("Lệch hợp đồng csbIsb: %v", err)
			j.emitAlert(report)
			return report, err
		}
		report.LockVerified = true
	}

	// 2. Đối soát đóng gói StreamChat (pack và options)
	input := domain.FlowMediaInput{
		RequestID:       "golden_req_001",
		Prompt:          "cinematic landscape at sunset",
		ProjectID:       projectUUID,
		SessionToken:    "lab_session_token",
		ModelID:         "veo_3_1_fast",
		DurationSeconds: 8,
		AspectCode:      2,
	}

	attempt, err := j.wire.MaterializeFlowMedia(labAccount, input)
	if err != nil {
		report.Passed = false
		report.AlertMessage = fmt.Sprintf("Lỗi đóng gói StreamChat: %v", err)
		j.emitAlert(report)
		return report, err
	}
	if attempt.Path == "" || attempt.Body == "" {
		report.Passed = false
		report.DriftDetected = true
		report.AlertMessage = "StreamChat attempt rỗng"
		j.emitAlert(report)
		return report, errors.New("StreamChat attempt rỗng")
	}

	report.MediaVerified = true
	report.Passed = true
	return report, nil
}

func (j *FlowMediaGoldenJob) emitAlert(report MediaGoldenReport) {
	if j.alertNotify != nil {
		j.alertNotify(report)
	}
}
