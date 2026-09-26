package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FlowProject đại diện cho một dự án sáng tạo trên Google Flow
type FlowProject struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	IsActive  bool      `json:"is_active"`
}

// FlowTrashProject đại diện cho một dự án trong thùng rác
type FlowTrashProject struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	DeletedAt          time.Time `json:"deleted_at"`
	DaysRemaining      int       `json:"days_remaining"`
	AssetsCount        int       `json:"assets_count"`
}

// BuildListProjectsPayload RPC UpteDb
func BuildListProjectsPayload() []any {
	return []any{}
}

// BuildTrashMovePayload RPC dK3x9 (Chuyển vào thùng rác)
func BuildTrashMovePayload(projectUUID string) []any {
	return []any{"projects/" + projectUUID}
}

// BuildListTrashPayload RPC tB6q8 (Lấy danh sách thùng rác)
func BuildListTrashPayload() []any {
	return []any{}
}

// BuildTrashRestorePayload RPC rS4y1 (Khôi phục dự án)
func BuildTrashRestorePayload(projectUUID string) []any {
	return []any{"projects/" + projectUUID}
}

// BuildPermanentDeletePayload RPC mrlkwd (Xóa vĩnh viễn)
func BuildPermanentDeletePayload(projectUUID string) []any {
	return []any{"projects/" + projectUUID}
}

// ParseProjectsListResponse phân tích phản hồi RPC UpteDb
func ParseProjectsListResponse(inner string, metrics *ContractMetrics) ([]FlowProject, error) {
	inner = strings.TrimSpace(inner)
	if inner == "" || inner == "[]" {
		return []FlowProject{}, nil
	}

	var raw []any
	if err := json.Unmarshal([]byte(inner), &raw); err != nil {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("UpteDb", ServiceFlow, "phản hồi UpteDb không đúng hợp đồng")
	}

	var projects []FlowProject
	var scanList func(v any)
	scanList = func(v any) {
		switch t := v.(type) {
		case []any:
			// Kiểm tra nếu đây là mảng project tuple: ["uuid", "title", timestamp, ...]
			if len(t) >= 2 {
				idStr, ok1 := t[0].(string)
				titleStr, ok2 := t[1].(string)
				if ok1 && ok2 && projectUUIDPattern.MatchString(idStr) {
					var updated time.Time
					if len(t) >= 3 {
						if tsFloat, ok := t[2].(float64); ok && tsFloat > 0 {
							updated = time.UnixMilli(int64(tsFloat))
						}
					}
					if updated.IsZero() {
						updated = time.Now()
					}
					projects = append(projects, FlowProject{
						ID:        idStr,
						Title:     titleStr,
						UpdatedAt: updated,
						IsActive:  true,
					})
					return
				}
			}
			for _, item := range t {
				scanList(item)
			}
		}
	}

	scanList(raw)
	return projects, nil
}

// ParseTrashListResponse phân tích phản hồi RPC tB6q8
func ParseTrashListResponse(inner string, metrics *ContractMetrics) ([]FlowTrashProject, error) {
	inner = strings.TrimSpace(inner)
	if inner == "" || inner == "[]" {
		return []FlowTrashProject{}, nil
	}

	var raw []any
	if err := json.Unmarshal([]byte(inner), &raw); err != nil {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("tB6q8", ServiceFlow, "phản hồi tB6q8 không đúng hợp đồng")
	}

	var trashList []FlowTrashProject
	var scanTrash func(v any)
	scanTrash = func(v any) {
		switch t := v.(type) {
		case []any:
			if len(t) >= 2 {
				idStr, ok1 := t[0].(string)
				titleStr, ok2 := t[1].(string)
				if ok1 && ok2 && (projectUUIDPattern.MatchString(idStr) || len(idStr) > 8) {
					item := FlowTrashProject{
						ID:            idStr,
						Title:         titleStr,
						DaysRemaining: 30,
					}
					if len(t) >= 3 {
						if tsFloat, ok := t[2].(float64); ok && tsFloat > 0 {
							item.DeletedAt = time.UnixMilli(int64(tsFloat))
						}
					}
					if len(t) >= 4 {
						if days, ok := t[3].(float64); ok {
							item.DaysRemaining = int(days)
						}
					}
					if len(t) >= 5 {
						if count, ok := t[4].(float64); ok {
							item.AssetsCount = int(count)
						}
					}
					trashList = append(trashList, item)
					return
				}
			}
			for _, item := range t {
				scanTrash(item)
			}
		}
	}

	scanTrash(raw)
	return trashList, nil
}

// ParseBoolStatusResponse phân tích các phản hồi dạng [1] hoặc [true] cho dK3x9, rS4y1, mrlkwd
func ParseBoolStatusResponse(rpcID string, inner string, metrics *ContractMetrics) error {
	inner = strings.TrimSpace(inner)
	var data []any
	if inner == "" || json.Unmarshal([]byte(inner), &data) != nil || len(data) == 0 {
		if metrics != nil {
			metrics.AddSchema()
		}
		return CodecSchema(rpcID, ServiceFlow, fmt.Sprintf("phản hồi %s không đúng hợp đồng", rpcID))
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
		return CodecSchema(rpcID, ServiceFlow, fmt.Sprintf("phản hồi %s trả về false", rpcID))
	}
	return nil
}
