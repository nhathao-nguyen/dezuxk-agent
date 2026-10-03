package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"
)

// loadChatUseCase đóng vai trò Mock Upstream Gemini có khả năng giả lập Chaos có kiểm soát
type loadChatUseCase struct {
	requestCount       int64
	simulated429       int64
	simulated503       int64
	simulatedSlow      int64
	simulatedSuccess   int64
	disconnectObserved int64
	slowDelay          time.Duration
}

func (m *loadChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	idx := atomic.AddInt64(&m.requestCount, 1)

	// Phân bổ bucket rời rạc bằng modulo rõ ràng (Deterministic Bucket), chống đè điều kiện sai
	// 0–1   (2%) => 429 Too Many Requests
	// 2     (1%) => 503 Service Unavailable
	// 3–6   (4%) => Slow upstream (chậm nhưng thành công hoặc bị timeout)
	// 7–99 (93%) => Success tức thì
	bucket := idx % 100

	switch {
	case bucket < 2:
		atomic.AddInt64(&m.simulated429, 1)
		return nil, domain.ClassifyUpstreamStatus(domain.OpChatCompletions, "Gemini", 429, true, domain.ServiceGemini)

	case bucket == 2:
		atomic.AddInt64(&m.simulated503, 1)
		return nil, domain.ClassifyUpstreamStatus(domain.OpChatCompletions, "Gemini", 503, true, domain.ServiceGemini)

	case bucket >= 3 && bucket <= 6:
		atomic.AddInt64(&m.simulatedSlow, 1)
		if m.slowDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(m.slowDelay):
			}
		}
		atomic.AddInt64(&m.simulatedSuccess, 1)

	default:
		atomic.AddInt64(&m.simulatedSuccess, 1)
	}

	stop := "stop"
	return &domain.OpenAIChatResponse{
		ID:      fmt.Sprintf("chatcmpl-load-%d", idx),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []domain.OpenAIChoice{
			{
				Index: 0,
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: fmt.Sprintf("Response for request #%d", idx),
				},
				FinishReason: &stop,
			},
		},
		Usage: &domain.OpenAIUsage{
			PromptTokens:     15,
			CompletionTokens: 10,
			TotalTokens:      25,
		},
	}, nil
}

func (m *loadChatUseCase) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, streamWriter io.Writer, flusher func()) error {
	idx := atomic.AddInt64(&m.requestCount, 1)

	// Stream 5 chunks với delay nhỏ để client có thời gian nhận và kiểm thử ngắt kết nối
	for c := 0; c < 5; c++ {
		select {
		case <-ctx.Done():
			atomic.AddInt64(&m.disconnectObserved, 1)
			return ctx.Err()
		default:
		}

		chunk := fmt.Sprintf("data: {\"id\":\"chatcmpl-stream-%d\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" token-%d\"},\"finish_reason\":null}]}\n\n", idx, c)
		if _, err := streamWriter.Write([]byte(chunk)); err != nil {
			atomic.AddInt64(&m.disconnectObserved, 1)
			return err
		}
		if flusher != nil {
			flusher()
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-ctx.Done():
		atomic.AddInt64(&m.disconnectObserved, 1)
		return ctx.Err()
	default:
	}

	_, _ = streamWriter.Write([]byte("data: [DONE]\n\n"))
	if flusher != nil {
		flusher()
	}
	atomic.AddInt64(&m.simulatedSuccess, 1)
	return nil
}

// BoundedLatencySampler lưu trữ mẫu độ trễ có giới hạn dung lượng (Ring Buffer), chống OOM khi chạy Soak Test dài
type BoundedLatencySampler struct {
	mu      sync.Mutex
	samples []time.Duration
	maxSize int
	head    int
	count   int
}

func NewBoundedLatencySampler(maxSize int) *BoundedLatencySampler {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &BoundedLatencySampler{
		samples: make([]time.Duration, maxSize),
		maxSize: maxSize,
	}
}

func (s *BoundedLatencySampler) Add(d time.Duration) {
	s.mu.Lock()
	s.samples[s.head] = d
	s.head = (s.head + 1) % s.maxSize
	if s.count < s.maxSize {
		s.count++
	}
	s.mu.Unlock()
}

func (s *BoundedLatencySampler) Percentiles() (p50, p95, p99 time.Duration) {
	s.mu.Lock()
	if s.count == 0 {
		s.mu.Unlock()
		return 0, 0, 0
	}
	copied := make([]time.Duration, s.count)
	copy(copied, s.samples[:s.count])
	s.mu.Unlock()

	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	n := len(copied)
	p50 = copied[n*50/100]
	p95 = copied[n*95/100]
	p99 = copied[n*99/100]
	return p50, p95, p99
}

func main() {
	chatConc := flag.Int("chat-concurrency", 500, "Number of concurrent chat completion requests")
	streamConc := flag.Int("stream-concurrency", 50, "Number of concurrent SSE streaming requests")
	agentBurst := flag.Int("agent-burst", 50, "Number of background agent runs submitted in burst")
	soakSecs := flag.Int("soak-seconds", 0, "Soak duration in seconds (0 for single burst)")
	maxErrorRate := flag.Float64("max-error-rate", 0.05, "Maximum allowable unexpected error rate (0.05 = 5%)")
	flag.Parse()

	log.Println("=================================================================")
	log.Println("     DEZUXK AGENT GATEWAY - TIER B PRODUCTION LOAD & SOAK TEST   ")
	log.Println("=================================================================")
	log.Printf("[Config] Chat Concurrency: %d | Stream Concurrency: %d | Agent Burst: %d | Soak: %ds",
		*chatConc, *streamConc, *agentBurst, *soakSecs)

	const testAPIKey = "tier-b-loadtest-secret-key"

	// 1. Khởi tạo Mock Upstream & Gateway Dependencies
	mockCU := &loadChatUseCase{
		slowDelay: 10 * time.Millisecond,
	}
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	agentRunRepo := session.NewMemoryAgentRunRepository()
	checkpointRepo := session.NewMemoryCheckpointRepository()
	toolRegistry := tools.NewToolRegistry()
	agentRunner := agent.NewRunner(mockCU, toolRegistry, nil)
	agentRunner.SetCheckpointRepository(checkpointRepo)
	jobService := agent.NewJobService(agentRunRepo, agentRunner)
	jobService.SetCheckpointRepository(checkpointRepo)

	jobCtx, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()
	_ = jobService.Start(jobCtx)

	readiness := adaptersHTTP.NewReadinessManager()
	readiness.SetReady(true)

	// Cấu hình router hoàn chỉnh đi qua toàn bộ HTTP Pipeline (Tier-B End-to-End)
	// Rate limit được cấu hình đủ rộng cho pha tải bình thường để không chặn nhầm
	gatewayCfg := &config.Config{
		Server: config.ServerConfig{
			APIKey:           testAPIKey,
			AllowedOrigins:   []string{"*"},
			EnableRequestLog: false,
			RateLimit: config.RateLimitConfig{
				MaxRequests:   200000,
				WindowSeconds: 60,
			},
		},
	}

	router := adaptersHTTP.BuildRouter(adaptersHTTP.RouterDependencies{
		Config:           gatewayCfg,
		ChatUseCase:      mockCU,
		ModelRegistry:    mr,
		Metrics:          metrics,
		AgentRunner:      agentRunner,
		ToolRegistry:     toolRegistry,
		CheckpointRepo:   checkpointRepo,
		AgentJobService:  jobService,
		AgentRunRepo:     agentRunRepo,
		ReadinessManager: readiness,
	})

	// Khởi chạy HTTP Server thực tế qua httptest.Server
	ts := httptest.NewServer(router)
	defer ts.Close()

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 500,
			IdleConnTimeout:     60 * time.Second,
		},
	}

	// 2. Warm-up Phase: Làm ấm Router, Go runtime và Workers trước khi lấy Baseline
	log.Println("[Tier B] Đang thực hiện warm-up trước khi lấy baseline tài nguyên...")
	for i := 0; i < 20; i++ {
		reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "warmup %d"}]}`, i)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewBufferString(reqBody))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		if resp, err := httpClient.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	time.Sleep(150 * time.Millisecond)

	baselineGoroutines := runtime.NumGoroutine()
	var mBaseline runtime.MemStats
	runtime.ReadMemStats(&mBaseline)
	peakGoroutines := baselineGoroutines

	log.Printf("[Baseline] Goroutines: %d | HeapAlloc: %.2fMB | HeapObjects: %d",
		baselineGoroutines, float64(mBaseline.Alloc)/(1024*1024), mBaseline.HeapObjects)

	sampler := NewBoundedLatencySampler(10000)

	var cumulativeRequests int64
	var cumulativeSuccess int64
	var cumulativeChaos429 int64
	var cumulativeChaos503 int64
	var cumulativeUnexpectedErr int64

	// Hàm thực thi một chu kỳ tải đầy đủ (Load Cycle)
	executeCycle := func(cycleIdx int, chatN, streamN int) {
		var wg sync.WaitGroup

		// 2.1 Chat Completions (JSON POST qua /v1/chat/completions)
		for i := 0; i < chatN; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				atomic.AddInt64(&cumulativeRequests, 1)

				start := time.Now()
				reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Load prompt c%d-#%d"}]}`, cycleIdx, idx)
				req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewBufferString(reqBody))
				if err != nil {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					return
				}
				req.Header.Set("Authorization", "Bearer "+testAPIKey)
				req.Header.Set("Content-Type", "application/json")

				resp, err := httpClient.Do(req)
				dur := time.Since(start)
				sampler.Add(dur)

				if err != nil {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					return
				}
				defer resp.Body.Close()
				bodyBytes, _ := io.ReadAll(resp.Body)

				switch resp.StatusCode {
				case http.StatusOK:
					var chatResp domain.OpenAIChatResponse
					if json.Unmarshal(bodyBytes, &chatResp) == nil && len(chatResp.Choices) > 0 {
						atomic.AddInt64(&cumulativeSuccess, 1)
					} else {
						atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					}
				case http.StatusTooManyRequests:
					// Upstream 429 chaos được phân loại đúng là chaos dự kiến
					atomic.AddInt64(&cumulativeChaos429, 1)
				case http.StatusServiceUnavailable, http.StatusBadGateway:
					// Upstream 503 chaos được phân loại đúng là chaos dự kiến
					atomic.AddInt64(&cumulativeChaos503, 1)
				default:
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
				}
			}(i)
		}

		// 2.2 /v1/responses endpoint verification (OpenAI Responses contract)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				atomic.AddInt64(&cumulativeRequests, 1)

				reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "input": "Responses test #%d"}`, idx)
				req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/responses", bytes.NewBufferString(reqBody))
				if err != nil {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					return
				}
				req.Header.Set("Authorization", "Bearer "+testAPIKey)
				req.Header.Set("Content-Type", "application/json")

				resp, err := httpClient.Do(req)
				if err != nil {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					return
				}
				defer resp.Body.Close()
				_, _ = io.Copy(io.Discard, resp.Body)

				if resp.StatusCode == http.StatusOK {
					atomic.AddInt64(&cumulativeSuccess, 1)
				} else if resp.StatusCode == http.StatusTooManyRequests {
					atomic.AddInt64(&cumulativeChaos429, 1)
				} else {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
				}
			}(i)
		}

		// 2.3 Concurrent SSE Streaming Connections (/v1/chat/completions với stream: true)
		for i := 0; i < streamN; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				atomic.AddInt64(&cumulativeRequests, 1)

				reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Stream c%d-#%d"}], "stream": true}`, cycleIdx, idx)
				req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewBufferString(reqBody))
				if err != nil {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					return
				}
				req.Header.Set("Authorization", "Bearer "+testAPIKey)
				req.Header.Set("Content-Type", "application/json")

				resp, err := httpClient.Do(req)
				if err != nil {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode == http.StatusOK {
					scanner := bufio.NewScanner(resp.Body)
					hasDone := false
					for scanner.Scan() {
						line := scanner.Text()
						if strings.Contains(line, "[DONE]") {
							hasDone = true
							break
						}
					}
					if hasDone {
						atomic.AddInt64(&cumulativeSuccess, 1)
					} else {
						atomic.AddInt64(&cumulativeUnexpectedErr, 1)
					}
				} else if resp.StatusCode == http.StatusTooManyRequests {
					atomic.AddInt64(&cumulativeChaos429, 1)
				} else {
					atomic.AddInt64(&cumulativeUnexpectedErr, 1)
				}
			}(i)
		}

		wg.Wait()

		curGoroutines := runtime.NumGoroutine()
		if curGoroutines > peakGoroutines {
			peakGoroutines = curGoroutines
		}
	}

	// 3. Thực thi Soak Mode hoặc Single Burst Mode
	if *soakSecs > 0 {
		log.Printf("[Soak Mode] Bắt đầu chạy vòng lặp ngâm tải liên tục trong %d giây...", *soakSecs)
		deadline := time.Now().Add(time.Duration(*soakSecs) * time.Second)
		cycle := 0
		for time.Now().Before(deadline) {
			cycle++
			// Mỗi chu kỳ chạy tải với tỷ lệ phù hợp để ngâm ổn định
			cycleChat := *chatConc / 5
			if cycleChat < 20 {
				cycleChat = 20
			}
			cycleStream := *streamConc / 5
			if cycleStream < 5 {
				cycleStream = 5
			}

			executeCycle(cycle, cycleChat, cycleStream)
			time.Sleep(50 * time.Millisecond)

			if cycle%5 == 0 {
				p50, p95, p99 := sampler.Percentiles()
				log.Printf("[Soak Telemetry] Chu kỳ #%d | Tổng Reqs: %d | Thành công: %d | 429: %d | 503: %d | Lỗi bất ngờ: %d | P50: %v | P95: %v | P99: %v",
					cycle, atomic.LoadInt64(&cumulativeRequests), atomic.LoadInt64(&cumulativeSuccess),
					atomic.LoadInt64(&cumulativeChaos429), atomic.LoadInt64(&cumulativeChaos503),
					atomic.LoadInt64(&cumulativeUnexpectedErr), p50, p95, p99)
			}
		}
		log.Printf("[Soak Mode] Hoàn tất thời gian ngâm tải %d giây qua %d chu kỳ.", *soakSecs, cycle)
	} else {
		log.Printf("[Burst Mode] Bắt đầu tải bùng phát đơn: %d Chat, %d Stream...", *chatConc, *streamConc)
		startBurst := time.Now()
		executeCycle(1, *chatConc, *streamConc)
		log.Printf("[Burst Mode] Hoàn tất bùng phát trong %v", time.Since(startBurst))
	}

	// 4. Client Disconnect Simulation (Realistic Multi-chunk Stream Cancel)
	log.Println("[Tier B] Kiểm thử mô phỏng client chủ động ngắt kết nối giữa luồng stream (Client Disconnects)...")
	var disconnectWg sync.WaitGroup
	for i := 0; i < 20; i++ {
		disconnectWg.Add(1)
		go func(idx int) {
			defer disconnectWg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "disconnect %d"}], "stream": true}`, idx)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewBufferString(reqBody))
			if err != nil {
				return
			}
			req.Header.Set("Authorization", "Bearer "+testAPIKey)
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			// Đọc 1 chunk đầu tiên rồi ngay lập tức hủy context để giả lập client đóng kết nối
			buf := make([]byte, 64)
			_, _ = resp.Body.Read(buf)
			cancel()
		}(i)
	}
	disconnectWg.Wait()
	time.Sleep(100 * time.Millisecond)

	obsDisconnects := atomic.LoadInt64(&mockCU.disconnectObserved)
	log.Printf("[Disconnect Telemetry] Upstream ghi nhận %d lần ngắt kết nối giữa chừng (xử lý an toàn, không panic).", obsDisconnects)

	// 5. Dedicated Rate Limiting Rejection Phase (Phân biệt rõ ràng Rejection có chủ đích vs Lỗi hệ thống)
	log.Println("[Tier B] Kiểm thử cơ chế Rate Limiter từ chối yêu cầu (Dedicated 429 Rejection Phase)...")
	rateLimitCfg := &config.Config{
		Server: config.ServerConfig{
			APIKey:           testAPIKey,
			AllowedOrigins:   []string{"*"},
			EnableRequestLog: false,
			RateLimit: config.RateLimitConfig{
				MaxRequests:   5, // Hạ thấp giới hạn xuống 5 req / 10s để kiểm chứng chặn 429
				WindowSeconds: 10,
			},
		},
	}
	strictLimiterRouter := adaptersHTTP.BuildRouter(adaptersHTTP.RouterDependencies{
		Config:           rateLimitCfg,
		ChatUseCase:      mockCU,
		ModelRegistry:    mr,
		Metrics:          metrics,
		ReadinessManager: readiness,
	})
	strictTS := httptest.NewServer(strictLimiterRouter)
	defer strictTS.Close()

	var expectedRateLimits int64
	var strictSuccess int64
	for i := 0; i < 15; i++ {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, strictTS.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"test"}]}`))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusTooManyRequests {
				atomic.AddInt64(&expectedRateLimits, 1)
			} else if resp.StatusCode == http.StatusOK {
				atomic.AddInt64(&strictSuccess, 1)
			}
			_ = resp.Body.Close()
		}
	}
	log.Printf("[RateLimit Phase] Đã gửi 15 yêu cầu tới limiter ngặt nghèo: %d thành công, %d bị từ chối 429 đúng như mong đợi.",
		strictSuccess, expectedRateLimits)
	strictTS.Close()
	if expectedRateLimits == 0 {
		log.Fatalf("FAIL: Rate limiter không chặn 429 khi vượt ngưỡng MaxRequests!")
	}

	// 6. Agent Load Test & Multi-tenant Idempotency Verification
	log.Printf("[Tier B] Bắt đầu burst %d tác vụ Agent chạy nền và đối soát idempotency key đa tenant...", *agentBurst)
	tempWorkspace, err := os.MkdirTemp("", "agent_loadtest_*")
	if err == nil {
		defer os.RemoveAll(tempWorkspace)
	}

	var agentWg sync.WaitGroup
	for i := 0; i < *agentBurst; i++ {
		agentWg.Add(1)
		go func(idx int) {
			defer agentWg.Done()
			tenant := "tenant-a"
			if idx%2 == 1 {
				tenant = "tenant-b"
			}
			ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
				TenantID: tenant,
				Role:     "user",
				Scopes:   []string{domain.ScopeAgent},
			})
			idempKey := fmt.Sprintf("idemp-key-%d", idx%10) // 10 unique keys per tenant
			_, _ = jobService.SubmitRun(ctx, fmt.Sprintf("Goal #%d", idx), domain.AgentRunOptions{
				Workspace: tempWorkspace,
			}, idempKey)
		}(i)
	}
	agentWg.Wait()

	// Verify idempotency dedup: at most 20 distinct runs across 2 tenants (10 keys each)
	allRuns, _ := agentRunRepo.List(context.Background(), "", 100, 0)
	log.Printf("[Agent Summary] Đã gửi %d requests. Số runs độc bản thực tế: %d (Idempotency deduplication: thành công)",
		*agentBurst, len(allRuns))
	if len(allRuns) > 20 {
		log.Fatalf("FAIL: Idempotency violation: expected <= 20 runs for 10 unique keys over 2 tenants, got %d", len(allRuns))
	}

	// 7. Dọn dẹp tài nguyên & Đo lường rò rỉ (Resource Leak Settlement)
	log.Println("[Tier B] Đang tắt các dịch vụ nền và đo lường ổn định tài nguyên (Resource Leak Check)...")
	jobCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = jobService.Shutdown(shutdownCtx)
	shutdownCancel()

	httpClient.CloseIdleConnections()
	ts.CloseClientConnections()
	ts.Close()

	time.Sleep(300 * time.Millisecond)
	runtime.GC()
	time.Sleep(150 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	var mFinal runtime.MemStats
	runtime.ReadMemStats(&mFinal)

	goroutineDiff := finalGoroutines - baselineGoroutines
	allocMB := float64(mFinal.Alloc) / (1024 * 1024)
	baselineAllocMB := float64(mBaseline.Alloc) / (1024 * 1024)

	p50, p95, p99 := sampler.Percentiles()

	log.Println("-----------------------------------------------------------------")
	log.Printf("[Final Telemetry] Tổng Reqs: %d | Thành công: %d | 429 Chaos: %d | 503 Chaos: %d | Lỗi bất ngờ: %d",
		atomic.LoadInt64(&cumulativeRequests), atomic.LoadInt64(&cumulativeSuccess),
		atomic.LoadInt64(&cumulativeChaos429), atomic.LoadInt64(&cumulativeChaos503),
		atomic.LoadInt64(&cumulativeUnexpectedErr))
	log.Printf("[Latency Telemetry] P50: %v | P95: %v | P99: %v", p50, p95, p99)
	log.Printf("[Resource Telemetry] Goroutines: Baseline=%d -> Peak=%d -> Final=%d (Diff=%d)",
		baselineGoroutines, peakGoroutines, finalGoroutines, goroutineDiff)
	log.Printf("[Resource Telemetry] Memory Heap: Baseline=%.2fMB -> Final=%.2fMB (Objects: %d -> %d)",
		baselineAllocMB, allocMB, mBaseline.HeapObjects, mFinal.HeapObjects)
	log.Printf("[Chaos Telemetry] Upstream 429=%d | 503=%d | Slow=%d | Success=%d | Disconnects=%d",
		atomic.LoadInt64(&mockCU.simulated429), atomic.LoadInt64(&mockCU.simulated503),
		atomic.LoadInt64(&mockCU.simulatedSlow), atomic.LoadInt64(&mockCU.simulatedSuccess),
		obsDisconnects)
	log.Println("-----------------------------------------------------------------")

	// 7. Assertions nghiêm ngặt theo Acceptance Criteria
	// 7.1 Kiểm chứng cả 429, 503, slow, success đều được kích hoạt
	if atomic.LoadInt64(&mockCU.simulated429) == 0 {
		log.Fatalf("FAIL: Chaos upstream 429 chưa bao giờ được exercise!")
	}
	if atomic.LoadInt64(&mockCU.simulated503) == 0 {
		log.Fatalf("FAIL: Chaos upstream 503 chưa bao giờ được exercise!")
	}
	if atomic.LoadInt64(&mockCU.simulatedSlow) == 0 {
		log.Fatalf("FAIL: Chaos upstream slow delay chưa bao giờ được exercise!")
	}
	if atomic.LoadInt64(&mockCU.simulatedSuccess) == 0 {
		log.Fatalf("FAIL: Upstream success chưa bao giờ được exercise!")
	}

	// 7.2 Tỷ lệ lỗi bất ngờ không vượt quá ngưỡng cho phép
	totReqs := atomic.LoadInt64(&cumulativeRequests)
	unexpErr := atomic.LoadInt64(&cumulativeUnexpectedErr)
	unexpectedErrRate := float64(unexpErr) / float64(totReqs)
	if unexpectedErrRate > *maxErrorRate {
		log.Fatalf("FAIL: Unexpected error rate %.2f%% vượt ngưỡng cho phép (%.2f%%)", unexpectedErrRate*100, *maxErrorRate*100)
	}

	// 7.3 Rò rỉ Goroutines sau khi kết thúc và GC lắng đọng không vượt quá 35
	if goroutineDiff > 35 {
		log.Fatalf("FAIL: Phát hiện rò rỉ Goroutine: Goroutine count tăng %d sau khi dọn dẹp!", goroutineDiff)
	}

	log.Println("=================================================================")
	log.Println("     KẾT QUẢ TIER B LOAD & SOAK TEST: TOÀN BỘ ĐÃ VƯỢT QUA (PASS) ")
	log.Println("=================================================================")
}
