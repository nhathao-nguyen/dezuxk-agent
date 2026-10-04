package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type scanExtractor struct {
	calls int
}

func (e *scanExtractor) ExtractTokens(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) (string, string, error) {
	e.calls++
	return "fetched-from-origin", "", nil
}

func TestScanAndDiscoverLoadsReadyWithoutOrigin(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "acc")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stored := StoredProfileSession{
		ProfileID:    "acc",
		Email:        "lab@example.com",
		GeminiSNlM0e: "gemini-at-from-file",
		Cookies: map[string]string{
			"__Secure-1PSID":   "psid",
			"__Secure-1PSIDTS": "psidts",
		},
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "session.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	repo := NewMemorySessionRepository(nil)
	models := domain.NewModelRegistry(nil)
	extractor := &scanExtractor{}
	var _ ports.TokenExtractor = extractor
	pm, err := NewProfileManager(&config.Config{
		Profiles: config.ProfilesConfig{BaseDir: dir, ChromeBinary: filepath.Join(dir, "missing-chrome")},
	}, repo, models, extractor)
	if err != nil {
		t.Fatal(err)
	}
	found, err := pm.ScanAndDiscover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if extractor.calls != 0 {
		t.Fatalf("origin calls extract=%d", extractor.calls)
	}
	if models.Count() != 0 {
		t.Fatal("local catalogs must not be activated statically in production scan")
	}
	acc, err := repo.FindByID(context.Background(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	if acc.ServiceState(domain.ServiceGemini) != domain.StateReady {
		t.Fatalf("gemini=%s", acc.ServiceState(domain.ServiceGemini))
	}
	if acc.GetAtToken(domain.ServiceGemini) != "gemini-at-from-file" {
		t.Fatal("startup replaced the stored derived secret")
	}
	var profile *domain.Profile
	for _, item := range found {
		if item.ID == "acc" {
			profile = item
		}
	}
	if profile == nil || !profile.IsLoggedIn {
		t.Fatalf("profile = %+v", profile)
	}
}
