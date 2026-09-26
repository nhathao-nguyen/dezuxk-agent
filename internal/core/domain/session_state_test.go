package domain_test

import (
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

func TestMoveService_RejectsIllegalJump(t *testing.T) {
	account := &domain.ManagedAccount{}
	if err := account.MoveService(domain.ServiceFlow, domain.StateQuarantined); err == nil {
		t.Fatal("empty to quarantined must fail")
	}
	state, _ := account.ServiceSnapshot(domain.ServiceFlow)
	if state != domain.StateEmpty {
		t.Fatalf("state = %s", state)
	}
}

func TestMoveService_RefreshCycle(t *testing.T) {
	account := &domain.ManagedAccount{IsHealthy: true}
	if err := account.MoveService(domain.ServiceFlow, domain.StateReady); err != nil {
		t.Fatal(err)
	}
	if err := account.MoveService(domain.ServiceFlow, domain.StateRefreshing); err != nil {
		t.Fatal(err)
	}
	if account.ServiceReady(domain.ServiceFlow) {
		t.Fatal("refreshing session must not be ready")
	}
	if err := account.MoveService(domain.ServiceFlow, domain.StateInvalid); err != nil {
		t.Fatal(err)
	}
	if err := account.MoveService(domain.ServiceGemini, domain.StateReady); err != nil {
		t.Fatal(err)
	}
	if account.ServiceState(domain.ServiceFlow) != domain.StateInvalid {
		t.Fatal("flow state changed while moving gemini")
	}
}

func TestTryWriteLease_FlowDoesNotBlockGemini(t *testing.T) {
	account := &domain.ManagedAccount{ID: "lab"}
	if !account.TryWriteLease(domain.ServiceFlow) {
		t.Fatal("first flow lease")
	}
	if account.TryWriteLease(domain.ServiceFlow) {
		t.Fatal("second flow lease")
	}
	if !account.TryWriteLease(domain.ServiceGemini) {
		t.Fatal("gemini lease is a different key")
	}
	account.ReleaseWriteLease(domain.ServiceFlow)
	if !account.TryWriteLease(domain.ServiceFlow) {
		t.Fatal("flow lease after release")
	}
}

func TestMoveService_CoolingCycle(t *testing.T) {
	account := &domain.ManagedAccount{IsHealthy: true}
	if err := account.MoveService(domain.ServiceGemini, domain.StateReady); err != nil {
		t.Fatal(err)
	}

	// Move to cooling
	coolUntil := time.Now().Add(50 * time.Millisecond)
	if err := account.MoveService(domain.ServiceGemini, domain.StateCooling); err != nil {
		t.Fatal(err)
	}
	account.CoolService(domain.ServiceGemini, coolUntil, domain.ClassRateLimited)

	if account.ServiceState(domain.ServiceGemini) != domain.StateCooling {
		t.Fatalf("expected state cooling, got %s", account.ServiceState(domain.ServiceGemini))
	}
	if account.ServiceReady(domain.ServiceGemini) {
		t.Fatal("cooling session must not be ready")
	}
	class, isCooling := account.ActiveCooldown(domain.ServiceGemini)
	if !isCooling || class != domain.ClassRateLimited {
		t.Fatalf("expected active cooldown rate_limited, got %v, %s", isCooling, class)
	}

	// Wait for cooldown to expire
	time.Sleep(60 * time.Millisecond)
	if !account.ServiceReady(domain.ServiceGemini) {
		t.Fatal("cooling session should be ready after cooldown expires")
	}
	if account.ServiceState(domain.ServiceGemini) != domain.StateReady {
		t.Fatalf("expected auto-transition to ready, got %s", account.ServiceState(domain.ServiceGemini))
	}
}

