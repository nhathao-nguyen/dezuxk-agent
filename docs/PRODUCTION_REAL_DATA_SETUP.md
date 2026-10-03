# Hướng Dẫn Triển Khai Cụm Multi-Node Production Dữ Liệu Thật (Production Real Data Setup)

Tài liệu này là hướng dẫn toàn diện dành cho DevOps, SRE và Production Engineers để triển khai, bảo mật và vận hành cụm **Dezuxk AI Gateway** trên môi trường Production với dữ liệu thật.

---

## 🏛️ 1. Kiến Trúc Cụm Sản Xuất (Production Architecture)

```text
                                DNS: ai.yourdomain.com
                                          │
                                          ▼
                                TLS Termination / ALB / Nginx
                                     (Port 80 / 443)
                                          │
                     ┌────────────────────┼────────────────────┐
                     │ (Round-Robin, SSE, Keepalive, Failover) │
                     ▼                    ▼                    ▼
             Gateway Node A       Gateway Node B       Gateway Node C
             (gateway-node-a)     (gateway-node-b)     (gateway-node-c)
                     │                    │                    │
                     └────────────────────┼────────────────────┘
                                          │
                     ┌────────────────────┼────────────────────┐
                     ▼                    ▼                    ▼
           PostgreSQL 16 (Pool)        Redis 7           AWS S3 / MinIO
         [Single Source of Truth]   [Coordination]      [Shared Media]
                     │                    │
                     ▼                    ▼
          • Sessions (AES-256-GCM) • Distributed Locks
          • Virtual API Keys       • Rate Limiter (Lua)
          • Agent Runs & Events    • Event Bus SSE Fanout
          • Fencing Token Leases   • Leader Coordinator
          • Checkpoints & Memory
```

### Nguyên tắc thiết kế cốt lõi:
1. **Zero Secret Hardcoding**: Không commit bất kỳ mật khẩu, API key hay session cookie nào lên Git.
2. **Không Lưu Cookie Vào File Phẳng**: Sau khi ingest, cookie Google được mã hóa đối xứng AES-256-GCM và lưu trữ vào PostgreSQL. Không có file `cookies.json` hay `gemini_cookie.txt`.
3. **Phân tách Rạch ròi Test và Production**: File `docker-compose.multinode.yml` giữ nguyên cho CI/test. File `docker-compose.production.yml` là deployment riêng biệt cho production.
4. **Fail-Fast Safety**: Cụm production từ chối khởi động nếu phát hiện cờ `DEZUXK_TEST_MODE=true`, thiếu khóa bảo mật, mật khẩu mặc định, hoặc cấu hình lưu trữ media cục bộ.

---

## 🛠️ 2. Hai Kiểu Hạ Tầng Triển Khai (Deployment Modes)

`docker-compose.production.yml` hỗ trợ linh hoạt 2 kiểu hạ tầng thông qua Docker Compose Profiles:

### Mode A: Tự Host Hoàn Toàn (Self-Hosted Containers)
Thích hợp cho VPS riêng biệt (Hetzner, OVH, DigitalOcean, Linode) muốn chạy toàn bộ cụm trên Docker:
- PostgreSQL 16 container (nội bộ, không mở port công khai)
- Redis 7 container (nội bộ, bảo vệ bằng password)
- MinIO S3-compatible container (nội bộ)
- Gateway A, B, C (chạy ngầm trong Docker network `dezuxk-backend`)
- Nginx Load Balancer (chỉ mở port 80/443 ra ngoài host)

*Cách bật*: Đặt `COMPOSE_PROFILES=self-hosted` trong `.env.production`.

### Mode B: Sử Dụng Cloud Managed Services (External Infrastructure)
Thích hợp cho kiến trúc Cloud quy mô lớn (AWS, GCP, Supabase, Neon, Upstash, Cloudflare):
- **Database**: AWS Aurora PostgreSQL / RDS, Supabase, Neon
- **Distributed Cache**: AWS ElastiCache Redis, Upstash Redis, Redis Cloud
- **Object Storage**: AWS S3, Cloudflare R2, MinIO Cluster
- **Gateways**: Docker Compose chỉ khởi chạy Gateway A, B, C và Nginx (không khởi chạy postgres/redis/minio local).

*Cách bật*: Đặt `COMPOSE_PROFILES=external` trong `.env.production` và điền endpoint của các dịch vụ cloud.

---

## 📋 3. Bảng Kiểm Tra Biến Môi Trường (User-Fill Checklist)

Sao chép template cấu hình:
```bash
cp configs/production.env.example .env.production
```

| Biến Môi Trường | Bắt Buộc? | Lấy Ở Đâu? | Giá Trị Mẫu | Là Secret? | Giống Nhau Trên Mọi Node? |
| :--- | :---: | :--- | :--- | :---: | :---: |
| `DEZUXK_ENV` | **Có** | Cố định cho production | `production` | Không | **CÓ** |
| `DEZUXK_TEST_MODE` | **Có** | Cố định `false` cho production | `false` | Không | **CÓ** |
| `DEZUXK_HOST` | **Có** | Lắng nghe mạng nội bộ container | `0.0.0.0` | Không | **CÓ** |
| `DEZUXK_PORT` | **Có** | Cổng HTTP Gateway nội bộ | `8080` | Không | **CÓ** |
| `DEZUXK_API_KEY` | **Có** | Sinh ngẫu nhiên bảo mật (`openssl rand -hex 32`) | `sk-dez-prod-9a8b7c6d5e4f3a2b1c...` | **CÓ** | **CÓ** |
| `DEZUXK_MASTER_KEY` | **Có** | Sinh ngẫu nhiên bảo mật (`openssl rand -base64 32`) | `k8s-vault-master-sec-key-32bytes-1` | **CÓ** | **CÓ (BẮT BUỘC)** |
| `DEZUXK_METRICS_TOKEN` | Tùy chọn | Sinh ngẫu nhiên cho Prometheus scraping | `metrics-secret-token-prometheus-2026` | **CÓ** | **CÓ** |
| `DEZUXK_ADMIN_USERNAME`| **Có** | Tài khoản quản trị Web Dashboard | `admin` | Không | **CÓ** |
| `DEZUXK_ADMIN_PASSWORD`| **Có** | Mật khẩu quản trị mạnh (> 16 ký tự) | `Str0ngP@ssw0rd_Dezuxk_2026!` | **CÓ** | **CÓ** |
| `DEZUXK_ADMIN_SESSION_TOKEN` | **Có** | Token xác thực phiên đăng nhập Admin | `sec-adm-token-998877665544332211` | **CÓ** | **CÓ** |
| `DEZUXK_CLUSTER_ENABLED` | **Có** | Kích hoạt cụm đa node | `true` | Không | **CÓ** |
| `DEZUXK_CLUSTER_NODE_ID` | **Có** | Gán tự động theo service Compose | `gateway-node-a` (b, c) | Không | **KHÔNG (Mỗi node 1 ID)** |
| `DEZUXK_STORAGE_DRIVER` | **Có** | Cố định `postgres` cho cụm production | `postgres` | Không | **CÓ** |
| `DEZUXK_POSTGRES_DSN` | Tùy chọn | DSN từ nhà cung cấp Cloud (AWS/Neon/Supabase) | `postgres://user:pass@db.internal:5432/dezuxk?sslmode=require` | **CÓ** | **CÓ** |
| `DEZUXK_POSTGRES_HOST` | **Có** (*) | Host DB (`postgres` nếu Mode A, endpoint nếu Mode B) | `postgres` hoặc `rds.ap-southeast-1.amazonaws.com` | Không | **CÓ** |
| `DEZUXK_POSTGRES_PORT` | **Có** (*) | Cổng PostgreSQL | `5432` | Không | **CÓ** |
| `DEZUXK_POSTGRES_USER` | **Có** (*) | Tên người dùng database | `dezuxk_admin` | Không | **CÓ** |
| `DEZUXK_POSTGRES_PASSWORD` | **Có** (*) | Mật khẩu database PostgreSQL | `db-strong-prod-password-2026` | **CÓ** | **CÓ** |
| `DEZUXK_POSTGRES_DBNAME` | **Có** (*) | Tên database | `dezuxk` | Không | **CÓ** |
| `DEZUXK_POSTGRES_SSLMODE`| **Có** | SSL Mode (`disable` nếu docker local, `require` nếu cloud) | `disable` hoặc `require` | Không | **CÓ** |
| `DEZUXK_DISTRIBUTED_ENABLED` | **Có** | Bật cơ chế điều phối phân tán | `true` | Không | **CÓ** |
| `DEZUXK_REDIS_ADDR` | **Có** | Địa chỉ Redis (`redis:6379` nếu Mode A) | `redis:6379` hoặc `elasticache.internal:6379` | Không | **CÓ** |
| `DEZUXK_REDIS_PASSWORD`| Tùy chọn | Mật khẩu xác thực Redis | `redis-prod-secure-password-2026` | **CÓ** | **CÓ** |
| `DEZUXK_MEDIA_DRIVER` | **Có** | Bắt buộc `s3` cho multi-node cluster | `s3` | Không | **CÓ** |
| `DEZUXK_S3_ENDPOINT` | Tùy chọn | Endpoint MinIO / R2 (để trống nếu dùng AWS S3) | `http://minio:9000` hoặc `https://r2.cloudflarestorage.com` | Không | **CÓ** |
| `DEZUXK_S3_BUCKET` | **Có** | Tên bucket S3 lưu trữ media | `dezuxk-prod-media` | Không | **CÓ** |
| `DEZUXK_S3_ACCESS_KEY` | **Có** | S3 Access Key / MinIO Root User | `prod-s3-access-key-id` | **CÓ** | **CÓ** |
| `DEZUXK_S3_SECRET_KEY` | **Có** | S3 Secret Access Key / MinIO Root Pass | `prod-s3-secret-access-key` | **CÓ** | **CÓ** |
| `DEZUXK_S3_USE_PATH_STYLE` | **Có** | Bật cho MinIO/Ceph, tắt cho AWS S3 | `true` | Không | **CÓ** |
| `DEZUXK_ALLOWED_ORIGINS` | **Có** | Tên miền frontend cụ thể (CẤM `*`) | `https://ai.yourdomain.com` | Không | **CÓ** |
| `DEZUXK_TRUSTED_PROXIES` | **Có** | Subnet tin cậy chuyển tiếp IP client | `127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` | Không | **CÓ** |
| `COMPOSE_PROFILES` | **Có** | `self-hosted` hoặc `external` | `self-hosted` | Không | N/A |
| `DEZUXK_LB_PORT` | Tùy chọn | Port public host của Load Balancer | `8080` (hoặc `80`) | Không | N/A |

*Ghi chú (*):* Nếu đã cung cấp `DEZUXK_POSTGRES_DSN`, hệ thống sẽ ưu tiên dùng DSN.

---

## 🔐 4. An Toàn Master Key (Critical Master Key Safety)

`DEZUXK_MASTER_KEY` là khóa bí mật sống còn của hệ thống:
1. **Quy tắc Tính nhất quán**: Cả 3 node `gateway-a`, `gateway-b`, `gateway-c` **bắt buộc** phải dùng chung một `DEZUXK_MASTER_KEY`.
2. **Hậu quả Mất Master Key**:
   > **CẢNH BÁO QUAN TRỌNG:** Nếu mất `DEZUXK_MASTER_KEY`, toàn bộ session cookies Google/Gemini đã mã hóa trong PostgreSQL **hoàn toàn không thể khôi phục hay giải mã**! Dữ liệu backup database mà không có Master Key tương ứng là vô giá trị.
3. **Không Random Key Trong Production**: Gateway từ chối khởi động nếu thiếu Master Key trong production. Không tự sinh khóa RAM ngẫu nhiên.
4. **Kiểm tra Fingerprint**: Khi khởi động, Gateway ghi nhận log:
   ```text
   [Security Vault] Đã khởi tạo AES-256-GCM Vault (vault_key_fingerprint=a1b2c3d4e5f67890)
   ```
   Đây là mã băm SHA-256 rút gọn (non-reversible). Bạn có thể kiểm tra log của Node A, B, C: nếu `vault_key_fingerprint` trùng nhau thì cụm đã đồng nhất khóa an toàn.

---

## 🚀 5. Quy Trình Khởi Động Lần Đầu (First-Boot Flow)

```text
[1. Copy env template] ──> [2. Điền secrets thật] ──> [3. Chạy pre-flight check]
                                                                │
                                                                ▼ (PASS)
[5. /health = 200] <── [Postgres Migrations tự chạy] <── [4. docker compose up]
         │
         ▼
[6. /ready = 503] (Bình thường vì chưa có tài khoản Google)
         │
         ▼
[7. Nạp Google Profile qua CDP hoặc API /ingest]
         │
         ▼
[8. Gateway mã hóa AES-256-GCM & lưu PostgreSQL]
         │
         ▼
[9. Model discovery thành công] ──> [10. /ready = 200]
                                           │
                                           ▼
                    [11. Sẵn sàng nhận traffic /v1/chat/completions]
```

### Các bước thực hiện chi tiết:

#### Bước 1: Chuẩn bị file môi trường
```bash
cp configs/production.env.example .env.production
chmod 600 .env.production
nano .env.production
```

#### Bước 2: Chạy kiểm tra tiền trạm (Production Pre-flight)
Chạy script kiểm tra để xác thực cấu hình trước khi khởi động:
```bash
./scripts/production-preflight.sh
```
Kết quả mong muốn:
```text
=== DEZUXK PRODUCTION PRE-FLIGHT VERIFICATION ===

Environment             PASS
Test mode disabled      PASS
API Security            PASS
Vault                   PASS
Admin Security          PASS
Cluster                 PASS
Config Validation       PASS
PostgreSQL              PASS
Redis                   PASS
S3                      PASS

Pre-flight verification PASSED! Hệ thống đã sẵn sàng khởi động trong môi trường Production.
```

#### Bước 3: Khởi động cụm Production
```bash
docker compose --env-file .env.production -f docker-compose.production.yml up -d --build
```

#### Bước 4: Kiểm tra Liveness và Readiness ban đầu
```bash
# 1. Kiểm tra Liveness (tiến trình đang chạy tốt)
curl -i http://localhost:8080/health
# Trả về: HTTP/1.1 200 OK {"status":"ok","cluster_nodes":3}

# 2. Kiểm tra Readiness
curl -i http://localhost:8080/ready
# Trả về: HTTP/1.1 503 Service Unavailable
# Lý do: Cụm mới khởi động, chưa có tài khoản Google nào được nạp!
```

---

## 🔑 6. Quy Trình Nạp Tài Khoản Google Thật (Real Account Ingest)

Tuyệt đối không lưu cookie vào file `.env`. Sử dụng 1 trong 2 luồng chuẩn sau:

### Flow A — Chrome Remote Debugging (CDP)
1. Tạo profile mới:
   ```bash
   curl -X POST http://localhost:8080/v1/profiles \
     -H "Authorization: Bearer <DEZUXK_API_KEY>" \
     -H "Content-Type: application/json" \
     -d '{"id":"prod-account-01"}'
   ```
2. Khởi chạy Chrome:
   ```bash
   curl -X POST http://localhost:8080/v1/profiles/prod-account-01/launch \
     -H "Authorization: Bearer <DEZUXK_API_KEY>"
   ```
3. Đăng nhập Google trên cửa sổ Chrome mở ra và truy cập `https://gemini.google.com/app`.
4. Đồng bộ session:
   ```bash
   curl -X POST http://localhost:8080/v1/profiles/prod-account-01/sync \
     -H "Authorization: Bearer <DEZUXK_API_KEY>"
   ```
   Gateway tự động trích xuất cookies, CSRF token `SNlM0e`, mã hóa AES-256-GCM và ghi vào PostgreSQL. Cả 3 Node A/B/C lập tức dùng được tài khoản này.

### Flow B — Headless Ingest (API Trực Tiếp từ Máy Chủ)
Nếu chạy trên Cloud/VPS không có giao diện đồ họa, trích xuất cookie từ trình duyệt cá nhân và gọi API:

```bash
curl -X POST http://localhost:8080/v1/profiles/prod-account-01/ingest \
  -H "Authorization: Bearer <DEZUXK_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "primary-gemini@yourcompany.com",
    "cookie_str": "__Secure-1PSID=YOUR_PSID_VAL; __Secure-1PSIDTS=YOUR_PSIDTS_VAL; OSID=YOUR_FLOW_OSID_VAL;",
    "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
    "proxy": "http://user:pass@proxy-provider.com:8080"
  }'
```

*Sau khi nạp thành công:*
```bash
curl -i http://localhost:8080/ready
# Trả về: HTTP/1.1 200 OK
# {"status":"ready","accounts":"ok","models":"ok","storage":"ok","redis":"ok"}
```

---

## 🌐 7. Cấu Hình Tên Miền, SSL/TLS & Firewall

### 1. Phân giải DNS
Tạo bản ghi A/AAAA trỏ domain của bạn tới địa chỉ IP công khai của server VPS:
```text
ai.yourdomain.com.    IN A    203.0.113.10
```

### 2. Tường lửa (Firewall - UFW / Security Groups)
Chỉ mở công khai cổng **80** và **443**:
```bash
# Cho phép SSH và HTTP/HTTPS
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp

# Bật tường lửa
sudo ufw enable
```
> **QUAN TRỌNG:** Không bao giờ mở cổng PostgreSQL (5432), Redis (6379), MinIO (9000/9001) hay các cổng Gateway trực tiếp (8081, 8082, 8083) ra ngoài Internet. Trong `docker-compose.production.yml`, các cổng này đã được cấu hình chạy nội bộ trong network `dezuxk-backend`.

### 3. Cấu hình SSL miễn phí với Let's Encrypt (Certbot)
Nếu cài Nginx trên Host OS làm reverse proxy trước Docker:
```bash
sudo apt update && sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d ai.yourdomain.com
```

---

## 💾 8. Chính Sách Sao Lưu & Phục Hồi (Backup & Disaster Recovery Policy)

### Các thành phần BẮT BUỘC sao lưu:
1. **Cơ sở dữ liệu PostgreSQL**: Chứa toàn bộ phiên tài khoản (đã mã hóa), Virtual API keys, Agent runs, Tool execution ledger, Checkpoints và Memory.
2. **Khóa `DEZUXK_MASTER_KEY`**: Bắt buộc lưu trữ trong Password Manager hoặc Secret Manager chuyên dụng (1Password, Bitwarden, AWS Secrets Manager, HashiCorp Vault).
3. **S3 Object Bucket**: Chứa các tệp media nhị phân (hình ảnh, tài liệu multimodal).

> **LƯU Ý SRE:** Redis **KHÔNG** cần sao lưu bền vững vì Redis chỉ đóng vai trò phân tán khóa (Distributed Locks), Rate Limit tạm thời và Pub/Sub event bus. Khi cụm restart, dữ liệu bền vững được nạp lại hoàn toàn từ PostgreSQL.

### Lệnh sao lưu PostgreSQL:
```bash
# Tạo bản sao lưu nén gzip
docker exec -t dezuxk-prod-postgres pg_dump -U dezuxk -d dezuxk | gzip > "backup_dezuxk_$(date +%Y%m%d_%H%M%S).sql.gz"
```

### Quy trình phục hồi (Restore Procedure):
1. Khởi tạo cụm mới với `docker-compose.production.yml`.
2. Đảm bảo cấu hình biến `DEZUXK_MASTER_KEY` **CHÍNH XÁC** bằng khóa ban đầu đã dùng khi mã hóa.
3. Phục hồi dữ liệu SQL vào PostgreSQL:
   ```bash
   gunzip -c backup_dezuxk_20261004.sql.gz | docker exec -i dezuxk-prod-postgres psql -U dezuxk -d dezuxk
   ```
4. Khởi động lại các Gateway node:
   ```bash
   docker compose -f docker-compose.production.yml restart gateway-a gateway-b gateway-c
   ```
5. Kiểm tra `/ready` $\rightarrow$ 200 OK. Toàn bộ phiên tài khoản và Virtual API keys được giải mã tức thì.

---

## 🔒 9. Tích Hợp Quản Lý Bí Mật Ngoài (Future Secrets Providers)

Hiện tại, việc nạp secret qua `.env.production` là phương án nhanh nhất. Mã nguồn của Gateway đã được thiết kế sẵn sàng tích hợp với:
- **Docker Secrets**: Nạp secret file mount tại `/run/secrets/*`
- **Kubernetes Secrets**: Inject biến môi trường qua `SecretKeyRef`
- **AWS Secrets Manager**: Nạp secret lúc bootstrap pod qua IAM Role / IRSA
- **HashiCorp Vault**: Inject qua Vault Agent Sidecar

Hệ thống core không bị ràng buộc vào file `.env`, cho phép chuyển đổi phương thức lưu trữ secret bất kỳ lúc nào.
