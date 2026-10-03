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
		GeminiSNlM0e: "gemini-at",
		Jar: domain.NewCookieJar(map[string]string{
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
		leaderErr <- repo.RefreshDerived(context.Background(), account, domain.ServiceGemini)
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	waitErr := repo.RefreshDerived(ctx, account, domain.ServiceGemini)
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
		t.Fatalf("leader = %v", err)
	}
	if state, _ := account.ServiceSnapshot(domain.ServiceGemini); state != domain.StateReady {
		t.Fatalf("session state after refresh = %s", state)
	}
}

func TestGetAvailable_WaitsOnRefreshingSession(t *testing.T) {
	entered := make(chan struct{})
	hold := make(chan struct{})
	refresher := &scriptedRefresher{enter: entered, hold: hold}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- repo.RefreshDerived(context.Background(), account, domain.ServiceGemini)
	}()
	<-entered

	for account.ServiceState(domain.ServiceGemini) != domain.StateRefreshing {
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := repo.GetAvailable(ctx, domain.ServiceGemini, 0)
	if err == nil {
		t.Fatal("expected timeout while session is refreshing")
	}

	close(hold)
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh error: %v", err)
	}

	got, err := repo.GetAvailable(context.Background(), domain.ServiceGemini, 0)
	if err != nil {
		t.Fatalf("GetAvailable error: %v", err)
	}
	if got.ID != account.ID {
		t.Fatalf("expected account %s, got %s", account.ID, got.ID)
	}
	repo.Release(got, nil)
}

func TestRefreshDerived_ExpiredInvalidatesSession(t *testing.T) {
	refresher := &scriptedRefresher{
		err: domain.Expired(domain.OpSession, domain.OriginHandshake, domain.ServiceGemini, "phiên gốc hết hạn"),
	}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceGemini); err == nil {
		t.Fatal("expected expired error")
	}
	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceGemini); err == nil {
		t.Fatal("second call should still fail")
	}
	if refresher.calls.Load() != 1 {
		t.Fatalf("calls after invalidation = %d", refresher.calls.Load())
	}
	if state, _ := account.ServiceSnapshot(domain.ServiceGemini); state != domain.StateInvalid {
		t.Fatalf("session state after invalidation = %s", state)
	}

	replacement := testAccount()
	if err := repo.Save(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if state, _ := account.ServiceSnapshot(domain.ServiceGemini); state != domain.StateInvalid {
		t.Fatalf("account state after replacement save = %s", state)
	}
	if state, _ := replacement.ServiceSnapshot(domain.ServiceGemini); state != domain.StateReady {
		t.Fatalf("replacement state after save = %s", state)
	}
}

func TestRefreshDerived_TransportErrorCoolsSession(t *testing.T) {
	refresher := &scriptedRefresher{
		err: domain.ClassifyTransport(domain.OpSession, domain.OriginHandshake, domain.ServiceGemini, context.DeadlineExceeded),
	}
	repo := NewMemorySessionRepository(refresher)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	if err := repo.RefreshDerived(context.Background(), account, domain.ServiceGemini); err == nil {
		t.Fatal("expected timeout error")
	}
	state, cooldown := account.ServiceSnapshot(domain.ServiceGemini)
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
	repo.Release(account, domain.SchemaUnexpected(domain.OpChatCompletions, domain.OriginStreamGenerate, domain.ServiceGemini, "phản hồi không đúng hợp đồng"))
	state, cooldown := account.ServiceSnapshot(domain.ServiceGemini)
	if state != domain.StateReady || !cooldown.IsZero() {
		t.Fatalf("schema release changed session: %s %s", state, cooldown)
	}

	repo.Release(account, domain.ClassifyUpstreamStatus(domain.OpChatCompletions, domain.OriginStreamGenerate, 429, true, domain.ServiceGemini))
	state, cooldown = account.ServiceSnapshot(domain.ServiceGemini)
	if state != domain.StateReady || !cooldown.After(time.Now()) {
		t.Fatalf("rate limit release = %s %s", state, cooldown)
	}
	_, err := repo.GetAvailable(context.Background(), domain.ServiceGemini, 0)
	ge, ok := domain.AsGatewayError(err)
	if !ok || ge.Class != domain.ClassRateLimited {
		t.Fatalf("cooled gemini session error = %v", err)
	}
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
}

func TestRetryAfterRefresh_SecondExpiryInvalidates(t *testing.T) {
	var calls atomic.Int32
	repo := NewMemorySessionRepository(&scriptedRefresher{})
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	err := RetryAfterRefresh(context.Background(), repo, account, domain.ServiceGemini, func() error {
		calls.Add(1)
		return domain.Expired(domain.OpChatCompletions, domain.OriginStreamGenerate, domain.ServiceGemini, "phiên gốc hết hạn")
	})
	if err == nil {
		t.Fatal("expected expiry")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if account.ServiceState(domain.ServiceGemini) != domain.StateInvalid {
		t.Fatalf("state = %s", account.ServiceState(domain.ServiceGemini))
	}
}

func TestMaxInFlightPerAccountCeiling(t *testing.T) {
	repo := NewMemorySessionRepository(nil)
	repo.SetMaxInFlightPerAccount(2)
	account := testAccount()
	if err := repo.Save(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	// Request 1: Thành công, in-flight = 1
	acc1, err := repo.GetAvailable(context.Background(), domain.ServiceGemini, 0)
	if err != nil || acc1 == nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	if acc1.InFlightReqs != 1 {
		t.Errorf("expected InFlightReqs = 1, got %d", acc1.InFlightReqs)
	}

	// Request 2: Thành công, in-flight = 2
	acc2, err := repo.GetAvailable(context.Background(), domain.ServiceGemini, 0)
	if err != nil || acc2 == nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	if acc2.InFlightReqs != 2 {
		t.Errorf("expected InFlightReqs = 2, got %d", acc2.InFlightReqs)
	}

	// Request 3: Khi tài khoản đã đạt trần (2), request phải đợi. Với timeout 50ms, nó phải hết hạn
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, errWait := repo.GetAvailable(ctxTimeout, domain.ServiceGemini, 0)
	if errWait == nil {
		t.Fatal("expected request 3 to wait and timeout due to max in-flight ceiling")
	}

	// Giải phóng request 1
	repo.Release(acc1, nil)
	if account.InFlightReqs != 1 {
		t.Errorf("expected InFlightReqs after release = 1, got %d", account.InFlightReqs)
	}

	// Request 3 thử lại: Thành công ngay khi slot được giải phóng
	acc3, errRetry := repo.GetAvailable(context.Background(), domain.ServiceGemini, 0)
	if errRetry != nil || acc3 == nil {
		t.Fatalf("request 3 failed after release: %v", errRetry)
	}
	if acc3.InFlightReqs != 2 {
		t.Errorf("expected InFlightReqs after request 3 = 2, got %d", acc3.InFlightReqs)
	}

	repo.Release(acc2, nil)
	repo.Release(acc3, nil)
	if account.InFlightReqs != 0 {
		t.Errorf("expected InFlightReqs = 0, got %d", account.InFlightReqs)
	}
}
