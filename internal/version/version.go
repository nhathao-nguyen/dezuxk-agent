package version

import (
	"fmt"
	"runtime"
)

var (
	// Version là mã phiên bản chuẩn SemVer (MAJOR.MINOR.PATCH) của Dezuxk Agent Gateway.
	// Có thể được ghi đè tại thời điểm build: -ldflags "-X dezuxk-gateway/internal/version.Version=v0.9.1"
	Version = "v0.9.1"

	// GitCommit là mã hash Git commit SHA tại thời điểm build.
	// Có thể được ghi đè tại thời điểm build: -ldflags "-X dezuxk-gateway/internal/version.GitCommit=$(git rev-parse --short HEAD)"
	GitCommit = "dev"

	// BuildDate là thời gian build chuẩn UTC ISO-8601.
	// Có thể được ghi đè tại thời điểm build: -ldflags "-X dezuxk-gateway/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	BuildDate = "unknown"
)

// Info chứa thông tin phiên bản và môi trường runtime an toàn (tuyệt đối không leak secret)
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

// GetInfo trả về snapshot thông tin phiên bản hệ thống
func GetInfo() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String định dạng phiên bản ngắn gọn cho logging và CLI
func String() string {
	return fmt.Sprintf("Dezuxk Gateway %s (commit: %s, built: %s)", Version, GitCommit, BuildDate)
}
