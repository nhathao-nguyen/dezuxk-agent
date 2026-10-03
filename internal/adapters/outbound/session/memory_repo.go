package session

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const (
	// Nghỉ cục bộ sau HTTP 429. Không phải hạn mức của Google.
	rateLimitCooldown = 60 * time.Second
	// Nghỉ cục bộ khi lần xoay bí mật không tới được máy chủ gốc.
	refreshBackoff = 60 * time.Second
)

type refreshKey struct {
	accountID string
	service   domain.ServiceKind
}

type refreshFlight struct {
	done chan struct{}
	err  error
}

type MemorySessionRepository struct {
	mu              sync.Mutex
	accounts        map[string]*domain.ManagedAccount
	order           []string
	cursor          int
	refreshing      map[refreshKey]*refreshFlight
	refresher       ports.DerivedSecretRefresher
	alerts          []domain.SessionAlert
	coolingDuration time.Duration
	alertDispatcher ports.AlertDispatcher
	strategy        ports.AccountSelectionStrategy
}

func (r *MemorySessionRepository) SetAlertDispatcher(d ports.AlertDispatcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alertDispatcher = d
}

// SetSelectionStrategy thiết lập chiến lược lựa chọn tài khoản
func (r *MemorySessionRepository) SetSelectionStrategy(s ports.AccountSelectionStrategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strategy = s
}

// GetSelectionStrategy lấy chiến lược lựa chọn tài khoản hiện tại
func (r *MemorySessionRepository) GetSelectionStrategy() ports.AccountSelectionStrategy {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.strategy == nil {
		return NewWeightedHealthScoreStrategy()
	}
	return r.strategy
}

func NewMemorySessionRepository(refresher ports.DerivedSecretRefresher) *MemorySessionRepository {
	return &MemorySessionRepository{
		accounts:        make(map[string]*domain.ManagedAccount),
		order:           make([]string, 0),
		refreshing:      make(map[refreshKey]*refreshFlight),
		refresher:       refresher,
		alerts:          make([]domain.SessionAlert, 0),
		coolingDuration: rateLimitCooldown,
		strategy:        NewWeightedHealthScoreStrategy(),
	}
}

func (r *MemorySessionRepository) SetCoolingDuration(d time.Duration) {
	if d > 0 {
		r.coolingDuration = d
	}
}

func (r *MemorySessionRepository) Save(ctx context.Context, account *domain.ManagedAccount) error {
	if account == nil || account.ID == "" {
		return domain.InvalidRequest(domain.OpSession, "", domain.ServiceGemini, "thiếu định danh phiên")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	prev, exists := r.accounts[account.ID]
	if !exists {
		r.order = append(r.order, account.ID)
	}
	r.accounts[account.ID] = account
	if !exists || prev != account {
		r.promote(account)
	}
	return nil
}

func (r *MemorySessionRepository) promote(account *domain.ManagedAccount) {
	if account == nil || !account.IsHealthy || account.Jar == nil {
		return
	}
	if account.Jar.HasKey("__Secure-1PSID") {
		r.promoteService(account, domain.ServiceGemini)
	}
}

func (r *MemorySessionRepository) promoteService(account *domain.ManagedAccount, service domain.ServiceKind) {
	state := account.ServiceState(service)
	if state == domain.StateEmpty || state == domain.StateInvalid {
		_ = account.MoveService(service, domain.StateReady)
	}
}

func (r *MemorySessionRepository) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	acc, ok := r.accounts[id]
	if !ok {
		return nil, domain.Unauthenticated(domain.OpSession, "", domain.ServiceGemini, "không có phiên này")
	}
	return acc, nil
}

func (r *MemorySessionRepository) ListAll(ctx context.Context) []*domain.ManagedAccount {
	r.mu.Lock()
	defer r.mu.Unlock()

	list := make([]*domain.ManagedAccount, 0, len(r.accounts))
	for _, acc := range r.accounts {
		list = append(list, acc)
	}
	return list
}

func (r *MemorySessionRepository) hasServiceCookie(acc *domain.ManagedAccount, service domain.ServiceKind) bool {
	if acc == nil || acc.Jar == nil {
		return false
	}
	return acc.Jar.HasKey("__Secure-1PSID")
}

func (r *MemorySessionRepository) usable(acc *domain.ManagedAccount, service domain.ServiceKind, minCredits int) bool {
	if acc == nil || !acc.ServiceReady(service) {
		return false
	}
	if acc.Jar == nil || !acc.Jar.HasKey("__Secure-1PSID") {
		return false
	}
	return true
}

func (r *MemorySessionRepository) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		r.mu.Lock()
		if len(r.order) == 0 {
			r.mu.Unlock()
			return nil, domain.Unauthenticated(domain.OpSession, "", service, "chưa có phiên nào").WithPublicStatus(http.StatusServiceUnavailable)
		}

		var wait <-chan struct{}
		var paused domain.ErrorClass
		n := len(r.order)

		// Thu thập tất cả các tài khoản khả dụng
		var candidates []*domain.ManagedAccount
		for i := 0; i < n; i++ {
			idx := (r.cursor + i) % n
			acc := r.accounts[r.order[idx]]
			if r.usable(acc, service, minCredits) {
				candidates = append(candidates, acc)
			}
			if acc != nil && acc.ServiceState(service) == domain.StateRefreshing {
				if flight, ok := r.refreshing[refreshKey{acc.ID, service}]; ok {
					wait = flight.done
				}
			}
			if class, cooling := acc.ActiveCooldown(service); cooling && paused == "" && r.hasServiceCookie(acc, service) {
				paused = class
			}
		}

		if len(candidates) > 0 {
			strat := r.strategy
			if strat == nil {
				strat = NewWeightedHealthScoreStrategy()
			}
			bestAcc, err := strat.Select(ctx, candidates)
			if err == nil && bestAcc != nil {
				bestAcc.InFlightReqs++
				r.cursor = (r.cursor + 1) % n
				r.mu.Unlock()
				return bestAcc, nil
			}
		}
		r.mu.Unlock()

		if wait == nil {
			if paused == domain.ClassUpstreamUnavailable {
				return nil, domain.UpstreamUnavailable(domain.OpSession, "", service, "phiên đang nghỉ sau lỗi kết nối")
			}
			if paused != "" {
				return nil, domain.ClassifyUpstreamStatus(domain.OpSession, "", http.StatusTooManyRequests, true, service)
			}
			return nil, domain.Unauthenticated(domain.OpSession, "", service, "không có phiên sẵn sàng").WithPublicStatus(http.StatusServiceUnavailable)
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, domain.ClassifyTransport(domain.OpSession, "", service, ctx.Err())
		}
	}
}

func (r *MemorySessionRepository) Release(account *domain.ManagedAccount, err error) {
	if account == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if account.InFlightReqs > 0 {
		account.InFlightReqs--
	}

	if err == nil {
		account.RecordSuccess()
		return
	}

	class, service, ok := domain.ClassifiedFailure(err)
	if !ok {
		account.RecordFailure(domain.ErrorClass(""), 500)
		return
	}

	statusCode := 500
	if class == domain.ClassRateLimited {
		statusCode = http.StatusTooManyRequests
	} else if class == domain.ClassExpired || class == domain.ClassUnauthenticated {
		statusCode = http.StatusUnauthorized
	} else if class == domain.ClassUpstreamUnavailable {
		statusCode = http.StatusServiceUnavailable
	}
	account.RecordFailure(class, statusCode)

	if (class == domain.ClassRateLimited || class == domain.ClassUpstreamUnavailable) && service != "" {
		dynamicCooldown := account.GetDynamicCooldown(r.coolingDuration)
		cdUntil := time.Now().Add(dynamicCooldown)
		account.CoolService(service, cdUntil, class)
		account.SetCooldown(cdUntil)

		// Ghi nhận cảnh báo Proxy khi gặp lỗi kết nối và tài khoản có cấu hình proxy
		if account.GetProxy() != "" && class == domain.ClassUpstreamUnavailable {
			r.addAlertLocked(domain.SessionAlert{
				AccountID:      account.ID,
				Service:        service,
				Reason:         fmt.Sprintf("Proxy gặp lỗi kết nối (%s).", account.GetProxy()),
				StatusCode:     http.StatusBadGateway,
				ActionRequired: "Vui lòng kiểm tra lại Proxy URL hoặc mở Chrome để đồng bộ lại CDP qua /v1/profiles/" + account.ID + "/sync",
				CreatedAt:      time.Now(),
			})
		}
		return
	}

	// Ghi nhận cảnh báo khi session hết hạn hoặc bị từ chối xác thực (401/403/Expired)
	if class == domain.ClassExpired || class == domain.ClassUnauthenticated {
		account.IsHealthy = false
		account.HealthStatus = domain.HealthStatusAuthExpired
		r.addAlertLocked(domain.SessionAlert{
			AccountID:      account.ID,
			Service:        service,
			Reason:         "Cookie hoặc phiên đăng nhập đã hết hạn / bị thu hồi (401/403).",
			StatusCode:     http.StatusUnauthorized,
			ActionRequired: "Vui lòng mở Chrome đăng nhập lại qua API /v1/profiles/" + account.ID + "/launch hoặc /v1/profiles/" + account.ID + "/sync",
			CreatedAt:      time.Now(),
		})
	}
}

func (r *MemorySessionRepository) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	if account == nil {
		return false
	}
	return account.TryWriteLease(service)
}

func (r *MemorySessionRepository) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
	if account == nil {
		return
	}
	account.ReleaseWriteLease(service)
}

func (r *MemorySessionRepository) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {
	if account == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = account.MoveService(service, domain.StateInvalid)
	account.IsHealthy = false
	r.addAlertLocked(domain.SessionAlert{
		AccountID:      account.ID,
		Service:        service,
		Reason:         "Phiên bị đánh dấu không hợp lệ (Invalidated).",
		StatusCode:     http.StatusUnauthorized,
		ActionRequired: "Vui lòng mở Chrome đồng bộ lại cookie qua API /v1/profiles/" + account.ID + "/sync",
		CreatedAt:      time.Now(),
	})
}

func (r *MemorySessionRepository) addAlertLocked(alert domain.SessionAlert) {
	// Tránh trùng lặp cảnh báo cho cùng một account và service trong vòng 5 phút
	for _, a := range r.alerts {
		if a.AccountID == alert.AccountID && a.Service == alert.Service && time.Since(a.CreatedAt) < 5*time.Minute {
			return
		}
	}
	r.alerts = append(r.alerts, alert)
	// Giữ tối đa 100 alerts gần nhất
	if len(r.alerts) > 100 {
		r.alerts = r.alerts[len(r.alerts)-100:]
	}

	// Phát cảnh báo tự động qua Webhook Dispatcher
	if r.alertDispatcher != nil {
		errorType := "Session Alert"
		if alert.StatusCode == http.StatusUnauthorized {
			errorType = "Google HTTP 401 (Cookie/Session Revoked)"
		} else if strings.Contains(alert.Reason, "Proxy") {
			errorType = "Proxy Connection Error"
		}
		r.alertDispatcher.Dispatch(domain.AlertPayload{
			AccountID:      alert.AccountID,
			ErrorType:      errorType,
			Service:        alert.Service,
			Reason:         alert.Reason,
			StatusCode:     alert.StatusCode,
			ActionRequired: alert.ActionRequired,
			Timestamp:      alert.CreatedAt,
		})
	}
}

func (r *MemorySessionRepository) GetAlerts() []domain.SessionAlert {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.SessionAlert, len(r.alerts))
	copy(result, r.alerts)
	return result
}

func (r *MemorySessionRepository) AddAlert(alert domain.SessionAlert) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addAlertLocked(alert)
}

func (r *MemorySessionRepository) ClearAlerts(accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if accountID == "" {
		r.alerts = make([]domain.SessionAlert, 0)
		return
	}
	filtered := make([]domain.SessionAlert, 0, len(r.alerts))
	for _, a := range r.alerts {
		if a.AccountID != accountID {
			filtered = append(filtered, a)
		}
	}
	r.alerts = filtered
}

func (r *MemorySessionRepository) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	if account == nil {
		return domain.Unauthenticated(domain.OpSession, "", service, "chưa có phiên nào").WithPublicStatus(http.StatusServiceUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := refreshKey{account.ID, service}

	r.mu.Lock()
	if flight, ok := r.refreshing[key]; ok {
		r.mu.Unlock()
		return waitFlight(ctx, flight, service)
	}
	state := account.ServiceState(service)
	if state == domain.StateInvalid || state == domain.StateQuarantined {
		r.mu.Unlock()
		return domain.Expired(domain.OpSession, domain.OriginHandshake, service, "phiên không còn dùng được")
	}
	if err := account.MoveService(service, domain.StateRefreshing); err != nil {
		r.mu.Unlock()
		return domain.UpstreamRejected(domain.OpSession, domain.OriginHandshake, service, "không chuyển được trạng thái phiên")
	}
	flight := &refreshFlight{done: make(chan struct{})}
	r.refreshing[key] = flight
	r.mu.Unlock()

	var callErr error
	if r.refresher == nil {
		callErr = domain.Expired(domain.OpSession, domain.OriginHandshake, service, "chưa có đường xoay bí mật dẫn xuất")
	} else {
		callErr = r.refresher.RefreshDerivedSecret(ctx, account, service)
	}
	return r.finishRefresh(account, service, key, flight, callErr)
}

func (r *MemorySessionRepository) finishRefresh(account *domain.ManagedAccount, service domain.ServiceKind, key refreshKey, flight *refreshFlight, callErr error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if ce, ok := domain.AsCodecError(callErr); ok {
		callErr = domain.EnsureGateway(ce, domain.OpSession, service)
	}
	if callErr == nil {
		account.CommitServiceState(service, domain.StateReady)
	} else if ge, ok := domain.AsGatewayError(callErr); ok && ge.Class == domain.ClassUpstreamUnavailable {
		account.CommitServiceState(service, domain.StateReady)
		account.CoolService(service, time.Now().Add(refreshBackoff), domain.ClassUpstreamUnavailable)
	} else {
		account.CommitServiceState(service, domain.StateInvalid)
		if _, ok := domain.AsGatewayError(callErr); !ok {
			callErr = domain.Expired(domain.OpSession, domain.OriginHandshake, service, "không xoay được bí mật dẫn xuất")
		}
	}
	flight.err = callErr
	close(flight.done)
	delete(r.refreshing, key)
	return callErr
}

func waitFlight(ctx context.Context, flight *refreshFlight, service domain.ServiceKind) error {
	select {
	case <-flight.done:
		return flight.err
	case <-ctx.Done():
		return domain.ClassifyTransport(domain.OpSession, "", service, ctx.Err())
	}
}
