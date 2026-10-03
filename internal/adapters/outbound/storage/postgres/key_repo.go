package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// PostgresKeyRepository quản lý Virtual API Keys trên cơ sở dữ liệu PostgreSQL
type PostgresKeyRepository struct {
	mu   sync.Mutex
	pool *pgxpool.Pool
}

var _ ports.KeyRepository = (*PostgresKeyRepository)(nil)

// NewPostgresKeyRepository khởi tạo repository lưu trữ API Keys
func NewPostgresKeyRepository(pool *pgxpool.Pool) (*PostgresKeyRepository, error) {
	if pool == nil {
		return nil, errors.New("pgxpool không được là nil")
	}
	return &PostgresKeyRepository{pool: pool}, nil
}

func (r *PostgresKeyRepository) Save(ctx context.Context, key *domain.VirtualKey) error {
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
	isActiveInt := 0
	if key.IsActive {
		isActiveInt = 1
	}

	var expiresAtVal *time.Time
	if key.ExpiresAt != nil {
		t := key.ExpiresAt.UTC()
		expiresAtVal = &t
	}

	createdAtVal := key.CreatedAt.UTC()
	if createdAtVal.IsZero() {
		createdAtVal = time.Now().UTC()
	}

	query := `
	INSERT INTO virtual_keys (
		id, key_hash, key_prefix, name, role, rate_limit_rpm, daily_quota_requests,
		used_today, last_used_date, allowed_models_json, is_active, expires_at, created_at,
		prompt_tokens_total, completion_tokens_total, total_tokens, max_token_quota,
		tenant_id, scopes_json, allowed_tools_json, allowed_workspace_roots_json,
		max_agent_steps, max_concurrent_runs, max_tool_runtime_seconds,
		require_approval, allow_shell, enforce_sandbox, auto_merge_allowed
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, $11, $12, $13,
		$14, $15, $16, $17,
		$18, $19, $20, $21,
		$22, $23, $24,
		$25, $26, $27, $28
	)
	ON CONFLICT (id) DO UPDATE SET
		name = EXCLUDED.name,
		role = EXCLUDED.role,
		rate_limit_rpm = EXCLUDED.rate_limit_rpm,
		daily_quota_requests = EXCLUDED.daily_quota_requests,
		allowed_models_json = EXCLUDED.allowed_models_json,
		is_active = EXCLUDED.is_active,
		expires_at = EXCLUDED.expires_at,
		scopes_json = EXCLUDED.scopes_json,
		allowed_tools_json = EXCLUDED.allowed_tools_json,
		allowed_workspace_roots_json = EXCLUDED.allowed_workspace_roots_json,
		max_agent_steps = EXCLUDED.max_agent_steps,
		max_concurrent_runs = EXCLUDED.max_concurrent_runs,
		max_tool_runtime_seconds = EXCLUDED.max_tool_runtime_seconds,
		require_approval = EXCLUDED.require_approval,
		allow_shell = EXCLUDED.allow_shell,
		enforce_sandbox = EXCLUDED.enforce_sandbox,
		auto_merge_allowed = EXCLUDED.auto_merge_allowed;
	`
	_, err = r.pool.Exec(ctx, query,
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
		createdAtVal,
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

func (r *PostgresKeyRepository) FindByKeyHash(ctx context.Context, keyHash string) (*domain.VirtualKey, error) {
	query := `SELECT ` + selectVirtualKeyCols + ` FROM virtual_keys WHERE key_hash = $1 LIMIT 1`
	row := r.pool.QueryRow(ctx, query, keyHash)
	return r.scanKey(row)
}

func (r *PostgresKeyRepository) FindByID(ctx context.Context, id string) (*domain.VirtualKey, error) {
	query := `SELECT ` + selectVirtualKeyCols + ` FROM virtual_keys WHERE id = $1 LIMIT 1`
	row := r.pool.QueryRow(ctx, query, id)
	return r.scanKey(row)
}

func (r *PostgresKeyRepository) ListActive(ctx context.Context) ([]*domain.VirtualKey, error) {
	query := `SELECT ` + selectVirtualKeyCols + ` FROM virtual_keys WHERE is_active = 1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*domain.VirtualKey
	for rows.Next() {
		k, err := r.scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *PostgresKeyRepository) Revoke(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	res, err := r.pool.Exec(ctx, "UPDATE virtual_keys SET is_active = 0 WHERE id = $1", id)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return domain.ErrInvalidAPIKey
	}
	return nil
}

type pgRowScanner interface {
	Scan(dest ...any) error
}

func (r *PostgresKeyRepository) scanKey(row pgRowScanner) (*domain.VirtualKey, error) {
	var (
		k                 domain.VirtualKey
		allowedJSON       *string
		lastUsed          *string
		isActiveInt       int
		expiresAt         *time.Time
		tenantID          *string
		scopesJSON        *string
		allowedToolsJSON  *string
		allowedRootsJSON  *string
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidAPIKey
		}
		return nil, err
	}

	if lastUsed != nil {
		k.LastUsedDate = *lastUsed
	}
	k.IsActive = (isActiveInt == 1)
	if expiresAt != nil {
		t := expiresAt.UTC()
		k.ExpiresAt = &t
	}

	if tenantID != nil && *tenantID != "" {
		k.TenantID = *tenantID
	} else {
		k.TenantID = "default_tenant"
	}

	k.RequireApproval = (reqApprovalInt == 1)
	k.AllowShell = (allowShellInt == 1)
	k.EnforceSandbox = (enforceSandboxInt == 1)
	k.AutoMergeAllowed = (autoMergeInt == 1)

	if allowedJSON != nil && *allowedJSON != "" {
		k.AllowedModelsJSON = *allowedJSON
		_ = json.Unmarshal([]byte(*allowedJSON), &k.AllowedModels)
	}
	if len(k.AllowedModels) == 0 {
		k.AllowedModels = []string{"*"}
	}

	if scopesJSON != nil && *scopesJSON != "" {
		k.ScopesJSON = *scopesJSON
		_ = json.Unmarshal([]byte(*scopesJSON), &k.Scopes)
	}

	if allowedToolsJSON != nil && *allowedToolsJSON != "" {
		k.AllowedToolsJSON = *allowedToolsJSON
		_ = json.Unmarshal([]byte(*allowedToolsJSON), &k.AllowedTools)
	}

	if allowedRootsJSON != nil && *allowedRootsJSON != "" {
		k.AllowedWorkspaceRootsJSON = *allowedRootsJSON
		_ = json.Unmarshal([]byte(*allowedRootsJSON), &k.AllowedWorkspaceRoots)
	}

	return &k, nil
}

func (r *PostgresKeyRepository) ConsumeDailyQuota(ctx context.Context, id string, date string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		dailyQuota int
		usedToday  int
		lastDate   *string
	)

	query := "SELECT daily_quota_requests, used_today, last_used_date FROM virtual_keys WHERE id = $1 FOR UPDATE"
	err = tx.QueryRow(ctx, query, id).Scan(&dailyQuota, &usedToday, &lastDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.ErrInvalidAPIKey
		}
		return 0, err
	}

	lastDateStr := ""
	if lastDate != nil {
		lastDateStr = *lastDate
	}

	if lastDateStr != date {
		usedToday = 0
	}

	if dailyQuota > 0 && usedToday >= dailyQuota {
		return 0, domain.ErrDailyQuotaExceeded
	}

	newUsedToday := usedToday + 1
	_, err = tx.Exec(ctx,
		"UPDATE virtual_keys SET used_today = $1, last_used_date = $2 WHERE id = $3",
		newUsedToday, date, id,
	)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
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

func (r *PostgresKeyRepository) RecordTokenUsage(ctx context.Context, id string, promptTokens, completionTokens int) error {
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	total := int64(promptTokens + completionTokens)
	today := time.Now().UTC().Format("2006-01-02")

	if id != "master" {
		queryKey := `
		UPDATE virtual_keys
		SET prompt_tokens_total = prompt_tokens_total + $1,
		    completion_tokens_total = completion_tokens_total + $2,
		    total_tokens = total_tokens + $3
		WHERE id = $4
		`
		_, _ = r.pool.Exec(ctx, queryKey, promptTokens, completionTokens, total, id)
	}

	queryUsage := `
	INSERT INTO virtual_key_token_usages (key_id, date, prompt_tokens, completion_tokens, total_tokens, request_count)
	VALUES ($1, $2, $3, $4, $5, 1)
	ON CONFLICT (key_id, date) DO UPDATE SET
		prompt_tokens = virtual_key_token_usages.prompt_tokens + EXCLUDED.prompt_tokens,
		completion_tokens = virtual_key_token_usages.completion_tokens + EXCLUDED.completion_tokens,
		total_tokens = virtual_key_token_usages.total_tokens + EXCLUDED.total_tokens,
		request_count = virtual_key_token_usages.request_count + 1
	`
	_, err := r.pool.Exec(ctx, queryUsage, id, today, promptTokens, completionTokens, total)
	return err
}

func (r *PostgresKeyRepository) GetTokenUsageHistory(ctx context.Context, keyID string, days int) ([]domain.KeyTokenUsage, error) {
	if days <= 0 {
		days = 30
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `
	SELECT key_id, date, prompt_tokens, completion_tokens, total_tokens, request_count
	FROM virtual_key_token_usages
	WHERE key_id = $1
	ORDER BY date DESC
	LIMIT $2
	`
	rows, err := r.pool.Query(ctx, query, keyID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []domain.KeyTokenUsage
	for rows.Next() {
		var u domain.KeyTokenUsage
		if err := rows.Scan(&u.KeyID, &u.Date, &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens, &u.RequestCount); err != nil {
			return nil, err
		}
		history = append(history, u)
	}
	return history, rows.Err()
}

func (r *PostgresKeyRepository) GetSystemTokenUsageHistory(ctx context.Context, days int) ([]domain.KeyTokenUsage, error) {
	if days <= 0 {
		days = 30
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `
	SELECT 'system' as key_id, date, SUM(prompt_tokens) as prompt_tokens, SUM(completion_tokens) as completion_tokens, SUM(total_tokens) as total_tokens, SUM(request_count) as request_count
	FROM virtual_key_token_usages
	GROUP BY date
	ORDER BY date DESC
	LIMIT $1
	`
	rows, err := r.pool.Query(ctx, query, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []domain.KeyTokenUsage
	for rows.Next() {
		var u domain.KeyTokenUsage
		if err := rows.Scan(&u.KeyID, &u.Date, &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens, &u.RequestCount); err != nil {
			return nil, err
		}
		history = append(history, u)
	}
	return history, rows.Err()
}
