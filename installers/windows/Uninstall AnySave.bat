@echo off
rem Double-click to uninstall AnySave. Runs uninstall.ps1 without changing system settings.
rem Your downloads in Downloads\AnySave are kept.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0uninstall.ps1"
echo.
pause
