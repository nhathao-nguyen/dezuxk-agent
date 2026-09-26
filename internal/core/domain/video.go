package domain

// CameraMotionVector ma trận chuyển động 4 chiều cho mô hình Veo 3.1
type CameraMotionVector struct {
	PanHorizontal    float64 `json:"pan_horizontal"`    // -1.0 đến 1.0 (Trái sang phải)
	TiltVertical     float64 `json:"tilt_vertical"`     // -1.0 đến 1.0 (Cúi xuống ngửa lên)
	ZoomDepth        float64 `json:"zoom_depth"`        // -1.0 đến 1.0 (Lùi xa tiến gần)
	OrbitTrajectory  float64 `json:"orbit_trajectory"`  // -1.0 đến 1.0 (Xoay tròn góc nhìn)
	TruckLateral     float64 `json:"truck_lateral,omitempty"`
	PedestalVertical float64 `json:"pedestal_vertical,omitempty"`
}

// VideoGenerationRequest yêu cầu sinh video mới
type VideoGenerationRequest struct {
	Model        string              `json:"model"`
	Prompt       string              `json:"prompt"`
	ImageURL     string              `json:"image_url,omitempty"` // First Frame nếu là I2V
	Duration     int                 `json:"duration"`            // 4, 6, 8, 10
	AspectRatio  string              `json:"aspect_ratio"`        // "16:9", "9:16", "1:1"
	Seed         int64               `json:"seed,omitempty"`
	Camera       *CameraMotionVector `json:"camera,omitempty"`
	CameraConfig *CameraMotionConfig `json:"camera_config,omitempty"`
	Stream       bool                `json:"stream"`
}

// VideoExtensionRequest yêu cầu nối dài video có sẵn
type VideoExtensionRequest struct {
	SourceVideoAssetID string              `json:"source_video_asset_id"`
	Prompt             string              `json:"prompt"`
	ExtensionDuration  int                 `json:"extension_duration"` // 4 hoặc 6 giây
	Model              string              `json:"model,omitempty"`
	PreservePhysics    bool                `json:"preserve_physics"`
	CameraMotion       *CameraMotionConfig `json:"camera_motion,omitempty"`
}

// Upsample4KRequest yêu cầu nâng cấp video lên 4K Ultra HD
type Upsample4KRequest struct {
	VideoAssetID     string `json:"video_asset_id"`
	EnhancementLevel int    `json:"enhancement_level,omitempty"` // 1: Tiêu chuẩn / 2: Tăng chi tiết
	PreserveAudio    bool   `json:"preserve_audio"`
}

// VideoProgressEvent sự kiện stream tiến độ render
type VideoProgressEvent struct {
	Status          string  `json:"status"` // "PROCESSING", "COMPLETED", "FAILED"
	Progress        int     `json:"progress"`
	Message         string  `json:"message,omitempty"`
	VideoURL        string  `json:"video_url,omitempty"`
	Duration        float64 `json:"duration,omitempty"`
	Resolution      string  `json:"resolution,omitempty"`
	CreditsDeducted int     `json:"credits_deducted,omitempty"`
}
