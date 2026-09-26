package web_test

import (
	"io"
	"testing"

	"dezuxk-gateway/internal/adapters/inbound/web"
)

func TestGetFileSystem(t *testing.T) {
	hfs, err := web.GetFileSystem()
	if err != nil {
		t.Fatalf("failed to get embedded file system: %v", err)
	}

	// 1. Kiểm tra đọc index.html
	f, err := hfs.Open("index.html")
	if err != nil {
		t.Fatalf("expected index.html to exist in embedded fs: %v", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected non-empty index.html")
	}

	// 2. Kiểm tra đọc style.css
	fCSS, err := hfs.Open("style.css")
	if err != nil {
		t.Fatalf("expected style.css to exist in embedded fs: %v", err)
	}
	defer fCSS.Close()

	// 3. Kiểm tra đọc app.js
	fJS, err := hfs.Open("app.js")
	if err != nil {
		t.Fatalf("expected app.js to exist in embedded fs: %v", err)
	}
	defer fJS.Close()
}
