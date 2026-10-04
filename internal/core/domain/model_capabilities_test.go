package domain_test

import (
	"encoding/json"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestModelCapabilityRegistry_PriorityResolution(t *testing.T) {
	registry := domain.NewModelCapabilityRegistry()

	// 1. Safe Fallback when nothing is registered
	desc := &domain.ModelDescriptor{
		ID:           "test-unknown-model",
		Capabilities: []domain.ModelCapability{domain.CapChat},
	}
	capsFallback := registry.Resolve("test-unknown-model", desc)
	if capsFallback.SourceOfCapability != domain.CapabilitySourceSafeFallback {
		t.Errorf("expected source safe_fallback, got %s", capsFallback.SourceOfCapability)
	}
	if !capsFallback.IsEstimated {
		t.Errorf("expected fallback to be marked as estimated")
	}
	if capsFallback.VerificationState != domain.VerificationStateUnknown {
		t.Errorf("expected unknown model to have VerificationStateUnknown, got %s", capsFallback.VerificationState)
	}

	// 2. Configured capability overrides fallback
	registry.RegisterConfigured(domain.ModelCapabilities{
		ModelID:             "test-unknown-model",
		ContextWindow:       65536,
		ContextWindowTokens: 65536,
		VerificationState:   domain.VerificationStateKnown,
	})
	capsConfigured := registry.Resolve("test-unknown-model", desc)
	if capsConfigured.SourceOfCapability != domain.CapabilitySourceConfigured {
		t.Errorf("expected source configured, got %s", capsConfigured.SourceOfCapability)
	}
	if capsConfigured.ContextWindow != 65536 {
		t.Errorf("expected context window 65536, got %d", capsConfigured.ContextWindow)
	}

	// 3. Runtime discovery has highest priority and overrides configured
	registry.RegisterDiscovered(domain.ModelCapabilities{
		ModelID:                  "test-unknown-model",
		ContextWindow:            1048576,
		RecommendedContextWindow: 900000,
		MaxOutputTokens:          16384,
		SupportsReasoning:        true,
		HasThinking:              true,
		NativeThinkingBudget:     false,
		NativeReasoningEffort:    true,
		SupportedReasoningModes:  []string{"low", "high"},
		ThinkingBudgetSupport:    domain.FeatureSupportEmulated,
		MaxOutputSupport:         domain.FeatureSupportEmulated,
		SupportsTools:            true,
		SupportsContinuation:     true,
		VerificationState:        domain.VerificationStateKnown,
	})
	capsDiscovered := registry.Resolve("test-unknown-model", desc)
	if capsDiscovered.SourceOfCapability != domain.CapabilitySourceRuntimeDiscovery {
		t.Errorf("expected source runtime_discovery, got %s", capsDiscovered.SourceOfCapability)
	}
	if capsDiscovered.VerificationState != domain.VerificationStateKnown {
		t.Errorf("expected VerificationStateKnown, got %s", capsDiscovered.VerificationState)
	}
	if capsDiscovered.ContextWindow != 1048576 {
		t.Errorf("expected context window 1048576, got %d", capsDiscovered.ContextWindow)
	}
	if capsDiscovered.EffectiveContextWindow() != 900000 {
		t.Errorf("expected effective context window 900000, got %d", capsDiscovered.EffectiveContextWindow())
	}
	if capsDiscovered.GetContextWindow() != 1048576 {
		t.Errorf("expected GetContextWindow 1048576, got %d", capsDiscovered.GetContextWindow())
	}
	if capsDiscovered.GetMaxOutputTokens() != 16384 {
		t.Errorf("expected GetMaxOutputTokens 16384, got %d", capsDiscovered.GetMaxOutputTokens())
	}
	if !capsDiscovered.SupportsReasoningMode("high") {
		t.Errorf("expected support for reasoning mode high")
	}
	if capsDiscovered.SupportsReasoningMode("medium") {
		t.Errorf("expected medium mode unsupported since only low and high registered")
	}
	if !capsDiscovered.CanContinue() {
		t.Errorf("expected CanContinue to be true")
	}
	if !capsDiscovered.SupportsReasoningEffort() {
		t.Errorf("expected SupportsReasoningEffort to be true")
	}
}

func TestModelCapabilities_StateClassification(t *testing.T) {
	// A. Unknown model: upstream not exposing limits
	unknownCaps := domain.DefaultFallbackCapabilities("custom-foreign-model", nil)
	if unknownCaps.VerificationState != domain.VerificationStateUnknown {
		t.Errorf("expected unknown model to have verification_state UNKNOWN, got %s", unknownCaps.VerificationState)
	}
	if unknownCaps.EffectiveContextWindow() > 32768 {
		t.Errorf("conservative policy for unknown model should not assume huge context, got %d", unknownCaps.EffectiveContextWindow())
	}

	// B. Inferred model: known family name
	inferredCaps := domain.DefaultFallbackCapabilities("gemini-2.5-flash", nil)
	if inferredCaps.VerificationState != domain.VerificationStateInferred {
		t.Errorf("expected flash model to have verification_state INFERRED, got %s", inferredCaps.VerificationState)
	}
	if inferredCaps.ContextWindow < 128000 {
		t.Errorf("expected flash model to have at least 128k context window fallback, got %d", inferredCaps.ContextWindow)
	}

	// C. Known model via discovery
	knownCaps := domain.ModelCapabilities{
		ModelID:               "gemini-3.8-pro",
		ContextWindow:         2097152,
		MaxOutputTokens:       65536,
		HasThinking:           true,
		NativeThinkingBudget:  true,
		ThinkingBudgetSupport: domain.FeatureSupportNative,
		VerificationState:     domain.VerificationStateKnown,
		ContextWindowState:    domain.VerificationStateKnown,
		MaxOutputTokensState:  domain.VerificationStateKnown,
	}
	if !knownCaps.SupportsNativeThinkingBudget() {
		t.Errorf("expected SupportsNativeThinkingBudget to be true")
	}
	if knownCaps.GetMaxOutputTokens() != 65536 {
		t.Errorf("expected max output tokens 65536, got %d", knownCaps.GetMaxOutputTokens())
	}
	if !knownCaps.ResolveCapability(domain.CapThinking) {
		t.Errorf("expected ResolveCapability(CapThinking) to be true")
	}
}

func TestModelCapabilities_SerializationAndDescriptor(t *testing.T) {
	caps := domain.ModelCapabilities{
		ModelID:               "gemini-2.5-thinking",
		ContextWindow:         1048576,
		MaxOutputTokens:       32768,
		HasThinking:           true,
		NativeThinkingBudget:  false,
		ThinkingBudgetSupport: domain.FeatureSupportEmulated,
		SupportsTools:         true,
		SupportsContinuation:  true,
		VerificationState:     domain.VerificationStateKnown,
	}

	b, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var restored domain.ModelCapabilities
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if restored.ModelID != caps.ModelID || restored.ContextWindow != caps.ContextWindow {
		t.Errorf("restored capabilities mismatch: got %+v, want %+v", restored, caps)
	}

	desc := domain.ModelDescriptor{
		ID:          "gemini-2.5-thinking",
		DisplayName: "Gemini 2.5 Thinking",
		Capabilities: []domain.ModelCapability{
			domain.CapChat,
			domain.CapThinking,
			domain.CapToolUse,
		},
		Metadata: map[string]any{
			"capabilities_profile": caps,
		},
	}

	profile := desc.CapabilitiesProfile()
	if profile.ModelID != "gemini-2.5-thinking" {
		t.Errorf("expected profile model_id gemini-2.5-thinking, got %s", profile.ModelID)
	}
	if !profile.SupportsThinking() {
		t.Errorf("expected profile to support thinking")
	}
}
