package agent

import (
	"testing"

	"dezuxk-gateway/internal/core/domain"
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
