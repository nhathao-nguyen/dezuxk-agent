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
	DefaultRefreshInterval   = 15 * time.Minute
	DefaultStaleThreshold    = 1 * time.Hour
)

type DiscoveryResult struct {
	AccountsScanned int `json:"accounts_scanned"`
	ModelsSeen      int `json:"models_seen"`
	ModelsAdded     int `json:"models_added"`
	ModelsUpdated   int `json:"models_updated"`
}

type CatalogChangedEvent struct {
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
	Count     int       `json:"count"`
}

// ModelDiscoveryService điều phối toàn bộ quy trình discovery mô hình động, đồng bộ PostgreSQL và lan truyền sự kiện Redis
type ModelDiscoveryService struct {
	mu              sync.RWMutex
	discovery       ports.ModelDiscoveryProvider
	catalogRepo     ports.ModelCatalogRepository
	sessionRepo     ports.SessionRepository
	modelRegistry   *domain.ModelRegistry
	eventBus        ports.EventBus
	metrics         *domain.ContractMetrics
	nodeID          string
	refreshInterval time.Duration
	staleThreshold  time.Duration
	stopCh          chan struct{}
	running         bool
	isLeader        bool
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
		discovery:       discovery,
		catalogRepo:     catalogRepo,
		sessionRepo:     sessionRepo,
		modelRegistry:   modelRegistry,
		eventBus:        eventBus,
		metrics:         metrics,
		nodeID:          nodeID,
		refreshInterval: DefaultRefreshInterval,
		staleThreshold:  DefaultStaleThreshold,
		stopCh:          make(chan struct{}),
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
		eventCh, unsub, err := s.eventBus.Subscribe(ctx, TopicModelCatalogChanged)
		if err != nil {
			log.Printf("[Model Discovery Warning] Không thể subscribe topic %s: %v", TopicModelCatalogChanged, err)
		} else {
			go func() {
				defer unsub()
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
							// Bỏ qua sự kiện do chính node này phát ra
							if ev.NodeID != s.nodeID {
								log.Printf("[Model Discovery] Nhận tín hiệu catalog thay đổi từ node %s, nạp lại từ PostgreSQL...", ev.NodeID)
								_ = s.ReloadFromStorage(ctx)
							}
						} else {
							_ = s.ReloadFromStorage(ctx)
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

	if len(models) > 0 && s.modelRegistry != nil {
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
				if err == nil && len(elList) > 0 {
					var supported []string
					for _, el := range elList {
						if el.IsEligible && el.IsAvailable {
							supported = append(supported, el.ModelID)
						}
					}
					acc.SetSupportedModels(supported)
					if s.metrics != nil {
						s.metrics.SetRuntimeAccountModelEligibility(acc.ID, int64(len(supported)))
					}
				}
			}
		}
	}

	return nil
}

// DiscoverAndSync là bí danh của Refresh phục vụ discovery đồng bộ
func (s *ModelDiscoveryService) DiscoverAndSync(ctx context.Context) (DiscoveryResult, error) {
	return s.Refresh(ctx)
}

// Refresh thực hiện một chu kỳ quét discovery trên toàn bộ tài khoản khả dụng
func (s *ModelDiscoveryService) Refresh(ctx context.Context) (DiscoveryResult, error) {
	var result DiscoveryResult
	if s.sessionRepo == nil || s.discovery == nil {
		return result, fmt.Errorf("session repo hoặc discovery provider chưa được khởi tạo")
	}

	accounts := s.sessionRepo.ListAll(ctx)
	if len(accounts) == 0 {
		return result, nil
	}

	globalMap := make(map[string]domain.ModelDescriptor)
	now := time.Now()

	// 1. Quét từng tài khoản Google khả dụng
	for _, acc := range accounts {
		if acc == nil || !acc.IsHealthy || !acc.ServiceReady(domain.ServiceGemini) {
			continue
		}
		if acc.GetHealthStatus() == domain.HealthStatusQuotaExhausted || acc.GetHealthStatus() == domain.HealthStatusAuthExpired {
			continue
		}

		result.AccountsScanned++
		scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		models, err := s.discovery.DiscoverModels(scanCtx, acc)
		cancel()

		if err != nil {
			log.Printf("[Model Discovery Warning] Quét model thất bại trên account %s: %v", acc.ID, err)
			if s.metrics != nil {
				s.metrics.IncRuntimeModelDiscoveryFail()
			}
			continue
		}

		if s.metrics != nil {
			s.metrics.IncRuntimeModelDiscoverySuccess()
		}

		var accModelIDs []string
		for _, m := range models {
			accModelIDs = append(accModelIDs, m.ID)
			if existing, ok := globalMap[m.ID]; ok {
				// Cập nhật last seen
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

		// Cập nhật danh sách model tài khoản này hỗ trợ
		acc.SetSupportedModels(accModelIDs)
		if s.catalogRepo != nil {
			_ = s.catalogRepo.UpsertAccountModels(ctx, acc.ID, accModelIDs, true)
		}
		if s.metrics != nil {
			s.metrics.SetRuntimeAccountModelEligibility(acc.ID, int64(len(accModelIDs)))
		}
	}

	if len(globalMap) == 0 {
		return result, nil
	}

	var globalList []domain.ModelDescriptor
	for _, m := range globalMap {
		globalList = append(globalList, m)
	}

	// 2. Lưu trữ bền vững vào PostgreSQL
	if s.catalogRepo != nil {
		if err := s.catalogRepo.UpsertModels(ctx, globalList); err != nil {
			log.Printf("[Model Discovery Error] Lưu models vào PostgreSQL thất bại: %v", err)
			return result, fmt.Errorf("lỗi lưu models vào catalog repository: %w", err)
		}
		result.ModelsUpdated = len(globalList)
		if s.metrics != nil {
			s.metrics.IncRuntimeModelCatalogUpdates()
		}
	}

	// 3. Cập nhật bộ nhớ cache ModelRegistry cục bộ
	if s.modelRegistry != nil {
		s.modelRegistry.ReplaceAll(globalList)
		if s.metrics != nil {
			s.metrics.SetRuntimeModelCatalogSize(int64(len(globalList)))
		}
	}

	// 4. Phát tín hiệu qua Redis EventBus cho các Gateway Node B/C cập nhật tức thì
	if s.eventBus != nil {
		ev := CatalogChangedEvent{
			NodeID:    s.nodeID,
			Timestamp: now,
			Count:     len(globalList),
		}
		if evBytes, err := json.Marshal(ev); err == nil {
			_ = s.eventBus.Publish(ctx, TopicModelCatalogChanged, evBytes)
		}
	}

	log.Printf("[Model Discovery] Hoàn tất quét discovery: %d tài khoản, %d mô hình hoạt động.", result.AccountsScanned, len(globalList))
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

		// 2. Chạy discovery (nếu là leader hoặc chạy chế độ đơn lẻ)
		if isLeader || s.eventBus == nil {
			log.Println("[Model Discovery] Chạy chu kỳ định kỳ cập nhật mô hình từ Google upstream...")
			_, _ = s.Refresh(ctx)
		} else {
			// Node Worker (B/C): Đồng bộ đối soát định kỳ từ PostgreSQL để tự phục hồi kể cả khi Redis rớt gói tin
			_ = s.ReloadFromStorage(ctx)
		}
	}
}
