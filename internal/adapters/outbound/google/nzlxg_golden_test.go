package google_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/core/domain"
)

func TestNzlxgGoldenJob_VectorContractPass(t *testing.T) {
	mockResp := `)]}'

35
[[["wrb.fr","nzlxg","[1050, 1, 2, 2, null, 1050]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockResp))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), metrics)

	alerts := 0
	job := google.NewNzlxgGoldenJob(client, metrics, func(report google.GoldenReport) {
		alerts++
	})

	labAcc := &domain.ManagedAccount{
		ID:         "lab_test_account",
		FlowSNlM0e: "fake-sn",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "fake-osid"}),
	}

	report, err := job.Run(context.Background(), labAcc)
	if err != nil {
		t.Fatalf("golden job failed: %v", err)
	}

	if !report.Passed {
		t.Fatal("expected report to pass")
	}
	if report.DriftDetected {
		t.Fatal("baseline vector should not have drift")
	}
	if report.Amount != 1050 || report.UnmappedFields != 5 {
		t.Fatalf("unexpected report data: %+v", report)
	}
	if alerts != 0 {
		t.Fatalf("expected 0 alerts, got %d", alerts)
	}
}

func TestNzlxgGoldenJob_DetectsSchemaDrift(t *testing.T) {
	// Google trả về chuỗi thay vì số nguyên
	mockResp := `)]}'

35
[[["wrb.fr","nzlxg","[\"invalid_string\", 1, 2]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockResp))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), metrics)

	var alertReceived google.GoldenReport
	alerts := 0
	job := google.NewNzlxgGoldenJob(client, metrics, func(report google.GoldenReport) {
		alerts++
		alertReceived = report
	})

	labAcc := &domain.ManagedAccount{
		ID:         "lab_account_01",
		FlowSNlM0e: "fake-sn",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "fake-osid"}),
	}

	report, err := job.Run(context.Background(), labAcc)
	if err == nil {
		t.Fatal("expected schema error")
	}
	if report.Passed {
		t.Fatal("expected passed to be false")
	}
	if alerts != 1 {
		t.Fatalf("expected 1 alert, got %d", alerts)
	}
	if !strings.Contains(alertReceived.AlertMessage, "Lệch hợp đồng nzlxg") {
		t.Fatalf("unexpected alert: %s", alertReceived.AlertMessage)
	}
}

func TestNzlxgGoldenJob_DetectsUnmappedDrift(t *testing.T) {
	// Google thêm 2 trường lạ mới (tổng 7 unmapped thay vì 5)
	mockResp := `)]}'

45
[[["wrb.fr","nzlxg","[1050, 1, 2, 2, null, 1050, \"new_field_1\", {\"sub\": true}]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockResp))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	client := google.NewFlowClientAdapter(transport, domain.DefaultRpcRegistry(), metrics)

	var alertReceived google.GoldenReport
	alerts := 0
	job := google.NewNzlxgGoldenJob(client, metrics, func(report google.GoldenReport) {
		alerts++
		alertReceived = report
	})

	labAcc := &domain.ManagedAccount{
		ID:         "lab_experiment",
		FlowSNlM0e: "fake-sn",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "fake-osid"}),
	}

	report, err := job.Run(context.Background(), labAcc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Passed {
		t.Fatal("unmapped field addition must still pass extraction")
	}
	if !report.DriftDetected {
		t.Fatal("expected drift detected when unmapped count changes")
	}
	if alerts != 1 {
		t.Fatalf("expected 1 drift alert, got %d", alerts)
	}
	if !strings.Contains(alertReceived.AlertMessage, "unmapped fields đổi từ 5 thành 7") {
		t.Fatalf("alert message: %s", alertReceived.AlertMessage)
	}
}

func TestNzlxgGoldenJob_RejectsProductionAccount(t *testing.T) {
	metrics := domain.NewContractMetrics()
	client := google.NewFlowClientAdapter(nil, domain.DefaultRpcRegistry(), metrics)
	job := google.NewNzlxgGoldenJob(client, metrics, nil)

	prodAcc := &domain.ManagedAccount{
		ID:         "production_user_account",
		FlowSNlM0e: "prod-sn",
	}

	_, err := job.Run(context.Background(), prodAcc)
	if err == nil {
		t.Fatal("golden job must reject non-lab account")
	}
	if !strings.Contains(err.Error(), "không phải tài khoản lab") {
		t.Fatalf("expected isolation error, got: %v", err)
	}
}

func TestNzlxgGoldenJob_LiveLabAccount(t *testing.T) {
	cookie := os.Getenv("FLOW_LAB_COOKIE")
	if cookie == "" {
		cookie = os.Getenv("DEZUXK_LAB_COOKIE")
	}
	if cookie == "" {
		t.Skip("bỏ qua live lab test: không có biến môi trường FLOW_LAB_COOKIE / DEZUXK_LAB_COOKIE")
	}

	// Chạy kiểm thử sống chỉ khi môi trường lab được cấp
	// Tuyệt đối không hardcode cookie trong code
}
