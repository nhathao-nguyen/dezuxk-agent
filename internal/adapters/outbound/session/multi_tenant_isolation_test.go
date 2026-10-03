package session_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/core/domain"

	_ "dezuxk-gateway/internal/pkg/sqlite"
)

func TestMultiTenantMemoryIsolation(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	defer db.Close()

	repo, err := session.NewSqliteMemoryRepository(db)
	if err != nil {
		t.Fatalf("failed to init sqlite memory repo: %v", err)
	}

	ctx := context.Background()

	// Tenant A lưu một memory
	memA := &domain.ArchivalMemoryItem{
		Key:       "secret_alpha",
		TenantID:  "tenant-alpha",
		ProjectID: "proj-1",
		AgentID:   "agent-1",
		Content:   "Bí mật tối cao của Tenant Alpha",
		CreatedAt: time.Now(),
	}
	if err := repo.Store(ctx, memA); err != nil {
		t.Fatalf("Tenant A failed to store memory: %v", err)
	}

	// Tenant B lưu một memory
	memB := &domain.ArchivalMemoryItem{
		Key:       "secret_beta",
		TenantID:  "tenant-beta",
		ProjectID: "proj-1",
		AgentID:   "agent-1",
		Content:   "Dữ liệu kinh doanh của Tenant Beta",
		CreatedAt: time.Now(),
	}
	if err := repo.Store(ctx, memB); err != nil {
		t.Fatalf("Tenant B failed to store memory: %v", err)
	}

	// Context Tenant A
	ctxA := domain.ContextWithMemoryNamespace(ctx, domain.MemoryNamespace{
		TenantID:  "tenant-alpha",
		ProjectID: "proj-1",
		AgentID:   "agent-1",
	})

	// Context Tenant B
	ctxB := domain.ContextWithMemoryNamespace(ctx, domain.MemoryNamespace{
		TenantID:  "tenant-beta",
		ProjectID: "proj-1",
		AgentID:   "agent-1",
	})

	// 1. Tenant A tìm kiếm không được thấy dữ liệu của Tenant B
	resultsA, err := repo.SearchFTS(ctxA, "Beta", 10)
	if err != nil {
		t.Fatalf("Tenant A SearchFTS error: %v", err)
	}
	if len(resultsA) != 0 {
		t.Errorf("LỖI CÁCH LY: Tenant A đọc được dữ liệu của Tenant B: %+v", resultsA)
	}

	// 2. Tenant B tìm kiếm thấy dữ liệu của Tenant B
	resultsB, err := repo.SearchFTS(ctxB, "Beta", 10)
	if err != nil {
		t.Fatalf("Tenant B SearchFTS error: %v", err)
	}
	if len(resultsB) != 1 {
		t.Errorf("Tenant B phải tìm thấy 1 kết quả, nhận được %d", len(resultsB))
	}

	// 3. Tenant A không được phép đọc trực tiếp Key của Tenant B
	fetchedByA, err := repo.Get(ctxA, "secret_beta")
	if err == nil && fetchedByA != nil {
		t.Errorf("LỖI CÁCH LY: Tenant A Get được memory item của Tenant B: %+v", fetchedByA)
	}

	// 4. Tenant B đọc đúng item của mình
	fetchedByB, err := repo.Get(ctxB, "secret_beta")
	if err != nil || fetchedByB == nil {
		t.Errorf("Tenant B không đọc được item của chính mình: %v", err)
	}

	// 5. Tenant A xóa Key của Tenant B phải bị từ chối
	err = repo.Delete(ctxA, "secret_beta")
	if err == nil {
		t.Errorf("LỖI BẢO MẬT: Tenant A xóa được memory của Tenant B!")
	}

	// 6. Kiểm tra ListAll isolation
	listA, _ := repo.ListAll(ctxA)
	for _, item := range listA {
		if item.TenantID != "tenant-alpha" {
			t.Errorf("ListAll của Tenant A chứa item của tenant khác: %s", item.TenantID)
		}
	}
	if len(listA) != 1 || listA[0].Key != "secret_alpha" {
		t.Errorf("ListAll của Tenant A không đúng số lượng: %d", len(listA))
	}
}

func TestMultiTenantCheckpointIsolation(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	defer db.Close()

	repo, err := session.NewSqliteCheckpointRepository(db)
	if err != nil {
		t.Fatalf("failed to init sqlite checkpoint repo: %v", err)
	}

	ctx := context.Background()

	// Tenant 1 lưu checkpoint
	cp1 := &domain.AgentCheckpoint{
		TenantID:  "tenant-one",
		TaskID:    "task-alpha",
		NodeKind:  domain.NodeKindPlan,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      "task-alpha",
			Goal:        "Plan task",
			IsCompleted: false,
		},
		CreatedAt: time.Now(),
	}
	if err := repo.SaveCheckpoint(ctx, cp1); err != nil {
		t.Fatalf("failed to save cp1: %v", err)
	}

	// Tenant 2 lưu checkpoint
	cp2 := &domain.AgentCheckpoint{
		TenantID:  "tenant-two",
		TaskID:    "task-beta",
		NodeKind:  domain.NodeKindExecute,
		StepIndex: 2,
		StateSnapshot: domain.AgentState{
			TaskID:      "task-beta",
			Goal:        "Execute task",
			IsCompleted: false,
		},
		CreatedAt: time.Now(),
	}
	if err := repo.SaveCheckpoint(ctx, cp2); err != nil {
		t.Fatalf("failed to save cp2: %v", err)
	}

	ctxTenantOne := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{TenantID: "tenant-one", Role: "user"})
	ctxTenantTwo := domain.ContextWithTenantIdentity(ctx, domain.TenantIdentity{TenantID: "tenant-two", Role: "user"})

	// Tenant 1 không được phép lấy checkpoint của task-beta (thuộc Tenant 2)
	cp, err := repo.GetLatestCheckpoint(ctxTenantOne, "task-beta")
	if err == nil && cp != nil {
		t.Errorf("LỖI CÁCH LY CHECKPOINT: Tenant 1 lấy được checkpoint của Tenant 2!")
	}

	// Tenant 2 lấy được checkpoint của mình
	cpValid, err := repo.GetLatestCheckpoint(ctxTenantTwo, "task-beta")
	if err != nil || cpValid == nil {
		t.Errorf("Tenant 2 không lấy được checkpoint của mình: %v", err)
	}

	// Tenant 1 ListCheckpoints cho task-beta (của Tenant 2) phải rỗng
	list, err := repo.ListCheckpoints(ctxTenantOne, "task-beta")
	if err != nil {
		t.Fatalf("ListCheckpoints error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("LỖI CÁCH LY: Tenant 1 thấy checkpoints của Tenant 2: %d", len(list))
	}
}
