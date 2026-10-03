package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/linter"
)

// resolvePath chuẩn hóa đường dẫn tương đối theo workspace và áp dụng Path Sandboxing để ngăn chặn Path Traversal
func resolvePath(ctx context.Context, defaultWS, userPath string) (string, error) {
	ws := domain.GetWorkspace(ctx, defaultWS)
	if ws == "" {
		ws = "."
	}

	absWS, err := filepath.Abs(ws)
	if err != nil {
		return "", fmt.Errorf("không thể xác định đường dẫn tuyệt đối của workspace %s: %w", ws, err)
	}
	absWS = filepath.Clean(absWS)

	// Đánh giá canonical symlinks của workspace nếu thư mục đã tồn tại
	canonicalWS := absWS
	if evalWS, err := filepath.EvalSymlinks(absWS); err == nil {
		canonicalWS = evalWS
	}

	trimmedUser := strings.TrimSpace(userPath)
	if trimmedUser == "" || trimmedUser == "." {
		return absWS, nil
	}

	// Chặn mọi nỗ lực traversal chứa chuỗi '..'
	cleanUser := filepath.Clean(trimmedUser)
	if strings.HasPrefix(cleanUser, "..") || strings.Contains(cleanUser, "/../") || strings.Contains(cleanUser, "\\..\\") {
		return "", fmt.Errorf("truy cập bị chặn bởi Path Sandboxing: phát hiện nỗ lực path traversal '..' trong đường dẫn %q", userPath)
	}

	var target string
	if filepath.IsAbs(trimmedUser) {
		target = filepath.Clean(trimmedUser)
	} else {
		target = filepath.Join(absWS, cleanUser)
		target = filepath.Clean(target)
	}

	// Trên Windows, đồng bộ ký tự ổ đĩa (C: vs c:)
	if runtime.GOOS == "windows" {
		volWS := filepath.VolumeName(absWS)
		volTarget := filepath.VolumeName(target)
		if strings.EqualFold(volWS, volTarget) && volWS != volTarget {
			target = volWS + target[len(volTarget):]
		}
	}

	// Kiểm tra Path Sandboxing cơ bản
	rel, err := filepath.Rel(absWS, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("truy cập bị chặn bởi Path Sandboxing: đường dẫn %q nằm ngoài phạm vi workspace an toàn %q", userPath, ws)
	}

	// Kiểm tra Symlink Escape: Tìm nút cha đã tồn tại và đối soát canonical path
	evalTarget, err := evalExistingPathPrefix(target)
	if err == nil {
		relCanonical, err := filepath.Rel(canonicalWS, evalTarget)
		if err != nil || strings.HasPrefix(relCanonical, "..") || filepath.IsAbs(relCanonical) {
			return "", fmt.Errorf("truy cập bị chặn bởi Symlink Sandboxing: symlink %q trỏ tới mục tiêu ngoài workspace (%q)", userPath, evalTarget)
		}
	}

	// Đối soát với chính sách phân quyền Tenant nếu có
	if identity, ok := domain.TenantIdentityFromContext(ctx); ok {
		if !identity.IsWorkspaceAllowed(target) {
			return "", fmt.Errorf("truy cập bị chặn bởi chính sách Tenant: workspace %q không được cấp phép cho tenant %q", target, identity.TenantID)
		}
	}

	return target, nil
}

// evalExistingPathPrefix giải mã canonical symlinks cho phần đường dẫn đã tồn tại trên đĩa
func evalExistingPathPrefix(path string) (string, error) {
	curr := filepath.Clean(path)
	var suffixParts []string

	for {
		if _, err := os.Lstat(curr); err == nil {
			eval, err := filepath.EvalSymlinks(curr)
			if err != nil {
				return "", err
			}
			for i := len(suffixParts) - 1; i >= 0; i-- {
				eval = filepath.Join(eval, suffixParts[i])
			}
			return eval, nil
		}

		parent := filepath.Dir(curr)
		if parent == curr || parent == "." || parent == "/" || parent == "" {
			break
		}
		suffixParts = append(suffixParts, filepath.Base(curr))
		curr = parent
	}

	return path, nil
}

// -------------------------------------------------------------
// ReadFileTool: Đọc file với số dòng rõ ràng
// -------------------------------------------------------------
type ReadFileTool struct {
	workspace string
}

func NewReadFileTool(workspace string) *ReadFileTool {
	return &ReadFileTool{workspace: workspace}
}

func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "Đọc nội dung một tập tin từ hệ thống tập tin kèm số dòng. Có thể chỉ định start_line và end_line để đọc một phần của file lớn."
}
func (t *ReadFileTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *ReadFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Đường dẫn tương đối hoặc tuyệt đối tới file cần đọc"
			},
			"start_line": {
				"type": "integer",
				"description": "Dòng bắt đầu đọc (1-indexed, tùy chọn)"
			},
			"end_line": {
				"type": "integer",
				"description": "Dòng kết thúc đọc (1-indexed, tùy chọn)"
			}
		},
		"required": ["path"]
	}`)
}

type ReadFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

func (t *ReadFileTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args ReadFileArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho read_file: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("đường dẫn path không được để trống")
	}

	targetPath, err := resolvePath(ctx, t.workspace, args.Path)
	if err != nil {
		return "", err
	}

	file, err := os.Open(targetPath)
	if err != nil {
		return "", fmt.Errorf("không thể mở file %s: %w", args.Path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Cho phép đọc dòng dài lên tới 1MB
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	var sb strings.Builder
	lineNum := 1
	start := args.StartLine
	end := args.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 {
		end = 10000000 // Không giới hạn dòng cuối
	}

	for scanner.Scan() {
		if lineNum >= start && lineNum <= end {
			sb.WriteString(fmt.Sprintf("%4d: %s\n", lineNum, scanner.Text()))
		}
		lineNum++
		if lineNum > end {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("lỗi khi đọc file: %w", err)
	}

	res := sb.String()
	if res == "" {
		return fmt.Sprintf("[File rỗng hoặc không có dòng nào nằm trong khoảng %d-%d: %s]", start, end, args.Path), nil
	}
	return fmt.Sprintf("=== Nội dung file: %s (Dòng %d-%d) ===\n%s", args.Path, start, lineNum-1, res), nil
}

// -------------------------------------------------------------
// WriteFileTool: Tạo hoặc ghi đè file
// -------------------------------------------------------------
type WriteFileTool struct {
	workspace string
}

func NewWriteFileTool(workspace string) *WriteFileTool {
	return &WriteFileTool{workspace: workspace}
}

func (t *WriteFileTool) Name() string { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Tạo một file mới hoặc ghi đè toàn bộ nội dung file. Tự động tạo thư mục cha nếu chưa tồn tại."
}
func (t *WriteFileTool) Permission() domain.PermissionLevel { return domain.PermissionDestructive }

func (t *WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Đường dẫn file cần tạo hoặc ghi đè"
			},
			"content": {
				"type": "string",
				"description": "Toàn bộ nội dung văn bản cần ghi vào file"
			},
			"overwrite": {
				"type": "boolean",
				"description": "Đặt thành true nếu muốn ghi đè nếu file đã tồn tại"
			}
		},
		"required": ["path", "content"]
	}`)
}

type WriteFileArgs struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
}

func (t *WriteFileTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args WriteFileArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho write_file: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("đường dẫn path không được để trống")
	}

	targetPath, err := resolvePath(ctx, t.workspace, args.Path)
	if err != nil {
		return "", err
	}

	// Kiểm tra nếu file đã tồn tại mà không cho phép overwrite
	if _, err := os.Stat(targetPath); err == nil && !args.Overwrite {
		return "", fmt.Errorf("file %s đã tồn tại trên đĩa. Để ghi đè, hãy đặt overwrite: true", args.Path)
	}

	// Đảm bảo thư mục cha tồn tại
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("không thể tạo thư mục %s: %w", dir, err)
	}

	if err := os.WriteFile(targetPath, []byte(args.Content), 0644); err != nil {
		return "", fmt.Errorf("không thể ghi file %s: %w", targetPath, err)
	}

	msg := fmt.Sprintf("Đã ghi thành công %d bytes vào file %s", len(args.Content), args.Path)
	lintRes := linter.CheckFile(ctx, t.workspace, targetPath)
	if len(lintRes.Issues) > 0 {
		msg += "\n\n[TỰ ĐỘNG KIỂM TRA LỖI CÚ PHÁP / LINTER]:"
		for _, iss := range lintRes.Issues {
			msg += "\n- " + iss
		}
		if lintRes.HasError {
			msg += "\n⚠️ Vui lòng sửa các lỗi cú pháp/logic trên trước khi hoàn thành tác vụ!"
		}
	}

	return msg, nil
}

// -------------------------------------------------------------
// ReplaceFileContentTool: Chỉnh sửa khối mã nguồn chính xác
// -------------------------------------------------------------
type ReplaceFileContentTool struct {
	workspace string
}

func NewReplaceFileContentTool(workspace string) *ReplaceFileContentTool {
	return &ReplaceFileContentTool{workspace: workspace}
}

func (t *ReplaceFileContentTool) Name() string { return "replace_file_content" }
func (t *ReplaceFileContentTool) Description() string {
	return "Chỉnh sửa tệp bằng cách thay thế chính xác một khối nội dung mục tiêu (target_content) bằng nội dung mới (replacement_content). target_content phải xuất hiện chính xác 1 lần trong file."
}
func (t *ReplaceFileContentTool) Permission() domain.PermissionLevel {
	return domain.PermissionDestructive
}

func (t *ReplaceFileContentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Đường dẫn file cần sửa"
			},
			"target_content": {
				"type": "string",
				"description": "Đoạn văn bản/code gốc chính xác từng ký tự và khoảng trắng cần thay thế"
			},
			"replacement_content": {
				"type": "string",
				"description": "Đoạn văn bản/code mới thay thế vào"
			}
		},
		"required": ["path", "target_content", "replacement_content"]
	}`)
}

type ReplaceArgs struct {
	Path               string `json:"path"`
	TargetContent      string `json:"target_content"`
	ReplacementContent string `json:"replacement_content"`
}

func (t *ReplaceFileContentTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args ReplaceArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho replace_file_content: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("đường dẫn path không được để trống")
	}
	if args.TargetContent == "" {
		return "", fmt.Errorf("target_content không được để trống")
	}

	targetPath, err := resolvePath(ctx, t.workspace, args.Path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("không thể đọc file %s: %w", args.Path, err)
	}

	content := string(data)
	count := strings.Count(content, args.TargetContent)
	if count == 0 {
		return "", fmt.Errorf("không tìm thấy target_content trong file %s. Vui lòng kiểm tra lại chính xác từng khoảng trắng và thụt lề", args.Path)
	}
	if count > 1 {
		return "", fmt.Errorf("tìm thấy %d vị trí trùng khớp với target_content trong file %s. Hãy mở rộng thêm các dòng ngữ cảnh xung quanh để target_content trở nên duy nhất", count, args.Path)
	}

	newContent := strings.Replace(content, args.TargetContent, args.ReplacementContent, 1)
	if err := os.WriteFile(targetPath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("không thể lưu lại file %s: %w", targetPath, err)
	}

	msg := fmt.Sprintf("Đã thay thế thành công khối nội dung trong file %s", args.Path)
	lintRes := linter.CheckFile(ctx, t.workspace, targetPath)
	if len(lintRes.Issues) > 0 {
		msg += "\n\n[TỰ ĐỘNG KIỂM TRA LỖI CÚ PHÁP / LINTER]:"
		for _, iss := range lintRes.Issues {
			msg += "\n- " + iss
		}
		if lintRes.HasError {
			msg += "\n⚠️ Vui lòng sửa các lỗi cú pháp/logic trên trước khi hoàn thành tác vụ!"
		}
	}

	return msg, nil
}

// -------------------------------------------------------------
// ListDirectoryTool: Liệt kê nội dung thư mục
// -------------------------------------------------------------
type ListDirectoryTool struct {
	workspace string
}

func NewListDirectoryTool(workspace string) *ListDirectoryTool {
	return &ListDirectoryTool{workspace: workspace}
}

func (t *ListDirectoryTool) Name() string { return "list_directory" }
func (t *ListDirectoryTool) Description() string {
	return "Liệt kê danh sách các tệp tin và thư mục con trong một đường dẫn chỉ định."
}
func (t *ListDirectoryTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *ListDirectoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Đường dẫn thư mục cần xem (để trống hoặc \".\" để xem thư mục làm việc hiện tại)"
			}
		}
	}`)
}

type ListDirectoryArgs struct {
	Path string `json:"path,omitempty"`
}

func (t *ListDirectoryTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args ListDirectoryArgs
	if strings.TrimSpace(argsJSON) != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}

	targetPath, err := resolvePath(ctx, t.workspace, args.Path)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return "", fmt.Errorf("không thể đọc thư mục %s: %w", args.Path, err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Danh mục thư mục: %s (%d mục) ===\n", args.Path, len(entries)))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			sb.WriteString(fmt.Sprintf("%-30s [Lỗi đọc info]\n", entry.Name()))
			continue
		}
		if entry.IsDir() {
			sb.WriteString(fmt.Sprintf("%-35s <DIR>\n", entry.Name()+"/"))
		} else {
			sb.WriteString(fmt.Sprintf("%-35s %10d bytes\n", entry.Name(), info.Size()))
		}
	}

	return sb.String(), nil
}
