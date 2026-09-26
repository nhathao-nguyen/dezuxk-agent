# Giai Đoạn 3: Embedded Admin Dashboard & In-Memory Response Caching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Triển khai Giai đoạn 3 cho `dezuxk-gateway`: Tầng Bộ nhớ đệm Phản hồi Thông minh (In-Memory Response Caching với SHA-256, phản hồi < 5ms, LRU, TTL) và Giao diện Quản trị nhúng (Embedded Admin Dashboard dùng `//go:embed`, SPA Dark theme, giám sát Account Pool, điều khiển Chrome 1-Click & đồng bộ CDP, cấu hình Proxy nhanh, theo dõi Metrics/GPU `HTrJv`/`yBhWQ`).

**Architecture:** Sử dụng Clean Architecture phân lớp của Go. Tầng Cache được tích hợp trong core services/http adapter với thread-safe in-memory store; Tầng Web UI sử dụng `embed.FS` nhúng toàn bộ HTML/CSS/JS thuần không phụ thuộc npm hay CDN ngoài; Các API `/v1/admin/*` được bảo vệ bằng Admin Auth Middleware (Token/Cookie/BasicAuth).

**Tech Stack:** Go 1.24, Chi v5 router, Go `embed.FS`, HTML5/CSS3/Vanilla JS (ES6), HTML5 Canvas, SQLite3 (WAL mode), Chrome DevTools Protocol (CDP).

**Spec:** `docs/superpowers/specs/2026-09-26-giai-doan-3-dashboard-va-caching-design.md`

## Global Constraints

- **TUYỆT ĐỐI KHÔNG CHỈNH SỬA**: `gateway-goi-thang-server-kien-truc.md`, `kientruc.md`, `ARCHITECTURE.md`.
- **CẤM TUYỆT ĐỐI HARDCODE**: Tất cả URL, cổng CDP, thông số TTL cache, dung lượng RAM cache cấu hình qua `config.yaml`.
- **Giao diện nhúng**: Sử dụng `embed.FS` để nhúng SPA, không yêu cầu Node.js/npm khi chạy daemon binary.
- **Tiêu chuẩn kiểm thử**: Toàn bộ `go test -count=1 ./...` và `go build ./...` đạt 100% PASS.

---

### Task 1: Cấu hình Hạ tầng Cache & Admin (`config.yaml`, `internal/config/config.go`)

**Files:**
- Modify: `configs/config.yaml`
- Modify: `configs/config.example.yaml`
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

---

### Task 2: In-Memory Response Caching Core Engine (`internal/core/services/`)

**Files:**
- Create: `internal/core/services/response_cache.go`
- Test: `internal/core/services/response_cache_test.go`

---

### Task 3: Tích hợp Cache vào Chat Completion & Flow Credits Handlers

**Files:**
- Modify: `internal/adapters/inbound/http/chat_handler.go`
- Modify: `internal/adapters/inbound/http/flow_handler.go`
- Modify: `internal/adapters/inbound/http/router.go`
- Test: `internal/adapters/inbound/http/chat_cache_test.go`
- Test: `internal/adapters/inbound/http/flow_cache_test.go`

---

### Task 4: Mở rộng Gateway Metrics & Overview API

**Files:**
- Modify: `internal/core/domain/contract_metrics.go`
- Modify: `internal/adapters/inbound/http/admin_handler.go`
- Test: `internal/adapters/inbound/http/admin_overview_test.go`

---

### Task 5: Admin Authentication & Security Middleware

**Files:**
- Create: `internal/adapters/inbound/http/admin_auth_middleware.go`
- Modify: `internal/adapters/inbound/http/admin_handler.go`
- Modify: `internal/adapters/inbound/http/router.go`
- Test: `internal/adapters/inbound/http/admin_auth_test.go`

---

### Task 6: Embedded Web Dashboard SPA (`internal/adapters/inbound/web/`)

**Files:**
- Create: `internal/adapters/inbound/web/embed.go`
- Create: `internal/adapters/inbound/web/static/index.html`
- Create: `internal/adapters/inbound/web/static/style.css`
- Create: `internal/adapters/inbound/web/static/app.js`
- Test: `internal/adapters/inbound/web/embed_test.go`

---

### Task 7: Tích hợp Router, Daemon & Toàn Bộ Verification Suite

**Files:**
- Modify: `internal/adapters/inbound/http/router.go`
- Modify: `internal/app/daemon/daemon.go`
- Test toàn bộ `go test -count=1 ./...`
- Test build `go build ./...`
- Xác thực Chrome DevTools MCP
