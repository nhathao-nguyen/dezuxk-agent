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
}

var (
	catalogMu           sync.RWMutex
	customGeminiCatalog []ModelDescriptor
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
	return nil
}

// SetCustomCatalogs gán trực tiếp danh mục mô hình trong runtime
func SetCustomCatalogs(gemini []ModelDescriptor) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	customGeminiCatalog = gemini
}

// ResetCatalogsToDefault hoàn nguyên danh mục mô hình về mặc định
func ResetCatalogsToDefault() {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	customGeminiCatalog = nil
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

func defaultGeminiCatalog() []ModelDescriptor {
	return []ModelDescriptor{
		{
			ID:                "gemini-3.5-flash-lite",
			DisplayName:       "3.5 Flash-Lite",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.5 Flash-Lite",
			ModeID:            "8c46e95b1a07cecc",
			ModelTierCode:     1,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
		{
			ID:                "gemini-3.8-flash",
			DisplayName:       "3.8 Flash (Default)",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.8 Flash",
			ModeID:            "56fdd199312815e2",
			ModelTierCode:     1,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
		{
			ID:                "gemini-3.8-flash-thinking",
			DisplayName:       "3.8 Flash Thinking",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.8 Flash",
			ModeID:            "56fdd199312815e2",
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
			ModeID:            "e6fa609c3fa255c0",
			ModelTierCode:     3,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
		{
			ID:                "gemini-3.1-pro-thinking",
			DisplayName:       "3.1 Pro Thinking",
			TargetService:     ServiceGemini,
			Capabilities:      []ModelCapability{CapChat},
			InternalBackendID: "3.1 Pro",
			ModeID:            "e6fa609c3fa255c0",
			ModelTierCode:     3,
			CreditCostPerUnit: 0,
			IsActive:          true,
		},
	}
}
