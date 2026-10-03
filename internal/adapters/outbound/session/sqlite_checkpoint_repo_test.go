package session

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"

	_ "dezuxk-gateway/internal/pkg/sqlite"
)

func TestSqliteCheckpointRepository(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Không thể mở in-memory sqlite: %v", err)
	}
	defer db.Close()

	repo, err := NewSqliteCheckpointRepository(db)
	if err != nil {
		t.Fatalf("Lỗi NewSqliteCheckpointRepository: %v", err)
	}

	ctx := context.Background()
	taskID := "task_abc123"

	cp1 := &domain.AgentCheckpoint{
		TaskID:    taskID,
		NodeKind:  domain.NodeKindPlan,
		StepIndex: 1,
		StateSnapshot: domain.AgentState{
			TaskID:      taskID,
			Goal:        "Fix build error",
			IsCompleted: false,
		},
		PlanSnapshot: domain.TaskPlan{
			Goal: "Fix build error",
			Steps: []domain.PlanStep{
				{ID: 1, Title: "Inspect files", Status: domain.StepStatusPassed},
				{ID: 2, Title: "Run tests", VerificationCommand: "go test ./...", Status: domain.StepStatusPending},
			},
		},
		CreatedAt: time.Now(),
	}

	if err := repo.SaveCheckpoint(ctx, cp1); err != nil {
		t.Fatalf("Lỗi SaveCheckpoint 1: %v", err)
	}
	if cp1.ID <= 0 {
		t.Errorf("Kỳ vọng ID > 0, nhận được %d", cp1.ID)
	}

	// Lưu checkpoint thứ 2 (VERIFY)
	cp2 := &domain.AgentCheckpoint{
		TaskID:    taskID,
		NodeKind:  domain.NodeKindVerify,
		StepIndex: 2,
		StateSnapshot: domain.AgentState{
			TaskID:      taskID,
			Goal:        "Fix build error",
			IsCompleted: false,
		},
		PlanSnapshot: domain.TaskPlan{
			Goal: "Fix build error",
			Steps: []domain.PlanStep{
				{ID: 1, Title: "Inspect files", Status: domain.StepStatusPassed},
				{ID: 2, Title: "Run tests", VerificationCommand: "go test ./...", Status: domain.StepStatusPassed, Evidence: "PASS"},
			},
		},
		CreatedAt: time.Now(),
	}

	if err := repo.SaveCheckpoint(ctx, cp2); err != nil {
		t.Fatalf("Lỗi SaveCheckpoint 2: %v", err)
	}

	// 1. Kiểm tra GetLatestCheckpoint
	latest, err := repo.GetLatestCheckpoint(ctx, taskID)
	if err != nil {
		t.Fatalf("Lỗi GetLatestCheckpoint: %v", err)
	}
	if latest.NodeKind != domain.NodeKindVerify {
		t.Errorf("Kỳ vọng NodeKind 'VERIFY', nhận được: %s", latest.NodeKind)
	}
	if len(latest.PlanSnapshot.Steps) != 2 || latest.PlanSnapshot.Steps[1].Status != domain.StepStatusPassed {
		t.Errorf("PlanSnapshot không đúng dữ liệu mới nhất: %+v", latest.PlanSnapshot)
	}

	// 2. Kiểm tra ListCheckpoints
	list, err := repo.ListCheckpoints(ctx, taskID)
	if err != nil {
		t.Fatalf("Lỗi ListCheckpoints: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("Kỳ vọng 2 checkpoints, nhận được: %d", len(list))
	}

	// 3. Kiểm tra DeleteCheckpoints
	if err := repo.DeleteCheckpoints(ctx, taskID); err != nil {
		t.Fatalf("Lỗi DeleteCheckpoints: %v", err)
	}
	_, err = repo.GetLatestCheckpoint(ctx, taskID)
	if err == nil {
		t.Errorf("Kỳ vọng lỗi khi checkpoint đã bị xóa, nhưng không có lỗi")
	}
}
