package sandbox_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/sandbox"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test Agent")
	run("config", "user.email", "agent@test.com")

	initialFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	run("add", ".")
	run("commit", "-m", "initial commit")

	return dir
}

func TestGitWorktreeSandbox_Lifecycle(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := sandbox.NewWorktreeManager()
	ctx := context.Background()
	taskID := "test_task_123"

	// 1. Tạo Sandbox
	sb, err := mgr.CreateSandbox(ctx, taskID, repoDir)
	if err != nil {
		t.Fatalf("CreateSandbox failed: %v", err)
	}
	if !sb.IsGit || !sb.IsActive {
		t.Fatalf("expected sandbox to be active git sandbox")
	}

	// Kiểm tra thư mục worktree tồn tại
	if _, err := os.Stat(sb.WorktreePath); os.IsNotExist(err) {
		t.Fatalf("worktree path %s does not exist", sb.WorktreePath)
	}

	// 2. Thực hiện sửa đổi và thêm file trong sandbox
	newFile := filepath.Join(sb.WorktreeWorkspace, "agent_code.go")
	if err := os.WriteFile(newFile, []byte("package main\nfunc Hello() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write file in sandbox: %v", err)
	}

	// Xác nhận file chưa xuất hiện ở repo gốc
	origNewFile := filepath.Join(repoDir, "agent_code.go")
	if _, err := os.Stat(origNewFile); err == nil {
		t.Fatalf("file should NOT exist in original repo before merge")
	}

	// 3. Kiểm tra GetDiff
	diff, err := mgr.GetDiff(ctx, sb)
	if err != nil {
		t.Fatalf("GetDiff failed: %v", err)
	}
	if !strings.Contains(diff, "agent_code.go") {
		t.Fatalf("expected diff to contain agent_code.go, got:\n%s", diff)
	}

	// 4. Test Rollback
	if err := mgr.Rollback(ctx, sb); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Sau khi rollback, file gốc vẫn sạch, worktree bị xóa
	if _, err := os.Stat(origNewFile); err == nil {
		t.Fatalf("file should NOT exist in original repo after rollback")
	}
	if _, err := os.Stat(sb.WorktreePath); err == nil {
		t.Fatalf("worktree path should be deleted after rollback")
	}
}

func TestGitWorktreeSandbox_Merge(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	mgr := sandbox.NewWorktreeManager()
	ctx := context.Background()
	taskID := "test_task_merge"

	// 1. Tạo Sandbox
	sb, err := mgr.CreateSandbox(ctx, taskID, repoDir)
	if err != nil {
		t.Fatalf("CreateSandbox failed: %v", err)
	}

	// 2. Thêm file mới trong sandbox
	newFile := filepath.Join(sb.WorktreeWorkspace, "feature.txt")
	if err := os.WriteFile(newFile, []byte("FEATURE CONTENT\n"), 0644); err != nil {
		t.Fatalf("failed to write file in sandbox: %v", err)
	}

	// 3. Apply Merge
	if err := mgr.ApplyMerge(ctx, sb); err != nil {
		t.Fatalf("ApplyMerge failed: %v", err)
	}

	// 4. File mới phải có mặt trong repo gốc sau khi merge thành công
	origMergedFile := filepath.Join(repoDir, "feature.txt")
	data, err := os.ReadFile(origMergedFile)
	if err != nil {
		t.Fatalf("file should exist in original repo after merge: %v", err)
	}
	if strings.TrimSpace(string(data)) != "FEATURE CONTENT" {
		t.Fatalf("expected content 'FEATURE CONTENT', got: %q", string(data))
	}
}
