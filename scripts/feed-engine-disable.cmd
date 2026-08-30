@echo off
REM Double-click to turn the harvester off. Nothing else is touched: the local
REM bank, the seen-id store and the Chrome profile all stay as they are, so
REM enabling again picks up exactly where this left off.
setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-task.ps1" -Uninstall
echo.
pause
