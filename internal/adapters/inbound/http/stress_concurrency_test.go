package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adaptersHTTP "dezuxk-gateway/internal/adapters/inbound/http"
	"dezuxk-gateway/internal/core/domain"
)

// TestStress_100ConcurrentChatRequests verifies 100 parallel chat requests execute without panic, deadlock or corruption
func TestStress_100ConcurrentChatRequests(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	stop := "stop"
	mockCU := &mockChatUseCase{
		syncResp: &domain.OpenAIChatResponse{
			ID:      "chatcmpl-stress-100",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "gemini-3.8-flash",
			Choices: []domain.OpenAIChoice{
				{
					Index: 0,
					Message: domain.OpenAIMessage{
						Role:    "assistant",
						Content: "Concurrent stress response payload.",
					},
					FinishReason: &stop,
				},
			},
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	const total = 100
	var wg sync.WaitGroup
	var successCount int64
	var errCount int64

	wg.Add(total)
	for i := 0; i < total; i++ {
		go func(idx int) {
			defer wg.Done()
			reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Req %d"}]}`, idx)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleChatCompletions(rec, req)

			if rec.Code == http.StatusOK {
				var resp domain.OpenAIChatResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil && len(resp.Choices) > 0 {
					atomic.AddInt64(&successCount, 1)
					return
				}
			}
			atomic.AddInt64(&errCount, 1)
		}(i)
	}

	wg.Wait()

	if successCount != total {
		t.Fatalf("expected %d successful responses, got %d (errors: %d)", total, successCount, errCount)
	}
}

// TestStress_20ConcurrentStreams verifies 20 parallel SSE stream requests
func TestStress_20ConcurrentStreams(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	mockCU := &mockChatUseCase{
		onStreamDo: func(ctx context.Context, w io.Writer, flusher func()) error {
			for i := 0; i < 5; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				chunk := fmt.Sprintf(`data: {"id":"chatcmpl-stream","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" chunk-%d"},"finish_reason":null}]}`+"\n\n", i)
				_, _ = w.Write([]byte(chunk))
				flusher()
				time.Sleep(2 * time.Millisecond)
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			flusher()
			return nil
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	const total = 20
	var wg sync.WaitGroup
	var completedCount int64

	wg.Add(total)
	for i := 0; i < total; i++ {
		go func(idx int) {
			defer wg.Done()
			reqBody := fmt.Sprintf(`{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Stream %d"}], "stream": true}`, idx)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleChatCompletions(rec, req)

			if rec.Code == http.StatusOK && rec.Body.Len() > 0 {
				atomic.AddInt64(&completedCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if completedCount != total {
		t.Fatalf("expected %d completed streams, got %d", total, completedCount)
	}
}

// TestStress_ClientDisconnect verifies client disconnection during stream does not panic or leak
func TestStress_ClientDisconnect(t *testing.T) {
	mr := domain.NewModelRegistry(domain.GetGeminiCatalog())
	metrics := domain.NewContractMetrics()

	mockCU := &mockChatUseCase{
		onStreamDo: func(ctx context.Context, w io.Writer, flusher func()) error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				_, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ping\"}}]}\n\n"))
				if err != nil {
					return err
				}
				flusher()
				time.Sleep(5 * time.Millisecond)
			}
		},
	}

	handler := adaptersHTTP.NewChatHandler(mockCU, mr, metrics)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"test"}],"stream":true}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Cancel context after 15ms to simulate client disconnecting mid-stream
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	// Must terminate cleanly without panic
	handler.HandleChatCompletions(rec, req)
}
