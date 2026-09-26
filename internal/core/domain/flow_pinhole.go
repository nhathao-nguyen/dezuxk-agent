package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PinholeNode đại diện cho 1 nút trong đồ thị PINHOLE
type PinholeNode struct {
	ID    string  `json:"id"`
	Type  string  `json:"type"`  // "abra", "veo_3_1_quality", "veo_3_1_fast", "prompt_box", "narwhal_display"
	Title string  `json:"title"`
	PosX  float64 `json:"pos_x"`
	PosY  float64 `json:"pos_y"`
}

// PinholeEdge liên kết giữa 2 chân của 2 nút
type PinholeEdge struct {
	ID           string `json:"id"`
	SourceNodeID string `json:"source_node_id"`
	SourcePinID  string `json:"source_pin_id"` // "image_out", "text_out"
	TargetNodeID string `json:"target_node_id"`
	TargetPinID  string `json:"target_pin_id"` // "first_frame_in", "prompt_in", "style_ref_in"
}

// BuildAddNodePayload đóng gói mảng JSON cho RPC kF8z7b (Thêm Node mới)
func BuildAddNodePayload(projectUUID string, nodeType string, title string, posX, posY float64) []any {
	return []any{
		"projects/" + projectUUID,
		nodeType,
		map[string]any{"pos_x": posX, "pos_y": posY},
		title,
	}
}

// BuildConnectPinsPayload đóng gói mảng JSON cho RPC jE2m9c (Nối chân Pins)
func BuildConnectPinsPayload(projectUUID string, edge PinholeEdge, sessionToken string) []any {
	return []any{
		"projects/" + projectUUID,
		map[string]any{
			"source_node_id": edge.SourceNodeID,
			"source_pin_id":  edge.SourcePinID,
			"target_node_id": edge.TargetNodeID,
			"target_pin_id":  edge.TargetPinID,
		},
		sessionToken,
	}
}

// BuildDeleteNodePayload đóng gói mảng JSON cho RPC dL5p2 (Xóa Nodes/Edges)
func BuildDeleteNodePayload(projectUUID string, nodeIDs []string, edgeIDs []string) []any {
	if nodeIDs == nil {
		nodeIDs = []string{}
	}
	if edgeIDs == nil {
		edgeIDs = []string{}
	}
	return []any{
		"projects/" + projectUUID,
		nodeIDs,
		edgeIDs,
	}
}

// BuildGetGraphPayload đóng gói mảng JSON cho RPC ngNC2 (Lấy đồ thị PINHOLE)
func BuildGetGraphPayload(projectUUID string) []any {
	return []any{"projects/" + projectUUID}
}

// ParseAddNodeResponse phân tích phản hồi RPC kF8z7b
func ParseAddNodeResponse(inner string, metrics *ContractMetrics) (string, error) {
	inner = strings.TrimSpace(inner)
	var data []any
	if inner == "" || json.Unmarshal([]byte(inner), &data) != nil || len(data) == 0 {
		if metrics != nil {
			metrics.AddSchema()
		}
		return "", CodecSchema("kF8z7b", ServiceFlow, "phản hồi kF8z7b không đúng hợp đồng")
	}
	nodeID := fmt.Sprintf("%v", data[0])
	return nodeID, nil
}
