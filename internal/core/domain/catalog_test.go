package domain_test

import (
	"os"
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestCatalog_LoadFromYAMLAndFallback(t *testing.T) {
	domain.ResetCatalogsToDefault()
	t.Cleanup(domain.ResetCatalogsToDefault)

	// 1. Kiểm tra fallback khi chưa nạp
	defaultGemini := domain.GetGeminiCatalog()
	if len(defaultGemini) < 3 {
		t.Fatalf("kỳ vọng ít nhất 3 model mặc định gemini, nhận %d", len(defaultGemini))
	}

	// 2. Nạp từ YAML tùy chỉnh
	yamlContent := `
gemini_models:
  - id: "custom-gemini-test"
    display_name: "Custom Gemini"
    target_service: "gemini"
    capabilities: ["chat"]
    internal_backend_id: "custom-test"
    model_tier_code: 1
    credit_cost_per_unit: 0
    is_active: true
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test_models.yaml")
	if err := os.WriteFile(filePath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("lỗi tạo file temp yaml: %v", err)
	}

	if err := domain.LoadModelsFile(filePath); err != nil {
		t.Fatalf("LoadModelsFile trả về lỗi: %v", err)
	}

	loadedGemini := domain.GetGeminiCatalog()
	if len(loadedGemini) != 1 || loadedGemini[0].ID != "custom-gemini-test" {
		t.Fatalf("Gemini catalog không khớp cấu hình YAML: %+v", loadedGemini)
	}

	// 3. Reset về default
	domain.ResetCatalogsToDefault()
	resetGemini := domain.GetGeminiCatalog()
	if len(resetGemini) != len(defaultGemini) {
		t.Fatalf("Reset thất bại, nhận %d models", len(resetGemini))
	}
}
