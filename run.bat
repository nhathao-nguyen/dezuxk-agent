@echo off
chcp 65001 >nul
title Dezuxk AI Gateway Server (Google Gemini)

echo ======================================================================
echo    🚀 KHỞI ĐỘNG DEZUXK AI GATEWAY SERVER (OPENAI COMPATIBLE)
echo ======================================================================
echo.

if not exist "storage" mkdir "storage"
if not exist "profiles" mkdir "profiles"

if not exist "dezuxk.exe" (
    echo [Build] Binary dezuxk.exe chưa tồn tại, đang tiến hành build từ source...
    go build -o dezuxk.exe .
    if errorlevel 1 (
        echo [Lỗi] Build thất bại. Hãy kiểm tra lại cài đặt Golang.
        pause
        exit /b 1
    )
    echo [Build] Build hoàn tất thành công!
    echo.
)

echo [Server] Đang khởi chạy Dezuxk Gateway tại http://127.0.0.1:8080 ...
echo [Server] Nhấn Ctrl+C để dừng server.
echo.
dezuxk.exe --config configs/config.yaml
pause
