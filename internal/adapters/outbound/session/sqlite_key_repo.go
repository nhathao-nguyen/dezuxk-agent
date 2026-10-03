package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// SqliteKeyRepository lưu trữ và quản lý Virtual API Keys trong cơ sở dữ liệu SQLite
type SqliteKeyRepository struct {
	mu sync.Mutex
	db *sql.DB
}

// NewSqliteKeyRepository khởi tạo kho lưu trữ Virtual API Keys
func NewSqliteKeyRepository(db *sql.DB) (*SqliteKeyRepository, error) {
	if db == nil {
		return nil, errors.New("sql.DB không được là nil")
	}

	repo := &SqliteKeyRepository{
		db: db,
	}

	if err := repo.migrate(); err != nil {
		return nil, fmt.Errorf("không thể khởi tạo bảng virtual_keys: %w", err)
	}

	return repo, nil
}

func (r *SqliteKeyRepository) migrate() error {
	schema := `
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
		created_at DATETIME NOT NULL,
		prompt_tokens_total INTEGER DEFAULT 0,
		completion_tokens_total INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		max_token_quota INTEGER DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_virtual_keys_hash ON virtual_keys(key_hash);
	CREATE INDEX IF NOT EXISTS idx_virtual_keys_active ON virtual_keys(is_active);

	CREATE TABLE IF NOT EXISTS virtual_key_token_usages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key_id TEXT NOT NULL,
		date TEXT NOT NULL,
		prompt_tokens INTEGER DEFAULT 0,
		completion_tokens INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		request_count INTEGER DEFAULT 0,
		UNIQUE(key_id, date)
	);
	CREATE INDEX IF NOT EXISTS idx_vkey_usage_key_date ON virtual_key_token_usages(key_id, date);
	`
	if _, err := r.db.Exec(schema); err != nil {
		return err
	}

	// Đảm bảo tương thích ngược nếu bảng đã được tạo trước đó
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN prompt_tokens_total INTEGER DEFAULT 0")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN completion_tokens_total INTEGER DEFAULT 0")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN total_tokens INTEGER DEFAULT 0")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN max_token_quota INTEGER DEFAULT 0")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN tenant_id TEXT DEFAULT 'default_tenant'")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN scopes_json TEXT DEFAULT '[]'")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN allowed_tools_json TEXT DEFAULT '[]'")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN allowed_workspace_roots_json TEXT DEFAULT '[]'")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN max_agent_steps INTEGER DEFAULT 25")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN max_concurrent_runs INTEGER DEFAULT 3")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN max_tool_runtime_seconds INTEGER DEFAULT 60")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN require_approval INTEGER DEFAULT 1")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN allow_shell INTEGER DEFAULT 0")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN enforce_sandbox INTEGER DEFAULT 1")
	_, _ = r.db.Exec("ALTER TABLE virtual_keys ADD COLUMN auto_merge_allowed INTEGER DEFAULT 0")

	return nil
}

// Save lưu một Virtual API Key mới vào SQLite
func (r *SqliteKeyRepository) Save(ctx context.Context, key *domain.VirtualKey) error {
	if key == nil {
		return errors.New("key không được là nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	allowedModelsJSON, err := json.Marshal(key.AllowedModels)
	if err != nil {
		allowedModelsJSON = []byte(`["*"]`)
	}

	scopesJSON, _ := json.Marshal(key.Scopes)
	allowedToolsJSON, _ := json.Marshal(key.AllowedTools)
	allowedRootsJSON, _ := json.Marshal(key.AllowedWorkspaceRoots)

	tenantID := key.TenantID
	if tenantID == "" {
		tenantID = "default_tenant"
	}
	maxSteps := key.MaxAgentSteps
	if maxSteps <= 0 {
		maxSteps = 25
	}
	maxConcurrent := key.MaxConcurrentRuns
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	runtimeSec := key.MaxToolRuntimeSeconds
	if runtimeSec <= 0 {
		runtimeSec = 60
	}
	reqApprovalInt := 0
	if key.RequireApproval {
		reqApprovalInt = 1
	}
	allowShellInt := 0
	if key.AllowShell {
		allowShellInt = 1
	}
	enforceSandboxInt := 0
	if key.EnforceSandbox {
		enforceSandboxInt = 1
	}
	autoMergeInt := 0
	if key.AutoMergeAllowed {
		autoMergeInt = 1
	}

	query := `
	INSERT INTO virtual_keys (
		id, key_hash, key_prefix, name, role, rate_limit_rpm, daily_quota_requests,
		used_today, last_used_date, allowed_models_json, is_active, expires_at, created_at,
		prompt_tokens_total, completion_tokens_total, total_tokens, max_token_quota,
		tenant_id, scopes_json, allowed_tools_json, allowed_workspace_roots_json,
		max_agent_steps, max_concurrent_runs, max_tool_runtime_seconds,
		require_approval, allow_shell, enforce_sandbox, auto_merge_allowed
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	isActiveInt := 0
	if key.IsActive {
		isActiveInt = 1
	}

	var expiresAtVal any = nil
	if key.ExpiresAt != nil {
		expiresAtVal = key.ExpiresAt.UTC()
	}

	_, err = r.db.ExecContext(ctx, query,
		key.ID,
		key.KeyHash,
		key.KeyPrefix,
		key.Name,
		key.Role,
		key.RateLimitRPM,
		key.DailyQuotaRequests,
		key.UsedToday,
		key.LastUsedDate,
		string(allowedModelsJSON),
		isActiveInt,
		expiresAtVal,
		key.CreatedAt.UTC(),
		key.PromptTokensTotal,
		key.CompletionTokensTotal,
		key.TotalTokens,
		key.MaxTokenQuota,
		tenantID,
		string(scopesJSON),
		string(allowedToolsJSON),
		string(allowedRootsJSON),
		maxSteps,
		maxConcurrent,
		runtimeSec,
		reqApprovalInt,
		allowShellInt,
		enforceSandboxInt,
		autoMergeInt,
	)
	return err
}

const selectVirtualKeyCols = `
	id, key_hash, key_prefix, name, role, rate_limit_rpm, daily_quota_requests,
	used_today, last_used_date, allowed_models_json, is_active, expires_at, created_at,
	prompt_tokens_total, completion_tokens_total, total_tokens, max_token_quota,
	COALESCE(tenant_id, 'default_tenant'), COALESCE(scopes_json, '[]'),
	COALESCE(allowed_tools_json, '[]'), COALESCE(allowed_workspace_roots_json, '[]'),
	COALESCE(max_agent_steps, 25), COALESCE(max_concurrent_runs, 3),
	COALESCE(max_tool_runtime_seconds, 60), COALESCE(require_approval, 1),
	COALESCE(allow_shell, 0), COALESCE(enforce_sandbox, 1), COALESCE(auto_merge_allowed, 0)
`

// FindByKeyHash tìm Virtual API Key theo mã băm SHA-256
func (r *SqliteKeyRepository) FindByKeyHash(ctx context.Context, keyHash string) (*domain.VirtualKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `SELECT ` + selectVirtualKeyCols + ` FROM virtual_keys WHERE key_hash = ? LIMIT 1`
	row := r.db.QueryRowContext(ctx, query, keyHash)
	return r.scanKey(row)
}

// FindByID tìm Virtual API Key theo ID
func (r *SqliteKeyRepository) FindByID(ctx context.Context, id string) (*domain.VirtualKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `SELECT ` + selectVirtualKeyCols + ` FROM virtual_keys WHERE id = ? LIMIT 1`
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanKey(row)
}

// ListActive trả về danh sách các key đang hoạt động (is_active = 1)
func (r *SqliteKeyRepository) ListActive(ctx context.Context) ([]*domain.VirtualKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `SELECT ` + selectVirtualKeyCols + ` FROM virtual_keys WHERE is_active = 1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*domain.VirtualKey
	for rows.Next() {
		k, err := r.scanKeyFromRows(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Revoke thu hồi Virtual API Key ngay lập tức (is_active = 0)
func (r *SqliteKeyRepository) Revoke(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	res, err := r.db.ExecContext(ctx, "UPDATE virtual_keys SET is_active = 0 WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("không tìm thấy key để thu hồi")
	}
	return nil
}

// ConsumeDailyQuota tiêu thụ 1 lượt quota trong ngày và trả về số quota còn lại (hoặc -1 nếu không giới hạn)
func (r *SqliteKeyRepository) ConsumeDailyQuota(ctx context.Context, id string, date string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Kiểm tra trạng thái hiện tại của key
	var (
		dailyQuota int
		usedToday  int
		lastDate   sql.NullString
		isActive   int
	)
	err := r.db.QueryRowContext(ctx,
		"SELECT daily_quota_requests, used_today, last_used_date, is_active FROM virtual_keys WHERE id = ?", id,
	).Scan(&dailyQuota, &usedToday, &lastDate, &isActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, domain.ErrInvalidAPIKey
		}
		return 0, err
	}

	if isActive != 1 {
		return 0, domain.ErrKeyRevoked
	}

	// Nếu ngày mới -> reset used_today về 0
	if lastDate.String != date {
		usedToday = 0
	}

	// Kiểm tra hạn ngạch nếu có giới hạn
	if dailyQuota > 0 && usedToday >= dailyQuota {
		return 0, domain.ErrDailyQuotaExceeded
	}

	// Tiêu thụ lượt
	newUsedToday := usedToday + 1
	_, err = r.db.ExecContext(ctx,
		"UPDATE virtual_keys SET used_today = ?, last_used_date = ? WHERE id = ?",
		newUsedToday, date, id,
	)
	if err != nil {
		return 0, err
	}

	if dailyQuota <= 0 {
		return -1, nil
	}

	remaining := dailyQuota - newUsedToday
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *SqliteKeyRepository) scanKey(row rowScanner) (*domain.VirtualKey, error) {
	var (
		k                 domain.VirtualKey
		allowedJSON       sql.NullString
		lastUsed          sql.NullString
		isActiveInt       int
		expiresAt         sql.NullTime
		tenantID          sql.NullString
		scopesJSON        sql.NullString
		allowedToolsJSON  sql.NullString
		allowedRootsJSON  sql.NullString
		reqApprovalInt    int
		allowShellInt     int
		enforceSandboxInt int
		autoMergeInt      int
	)

	err := row.Scan(
		&k.ID,
		&k.KeyHash,
		&k.KeyPrefix,
		&k.Name,
		&k.Role,
		&k.RateLimitRPM,
		&k.DailyQuotaRequests,
		&k.UsedToday,
		&lastUsed,
		&allowedJSON,
		&isActiveInt,
		&expiresAt,
		&k.CreatedAt,
		&k.PromptTokensTotal,
		&k.CompletionTokensTotal,
		&k.TotalTokens,
		&k.MaxTokenQuota,
		&tenantID,
		&scopesJSON,
		&allowedToolsJSON,
		&allowedRootsJSON,
		&k.MaxAgentSteps,
		&k.MaxConcurrentRuns,
		&k.MaxToolRuntimeSeconds,
		&reqApprovalInt,
		&allowShellInt,
		&enforceSandboxInt,
		&autoMergeInt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrInvalidAPIKey
		}
		return nil, err
	}

	k.LastUsedDate = lastUsed.String
	k.IsActive = (isActiveInt == 1)
	if expiresAt.Valid {
		t := expiresAt.Time.UTC()
		k.ExpiresAt = &t
	}

	k.TenantID = tenantID.String
	if k.TenantID == "" {
		k.TenantID = "default_tenant"
	}
	k.RequireApproval = (reqApprovalInt == 1)
	k.AllowShell = (allowShellInt == 1)
	k.EnforceSandbox = (enforceSandboxInt == 1)
	k.AutoMergeAllowed = (autoMergeInt == 1)

	k.AllowedModelsJSON = allowedJSON.String
	if allowedJSON.Valid && allowedJSON.String != "" {
		_ = json.Unmarshal([]byte(allowedJSON.String), &k.AllowedModels)
	}
	if len(k.AllowedModels) == 0 {
		k.AllowedModels = []string{"*"}
	}

	k.ScopesJSON = scopesJSON.String
	if scopesJSON.Valid && scopesJSON.String != "" {
		_ = json.Unmarshal([]byte(scopesJSON.String), &k.Scopes)
	}

	k.AllowedToolsJSON = allowedToolsJSON.String
	if allowedToolsJSON.Valid && allowedToolsJSON.String != "" {
		_ = json.Unmarshal([]byte(allowedToolsJSON.String), &k.AllowedTools)
	}

	k.AllowedWorkspaceRootsJSON = allowedRootsJSON.String
	if allowedRootsJSON.Valid && allowedRootsJSON.String != "" {
		_ = json.Unmarshal([]byte(allowedRootsJSON.String), &k.AllowedWorkspaceRoots)
	}

	return &k, nil
}

func (r *SqliteKeyRepository) scanKeyFromRows(rows *sql.Rows) (*domain.VirtualKey, error) {
	return r.scanKey(rows)
}

// RecordTokenUsage cập nhật số lượng token tiêu thụ lũy kế và theo ngày trong SQLite
func (r *SqliteKeyRepository) RecordTokenUsage(ctx context.Context, id string, promptTokens, completionTokens int) error {
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	total := int64(promptTokens + completionTokens)
	today := time.Now().UTC().Format("2006-01-02")

	// 1. Cập nhật tổng lũy kế trong bảng virtual_keys (nếu không phải khóa master ảo)
	if id != "master" {
		queryKey := `
		UPDATE virtual_keys
		SET prompt_tokens_total = prompt_tokens_total + ?,
		    completion_tokens_total = completion_tokens_total + ?,
		    total_tokens = total_tokens + ?
		WHERE id = ?
		`
		if _, err := r.db.ExecContext(ctx, queryKey, promptTokens, completionTokens, total, id); err != nil {
			return err
		}
	}

	// 2. Cập nhật hoặc chèn bản ghi theo ngày trong virtual_key_token_usages (Upsert)
	queryDaily := `
	INSERT INTO virtual_key_token_usages (key_id, date, prompt_tokens, completion_tokens, total_tokens, request_count)
	VALUES (?, ?, ?, ?, ?, 1)
	ON CONFLICT(key_id, date) DO UPDATE SET
	    prompt_tokens = prompt_tokens + excluded.prompt_tokens,
	    completion_tokens = completion_tokens + excluded.completion_tokens,
	    total_tokens = total_tokens + excluded.total_tokens,
	    request_count = request_count + 1
	`
	_, err := r.db.ExecContext(ctx, queryDaily, id, today, promptTokens, completionTokens, total)
	return err
}

// GetTokenUsageHistory lấy lịch sử tiêu thụ token của một khóa theo số ngày
func (r *SqliteKeyRepository) GetTokenUsageHistory(ctx context.Context, keyID string, days int) ([]domain.KeyTokenUsage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if days <= 0 {
		days = 7
	}

	query := `
	SELECT key_id, date, prompt_tokens, completion_tokens, total_tokens, request_count
	FROM virtual_key_token_usages
	WHERE key_id = ?
	ORDER BY date DESC
	LIMIT ?
	`
	rows, err := r.db.QueryContext(ctx, query, keyID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.KeyTokenUsage
	for rows.Next() {
		var u domain.KeyTokenUsage
		if err := rows.Scan(&u.KeyID, &u.Date, &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens, &u.RequestCount); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// GetSystemTokenUsageHistory lấy lịch sử tiêu thụ token gộp toàn hệ thống
func (r *SqliteKeyRepository) GetSystemTokenUsageHistory(ctx context.Context, days int) ([]domain.KeyTokenUsage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if days <= 0 {
		days = 7
	}

	query := `
	SELECT '' as key_id, date, SUM(prompt_tokens) as prompt_tokens, SUM(completion_tokens) as completion_tokens,
	       SUM(total_tokens) as total_tokens, SUM(request_count) as request_count
	FROM virtual_key_token_usages
	GROUP BY date
	ORDER BY date DESC
	LIMIT ?
	`
	rows, err := r.db.QueryContext(ctx, query, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.KeyTokenUsage
	for rows.Next() {
		var u domain.KeyTokenUsage
		if err := rows.Scan(&u.KeyID, &u.Date, &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens, &u.RequestCount); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// MemoryKeyRepository lưu trữ Virtual API Keys trong RAM cho kiểm thử nhanh
type MemoryKeyRepository struct {
	mu     sync.Mutex
	keys   map[string]*domain.VirtualKey // id -> key
	usages []domain.KeyTokenUsage
}

func NewMemoryKeyRepository() *MemoryKeyRepository {
	return &MemoryKeyRepository{
		keys:   make(map[string]*domain.VirtualKey),
		usages: make([]domain.KeyTokenUsage, 0),
	}
}

func (m *MemoryKeyRepository) Save(ctx context.Context, key *domain.VirtualKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[key.ID] = key
	return nil
}

func (m *MemoryKeyRepository) FindByKeyHash(ctx context.Context, keyHash string) (*domain.VirtualKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.keys {
		if k.KeyHash == keyHash {
			return k, nil
		}
	}
	return nil, domain.ErrInvalidAPIKey
}

func (m *MemoryKeyRepository) FindByID(ctx context.Context, id string) (*domain.VirtualKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return nil, errors.New("không tìm thấy key")
	}
	return k, nil
}

func (m *MemoryKeyRepository) ListActive(ctx context.Context) ([]*domain.VirtualKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.VirtualKey
	for _, k := range m.keys {
		if k.IsActive {
			list = append(list, k)
		}
	}
	return list, nil
}

func (m *MemoryKeyRepository) Revoke(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return errors.New("không tìm thấy key")
	}
	k.IsActive = false
	return nil
}

func (m *MemoryKeyRepository) ConsumeDailyQuota(ctx context.Context, id string, date string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return 0, domain.ErrInvalidAPIKey
	}
	if !k.IsActive {
		return 0, domain.ErrKeyRevoked
	}
	if k.LastUsedDate != date {
		k.UsedToday = 0
		k.LastUsedDate = date
	}
	if k.DailyQuotaRequests > 0 && k.UsedToday >= k.DailyQuotaRequests {
		return 0, domain.ErrDailyQuotaExceeded
	}
	k.UsedToday++
	if k.DailyQuotaRequests <= 0 {
		return -1, nil
	}
	return k.DailyQuotaRequests - k.UsedToday, nil
}

func (m *MemoryKeyRepository) RecordTokenUsage(ctx context.Context, id string, promptTokens, completionTokens int) error {
	if id == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	total := int64(promptTokens + completionTokens)
	if id != "master" {
		if k, ok := m.keys[id]; ok {
			k.PromptTokensTotal += int64(promptTokens)
			k.CompletionTokensTotal += int64(completionTokens)
			k.TotalTokens += total
		}
	}

	today := time.Now().UTC().Format("2006-01-02")
	found := false
	for i := range m.usages {
		if m.usages[i].KeyID == id && m.usages[i].Date == today {
			m.usages[i].PromptTokens += int64(promptTokens)
			m.usages[i].CompletionTokens += int64(completionTokens)
			m.usages[i].TotalTokens += total
			m.usages[i].RequestCount++
			found = true
			break
		}
	}
	if !found {
		m.usages = append(m.usages, domain.KeyTokenUsage{
			KeyID:            id,
			Date:             today,
			PromptTokens:     int64(promptTokens),
			CompletionTokens: int64(completionTokens),
			TotalTokens:      total,
			RequestCount:     1,
		})
	}
	return nil
}

func (m *MemoryKeyRepository) GetTokenUsageHistory(ctx context.Context, keyID string, days int) ([]domain.KeyTokenUsage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []domain.KeyTokenUsage
	for _, u := range m.usages {
		if u.KeyID == keyID {
			list = append(list, u)
		}
	}
	return list, nil
}

func (m *MemoryKeyRepository) GetSystemTokenUsageHistory(ctx context.Context, days int) ([]domain.KeyTokenUsage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byDate := make(map[string]*domain.KeyTokenUsage)
	for _, u := range m.usages {
		if cur, ok := byDate[u.Date]; ok {
			cur.PromptTokens += u.PromptTokens
			cur.CompletionTokens += u.CompletionTokens
			cur.TotalTokens += u.TotalTokens
			cur.RequestCount += u.RequestCount
		} else {
			copyU := u
			copyU.KeyID = ""
			byDate[u.Date] = &copyU
		}
	}
	var list []domain.KeyTokenUsage
	for _, v := range byDate {
		list = append(list, *v)
	}
	return list, nil
}

// Đảm bảo implement đúng ports.KeyRepository
var _ ports.KeyRepository = (*SqliteKeyRepository)(nil)
var _ ports.KeyRepository = (*MemoryKeyRepository)(nil)
