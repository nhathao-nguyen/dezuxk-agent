package tools

import (
	"context"
	"fmt"
	"sync"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// FilteredToolRegistry bọc một ToolRegistry gốc và chỉ cho phép một tập hợp các công cụ nhất định
type FilteredToolRegistry struct {
	base    ports.ToolRegistry
	mu      sync.RWMutex
	allowed map[string]bool
}

// NewFilteredToolRegistry tạo một registry được lọc theo danh sách công cụ cho phép
func NewFilteredToolRegistry(base ports.ToolRegistry, allowedTools []string) *FilteredToolRegistry {
	allowed := make(map[string]bool, len(allowedTools))
	for _, name := range allowedTools {
		allowed[name] = true
	}
	return &FilteredToolRegistry{
		base:    base,
		allowed: allowed,
	}
}

var _ ports.ToolRegistry = (*FilteredToolRegistry)(nil)

func (r *FilteredToolRegistry) RegisterTool(tool domain.AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.allowed != nil {
		r.allowed[tool.Name()] = true
	}
	r.base.RegisterTool(tool)
}

func (r *FilteredToolRegistry) GetTool(name string) (domain.AgentTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.allowed) > 0 && !r.allowed[name] {
		return nil, false
	}
	return r.base.GetTool(name)
}

func (r *FilteredToolRegistry) ListTools() []domain.AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := r.base.ListTools()
	if len(r.allowed) == 0 {
		return all
	}
	filtered := make([]domain.AgentTool, 0, len(r.allowed))
	for _, t := range all {
		if r.allowed[t.Name()] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func (r *FilteredToolRegistry) ToOpenAITools() []domain.OpenAITool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := r.base.ToOpenAITools()
	if len(r.allowed) == 0 {
		return all
	}
	filtered := make([]domain.OpenAITool, 0, len(r.allowed))
	for _, t := range all {
		if r.allowed[t.Function.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func (r *FilteredToolRegistry) Execute(ctx context.Context, name string, argsJSON string) (string, error) {
	r.mu.RLock()
	isAllowed := len(r.allowed) == 0 || r.allowed[name]
	r.mu.RUnlock()

	if !isAllowed {
		return "", fmt.Errorf("công cụ %q bị hạn chế và không được phép sử dụng bởi vai trò sub-agent này", name)
	}

	return r.base.Execute(ctx, name, argsJSON)
}
