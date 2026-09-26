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

type scanFlow struct {
	calls int
}

func (f *scanFlow) GetCreditsBalance(ctx context.Context, account *domain.ManagedAccount) (domain.FlowCreditBalance, error) {
	f.calls++
	return domain.FlowCreditBalance{}, nil
}
func (f *scanFlow) RegisterSessionLock(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	f.calls++
	return nil
}
func (f *scanFlow) CreateProject(ctx context.Context, account *domain.ManagedAccount, title string) (string, error) {
	f.calls++
	return "", nil
}
func (f *scanFlow) ListProjects(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowProject, error) {
	f.calls++
	return nil, nil
}
func (f *scanFlow) MoveProjectToTrash(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	f.calls++
	return nil
}
func (f *scanFlow) ListTrash(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowTrashProject, error) {
	f.calls++
	return nil, nil
}
func (f *scanFlow) RestoreProject(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	f.calls++
	return nil
}
func (f *scanFlow) DeleteProjectPermanently(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	f.calls++
	return nil
}
func (f *scanFlow) GetActiveModels(ctx context.Context, account *domain.ManagedAccount) (map[string]bool, error) {
	f.calls++
	return map[string]bool{"abra": true}, nil
}
func (f *scanFlow) ListVoicePersonas(ctx context.Context, account *domain.ManagedAccount, projectUUID string) ([]domain.VoicePersona, error) {
	f.calls++
	return domain.DefaultVoicePersonas(), nil
}

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
		FlowSNlM0e:   "flow-at-from-file",
		GeminiSNlM0e: "gemini-at-from-file",
		Cookies: map[string]string{
			"__Secure-1PSID":   "psid",
			"__Secure-1PSIDTS": "psidts",
			"OSID":             "osid",
			"__Secure-OSID":    "secure-osid",
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
	flow := &scanFlow{}
	extractor := &scanExtractor{}
	var _ ports.TokenExtractor = extractor
	pm, err := NewProfileManager(&config.Config{
		Profiles: config.ProfilesConfig{BaseDir: dir, ChromeBinary: filepath.Join(dir, "missing-chrome")},
	}, repo, models, extractor, flow)
	if err != nil {
		t.Fatal(err)
	}
	found, err := pm.ScanAndDiscover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if flow.calls != 0 || extractor.calls != 0 {
		t.Fatalf("origin calls flow=%d extract=%d", flow.calls, extractor.calls)
	}
	if models.Count() == 0 {
		t.Fatal("local catalogs were not activated")
	}
	acc, err := repo.FindByID(context.Background(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	if acc.ServiceState(domain.ServiceFlow) != domain.StateReady || acc.ServiceState(domain.ServiceGemini) != domain.StateReady {
		t.Fatalf("flow=%s gemini=%s", acc.ServiceState(domain.ServiceFlow), acc.ServiceState(domain.ServiceGemini))
	}
	if acc.GetAtToken(domain.ServiceFlow) != "flow-at-from-file" || acc.GetAtToken(domain.ServiceGemini) != "gemini-at-from-file" {
		t.Fatal("startup replaced the stored derived secret")
	}
	var profile *domain.Profile
	for _, item := range found {
		if item.ID == "acc" {
			profile = item
		}
	}
	if profile == nil || !profile.IsLoggedIn || profile.FlowCredits != 0 {
		t.Fatalf("profile = %+v", profile)
	}
}
