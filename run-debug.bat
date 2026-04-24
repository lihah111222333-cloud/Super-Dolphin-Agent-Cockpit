@echo off
setlocal EnableDelayedExpansion
REM ============================================================
REM  run-debug.bat - Windows compile & run for agent-terminal
REM  Simplified port of run-debug.sh:
REM    - no Frida (not supported on Windows)
REM    - no git worktree / git tag selection
REM    - no codemap / code-size-guard pre-flights (run via Makefile
REM      on WSL or the shell script if you need them)
REM  Requires: Go toolchain, Node.js (with npm + npx), curl
REM ============================================================

set "PROJECT_DIR=%~dp0"
if "%PROJECT_DIR:~-1%"=="\" set "PROJECT_DIR=%PROJECT_DIR:~0,-1%"
set "FRONTEND_DIR=%PROJECT_DIR%\cmd\agent-terminal\frontend"
set "BUILD_DIR=%PROJECT_DIR%"
set "GO_GUARD_ALLOW_RAW=run-debug.bat"
set "NPM_REGISTRY=https://registry.npmmirror.com"
set "VITE_DEV_URL="

if "%ENABLE_MEMORY_SYSTEM%"=="" set "ENABLE_MEMORY_SYSTEM=1"
if "%ENABLE_MEMORY_TOOLS%"=="" set "ENABLE_MEMORY_TOOLS=1"
if "%MULTI_AGENT_MEMORY_FEATURE_TEAMMEM%"=="" set "MULTI_AGENT_MEMORY_FEATURE_TEAMMEM=1"

echo.
echo +----------------------------------+
echo ^|    agent-terminal compile tool   ^|
echo +----------------------------------+
echo.
echo   [1] debug compile (no Frida - Windows default)
echo   [2] debug server mode (browser -^> http://localhost:4511)
echo   [3] normal compile (release)
echo   [4] run already-compiled binary (debug + vite HMR)
echo.
set /p choice=Select (1/2/3/4):

set "MODE="
set "USE_SERVER=0"
set "LABEL="
if "%choice%"=="1" ( set "MODE=debug" & set "LABEL=debug - no Frida" )
if "%choice%"=="2" ( set "MODE=debug" & set "USE_SERVER=1" & set "LABEL=debug server" )
if "%choice%"=="3" ( set "MODE=normal" & set "LABEL=release" )
if "%choice%"=="4" ( set "MODE=run-only" & set "LABEL=run-only - debug" )

if "%MODE%"=="" (
    echo [X] invalid choice
    exit /b 1
)

echo.
echo ^> mode: !LABEL!
echo ^> dir : %BUILD_DIR%
echo ------------------------------------

REM ============================================================
REM run-only: skip compile, start binary directly
REM ============================================================
if "%MODE%"=="run-only" (
    if not exist "%BUILD_DIR%\super-agent-debug.exe" (
        echo [X] binary not found: super-agent-debug.exe
        echo     please run option 1/2/3 first to build
        exit /b 1
    )
    echo [1/3] stopping old processes...
    call :stop_old_processes
    echo [2/3] clearing webview cache...
    call :clear_webview_cache
    echo [3/3] starting vite dev server...
    call :start_vite_dev
    echo.
    echo ====================================
    echo ^> launching already-compiled binary (debug)...
    if defined VITE_DEV_URL echo ^> frontend HMR -^> !VITE_DEV_URL!
    start "" /b cmd /c "timeout /t 1 /nobreak >nul && start http://localhost:4511"
    "%BUILD_DIR%\super-agent-debug.exe" --debug %*
    set "AGENT_EXIT=!ERRORLEVEL!"
    call :cleanup_vite
    exit /b !AGENT_EXIT!
)

REM ============================================================
REM 1) Frontend
REM ============================================================
if exist "%FRONTEND_DIR%\package.json" (
    echo [1/4] frontend processing...
    pushd "%FRONTEND_DIR%"
    if not exist "node_modules" (
        echo   -^> npm ci ^(first install^)...
        call npm ci --registry=%NPM_REGISTRY%
        if errorlevel 1 (
            echo [X] npm ci failed
            popd
            exit /b 1
        )
    ) else (
        echo   -^> node_modules exists, skipping install
    )

    if exist "scripts\check-templates.cjs" (
        echo   -^> pre-check Vue templates ^(runtime compiler^)...
        call node scripts\check-templates.cjs
        if errorlevel 1 (
            echo.
            echo   [!] Vue template pre-check failed - webview may show blank screen
            echo       Press Enter to ignore and continue, Ctrl+C to abort
            pause >nul
            echo   -^> template guard bypassed
        )
    )

    if "%MODE%"=="debug" (
        REM Kill any process holding port 5173 so strictPort succeeds.
        for /f "tokens=5" %%a in ('netstat -ano ^| findstr :5173 ^| findstr LISTENING') do taskkill /F /PID %%a >nul 2>&1
        echo   -^> starting vite dev server ^(port 5173^)...
        start "vite-dev" /b cmd /c "npx vite --port 5173 --strictPort"
        REM Wait for vite to become ready.
        set "VITE_DEV_URL="
        for /l %%i in (1,1,30) do (
            if not defined VITE_DEV_URL (
                curl -s http://localhost:5173 >nul 2>&1
                if not errorlevel 1 set "VITE_DEV_URL=http://localhost:5173"
            )
            if not defined VITE_DEV_URL timeout /t 1 /nobreak >nul
        )
        if defined VITE_DEV_URL (
            echo   -^> vite dev server ready [OK]
        ) else (
            echo   [!] vite failed to start; will fall back to dist/ static assets
            echo       ^(a stale dist/ may cause a blank webview^)
        )
    ) else (
        echo   -^> npm run build...
        call npm run build
        if errorlevel 1 (
            echo.
            echo [!] frontend build failed. Press Enter to skip and continue, Ctrl+C to abort
            pause >nul
            echo   -^> frontend error bypassed
        )
    )
    popd
) else (
    echo [1/4] skipping frontend ^(no package.json^)
)

REM ============================================================
REM 2) Webview cache cleanup
REM   Wails v3 on Windows stores WebView2 user data under
REM   %LOCALAPPDATA%\<app_name>. Kill both the friendly and the
REM   bundle-id-style directories like the macOS sibling script does.
REM ============================================================
echo [2/4] clearing webview cache...
call :clear_webview_cache

REM ============================================================
REM 3) Backend build
REM ============================================================
pushd "%BUILD_DIR%"
echo [3/4] compiling backend (%MODE%)...
if "%MODE%"=="debug" (
    go build -gcflags="-N -l" -o super-agent-debug.exe .\cmd\agent-terminal\
    if errorlevel 1 goto build_failed
    go build -gcflags="-N -l" -o mcp-orch.exe .\cmd\mcp-orch\
    if errorlevel 1 goto build_failed
    go build -gcflags="-N -l" -o mcp-lsp.exe .\cmd\mcp-lsp\
    if errorlevel 1 goto build_failed
) else (
    go build -o super-agent-debug.exe .\cmd\agent-terminal\
    if errorlevel 1 goto build_failed
    go build -o mcp-orch.exe .\cmd\mcp-orch\
    if errorlevel 1 goto build_failed
    go build -o mcp-lsp.exe .\cmd\mcp-lsp\
    if errorlevel 1 goto build_failed
)
echo   [OK] super-agent-debug.exe
echo   [OK] mcp-orch.exe
echo   [OK] mcp-lsp.exe

REM ============================================================
REM 4) Stop old processes
REM ============================================================
echo [4/4] stopping old processes...
call :stop_old_processes
popd

REM ============================================================
REM Launch
REM ============================================================
echo.
echo ====================================
if "%MODE%"=="debug" (
    if defined VITE_DEV_URL (
        echo ^> launching debug mode ^(frontend HMR -^> !VITE_DEV_URL!^)...
    ) else (
        echo ^> launching debug mode...
    )
    start "" /b cmd /c "timeout /t 1 /nobreak >nul && start http://localhost:4511"
    "%BUILD_DIR%\super-agent-debug.exe" --debug %*
) else (
    echo ^> launching release mode...
    "%BUILD_DIR%\super-agent-debug.exe" %*
)
set "AGENT_EXIT=!ERRORLEVEL!"
call :cleanup_vite
exit /b !AGENT_EXIT!

:build_failed
echo [X] backend compile failed
popd
call :cleanup_vite
exit /b 1

REM ============================================================
REM Subroutines
REM ============================================================
:stop_old_processes
taskkill /F /IM super-agent-debug.exe /T >nul 2>&1
taskkill /F /IM mcp-orch.exe /T >nul 2>&1
taskkill /F /IM mcp-lsp.exe /T >nul 2>&1
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :4510 ^| findstr LISTENING') do taskkill /F /PID %%a >nul 2>&1
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :4511 ^| findstr LISTENING') do taskkill /F /PID %%a >nul 2>&1
goto :eof

:clear_webview_cache
if exist "%LOCALAPPDATA%\agent-terminal" rmdir /s /q "%LOCALAPPDATA%\agent-terminal" >nul 2>&1
if exist "%LOCALAPPDATA%\com.multi-agent.agent-terminal" rmdir /s /q "%LOCALAPPDATA%\com.multi-agent.agent-terminal" >nul 2>&1
goto :eof

:start_vite_dev
set "RUNONLY_FRONT=%BUILD_DIR%\cmd\agent-terminal\frontend"
if not exist "%RUNONLY_FRONT%\node_modules" (
    echo   -^> skipping vite ^(missing node_modules^); dist/ fallback
    goto :eof
)
if not exist "%RUNONLY_FRONT%\package.json" (
    echo   -^> skipping vite ^(missing package.json^); dist/ fallback
    goto :eof
)
if exist "%RUNONLY_FRONT%\scripts\check-templates.cjs" (
    echo   -^> pre-check Vue templates...
    pushd "%RUNONLY_FRONT%"
    call node scripts\check-templates.cjs
    popd
)
echo   -^> starting vite dev server ^(port 5173^)...
pushd "%RUNONLY_FRONT%"
start "vite-dev" /b cmd /c "npx vite --port 5173 --strictPort"
popd
for /l %%i in (1,1,30) do (
    if not defined VITE_DEV_URL (
        curl -s http://localhost:5173 >nul 2>&1
        if not errorlevel 1 (
            set "VITE_DEV_URL=http://localhost:5173"
            echo   -^> vite dev server ready [OK]
        )
    )
    if not defined VITE_DEV_URL timeout /t 1 /nobreak >nul
)
if not defined VITE_DEV_URL echo   [!] vite failed to start
goto :eof

:cleanup_vite
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :5173 ^| findstr LISTENING') do taskkill /F /PID %%a >nul 2>&1
goto :eof
