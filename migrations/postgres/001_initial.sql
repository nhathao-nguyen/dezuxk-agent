-- 001_initial.sql: Sessions, alerts, and virtual keys
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(128) PRIMARY KEY,
    email VARCHAR(255),
    cookies_json TEXT,
    gemini_sn_token TEXT,
    user_agent TEXT,
    proxy TEXT,
    credits_balance INTEGER DEFAULT 0,
    tier INTEGER DEFAULT 1,
    is_healthy INTEGER DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    success_count BIGINT DEFAULT 0,
    failure_count BIGINT DEFAULT 0,
    consecutive_failures INTEGER DEFAULT 0,
    health_score DOUBLE PRECISION DEFAULT 1.0,
    count_429 BIGINT DEFAULT 0,
    count_403 BIGINT DEFAULT 0,
    avg_latency_ms DOUBLE PRECISION DEFAULT 0.0,
    cooldown_until TIMESTAMPTZ,
    health_status VARCHAR(64) DEFAULT 'healthy'
);

CREATE INDEX IF NOT EXISTS idx_sessions_health ON sessions (is_healthy, health_status);

CREATE TABLE IF NOT EXISTS session_alerts (
    id BIGSERIAL PRIMARY KEY,
    account_id VARCHAR(128) NOT NULL,
    service VARCHAR(64) NOT NULL,
    reason TEXT NOT NULL,
    status_code INTEGER,
    action_required TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_session_alerts_account ON session_alerts (account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS virtual_keys (
    id VARCHAR(128) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default_tenant',
    key_hash VARCHAR(128) UNIQUE NOT NULL,
    key_prefix VARCHAR(32) NOT NULL,
    name VARCHAR(255) NOT NULL,
    role VARCHAR(64) NOT NULL DEFAULT 'user',
    rate_limit_rpm INTEGER DEFAULT 60,
    daily_quota_requests INTEGER DEFAULT 1000,
    used_today INTEGER DEFAULT 0,
    last_used_date VARCHAR(32) DEFAULT '',
    allowed_models_json TEXT DEFAULT '["*"]',
    is_active INTEGER DEFAULT 1,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    prompt_tokens_total BIGINT DEFAULT 0,
    completion_tokens_total BIGINT DEFAULT 0,
    total_tokens BIGINT DEFAULT 0,
    max_token_quota BIGINT DEFAULT 0,
    scopes_json TEXT DEFAULT '[]',
    allowed_tools_json TEXT DEFAULT '[]',
    allowed_workspace_roots_json TEXT DEFAULT '[]',
    max_agent_steps INTEGER DEFAULT 25,
    max_concurrent_runs INTEGER DEFAULT 3,
    max_tool_runtime_seconds INTEGER DEFAULT 60,
    require_approval INTEGER DEFAULT 1,
    allow_shell INTEGER DEFAULT 0,
    enforce_sandbox INTEGER DEFAULT 1,
    auto_merge_allowed INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_virtual_keys_hash ON virtual_keys (key_hash);
CREATE INDEX IF NOT EXISTS idx_virtual_keys_active ON virtual_keys (is_active);
CREATE INDEX IF NOT EXISTS idx_virtual_keys_tenant ON virtual_keys (tenant_id);

CREATE TABLE IF NOT EXISTS virtual_key_token_usages (
    id BIGSERIAL PRIMARY KEY,
    key_id VARCHAR(128) NOT NULL,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default_tenant',
    date VARCHAR(32) NOT NULL,
    prompt_tokens BIGINT DEFAULT 0,
    completion_tokens BIGINT DEFAULT 0,
    total_tokens BIGINT DEFAULT 0,
    request_count BIGINT DEFAULT 0,
    CONSTRAINT uq_virtual_key_token_usages UNIQUE (key_id, date)
);

CREATE INDEX IF NOT EXISTS idx_vkey_usage_key_date ON virtual_key_token_usages (key_id, date);
