package http

import (
	"context"
	"fmt"
	"net/http"
	"sort"
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

func writeHistogram(sb *strings.Builder, name, help string, h domain.HistogramSnapshot, fallbackAvg float64) {
	sb.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
	sb.WriteString(fmt.Sprintf("# TYPE %s histogram\n", name))
	if len(h.Buckets) > 0 {
		for _, b := range h.Buckets {
			sb.WriteString(fmt.Sprintf("%s_bucket{le=\"%g\"} %d\n", name, b.Le, b.Count))
		}
		sb.WriteString(fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", name, h.Count))
		sb.WriteString(fmt.Sprintf("%s_sum %.4f\n", name, h.Sum))
		sb.WriteString(fmt.Sprintf("%s_count %d\n", name, h.Count))
	} else {
		count := uint64(0)
		sum := 0.0
		if fallbackAvg > 0 {
			count = 1
			sum = fallbackAvg
		}
		sb.WriteString(fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", name, count))
		sb.WriteString(fmt.Sprintf("%s_sum %.4f\n", name, sum))
		sb.WriteString(fmt.Sprintf("%s_count %d\n", name, count))
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
	var snap domain.ContractSnapshot
	if e.metrics != nil {
		snap = e.metrics.Snapshot()
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

	// 2. Active Gauges (Requests, Streams, Queue Depth)
	sb.WriteString("# HELP gateway_active_requests Number of current in-flight HTTP requests.\n")
	sb.WriteString("# TYPE gateway_active_requests gauge\n")
	sb.WriteString(fmt.Sprintf("gateway_active_requests %d\n", snap.ActiveRequests))

	sb.WriteString("# HELP gateway_active_streams Number of current active SSE streaming connections.\n")
	sb.WriteString("# TYPE gateway_active_streams gauge\n")
	sb.WriteString(fmt.Sprintf("gateway_active_streams %d\n", snap.ActiveStreams))

	queueDepth := snap.AgentQueueDepth
	if e.agentRunRepo != nil {
		if pending, err := e.agentRunRepo.ListPendingRuns(r.Context()); err == nil {
			queueDepth = int64(len(pending))
		}
	}
	sb.WriteString("# HELP gateway_agent_queue_depth Number of queued and pending agent runs waiting to execute.\n")
	sb.WriteString("# TYPE gateway_agent_queue_depth gauge\n")
	sb.WriteString(fmt.Sprintf("gateway_agent_queue_depth %d\n", queueDepth))

	// 3. Models Active
	sb.WriteString("# HELP gateway_models_active Number of active registered AI models.\n")
	sb.WriteString("# TYPE gateway_models_active gauge\n")
	activeModels := 0
	if e.modelRegistry != nil {
		activeModels = e.modelRegistry.Count()
	}
	sb.WriteString(fmt.Sprintf("gateway_models_active %d\n", activeModels))

	// 4. Latency Histograms
	writeHistogram(&sb, "gateway_request_duration_seconds", "HTTP request latency distribution in seconds.", snap.ReqDurationHist, snap.AvgRequestLatencyMs/1000.0)
	writeHistogram(&sb, "gateway_upstream_duration_seconds", "Upstream Google AI call duration in seconds.", snap.UpstreamDurationHist, snap.AvgUpstreamLatencyMs/1000.0)
	writeHistogram(&sb, "gateway_tool_duration_seconds", "Tool execution duration in seconds.", snap.ToolDurationHist, snap.AvgToolLatencyMs/1000.0)
	writeHistogram(&sb, "gateway_agent_run_duration_seconds", "Autonomous agent run execution duration in seconds.", snap.AgentRunDurationHist, 0)

	// Backward-compatible gauge latency aliases
	sb.WriteString("# HELP llm_latency_seconds Average LLM upstream call latency in seconds.\n")
	sb.WriteString("# TYPE llm_latency_seconds gauge\n")
	sb.WriteString(fmt.Sprintf("llm_latency_seconds %.4f\n", snap.AvgUpstreamLatencyMs/1000.0))

	sb.WriteString("# HELP tool_duration_seconds Average tool execution duration in seconds.\n")
	sb.WriteString("# TYPE tool_duration_seconds gauge\n")
	sb.WriteString(fmt.Sprintf("tool_duration_seconds %.4f\n", snap.AvgToolLatencyMs/1000.0))

	// 5. Rate Limiting & Concurrency Rejections
	sb.WriteString("# HELP gateway_rate_limit_rejections_total Total rate limit rejections (HTTP 429).\n")
	sb.WriteString("# TYPE gateway_rate_limit_rejections_total counter\n")
	if len(snap.RateLimitRejections) == 0 {
		sb.WriteString("gateway_rate_limit_rejections_total{reason=\"rate_limit_exceeded\"} 0\n")
	} else {
		keys := sortedKeys(snap.RateLimitRejections)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("gateway_rate_limit_rejections_total{reason=%q} %d\n", k, snap.RateLimitRejections[k]))
		}
	}

	sb.WriteString("# HELP gateway_concurrency_rejections_total Total concurrency limit rejections.\n")
	sb.WriteString("# TYPE gateway_concurrency_rejections_total counter\n")
	if len(snap.ConcurrencyRejections) == 0 {
		sb.WriteString("gateway_concurrency_rejections_total{reason=\"concurrency_limit_exceeded\"} 0\n")
	} else {
		keys := sortedKeys(snap.ConcurrencyRejections)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("gateway_concurrency_rejections_total{reason=%q} %d\n", k, snap.ConcurrencyRejections[k]))
		}
	}

	// 6. Upstream Failover & Circuit Breaker State
	sb.WriteString("# HELP gateway_upstream_failover_total Total failover occurrences across upstream accounts.\n")
	sb.WriteString("# TYPE gateway_upstream_failover_total counter\n")
	if len(snap.UpstreamFailovers) == 0 {
		sb.WriteString(fmt.Sprintf("gateway_upstream_failover_total{reason=\"failover\"} %d\n", snap.Failovers))
	} else {
		keys := sortedKeys(snap.UpstreamFailovers)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("gateway_upstream_failover_total{reason=%q} %d\n", k, snap.UpstreamFailovers[k]))
		}
	}

	sb.WriteString("# HELP gateway_circuit_breaker_state Upstream circuit breaker state (0=closed/healthy, 1=half_open, 2=open/tripped).\n")
	sb.WriteString("# TYPE gateway_circuit_breaker_state gauge\n")
	sb.WriteString(fmt.Sprintf("gateway_circuit_breaker_state %d\n", snap.CircuitBreakerState))

	// 7. Tool Executions & Tool Execution Errors
	sb.WriteString("# HELP gateway_tool_execution_total Total agent tool execution count.\n")
	sb.WriteString("# TYPE gateway_tool_execution_total counter\n")
	if len(snap.ToolExecutions) == 0 {
		sb.WriteString("gateway_tool_execution_total{tool_name=\"none\"} 0\n")
	} else {
		keys := sortedKeys(snap.ToolExecutions)
		for _, toolName := range keys {
			sb.WriteString(fmt.Sprintf("gateway_tool_execution_total{tool_name=%q} %d\n", toolName, snap.ToolExecutions[toolName]))
		}
	}

	sb.WriteString("# HELP gateway_tool_execution_errors_total Total agent tool execution errors.\n")
	sb.WriteString("# TYPE gateway_tool_execution_errors_total counter\n")
	if len(snap.ToolExecutionErrors) == 0 {
		sb.WriteString("gateway_tool_execution_errors_total{tool_name=\"none\",error_class=\"none\"} 0\n")
	} else {
		tools := make([]string, 0, len(snap.ToolExecutionErrors))
		for t := range snap.ToolExecutionErrors {
			tools = append(tools, t)
		}
		sort.Strings(tools)
		for _, toolName := range tools {
			errMap := snap.ToolExecutionErrors[toolName]
			errClasses := sortedKeys(errMap)
			for _, errClass := range errClasses {
				sb.WriteString(fmt.Sprintf("gateway_tool_execution_errors_total{tool_name=%q,error_class=%q} %d\n", toolName, errClass, errMap[errClass]))
			}
		}
	}
	sb.WriteString(fmt.Sprintf("tool_errors_total %d\n", snap.ToolErrors+snap.MCPErrors))

	// 8. Agent Runs & Agent Failures
	sb.WriteString("# HELP gateway_agent_runs_total Total durable agent runs submitted.\n")
	sb.WriteString("# TYPE gateway_agent_runs_total counter\n")
	runsStatusCounts := make(map[string]int)
	if e.agentRunRepo != nil {
		runs, err := e.agentRunRepo.List(context.Background(), "", 100, 0)
		if err == nil {
			for _, r := range runs {
				runsStatusCounts[string(r.Status)]++
			}
		}
	}
	if len(runsStatusCounts) == 0 && len(snap.AgentRuns) > 0 {
		for st, count := range snap.AgentRuns {
			runsStatusCounts[st] = int(count)
		}
	}
	if len(runsStatusCounts) == 0 {
		sb.WriteString("gateway_agent_runs_total{status=\"none\"} 0\n")
	} else {
		var statuses []string
		for st := range runsStatusCounts {
			statuses = append(statuses, st)
		}
		sort.Strings(statuses)
		for _, st := range statuses {
			sb.WriteString(fmt.Sprintf("gateway_agent_runs_total{status=%q} %d\n", st, runsStatusCounts[st]))
		}
	}

	sb.WriteString("# HELP gateway_agent_run_failures_total Total agent run failures.\n")
	sb.WriteString("# TYPE gateway_agent_run_failures_total counter\n")
	if len(snap.AgentRunFailures) == 0 {
		sb.WriteString("gateway_agent_run_failures_total{reason=\"none\"} 0\n")
	} else {
		reasons := sortedKeys(snap.AgentRunFailures)
		for _, r := range reasons {
			sb.WriteString(fmt.Sprintf("gateway_agent_run_failures_total{reason=%q} %d\n", r, snap.AgentRunFailures[r]))
		}
	}

	sb.WriteString(fmt.Sprintf("agent_active_runs %d\n", snap.ActiveAgents))

	// 9. Upstream Account Metrics (Masked for zero secret leak)
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

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(sb.String()))
}

func sortedKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
