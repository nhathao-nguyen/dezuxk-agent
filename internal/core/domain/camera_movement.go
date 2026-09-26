package domain

import "fmt"

// CameraMotionPreset định nghĩa các góc quay mẫu tiêu chuẩn
type CameraMotionPreset string

const (
	PresetCustomVector CameraMotionPreset = "CUSTOM_VECTOR"
	PresetOrbit        CameraMotionPreset = "ORBIT"
	PresetDollyZoom    CameraMotionPreset = "DOLLY_ZOOM"
	PresetPanTilt      CameraMotionPreset = "PAN_TILT"
	PresetStatic       CameraMotionPreset = "STATIC"
	PresetFlyIn        CameraMotionPreset = "FLY_IN"
)

// CameraMotionConfig cấu hình chuyển động máy quay 3 chiều
type CameraMotionConfig struct {
	MotionPreset     CameraMotionPreset `json:"motion_preset"`
	MotionVector     CameraMotionVector `json:"motion_vector"`
	SpeedMultiplier  float64            `json:"speed_multiplier"`  // 0.5 .. 2.0 (mặc định 1.0)
	SmoothnessFactor float64            `json:"smoothness_factor"` // 0.0 .. 1.0 (mặc định 0.8)
}

// Validate kiểm tra tính hợp lệ của vector chuyển động
func (c *CameraMotionConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.SpeedMultiplier <= 0 {
		c.SpeedMultiplier = 1.0
	}
	if c.SmoothnessFactor <= 0 {
		c.SmoothnessFactor = 0.8
	}
	v := c.MotionVector
	if v.PanHorizontal < -1.0 || v.PanHorizontal > 1.0 {
		return fmt.Errorf("pan_horizontal nằm ngoài phạm vi [-1.0, 1.0]: %f", v.PanHorizontal)
	}
	if v.TiltVertical < -1.0 || v.TiltVertical > 1.0 {
		return fmt.Errorf("tilt_vertical nằm ngoài phạm vi [-1.0, 1.0]: %f", v.TiltVertical)
	}
	if v.ZoomDepth < -1.0 || v.ZoomDepth > 1.0 {
		return fmt.Errorf("zoom_depth nằm ngoài phạm vi [-1.0, 1.0]: %f", v.ZoomDepth)
	}
	if v.OrbitTrajectory < -1.0 || v.OrbitTrajectory > 1.0 {
		return fmt.Errorf("orbit_trajectory nằm ngoài phạm vi [-1.0, 1.0]: %f", v.OrbitTrajectory)
	}
	return nil
}

// BuildWireObject xuất sang map phù hợp để serialize vào options của StreamChat
func (c *CameraMotionConfig) BuildWireObject() map[string]any {
	if c == nil {
		return nil
	}
	preset := c.MotionPreset
	if preset == "" {
		preset = PresetCustomVector
	}
	speed := c.SpeedMultiplier
	if speed <= 0 {
		speed = 1.0
	}
	smooth := c.SmoothnessFactor
	if smooth <= 0 {
		smooth = 0.8
	}
	vec := map[string]any{
		"pan_horizontal":   c.MotionVector.PanHorizontal,
		"tilt_vertical":    c.MotionVector.TiltVertical,
		"zoom_depth":       c.MotionVector.ZoomDepth,
		"orbit_trajectory": c.MotionVector.OrbitTrajectory,
	}
	if c.MotionVector.TruckLateral != 0 {
		vec["truck_lateral"] = c.MotionVector.TruckLateral
	}
	if c.MotionVector.PedestalVertical != 0 {
		vec["pedestal_vertical"] = c.MotionVector.PedestalVertical
	}
	return map[string]any{
		"motion_preset":     string(preset),
		"motion_vector":     vec,
		"speed_multiplier":  speed,
		"smoothness_factor": smooth,
	}
}
