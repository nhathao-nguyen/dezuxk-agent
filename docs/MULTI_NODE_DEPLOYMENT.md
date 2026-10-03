# Hướng dẫn Triển khai Cụm Đa Node (Multi-Node Deployment Guide)

Tài liệu này cung cấp hướng dẫn toàn diện để triển khai Dezuxk AI Gateway ở quy mô cụm phân tán sản xuất (Production Multi-Node Cluster).

---

## 1. Yêu cầu Hạ tầng Tối thiểu (Minimum Requirements)

| Dịch vụ | Phiên bản khuyến nghị | Cấu hình tối thiểu | Vai trò trong cụm |
| :--- | :--- | :--- | :--- |
| **Gateway Nodes** | v1.0.0+ | 2+ Nodes (2 CPU, 4GB RAM / node) | Stateless HTTP/SSE API + Agent Workers |
| **PostgreSQL** | PostgreSQL 15 hoặc 16 | 2 CPU, 4GB RAM, SSD NVMe | Single Source-of-Truth: Runs, Keys, Sessions |
| **Redis** | Redis 7.0+ (Standalone / Sentinel / Cluster) | 2 CPU, 2GB RAM | Điều phối phân tán: Locks, SSE Bus, Rate Limiting |
| **S3 Storage** | AWS S3 hoặc MinIO RELEASE.2023+ | Dung lượng theo nhu cầu lưu trữ media | Binary Object Store dùng chung giữa các node |
| **Load Balancer**| NGINX / HAProxy / AWS ALB | 1 CPU, 1GB RAM | Round-Robin cân bằng tải (Không cần sticky session) |

---

## 2. Biến Môi trường Cấu hình (Environment Variables)

Hệ thống hỗ trợ nạp cấu hình qua file YAML (`configs/config.yaml`) hoặc đè bằng biến môi trường sản xuất (Secret Overrides):

### 2.1. Cụm Phân tán (Cluster & Node Identity)
```bash
# Bật chế độ cụm phân tán
DEZUXK_CLUSTER_ENABLED=true

# Định danh duy nhất cho từng node (Ví dụ: gateway-node-01, gateway-node-02)
# Nếu để trống, gateway sẽ tự sinh theo format: <hostname>-<random_hex>
DEZUXK_CLUSTER_NODE_ID=gateway-node-a
```

### 2.2. Cơ sở Dữ liệu PostgreSQL
```bash
DEZUXK_STORAGE_DRIVER=postgres
DEZUXK_POSTGRES_HOST=postgres.internal.net
DEZUXK_POSTGRES_PORT=5432
DEZUXK_POSTGRES_USER=dezuxk_admin
DEZUXK_POSTGRES_PASSWORD=your_super_secure_postgres_password
DEZUXK_POSTGRES_DBNAME=dezuxk_prod
DEZUXK_POSTGRES_SSLMODE=prefer # disable | require | verify-full
DEZUXK_POSTGRES_MAX_CONNS=25
DEZUXK_POSTGRES_MIN_CONNS=5
DEZUXK_POSTGRES_MAX_CONN_LIFETIME=1h
DEZUXK_POSTGRES_MAX_CONN_IDLE_TIME=30m
DEZUXK_POSTGRES_HEALTH_CHECK_PERIOD=1m
DEZUXK_POSTGRES_CONNECT_TIMEOUT=5s
```

### 2.3. Tầng Điều phối Redis
```bash
DEZUXK_DISTRIBUTED_ENABLED=true
DEZUXK_REDIS_ADDR=redis.internal.net:6379
DEZUXK_REDIS_PASSWORD=your_redis_auth_password
DEZUXK_REDIS_DB=0
DEZUXK_REDIS_POOL_SIZE=50
DEZUXK_REDIS_MIN_IDLE_CONNS=10
DEZUXK_REDIS_DIAL_TIMEOUT=5s
DEZUXK_REDIS_READ_TIMEOUT=3s
DEZUXK_REDIS_WRITE_TIMEOUT=3s
```

### 2.4. Lưu trữ Đa phương tiện S3 / MinIO
```bash
DEZUXK_MEDIA_DRIVER=s3
DEZUXK_S3_ENDPOINT=https://s3.us-east-1.amazonaws.com # hoặc http://minio:9000
DEZUXK_S3_BUCKET=dezuxk-media-production
DEZUXK_S3_REGION=us-east-1
DEZUXK_S3_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
DEZUXK_S3_SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
DEZUXK_S3_USE_PATH_STYLE=false # true nếu dùng MinIO
```

---

## 3. Triển khai Nhanh bằng Docker Compose

Để kiểm tra hoặc chạy toàn bộ môi trường cụm đa node cục bộ:

```bash
# 1. Khởi động hạ tầng PostgreSQL, Redis, MinIO, 3 Gateway Nodes và Nginx LB
docker compose -f docker-compose.multinode.yml up -d

# 2. Kiểm tra trạng thái sức khỏe của toàn bộ containers
docker compose -f docker-compose.multinode.yml ps

# 3. Kiểm tra độ sẵn sàng của các node
curl http://localhost:8081/ready  # Gateway Node A
curl http://localhost:8082/ready  # Gateway Node B
curl http://localhost:8083/ready  # Gateway Node C
curl http://localhost:8080/ready  # Thông qua Load Balancer Nginx
```

---

## 4. Kiểm tra Độ Sẵn sàng (Readiness Semantics)

- **Liveness Probe** (`/health`):
  - Luôn trả về HTTP 200 khi tiến trình Gateway đang sống.
  - Phù hợp cho Kubernetes liveness probe để khởi động lại container khi crash/deadlock.
- **Readiness Probe** (`/ready`):
  - Trả về HTTP 200 khi và chỉ khi:
    - Kết nối PostgreSQL Pool phản hồi `Ping(ctx)` thành công.
    - Kết nối Redis phản hồi `Ping(ctx)` thành công.
    - Các kho lưu trữ (Sessions, Keys, Runs, Checkpoints) đã được nạp hoàn chỉnh.
    - S3 Media Storage sẵn sàng (nếu bật `media.driver=s3`).
  - Nếu bất kỳ phụ thuộc cốt lõi nào bị ngắt kết nối: Trả về **HTTP 503 Service Unavailable**. Load Balancer sẽ lập tức ngừng chuyển tiếp traffic vào node bị lỗi.

---

## 5. Quy trình Tắt Dịu (Graceful Shutdown Runbook)

Khi nhận tín hiệu `SIGTERM` hoặc `SIGINT`, Gateway tuân thủ trình tự tắt dịu chuẩn:

1. Chuyển trạng thái `/ready` sang HTTP 503 để Load Balancer rút node khỏi pool phân phối.
2. Chờ 5-10 giây để các kết nối HTTP đang dở dang hoàn tất (Drain HTTP in-flight requests).
3. Ngừng tiếp nhận tác vụ Agent mới (`accepting = false`).
4. Ngừng việc tranh chấp và nhận thêm tác vụ từ hàng đợi (`ClaimRun`).
5. Đợi các Worker đang chạy hoàn tất bước hiện tại hoặc lưu checkpoint an toàn.
6. Hủy bỏ Heartbeat lease và giải phóng các khóa Singleton Leader.
7. Đóng kết nối Redis Client.
8. Đóng kết nối PostgreSQL Pool.

```mermaid
sequenceDiagram
    participant K8s as Orchestrator / LB
    participant Node as Gateway Node
    participant DB as PostgreSQL / Redis

    K8s->>Node: SIGTERM
    Node->>Node: Mark /ready = 503
    Note over K8s,Node: Drain traffic từ Load Balancer (5-10s)
    Node->>Node: Stop accepting new Agent Runs
    Node->>Node: Flush in-flight SSE events
    Node->>DB: Save checkpoints & release leader locks
    Node->>DB: Close Redis & PostgreSQL Pool
    Node->>K8s: Process exit 0
```

---

## 6. Xử lý Sự cố & Khắc phục Thảm họa (Failover Runbook)

### 6.1. Sự cố Worker Node Bị Crash (Node Crash)
- **Hành vi**: Khi Node A chết đột ngột (`kill -9` hoặc mất nguồn điện):
  1. Hạn thuê (`lease_until`) của các tác vụ do Node A nắm giữ sẽ hết hiệu lực sau thời gian cấu hình (mặc định 60 giây).
  2. Recovery Sweeper trên Node B hoặc Node C quét định kỳ sẽ phát hiện tác vụ có trạng thái `running` nhưng lease đã hết hạn.
  3. Node B thực hiện `ClaimRun` nguyên tử, nâng `claim_generation` từ $N$ lên $N+1$.
  4. Node B tải Checkpoint gần nhất từ PostgreSQL và tiếp tục chạy an toàn.

### 6.2. Hiện tượng Phân mảnh Mạng (Zombie Worker Fencing)
- **Hành vi**: Nếu Node A bị nghẽn mạng (Network Partition) khiến lease hết hạn, sau đó Node A kết nối lại và cố gắng ghi kết quả:
  - Mọi thao tác `UpdateOwned` hoặc `AppendOwnedEvent` từ Node A sử dụng generation $N$ cũ sẽ bị PostgreSQL từ chối thẳng thừng (`RowsAffected == 0`).
  - Dữ liệu hoàn toàn không bị ghi đè hay corrupt.

### 6.3. Sự cố Redis Bị Gián đoạn
- **Hành vi**:
  - Dữ liệu bền vững của Agent vẫn an toàn 100% vì PostgreSQL là thẩm quyền duy nhất.
  - Kênh SSE có thể bị gián đoạn truyền phát thời gian thực.
  - Shared Rate Limiter sẽ áp dụng chính sách an toàn (Fail-Closed) để bảo vệ hạn mức upstream Google.
