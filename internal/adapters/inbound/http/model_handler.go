package http

import (
	"encoding/json"
	"net/http"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

type ModelHandler struct {
	modelRegistry *domain.ModelRegistry
}

func NewModelHandler(mr *domain.ModelRegistry) *ModelHandler {
	return &ModelHandler{modelRegistry: mr}
}

type OpenAIModelListResponse struct {
	Object string             `json:"object"`
	Data   []OpenAIModelEntry `json:"data"`
}

type OpenAIModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (h *ModelHandler) HandleListModels(w http.ResponseWriter, r *http.Request) {
	descriptors := h.modelRegistry.List()
	entries := make([]OpenAIModelEntry, 0, len(descriptors))

	now := time.Now().Unix()
	for _, m := range descriptors {
		ownedBy := "google"
		if m.TargetService == domain.ServiceFlow {
			ownedBy = "google-flow"
		}
		entries = append(entries, OpenAIModelEntry{
			ID:      m.ID,
			Object:  "model",
			Created: now,
			OwnedBy: ownedBy,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(OpenAIModelListResponse{
		Object: "list",
		Data:   entries,
	})
}
