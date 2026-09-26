package domain_test

import (
	"sync"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestFlowCreditCostRegistry_DynamicConfig(t *testing.T) {
	// Baseline defaults
	reg := domain.NewFlowCreditCostRegistry(nil)
	if reg.GetCost(domain.CreditOpVeoQuality, 0) != 100 {
		t.Fatalf("expected 100, got %d", reg.GetCost(domain.CreditOpVeoQuality, 0))
	}
	if reg.GetCost(domain.CreditOpUpsample4K, 0) != 50 {
		t.Fatalf("expected 50, got %d", reg.GetCost(domain.CreditOpUpsample4K, 0))
	}

	// Custom override from infrastructure config
	custom := map[string]int{
		domain.CreditOpVeoQuality: 80,
		domain.CreditOpUpsample4K: 40,
		"custom_experiment":       15,
	}
	customReg := domain.NewFlowCreditCostRegistry(custom)
	if customReg.GetCost(domain.CreditOpVeoQuality, 0) != 80 {
		t.Fatalf("expected 80, got %d", customReg.GetCost(domain.CreditOpVeoQuality, 0))
	}
	if customReg.GetCost(domain.CreditOpUpsample4K, 0) != 40 {
		t.Fatalf("expected 40, got %d", customReg.GetCost(domain.CreditOpUpsample4K, 0))
	}
	if customReg.GetCost("custom_experiment", 0) != 15 {
		t.Fatalf("expected 15, got %d", customReg.GetCost("custom_experiment", 0))
	}

	// Fallback behavior
	if customReg.GetCost("non_existent_key", 999) != 999 {
		t.Fatalf("expected fallback 999, got %d", customReg.GetCost("non_existent_key", 999))
	}

	// Runtime dynamic update
	customReg.SetCost("custom_experiment", 25)
	if customReg.GetCost("custom_experiment", 0) != 25 {
		t.Fatalf("expected 25, got %d", customReg.GetCost("custom_experiment", 0))
	}

	// Thread-safety test
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(val int) {
			defer wg.Done()
			customReg.SetCost("concurrency_key", val)
		}(i)
		go func() {
			defer wg.Done()
			_ = customReg.GetCost("concurrency_key", 0)
			_ = customReg.AllCosts()
		}()
	}
	wg.Wait()
}
