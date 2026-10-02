package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"

	_ "github.com/mattn/go-sqlite3"
)

// SqliteSessionRepository triển khai ports.SessionRepository bền vững với cơ sở dữ liệu SQLite cục bộ (WAL mode)
type SqliteSessionRepository struct {
	mu              sync.Mutex
	db              *sql.DB
	accounts        map[string]*domain.ManagedAccount
	order           []string
	cursor          int
	refreshing      map[refreshKey]*refreshFlight
	refresher       ports.DerivedSecretRefresher
	alerts          []domain.SessionAlert
	vault           *Vault
	coolingDuration time.Duration
	alertDispatcher ports.AlertDispatcher
}

func (r *SqliteSessionRepository) DB() *sql.DB {
	return r.db
}

func (r *SqliteSessionRepository) SetAlertDispatcher(d ports.AlertDispatcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alertDispatcher = d
}

// NewSqliteSessionRepository khởi tạo SQLite Session Store với chế độ WAL cho hiệu năng cao
func NewSqliteSessionRepository(dbPath string, refresher ports.DerivedSecretRefresher, v ...*Vault) (*SqliteSessionRepository, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("không thể tạo thư mục database: %w", err)
	}

	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_sync=NORMAL&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("không thể mở SQLite database: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite một file an toàn nhất với single writer
	db.SetMaxIdleConns(1)

	var vault *Vault
	if len(v) > 0 && v[0] != nil {
		vault = v[0]
	} else {
		vault = NewVault(ResolveMasterKey(""))
	}

	repo := &SqliteSessionRepository{
		db:              db,
		accounts:        make(map[string]*domain.ManagedAccount),
		order:           make([]string, 0),
		refreshing:      make(map[refreshKey]*refreshFlight),
		refresher:       refresher,
		alerts:          make([]domain.SessionAlert, 0),
		vault:           vault,
		coolingDuration: rateLimitCooldown,
	}

	if err := repo.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("lỗi khởi tạo bảng SQLite: %w", err)
	}

	if err := repo.loadPersistedSessions(); err != nil {
		db.Close()
		return nil, fmt.Errorf("lỗi nạp sessions từ SQLite: %w", err)
	}

	return repo, nil
}

func (r *SqliteSessionRepository) SetCoolingDuration(d time.Duration) {
	if d > 0 {
		r.coolingDuration = d
	}
}

func (r *SqliteSessionRepository) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		email TEXT,
		cookies_json TEXT,
		gemini_sn_token TEXT,
		user_agent TEXT,
		proxy TEXT,
		credits_balance INTEGER DEFAULT 0,
		tier INTEGER DEFAULT 1,
		is_healthy INTEGER DEFAULT 1,
		updated_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS session_alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id TEXT,
		service TEXT,
		reason TEXT,
		status_code INTEGER,
		action_required TEXT,
		created_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS virtual_keys (
		id TEXT PRIMARY KEY,
		key_hash TEXT UNIQUE NOT NULL,
		key_prefix TEXT NOT NULL,
		name TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		rate_limit_rpm INTEGER DEFAULT 60,
		daily_quota_requests INTEGER DEFAULT 1000,
		used_today INTEGER DEFAULT 0,
		last_used_date TEXT DEFAULT '',
		allowed_models_json TEXT DEFAULT '["*"]',
		is_active INTEGER DEFAULT 1,
		expires_at DATETIME,
		created_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_virtual_keys_hash ON virtual_keys(key_hash);
	CREATE INDEX IF NOT EXISTS idx_virtual_keys_active ON virtual_keys(is_active);
	`
	if _, err := r.db.Exec(schema); err != nil {
		return err
	}
	// Đảm bảo tương thích với database SQLite đã tồn tại trước đó
	_, _ = r.db.Exec("ALTER TABLE sessions ADD COLUMN proxy TEXT;")
	_, _ = r.db.Exec("ALTER TABLE sessions ADD COLUMN success_count INTEGER DEFAULT 0;")
	_, _ = r.db.Exec("ALTER TABLE sessions ADD COLUMN failure_count INTEGER DEFAULT 0;")
	_, _ = r.db.Exec("ALTER TABLE sessions ADD COLUMN consecutive_failures INTEGER DEFAULT 0;")
	_, _ = r.db.Exec("ALTER TABLE sessions ADD COLUMN health_score REAL DEFAULT 1.0;")
	return nil
}

func (r *SqliteSessionRepository) loadPersistedSessions() error {
	rows, err := r.db.Query(`
		SELECT id, email, cookies_json, gemini_sn_token, user_agent, 
		       COALESCE(proxy, ''), credits_balance, tier, is_healthy, updated_at,
		       COALESCE(success_count, 0), COALESCE(failure_count, 0),
		       COALESCE(consecutive_failures, 0), COALESCE(health_score, 1.0)
		FROM sessions
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, email, cookiesJSON, geminiSN, ua, proxy string
		var credits, tier, healthyInt int
		var updatedAt time.Time
		var successCount, failureCount int64
		var consecutiveFailures int
		var healthScore float64

		if err := rows.Scan(
			&id, &email, &cookiesJSON, &geminiSN,
			&ua, &proxy, &credits,
			&tier, &healthyInt, &updatedAt,
			&successCount, &failureCount,
			&consecutiveFailures, &healthScore,
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

		acc := &domain.ManagedAccount{
			ID:                  id,
			Email:               email,
			Jar:                 domain.NewCookieJar(cookieMap),
			GeminiSNlM0e:        geminiSN,
			UserAgent:           ua,
			ProxyURL:            proxy,
			Tier:                tier,
			IsHealthy:           healthyInt == 1,
			LastRefresh:         updatedAt,
			SuccessCount:        successCount,
			FailureCount:        failureCount,
			ConsecutiveFailures: consecutiveFailures,
			HealthScore:         healthScore,
		}

		r.accounts[id] = acc
		r.order = append(r.order, id)
		r.promote(acc)
	}

	// Nạp các alert gần đây
	alertRows, err := r.db.Query(`
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

func (r *SqliteSessionRepository) Save(ctx context.Context, account *domain.ManagedAccount) error {
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

	// Đồng bộ bền vững vào SQLite với kho mã hóa Vault (Encryption at Rest)
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
		success_count, failure_count, consecutive_failures, health_score
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		email = excluded.email,
		cookies_json = excluded.cookies_json,
		gemini_sn_token = excluded.gemini_sn_token,
		user_agent = excluded.user_agent,
		proxy = excluded.proxy,
		credits_balance = excluded.credits_balance,
		tier = excluded.tier,
		is_healthy = excluded.is_healthy,
		updated_at = excluded.updated_at,
		success_count = excluded.success_count,
		failure_count = excluded.failure_count,
		consecutive_failures = excluded.consecutive_failures,
		health_score = excluded.health_score;
	`
	_, err := r.db.ExecContext(ctx, query,
		account.ID, account.Email, cookiesStored, account.GeminiSNlM0e,
		account.UserAgent, account.ProxyURL, 0,
		account.Tier, healthyInt, time.Now(),
		account.SuccessCount, account.FailureCount,
		account.ConsecutiveFailures, account.GetHealthScore(),
	)
	return err
}

func (r *SqliteSessionRepository) promote(account *domain.ManagedAccount) {
	if account == nil || !account.IsHealthy || account.Jar == nil {
		return
	}
	if account.Jar.HasKey("__Secure-1PSID") {
		r.promoteService(account, domain.ServiceGemini)
	}
}

func (r *SqliteSessionRepository) promoteService(account *domain.ManagedAccount, service domain.ServiceKind) {
	state := account.ServiceState(service)
	if state == domain.StateEmpty || state == domain.StateInvalid {
		_ = account.MoveService(service, domain.StateReady)
	}
}

func (r *SqliteSessionRepository) FindByID(ctx context.Context, id string) (*domain.ManagedAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	acc, ok := r.accounts[id]
	if !ok {
		return nil, domain.Unauthenticated(domain.OpSession, "", domain.ServiceGemini, "không có phiên này")
	}
	return acc, nil
}

func (r *SqliteSessionRepository) ListAll(ctx context.Context) []*domain.ManagedAccount {
	r.mu.Lock()
	defer r.mu.Unlock()

	list := make([]*domain.ManagedAccount, 0, len(r.accounts))
	for _, acc := range r.accounts {
		list = append(list, acc)
	}
	return list
}

func (r *SqliteSessionRepository) hasServiceCookie(acc *domain.ManagedAccount, service domain.ServiceKind) bool {
	if acc == nil || acc.Jar == nil {
		return false
	}
	return acc.Jar.HasKey("__Secure-1PSID")
}

func (r *SqliteSessionRepository) usable(acc *domain.ManagedAccount, service domain.ServiceKind, minCredits int) bool {
	if acc == nil || !acc.ServiceReady(service) {
		return false
	}
	if acc.Jar == nil || !acc.Jar.HasKey("__Secure-1PSID") {
		return false
	}
	return true
}

func (r *SqliteSessionRepository) GetAvailable(ctx context.Context, service domain.ServiceKind, minCredits int) (*domain.ManagedAccount, error) {
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

		// Thuật toán Dynamic Health-Scored Load Balancing (kết hợp Health Score & In-Flight Reqs)
		var bestAcc *domain.ManagedAccount
		var bestIdx int = -1
		var maxScore float64 = -1e9

		for i := 0; i < n; i++ {
			idx := (r.cursor + i) % n
			acc := r.accounts[r.order[idx]]
			if r.usable(acc, service, minCredits) {
				inFlight := acc.InFlightReqs
				health := acc.GetHealthScore()
				// Điểm tổng hợp: Điểm sức khỏe cao + in-flight thấp nhất sẽ được ưu tiên điều phối trước
				compositeScore := health - (0.25 * float64(inFlight))
				if compositeScore > maxScore {
					maxScore = compositeScore
					bestAcc = acc
					bestIdx = idx
				} else if compositeScore == maxScore && bestAcc != nil && inFlight < bestAcc.InFlightReqs {
					bestAcc = acc
					bestIdx = idx
				}
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

		if bestAcc != nil {
			bestAcc.InFlightReqs++
			r.cursor = (bestIdx + 1) % n
			r.mu.Unlock()
			return bestAcc, nil
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

func (r *SqliteSessionRepository) Release(account *domain.ManagedAccount, err error) {
	if account == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if account.InFlightReqs > 0 {
		account.InFlightReqs--
	}

	// Trường hợp yêu cầu thành công: Ghi nhận success và hồi phục điểm sức khỏe
	if err == nil {
		account.RecordSuccess()
		_, _ = r.db.Exec(
			`UPDATE sessions SET success_count = ?, consecutive_failures = 0, health_score = ?, updated_at = ? WHERE id = ?`,
			account.SuccessCount, account.GetHealthScore(), time.Now(), account.ID,
		)
		return
	}

	class, service, ok := domain.ClassifiedFailure(err)
	if !ok {
		account.RecordFailure(domain.ErrorClass(""), 500)
		_, _ = r.db.Exec(
			`UPDATE sessions SET failure_count = ?, consecutive_failures = ?, health_score = ?, updated_at = ? WHERE id = ?`,
			account.FailureCount, account.ConsecutiveFailures, account.GetHealthScore(), time.Now(), account.ID,
		)
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
		account.CoolService(service, time.Now().Add(dynamicCooldown), class)

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
		_, _ = r.db.Exec(
			`UPDATE sessions SET failure_count = ?, consecutive_failures = ?, health_score = ?, updated_at = ? WHERE id = ?`,
			account.FailureCount, account.ConsecutiveFailures, account.GetHealthScore(), time.Now(), account.ID,
		)
		return
	}

	// Ghi nhận cảnh báo khi session hết hạn (401/403/Expired)
	if class == domain.ClassExpired || class == domain.ClassUnauthenticated {
		account.IsHealthy = false
		r.addAlertLocked(domain.SessionAlert{
			AccountID:      account.ID,
			Service:        service,
			Reason:         "Cookie hoặc phiên đăng nhập đã hết hạn / bị thu hồi (401/403).",
			StatusCode:     http.StatusUnauthorized,
			ActionRequired: "Vui lòng mở Chrome đăng nhập lại qua API /v1/profiles/" + account.ID + "/launch hoặc /v1/profiles/" + account.ID + "/sync",
			CreatedAt:      time.Now(),
		})
		_, _ = r.db.Exec(
			`UPDATE sessions SET failure_count = ?, consecutive_failures = ?, health_score = 0, is_healthy = 0, updated_at = ? WHERE id = ?`,
			account.FailureCount, account.ConsecutiveFailures, time.Now(), account.ID,
		)
	}
}

func (r *SqliteSessionRepository) TryWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) bool {
	if account == nil {
		return false
	}
	return account.TryWriteLease(service)
}

func (r *SqliteSessionRepository) ReleaseWriteLease(account *domain.ManagedAccount, service domain.ServiceKind) {
	if account == nil {
		return
	}
	account.ReleaseWriteLease(service)
}

func (r *SqliteSessionRepository) Invalidate(account *domain.ManagedAccount, service domain.ServiceKind) {
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
	_, _ = r.db.Exec(`UPDATE sessions SET is_healthy = 0, updated_at = ? WHERE id = ?`, time.Now(), account.ID)
}

func (r *SqliteSessionRepository) addAlertLocked(alert domain.SessionAlert) {
	for _, a := range r.alerts {
		if a.AccountID == alert.AccountID && a.Service == alert.Service && time.Since(a.CreatedAt) < 5*time.Minute {
			return
		}
	}
	r.alerts = append(r.alerts, alert)
	if len(r.alerts) > 100 {
		r.alerts = r.alerts[len(r.alerts)-100:]
	}
	_, _ = r.db.Exec(
		`INSERT INTO session_alerts (account_id, service, reason, status_code, action_required, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		alert.AccountID, string(alert.Service), alert.Reason, alert.StatusCode, alert.ActionRequired, alert.CreatedAt,
	)

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

func (r *SqliteSessionRepository) GetAlerts() []domain.SessionAlert {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.SessionAlert, len(r.alerts))
	copy(result, r.alerts)
	return result
}

func (r *SqliteSessionRepository) AddAlert(alert domain.SessionAlert) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addAlertLocked(alert)
}

func (r *SqliteSessionRepository) ClearAlerts(accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if accountID == "" {
		r.alerts = make([]domain.SessionAlert, 0)
		_, _ = r.db.Exec(`DELETE FROM session_alerts`)
		return
	}
	filtered := make([]domain.SessionAlert, 0, len(r.alerts))
	for _, a := range r.alerts {
		if a.AccountID != accountID {
			filtered = append(filtered, a)
		}
	}
	r.alerts = filtered
	_, _ = r.db.Exec(`DELETE FROM session_alerts WHERE account_id = ?`, accountID)
}

func (r *SqliteSessionRepository) RefreshDerived(ctx context.Context, account *domain.ManagedAccount, service domain.ServiceKind) error {
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
		return domain.Expired(domain.OpSession, domain.OriginHandshake, service, "phiên không trong trạng thái có thể xoay")
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

	var refreshErr error
	if r.refresher != nil {
		refreshErr = r.refresher.RefreshDerivedSecret(ctx, account, service)
	} else {
		refreshErr = domain.Unauthenticated(domain.OpSession, domain.OriginHandshake, service, "không có bộ xoay bí mật")
	}
	flight.err = refreshErr

	r.mu.Lock()
	defer r.mu.Unlock()
	if refreshErr != nil {
		account.CoolService(service, time.Now().Add(refreshBackoff), domain.ClassUpstreamUnavailable)
		_ = account.MoveService(service, domain.StateQuarantined)
		return refreshErr
	}
	_ = account.MoveService(service, domain.StateReady)
	return nil
}

// Close đóng kết nối cơ sở dữ liệu SQLite
func (r *SqliteSessionRepository) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}
