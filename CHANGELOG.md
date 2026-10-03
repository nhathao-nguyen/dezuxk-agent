# Changelog

Tất cả các thay đổi đáng chú ý của dự án **Dezuxk AI Gateway** được ghi nhận chi tiết tại tài liệu này.
Định dạng tuân theo chuẩn [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), và phiên bản tuân thủ [Semantic Versioning (SemVer)](https://semver.org/spec/v2.0.0.html).

---

## [v0.9.1] - 2026-10-03

### Added
- **Observability Expansion**: Bổ sung đầy đủ 15 metrics chuẩn Prometheus:
  - `gateway_request_duration_seconds` (histogram với các bucket độ trễ chuẩn)
  - `gateway_upstream_duration_seconds` (histogram cho cuộc gọi Google AI)
  - `gateway_tool_duration_seconds` (histogram cho thời gian thực thi công cụ Agent)
  - `gateway_agent_run_duration_seconds` (histogram cho vòng đời tác vụ tự trị)
  - `gateway_active_requests` (gauge đếm số lượng HTTP requests đang xử lý)
  - `gateway_active_streams` (gauge đếm số lượng SSE streaming active)
  - `gateway_agent_queue_depth` (gauge đếm số lượng agent runs đang chờ xử lý)
  - `gateway_rate_limit_rejections_total` (counter phân loại theo reason)
  - `gateway_concurrency_rejections_total` (counter phân loại theo reason)
  - `gateway_upstream_failover_total` (counter phân loại theo reason)
  - `gateway_circuit_breaker_state` (gauge: 0=closed, 1=half-open, 2=open)
  - `gateway_tool_execution_total` (counter phân loại theo tool_name)
  - `gateway_tool_execution_errors_total` (counter phân loại theo tool_name và error_class)
  - `gateway_agent_runs_total` (counter phân loại theo status)
  - `gateway_agent_run_failures_total` (counter phân loại theo reason)
- **Centralized Versioning**: Khởi tạo gói `internal/version` hỗ trợ inject thông tin phiên bản tại thời điểm build qua `-ldflags` (`Version`, `GitCommit`, `BuildDate`).
- **Version API**: Cung cấp endpoint an toàn `GET /version` và tích hợp thông tin phiên bản vào Admin Overview API.
- **Load Testing Tier B**: Bổ sung workflow kiểm thử tải nặng độc lập `.github/workflows/load_test.yml` (500+ chat requests, 50+ streams, chaos 429/500/timeout, mock upstream, kiểm soát rò rỉ goroutine/memory).
- **Branch Protection Documentation**: Soạn thảo tài liệu chuẩn `docs/BRANCH_PROTECTION.md` quy định required status check `Production Verification Gate` cho nhánh `main`.
- **Multi-Node Architecture**: Soạn thảo tài liệu thiết kế cụm phân tán `docs/MULTI_NODE_ARCHITECTURE.md` chuẩn hóa các cổng `DistributedLocker`, `SharedRateLimiter`, `LeaseManager`, `EventBus`, cùng mô hình fencing token chống split-brain.

### Changed
- **Fail-Closed Database Initialization**: Trong môi trường `production`, nếu SQLite hoặc cơ sở dữ liệu persistent bị lỗi kết nối hoặc migration thất bại, daemon **lập tức dừng khởi động (FAIL CLOSED)**. Cấu hình `allow_memory_fallback: true` chỉ có hiệu lực trong môi trường `development`/`test` và tuyệt đối không được phép ghi đè tính bền vững dữ liệu trong `production`.
- **Atomic Media Metadata Persistence**: Nâng cấp cơ chế lưu trữ metadata media sidecar:
  - Ghi tệp tạm `<id>.metadata.json.tmp` -> gọi `Sync()` (fsync) -> đóng file -> đổi tên nguyên tử (`os.Rename`).
  - Tự động dọn dẹp (rollback) tệp media nhị phân và tệp tạm nếu quá trình ghi metadata gặp lỗi (chống triệt để orphan assets).
  - Tự động từ chối phục vụ (fail closed) khi tệp metadata bị mất hoặc bị hỏng định dạng JSON.
- **Gemini Live Smoke Workflow**: Loại bỏ điều kiện `if` sai context tại job level; chuyển việc kiểm tra secret xuống bước runtime trong step để đảm bảo workflow dispatch luôn chạy và không bị đỏ khi chưa có secret lab.

### Fixed
- Lỗi rò rỉ tài nguyên khi ghi metadata media thất bại bỏ sót file nhị phân trên đĩa.
- Lỗi im lặng chuyển sang bộ nhớ RAM trong production khi SQLite bị hỏng hoặc chưa mount storage volume.

---

## [v0.9.0] - 2026-10-03

### Added
- **Production Verification Gate**: Thiết lập CI pipeline kiểm chuẩn nghiêm ngặt với `go mod verify`, `gofmt`, `go vet`, `staticcheck`, `govulncheck`, và concurrency race detector (`go test -race`).
- **Hexagonal Clean Architecture**: Tách biệt rõ ràng Domain, Ports, Adapters, và App Daemon.
- **Virtual API Keys & Secret Vault**: Hỗ trợ mã hóa AES-256-GCM cho master key và SQLite key storage.
- **Circuit Breaker & Exponential Backoff**: Bảo vệ chống nghẽn và tự động failover giữa các tài khoản upstream Google.
- **Durable Agent ReAct & Graph Engine**: Lưu trạng thái checkpoint và nhật ký tool execution phòng vệ chống side-effect replay.
- **Multi-Tier Rate Limiting**: Bảo vệ chống abuse phân tầng theo IP, TenantID, VirtualKey, Endpoint, và Model.
