# Hướng Dẫn Triển Khai Cụm Multi-Node Production Dữ Liệu Thật (Production Real Data Setup)

Tài liệu này là hướng dẫn toàn diện dành cho DevOps, SRE và Production Engineers để triển khai, bảo mật và vận hành cụm **Dezuxk AI Gateway** trên môi trường Production với dữ liệu thật.

---

## 🏛️ 1. Kiến Trúc Cụm Sản Xuất (Production Architecture)

```text
                                DNS: ai.yourdomain.com
                                          │
                                          ▼
                                TLS / Reverse Proxy / Nginx
                                     (Port 80 / 443)
                                          │
                   ┌──────────────────────┴──────────────────────┐
                   │                                             │
                   ▼ (Độc quyền: /v1/profiles)                   ▼ (Cân bằng tải Round-Robin: /v1/chat/completions, /v1/agent/runs, /)
            Gateway Node A                                dezuxk_cluster (HA Upstream)
     [Profile/CDP Control Plane]                          ┌──────────────┴──────────────┐
     [+ AI/Chat Data Plane]                               ▼                             ▼
   (./profiles/gateway-a volume)                    Gateway Node B                Gateway Node C
            │                                     (./profiles/gateway-b)        (./profiles/gateway-c)
            │                                             │                             │
            └─────────────────────────────┬───────────────┴─────────────────────────────┘
                                          │
                   ┌──────────────────────┼──────────────────────┐
                   ▼                      ▼                      ▼
         PostgreSQL 16 (Pool)          Redis 7             AWS S3 / MinIO
       [Single Source of Truth]     [Coordination]        [Shared Media]
                   │                      │                      │
                   ▼                      ▼                      ▼
        • Sessions (AES-256-GCM)   • Distributed Locks    • Multimodal Images
        • Virtual API Keys         • Rate Limiter (Lua)   • Uploaded Documents
        • Agent Runs & Events      • Event Bus SSE Fanout • Vision Assets
        • Fencing Token Leases     • Leader Coordinator
        • Checkpoints & Memory
```

### Nguyên tắc thiết kế cốt lõi:
1. **Zero Secret Hardcoding**: Không commit bất kỳ mật khẩu, API key hay session cookie nào lên Git.
2. **Không Lưu Cookie Vào File Phẳng**: Sau khi ingest, cookie Google được mã hóa đối xứng AES-256-GCM và lưu trữ vào PostgreSQL. Không có file `cookies.json` hay `gemini_cookie.txt`.
3. **Phân tách Rạch ròi Test và Production**: File `docker-compose.multinode.yml` giữ nguyên cho CI/test. File `docker-compose.production.yml` là deployment riêng biệt cho production.
4. **Cấu hình Production Rõ ràng**: Cụm production sử dụng rõ ràng `configs/config.production.yaml` và `.env.production`. Cả 2 file này đều được gitignore an toàn.
5. **Fail-Fast Safety**: Cụm production từ chối khởi động nếu phát hiện cờ `DEZUXK_TEST_MODE=true`, thiếu khóa bảo mật, mật khẩu mặc định, hoặc cấu hình lưu trữ media cục bộ.

---

## 🛠️ 2. Hai Kiểu Hạ Tầng Triển Khai (Deployment Modes)

`docker-compose.production.yml` hỗ trợ linh hoạt 2 kiểu hạ tầng thông qua Docker Compose Profiles:

### Mode A: Tự Host Hoàn Toàn (Self-Hosted Containers)
Thích hợp cho VPS riêng biệt (Hetzner, OVH, DigitalOcean, Linode) muốn chạy toàn bộ cụm trên Docker:
- PostgreSQL 16 container (nội bộ, không mở port công khai)
- Redis 7 container (nội bộ, bảo vệ bằng password)
- MinIO S3-compatible container + service tự động tạo bucket `minio-init` (nội bộ)
- Gateway A, B, C (chạy ngầm trong Docker network `dezuxk-backend`)
- Nginx Load Balancer (chỉ mở port 80/443 ra ngoài host)

*Cách bật*: Đặt `COMPOSE_PROFILES=self-hosted` trong `.env.production`.

### Mode B: Sử Dụng Cloud Managed Services (External Infrastructure)
Thích hợp cho kiến trúc Cloud quy mô lớn (AWS, GCP, Supabase, Neon, Upstash, Cloudflare):
- **Database**: AWS Aurora PostgreSQL / RDS, Supabase, Neon
- **Distributed Cache**: AWS ElastiCache Redis, Upstash Redis, Redis Cloud
- **Object Storage**: AWS S3, Cloudflare R2, MinIO Cluster ngoài
- **Gateways**: Docker Compose chỉ khởi chạy Gateway A, B, C và Nginx (không khởi chạy postgres/redis/minio local).
- **Lưu ý S3**: Hệ thống KHÔNG tự động tạo bucket trên AWS S3 / Cloudflare R2; DevOps chịu trách nhiệm tạo bucket trước theo chính sách đám mây.

*Cách bật*: Đặt `COMPOSE_PROFILES=external` trong `.env.production` và điền endpoint của các dịch vụ cloud.

---

## 📋 3. Bảng Kiểm Tra Biến Môi Trường & Cấu Hình

Sao chép template cấu hình:
```bash
cp configs/production.env.example .env.production
cp configs/config.production.example.yaml configs/config.production.yaml
mkdir -p profiles/gateway-a profiles/gateway-b profiles/gateway-c
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
| `DEZUXK_REDIS_PASSWORD`| **Có** | Mật khẩu xác thực Redis | `redis-strong-prod-password-2026` | **CÓ** | **CÓ** |
| `DEZUXK_MEDIA_DRIVER` | **Có** | Cố định `s3` cho cụm phân tán | `s3` | Không | **CÓ** |
| `DEZUXK_S3_BUCKET` | **Có** | Tên bucket lưu trữ media | `dezuxk-prod-media` | Không | **CÓ** |
| `DEZUXK_S3_ACCESS_KEY`| **Có** | Access Key của S3/MinIO | `s3-access-key-here` | **CÓ** | **CÓ** |
| `DEZUXK_S3_SECRET_KEY`| **Có** | Secret Key của S3/MinIO | `s3-secret-key-here` | **CÓ** | **CÓ** |

---

## 🔐 4. Đồng Nhất Bảo Mật Session Qua Secret Vault

Toàn bộ phiên làm việc của Google (Cookies, CSRF Token `SNlM0e`) đều được mã hóa bằng AES-256-GCM trước khi ghi vào PostgreSQL:
1. `DEZUXK_MASTER_KEY` **BẮT BUỘC PHẢI GIỐNG NHAU** trên cả Gateway Node A, B, C.
2. Khi khởi động, Gateway in ra mã kiểm tra dấu vân tay khóa (key fingerprint):
   ```text
   [Security Vault] Đã khởi tạo AES-256-GCM Vault (vault_key_fingerprint=a1b2c3d4e5f67890)
   ```
   Nếu `vault_key_fingerprint` giữa các node trùng khớp, cụm đã đồng nhất khóa an toàn.

---

## 🔄 5. Vòng Đời Khởi Động Lần Đầu (Production First-Boot Lifecycle)

### Sơ đồ quy trình chuẩn (Deterministic First-Boot Pipeline):

```text
[Step 1-3. Tạo .env.production & configs/config.production.yaml, điền secrets]
         │
         ▼
[Step 4. Preflight chỉ kiểm tra cấu hình: ./scripts/production-preflight.sh --skip-infra]
         │
         ▼
[Step 5. Khởi động tầng dữ liệu: Postgres + Redis + MinIO + MinIO-init]
         │  (minio-init tự động tạo bucket DEZUXK_S3_BUCKET và set private)
         ▼
[Step 6. Preflight toàn diện hạ tầng: ./scripts/production-preflight.sh]
         │  (Postgres pool ping + Redis ping + MinIO S3 HeadBucket -> PASS)
         ▼
[Step 7. Khởi động toàn cụm: Gateway A/B/C + Nginx Load Balancer]
         │
         ▼
[Step 8. Kiểm tra ban đầu: /health = 200, /ready = 503]
         │  (503 là bình thường vì chưa có tài khoản Google nào trong database)
         ▼
[Step 9. Nạp tài khoản Google qua /v1/profiles (Định tuyến cố định về Gateway Node A)]
         │  (Session mã hóa AES-256-GCM lưu vào PostgreSQL làm Single Source of Truth)
         ▼
[Step 10. Model Discovery thành công tự động trên toàn cụm A/B/C]
         │
         ▼
[Step 11. /ready = 200 OK — Sẵn sàng nhận traffic /v1/chat/completions]
```

### Các bước thực hiện chi tiết:

#### Bước 1: Chuẩn bị file môi trường và file cấu hình production
```bash
# 1. Sao chép và phân quyền file biến môi trường
cp configs/production.env.example .env.production
chmod 600 .env.production

# 2. Sao chép file cấu hình production chuyên biệt
cp configs/config.production.example.yaml configs/config.production.yaml
chmod 600 configs/config.production.yaml

# 3. Tạo thư mục lưu trữ profile trình duyệt riêng biệt cho từng Gateway Node
mkdir -p profiles/gateway-a profiles/gateway-b profiles/gateway-c
```
> **BẢO MẬT:** Cả `.env.production` và `configs/config.production.yaml` đều đã được đưa vào `.gitignore`. Tuyệt đối không commit các file cấu hình thật lên Git repository.

#### Bước 2: Điền secret và cấu hình thực tế
Mở `.env.production` và `configs/config.production.yaml` để điền:
- `DEZUXK_API_KEY`: Khóa API bảo mật cho client
- `DEZUXK_MASTER_KEY`: Khóa đối xứng AES-256-GCM 32 bytes bảo vệ session Google
- `DEZUXK_ADMIN_PASSWORD` & `DEZUXK_ADMIN_SESSION_TOKEN`: Thông tin bảo vệ Web Admin
- `DEZUXK_POSTGRES_PASSWORD`, `DEZUXK_REDIS_PASSWORD`, `DEZUXK_S3_SECRET_KEY`: Thông tin tầng dữ liệu

#### Bước 3: Chạy Pre-flight kiểm tra cấu hình ban đầu (Config-only)
Trước khi hạ tầng cơ sở dữ liệu khởi động, chạy pre-flight với cờ `--skip-infra` để kiểm tra biến môi trường và cú pháp config:
```bash
./scripts/production-preflight.sh --skip-infra
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
PostgreSQL              PASS  (skipped network ping)
Redis                   PASS  (skipped network ping)
S3                      PASS  (skipped network ping)

Pre-flight verification PASSED! Hệ thống đã sẵn sàng khởi động trong môi trường Production.
```

#### Bước 4: Khởi động tầng dữ liệu và khởi tạo MinIO Bucket (Mode A: Self-Hosted)
Khởi động trước Postgres, Redis, MinIO và service khởi tạo bucket `minio-init`:
```bash
docker compose \
  --env-file .env.production \
  -f docker-compose.production.yml \
  --profile self-hosted \
  up -d postgres redis minio minio-init
```
> **Cơ chế tự động hóa MinIO**: Service `minio-init` sử dụng client `minio/mc`, chờ MinIO server sẵn sàng, tự động tạo bucket `$DEZUXK_S3_BUCKET` (nếu chưa có) và thiết lập chính sách truy cập `private` mặc định (`mc anonymous set none`). Quá trình này mang tính lũy thừa (idempotent), an toàn khi chạy lại nhiều lần.
>
> **Lưu ý External S3**: Đối với `COMPOSE_PROFILES=external`, hệ thống KHÔNG tự động tạo bucket trên AWS S3 / Cloudflare R2. DevOps chịu trách nhiệm tạo bucket trước theo chính sách bảo mật đám mây của doanh nghiệp.

#### Bước 5: Chạy Pre-flight toàn diện hạ tầng (Full Pre-flight)
Sau khi hạ tầng dữ liệu đã online, chạy kiểm tra kết nối mạng và quyền truy cập thực tế:
```bash
./scripts/production-preflight.sh
```
Lúc này script sẽ ping thực tế tới PostgreSQL, Redis và thực hiện `HeadBucket` tới S3/MinIO:
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

#### Bước 6: Khởi động toàn bộ Cụm Gateway và Nginx Load Balancer
```bash
# Đối với Self-Hosted Mode:
docker compose \
  --env-file .env.production \
  -f docker-compose.production.yml \
  --profile self-hosted \
  up -d --build

# Đối với External Cloud Mode (RDS, ElastiCache, AWS S3/R2):
COMPOSE_PROFILES=external docker compose \
  --env-file .env.production \
  -f docker-compose.production.yml \
  up -d --build
```

#### Bước 7: Kiểm tra Liveness và Readiness ban đầu
```bash
# 1. Kiểm tra Liveness (tiến trình đang chạy tốt)
curl -i http://localhost:8080/health
# Trả về: HTTP/1.1 200 OK {"status":"ok","cluster_nodes":3}

# 2. Kiểm tra Readiness
curl -i http://localhost:8080/ready
# Trả về: HTTP/1.1 503 Service Unavailable
# Lý do: Cụm mới khởi động, chưa có tài khoản Google nào được nạp vào cơ sở dữ liệu!
```

---

## 🔑 6. Quản Lý Chrome Profile & Nạp Tài Khoản Google Thật

### 🌐 Kiến Trúc Phân Định Quyền Sở Hữu Profile (Chrome Profile Ownership)

Trong mô hình đa node phía sau Load Balancer, các thao tác Chrome CDP (`/launch`, `/sync`) có ngữ nghĩa gắn liền với máy chủ cục bộ (local node semantics):
1. **Cô lập thư mục Chrome**: Mỗi Gateway Node sử dụng volume riêng biệt:
   - `gateway-a` $\rightarrow$ `./profiles/gateway-a:/app/profiles`
   - `gateway-b` $\rightarrow$ `./profiles/gateway-b:/app/profiles`
   - `gateway-c` $\rightarrow$ `./profiles/gateway-c:/app/profiles`
2. **Định tuyến điều khiển tập trung (Dedicated Profile Node)**:
   - Nginx cấu hình: mọi request `^~ /v1/profiles` được chuyển tiếp độc quyền về `gateway-a:8080`.
   - Đảm bảo lệnh `launch` và `sync` không bao giờ bị phân mảnh sang 2 node khác nhau.
3. **Phân tách Mặt phẳng Điều khiển vs Mặt phẳng Dữ liệu**:
   - **Profile/CDP Control Plane**: Single-owner trên Node A. Nếu Node A gặp sự cố, tính năng quản lý profile tạm thời gián đoạn.
   - **AI/Chat Data Plane**: Multi-node HA cân bằng tải qua toàn bộ Node A, B, C. Nếu Node A chết, traffic chat và agent run vẫn hoạt động 100% bình thường trên Node B và Node C.
4. **Cảnh báo Network Share**:
   > ⚠️ **CẢNH BÁO:** TUYỆT ĐỐI KHÔNG chia sẻ cùng một thư mục Chrome `user-data-dir` qua **NFS**, **SMB** hoặc **Shared Docker Volume** cho nhiều tiến trình Chromium chạy đồng thời. Khóa profile lockfile của Chrome sẽ gây crash hoặc corrupt dữ liệu.
   >
   > **Quy tắc:** Một Chrome profile chỉ được launch bởi một Gateway node tại một thời điểm.
5. **Nguồn Chân Lý Duy Nhất (Single Source of Truth)**: Thư mục profile Chrome cục bộ chỉ là trạng thái tạm thời của trình duyệt. Ngay sau khi đồng bộ hoặc nạp, session thật được mã hóa AES-256-GCM và lưu vào PostgreSQL. Cả 3 node lập tức truy xuất được session từ PostgreSQL mà không cần chia sẻ filesystem!

### Quy trình nạp tài khoản:

#### Flow A — Chrome Remote Debugging (CDP)
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

#### Flow B — Headless Ingest (API Trực Tiếp từ Máy Chủ)
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

Hiện tại, việc nạp secret qua `.env.production` và `configs/config.production.yaml` là phương án nhanh và an toàn nhất. Mã nguồn của Gateway đã được thiết kế sẵn sàng tích hợp với:
- **Docker Secrets**: Nạp secret file mount tại `/run/secrets/*`
- **Kubernetes Secrets**: Inject biến môi trường qua `SecretKeyRef`
- **AWS Secrets Manager**: Nạp secret lúc bootstrap pod qua IAM Role / IRSA
- **HashiCorp Vault**: Inject qua Vault Agent Sidecar

Hệ thống core không bị ràng buộc vào file `.env`, cho phép chuyển đổi phương thức lưu trữ secret bất kỳ lúc nào.
