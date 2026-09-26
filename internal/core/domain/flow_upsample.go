package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Upsample4KResponse kết quả trả về từ RPC uW3g7e
type Upsample4KResponse struct {
	TaskID          string `json:"task_id"`
	Status          string `json:"status"` // QUEUED, PROCESSING, COMPLETED
	OutputURL       string `json:"output_url,omitempty"`
	AssetID         string `json:"asset_id,omitempty"`
	Resolution      string `json:"resolution,omitempty"` // 3840x2160
	Duration        float64 `json:"duration,omitempty"`
	CreditsDeducted int    `json:"credits_deducted"`
	RemainingCredit int    `json:"remaining_credits"`
}

// BuildUpsample4KPayload tạo payload mảng JSON cho RPC uW3g7e
func BuildUpsample4KPayload(projectUUID string, sourceAssetID string, sessionToken string, clientGuid string, enhancementLevel int, preserveAudio bool) []any {
	if enhancementLevel <= 0 {
		enhancementLevel = 1
	}

	configObj := map[string]any{
		"target_resolution":  "4K",
		"upsampler_model_id": "veo_3_1_upsampler_4k",
		"enhancement_level":  enhancementLevel,
		"preserve_audio":     preserveAudio,
	}

	return []any{
		"projects/" + projectUUID,
		sourceAssetID,
		configObj,
		sessionToken,
		clientGuid,
	}
}

// ParseUpsample4KResponse phân tích phản hồi RPC uW3g7e
func ParseUpsample4KResponse(inner string, metrics *ContractMetrics) (*Upsample4KResponse, error) {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("uW3g7e", ServiceFlow, "phản hồi uW3g7e rỗng")
	}

	// Trường hợp 1: inner data là JSON object
	var objMap map[string]any
	if err := json.Unmarshal([]byte(inner), &objMap); err == nil && len(objMap) > 0 {
		res := &Upsample4KResponse{
			TaskID:          fmt.Sprintf("%v", objMap["task_id"]),
			Status:          fmt.Sprintf("%v", objMap["status"]),
			CreditsDeducted: 50,
		}
		if outAsset, ok := objMap["output_asset"].(map[string]any); ok {
			res.OutputURL = fmt.Sprintf("%v", outAsset["url"])
			res.AssetID = fmt.Sprintf("%v", outAsset["asset_id"])
			res.Resolution = fmt.Sprintf("%v", outAsset["resolution"])
			if dur, ok := outAsset["duration"].(float64); ok {
				res.Duration = dur
			}
		}
		if res.OutputURL == "" {
			// Thử quét link URL có sẵn
			ext, err := ExtractMediaDocument(inner)
			if err == nil && ext.URL != "" {
				res.OutputURL = ext.URL
				res.Status = "COMPLETED"
				res.Resolution = "3840x2160"
			}
		}
		return res, nil
	}

	// Trường hợp 2: mảng lồng nhau
	ext, err := ExtractMediaDocument(inner)
	if err != nil {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("uW3g7e", ServiceFlow, "phản hồi uW3g7e không đúng cấu trúc")
	}

	return &Upsample4KResponse{
		Status:          "COMPLETED",
		OutputURL:       ext.URL,
		Resolution:      "3840x2160",
		CreditsDeducted: 50,
	}, nil
}
