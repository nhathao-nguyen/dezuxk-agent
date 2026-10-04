package session

import (
	"context"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type MemoryModelCatalogRepository struct {
	mu            sync.RWMutex
	models        map[string]domain.ModelDescriptor
	accountModels map[string]map[string]domain.AccountModelEligibility // accountID -> modelID -> eligibility
	generation    int64
}

var _ ports.ModelCatalogRepository = (*MemoryModelCatalogRepository)(nil)

func NewMemoryModelCatalogRepository() *MemoryModelCatalogRepository {
	return &MemoryModelCatalogRepository{
		models:        make(map[string]domain.ModelDescriptor),
		accountModels: make(map[string]map[string]domain.AccountModelEligibility),
		generation:    1,
	}
}

func (r *MemoryModelCatalogRepository) UpsertModels(ctx context.Context, models []domain.ModelDescriptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, m := range models {
		if m.FirstSeenAt.IsZero() {
			m.FirstSeenAt = now
		}
		if m.LastSeenAt.IsZero() {
			m.LastSeenAt = now
		}
		if m.UpdatedAt.IsZero() {
			m.UpdatedAt = now
		}
		r.models[m.ID] = m
	}
	return nil
}

func (r *MemoryModelCatalogRepository) ListModels(ctx context.Context, service domain.ServiceKind) ([]domain.ModelDescriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []domain.ModelDescriptor
	for _, m := range r.models {
		if m.IsActive {
			if service == "" || m.TargetService == service {
				list = append(list, m)
			}
		}
	}
	return list, nil
}

func (r *MemoryModelCatalogRepository) MarkAvailability(ctx context.Context, modelID string, isAvailable bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.models[modelID]; ok {
		m.IsActive = isAvailable
		m.UpdatedAt = time.Now()
		r.models[modelID] = m
	}
	return nil
}

func (r *MemoryModelCatalogRepository) UpsertAccountModels(ctx context.Context, accountID string, modelIDs []string, isEligible bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.accountModels[accountID]; !ok {
		r.accountModels[accountID] = make(map[string]domain.AccountModelEligibility)
	}

	// Snapshot semantics: đánh dấu tất cả models cũ của account là unavailable
	for mID, el := range r.accountModels[accountID] {
		el.IsAvailable = false
		r.accountModels[accountID][mID] = el
	}

	now := time.Now()
	for _, mID := range modelIDs {
		r.accountModels[accountID][mID] = domain.AccountModelEligibility{
			AccountID:   accountID,
			ModelID:     mID,
			IsAvailable: true,
			IsEligible:  isEligible,
			LastSeenAt:  now,
		}
	}
	return nil
}

func (r *MemoryModelCatalogRepository) ListAccountModels(ctx context.Context, accountID string) ([]domain.AccountModelEligibility, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []domain.AccountModelEligibility
	if m, ok := r.accountModels[accountID]; ok {
		for _, el := range m {
			list = append(list, el)
		}
	}
	return list, nil
}

func (r *MemoryModelCatalogRepository) ListEligibleAccountsForModel(ctx context.Context, modelID string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var accounts []string
	for accID, models := range r.accountModels {
		if el, ok := models[modelID]; ok && el.IsEligible && el.IsAvailable {
			accounts = append(accounts, accID)
		}
	}
	return accounts, nil
}

func (r *MemoryModelCatalogRepository) MarkStaleModels(ctx context.Context, staleBefore time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	now := time.Now()
	for k, m := range r.models {
		if m.IsActive && m.LastSeenAt.Before(staleBefore) {
			m.IsActive = false
			m.UpdatedAt = now
			r.models[k] = m
			count++
		}
	}
	return count, nil
}

func (r *MemoryModelCatalogRepository) GetCatalogGeneration(ctx context.Context, service domain.ServiceKind) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation, nil
}

func (r *MemoryModelCatalogRepository) IncrementCatalogGeneration(ctx context.Context, service domain.ServiceKind) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation++
	return r.generation, nil
}
