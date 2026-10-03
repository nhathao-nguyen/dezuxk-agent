-- 002_agent_runs.sql: Agent runs, events, checkpoints, memory
CREATE TABLE IF NOT EXISTS agent_runs (
    id VARCHAR(128) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default',
    idempotency_key VARCHAR(255),
    goal TEXT NOT NULL,
    status VARCHAR(64) NOT NULL,
    model VARCHAR(128) NOT NULL,
    workspace TEXT NOT NULL,
    current_step INTEGER NOT NULL DEFAULT 0,
    max_steps INTEGER NOT NULL DEFAULT 25,
    total_tool_calls INTEGER NOT NULL DEFAULT 0,
    stop_reason VARCHAR(64),
    final_answer TEXT,
    error TEXT,
    git_diff TEXT,
    parent_run_id VARCHAR(128),
    resume_from_run_id VARCHAR(128),
    worker_id VARCHAR(128),
    claim_generation BIGINT NOT NULL DEFAULT 0,
    billing_key_id VARCHAR(128),
    lease_until TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    security_context TEXT,
    execution_config TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_status_lease ON agent_runs (status, lease_until);
CREATE INDEX IF NOT EXISTS idx_agent_runs_tenant_created ON agent_runs (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_runs_worker_fencing ON agent_runs (worker_id, claim_generation);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_tenant_idempotency 
    ON agent_runs (tenant_id, idempotency_key) 
    WHERE idempotency_key IS NOT NULL AND idempotency_key != '';

CREATE TABLE IF NOT EXISTS agent_run_events (
    id BIGSERIAL PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default',
    run_id VARCHAR(128) NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step INTEGER NOT NULL DEFAULT 0,
    kind VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_run_events_run_id ON agent_run_events (run_id, id ASC);
CREATE INDEX IF NOT EXISTS idx_agent_run_events_tenant_run ON agent_run_events (tenant_id, run_id, id ASC);

CREATE TABLE IF NOT EXISTS agent_checkpoints (
    id BIGSERIAL PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default',
    task_id VARCHAR(128) NOT NULL,
    node_kind VARCHAR(64) NOT NULL DEFAULT '',
    step_index INTEGER NOT NULL DEFAULT 0,
    state_snapshot_json TEXT NOT NULL,
    plan_snapshot TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_checkpoints_task_step ON agent_checkpoints (tenant_id, task_id, step_index DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS archival_memory (
    id BIGSERIAL PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default',
    key VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    search_vector tsvector,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_archival_memory_tenant_key UNIQUE (tenant_id, key)
);

CREATE INDEX IF NOT EXISTS idx_archival_memory_search ON archival_memory USING gin(to_tsvector('english', key || ' ' || tags || ' ' || content));
