package domain

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// ModelsConfigFile biểu diễn cấu trúc tệp cấu hình configs/models.yaml
type ModelsConfigFile struct {
	GeminiModels []ModelDescriptor `yaml:"gemini_models"`
	FlowModels   []ModelDescriptor `yaml:"flow_models"`
}

var (
	catalogMu           sync.RWMutex
	customGeminiCatalog []ModelDescriptor
	customFlowCatalog   []ModelDescriptor
)

// LoadModelsFile nạp danh mục mô hình động từ tệp YAML (ví dụ: configs/models.yaml)
func LoadModelsFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("không thể đọc file danh mục mô hình tại %s: %w", path, err)
	}
	return LoadModelsFromYAML(data)
}

// LoadModelsFromYAML nạp danh mục mô hình động từ dữ liệu YAML
func LoadModelsFromYAML(data []byte) error {
	var cfg ModelsConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("lỗi cú pháp YAML danh mục mô hình: %w", err)
	}

	catalogMu.Lock()
	defer catalogMu.Unlock()

	if len(cfg.GeminiModels) > 0 {
		customGeminiCatalog = cfg.GeminiModels
	}
	if len(cfg.FlowModels) > 0 {
		customFlowCatalog = cfg.FlowModels
	}
	return nil
}

// SetCustomCatalogs gán trực tiếp danh mục mô hình trong runtime
func SetCustomCatalogs(gemini, flow []ModelDescriptor) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	customGeminiCatalog = gemini
	customFlowCatalog = flow
}

// ResetCatalogsToDefault hoàn nguyên danh mục mô hình về mặc định
func ResetCatalogsToDefault() {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	customGeminiCatalog = nil
	customFlowCatalog = nil
}

// GetGeminiCatalog trả về danh mục mô hình Gemini đã nạp từ config (hoặc mặc định nếu chưa cấu hình)
func GetGeminiCatalog() []ModelDescriptor {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if len(customGeminiCatalog) > 0 {
		result := make([]ModelDescriptor, len(customGeminiCatalog))
		copy(result, customGeminiCatalog)
		return result
	}
	return defaultGeminiCatalog()
}

// GetFlowCatalog trả về danh mục mô hình Flow đã nạp từ config (hoặc mặc định nếu chưa cấu hình)
func GetFlowCatalog() []ModelDescriptor {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if len(customFlowCatalog) > 0 {
		result := make([]ModelDescriptor, len(customFlowCatalog))
		copy(result, customFlowCatalog)
		return result
	}
	return defaultFlowCatalog()
}

func defaultGeminiCatalog() []ModelDescriptor {
	return []ModelDescriptor{
		{
			ID:                "gemini-3.5-flash-lite",
			DisplayName:       "3.5 Flash-Lite",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.5 Flash-Lite",
			ModelTierCode:     1,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
		{
			ID:                "gemini-3.8-flash",
			DisplayName:       "3.8 Flash",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.8 Flash",
			ModelTierCode:     1,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
		{
			ID:                "gemini-3.1-pro",
			DisplayName:       "3.1 Pro",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.1 Pro",
			ModelTierCode:     3,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
	}
}

func defaultFlowCatalog() []ModelDescriptor {
	return []ModelDescriptor{
		{
			ID:                 "veo-3.1-quality",
			DisplayName:        "Veo 3.1 - Cinematic Quality",
			TargetService:      ServiceFlow,
			Capabilities:       []ModelCapability{CapVideo},
			InternalBackendID:  "veo_3_1_quality",
			ModelTierCode:      2,
			CreditCostPerUnit:  100,
			SupportedDurations: []int{4, 6, 8, 10},
			SupportedAspects:   []string{"16:9", "9:16"},
			IsActive:           true,
		},
		{
			ID:                 "veo-3.1-fast",
			DisplayName:        "Veo 3.1 - Fast Video",
			TargetService:      ServiceFlow,
			Capabilities:       []ModelCapability{CapVideo},
			InternalBackendID:  "veo_3_1_fast",
			ModelTierCode:      2,
			CreditCostPerUnit:  20,
			SupportedDurations: []int{4, 6, 8},
			SupportedAspects:   []string{"16:9", "9:16"},
			IsActive:           true,
		},
		{
			ID:                 "veo-3.1-lite",
			DisplayName:        "Veo 3.1 - Lite Video",
			TargetService:      ServiceFlow,
			Capabilities:       []ModelCapability{CapVideo},
			InternalBackendID:  "veo_3_1_lite",
			ModelTierCode:      2,
			CreditCostPerUnit:  10,
			SupportedDurations: []int{4, 6, 8},
			SupportedAspects:   []string{"16:9", "9:16"},
			IsActive:           true,
		},
		{
			ID:                "abra-imagen-3",
			DisplayName:       "Abra Studio / Imagen 3",
			TargetService:     ServiceFlow,
			Capabilities:      []ModelCapability{CapImage},
			InternalBackendID: "abra",
			ModelTierCode:     2,
			CreditCostPerUnit: 7,
			SupportedAspects:  []string{"1:1", "16:9", "9:16", "4:3", "3:4"},
			IsActive:          true,
		},
	}
}
