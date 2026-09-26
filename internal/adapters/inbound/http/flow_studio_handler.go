package http

import (
	"encoding/json"
	"net/http"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

type FlowStudioHandler struct {
	flow    ports.FlowUseCase
	metrics *domain.ContractMetrics
}

func NewFlowStudioHandler(flow ports.FlowUseCase, metrics *domain.ContractMetrics) *FlowStudioHandler {
	return &FlowStudioHandler{flow: flow, metrics: metrics}
}

// HandleExtendVideo nối dài video có sẵn
func (h *FlowStudioHandler) HandleExtendVideo(w http.ResponseWriter, r *http.Request) {
	var req domain.VideoExtensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpFlowExtendVideo, domain.OriginExtendVideo, domain.ServiceFlow, "yêu cầu không đọc được"), domain.OpFlowExtendVideo, domain.ServiceFlow)
		return
	}
	result, err := h.flow.ExtendVideo(r.Context(), &req)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowExtendVideo, domain.ServiceFlow)
		return
	}
	meta := mediaMeta(r, domain.OpFlowExtendVideo, result.UnmappedFields, result.SpecVersion, result.MimeType)
	resp := map[string]any{
		"created": time.Now().Unix(),
		"data":    map[string]any{"url": result.URL, "mime_type": result.MimeType},
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

// HandleUpsample4K kích hoạt nâng cấp video 4K
func (h *FlowStudioHandler) HandleUpsample4K(w http.ResponseWriter, r *http.Request) {
	var req domain.Upsample4KRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpFlowUpsample4K, domain.OriginUpsample4K, domain.ServiceFlow, "yêu cầu không đọc được"), domain.OpFlowUpsample4K, domain.ServiceFlow)
		return
	}
	res, err := h.flow.UpsampleVideo4K(r.Context(), &req)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowUpsample4K, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  res,
		"error": nil,
	})
}

// HandleGenerateAudio tạo nhạc nền MusicFX
func (h *FlowStudioHandler) HandleGenerateAudio(w http.ResponseWriter, r *http.Request) {
	var req domain.FlowMusicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpFlowAudio, domain.OriginMusicFX, domain.ServiceFlow, "yêu cầu không đọc được"), domain.OpFlowAudio, domain.ServiceFlow)
		return
	}
	res, err := h.flow.GenerateAudio(r.Context(), req)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowAudio, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  res,
		"error": nil,
	})
}

// HandleListVoices lấy danh sách 30 nhân vật giọng đọc
func (h *FlowStudioHandler) HandleListVoices(w http.ResponseWriter, r *http.Request) {
	voices, err := h.flow.ListVoicePersonas(r.Context())
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowVoices, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  voices,
		"error": nil,
	})
}

// HandleListProjects lấy danh sách dự án
func (h *FlowStudioHandler) HandleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.flow.ListProjects(r.Context())
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowProjects, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  projects,
		"error": nil,
	})
}

// HandleCreateProject tạo dự án mới
func (h *FlowStudioHandler) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Title == "" {
		body.Title = "Dự án mới"
	}
	id, err := h.flow.CreateProject(r.Context(), body.Title)
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowCreateProject, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  map[string]string{"id": id, "title": body.Title},
		"error": nil,
	})
}

// HandleTrashProject chuyển dự án vào thùng rác
func (h *FlowStudioHandler) HandleTrashProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpFlowTrash, domain.OriginTrashMove, domain.ServiceFlow, "thiếu id dự án"), domain.OpFlowTrash, domain.ServiceFlow)
		return
	}
	if err := h.flow.MoveProjectToTrash(r.Context(), id); err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowTrash, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleListTrash lấy danh sách dự án trong thùng rác
func (h *FlowStudioHandler) HandleListTrash(w http.ResponseWriter, r *http.Request) {
	trashList, err := h.flow.ListTrash(r.Context())
	if err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowTrash, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  trashList,
		"error": nil,
	})
}

// HandleRestoreProject khôi phục dự án
func (h *FlowStudioHandler) HandleRestoreProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpFlowTrash, domain.OriginTrashRestore, domain.ServiceFlow, "thiếu id dự án"), domain.OpFlowTrash, domain.ServiceFlow)
		return
	}
	if err := h.flow.RestoreProject(r.Context(), id); err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowTrash, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleDeleteProject xóa vĩnh viễn dự án
func (h *FlowStudioHandler) HandleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeOperationError(w, r, h.metrics, domain.InvalidRequest(domain.OpFlowProjects, domain.OriginProjectDelete, domain.ServiceFlow, "thiếu id dự án"), domain.OpFlowProjects, domain.ServiceFlow)
		return
	}
	if err := h.flow.DeleteProjectPermanently(r.Context(), id); err != nil {
		writeOperationError(w, r, h.metrics, err, domain.OpFlowProjects, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// HandleListMediaGallery lấy danh sách tác phẩm media đã lưu
func (h *FlowStudioHandler) HandleListMediaGallery(w http.ResponseWriter, r *http.Request) {
	kind := domain.MediaKind(r.URL.Query().Get("kind"))
	list, err := h.flow.ListMediaGallery(r.Context(), kind)
	if err != nil {
		writeOperationError(w, r, h.metrics, domain.EnsureGateway(err, domain.OpFlowGallery, domain.ServiceFlow), domain.OpFlowGallery, domain.ServiceFlow)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  list,
		"error": nil,
	})
}
