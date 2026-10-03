# Hướng dẫn Nâng cấp & Di chuyển Dữ liệu PostgreSQL (PostgreSQL Migration Guide)

Tài liệu này hướng dẫn chi tiết quy trình di chuyển dữ liệu và kiến trúc cơ sở dữ liệu từ SQLite (chế độ Single-Node) lên PostgreSQL (chế độ Multi-Node Cluster).

---

## 1. Cơ chế Chạy Migration Tự động (Atomic Migration Runner)

Hệ thống quản lý schema thông qua cơ chế tự động nạp phiên bản tại [`internal/adapters/outbound/storage/postgres/migrator.go`](file:///d:/nhathao/Vibe/dezuxk-agent/internal/adapters/outbound/storage/postgres/migrator.go):

### 1.1. Chống Tranh chấp Khởi động Đa Node (Advisory Lock)
Khi 3 hoặc nhiều node Gateway cùng khởi động đồng thời, chúng không được phép chạy DDL cạnh tranh làm hỏng schema:
- Trước khi thực thi migration, node khởi động xin PostgreSQL Session-level Advisory Lock:
  ```sql
  SELECT pg_advisory_lock(8472910482);
  ```
- Sau khi kiểm tra bảng `schema_migrations`, áp dụng các file SQL chưa chạy trong transaction nguyên tử, node giải phóng lock:
  ```sql
  SELECT pg_advisory_unlock(8472910482);
  ```

### 1.2. Danh mục Phiên bản Migration
1. `001_initial.sql`:
   - `sessions`: Tài khoản Google / Gemini, cookie và token đã mã hóa AES-256-GCM.
   - `session_alerts`: Lịch sử cảnh báo tài khoản.
   - `virtual_keys`: Virtual API Keys, quota, tỷ lệ RPM, hashed secrets.
   - `virtual_key_daily_usage` & `virtual_key_token_usage`: Thống kê token và hạn mức.
2. `002_agent_runs.sql`:
   - `agent_runs`: Lưu trữ tác vụ tự trị với các cột phân tán (`worker_id`, `claim_generation`, `lease_until`, `heartbeat_at`).
   - `idx_agent_runs_tenant_idempotency`: Ràng buộc Partial Unique Index cho idempotency.
   - `agent_run_events`: Nhật ký sự kiện bền vững của Agent.
   - `agent_checkpoints`: Snapshot trạng thái nén JSON để phục hồi ReAct loop.
   - `archival_memory`: Bộ nhớ dài hạn sử dụng Full-Text Search PostgreSQL (`tsvector` + GIN index).
3. `003_tool_ledger.sql`:
   - `agent_tool_executions`: Sổ cái ghi nhận thực thi công cụ với Fencing Token để ngăn ngừa duplicate side-effects.
4. `004_media_metadata.sql`:
   - `media_assets`: Metadata tập tin nhị phân đa phương tiện trên S3/MinIO.

---

## 2. Bảo mật Dữ liệu & Mã hóa Bí mật (Secret Encryption Parity)

- **Nguyên tắc bảo vệ AES-256-GCM**: Dữ liệu phiên đăng nhập Google (`cookies`, `access_token`, `refresh_token`) khi lưu vào PostgreSQL **tiếp tục được mã hóa bằng Vault AES-256-GCM** tương tự như SQLite.
- Ngay cả khi cơ sở dữ liệu PostgreSQL bị xâm nhập (DB dump compromise), kẻ tấn công **hoàn toàn không thể đọc plain-text cookies** nếu không có khóa bí mật `DEZUXK_MASTER_KEY`.

---

## 3. Quy trình Chuyển dịch Dữ liệu (Data Export / Import Runbook)

Nếu bạn có dữ liệu tài khoản và Virtual API Keys trong SQLite (`storage/gateway.db`) và muốn chuyển sang PostgreSQL:

### Bước 1: Xuất Dữ liệu từ SQLite
Sử dụng script Python hoặc SQLite CLI để trích xuất các bản ghi:
```bash
sqlite3 storage/gateway.db ".dump accounts" > accounts_backup.sql
sqlite3 storage/gateway.db ".dump virtual_keys" > keys_backup.sql
```

### Bước 2: Khởi động PostgreSQL và Áp dụng Migration
Khởi động một instance Gateway với `storage.driver=postgres` để hệ thống tự động chạy toàn bộ migration tạo bảng hoàn chỉnh.

### Bước 3: Nạp Dữ liệu vào PostgreSQL
- Lưu ý: Do format câu lệnh DDL của SQLite khác với PostgreSQL, bạn chỉ cần nạp dữ liệu các cột giá trị (INSERT statements):
  - Bảng `sessions`: giữ nguyên chuỗi `cookies` đã được mã hóa bởi Master Key.
  - Bảng `virtual_keys`: giữ nguyên `key_hash` và quyền hạn.

---

## 4. Bảng Đối Chiếu Kiểu Dữ liệu (Schema Equivalence Matrix)

| Kiểu dữ liệu trong SQLite | Kiểu dữ liệu trong PostgreSQL | Mục đích sử dụng |
| :--- | :--- | :--- |
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGSERIAL PRIMARY KEY` | Khóa chính tăng dần cho sự kiện và bản ghi sử dụng |
| `TEXT` | `VARCHAR` hoặc `TEXT` | Định danh UUID, tên, JSON payload |
| `TIMESTAMP / DATETIME` | `TIMESTAMPTZ` | Lưu trữ mốc thời gian kèm múi giờ chính xác |
| `BLOB` | `BYTEA` | Dữ liệu nhị phân |
| `FTS5 Virtual Table` | `tsvector` + GIN Index | Tìm kiếm toàn văn bản (Full-Text Search) cho bộ nhớ Agent |
