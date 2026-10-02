package tools

import (
	"context"
	"fmt"
	"sync"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// InMemToolRegistry cài đặt ports.ToolRegistry
type InMemToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]domain.AgentTool
}

// NewToolRegistry khởi tạo Tool Registry mới
func NewToolRegistry() *InMemToolRegistry {
	return &InMemToolRegistry{
		tools: make(map[string]domain.AgentTool),
	}
}

var _ ports.ToolRegistry = (*InMemToolRegistry)(nil)

func (r *InMemToolRegistry) RegisterTool(tool domain.AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

func (r *InMemToolRegistry) GetTool(name string) (domain.AgentTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *InMemToolRegistry) ListTools() []domain.AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]domain.AgentTool, 0, len(r.tools))
	for _, t := range r.tools {
		list = append(list, t)
	}
	return list
}

func (r *InMemToolRegistry) ToOpenAITools() []domain.OpenAITool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var openAITools []domain.OpenAITool
	for _, t := range r.tools {
		openAITools = append(openAITools, domain.OpenAITool{
			Type: "function",
			Function: domain.OpenAIFunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return openAITools
}

func (r *InMemToolRegistry) Execute(ctx context.Context, name string, argsJSON string) (string, error) {
	r.mu.RLock()
	tool, exists := r.tools[name]
	r.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("công cụ %q không tồn tại trong hệ thống", name)
	}

	return tool.Execute(ctx, argsJSON)
}

// RegisterDefaultTools đăng ký toàn bộ bộ công cụ mặc định cho Agent
func RegisterDefaultTools(registry ports.ToolRegistry, workspace string) {
	registry.RegisterTool(NewReadFileTool(workspace))
	registry.RegisterTool(NewWriteFileTool(workspace))
	registry.RegisterTool(NewReplaceFileContentTool(workspace))
	registry.RegisterTool(NewListDirectoryTool(workspace))
	registry.RegisterTool(NewRunCommandTool(workspace))
	registry.RegisterTool(NewGrepCodeTool(workspace))
	registry.RegisterTool(NewReadSymbolDefinitionTool(workspace))
}
