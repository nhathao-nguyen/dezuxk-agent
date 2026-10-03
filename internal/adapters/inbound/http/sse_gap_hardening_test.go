package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"

	"github.com/go-chi/chi/v5"
)

// Test 11 (HTTP level): Cancel Completed Run returns 409 Conflict
func TestHardening_CancelCompletedRunReturns409(t *testing.T) {
	handler, _, runRepo, _ := setupTenantTestHandler(t)

	r := chi.NewRouter()
	r.Post("/v1/agent/runs/{id}/cancel", handler.HandleCancelRun)

	ctx := context.Background()

	// 1. Tạo 1 run đã ở trạng thái completed
	run := &domain.AgentRun{
		ID:        "run_already_completed",
		TenantID:  "tenant-409-test",
		Goal:      "Already done goal",
		Status:    domain.RunStatusCompleted,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// 2. Client gửi yêu cầu cancel
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+run.ID+"/cancel", nil)
	req = withTenantCtx(req, "tenant-409-test", "user")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	// Phải trả về HTTP 409 Conflict
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected HTTP 409 Conflict, got: %d, body: %s", rec.Code, rec.Body.String())
	}

	var respBody map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&respBody)
	if !strings.Contains(respBody["error"], "invalid_status_transition") {
		t.Fatalf("expected error body to contain invalid_status_transition, got: %v", respBody)
	}
}

// Test 12: SSE Gap, Replay & Last-Event-ID Deduplication
func TestHardening_SSEEventStreamGapAndDedupe(t *testing.T) {
	runRepo := session.NewMemoryAgentRunRepository()
	runner := &mockAgentRunner{}
	jobSvc := agent.NewJobService(runRepo, runner)

	handler := NewAgentHandler(runner, nil, nil, nil, nil)
	handler.SetJobService(jobSvc, runRepo)

	r := chi.NewRouter()
	r.Get("/v1/agent/runs/{id}/events", handler.HandleStreamRunEvents)

	ctx := context.Background()
	tenantID := "tenant-sse-gap"
	runID := "run_sse_test_123"

	run := &domain.AgentRun{
		ID:        runID,
		TenantID:  tenantID,
		Goal:      "SSE Stream Test Goal",
		Status:    domain.RunStatusRunning,
		Model:     "gemini-3.8-flash",
		Workspace: ".",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Lưu 3 sự kiện ban đầu vào repo (ID: 1, 2, 3)
	ev1 := &domain.AgentRunEvent{TenantID: tenantID, RunID: runID, Step: 1, Kind: "thinking", Message: "Event 1", Timestamp: time.Now()}
	ev2 := &domain.AgentRunEvent{TenantID: tenantID, RunID: runID, Step: 1, Kind: "tool_start", Message: "Event 2", Timestamp: time.Now()}
	ev3 := &domain.AgentRunEvent{TenantID: tenantID, RunID: runID, Step: 1, Kind: "tool_end", Message: "Event 3", Timestamp: time.Now()}
	_ = runRepo.AppendEvent(ctx, ev1)
	_ = runRepo.AppendEvent(ctx, ev2)
	_ = runRepo.AppendEvent(ctx, ev3)

	// Trường hợp 1: Client kết nối với Last-Event-ID = 1
	// Client phải nhận được Event 2 và 3, KHÔNG được nhận lại Event 1
	req := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/"+runID+"/events", nil)
	req = withTenantCtx(req, tenantID, "user")
	req.Header.Set("Last-Event-ID", "1")

	// Sử dụng context có timeout để đóng stream sau khi đọc
	streamCtx, cancelStream := context.WithTimeout(req.Context(), 150*time.Millisecond)
	defer cancelStream()
	req = req.WithContext(streamCtx)

	rec := httptest.NewRecorder()

	// Chạy handler
	r.ServeHTTP(rec, req)

	bodyStr := rec.Body.String()

	// Event 1 (id: 1) KHÔNG được xuất hiện
	if strings.Contains(bodyStr, "id: 1\n") {
		t.Fatalf("Event 1 with id: 1 should NOT be returned when Last-Event-ID is 1! Body:\n%s", bodyStr)
	}

	// Event 2 (id: 2) và Event 3 (id: 3) PHẢI xuất hiện
	if !strings.Contains(bodyStr, "id: 2\n") {
		t.Fatalf("Event 2 with id: 2 was missing from stream! Body:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "id: 3\n") {
		t.Fatalf("Event 3 with id: 3 was missing from stream! Body:\n%s", bodyStr)
	}

	// Kiểm tra định dạng SSE hợp lệ
	scanner := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	foundIDs := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			foundIDs = append(foundIDs, strings.TrimPrefix(line, "id: "))
		}
	}

	// Các ID nhận được phải là 2 và 3 theo đúng thứ tự
	if len(foundIDs) < 2 || foundIDs[0] != "2" || foundIDs[1] != "3" {
		t.Fatalf("unexpected order or content of event IDs: %v", foundIDs)
	}
}
