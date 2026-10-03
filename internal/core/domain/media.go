package domain

import (
	"time"
)

type MediaKind string

const (
	MediaImagePNG MediaKind = "image/png"
	MediaVideoMP4 MediaKind = "video/mp4"
	MediaAudioWAV MediaKind = "audio/wav"
)

// MediaAsset đại diện cho tài nguyên truyền thông được lưu trữ cục bộ
type MediaAsset struct {
	ID          string    `json:"id"`
	FileName    string    `json:"file_name"`
	FilePath    string    `json:"file_path"`
	LocalURL    string    `json:"local_url"`
	OriginalURL string    `json:"original_url"`
	Kind        MediaKind `json:"kind"`
	SizeBytes   int64     `json:"size_bytes"`
	Prompt      string    `json:"prompt,omitempty"`
	Model       string    `json:"model,omitempty"`
	Duration    float64   `json:"duration,omitempty"`
	Resolution  string    `json:"resolution,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	IsReady     bool      `json:"is_ready"`
	TenantID    string    `json:"tenant_id,omitempty"`
	IsPublic    bool      `json:"is_public,omitempty"`
}
