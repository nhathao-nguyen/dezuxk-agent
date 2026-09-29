# ==============================================================================
# Dezuxk AI Gateway - 1-Click Server Runner
# ==============================================================================

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = "Stop"

Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host "   🚀 KHỞI ĐỘNG DEZUXK AI GATEWAY SERVER (OPENAI COMPATIBLE)         " -ForegroundColor Yellow
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

if (-not (Test-Path "storage")) { New-Item -ItemType Directory -Path "storage" | Out-Null }
if (-not (Test-Path "profiles")) { New-Item -ItemType Directory -Path "profiles" | Out-Null }

if (-not (Test-Path "dezuxk.exe")) {
    Write-Host "[Build] Binary dezuxk.exe chưa tồn tại, đang tiến hành build từ source..." -ForegroundColor Yellow
    go build -o dezuxk.exe .
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[Lỗi] Build thất bại. Hãy kiểm tra lại môi trường Go." -ForegroundColor Red
        exit 1
    }
    Write-Host "[Build] Build hoàn tất thành công!" -ForegroundColor Green
    Write-Host ""
}

Write-Host "[Server] Đang khởi chạy Dezuxk Gateway tại http://127.0.0.1:8080 ..." -ForegroundColor Green
Write-Host "[Server] Nhấn Ctrl+C để dừng server." -ForegroundColor Gray
Write-Host ""

.\dezuxk.exe --config configs/config.yaml
