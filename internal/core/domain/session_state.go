package domain

import (
	"fmt"
	"time"
)

type SessionState string

const (
	StateEmpty          SessionState = ""
	StateAuthenticating SessionState = "authenticating"
	StateReady          SessionState = "ready"
	StateRefreshing     SessionState = "refreshing"
	StateCooling        SessionState = "cooling"
	StateInvalid        SessionState = "invalid"
	StateQuarantined    SessionState = "quarantined"
)

type serviceGate struct {
	State         SessionState
	CooldownUntil time.Time
	CooldownClass ErrorClass
	Writing       bool
}

func (a *ManagedAccount) gatePtr(service ServiceKind) *serviceGate {
	if service == ServiceFlow {
		return &a.flowGate
	}
	return &a.geminiGate
}

func legalTransition(from, to SessionState) bool {
	if from == to {
		return true
	}
	switch from {
	case StateEmpty:
		return to == StateReady || to == StateRefreshing || to == StateAuthenticating
	case StateAuthenticating:
		return to == StateReady || to == StateRefreshing || to == StateInvalid
	case StateReady:
		return to == StateRefreshing || to == StateInvalid || to == StateQuarantined || to == StateCooling
	case StateRefreshing:
		return to == StateReady || to == StateInvalid || to == StateCooling
	case StateCooling:
		return to == StateReady || to == StateRefreshing || to == StateInvalid || to == StateQuarantined
	case StateInvalid:
		return to == StateReady || to == StateAuthenticating
	case StateQuarantined:
		return to == StateReady || to == StateInvalid
	default:
		return false
	}
}

func (a *ManagedAccount) ServiceState(service ServiceKind) SessionState {
	if a == nil {
		return StateEmpty
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.gatePtr(service).State
}

func (a *ManagedAccount) ServiceSnapshot(service ServiceKind) (SessionState, time.Time) {
	if a == nil {
		return StateEmpty, time.Time{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	gate := a.gatePtr(service)
	return gate.State, gate.CooldownUntil
}

func (a *ManagedAccount) ServiceReady(service ServiceKind) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.IsHealthy {
		return false
	}
	gate := a.gatePtr(service)
	if gate.State == StateCooling {
		if !gate.CooldownUntil.IsZero() && time.Now().After(gate.CooldownUntil) {
			gate.State = StateReady
			gate.CooldownUntil = time.Time{}
			gate.CooldownClass = ""
			return true
		}
		return false
	}
	if gate.State != StateReady {
		return false
	}
	return gate.CooldownUntil.IsZero() || time.Now().After(gate.CooldownUntil)
}

func (a *ManagedAccount) MoveService(service ServiceKind, to SessionState) error {
	if a == nil {
		return fmt.Errorf("phiên rỗng")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	gate := a.gatePtr(service)
	if !legalTransition(gate.State, to) {
		return fmt.Errorf("chuyển trạng thái %s không hợp lệ", service)
	}
	gate.State = to
	return nil
}

// CommitServiceState ghi trạng thái kết thúc của một flight xoay bí mật.
func (a *ManagedAccount) CommitServiceState(service ServiceKind, to SessionState) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.gatePtr(service).State = to
	a.mu.Unlock()
}

func (a *ManagedAccount) CoolService(service ServiceKind, until time.Time, class ErrorClass) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	gate := a.gatePtr(service)
	gate.CooldownUntil = until
	gate.CooldownClass = class
}

// TryWriteLease giữ một ghi trên (account, service). Ảnh và video dùng ServiceFlow.
func (a *ManagedAccount) TryWriteLease(service ServiceKind) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	gate := a.gatePtr(service)
	if gate.Writing {
		return false
	}
	gate.Writing = true
	return true
}

// ReleaseWriteLease trả lease ghi. Gọi thừa không đổi trạng thái phiên.
func (a *ManagedAccount) ReleaseWriteLease(service ServiceKind) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.gatePtr(service).Writing = false
	a.mu.Unlock()
}

// ActiveCooldown báo service đang ready nhưng chưa được phát cho caller.
func (a *ManagedAccount) ActiveCooldown(service ServiceKind) (ErrorClass, bool) {
	if a == nil {
		return "", false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	gate := a.gatePtr(service)
	if gate.State != StateReady && gate.State != StateCooling {
		return "", false
	}
	if gate.CooldownUntil.IsZero() || time.Now().After(gate.CooldownUntil) {
		return "", false
	}
	if gate.CooldownClass == "" {
		return ClassRateLimited, true
	}
	return gate.CooldownClass, true
}
