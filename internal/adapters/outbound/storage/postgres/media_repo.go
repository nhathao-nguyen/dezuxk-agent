package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/core/domain"
)

// PostgresMediaMetadataRepository quản lý siêu dữ liệu Media Assets bền vững trên PostgreSQL
type PostgresMediaMetadataRepository struct {
	mu   sync.Mutex
	pool *pgxpool.Pool
}

func NewPostgresMediaMetadataRepository(pool *pgxpool.Pool) (*PostgresMediaMetadataRepository, error) {
	if pool == nil {
		return nil, errors.New("pgxpool không được để nil")
	}
	return &PostgresMediaMetadataRepository{pool: pool}, nil
}

func (r *PostgresMediaMetadataRepository) SaveAssetMetadata(ctx context.Context, asset *domain.MediaAsset, objectKey string) error {
	if asset == nil || asset.ID == "" {
		return errors.New("asset không hợp lệ hoặc thiếu ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID := asset.TenantID
	if tenantID == "" {
		tenantID = "default"
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now()
	}

	query := `
	INSERT INTO media_assets (
		asset_id, tenant_id, object_key, file_name, kind, mime_type,
		size_bytes, is_public, original_url, prompt, model, created_at, expires_at
	) VALUES (
		$1, $2, $3, $4, $5, $6,
		$7, $8, $9, $10, $11, $12, $13
	)
	ON CONFLICT (asset_id) DO UPDATE SET
		object_key = EXCLUDED.object_key,
		size_bytes = EXCLUDED.size_bytes,
		is_public = EXCLUDED.is_public,
		expires_at = EXCLUDED.expires_at;
	`
	_, err := r.pool.Exec(ctx, query,
		asset.ID, tenantID, objectKey, asset.FileName, string(asset.Kind), string(asset.Kind),
		asset.SizeBytes, asset.IsPublic, asset.OriginalURL, asset.Prompt, asset.Model,
		asset.CreatedAt, asset.ExpiresAt,
	)
	return err
}

func (r *PostgresMediaMetadataRepository) GetAssetMetadata(ctx context.Context, assetID string) (*domain.MediaAsset, string, error) {
	query := `
	SELECT asset_id, tenant_id, object_key, file_name, kind, size_bytes,
	       is_public, COALESCE(original_url, ''), COALESCE(prompt, ''), COALESCE(model, ''),
	       created_at, expires_at
	FROM media_assets
	WHERE asset_id = $1
	LIMIT 1;
	`
	row := r.pool.QueryRow(ctx, query, assetID)

	var (
		a         domain.MediaAsset
		objectKey string
		kindStr   string
	)

	err := row.Scan(
		&a.ID, &a.TenantID, &objectKey, &a.FileName, &kindStr, &a.SizeBytes,
		&a.IsPublic, &a.OriginalURL, &a.Prompt, &a.Model,
		&a.CreatedAt, &a.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", fmt.Errorf("không tìm thấy asset metadata cho ID: %s", assetID)
		}
		return nil, "", err
	}
	a.Kind = domain.MediaKind(kindStr)
	a.IsReady = true
	return &a, objectKey, nil
}

func (r *PostgresMediaMetadataRepository) DeleteAssetMetadata(ctx context.Context, assetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.pool.Exec(ctx, "DELETE FROM media_assets WHERE asset_id = $1", assetID)
	return err
}

func (r *PostgresMediaMetadataRepository) ListAssetsMetadata(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	var query string
	var args []any

	if kind != "" {
		query = `SELECT asset_id, tenant_id, object_key, file_name, kind, size_bytes, is_public, COALESCE(original_url, ''), COALESCE(prompt, ''), COALESCE(model, ''), created_at, expires_at FROM media_assets WHERE kind = $1 ORDER BY created_at DESC;`
		args = []any{string(kind)}
	} else {
		query = `SELECT asset_id, tenant_id, object_key, file_name, kind, size_bytes, is_public, COALESCE(original_url, ''), COALESCE(prompt, ''), COALESCE(model, ''), created_at, expires_at FROM media_assets ORDER BY created_at DESC;`
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.MediaAsset
	for rows.Next() {
		var a domain.MediaAsset
		var objectKey, kindStr string
		if err := rows.Scan(
			&a.ID, &a.TenantID, &objectKey, &a.FileName, &kindStr, &a.SizeBytes,
			&a.IsPublic, &a.OriginalURL, &a.Prompt, &a.Model,
			&a.CreatedAt, &a.ExpiresAt,
		); err != nil {
			return nil, err
		}
		a.Kind = domain.MediaKind(kindStr)
		a.IsReady = true
		list = append(list, &a)
	}
	return list, rows.Err()
}

func (r *PostgresMediaMetadataRepository) DeleteExpiredMetadata(ctx context.Context, maxAgeDays int) ([]string, error) {
	if maxAgeDays <= 0 {
		return nil, nil
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)

	r.mu.Lock()
	defer r.mu.Unlock()

	query := `
	DELETE FROM media_assets
	WHERE created_at < $1 OR (expires_at IS NOT NULL AND expires_at < NOW())
	RETURNING object_key;
	`
	rows, err := r.pool.Query(ctx, query, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	return keys, rows.Err()
}
