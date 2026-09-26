package domain_test

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestCanvasCreateRequestAndResponse(t *testing.T) {
	req, err := domain.BuildCreateCanvasRequest("c_123", "Thiết kế kiến trúc", "MARKDOWN", "# Kế hoạch")
	if err != nil {
		t.Fatalf("BuildCreateCanvasRequest thất bại: %v", err)
	}

	if !strings.Contains(req, "tVk3Sc") || !strings.Contains(req, "c_123") || !strings.Contains(req, "Thiết kế kiến trúc") {
		t.Fatalf("payload không đúng: %s", req)
	}

	// Test parse response
	fixture := `[["wrb.fr", "tVk3Sc", "[[\"canvas_987654321\", 1, 1790172500000]]", null, null, null, "generic"]]`
	canvasID, ver, err := domain.ParseCreateCanvasResponse(fixture)
	if err != nil {
		t.Fatalf("ParseCreateCanvasResponse thất bại: %v", err)
	}
	if canvasID != "canvas_987654321" || ver != 1 {
		t.Errorf("kết quả parse canvas không đúng: id=%s ver=%d", canvasID, ver)
	}
}

func TestCanvasUpdateDeltaRequestAndResponse(t *testing.T) {
	diffOps := []domain.CanvasDiffOp{
		{Op: "retain", Count: 10},
		{Op: "delete", Count: 5},
		{Op: "insert", Text: "thay thế mới"},
	}
	req, err := domain.BuildUpdateCanvasDeltaRequest("canvas_987654321", 1, diffOps)
	if err != nil {
		t.Fatalf("BuildUpdateCanvasDeltaRequest thất bại: %v", err)
	}

	if !strings.Contains(req, "sA4a8") || !strings.Contains(req, "canvas_987654321") || !strings.Contains(req, "thay thế mới") {
		t.Fatalf("payload delta không đúng: %s", req)
	}

	fixture := `[["wrb.fr", "sA4a8", "[[\"canvas_987654321\", 2, \"SUCCESS\"]]", null, null, null, "generic"]]`
	cid, ver, status, err := domain.ParseUpdateCanvasDeltaResponse(fixture)
	if err != nil {
		t.Fatalf("ParseUpdateCanvasDeltaResponse thất bại: %v", err)
	}
	if cid != "canvas_987654321" || ver != 2 || status != "SUCCESS" {
		t.Errorf("kết quả update delta không đúng: id=%s ver=%d status=%s", cid, ver, status)
	}
}

func TestCanvasPublishRequestAndResponse(t *testing.T) {
	req, err := domain.BuildPublishCanvasRequest("canvas_987654321", 1, true)
	if err != nil {
		t.Fatalf("BuildPublishCanvasRequest thất bại: %v", err)
	}

	if !strings.Contains(req, "H8s0fe") || !strings.Contains(req, "canvas_987654321") {
		t.Fatalf("payload publish không đúng: %s", req)
	}

	fixture := `[["wrb.fr", "H8s0fe", "[[\"https://gemini.google.com/share/canvas/pub12345\", 1790172550000]]", null, null, null, "generic"]]`
	shareURL, err := domain.ParsePublishCanvasResponse(fixture)
	if err != nil {
		t.Fatalf("ParsePublishCanvasResponse thất bại: %v", err)
	}
	if shareURL != "https://gemini.google.com/share/canvas/pub12345" {
		t.Errorf("share URL không đúng: %s", shareURL)
	}
}
