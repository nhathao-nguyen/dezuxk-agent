# Kế Hoạch Triển Khai: Chuyển Đổi Sang Dezuxk Unified CLI (Cobra Hybrid Engine)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Loại bỏ hoàn toàn Web UI nhúng và web client khỏi repository `dezuxk-gateway`, đồng thời xây dựng hệ thống dòng lệnh đa năng Unified CLI bằng Cobra với cơ chế thực thi lai (Hybrid Execution Engine) hỗ trợ 100% tính năng của Gateway.

**Architecture:** Sử dụng kiến trúc Hexagonal với adapter Inbound mới `internal/adapters/inbound/cli/`. Tích hợp thư viện Cobra cho phân cấp lệnh, cơ chế Hybrid tự động phát hiện Daemon đang chạy để gọi REST API nội bộ hoặc truy cập trực tiếp SQLite/Vault khi offline, bộ định dạng ANSI Table / JSON / Stream.

**Tech Stack:** Go 1.25+, `github.com/spf13/cobra`, Chi HTTP Router, SQLite (WAL mode), AES-256-GCM Secret Vault.

**Spec:** [`docs/superpowers/specs/2026-09-26-cli-operational-redesign.md`](file:///d:/nhathao/AI/dezuxk/docs/superpowers/specs/2026-09-26-cli-operational-redesign.md)

## Global Constraints

- Không hardcode model, URL, secret, hay magic numbers.
- Bảo toàn nguyên vẹn 100% tính năng nghiệp vụ của Gateway (Profiles, Chrome CDP, Virtual Keys, Gemini Chat, Flow Studio, Response Cache, Alerts, Health).
- Tất cả các lệnh CLI phải chạy được ở chế độ bảng màu ANSI Table (`--format=table`) và định dạng cấu trúc (`--format=json`).
- Sau khi loại bỏ Web UI, toàn bộ test suite `go test -count=1 ./...` phải PASS hoàn toàn không lỗi.

---

### Task 1: Gỡ Bỏ Web UI Nhúng & Dọn Dẹp Tuyến Đường Router

**Files:**
- Modify: `internal/adapters/inbound/http/router.go:1-35` và `170-177`
- Delete: `internal/adapters/inbound/web/embed.go`
- Delete: `internal/adapters/inbound/web/embed_test.go`
- Delete: `internal/adapters/inbound/web/static/*`
- Test: `internal/adapters/inbound/http/router_test.go`

**Interfaces:**
- Consumes: `internal/adapters/inbound/http/router.go`
- Produces: Router sạch không còn phụ thuộc gói `web`, route `/admin` đã bị gỡ bỏ, các route `/v1/*` và `/health` hoạt động bình thường.

- [ ] **Step 1: Viết test kiểm tra router không còn phục vụ /admin**

Tạo `internal/adapters/inbound/http/router_admin_removed_test.go`:
```go
package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
)

func TestRouter_AdminWebRemoved(t *testing.T) {
	cfg := config.DefaultConfig()
	router := BuildRouter(RouterDependencies{
		Config:        cfg,
		ModelRegistry: domain.NewModelRegistry(nil),
	})

	// Kiểm tra route /admin trả về 404 Not Found
	req := httptest.NewRequest("GET", "/admin", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("kỳ vọng /admin trả về 404 Not Found, nhận được %d", rec.Code)
	}

	// Kiểm tra /health vẫn hoạt động bình thường
	reqHealth := httptest.NewRequest("GET", "/health", nil)
	recHealth := httptest.NewRecorder()
	router.ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK {
		t.Fatalf("kỳ vọng /health trả về 200 OK, nhận được %d", recHealth.Code)
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận test thất bại**

Run: `go test -v ./internal/adapters/inbound/http -run TestRouter_AdminWebRemoved`
Expected: FAIL (vì `/admin` hiện tại vẫn đang chuyển hướng hoặc phục vụ file nhúng).

- [ ] **Step 3: Gỡ bỏ import web và mount /admin trong router.go, xóa thư mục internal/adapters/inbound/web**

Chỉnh sửa [`internal/adapters/inbound/http/router.go`](file:///d:/nhathao/AI/dezuxk/internal/adapters/inbound/http/router.go):
- Bỏ import `"dezuxk-gateway/internal/adapters/inbound/web"`
- Xóa khối code:
```go
	// Phục vụ Giao diện Embedded Admin Dashboard tại /admin
	hfs, err := web.GetFileSystem()
	if err == nil {
		r.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		})
		r.Handle("/admin/*", http.StripPrefix("/admin", http.FileServer(hfs)))
	}
```
Xóa thư mục `internal/adapters/inbound/web` khỏi hệ thống.

- [ ] **Step 4: Chạy test để xác nhận test PASS**

Run: `go test -v ./internal/adapters/inbound/http -run TestRouter_AdminWebRemoved`
Expected: PASS

- [ ] **Step 5: Chạy toàn bộ test suites hiện có**

Run: `go test -count=1 ./...`
Expected: 100% PASS

---

### Task 2: Cài Đặt Cobra & Xây Dựng Bộ Khung Root Command & Printer Formatter

**Files:**
- Modify: `go.mod`
- Create: `internal/adapters/inbound/cli/root.go`
- Create: `internal/adapters/inbound/cli/printer.go`
- Test: `internal/adapters/inbound/cli/printer_test.go`

**Interfaces:**
- Consumes: `github.com/spf13/cobra`
- Produces: `cli.NewRootCmd() *cobra.Command`, `cli.PrintTable(headers []string, rows [][]string)`, `cli.PrintJSON(data any)`

- [ ] **Step 1: Viết test cho printer.go**

Tạo `internal/adapters/inbound/cli/printer_test.go`:
```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrinter_FormatTable(t *testing.T) {
	buf := new(bytes.Buffer)
	headers := []string{"ID", "NAME", "STATUS"}
	rows := [][]string{
		{"acc_01", "Lab Test", "READY"},
		{"acc_02", "Prod User", "EXPIRED"},
	}

	RenderTable(buf, headers, rows)
	out := buf.String()

	if !strings.Contains(out, "acc_01") || !strings.Contains(out, "READY") {
		t.Fatalf("bảng không chứa dữ liệu mong đợi: %s", out)
	}
}

func TestPrinter_FormatJSON(t *testing.T) {
	buf := new(bytes.Buffer)
	data := map[string]string{"status": "ok", "version": "1.0.0"}

	err := RenderJSON(buf, data)
	if err != nil {
		t.Fatalf("lỗi render JSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"status": "ok"`) {
		t.Fatalf("JSON không hợp lệ: %s", out)
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận test thất bại**

Run: `go test -v ./internal/adapters/inbound/cli -run TestPrinter`
Expected: FAIL with compilation error (chưa có package `cli`).

- [ ] **Step 3: Cài đặt spf13/cobra và viết printer.go cùng root.go**

Chạy: `go get github.com/spf13/cobra@latest`
Tạo `internal/adapters/inbound/cli/printer.go`:
- Triển khai `RenderTable(w io.Writer, headers []string, rows [][]string)` dùng tabwriter hoặc ASCII box borders.
- Triển khai `RenderJSON(w io.Writer, data any) error` dùng `json.MarshalIndent`.

Tạo `internal/adapters/inbound/cli/root.go`:
- Khởi tạo `RootCmd = &cobra.Command{Use: "dezuxk", Short: "Dezuxk AI Gateway CLI Controller"}`
- Gắn PersistentFlags: `--config`, `--format`, `--url`, `--offline`, `--token`.

- [ ] **Step 4: Chạy test để xác nhận test PASS**

Run: `go test -v ./internal/adapters/inbound/cli -run TestPrinter`
Expected: PASS

---

### Task 3: Xây Dựng Cơ Chế Thực Thi Lai (Hybrid Engine: Client HTTP + Direct DB)

**Files:**
- Create: `internal/adapters/inbound/cli/client.go`
- Create: `internal/adapters/inbound/cli/direct.go`
- Create: `internal/adapters/inbound/cli/context.go`
- Test: `internal/adapters/inbound/cli/hybrid_test.go`

**Interfaces:**
- Consumes: `internal/config`, `internal/adapters/outbound/session`, `internal/core/services`
- Produces: `GetExecutionContext(cmd *cobra.Command) (*ExecutionContext, error)` tự động xác định `IsOnline` (có HTTP client) hoặc `DirectServices` (SessionRepo, KeyService, ProfileManager).

- [ ] **Step 1: Viết test cho cơ chế Hybrid phát hiện Online/Offline**

Tạo `internal/adapters/inbound/cli/hybrid_test.go`:
```go
package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHybrid_DetectOnline(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ready":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	online := ProbeDaemon(ts.URL, 500)
	if !online {
		t.Fatalf("kỳ vọng phát hiện server online, nhưng trả về offline")
	}

	offline := ProbeDaemon("http://127.0.0.1:59999", 100)
	if offline {
		t.Fatalf("kỳ vọng cổng không mở trả về offline, nhưng trả về online")
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận test thất bại**

Run: `go test -v ./internal/adapters/inbound/cli -run TestHybrid_DetectOnline`
Expected: FAIL (hàm `ProbeDaemon` chưa tồn tại).

- [ ] **Step 3: Triển khai client.go, direct.go và context.go**

Tạo `internal/adapters/inbound/cli/client.go`:
- Triển khai `ProbeDaemon(baseURL string, timeoutMs int) bool` (thử ping `GET /ready`).
- Triển khai `GatewayClient` với các hàm tiện ích gọi REST: `GetOverview()`, `ListProfiles()`, `CreateProfile()`, `LaunchChrome()`, `SyncCDP()`, `ListKeys()`, `CreateKey()`, `RevokeKey()`, `GetFlowCredits()`, `GetGeminiUsage()`, v.v.

Tạo `internal/adapters/inbound/cli/direct.go`:
- Triển khai `InitDirectServices(configPath string) (*DirectServices, error)`: Tự nạp `config.yaml`, mở SQLite SessionRepo, KeyRepo, Vault, và ProfileManager cục bộ khi không có daemon.

Tạo `internal/adapters/inbound/cli/context.go`:
- Ghép nối logic: Nếu không có cờ `--offline` và `ProbeDaemon` trả về `true`, chọn Online Mode; ngược lại chọn Direct Mode.

- [ ] **Step 4: Chạy test để xác nhận test PASS**

Run: `go test -v ./internal/adapters/inbound/cli -run TestHybrid_DetectOnline`
Expected: PASS

---

### Task 4: Triển Khai Nhóm Lệnh Server, Status, Profile & Key

**Files:**
- Create: `internal/adapters/inbound/cli/start.go`
- Create: `internal/adapters/inbound/cli/status.go`
- Create: `internal/adapters/inbound/cli/profile.go`
- Create: `internal/adapters/inbound/cli/key.go`
- Test: `internal/adapters/inbound/cli/commands_test.go`

**Interfaces:**
- Consumes: `internal/app/daemon`, `internal/adapters/inbound/cli/context.go`
- Produces: Các lệnh con của Cobra: `startCmd`, `statusCmd`, `profileCmd`, `keyCmd`.

- [ ] **Step 1: Viết test cho profile và key CLI commands**

Tạo `internal/adapters/inbound/cli/commands_test.go`:
```go
package cli

import (
	"bytes"
	"testing"
)

func TestCLI_ProfileList_Empty(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"profile", "list", "--offline", "--config", "configs/config.yaml"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("lệnh profile list trả về lỗi: %v", err)
	}
	out := buf.String()
	if len(out) == 0 {
		t.Fatalf("kỳ vọng output không rỗng")
	}
}

func TestCLI_KeyList_Empty(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "list", "--offline", "--config", "configs/config.yaml"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("lệnh key list trả về lỗi: %v", err)
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận test thất bại**

Run: `go test -v ./internal/adapters/inbound/cli -run TestCLI_ProfileList`
Expected: FAIL (các lệnh con chưa được đăng ký).

- [ ] **Step 3: Triển khai start.go, status.go, profile.go, key.go**

- `start.go`: Gọi `daemon.Run(configPath, portOverride)`.
- `status.go`: Lấy health & metric snapshot, render bảng thống kê hoặc JSON.
- `profile.go`: Đăng ký `profile` command với các subcommand `list`, `create`, `launch`, `sync`, `ingest`, `proxy`, `delete`.
- `key.go`: Đăng ký `key` command với các subcommand `list`, `create`, `revoke`.
Gắn các lệnh vào `RootCmd`.

- [ ] **Step 4: Chạy test để xác nhận test PASS**

Run: `go test -v ./internal/adapters/inbound/cli -run TestCLI_`
Expected: PASS

---

### Task 5: Triển Khai Nhóm Lệnh Nghiệp Vụ AI & Vận Hành (Chat, Flow, Gemini, Cache, Alerts)

**Files:**
- Create: `internal/adapters/inbound/cli/chat.go`
- Create: `internal/adapters/inbound/cli/flow.go`
- Create: `internal/adapters/inbound/cli/gemini.go`
- Create: `internal/adapters/inbound/cli/cache.go`
- Create: `internal/adapters/inbound/cli/alerts.go`
- Test: `internal/adapters/inbound/cli/ai_ops_test.go`

**Interfaces:**
- Consumes: `GatewayClient`, `DirectServices`, `ModelRegistry`
- Produces: Các lệnh con của Cobra: `chatCmd`, `flowCmd`, `geminiCmd`, `cacheCmd`, `alertsCmd`.

- [ ] **Step 1: Viết test cho cache và alerts CLI commands**

Tạo `internal/adapters/inbound/cli/ai_ops_test.go`:
```go
package cli

import (
	"bytes"
	"testing"
)

func TestCLI_CacheStatus(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"cache", "status", "--offline", "--config", "configs/config.yaml"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("lệnh cache status trả về lỗi: %v", err)
	}
}

func TestCLI_AlertsList(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"alerts", "list", "--offline", "--config", "configs/config.yaml"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("lệnh alerts list trả về lỗi: %v", err)
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận test thất bại**

Run: `go test -v ./internal/adapters/inbound/cli -run TestCLI_CacheStatus`
Expected: FAIL (chưa gắn các lệnh nghiệp vụ).

- [ ] **Step 3: Triển khai chat.go, flow.go, gemini.go, cache.go, alerts.go**

- `chat.go`: Tiếp nhận prompt từ flag `--prompt` hoặc stdin, gửi request completions đến Gateway (hỗ trợ đọc SSE streaming và in trực tiếp ra terminal).
- `flow.go`: Lệnh con `credits`, `projects`, `voices`, `audio`.
- `gemini.go`: Lệnh con `usage`, `conversations`.
- `cache.go`: Lệnh con `status`, `purge`.
- `alerts.go`: Lệnh con `list`, `clear`.
Gắn tất cả các lệnh vào `RootCmd`.

- [ ] **Step 4: Chạy test để xác nhận test PASS**

Run: `go test -v ./internal/adapters/inbound/cli -run TestCLI_CacheStatus`
Expected: PASS

---

### Task 6: Cập Nhật Entrypoint main.go, Biên Dịch Binary & Smoke Test E2E

**Files:**
- Modify: `main.go:1-19`
- Modify: `cmd/daemon/main.go:1-19`
- Test: Toàn bộ test suite & smoke test binary

**Interfaces:**
- Consumes: `internal/adapters/inbound/cli.Execute()`
- Produces: Binary `dezuxk.exe` hoàn chỉnh.

- [ ] **Step 1: Cập nhật main.go để gọi cli.Execute()**

Chỉnh sửa [`main.go`](file:///d:/nhathao/AI/dezuxk/main.go):
```go
package main

import (
	"os"

	"dezuxk-gateway/internal/adapters/inbound/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
```

Đồng bộ tương tự cho [`cmd/daemon/main.go`](file:///d:/nhathao/AI/dezuxk/cmd/daemon/main.go).

- [ ] **Step 2: Chạy toàn bộ test suites hồi quy**

Run: `go test -count=1 ./...`
Expected: 100% PASS trên tất cả các package.

- [ ] **Step 3: Biên dịch binary dezuxk.exe**

Run: `go build -o dezuxk.exe main.go`
Expected: Biên dịch thành công, sinh ra file thực thi `dezuxk.exe`.

- [ ] **Step 4: Chạy Smoke Test E2E kiểm tra CLI thực tế**

Run:
1. `.\dezuxk.exe --help`
2. `.\dezuxk.exe status --offline`
3. `.\dezuxk.exe profile list --offline`
4. `.\dezuxk.exe key list --offline`
5. `.\dezuxk.exe cache status --offline`
6. `.\dezuxk.exe alerts list --offline`
Expected: Tất cả các lệnh đều in bảng ANSI đẹp mắt và thoát với mã 0.
