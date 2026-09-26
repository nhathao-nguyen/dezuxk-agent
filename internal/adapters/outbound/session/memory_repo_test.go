package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

type scriptedRefresher struct {
	calls atomic.Int32
	err   error
	enter chan struct{}
	hold  chan struct{}
	once  sync.Once
}

func (s *scriptedRefresher) RefreshDerivedSecret(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	s.calls.Add(1)
	if s.enter != nil {
		s.once.Do(func() { close(s.enter) })
	}
	if s.hold != nil {
		<-s.hold
	}
	return s.err
}

func testAccount() *domain.ManagedAccount {
	return &domain.ManagedAccount{
		ID:           "lab",
		IsHealthy:    true,
		FlowSNlM0e:   "flow-at",
		GeminiSNlM0e: "gemini-at",
		Jar: domain.NewCookieJar(map[string]string{
			"OSID":           "osid",
			"__Secure-1PSID": "psid",
		}),
	}
}

func TestRefreshDerived_SingleFlight(t *testing.T) {
	entered := make(chan struct{})
	hold := make(chan struct{})
	refresher := &scriptedRefresher{enter: entered, hold: hold}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	leaderErr := make(chan error, 1)
	go func() {
		leaderErr <- repo.RefreshDerived(context.Background(), account, domain.ServiceFlow)
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	waitErr := repo.RefreshDerived(ctx, account, domain.ServiceFlow)
	cancel()
	if refresher.calls.Load() != 1 {
		t.Fatalf("refresher calls while leader held = %d", refresher.calls.Load())
	}
	ge, ok := domain.AsGatewayError(waitErr)
	if !ok || ge.Class != domain.ClassUpstreamUnavailable {
		t.Fatalf("waiter = %v", waitErr)
	}

	close(hold)
	if err := <-leaderErr; err != nil {
		t.Fatal(err)
	}
	if refresher.calls.Load() != 1 {
		t.Fatalf("refresher calls = %d", refresher.calls.Load())
	}
	if state, _ := account.ServiceSnapshot(domain.ServiceFlow); state != domain.StateReady {
		t.Fatalf("state = %s", state)
	}
}

func TestGetAvailable_WaitsOutRefresh(t *testing.T) {
	hold := make(chan struct{})
	refresher := &scriptedRefresher{hold: hold}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- repo.RefreshDerived(context.Background(), account, domain.ServiceFlow)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for account.ServiceState(domain.ServiceFlow) != domain.StateRefreshing {
		if time.Now().After(deadline) {
			t.Fatal("refresh did not start")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	_, err := repo.GetAvailable(ctx, domain.ServiceFlow, 0)
	cancel()
	if err == nil {
		t.Fatal("GetAvailable returned a session that is still refreshing")
	}

	close(hold)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetAvailable(context.Background(), domain.ServiceFlow, 0)
	if err != nil {
		t.Fatal(err)
	}
	repo.Release(got, nil)
}

func TestRefreshDerived_InvalidDoesNotLoop(t *testing.T) {
	refresher := &scriptedRefresher{
		err: domain.Expired(domain.OpSession, domain.OriginHandshake, domain.ServiceFlow, "phiên gốc hết hạn"),
	}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceFlow); err == nil {
		t.Fatal("expected refresh failure")
	}
	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceFlow); err == nil {
		t.Fatal("expected invalid session to stay failed")
	}
	if refresher.calls.Load() != 1 {
		t.Fatalf("refresher calls = %d", refresher.calls.Load())
	}
	if state, _ := account.ServiceSnapshot(domain.ServiceFlow); state != domain.StateInvalid {
		t.Fatalf("state = %s", state)
	}
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if state, _ := account.ServiceSnapshot(domain.ServiceFlow); state != domain.StateInvalid {
		t.Fatal("saving the same account must not revive an invalid session")
	}

	replacement := testAccount()
	if err := repo.Save(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if state, _ := replacement.ServiceSnapshot(domain.ServiceFlow); state != domain.StateReady {
		t.Fatalf("replacement state = %s", state)
	}
}

func TestRefreshDerived_NetworkFailureKeepsReady(t *testing.T) {
	refresher := &scriptedRefresher{
		err: domain.ClassifyTransport(domain.OpSession, domain.OriginHandshake, domain.ServiceFlow, context.DeadlineExceeded),
	}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceFlow); err == nil {
		t.Fatal("expected transport failure")
	}
	state, cooldown := account.ServiceSnapshot(domain.ServiceFlow)
	if state != domain.StateReady {
		t.Fatalf("state = %s", state)
	}
	if !cooldown.After(time.Now()) {
		t.Fatal("expected cooldown after a failed refresh")
	}
}

func TestRelease_ClassifiesWithoutCoolingSchema(t *testing.T) {
	repo := NewMemorySessionRepository(nil)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	repo.Release(account, domain.SchemaUnexpected(domain.OpFlowGetCredits, domain.OriginNzlxg, domain.ServiceFlow, "phản hồi số dư không đúng hợp đồng"))
	state, cooldown := account.ServiceSnapshot(domain.ServiceFlow)
	if state != domain.StateReady || !cooldown.IsZero() {
		t.Fatalf("schema release changed session: %s %s", state, cooldown)
	}

	repo.Release(account, domain.ClassifyUpstreamStatus(domain.OpFlowGetCredits, domain.OriginNzlxg, 429, true, domain.ServiceFlow))
	state, cooldown = account.ServiceSnapshot(domain.ServiceFlow)
	if state != domain.StateReady || !cooldown.After(time.Now()) {
		t.Fatalf("rate limit release = %s %s", state, cooldown)
	}
	_, err := repo.GetAvailable(context.Background(), domain.ServiceFlow, 0)
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassRateLimited {
		t.Fatalf("cooled flow session error = %v", err)
	}
	got, err := repo.GetAvailable(context.Background(), domain.ServiceGemini, 0)
	if err != nil {
		t.Fatal(err)
	}
	repo.Release(got, nil)
}

func TestNilRefresherMarksInvalidOnce(t *testing.T) {
	repo := NewMemorySessionRepository(nil)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceGemini); err == nil {
		t.Fatal("expected missing refresher to fail")
	}
	if account.ServiceState(domain.ServiceGemini) != domain.StateInvalid {
		t.Fatal("gemini should be invalid")
	}
	if account.ServiceState(domain.ServiceFlow) != domain.StateReady {
		t.Fatal("flow should stay ready")
	}
}

func TestRetryAfterRefresh_SecondExpiryInvalidates(t *testing.T) {
	var calls atomic.Int32
	repo := NewMemorySessionRepository(&scriptedRefresher{})
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	err := RetryAfterRefresh(context.Background(), repo, account, domain.ServiceFlow, func() error {
		calls.Add(1)
		return domain.Expired(domain.OpFlowGetCredits, domain.OriginNzlxg, domain.ServiceFlow, "phiên gốc hết hạn")
	})
	if err == nil {
		t.Fatal("expected expiry")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if account.ServiceState(domain.ServiceFlow) != domain.StateInvalid {
		t.Fatalf("state = %s", account.ServiceState(domain.ServiceFlow))
	}
}
