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
	if strings.TrimSpace(req.Model) == "" || len(req.Messages) == 0 {
		writeChatError(w, r, h.metrics, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "thiếu hội thoại hoặc mô hình"), false)
		return
	}
	desc, ok := h.modelRegistry.Get(req.Model)
	if !ok || desc.TargetService != domain.ServiceGemini {
		writeChatError(w, r, h.metrics, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "không có mô hình này"), false)
		return
	}

	var cacheKey string
	if !req.Stream && h.cache != nil && h.cache.SupportsMethod("chat") {
		var sysPrompt string
		for _, m := range req.Messages {
			if strings.EqualFold(m.Role, "system") {
				sysPrompt = m.Content
				break
			}
		}
		cacheKey = services.GenerateChatCacheKey(req.Model, req.Messages, req.Temperature, sysPrompt)
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
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeChatError(w, r, h.metrics, domain.UpstreamRejected(domain.OpChatCompletions, "", domain.ServiceGemini, "máy chủ không hỗ trợ luồng"), false)
			return
		}

		err := h.chatUseCase.ExecuteChatStream(r.Context(), &req, w, flusher.Flush)
		if err != nil {
			if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			writeChatError(w, r, h.metrics, err, true)
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
	if cacheKey != "" && h.cache != nil && h.cache.SupportsMethod("chat") {
		h.cache.Set(cacheKey, payload, "application/json", nil)
		w.Header().Set("X-Cache", "MISS")
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
