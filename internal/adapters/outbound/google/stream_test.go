package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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

func TestComputeDelta_NoPrefixReplay(t *testing.T) {
	// 1. Bình thường: text mới có tiền tố là text cũ
	delta1 := google.ComputeDelta("Xin chào bạn", "Xin chào ")
	if delta1 != "bạn" {
		t.Errorf("expected 'bạn', got %q", delta1)
	}

	// 2. Chèn ảnh Markdown vào giữa: currentText chứa lastText nhưng bị lệch
	delta2 := google.ComputeDelta("Xem ảnh này: ![anh](https://img.png) đẹp không", "Xem ảnh này: ")
	if delta2 != "![anh](https://img.png) đẹp không" {
		t.Errorf("expected image markdown delta, got %q", delta2)
	}

	// 3. Tiền tố chung dài nhất: không lặp lại tiền tố đã phát
	delta3 := google.ComputeDelta("Dự báo thời tiết hôm nay trời nắng", "Dự báo thời tiết hôm nay ")
	if delta3 != "trời nắng" {
		t.Errorf("expected 'trời nắng', got %q", delta3)
	}

	// 4. Lần đầu tiên phát text
	delta4 := google.ComputeDelta("Bắt đầu", "")
	if delta4 != "Bắt đầu" {
		t.Errorf("expected 'Bắt đầu', got %q", delta4)
	}
}

func TestParseEnvelopeChunk_ProductComparisonTable(t *testing.T) {
	xmlTable := `<ProductComparisonTable title="Bảng so sánh chi tiết">
[
  {
    "display_name": "iPhone 16 Pro Max",
    "Chip": "Apple A18 Pro",
    "RAM": "8 GB",
    "Giá": "34.990.000đ"
  },
  {
    "display_name": "Galaxy S25 Ultra",
    "Chip": "Snapdragon 8 Elite",
    "RAM": "12 GB",
    "Giá": "31.990.000đ"
  }
]
</ProductComparisonTable>`

	rawText := "Dưới đây là so sánh:\n\n" + xmlTable + "\n\nhttp://googleusercontent.com/shopping_content/123456\nChúc bạn chọn được máy ưng ý!"
	innerObj := []interface{}{
		nil,
		[]interface{}{"c_shop", "r_shop"},
		nil,
		nil,
		[]interface{}{
			[]interface{}{
				"rc_shop",
				[]interface{}{rawText},
			},
		},
		"http://googleusercontent.com/shopping_content/123456",
		"http://googleusercontent.com/youtube_content/987654",
		"http://googleusercontent.com/immersive_entry_chip/0",
	}

	innerBytes, _ := json.Marshal(innerObj)
	envelopeObj := [][]interface{}{
		{"wrb.fr", "assistant.lamda.BardFrontendService", string(innerBytes)},
	}
	lineBytes, _ := json.Marshal(envelopeObj)

	meta := google.ParseEnvelopeChunk(string(lineBytes))

	// 1. Text không được còn thẻ <ProductComparisonTable> thô
	if strings.Contains(meta.Text, "<ProductComparisonTable") {
		t.Errorf("expected <ProductComparisonTable> to be converted, got raw XML in Text: %q", meta.Text)
	}
	// 2. Phải có bảng Markdown với tiêu đề cột
	if !strings.Contains(meta.Text, "| Thông số | iPhone 16 Pro Max | Galaxy S25 Ultra |") &&
		!strings.Contains(meta.Text, "| iPhone 16 Pro Max | Galaxy S25 Ultra |") {
		t.Errorf("expected Markdown table, got: %q", meta.Text)
	}
	// 3. Phải dọn dẹp các shopping_content placeholder URL
	if strings.Contains(meta.Text, "http://googleusercontent.com/shopping_content/") {
		t.Errorf("expected shopping_content URL to be cleaned from Text, got: %q", meta.Text)
	}
	// 4. MediaURLs KHÔNG được chứa shopping_content, youtube_content, hay immersive_entry_chip
	for _, u := range meta.MediaURLs {
		if strings.Contains(u, "shopping_content") || strings.Contains(u, "youtube_content") || strings.Contains(u, "immersive_entry_chip") {
			t.Errorf("non-media placeholder leaked into MediaURLs: %s", u)
		}
	}
}

func TestParseEnvelopeChunk_MultipleDrafts(t *testing.T) {
	innerObj := []interface{}{
		nil,
		[]interface{}{"c_multi", "r_multi"},
		nil,
		nil,
		[]interface{}{
			[]interface{}{
				"rc_draft_1",
				[]interface{}{"Bản nháp 1: Chào bạn!"},
			},
			[]interface{}{
				"rc_draft_2",
				[]interface{}{"Bản nháp 2: Xin chào, tôi có thể giúp gì?"},
			},
			[]interface{}{
				"rc_draft_3",
				[]interface{}{"Bản nháp 3: Rất vui được gặp bạn!"},
			},
		},
	}

	innerBytes, _ := json.Marshal(innerObj)
	envelopeObj := [][]interface{}{
		{"wrb.fr", "assistant.lamda.BardFrontendService", string(innerBytes)},
	}
	lineBytes, _ := json.Marshal(envelopeObj)

	meta := google.ParseEnvelopeChunk(string(lineBytes))

	if meta.Text != "Bản nháp 1: Chào bạn!" {
		t.Errorf("expected main text from first draft, got %q", meta.Text)
	}
	if len(meta.Drafts) != 3 {
		t.Fatalf("expected 3 drafts, got %d", len(meta.Drafts))
	}
	if meta.Drafts[0] != "Bản nháp 1: Chào bạn!" ||
		meta.Drafts[1] != "Bản nháp 2: Xin chào, tôi có thể giúp gì?" ||
		meta.Drafts[2] != "Bản nháp 3: Rất vui được gặp bạn!" {
		t.Errorf("drafts mismatch: %+v", meta.Drafts)
	}
}

func TestCleanInternalPlaceholders_EntityCard(t *testing.T) {
	raw := `<EntityCard entity_id="2516887405592170857" entity_type="product" title="Google Pixel 9 Pro 5G (16GB | 128GB)"/>
Chiếc **Google Pixel 9 Pro** nổi bật với dung lượng **RAM lớn 16GB**.
<EntityCard entity_id="8650286678420419758" entity_type="product" title="Apple iPhone 16 Pro 128GB Titan Trắng"></EntityCard>
Mẫu **iPhone 16 Pro** cao cấp.`

	cleaned := google.CleanInternalPlaceholders(raw)

	if strings.Contains(cleaned, "<EntityCard") {
		t.Errorf("expected <EntityCard> to be removed or replaced, got: %q", cleaned)
	}
	if !strings.Contains(cleaned, "> 📦 **Google Pixel 9 Pro 5G (16GB | 128GB)**") {
		t.Errorf("expected quoted title block for Pixel 9 Pro, got: %q", cleaned)
	}
	if !strings.Contains(cleaned, "> 📦 **Apple iPhone 16 Pro 128GB Titan Trắng**") {
		t.Errorf("expected quoted title block for iPhone 16 Pro, got: %q", cleaned)
	}
}

func TestReadGeminiStreamWithThinking(t *testing.T) {
	// Chunk 1: Thinking Bước 1
	chunk1Inner := []interface{}{
		nil, []interface{}{"c_thk", "r_thk"}, nil, nil,
		[]interface{}{
			[]interface{}{
				"rc_thk", []interface{}{""}, nil, nil, nil,
				[]interface{}{map[string]interface{}{"thought_content": "Bước 1: Phân tích", "is_thinking": true}},
			},
		},
	}
	b1, _ := json.Marshal(chunk1Inner)
	line1, _ := json.Marshal([][]interface{}{{"wrb.fr", "assistant.lamda.BardFrontendService", string(b1)}})

	// Chunk 2: Cumulative thinking: Bước 1 + Bước 2
	chunk2Inner := []interface{}{
		nil, []interface{}{"c_thk", "r_thk"}, nil, nil,
		[]interface{}{
			[]interface{}{
				"rc_thk", []interface{}{""}, nil, nil, nil,
				[]interface{}{map[string]interface{}{"thought_content": "Bước 1: Phân tích\nBước 2: Giải xong", "is_thinking": true}},
			},
		},
	}
	b2, _ := json.Marshal(chunk2Inner)
	line2, _ := json.Marshal([][]interface{}{{"wrb.fr", "assistant.lamda.BardFrontendService", string(b2)}})

	// Chunk 3: Final Answer text
	chunk3Inner := []interface{}{
		nil, []interface{}{"c_thk", "r_thk"}, nil, nil,
		[]interface{}{
			[]interface{}{
				"rc_thk", []interface{}{"Đáp án: x = 2"}, nil, nil, nil,
				[]interface{}{map[string]interface{}{"thought_content": "Bước 1: Phân tích\nBước 2: Giải xong", "is_thinking": true}},
			},
		},
	}
	b3, _ := json.Marshal(chunk3Inner)
	line3, _ := json.Marshal([][]interface{}{{"wrb.fr", "assistant.lamda.BardFrontendService", string(b3)}})

	streamData := strings.Join([]string{
		")]}'",
		"100",
		string(line1),
		"120",
		string(line2),
		"150",
		string(line3),
	}, "\n")

	var reasoningDeltas []string
	var contentDeltas []string

	onContent := func(delta, convID string) error {
		contentDeltas = append(contentDeltas, delta)
		return nil
	}
	onReasoning := func(delta, convID string) error {
		reasoningDeltas = append(reasoningDeltas, delta)
		return nil
	}

	reply, err := google.ReadGeminiStreamWithThinking(
		context.Background(),
		strings.NewReader(streamData),
		nil,
		onContent,
		onReasoning,
	)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	if len(reasoningDeltas) != 2 {
		t.Fatalf("expected 2 reasoning deltas, got %d (%+v)", len(reasoningDeltas), reasoningDeltas)
	}
	if reasoningDeltas[0] != "Bước 1: Phân tích" {
		t.Errorf("expected first delta to be 'Bước 1: Phân tích', got %q", reasoningDeltas[0])
	}
	if reasoningDeltas[1] != "\nBước 2: Giải xong" {
		t.Errorf("expected second delta to be '\nBước 2: Giải xong', got %q", reasoningDeltas[1])
	}

	if len(contentDeltas) != 1 || contentDeltas[0] != "Đáp án: x = 2" {
		t.Errorf("content deltas mismatch: %+v", contentDeltas)
	}
	if reply.ConversationID != "c_thk" {
		t.Errorf("expected conversation ID c_thk, got %q", reply.ConversationID)
	}
}

func TestStream_IdleTimeout_Triggered(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	ctx := google.WithStreamIdleTimeout(context.Background(), 50*time.Millisecond)
	tracker := google.NewStreamMetricsTracker()
	ctx = google.WithStreamMetricsTracker(ctx, tracker)

	// Send one line, then hang
	go func() {
		sampleLine := `[["wrb.fr","assistant.lamda.BardFrontendService","[null,[\"c_1\",\"r_1\"],null,null,[[\"rc_1\",[\"Hello!\"]]]]"]]` + "\n"
		_, _ = pw.Write([]byte(sampleLine))
		// Do not write anything further, keep pipe open
	}()

	var received []string
	_, err := google.ReadGeminiStreamWithThinking(
		ctx,
		pr,
		nil,
		func(delta, convID string) error {
			received = append(received, delta)
			return nil
		},
		nil,
	)

	if err == nil {
		t.Fatalf("expected idle timeout error, got nil")
	}
	if !errors.Is(err, google.ErrStreamIdleTimeout) && !strings.Contains(err.Error(), "idle timeout") {
		t.Errorf("expected ErrStreamIdleTimeout, got: %v", err)
	}
	snap := tracker.Snapshot()
	if snap.TimeoutKind != "idle" {
		t.Errorf("expected TimeoutKind idle, got %s", snap.TimeoutKind)
	}
}

func TestStream_SlowActiveStream_DoesNotTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	// Short idle timeout: 80ms
	ctx := google.WithStreamIdleTimeout(context.Background(), 80*time.Millisecond)
	tracker := google.NewStreamMetricsTracker()
	ctx = google.WithStreamMetricsTracker(ctx, tracker)

	// Write 5 chunks with 25ms pause between each (within the 80ms idle window)
	go func() {
		defer pw.Close()
		for i := 1; i <= 5; i++ {
			text := strings.Repeat("A", i)
			inner := []interface{}{
				nil, []interface{}{"c_slow", "r_slow"}, nil, nil,
				[]interface{}{[]interface{}{"rc_slow", []interface{}{text}}},
			}
			b, _ := json.Marshal(inner)
			line, _ := json.Marshal([][]interface{}{{"wrb.fr", "assistant.lamda.BardFrontendService", string(b)}})
			_, _ = pw.Write(append(line, '\n'))
			time.Sleep(25 * time.Millisecond)
		}
	}()

	var finalTokens []string
	reply, err := google.ReadGeminiStreamWithThinking(
		ctx,
		pr,
		nil,
		func(delta, convID string) error {
			finalTokens = append(finalTokens, delta)
			return nil
		},
		nil,
	)

	if err != nil {
		t.Fatalf("unexpected error for active slow stream: %v", err)
	}
	if reply.Text != "AAAAA" {
		t.Errorf("expected final text AAAAA, got %q", reply.Text)
	}
	snap := tracker.Snapshot()
	if snap.TimeoutKind != "" {
		t.Errorf("expected no timeout kind, got %q", snap.TimeoutKind)
	}
	if !snap.UpstreamEOF {
		t.Errorf("expected UpstreamEOF true")
	}
	if snap.TokensEmitted != 5 {
		t.Errorf("expected 5 tokens emitted, got %d", snap.TokensEmitted)
	}
}

func TestStream_LargeLinesAndRobustness(t *testing.T) {
	// Line with 150KB of content
	largeContent := strings.Repeat("0123456789abcdef", 10000)
	inner := []interface{}{
		nil, []interface{}{"c_large", "r_large"}, nil, nil,
		[]interface{}{[]interface{}{"rc_large", []interface{}{largeContent}}},
	}
	b, _ := json.Marshal(inner)
	line, _ := json.Marshal([][]interface{}{{"wrb.fr", "assistant.lamda.BardFrontendService", string(b)}})

	// Stream contains prefix, large line, and a final line without trailing newline
	rawStream := strings.Join([]string{
		")]}'",
		"99999",
		string(line),
	}, "\n")

	reply, err := google.ReadGeminiStreamWithThinking(
		context.Background(),
		strings.NewReader(rawStream),
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("failed reading large stream: %v", err)
	}
	if reply.Text != largeContent {
		t.Errorf("expected largeContent length %d, got %d", len(largeContent), len(reply.Text))
	}
	if reply.ConversationID != "c_large" {
		t.Errorf("expected conversation ID c_large, got %q", reply.ConversationID)
	}
}
