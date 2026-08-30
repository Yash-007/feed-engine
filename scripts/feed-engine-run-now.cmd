@echo off
REM Double-click to run one harvest immediately, in this window, with no start
REM jitter. A Chrome window will open and scroll; leave it alone until it exits.
REM
REM This does not need the scheduled task to be registered.
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0preflight.ps1" -Strict
if errorlevel 1 (
  echo.
  echo Not running: fix the items above first.
  pause
  exit /b 1
)
echo.
echo Running a full harvest. This takes a few minutes.
echo.
"%~dp0..\bin\feed-engine.exe" -no-jitter
echo.
pause
