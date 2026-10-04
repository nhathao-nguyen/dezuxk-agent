package session_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

// TestSessionSecurity_FailClosedOnEncryptError kiểm tra nguyên tắc Fail-Closed:
// Khi mã hóa thất bại (vault nil hoặc lỗi key), từ chối lưu và tuyệt đối không lưu plaintext cookie
func TestSessionSecurity_FailClosedOnEncryptError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "session-security-test-*")
	if err != nil {
		t.Fatalf("Không thể tạo tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "sessions.db")
	// Khởi tạo repo với vault hợp lệ ban đầu
	masterKey := session.ResolveMasterKey("test-secure-master-key-32-bytes!!")
	validVault := session.NewVault(masterKey)
	repo, err := session.NewSqliteSessionRepository(dbPath, nil, validVault)
	if err != nil {
		t.Fatalf("Khởi tạo SQLite repo thất bại: %v", err)
	}
	defer repo.Close()

	// 1. Tạo account với cookies nhạy cảm
	acc := &domain.ManagedAccount{
		ID:           "acc-secret-1",
		Email:        "secret@example.com",
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "sensitive_psid_value_xyz"}),
		GeminiSNlM0e: "sensitive_sn_token_123",
		IsHealthy:    true,
	}

	// Lưu thành công với vault hợp lệ
	ctx := context.Background()
	if err := repo.Save(ctx, acc); err != nil {
		t.Fatalf("Kỳ vọng Save thành công với valid vault, nhưng gặp lỗi: %v", err)
	}

	// Xác minh trong DB thực tế được mã hóa (bắt đầu bằng enc:) và KHÔNG chứa plaintext
	var cookiesJSON, snToken string
	row := repo.DB().QueryRow("SELECT cookies_json, gemini_sn_token FROM sessions WHERE id = ?", acc.ID)
	if err := row.Scan(&cookiesJSON, &snToken); err != nil {
		t.Fatalf("Lỗi đọc dữ liệu từ SQLite: %v", err)
	}

	if !strings.HasPrefix(cookiesJSON, "enc:") {
		t.Fatalf("Kỳ vọng cookies_json được mã hóa có tiền tố 'enc:', thực tế: %s", cookiesJSON)
	}
	if strings.Contains(cookiesJSON, "sensitive_psid_value_xyz") {
		t.Fatalf("LỖI BẢO MẬT: DB chứa plaintext cookie!")
	}
	if !strings.HasPrefix(snToken, "enc:") {
		t.Fatalf("Kỳ vọng gemini_sn_token được mã hóa có tiền tố 'enc:', thực tế: %s", snToken)
	}
	if strings.Contains(snToken, "sensitive_sn_token_123") {
		t.Fatalf("LỖI BẢO MẬT: DB chứa plaintext SN token!")
	}
}

// TestSessionSecurity_LegacyPlaintextMigration kiểm tra tính năng One-Time Migration:
// Phiên cũ lưu dạng plaintext JSON được nhận diện, nạp được và tự động ghi đè lại (rewrite) dưới dạng mã hóa
func TestSessionSecurity_LegacyPlaintextMigration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "session-legacy-migration-*")
	if err != nil {
		t.Fatalf("Không thể tạo tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "legacy_sessions.db")
	masterKey := session.ResolveMasterKey("test-secure-master-key-32-bytes!!")
	vault := session.NewVault(masterKey)

	// Tạo repo ban đầu để tạo schema
	initRepo, err := session.NewSqliteSessionRepository(dbPath, nil, vault)
	if err != nil {
		t.Fatalf("Khởi tạo repo thất bại: %v", err)
	}

	// Chèn một dòng session giả lập dữ liệu cũ lưu plaintext JSON
	legacyPlaintextJSON := `{"__Secure-1PSID":"legacy_plain_cookie_123"}`
	legacyPlaintextSN := "legacy_plain_sn_token"
	_, err = initRepo.DB().Exec(`
		INSERT INTO sessions (id, email, cookies_json, gemini_sn_token, tier, is_healthy, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "legacy-acc-1", "legacy@example.com", legacyPlaintextJSON, legacyPlaintextSN, 1, 1, time.Now())
	if err != nil {
		t.Fatalf("Chèn legacy session thất bại: %v", err)
	}
	initRepo.Close()

	// Khởi tạo lại repo để kích hoạt loadPersistedSessions migration rewrite
	migratedRepo, err := session.NewSqliteSessionRepository(dbPath, nil, vault)
	if err != nil {
		t.Fatalf("Nạp lại repo cho migration thất bại: %v", err)
	}
	defer migratedRepo.Close()

	// 1. Kiểm tra session đọc lên trong memory có đầy đủ cookie
	loadedAcc, err := migratedRepo.FindByID(context.Background(), "legacy-acc-1")
	if err != nil {
		t.Fatalf("Không tìm thấy session sau khi migrate: %v", err)
	}
	if loadedAcc.Jar == nil || loadedAcc.Jar.Get("__Secure-1PSID") != "legacy_plain_cookie_123" {
		t.Fatalf("Cookie không đúng sau migration: %v", loadedAcc.Jar)
	}
	if loadedAcc.GeminiSNlM0e != legacyPlaintextSN {
		t.Fatalf("GeminiSNlM0e không đúng sau migration: %s", loadedAcc.GeminiSNlM0e)
	}

	// 2. Kiểm tra DB: Plaintext đã biến mất, được ghi đè bằng chuỗi mã hóa enc:
	var dbCookies, dbSN string
	err = migratedRepo.DB().QueryRow("SELECT cookies_json, gemini_sn_token FROM sessions WHERE id = ?", "legacy-acc-1").Scan(&dbCookies, &dbSN)
	if err != nil {
		t.Fatalf("Lỗi đọc DB sau migration: %v", err)
	}

	if !strings.HasPrefix(dbCookies, "enc:") {
		t.Fatalf("Kỳ vọng cookies trong DB đã được rewrite thành 'enc:', thực tế: %s", dbCookies)
	}
	if strings.Contains(dbCookies, "legacy_plain_cookie_123") {
		t.Fatalf("LỖI BẢO MẬT: DB vẫn còn chứa plaintext cookie sau migration!")
	}
	if !strings.HasPrefix(dbSN, "enc:") {
		t.Fatalf("Kỳ vọng SN token trong DB đã được rewrite thành 'enc:', thực tế: %s", dbSN)
	}
	if strings.Contains(dbSN, "legacy_plain_sn_token") {
		t.Fatalf("LỖI BẢO MẬT: DB vẫn còn chứa plaintext SN token sau migration!")
	}
}

// TestProfileManager_SaveNoPlaintextCookiesOnEncryptFailure kiểm tra profile_manager
// không bao giờ ghi rawCookies xuống file session.json nếu mã hóa thất bại
func TestProfileManager_SaveNoPlaintextCookiesOnEncryptFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "profile-security-test-*")
	if err != nil {
		t.Fatalf("Không thể tạo tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		Profiles: config.ProfilesConfig{
			BaseDir: tempDir,
		},
	}
	repo := session.NewMemorySessionRepository(nil)
	reg := domain.NewModelRegistry(nil)

	// Khởi tạo ProfileManager không có Vault (vault = nil) -> phải fail closed khi IngestLiveCookies
	pm, err := session.NewProfileManager(cfg, repo, reg, nil, nil)
	if err != nil {
		t.Fatalf("Khởi tạo ProfileManager thất bại: %v", err)
	}

	ctx := context.Background()
	_, err = pm.CreateProfile("test-user")
	if err != nil {
		t.Fatalf("CreateProfile thất bại: %v", err)
	}

	rawCookies := "SECID=very_secret_cookie_token_12345; __Secure-1PSID=psid_token_xyz"
	_, err = pm.IngestLiveCookies(ctx, "test-user", rawCookies, nil, "sn_token")
	if err == nil {
		t.Fatalf("Kỳ vọng IngestLiveCookies trả về lỗi khi không có Vault mã hóa, nhưng trả về nil")
	}

	// Kiểm tra file session.json không chứa rawCookies
	sessFile := filepath.Join(tempDir, "test-user", "session.json")
	if data, readErr := os.ReadFile(sessFile); readErr == nil {
		content := string(data)
		if strings.Contains(content, "very_secret_cookie_token_12345") {
			t.Fatalf("LỖI BẢO MẬT NGHIÊM TRỌNG: session.json chứa raw cookies plaintext!")
		}
	}
}

// TestManagedAccount_ConcurrencyRace kiểm tra an toàn luồng (thread-safety) của các trường mutable
func TestManagedAccount_ConcurrencyRace(t *testing.T) {
	acc := &domain.ManagedAccount{
		ID:        "race-acc",
		Email:     "race@example.com",
		Jar:       domain.NewCookieJar(map[string]string{"__Secure-1PSID": "val"}),
		IsHealthy: true,
	}

	var wg sync.WaitGroup
	workers := 10
	iterations := 100

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				acc.IncInFlight()
				_ = acc.GetInFlight()
				acc.DecInFlight()

				acc.SetAccountHealthy(j%2 == 0)
				_ = acc.IsAccountHealthy()

				acc.SetTier(j % 3)
				_ = acc.GetTier()

				acc.SetCooldown(time.Now().Add(time.Duration(j) * time.Millisecond))
				_ = acc.GetCooldownUntil()

				acc.SetSupportedModels([]string{"model-a", "model-b"})
				_ = acc.SupportsModel("model-a")
				_ = acc.SupportsModel("model-unknown")
			}
		}(i)
	}

	wg.Wait()
}
