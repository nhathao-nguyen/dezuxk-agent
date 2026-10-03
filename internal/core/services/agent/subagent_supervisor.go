package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// SubagentSupervisor quản lý việc khởi tạo và thực thi các sub-agent biệt lập
type SubagentSupervisor struct {
	chatUseCase ports.ChatUseCase
	toolReg     ports.ToolRegistry
	approval    ports.ApprovalProvider
	memorySvc   ports.MemoryService

	mu           sync.RWMutex
	descriptors  map[domain.SubagentRole]domain.SubagentDescriptor
	defaultModel string
}

func (s *SubagentSupervisor) SetDefaultModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultModel = model
}

var _ ports.SubagentSupervisor = (*SubagentSupervisor)(nil)

// NewSubagentSupervisor khởi tạo Supervisor cho các sub-agents
func NewSubagentSupervisor(
	chatUseCase ports.ChatUseCase,
	toolReg ports.ToolRegistry,
	approval ports.ApprovalProvider,
	memorySvc ports.MemoryService,
) *SubagentSupervisor {
	s := &SubagentSupervisor{
		chatUseCase: chatUseCase,
		toolReg:     toolReg,
		approval:    approval,
		memorySvc:   memorySvc,
		descriptors: make(map[domain.SubagentRole]domain.SubagentDescriptor),
	}
	s.registerDefaultRoles()
	return s
}

func (s *SubagentSupervisor) registerDefaultRoles() {
	s.RegisterRole(domain.SubagentDescriptor{
		Role:        domain.RoleResearcher,
		Description: "Sub-agent chuyên nghiên cứu, đọc mã nguồn, cấu trúc thư mục, web CDP và tra cứu bộ nhớ mà không can thiệp sửa đổi tệp hay chạy lệnh.",
		SystemPrompt: `You are Dezuxk Research Subagent.
Your goal is to inspect codebases, investigate documentation, and gather technical facts.
Focus purely on deep reading, indexing, and synthesis. Return a structured, high-signal report with explicit file paths and code snippets.
Do NOT attempt to write or edit files or run shell commands.`,
		AllowedTools: []string{
			"read_file",
			"list_directory",
			"browser_navigate",
			"browser_evaluate",
			"browser_screenshot",
			"memory_search",
		},
		MaxSteps: 12,
	})

	s.RegisterRole(domain.SubagentDescriptor{
		Role:        domain.RoleCoder,
		Description: "Sub-agent chuyên lập trình, sửa đổi code, áp dụng diff chính xác và thực thi các lệnh kiểm thử.",
		SystemPrompt: `You are Dezuxk Coder Subagent.
Your goal is to write clean, idiomatic, robust code and verify changes.
Focus on minimal, clean modifications using replace_file_content and run verification tests using run_command.
Report the exact files changed and the outcome of the verification tests.`,
		AllowedTools: []string{
			"read_file",
			"write_file",
			"replace_file_content",
			"list_directory",
			"run_command",
			"memory_search",
			"memory_store",
		},
		MaxSteps: 20,
	})

	s.RegisterRole(domain.SubagentDescriptor{
		Role:        domain.RoleReviewer,
		Description: "Sub-agent chuyên đánh giá chất lượng mã nguồn, kiểm tra lỗi tiềm ẩn, bảo mật, và đối chiếu tiêu chuẩn trước khi nghiệm thu.",
		SystemPrompt: `You are Dezuxk Code Reviewer Subagent.
Your goal is to review code changes, architecture compliance, test coverage, potential edge-case bugs, and security risks.
Conclude your review with a structured verdict: [PASS], [WARN], or [FAIL] with clear, actionable rationale.`,
		AllowedTools: []string{
			"read_file",
			"list_directory",
			"run_command",
			"memory_search",
		},
		MaxSteps: 10,
	})

	s.RegisterRole(domain.SubagentDescriptor{
		Role:        domain.RoleGeneral,
		Description: "Sub-agent đa nhiệm tổng quát độc lập.",
		SystemPrompt: `You are Dezuxk Subagent.
Execute the assigned task systematically and provide a concise, structured response of the outcome.`,
		AllowedTools: nil, // Tất cả công cụ ngoại trừ invoke_subagent
		MaxSteps:     15,
	})
}

// RegisterRole đăng ký một vai trò sub-agent mới hoặc tùy biến
func (s *SubagentSupervisor) RegisterRole(descriptor domain.SubagentDescriptor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.descriptors[descriptor.Role] = descriptor
}

// GetRoleDescriptor lấy cấu hình của vai trò
func (s *SubagentSupervisor) GetRoleDescriptor(role domain.SubagentRole) (*domain.SubagentDescriptor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	desc, exists := s.descriptors[role]
	if !exists {
		return nil, false
	}
	return &desc, true
}

// ListRoles liệt kê tất cả các vai trò sub-agent được hỗ trợ
func (s *SubagentSupervisor) ListRoles() []domain.SubagentDescriptor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]domain.SubagentDescriptor, 0, len(s.descriptors))
	for _, desc := range s.descriptors {
		list = append(list, desc)
	}
	return list
}

// InvokeSubagent khởi tạo và thực thi một sub-agent biệt lập
func (s *SubagentSupervisor) InvokeSubagent(ctx context.Context, task domain.SubagentTask) (*domain.SubagentResult, error) {
	desc, exists := s.GetRoleDescriptor(task.Role)
	if !exists {
		// Fallback sang General nếu vai trò không xác định
		generalDesc, _ := s.GetRoleDescriptor(domain.RoleGeneral)
		desc = generalDesc
	}

	// Xây dựng danh sách công cụ cho phép, loại bỏ invoke_subagent để tránh đệ quy vô hạn
	var allowed []string
	if desc != nil && len(desc.AllowedTools) > 0 {
		for _, toolName := range desc.AllowedTools {
			if toolName != "invoke_subagent" {
				allowed = append(allowed, toolName)
			}
		}
	} else {
		// Cho phép tất cả công cụ hiện có ngoại trừ chính nó
		for _, t := range s.toolReg.ListTools() {
			if t.Name() != "invoke_subagent" {
				allowed = append(allowed, t.Name())
			}
		}
	}

	filteredTools := tools.NewFilteredToolRegistry(s.toolReg, allowed)

	// Khởi tạo Runner độc lập với ngữ cảnh riêng biệt
	subRunner := NewRunner(s.chatUseCase, filteredTools, s.approval)
	if s.memorySvc != nil {
		subRunner.SetMemoryService(s.memorySvc)
	}

	// Chuẩn bị mục tiêu thực thi
	goal := task.Prompt
	if strings.TrimSpace(task.Context) != "" {
		goal = fmt.Sprintf("Context:\n%s\n\nTask Objective:\n%s", strings.TrimSpace(task.Context), strings.TrimSpace(task.Prompt))
	}

	maxSteps := 15
	if desc != nil && desc.MaxSteps > 0 {
		maxSteps = desc.MaxSteps
	}

	s.mu.RLock()
	defModel := s.defaultModel
	s.mu.RUnlock()

	model := task.Model
	if model == "" {
		model = defModel
	}

	runOpts := domain.AgentRunOptions{
		Model:        model,
		MaxSteps:     maxSteps,
		Supervised:   task.Supervised,
		Workspace:    task.Workspace,
		CustomPrompt: desc.SystemPrompt,
		OnProgress:   task.OnProgress,
	}

	state, err := subRunner.Run(ctx, goal, runOpts)
	if err != nil {
		return &domain.SubagentResult{
			Role:       task.Role,
			Prompt:     task.Prompt,
			StepsCount: 0,
			Success:    false,
			Error:      err.Error(),
		}, err
	}

	return &domain.SubagentResult{
		Role:       task.Role,
		Prompt:     task.Prompt,
		Summary:    state.FinalAnswer,
		StepsCount: len(state.Steps),
		Success:    state.IsCompleted,
		Error:      state.Error,
	}, nil
}
