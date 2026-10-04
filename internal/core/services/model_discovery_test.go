package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/google"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type mockDiscoveryProvider struct {
	discoverFn func(ctx context.Context, account *domain.ManagedAccount) ([]domain.ModelDescriptor, error)
}

func (m *mockDiscoveryProvider) DiscoverModels(ctx context.Context, account *domain.ManagedAccount) ([]domain.ModelDescriptor, error) {
	if m.discoverFn != nil {
		return m.discoverFn(ctx, account)
	}
	return nil, nil
}

// TestModelDiscovery_ParseUpstreamErrors kiểm tra phản hồi lỗi từ Google Upstream:
// Không được bịa đặt (fabricate) model khi gặp phản hồi rỗng hoặc sai cấu trúc
func TestModelDiscovery_ParseUpstreamErrors(t *testing.T) {
	// 1. Phản hồi rỗng -> ErrDiscoveryEmptyResponse
	_, err := google.ParseUpstreamDiscoveryResponse("", 1)
	if err == nil || !errors.Is(err, google.ErrDiscoveryEmptyResponse) {
		t.Fatalf("Kỳ vọng ErrDiscoveryEmptyResponse khi payload rỗng, nhận: %v", err)
	}

	// 2. Phản hồi sai cấu trúc (malformed / field moved) -> ErrDiscoveryProtocolDrift
	malformed := `)]}'\n[["wrb.fr","otAQ7b","{\"unknown_key\": true}"]]`
	_, err = google.ParseUpstreamDiscoveryResponse(malformed, 1)
	if err == nil || !errors.Is(err, google.ErrDiscoveryProtocolDrift) {
		t.Fatalf("Kỳ vọng ErrDiscoveryProtocolDrift khi payload thay đổi cấu trúc, nhận: %v", err)
	}
}

// TestModelDiscovery_PartialFailurePreservesLKG kiểm tra khi 1 tài khoản quét lỗi và 1 tài khoản thành công:
// Last-Known-Good của tài khoản lỗi không bị xóa, và không tự tạo model đoán
func TestModelDiscovery_PartialFailurePreservesLKG(t *testing.T) {
	catalogRepo := session.NewMemoryModelCatalogRepository()
	sessionRepo := session.NewMemorySessionRepository(nil)
	modelRegistry := domain.NewModelRegistry(nil)

	ctx := context.Background()

	// Tài khoản A và B
	accA := &domain.ManagedAccount{
		ID:        "acc-a",
		Email:     "a@example.com",
		Jar:       domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookie-a"}),
		IsHealthy: true,
	}
	accB := &domain.ManagedAccount{
		ID:        "acc-b",
		Email:     "b@example.com",
		Jar:       domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookie-b"}),
		IsHealthy: true,
	}
	_ = sessionRepo.Save(ctx, accA)
	_ = sessionRepo.Save(ctx, accB)

	// Ban đầu cả 2 account đều có LKG models trong catalogRepo
	now := time.Now()
	flashDesc := domain.ModelDescriptor{
		ID:            "gemini-3.8-flash",
		DisplayName:   "3.8 Flash",
		TargetService: domain.ServiceGemini,
		Capabilities:  []domain.ModelCapability{domain.CapChat},
		IsActive:      true,
		FirstSeenAt:   now,
		LastSeenAt:    now,
	}
	proDesc := domain.ModelDescriptor{
		ID:            "gemini-3.1-pro",
		DisplayName:   "3.1 Pro",
		TargetService: domain.ServiceGemini,
		Capabilities:  []domain.ModelCapability{domain.CapChat},
		IsActive:      true,
		FirstSeenAt:   now,
		LastSeenAt:    now,
	}
	_ = catalogRepo.UpsertModels(ctx, []domain.ModelDescriptor{flashDesc, proDesc})
	_ = catalogRepo.UpsertAccountModels(ctx, accA.ID, []string{flashDesc.ID}, true)
	_ = catalogRepo.UpsertAccountModels(ctx, accB.ID, []string{proDesc.ID}, true)

	// Discovery mock: Acc A thành công với Flash, Acc B gặp lỗi upstream timeout
	provider := &mockDiscoveryProvider{
		discoverFn: func(ctx context.Context, account *domain.ManagedAccount) ([]domain.ModelDescriptor, error) {
			if account.ID == "acc-a" {
				return []domain.ModelDescriptor{flashDesc}, nil
			}
			return nil, google.ErrDiscoveryUpstreamError
		},
	}

	discoverySvc := services.NewModelDiscoveryService(
		provider,
		catalogRepo,
		sessionRepo,
		modelRegistry,
		nil,
		nil,
		"test-node",
	)
	discoverySvc.SetIsLeader(true)

	res, err := discoverySvc.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh không nên trả lỗi khi có ít nhất 1 account thành công: %v", err)
	}
	if res.AccountsSucceeded != 1 || res.AccountsFailed != 1 {
		t.Fatalf("Kết quả aggregation không đúng: succeeded=%d, failed=%d", res.AccountsSucceeded, res.AccountsFailed)
	}

	// Kiểm tra LKG của acc-b vẫn còn nguyên vẹn trong DB
	bModels, err := catalogRepo.ListAccountModels(ctx, accB.ID)
	if err != nil || len(bModels) == 0 {
		t.Fatalf("LKG của acc-b bị mất: %v", err)
	}
	if !bModels[0].IsAvailable || bModels[0].ModelID != proDesc.ID {
		t.Fatalf("LKG của acc-b bị biến đổi: %+v", bModels[0])
	}
}

// TestModelDiscovery_EmptyDBClearsRegistry kiểm tra khi DB trả về danh sách rỗng:
// modelRegistry.ReplaceAll([]) phải xóa sạch các model cũ, không được giữ cache cũ
func TestModelDiscovery_EmptyDBClearsRegistry(t *testing.T) {
	catalogRepo := session.NewMemoryModelCatalogRepository()
	modelRegistry := domain.NewModelRegistry(nil)

	// Ban đầu gán sẵn 2 model vào registry
	initialModels := []domain.ModelDescriptor{
		{ID: "m1", DisplayName: "Model 1", TargetService: domain.ServiceGemini, IsActive: true},
		{ID: "m2", DisplayName: "Model 2", TargetService: domain.ServiceGemini, IsActive: true},
	}
	modelRegistry.ReplaceAll(initialModels)
	if modelRegistry.Count() != 2 {
		t.Fatalf("Registry ban đầu phải có 2 models, nhận: %d", modelRegistry.Count())
	}

	// DB hoàn toàn rỗng (catalogRepo không có model nào)
	svc := services.NewModelDiscoveryService(
		nil,
		catalogRepo,
		nil,
		modelRegistry,
		nil,
		nil,
		"test-node",
	)

	// ReloadFromStorage phải thay thế sạch registry
	if err := svc.ReloadFromStorage(context.Background()); err != nil {
		t.Fatalf("ReloadFromStorage thất bại: %v", err)
	}

	if modelRegistry.Count() != 0 {
		t.Fatalf("Kỳ vọng Registry bị xóa sạch khi DB rỗng (count=0), thực tế count=%d", modelRegistry.Count())
	}
}

// TestAccount_UnknownEligibilityDeniesExactRouting kiểm tra:
// SupportedModels = nil / rỗng nghĩa là UNKNOWN eligibility, không được giả định hỗ trợ model trong production
func TestAccount_UnknownEligibilityDeniesExactRouting(t *testing.T) {
	acc := &domain.ManagedAccount{
		ID:        "acc-unknown",
		Email:     "test@example.com",
		Tier:      2, // Tier Pro
		IsHealthy: true,
	}

	// SupportedModels rỗng
	acc.SetSupportedModels(nil)

	// Không được suy đoán hỗ trợ model Pro hay Flash chỉ dựa vào Tier
	if acc.SupportsModel("gemini-3.1-pro") {
		t.Fatalf("LỖI: Account với unknown eligibility (SupportedModels=nil) không được hỗ trợ exact model")
	}
	if acc.SupportsModel("gemini-3.8-flash") {
		t.Fatalf("LỖI: Account với unknown eligibility (SupportedModels=nil) không được hỗ trợ exact model")
	}

	// Chỉ khi được xác thực và gán SupportedModels rõ ràng mới trả về true
	acc.SetSupportedModels([]string{"gemini-3.8-flash"})
	if !acc.SupportsModel("gemini-3.8-flash") {
		t.Fatalf("Kỳ vọng hỗ trợ gemini-3.8-flash sau khi gán eligibility")
	}
	if acc.SupportsModel("gemini-3.1-pro") {
		t.Fatalf("Không được hỗ trợ gemini-3.1-pro khi không nằm trong SupportedModels")
	}
}
