package multinode_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
)

// mockExtractor là TokenExtractor giả lập dùng trong môi trường kiểm thử
type mockExtractor struct{}

func (m *mockExtractor) ExtractTokens(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) (string, string, error) {
	return "extracted-sn-token", "extracted-cfb2h", nil
}

func TestClusterSessionPersistence_CrossNodeEncryption(t *testing.T) {
	tempDir := t.TempDir()
	sharedDBPath := filepath.Join(tempDir, "shared_cluster.db")

	const masterKeyShared = "cluster-shared-master-key-32b-sec!"
	const masterKeyRogue = "different-unauthorized-master-key!"

	vaultA := session.NewVault(masterKeyShared)
	vaultB := session.NewVault(masterKeyShared)
	vaultC := session.NewVault(masterKeyShared)
	vaultRogue := session.NewVault(masterKeyRogue)

	// 0. Fingerprint check (Requirement 10)
	if vaultA.Fingerprint() != vaultB.Fingerprint() || vaultA.Fingerprint() != vaultC.Fingerprint() {
		t.Fatalf("kỳ vọng fingerprint giống nhau cho cùng master key: A=%s, B=%s, C=%s",
			vaultA.Fingerprint(), vaultB.Fingerprint(), vaultC.Fingerprint())
	}
	if vaultA.Fingerprint() == vaultRogue.Fingerprint() {
		t.Fatalf("kỳ vọng fingerprint khác nhau cho master key khác nhau")
	}

	extractor := &mockExtractor{}
	refresherA := google.NewDerivedSecretRefresher(extractor)
	refresherB := google.NewDerivedSecretRefresher(extractor)
	refresherC := google.NewDerivedSecretRefresher(extractor)
	refresherRogue := google.NewDerivedSecretRefresher(extractor)

	ctx := context.Background()

	// 1. Node A khởi tạo và ghi phiên Google được mã hóa đối xứng AES-256-GCM
	repoA, err := session.NewSqliteSessionRepository(sharedDBPath, refresherA, vaultA)
	if err != nil {
		t.Fatalf("khởi tạo repoA thất bại: %v", err)
	}

	syntheticCookies := map[string]string{
		"__Secure-1PSID":   "synthetic_psid_node_a_token_secret_998877",
		"__Secure-1PSIDTS": "synthetic_psidts_node_a_token_secret_112233",
		"OSID":             "synthetic_osid_node_a_flow_token_445566",
	}

	acc := &domain.ManagedAccount{
		ID:           "acc-cluster-01",
		Email:        "cluster-prod-user@example.com",
		Jar:          domain.NewCookieJar(syntheticCookies),
		GeminiSNlM0e: "sn-test-initial-token-node-a",
		UserAgent:    "Mozilla/5.0 (X11; Linux x86_64) DezuxkCluster/2.0",
		Tier:         2,
		IsHealthy:    true,
		HealthStatus: domain.HealthStatusHealthy,
		LastRefresh:  time.Now(),
	}

	if err := repoA.Save(ctx, acc); err != nil {
		t.Fatalf("Node A lưu account thất bại: %v", err)
	}
	_ = repoA.Close()

	// 2. Node B (cùng master key) đọc và giải mã thành công
	repoB, err := session.NewSqliteSessionRepository(sharedDBPath, refresherB, vaultB)
	if err != nil {
		t.Fatalf("khởi tạo repoB thất bại: %v", err)
	}

	accFromB, err := repoB.FindByID(ctx, "acc-cluster-01")
	if err != nil || accFromB == nil {
		t.Fatalf("Node B không tìm thấy account 'acc-cluster-01': %v", err)
	}

	if accFromB.Email != "cluster-prod-user@example.com" {
		t.Errorf("Node B: email không khớp: %s", accFromB.Email)
	}
	if accFromB.GeminiSNlM0e != "sn-test-initial-token-node-a" {
		t.Errorf("Node B: SNlM0e không khớp: %s", accFromB.GeminiSNlM0e)
	}
	if accFromB.Jar == nil {
		t.Fatalf("Node B: cookie jar bị nil")
	}

	for k, expectedVal := range syntheticCookies {
		val := accFromB.Jar.Get(k)
		if val != expectedVal {
			t.Errorf("Node B giải mã cookie %s không đúng: got %q, want %q", k, val, expectedVal)
		}
	}
	_ = repoB.Close()

	// 3. Khởi động lại cụm - Node C (cùng master key) tiếp quản và giải mã thành công
	repoC, err := session.NewSqliteSessionRepository(sharedDBPath, refresherC, vaultC)
	if err != nil {
		t.Fatalf("khởi tạo repoC thất bại: %v", err)
	}

	accFromC, err := repoC.FindByID(ctx, "acc-cluster-01")
	if err != nil || accFromC == nil {
		t.Fatalf("Node C không tìm thấy account sau khi restart: %v", err)
	}
	if accFromC.Jar.Get("__Secure-1PSID") != "synthetic_psid_node_a_token_secret_998877" {
		t.Errorf("Node C không đọc được cookie sau khi restart")
	}
	_ = repoC.Close()

	// 4. Node Rogue (dùng Master Key khác) thất bại an toàn, không lộ cookie và không crash database
	repoRogue, err := session.NewSqliteSessionRepository(sharedDBPath, refresherRogue, vaultRogue)
	if err != nil {
		t.Fatalf("khởi tạo repoRogue không nên crash database: %v", err)
	}

	accFromRogue, err := repoRogue.FindByID(ctx, "acc-cluster-01")
	if err == nil && accFromRogue != nil && accFromRogue.Jar != nil {
		// Do sai master key, việc giải mã AES-256-GCM phải thất bại và KHÔNG ĐƯỢC chứa giá trị bí mật
		decryptedVal := accFromRogue.Jar.Get("__Secure-1PSID")
		if decryptedVal == "synthetic_psid_node_a_token_secret_998877" {
			t.Fatalf("LỖI BẢO MẬT: Node với Master Key khác lại giải mã được cookie thật!")
		}
	}
	_ = repoRogue.Close()
}
