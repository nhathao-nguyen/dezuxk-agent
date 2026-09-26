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

func TestFlowMediaGoldenJob_VectorPass(t *testing.T) {
	mockLockResp := `)]}'

25
[[["wrb.fr","csbIsb","[1]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockLockResp))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	rpcReg := domain.DefaultRpcRegistry()
	client := google.NewFlowClientAdapter(transport, rpcReg, metrics)
	wire := google.NewWireAdapter(rpcReg)

	alerts := 0
	job := google.NewFlowMediaGoldenJob(client, wire, metrics, func(report google.MediaGoldenReport) {
		alerts++
	})

	labAcc := &domain.ManagedAccount{
		ID:         "lab_media_tester",
		FlowSNlM0e: "fake-sn",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "fake-osid"}),
	}

	report, err := job.Run(context.Background(), labAcc, "11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatalf("media golden job failed: %v", err)
	}

	if !report.Passed || !report.LockVerified || !report.MediaVerified {
		t.Fatalf("expected report passed, got: %+v", report)
	}
	if alerts != 0 {
		t.Fatalf("expected 0 alerts, got %d", alerts)
	}
}

func TestFlowMediaGoldenJob_DetectsLockDrift(t *testing.T) {
	// Google trả về phản hồi sai lệch cho csbIsb (ví dụ rỗng hoặc lỗi)
	mockLockResp := `)]}'

25
[[["wrb.fr","csbIsb","[\"error_lock\"]"]]]
`
	transport := &mockTransport{
		doRequestFunc: func(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind, method string, path string, body io.Reader, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(mockLockResp))}, nil
		},
	}

	metrics := domain.NewContractMetrics()
	rpcReg := domain.DefaultRpcRegistry()
	client := google.NewFlowClientAdapter(transport, rpcReg, metrics)
	wire := google.NewWireAdapter(rpcReg)

	var alertReceived google.MediaGoldenReport
	alerts := 0
	job := google.NewFlowMediaGoldenJob(client, wire, metrics, func(report google.MediaGoldenReport) {
		alerts++
		alertReceived = report
	})

	labAcc := &domain.ManagedAccount{
		ID:         "lab_media_tester",
		FlowSNlM0e: "fake-sn",
		Jar:        domain.NewCookieJar(map[string]string{"OSID": "fake-osid"}),
	}

	report, err := job.Run(context.Background(), labAcc, "11111111-2222-3333-4444-555555555555")
	if err == nil {
		t.Fatal("expected error on invalid lock payload")
	}
	if report.Passed || alerts != 1 {
		t.Fatalf("expected failure and 1 alert: passed=%v alerts=%d", report.Passed, alerts)
	}
	if !strings.Contains(alertReceived.AlertMessage, "Lệch hợp đồng csbIsb") {
		t.Fatalf("unexpected alert: %s", alertReceived.AlertMessage)
	}
}

func TestFlowMediaGoldenJob_RejectsProductionAccount(t *testing.T) {
	wire := google.NewWireAdapter(domain.DefaultRpcRegistry())
	job := google.NewFlowMediaGoldenJob(nil, wire, nil, nil)

	prodAcc := &domain.ManagedAccount{
		ID:         "prod_video_creator",
		FlowSNlM0e: "prod-sn",
	}

	_, err := job.Run(context.Background(), prodAcc, "11111111-2222-3333-4444-555555555555")
	if err == nil {
		t.Fatal("expected rejection of non-lab account")
	}
	if !strings.Contains(err.Error(), "không phải tài khoản lab") {
		t.Fatalf("expected isolation error, got: %v", err)
	}
}

func TestFlowMediaGoldenJob_LiveLabAccount(t *testing.T) {
	cookie := os.Getenv("FLOW_LAB_COOKIE")
	if cookie == "" {
		cookie = os.Getenv("DEZUXK_LAB_COOKIE")
	}
	if cookie == "" {
		t.Skip("bỏ qua live media lab test: không có biến môi trường FLOW_LAB_COOKIE / DEZUXK_LAB_COOKIE")
	}
}
