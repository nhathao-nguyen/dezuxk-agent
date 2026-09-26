package domain_test

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestFeedbackRequestAndResponse(t *testing.T) {
	req, err := domain.BuildFeedbackRequest("c_123", "r_456", "rc_789", 2, []int{1, 4}, "Chưa chuẩn", "vi")
	if err != nil {
		t.Fatalf("BuildFeedbackRequest thất bại: %v", err)
	}

	if !strings.Contains(req, "uP80Sb") || !strings.Contains(req, "c_123") || !strings.Contains(req, "Chưa chuẩn") {
		t.Fatalf("payload feedback không đúng: %s", req)
	}

	ok, err := domain.ParseFeedbackResponse(`[1]`)
	if err != nil || !ok {
		t.Errorf("ParseFeedbackResponse thất bại: %v", err)
	}
}
