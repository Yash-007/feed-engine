@echo off
REM Double-click to see whether the harvester is set up, when it last ran, and
REM when it runs next. Changes nothing.
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0preflight.ps1"
echo.
echo Last 20 log lines:
echo.
powershell -NoProfile -ExecutionPolicy Bypass -Command "if (Test-Path '.\data\run.log') { Get-Content '.\data\run.log' -Tail 20 } else { 'no run.log yet' }"
echo.
pause
