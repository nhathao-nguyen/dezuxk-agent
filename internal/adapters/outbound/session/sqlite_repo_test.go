package session

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

type dummyRefresher struct{}

func (dummyRefresher) RefreshDerivedSecret(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	return nil
}

func TestSqliteSessionRepository_CRUD_Persistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test_sqlite_repo_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test_gateway.db")
	repo, err := NewSqliteSessionRepository(dbPath, dummyRefresher{})
	if err != nil {
		t.Fatalf("NewSqliteSessionRepository failed: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	// 1. Lưu tài khoản
	acc := &domain.ManagedAccount{
		ID:             "user-1",
		Email:          "user1@gmail.com",
		Jar:            domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookie1", "OSID": "flowcookie1"}),
		FlowSNlM0e:     "sn-flow-1",
		GeminiSNlM0e:   "sn-gemini-1",
		CreditsBalance: 500,
		Tier:           2,
		IsHealthy:      true,
		LastRefresh:    time.Now(),
	}

	if err := repo.Save(ctx, acc); err != nil {
		t.Fatalf("Save account failed: %v", err)
	}

	// 2. Tìm lại
	found, err := repo.FindByID(ctx, "user-1")
	if err != nil || found == nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.Email != "user1@gmail.com" || found.CreditsBalance != 500 {
		t.Errorf("Unexpected account data: %+v", found)
	}

	// 3. Test Least-Connections / Round-Robin GetAvailable
	available, err := repo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err != nil || available == nil {
		t.Fatalf("GetAvailable failed: %v", err)
	}
	if available.ID != "user-1" {
		t.Errorf("Expected user-1, got %s", available.ID)
	}
	if available.InFlightReqs != 1 {
		t.Errorf("Expected InFlightReqs = 1, got %d", available.InFlightReqs)
	}

	repo.Release(available, nil)
	if available.InFlightReqs != 0 {
		t.Errorf("Expected InFlightReqs = 0 after release, got %d", available.InFlightReqs)
	}

	// 4. Test Alert khi gặp lỗi 401
	err401 := domain.Unauthenticated("Chat", "", domain.ServiceGemini, "session expired").WithPublicStatus(http.StatusUnauthorized)
	repo.Release(available, err401)

	alerts := repo.GetAlerts()
	if len(alerts) == 0 {
		t.Fatalf("Expected alert after 401, got 0")
	}
	if alerts[0].AccountID != "user-1" {
		t.Errorf("Expected alert for user-1, got %s", alerts[0].AccountID)
	}

	// 5. Test Persistence: Đóng DB và mở lại, dữ liệu phải còn nguyên
	repo.Close()

	repo2, err := NewSqliteSessionRepository(dbPath, dummyRefresher{})
	if err != nil {
		t.Fatalf("NewSqliteSessionRepository reopened failed: %v", err)
	}
	defer repo2.Close()

	reopenedAcc, err := repo2.FindByID(ctx, "user-1")
	if err != nil || reopenedAcc == nil {
		t.Fatalf("Persisted account not found after reopen: %v", err)
	}
	if reopenedAcc.Email != "user1@gmail.com" {
		t.Errorf("Persisted email mismatch: %s", reopenedAcc.Email)
	}

	reopenedAlerts := repo2.GetAlerts()
	if len(reopenedAlerts) == 0 {
		t.Errorf("Expected persisted alerts, got 0")
	}
}
