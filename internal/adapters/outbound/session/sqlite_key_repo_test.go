package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
	_ "github.com/mattn/go-sqlite3"
)

func TestSqliteKeyRepository_CRUDAndQuota(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_keys.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo, err := NewSqliteKeyRepository(db)
	if err != nil {
		t.Fatalf("failed to create sqlite key repo: %v", err)
	}

	ctx := context.Background()

	// 1. Lưu key mới
	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	key := &domain.VirtualKey{
		ID:                 "vk_test_123",
		KeyHash:            domain.HashKey("sk-dez-rawsecret123"),
		KeyPrefix:          "sk-dez-raw...123",
		Name:               "Test App",
		Role:               "user",
		RateLimitRPM:       100,
		DailyQuotaRequests: 3,
		UsedToday:          0,
		LastUsedDate:       time.Now().UTC().Format("2006-01-02"),
		AllowedModels:      []string{"gemini-2.5-flash", "gemini-2.5-pro"},
		IsActive:           true,
		ExpiresAt:          &exp,
		CreatedAt:          time.Now().UTC().Truncate(time.Second),
	}

	if err := repo.Save(ctx, key); err != nil {
		t.Fatalf("save key failed: %v", err)
	}

	// 2. Tra cứu theo Hash
	found, err := repo.FindByKeyHash(ctx, key.KeyHash)
	if err != nil {
		t.Fatalf("find by hash failed: %v", err)
	}
	if found.ID != key.ID || found.Name != key.Name || found.Role != key.Role {
		t.Fatalf("mismatched key data: %+v", found)
	}
	if len(found.AllowedModels) != 2 || found.AllowedModels[0] != "gemini-2.5-flash" {
		t.Fatalf("mismatched allowed models: %v", found.AllowedModels)
	}

	// 3. Tra cứu theo ID
	foundByID, err := repo.FindByID(ctx, key.ID)
	if err != nil {
		t.Fatalf("find by id failed: %v", err)
	}
	if foundByID.KeyHash != key.KeyHash {
		t.Fatalf("expected hash %s, got %s", key.KeyHash, foundByID.KeyHash)
	}

	// 4. Tiêu thụ Daily Quota (Hạn ngạch 3)
	today := time.Now().UTC().Format("2006-01-02")

	// Lần 1: Còn 2
	rem, err := repo.ConsumeDailyQuota(ctx, key.ID, today)
	if err != nil || rem != 2 {
		t.Fatalf("expected rem 2, got %d, err %v", rem, err)
	}

	// Lần 2: Còn 1
	rem, err = repo.ConsumeDailyQuota(ctx, key.ID, today)
	if err != nil || rem != 1 {
		t.Fatalf("expected rem 1, got %d, err %v", rem, err)
	}

	// Lần 3: Còn 0
	rem, err = repo.ConsumeDailyQuota(ctx, key.ID, today)
	if err != nil || rem != 0 {
		t.Fatalf("expected rem 0, got %d, err %v", rem, err)
	}

	// Lần 4: Quá hạn ngạch -> ErrDailyQuotaExceeded
	_, err = repo.ConsumeDailyQuota(ctx, key.ID, today)
	if !errors.Is(err, domain.ErrDailyQuotaExceeded) {
		t.Fatalf("expected ErrDailyQuotaExceeded, got %v", err)
	}

	// 5. Date Rollover: Chuyển sang ngày mới (tomorrow) -> Quota được làm mới tự động!
	tomorrow := time.Now().Add(24 * time.Hour).UTC().Format("2006-01-02")
	rem, err = repo.ConsumeDailyQuota(ctx, key.ID, tomorrow)
	if err != nil || rem != 2 {
		t.Fatalf("expected reset quota with rem 2 on new day, got %d, err %v", rem, err)
	}

	// 6. Liệt kê key đang hoạt động
	activeKeys, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active failed: %v", err)
	}
	if len(activeKeys) != 1 {
		t.Fatalf("expected 1 active key, got %d", len(activeKeys))
	}

	// 7. Thu hồi key
	if err := repo.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoke key failed: %v", err)
	}

	// Sau khi thu hồi, ListActive phải trả về 0
	activeKeys, err = repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active after revoke failed: %v", err)
	}
	if len(activeKeys) != 0 {
		t.Fatalf("expected 0 active keys after revoke, got %d", len(activeKeys))
	}

	// Tiêu thụ quota trên key đã thu hồi phải báo lỗi ErrKeyRevoked
	_, err = repo.ConsumeDailyQuota(ctx, key.ID, today)
	if !errors.Is(err, domain.ErrKeyRevoked) {
		t.Fatalf("expected ErrKeyRevoked, got %v", err)
	}
}
