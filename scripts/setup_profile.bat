@echo off
chcp 65001 >nul
title Thiết lập Profile Google Gemini - Dezuxk AI Gateway
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0setup_profile.ps1"
pause
