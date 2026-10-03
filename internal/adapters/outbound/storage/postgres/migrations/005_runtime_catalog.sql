-- 005_runtime_catalog.sql: Shared Dynamic Model Catalog and Account-Model Eligibility

CREATE TABLE IF NOT EXISTS runtime_models (
    id VARCHAR(128) PRIMARY KEY,
    service VARCHAR(64) NOT NULL,
    upstream_id TEXT,
    display_name TEXT NOT NULL,
    backend_id TEXT,
    mode_id TEXT,
    tier INTEGER NOT NULL DEFAULT 1,
    capabilities_json TEXT NOT NULL DEFAULT '["chat"]',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    source VARCHAR(64) NOT NULL DEFAULT 'discovery',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_runtime_models_service_avail ON runtime_models (service, is_available);
CREATE INDEX IF NOT EXISTS idx_runtime_models_updated ON runtime_models (updated_at);

CREATE TABLE IF NOT EXISTS account_models (
    account_id VARCHAR(128) NOT NULL,
    model_id VARCHAR(128) NOT NULL,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    is_eligible BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, model_id)
);

CREATE INDEX IF NOT EXISTS idx_account_models_model_eligible ON account_models (model_id, is_eligible);
CREATE INDEX IF NOT EXISTS idx_account_models_account ON account_models (account_id);
