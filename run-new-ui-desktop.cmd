@echo off
setlocal

set "PROJECT_DIR=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%PROJECT_DIR%run-new-ui-desktop.ps1" %*
exit /b %ERRORLEVEL%
