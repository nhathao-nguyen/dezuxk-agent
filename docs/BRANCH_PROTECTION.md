# Branch Protection & Merge Governance Policy

## 1. Mục tiêu và Nguyên tắc Quản trị Nhánh (Governance Rules)

Để đảm bảo tính toàn vẹn và độ tin cậy của mã nguồn trên nhánh chính (`main` / `master`), hệ thống CI/CD áp dụng quy tắc kiểm thử tự động nghiêm ngặt thông qua **Production Verification Gate**. 

Tuyệt đối cấm hợp nhất mã nguồn vào nhánh chính nếu chưa vượt qua đầy đủ các bài kiểm tra chất lượng (code style, static analysis, vulnerability scan, unit/integration tests và concurrency race detection).

---

## 2. Chính sách Bảo vệ Nhánh Bắt buộc (Mandatory Branch Protection Policy)

Áp dụng cho nhánh: `main` (hoặc `master`):

| Cài đặt (Setting) | Giá trị yêu cầu | Mục đích kỹ thuật |
| :--- | :--- | :--- |
| **Require a pull request before merging** | **Bật (Enabled)** | Cấm push trực tiếp (`git push origin main`), buộc phải qua Pull Request kiểm duyệt |
| **Require approvals** | **Tối thiểu 1** | Đảm bảo code review kỹ lưỡng trước khi đưa vào sản xuất |
| **Require status checks to pass before merging** | **Bật (Enabled)** | Khóa merge nếu CI chưa hoàn toàn xanh |
| **Status check name** | `Production Verification Gate` / `verify` | Định danh workflow kiểm chuẩn bất biến |
| **Require branches to be up to date before merging** | **Bật (Enabled)** | Ngăn chặn merge drift khi nhánh `main` đã có commit mới |
| **Do not allow bypassing the above settings** | **Bật (Enabled)** | Áp dụng bình đẳng cho cả Repository Administrators / Owners |
| **Block force pushes** | **Bật (Enabled)** | Chống ghi đè lịch sử commit (`git push --force`) gây mất dấu kiểm toán |
| **Block branch deletions** | **Bật (Enabled)** | Ngăn chặn vô tình xóa nhánh sản xuất `main` |

---

## 3. Hướng dẫn Kích hoạt qua Giao diện GitHub Web (Step-by-step UI)

1. Truy cập Repository: [https://github.com/nhathao-nguyen/dezuxk-agent](https://github.com/nhathao-nguyen/dezuxk-agent)
2. Chọn **Settings** (biểu tượng bánh răng) trên thanh điều hướng của repository.
3. Trong thanh bên trái, chọn **Branches** (nằm dưới mục *Code and automation*).
4. Nhấn nút **Add branch ruleset** hoặc **Add branch protection rule**.
5. Cấu hình các mục theo đúng quy chuẩn:
   - **Branch name pattern**: `main` (hoặc `master`)
   - Đánh dấu chọn:
     - `[x] Require a pull request before merging`
     - `[x] Require approvals` (số lượng: 1)
     - `[x] Require status checks to pass before merging`
       - Trong ô tìm kiếm status checks, tìm và chọn: `verify` (thuộc job *Production Verification Gate*)
     - `[x] Require branches to be up to date before merging`
     - `[x] Do not allow bypassing the above settings` (Include administrators)
     - `[x] Restrict force pushes`
     - `[x] Restrict deletions`
6. Nhấn **Save changes** (hoặc **Create**) và xác nhận mật khẩu/2FA nếu được yêu cầu.

---

## 4. Cấu hình tự động qua GitHub CLI (`gh`)

Nếu có tài khoản có quyền Quản trị viên (Repo Admin) với GitHub CLI đã đăng nhập (`gh auth login`):

```bash
gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  /repos/nhathao-nguyen/dezuxk-agent/branches/main/protection \
  --input - << 'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "verify"
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false,
    "required_approving_review_count": 1
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

---

## 5. Workflow Tương thích với Required Status Check

Workflow [.github/workflows/ci.yml](file:///.github/workflows/ci.yml) được cố định với định danh:
- Workflow Name: `Production Verification Gate`
- Job Identifier: `verify`
- Job Display Name: `Lint, Test & Race Verification`
- Triggers:
  ```yaml
  on:
    push:
      branches: [ main, master ]
    pull_request:
      branches: [ main, master ]
  ```

Tên của status check trong GitHub Status Context là `verify`. Khi kích hoạt protection rule, GitHub sẽ khóa nút Merge của mọi Pull Request cho đến khi check `verify` chuyển sang trạng thái xanh (Success).
