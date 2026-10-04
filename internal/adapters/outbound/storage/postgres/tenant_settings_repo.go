package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type PostgresTenantRuntimeSettingsRepository struct {
	pool *pgxpool.Pool
}

var _ ports.TenantRuntimeSettingsRepository = (*PostgresTenantRuntimeSettingsRepository)(nil)

func NewPostgresTenantRuntimeSettingsRepository(pool *pgxpool.Pool) *PostgresTenantRuntimeSettingsRepository {
	return &PostgresTenantRuntimeSettingsRepository{pool: pool}
}

func (r *PostgresTenantRuntimeSettingsRepository) Get(ctx context.Context, tenantID string) (*domain.TenantRuntimeSettings, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("pgxpool không khả dụng")
	}
	if tenantID == "" {
		tenantID = "default_tenant"
	}

	query := `
	SELECT tenant_id, preferred_model, model_policy, persona, project_context,
	       allowed_capabilities_json, allowed_tools_json, settings_json, strict_model, updated_at
	FROM tenant_runtime_settings
	WHERE tenant_id = $1;
	`

	row := r.pool.QueryRow(ctx, query, tenantID)
	var s domain.TenantRuntimeSettings
	var capsJSON, toolsJSON, settingsJSON string
	if err := row.Scan(
		&s.TenantID, &s.PreferredModel, &s.ModelPolicy, &s.Persona, &s.ProjectContext,
		&capsJSON, &toolsJSON, &settingsJSON, &s.StrictModel, &s.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("lỗi truy vấn tenant_runtime_settings: %w", err)
	}

	if capsJSON != "" {
		_ = json.Unmarshal([]byte(capsJSON), &s.AllowedCapabilities)
	}
	if toolsJSON != "" {
		_ = json.Unmarshal([]byte(toolsJSON), &s.AllowedTools)
	}
	if settingsJSON != "" {
		_ = json.Unmarshal([]byte(settingsJSON), &s.Settings)
	}

	return &s, nil
}

func (r *PostgresTenantRuntimeSettingsRepository) Upsert(ctx context.Context, settings *domain.TenantRuntimeSettings) error {
	if r.pool == nil || settings == nil || settings.TenantID == "" {
		return fmt.Errorf("cấu hình tenant không hợp lệ")
	}

	capsBytes, _ := json.Marshal(settings.AllowedCapabilities)
	toolsBytes, _ := json.Marshal(settings.AllowedTools)
	settingsBytes, _ := json.Marshal(settings.Settings)

	query := `
	INSERT INTO tenant_runtime_settings (
		tenant_id, preferred_model, model_policy, persona, project_context,
		allowed_capabilities_json, allowed_tools_json, settings_json, strict_model, updated_at
	) VALUES (
		$1, $2, $3, $4, $5,
		$6, $7, $8, $9, NOW()
	) ON CONFLICT (tenant_id) DO UPDATE SET
		preferred_model = EXCLUDED.preferred_model,
		model_policy = EXCLUDED.model_policy,
		persona = EXCLUDED.persona,
		project_context = EXCLUDED.project_context,
		allowed_capabilities_json = EXCLUDED.allowed_capabilities_json,
		allowed_tools_json = EXCLUDED.allowed_tools_json,
		settings_json = EXCLUDED.settings_json,
		strict_model = EXCLUDED.strict_model,
		updated_at = NOW();
	`

	_, err := r.pool.Exec(ctx, query,
		settings.TenantID, settings.PreferredModel, settings.ModelPolicy, settings.Persona, settings.ProjectContext,
		string(capsBytes), string(toolsBytes), string(settingsBytes), settings.StrictModel,
	)
	return err
}

func (r *PostgresTenantRuntimeSettingsRepository) ListAll(ctx context.Context) ([]*domain.TenantRuntimeSettings, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("pgxpool không khả dụng")
	}

	query := `
	SELECT tenant_id, preferred_model, model_policy, persona, project_context,
	       allowed_capabilities_json, allowed_tools_json, settings_json, strict_model, updated_at
	FROM tenant_runtime_settings
	ORDER BY tenant_id ASC;
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*domain.TenantRuntimeSettings
	for rows.Next() {
		var s domain.TenantRuntimeSettings
		var capsJSON, toolsJSON, settingsJSON string
		if err := rows.Scan(
			&s.TenantID, &s.PreferredModel, &s.ModelPolicy, &s.Persona, &s.ProjectContext,
			&capsJSON, &toolsJSON, &settingsJSON, &s.StrictModel, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if capsJSON != "" {
			_ = json.Unmarshal([]byte(capsJSON), &s.AllowedCapabilities)
		}
		if toolsJSON != "" {
			_ = json.Unmarshal([]byte(toolsJSON), &s.AllowedTools)
		}
		if settingsJSON != "" {
			_ = json.Unmarshal([]byte(settingsJSON), &s.Settings)
		}
		result = append(result, &s)
	}

	return result, rows.Err()
}
