package domain_test

import (
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

func TestMoveService_RejectsIllegalJump(t *testing.T) {
	account := &domain.ManagedAccount{}
	if err := account.MoveService(domain.ServiceGemini, domain.StateQuarantined); err == nil {
		t.Fatal("empty to quarantined must fail")
	}
	state, _ := account.ServiceSnapshot(domain.ServiceGemini)
	if state != domain.StateEmpty {
		t.Fatalf("state = %s", state)
	}
}

func TestMoveService_RefreshCycle(t *testing.T) {
	account := &domain.ManagedAccount{IsHealthy: true}
	if err := account.MoveService(domain.ServiceGemini, domain.StateReady); err != nil {
		t.Fatal(err)
	}
	if err := account.MoveService(domain.ServiceGemini, domain.StateRefreshing); err != nil {
		t.Fatal(err)
	}
	if account.ServiceReady(domain.ServiceGemini) {
		t.Fatal("refreshing session must not be ready")
	}
	if err := account.MoveService(domain.ServiceGemini, domain.StateInvalid); err != nil {
		t.Fatal(err)
	}
	if account.ServiceState(domain.ServiceGemini) != domain.StateInvalid {
		t.Fatal("gemini state should be invalid")
	}
}

func TestTryWriteLease(t *testing.T) {
	account := &domain.ManagedAccount{ID: "lab"}
	if !account.TryWriteLease(domain.ServiceGemini) {
		t.Fatal("first gemini lease")
	}
	if account.TryWriteLease(domain.ServiceGemini) {
		t.Fatal("second gemini lease must fail while locked")
	}
	account.ReleaseWriteLease(domain.ServiceGemini)
	if !account.TryWriteLease(domain.ServiceGemini) {
		t.Fatal("gemini lease after release")
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
