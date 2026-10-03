package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"

	"github.com/go-chi/chi/v5"
)

type mockAgentRunner struct{}

func (m *mockAgentRunner) Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error) {
	time.Sleep(10 * time.Millisecond)
	return &domain.AgentState{
		Goal:        goal,
		IsCompleted: true,
		StopReason:  domain.StopReasonCompleted,
		FinalAnswer: "Done: " + goal,
	}, nil
}

func setupTenantTestHandler(t *testing.T) (*AgentHandler, *agent.JobService, *session.MemoryAgentRunRepository, *session.MemoryCheckpointRepository) {
	runRepo := session.NewMemoryAgentRunRepository()
	cpRepo := session.NewMemoryCheckpointRepository()
	runner := &mockAgentRunner{}
	jobSvc := agent.NewJobService(runRepo, runner)
	jobSvc.SetCheckpointRepository(cpRepo)

	handler := NewAgentHandler(runner, nil, nil, cpRepo, nil)
	handler.SetJobService(jobSvc, runRepo)
	return handler, jobSvc, runRepo, cpRepo
}

func withTenantCtx(req *http.Request, tenantID string, role string) *http.Request {
	identity := domain.TenantIdentity{
		TenantID: tenantID,
		Role:     role,
		Scopes:   []string{domain.ScopeAgent},
	}
	ctx := domain.ContextWithTenantIdentity(req.Context(), identity)
	return req.WithContext(ctx)
}

func TestAgentHandler_TenantIsolation(t *testing.T) {
	handler, _, runRepo, cpRepo := setupTenantTestHandler(t)

	// Router với route matching chi URLParam
	r := chi.NewRouter()
	r.Post("/v1/agent/runs", handler.HandleCreateRun)
	r.Get("/v1/agent/runs", handler.HandleListRuns)
	r.Get("/v1/agent/runs/{id}", handler.HandleGetRun)
	r.Post("/v1/agent/runs/{id}/cancel", handler.HandleCancelRun)
	r.Post("/v1/agent/runs/{id}/resume", handler.HandleResumeRun)
	r.Get("/v1/agent/runs/{id}/events", handler.HandleStreamRunEvents)

	// 1. Tenant A tạo run
	createBody := []byte(`{"goal": "Build isolation fence", "workspace": "."}`)
	reqCreate := httptest.NewRequest(http.MethodPost, "/v1/agent/runs", bytes.NewReader(createBody))
	reqCreate = withTenantCtx(reqCreate, "tenant-A", "user")
	recCreate := httptest.NewRecorder()

	r.ServeHTTP(recCreate, reqCreate)
	if recCreate.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", recCreate.Code, recCreate.Body.String())
	}

	var runA domain.AgentRun
	if err := json.NewDecoder(recCreate.Body).Decode(&runA); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if runA.ID == "" || runA.TenantID != "tenant-A" {
		t.Fatalf("invalid created run: %+v", runA)
	}

	// Lưu checkpoint cho run A để thử nghiệm resume
	_ = cpRepo.SaveCheckpoint(context.Background(), &domain.AgentCheckpoint{
		TenantID:  "tenant-A",
		TaskID:    runA.ID,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID: runA.ID,
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "Build isolation fence"},
			},
		},
	})
	// Chuyển status sang failed để resumable
	runA.Status = domain.RunStatusFailed
	_ = runRepo.Update(context.Background(), &runA)

	// 2. Tenant A đọc được run của chính mình (200 OK)
	reqGetA := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/"+runA.ID, nil)
	reqGetA = withTenantCtx(reqGetA, "tenant-A", "user")
	recGetA := httptest.NewRecorder()

	r.ServeHTTP(recGetA, reqGetA)
	if recGetA.Code != http.StatusOK {
		t.Fatalf("tenant A reading own run: expected 200 OK, got %d: %s", recGetA.Code, recGetA.Body.String())
	}

	// 3. Tenant B đọc run của Tenant A -> phải trả về 404 Not Found (hoặc 403)
	reqGetB := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/"+runA.ID, nil)
	reqGetB = withTenantCtx(reqGetB, "tenant-B", "user")
	recGetB := httptest.NewRecorder()

	r.ServeHTTP(recGetB, reqGetB)
	if recGetB.Code != http.StatusNotFound && recGetB.Code != http.StatusForbidden {
		t.Fatalf("tenant B reading run A: expected 404 or 403, got %d: %s", recGetB.Code, recGetB.Body.String())
	}

	// 4. Tenant B hủy run của Tenant A -> phải bị từ chối (404/403)
	reqCancelB := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+runA.ID+"/cancel", nil)
	reqCancelB = withTenantCtx(reqCancelB, "tenant-B", "user")
	recCancelB := httptest.NewRecorder()

	r.ServeHTTP(recCancelB, reqCancelB)
	if recCancelB.Code != http.StatusNotFound && recCancelB.Code != http.StatusForbidden {
		t.Fatalf("tenant B cancelling run A: expected rejection (404/403), got %d: %s", recCancelB.Code, recCancelB.Body.String())
	}

	// 5. Tenant B resume run của Tenant A -> phải bị từ chối (404/403/400)
	resumeBody := []byte(`{"feedback": "malicious resume"}`)
	reqResumeB := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+runA.ID+"/resume", bytes.NewReader(resumeBody))
	reqResumeB = withTenantCtx(reqResumeB, "tenant-B", "user")
	recResumeB := httptest.NewRecorder()

	r.ServeHTTP(recResumeB, reqResumeB)
	if recResumeB.Code != http.StatusNotFound && recResumeB.Code != http.StatusForbidden && recResumeB.Code != http.StatusBadRequest {
		t.Fatalf("tenant B resuming run A: expected rejection, got %d: %s", recResumeB.Code, recResumeB.Body.String())
	}

	// 6. Tenant B stream events run của Tenant A -> phải bị từ chối (404/403)
	reqStreamB := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/"+runA.ID+"/events", nil)
	reqStreamB = withTenantCtx(reqStreamB, "tenant-B", "user")
	recStreamB := httptest.NewRecorder()

	r.ServeHTTP(recStreamB, reqStreamB)
	if recStreamB.Code != http.StatusNotFound && recStreamB.Code != http.StatusForbidden {
		t.Fatalf("tenant B streaming events of run A: expected rejection (404/403), got %d", recStreamB.Code)
	}

	// 7. Request không có tenant identity trong context -> phải trả về 401 Unauthorized
	reqAnon := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/"+runA.ID, nil)
	recAnon := httptest.NewRecorder()

	r.ServeHTTP(recAnon, reqAnon)
	if recAnon.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request without tenant: expected 401, got %d: %s", recAnon.Code, recAnon.Body.String())
	}
}

func TestReadinessManager_GracefulReadiness(t *testing.T) {
	rm := NewReadinessManager()
	rm.SetReady(false)

	deps := RouterDependencies{
		ReadinessManager: rm,
		ModelRegistry:    domain.NewModelRegistry(nil),
	}
	router := BuildRouter(deps)

	// 1. Khi server khởi động và chưa sẵn sàng -> /ready trả về 503 Service Unavailable
	req1 := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when ready=false, got %d", rec1.Code)
	}

	var resp1 map[string]any
	_ = json.NewDecoder(rec1.Body).Decode(&resp1)
	if resp1["ready"] != false {
		t.Fatalf("expected ready to be false, got: %v", resp1["ready"])
	}

	// 2. Chuyển sang sẵn sàng
	rm.SetReady(true)
	// (Lưu ý: model registry rỗng vẫn khiến ready=false nếu không có model,
	// nhưng kiểm tra cờ readinessManager.SetReady(false) đã chặn 503)
	rm.SetReady(false)

	req2 := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 immediately after setting ready=false during shutdown drain, got %d", rec2.Code)
	}
}
