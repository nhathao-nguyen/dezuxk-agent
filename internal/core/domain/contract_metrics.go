package domain

import (
	"sync"
	"time"
)

// ContractMetrics đếm lệch hợp đồng trên đường nóng. Nhãn không chứa account hay secret.
type ContractMetrics struct {
	state     *metricState
	operation string
}

type opCounter struct {
	schema   uint64
	unmapped uint64
	classes  map[ErrorClass]uint64
}

// HistogramBucket biểu diễn một bucket Prometheus với ngưỡng le (less than or equal)
type HistogramBucket struct {
	Le    float64 `json:"le"`
	Count uint64  `json:"count"`
}

// HistogramSnapshot biểu diễn dữ liệu thống kê phân phối độ trễ chuẩn Prometheus
type HistogramSnapshot struct {
	Buckets []HistogramBucket `json:"buckets"`
	Sum     float64           `json:"sum"`
	Count   uint64            `json:"count"`
}

// HistogramTracker theo dõi phân phối độ trễ theo các bucket định trước
type HistogramTracker struct {
	bounds  []float64
	buckets []uint64
	sum     float64
	count   uint64
}

func newHistogramTracker(bounds []float64) *HistogramTracker {
	return &HistogramTracker{
		bounds:  bounds,
		buckets: make([]uint64, len(bounds)),
	}
}

func (h *HistogramTracker) Observe(valSeconds float64) {
	if h == nil {
		return
	}
	h.sum += valSeconds
	h.count++
	for i, b := range h.bounds {
		if valSeconds <= b {
			h.buckets[i]++
		}
	}
}

func (h *HistogramTracker) Snapshot() HistogramSnapshot {
	if h == nil {
		return HistogramSnapshot{}
	}
	buckets := make([]HistogramBucket, len(h.bounds))
	for i, b := range h.bounds {
		buckets[i] = HistogramBucket{Le: b, Count: h.buckets[i]}
	}
	return HistogramSnapshot{
		Buckets: buckets,
		Sum:     h.sum,
		Count:   h.count,
	}
}

type metricState struct {
	mu            sync.Mutex
	totals        opCounter
	ops           map[string]*opCounter
	totalRequests uint64
	rpmBuckets    [60]uint64
	lastSec       int64

	// Metrics mở rộng cho Section 12 (Observability)
	totalReqDurationMs float64
	reqCount           uint64
	totalUpstreamMs    float64
	upstreamCount      uint64
	totalToolMs        float64
	toolCount          uint64
	modelRetries       uint64
	failoverCount      uint64
	activeAgents       int64
	toolErrors         uint64
	mcpErrors          uint64
	cacheHits          uint64
	cacheMisses        uint64
	quotaFailures      uint64
	sandboxFailures    uint64

	// Histograms
	reqDurationHist      *HistogramTracker
	upstreamDurationHist *HistogramTracker
	toolDurationHist     *HistogramTracker
	agentRunDurationHist *HistogramTracker

	// Gauges
	activeRequests      int64
	activeStreams       int64
	agentQueueDepth     int64
	circuitBreakerState int

	// Counters với low-cardinality labels
	rateLimitRejections   map[string]uint64
	concurrencyRejections map[string]uint64
	upstreamFailovers     map[string]uint64
	toolExecutions        map[string]uint64
	toolExecutionErrors   map[string]map[string]uint64
	agentRuns             map[string]uint64
	agentRunFailures      map[string]uint64

	// Multi-Node Cluster Observability Metrics (Section 13)
	clusterRateLimitRejections map[string]uint64
	dependencyHealth           map[string]int
	agentReclaims              map[string]uint64
	staleWorkerRejections      map[string]uint64
	toolFencingRejections      map[string]uint64
	agentLeaseExpirations      uint64
	crossNodeEvents            map[string]uint64
}

func NewContractMetrics() *ContractMetrics {
	return &ContractMetrics{state: &metricState{
		totals: opCounter{classes: map[ErrorClass]uint64{}},
		ops:    map[string]*opCounter{},
		reqDurationHist: newHistogramTracker([]float64{
			0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0,
		}),
		upstreamDurationHist: newHistogramTracker([]float64{
			0.05, 0.1, 0.25, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0,
		}),
		toolDurationHist: newHistogramTracker([]float64{
			0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0, 15.0, 30.0,
		}),
		agentRunDurationHist: newHistogramTracker([]float64{
			1.0, 5.0, 15.0, 30.0, 60.0, 120.0, 300.0, 600.0,
		}),
		rateLimitRejections:        make(map[string]uint64),
		concurrencyRejections:      make(map[string]uint64),
		upstreamFailovers:          make(map[string]uint64),
		toolExecutions:             make(map[string]uint64),
		toolExecutionErrors:        make(map[string]map[string]uint64),
		agentRuns:                  make(map[string]uint64),
		agentRunFailures:           make(map[string]uint64),
		clusterRateLimitRejections: make(map[string]uint64),
		dependencyHealth:           make(map[string]int),
		agentReclaims:              make(map[string]uint64),
		staleWorkerRejections:      make(map[string]uint64),
		toolFencingRejections:      make(map[string]uint64),
		crossNodeEvents:            make(map[string]uint64),
	}}
}

func (m *ContractMetrics) RecordRequest() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	now := time.Now().Unix()
	m.state.totalRequests++

	if m.state.lastSec == 0 {
		m.state.lastSec = now
	} else if now > m.state.lastSec {
		diff := now - m.state.lastSec
		if diff >= 60 {
			m.state.rpmBuckets = [60]uint64{}
		} else {
			for i := int64(1); i <= diff; i++ {
				m.state.rpmBuckets[(m.state.lastSec+i)%60] = 0
			}
		}
		m.state.lastSec = now
	}

	m.state.rpmBuckets[now%60]++
}

func (m *ContractMetrics) GetRPM() int {
	if m == nil || m.state == nil {
		return 0
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	now := time.Now().Unix()
	if m.state.lastSec > 0 && now-m.state.lastSec >= 60 {
		return 0
	}

	var sum uint64
	for i := int64(0); i < 60; i++ {
		sec := now - i
		if m.state.lastSec-sec < 60 && sec <= m.state.lastSec {
			sum += m.state.rpmBuckets[sec%60]
		}
	}
	return int(sum)
}

func (m *ContractMetrics) GetTotalRequests() uint64 {
	if m == nil || m.state == nil {
		return 0
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.totalRequests
}

func (m *ContractMetrics) GetRPMHistory() []int {
	if m == nil || m.state == nil {
		return make([]int, 60)
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	now := time.Now().Unix()
	res := make([]int, 60)
	for i := 59; i >= 0; i-- {
		sec := now - int64(59-i)
		if m.state.lastSec > 0 && now-m.state.lastSec < 60 && sec <= m.state.lastSec && m.state.lastSec-sec < 60 {
			res[i] = int(m.state.rpmBuckets[sec%60])
		} else {
			res[i] = 0
		}
	}
	return res
}

// Bind trả một cửa sổ đếm gắn tên operation. Parser gọi AddSchema trên cửa sổ này.
func (m *ContractMetrics) Bind(operation string) *ContractMetrics {
	if m == nil || m.state == nil || operation == "" {
		return m
	}
	return &ContractMetrics{state: m.state, operation: operation}
}

func (m *ContractMetrics) AddSchema() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.totals.schema++
	if m.operation != "" {
		m.state.bucket(m.operation).schema++
	}
}

func (m *ContractMetrics) AddUnmapped(n int) {
	if m == nil || m.state == nil || n <= 0 {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.totals.unmapped += uint64(n)
	if m.operation != "" {
		m.state.bucket(m.operation).unmapped += uint64(n)
	}
}

func (m *ContractMetrics) AddClass(class ErrorClass) {
	if m == nil || m.state == nil || class == "" {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	addClass(&m.state.totals, class)
	if m.operation != "" {
		addClass(m.state.bucket(m.operation), class)
	}
}

func (s *metricState) bucket(operation string) *opCounter {
	counter, ok := s.ops[operation]
	if !ok {
		counter = &opCounter{classes: map[ErrorClass]uint64{}}
		s.ops[operation] = counter
	}
	if counter.classes == nil {
		counter.classes = map[ErrorClass]uint64{}
	}
	return counter
}

func addClass(counter *opCounter, class ErrorClass) {
	if counter.classes == nil {
		counter.classes = map[ErrorClass]uint64{}
	}
	counter.classes[class]++
}

func (m *ContractMetrics) IncActiveRequests() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.activeRequests++
}

func (m *ContractMetrics) DecActiveRequests() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.activeRequests > 0 {
		m.state.activeRequests--
	}
}

func (m *ContractMetrics) GetActiveRequests() int64 {
	if m == nil || m.state == nil {
		return 0
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.activeRequests
}

func (m *ContractMetrics) IncActiveStreams() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.activeStreams++
}

func (m *ContractMetrics) DecActiveStreams() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.activeStreams > 0 {
		m.state.activeStreams--
	}
}

func (m *ContractMetrics) GetActiveStreams() int64 {
	if m == nil || m.state == nil {
		return 0
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.activeStreams
}

func (m *ContractMetrics) SetAgentQueueDepth(depth int64) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if depth < 0 {
		depth = 0
	}
	m.state.agentQueueDepth = depth
}

func (m *ContractMetrics) GetAgentQueueDepth() int64 {
	if m == nil || m.state == nil {
		return 0
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.agentQueueDepth
}

func (m *ContractMetrics) SetCircuitBreakerState(state int) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.circuitBreakerState = state
}

func (m *ContractMetrics) GetCircuitBreakerState() int {
	if m == nil || m.state == nil {
		return 0
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.circuitBreakerState
}

func (m *ContractMetrics) RecordRequestDuration(d time.Duration, method, endpoint, statusClass string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.reqDurationHist != nil {
		m.state.reqDurationHist.Observe(sec)
	}
	m.state.totalReqDurationMs += float64(d.Milliseconds())
	m.state.reqCount++
}

func (m *ContractMetrics) RecordUpstreamDuration(d time.Duration, model string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.upstreamDurationHist != nil {
		m.state.upstreamDurationHist.Observe(sec)
	}
	m.state.totalUpstreamMs += float64(d.Milliseconds())
	m.state.upstreamCount++
}

func (m *ContractMetrics) RecordToolDuration(d time.Duration, toolName string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.toolDurationHist != nil {
		m.state.toolDurationHist.Observe(sec)
	}
	m.state.totalToolMs += float64(d.Milliseconds())
	m.state.toolCount++
}

func (m *ContractMetrics) RecordAgentRunDuration(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.agentRunDurationHist != nil {
		m.state.agentRunDurationHist.Observe(sec)
	}
}

func (m *ContractMetrics) RecordRateLimitRejection(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if reason == "" {
		reason = "rate_limit_exceeded"
	}
	if m.state.rateLimitRejections == nil {
		m.state.rateLimitRejections = make(map[string]uint64)
	}
	m.state.rateLimitRejections[reason]++
}

func (m *ContractMetrics) RecordConcurrencyRejection(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if reason == "" {
		reason = "concurrency_limit_exceeded"
	}
	if m.state.concurrencyRejections == nil {
		m.state.concurrencyRejections = make(map[string]uint64)
	}
	m.state.concurrencyRejections[reason]++
}

func (m *ContractMetrics) RecordUpstreamFailover(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if reason == "" {
		reason = "failover"
	}
	if m.state.upstreamFailovers == nil {
		m.state.upstreamFailovers = make(map[string]uint64)
	}
	m.state.upstreamFailovers[reason]++
	m.state.failoverCount++
}

func (m *ContractMetrics) RecordToolExecution(toolName string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if toolName == "" {
		toolName = "unknown"
	}
	if m.state.toolExecutions == nil {
		m.state.toolExecutions = make(map[string]uint64)
	}
	m.state.toolExecutions[toolName]++
}

func (m *ContractMetrics) RecordToolExecutionError(toolName, errClass string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if toolName == "" {
		toolName = "unknown"
	}
	if errClass == "" {
		errClass = "runtime_error"
	}
	if m.state.toolExecutionErrors == nil {
		m.state.toolExecutionErrors = make(map[string]map[string]uint64)
	}
	if m.state.toolExecutionErrors[toolName] == nil {
		m.state.toolExecutionErrors[toolName] = make(map[string]uint64)
	}
	m.state.toolExecutionErrors[toolName][errClass]++
	m.state.toolErrors++
}

func (m *ContractMetrics) RecordAgentRun(status string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if status == "" {
		status = "submitted"
	}
	if m.state.agentRuns == nil {
		m.state.agentRuns = make(map[string]uint64)
	}
	m.state.agentRuns[status]++
}

func (m *ContractMetrics) RecordAgentRunFailure(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if reason == "" {
		reason = "execution_error"
	}
	if m.state.agentRunFailures == nil {
		m.state.agentRunFailures = make(map[string]uint64)
	}
	m.state.agentRunFailures[reason]++
}

func (m *ContractMetrics) RecordRequestLatency(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.reqDurationHist != nil {
		m.state.reqDurationHist.Observe(sec)
	}
	m.state.totalReqDurationMs += float64(d.Milliseconds())
	m.state.reqCount++
}

func (m *ContractMetrics) RecordUpstreamLatency(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.upstreamDurationHist != nil {
		m.state.upstreamDurationHist.Observe(sec)
	}
	m.state.totalUpstreamMs += float64(d.Milliseconds())
	m.state.upstreamCount++
}

func (m *ContractMetrics) RecordToolLatency(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	sec := d.Seconds()
	if m.state.toolDurationHist != nil {
		m.state.toolDurationHist.Observe(sec)
	}
	m.state.totalToolMs += float64(d.Milliseconds())
	m.state.toolCount++
}

func (m *ContractMetrics) RecordModelRetry() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.modelRetries++
}

func (m *ContractMetrics) RecordFailover() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.failoverCount++
}

func (m *ContractMetrics) RecordActiveAgent(delta int) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.activeAgents += int64(delta)
	if m.state.activeAgents < 0 {
		m.state.activeAgents = 0
	}
}

func (m *ContractMetrics) RecordToolError() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.toolErrors++
}

func (m *ContractMetrics) RecordMCPError() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.mcpErrors++
}

func (m *ContractMetrics) RecordCacheHit() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.cacheHits++
}

func (m *ContractMetrics) RecordCacheMiss() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.cacheMisses++
}

func (m *ContractMetrics) RecordQuotaFailure() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.quotaFailures++
}

func (m *ContractMetrics) RecordSandboxFailure() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.sandboxFailures++
}

type OperationMetrics struct {
	SchemaUnexpected uint64            `json:"schema_unexpected"`
	UnmappedFields   uint64            `json:"unmapped_fields"`
	Classes          map[string]uint64 `json:"classes"`
}

type ContractSnapshot struct {
	TotalRequests        uint64                      `json:"total_requests"`
	RPM                  int                         `json:"rpm"`
	RPMHistory           []int                       `json:"rpm_history"`
	SchemaUnexpected     uint64                      `json:"schema_unexpected"`
	UnmappedFields       uint64                      `json:"unmapped_fields"`
	Classes              map[string]uint64           `json:"classes"`
	Operations           map[string]OperationMetrics `json:"operations"`
	AvgRequestLatencyMs  float64                     `json:"avg_request_latency_ms"`
	AvgUpstreamLatencyMs float64                     `json:"avg_upstream_latency_ms"`
	AvgToolLatencyMs     float64                     `json:"avg_tool_latency_ms"`
	ModelRetries         uint64                      `json:"model_retries"`
	Failovers            uint64                      `json:"failovers"`
	ActiveAgents         int64                       `json:"active_agents"`
	ToolErrors           uint64                      `json:"tool_errors"`
	MCPErrors            uint64                      `json:"mcp_errors"`
	CacheHits            uint64                      `json:"cache_hits"`
	CacheMisses          uint64                      `json:"cache_misses"`
	CacheHitRatio        float64                     `json:"cache_hit_ratio"`
	QuotaFailures        uint64                      `json:"quota_failures"`
	SandboxFailures      uint64                      `json:"sandbox_failures"`

	// Trường bổ sung cho Observability Hardening
	ActiveRequests        int64                        `json:"active_requests"`
	ActiveStreams         int64                        `json:"active_streams"`
	AgentQueueDepth       int64                        `json:"agent_queue_depth"`
	CircuitBreakerState   int                          `json:"circuit_breaker_state"`
	ReqDurationHist       HistogramSnapshot            `json:"request_duration_histogram"`
	UpstreamDurationHist  HistogramSnapshot            `json:"upstream_duration_histogram"`
	ToolDurationHist      HistogramSnapshot            `json:"tool_duration_histogram"`
	AgentRunDurationHist  HistogramSnapshot            `json:"agent_run_duration_histogram"`
	RateLimitRejections   map[string]uint64            `json:"rate_limit_rejections"`
	ConcurrencyRejections map[string]uint64            `json:"concurrency_rejections"`
	UpstreamFailovers     map[string]uint64            `json:"upstream_failovers"`
	ToolExecutions        map[string]uint64            `json:"tool_executions"`
	ToolExecutionErrors   map[string]map[string]uint64 `json:"tool_execution_errors"`
	AgentRuns             map[string]uint64            `json:"agent_runs"`
	AgentRunFailures      map[string]uint64            `json:"agent_run_failures"`

	// Multi-Node Cluster Observability Metrics (Section 13)
	ClusterRateLimitRejections map[string]uint64 `json:"cluster_rate_limit_rejections"`
	DependencyHealth           map[string]int    `json:"dependency_health"`
	AgentReclaims              map[string]uint64 `json:"agent_reclaims"`
	StaleWorkerRejections      map[string]uint64 `json:"stale_worker_rejections"`
	ToolFencingRejections      map[string]uint64 `json:"tool_fencing_rejections"`
	AgentLeaseExpirations      uint64            `json:"agent_lease_expirations"`
	CrossNodeEvents            map[string]uint64 `json:"cross_node_events"`
}

func (m *ContractMetrics) Snapshot() ContractSnapshot {
	if m == nil || m.state == nil {
		return ContractSnapshot{Classes: map[string]uint64{}, Operations: map[string]OperationMetrics{}}
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()

	now := time.Now().Unix()
	var rpm uint64
	if m.state.lastSec > 0 && now-m.state.lastSec < 60 {
		for i := int64(0); i < 60; i++ {
			sec := now - i
			if m.state.lastSec-sec < 60 && sec <= m.state.lastSec {
				rpm += m.state.rpmBuckets[sec%60]
			}
		}
	}

	history := make([]int, 60)
	for i := 59; i >= 0; i-- {
		sec := now - int64(59-i)
		if m.state.lastSec > 0 && now-m.state.lastSec < 60 && sec <= m.state.lastSec && m.state.lastSec-sec < 60 {
			history[i] = int(m.state.rpmBuckets[sec%60])
		} else {
			history[i] = 0
		}
	}

	operations := make(map[string]OperationMetrics, len(m.state.ops))
	for name, counter := range m.state.ops {
		operations[name] = counter.snapshot()
	}

	var avgReqMs, avgUpstreamMs, avgToolMs float64
	if m.state.reqCount > 0 {
		avgReqMs = m.state.totalReqDurationMs / float64(m.state.reqCount)
	}
	if m.state.upstreamCount > 0 {
		avgUpstreamMs = m.state.totalUpstreamMs / float64(m.state.upstreamCount)
	}
	if m.state.toolCount > 0 {
		avgToolMs = m.state.totalToolMs / float64(m.state.toolCount)
	}
	var hitRatio float64
	totCache := m.state.cacheHits + m.state.cacheMisses
	if totCache > 0 {
		hitRatio = float64(m.state.cacheHits) / float64(totCache)
	}

	var reqHist, upHist, toolHist, agentHist HistogramSnapshot
	if m.state.reqDurationHist != nil {
		reqHist = m.state.reqDurationHist.Snapshot()
	}
	if m.state.upstreamDurationHist != nil {
		upHist = m.state.upstreamDurationHist.Snapshot()
	}
	if m.state.toolDurationHist != nil {
		toolHist = m.state.toolDurationHist.Snapshot()
	}
	if m.state.agentRunDurationHist != nil {
		agentHist = m.state.agentRunDurationHist.Snapshot()
	}

	rateLimitRejections := make(map[string]uint64, len(m.state.rateLimitRejections))
	for k, v := range m.state.rateLimitRejections {
		rateLimitRejections[k] = v
	}
	concurrencyRejections := make(map[string]uint64, len(m.state.concurrencyRejections))
	for k, v := range m.state.concurrencyRejections {
		concurrencyRejections[k] = v
	}
	upstreamFailovers := make(map[string]uint64, len(m.state.upstreamFailovers))
	for k, v := range m.state.upstreamFailovers {
		upstreamFailovers[k] = v
	}
	toolExecutions := make(map[string]uint64, len(m.state.toolExecutions))
	for k, v := range m.state.toolExecutions {
		toolExecutions[k] = v
	}
	toolExecutionErrors := make(map[string]map[string]uint64, len(m.state.toolExecutionErrors))
	for k, v := range m.state.toolExecutionErrors {
		inner := make(map[string]uint64, len(v))
		for ik, iv := range v {
			inner[ik] = iv
		}
		toolExecutionErrors[k] = inner
	}
	agentRuns := make(map[string]uint64, len(m.state.agentRuns))
	for k, v := range m.state.agentRuns {
		agentRuns[k] = v
	}
	agentRunFailures := make(map[string]uint64, len(m.state.agentRunFailures))
	for k, v := range m.state.agentRunFailures {
		agentRunFailures[k] = v
	}

	clusterRateLimitRejections := make(map[string]uint64, len(m.state.clusterRateLimitRejections))
	for k, v := range m.state.clusterRateLimitRejections {
		clusterRateLimitRejections[k] = v
	}
	dependencyHealth := make(map[string]int, len(m.state.dependencyHealth))
	for k, v := range m.state.dependencyHealth {
		dependencyHealth[k] = v
	}
	agentReclaims := make(map[string]uint64, len(m.state.agentReclaims))
	for k, v := range m.state.agentReclaims {
		agentReclaims[k] = v
	}
	staleWorkerRejections := make(map[string]uint64, len(m.state.staleWorkerRejections))
	for k, v := range m.state.staleWorkerRejections {
		staleWorkerRejections[k] = v
	}
	toolFencingRejections := make(map[string]uint64, len(m.state.toolFencingRejections))
	for k, v := range m.state.toolFencingRejections {
		toolFencingRejections[k] = v
	}
	crossNodeEvents := make(map[string]uint64, len(m.state.crossNodeEvents))
	for k, v := range m.state.crossNodeEvents {
		crossNodeEvents[k] = v
	}

	return ContractSnapshot{
		TotalRequests:              m.state.totalRequests,
		RPM:                        int(rpm),
		RPMHistory:                 history,
		SchemaUnexpected:           m.state.totals.schema,
		UnmappedFields:             m.state.totals.unmapped,
		Classes:                    classMap(m.state.totals.classes),
		Operations:                 operations,
		AvgRequestLatencyMs:        avgReqMs,
		AvgUpstreamLatencyMs:       avgUpstreamMs,
		AvgToolLatencyMs:           avgToolMs,
		ModelRetries:               m.state.modelRetries,
		Failovers:                  m.state.failoverCount,
		ActiveAgents:               m.state.activeAgents,
		ToolErrors:                 m.state.toolErrors,
		MCPErrors:                  m.state.mcpErrors,
		CacheHits:                  m.state.cacheHits,
		CacheMisses:                m.state.cacheMisses,
		CacheHitRatio:              hitRatio,
		QuotaFailures:              m.state.quotaFailures,
		SandboxFailures:            m.state.sandboxFailures,
		ActiveRequests:             m.state.activeRequests,
		ActiveStreams:              m.state.activeStreams,
		AgentQueueDepth:            m.state.agentQueueDepth,
		CircuitBreakerState:        m.state.circuitBreakerState,
		ReqDurationHist:            reqHist,
		UpstreamDurationHist:       upHist,
		ToolDurationHist:           toolHist,
		AgentRunDurationHist:       agentHist,
		RateLimitRejections:        rateLimitRejections,
		ConcurrencyRejections:      concurrencyRejections,
		UpstreamFailovers:          upstreamFailovers,
		ToolExecutions:             toolExecutions,
		ToolExecutionErrors:        toolExecutionErrors,
		AgentRuns:                  agentRuns,
		AgentRunFailures:           agentRunFailures,
		ClusterRateLimitRejections: clusterRateLimitRejections,
		DependencyHealth:           dependencyHealth,
		AgentReclaims:              agentReclaims,
		StaleWorkerRejections:      staleWorkerRejections,
		ToolFencingRejections:      toolFencingRejections,
		AgentLeaseExpirations:      m.state.agentLeaseExpirations,
		CrossNodeEvents:            crossNodeEvents,
	}
}

func (m *ContractMetrics) RecordClusterRateLimitRejection(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.clusterRateLimitRejections == nil {
		m.state.clusterRateLimitRejections = make(map[string]uint64)
	}
	if reason == "" {
		reason = "rate_limit_exceeded"
	}
	m.state.clusterRateLimitRejections[reason]++
}

func (m *ContractMetrics) RecordDependencyHealth(dep string, healthy bool) {
	if m == nil || m.state == nil || dep == "" {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.dependencyHealth == nil {
		m.state.dependencyHealth = make(map[string]int)
	}
	val := 0
	if healthy {
		val = 1
	}
	m.state.dependencyHealth[dep] = val
}

func (m *ContractMetrics) RecordAgentReclaim(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.agentReclaims == nil {
		m.state.agentReclaims = make(map[string]uint64)
	}
	if reason == "" {
		reason = "lease_expired"
	}
	m.state.agentReclaims[reason]++
}

func (m *ContractMetrics) RecordStaleWorkerRejection(reason string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.staleWorkerRejections == nil {
		m.state.staleWorkerRejections = make(map[string]uint64)
	}
	if reason == "" {
		reason = "fencing_token_mismatch"
	}
	m.state.staleWorkerRejections[reason]++
}

func (m *ContractMetrics) RecordToolFencingRejection(toolClass string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.toolFencingRejections == nil {
		m.state.toolFencingRejections = make(map[string]uint64)
	}
	if toolClass == "" {
		toolClass = "destructive"
	}
	m.state.toolFencingRejections[toolClass]++
}

func (m *ContractMetrics) RecordAgentLeaseExpiration() {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.agentLeaseExpirations++
}

func (m *ContractMetrics) RecordCrossNodeEvent(kind string) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.crossNodeEvents == nil {
		m.state.crossNodeEvents = make(map[string]uint64)
	}
	if kind == "" {
		kind = "event"
	}
	m.state.crossNodeEvents[kind]++
}

func (c *opCounter) snapshot() OperationMetrics {
	return OperationMetrics{
		SchemaUnexpected: c.schema,
		UnmappedFields:   c.unmapped,
		Classes:          classMap(c.classes),
	}
}

func classMap(classes map[ErrorClass]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(classes))
	for class, count := range classes {
		out[string(class)] = count
	}
	return out
}
