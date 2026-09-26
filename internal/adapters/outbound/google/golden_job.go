package google

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const (
	// BaselineUnmappedCount là số lượng trường unmapped trong spec vector 2026-09-23 ([1050, 1, 2, 2, null, 1050])
	BaselineUnmappedCount = 5
)

// GoldenReport chứa kết quả đối soát hợp đồng định kỳ với tài khoản lab.
// Báo cáo tuyệt đối không chứa cookie, SNlM0e hay token.
type GoldenReport struct {
	Timestamp      time.Time `json:"timestamp"`
	AccountID      string    `json:"account_id"`
	SpecVersion    string    `json:"spec_version"`
	Amount         int       `json:"amount"`
	UnmappedFields int       `json:"unmapped_fields"`
	Passed         bool      `json:"passed"`
	DriftDetected  bool      `json:"drift_detected"`
	AlertMessage   string    `json:"alert_message,omitempty"`
}

// NzlxgGoldenJob thực hiện đối soát hợp đồng nzlxg định kỳ trên tài khoản lab.
type NzlxgGoldenJob struct {
	client      ports.FlowClient
	metrics     *domain.ContractMetrics
	alertNotify func(report GoldenReport)
}

func NewNzlxgGoldenJob(client ports.FlowClient, metrics *domain.ContractMetrics, alertNotify func(report GoldenReport)) *NzlxgGoldenJob {
	return &NzlxgGoldenJob{
		client:      client,
		metrics:     metrics,
		alertNotify: alertNotify,
	}
}

// Run thực thi một vòng đối soát hợp đồng nzlxg.
// Bắt buộc: tài khoản phải là tài khoản lab (ID bắt đầu bằng "lab" hoặc gắn cờ nhận diện lab).
// Cấm chạy golden job trên tài khoản người dùng / production.
func (j *NzlxgGoldenJob) Run(ctx context.Context, labAccount *domain.ManagedAccount) (GoldenReport, error) {
	if labAccount == nil {
		return GoldenReport{}, errors.New("golden job: tài khoản kiểm thử rỗng")
	}
	if !isLabAccount(labAccount.ID) {
		return GoldenReport{}, fmt.Errorf("golden job vi phạm quy tắc cách ly: tài khoản %q không phải tài khoản lab", labAccount.ID)
	}

	report := GoldenReport{
		Timestamp:   time.Now(),
		AccountID:   labAccount.ID,
		SpecVersion: domain.FlowCreditSpecVersion,
	}

	balance, err := j.client.GetCreditsBalance(ctx, labAccount)
	if err != nil {
		report.Passed = false
		report.AlertMessage = fmt.Sprintf("Lệch hợp đồng nzlxg: %v", err)
		if j.metrics != nil {
			j.metrics.Bind(domain.OpFlowGetCredits).AddSchema()
		}
		j.emitAlert(report)
		return report, err
	}

	report.Amount = balance.Amount
	report.UnmappedFields = balance.UnmappedFields
	report.Passed = true

	// Kiểm tra độ trôi (drift) số lượng trường unmapped so với baseline spec
	if balance.UnmappedFields != BaselineUnmappedCount {
		report.DriftDetected = true
		report.AlertMessage = fmt.Sprintf("Phát hiện thay đổi schema nzlxg: unmapped fields đổi từ %d thành %d", BaselineUnmappedCount, balance.UnmappedFields)
		if j.metrics != nil {
			j.metrics.Bind(domain.OpFlowGetCredits).AddSchema()
		}
		j.emitAlert(report)
	}

	return report, nil
}

func (j *NzlxgGoldenJob) emitAlert(report GoldenReport) {
	if j.alertNotify != nil {
		j.alertNotify(report)
	}
}

// isLabAccount kiểm tra định danh tài khoản có thuộc phân vùng lab không
func isLabAccount(id string) bool {
	lower := strings.ToLower(id)
	return strings.HasPrefix(lower, "lab") || strings.Contains(lower, "_lab") || strings.Contains(lower, "-lab")
}
