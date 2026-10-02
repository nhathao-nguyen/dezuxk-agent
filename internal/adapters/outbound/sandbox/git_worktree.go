package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sandbox đại diện cho một môi trường cô lập git worktree cho một tác vụ Agent
type Sandbox struct {
	TaskID            string `json:"task_id"`
	BranchName        string `json:"branch_name"`        // Ví dụ: agent/<task_id>
	WorktreePath      string `json:"worktree_path"`      // Thư mục gốc của worktree (.dezuxk/worktrees/<task_id>)
	WorktreeWorkspace string `json:"worktree_workspace"` // Thư mục làm việc tương ứng bên trong worktree
	RepoRoot          string `json:"repo_root"`          // Thư mục gốc của git repo chính
	OriginalWorkspace string `json:"original_workspace"` // Thư mục làm việc ban đầu
	IsGit             bool   `json:"is_git"`
	IsActive          bool   `json:"is_active"`
	CreatedAt         time.Time `json:"created_at"`
}

// WorktreeManager quản lý vòng đời tạo, merge và rollback các worktree sandbox
type WorktreeManager struct {
	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
}

var (
	defaultManager *WorktreeManager
	once           sync.Once
)

// GetDefaultManager trả về instance WorktreeManager dùng chung
func GetDefaultManager() *WorktreeManager {
	once.Do(func() {
		defaultManager = NewWorktreeManager()
	})
	return defaultManager
}

// NewWorktreeManager khởi tạo một WorktreeManager mới
func NewWorktreeManager() *WorktreeManager {
	return &WorktreeManager{
		sandboxes: make(map[string]*Sandbox),
	}
}

// CreateSandbox tạo một nhánh git tạm và worktree tương ứng: git worktree add .dezuxk/worktrees/<task_id> -b agent/<task_id>
func (m *WorktreeManager) CreateSandbox(ctx context.Context, taskID, workspace string) (*Sandbox, error) {
	if workspace == "" {
		workspace = "."
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		absWS = workspace
	}

	sb := &Sandbox{
		TaskID:            taskID,
		BranchName:        "agent/" + taskID,
		OriginalWorkspace: absWS,
		CreatedAt:         time.Now(),
	}

	// 1. Kiểm tra xem workspace có nằm trong một Git Repository không
	repoRoot, isGit := findGitRoot(ctx, absWS)
	if !isGit {
		sb.IsGit = false
		sb.WorktreePath = absWS
		sb.WorktreeWorkspace = absWS
		sb.IsActive = true
		m.register(sb)
		return sb, nil
	}

	sb.IsGit = true
	sb.RepoRoot = repoRoot

	// Xác định thư mục worktree: <repoRoot>/.dezuxk/worktrees/<taskID>
	worktreeRoot := filepath.Join(repoRoot, ".dezuxk", "worktrees", taskID)
	sb.WorktreePath = worktreeRoot

	// Tính toán đường dẫn tương đối từ repoRoot tới workspace ban đầu
	relPath, err := filepath.Rel(repoRoot, absWS)
	if err != nil || strings.HasPrefix(relPath, "..") {
		sb.WorktreeWorkspace = worktreeRoot
	} else {
		sb.WorktreeWorkspace = filepath.Join(worktreeRoot, relPath)
	}

	// Đảm bảo thư mục cha .dezuxk/worktrees tồn tại
	_ = os.MkdirAll(filepath.Dir(worktreeRoot), 0755)

	// Dọn dẹp tàn dư cũ nếu có cùng task_id
	_, _ = runGit(ctx, repoRoot, "worktree", "remove", "--force", worktreeRoot)
	_, _ = runGit(ctx, repoRoot, "branch", "-D", sb.BranchName)
	_, _ = runGit(ctx, repoRoot, "worktree", "prune")

	// Tạo nhánh tạm và thêm worktree
	out, err := runGit(ctx, repoRoot, "worktree", "add", worktreeRoot, "-b", sb.BranchName)
	if err != nil {
		return nil, fmt.Errorf("không thể tạo git worktree sandbox: %w\nOutput: %s", err, out)
	}

	// Đảm bảo thư mục workspace bên trong worktree tồn tại
	_ = os.MkdirAll(sb.WorktreeWorkspace, 0755)

	sb.IsActive = true
	m.register(sb)
	return sb, nil
}

// GetDiff lấy toàn bộ sự thay đổi (diff) so với nhánh chính trong worktree
func (m *WorktreeManager) GetDiff(ctx context.Context, sb *Sandbox) (string, error) {
	if sb == nil || !sb.IsGit || !sb.IsActive {
		return "[Không có sandbox git nào đang hoạt động]", nil
	}

	// Đánh dấu cả các file mới tạo (untracked) vào staging intent để git diff nhận diện
	_, _ = runGit(ctx, sb.WorktreePath, "add", "-N", ".")

	statusOut, _ := runGit(ctx, sb.WorktreePath, "status", "--short")
	diffOut, err := runGit(ctx, sb.WorktreePath, "diff", "HEAD")
	if err != nil {
		// Thử diff không có HEAD nếu là commit đầu tiên
		diffOut, _ = runGit(ctx, sb.WorktreePath, "diff")
	}

	var sbMsg strings.Builder
	sbMsg.WriteString(fmt.Sprintf("=== GIT DIFF SANDBOX (Nhánh: %s | Worktree: %s) ===\n", sb.BranchName, sb.WorktreePath))
	if strings.TrimSpace(statusOut) != "" {
		sbMsg.WriteString("--- DANH SÁCH TỆP TIN THAY ĐỔI ---\n")
		sbMsg.WriteString(strings.TrimSpace(statusOut))
		sbMsg.WriteString("\n\n")
	}
	if strings.TrimSpace(diffOut) != "" {
		sbMsg.WriteString("--- CHI TIẾT NỘI DUNG THAY ĐỔI (DIFF) ---\n")
		sbMsg.WriteString(strings.TrimSpace(diffOut))
		sbMsg.WriteString("\n")
	} else {
		sbMsg.WriteString("[Không có sự thay đổi nào đối với mã nguồn trong sandbox]\n")
	}

	return sbMsg.String(), nil
}

// ApplyMerge gộp các thay đổi từ sandbox vào nhánh chính và dọn dẹp worktree
func (m *WorktreeManager) ApplyMerge(ctx context.Context, sb *Sandbox) error {
	if sb == nil || !sb.IsGit || !sb.IsActive {
		return nil
	}

	// 1. Commit toàn bộ thay đổi bên trong worktree
	_, _ = runGit(ctx, sb.WorktreePath, "add", "-A")
	commitMsg := fmt.Sprintf("feat(agent): hoàn thành tác vụ %s", sb.TaskID)
	_, _ = runGit(ctx, sb.WorktreePath, "commit", "-m", commitMsg)

	// 2. Trở lại repo chính và merge nhánh agent
	mergeMsg := fmt.Sprintf("Merge sandbox branch '%s' for task %s", sb.BranchName, sb.TaskID)
	out, err := runGit(ctx, sb.RepoRoot, "merge", "--no-ff", sb.BranchName, "-m", mergeMsg)
	if err != nil {
		return fmt.Errorf("lỗi khi merge nhánh %s vào nhánh chính: %w\nOutput: %s", sb.BranchName, err, out)
	}

	// 3. Dọn dẹp worktree và xóa nhánh tạm
	_ = m.cleanupWorktree(ctx, sb)
	sb.IsActive = false
	m.unregister(sb.TaskID)
	return nil
}

// Rollback xóa bỏ worktree và nhánh tạm mà không ảnh hưởng bất kỳ dòng code nào của dự án thật
func (m *WorktreeManager) Rollback(ctx context.Context, sb *Sandbox) error {
	if sb == nil || !sb.IsGit || !sb.IsActive {
		return nil
	}

	err := m.cleanupWorktree(ctx, sb)
	sb.IsActive = false
	m.unregister(sb.TaskID)
	return err
}

func (m *WorktreeManager) cleanupWorktree(ctx context.Context, sb *Sandbox) error {
	var errs []string

	// Xóa worktree
	if out, err := runGit(ctx, sb.RepoRoot, "worktree", "remove", "--force", sb.WorktreePath); err != nil {
		errs = append(errs, fmt.Sprintf("worktree remove: %v (out: %s)", err, out))
		// Fallback xóa tay thư mục nếu git worktree remove gặp lỗi trên Windows
		_ = os.RemoveAll(sb.WorktreePath)
	}

	// Xóa nhánh tạm
	if out, err := runGit(ctx, sb.RepoRoot, "branch", "-D", sb.BranchName); err != nil {
		errs = append(errs, fmt.Sprintf("branch delete: %v (out: %s)", err, out))
	}

	// Prune worktree metadata
	_, _ = runGit(ctx, sb.RepoRoot, "worktree", "prune")

	if len(errs) > 0 {
		return fmt.Errorf("lỗi khi dọn dẹp worktree: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (m *WorktreeManager) register(sb *Sandbox) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxes[sb.TaskID] = sb
}

func (m *WorktreeManager) unregister(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sandboxes, taskID)
}

// GetSandbox tìm sandbox theo taskID
func (m *WorktreeManager) GetSandbox(taskID string) *Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sandboxes[taskID]
}

func findGitRoot(ctx context.Context, dir string) (string, bool) {
	out, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", false
	}
	return filepath.Clean(root), true
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	return strings.TrimSpace(output), err
}
