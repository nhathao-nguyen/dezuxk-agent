package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
)

type ChatHandler struct {
	chatUseCase   ports.ChatUseCase
	modelRegistry *domain.ModelRegistry
	metrics       *domain.ContractMetrics
	cache         *services.ResponseCache
}

func NewChatHandler(cu ports.ChatUseCase, mr *domain.ModelRegistry, metrics *domain.ContractMetrics) *ChatHandler {
	return &ChatHandler{
		chatUseCase:   cu,
		modelRegistry: mr,
		metrics:       metrics,
	}
}

func (h *ChatHandler) SetCache(c *services.ResponseCache) {
	h.cache = c
}

func (h *ChatHandler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if h.modelRegistry.Count() == 0 {
		writeChatError(w, r, h.metrics, domain.Unauthenticated(domain.OpChatCompletions, "", domain.ServiceGemini, "chưa có phiên Gemini sẵn sàng").WithPublicStatus(http.StatusServiceUnavailable), false)
		return
	}

	var req domain.OpenAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeChatError(w, r, h.metrics, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "hội thoại không đọc được"), false)
		return
	}
	if len(req.Messages) == 0 {
		writeChatError(w, r, h.metrics, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "thiếu danh sách tin nhắn"), false)
		return
	}
	// Hỗ trợ truyền conversation_id qua Request Header nếu chưa có trong body
	if req.ConversationID == "" {
		if cID := r.Header.Get("X-Conversation-Id"); cID != "" {
			req.ConversationID = cID
		} else if cID := r.Header.Get("X-Session-Id"); cID != "" {
			req.ConversationID = cID
		}
	}

	desc, ok := h.modelRegistry.ResolveGeminiModel(req.Model)
	if !ok {
		writeChatError(w, r, h.metrics, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "chưa có mô hình Gemini khả dụng"), false)
		return
	}
	req.Model = desc.ID

	// Kiểm tra xem yêu cầu có thể cache an toàn không (Rule 4.3: Không cache tools, stateful, multimodal)
	isCacheable := !req.Stream && h.cache != nil && h.cache.SupportsMethod("chat")
	if isCacheable {
		if len(req.Tools) > 0 || req.ToolChoice != nil {
			isCacheable = false
		} else {
			for _, m := range req.Messages {
				if len(m.ToolCalls) > 0 || strings.EqualFold(m.Role, "tool") {
					isCacheable = false
					break
				}
				for _, p := range m.ContentParts {
					if p.Type == "image_url" || p.ImageURL != nil || p.Type == "input_audio" {
						isCacheable = false
						break
					}
				}
				if !isCacheable {
					break
				}
			}
		}
	}

	var cacheKey string
	if isCacheable {
		var sysPrompt string
		for _, m := range req.Messages {
			if strings.EqualFold(m.Role, "system") {
				sysPrompt = m.Content
				break
			}
		}
		isThinking := false
		if req.Thinking != nil {
			isThinking = *req.Thinking
		} else if strings.Contains(strings.ToLower(req.Model), "thinking") {
			isThinking = true
		}
		isGrounding := false
		if req.SearchGrounding != nil {
			isGrounding = *req.SearchGrounding
		}
		isCodeInterpreter := false
		if req.CodeInterpreter != nil {
			isCodeInterpreter = *req.CodeInterpreter
		}

		tenantID := "default"
		keyID := "default"
		if id, ok := domain.TenantIdentityFromContext(r.Context()); ok {
			tenantID = id.TenantID
			keyID = id.KeyID
		}

		cacheKey = services.GenerateIsolatedChatCacheKey(services.IsolatedChatCacheKeyParams{
			TenantID:        tenantID,
			KeyID:           keyID,
			Model:           req.Model,
			Messages:        req.Messages,
			Temperature:     req.Temperature,
			SystemPrompt:    sysPrompt,
			Thinking:        isThinking,
			Grounding:       isGrounding,
			CodeInterpreter: isCodeInterpreter,
			ResponseFormat:  req.ResponseFormat,
		})
		if entry, hit := h.cache.Get(cacheKey); hit {
			w.Header().Set("Content-Type", entry.ContentType)
			w.Header().Set("X-Cache", "HIT")
			remaining := int(time.Until(entry.ExpiresAt).Seconds())
			if remaining > 0 {
				w.Header().Set("X-Cache-TTL", fmt.Sprintf("%d", remaining))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(entry.Payload)
			return
		}
	}

	if req.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeChatError(w, r, h.metrics, domain.UpstreamRejected(domain.OpChatCompletions, "", domain.ServiceGemini, "máy chủ không hỗ trợ luồng"), false)
			return
		}

		lazy := newLazyStreamWriter(w, flusher)
		err := h.chatUseCase.ExecuteChatStream(r.Context(), &req, lazy, lazy.Flush)
		if err != nil {
			if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			if !lazy.headersSent {
				// Chưa gửi chunk nào ra client -> Trả về HTTP error JSON chuẩn (429, 503, v.v.)
				writeChatError(w, r, h.metrics, err, false)
			} else {
				// Đã gửi một phần chunk ra stream -> Ghi SSE chunk báo lỗi kèm [DONE]
				writeChatError(w, r, h.metrics, err, true)
			}
		}
		return
	}

	resp, err := h.chatUseCase.ExecuteChatSync(r.Context(), &req)
	if err != nil {
		if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		writeChatError(w, r, h.metrics, err, false)
		return
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		writeChatError(w, r, h.metrics, domain.UpstreamRejected(domain.OpChatCompletions, "", domain.ServiceGemini, "lỗi mã hóa dữ liệu phản hồi"), false)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if resp != nil && resp.ConversationID != "" {
		w.Header().Set("X-Conversation-Id", resp.ConversationID)
	}
	hasToolCallsInResp := false
	if resp != nil {
		for _, choice := range resp.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				hasToolCallsInResp = true
				break
			}
		}
	}
	if cacheKey != "" && !hasToolCallsInResp && h.cache != nil && h.cache.SupportsMethod("chat") {
		h.cache.Set(cacheKey, payload, "application/json", nil)
		w.Header().Set("X-Cache", "MISS")
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

type lazyStreamWriter struct {
	w           http.ResponseWriter
	flusher     http.Flusher
	headersSent bool
}

func newLazyStreamWriter(w http.ResponseWriter, flusher http.Flusher) *lazyStreamWriter {
	return &lazyStreamWriter{
		w:       w,
		flusher: flusher,
	}
}

func (l *lazyStreamWriter) Write(p []byte) (int, error) {
	if !l.headersSent {
		l.w.Header().Set("Content-Type", "text/event-stream")
		l.w.Header().Set("Cache-Control", "no-cache")
		l.w.Header().Set("Connection", "keep-alive")
		l.w.Header().Set("X-Accel-Buffering", "no")
		l.headersSent = true
	}
	return l.w.Write(p)
}

func (l *lazyStreamWriter) Flush() {
	if l.flusher != nil && l.headersSent {
		l.flusher.Flush()
	}
}

// HandleResponses xử lý endpoint POST /v1/responses (OpenAI Responses API dành cho OpenAI Codex CLI & SDK mới)
func (h *ChatHandler) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if h.modelRegistry.Count() == 0 {
		http.Error(w, `{"error":{"message":"chưa có phiên Gemini sẵn sàng","type":"service_unavailable"}}`, http.StatusServiceUnavailable)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"không thể đọc dữ liệu yêu cầu","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return
	}

	var raw struct {
		Model        string          `json:"model"`
		Instructions string          `json:"instructions"`
		Input        any             `json:"input"`
		Tools        json.RawMessage `json:"tools"`
		Stream       bool            `json:"stream"`
		Temperature  *float64        `json:"temperature"`
	}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		http.Error(w, `{"error":{"message":"dữ liệu JSON không hợp lệ","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return
	}

	var tools []domain.OpenAITool
	if len(raw.Tools) > 0 {
		var rawToolsList []map[string]any
		if err := json.Unmarshal(raw.Tools, &rawToolsList); err == nil {
			for _, rt := range rawToolsList {
				toolType, _ := rt["type"].(string)
				if toolType == "" {
					toolType = "function"
				}
				if name, ok := rt["name"].(string); ok && name != "" {
					desc, _ := rt["description"].(string)
					paramsBytes, _ := json.Marshal(rt["parameters"])
					tools = append(tools, domain.OpenAITool{
						Type: toolType,
						Function: domain.OpenAIFunctionDef{
							Name:        name,
							Description: desc,
							Parameters:  paramsBytes,
						},
					})
				} else if fnMap, ok := rt["function"].(map[string]any); ok {
					fnName, _ := fnMap["name"].(string)
					fnDesc, _ := fnMap["description"].(string)
					paramsBytes, _ := json.Marshal(fnMap["parameters"])
					tools = append(tools, domain.OpenAITool{
						Type: toolType,
						Function: domain.OpenAIFunctionDef{
							Name:        fnName,
							Description: fnDesc,
							Parameters:  paramsBytes,
						},
					})
				}
			}
		}
	}

	var messages []domain.OpenAIMessage
	if strings.TrimSpace(raw.Instructions) != "" {
		messages = append(messages, domain.OpenAIMessage{
			Role:    "system",
			Content: strings.TrimSpace(raw.Instructions),
		})
	}

	switch v := raw.Input.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			messages = append(messages, domain.OpenAIMessage{
				Role:    "user",
				Content: strings.TrimSpace(v),
			})
		}
	case []any:
		for _, item := range v {
			if itemMap, ok := item.(map[string]any); ok {
				role, _ := itemMap["role"].(string)
				itemType, _ := itemMap["type"].(string)
				if itemType == "function_call_output" {
					callID, _ := itemMap["call_id"].(string)
					output, _ := itemMap["output"].(string)
					messages = append(messages, domain.OpenAIMessage{
						Role:       "tool",
						ToolCallID: callID,
						Content:    output,
					})
					continue
				}
				if role == "" {
					role = "user"
				}
				var textContent string
				if strContent, ok := itemMap["content"].(string); ok {
					textContent = strContent
				} else if contentList, ok := itemMap["content"].([]any); ok {
					var sb strings.Builder
					for _, part := range contentList {
						if partMap, ok := part.(map[string]any); ok {
							if text, ok := partMap["text"].(string); ok {
								sb.WriteString(text)
							} else if text, ok := partMap["input_text"].(string); ok {
								sb.WriteString(text)
							}
						}
					}
					textContent = sb.String()
				}
				messages = append(messages, domain.OpenAIMessage{
					Role:    role,
					Content: textContent,
				})
			}
		}
	}

	if len(messages) == 0 {
		messages = append(messages, domain.OpenAIMessage{
			Role:    "user",
			Content: "Hello",
		})
	}

	targetModel := raw.Model
	if desc, ok := h.modelRegistry.ResolveGeminiModel(targetModel); ok {
		targetModel = desc.ID
	} else {
		descriptors := h.modelRegistry.List()
		if len(descriptors) > 0 {
			targetModel = descriptors[0].ID
		}
	}

	chatReq := &domain.OpenAIChatRequest{
		Model:    targetModel,
		Messages: messages,
		Tools:    tools,
		Stream:   raw.Stream,
	}
	if raw.Temperature != nil {
		chatReq.Temperature = *raw.Temperature
	}

	log.Printf("[HandleResponses] Model: %s, Stream: %v", targetModel, raw.Stream)

	nowNano := time.Now().UnixNano()
	respID := fmt.Sprintf("resp_%x", nowNano)
	itemID := fmt.Sprintf("item_%x", nowNano)

	// Compatibility fallback: OpenAI Codex CLI / SDK streams responses when Accept is text/event-stream or User-Agent is codex
	isStream := raw.Stream || strings.Contains(r.Header.Get("Accept"), "text/event-stream") || strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "codex")

	if isStream {
		chatReq.Stream = true
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, `{"error":{"message":"máy chủ không hỗ trợ luồng","type":"upstream_error"}}`, http.StatusInternalServerError)
			return
		}

		translator := newResponsesStreamTranslator(w, flusher, respID, itemID, targetModel)
		translator.ensureHeaders()
		translator.startKeepAlive()
		defer translator.finalize()

		err := h.chatUseCase.ExecuteChatStream(r.Context(), chatReq, translator, translator.Flush)
		if err != nil {
			if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("[Responses API Stream Error] %v", err)
		}
		return
	}

	resp, err := h.chatUseCase.ExecuteChatSync(r.Context(), chatReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q,"type":"upstream_error"}}`, err.Error()), http.StatusInternalServerError)
		return
	}

	var outputItems []any
	var replyText string
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		replyText = choice.Message.Content
		outputItems = append(outputItems, map[string]any{
			"id":     itemID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{
					"type": "output_text",
					"text": replyText,
				},
			},
		})
		for _, tc := range choice.Message.ToolCalls {
			outputItems = append(outputItems, map[string]any{
				"id":        tc.ID,
				"type":      "function_call",
				"status":    "completed",
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			})
		}
	}

	result := map[string]any{
		"id":     respID,
		"object": "response",
		"status": "completed",
		"model":  targetModel,
		"output": outputItems,
	}
	if resp.Usage != nil {
		result["usage"] = map[string]any{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

type responsesStreamTranslator struct {
	mu               sync.Mutex
	w                http.ResponseWriter
	flusher          http.Flusher
	respID           string
	itemID           string
	model            string
	createdAt        int64
	fullText         strings.Builder
	lineBuf          strings.Builder
	hasItemAdded     bool
	hasPartAdded     bool
	toolCallsEmitted map[string]bool
	headersSent      bool
	completed        bool
	doneCh           chan struct{}
}

func newResponsesStreamTranslator(w http.ResponseWriter, flusher http.Flusher, respID, itemID, model string) *responsesStreamTranslator {
	return &responsesStreamTranslator{
		w:                w,
		flusher:          flusher,
		respID:           respID,
		itemID:           itemID,
		model:            model,
		createdAt:        time.Now().Unix(),
		toolCallsEmitted: make(map[string]bool),
		doneCh:           make(chan struct{}),
	}
}

func (t *responsesStreamTranslator) startKeepAlive() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-t.doneCh:
				return
			case <-ticker.C:
				t.mu.Lock()
				if !t.completed && t.headersSent {
					fmt.Fprintf(t.w, ": keep-alive\n\n")
					t.flusher.Flush()
				}
				t.mu.Unlock()
			}
		}
	}()
}

func (t *responsesStreamTranslator) ensureHeaders() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.headersSent {
		t.w.Header().Set("Content-Type", "text/event-stream")
		t.w.Header().Set("Cache-Control", "no-cache")
		t.w.Header().Set("Connection", "keep-alive")
		t.w.Header().Set("X-Accel-Buffering", "no")
		t.headersSent = true
		t.flusher.Flush()

		createdPayload, _ := json.Marshal(map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":         t.respID,
				"object":     "response",
				"created_at": t.createdAt,
				"status":     "in_progress",
				"model":      t.model,
				"output":     []any{},
			},
		})
		fmt.Fprintf(t.w, "event: response.created\ndata: %s\n\n", createdPayload)
		t.flusher.Flush()
	}
}

func (t *responsesStreamTranslator) Write(p []byte) (int, error) {
	t.ensureHeaders()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.lineBuf.Write(p)
	content := t.lineBuf.String()
	lastNewline := strings.LastIndex(content, "\n")
	if lastNewline == -1 {
		return len(p), nil
	}
	readyContent := content[:lastNewline]
	t.lineBuf.Reset()
	t.lineBuf.WriteString(content[lastNewline+1:])

	lines := strings.Split(readyContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(line[5:])
		if dataStr == "[DONE]" {
			t.finalizeLocked()
			continue
		}
		var chunk domain.OpenAIChatResponse
		if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			if !t.hasItemAdded {
				itemAddedPayload, _ := json.Marshal(map[string]any{
					"type":         "response.output_item.added",
					"response_id":  t.respID,
					"output_index": 0,
					"item": map[string]any{
						"id":      t.itemID,
						"type":    "message",
						"status":  "in_progress",
						"role":    "assistant",
						"content": []any{},
					},
				})
				fmt.Fprintf(t.w, "event: response.output_item.added\ndata: %s\n\n", itemAddedPayload)

				partAddedPayload, _ := json.Marshal(map[string]any{
					"type":          "response.content_part.added",
					"response_id":   t.respID,
					"item_id":       t.itemID,
					"output_index":  0,
					"content_index": 0,
					"part": map[string]any{
						"type": "output_text",
						"text": "",
					},
				})
				fmt.Fprintf(t.w, "event: response.content_part.added\ndata: %s\n\n", partAddedPayload)
				t.hasItemAdded = true
				t.hasPartAdded = true
			}
			t.fullText.WriteString(choice.Delta.Content)
			deltaPayload, _ := json.Marshal(map[string]any{
				"type":          "response.output_text.delta",
				"response_id":   t.respID,
				"item_id":       t.itemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         choice.Delta.Content,
			})
			fmt.Fprintf(t.w, "event: response.output_text.delta\ndata: %s\n\n", deltaPayload)
			t.flusher.Flush()
		}

		if len(choice.Delta.ToolCalls) > 0 {
			for _, tc := range choice.Delta.ToolCalls {
				if !t.toolCallsEmitted[tc.ID] {
					t.toolCallsEmitted[tc.ID] = true
					itemAdded, _ := json.Marshal(map[string]any{
						"type":         "response.output_item.added",
						"response_id":  t.respID,
						"output_index": 1,
						"item": map[string]any{
							"id":        tc.ID,
							"type":      "function_call",
							"status":    "in_progress",
							"name":      tc.Function.Name,
							"arguments": "",
						},
					})
					fmt.Fprintf(t.w, "event: response.output_item.added\ndata: %s\n\n", itemAdded)

					argsDelta, _ := json.Marshal(map[string]any{
						"type":          "response.function_call_arguments.delta",
						"response_id":   t.respID,
						"item_id":       tc.ID,
						"output_index":  1,
						"delta":         tc.Function.Arguments,
					})
					fmt.Fprintf(t.w, "event: response.function_call_arguments.delta\ndata: %s\n\n", argsDelta)

					argsDone, _ := json.Marshal(map[string]any{
						"type":          "response.function_call_arguments.done",
						"response_id":   t.respID,
						"item_id":       tc.ID,
						"output_index":  1,
						"arguments":     tc.Function.Arguments,
					})
					fmt.Fprintf(t.w, "event: response.function_call_arguments.done\ndata: %s\n\n", argsDone)

					itemDone, _ := json.Marshal(map[string]any{
						"type":         "response.output_item.done",
						"response_id":  t.respID,
						"output_index": 1,
						"item": map[string]any{
							"id":        tc.ID,
							"type":      "function_call",
							"status":    "completed",
							"name":      tc.Function.Name,
							"arguments": tc.Function.Arguments,
						},
					})
					fmt.Fprintf(t.w, "event: response.output_item.done\ndata: %s\n\n", itemDone)
					t.flusher.Flush()
				}
			}
		}
	}
	return len(p), nil
}

func (t *responsesStreamTranslator) finalize() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finalizeLocked()
}

func (t *responsesStreamTranslator) finalizeLocked() {
	if t.completed {
		return
	}
	t.completed = true
	select {
	case <-t.doneCh:
	default:
		close(t.doneCh)
	}

	var outItems []any
	textStr := t.fullText.String()
	if t.hasPartAdded {
		textDonePayload, _ := json.Marshal(map[string]any{
			"type":          "response.output_text.done",
			"response_id":   t.respID,
			"item_id":       t.itemID,
			"output_index":  0,
			"content_index": 0,
			"text":          textStr,
		})
		fmt.Fprintf(t.w, "event: response.output_text.done\ndata: %s\n\n", textDonePayload)

		partDonePayload, _ := json.Marshal(map[string]any{
			"type":          "response.content_part.done",
			"response_id":   t.respID,
			"item_id":       t.itemID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": textStr,
			},
		})
		fmt.Fprintf(t.w, "event: response.content_part.done\ndata: %s\n\n", partDonePayload)

		itemDonePayload, _ := json.Marshal(map[string]any{
			"type":         "response.output_item.done",
			"response_id":  t.respID,
			"output_index": 0,
			"item": map[string]any{
				"id":     t.itemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": textStr,
					},
				},
			},
		})
		fmt.Fprintf(t.w, "event: response.output_item.done\ndata: %s\n\n", itemDonePayload)

		outItems = append(outItems, map[string]any{
			"id":     t.itemID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{
					"type": "output_text",
					"text": textStr,
				},
			},
		})
	}

	completedBytes, _ := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         t.respID,
			"object":     "response",
			"created_at": t.createdAt,
			"status":     "completed",
			"model":      t.model,
			"output":     outItems,
			"usage": map[string]any{
				"input_tokens":          10,
				"output_tokens":         10,
				"total_tokens":          20,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 0},
			},
		},
	})
	fmt.Fprintf(t.w, "event: response.completed\ndata: %s\n\n", completedBytes)
	t.flusher.Flush()
}

func (t *responsesStreamTranslator) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.flusher != nil {
		t.flusher.Flush()
	}
}
