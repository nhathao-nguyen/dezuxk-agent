package google_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/google"
)

func TestParseEnvelopeChunk_FullMeta(t *testing.T) {
	// Sample wrb.fr payload with c_, r_, rc_ and text
	sampleLine := `[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_12345\",\"r_67890\"],null,null,[[\"rc_abcde\",[\"Xin chào bạn!\"]]]]"]]`

	meta := google.ParseEnvelopeChunk(sampleLine)
	if meta.ConversationID != "c_12345" {
		t.Errorf("expected ConversationID c_12345, got %q", meta.ConversationID)
	}
	if meta.ResponseID != "r_67890" {
		t.Errorf("expected ResponseID r_67890, got %q", meta.ResponseID)
	}
	if meta.ChoiceID != "rc_abcde" {
		t.Errorf("expected ChoiceID rc_abcde, got %q", meta.ChoiceID)
	}
	if meta.Text != "Xin chào bạn!" {
		t.Errorf("expected Text 'Xin chào bạn!', got %q", meta.Text)
	}
}

func TestParseEnvelopeChunk_Thinking(t *testing.T) {
	sampleLine := `[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_123\",\"r_456\"],null,null,[[\"rc_789\",[\"Câu trả lời\"],null,null,null,[{\"thought_content\":\"Bước 1: Phân tích\",\"is_thinking\":true}]]]]"]]`

	meta := google.ParseEnvelopeChunk(sampleLine)
	if !strings.Contains(meta.ThinkingContent, "Bước 1: Phân tích") {
		t.Errorf("expected ThinkingContent to contain 'Bước 1: Phân tích', got %q", meta.ThinkingContent)
	}
	if len(meta.ThinkingBlocks) == 0 || meta.ThinkingBlocks[0].Content != "Bước 1: Phân tích" {
		t.Errorf("expected ThinkingBlocks to be populated, got %+v", meta.ThinkingBlocks)
	}
}

func TestParseEnvelopeChunk_CodeExecution(t *testing.T) {
	sampleLine := `[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_code\",\"r_code\"],null,null,[[\"rc_code\",[\"Kết quả chạy mã Python bên dưới:\"],null,null,null,null,null,null,null,null,[{\"code_block\":{\"language\":\"python\",\"code\":\"import numpy as np\\nprint('Hello Numpy')\"},\"execution_result\":{\"exit_code\":0,\"stdout\":\"Hello Numpy\\n\",\"output_images\":[{\"mime_type\":\"image/png\",\"image_format\":\"base64\",\"data\":\"iVBORw0KGgoAAAANSUhEUg==\"}]}}]]]]"]]`

	meta := google.ParseEnvelopeChunk(sampleLine)
	if len(meta.CodeExecutions) == 0 {
		t.Fatalf("expected CodeExecutions, got 0")
	}
	exec := meta.CodeExecutions[0]
	if exec.Language != "python" || !strings.Contains(exec.Code, "import numpy") {
		t.Errorf("code block mismatch: %+v", exec)
	}
	if exec.ExitCode != 0 || !strings.Contains(exec.Stdout, "Hello Numpy") {
		t.Errorf("execution result mismatch: %+v", exec)
	}
	if len(exec.Images) == 0 || exec.Images[0].MimeType != "image/png" {
		t.Errorf("output images mismatch: %+v", exec.Images)
	}
}

func TestParseEnvelopeChunk_Grounding(t *testing.T) {
	sampleLine := `[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_ground\",\"r_ground\"],null,null,[[\"rc_ground\",[\"Thời tiết hôm nay 28 độ C theo VnExpress [1]\"],null,null,null,null,null,null,null,null,null,null,[[\"thời tiết Hà Nội hôm nay\"],null,[[1,[\"https://vnexpress.net/thoi-tiet\",\"Dự báo thời tiết Hà Nội\",\"Nhiệt độ hiện tại 28 độ C\",\"https://google.com/favicon.ico\",\"vnexpress.net\"]]],[{\"segment\":{\"start_index\":0,\"end_index\":30,\"text\":\"Thời tiết hôm nay 28 độ C\"},\"grounding_chunk_indices\":[1],\"confidence_scores\":[0.95]}]]]]]"]]`

	meta := google.ParseEnvelopeChunk(sampleLine)
	if meta.Grounding == nil {
		t.Fatalf("expected Grounding metadata, got nil")
	}
	if len(meta.Grounding.SearchQueries) == 0 || meta.Grounding.SearchQueries[0] != "thời tiết Hà Nội hôm nay" {
		t.Errorf("search queries mismatch: %+v", meta.Grounding.SearchQueries)
	}
	if len(meta.Grounding.Sources) == 0 {
		t.Fatalf("expected Grounding Sources, got 0")
	}
	src := meta.Grounding.Sources[0]
	if src.URL != "https://vnexpress.net/thoi-tiet" || src.Domain != "vnexpress.net" || src.Title != "Dự báo thời tiết Hà Nội" {
		t.Errorf("grounding source mismatch: %+v", src)
	}
	if len(meta.Grounding.Supports) == 0 || meta.Grounding.Supports[0].SegmentText != "Thời tiết hôm nay 28 độ C" {
		t.Errorf("grounding supports mismatch: %+v", meta.Grounding.Supports)
	}
}

func TestParseEnvelopeChunk_GeneratedImages(t *testing.T) {
	expectedURL := "https://lh3.googleusercontent.com/gg-dl/AAQ_wbF8yNvZMEL5rfopm2K9oVzan_cPxjiyfYK9x05kCvwfHvbZGPpR92Fj3Zyyt2xKWXXOm1qq24CAeL2Z0mJywh_4MblKH_q0qxGpkEPR1CKvUT84si3ALrzc3g2r9X_WndVMgO5Btmd3Mfn5HuaHjI-072arq6FyBmjtEF7OK-tuTzU-ZA"
	placeholder := "http://googleusercontent.com/image_generation_content/0_624"

	innerObj := []interface{}{
		nil,
		[]interface{}{"c_cat", "r_cat"},
		nil,
		nil,
		[]interface{}{
			[]interface{}{
				"rc_cat",
				[]interface{}{"\n\n" + placeholder + "\n\n"},
				nil, nil, nil, nil, nil, nil,
				[]interface{}{1},
				nil, nil, nil,
				[]interface{}{
					nil, nil, nil, nil, nil, nil, nil,
					[]interface{}{
						[]interface{}{
							[]interface{}{
								[]interface{}{
									nil, nil, nil,
									[]interface{}{
										nil, 1, "cat_art.png",
										expectedURL,
										nil, "token", nil, nil, nil,
										[]interface{}{1790308899, 482767390},
										nil, "image/png", nil, nil, nil,
										[]interface{}{1024, 1024, 12345},
									},
								},
							},
							[]interface{}{placeholder},
						},
					},
				},
			},
		},
	}
	innerBytes, _ := json.Marshal(innerObj)
	envelopeObj := [][]interface{}{
		{"wrb.fr", "assistant.lamda.BardFrontendService", string(innerBytes)},
	}
	lineBytes, _ := json.Marshal(envelopeObj)
	sampleLine := string(lineBytes)

	meta := google.ParseEnvelopeChunk(sampleLine)
	if len(meta.MediaURLs) == 0 {
		t.Fatalf("expected MediaURLs, got 0")
	}
	if meta.MediaURLs[0] != expectedURL {
		t.Errorf("expected MediaURL %q, got %q", expectedURL, meta.MediaURLs[0])
	}
	if strings.Contains(meta.Text, placeholder) {
		t.Errorf("placeholder URL was not replaced in Text: %q", meta.Text)
	}
	if !strings.Contains(meta.Text, "![Hình ảnh]") || !strings.Contains(meta.Text, expectedURL) {
		t.Errorf("expected markdown image in Text, got %q", meta.Text)
	}
}
