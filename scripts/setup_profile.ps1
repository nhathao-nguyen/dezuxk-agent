# ==============================================================================
# Dezuxk AI Gateway - 1-Click Profile Onboarding Script
# ==============================================================================

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = "Stop"

$BaseURL = "http://127.0.0.1:8080"
$AdminToken = "sk-dez-12f564ddef831e78546a198cd56f4deb"

Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host "   DEZUXK AI GATEWAY - THIET LAP TAI KHOAN GOOGLE GEMINI (1-CLICK)    " -ForegroundColor Yellow
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

# 1. Kiem tra trang thai Dezuxk Server
Write-Host "[1/5] Kiem tra ket noi toi Dezuxk Gateway Server ($BaseURL)... " -NoNewline
try {
    $resp = Invoke-RestMethod -Uri "$BaseURL/health" -Method Get -TimeoutSec 3 -ErrorAction Stop
    Write-Host "[OK] Server dang hoat dong!" -ForegroundColor Green
} catch {
    Write-Host "[CHUA BAT]" -ForegroundColor Red
    Write-Host "Canh bao: Dezuxk Gateway chua duoc khoi dong." -ForegroundColor Yellow
    Write-Host "Hay chay 'run.bat' hoac 'dezuxk.exe' trong terminal khac, sau do chay lai script nay." -ForegroundColor Gray
    Write-Host ""
    Read-Host "Nhan Enter de thoat..."
    exit 1
}

# 2. Nhap ten Profile va Proxy
Write-Host ""
$ProfileID = Read-Host "Nhap ten dai dien cho Profile (mac dinh: profile_1)"
if ([string]::IsNullOrWhiteSpace($ProfileID)) {
    $ProfileID = "profile_1"
}
$ProfileID = $ProfileID.Trim().ToLower() -replace "[^a-z0-9_-]", "_"

$Proxy = Read-Host "Nhap Proxy cho profile nay (Enter de bo qua neu dung mang truc tiep)"
$Proxy = $Proxy.Trim()

# 3. Tao Profile tren Gateway
Write-Host ""
Write-Host "[2/5] Dang khoi tao Profile '$ProfileID' tren he thong... " -NoNewline
$createBody = @{
    id = $ProfileID
    proxy = $Proxy
} | ConvertTo-Json

try {
    $createParams = @{
        Uri = "$BaseURL/v1/profiles"
        Method = "Post"
        Headers = @{
            "Authorization" = "Bearer $AdminToken"
            "Content-Type" = "application/json"
        }
        Body = $createBody
    }
    $createResp = Invoke-RestMethod @createParams
    Write-Host "[THANH CONG]" -ForegroundColor Green
} catch {
    Write-Host "[LOI] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

# 4. Mo trinh duyet Chrome doc lap cho Profile nay
Write-Host "[3/5] Dang mo trinh duyet Chrome cho Profile '$ProfileID'... " -NoNewline
try {
    $launchParams = @{
        Uri = "$BaseURL/v1/profiles/$ProfileID/launch"
        Method = "Post"
        Headers = @{ "Authorization" = "Bearer $AdminToken" }
    }
    $launchResp = Invoke-RestMethod @launchParams
    Write-Host "[DA MO CHROME]" -ForegroundColor Green
} catch {
    Write-Host "[LOI KHOI CHAY CHROME] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "----------------------------------------------------------------------" -ForegroundColor Magenta
Write-Host "HANH DONG CUA BAN TREN CUA SO CHROME VUA MO:" -ForegroundColor Yellow
Write-Host "  1. Dang nhap tai khoan Google cua ban." -ForegroundColor White
Write-Host "  2. Truy cap vao https://gemini.google.com/app va xac nhan da vao khung chat." -ForegroundColor White
Write-Host "----------------------------------------------------------------------" -ForegroundColor Magenta
Write-Host ""

Read-Host "Sau khi dang nhap thanh cong tren Chrome, nhan ENTER de dong bo Cookie"

# 5. Dong bo Cookie qua CDP va trich xuat CSRF Token SNlM0e
Write-Host ""
Write-Host "[4/5] Dang ket noi vao Chrome CDP va dong bo Cookie bao mat... " -NoNewline
try {
    $syncParams = @{
        Uri = "$BaseURL/v1/profiles/$ProfileID/sync"
        Method = "Post"
        Headers = @{ "Authorization" = "Bearer $AdminToken" }
    }
    $syncResp = Invoke-RestMethod @syncParams
    Write-Host "[DONG BO THANH CONG]" -ForegroundColor Green
    Write-Host "   Email: $($syncResp.email)" -ForegroundColor Cyan
    if ($syncResp.gemini_sn_token) {
        $snToken = $syncResp.gemini_sn_token
        $previewLen = [Math]::Min(16, $snToken.Length)
        Write-Host "   Gemini CSRF Token: $($snToken.Substring(0, $previewLen))..." -ForegroundColor Gray
    }
} catch {
    $errMsg = $_.Exception.Message
    if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
        try {
            $jsonErr = $_.ErrorDetails.Message | ConvertFrom-Json
            if ($jsonErr.error) { $errMsg = $jsonErr.error }
        } catch {
            $errMsg = $_.ErrorDetails.Message
        }
    } elseif ($_.Exception.Response) {
        try {
            $stream = $_.Exception.Response.GetResponseStream()
            if ($stream) {
                $reader = New-Object System.IO.StreamReader($stream)
                $body = $reader.ReadToEnd()
                $jsonErr = $body | ConvertFrom-Json
                if ($jsonErr.error) { $errMsg = $jsonErr.error }
            }
        } catch {}
    }
    Write-Host "[LOI DONG BO] $errMsg" -ForegroundColor Red
    Write-Host "Kiem tra lai cua so Chrome xem da dang nhap thanh cong vao gemini.google.com chua." -ForegroundColor Yellow
    exit 1
}

# 6. Tu dong sinh Virtual API Key
Write-Host "[5/5] Dang tao Virtual API Key cho nguoi dung... " -NoNewline
$keyBody = @{
    name = "client-$ProfileID"
    role = "user"
    rate_limit_rpm = 120
    daily_quota_requests = 2000
    allowed_models = @("*")
} | ConvertTo-Json

try {
    $keyParams = @{
        Uri = "$BaseURL/v1/admin/keys"
        Method = "Post"
        Headers = @{
            "Authorization" = "Bearer $AdminToken"
            "Content-Type" = "application/json"
        }
        Body = $keyBody
    }
    $keyResp = Invoke-RestMethod @keyParams
    Write-Host "[HOAN TAT]" -ForegroundColor Green
    $VirtualKey = $keyResp.key
} catch {
    Write-Host "[CANH BAO] Dung Master API Key mac dinh" -ForegroundColor Yellow
    $VirtualKey = $AdminToken
}

# 7. Hien thi thong so ket noi
Write-Host ""
Write-Host "======================================================================" -ForegroundColor Green
Write-Host "THIET LAP THANH CONG! BAN DA CO THE SU DUNG AI GATEWAY NGAY BAY GIO" -ForegroundColor Yellow
Write-Host "======================================================================" -ForegroundColor Green
Write-Host ""
Write-Host "THONG SO KET NOI OPENAI-COMPATIBLE CLIENT:" -ForegroundColor Cyan
Write-Host "  Base URL:  http://127.0.0.1:8080/v1" -ForegroundColor White
Write-Host "  API Key:   $VirtualKey" -ForegroundColor Yellow
Write-Host "  Models:    gemini-3.8-flash, gemini-3.1-pro, gemini-3.0-ultra" -ForegroundColor White
Write-Host ""
Write-Host "CAC TINH NANG CAO CAP HO TRO:" -ForegroundColor Cyan
Write-Host "  * Thinking Mode:     Gui kem '\"thinking\": true' trong body" -ForegroundColor Gray
Write-Host "  * Search Grounding:  Gui kem '\"grounding\": true' trong body" -ForegroundColor Gray
Write-Host "  * Code Interpreter:  Gui kem '\"code_interpreter\": true' trong body" -ForegroundColor Gray
Write-Host "  * Multimodal Vision: Gui kem anh base64 hoac URL trong message content" -ForegroundColor Gray
Write-Host ""
Write-Host "======================================================================" -ForegroundColor Green
Write-Host ""
Read-Host "Nhan Enter de hoan tat..."
