package session

import (
	"context"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type MemoryTenantRuntimeSettingsRepository struct {
	mu       sync.RWMutex
	settings map[string]*domain.TenantRuntimeSettings
}

var _ ports.TenantRuntimeSettingsRepository = (*MemoryTenantRuntimeSettingsRepository)(nil)

func NewMemoryTenantRuntimeSettingsRepository() *MemoryTenantRuntimeSettingsRepository {
	return &MemoryTenantRuntimeSettingsRepository{
		settings: make(map[string]*domain.TenantRuntimeSettings),
	}
}

func (r *MemoryTenantRuntimeSettingsRepository) Get(ctx context.Context, tenantID string) (*domain.TenantRuntimeSettings, error) {
	if tenantID == "" {
		tenantID = "default_tenant"
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.settings[tenantID]
	if !ok {
		return nil, nil
	}
	copy := *s
	return &copy, nil
}

func (r *MemoryTenantRuntimeSettingsRepository) Upsert(ctx context.Context, settings *domain.TenantRuntimeSettings) error {
	if settings == nil || settings.TenantID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := *settings
	copied.UpdatedAt = time.Now()
	r.settings[settings.TenantID] = &copied
	return nil
}

func (r *MemoryTenantRuntimeSettingsRepository) ListAll(ctx context.Context) ([]*domain.TenantRuntimeSettings, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*domain.TenantRuntimeSettings, 0, len(r.settings))
	for _, s := range r.settings {
		copy := *s
		list = append(list, &copy)
	}
	return list, nil
}
