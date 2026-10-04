-- 007_strict_model_and_catalog_state.sql: StrictModel for Tenant and Distributed Catalog Generation

ALTER TABLE tenant_runtime_settings
ADD COLUMN IF NOT EXISTS strict_model BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS runtime_catalog_state (
    service VARCHAR(64) PRIMARY KEY,
    generation BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO runtime_catalog_state (service, generation, updated_at)
VALUES ('gemini', 1, NOW()), ('global', 1, NOW())
ON CONFLICT (service) DO NOTHING;
