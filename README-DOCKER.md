# Hướng Dẫn Triển Khai Dezuxk AI Gateway với Docker & VPS Linux

Tài liệu này cung cấp hướng dẫn đầy đủ cách đóng gói, chạy và vận hành **Dezuxk AI Gateway** trên môi trường Docker, máy chủ nội bộ hoặc Cloud VPS (Ubuntu, Debian, CentOS, AlmaLinux).

---

## 🚀 1. Khởi Động Nhanh với Docker Compose

### Yêu Cầu Hệ Thống:
- Đã cài đặt [Docker](https://docs.docker.com/engine/install/) & [Docker Compose v2](https://docs.docker.com/compose/install/).
- Tối thiểu 1 vCPU, 1GB RAM (khuyến nghị 2GB RAM nếu chạy Chromium CDP sync).

### Các bước thực hiện:

1. **Khởi tạo thư mục dữ liệu:**
   ```bash
   mkdir -p configs storage profiles
   ```

2. **Chạy dịch vụ ngầm (Detached mode):**
   ```bash
   docker compose up -d --build
   ```

3. **Kiểm tra trạng thái container và logs:**
   ```bash
   # Xem trạng thái container
   docker compose ps

   # Theo dõi logs trực tiếp
   docker compose logs -f
   ```

4. **Truy cập Dashboard Quản Trị & Playground:**
   - Mở trình duyệt và truy cập: `http://localhost:8080/admin/` (hoặc `http://<IP_VPS>:8080/admin/`).
   - Đăng nhập với thông tin mặc định (hoặc cấu hình trong biến môi trường / `configs/config.yaml`):
     - **Tài khoản:** `admin`
     - **Mật khẩu:** `dezuxk_admin_secret_pass`

---

## 🐳 2. Chạy với Docker Trực Tiếp (Không Dùng Compose)

Nếu bạn muốn build và chạy container bằng lệnh `docker` tiêu chuẩn:

### 2.1. Build Docker Image
```bash
docker build -t dezuxk-gateway:latest .
```

### 2.2. Run Container
```bash
docker run -d \
  --name dezuxk-gateway \
  --restart unless-stopped \
  -p 8080:8080 \
  --shm-size=1g \
  --security-opt seccomp=unconfined \
  -v "$(pwd)/configs:/app/configs" \
  -v "$(pwd)/storage:/app/storage" \
  -v "$(pwd)/profiles:/app/profiles" \
  -e DEZUXK_HOST=0.0.0.0 \
  -e DEZUXK_PORT=8080 \
  -e DEZUXK_CHROME_BINARY=/usr/bin/chromium \
  -e TZ=Asia/Ho_Chi_Minh \
  dezuxk-gateway:latest
```

---

## ⚙️ 3. Danh Sách Biến Môi Trường (Environment Variables)

Dezuxk Gateway hỗ trợ cấu hình nhanh chóng qua các biến môi trường mà không cần chỉnh sửa file `config.yaml`:

| Biến Môi Trường | Giá Trị Mặc Định | Mô Tả |
| :--- | :--- | :--- |
| `DEZUXK_HOST` | `127.0.0.1` (`0.0.0.0` trong Docker) | Địa chỉ IP máy chủ lắng nghe |
| `DEZUXK_PORT` hoặc `PORT` | `8080` | Cổng HTTP lắng nghe của Gateway |
| `DEZUXK_API_KEY` | *(Tùy cấu hình)* | Master API Key dùng cho kết nối OpenAI SDK |
| `DEZUXK_MASTER_KEY` | `dezuxk-gateway-deterministic...` | Khóa mã hóa Secret Vault AES-256-GCM trên đĩa |
| `DEZUXK_CHROME_BINARY` | `/usr/bin/chromium` | Đường dẫn file thực thi trình duyệt Chrome/Chromium |
| `DEZUXK_PROFILES_DIR` | `./profiles` | Thư mục lưu trữ các profile Chrome tài khoản |
| `DEZUXK_DATABASE_PATH` | `./storage/gateway.db` | Đường dẫn cơ sở dữ liệu SQLite (WAL mode) |
| `DEZUXK_ADMIN_USERNAME`| `admin` | Tên đăng nhập Web Dashboard |
| `DEZUXK_ADMIN_PASSWORD`| `dezuxk_admin_secret_pass` | Mật khẩu đăng nhập Web Dashboard |

---

## 🔒 4. Cấu Hình Nginx Reverse Proxy & SSL (Khuyến Nghị cho VPS)

Để bảo mật với tên miền và chứng chỉ HTTPS (Let's Encrypt), hãy cấu hình Nginx phía trước container Dezuxk:

```nginx
server {
    server_name ai.yourdomain.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        # Cần thiết cho Server-Sent Events (SSE) streaming real-time
        proxy_set_header Connection '';
        proxy_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;

        # Proxy headers
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Timeout dài cho mô hình suy nghĩ lâu (Deep Thinking)
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }

    listen 80;
}
```

Sau đó cài đặt SSL miễn phí với Certbot:
```bash
sudo certbot --nginx -d ai.yourdomain.com
```

---

## 🔑 5. Quản Lý Profile Google trên VPS Headless (Không Có Màn Hình)

Khi chạy trên VPS Linux không có giao diện đồ họa (headless), có 2 flow chuẩn:

### Flow A: Điều Khiển Trình Duyệt Tự Động (Chrome Remote Debugging / CDP)
1. Tạo profile: `POST /v1/profiles` với `{"id":"acc_01"}`
2. Khởi chạy Chrome: `POST /v1/profiles/acc_01/launch`
3. Đăng nhập Google trên cửa sổ Chrome được mở.
4. Đồng bộ session: `POST /v1/profiles/acc_01/sync`
   - Gateway tự động trích xuất cookie và CSRF token `SNlM0e`.
   - Dữ liệu được mã hóa bằng AES-256-GCM qua Secret Vault và lưu trữ an toàn vào PostgreSQL.

### Flow B: Nạp Cookie Trực Tiếp qua API Ingest (Khuyến nghị cho Server Headless)
Sau khi lấy chuỗi cookie xác thực từ trình duyệt cá nhân (chứa tối thiểu `__Secure-1PSID` và `__Secure-1PSIDTS` cho Gemini, cùng `OSID` cho Flow):

```bash
curl -X POST http://<IP_GATEWAY>:8080/v1/profiles/acc_01/ingest \
  -H "Authorization: Bearer <DEZUXK_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "your_account@gmail.com",
    "cookie_str": "__Secure-1PSID=YOUR_PSID_HERE; __Secure-1PSIDTS=YOUR_PSIDTS_HERE; OSID=YOUR_OSID_HERE;",
    "user_agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
    "proxy": "http://user:pass@proxy.example.com:8080"
  }'
```

Payload chấp nhận các trường:
- `email` (string): Địa chỉ email Google của tài khoản.
- `cookie_str` (string): Chuỗi cookie định dạng thô `Key=Val; Key2=Val2;`.
- `cookies` (array/object): Mảng CDP JSON `[{"name":"...", "value":"...", "domain":"..."}]` hoặc Map JSON `{"__Secure-1PSID":"..."}`.
- `user_agent` (string, tùy chọn): User-Agent của trình duyệt nguồn.
- `proxy` (string, tùy chọn): HTTP/SOCKS5 proxy riêng cho profile này.

> **NGUYÊN TẮC BẢO MẬT BẮT BUỘC:**
> - Tuyệt đối **KHÔNG commit** cookie hoặc lưu cookie trong file `.env`.
> - Gateway không in giá trị cookie thô ra log, và không trả về cookie trong API response.
> - Sau khi nạp thành công, session được mã hóa bằng AES-256-GCM và lưu trữ bền vững trong PostgreSQL.

---

## 🛠️ 6. Quy Trình Khởi Động Multi-Node Production Dữ Liệu Thật (First-Boot)

### Bước 1: Khởi tạo file cấu hình và thư mục profile riêng biệt
`ash
# Copy template môi trường và cấu hình production
cp configs/production.env.example .env.production
cp configs/config.production.example.yaml configs/config.production.yaml

# Tạo thư mục Chrome profile riêng cho từng Gateway Node (ngăn chặn xung đột Chromium user-data-dir)
mkdir -p profiles/gateway-a profiles/gateway-b profiles/gateway-c
`

### Bước 2: Kiểm tra tiền trạm cấu hình (Config-only pre-flight)
`ash
./scripts/production-preflight.sh --skip-infra
`

### Bước 3: Khởi động tầng dữ liệu (Postgres, Redis, MinIO và minio-init)
`ash
# Service minio-init tự động tạo bucket DEZUXK_S3_BUCKET an toàn (idempotent, private)
docker compose \
  --env-file .env.production \
  -f docker-compose.production.yml \
  --profile self-hosted \
  up -d postgres redis minio minio-init
`

### Bước 4: Kiểm tra tiền trạm toàn diện hạ tầng
`ash
./scripts/production-preflight.sh
`

### Bước 5: Khởi động toàn bộ cụm Gateways và Nginx Load Balancer
`ash
docker compose \
  --env-file .env.production \
  -f docker-compose.production.yml \
  --profile self-hosted \
  up -d --build
`

### Bước 6: Theo dõi và Quản trị Cụm
`ash
# Kiểm tra trạng thái các service trong cụm
docker compose -f docker-compose.production.yml ps

# Xem logs thời gian thực
docker compose -f docker-compose.production.yml logs -f loadbalancer gateway-a

# Dừng cụm mà không mất dữ liệu bền vững (PostgreSQL, Redis data, MinIO media data)
docker compose -f docker-compose.production.yml down
`
