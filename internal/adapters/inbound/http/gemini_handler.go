package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type GeminiHandler struct {
	historyUseCase  ports.GeminiHistoryUseCase
	uploadUseCase   ports.GeminiUploadUseCase
	canvasUseCase   ports.GeminiCanvasUseCase
	quotaUseCase    ports.GeminiQuotaUseCase
	feedbackUseCase ports.GeminiFeedbackUseCase
	metrics         *domain.ContractMetrics
}

func NewGeminiHandler(
	hu ports.GeminiHistoryUseCase,
	uu ports.GeminiUploadUseCase,
	cu ports.GeminiCanvasUseCase,
	qu ports.GeminiQuotaUseCase,
	fu ports.GeminiFeedbackUseCase,
	metrics *domain.ContractMetrics,
) *GeminiHandler {
	return &GeminiHandler{
		historyUseCase:  hu,
		uploadUseCase:   uu,
		canvasUseCase:   cu,
		quotaUseCase:    qu,
		feedbackUseCase: fu,
		metrics:         metrics,
	}
}

// HandleListConversations GET /v1/gemini/conversations
func (h *GeminiHandler) HandleListConversations(w http.ResponseWriter, r *http.Request) {
	if h.historyUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("MaZiqc", "", domain.ServiceGemini, "chức năng lịch sử chưa sẵn sàng"), "MaZiqc", domain.ServiceGemini)
		return
	}

	limit := 25
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	conversations, nextToken, err := h.historyUseCase.ListConversations(r.Context(), limit)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "MaZiqc", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"conversations":   conversations,
			"next_page_token": nextToken,
		},
		"error": nil,
	})
}

// HandleGetConversation GET /v1/gemini/conversations/{id}
func (h *GeminiHandler) HandleGetConversation(w http.ResponseWriter, r *http.Request) {
	if h.historyUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("cZOhpc", "", domain.ServiceGemini, "chức năng lịch sử chưa sẵn sàng"), "cZOhpc", domain.ServiceGemini)
		return
	}

	convID := chi.URLParam(r, "id")
	if convID == "" {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest("cZOhpc", "", domain.ServiceGemini, "thiếu conversation_id"), "cZOhpc", domain.ServiceGemini)
		return
	}

	tree, err := h.historyUseCase.GetConversationDetail(r.Context(), convID)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "cZOhpc", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  tree,
		"error": nil,
	})
}

// HandleRenameConversation POST /v1/gemini/conversations/{id}/rename
func (h *GeminiHandler) HandleRenameConversation(w http.ResponseWriter, r *http.Request) {
	if h.historyUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("PCck7e", "", domain.ServiceGemini, "chức năng lịch sử chưa sẵn sàng"), "PCck7e", domain.ServiceGemini)
		return
	}

	convID := chi.URLParam(r, "id")
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest("PCck7e", "", domain.ServiceGemini, "tiêu đề mới không hợp lệ"), "PCck7e", domain.ServiceGemini)
		return
	}

	if err := h.historyUseCase.RenameConversation(r.Context(), convID, body.Title); err != nil {
		writeOperationError(w, r, h.metrics, err, "PCck7e", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleDeleteConversation DELETE /v1/gemini/conversations/{id}
func (h *GeminiHandler) HandleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if h.historyUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("VxUbXb", "", domain.ServiceGemini, "chức năng lịch sử chưa sẵn sàng"), "VxUbXb", domain.ServiceGemini)
		return
	}

	convID := chi.URLParam(r, "id")
	if err := h.historyUseCase.DeleteConversation(r.Context(), convID); err != nil {
		writeOperationError(w, r, h.metrics, err, "VxUbXb", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleSwitchBranch POST /v1/gemini/conversations/{id}/branch
func (h *GeminiHandler) HandleSwitchBranch(w http.ResponseWriter, r *http.Request) {
	if h.historyUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("wEb32b", "", domain.ServiceGemini, "chức năng rẽ nhánh chưa sẵn sàng"), "wEb32b", domain.ServiceGemini)
		return
	}

	convID := chi.URLParam(r, "id")
	var body struct {
		ResponseID string `json:"response_id"`
		ChoiceID   string `json:"choice_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ResponseID == "" || body.ChoiceID == "" {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest("wEb32b", "", domain.ServiceGemini, "thiếu response_id hoặc choice_id"), "wEb32b", domain.ServiceGemini)
		return
	}

	if err := h.historyUseCase.SwitchBranch(r.Context(), convID, body.ResponseID, body.ChoiceID); err != nil {
		writeOperationError(w, r, h.metrics, err, "wEb32b", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleUpload POST /v1/gemini/upload
func (h *GeminiHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if h.uploadUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("UploadFile", "", domain.ServiceGemini, "chức năng upload chưa sẵn sàng"), "UploadFile", domain.ServiceGemini)
		return
	}

	contentType := r.Header.Get("Content-Type")
	var fileName string
	var mimeType string
	var reader io.Reader
	var size int64

	if strings.HasPrefix(contentType, "multipart/form-data") {
		err := r.ParseMultipartForm(50 << 20) // max 50MB
		if err != nil {
			writeOperationError(w, r, h.metrics, domain.InvalidRequest("UploadFile", "", domain.ServiceGemini, "không đọc được multipart form"), "UploadFile", domain.ServiceGemini)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeOperationError(w, r, h.metrics, domain.InvalidRequest("UploadFile", "", domain.ServiceGemini, "thiếu field file trong form"), "UploadFile", domain.ServiceGemini)
			return
		}
		defer file.Close()
		fileName = header.Filename
		mimeType = header.Header.Get("Content-Type")
		size = header.Size
		reader = file
	} else {
		// Hỗ trợ nhận JSON payload Base64
		var body struct {
			FileName   string `json:"file_name"`
			MimeType   string `json:"mime_type"`
			DataBase64 string `json:"data_base64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DataBase64 == "" {
			writeOperationError(w, r, h.metrics, domain.InvalidRequest("UploadFile", "", domain.ServiceGemini, "yêu cầu multipart/form-data hoặc JSON data_base64"), "UploadFile", domain.ServiceGemini)
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(body.DataBase64)
		if err != nil {
			writeOperationError(w, r, h.metrics, domain.InvalidRequest("UploadFile", "", domain.ServiceGemini, "chuỗi base64 không hợp lệ"), "UploadFile", domain.ServiceGemini)
			return
		}
		fileName = body.FileName
		mimeType = body.MimeType
		size = int64(len(decoded))
		reader = bytes.NewReader(decoded)
	}

	storageToken, err := h.uploadUseCase.UploadFile(r.Context(), fileName, mimeType, reader, size)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "UploadFile", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"storage_token": storageToken,
		"file_name":     fileName,
		"mime_type":     mimeType,
	})
}

// HandleGetUsage GET /v1/gemini/usage
func (h *GeminiHandler) HandleGetUsage(w http.ResponseWriter, r *http.Request) {
	if h.quotaUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("usage", "", domain.ServiceGemini, "chức năng quota chưa sẵn sàng"), "usage", domain.ServiceGemini)
		return
	}

	quota, err := h.quotaUseCase.GetQuota(r.Context())
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "usage", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  quota,
		"error": nil,
	})
}

// HandleGetAccountTier GET /v1/gemini/account-tier
func (h *GeminiHandler) HandleGetAccountTier(w http.ResponseWriter, r *http.Request) {
	if h.quotaUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("I4z33b", "", domain.ServiceGemini, "chức năng tier chưa sẵn sàng"), "I4z33b", domain.ServiceGemini)
		return
	}

	tier, err := h.quotaUseCase.GetAccountTier(r.Context())
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "I4z33b", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  tier,
		"error": nil,
	})
}

// HandleFeedback POST /v1/gemini/feedback
func (h *GeminiHandler) HandleFeedback(w http.ResponseWriter, r *http.Request) {
	if h.feedbackUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("uP80Sb", "", domain.ServiceGemini, "chức năng feedback chưa sẵn sàng"), "uP80Sb", domain.ServiceGemini)
		return
	}

	var req domain.FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest("uP80Sb", "", domain.ServiceGemini, "payload feedback không đọc được"), "uP80Sb", domain.ServiceGemini)
		return
	}

	if err := h.feedbackUseCase.SendFeedback(r.Context(), &req); err != nil {
		writeOperationError(w, r, h.metrics, err, "uP80Sb", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleCreateCanvas POST /v1/gemini/canvas
func (h *GeminiHandler) HandleCreateCanvas(w http.ResponseWriter, r *http.Request) {
	if h.canvasUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("tVk3Sc", "", domain.ServiceGemini, "chức năng canvas chưa sẵn sàng"), "tVk3Sc", domain.ServiceGemini)
		return
	}

	var body struct {
		ConversationID string `json:"conversation_id"`
		Title          string `json:"title"`
		ContentType    string `json:"content_type"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest("tVk3Sc", "", domain.ServiceGemini, "payload canvas không đọc được"), "tVk3Sc", domain.ServiceGemini)
		return
	}

	artifact, err := h.canvasUseCase.CreateCanvas(r.Context(), body.ConversationID, body.Title, body.ContentType, body.Content)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "tVk3Sc", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  artifact,
		"error": nil,
	})
}

// HandleUpdateCanvasDelta POST /v1/gemini/canvas/{id}/delta
func (h *GeminiHandler) HandleUpdateCanvasDelta(w http.ResponseWriter, r *http.Request) {
	if h.canvasUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("sA4a8", "", domain.ServiceGemini, "chức năng canvas chưa sẵn sàng"), "sA4a8", domain.ServiceGemini)
		return
	}

	canvasID := chi.URLParam(r, "id")
	var body struct {
		BaseVersion int                   `json:"base_version"`
		DiffOps     []domain.CanvasDiffOp `json:"diff_ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest("sA4a8", "", domain.ServiceGemini, "payload delta không đọc được"), "sA4a8", domain.ServiceGemini)
		return
	}

	artifact, err := h.canvasUseCase.UpdateDelta(r.Context(), canvasID, body.BaseVersion, body.DiffOps)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "sA4a8", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  artifact,
		"error": nil,
	})
}

// HandlePublishCanvas POST /v1/gemini/canvas/{id}/publish
func (h *GeminiHandler) HandlePublishCanvas(w http.ResponseWriter, r *http.Request) {
	if h.canvasUseCase == nil {
		writeOperationError(w, r, h.metrics, domain.UpstreamRejected("H8s0fe", "", domain.ServiceGemini, "chức năng canvas chưa sẵn sàng"), "H8s0fe", domain.ServiceGemini)
		return
	}

	canvasID := chi.URLParam(r, "id")
	var body struct {
		Visibility int  `json:"visibility"`
		AllowFork  bool `json:"allow_fork"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	shareURL, err := h.canvasUseCase.Publish(r.Context(), canvasID, body.Visibility, body.AllowFork)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, "H8s0fe", domain.ServiceGemini)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"share_url": shareURL,
	})
}
