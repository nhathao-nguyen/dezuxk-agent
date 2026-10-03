package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// PrometheusMetricsExporter xuất dữ liệu giám sát định dạng chuẩn Prometheus (text/plain 0.0.4)
type PrometheusMetricsExporter struct {
	sessionRepo   ports.SessionRepository
	modelRegistry *domain.ModelRegistry
	agentRunRepo  ports.AgentRunRepository
	metrics       *domain.ContractMetrics
}

// NewPrometheusMetricsExporter khởi tạo exporter giám sát
func NewPrometheusMetricsExporter(
	sessionRepo ports.SessionRepository,
	modelRegistry *domain.ModelRegistry,
	agentRunRepo ports.AgentRunRepository,
	metrics *domain.ContractMetrics,
) *PrometheusMetricsExporter {
	return &PrometheusMetricsExporter{
		sessionRepo:   sessionRepo,
		modelRegistry: modelRegistry,
		agentRunRepo:  agentRunRepo,
		metrics:       metrics,
	}
}

// ServeHTTP xử lý GET /metrics
func (e *PrometheusMetricsExporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var sb strings.Builder

	// 1. Gateway Request Metrics
	sb.WriteString("# HELP gateway_requests_total Total number of HTTP requests processed by the gateway.\n")
	sb.WriteString("# TYPE gateway_requests_total counter\n")
	var reqCount uint64
	var errCount uint64
	if e.metrics != nil {
		snap := e.metrics.Snapshot()
		reqCount = snap.TotalRequests
		errCount = snap.ToolErrors + snap.MCPErrors + snap.SchemaUnexpected
		for _, count := range snap.Classes {
			errCount += count
		}
	}
	sb.WriteString(fmt.Sprintf("gateway_requests_total %d\n", reqCount))

	sb.WriteString("# HELP gateway_errors_total Total number of errors encountered.\n")
	sb.WriteString("# TYPE gateway_errors_total counter\n")
	sb.WriteString(fmt.Sprintf("gateway_errors_total %d\n", errCount))

	// 2. Models Active
	sb.WriteString("# HELP gateway_models_active Number of active registered AI models.\n")
	sb.WriteString("# TYPE gateway_models_active gauge\n")
	activeModels := 0
	if e.modelRegistry != nil {
		activeModels = e.modelRegistry.Count()
	}
	sb.WriteString(fmt.Sprintf("gateway_models_active %d\n", activeModels))

	// 3. Upstream Account Metrics (Masked for zero secret leak)
	if e.sessionRepo != nil {
		accounts := e.sessionRepo.ListAll(r.Context())
		sb.WriteString("# HELP gemini_account_health_score Upstream Gemini account health score (0.0 to 1.0).\n")
		sb.WriteString("# TYPE gemini_account_health_score gauge\n")
		for _, acc := range accounts {
			masked := domain.MaskAccountID(acc.ID)
			score := acc.GetHealthScore()
			status := acc.GetHealthStatus()
			sb.WriteString(fmt.Sprintf("gemini_account_health_score{account_id=%q,status=%q} %.2f\n", masked, status, score))
		}

		sb.WriteString("# HELP gemini_account_failures_total Total failure count per account.\n")
		sb.WriteString("# TYPE gemini_account_failures_total counter\n")
		for _, acc := range accounts {
			masked := domain.MaskAccountID(acc.ID)
			st, c429, c403, latency, _ := acc.GetExtendedStats()
			sb.WriteString(fmt.Sprintf("gemini_account_failures_total{account_id=%q,status=%q,code=\"429\"} %d\n", masked, st, c429))
			sb.WriteString(fmt.Sprintf("gemini_account_failures_total{account_id=%q,status=%q,code=\"403\"} %d\n", masked, st, c403))
			sb.WriteString(fmt.Sprintf("gemini_account_avg_latency_ms{account_id=%q} %.1f\n", masked, latency))
		}
	}

	// 4. Agent Runs & Execution Metrics
	if e.metrics != nil {
		snap := e.metrics.Snapshot()
		sb.WriteString("# HELP gateway_request_duration_seconds Average gateway request duration in seconds.\n")
		sb.WriteString("# TYPE gateway_request_duration_seconds gauge\n")
		sb.WriteString(fmt.Sprintf("gateway_request_duration_seconds %.4f\n", snap.AvgRequestLatencyMs/1000.0))

		sb.WriteString("# HELP llm_latency_seconds Average LLM upstream call latency in seconds.\n")
		sb.WriteString("# TYPE llm_latency_seconds gauge\n")
		sb.WriteString(fmt.Sprintf("llm_latency_seconds %.4f\n", snap.AvgUpstreamLatencyMs/1000.0))

		sb.WriteString("# HELP tool_duration_seconds Average tool execution duration in seconds.\n")
		sb.WriteString("# TYPE tool_duration_seconds gauge\n")
		sb.WriteString(fmt.Sprintf("tool_duration_seconds %.4f\n", snap.AvgToolLatencyMs/1000.0))

		sb.WriteString("# HELP tool_errors_total Total number of tool execution failures.\n")
		sb.WriteString("# TYPE tool_errors_total counter\n")
		sb.WriteString(fmt.Sprintf("tool_errors_total %d\n", snap.ToolErrors+snap.MCPErrors))

		sb.WriteString("# HELP agent_active_runs Current number of actively running agents.\n")
		sb.WriteString("# TYPE agent_active_runs gauge\n")
		sb.WriteString(fmt.Sprintf("agent_active_runs %d\n", snap.ActiveAgents))

		sb.WriteString("# HELP upstream_failover_total Total number of upstream failover events triggered.\n")
		sb.WriteString("# TYPE upstream_failover_total counter\n")
		sb.WriteString(fmt.Sprintf("upstream_failover_total %d\n", snap.Failovers))
	}

	if e.agentRunRepo != nil {
		sb.WriteString("# HELP agent_runs_total Total durable agent runs submitted.\n")
		sb.WriteString("# TYPE agent_runs_total counter\n")
		runs, err := e.agentRunRepo.List(context.Background(), "", 100, 0)
		if err == nil {
			statusCounts := make(map[string]int)
			for _, r := range runs {
				statusCounts[string(r.Status)]++
			}
			for st, count := range statusCounts {
				sb.WriteString(fmt.Sprintf("agent_runs_total{status=%q} %d\n", st, count))
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(sb.String()))
}
