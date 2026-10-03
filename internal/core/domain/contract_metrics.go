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
}

func NewContractMetrics() *ContractMetrics {
	return &ContractMetrics{state: &metricState{
		totals: opCounter{classes: map[ErrorClass]uint64{}},
		ops:    map[string]*opCounter{},
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

func (m *ContractMetrics) RecordRequestLatency(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.totalReqDurationMs += float64(d.Milliseconds())
	m.state.reqCount++
}

func (m *ContractMetrics) RecordUpstreamLatency(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.totalUpstreamMs += float64(d.Milliseconds())
	m.state.upstreamCount++
}

func (m *ContractMetrics) RecordToolLatency(d time.Duration) {
	if m == nil || m.state == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
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

	return ContractSnapshot{
		TotalRequests:        m.state.totalRequests,
		RPM:                  int(rpm),
		RPMHistory:           history,
		SchemaUnexpected:     m.state.totals.schema,
		UnmappedFields:       m.state.totals.unmapped,
		Classes:              classMap(m.state.totals.classes),
		Operations:           operations,
		AvgRequestLatencyMs:  avgReqMs,
		AvgUpstreamLatencyMs: avgUpstreamMs,
		AvgToolLatencyMs:     avgToolMs,
		ModelRetries:         m.state.modelRetries,
		Failovers:            m.state.failoverCount,
		ActiveAgents:         m.state.activeAgents,
		ToolErrors:           m.state.toolErrors,
		MCPErrors:            m.state.mcpErrors,
		CacheHits:            m.state.cacheHits,
		CacheMisses:          m.state.cacheMisses,
		CacheHitRatio:        hitRatio,
		QuotaFailures:        m.state.quotaFailures,
		SandboxFailures:      m.state.sandboxFailures,
	}
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
