package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
	_ "dezuxk-gateway/internal/pkg/sqlite"
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

func TestSqliteKeyRepository_TokenUsage(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_tokens.db")

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

	key := &domain.VirtualKey{
		ID:            "vk_token_test",
		KeyHash:       domain.HashKey("sk-dez-tokentest"),
		KeyPrefix:     "sk-dez-toke...est",
		Name:          "Token Test Key",
		Role:          "user",
		RateLimitRPM:  60,
		MaxTokenQuota: 50000,
		IsActive:      true,
		CreatedAt:     time.Now().UTC(),
	}

	if err := repo.Save(ctx, key); err != nil {
		t.Fatalf("save key failed: %v", err)
	}

	// 1. Ghi nhận lượt dùng token
	if err := repo.RecordTokenUsage(ctx, key.ID, 500, 1500); err != nil {
		t.Fatalf("record token usage 1 failed: %v", err)
	}
	if err := repo.RecordTokenUsage(ctx, key.ID, 300, 700); err != nil {
		t.Fatalf("record token usage 2 failed: %v", err)
	}

	// 2. Kiểm tra tổng lũy kế trong key
	found, err := repo.FindByID(ctx, key.ID)
	if err != nil {
		t.Fatalf("find by ID failed: %v", err)
	}

	if found.PromptTokensTotal != 800 {
		t.Errorf("expected prompt tokens 800, got %d", found.PromptTokensTotal)
	}
	if found.CompletionTokensTotal != 2200 {
		t.Errorf("expected completion tokens 2200, got %d", found.CompletionTokensTotal)
	}
	if found.TotalTokens != 3000 {
		t.Errorf("expected total tokens 3000, got %d", found.TotalTokens)
	}
	if found.MaxTokenQuota != 50000 {
		t.Errorf("expected max token quota 50000, got %d", found.MaxTokenQuota)
	}

	// 3. Lấy lịch sử theo key
	history, err := repo.GetTokenUsageHistory(ctx, key.ID, 7)
	if err != nil {
		t.Fatalf("get token usage history failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(history))
	}
	if history[0].TotalTokens != 3000 {
		t.Errorf("expected 3000 total tokens in history, got %d", history[0].TotalTokens)
	}
	if history[0].RequestCount != 2 {
		t.Errorf("expected 2 request counts in history, got %d", history[0].RequestCount)
	}

	// 4. Lấy lịch sử toàn hệ thống
	sysHistory, err := repo.GetSystemTokenUsageHistory(ctx, 7)
	if err != nil {
		t.Fatalf("get system token usage history failed: %v", err)
	}
	if len(sysHistory) != 1 {
		t.Fatalf("expected 1 system history record, got %d", len(sysHistory))
	}
	if sysHistory[0].TotalTokens != 3000 {
		t.Errorf("expected 3000 total tokens in system history, got %d", sysHistory[0].TotalTokens)
	}

	// 5. Ghi nhận token cho master key (hỗ trợ Dashboard & Playground)
	if err := repo.RecordTokenUsage(ctx, "master", 200, 400); err != nil {
		t.Fatalf("record token usage for master failed: %v", err)
	}
	sysHistoryAfterMaster, err := repo.GetSystemTokenUsageHistory(ctx, 7)
	if err != nil {
		t.Fatalf("get system token usage history after master failed: %v", err)
	}
	if sysHistoryAfterMaster[0].TotalTokens != 3600 {
		t.Errorf("expected 3600 total tokens in system history after master, got %d", sysHistoryAfterMaster[0].TotalTokens)
	}
}
