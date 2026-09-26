package http

import (
	"encoding/json"
	"net/http"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/go-chi/chi/v5/middleware"
)

type MediaHandler struct {
	media   ports.MediaUseCase
	metrics *domain.ContractMetrics
}

func NewMediaHandler(media ports.MediaUseCase, metrics *domain.ContractMetrics) *MediaHandler {
	return &MediaHandler{media: media, metrics: metrics}
}

func (h *MediaHandler) HandleImage(w http.ResponseWriter, r *http.Request) {
	var req domain.ImageGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpImages, domain.OriginStreamChat, domain.ServiceFlow, "yêu cầu không đọc được"), domain.OpImages, domain.ServiceFlow)
		return
	}
	result, err := h.media.GenerateImage(r.Context(), &req)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpImages, domain.ServiceFlow)
		return
	}
	meta := mediaMeta(r, domain.OpImages, result.UnmappedFields, result.SpecVersion, result.MimeType)
	resp := map[string]any{
		"created": time.Now().Unix(),
		"data":    []map[string]string{{"url": result.URL}},
		"error":   nil,
		"meta":    meta,
	}
	if result.CreditsBalance != nil {
		resp["credits"] = map[string]any{"total_credits": *result.CreditsBalance}
		meta["credits_balance"] = *result.CreditsBalance
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *MediaHandler) HandleVideo(w http.ResponseWriter, r *http.Request) {
	var req domain.VideoGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpVideos, domain.OriginStreamChat, domain.ServiceFlow, "yêu cầu không đọc được"), domain.OpVideos, domain.ServiceFlow)
		return
	}
	result, err := h.media.GenerateVideo(r.Context(), &req)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpVideos, domain.ServiceFlow)
		return
	}
	data := map[string]any{"url": result.URL}
	if result.MimeType != "" {
		data["mime_type"] = result.MimeType
	}
	meta := mediaMeta(r, domain.OpVideos, result.UnmappedFields, result.SpecVersion, result.MimeType)
	resp := map[string]any{
		"data":  data,
		"error": nil,
		"meta":  meta,
	}
	if result.CreditsBalance != nil {
		resp["credits"] = map[string]any{"total_credits": *result.CreditsBalance}
		meta["credits_balance"] = *result.CreditsBalance
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mediaMeta(r *http.Request, operation string, unmapped int, spec, mime string) map[string]any {
	meta := map[string]any{
		"operation":       operation,
		"retryable":       false,
		"unmapped_fields": unmapped,
		"spec_version":    spec,
		"debug": map[string]any{
			"origin_operation": domain.OriginStreamChat,
		},
	}
	if mime != "" {
		meta["mime_type"] = mime
	}
	if id := middleware.GetReqID(r.Context()); id != "" {
		meta["correlation_id"] = id
	}
	return meta
}
