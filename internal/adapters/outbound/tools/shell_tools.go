package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

// -------------------------------------------------------------
// RunCommandTool: Thực thi lệnh Shell (PowerShell / Bash)
// -------------------------------------------------------------
type RunCommandTool struct {
	workspace string
}

func NewRunCommandTool(workspace string) *RunCommandTool {
	return &RunCommandTool{workspace: workspace}
}

func (t *RunCommandTool) Name() string { return "run_command" }
func (t *RunCommandTool) Description() string {
	return "Thực thi một câu lệnh shell (PowerShell trên Windows, Bash trên Linux/macOS) và trả về stdout, stderr, mã thoát (exit code) cùng thời gian chạy."
}
func (t *RunCommandTool) Permission() domain.PermissionLevel {
	return domain.PermissionDestructive
}

func (t *RunCommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "Câu lệnh shell chính xác cần chạy"
			},
			"cwd": {
				"type": "string",
				"description": "Thư mục làm việc thực thi lệnh (tùy chọn, mặc định là workspace hiện tại)"
			},
			"timeout_seconds": {
				"type": "integer",
				"description": "Thời gian chờ tối đa bằng giây trước khi buộc ngắt lệnh (mặc định: 60s, tối đa: 300s)"
			}
		},
		"required": ["command"]
	}`)
}

type RunCommandArgs struct {
	Command        string `json:"command"`
	Cwd            string `json:"cwd,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *RunCommandTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args RunCommandArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho run_command: %w", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", fmt.Errorf("câu lệnh command không được để trống")
	}

	timeoutSec := args.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}

	workDir, err := resolvePath(ctx, t.workspace, args.Cwd)
	if err != nil {
		return "", err
	}
	if workDir == "" {
		workDir = "."
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("không thể tạo thư mục thực thi %s: %w", workDir, err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", args.Command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", args.Command)
	}
	cmd.Dir = workDir

	// Làm sạch biến môi trường, tuyệt đối không truyền bí mật server vào tiến trình con
	var cleanEnv []string
	sensitiveKeys := []string{
		"DEZUXK_MASTER_KEY",
		"DEZUXK_API_KEY",
		"DEZUXK_ADMIN_PASSWORD",
		"DEZUXK_ADMIN_SESSION_TOKEN",
	}
	for _, env := range os.Environ() {
		isSensitive := false
		upperEnv := strings.ToUpper(env)
		for _, sk := range sensitiveKeys {
			if strings.HasPrefix(upperEnv, sk+"=") {
				isSensitive = true
				break
			}
		}
		if !isSensitive {
			cleanEnv = append(cleanEnv, env)
		}
	}
	cmd.Env = cleanEnv

	// Khởi tạo Process Group / Windows Job Object để quản lý toàn bộ cây tiến trình
	jobGroup, _ := setupProcessGroup(cmd)

	liveOutput := domain.GetLiveOutput(ctx)

	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutWriter := &liveCaptureWriter{buf: &stdoutBuf, streamType: "stdout", onChunk: liveOutput}
	stderrWriter := &liveCaptureWriter{buf: &stderrBuf, streamType: "stderr", onChunk: liveOutput}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	startTime := time.Now()
	err = cmd.Start()
	if err != nil {
		if jobGroup != nil {
			jobGroup.dispose(false)
		}
		return "", fmt.Errorf("không thể khởi chạy tiến trình: %w", err)
	}

	// Gán tiến trình vào Job Object
	if jobGroup != nil {
		_ = jobGroup.attachProcess(cmd)
	}

	waitChan := make(chan error, 1)
	go func() {
		waitChan <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitChan:
		// Tiến trình hoàn tất bình thường
		if jobGroup != nil {
			jobGroup.dispose(false)
		}
	case <-cmdCtx.Done():
		// Bị timeout hoặc client hủy yêu cầu -> Hủy triệt để toàn bộ cây tiến trình qua Job Object
		if jobGroup != nil {
			jobGroup.dispose(true)
		}
		_ = cmd.Process.Kill()
		waitErr = <-waitChan
	}

	duration := time.Since(startTime)

	stdoutStr := strings.TrimRight(stdoutBuf.String(), "\r\n")
	stderrStr := strings.TrimRight(stderrBuf.String(), "\r\n")

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if cmdCtx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("=== LỖI QUÁ THỜI GIAN CHỜ (TIMEOUT) ===\nLệnh chạy vượt quá %d giây và toàn bộ cây tiến trình đã bị tiêu diệt sạch sẽ.\nOutput thu thập được:\n%s\nStderr:\n%s",
				timeoutSec, stdoutStr, stderrStr), nil
		} else {
			return "", fmt.Errorf("lỗi thực thi tiến trình: %w", waitErr)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== KẾT QUẢ THỰC THI (Exit Code: %d, Thời gian: %v) ===\n", exitCode, duration.Round(time.Millisecond)))
	if stdoutStr != "" {
		sb.WriteString("--- STDOUT ---\n")
		sb.WriteString(stdoutStr)
		sb.WriteString("\n")
	}
	if stderrStr != "" {
		sb.WriteString("--- STDERR ---\n")
		sb.WriteString(stderrStr)
		sb.WriteString("\n")
	}
	if stdoutStr == "" && stderrStr == "" {
		sb.WriteString("[Lệnh thực thi thành công và không xuất dữ liệu ra màn hình]\n")
	}

	return sb.String(), nil
}

type liveCaptureWriter struct {
	buf        *bytes.Buffer
	streamType string
	onChunk    domain.LiveOutputFunc
}

func (w *liveCaptureWriter) Write(p []byte) (n int, err error) {
	n, err = w.buf.Write(p)
	if w.onChunk != nil && len(p) > 0 {
		w.onChunk(w.streamType, string(p))
	}
	return n, err
}
