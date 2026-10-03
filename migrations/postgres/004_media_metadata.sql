-- 004_media_metadata.sql: Shared Media metadata in PostgreSQL
CREATE TABLE IF NOT EXISTS media_assets (
    asset_id VARCHAR(128) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL DEFAULT 'default',
    object_key VARCHAR(512) NOT NULL,
    file_name VARCHAR(255) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    original_url TEXT,
    prompt TEXT,
    model VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_media_assets_tenant ON media_assets (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_expires ON media_assets (expires_at) WHERE expires_at IS NOT NULL;
