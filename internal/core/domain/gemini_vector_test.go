package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestGeminiPayloadMatchesNewChatVector(t *testing.T) {
	got, err := (&domain.GeminiPayloadBuilder{
		UserPrompt:     "Câu hỏi mở đầu cho cuộc trò chuyện mới",
		Locale:         "vi",
		ModelTier:      1,
		EnableThinking: false,
		ClientUUID:     "NEW_CHAT_UUID",
	}).BuildArray()
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.GeminiFixtureSlots(false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, got, want)
}

func TestGeminiPayloadMatchesThinkingVector(t *testing.T) {
	got, err := (&domain.GeminiPayloadBuilder{
		UserPrompt:     "Hãy chứng minh định lý Fermat nhỏ và giải thích từng bước tư duy",
		Locale:         "vi",
		ModelTier:      3,
		EnableThinking: true,
		ClientUUID:     "THINKING_UUID",
	}).BuildArray()
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.GeminiFixtureSlots(true)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, got, want)
}

func TestGeminiPayloadMatchesMultiTurnVector(t *testing.T) {
	got, err := (&domain.GeminiPayloadBuilder{
		UserPrompt:     "Xin chào Gemini, hôm nay thời tiết thế nào?",
		Locale:         "vi",
		ConversationID: "c_123456789",
		ResponseID:     "r_987654321",
		ChoiceID:       "rc_11223344",
		ContextBlob:    "!context_blob_token",
		ModelTier:      1,
		EnableThinking: false,
		ClientUUID:     "SESSION_UUID",
	}).BuildArray()
	if err != nil {
		t.Fatal(err)
	}

	contextIDs, ok := got[domain.SlotContextIDs].([]any)
	if !ok || len(contextIDs) < 3 {
		t.Fatalf("contextIDs slot = %#v", got[domain.SlotContextIDs])
	}
	if contextIDs[0] != "c_123456789" || contextIDs[1] != "r_987654321" || contextIDs[2] != "rc_11223344" {
		t.Fatalf("contextIDs mismatch: %#v", contextIDs)
	}
	if got[domain.SlotContextBlob] != "!context_blob_token" {
		t.Fatalf("contextBlob mismatch: %v", got[domain.SlotContextBlob])
	}
}

func TestFlowMediaMatchesCurlVector(t *testing.T) {
	video := domain.BuildFlowMediaInner(domain.FlowMediaInput{
		RequestID:    "VEO_REQ_UUID_001",
		Prompt:       "A cinematic wide angle drone shot flying over a futuristic neon city in rain at night, 8k, photorealistic",
		ProjectID:    "<PROJECT_UUID>",
		SessionToken: "SESSION_STATE_TOKEN",
	})
	wantVideo, err := domain.FlowMediaInnerFromFreq(domain.FlowVideoFreq())
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, video, wantVideo)

	image := domain.BuildFlowMediaInner(domain.FlowMediaInput{
		RequestID:    "ABRA_REQ_UUID_002",
		Prompt:       "A studio portrait of a futuristic astronaut in reflective visor, octane render, soft studio light",
		ProjectID:    "<PROJECT_UUID>",
		SessionToken: "SESSION_STATE_TOKEN",
	})
	wantImage, err := domain.FlowMediaInnerFromFreq(domain.FlowImageFreq())
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, image, wantImage)
}

func TestFlowMediaOptionsAreExplicit(t *testing.T) {
	got := domain.BuildFlowMediaInner(domain.FlowMediaInput{
		RequestID:       "req-1",
		Prompt:          "a cat",
		ProjectID:       "proj-1",
		SessionToken:    "tok-1",
		ModelID:         "veo_3_1_quality",
		DurationSeconds: 8,
		AspectCode:      2,
	})
	raw, _ := json.Marshal(got)
	var decoded []any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	contextRow, ok := decoded[2].([]any)
	if !ok || len(contextRow) < 4 {
		t.Fatalf("context = %#v", decoded)
	}
	opts, ok := contextRow[3].(map[string]any)
	if !ok {
		t.Fatalf("options = %#v", contextRow[3])
	}
	if opts["model_id"] != "veo_3_1_quality" || opts["duration_seconds"].(float64) != 8 || opts["aspect_ratio"].(float64) != 2 {
		t.Fatalf("options = %#v", opts)
	}
}

func TestParseCreatedProject(t *testing.T) {
	id, unmapped, err := domain.ParseCreatedProject(`["1cfeebb6-4d61-4373-aa21-dd52fd93679f",["sept. 23 - 19:48"]]`)
	if err != nil {
		t.Fatal(err)
	}
	if id != "1cfeebb6-4d61-4373-aa21-dd52fd93679f" || unmapped != 1 {
		t.Fatalf("id=%s unmapped=%d", id, unmapped)
	}
	_, _, err = domain.ParseCreatedProject(`["SUPERSECRET"]`)
	if err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("error = %v", err)
	}
}

func TestExtractMediaKeepsAllowlistedURL(t *testing.T) {
	video := `{"status":"COMPLETED","video_asset":{"asset_id":"video_asset_uuid_999","url":"https://storage.googleapis.com/flow-rendered-videos/output_999.mp4","resolution":"1280x720","duration":6},"credits_deducted":20}`
	got, err := domain.ExtractMediaDocument(video)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://storage.googleapis.com/flow-rendered-videos/output_999.mp4" || got.Unmapped != 0 {
		t.Fatalf("extract = %+v", got)
	}
	image := `{"task_status":"COMPLETED","model_used":"abra","output_assets":[{"asset_id":"image_asset_uuid_777","url":"https://lh3.googleusercontent.com/ai-sandbox/output_777.png","mime_type":"image/png","width":1024,"height":1024,"seed":847291039}],"credits_deducted":7}`
	got, err = domain.ExtractMediaDocument(image)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://lh3.googleusercontent.com/ai-sandbox/output_777.png" || got.MimeType != "image/png" {
		t.Fatalf("image = %+v", got)
	}
	_, err = domain.ExtractMediaDocument(`{"note":"SUPERSECRET"}`)
	if err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("error = %v", err)
	}
	progress, err := domain.ExtractMediaDocument(`{"progress":25}`)
	if err != nil || !progress.SawProgress || progress.URL != "" {
		t.Fatalf("progress = %+v err=%v", progress, err)
	}
}

func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("got %s\nwant %s", gb, wb)
	}
}
