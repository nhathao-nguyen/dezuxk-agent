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

Khi chạy trên VPS Linux không có giao diện đồ họa (headless), có 2 cách để nạp Cookie và kích hoạt tài khoản:

### Cách 1: Nạp Cookie Trực Tiếp qua API (Khuyến nghị cho Headless)
Sau khi lấy cookie `__Secure-1PSID` và `__Secure-1PSIDTS` từ trình duyệt của bạn (dùng DevTools F12 trên máy tính cá nhân):
```bash
curl -X POST http://<IP_VPS>:8080/v1/profiles/acc_01/ingest \
  -H "Authorization: Bearer <MASTER_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "cookies": "__Secure-1PSID=...; __Secure-1PSIDTS=...;",
    "set_default": true
  }'
```

### Cách 2: Nhập Cookie Trực Tiếp trên Web Dashboard UI
1. Truy cập `http://<IP_VPS>:8080/admin/`
2. Chọn tab **"Profiles & CDP"** $\rightarrow$ bấm **"+ Thêm Profile Mới"**.
3. Điền Account ID và dán chuỗi Cookie $\rightarrow$ Bấm **"Lưu & Kích Hoạt"**. Hệ thống sẽ tự động handshake và trích xuất token `SNlM0e`.

---

## 🛠️ 6. Các Lệnh Vận Hành Thường Dùng

```bash
# Xem logs container liên tục
docker compose logs -f dezuxk-gateway

# Khởi động lại Gateway
docker compose restart dezuxk-gateway

# Dừng Gateway mà không mất dữ liệu
docker compose down

# Cập nhật code mới nhất và build lại
git pull
docker compose up -d --build
```
