package agent_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"
)

// mockTenantSettingsRepo implements minimal ports.TenantRuntimeSettingsRepository
type mockTenantSettingsRepo struct {
	settings map[string]*domain.TenantRuntimeSettings
	mu       sync.RWMutex
}

func (m *mockTenantSettingsRepo) Get(ctx context.Context, tenantID string) (*domain.TenantRuntimeSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings[tenantID], nil
}

func (m *mockTenantSettingsRepo) Upsert(ctx context.Context, settings *domain.TenantRuntimeSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[settings.TenantID] = settings
	return nil
}

func (m *mockTenantSettingsRepo) ListAll(ctx context.Context) ([]*domain.TenantRuntimeSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res []*domain.TenantRuntimeSettings
	for _, s := range m.settings {
		res = append(res, s)
	}
	return res, nil
}

type mockChatUseCaseForTenant struct {
	mu           sync.Mutex
	lastRequest  *domain.OpenAIChatRequest
	toolToReturn *domain.OpenAIToolCall
}

func (m *mockChatUseCaseForTenant) ExecuteChatSync(ctx context.Context, req *domain.OpenAIChatRequest) (*domain.OpenAIChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastRequest = req

	if m.toolToReturn != nil {
		return &domain.OpenAIChatResponse{
			Choices: []domain.OpenAIChoice{
				{
					Message: domain.OpenAIMessage{
						Role:      "assistant",
						ToolCalls: []domain.OpenAIToolCall{*m.toolToReturn},
					},
				},
			},
		}, nil
	}

	return &domain.OpenAIChatResponse{
		Choices: []domain.OpenAIChoice{
			{
				Message: domain.OpenAIMessage{
					Role:    "assistant",
					Content: "Task done",
				},
			},
		},
	}, nil
}

func (m *mockChatUseCaseForTenant) ExecuteChatStream(ctx context.Context, req *domain.OpenAIChatRequest, w io.Writer, flush func()) error {
	_, err := m.ExecuteChatSync(ctx, req)
	return err
}

// TestTenant_MemoryIsolation_PromptSeparation kiểm tra:
// Tenant A core memory = "A-secret"
// Tenant B core memory = "B-secret"
// Prompt A không chứa B. Prompt B không chứa A.
// Chạy đồng thời nhiều goroutine để kiểm tra bằng race detector.
func TestTenant_MemoryIsolation_PromptSeparation(t *testing.T) {
	memRepo := session.NewMemoryMemoryRepository()
	chatMock := &mockChatUseCaseForTenant{}
	memMgr := agent.NewMemoryManager(memRepo, chatMock, "gemini-3.8-flash", domain.CoreMemory{})

	// Thiết lập Core Memory cho Tenant A
	ctxA := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{TenantID: "tenant-A"})
	ctxA = domain.ContextWithMemoryNamespace(ctxA, domain.MemoryNamespace{TenantID: "tenant-A"})
	memMgr.UpdateCoreMemoryForContext(ctxA, func(c *domain.CoreMemory) {
		c.Scratchpad = "A-secret-super-confidential-token-999"
	})

	// Thiết lập Core Memory cho Tenant B
	ctxB := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{TenantID: "tenant-B"})
	ctxB = domain.ContextWithMemoryNamespace(ctxB, domain.MemoryNamespace{TenantID: "tenant-B"})
	memMgr.UpdateCoreMemoryForContext(ctxB, func(c *domain.CoreMemory) {
		c.Scratchpad = "B-secret-ultra-private-data-888"
	})

	// Kiểm tra tính biệt lập của Core Memory
	coreA := memMgr.GetCoreMemoryForContext(ctxA)
	coreB := memMgr.GetCoreMemoryForContext(ctxB)

	promptA := coreA.FormatPrompt()
	promptB := coreB.FormatPrompt()

	if strings.Contains(promptA, "B-secret") {
		t.Fatalf("LEAK: Prompt của Tenant A chứa bí mật của Tenant B: %s", promptA)
	}
	if strings.Contains(promptB, "A-secret") {
		t.Fatalf("LEAK: Prompt của Tenant B chứa bí mật của Tenant A: %s", promptB)
	}
	if !strings.Contains(promptA, "A-secret") {
		t.Fatalf("Prompt A phải chứa A-secret")
	}
	if !strings.Contains(promptB, "B-secret") {
		t.Fatalf("Prompt B phải chứa B-secret")
	}

	// Concurrency test với race detector: 20 goroutines truy cập đồng thời
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			c := memMgr.GetCoreMemoryForContext(ctxA)
			p := c.FormatPrompt()
			if strings.Contains(p, "B-secret") {
				panic("Race leak: A contains B-secret")
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			c := memMgr.GetCoreMemoryForContext(ctxB)
			p := c.FormatPrompt()
			if strings.Contains(p, "A-secret") {
				panic("Race leak: B contains A-secret")
			}
		}(i)
	}
	wg.Wait()
}

// TestTenant_AllowedTools_Enforcement kiểm tra:
// Tenant cấu hình AllowedTools=["read_file"].
// Agent cố gọi "run_command" -> Bị DENY trước execution.
func TestTenant_AllowedTools_Enforcement(t *testing.T) {
	tenantID := "tenant-restricted-tools"
	tenantRepo := &mockTenantSettingsRepo{
		settings: map[string]*domain.TenantRuntimeSettings{
			tenantID: {
				TenantID:     tenantID,
				AllowedTools: []string{"read_file"},
			},
		},
	}

	toolCall := &domain.OpenAIToolCall{
		ID:   "call_dangerous_cmd",
		Type: "function",
		Function: domain.OpenAIFunctionCallData{
			Name:      "run_command",
			Arguments: `{"command": "rm -rf /"}`,
		},
	}

	chatMock := &mockChatUseCaseForTenant{
		toolToReturn: toolCall,
	}

	runner := agent.NewRunner(chatMock, nil, nil)
	runner.SetTenantSettingsRepository(tenantRepo)

	ctx := domain.ContextWithTenantIdentity(context.Background(), domain.TenantIdentity{
		TenantID: tenantID,
	})

	state, err := runner.Run(ctx, "Hãy thực thi lệnh nguy hiểm", domain.AgentRunOptions{
		MaxSteps: 3,
	})

	if err == nil {
		t.Fatalf("Kỳ vọng lỗi DENY khi gọi run_command vi phạm AllowedTools, nhưng nhận nil")
	}

	if !strings.Contains(err.Error(), "AllowedTools") {
		t.Fatalf("Kỳ vọng thông báo lỗi đề cập AllowedTools, nhận được: %v", err)
	}

	if state.StopReason != domain.StopReasonVerificationFailed {
		t.Errorf("Kỳ vọng StopReason là VerificationFailed, nhận: %s", state.StopReason)
	}

	foundDenyResult := false
	for _, step := range state.Steps {
		for _, res := range step.ToolResults {
			if strings.Contains(res, "DENY") && strings.Contains(res, "run_command") {
				foundDenyResult = true
				break
			}
		}
	}
	if !foundDenyResult {
		t.Errorf("Kỳ vọng ghi nhận kết quả DENY trong ToolResults của step record")
	}
}
