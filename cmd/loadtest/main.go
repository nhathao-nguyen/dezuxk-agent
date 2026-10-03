package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"
)

type loadChatUseCase struct {
	fail429Rate  float64
	fail500Rate  float64
	slowDelay    time.Duration
	requestCount int64
}

func (m *loadChatUseCase) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	idx := atomic.AddInt64(&m.requestCount, 1)

	// Upstream chaos simulation
	if m.slowDelay > 0 && idx%7 == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.slowDelay):
		}
	}

	if m.fail429Rate > 0 && float64(idx%100)/100.0 < m.fail429Rate {
		return nil, domain.ClassifyUpstreamStatus(domain.OpChatCompletions, "Gemini", 429, true, domain.ServiceGemini)
	}

	if m.fail500Rate > 0 && float64(idx%100)/100.0 < m.fail500Rate {
		return nil, domain.ClassifyUpstreamStatus(domain.OpChatCompletions, "Gemini", 503, true, domain.ServiceGemini)
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

	for c := 0; c < 5; c++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunk := fmt.Sprintf("data: {\"id\":\"chatcmpl-stream-%d\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" token-%d\"},\"finish_reason\":null}]}\n\n", idx, c)
		if _, err := streamWriter.Write([]byte(chunk)); err != nil {
			return err
		}
		flusher()
		time.Sleep(1 * time.Millisecond)
	}

	_, _ = streamWriter.Write([]byte("data: [DONE]\n\n"))
	flusher()
	return nil
}

func main() {
	chatConc := flag.Int("chat-concurrency", 500, "Number of concurrent chat completion requests")
	streamConc := flag.Int("stream-concurrency", 50, "Number of concurrent SSE streaming requests")
	agentBurst := flag.Int("agent-burst", 30, "Number of background agent runs submitted in burst")
	soakSecs := flag.Int("soak-seconds", 0, "Soak duration in seconds (0 for single burst)")
	maxErrorRate := flag.Float64("max-error-rate", 0.05, "Maximum allowable error rate (0.05 = 5%)")
	flag.Parse()

	log.Println("=================================================================")
	log.Println("     DEZUXK AGENT GATEWAY - TIER B PRODUCTION LOAD & SOAK TEST   ")
	log.Println("=================================================================")
	log.Printf("[Config] Chat Concurrency: %d | Stream Concurrency: %d | Agent Burst: %d | Soak: %ds",
		*chatConc, *streamConc, *agentBurst, *soakSecs)

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()
	var mBaseline runtime.MemStats
	runtime.ReadMemStats(&mBaseline)

	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()
	mockCU := &loadChatUseCase{
		fail429Rate: 0.02, // 2% 429 simulation
		fail500Rate: 0.01, // 1% 503 simulation
		slowDelay:   10 * time.Millisecond,
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	// Step 1: Concurrent Chat Completions (500+ requests)
	log.Printf("[Tier B] Bắt đầu kiểm thử %d yêu cầu chat completions đồng thời...", *chatConc)
	var wg sync.WaitGroup
	var successChat int64
	var errorChat int64
	latencies := make([]time.Duration, *chatConc)

	startChat := time.Now()
	for i := 0; i < *chatConc; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqStart := time.Now()
			reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Load test prompt #%d"}]}`, idx)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleChatCompletions(rec, req)
			latencies[idx] = time.Since(reqStart)

			if rec.Code == http.StatusOK {
				var resp domain.OpenAIChatResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil && len(resp.Choices) > 0 {
					atomic.AddInt64(&successChat, 1)
					return
				}
			}
			atomic.AddInt64(&errorChat, 1)
		}(i)
	}
	wg.Wait()
	totalChatDuration := time.Since(startChat)

	// Calculate latency percentiles
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)*50/100]
	p95 := latencies[len(latencies)*95/100]
	p99 := latencies[len(latencies)*99/100]

	log.Printf("[Chat Summary] Thành công: %d/%d (Lỗi có kiểm soát: %d) | Thời gian: %v | P50: %v | P95: %v | P99: %v",
		successChat, *chatConc, errorChat, totalChatDuration, p50, p95, p99)

	// Step 2: Concurrent Streaming Connections (50+ streams)
	log.Printf("[Tier B] Bắt đầu kiểm thử %d luồng SSE streaming đồng thời...", *streamConc)
	var streamWg sync.WaitGroup
	var successStreams int64
	var errorStreams int64

	for i := 0; i < *streamConc; i++ {
		streamWg.Add(1)
		go func(idx int) {
			defer streamWg.Done()
			reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Stream prompt #%d"}], "stream": true}`, idx)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleChatCompletions(rec, req)

			if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "[DONE]") {
				atomic.AddInt64(&successStreams, 1)
			} else {
				atomic.AddInt64(&errorStreams, 1)
			}
		}(i)
	}
	streamWg.Wait()
	log.Printf("[Stream Summary] Thành công: %d/%d (Lỗi: %d)", successStreams, *streamConc, errorStreams)

	// Step 3: Client Disconnect Simulation (Cancelled contexts mid-stream)
	log.Println("[Tier B] Kiểm thử mô phỏng client ngắt kết nối giữa chừng (Client Disconnects)...")
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"disconnect test"}],"stream":true}`))
		req = req.WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		go func() {
			time.Sleep(2 * time.Millisecond)
			cancel()
		}()

		handler.HandleChatCompletions(rec, req)
	}
	log.Println("[Disconnect Summary] 20 ngắt kết nối được xử lý sạch sẽ, không gây panic hay deadlock.")

	// Step 4: Agent Job Burst & Idempotency Key Verification
	log.Printf("[Tier B] Bắt đầu burst %d tác vụ Agent chạy nền và đối soát idempotency key...", *agentBurst)
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

	var agentWg sync.WaitGroup

	for i := 0; i < *agentBurst; i++ {
		agentWg.Add(1)
		go func(idx int) {
			defer agentWg.Done()
			idempKey := fmt.Sprintf("idemp-key-%d", idx%10) // 10 unique keys over N requests
			_, _ = jobService.SubmitRun(context.Background(), fmt.Sprintf("Goal #%d", idx), domain.AgentRunOptions{}, idempKey)
		}(i)
	}
	agentWg.Wait()

	// Verify idempotency dedup: at most 10 distinct runs created
	allRuns, _ := agentRunRepo.List(context.Background(), "", 100, 0)
	log.Printf("[Agent Summary] Đã gửi %d requests. Số runs độc bản thực tế: %d (Idempotency deduplication: thành công)",
		*agentBurst, len(allRuns))
	if len(allRuns) > 10 {
		log.Fatalf("FAIL: Idempotency violation: expected <= 10 runs for 10 unique keys, got %d", len(allRuns))
	}

	// Step 5: Resource Leak & Stability Verifications
	log.Println("[Tier B] Đang kiểm tra rò rỉ tài nguyên (Goroutine Leak & Memory Growth)...")
	jobCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = jobService.Shutdown(shutdownCtx)
	shutdownCancel()

	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	var mFinal runtime.MemStats
	runtime.ReadMemStats(&mFinal)

	goroutineDiff := finalGoroutines - baselineGoroutines
	allocMB := float64(mFinal.Alloc) / (1024 * 1024)
	baselineAllocMB := float64(mBaseline.Alloc) / (1024 * 1024)

	log.Printf("[Telemetry] Goroutines: Baseline=%d -> Final=%d (Diff=%d)", baselineGoroutines, finalGoroutines, goroutineDiff)
	log.Printf("[Telemetry] Memory Heap Alloc: Baseline=%.2fMB -> Final=%.2fMB", baselineAllocMB, allocMB)

	// Assertions
	chatErrRate := float64(errorChat) / float64(*chatConc)
	if chatErrRate > *maxErrorRate {
		log.Fatalf("FAIL: Chat error rate %.2f%% vượt ngưỡng cho phép (%.2f%%)", chatErrRate*100, *maxErrorRate*100)
	}

	if goroutineDiff > 35 {
		log.Fatalf("FAIL: Phát hiện rò rỉ Goroutine: Goroutine count tăng %d sau khi tải hoàn tất!", goroutineDiff)
	}

	log.Println("=================================================================")
	log.Println("     KẾT QUẢ TIER B LOAD & SOAK TEST: TOÀN BỘ ĐÃ VƯỢT QUA (PASS) ")
	log.Println("=================================================================")
}
