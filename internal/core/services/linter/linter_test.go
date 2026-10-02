package linter_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/core/services/linter"
)

func TestCheckFile_GoSyntaxError(t *testing.T) {
	tmpDir := t.TempDir()
	badFile := filepath.Join(tmpDir, "bad.go")
	_ = os.WriteFile(badFile, []byte("package main\n\nfunc main() {\n    fmt.Println(\"unclosed string)\n}\n"), 0644)

	res := linter.CheckFile(context.Background(), tmpDir, badFile)
	if !res.HasError {
		t.Fatalf("expected syntax error on unclosed string, but got HasError=false")
	}
	if len(res.Issues) == 0 {
		t.Fatalf("expected issues to be reported")
	}
}

func TestCheckFile_MissingListenAndServe(t *testing.T) {
	tmpDir := t.TempDir()
	forgotServerFile := filepath.Join(tmpDir, "server.go")
	content := `package main

import "net/http"

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("pong"))
    })
    // Forgetting http.ListenAndServe(":8080", mux)
}
`
	_ = os.WriteFile(forgotServerFile, []byte(content), 0644)

	res := linter.CheckFile(context.Background(), tmpDir, forgotServerFile)
	if !res.HasError {
		t.Fatalf("expected linter to flag missing ListenAndServe, got HasError=false")
	}
	foundWarning := false
	for _, iss := range res.Issues {
		if len(iss) > 0 {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected warning message about ListenAndServe")
	}
}

func TestCheckFile_ValidGoWithListenAndServe(t *testing.T) {
	tmpDir := t.TempDir()
	goodFile := filepath.Join(tmpDir, "server.go")
	content := `package main

import (
    "log"
    "net/http"
)

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("pong"))
    })
    log.Fatal(http.ListenAndServe(":8080", mux))
}
`
	_ = os.WriteFile(goodFile, []byte(content), 0644)

	res := linter.CheckFile(context.Background(), tmpDir, goodFile)
	if res.HasError {
		t.Fatalf("expected valid server file to have HasError=false, got issues: %v", res.Issues)
	}
}
