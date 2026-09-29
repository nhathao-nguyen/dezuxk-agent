package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func TestProfileProxyAndVaultEncryption(t *testing.T) {
	tempDir := t.TempDir()
	vault := NewVault("test-passphrase-for-proxy-and-vault")

	cfg := &config.Config{
		Profiles: config.ProfilesConfig{
			BaseDir:      tempDir,
			ChromeBinary: filepath.Join(tempDir, "mock-chrome"),
			CDPPortStart: 9300,
		},
	}

	repo := NewMemorySessionRepository(nil)
	models := domain.NewModelRegistry(nil)
	extractor := &scanExtractor{}

	pm, err := NewProfileManager(cfg, repo, models, extractor, vault)
	if err != nil {
		t.Fatalf("NewProfileManager failed: %v", err)
	}

	profileID := "test-proxy-user"
	proxyURL := "http://user:pass@10.0.0.1:8080"

	// 1. CreateProfileWithProxy
	prof, err := pm.CreateProfileWithProxy(profileID, proxyURL)
	if err != nil {
		t.Fatalf("CreateProfileWithProxy failed: %v", err)
	}
	if prof.Proxy != proxyURL {
		t.Fatalf("expected proxy %s, got %s", proxyURL, prof.Proxy)
	}

	// 2. IngestLiveCookiesWithProxy
	cookies := map[string]string{
		"__Secure-1PSID":   "test-psid",
		"__Secure-1PSIDTS": "test-psidts",
		"SNlM0e":           "test-snlm0e",
	}

	acc, err := pm.IngestLiveCookiesWithProxy(context.Background(), profileID, "proxy-user@example.com", cookies, "MockUA/1.0", proxyURL)
	if err != nil {
		t.Fatalf("IngestLiveCookiesWithProxy failed: %v", err)
	}

	if acc.GetProxy() != proxyURL {
		t.Fatalf("expected account proxy %s, got %s", proxyURL, acc.GetProxy())
	}

	// 3. Inspect session.json on disk to verify cookies are ENCRYPTED at rest
	sessionPath := filepath.Join(tempDir, profileID, "session.json")
	fileBytes, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read session.json: %v", err)
	}

	var stored StoredProfileSession
	if err := json.Unmarshal(fileBytes, &stored); err != nil {
		t.Fatalf("failed to parse session.json: %v", err)
	}

	if stored.Proxy != proxyURL {
		t.Fatalf("expected stored proxy %s, got %s", proxyURL, stored.Proxy)
	}

	// Plaintext cookies should NOT be stored or should be empty
	if len(stored.Cookies) > 0 {
		t.Fatalf("security violation: plaintext cookies found in session.json: %v", stored.Cookies)
	}

	// EncryptedCookies MUST have enc:v1: prefix
	if !strings.HasPrefix(stored.EncryptedCookies, EncryptedPrefix) {
		t.Fatalf("expected encrypted_cookies to have prefix %s, got %s", EncryptedPrefix, stored.EncryptedCookies)
	}

	// 4. Test ScanAndDiscover with a fresh ProfileManager to verify decryption at startup
	repo2 := NewMemorySessionRepository(nil)
	models2 := domain.NewModelRegistry(nil)
	pm2, err := NewProfileManager(cfg, repo2, models2, extractor, vault)
	if err != nil {
		t.Fatalf("NewProfileManager 2 failed: %v", err)
	}

	discovered, err := pm2.ScanAndDiscover(context.Background())
	if err != nil {
		t.Fatalf("ScanAndDiscover failed: %v", err)
	}

	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered profile, got %d", len(discovered))
	}

	if discovered[0].Proxy != proxyURL {
		t.Fatalf("expected discovered proxy %s, got %s", proxyURL, discovered[0].Proxy)
	}

	acc2, err := repo2.FindByID(context.Background(), profileID)
	if err != nil {
		t.Fatalf("failed to find restored account: %v", err)
	}

	if acc2.GetProxy() != proxyURL {
		t.Fatalf("expected restored account proxy %s, got %s", proxyURL, acc2.GetProxy())
	}

	if !acc2.Jar.HasKey("__Secure-1PSID") {
		t.Fatalf("decrypted cookie jar missing keys: %v", acc2.Jar.GetAll())
	}

	// 5. Test SetProfileProxy
	newProxy := "socks5://192.168.1.50:1080"
	if err := pm2.SetProfileProxy(profileID, newProxy); err != nil {
		t.Fatalf("SetProfileProxy failed: %v", err)
	}

	acc3, _ := repo2.FindByID(context.Background(), profileID)
	if acc3.GetProxy() != newProxy {
		t.Fatalf("expected updated proxy %s, got %s", newProxy, acc3.GetProxy())
	}
}
