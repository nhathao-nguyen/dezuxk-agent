package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const (
	rateLimitCooldown            = 60 * time.Second
	DefaultMaxInFlightPerAccount = 3
)

type refreshKey struct {
	accountID string
	service   domain.ServiceKind
}

type refreshFlight struct {
	done chan struct{}
	err  error
}

// PostgresSessionRepository triển khai ports.SessionRepository bền vững trên cụm PostgreSQL chuẩn production
type PostgresSessionRepository struct {
	mu              sync.Mutex
	pool            *pgxpool.Pool
	accounts        map[string]*domain.ManagedAccount
	order           []string
	cursor          int
	refreshing      map[refreshKey]*refreshFlight
	refresher       ports.DerivedSecretRefresher
	alerts          []domain.SessionAlert
	vault           *session.Vault
	coolingDuration time.Duration
	alertDispatcher ports.AlertDispatcher
	strategy        ports.AccountSelectionStrategy
	maxInFlight     int
}

var _ ports.SessionRepository = (*PostgresSessionRepository)(nil)
var _ ports.SessionSelectionConfigurable = (*PostgresSessionRepository)(nil)
var _ ports.SessionAlertNotifier = (*PostgresSessionRepository)(nil)

// NewPostgresSessionRepository khởi tạo Session Repository bền vững trên PostgreSQL
func NewPostgresSessionRepository(ctx context.Context, pool *pgxpool.Pool, refresher ports.DerivedSecretRefresher, v ...*session.Vault) (*PostgresSessionRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgxpool không được để nil")
	}

	var vault *session.Vault
	if len(v) > 0 && v[0] != nil {
		vault = v[0]
	} else {
		vault = session.NewVault(session.ResolveMasterKey(""))
	}

	repo := &PostgresSessionRepository{
		pool:            pool,
		accounts:        make(map[string]*domain.ManagedAccount),
		order:           make([]string, 0),
		refreshing:      make(map[refreshKey]*refreshFlight),
		refresher:       refresher,
		alerts:          make([]domain.SessionAlert, 0),
		vault:           vault,
		coolingDuration: rateLimitCooldown,
		strategy:        session.NewWeightedHealthScoreStrategy(),
		maxInFlight:     DefaultMaxInFlightPerAccount,
	}

	if err := repo.loadPersistedSessions(ctx); err != nil {
		return nil, fmt.Errorf("lỗi nạp sessions từ PostgreSQL: %w", err)
	}

	return repo, nil
}

func (r *PostgresSessionRepository) SetAlertDispatcher(d ports.AlertDispatcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alertDispatcher = d
}

func (r *PostgresSessionRepository) SetSelectionStrategy(s ports.AccountSelectionStrategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strategy = s
}

func (r *PostgresSessionRepository) GetSelectionStrategy() ports.AccountSelectionStrategy {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.strategy == nil {
		return session.NewWeightedHealthScoreStrategy()
	}
	return r.strategy
}

func (r *PostgresSessionRepository) SetCoolingDuration(d time.Duration) {
	if d > 0 {
		r.mu.Lock()
		r.coolingDuration = d
		r.mu.Unlock()
	}
}

func (r *PostgresSessionRepository) SetMaxInFlightPerAccount(max int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxInFlight = max
}

func (r *PostgresSessionRepository) loadPersistedSessions(ctx context.Context) error {
	query := `
		SELECT id, email, cookies_json, gemini_sn_token, user_agent, 
		       COALESCE(proxy, ''), credits_balance, tier, is_healthy, updated_at,
		       COALESCE(success_count, 0), COALESCE(failure_count, 0),
		       COALESCE(consecutive_failures, 0), COALESCE(health_score, 1.0),
		       COALESCE(count_429, 0), COALESCE(count_403, 0),
		       COALESCE(avg_latency_ms, 0.0), cooldown_until,
		       COALESCE(health_status, 'healthy')
		FROM sessions
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, email, cookiesJSON, geminiSN, ua, proxy, healthStatus string
		var credits, tier, healthyInt int
		var updatedAt time.Time
		var successCount, failureCount, count429, count403 int64
		var consecutiveFailures int
		var healthScore, avgLatency float64
		var cdUntil *time.Time

		if err := rows.Scan(
			&id, &email, &cookiesJSON, &geminiSN,
			&ua, &proxy, &credits,
			&tier, &healthyInt, &updatedAt,
			&successCount, &failureCount,
			&consecutiveFailures, &healthScore,
			&count429, &count403,
			&avgLatency, &cdUntil,
			&healthStatus,
		); err != nil {
			return err
		}

		cookieMap := make(map[string]string)
		decBytes, decErr := r.vault.Decrypt(cookiesJSON)
		if decErr == nil {
			_ = json.Unmarshal(decBytes, &cookieMap)
		} else {
			_ = json.Unmarshal([]byte(cookiesJSON), &cookieMap)
		}

		var cd time.Time
		if cdUntil != nil {
			cd = *cdUntil
		}

		acc := &domain.ManagedAccount{
			ID:                  id,
			Email:               email,
			Jar:                 domain.NewCookieJar(cookieMap),
			GeminiSNlM0e:        geminiSN,
			UserAgent:           ua,
			ProxyURL:            proxy,
			Tier:                tier,
			IsHealthy:           healthyInt == 1,
			HealthStatus:        domain.AccountHealthStatus(healthStatus),
			LastRefresh:         updatedAt,
			SuccessCount:        successCount,
			FailureCount:        failureCount,
			ConsecutiveFailures: consecutiveFailures,
			Count429:            count429,
			Count403:            count403,
			AvgLatencyMs:        avgLatency,
			CooldownUntil:       cd,
			HealthScore:         healthScore,
		}

		r.accounts[id] = acc
		r.order = append(r.order, id)
		r.promote(acc)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Nạp các alert gần đây
	alertRows, err := r.pool.Query(ctx, `
		SELECT account_id, service, reason, status_code, action_required, created_at 
		FROM session_alerts ORDER BY id DESC LIMIT 50
	`)
	if err == nil {
		defer alertRows.Close()
		for alertRows.Next() {
			var a domain.SessionAlert
			var s string
			if err := alertRows.Scan(&a.AccountID, &s, &a.Reason, &a.StatusCode, &a.ActionRequired, &a.CreatedAt); err == nil {
				a.Service = domain.ServiceKind(s)
				r.alerts = append(r.alerts, a)
			}
		}
	}

	return nil
}

func (r *PostgresSessionRepository) promote(acc *domain.ManagedAccount) {
	if acc == nil || !acc.IsHealthy || acc.Jar == nil {
		return
	}
	if acc.Jar.HasKey("__Secure-1PSID") || acc.GeminiSNlM0e != "" {
		state := acc.ServiceState(domain.ServiceGemini)
		if state == domain.StateEmpty || state == domain.StateInvalid {
			acc.CommitServiceState(domain.ServiceGemini, domain.StateReady)
		}
	}
}

func (r *PostgresSessionRepository) Save(ctx context.Context, account *domain.ManagedAccount) error {
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

	// Mã hóa cookies an toàn bằng AES-256-GCM trước khi lưu xuống PostgreSQL
	cookiesMap := account.Jar.ToMap()
	cookiesBytes, _ := json.Marshal(cookiesMap)
	cookiesStored, encErr := r.vault.Encrypt(cookiesBytes)
	if encErr != nil {
		cookiesStored = string(cookiesBytes)
	}

	healthyInt := 0
	if account.IsHealthy {
		healthyInt = 1
	}

	query := `
	INSERT INTO sessions (
		id, email, cookies_json, gemini_sn_token, 
		user_agent, proxy, credits_balance, 
		tier, is_healthy, updated_at,
		success_count, failure_count, consecutive_failures, health_score,
		count_429, count_403, avg_latency_ms, cooldown_until, health_status
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	ON CONFLICT(id) DO UPDATE SET
		email = EXCLUDED.email,
		cookies_json = EXCLUDED.cookies_json,
		gemini_sn_token = EXCLUDED.gemini_sn_token,
		user_agent = EXCLUDED.user_agent,
		proxy = EXCLUDED.proxy,
		credits_balance = EXCLUDED.credits_balance,
		tier = EXCLUDED.tier,
		is_healthy = EXCLUDED.is_healthy,
		updated_at = EXCLUDED.updated_at,
		success_count = EXCLUDED.success_count,
		failure_count = EXCLUDED.failure_count,
		consecutive_failures = EXCLUDED.consecutive_failures,
		health_score = EXCLUDED.health_score,
		count_429 = EXCLUDED.count_429,
		count_403 = EXCLUDED.count_403,
		avg_latency_ms = EXCLUDED.avg_latency_ms,
		cooldown_until = EXCLUDED.cooldown_until,
		health_status = EXCLUDED.health_status;
	`
	var cdVal *time.Time
	if !account.CooldownUntil.IsZero() {
		cdVal = &account.CooldownUntil
	}

	_, err := r.pool.Exec(ctx, query,
		account.ID, account.Email, cookiesStored, account.GeminiSNlM0e,
		account.UserAgent, account.ProxyURL, 0,
		account.Tier, healthyInt, time.Now(),
		account.SuccessCount, account.FailureCount, account.ConsecutiveFailures,
		account.GetHealthScore(), account.Count429, account.Count403,
		account.AvgLatencyMs, cdVal, string(account.GetHealthStatus()),
	)
	return err
}

func (r *PostgresSessionRepository) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	r.mu.Lock()
	acc, ok := r.accounts[id]
	r.mu.Unlock()
	if ok {
		return acc, nil
	}

	query := `
		SELECT id, email, cookies_json, gemini_sn_token, user_agent, 
		       COALESCE(proxy, ''), credits_balance, tier, is_healthy, updated_at,
		       COALESCE(success_count, 0), COALESCE(failure_count, 0),
		       COALESCE(consecutive_failures, 0), COALESCE(health_score, 1.0),
		       COALESCE(count_429, 0), COALESCE(count_403, 0),
		       COALESCE(avg_latency_ms, 0.0), cooldown_until,
		       COALESCE(health_status, 'healthy')
		FROM sessions WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, query, id)
	var email, cookiesJSON, geminiSN, ua, proxy, healthStatus string
	var credits, tier, healthyInt int
	var updatedAt time.Time
	var successCount, failureCount, count429, count403 int64
	var consecutiveFailures int
	var healthScore, avgLatency float64
	var cdUntil *time.Time

	if err := row.Scan(
		&id, &email, &cookiesJSON, &geminiSN,
		&ua, &proxy, &credits,
		&tier, &healthyInt, &updatedAt,
		&successCount, &failureCount,
		&consecutiveFailures, &healthScore,
		&count429, &count403,
		&avgLatency, &cdUntil,
		&healthStatus,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.Unauthenticated(domain.OpSession, "", domain.ServiceGemini, "không tìm thấy phiên").WithPublicStatus(http.StatusNotFound)
		}
		return nil, err
	}

	cookieMap := make(map[string]string)
	decBytes, decErr := r.vault.Decrypt(cookiesJSON)
	if decErr == nil {
		_ = json.Unmarshal(decBytes, &cookieMap)
	} else {
		_ = json.Unmarshal([]byte(cookiesJSON), &cookieMap)
	}

	var cd time.Time
	if cdUntil != nil {
		cd = *cdUntil
	}

	foundAcc := &domain.ManagedAccount{
		ID:                  id,
		Email:               email,
		Jar:                 domain.NewCookieJar(cookieMap),
		GeminiSNlM0e:        geminiSN,
		UserAgent:           ua,
		ProxyURL:            proxy,
		Tier:                tier,
		IsHealthy:           healthyInt == 1,
		HealthStatus:        domain.AccountHealthStatus(healthStatus),
		LastRefresh:         updatedAt,
		SuccessCount:        successCount,
		FailureCount:        failureCount,
		ConsecutiveFailures: consecutiveFailures,
		Count429:            count429,
		Count403:            count403,
		AvgLatencyMs:        avgLatency,
		CooldownUntil:       cd,
		HealthScore:         healthScore,
	}

	r.mu.Lock()
	r.accounts[id] = foundAcc
	r.order = append(r.order, id)
	r.promote(foundAcc)
	r.mu.Unlock()

	return foundAcc, nil
}

func (r *PostgresSessionRepository) ListAll(ctx context.Context) []*domain.ManagedAccount {
	r.mu.Lock()
	defer r.mu.Unlock()

	list := make([]*domain.ManagedAccount, 0, len(r.order))
	for _, id := range r.order {
		acc := r.accounts[id]
		if acc == nil {
			continue
		}
		list = append(list, acc)
	}
	return list
}

func (r *PostgresSessionRepository) hasServiceCookie(acc *domain.ManagedAccount, service domain.ServiceKind) bool {
	if acc == nil || acc.Jar == nil {
		return false
	}
	return acc.Jar.HasKey("__Secure-1PSID")
}

func (r *PostgresSessionRepository) usable(acc *domain.ManagedAccount, service domain.ServiceKind, minCredits int) bool {
	if acc == nil || !acc.ServiceReady(service) {
		return false
	}
	if acc.Jar == nil || !acc.Jar.HasKey("__Secure-1PSID") {
		return false
	}
	limit := r.maxInFlight
	if limit <= 0 {
		limit = DefaultMaxInFlightPerAccount
	}
	if acc.InFlightReqs >= int64(limit) {
		return false
	}
	return true
}

func (r *PostgresSessionRepository) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
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
				strat = session.NewWeightedHealthScoreStrategy()
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
			limit := r.maxInFlight
			if limit <= 0 {
				limit = DefaultMaxInFlightPerAccount
			}
			allBusy := false
			r.mu.Lock()
			for _, id := range r.order {
				if a := r.accounts[id]; a != nil && a.InFlightReqs >= int64(limit) {
					allBusy = true
					break
				}
			}
			r.mu.Unlock()

			if allBusy {
				select {
				case <-time.After(100 * time.Millisecond):
					continue
				case <-ctx.Done():
					return nil, domain.ClassifyTransport(domain.OpSession, "", service, ctx.Err())
				}
			}

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

func (r *PostgresSessionRepository) Release(account *domain.ManagedAccount, err error) {
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
		_, _ = r.pool.Exec(context.Background(),
			`UPDATE sessions SET success_count = $1, consecutive_failures = 0, health_score = $2, avg_latency_ms = $3, health_status = $4, updated_at = $5 WHERE id = $6`,
			account.SuccessCount, account.GetHealthScore(), account.AvgLatencyMs, string(account.GetHealthStatus()), time.Now(), account.ID,
		)
		return
	}

	class, service, ok := domain.ClassifiedFailure(err)
	if !ok {
		account.RecordFailure(domain.ErrorClass(""), 500)
		_, _ = r.pool.Exec(context.Background(),
			`UPDATE sessions SET failure_count = $1, consecutive_failures = $2, health_score = $3, avg_latency_ms = $4, health_status = $5, updated_at = $6 WHERE id = $7`,
			account.FailureCount, account.ConsecutiveFailures, account.GetHealthScore(), account.AvgLatencyMs, string(account.GetHealthStatus()), time.Now(), account.ID,
		)
		return
	}

	var reason, actionRequired string
	statusCode := 500

	switch class {
	case domain.ClassRateLimited:
		statusCode = http.StatusTooManyRequests
		reason = "Google rate limit (429 RESOURCE_EXHAUSTED). Tạm thời hạ tải tài khoản."
		actionRequired = "Chờ hết thời gian cooldown."
	case domain.ClassUpstreamUnavailable:
		statusCode = http.StatusServiceUnavailable
		reason = "Mô hình upstream tạm thời không phản hồi (503 MODEL_UNAVAILABLE)."
		actionRequired = "Chờ hạ tải và tự động thử lại trên tài khoản khác."
	case domain.ClassExpired, domain.ClassUnauthenticated, domain.ClassUnauthorized:
		account.CommitServiceState(service, domain.StateInvalid)
		account.IsHealthy = false
		account.HealthStatus = domain.HealthStatusAuthExpired
		statusCode = http.StatusUnauthorized
		reason = "Cookie Google đã hết hạn hoặc bị thu hồi (401/403)."
		actionRequired = "Mở Chrome để đồng bộ lại CDP qua /v1/profiles/" + account.ID + "/launch hoặc /v1/profiles/" + account.ID + "/sync."
	default:
		statusCode = 500
		reason = fmt.Sprintf("Lỗi upstream không xác định (%s): %v", class, err)
		actionRequired = "Kiểm tra log Gateway và tài khoản Google."
	}
	account.RecordFailure(class, statusCode)

	if (class == domain.ClassRateLimited || class == domain.ClassUpstreamUnavailable) && service != "" {
		dynamicCooldown := account.GetDynamicCooldown(r.coolingDuration)
		cdUntil := time.Now().Add(dynamicCooldown)
		account.CoolService(service, cdUntil, class)
		account.SetCooldown(cdUntil)
	}

	var cdVal *time.Time
	if !account.CooldownUntil.IsZero() {
		cdVal = &account.CooldownUntil
	}
	healthyInt := 0
	if account.IsHealthy {
		healthyInt = 1
	}

	_, _ = r.pool.Exec(context.Background(),
		`UPDATE sessions SET failure_count = $1, consecutive_failures = $2, health_score = $3, count_429 = $4, count_403 = $5, avg_latency_ms = $6, cooldown_until = $7, is_healthy = $8, health_status = $9, updated_at = $10 WHERE id = $11`,
		account.FailureCount, account.ConsecutiveFailures, account.GetHealthScore(), account.Count429, account.Count403, account.AvgLatencyMs, cdVal, healthyInt, string(account.GetHealthStatus()), time.Now(), account.ID,
	)

	alert := domain.SessionAlert{
		AccountID:      account.ID,
		Service:        service,
		Reason:         reason,
		StatusCode:     statusCode,
		ActionRequired: actionRequired,
		CreatedAt:      time.Now(),
	}
	r.alerts = append(r.alerts, alert)
	if len(r.alerts) > 100 {
		r.alerts = r.alerts[len(r.alerts)-100:]
	}
	_, _ = r.pool.Exec(context.Background(),
		`INSERT INTO session_alerts (account_id, service, reason, status_code, action_required, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		alert.AccountID, string(alert.Service), alert.Reason, alert.StatusCode, alert.ActionRequired, alert.CreatedAt,
	)

	if r.alertDispatcher != nil {
		r.alertDispatcher.Dispatch(domain.AlertPayload{
			AccountID:      account.ID,
			Service:        service,
			ErrorType:      string(class),
			StatusCode:     statusCode,
			Reason:         reason,
			ActionRequired: actionRequired,
			Timestamp:      alert.CreatedAt,
		})
	}
}

func (r *PostgresSessionRepository) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {
	if account == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	account.CommitServiceState(service, domain.StateInvalid)
	account.IsHealthy = false
	account.HealthStatus = domain.HealthStatusAuthExpired
	_, _ = r.pool.Exec(context.Background(),
		`UPDATE sessions SET is_healthy = 0, health_status = 'auth_expired', updated_at = $1 WHERE id = $2`,
		time.Now(), account.ID,
	)
}

func (r *PostgresSessionRepository) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	if account == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return account.TryWriteLease(service)
}

func (r *PostgresSessionRepository) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
	if account == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	account.ReleaseWriteLease(service)
}

func (r *PostgresSessionRepository) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
	if r.refresher == nil || account == nil {
		return nil
	}

	key := refreshKey{accountID: account.ID, service: service}
	r.mu.Lock()
	if flight, exists := r.refreshing[key]; exists {
		r.mu.Unlock()
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	flight := &refreshFlight{done: make(chan struct{})}
	r.refreshing[key] = flight
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.refreshing, key)
		close(flight.done)
		r.mu.Unlock()
	}()

	err := r.refresher.RefreshDerivedSecret(ctx, account, service)
	flight.err = err
	if err == nil {
		_ = r.Save(ctx, account)
	}
	return err
}

func (r *PostgresSessionRepository) GetAlerts() []domain.SessionAlert {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]domain.SessionAlert, len(r.alerts))
	copy(res, r.alerts)
	return res
}

func (r *PostgresSessionRepository) AddAlert(alert domain.SessionAlert) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alerts = append(r.alerts, alert)
	if len(r.alerts) > 100 {
		r.alerts = r.alerts[len(r.alerts)-100:]
	}
	_, _ = r.pool.Exec(context.Background(),
		`INSERT INTO session_alerts (account_id, service, reason, status_code, action_required, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		alert.AccountID, string(alert.Service), alert.Reason, alert.StatusCode, alert.ActionRequired, alert.CreatedAt,
	)
}

func (r *PostgresSessionRepository) ClearAlerts(accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if accountID == "" {
		r.alerts = nil
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM session_alerts;`)
		return
	}
	filtered := make([]domain.SessionAlert, 0, len(r.alerts))
	for _, a := range r.alerts {
		if a.AccountID != accountID {
			filtered = append(filtered, a)
		}
	}
	r.alerts = filtered
	_, _ = r.pool.Exec(context.Background(), `DELETE FROM session_alerts WHERE account_id = $1;`, accountID)
}

// Ping kiểm tra thực tế kết nối sống với PostgreSQL thông qua pool.Ping
func (r *PostgresSessionRepository) Ping(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("postgres connection pool is nil")
	}
	return r.pool.Ping(ctx)
}
