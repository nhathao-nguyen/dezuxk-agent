package session

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"dezuxk-gateway/internal/core/domain"

	_ "github.com/mattn/go-sqlite3"
)

func TestSqliteMemoryRepository_HybridSearch(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở sqlite :memory: %v", err)
	}
	defer db.Close()

	repo, err := NewSqliteMemoryRepository(db)
	if err != nil {
		t.Fatalf("Lỗi NewSqliteMemoryRepository: %v", err)
	}

	ctx := context.Background()

	// Lưu 3 mẩu kiến trúc vào Archival Memory
	item1 := &domain.ArchivalMemoryItem{
		Key:     "clean_arch_rules",
		Content: "Dự án Dezuxk phải tuân thủ Hexagonal Clean Architecture. Tuyệt đối không hardcode URL, RPC hay model ID trong core domain.",
		Tags:    []string{"architecture", "clean_code", "rules"},
	}
	item2 := &domain.ArchivalMemoryItem{
		Key:     "google_waf_cookies",
		Content: "Bypass Google WAF bằng cách xoay vòng __Secure-1PSIDTS và trích xuất SNlM0e qua Chrome CDP.",
		Tags:    []string{"security", "waf", "cookies"},
	}
	item3 := &domain.ArchivalMemoryItem{
		Key:     "sqlite_wal_mode",
		Content: "Kích hoạt WAL mode trên SQLite để hỗ trợ concurrency cao và checkpoint bền vững cho Agent.",
		Tags:    []string{"database", "sqlite", "wal"},
	}

	if err := repo.Store(ctx, item1); err != nil {
		t.Fatalf("Lỗi lưu item1: %v", err)
	}
	if err := repo.Store(ctx, item2); err != nil {
		t.Fatalf("Lỗi lưu item2: %v", err)
	}
	if err := repo.Store(ctx, item3); err != nil {
		t.Fatalf("Lỗi lưu item3: %v", err)
	}

	// 1. Kiểm tra Get
	fetched, err := repo.Get(ctx, "clean_arch_rules")
	if err != nil {
		t.Fatalf("Lỗi Get item1: %v", err)
	}
	if !strings.Contains(fetched.Content, "Hexagonal Clean Architecture") {
		t.Errorf("Nội dung fetched không khớp: %s", fetched.Content)
	}

	// 2. Kiểm tra FTS5 Search
	ftsResults, err := repo.SearchFTS(ctx, "Hexagonal", 5)
	if err != nil {
		t.Fatalf("Lỗi SearchFTS: %v", err)
	}
	if len(ftsResults) == 0 {
		t.Errorf("SearchFTS không tìm thấy kết quả cho 'Hexagonal'")
	} else if ftsResults[0].Item.Key != "clean_arch_rules" {
		t.Errorf("Kết quả top 1 FTS không phải clean_arch_rules: %s", ftsResults[0].Item.Key)
	}

	// 3. Kiểm tra Hybrid Search (FTS5 + Vector)
	hybridResults, err := repo.SearchHybrid(ctx, "SQLite WAL mode database concurrency", 5)
	if err != nil {
		t.Fatalf("Lỗi SearchHybrid: %v", err)
	}
	if len(hybridResults) == 0 {
		t.Fatalf("SearchHybrid không trả về kết quả")
	}
	if hybridResults[0].Item.Key != "sqlite_wal_mode" {
		t.Errorf("Kỳ vọng top 1 là 'sqlite_wal_mode', nhận được: %s", hybridResults[0].Item.Key)
	}
	if hybridResults[0].MatchType != "hybrid" && hybridResults[0].MatchType != "fts" && hybridResults[0].MatchType != "vector" {
		t.Errorf("MatchType không hợp lệ: %s", hybridResults[0].MatchType)
	}

	// 4. Kiểm tra Delete
	if err := repo.Delete(ctx, "google_waf_cookies"); err != nil {
		t.Fatalf("Lỗi Delete: %v", err)
	}
	_, err = repo.Get(ctx, "google_waf_cookies")
	if err == nil {
		t.Errorf("Kỳ vọng lỗi sau khi xóa item")
	}
}

func TestSemanticVectorAndCosineSimilarity(t *testing.T) {
	v1 := ComputeSemanticVector("Golang Hexagonal Clean Architecture")
	v2 := ComputeSemanticVector("Golang Clean Architecture Pattern")
	v3 := ComputeSemanticVector("Cooking delicious pizza with tomato sauce")

	sim12 := CosineSimilarity(v1, v2)
	sim13 := CosineSimilarity(v1, v3)

	if sim12 <= sim13 {
		t.Errorf("Kỳ vọng similarity(v1, v2) > similarity(v1, v3), nhận được: %f vs %f", sim12, sim13)
	}
	if sim12 < 0.3 {
		t.Errorf("Kỳ vọng sim12 > 0.3 cho các cụm từ tương đồng, nhận được: %f", sim12)
	}
}
