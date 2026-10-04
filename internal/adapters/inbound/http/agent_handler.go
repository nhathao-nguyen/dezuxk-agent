package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/sandbox"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	"github.com/go-chi/chi/v5"
)

// AgentHandler xử lý các yêu cầu tác vụ tự trị qua REST API
type AgentHandler struct {
	runner         ports.AgentRunner
	graphRunner    ports.GraphWorkflowRunner
	toolReg        ports.ToolRegistry
	checkpointRepo ports.CheckpointRepository
	memorySvc      ports.MemoryService
	subagentSup    ports.SubagentSupervisor
	jobService     ports.AgentJobService
	runRepo        ports.AgentRunRepository
}

// NewAgentHandler khởi tạo AgentHandler
func NewAgentHandler(
	runner ports.AgentRunner,
	graphRunner ports.GraphWorkflowRunner,
	toolReg ports.ToolRegistry,
	checkpointRepo ports.CheckpointRepository,
	memorySvc ports.MemoryService,
) *AgentHandler {
	return &AgentHandler{
		runner:         runner,
		graphRunner:    graphRunner,
		toolReg:        toolReg,
		checkpointRepo: checkpointRepo,
		memorySvc:      memorySvc,
	}
}

// SetJobService cấu hình Async Job Service và Repository cho Durable Agent Jobs
func (h *AgentHandler) SetJobService(js ports.AgentJobService, repo ports.AgentRunRepository) {
	h.jobService = js
	h.runRepo = repo
}

// AgentRunRequest cấu trúc payload gửi lên /v1/agent/run
type AgentRunRequest struct {
	Goal           string `json:"goal"`
	Workflow       string `json:"workflow,omitempty"` // "graph" (mặc định) | "react"
	ResumeTaskID   string `json:"resume_task_id,omitempty"`
	Model          string `json:"model,omitempty"`
	Workspace      string `json:"workspace,omitempty"`
	Supervised     bool   `json:"supervised,omitempty"`
	MaxSteps       int    `json:"max_steps,omitempty"`
	UseSandbox     bool   `json:"use_sandbox,omitempty"`
	AutoMerge      bool   `json:"auto_merge,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (h *AgentHandler) resolveEffectiveOptions(ctx context.Context, req AgentRunRequest) (domain.AgentRunOptions, error) {
	identity, ok := domain.TenantIdentityFromContext(ctx)
	if !ok {
		identity = domain.DefaultRestrictedIdentity()
	}

	if !identity.HasScope(domain.ScopeAgent) {
		return domain.AgentRunOptions{}, fmt.Errorf("khóa API không có quyền truy cập phạm vi (scope): %s", domain.ScopeAgent)
	}

	ws := strings.TrimSpace(req.Workspace)
	if ws == "" {
		ws = "."
	}
	if !identity.IsWorkspaceAllowed(ws) {
		return domain.AgentRunOptions{}, fmt.Errorf("truy cập bị chặn: workspace %q không được phép cho tenant %q", ws, identity.TenantID)
	}

	supervised := req.Supervised
	if identity.RequireApproval {
		supervised = true
	}

	useSandbox := req.UseSandbox
	if identity.EnforceSandbox {
		useSandbox = true
	}

	autoMerge := req.AutoMerge
	if !identity.AutoMergeAllowed {
		autoMerge = false
	}

	maxSteps := identity.EffectiveMaxSteps(req.MaxSteps)

	return domain.AgentRunOptions{
		Model:      req.Model,
		Workspace:  ws,
		Supervised: supervised,
		MaxSteps:   maxSteps,
		UseSandbox: useSandbox,
		AutoMerge:  autoMerge,
	}, nil
}

// HandleRun xử lý POST /v1/agent/run
func (h *AgentHandler) HandleRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Payload JSON không hợp lệ: " + err.Error(),
		})
		return
	}

	if req.ResumeTaskID == "" && req.Goal == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Trường 'goal' (hoặc 'resume_task_id') là bắt buộc",
		})
		return
	}

	opts, err := h.resolveEffectiveOptions(r.Context(), req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	startTime := time.Now()

	// 1. Nếu chọn chế độ State Machine Graph hoặc Resume task
	if (req.Workflow == "graph" || req.Workflow == "" || req.ResumeTaskID != "") && h.graphRunner != nil {
		var graphState *domain.AgentGraphState
		var err error

		if req.ResumeTaskID != "" {
			graphState, err = h.graphRunner.ResumeGraph(r.Context(), req.ResumeTaskID, opts)
		} else {
			graphState, err = h.graphRunner.RunGraph(r.Context(), req.Goal, opts)
		}

		elapsed := time.Since(startTime)
		if err != nil && graphState == nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":        err.Error(),
				"elapsed_time": elapsed.String(),
			})
			return
		}

		responsePayload := map[string]any{
			"workflow":     "graph",
			"task_id":      graphState.TaskID,
			"graph_state":  graphState,
			"elapsed_time": elapsed.String(),
			"success":      graphState.IsCompleted && graphState.CurrentNode == domain.NodeKindComplete,
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(responsePayload)
		return
	}

	// 2. Chế độ phẳng ReAct Runner
	if h.runner == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Agent Runner chưa được cấu hình trên máy chủ này",
		})
		return
	}

	state, err := h.runner.Run(r.Context(), req.Goal, opts)
	elapsed := time.Since(startTime)

	if err != nil && state == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":        err.Error(),
			"elapsed_time": elapsed.String(),
		})
		return
	}

	responsePayload := map[string]any{
		"workflow":     "react",
		"state":        state,
		"elapsed_time": elapsed.String(),
		"success":      state.IsCompleted && state.StopReason == "completed",
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(responsePayload)
}

// HandleRunStream xử lý POST /v1/agent/run/stream qua Server-Sent Events (SSE)
func (h *AgentHandler) HandleRunStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		if u, hasUnwrap := w.(interface{ Unwrap() http.ResponseWriter }); hasUnwrap {
			flusher, ok = u.Unwrap().(http.Flusher)
		}
	}
	if !ok {
		http.Error(w, "Streaming không được hỗ trợ bởi server", http.StatusInternalServerError)
		return
	}

	var req AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Payload JSON không hợp lệ: " + err.Error(),
		})
		return
	}

	if req.ResumeTaskID == "" && req.Goal == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Trường 'goal' (hoặc 'resume_task_id') là bắt buộc",
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	sendSSE := func(event string, data any) {
		bytes, err := json.Marshal(data)
		if err != nil {
			return
		}
		_ = rc.SetWriteDeadline(time.Time{})
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, bytes)
		flusher.Flush()
	}

	// Gắn LiveOutput callback vào context để stream terminal output từ shell_tools theo thời gian thực
	streamCtx := domain.WithLiveOutput(r.Context(), func(streamType string, chunk string) {
		sendSSE("terminal_output", map[string]any{
			"type":      streamType,
			"chunk":     chunk,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	opts, err := h.resolveEffectiveOptions(r.Context(), req)
	if err != nil {
		sendSSE("error", map[string]string{"error": err.Error()})
		return
	}

	opts.OnProgress = func(step int, kind string, message string) {
		sseEvent := "progress"
		switch kind {
		case "node_change", "node_plan", "node_execute", "node_verify", "node_fix", "node_complete":
			sseEvent = kind
		case "plan_created":
			sseEvent = "plan_created"
		case "reasoning":
			sseEvent = "reasoning"
		case "thinking":
			sseEvent = "thinking"
		case "tool_start":
			sseEvent = "tool_start"
		case "tool_end":
			sseEvent = "tool_end"
		case "verify_exec":
			sseEvent = "verify_exec"
		case "verify_pass":
			sseEvent = "verify_pass"
		case "verify_fail":
			sseEvent = "verify_fail"
		case "approval_wait":
			sseEvent = "approval_wait"
		case "enforce_action":
			sseEvent = "enforce_action"
		case "checkpoint":
			sseEvent = "checkpoint"
		case "sandbox_created":
			sseEvent = "sandbox_created"
		case "git_diff":
			sseEvent = "git_diff"
		}

		sendSSE(sseEvent, map[string]any{
			"step":      step,
			"kind":      kind,
			"message":   message,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	}

	sendSSE("run_start", map[string]any{
		"workflow":  req.Workflow,
		"goal":      req.Goal,
		"model":     req.Model,
		"workspace": req.Workspace,
		"timestamp": time.Now().Format(time.RFC3339),
	})

	if (req.Workflow == "graph" || req.Workflow == "" || req.ResumeTaskID != "") && h.graphRunner != nil {
		var graphState *domain.AgentGraphState
		var err error
		if req.ResumeTaskID != "" {
			graphState, err = h.graphRunner.ResumeGraph(streamCtx, req.ResumeTaskID, opts)
		} else {
			graphState, err = h.graphRunner.RunGraph(streamCtx, req.Goal, opts)
		}

		if err != nil {
			sendSSE("error", map[string]any{
				"error": err.Error(),
				"state": graphState,
			})
			return
		}

		sendSSE("run_complete", map[string]any{
			"workflow":    "graph",
			"task_id":     graphState.TaskID,
			"graph_state": graphState,
			"success":     graphState.IsCompleted && graphState.CurrentNode == domain.NodeKindComplete,
		})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	if h.runner != nil {
		state, err := h.runner.Run(streamCtx, req.Goal, opts)
		if err != nil {
			sendSSE("error", map[string]any{
				"error": err.Error(),
				"state": state,
			})
			return
		}

		sendSSE("run_complete", map[string]any{
			"workflow": "react",
			"state":    state,
			"success":  state.IsCompleted && state.StopReason == "completed",
		})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	sendSSE("error", map[string]string{"error": "Không có runner phù hợp"})
}

// HandleListTools xử lý GET /v1/agent/tools
func (h *AgentHandler) HandleListTools(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.toolReg == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tools": []any{},
		})
		return
	}

	toolsList := h.toolReg.ListTools()
	var dtos []map[string]any
	for _, t := range toolsList {
		dtos = append(dtos, map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
			"permission":  t.Permission(),
		})
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tools": dtos,
		"total": len(dtos),
	})
}

// HandleGetCheckpoints xử lý GET /v1/agent/checkpoints/{taskId}
func (h *AgentHandler) HandleGetCheckpoints(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	taskID := chi.URLParam(r, "taskId")

	if h.checkpointRepo == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Checkpoint repository không khả dụng",
		})
		return
	}

	list, err := h.checkpointRepo.ListCheckpoints(r.Context(), taskID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"task_id":     taskID,
		"checkpoints": list,
		"total":       len(list),
	})
}

// HandleGetCoreMemory xử lý GET /v1/agent/memory/core
func (h *AgentHandler) HandleGetCoreMemory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.memorySvc == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "MemoryService chưa được kích hoạt"})
		return
	}
	_ = json.NewEncoder(w).Encode(h.memorySvc.GetCoreMemoryForContext(r.Context()))
}

// HandleUpdateCoreMemory xử lý POST /v1/agent/memory/core
func (h *AgentHandler) HandleUpdateCoreMemory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.memorySvc == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "MemoryService chưa được kích hoạt"})
		return
	}

	var payload struct {
		Target  string `json:"target"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "JSON không hợp lệ: " + err.Error()})
		return
	}

	h.memorySvc.UpdateCoreMemoryForContext(r.Context(), func(core *domain.CoreMemory) {
		switch payload.Target {
		case "scratchpad":
			core.Scratchpad = payload.Content
		case "human_profile":
			core.HumanProfile = payload.Content
		case "project_context":
			core.ProjectContext = payload.Content
		case "persona":
			core.Persona = payload.Content
		}
	})

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"core":    h.memorySvc.GetCoreMemoryForContext(r.Context()),
	})
}

// HandleSearchArchival xử lý GET /v1/agent/memory/search?q=...
func (h *AgentHandler) HandleSearchArchival(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.memorySvc == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "MemoryService chưa được kích hoạt"})
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Thiếu tham số 'q'"})
		return
	}

	results, err := h.memorySvc.SearchArchival(r.Context(), query, 10)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"query":   query,
		"results": results,
		"total":   len(results),
	})
}

// HandleStoreArchival xử lý POST /v1/agent/memory/store
func (h *AgentHandler) HandleStoreArchival(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.memorySvc == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "MemoryService chưa được kích hoạt"})
		return
	}

	var payload struct {
		Key     string   `json:"key"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "JSON không hợp lệ: " + err.Error()})
		return
	}

	if payload.Key == "" || payload.Content == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Trường 'key' và 'content' là bắt buộc"})
		return
	}

	if err := h.memorySvc.StoreArchival(r.Context(), payload.Key, payload.Content, payload.Tags); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Đã lưu vào Archival Memory thành công",
		"key":     payload.Key,
	})
}

// SetSubagentSupervisor thiết lập supervisor điều phối sub-agents
func (h *AgentHandler) SetSubagentSupervisor(sup ports.SubagentSupervisor) {
	h.subagentSup = sup
}

// HandleListSubagents xử lý GET /v1/agent/subagents
func (h *AgentHandler) HandleListSubagents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.subagentSup == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "SubagentSupervisor chưa được kích hoạt"})
		return
	}

	roles := h.subagentSup.ListRoles()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"roles": roles,
		"total": len(roles),
	})
}

// SubagentRunRequest chứa tham số gửi tới POST /v1/agent/subagents/run
type SubagentRunRequest struct {
	Role       string `json:"role"`
	Prompt     string `json:"prompt"`
	Context    string `json:"context,omitempty"`
	Workspace  string `json:"workspace,omitempty"`
	Model      string `json:"model,omitempty"`
	Supervised bool   `json:"supervised,omitempty"`
}

// HandleInvokeSubagent xử lý POST /v1/agent/subagents/run
func (h *AgentHandler) HandleInvokeSubagent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.subagentSup == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "SubagentSupervisor chưa được kích hoạt"})
		return
	}

	var req SubagentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "JSON không hợp lệ: " + err.Error()})
		return
	}

	if req.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Tham số 'prompt' không được để trống"})
		return
	}

	result, err := h.subagentSup.InvokeSubagent(r.Context(), domain.SubagentTask{
		Role:       domain.SubagentRole(req.Role),
		Prompt:     req.Prompt,
		Context:    req.Context,
		Workspace:  req.Workspace,
		Model:      req.Model,
		Supervised: req.Supervised,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  err.Error(),
			"result": result,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(result)
}

// HandleSandboxDiff xử lý GET /v1/agent/sandbox/diff/{taskId}
func (h *AgentHandler) HandleSandboxDiff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	taskID := chi.URLParam(r, "taskId")
	mgr := sandbox.GetDefaultManager()
	sb, err := mgr.GetSandboxForTenant(r.Context(), taskID)
	if err != nil || sb == nil {
		w.WriteHeader(http.StatusNotFound)
		msg := fmt.Sprintf("Không tìm thấy sandbox cho task %s", taskID)
		if err != nil {
			msg = err.Error()
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	diff, err := mgr.GetDiff(r.Context(), sb)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"task_id":   taskID,
		"branch":    sb.BranchName,
		"worktree":  sb.WorktreePath,
		"is_active": sb.IsActive,
		"git_diff":  diff,
	})
}

// HandleSandboxMerge xử lý POST /v1/agent/sandbox/merge
func (h *AgentHandler) HandleSandboxMerge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Yêu cầu cung cấp task_id hợp lệ"})
		return
	}
	mgr := sandbox.GetDefaultManager()
	sb, err := mgr.GetSandboxForTenant(r.Context(), req.TaskID)
	if err != nil || sb == nil {
		w.WriteHeader(http.StatusNotFound)
		msg := fmt.Sprintf("Không tìm thấy sandbox cho task %s", req.TaskID)
		if err != nil {
			msg = err.Error()
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	if err := mgr.ApplyMerge(r.Context(), sb); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": fmt.Sprintf("Đã gộp thành công nhánh %s vào nhánh chính và xóa bỏ worktree.", sb.BranchName),
		"task_id": req.TaskID,
	})
}

// HandleSandboxRollback xử lý POST /v1/agent/sandbox/rollback
func (h *AgentHandler) HandleSandboxRollback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Yêu cầu cung cấp task_id hợp lệ"})
		return
	}
	mgr := sandbox.GetDefaultManager()
	sb, err := mgr.GetSandboxForTenant(r.Context(), req.TaskID)
	if err != nil || sb == nil {
		w.WriteHeader(http.StatusNotFound)
		msg := fmt.Sprintf("Không tìm thấy sandbox cho task %s", req.TaskID)
		if err != nil {
			msg = err.Error()
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	if err := mgr.Rollback(r.Context(), sb); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": fmt.Sprintf("Đã hủy bỏ toàn bộ thay đổi và xóa worktree của task %s.", req.TaskID),
		"task_id": req.TaskID,
	})
}

func (h *AgentHandler) resolveTenant(r *http.Request) (string, error) {
	identity, ok := domain.TenantIdentityFromContext(r.Context())
	if !ok || strings.TrimSpace(identity.TenantID) == "" {
		return "", errors.New("yêu cầu định danh tenant hợp lệ (thiếu thông tin tenant_id trong context xác thực)")
	}
	return strings.TrimSpace(identity.TenantID), nil
}

// HandleCreateRun xử lý POST /v1/agent/runs (Async Job Submission)
func (h *AgentHandler) HandleCreateRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.jobService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Async Agent Job System chưa được kích hoạt trên gateway",
		})
		return
	}

	var req AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Payload JSON không hợp lệ: " + err.Error(),
		})
		return
	}

	if strings.TrimSpace(req.Goal) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Trường 'goal' là bắt buộc khi khởi tạo agent run",
		})
		return
	}

	opts, err := h.resolveEffectiveOptions(r.Context(), req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	idempKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempKey == "" {
		idempKey = strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	}
	if idempKey == "" {
		idempKey = strings.TrimSpace(req.IdempotencyKey)
	}
	run, err := h.jobService.SubmitRun(r.Context(), req.Goal, opts, idempKey)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Lỗi khởi tạo agent run: " + err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(run)
}

// HandleGetRun xử lý GET /v1/agent/runs/{id}
func (h *AgentHandler) HandleGetRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.jobService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Async Agent Job System chưa được kích hoạt trên gateway",
		})
		return
	}

	tenantID, err := h.resolveTenant(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	runID := chi.URLParam(r, "id")
	run, err := h.jobService.GetRunForTenant(r.Context(), tenantID, runID)
	if err != nil || run == nil {
		w.WriteHeader(http.StatusNotFound)
		msg := "Không tìm thấy agent run"
		if err != nil && !errors.Is(err, session.ErrRunNotFound) && !strings.Contains(err.Error(), "not found") {
			msg = err.Error()
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": msg,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(run)
}

// HandleListRuns xử lý GET /v1/agent/runs
func (h *AgentHandler) HandleListRuns(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.runRepo == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Agent Run Repository chưa được cấu hình",
		})
		return
	}

	tenantID, err := h.resolveTenant(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	runs, err := h.runRepo.List(r.Context(), tenantID, 50, 0)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Lỗi lấy danh sách runs: " + err.Error(),
		})
		return
	}

	if runs == nil {
		runs = []*domain.AgentRun{}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  runs,
		"count": len(runs),
	})
}

// HandleCancelRun xử lý POST /v1/agent/runs/{id}/cancel
func (h *AgentHandler) HandleCancelRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.jobService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Async Agent Job System chưa được kích hoạt trên gateway",
		})
		return
	}

	tenantID, err := h.resolveTenant(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	runID := chi.URLParam(r, "id")
	if err := h.jobService.CancelRunForTenant(r.Context(), tenantID, runID); err != nil {
		if errors.Is(err, session.ErrRunNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Không tìm thấy agent run hoặc không thuộc tenant này",
			})
			return
		}
		if errors.Is(err, session.ErrInvalidStatusTransition) || strings.Contains(strings.ToLower(err.Error()), "invalid status transition") {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid_status_transition: " + err.Error(),
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Lỗi khi hủy agent run: " + err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"run_id":  runID,
		"status":  domain.RunStatusCancelled,
	})
}

// HandleResumeRun xử lý POST /v1/agent/runs/{id}/resume
func (h *AgentHandler) HandleResumeRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.jobService == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Async Agent Job System chưa được kích hoạt trên gateway",
		})
		return
	}

	tenantID, err := h.resolveTenant(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	runID := chi.URLParam(r, "id")
	var req struct {
		Feedback string `json:"feedback,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	newRun, err := h.jobService.ResumeRunForTenant(r.Context(), tenantID, runID, req.Feedback)
	if err != nil {
		if errors.Is(err, session.ErrRunNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Không tìm thấy agent run hoặc không thuộc tenant này",
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Không thể resume agent run: " + err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(newRun)
}

// HandleStreamRunEvents xử lý GET /v1/agent/runs/{id}/events (SSE)
func (h *AgentHandler) HandleStreamRunEvents(w http.ResponseWriter, r *http.Request) {
	if h.jobService == nil || h.runRepo == nil {
		http.Error(w, "Async Agent Job System chưa được kích hoạt trên gateway", http.StatusServiceUnavailable)
		return
	}

	tenantID, err := h.resolveTenant(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	runID := chi.URLParam(r, "id")
	run, err := h.jobService.GetRunForTenant(r.Context(), tenantID, runID)
	if err != nil || run == nil {
		http.Error(w, "Không tìm thấy agent run", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		if u, hasUnwrap := w.(interface{ Unwrap() http.ResponseWriter }); hasUnwrap {
			flusher, ok = u.Unwrap().(http.Flusher)
		}
	}
	if !ok {
		http.Error(w, "Streaming không được hỗ trợ", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Parse Last-Event-ID nếu client gửi để khôi phục stream sau khi đứt kết nối
	var startAfterID int64 = 0
	if lastEventIDStr := r.Header.Get("Last-Event-ID"); lastEventIDStr != "" {
		if val, parseErr := strconv.ParseInt(strings.TrimSpace(lastEventIDStr), 10, 64); parseErr == nil && val > 0 {
			startAfterID = val
		}
	}

	// 1. Subscribe realtime TRƯỚC để không bỏ lỡ bất kỳ event nào xảy ra giữa lịch sử và đăng ký
	eventsCh, unsubscribe, err := h.jobService.SubscribeEventsForTenant(r.Context(), tenantID, runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer unsubscribe()

	// 2. Gửi các sự kiện lịch sử đã lưu trước đó có kiểm tra tenant isolation và Last-Event-ID
	existingEvents, _ := h.runRepo.GetEventsForTenant(r.Context(), tenantID, runID, startAfterID)
	lastSentID := startAfterID
	for _, ev := range existingEvents {
		if ev.ID > lastSentID {
			lastSentID = ev.ID
		}
		data, _ := json.Marshal(ev)
		_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.ID, data)
		flusher.Flush()
	}

	// Nếu run đã kết thúc, kiểm tra lần cuối từ DB và kết thúc stream
	latestRun, _ := h.jobService.GetRunForTenant(r.Context(), tenantID, runID)
	if latestRun != nil && (latestRun.Status == domain.RunStatusCompleted || latestRun.Status == domain.RunStatusFailed || latestRun.Status == domain.RunStatusCancelled) {
		moreEvents, _ := h.runRepo.GetEventsForTenant(r.Context(), tenantID, runID, lastSentID)
		for _, ev := range moreEvents {
			if ev.ID > lastSentID {
				lastSentID = ev.ID
				data, _ := json.Marshal(ev)
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.ID, data)
				flusher.Flush()
			}
		}
		_, _ = fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
		flusher.Flush()
		return
	}

	// 3. Xử lý các sự kiện realtime từ channel với cơ chế khử trùng lặp và tự động backfill nếu có gap
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-eventsCh:
			if !ok {
				// Kênh channel đã đóng (run hoàn tất) -> backfill kiểm tra các sự kiện cuối cùng từ DB
				finalEvents, _ := h.runRepo.GetEventsForTenant(r.Context(), tenantID, runID, lastSentID)
				for _, fev := range finalEvents {
					if fev.ID > lastSentID {
						lastSentID = fev.ID
						data, _ := json.Marshal(fev)
						_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", fev.ID, data)
						flusher.Flush()
					}
				}
				_, _ = fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
				flusher.Flush()
				return
			}

			// Nếu phát hiện gap giữa lastSentID và ev.ID (do channel đầy hoặc broadcast drop) -> backfill từ DB
			if ev.ID > lastSentID+1 {
				missed, _ := h.runRepo.GetEventsForTenant(r.Context(), tenantID, runID, lastSentID)
				for _, mEv := range missed {
					if mEv.ID > lastSentID && mEv.ID < ev.ID {
						lastSentID = mEv.ID
						data, _ := json.Marshal(mEv)
						_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", mEv.ID, data)
						flusher.Flush()
					}
				}
			}

			if ev.ID > lastSentID {
				lastSentID = ev.ID
				data, _ := json.Marshal(ev)
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.ID, data)
				flusher.Flush()
			}
		}
	}
}
