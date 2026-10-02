package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

const vectorDimensions = 128

// ComputeSemanticVector tạo vector đặc trưng 128 chiều với chuẩn L2 (Deterministic Subword Feature Hashing)
func ComputeSemanticVector(text string) []float32 {
	vec := make([]float32, vectorDimensions)
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return vec
	}

	// 1. Phân tích token và n-gram
	words := strings.Fields(text)
	for _, w := range words {
		// Word hash
		h := fnv1a32(w) % uint32(vectorDimensions)
		vec[h] += 1.0

		// Subword 3-grams
		if len(w) >= 3 {
			for i := 0; i <= len(w)-3; i++ {
				ngram := w[i : i+3]
				nh := fnv1a32(ngram) % uint32(vectorDimensions)
				vec[nh] += 0.5
			}
		}
	}

	// 2. Chuẩn hóa L2 (L2 Normalization)
	var sumSq float32
	for _, val := range vec {
		sumSq += val * val
	}
	if sumSq > 0 {
		norm := float32(math.Sqrt(float64(sumSq)))
		for i := range vec {
			vec[i] /= norm
		}
	}

	return vec
}

func fnv1a32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// CosineSimilarity tính độ tương đồng cosin giữa 2 vector
func CosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// SqliteMemoryRepository cài đặt ports.MemoryRepository hỗ trợ Hybrid Search (FTS5 + Vector)
type SqliteMemoryRepository struct {
	mu      sync.Mutex
	db      *sql.DB
	hasFTS5 bool
}

// NewSqliteMemoryRepository khởi tạo kho bộ nhớ SQLite FTS5 (tự động fallback nếu FTS5 chưa được kích hoạt trong SQLite build)
func NewSqliteMemoryRepository(db *sql.DB) (*SqliteMemoryRepository, error) {
	if db == nil {
		return nil, errors.New("sql.DB không được là nil")
	}

	repo := &SqliteMemoryRepository{db: db}
	if err := repo.migrate(); err != nil {
		return nil, fmt.Errorf("không thể khởi tạo bảng agent_archival_memories: %w", err)
	}

	return repo, nil
}

var _ ports.MemoryRepository = (*SqliteMemoryRepository)(nil)

func (r *SqliteMemoryRepository) migrate() error {
	baseSchema := `
	CREATE TABLE IF NOT EXISTS agent_archival_memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key TEXT UNIQUE NOT NULL,
		content TEXT NOT NULL,
		tags_json TEXT NOT NULL DEFAULT '[]',
		embedding_json TEXT DEFAULT '[]',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_agent_memories_key ON agent_archival_memories(key);
	`
	if _, err := r.db.Exec(baseSchema); err != nil {
		return err
	}

	ftsSchema := `
	CREATE VIRTUAL TABLE IF NOT EXISTS agent_archival_fts USING fts5(
		key,
		content,
		tags,
		content='agent_archival_memories',
		content_rowid='id'
	);

	CREATE TRIGGER IF NOT EXISTS trg_agent_memories_ai AFTER INSERT ON agent_archival_memories BEGIN
		INSERT INTO agent_archival_fts(rowid, key, content, tags) 
		VALUES (new.id, new.key, new.content, new.tags_json);
	END;

	CREATE TRIGGER IF NOT EXISTS trg_agent_memories_ad AFTER DELETE ON agent_archival_memories BEGIN
		INSERT INTO agent_archival_fts(agent_archival_fts, rowid, key, content, tags) 
		VALUES('delete', old.id, old.key, old.content, old.tags_json);
	END;

	CREATE TRIGGER IF NOT EXISTS trg_agent_memories_au AFTER UPDATE ON agent_archival_memories BEGIN
		INSERT INTO agent_archival_fts(agent_archival_fts, rowid, key, content, tags) 
		VALUES('delete', old.id, old.key, old.content, old.tags_json);
		INSERT INTO agent_archival_fts(rowid, key, content, tags) 
		VALUES (new.id, new.key, new.content, new.tags_json);
	END;
	`
	if _, err := r.db.Exec(ftsSchema); err != nil {
		// FTS5 không khả dụng trong bản build go-sqlite3 hiện tại, tự động kích hoạt fallback
		r.hasFTS5 = false
	} else {
		r.hasFTS5 = true
	}
	return nil
}

func (r *SqliteMemoryRepository) Store(ctx context.Context, item *domain.ArchivalMemoryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	// Tự động tính vector nếu chưa có
	if len(item.Embedding) == 0 {
		embedText := item.Key + " " + item.Content + " " + strings.Join(item.Tags, " ")
		item.Embedding = ComputeSemanticVector(embedText)
	}

	tagsJSON, _ := json.Marshal(item.Tags)
	embedJSON, _ := json.Marshal(item.Embedding)

	query := `
	INSERT INTO agent_archival_memories (key, content, tags_json, embedding_json, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(key) DO UPDATE SET
		content = excluded.content,
		tags_json = excluded.tags_json,
		embedding_json = excluded.embedding_json,
		updated_at = excluded.updated_at;
	`

	res, err := r.db.ExecContext(ctx, query, item.Key, item.Content, string(tagsJSON), string(embedJSON), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("lỗi khi lưu archival memory: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil && id > 0 {
		item.ID = id
	}

	return nil
}

func (r *SqliteMemoryRepository) Get(ctx context.Context, key string) (*domain.ArchivalMemoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `
	SELECT id, key, content, tags_json, embedding_json, created_at, updated_at
	FROM agent_archival_memories
	WHERE key = ?;
	`

	var item domain.ArchivalMemoryItem
	var tagsJSON, embedJSON string

	row := r.db.QueryRowContext(ctx, query, key)
	if err := row.Scan(&item.ID, &item.Key, &item.Content, &tagsJSON, &embedJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("không tìm thấy memory key %q", key)
		}
		return nil, err
	}

	_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
	_ = json.Unmarshal([]byte(embedJSON), &item.Embedding)

	return &item, nil
}

func (r *SqliteMemoryRepository) ListAll(ctx context.Context) ([]domain.ArchivalMemoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `
	SELECT id, key, content, tags_json, embedding_json, created_at, updated_at
	FROM agent_archival_memories
	ORDER BY updated_at DESC;
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.ArchivalMemoryItem
	for rows.Next() {
		var item domain.ArchivalMemoryItem
		var tagsJSON, embedJSON string
		if err := rows.Scan(&item.ID, &item.Key, &item.Content, &tagsJSON, &embedJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
		_ = json.Unmarshal([]byte(embedJSON), &item.Embedding)
		list = append(list, item)
	}

	return list, nil
}

func (r *SqliteMemoryRepository) Delete(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	query := `DELETE FROM agent_archival_memories WHERE key = ?;`
	_, err := r.db.ExecContext(ctx, query, key)
	return err
}

func (r *SqliteMemoryRepository) SearchFTS(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if topK <= 0 {
		topK = 5
	}

	if !r.hasFTS5 {
		return r.searchKeywordFallback(ctx, query, topK)
	}

	// Chuẩn hóa truy vấn FTS5 loại bỏ ký tự đặc biệt
	ftsQuery := cleanFTSQuery(query)
	if ftsQuery == "" {
		return []domain.MemorySearchResult{}, nil
	}

	sqlQuery := `
	SELECT m.id, m.key, m.content, m.tags_json, m.embedding_json, m.created_at, m.updated_at, rank
	FROM agent_archival_fts f
	JOIN agent_archival_memories m ON f.rowid = m.id
	WHERE agent_archival_fts MATCH ?
	ORDER BY rank
	LIMIT ?;
	`

	rows, err := r.db.QueryContext(ctx, sqlQuery, ftsQuery, topK)
	if err != nil {
		return nil, fmt.Errorf("lỗi truy vấn FTS5: %w", err)
	}
	defer rows.Close()

	var results []domain.MemorySearchResult
	for rows.Next() {
		var item domain.ArchivalMemoryItem
		var tagsJSON, embedJSON string
		var rank float64

		if err := rows.Scan(&item.ID, &item.Key, &item.Content, &tagsJSON, &embedJSON, &item.CreatedAt, &item.UpdatedAt, &rank); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
		_ = json.Unmarshal([]byte(embedJSON), &item.Embedding)

		results = append(results, domain.MemorySearchResult{
			Item:      item,
			Score:     float32(-rank), // FTS5 rank càng âm càng tốt
			MatchType: "fts",
		})
	}

	return results, nil
}

func (r *SqliteMemoryRepository) searchKeywordFallback(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return []domain.MemorySearchResult{}, nil
	}

	likePattern := "%" + terms[0] + "%"
	sqlQuery := `
	SELECT id, key, content, tags_json, embedding_json, created_at, updated_at
	FROM agent_archival_memories
	WHERE lower(key) LIKE ? OR lower(content) LIKE ? OR lower(tags_json) LIKE ?
	LIMIT ?;
	`

	rows, err := r.db.QueryContext(ctx, sqlQuery, likePattern, likePattern, likePattern, topK)
	if err != nil {
		return nil, fmt.Errorf("lỗi truy vấn fallback: %w", err)
	}
	defer rows.Close()

	var results []domain.MemorySearchResult
	for rows.Next() {
		var item domain.ArchivalMemoryItem
		var tagsJSON, embedJSON string

		if err := rows.Scan(&item.ID, &item.Key, &item.Content, &tagsJSON, &embedJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
		_ = json.Unmarshal([]byte(embedJSON), &item.Embedding)

		results = append(results, domain.MemorySearchResult{
			Item:      item,
			Score:     1.0,
			MatchType: "fts",
		})
	}
	return results, nil
}

func (r *SqliteMemoryRepository) SearchHybrid(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	if topK <= 0 {
		topK = 5
	}

	// 1. Chạy FTS5 tìm kiếm từ khóa
	ftsResults, _ := r.SearchFTS(ctx, query, topK*2)

	// 2. Chạy Vector Cosine Similarity
	allItems, err := r.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	queryVec := ComputeSemanticVector(query)

	type scoredItem struct {
		item  domain.ArchivalMemoryItem
		score float32
	}
	var vecResults []scoredItem
	for _, item := range allItems {
		sim := CosineSimilarity(queryVec, item.Embedding)
		if sim > 0.05 { // Ngưỡng tương đồng tối thiểu
			vecResults = append(vecResults, scoredItem{item: item, score: sim})
		}
	}
	sort.Slice(vecResults, func(i, j int) bool {
		return vecResults[i].score > vecResults[j].score
	})

	// 3. Hợp nhất bằng Reciprocal Rank Fusion (RRF)
	// score = 1/(60 + r_fts) + 1/(60 + r_vec)
	rrfScores := make(map[string]float32)
	itemMap := make(map[string]domain.ArchivalMemoryItem)
	matchTypes := make(map[string]string)

	for rank, r := range ftsResults {
		key := r.Item.Key
		rrfScores[key] += 1.0 / float32(60+rank+1)
		itemMap[key] = r.Item
		matchTypes[key] = "fts"
	}

	for rank, v := range vecResults {
		key := v.item.Key
		rrfScores[key] += 1.0 / float32(60+rank+1)
		itemMap[key] = v.item
		if matchTypes[key] == "fts" {
			matchTypes[key] = "hybrid"
		} else {
			matchTypes[key] = "vector"
		}
	}

	var combined []domain.MemorySearchResult
	for key, score := range rrfScores {
		combined = append(combined, domain.MemorySearchResult{
			Item:      itemMap[key],
			Score:     score,
			MatchType: matchTypes[key],
		})
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	if len(combined) > topK {
		combined = combined[:topK]
	}

	return combined, nil
}

func cleanFTSQuery(query string) string {
	terms := strings.Fields(query)
	var validTerms []string
	for _, t := range terms {
		t = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return -1
		}, t)
		if len(t) > 0 {
			validTerms = append(validTerms, t+"*")
		}
	}
	return strings.Join(validTerms, " OR ")
}

// -------------------------------------------------------------
// In-Memory Fallback cho Testing
// -------------------------------------------------------------

type MemoryMemoryRepository struct {
	mu    sync.RWMutex
	items map[string]domain.ArchivalMemoryItem
	idSeq int64
}

func NewMemoryMemoryRepository() *MemoryMemoryRepository {
	return &MemoryMemoryRepository{
		items: make(map[string]domain.ArchivalMemoryItem),
	}
}

var _ ports.MemoryRepository = (*MemoryMemoryRepository)(nil)

func (m *MemoryMemoryRepository) Store(ctx context.Context, item *domain.ArchivalMemoryItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.idSeq++
	item.ID = m.idSeq
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	if len(item.Embedding) == 0 {
		embedText := item.Key + " " + item.Content + " " + strings.Join(item.Tags, " ")
		item.Embedding = ComputeSemanticVector(embedText)
	}

	m.items[item.Key] = *item
	return nil
}

func (m *MemoryMemoryRepository) Get(ctx context.Context, key string) (*domain.ArchivalMemoryItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, exists := m.items[key]
	if !exists {
		return nil, fmt.Errorf("không tìm thấy key %s", key)
	}
	return &item, nil
}

func (m *MemoryMemoryRepository) ListAll(ctx context.Context) ([]domain.ArchivalMemoryItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []domain.ArchivalMemoryItem
	for _, item := range m.items {
		list = append(list, item)
	}
	return list, nil
}

func (m *MemoryMemoryRepository) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.items, key)
	return nil
}

func (m *MemoryMemoryRepository) SearchFTS(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	return m.SearchHybrid(ctx, query, topK)
}

func (m *MemoryMemoryRepository) SearchHybrid(ctx context.Context, query string, topK int) ([]domain.MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	queryVec := ComputeSemanticVector(query)
	var list []domain.MemorySearchResult

	for _, item := range m.items {
		sim := CosineSimilarity(queryVec, item.Embedding)
		list = append(list, domain.MemorySearchResult{
			Item:      item,
			Score:     sim,
			MatchType: "vector",
		})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Score > list[j].Score
	})

	if len(list) > topK {
		list = list[:topK]
	}
	return list, nil
}
