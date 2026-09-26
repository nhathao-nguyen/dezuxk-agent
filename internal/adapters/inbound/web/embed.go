package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// GetFileSystem trả về http.FileSystem trỏ vào thư mục static bên trong nhúng
func GetFileSystem() (http.FileSystem, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

// GetEmbeddedFS trả về embed.FS gốc để xử lý nâng cao
func GetEmbeddedFS() embed.FS {
	return staticFS
}
