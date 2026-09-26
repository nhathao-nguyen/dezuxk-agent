package domain

import (
	"encoding/json"
	"regexp"
	"strings"
)

const FlowMediaSpecVersion = "2026-09-23"

const flowVideoFreq = `[null,"[\"VEO_REQ_UUID_001\",[[[[\"A cinematic wide angle drone shot flying over a futuristic neon city in rain at night, 8k, photorealistic\"]]]],[\"projects/<PROJECT_UUID>\",null,[\"SESSION_STATE_TOKEN\",1],null,null,1]]"]`

const flowImageFreq = `[null,"[\"ABRA_REQ_UUID_002\",[[[[\"A studio portrait of a futuristic astronaut in reflective visor, octane render, soft studio light\"]]]],[\"projects/<PROJECT_UUID>\",null,[\"SESSION_STATE_TOKEN\",1],null,null,1]]"]`

var projectUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// FlowMediaInput là phần tính lại mỗi lần gọi StreamChat.
// ModelID rỗng giữ ô tùy chọn là null, đúng vector curl cơ bản.
// DurationSeconds và AspectCode chỉ có bằng chứng trên payload video.
type FlowMediaInput struct {
	RequestID       string
	Prompt          string
	ProjectID       string
	SessionToken    string
	ModelID         string
	DurationSeconds int
	AspectCode      int
	Seed            int64
	StartImageToken string
	EndImageToken   string
	Camera          string
	CameraMotion    *CameraMotionConfig
	Ingredients     []any
	VoicePersonaID  string
	DialogueText    string
}

func BuildFlowMediaInner(in FlowMediaInput) []any {
	var options any
	if in.ModelID != "" || in.DurationSeconds > 0 || in.AspectCode > 0 || in.Seed > 0 || in.StartImageToken != "" || in.EndImageToken != "" || in.Camera != "" || in.CameraMotion != nil || in.VoicePersonaID != "" {
		opts := map[string]any{}
		if in.ModelID != "" {
			opts["model_id"] = in.ModelID
		}
		if in.DurationSeconds > 0 {
			opts["duration_seconds"] = in.DurationSeconds
		}
		if in.AspectCode > 0 {
			opts["aspect_ratio"] = in.AspectCode
		}
		if in.Seed > 0 {
			opts["seed"] = in.Seed
		}
		if in.StartImageToken != "" {
			opts["start_image_token"] = in.StartImageToken
		}
		if in.EndImageToken != "" {
			opts["end_image_token"] = in.EndImageToken
		}
		if in.Camera != "" {
			opts["camera_movement"] = in.Camera
		}
		if in.CameraMotion != nil {
			opts["camera_motion"] = in.CameraMotion.BuildWireObject()
		}
		if in.VoicePersonaID != "" {
			opts["voice_persona_id"] = in.VoicePersonaID
			opts["lip_sync_enabled"] = true
			if in.DialogueText != "" {
				opts["dialogue_text"] = in.DialogueText
			}
		}
		options = opts
	}

	var ingredientsSlot any = nil
	if len(in.Ingredients) > 0 {
		ingredientsSlot = in.Ingredients
	}

	return []any{
		in.RequestID,
		[]any{[]any{[]any{[]any{in.Prompt}}}},
		[]any{
			"projects/" + in.ProjectID,
			ingredientsSlot,
			[]any{in.SessionToken, 1},
			options,
			nil,
			1,
		},
	}
}

// ParseSessionLockResponse phân tích phản hồi RPC csbIsb theo chuẩn docs-2 (trả về [1] hoặc [true]).
func ParseSessionLockResponse(inner string, metrics *ContractMetrics) error {
	inner = strings.TrimSpace(inner)
	var data []any
	if inner == "" || json.Unmarshal([]byte(inner), &data) != nil || len(data) == 0 {
		if metrics != nil {
			metrics.AddSchema()
		}
		return CodecSchema("csbIsb", ServiceFlow, "phản hồi khóa phiên không đúng hợp đồng")
	}
	isOK := false
	switch v := data[0].(type) {
	case bool:
		isOK = v
	case float64:
		isOK = v == 1
	}
	if !isOK {
		if metrics != nil {
			metrics.AddSchema()
		}
		return CodecSchema("csbIsb", ServiceFlow, "phản hồi khóa phiên không đúng hợp đồng")
	}
	if len(data) > 1 && metrics != nil {
		metrics.AddUnmapped(len(data) - 1)
	}
	return nil
}

func FlowVideoFreq() string { return flowVideoFreq }

func FlowImageFreq() string { return flowImageFreq }

func FlowMediaInnerFromFreq(freq string) ([]any, error) {
	var outer []any
	if err := json.Unmarshal([]byte(freq), &outer); err != nil {
		return nil, err
	}
	inner, _ := outer[1].(string)
	var slots []any
	if err := json.Unmarshal([]byte(inner), &slots); err != nil {
		return nil, err
	}
	return slots, nil
}

// FlowAspectRatioEnumCodes ánh xạ tỷ lệ khung hình theo đặc tả Google Flow (docs-2 mục credits_and_consumption & image_generation)
// 1: 9:16 (Portrait), 2: 16:9 (Landscape), 3: 1:1 (Square), 4: 4:3 (Landscape 4:3), 5: 3:4 (Portrait 3:4)
var FlowAspectRatioEnumCodes = map[string]int{
	"9:16": 1,
	"16:9": 2,
	"1:1":  3,
	"4:3":  4,
	"3:4":  5,
}

// AspectRatioCode trả về mã enum số nguyên của tỷ lệ khung hình theo chuẩn Google Flow
func AspectRatioCode(aspect string) (int, bool) {
	code, ok := FlowAspectRatioEnumCodes[strings.TrimSpace(aspect)]
	return code, ok
}

// VideoAspectCode duy trì tương thích ngược cho các dịch vụ gọi video và hình ảnh
func VideoAspectCode(aspect string) (int, bool) {
	return AspectRatioCode(aspect)
}

func BuildCreateProjectInner(title string) []any {
	return []any{"projects/*", []any{nil, []any{title}}, []any{nil, 22}}
}

func ParseCreatedProject(inner string) (string, int, error) {
	var data []any
	if json.Unmarshal([]byte(inner), &data) != nil || len(data) == 0 {
		return "", 0, CodecSchema(OriginCreateProject, ServiceFlow, "phản hồi tạo dự án không đúng hợp đồng")
	}
	id, ok := data[0].(string)
	if !ok || !projectUUIDPattern.MatchString(id) {
		return "", 0, CodecSchema(OriginCreateProject, ServiceFlow, "phản hồi tạo dự án không đúng hợp đồng")
	}
	return id, len(data) - 1, nil
}
