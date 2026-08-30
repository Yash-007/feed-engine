@echo off
REM Double-click to turn the harvester on: three runs a day at odd minutes,
REM each adding its own random delay. Checks the prerequisites first.
REM
REM The task runs only while you are logged in, on purpose: the run drives a
REM visible Chrome window and cannot work on the lock screen.
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0preflight.ps1" -Strict
if errorlevel 1 (
  echo.
  echo Not enabling: fix the items above first.
  pause
  exit /b 1
)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-task.ps1"
echo.
pause
