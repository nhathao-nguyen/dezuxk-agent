package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestPrometheusMetricsExporter(t *testing.T) {
	metrics := domain.NewContractMetrics()
	metrics.RecordRequest()
	metrics.AddSchema()

	exporter := NewPrometheusMetricsExporter(nil, nil, nil, metrics)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	exporter.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", contentType)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "gateway_requests_total 1") {
		t.Errorf("expected gateway_requests_total 1 in body, got:\n%s", body)
	}
	if !strings.Contains(body, "gateway_errors_total 1") {
		t.Errorf("expected gateway_errors_total 1 in body, got:\n%s", body)
	}
}
