package linter

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CheckResult lưu kết quả kiểm tra cú pháp và linter
type CheckResult struct {
	FilePath string
	HasError bool
	Issues   []string
}

// CheckFile kiểm tra cú pháp và quy tắc linter cho một tệp tin đơn lẻ
func CheckFile(ctx context.Context, workspace, filePath string) *CheckResult {
	res := &CheckResult{FilePath: filePath}
	absPath := filePath
	if !filepath.IsAbs(absPath) && workspace != "" {
		absPath = filepath.Join(workspace, filePath)
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".go":
		checkGoFile(ctx, absPath, res)
	case ".py":
		checkPythonFile(ctx, absPath, res)
	case ".js", ".mjs", ".cjs":
		checkNodeFile(ctx, absPath, res)
	}

	return res
}

func checkGoFile(ctx context.Context, absPath string, res *CheckResult) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return
	}

	// 1. Kiểm tra cú pháp Go bằng parser AST chuẩn
	fset := token.NewFileSet()
	node, parseErr := parser.ParseFile(fset, absPath, content, parser.AllErrors|parser.ParseComments)
	if parseErr != nil {
		res.HasError = true
		res.Issues = append(res.Issues, fmt.Sprintf("Lỗi cú pháp Go: %v", parseErr))
		return
	}

	// 2. Kiểm tra heuristic các lỗi phổ biến (như quên ListenAndServe khi khởi tạo HTTP server)
	hasHTTPServer := false
	hasServerListen := false

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var fnName string
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			fnName = f.Sel.Name
			if id, ok := f.X.(*ast.Ident); ok {
				fnName = id.Name + "." + f.Sel.Name
			}
		case *ast.Ident:
			fnName = f.Name
		}

		if strings.HasSuffix(fnName, "NewServeMux") ||
			strings.HasSuffix(fnName, "HandleFunc") ||
			strings.HasSuffix(fnName, "Handle") ||
			strings.HasSuffix(fnName, "NewRouter") ||
			strings.HasSuffix(fnName, "Default") ||
			strings.HasSuffix(fnName, "New") {
			hasHTTPServer = true
		}

		if strings.HasSuffix(fnName, "ListenAndServe") ||
			strings.HasSuffix(fnName, "ListenAndServeTLS") ||
			strings.HasSuffix(fnName, "Serve") ||
			strings.HasSuffix(fnName, "Run") ||
			strings.HasSuffix(fnName, "Listen") {
			hasServerListen = true
		}
		return true
	})

	if hasHTTPServer && !hasServerListen && node.Name != nil && node.Name.Name == "main" {
		res.HasError = true
		res.Issues = append(res.Issues, "CẢNH BÁO QUAN TRỌNG: Phát hiện cấu hình HTTP router/server trong package main nhưng KHÔNG thấy lệnh ListenAndServe / Serve / Run để lắng nghe kết nối! Server sẽ thoát ngay lập tức nếu không gọi ListenAndServe.")
	}

	// 3. Chạy go vet trên thư mục chứa file
	dir := filepath.Dir(absPath)
	vetCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(vetCtx, "go", "vet", absPath)
	cmd.Dir = dir
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	_ = cmd.Run()

	vetOut := strings.TrimSpace(outBuf.String())
	if vetOut != "" && !strings.Contains(vetOut, "no Go files") {
		// go vet cảnh báo lỗi logic/cú pháp
		if strings.Contains(vetOut, "error") || strings.Contains(vetOut, "syntax") {
			res.HasError = true
		}
		res.Issues = append(res.Issues, fmt.Sprintf("go vet phát hiện: %s", vetOut))
	}
}

func checkPythonFile(ctx context.Context, absPath string, res *CheckResult) {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "python", "-m", "py_compile", absPath)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	err := cmd.Run()
	if err != nil {
		res.HasError = true
		res.Issues = append(res.Issues, fmt.Sprintf("Lỗi cú pháp Python (py_compile): %s", strings.TrimSpace(outBuf.String())))
	}
}

func checkNodeFile(ctx context.Context, absPath string, res *CheckResult) {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "node", "--check", absPath)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	err := cmd.Run()
	if err != nil {
		res.HasError = true
		res.Issues = append(res.Issues, fmt.Sprintf("Lỗi cú pháp JavaScript (node --check): %s", strings.TrimSpace(outBuf.String())))
	}
}

// CheckWorkspace kiểm tra toàn bộ workspace xem có lỗi biên dịch / cú pháp nào còn tồn đọng không
func CheckWorkspace(ctx context.Context, workspace string) (summary string, hasError bool) {
	if workspace == "" {
		workspace = "."
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		absWS = workspace
	}

	var issues []string

	// 1. Kiểm tra Go project nếu có file .go hoặc go.mod
	hasGoMod := false
	hasGoFiles := false
	_ = filepath.Walk(absWS, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		// Bỏ qua thư mục .git, vendor, .dezuxk
		if info.IsDir() && (info.Name() == ".git" || info.Name() == "vendor" || info.Name() == ".dezuxk" || info.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if info.Name() == "go.mod" {
			hasGoMod = true
		}
		if strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go") {
			hasGoFiles = true
		}
		return nil
	})

	if hasGoFiles {
		if hasGoMod {
			// Chạy go vet ./...
			vetCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()

			cmdVet := exec.CommandContext(vetCtx, "go", "vet", "./...")
			cmdVet.Dir = absWS
			var vetBuf bytes.Buffer
			cmdVet.Stdout = &vetBuf
			cmdVet.Stderr = &vetBuf
			_ = cmdVet.Run()

			vetOut := strings.TrimSpace(vetBuf.String())
			if vetOut != "" && !strings.Contains(vetOut, "no Go files") {
				issues = append(issues, fmt.Sprintf("go vet:\n%s", vetOut))
				hasError = true
			}

			// Kiểm tra go build
			buildCtx, cancelBuild := context.WithTimeout(ctx, 10*time.Second)
			defer cancelBuild()

			nullDevice := "NUL"
			if runtime.GOOS != "windows" {
				nullDevice = "/dev/null"
			}

			cmdBuild := exec.CommandContext(buildCtx, "go", "build", "-o", nullDevice, "./...")
			cmdBuild.Dir = absWS
			var buildBuf bytes.Buffer
			cmdBuild.Stdout = &buildBuf
			cmdBuild.Stderr = &buildBuf
			buildErr := cmdBuild.Run()
			buildOut := strings.TrimSpace(buildBuf.String())
			if buildErr != nil && buildOut != "" && !strings.Contains(buildOut, "no Go files") {
				issues = append(issues, fmt.Sprintf("go build lỗi biên dịch:\n%s", buildOut))
				hasError = true
			}
		} else {
			// Không có go.mod: Kiểm tra cú pháp AST cho từng file .go
			fset := token.NewFileSet()
			_ = filepath.Walk(absWS, func(p string, info os.FileInfo, err error) error {
				if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
					return nil
				}
				content, readErr := os.ReadFile(p)
				if readErr == nil {
					if _, parseErr := parser.ParseFile(fset, p, content, parser.AllErrors); parseErr != nil {
						issues = append(issues, fmt.Sprintf("Lỗi cú pháp tại %s: %v", info.Name(), parseErr))
						hasError = true
					}
				}
				return nil
			})
		}
	}

	// 2. Quét các file .go kiểm tra lỗi ListenAndServe
	_ = filepath.Walk(absWS, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() && (info.Name() == ".git" || info.Name() == "vendor" || info.Name() == ".dezuxk" || info.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if strings.HasSuffix(info.Name(), ".go") {
			checkRes := &CheckResult{FilePath: p}
			checkGoFile(ctx, p, checkRes)
			if checkRes.HasError {
				hasError = true
				for _, iss := range checkRes.Issues {
					issues = append(issues, fmt.Sprintf("[%s]: %s", info.Name(), iss))
				}
			}
		} else if strings.HasSuffix(info.Name(), ".py") {
			checkRes := &CheckResult{FilePath: p}
			checkPythonFile(ctx, p, checkRes)
			if checkRes.HasError {
				hasError = true
				for _, iss := range checkRes.Issues {
					issues = append(issues, fmt.Sprintf("[%s]: %s", info.Name(), iss))
				}
			}
		} else if strings.HasSuffix(info.Name(), ".js") {
			checkRes := &CheckResult{FilePath: p}
			checkNodeFile(ctx, p, checkRes)
			if checkRes.HasError {
				hasError = true
				for _, iss := range checkRes.Issues {
					issues = append(issues, fmt.Sprintf("[%s]: %s", info.Name(), iss))
				}
			}
		}
		return nil
	})

	if len(issues) == 0 {
		return "", false
	}

	return strings.Join(issues, "\n\n"), hasError
}
