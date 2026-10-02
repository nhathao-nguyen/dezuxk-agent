package http

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

//go:embed codex_model_template.json
var codexModelTemplateJSON []byte

type ModelHandler struct {
	modelRegistry *domain.ModelRegistry
	templateMap   map[string]any
}

func NewModelHandler(mr *domain.ModelRegistry) *ModelHandler {
	var tpl map[string]any
	if len(codexModelTemplateJSON) > 0 {
		_ = json.Unmarshal(codexModelTemplateJSON, &tpl)
	}
	return &ModelHandler{
		modelRegistry: mr,
		templateMap:   tpl,
	}
}

type OpenAIModelListResponse struct {
	Object string           `json:"object"`
	Data   []map[string]any `json:"data"`
	Models []map[string]any `json:"models"`
}

func (h *ModelHandler) HandleListModels(w http.ResponseWriter, r *http.Request) {
	descriptors := h.modelRegistry.List()
	entries := make([]map[string]any, 0, len(descriptors))
	now := time.Now().Unix()

	for i, m := range descriptors {
		entry := make(map[string]any)
		// Copy all template fields if available
		for k, v := range h.templateMap {
			entry[k] = v
		}

		// Overwrite specific fields
		entry["id"] = m.ID
		entry["slug"] = m.ID
		entry["object"] = "model"
		entry["created"] = now
		entry["display_name"] = m.ID
		entry["description"] = "Google Gemini Model via Dezuxk Gateway (" + m.ID + ")"
		entry["priority"] = i + 1
		entry["visibility"] = "list"
		entry["supported_in_api"] = true

		if m.TargetService == domain.ServiceFlow {
			entry["owned_by"] = "google-flow"
		} else {
			entry["owned_by"] = "google"
		}

		entries = append(entries, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(OpenAIModelListResponse{
		Object: "list",
		Data:   entries,
		Models: entries,
	})
}
