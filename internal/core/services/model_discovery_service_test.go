package services_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

type memoryEventBus struct {
	mu          sync.Mutex
	subscribers map[string][]chan []byte
}

func newMemoryEventBus() *memoryEventBus {
	return &memoryEventBus{subscribers: make(map[string][]chan []byte)}
}

func (b *memoryEventBus) Publish(ctx context.Context, topic string, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subscribers[topic] {
		select {
		case ch <- payload:
		default:
		}
	}
	return nil
}

func (b *memoryEventBus) Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan []byte, 16)
	b.subscribers[topic] = append(b.subscribers[topic], ch)
	unsub := func() {}
	return ch, unsub, nil
}

type fakeDiscoveryProvider struct {
	modelsFunc func(account *domain.ManagedAccount) ([]domain.ModelDescriptor, error)
}

func (f *fakeDiscoveryProvider) DiscoverModels(ctx context.Context, account *domain.ManagedAccount) ([]domain.ModelDescriptor, error) {
	if f.modelsFunc != nil {
		return f.modelsFunc(account)
	}
	return nil, nil
}

func TestModelDiscovery_CrossNodePropagationAndPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventBus := newMemoryEventBus()
	catalogRepo := session.NewMemoryModelCatalogRepository()
	sessionRepo := session.NewMemorySessionRepository(nil)
	metrics := domain.NewContractMetrics()

	// 1. Thêm 2 tài khoản vào session repo: AccA (Tier 1 Free) và AccB (Tier 2 Pro)
	accA := &domain.ManagedAccount{
		ID:           "account-flash-only",
		Email:        "flash@example.com",
		Tier:         1,
		IsHealthy:    true,
		HealthStatus: domain.HealthStatusHealthy,
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookieA"}),
	}
	accA.CommitServiceState(domain.ServiceGemini, domain.StateReady)
	_ = sessionRepo.Save(ctx, accA)

	accB := &domain.ManagedAccount{
		ID:           "account-pro-capable",
		Email:        "pro@example.com",
		Tier:         2,
		IsHealthy:    true,
		HealthStatus: domain.HealthStatusHealthy,
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookieB"}),
	}
	accB.CommitServiceState(domain.ServiceGemini, domain.StateReady)
	_ = sessionRepo.Save(ctx, accB)

	// 2. Mock provider trả về model theo tier
	discoveryProvider := &fakeDiscoveryProvider{
		modelsFunc: func(account *domain.ManagedAccount) ([]domain.ModelDescriptor, error) {
			if account.Tier <= 1 {
				return []domain.ModelDescriptor{
					{
						ID:            "gemini-3.8-flash",
						DisplayName:   "3.8 Flash",
						TargetService: domain.ServiceGemini,
						Capabilities:  []domain.ModelCapability{domain.CapChat},
						ModelTierCode: 1,
						IsActive:      true,
					},
				}, nil
			}
			return []domain.ModelDescriptor{
				{
					ID:            "gemini-3.8-flash",
					DisplayName:   "3.8 Flash",
					TargetService: domain.ServiceGemini,
					Capabilities:  []domain.ModelCapability{domain.CapChat},
					ModelTierCode: 1,
					IsActive:      true,
				},
				{
					ID:            "gemini-3.1-pro",
					DisplayName:   "3.1 Pro",
					TargetService: domain.ServiceGemini,
					Capabilities:  []domain.ModelCapability{domain.CapChat},
					ModelTierCode: 3,
					IsActive:      true,
				},
				{
					ID:            "gemini-new-experimental",
					DisplayName:   "Gemini New Experimental",
					TargetService: domain.ServiceGemini,
					Capabilities:  []domain.ModelCapability{domain.CapChat, domain.CapThinking},
					ModelTierCode: 3,
					IsActive:      true,
				},
			}, nil
		},
	}

	// 3. Node A và Node B
	registryA := domain.NewModelRegistry(nil)
	serviceA := services.NewModelDiscoveryService(discoveryProvider, catalogRepo, sessionRepo, registryA, eventBus, metrics, "node-a")
	serviceA.SetIsLeader(true)
	if err := serviceA.Start(ctx); err != nil {
		t.Fatalf("Service A start failed: %v", err)
	}
	defer serviceA.Stop()

	registryB := domain.NewModelRegistry(nil)
	serviceB := services.NewModelDiscoveryService(discoveryProvider, catalogRepo, sessionRepo, registryB, eventBus, metrics, "node-b")
	serviceB.SetIsLeader(false)
	if err := serviceB.Start(ctx); err != nil {
		t.Fatalf("Service B start failed: %v", err)
	}
	defer serviceB.Stop()

	// 4. Node A kích hoạt Refresh
	res, err := serviceA.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if res.AccountsScanned != 2 {
		t.Errorf("expected 2 accounts scanned, got %d", res.AccountsScanned)
	}

	// Chờ Redis propagation sang Node B
	time.Sleep(50 * time.Millisecond)

	// Node B phải thấy toàn bộ 3 models
	if registryB.Count() != 3 {
		t.Fatalf("Node B registry should have 3 models, got %d", registryB.Count())
	}
	if _, ok := registryB.Get("gemini-new-experimental"); !ok {
		t.Errorf("Node B should have discovered unknown model gemini-new-experimental")
	}

	// 5. Kiểm tra account eligibility
	if !accA.SupportsModel("gemini-3.8-flash") {
		t.Errorf("accA should support gemini-3.8-flash")
	}
	if accA.SupportsModel("gemini-3.1-pro") {
		t.Errorf("accA should NOT support gemini-3.1-pro")
	}
	if !accB.SupportsModel("gemini-3.1-pro") {
		t.Errorf("accB should support gemini-3.1-pro")
	}
	if !accB.SupportsModel("gemini-new-experimental") {
		t.Errorf("accB should support gemini-new-experimental")
	}

	// 6. Giả lập Node B khởi động lại (Restart Node B)
	serviceB.Stop()
	registryBRestarted := domain.NewModelRegistry(nil)
	serviceBRestarted := services.NewModelDiscoveryService(discoveryProvider, catalogRepo, sessionRepo, registryBRestarted, eventBus, metrics, "node-b")
	if err := serviceBRestarted.Start(ctx); err != nil {
		t.Fatalf("Service B restart failed: %v", err)
	}
	defer serviceBRestarted.Stop()

	if registryBRestarted.Count() != 3 {
		t.Errorf("After restart from PostgreSQL, Node B should retain 3 models, got %d", registryBRestarted.Count())
	}
}

func TestModelDiscovery_StaleMarkingOnRemoval(t *testing.T) {
	ctx := context.Background()
	catalogRepo := session.NewMemoryModelCatalogRepository()

	now := time.Now()
	// Chu kỳ 1: Upstream có model A, B, C
	oldTime := now.Add(-2 * time.Hour)
	mOld := domain.ModelDescriptor{
		ID:            "model-c-deprecated",
		DisplayName:   "Model C Deprecated",
		TargetService: domain.ServiceGemini,
		Capabilities:  []domain.ModelCapability{domain.CapChat},
		ModelTierCode: 1,
		IsActive:      true,
		LastSeenAt:    oldTime,
	}
	mNew := domain.ModelDescriptor{
		ID:            "model-a-active",
		DisplayName:   "Model A Active",
		TargetService: domain.ServiceGemini,
		Capabilities:  []domain.ModelCapability{domain.CapChat},
		ModelTierCode: 1,
		IsActive:      true,
		LastSeenAt:    now,
	}

	if err := catalogRepo.UpsertModels(ctx, []domain.ModelDescriptor{mOld, mNew}); err != nil {
		t.Fatalf("UpsertModels failed: %v", err)
	}

	// Đánh dấu các model không xuất hiện trong 1 giờ qua là stale
	staleBefore := now.Add(-1 * time.Hour)
	marked, err := catalogRepo.MarkStaleModels(ctx, staleBefore)
	if err != nil {
		t.Fatalf("MarkStaleModels failed: %v", err)
	}
	if marked != 1 {
		t.Fatalf("expected 1 model marked stale, got %d", marked)
	}

	// Kiểm tra: Model C bị IsActive = false nên ListModels (chỉ lấy is_available=true) không còn trả về Model C
	available, err := catalogRepo.ListModels(ctx, "")
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("expected 1 available model, got %d", len(available))
	}
	if available[0].ID != "model-a-active" {
		t.Fatalf("expected model-a-active to be active, got %s", available[0].ID)
	}
}

func TestModelDiscovery_AccountEligibilityAndQuotaAwareSelection(t *testing.T) {
	ctx := context.Background()
	sessionRepo := session.NewMemorySessionRepository(nil)

	// AccA: Chỉ hỗ trợ Flash, Quota dồi dào
	accA := &domain.ManagedAccount{
		ID:           "acc-flash-only",
		Email:        "flash@example.com",
		Tier:         1,
		IsHealthy:    true,
		HealthStatus: domain.HealthStatusHealthy,
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookieA"}),
	}
	accA.SetSupportedModels([]string{"gemini-3.8-flash"})
	accA.CommitServiceState(domain.ServiceGemini, domain.StateReady)
	_ = sessionRepo.Save(ctx, accA)

	// AccB: Hỗ trợ Flash + Pro, Quota dồi dào
	accB := &domain.ManagedAccount{
		ID:           "acc-pro-capable",
		Email:        "pro@example.com",
		Tier:         2,
		IsHealthy:    true,
		HealthStatus: domain.HealthStatusHealthy,
		Jar:          domain.NewCookieJar(map[string]string{"__Secure-1PSID": "cookieB"}),
	}
	accB.SetSupportedModels([]string{"gemini-3.8-flash", "gemini-3.1-pro"})
	accB.CommitServiceState(domain.ServiceGemini, domain.StateReady)
	_ = sessionRepo.Save(ctx, accB)

	// 1. Khi request model Pro -> Bắt buộc phải chọn AccB
	selectedPro, err := sessionRepo.GetAvailableForModel(ctx, domain.ServiceGemini, "gemini-3.1-pro", 0)
	if err != nil {
		t.Fatalf("GetAvailableForModel failed for Pro: %v", err)
	}
	if selectedPro.ID != "acc-pro-capable" {
		t.Fatalf("expected acc-pro-capable for Pro request, got %s", selectedPro.ID)
	}

	// 2. Giả lập AccB bị cạn Quota hoặc rơi vào Cooldown
	accB.HealthStatus = domain.HealthStatusCooldown
	accB.IsHealthy = false
	_ = sessionRepo.Save(ctx, accB)

	// 3. Khi request model Pro -> Phải trả về lỗi dứt khoát không có tài khoản phù hợp (không fallback sai sang AccA)
	_, errUnavailable := sessionRepo.GetAvailableForModel(ctx, domain.ServiceGemini, "gemini-3.1-pro", 0)
	if errUnavailable == nil {
		t.Fatalf("expected error when no eligible account has quota/health for Pro model")
	}

	// 4. Khi request model Flash -> Vẫn phục vụ được bình thường qua AccA
	selectedFlash, errFlash := sessionRepo.GetAvailableForModel(ctx, domain.ServiceGemini, "gemini-3.8-flash", 0)
	if errFlash != nil {
		t.Fatalf("GetAvailableForModel failed for Flash: %v", errFlash)
	}
	if selectedFlash.ID != "acc-flash-only" {
		t.Fatalf("expected acc-flash-only for Flash request, got %s", selectedFlash.ID)
	}
}

func TestModelDiscovery_DynamicDefaultSelectorWithoutHardcodedNames(t *testing.T) {
	// Kiểm tra Selector hoàn toàn dựa trên metadata, tier và capability
	// KHÔNG chứa bất kỳ tên model Gemini nào hardcoded trong logic kiểm thử này
	syntheticCatalog := []domain.ModelDescriptor{
		{
			ID:                "synthetic-fast-lite",
			DisplayName:       "Synthetic Fast Lite",
			TargetService:     domain.ServiceGemini,
			Capabilities:      []domain.ModelCapability{domain.CapChat},
			ModelTierCode:     1,
			InternalBackendID: "synthetic-fast",
			IsActive:          true,
		},
		{
			ID:                "synthetic-standard-workhorse",
			DisplayName:       "Synthetic Standard (Default)",
			TargetService:     domain.ServiceGemini,
			Capabilities:      []domain.ModelCapability{domain.CapChat},
			ModelTierCode:     1,
			InternalBackendID: "synthetic-standard",
			IsActive:          true,
		},
		{
			ID:                "synthetic-frontier-reasoner",
			DisplayName:       "Synthetic Frontier Reasoner",
			TargetService:     domain.ServiceGemini,
			Capabilities:      []domain.ModelCapability{domain.CapChat, domain.CapThinking},
			ModelTierCode:     3,
			InternalBackendID: "synthetic-frontier",
			IsActive:          true,
		},
	}

	registry := domain.NewModelRegistry(syntheticCatalog)

	// Test 1: Preference = PrefFast -> Phải chọn synthetic-fast-lite
	fastModel, okFast := registry.ResolveModel("", domain.ModelRequirement{
		Capability: domain.CapChat,
		Preference: domain.PrefFast,
	})
	if !okFast || fastModel.ID != "synthetic-fast-lite" {
		t.Fatalf("expected synthetic-fast-lite for PrefFast, got ok=%v, id=%s", okFast, fastModel.ID)
	}

	// Test 2: Preference = PrefBalanced -> Phải chọn synthetic-standard-workhorse
	balancedModel, okBal := registry.ResolveModel("", domain.ModelRequirement{
		Capability: domain.CapChat,
		Preference: domain.PrefBalanced,
	})
	if !okBal || balancedModel.ID != "synthetic-standard-workhorse" {
		t.Fatalf("expected synthetic-standard-workhorse for PrefBalanced, got ok=%v, id=%s", okBal, balancedModel.ID)
	}

	// Test 3: Preference = PrefBest -> Phải chọn synthetic-frontier-reasoner
	bestModel, okBest := registry.ResolveModel("", domain.ModelRequirement{
		Capability: domain.CapChat,
		Preference: domain.PrefBest,
	})
	if !okBest || bestModel.ID != "synthetic-frontier-reasoner" {
		t.Fatalf("expected synthetic-frontier-reasoner for PrefBest, got ok=%v, id=%s", okBest, bestModel.ID)
	}
}

func TestTenantRuntimeSettings_IsolationAndPersistence(t *testing.T) {
	ctx := context.Background()
	repo := session.NewMemoryTenantRuntimeSettingsRepository()

	// Tenant A settings
	settingsA := &domain.TenantRuntimeSettings{
		TenantID:       "tenant-alpha",
		PreferredModel: "model-security-audit",
		ModelPolicy:    "strict",
		Persona:        "You are Senior Security Auditor specialized in threat modeling.",
		ProjectContext: "Repo: Alpha Security Core",
		UpdatedAt:      time.Now(),
	}
	if err := repo.Upsert(ctx, settingsA); err != nil {
		t.Fatalf("Upsert tenant A failed: %v", err)
	}

	// Tenant B settings
	settingsB := &domain.TenantRuntimeSettings{
		TenantID:       "tenant-beta",
		PreferredModel: "model-code-assistant",
		ModelPolicy:    "fast",
		Persona:        "You are Junior Code Reviewer focused on readability.",
		ProjectContext: "Repo: Beta Frontend App",
		UpdatedAt:      time.Now(),
	}
	if err := repo.Upsert(ctx, settingsB); err != nil {
		t.Fatalf("Upsert tenant B failed: %v", err)
	}

	// Fetch và xác minh phân lập dữ liệu (No leakage)
	gotA, errA := repo.Get(ctx, "tenant-alpha")
	if errA != nil {
		t.Fatalf("Get tenant A failed: %v", errA)
	}
	if gotA.Persona != settingsA.Persona || gotA.PreferredModel != settingsA.PreferredModel {
		t.Errorf("Tenant A settings mismatch, got persona=%q, model=%q", gotA.Persona, gotA.PreferredModel)
	}

	gotB, errB := repo.Get(ctx, "tenant-beta")
	if errB != nil {
		t.Fatalf("Get tenant B failed: %v", errB)
	}
	if gotB.Persona != settingsB.Persona || gotB.PreferredModel != settingsB.PreferredModel {
		t.Errorf("Tenant B settings mismatch, got persona=%q, model=%q", gotB.Persona, gotB.PreferredModel)
	}

	// Tenant không tồn tại
	gotNone, errNone := repo.Get(ctx, "tenant-non-existent")
	if errNone != nil {
		t.Fatalf("Get nonexistent tenant returned error: %v", errNone)
	}
	if gotNone != nil {
		t.Errorf("expected nil for nonexistent tenant, got %+v", gotNone)
	}
}
