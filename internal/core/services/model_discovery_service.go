package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const (
	TopicModelCatalogChanged = "model_catalog_changed"
	TopicAccountChanged      = "runtime.account.changed"
	DefaultRefreshInterval   = 15 * time.Minute
	DefaultStaleThreshold    = 1 * time.Hour
)

type DiscoveryResult struct {
	AccountsScanned   int `json:"accounts_scanned"`
	AccountsSucceeded int `json:"accounts_succeeded"`
	AccountsFailed    int `json:"accounts_failed"`
	ModelsSeen        int `json:"models_seen"`
	ModelsAdded       int `json:"models_added"`
	ModelsUpdated     int `json:"models_updated"`
}

type CatalogChangedEvent struct {
	NodeID         string    `json:"node_id"`
	CatalogVersion int64     `json:"catalog_version"`
	UpdatedAt      time.Time `json:"updated_at"`
	Count          int       `json:"count"`
}

// ModelDiscoveryService điều phối toàn bộ quy trình discovery mô hình động, đồng bộ PostgreSQL và lan truyền sự kiện Redis
type ModelDiscoveryService struct {
	mu                   sync.RWMutex
	refreshMu            sync.Mutex
	discovery            ports.ModelDiscoveryProvider
	catalogRepo          ports.ModelCatalogRepository
	sessionRepo          ports.SessionRepository
	modelRegistry        *domain.ModelRegistry
	eventBus             ports.EventBus
	metrics              *domain.ContractMetrics
	nodeID               string
	refreshInterval      time.Duration
	staleThreshold       time.Duration
	discoveryConcurrency int
	perAccountTimeout    time.Duration
	globalTimeout        time.Duration
	localGeneration      int64
	stopCh               chan struct{}
	running              bool
	isLeader             bool
}

func NewModelDiscoveryService(
	discovery ports.ModelDiscoveryProvider,
	catalogRepo ports.ModelCatalogRepository,
	sessionRepo ports.SessionRepository,
	modelRegistry *domain.ModelRegistry,
	eventBus ports.EventBus,
	metrics *domain.ContractMetrics,
	nodeID string,
) *ModelDiscoveryService {
	if nodeID == "" {
		nodeID = "gateway-node"
	}
	return &ModelDiscoveryService{
		discovery:            discovery,
		catalogRepo:          catalogRepo,
		sessionRepo:          sessionRepo,
		modelRegistry:        modelRegistry,
		eventBus:             eventBus,
		metrics:              metrics,
		nodeID:               nodeID,
		refreshInterval:      DefaultRefreshInterval,
		staleThreshold:       DefaultStaleThreshold,
		discoveryConcurrency: 5,
		perAccountTimeout:    20 * time.Second,
		globalTimeout:        2 * time.Minute,
		stopCh:               make(chan struct{}),
	}
}

func (s *ModelDiscoveryService) SetRefreshInterval(d time.Duration) {
	if d > 0 {
		s.mu.Lock()
		s.refreshInterval = d
		s.mu.Unlock()
	}
}

func (s *ModelDiscoveryService) SetStaleThreshold(d time.Duration) {
	if d > 0 {
		s.mu.Lock()
		s.staleThreshold = d
		s.mu.Unlock()
	}
}

func (s *ModelDiscoveryService) SetIsLeader(isLeader bool) {
	s.mu.Lock()
	s.isLeader = isLeader
	s.mu.Unlock()
}

func (s *ModelDiscoveryService) SetDiscoveryConcurrency(c int) {
	if c > 0 {
		s.mu.Lock()
		s.discoveryConcurrency = c
		s.mu.Unlock()
	}
}

func (s *ModelDiscoveryService) SetPerAccountTimeout(d time.Duration) {
	if d > 0 {
		s.mu.Lock()
		s.perAccountTimeout = d
		s.mu.Unlock()
	}
}

func (s *ModelDiscoveryService) SetGlobalTimeout(d time.Duration) {
	if d > 0 {
		s.mu.Lock()
		s.globalTimeout = d
		s.mu.Unlock()
	}
}

func (s *ModelDiscoveryService) GetLocalGeneration() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.localGeneration
}

func (s *ModelDiscoveryService) getGlobalTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.globalTimeout > 0 {
		return s.globalTimeout
	}
	return 2 * time.Minute
}

// Start khởi chạy worker lắng nghe sự kiện Redis và chu kỳ discovery định kỳ ngầm
func (s *ModelDiscoveryService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	// 1. Nạp ban đầu từ PostgreSQL vào in-memory cache
	if err := s.ReloadFromStorage(ctx); err != nil {
		log.Printf("[Model Discovery Warning] Nạp ban đầu từ storage gặp lỗi: %v", err)
	}

	// 2. Lắng nghe sự kiện lan truyền qua Redis EventBus
	if s.eventBus != nil {
		// Topic model catalog changed
		eventCh, unsubCatalog, err := s.eventBus.Subscribe(ctx, TopicModelCatalogChanged)
		if err != nil {
			log.Printf("[Model Discovery Warning] Không thể subscribe topic %s: %v", TopicModelCatalogChanged, err)
		} else {
			go func() {
				defer unsubCatalog()
				for {
					select {
					case <-ctx.Done():
						return
					case <-s.stopCh:
						return
					case msg, ok := <-eventCh:
						if !ok {
							return
						}
						var ev CatalogChangedEvent
						if err := json.Unmarshal(msg, &ev); err == nil {
							if ev.NodeID != s.nodeID || ev.CatalogVersion > s.GetLocalGeneration() {
								log.Printf("[Model Discovery] Nhận tín hiệu catalog thay đổi từ node %s (gen %d), nạp lại từ PostgreSQL...", ev.NodeID, ev.CatalogVersion)
								_ = s.ReloadFromStorage(ctx)
							}
						} else {
							_ = s.ReloadFromStorage(ctx)
						}
					}
				}
			}()
		}

		// Topic account changed (debounced)
		accountCh, unsubAccount, err := s.eventBus.Subscribe(ctx, TopicAccountChanged)
		if err != nil {
			log.Printf("[Model Discovery Warning] Không thể subscribe topic %s: %v", TopicAccountChanged, err)
		} else {
			go func() {
				defer unsubAccount()
				var debounceTimer *time.Timer
				var debounceCh <-chan time.Time

				for {
					select {
					case <-ctx.Done():
						if debounceTimer != nil {
							debounceTimer.Stop()
						}
						return
					case <-s.stopCh:
						if debounceTimer != nil {
							debounceTimer.Stop()
						}
						return
					case _, ok := <-accountCh:
						if !ok {
							return
						}
						if debounceTimer != nil {
							debounceTimer.Stop()
						}
						debounceTimer = time.NewTimer(800 * time.Millisecond)
						debounceCh = debounceTimer.C
					case <-debounceCh:
						s.mu.RLock()
						leader := s.isLeader
						noBus := s.eventBus == nil
						s.mu.RUnlock()

						if leader || noBus {
							log.Printf("[Model Discovery] Tài khoản thay đổi, kích hoạt refresh model discovery ngầm...")
							go func() {
								refreshCtx, cancel := context.WithTimeout(context.Background(), s.getGlobalTimeout())
								defer cancel()
								_, _ = s.Refresh(refreshCtx)
							}()
						} else {
							log.Printf("[Model Discovery] Worker node nhận tín hiệu tài khoản thay đổi, đồng bộ từ PostgreSQL...")
							go func() {
								_ = s.ReloadFromStorage(context.Background())
							}()
						}
					}
				}
			}()
		}
	}

	// 3. Chu kỳ hòa giải (Reconciliation loop) định kỳ với Jitter
	go s.runPeriodicReconciliation(ctx)

	return nil
}

func (s *ModelDiscoveryService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.running = false
		close(s.stopCh)
	}
}

// ReloadFromStorage đồng bộ danh mục mô hình từ PostgreSQL vào bộ nhớ cache ModelRegistry
func (s *ModelDiscoveryService) ReloadFromStorage(ctx context.Context) error {
	if s.catalogRepo == nil {
		return nil
	}
	models, err := s.catalogRepo.ListModels(ctx, "")
	if err != nil {
		return fmt.Errorf("lỗi đọc models từ catalog repository: %w", err)
	}

	// Thay thế toàn bộ registry bằng snapshot PostgreSQL (kể cả models rỗng)
	if s.modelRegistry != nil {
		s.modelRegistry.ReplaceAll(models)
		if s.metrics != nil {
			s.metrics.SetRuntimeModelCatalogSize(int64(len(models)))
		}
		log.Printf("[Model Discovery] Đã đồng bộ %d mô hình động vào runtime cache.", len(models))
	}

	// Đồng bộ lại ma trận eligibility cho các tài khoản hiện hữu
	if s.sessionRepo != nil {
		accounts := s.sessionRepo.ListAll(ctx)
		for _, acc := range accounts {
			if acc != nil {
				elList, err := s.catalogRepo.ListAccountModels(ctx, acc.ID)
				var supported []string
				if err == nil {
					for _, el := range elList {
						if el.IsEligible && el.IsAvailable {
							supported = append(supported, el.ModelID)
						}
					}
				}
				// Gán đúng danh sách từ DB (hoặc rỗng nếu không có) để tránh giữ cache cũ
				acc.SetSupportedModels(supported)
				if s.metrics != nil {
					s.metrics.SetRuntimeAccountModelEligibility(acc.ID, int64(len(supported)))
				}
			}
		}
	}

	if gen, err := s.catalogRepo.GetCatalogGeneration(ctx, domain.ServiceGemini); err == nil {
		s.mu.Lock()
		s.localGeneration = gen
		s.mu.Unlock()
	}

	return nil
}

// DiscoverAndSync là bí danh của Refresh phục vụ discovery đồng bộ
func (s *ModelDiscoveryService) DiscoverAndSync(ctx context.Context) (DiscoveryResult, error) {
	return s.Refresh(ctx)
}

// Refresh thực hiện một chu kỳ quét discovery trên toàn bộ tài khoản khả dụng với bounded worker pool
func (s *ModelDiscoveryService) Refresh(ctx context.Context) (DiscoveryResult, error) {
	var result DiscoveryResult
	if s.sessionRepo == nil || s.discovery == nil {
		return result, fmt.Errorf("session repo hoặc discovery provider chưa được khởi tạo")
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	accounts := s.sessionRepo.ListAll(ctx)
	var eligibleAccounts []*domain.ManagedAccount
	for _, acc := range accounts {
		if acc == nil || !acc.IsAccountHealthy() || !acc.ServiceReady(domain.ServiceGemini) {
			continue
		}
		if acc.GetHealthStatus() == domain.HealthStatusQuotaExhausted || acc.GetHealthStatus() == domain.HealthStatusAuthExpired {
			continue
		}
		eligibleAccounts = append(eligibleAccounts, acc)
	}

	if len(eligibleAccounts) == 0 {
		return result, nil
	}

	s.mu.RLock()
	concurrency := s.discoveryConcurrency
	perAccountTimeout := s.perAccountTimeout
	globalTimeout := s.globalTimeout
	s.mu.RUnlock()

	if concurrency <= 0 {
		concurrency = 5
	}
	if perAccountTimeout <= 0 {
		perAccountTimeout = 20 * time.Second
	}
	if globalTimeout <= 0 {
		globalTimeout = 2 * time.Minute
	}

	globalCtx, cancelGlobal := context.WithTimeout(ctx, globalTimeout)
	defer cancelGlobal()

	type accountResult struct {
		accountID string
		models    []domain.ModelDescriptor
		err       error
	}

	resultsChan := make(chan accountResult, len(eligibleAccounts))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, acc := range eligibleAccounts {
		wg.Add(1)
		go func(a *domain.ManagedAccount) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-globalCtx.Done():
				resultsChan <- accountResult{accountID: a.ID, err: globalCtx.Err()}
				return
			}
			defer func() { <-sem }()

			accCtx, cancelAcc := context.WithTimeout(globalCtx, perAccountTimeout)
			defer cancelAcc()

			mods, err := s.discovery.DiscoverModels(accCtx, a)
			resultsChan <- accountResult{accountID: a.ID, models: mods, err: err}
		}(acc)
	}

	wg.Wait()
	close(resultsChan)

	now := time.Now()
	globalMap := make(map[string]domain.ModelDescriptor)

	for res := range resultsChan {
		result.AccountsScanned++
		if res.err != nil {
			result.AccountsFailed++
			log.Printf("[Model Discovery Warning] Quét model thất bại trên account %s: %v", res.accountID, res.err)
			if s.metrics != nil {
				s.metrics.IncRuntimeModelDiscoveryFail()
			}
			// Giữ nguyên Last-Known-Good trong PostgreSQL, không ghi đè model đoán
			continue
		}

		result.AccountsSucceeded++
		if s.metrics != nil {
			s.metrics.IncRuntimeModelDiscoverySuccess()
		}

		var accModelIDs []string
		for _, m := range res.models {
			accModelIDs = append(accModelIDs, m.ID)
			if existing, ok := globalMap[m.ID]; ok {
				existing.LastSeenAt = now
				globalMap[m.ID] = existing
			} else {
				m.FirstSeenAt = now
				m.LastSeenAt = now
				m.UpdatedAt = now
				m.IsActive = true
				globalMap[m.ID] = m
				result.ModelsSeen++
			}
		}

		// Cập nhật ma trận eligibility dạng snapshot transactional trong PostgreSQL
		if s.catalogRepo != nil {
			_ = s.catalogRepo.UpsertAccountModels(ctx, res.accountID, accModelIDs, true)
		}
	}

	// Nếu không có tài khoản nào thành công, không thay đổi runtime catalog trong PostgreSQL
	if result.AccountsSucceeded == 0 {
		return result, nil
	}

	var globalList []domain.ModelDescriptor
	for _, m := range globalMap {
		globalList = append(globalList, m)
	}

	// 2. Lưu trữ bền vững vào PostgreSQL
	if s.catalogRepo != nil && len(globalList) > 0 {
		if err := s.catalogRepo.UpsertModels(ctx, globalList); err != nil {
			log.Printf("[Model Discovery Error] Lưu models vào PostgreSQL thất bại: %v", err)
			return result, fmt.Errorf("lỗi lưu models vào catalog repository: %w", err)
		}
		result.ModelsUpdated = len(globalList)
		if s.metrics != nil {
			s.metrics.IncRuntimeModelCatalogUpdates()
		}

		// Tăng số thế hệ catalog (Generation) để các node phân tán đồng bộ đối soát
		newGen, _ := s.catalogRepo.IncrementCatalogGeneration(ctx, domain.ServiceGemini)
		s.mu.Lock()
		s.localGeneration = newGen
		s.mu.Unlock()

		// 3. Leader nạp lại toàn bộ snapshot từ PostgreSQL (không rebuild trực tiếp từ globalMap partial)
		_ = s.ReloadFromStorage(ctx)

		// 4. Phát tín hiệu qua Redis EventBus cho các Gateway Node B/C cập nhật
		if s.eventBus != nil {
			ev := CatalogChangedEvent{
				NodeID:         s.nodeID,
				CatalogVersion: newGen,
				UpdatedAt:      now,
				Count:          len(globalList),
			}
			if evBytes, err := json.Marshal(ev); err == nil {
				_ = s.eventBus.Publish(ctx, TopicModelCatalogChanged, evBytes)
			}
		}
	} else {
		_ = s.ReloadFromStorage(ctx)
	}

	log.Printf("[Model Discovery] Hoàn tất quét discovery: %d tài khoản (%d thành công, %d thất bại), %d mô hình hoạt động.",
		result.AccountsScanned, result.AccountsSucceeded, result.AccountsFailed, len(globalList))
	return result, nil
}

func (s *ModelDiscoveryService) runPeriodicReconciliation(ctx context.Context) {
	for {
		s.mu.RLock()
		interval := s.refreshInterval
		staleThresh := s.staleThreshold
		isLeader := s.isLeader
		s.mu.RUnlock()

		if interval <= 0 {
			interval = DefaultRefreshInterval
		}

		// Áp dụng jitter ngẫu nhiên 0.9x đến 1.1x để tránh các node đồng loạt gọi upstream
		jitter := 0.9 + rand.Float64()*0.2
		sleepDuration := time.Duration(float64(interval) * jitter)

		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-time.After(sleepDuration):
		}

		// 1. Đánh dấu các model quá hạn không xuất hiện upstream là stale/unavailable
		if s.catalogRepo != nil && staleThresh > 0 {
			staleBefore := time.Now().Add(-staleThresh)
			if marked, err := s.catalogRepo.MarkStaleModels(ctx, staleBefore); err == nil && marked > 0 {
				log.Printf("[Model Discovery] Đã chuyển %d mô hình không hoạt động sang trạng thái stale/unavailable.", marked)
				if s.metrics != nil {
					s.metrics.AddRuntimeModelStale(uint64(marked))
				}
			}
		}

		// 2. Kiểm tra thế hệ catalog trong DB để phát hiện trường hợp rớt gói tin Redis Pub/Sub
		if s.catalogRepo != nil {
			if dbGen, err := s.catalogRepo.GetCatalogGeneration(ctx, domain.ServiceGemini); err == nil {
				if dbGen > s.GetLocalGeneration() {
					log.Printf("[Model Discovery] Phát hiện database catalog generation (%d) mới hơn local (%d), đồng bộ lại...", dbGen, s.GetLocalGeneration())
					_ = s.ReloadFromStorage(ctx)
				}
			}
		}

		// 3. Chạy discovery (nếu là leader hoặc chạy chế độ đơn lẻ)
		if isLeader || s.eventBus == nil {
			log.Println("[Model Discovery] Chạy chu kỳ định kỳ cập nhật mô hình từ Google upstream...")
			_, _ = s.Refresh(ctx)
		} else {
			// Node Worker (B/C): Đồng bộ đối soát định kỳ từ PostgreSQL
			_ = s.ReloadFromStorage(ctx)
		}
	}
}
