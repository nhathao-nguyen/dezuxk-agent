package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

// AdminKeyHandler xử lý các endpoint quản trị Virtual API Keys
type AdminKeyHandler struct {
	keyUseCase ports.KeyUseCase
}

func NewAdminKeyHandler(keyUseCase ports.KeyUseCase) *AdminKeyHandler {
	return &AdminKeyHandler{
		keyUseCase: keyUseCase,
	}
}

// HandleCreateKey: POST /v1/admin/keys
func (h *AdminKeyHandler) HandleCreateKey(w http.ResponseWriter, r *http.Request) {
	if h.keyUseCase == nil {
		http.Error(w, `{"error":"Key service unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	var req domain.CreateKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Dữ liệu JSON không hợp lệ: " + err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	created, err := h.keyUseCase.CreateKey(r.Context(), req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

// HandleListKeys: GET /v1/admin/keys
func (h *AdminKeyHandler) HandleListKeys(w http.ResponseWriter, r *http.Request) {
	if h.keyUseCase == nil {
		http.Error(w, `{"error":"Key service unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	keys, err := h.keyUseCase.ListActiveKeys(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Không thể lấy danh sách key: " + err.Error(),
				"type":    "internal_error",
			},
		})
		return
	}

	if keys == nil {
		keys = make([]*domain.VirtualKey, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys":  keys,
		"count": len(keys),
	})
}

// HandleRevokeKey: DELETE /v1/admin/keys/{id}
func (h *AdminKeyHandler) HandleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if h.keyUseCase == nil {
		http.Error(w, `{"error":"Key service unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Thiếu id của key cần thu hồi",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	if err := h.keyUseCase.RevokeKey(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "not_found_error",
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Khóa API đã được thu hồi thành công.",
		"id":      id,
	})
}
