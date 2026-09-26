package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services"
	"github.com/go-chi/chi/v5/middleware"
)

type FlowHandler struct {
	creditUseCase ports.FlowCreditUseCase
	metrics       *domain.ContractMetrics
	cache         *services.ResponseCache
}

func NewFlowHandler(creditUseCase ports.FlowCreditUseCase, metrics *domain.ContractMetrics) *FlowHandler {
	return &FlowHandler{
		creditUseCase: creditUseCase,
		metrics:       metrics,
	}
}

func (h *FlowHandler) SetCache(c *services.ResponseCache) {
	h.cache = c
}

func (h *FlowHandler) HandleGetCredits(w http.ResponseWriter, r *http.Request) {
	cacheKey := "credits:active"
	if h.cache != nil && h.cache.SupportsMethod("credits") {
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

	accountID, balance, err := h.creditUseCase.GetCredits(r.Context())
	if err != nil {
		writeFlowError(w, r, h.metrics, err)
		return
	}

	meta := map[string]any{
		"operation":       domain.OpFlowGetCredits,
		"retryable":       false,
		"unmapped_fields": balance.UnmappedFields,
		"spec_version":    balance.SpecVersion,
		"debug": map[string]any{
			"origin_operation": domain.OriginNzlxg,
		},
	}
	if id := middleware.GetReqID(r.Context()); id != "" {
		meta["correlation_id"] = id
	}
	body := map[string]any{
		"data": map[string]any{
			"account_id":    accountID,
			"total_credits": balance.Amount,
		},
		"error": nil,
		"meta":  meta,
	}
	payloadBytes, err := json.Marshal(body)
	if err != nil {
		writeFlowError(w, r, h.metrics, domain.UpstreamRejected(domain.OpFlowGetCredits, "", domain.ServiceFlow, "lỗi mã hóa dữ liệu phản hồi"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if h.cache != nil && h.cache.SupportsMethod("credits") {
		h.cache.Set(cacheKey, payloadBytes, "application/json", nil)
		w.Header().Set("X-Cache", "MISS")
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payloadBytes)
}

