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

func TestModelRegistry_ResolveGeminiModel(t *testing.T) {
	catalog := domain.GetGeminiCatalog()
	mr := domain.NewModelRegistry(catalog)

	// 1. Khớp chính xác
	exact, ok := mr.ResolveGeminiModel("gemini-3.8-flash")
	if !ok || exact.ID != "gemini-3.8-flash" {
		t.Fatalf("kỳ vọng tìm thấy gemini-3.8-flash, nhận ok=%v, id=%s", ok, exact.ID)
	}

	// 2. Chứa pro -> gemini-3.1-pro
	pro, ok := mr.ResolveGeminiModel("cursor-pro-experiment")
	if !ok || pro.ID != "gemini-3.1-pro" {
		t.Fatalf("kỳ vọng phân giải thành gemini-3.1-pro, nhận ok=%v, id=%s", ok, pro.ID)
	}

	// 3. Tên tùy ý / alias Cursor -> gemini-3.8-flash
	flash, ok := mr.ResolveGeminiModel("cursor-agent-test")
	if !ok || flash.ID != "gemini-3.8-flash" {
		t.Fatalf("kỳ vọng phân giải thành gemini-3.8-flash, nhận ok=%v, id=%s", ok, flash.ID)
	}

	// 4. Chuỗi rỗng -> gemini-3.8-flash
	empty, ok := mr.ResolveGeminiModel("")
	if !ok || empty.ID != "gemini-3.8-flash" {
		t.Fatalf("kỳ vọng phân giải chuỗi rỗng thành gemini-3.8-flash, nhận ok=%v, id=%s", ok, empty.ID)
	}

	// 5. Registry rỗng
	emptyMR := domain.NewModelRegistry(nil)
	_, okEmpty := emptyMR.ResolveGeminiModel("cursor-agent-test")
	if okEmpty {
		t.Fatalf("kỳ vọng trả về false khi registry rỗng")
	}
}
