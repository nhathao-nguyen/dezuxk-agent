package domain

import (
	"fmt"
	"strings"
)

// BuildVideoExtensionPayload tạo payload mảng JSON cho RPC StreamChat nối dài video
func BuildVideoExtensionPayload(reqUUID string, prompt string, projectUUID string, sessionToken string, parentAssetID string, extensionSeconds int, cameraMotion *CameraMotionConfig) []any {
	if extensionSeconds != 4 && extensionSeconds != 6 {
		extensionSeconds = 4
	}

	opts := map[string]any{
		"model_id":                   "veo_3_1_extend",
		"extension_duration_seconds": extensionSeconds,
		"preserve_physics":           true,
		"camera_motion_continuation": "MAINTAIN",
	}

	if cameraMotion != nil {
		opts["camera_motion"] = cameraMotion.BuildWireObject()
	}

	ingredient := map[string]any{
		"ingredient_type":       4, // Video Extension Anchor
		"source_video_asset_id": parentAssetID,
	}

	return []any{
		reqUUID,
		[]any{[]any{[]any{[]any{prompt}}}},
		[]any{
			"projects/" + projectUUID,
			[]any{ingredient},
			[]any{sessionToken, 1},
			opts,
			nil,
			1,
		},
	}
}

// ParseExtendedVideoResponse phân tích phản hồi StreamChat khi hoàn tất nối dài video
func ParseExtendedVideoResponse(raw string) (*VideoGenerationResult, error) {
	ext, err := ExtractMediaDocument(raw)
	if err != nil {
		return nil, err
	}
	return &VideoGenerationResult{
		URL:            ext.URL,
		MimeType:       ext.MimeType,
		UnmappedFields: ext.Unmapped,
		SpecVersion:    FlowMediaSpecVersion,
	}, nil
}

// ValidateVideoExtension kiểm tra tính hợp lệ của VideoExtensionRequest
func ValidateVideoExtension(req *VideoExtensionRequest) error {
	if req == nil {
		return fmt.Errorf("thiếu yêu cầu nối dài video")
	}
	if strings.TrimSpace(req.SourceVideoAssetID) == "" {
		return fmt.Errorf("thiếu source_video_asset_id của video gốc")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("thiếu mô tả phân cảnh tiếp theo (prompt)")
	}
	if req.ExtensionDuration != 4 && req.ExtensionDuration != 6 {
		return fmt.Errorf("thời lượng nối dài chỉ hỗ trợ 4 hoặc 6 giây (nhận được: %d)", req.ExtensionDuration)
	}
	if req.CameraMotion != nil {
		if err := req.CameraMotion.Validate(); err != nil {
			return err
		}
	}
	return nil
}
