# Kiến trúc Đa Node & Cụm Phân tán (Multi-Node Architecture)

Tài liệu này xác định mô hình kiến trúc phân tán (Multi-Node Gateway Cluster) chính thức cho Dezuxk AI Gateway, chuẩn hóa interface (Hexagonal Architecture Ports), mô hình nhất quán dữ liệu (PostgreSQL Source-of-Truth & Fencing Token), và cơ chế điều phối phân tán qua Redis.

---

## 1. Hiện trạng Hệ thống (Production Status)

| Thành phần (Component) | Trạng thái hiện tại | Công nghệ vận hành | Ghi chú độ tin cậy |
| :--- | :--- | :--- | :--- |
| **Single-Node Mode** | **Production Ready** | SQLite WAL (fsync/atomic) + In-Memory Coordination | Bảo toàn 100% tương thích ngược, zero dependency |
| **Multi-Node Cluster** | **Production Ready** | PostgreSQL + Redis + S3/MinIO + Nginx/HAProxy | Đã vượt qua 10/10 Acceptance Tests đa node |
| **Persistence Storage** | **PostgreSQL (pgxpool)** | PostgreSQL 15/16 + Connection Pool + Advisory Locks | Nguồn chân lý duy nhất cho Leases, Runs, Checkpoints |
| **Rate Limiter** | **Redis Shared Limiter** | Redis + Lua Script Token Bucket & Concurrency | Giới hạn dùng chung toàn cụm, chống overshoot |
| **Agent Fencing** | **Monotonic Generation** | `claim_generation` + DB time `NOW()` | Ngăn ngừa zombie worker, split-brain và replay side-effects |
| **Cross-Node SSE** | **Redis Pub/Sub EventBus** | Durable PostgreSQL Event Log + Live Fan-Out | SSE client kết nối tới Node B vẫn nhận đủ event từ Node A |
| **Singleton Jobs** | **Distributed Leader** | Redis Locker (Token + Lua release) + Auto Failover | Golden Job, KeepAlive chỉ chạy duy nhất trên 1 Leader |
| **Shared Media** | **S3 / MinIO Storage** | S3 API + PostgreSQL Metadata + NetGuard SSRF | Lưu trữ nhị phân phân tán, cách ly tenant nghiêm ngặt |

---

## 2. Mô hình Kiến trúc Mục tiêu (Target Architecture)

```
                       Load Balancer (NGINX / HAProxy / AWS ALB)
                                          │
                 ┌────────────────────────┼────────────────────────┐
                 ↓                        ↓                        ↓
          Gateway Node A           Gateway Node B           Gateway Node C
                 │                        │                        │
                 └────────────────────────┼────────────────────────┘
                                          │
                   ┌──────────────────────┴──────────────────────┐
                   ↓                                             ↓
         PostgreSQL (Pool)                              Redis (Cluster/Standalone)
  ├─ sessions (AES-256 Vault)                    ├─ shared rate limiter (Lua)
  ├─ virtual API keys                            ├─ cross-node SSE event bus
  ├─ agent runs (Fencing tokens)                 ├─ cross-node cancel signaling
  ├─ checkpoints (Deterministic)                 ├─ singleton leader locks (Lua)
  ├─ tool execution ledger (Fenced)              └─ shared coordination state
  └─ media metadata (Tenant-isolated)
                   │
                   ↓
         S3 / MinIO Object Storage
  └─ binary assets & files (private bucket, tenant prefix)
```

---

## 3. Nguyên tắc Thiết kế Bất biến (Core Principles)

### 3.1. PostgreSQL là Source-of-Truth duy nhất cho AgentRun Ownership
- **Quyền lực sở hữu**: `worker_id`, `claim_generation`, `lease_until`, `heartbeat_at`, `status` được lưu trữ và kiểm soát độc quyền tại PostgreSQL.
- **Không phân mảnh thẩm quyền**: Tuyệt đối không dùng Redis để quyết định quyền sở hữu tác vụ Agent. Redis chỉ phục vụ thông báo đánh thức (wakeup), fan-out SSE, và hủy tác vụ.
- **Khử trôi lệch đồng hồ (Clock Skew Defense)**: Câu lệnh `ClaimRun` và `RenewLease` sử dụng thời gian của cơ sở dữ liệu `NOW()` thay vì `time.Now()` cục bộ trên các máy chủ Gateway.

### 3.2. Fencing Token đơn điệu tăng (Monotonic Fencing)
- Mỗi lần chuyển giao quyền sở hữu (Claim hoặc Recover), `claim_generation` được tăng nguyên tử:
  ```sql
  UPDATE agent_runs
  SET status = 'running',
      worker_id = $2,
      claim_generation = claim_generation + 1,
      lease_until = NOW() + make_interval(secs => $3::float8),
      heartbeat_at = NOW(),
      updated_at = NOW()
  WHERE id = $1 AND (
      status = 'queued' 
      OR (status IN ('running', 'recovering', 'waiting_for_tool') AND (lease_until IS NULL OR lease_until <= NOW()))
  )
  RETURNING claim_generation;
  ```
- Mọi thao tác ghi từ worker (`UpdateOwned`, `AppendOwnedEvent`, `RecordFinishedOwned`) bắt buộc phải chứa `worker_id` và `claim_generation`:
  ```sql
  UPDATE agent_runs SET ...
  WHERE id = $1 AND worker_id = $2 AND claim_generation = $3 AND status != 'cancelled';
  ```
- Nếu `RowsAffected == 0`, tiến trình worker lập tức nhận diện trạng thái mất quyền sở hữu (Zombie Worker), hủy bỏ context cục bộ và dừng ngay việc gọi công cụ.

### 3.3. Bảo vệ Side-Effects & Fenced Tool Execution Ledger
- Tránh trùng lặp thực thi công cụ bên ngoài (webhooks, email, file writes, git operations):
  - Trước khi gọi tool: `RecordPlannedOrRunning` kiểm tra xem worker hiện tại còn giữ quyền sở hữu run hợp lệ hay không.
  - Sau khi gọi tool: `RecordFinishedOwned` chỉ cho phép ghi nhận kết quả khi `worker_id` và `claim_generation` khớp với thời điểm đăng ký.

### 3.4. Idempotency cấp Cơ sở Dữ liệu
- Ràng buộc duy nhất từng phần (Partial Unique Index):
  ```sql
  CREATE UNIQUE INDEX idx_agent_runs_tenant_idempotency
  ON agent_runs (tenant_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND idempotency_key != '';
  ```
- Khi nhiều node nhận cùng một yêu cầu gửi tác vụ đồng thời, cơ sở dữ liệu đóng vai trò trọng tài tối cao. Node thua cuộc sẽ nhận diện conflict và trả về bản ghi AgentRun đã được tạo bởi node thắng cuộc.

---

## 4. Tầng Điều phối Phân tán Redis (Redis Coordination)

### 4.1. Distributed Locker An toàn
- Khóa phân tán lưu trữ một mã ngẫu nhiên bảo mật (Cryptographic Ownership Token) làm giá trị:
  ```go
  token := hex.EncodeToString(randomBytes(16))
  ```
- **Giải phóng khóa**: Sử dụng Lua script nguyên tử kiểm tra token để tránh một tiến trình xóa nhầm khóa của tiến trình khác khi khóa đã hết hạn:
  ```lua
  if redis.call("get", KEYS[1]) == ARGV[1] then
      return redis.call("del", KEYS[1])
  else
      return 0
  end
  ```
- **Gia hạn khóa (Heartbeat Renewal)**: Thực hiện qua Lua script nguyên tử `luaRenewLock`.

### 4.2. Shared Rate Limiter
- Sử dụng thuật toán Sliding Window Token Bucket và Concurrency Limiting được cài đặt bằng Lua script trong Redis.
- Giới hạn phân tán chính xác theo:
  - Global RPM
  - Tenant RPM
  - API Key RPM
  - Pre-auth IP Limit
  - Concurrent In-Flight Agent Runs

### 4.3. Cross-Node SSE & Real-time Cancel
- **Truyền phát SSE**: Node thực thi (Node A) ghi event vào PostgreSQL (durable history) và phát qua Redis Pub/Sub topic `agent:events:<runID>`. Node giữ kết nối SSE của client (Node B) lắng nghe topic và chuyển tiếp event tới client mà không cần sticky session.
- **Hủy tác vụ**: Khi client gửi yêu cầu cancel tới Node B, Node B cập nhật DB thành `cancelled` và phát tín hiệu tới `agent:cancel:<runID>`. Worker trên Node A nhận tín hiệu và dừng thực thi ngay lập tức.

---

## 5. Singleton Leader Jobs

Các tác vụ nền chỉ được phép chạy duy nhất trên 1 node trong toàn cụm:
- `gemini-golden-job`: Job kiểm tra sức khỏe và đồng bộ quota upstream.
- `gemini-keepalive`: Giữ ấm phiên làm việc của Google account.
- `cleanup-jobs`: Dọn dẹp tài nguyên hết hạn.

Bộ điều phối `leader.Coordinator` tự động tham gia bầu cử qua Redis Locker. Khi Node Leader gặp sự cố hoặc tắt, các Node còn lại sẽ tự động phát hiện hết hạn TTL và tiếp quản vai trò Leader trong vòng 1-2 giây.
