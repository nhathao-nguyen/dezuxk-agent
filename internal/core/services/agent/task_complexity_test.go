package agent

import (
	"strings"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services"
)

func TestTaskComplexity_Classification(t *testing.T) {
	tests := []struct {
		name          string
		goal          string
		expectedLevel TaskComplexityLevel
		minDepth      int
		maxDepth      int
	}{
		{
			name:          "Trivial hello",
			goal:          "hello",
			expectedLevel: ComplexityTrivial,
			minDepth:      1,
			maxDepth:      2,
		},
		{
			name:          "Simple rename variable",
			goal:          "rename this variable in handler.go",
			expectedLevel: ComplexitySimple,
			minDepth:      1,
			maxDepth:      3,
		},
		{
			name:          "Complex debugging race condition",
			goal:          "debug distributed race condition in session manager and fix deadlock",
			expectedLevel: ComplexityDeep,
			minDepth:      4,
			maxDepth:      12,
		},
		{
			name:          "Repo-wide audit",
			goal:          "audit entire repo and prove production safety with benchmarks",
			expectedLevel: ComplexityDeep,
			minDepth:      4,
			maxDepth:      12,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := DetectTaskComplexity(tc.goal, "")
			if profile.Level != tc.expectedLevel {
				t.Fatalf("kỳ vọng level %s, nhận được %s", tc.expectedLevel, profile.Level)
			}
			if profile.MinPlanningDepth < tc.minDepth {
				t.Fatalf("kỳ vọng min planning depth >= %d, nhận được %d", tc.minDepth, profile.MinPlanningDepth)
			}
			if profile.MaxPlanningDepth > tc.maxDepth {
				t.Fatalf("kỳ vọng max planning depth <= %d, nhận được %d", tc.maxDepth, profile.MaxPlanningDepth)
			}
		})
	}
}

func TestResolveReasoningPolicy_NativeVsEmulated(t *testing.T) {
	profile := DetectTaskComplexity("debug distributed race condition", "")

	// 1. Model WITH native thinking support
	nativeCaps := domain.ModelCapabilities{
		ModelID:              "gemini-2.5-flash",
		HasThinking:          true,
		NativeThinkingBudget: true,
		VerificationState:    domain.VerificationStateKnown,
	}

	policyNative := ResolveReasoningPolicy(nativeCaps, profile, nil)
	if !policyNative.IsNative {
		t.Fatalf("kỳ vọng IsNative=true cho model hỗ trợ native thinking")
	}
	if policyNative.IsEmulated {
		t.Fatalf("kỳ vọng IsEmulated=false cho model hỗ trợ native thinking")
	}
	if policyNative.BudgetTokens <= 0 {
		t.Fatalf("kỳ vọng BudgetTokens > 0, nhận được %d", policyNative.BudgetTokens)
	}

	// 2. Model WITHOUT native thinking support
	nonNativeCaps := domain.ModelCapabilities{
		ModelID:              "gpt-4o-mini",
		HasThinking:          false,
		NativeThinkingBudget: false,
		VerificationState:    domain.VerificationStateKnown,
	}

	policyEmulated := ResolveReasoningPolicy(nonNativeCaps, profile, nil)
	if policyEmulated.IsNative {
		t.Fatalf("kỳ vọng IsNative=false cho model không hỗ trợ native thinking")
	}
	if !policyEmulated.IsEmulated {
		t.Fatalf("kỳ vọng IsEmulated=true cho model không hỗ trợ native thinking")
	}
	if policyEmulated.EmulationPrompt == "" {
		t.Fatalf("kỳ vọng EmulationPrompt không rỗng khi emulated")
	}
}

func TestResolveReasoningPolicy_ExplicitRequestOverride(t *testing.T) {
	caps := domain.ModelCapabilities{
		ModelID:              "custom-model",
		HasThinking:          true,
		NativeThinkingBudget: true,
		VerificationState:    domain.VerificationStateKnown,
	}

	profile := DetectTaskComplexity("hello", "") // normally trivial -> 0 budget
	budgetOverride := 4096
	req := &domain.OpenAIChatRequest{
		ReasoningEffort: "high",
		ThinkingBudget:  &budgetOverride,
	}

	policy := ResolveReasoningPolicy(caps, profile, req)
	if policy.Effort != "high" {
		t.Fatalf("kỳ vọng Effort=high theo request override, nhận được %s", policy.Effort)
	}
	if policy.BudgetTokens != 4096 {
		t.Fatalf("kỳ vọng BudgetTokens=4096 theo request override, nhận được %d", policy.BudgetTokens)
	}
}

func TestAdaptiveReasoningWiring_Section15(t *testing.T) {
	// Test A: Trivial input ("hello") -> policy none, Thinking=false, ReasoningEffort empty/none, no emulation prompt.
	t.Run("TestA_TrivialInput", func(t *testing.T) {
		profile := DetectTaskComplexity("hello", "")
		if profile.Level != ComplexityTrivial {
			t.Fatalf("expected ComplexityTrivial, got %s", profile.Level)
		}
		caps := domain.ModelCapabilities{
			ModelID:               "gemini-2.5-flash",
			HasThinking:           true,
			NativeReasoningEffort: true,
			NativeThinkingBudget:  true,
		}
		policy := ResolveReasoningPolicy(caps, profile, nil)
		if policy.Mode != "none" || policy.Effort != "none" || policy.BudgetTokens != 0 {
			t.Fatalf("expected policy none, got mode=%s, effort=%s, budget=%d", policy.Mode, policy.Effort, policy.BudgetTokens)
		}
		req := &domain.OpenAIChatRequest{
			Model: "gemini-2.5-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "hello"},
			},
		}
		ApplyReasoningPolicy(req, policy, caps)
		if req.Thinking != nil && *req.Thinking {
			t.Fatalf("expected Thinking to be false or nil, got %v", *req.Thinking)
		}
		if req.ReasoningEffort != "" && req.ReasoningEffort != "none" {
			t.Fatalf("expected ReasoningEffort empty or none, got %s", req.ReasoningEffort)
		}
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "[REASONING EMULATION") {
				t.Fatalf("unexpected emulation prompt in trivial request")
			}
		}
	})

	// Test B: Complex debugging ("debug distributed race condition") -> ReasoningEffort=high with SupportsReasoningEffort=true
	t.Run("TestB_ComplexDebugging_ReasoningEffortHigh", func(t *testing.T) {
		profile := DetectTaskComplexity("debug distributed race condition in session manager", "")
		if profile.Level != ComplexityDeep {
			t.Fatalf("expected ComplexityDeep, got %s", profile.Level)
		}
		caps := domain.ModelCapabilities{
			ModelID:               "o3-mini",
			SupportsReasoning:     true,
			NativeReasoningEffort: true,
		}
		policy := ResolveReasoningPolicy(caps, profile, nil)
		if policy.Effort != "high" {
			t.Fatalf("expected policy effort high, got %s", policy.Effort)
		}
		req := &domain.OpenAIChatRequest{
			Model: "o3-mini",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "debug distributed race condition in session manager"},
			},
		}
		ApplyReasoningPolicy(req, policy, caps)
		if req.ReasoningEffort != "high" {
			t.Fatalf("expected ReasoningEffort=high, got %s", req.ReasoningEffort)
		}
	})

	// Test C: Native budget -> SupportsNativeThinkingBudget=true -> Thinking=true, ThinkingBudget > 0
	t.Run("TestC_NativeBudget", func(t *testing.T) {
		profile := DetectTaskComplexity("debug distributed race condition", "")
		caps := domain.ModelCapabilities{
			ModelID:              "gemini-2.5-flash",
			HasThinking:          true,
			NativeThinkingBudget: true,
		}
		policy := ResolveReasoningPolicy(caps, profile, nil)
		if policy.BudgetTokens <= 0 {
			t.Fatalf("expected BudgetTokens > 0, got %d", policy.BudgetTokens)
		}
		req := &domain.OpenAIChatRequest{
			Model: "gemini-2.5-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "debug distributed race condition"},
			},
		}
		ApplyReasoningPolicy(req, policy, caps)
		if req.Thinking == nil || !*req.Thinking {
			t.Fatalf("expected Thinking=true, got %v", req.Thinking)
		}
		if req.ThinkingBudget == nil || *req.ThinkingBudget <= 0 {
			t.Fatalf("expected ThinkingBudget > 0, got %v", req.ThinkingBudget)
		}
	})

	// Test D: Emulated model -> SupportsThinking=false, policy deep -> no fake native thinking field, emulation prompt injected exactly once
	t.Run("TestD_EmulatedModel_NoFakeFields_InjectedOnce", func(t *testing.T) {
		profile := DetectTaskComplexity("audit entire repository and verify zero race conditions", "")
		caps := domain.ModelCapabilities{
			ModelID:               "gpt-4o-mini",
			SupportsReasoning:     false,
			HasThinking:           false,
			NativeReasoningEffort: false,
			NativeThinkingBudget:  false,
		}
		policy := ResolveReasoningPolicy(caps, profile, nil)
		if !policy.IsEmulated {
			t.Fatalf("expected IsEmulated=true")
		}
		if policy.EmulationPrompt == "" {
			t.Fatalf("expected non-empty EmulationPrompt")
		}

		origMessages := []domain.OpenAIMessage{
			{Role: "user", Content: "audit entire repository"},
		}
		req := &domain.OpenAIChatRequest{
			Model:    "gpt-4o-mini",
			Messages: append([]domain.OpenAIMessage(nil), origMessages...),
		}
		ApplyReasoningPolicy(req, policy, caps)

		if req.Thinking != nil && *req.Thinking {
			t.Fatalf("expected Thinking to not be true for emulated model")
		}
		if req.ThinkingBudget != nil {
			t.Fatalf("expected ThinkingBudget to be nil for emulated model, got %v", *req.ThinkingBudget)
		}
		if req.ReasoningEffort != "" {
			t.Fatalf("expected ReasoningEffort to be empty for emulated model, got %s", req.ReasoningEffort)
		}

		// Verify emulation prompt is injected
		emulationCount := 0
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "[REASONING EMULATION") {
				emulationCount++
			}
		}
		if emulationCount != 1 {
			t.Fatalf("expected emulation prompt injected exactly once, got %d", emulationCount)
		}

		// Apply again: ensure idempotent, NOT injected twice
		ApplyReasoningPolicy(req, policy, caps)
		emulationCount2 := 0
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "[REASONING EMULATION") {
				emulationCount2++
			}
		}
		if emulationCount2 != 1 {
			t.Fatalf("expected emulation prompt count still 1 after second apply, got %d", emulationCount2)
		}

		// Verify original messages slice was not corrupted/mutated
		if len(origMessages) != 1 || origMessages[0].Role != "user" {
			t.Fatalf("original messages slice was corrupted")
		}
	})

	// Test E: Client override -> client specifies reasoning_effort=low on deep task -> request has low
	t.Run("TestE_ClientOverride_LowOnDeepTask", func(t *testing.T) {
		profile := DetectTaskComplexity("debug distributed race condition", "")
		caps := domain.ModelCapabilities{
			ModelID:               "o3-mini",
			SupportsReasoning:     true,
			NativeReasoningEffort: true,
		}
		req := &domain.OpenAIChatRequest{
			Model:           "o3-mini",
			ReasoningEffort: "low", // client explicit override
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "debug distributed race condition"},
			},
		}
		policy := ResolveReasoningPolicy(caps, profile, req)
		ApplyReasoningPolicy(req, policy, caps)

		if req.ReasoningEffort != "low" {
			t.Fatalf("expected client override ReasoningEffort=low to be preserved, got %s", req.ReasoningEffort)
		}
	})

	// Test F: Reasoning + Context Budget consistency: mock deep task -> assert ContextBudgetManager reserves 8192
	t.Run("TestF_ContextBudgetManager_DeepTaskReserve8192", func(t *testing.T) {
		tc := services.NewTokenCounter(config.TokensConfig{})
		cbm := services.NewContextBudgetManager(tc)
		caps := domain.ModelCapabilities{
			ModelID:               "gemini-2.5-flash",
			ContextWindow:         1048576,
			MaxOutputTokens:       16384,
			SupportsReasoning:     true,
			HasThinking:           true,
			NativeThinkingBudget:  true,
			NativeReasoningEffort: true,
		}
		profile := DetectTaskComplexity("debug distributed race condition across cluster nodes", "")
		req := &domain.OpenAIChatRequest{
			Model: "gemini-2.5-flash",
			Messages: []domain.OpenAIMessage{
				{Role: "user", Content: "debug distributed race condition"},
			},
		}
		policy := ResolveReasoningPolicy(caps, profile, req)
		ApplyReasoningPolicy(req, policy, caps)

		budget := cbm.CalculateBudgetWithComplexity(caps, req.Messages, nil, req, string(profile.Level))
		if budget.ReasoningReserveTokens != 8192 {
			t.Fatalf("expected ContextBudgetManager ReasoningReserveTokens=8192 for deep task, got %d", budget.ReasoningReserveTokens)
		}
	})
}
