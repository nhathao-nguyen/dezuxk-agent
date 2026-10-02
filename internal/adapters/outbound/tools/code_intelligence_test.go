package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dezuxk-gateway/internal/adapters/outbound/tools"
)

func TestGrepCodeTool(t *testing.T) {
	tempDir := t.TempDir()

	file1 := filepath.Join(tempDir, "service.go")
	_ = os.WriteFile(file1, []byte("package services\n\nfunc ExecuteChatSync() string {\n    return \"ok\"\n}\n"), 0644)

	file2 := filepath.Join(tempDir, "handler.go")
	_ = os.WriteFile(file2, []byte("package http\n\nfunc HandleRequest() {\n    // calls ExecuteChatSync\n}\n"), 0644)

	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	ctx := context.Background()

	// 1. Tìm kiếm chuỗi "ExecuteChatSync"
	res, err := reg.Execute(ctx, "grep_code", `{"query": "ExecuteChatSync"}`)
	if err != nil {
		t.Fatalf("grep_code failed: %v", err)
	}
	if !strings.Contains(res, "service.go") || !strings.Contains(res, "handler.go") {
		t.Fatalf("expected matches in both service.go and handler.go, got:\n%s", res)
	}

	// 2. Tìm kiếm với file_pattern
	resFiltered, err := reg.Execute(ctx, "grep_code", `{"query": "ExecuteChatSync", "file_pattern": "*service.go"}`)
	if err != nil {
		t.Fatalf("grep_code with file_pattern failed: %v", err)
	}
	if !strings.Contains(resFiltered, "service.go") {
		t.Fatalf("expected match in service.go, got:\n%s", resFiltered)
	}
	if strings.Contains(resFiltered, "handler.go") {
		t.Fatalf("handler.go should be filtered out by pattern")
	}
}

func TestReadSymbolDefinitionTool(t *testing.T) {
	tempDir := t.TempDir()

	code := `package services

import "context"

type ChatService struct {
    ID string
}

// ExecuteChatSync xử lý yêu cầu chat đồng bộ
func (s *ChatService) ExecuteChatSync(ctx context.Context, msg string) (string, error) {
    result := "Hello " + msg
    return result, nil
}

func StandaloneFunc(a, b int) int {
    return a + b
}
`
	serviceFile := filepath.Join(tempDir, "chat_service.go")
	if err := os.WriteFile(serviceFile, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	reg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(reg, tempDir)

	ctx := context.Background()

	// 1. Đọc định nghĩa hàm ExecuteChatSync trong package services mà không cần biết số dòng
	res, err := reg.Execute(ctx, "read_symbol_definition", `{"symbol": "ExecuteChatSync", "package_or_dir": "services"}`)
	if err != nil {
		t.Fatalf("read_symbol_definition failed: %v", err)
	}

	if !strings.Contains(res, "=== TÌM THẤY ĐỊNH NGHĨA SYMBOL: ExecuteChatSync") {
		t.Fatalf("expected symbol header in output, got:\n%s", res)
	}
	if !strings.Contains(res, "Method (Receiver: s *ChatService)") {
		t.Fatalf("expected Method with receiver in output, got:\n%s", res)
	}
	if !strings.Contains(res, "func (s *ChatService) ExecuteChatSync") {
		t.Fatalf("expected function signature in output, got:\n%s", res)
	}
	if !strings.Contains(res, "return result, nil") {
		t.Fatalf("expected function body in output, got:\n%s", res)
	}

	// 2. Đọc định nghĩa struct ChatService
	resStruct, err := reg.Execute(ctx, "read_symbol_definition", `{"symbol": "ChatService"}`)
	if err != nil {
		t.Fatalf("read struct failed: %v", err)
	}
	if !strings.Contains(resStruct, "Loại: Struct") {
		t.Fatalf("expected Struct type in output, got:\n%s", resStruct)
	}
	if !strings.Contains(resStruct, "type ChatService struct") {
		t.Fatalf("expected struct definition in output, got:\n%s", resStruct)
	}

	// 3. Đọc hàm StandaloneFunc
	resFunc, err := reg.Execute(ctx, "read_symbol_definition", `{"symbol": "StandaloneFunc"}`)
	if err != nil {
		t.Fatalf("read standalone func failed: %v", err)
	}
	if !strings.Contains(resFunc, "Loại: Function") {
		t.Fatalf("expected Function type in output, got:\n%s", resFunc)
	}
	if !strings.Contains(resFunc, "return a + b") {
		t.Fatalf("expected body in output, got:\n%s", resFunc)
	}
}
