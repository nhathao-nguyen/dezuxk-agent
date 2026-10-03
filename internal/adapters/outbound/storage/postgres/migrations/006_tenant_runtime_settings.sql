-- 006_tenant_runtime_settings.sql: Tenant Runtime Settings and Model Preferences

CREATE TABLE IF NOT EXISTS tenant_runtime_settings (
    tenant_id VARCHAR(128) PRIMARY KEY,
    preferred_model TEXT DEFAULT '',
    model_policy VARCHAR(64) DEFAULT 'balanced',
    persona TEXT DEFAULT '',
    project_context TEXT DEFAULT '',
    allowed_capabilities_json TEXT DEFAULT '[]',
    allowed_tools_json TEXT DEFAULT '[]',
    settings_json TEXT DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tenant_settings_updated ON tenant_runtime_settings (updated_at);
