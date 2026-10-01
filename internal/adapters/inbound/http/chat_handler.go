package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

	var cacheKey string
	if !req.Stream && h.cache != nil && h.cache.SupportsMethod("chat") {
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

		cacheKey = services.GenerateChatCacheKey(req.Model, req.Messages, req.Temperature, sysPrompt, isThinking, isGrounding, isCodeInterpreter)
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
	if cacheKey != "" && h.cache != nil && h.cache.SupportsMethod("chat") {
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
