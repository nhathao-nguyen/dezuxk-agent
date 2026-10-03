package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

// GrepCodeTool công cụ tìm kiếm regex tốc độ cao trên codebase bằng ripgrep hoặc Go fallback
type GrepCodeTool struct {
	workspace string
}

func NewGrepCodeTool(workspace string) *GrepCodeTool {
	return &GrepCodeTool{workspace: workspace}
}

func (t *GrepCodeTool) Name() string { return "grep_code" }
func (t *GrepCodeTool) Description() string {
	return "Tìm kiếm biểu thức chính quy (Regex) hoặc chuỗi văn bản trên toàn bộ dự án sử dụng ripgrep (rg) tốc độ cao. Trả về danh sách tệp tin, số dòng và nội dung trùng khớp."
}
func (t *GrepCodeTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *GrepCodeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Biểu thức chính quy (Regex) hoặc từ khóa cần tìm kiếm trong toàn bộ mã nguồn"
			},
			"path": {
				"type": "string",
				"description": "Đường dẫn thư mục hoặc file cụ thể để tìm kiếm (tùy chọn, mặc định là toàn bộ workspace)"
			},
			"file_pattern": {
				"type": "string",
				"description": "Mẫu lọc tên tệp (glob), ví dụ: \"*.go\", \"*.py\", \"*.json\", \"*test*\" (tùy chọn)"
			},
			"case_sensitive": {
				"type": "boolean",
				"description": "Đặt thành true nếu muốn phân biệt chữ hoa/chữ thường (mặc định: false)"
			},
			"max_results": {
				"type": "integer",
				"description": "Số lượng kết quả tối đa cần trả về (mặc định: 50, tối đa: 200)"
			}
		},
		"required": ["query"]
	}`)
}

type GrepCodeArgs struct {
	Query         string `json:"query"`
	Path          string `json:"path,omitempty"`
	FilePattern   string `json:"file_pattern,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

func (t *GrepCodeTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args GrepCodeArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho grep_code: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("truy vấn query không được để trống")
	}

	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}
	if maxResults > 200 {
		maxResults = 200
	}

	searchPath, err := resolvePath(ctx, t.workspace, args.Path)
	if err != nil {
		return "", err
	}
	if searchPath == "" {
		searchPath = "."
	}

	// 1. Tìm đường dẫn ripgrep binary (rg / rg.exe)
	rgBin := findRipgrepBinary()
	if rgBin != "" {
		out, err := executeRipgrep(ctx, rgBin, searchPath, args, maxResults)
		if err == nil {
			return out, nil
		}
	}

	// 2. Fallback: Tìm kiếm bằng bộ quét regex Go nguyên bản (Pure Go Scanner)
	return executeGoRegexSearch(searchPath, args, maxResults)
}

func findRipgrepBinary() string {
	// Kiểm tra trong PATH
	if p, err := exec.LookPath("rg"); err == nil {
		return p
	}
	if p, err := exec.LookPath("rg.exe"); err == nil {
		return p
	}

	// Kiểm tra các đường dẫn cài đặt ripgrep phổ biến trên Windows
	candidates := []string{
		`C:\Users\PC\AppData\Local\OpenAI\Codex\bin\rg.exe`,
		`C:\Users\PC\AppData\Local\Programs\Antigravity IDE\resources\app\node_modules\@vscode\ripgrep\bin\rg.exe`,
		`C:\Users\PC\AppData\Local\Programs\Microsoft VS Code\resources\app\node_modules.asar.unpacked\@vscode\ripgrep\bin\rg.exe`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func executeRipgrep(ctx context.Context, rgBin, searchPath string, args GrepCodeArgs, maxResults int) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmdArgs := []string{
		"--line-number",
		"--color", "never",
		"--no-heading",
		"--max-count", fmt.Sprintf("%d", maxResults),
	}

	if !args.CaseSensitive {
		cmdArgs = append(cmdArgs, "-i")
	}
	if strings.TrimSpace(args.FilePattern) != "" {
		cmdArgs = append(cmdArgs, "-g", args.FilePattern)
	}

	// Bỏ qua các thư mục nhị phân và cache
	cmdArgs = append(cmdArgs,
		"-g", "!.git/*",
		"-g", "!node_modules/*",
		"-g", "!vendor/*",
		"-g", "!.dezuxk/*",
		"-e", args.Query,
		searchPath,
	)

	cmd := exec.CommandContext(cmdCtx, rgBin, cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil && stdout.Len() == 0 {
		return "", fmt.Errorf("ripgrep execution failed: %w: %s", err, stderr.String())
	}
	outLines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var validLines []string
	for _, l := range outLines {
		t := strings.TrimSpace(l)
		if t != "" {
			validLines = append(validLines, t)
			if len(validLines) >= maxResults {
				break
			}
		}
	}

	if len(validLines) == 0 {
		return fmt.Sprintf("[Không tìm thấy kết quả nào trùng khớp với truy vấn %q trong %s]", args.Query, searchPath), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== KẾT QUẢ GREP_CODE (ripgrep | %d kết quả) ===\n", len(validLines)))
	for _, line := range validLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func executeGoRegexSearch(searchPath string, args GrepCodeArgs, maxResults int) (string, error) {
	pattern := args.Query
	if !args.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("biểu thức chính quy không hợp lệ %q: %w", args.Query, err)
	}

	var matches []string

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".dezuxk" {
				return filepath.SkipDir
			}
			return nil
		}

		// Lọc file pattern nếu có
		if args.FilePattern != "" {
			matched, _ := filepath.Match(args.FilePattern, name)
			if !matched {
				return nil
			}
		}

		// Bỏ qua file nhị phân lớn
		if info.Size() > 5*1024*1024 {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNum := 1
		for scanner.Scan() {
			lineText := scanner.Text()
			if re.MatchString(lineText) {
				relPath, _ := filepath.Rel(searchPath, path)
				if relPath == "" {
					relPath = path
				}
				matches = append(matches, fmt.Sprintf("%s:%d: %s", relPath, lineNum, strings.TrimSpace(lineText)))
				if len(matches) >= maxResults {
					return filepath.SkipAll
				}
			}
			lineNum++
		}
		return scanner.Err()
	})

	if err != nil && err != filepath.SkipAll {
		return "", fmt.Errorf("lỗi khi duyệt cây thư mục: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Sprintf("[Không tìm thấy kết quả nào trùng khớp với truy vấn %q trong %s]", args.Query, searchPath), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== KẾT QUẢ GREP_CODE (Go Scanner | %d kết quả) ===\n", len(matches)))
	for _, m := range matches {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}
