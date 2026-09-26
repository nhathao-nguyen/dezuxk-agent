package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
)

func TestKeyService_CreateAndValidate(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "master-admin-secret")
	ctx := context.Background()

	// 1. Tạo key mới thành công
	req := domain.CreateKeyRequest{
		Name:               "Client Alpha",
		Role:               "user",
		RateLimitRPM:       60,
		DailyQuotaRequests: 10,
		AllowedModels:      []string{"gemini-2.5-flash", "gemini-2.5-pro"},
	}

	created, err := service.CreateKey(ctx, req)
	if err != nil {
		t.Fatalf("failed to create key: %v", err)
	}

	if created.Key == "" || created.ID == "" {
		t.Fatalf("expected raw key and id to be generated, got %+v", created)
	}
	if created.Name != "Client Alpha" || created.Role != "user" {
		t.Fatalf("unexpected metadata: %+v", created)
	}

	// 2. Validate với raw key hợp lệ
	vKey, err := service.ValidateKey(ctx, created.Key, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("expected key to be valid, got err: %v", err)
	}
	if vKey.ID != created.ID {
		t.Fatalf("expected ID %s, got %s", created.ID, vKey.ID)
	}

	// 3. Validate với Bearer prefix
	vKey, err = service.ValidateKey(ctx, "Bearer "+created.Key, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("expected key with Bearer prefix to be valid, got: %v", err)
	}

	// 4. Validate với model không được phép
	_, err = service.ValidateKey(ctx, created.Key, "veo-2.0-generate-001")
	if !errors.Is(err, domain.ErrModelNotAllowed) {
		t.Fatalf("expected ErrModelNotAllowed, got: %v", err)
	}

	// 5. Validate với key sai
	_, err = service.ValidateKey(ctx, "sk-dez-wrong-invalid-key-here", "")
	if !errors.Is(err, domain.ErrInvalidAPIKey) {
		t.Fatalf("expected ErrInvalidAPIKey, got: %v", err)
	}

	// 6. Validate với key rỗng
	_, err = service.ValidateKey(ctx, "", "")
	if !errors.Is(err, domain.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got: %v", err)
	}
}

func TestKeyService_MasterKeyBypass(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "master-admin-secret-123")
	ctx := context.Background()

	vKey, err := service.ValidateKey(ctx, "master-admin-secret-123", "any-model-at-all")
	if err != nil {
		t.Fatalf("expected master key to be valid, got %v", err)
	}
	if vKey.Role != "admin" || !vKey.IsModelAllowed("any-model") {
		t.Fatalf("expected master key to be admin with full model access, got: %+v", vKey)
	}
}

func TestKeyService_RevokeKey(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "")
	ctx := context.Background()

	created, err := service.CreateKey(ctx, domain.CreateKeyRequest{
		Name: "To Revoke",
		Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Thu hồi key
	if err := service.RevokeKey(ctx, created.ID); err != nil {
		t.Fatalf("failed to revoke key: %v", err)
	}

	// Validate lại phải trả về ErrKeyRevoked
	_, err = service.ValidateKey(ctx, created.Key, "")
	if !errors.Is(err, domain.ErrKeyRevoked) {
		t.Fatalf("expected ErrKeyRevoked, got: %v", err)
	}
}

func TestKeyService_ExpiredKey(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "")
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Hour)
	created, err := service.CreateKey(ctx, domain.CreateKeyRequest{
		Name:      "Expired Client",
		ExpiresAt: &past,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.ValidateKey(ctx, created.Key, "")
	if !errors.Is(err, domain.ErrKeyExpired) {
		t.Fatalf("expected ErrKeyExpired, got: %v", err)
	}
}

func TestKeyService_RateLimitRPM(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "")
	ctx := context.Background()

	created, err := service.CreateKey(ctx, domain.CreateKeyRequest{
		Name:         "RPM Limited",
		RateLimitRPM: 3, // Tối đa 3 req/phút
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3 requests đầu tiên phải thành công
	for i := 1; i <= 3; i++ {
		_, err := service.ValidateKey(ctx, created.Key, "")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}

	// Request thứ 4 phải nhận ErrRateLimitRPMExceeded
	_, err = service.ValidateKey(ctx, created.Key, "")
	if !errors.Is(err, domain.ErrRateLimitRPMExceeded) {
		t.Fatalf("expected ErrRateLimitRPMExceeded, got: %v", err)
	}
}

func TestKeyService_QuotaDecrementor(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "")
	ctx := context.Background()

	created, err := service.CreateKey(ctx, domain.CreateKeyRequest{
		Name:               "Quota Limited",
		DailyQuotaRequests: 2, // 2 req/ngày
	})
	if err != nil {
		t.Fatal(err)
	}

	// Lần tiêu thụ 1
	rem1, err := service.ConsumeQuota(ctx, created.ID)
	if err != nil {
		t.Fatalf("consume 1 failed: %v", err)
	}
	if rem1 != 1 {
		t.Fatalf("expected 1 remaining, got %d", rem1)
	}

	// Lần tiêu thụ 2
	rem2, err := service.ConsumeQuota(ctx, created.ID)
	if err != nil {
		t.Fatalf("consume 2 failed: %v", err)
	}
	if rem2 != 0 {
		t.Fatalf("expected 0 remaining, got %d", rem2)
	}

	// Lần tiêu thụ 3 (vượt hạn ngạch)
	_, err = service.ConsumeQuota(ctx, created.ID)
	if !errors.Is(err, domain.ErrDailyQuotaExceeded) {
		t.Fatalf("expected ErrDailyQuotaExceeded, got: %v", err)
	}

	// ValidateKey cũng phải chặn khi đã dùng hết hạn ngạch
	_, err = service.ValidateKey(ctx, created.Key, "")
	if !errors.Is(err, domain.ErrDailyQuotaExceeded) {
		t.Fatalf("expected ErrDailyQuotaExceeded from ValidateKey, got: %v", err)
	}
}

func TestKeyService_UnlimitedQuota(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "")
	ctx := context.Background()

	created, err := service.CreateKey(ctx, domain.CreateKeyRequest{
		Name:               "Unlimited Client",
		DailyQuotaRequests: 0, // 0 = unlimited
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 50; i++ {
		rem, err := service.ConsumeQuota(ctx, created.ID)
		if err != nil {
			t.Fatalf("consume failed on iter %d: %v", i, err)
		}
		if rem != -1 {
			t.Fatalf("expected -1 for unlimited quota, got %d", rem)
		}
	}
}

func TestKeyService_ListActiveKeys(t *testing.T) {
	repo := session.NewMemoryKeyRepository()
	service := NewKeyService(repo, "")
	ctx := context.Background()

	k1, _ := service.CreateKey(ctx, domain.CreateKeyRequest{Name: "Key 1"})
	k2, _ := service.CreateKey(ctx, domain.CreateKeyRequest{Name: "Key 2"})

	list, err := service.ListActiveKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 active keys, got %d", len(list))
	}

	_ = service.RevokeKey(ctx, k1.ID)

	list, err = service.ListActiveKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != k2.ID {
		t.Fatalf("expected only k2 to be active, got %d keys", len(list))
	}
}
