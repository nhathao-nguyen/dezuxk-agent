-- 003_tool_ledger.sql: Durable tool execution ledger
CREATE TABLE IF NOT EXISTS agent_tool_executions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default',
    run_id VARCHAR(128) NOT NULL,
    tool_call_id VARCHAR(128) NOT NULL,
    tool_name VARCHAR(128) NOT NULL,
    args_hash VARCHAR(128),
    status VARCHAR(64) NOT NULL, -- planned, running, completed, failed, unknown_after_restart
    result_json TEXT,
    error TEXT,
    worker_id VARCHAR(128),
    claim_generation BIGINT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT uq_tool_exec UNIQUE (tenant_id, run_id, tool_call_id)
);

CREATE INDEX IF NOT EXISTS idx_tool_exec_lookup ON agent_tool_executions (tenant_id, run_id, tool_call_id);
