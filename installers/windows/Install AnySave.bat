@echo off
rem Double-click to install AnySave. Runs install.ps1 without changing system settings.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1"
if errorlevel 1 (
  echo.
  echo Installation failed. Please send a screenshot of this window.
)
echo.
pause
