# ==============================================================================
# Dezuxk AI Gateway - 1-Click Profile Onboarding Script
# Tự động hóa tạo Profile Chrome, Mở đăng nhập, Trích xuất Cookie và Tạo API Key
# ==============================================================================

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = "Stop"

$BaseURL = "http://127.0.0.1:8080"
$AdminToken = "dezuxk_secure_admin_session_token_2026"

Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host "   🚀 DEZUXK AI GATEWAY - THIẾT LẬP TÀI KHOẢN GOOGLE GEMINI (1-CLICK) " -ForegroundColor Yellow
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

# 1. Kiểm tra trạng thái Dezuxk Server
Write-Host "[1/5] Kiểm tra kết nối tới Dezuxk Gateway Server ($BaseURL)..." -NoNewline
try {
    $resp = Invoke-RestMethod -Uri "$BaseURL/health" -Method Get -TimeoutSec 3 -ErrorAction Stop
    Write-Host " [OK] Server đang hoạt động!" -ForegroundColor Green
} catch {
    Write-Host " [CHƯA BẬT]" -ForegroundColor Red
    Write-Host "⚠️  Dezuxk Gateway chưa được khởi động." -ForegroundColor Yellow
    Write-Host "👉 Hãy chạy 'run.bat' hoặc 'go run main.go' trong một terminal khác, sau đó chạy lại script này." -ForegroundColor Gray
    Write-Host ""
    Read-Host "Nhấn Enter để thoát..."
    exit 1
}

# 2. Nhập tên Profile và Proxy
Write-Host ""
$ProfileID = Read-Host "Nhập tên đại diện cho Profile (mặc định: profile_1)"
if ([string]::IsNullOrWhiteSpace($ProfileID)) {
    $ProfileID = "profile_1"
}
$ProfileID = $ProfileID.Trim().ToLower() -replace "[^a-z0-9_-]", "_"

$Proxy = Read-Host "Nhập Proxy cho profile này (Enter để bỏ qua nếu dùng mạng trực tiếp)"
$Proxy = $Proxy.Trim()

# 3. Tạo Profile trên Gateway
Write-Host ""
Write-Host "[2/5] Đang khởi tạo Profile '$ProfileID' trên hệ thống..." -NoNewline
$createBody = @{
    id = $ProfileID
    proxy = $Proxy
} | ConvertTo-Json

try {
    $createResp = Invoke-RestMethod -Uri "$BaseURL/v1/profiles" -Method Post `
        -Headers @{ "Authorization" = "Bearer $AdminToken"; "Content-Type" = "application/json" } `
        -Body $createBody
    Write-Host " [THÀNH CÔNG]" -ForegroundColor Green
} catch {
    Write-Host " [LỖI] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

# 4. Mở trình duyệt Chrome độc lập cho Profile này
Write-Host "[3/5] Đang mở trình duyệt Chrome cho Profile '$ProfileID'..." -NoNewline
try {
    $launchResp = Invoke-RestMethod -Uri "$BaseURL/v1/profiles/$ProfileID/launch" -Method Post `
        -Headers @{ "Authorization" = "Bearer $AdminToken" }
    Write-Host " [ĐÃ MỞ CHROME]" -ForegroundColor Green
} catch {
    Write-Host " [LỖI KHỞI CHẠY CHROME] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "----------------------------------------------------------------------" -ForegroundColor Magenta
Write-Host "👉 HÀNH ĐỘNG CỦA BẠN TRÊN CỬA SỔ CHROME VỪA MỞ:" -ForegroundColor Yellow
Write-Host "   1. Đăng nhập tài khoản Google của bạn." -ForegroundColor White
Write-Host "   2. Truy cập vào https://gemini.google.com/app và xác nhận đã vào được khung chat." -ForegroundColor White
Write-Host "----------------------------------------------------------------------" -ForegroundColor Magenta
Write-Host ""

Read-Host "👉 Sau khi đăng nhập thành công trên Chrome, nhấn ENTER tại đây để đồng bộ Cookie"

# 5. Đồng bộ Cookie qua CDP và trích xuất CSRF Token SNlM0e
Write-Host ""
Write-Host "[4/5] Đang kết nối vào Chrome CDP và đồng bộ Cookie bảo mật..." -NoNewline
try {
    $syncResp = Invoke-RestMethod -Uri "$BaseURL/v1/profiles/$ProfileID/sync" -Method Post `
        -Headers @{ "Authorization" = "Bearer $AdminToken" }
    Write-Host " [ĐỒNG BỘ THÀNH CÔNG]" -ForegroundColor Green
    Write-Host "   📧 Email: $($syncResp.email)" -ForegroundColor Cyan
    Write-Host "   🔑 Gemini CSRF Token: $($syncResp.gemini_sn_token.Substring(0, [Math]::Min(16, $syncResp.gemini_sn_token.Length)))..." -ForegroundColor Gray
} catch {
    Write-Host " [LỖI ĐỒNG BỘ] $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "⚠️ Hãy kiểm tra lại cửa sổ Chrome xem đã đăng nhập hoàn tất vào gemini.google.com chưa." -ForegroundColor Yellow
    exit 1
}

# 6. Tự động sinh Virtual API Key
Write-Host "[5/5] Đang tạo Virtual API Key cho người dùng..." -NoNewline
$keyBody = @{
    name = "client-$ProfileID"
    role = "user"
    rate_limit_rpm = 120
    daily_quota_requests = 2000
    allowed_models = @("*")
} | ConvertTo-Json

try {
    $keyResp = Invoke-RestMethod -Uri "$BaseURL/v1/admin/keys" -Method Post `
        -Headers @{ "Authorization" = "Bearer $AdminToken"; "Content-Type" = "application/json" } `
        -Body $keyBody
    Write-Host " [HOÀN TẤT]" -ForegroundColor Green
    $VirtualKey = $keyResp.key
} catch {
    Write-Host " [CẢNH BÁO TẠO KEY] Dùng Master API Key mặc định" -ForegroundColor Yellow
    $VirtualKey = "dezuxk_admin_secret_key_2026"
}

# 7. Hiển thị thông số kết nối
Write-Host ""
Write-Host "======================================================================" -ForegroundColor Green
Write-Host "🎉 THIẾT LẬP THÀNH CÔNG! BẠN ĐÃ CÓ THỂ SỬ DỤNG AI GATEWAY NGAY BÂY GIỜ" -ForegroundColor Yellow
Write-Host "======================================================================" -ForegroundColor Green
Write-Host ""
Write-Host "THÔNG SỐ KẾT NỐI OPENAI-COMPATIBLE CLIENT:" -ForegroundColor Cyan
Write-Host "  🌐 Base URL:  http://127.0.0.1:8080/v1" -ForegroundColor White
Write-Host "  🔑 API Key:   $VirtualKey" -ForegroundColor Yellow
Write-Host "  🤖 Models:    gemini-3.8-flash, gemini-3.1-pro, gemini-3.0-ultra" -ForegroundColor White
Write-Host ""
Write-Host "CÁC TÍNH NĂNG CAO CẤP HỖ TRỢ:" -ForegroundColor Cyan
Write-Host "  • Thinking Mode:      Gửi kèm '\"thinking\": true' trong body" -ForegroundColor Gray
Write-Host "  • Search Grounding:   Gửi kèm '\"grounding\": true' trong body" -ForegroundColor Gray
Write-Host "  • Code Interpreter:   Gửi kèm '\"code_interpreter\": true' trong body" -ForegroundColor Gray
Write-Host "  • Multimodal Vision:  Gửi kèm ảnh base64 hoặc URL trong message content" -ForegroundColor Gray
Write-Host ""
Write-Host "======================================================================" -ForegroundColor Green
Write-Host ""
Read-Host "Nhấn Enter để hoàn tất..."
