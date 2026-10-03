package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

// PostgresMemoryRepository triển khai ports.MemoryRepository trên PostgreSQL với Full Text Search (tsvector) và Semantic Hashing
type PostgresMemoryRepository struct {
	mu   sync.Mutex
	pool *pgxpool.Pool
}

var _ ports.MemoryRepository = (*PostgresMemoryRepository)(nil)

// NewPostgresMemoryRepository khởi tạo PostgreSQL Memory Repository
func NewPostgresMemoryRepository(pool *pgxpool.Pool) (*PostgresMemoryRepository, error) {
	if pool == nil {
		return nil, errors.New("pgxpool không được là nil")
	}
	return &PostgresMemoryRepository{pool: pool}, nil
}

func (r *PostgresMemoryRepository) Store(ctx context.Context, item *domain.ArchivalMemoryItem) error {
	if item == nil || item.Key == "" {
		return errors.New("item không hợp lệ hoặc thiếu key")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID := item.TenantID
	if tenantID == "" {
		if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
			tenantID = id.TenantID
		} else {
			tenantID = "default"
		}
	}

	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	tagsStr := strings.Join(item.Tags, ",")

	query := `
	INSERT INTO archival_memory (tenant_id, key, content, tags, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (tenant_id, key) DO UPDATE SET
		content = EXCLUDED.content,
		tags = EXCLUDED.tags,
		updated_at = EXCLUDED.updated_at;
	`
	_, err := r.pool.Exec(ctx, query, tenantID, item.Key, item.Content, tagsStr, item.CreatedAt, item.UpdatedAt)
	return err
}

func (r *PostgresMemoryRepository) Get(ctx context.Context, key string) (*domain.ArchivalMemoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID := "default"
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
	}

	query := `
	SELECT key, content, tags, created_at, updated_at
	FROM archival_memory
	WHERE tenant_id = $1 AND key = $2
	LIMIT 1;
	`
	row := r.pool.QueryRow(ctx, query, tenantID, key)

	var item domain.ArchivalMemoryItem
	var tagsStr string
	err := row.Scan(&item.Key, &item.Content, &tagsStr, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("không tìm thấy memory item cho key: %s", key)
		}
		return nil, err
	}

	item.TenantID = tenantID
	if tagsStr != "" {
		item.Tags = strings.Split(tagsStr, ",")
	}
	item.Embedding = session.ComputeSemanticVector(item.Content + " " + item.Key)

	return &item, nil
}

func (r *PostgresMemoryRepository) SearchFTS(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	if topK <= 0 {
		topK = 5
	}
	cleanQuery := strings.TrimSpace(query)
	if cleanQuery == "" {
		return nil, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID := "default"
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
	}

	// Sử dụng PostgreSQL websearch_to_tsquery và ts_rank
	sqlQuery := `
	SELECT key, content, tags, created_at, updated_at,
	       ts_rank(to_tsvector('english', key || ' ' || tags || ' ' || content), websearch_to_tsquery('english', $2)) as rank
	FROM archival_memory
	WHERE tenant_id = $1 
	  AND to_tsvector('english', key || ' ' || tags || ' ' || content) @@ websearch_to_tsquery('english', $2)
	ORDER BY rank DESC
	LIMIT $3;
	`
	rows, err := r.pool.Query(ctx, sqlQuery, tenantID, cleanQuery, topK)
	if err != nil {
		// Fallback sang ILIKE nếu câu query có ký tự đặc biệt không parse được tsquery
		fallbackQuery := `
		SELECT key, content, tags, created_at, updated_at, 1.0 as rank
		FROM archival_memory
		WHERE tenant_id = $1 AND (content ILIKE $2 OR key ILIKE $2 OR tags ILIKE $2)
		LIMIT $3;
		`
		fallbackRows, fErr := r.pool.Query(ctx, fallbackQuery, tenantID, "%"+cleanQuery+"%", topK)
		if fErr != nil {
			return nil, err
		}
		defer fallbackRows.Close()
		rows = fallbackRows
	} else {
		defer rows.Close()
	}

	var results []domain.MemorySearchResult
	for rows.Next() {
		var item domain.ArchivalMemoryItem
		var tagsStr string
		var score float64

		if err := rows.Scan(&item.Key, &item.Content, &tagsStr, &item.CreatedAt, &item.UpdatedAt, &score); err != nil {
			return nil, err
		}
		item.TenantID = tenantID
		if tagsStr != "" {
			item.Tags = strings.Split(tagsStr, ",")
		}
		item.Embedding = session.ComputeSemanticVector(item.Content + " " + item.Key)

		results = append(results, domain.MemorySearchResult{
			Item:      item,
			Score:     float32(score),
			MatchType: "fts",
		})
	}

	return results, rows.Err()
}

func (r *PostgresMemoryRepository) SearchHybrid(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	if topK <= 0 {
		topK = 5
	}
	cleanQuery := strings.TrimSpace(query)
	if cleanQuery == "" {
		return nil, nil
	}

	// 1. Full-text search
	ftsResults, _ := r.SearchFTS(ctx, cleanQuery, topK*2)

	// 2. Vector semantic scoring
	all, err := r.ListAll(ctx)
	if err != nil {
		return ftsResults, nil
	}

	qVec := session.ComputeSemanticVector(cleanQuery)
	combined := make(map[string]*domain.MemorySearchResult)

	for _, res := range ftsResults {
		combined[res.Item.Key] = &domain.MemorySearchResult{
			Item:      res.Item,
			Score:     res.Score * 0.5,
			MatchType: "hybrid",
		}
	}

	for _, item := range all {
		itemVec := item.Embedding
		if len(itemVec) == 0 {
			itemVec = session.ComputeSemanticVector(item.Content + " " + item.Key)
		}
		vScore := session.CosineSimilarity(qVec, itemVec)
		if existing, ok := combined[item.Key]; ok {
			existing.Score += vScore * 0.5
		} else if vScore > 0.2 {
			combined[item.Key] = &domain.MemorySearchResult{
				Item:      item,
				Score:     vScore * 0.5,
				MatchType: "hybrid",
			}
		}
	}

	var list []domain.MemorySearchResult
	for _, res := range combined {
		list = append(list, *res)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Score > list[j].Score
	})

	if len(list) > topK {
		list = list[:topK]
	}
	return list, nil
}

func (r *PostgresMemoryRepository) Delete(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID := "default"
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
	}

	_, err := r.pool.Exec(ctx, "DELETE FROM archival_memory WHERE tenant_id = $1 AND key = $2", tenantID, key)
	return err
}

func (r *PostgresMemoryRepository) ListAll(ctx context.Context) ([]domain.ArchivalMemoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID := "default"
	if id, ok := domain.TenantIdentityFromContext(ctx); ok && id.TenantID != "" {
		tenantID = id.TenantID
	}

	query := `
	SELECT key, content, tags, created_at, updated_at
	FROM archival_memory
	WHERE tenant_id = $1
	ORDER BY created_at DESC;
	`
	rows, err := r.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.ArchivalMemoryItem
	for rows.Next() {
		var item domain.ArchivalMemoryItem
		var tagsStr string

		if err := rows.Scan(&item.Key, &item.Content, &tagsStr, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.TenantID = tenantID
		if tagsStr != "" {
			item.Tags = strings.Split(tagsStr, ",")
		}
		item.Embedding = session.ComputeSemanticVector(item.Content + " " + item.Key)
		list = append(list, item)
	}

	return list, rows.Err()
}
