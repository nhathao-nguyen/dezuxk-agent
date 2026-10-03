# Quy trình Phát hành và Kỷ luật Quản lý Phiên bản (Release Discipline & Process)

Tài liệu này định nghĩa quy trình chuẩn để phát hành các bản phân phối sản xuất (Production Releases) cho hệ thống **Dezuxk AI Gateway**.

---

## 1. Chiến lược Định danh Phiên bản (Semantic Versioning Strategy)

Dự án áp dụng chặt chẽ chuẩn **SemVer (MAJOR.MINOR.PATCH)**:

$$\text{v}\langle\text{MAJOR}\rangle.\langle\text{MINOR}\rangle.\langle\text{PATCH}\rangle$$

- **MAJOR (ví dụ: v1.0.0, v2.0.0)**: Tăng khi có các thay đổi phá vỡ tính tương thích ngược (Breaking API Changes), thay đổi cấu trúc cơ sở dữ liệu không thể rollback tự động, hoặc tái cấu trúc lớn ở tầng Domain/Ports.
- **MINOR (ví dụ: v0.9.0, v0.10.0)**: Tăng khi bổ sung tính năng mới (New Features), endpoint mới, hoặc nâng cấp năng lực tương thích mà vẫn đảm bảo tính tương thích ngược hoàn toàn.
- **PATCH (ví dụ: v0.9.1, v0.9.2)**: Tăng khi khắc phục lỗi (Bug Fixes), cải thiện độ ổn định, vá lỗ hổng bảo mật, hoặc tối ưu hóa hiệu năng mà không làm thay đổi hợp đồng API.

---

## 2. Tiêm Dữ liệu Phiên bản tại Thời điểm Build (Build-Time Version Injection)

Hệ thống quản lý phiên bản tập trung tại gói [`internal/version`](file:///d:/nhathao/Vibe/dezuxk-agent/internal/version/version.go). Tuyệt đối không hardcode phiên bản phân tán ở nhiều file nguồn.

Khi biên dịch artifact phát hành sản xuất, bộ tham số `-ldflags` được truyền tự động vào lệnh `go build`:

```bash
# Xác định thông tin bản build
VERSION="v0.9.1"
GIT_COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Biên dịch tệp thực thi sạch và tối ưu
go build -trimpath -ldflags "-s -w \
  -X dezuxk-gateway/internal/version.Version=${VERSION} \
  -X dezuxk-gateway/internal/version.GitCommit=${GIT_COMMIT} \
  -X dezuxk-gateway/internal/version.BuildDate=${BUILD_DATE}" \
  -o bin/dezuxk-gateway main.go
```

### Kiểm tra tính toàn vẹn của bản build:
```bash
./bin/dezuxk-gateway -version
# Hoặc truy vấn API khi máy chủ đang chạy:
curl -s http://127.0.0.1:8080/version
```

Phản hồi chuẩn JSON:
```json
{
  "version": "v0.9.1",
  "git_commit": "cc49c41",
  "build_date": "2026-10-03T15:00:00Z",
  "go_version": "go1.24.0",
  "compiler": "gc",
  "platform": "linux/amd64"
}
```

> [!NOTE]
> Phản hồi `/version` chỉ chứa siêu dữ liệu môi trường công khai, tuyệt đối không chứa cookie, mật khẩu, API key hay mã băm bí mật.

---

## 3. Danh mục Kiểm chuẩn Phát hành (Release Checklist)

Trước khi gắn thẻ git (Git Tag) và phát hành phiên bản mới, bắt buộc phải hoàn thành toàn bộ 9 bước kiểm định sau:

- [ ] **1. Production Verification Gate Green**: Workflow CI trên commit cuối cùng của nhánh `main` phải có trạng thái **SUCCESS**.
- [ ] **2. Concurrency Race Detector Green**: Chạy `go test -race -count=1 ./...` trên môi trường CGO bật (`ubuntu-latest`) và không phát hiện bất kỳ data race nào.
- [ ] **3. Govulncheck Clean**: Chạy `govulncheck ./...` và xác nhận không có lỗ hổng bảo mật nào trong chuỗi biểu tượng mã nguồn gọi đến.
- [ ] **4. Static Analysis Clean**: Chạy `gofmt -l .`, `go vet ./...` và `staticcheck ./...` không xuất hiện bất kỳ cảnh báo nào.
- [ ] **5. CHANGELOG.md Updated**: Ghi nhận đầy đủ các mục Added, Changed, Fixed, Security theo định dạng Keep a Changelog.
- [ ] **6. Version Bumped & Tagged**: Tạo tag git có chữ ký (annotated tag): `git tag -a v0.9.1 -m "Release v0.9.1"`.
- [ ] **7. Database Migrations Documented**: Kiểm tra tính tương thích ngược của schema SQLite / PostgreSQL (xem mục 4 bên dưới).
- [ ] **8. Config YAML Changes Documented**: Mọi biến cấu hình mới phải được cập nhật vào `configs/config.example.yaml` kèm chú thích chi tiết.
- [ ] **9. Gemini Live Smoke Reviewed**: Kiểm tra kết quả chạy live smoke test tự nguyện (nếu có tài khoản lab).

---

## 4. Quy trình Di chuyển Dữ liệu Cơ sở Dữ liệu (Database Migration & Upgrade Path)

Khi cập nhật lược đồ dữ liệu cơ sở dữ liệu SQLite trong sản xuất:

### 4.1. Nguyên tắc An toàn Dữ liệu (Durability Safety)
1. **Lược đồ bổ sung (Additive-only Schema Changes)**:
   - Các cột mới phải có thuộc tính `DEFAULT` hoặc `NULL` để các phiên bản cũ hoặc bản backup không bị lỗi parse.
   - Không được xóa (DROP) hoặc đổi tên (RENAME) cột trực tiếp trong phiên bản Minor/Patch mà phải qua giai đoạn Deprecation kéo dài ít nhất 1 phiên bản Minor.
2. **Quy trình Sao lưu Trước Nâng cấp (Pre-Upgrade Backup)**:
   Trước khi triển khai bản build mới, tiến hành sao lưu nóng an toàn cơ sở dữ liệu SQLite WAL:
   ```bash
   sqlite3 storage/gateway.db ".backup storage/gateway.db.backup_v0.9.0"
   ```
3. **Quy trình Phục hồi khi Sự cố (Rollback Path)**:
   Nếu bản phát hành mới gặp lỗi khởi động:
   ```bash
   # 1. Dừng tiến trình gateway
   # 2. Khôi phục tệp cơ sở dữ liệu
   cp storage/gateway.db.backup_v0.9.0 storage/gateway.db
   # 3. Chạy lại phiên bản nhị phân trước đó
   ```
