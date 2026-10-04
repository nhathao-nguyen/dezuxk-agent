package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type PostgresModelCatalogRepository struct {
	pool *pgxpool.Pool
}

var _ ports.ModelCatalogRepository = (*PostgresModelCatalogRepository)(nil)

func NewPostgresModelCatalogRepository(pool *pgxpool.Pool) *PostgresModelCatalogRepository {
	return &PostgresModelCatalogRepository{pool: pool}
}

func (r *PostgresModelCatalogRepository) UpsertModels(ctx context.Context, models []domain.ModelDescriptor) error {
	if r.pool == nil || len(models) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("không thể mở transaction để upsert models: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := `
	INSERT INTO runtime_models (
		id, service, upstream_id, display_name, backend_id, mode_id,
		tier, capabilities_json, metadata_json, is_available, source,
		first_seen_at, last_seen_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6,
		$7, $8, $9, $10, $11,
		$12, $13, $14
	) ON CONFLICT (id) DO UPDATE SET
		service = EXCLUDED.service,
		upstream_id = EXCLUDED.upstream_id,
		display_name = EXCLUDED.display_name,
		backend_id = EXCLUDED.backend_id,
		mode_id = EXCLUDED.mode_id,
		tier = EXCLUDED.tier,
		capabilities_json = EXCLUDED.capabilities_json,
		metadata_json = EXCLUDED.metadata_json,
		is_available = EXCLUDED.is_available,
		source = EXCLUDED.source,
		last_seen_at = EXCLUDED.last_seen_at,
		updated_at = EXCLUDED.updated_at;
	`

	now := time.Now()
	for _, m := range models {
		capBytes, _ := json.Marshal(m.Capabilities)
		metaBytes, _ := json.Marshal(m.Metadata)
		firstSeen := m.FirstSeenAt
		if firstSeen.IsZero() {
			firstSeen = now
		}
		lastSeen := m.LastSeenAt
		if lastSeen.IsZero() {
			lastSeen = now
		}
		updatedAt := m.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}
		source := m.Source
		if source == "" {
			source = "discovery"
		}

		upstreamID := m.UpstreamModelID
		if upstreamID == "" {
			upstreamID = m.InternalBackendID
		}

		_, err := tx.Exec(ctx, query,
			m.ID, string(m.TargetService), upstreamID, m.DisplayName, m.InternalBackendID, m.ModeID,
			m.ModelTierCode, string(capBytes), string(metaBytes), m.IsActive, source,
			firstSeen, lastSeen, updatedAt,
		)
		if err != nil {
			return fmt.Errorf("lỗi upsert model %s: %w", m.ID, err)
		}
	}

	return tx.Commit(ctx)
}

func (r *PostgresModelCatalogRepository) ListModels(ctx context.Context, service domain.ServiceKind) ([]domain.ModelDescriptor, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("pgxpool không khả dụng")
	}

	var query string
	var args []any
	if service != "" {
		query = `
		SELECT id, service, upstream_id, display_name, backend_id, mode_id,
		       tier, capabilities_json, metadata_json, is_available, source,
		       first_seen_at, last_seen_at, updated_at
		FROM runtime_models
		WHERE service = $1 AND is_available = true
		ORDER BY tier ASC, id ASC;
		`
		args = append(args, string(service))
	} else {
		query = `
		SELECT id, service, upstream_id, display_name, backend_id, mode_id,
		       tier, capabilities_json, metadata_json, is_available, source,
		       first_seen_at, last_seen_at, updated_at
		FROM runtime_models
		WHERE is_available = true
		ORDER BY service ASC, tier ASC, id ASC;
		`
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lỗi truy vấn runtime_models: %w", err)
	}
	defer rows.Close()

	var result []domain.ModelDescriptor
	for rows.Next() {
		var m domain.ModelDescriptor
		var srv, capJSON, metaJSON, src string
		var upID, bkID, modeID *string
		if err := rows.Scan(
			&m.ID, &srv, &upID, &m.DisplayName, &bkID, &modeID,
			&m.ModelTierCode, &capJSON, &metaJSON, &m.IsActive, &src,
			&m.FirstSeenAt, &m.LastSeenAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("lỗi scan runtime_models: %w", err)
		}
		m.TargetService = domain.ServiceKind(srv)
		if upID != nil {
			m.UpstreamModelID = *upID
		}
		if bkID != nil {
			m.InternalBackendID = *bkID
		}
		if modeID != nil {
			m.ModeID = *modeID
		}
		m.Source = src
		if capJSON != "" {
			_ = json.Unmarshal([]byte(capJSON), &m.Capabilities)
		}
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &m.Metadata)
		}
		result = append(result, m)
	}

	return result, rows.Err()
}

func (r *PostgresModelCatalogRepository) MarkAvailability(ctx context.Context, modelID string, isAvailable bool) error {
	if r.pool == nil {
		return fmt.Errorf("pgxpool không khả dụng")
	}
	query := `UPDATE runtime_models SET is_available = $1, updated_at = NOW() WHERE id = $2;`
	_, err := r.pool.Exec(ctx, query, isAvailable, modelID)
	return err
}

func (r *PostgresModelCatalogRepository) UpsertAccountModels(ctx context.Context, accountID string, modelIDs []string, isEligible bool) error {
	if r.pool == nil || accountID == "" {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("không thể mở transaction account_models: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Transactional snapshot: đánh dấu toàn bộ model trước đây của account là unavailable
	if _, err := tx.Exec(ctx, `UPDATE account_models SET is_available = false WHERE account_id = $1`, accountID); err != nil {
		return fmt.Errorf("lỗi vô hiệu hóa snapshot cũ account_models: %w", err)
	}

	// 2. Kích hoạt và cập nhật các model quan sát thấy trong chu kỳ discovery hiện tại
	query := `
	INSERT INTO account_models (account_id, model_id, is_available, is_eligible, last_seen_at)
	VALUES ($1, $2, true, $3, NOW())
	ON CONFLICT (account_id, model_id) DO UPDATE SET
		is_available = true,
		is_eligible = EXCLUDED.is_eligible,
		last_seen_at = NOW();
	`

	for _, mID := range modelIDs {
		if mID == "" {
			continue
		}
		if _, err := tx.Exec(ctx, query, accountID, mID, isEligible); err != nil {
			return fmt.Errorf("lỗi upsert account_models (%s, %s): %w", accountID, mID, err)
		}
	}

	return tx.Commit(ctx)
}

func (r *PostgresModelCatalogRepository) ListAccountModels(ctx context.Context, accountID string) ([]domain.AccountModelEligibility, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("pgxpool không khả dụng")
	}

	query := `
	SELECT account_id, model_id, is_available, is_eligible, last_seen_at
	FROM account_models
	WHERE account_id = $1;
	`
	rows, err := r.pool.Query(ctx, query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.AccountModelEligibility
	for rows.Next() {
		var a domain.AccountModelEligibility
		if err := rows.Scan(&a.AccountID, &a.ModelID, &a.IsAvailable, &a.IsEligible, &a.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (r *PostgresModelCatalogRepository) ListEligibleAccountsForModel(ctx context.Context, modelID string) ([]string, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("pgxpool không khả dụng")
	}

	query := `
	SELECT account_id
	FROM account_models
	WHERE model_id = $1 AND is_eligible = true AND is_available = true;
	`
	rows, err := r.pool.Query(ctx, query, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		accounts = append(accounts, id)
	}
	return accounts, rows.Err()
}

func (r *PostgresModelCatalogRepository) MarkStaleModels(ctx context.Context, staleBefore time.Time) (int64, error) {
	if r.pool == nil {
		return 0, fmt.Errorf("pgxpool không khả dụng")
	}

	query := `
	UPDATE runtime_models
	SET is_available = false, updated_at = NOW()
	WHERE last_seen_at < $1 AND is_available = true;
	`
	tag, err := r.pool.Exec(ctx, query, staleBefore)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *PostgresModelCatalogRepository) GetCatalogGeneration(ctx context.Context, service domain.ServiceKind) (int64, error) {
	if r.pool == nil {
		return 0, fmt.Errorf("pgxpool không khả dụng")
	}
	srvKey := string(service)
	if srvKey == "" {
		srvKey = "global"
	}
	var gen int64
	query := `SELECT generation FROM runtime_catalog_state WHERE service = $1;`
	err := r.pool.QueryRow(ctx, query, srvKey).Scan(&gen)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 1, nil
		}
		return 0, err
	}
	return gen, nil
}

func (r *PostgresModelCatalogRepository) IncrementCatalogGeneration(ctx context.Context, service domain.ServiceKind) (int64, error) {
	if r.pool == nil {
		return 0, fmt.Errorf("pgxpool không khả dụng")
	}
	srvKey := string(service)
	if srvKey == "" {
		srvKey = "global"
	}
	var newGen int64
	query := `
	INSERT INTO runtime_catalog_state (service, generation, updated_at)
	VALUES ($1, 1, NOW())
	ON CONFLICT (service) DO UPDATE SET
		generation = runtime_catalog_state.generation + 1,
		updated_at = NOW()
	RETURNING generation;
	`
	err := r.pool.QueryRow(ctx, query, srvKey).Scan(&newGen)
	if err != nil {
		return 0, fmt.Errorf("lỗi tăng generation danh mục mô hình: %w", err)
	}
	return newGen, nil
}
