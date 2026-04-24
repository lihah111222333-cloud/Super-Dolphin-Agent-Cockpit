#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
install-postgres.ps1 — 一键装 PostgreSQL 并对齐 super-agent-v3 所需配置。

步骤（幂等）：
  1. winget install PostgreSQL.PostgreSQL.16（端口 54320，静默）
  2. 创建业务角色 mima0000（LOGIN + CREATEDB）
  3. 追加 pg_hba.conf trust 规则（127.0.0.1/32 + ::1/128）
  4. 重启 service
  5. 用业务角色验证连接

用法（必须管理员 PowerShell）：
  .\scripts\install-postgres.ps1
#>

[CmdletBinding()]
param(
    [int]$Port          = 54320,
    [string]$AppRole    = 'mima0000',
    [string]$SuperPwd   = 'Pg16devlocal',
    [string]$WingetId   = 'PostgreSQL.PostgreSQL.16',
    [string]$Version    = '16'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-Step { param($m) Write-Host ">>> $m" -ForegroundColor Cyan }
function Write-Ok   { param($m) Write-Host "    OK $m" -ForegroundColor Green }
function Write-Skip { param($m) Write-Host "    -- $m" -ForegroundColor Yellow }
function Write-Warn2{ param($m) Write-Host "    !! $m" -ForegroundColor Yellow }

$InstallRoot = "C:\Program Files\PostgreSQL\$Version"
$DataDir     = Join-Path $InstallRoot 'data'
$PsqlExe     = Join-Path $InstallRoot 'bin\psql.exe'
$ServiceName = "postgresql-x64-$Version"
$PgHba       = Join-Path $DataDir 'pg_hba.conf'

# ============================================================
Write-Step "Step 1/5 安装 PostgreSQL $Version (端口 $Port)"

$svc = Get-Service $ServiceName -ErrorAction SilentlyContinue
if ($svc -and (Test-Path $PsqlExe)) {
    Write-Skip "已装：service=$ServiceName, bin=$PsqlExe"
} else {
    $override = "--mode unattended --unattendedmodeui none --superpassword $SuperPwd --serverport $Port --servicename $ServiceName --disable-components stackbuilder"
    Write-Host "    -> winget install $WingetId ..."
    Write-Host "       override: $override"
    & winget install --exact --id $WingetId `
        --override $override `
        --accept-package-agreements --accept-source-agreements --silent
    if ($LASTEXITCODE -ne 0) { throw "winget install 失败 (exit=$LASTEXITCODE)" }
    Write-Ok "winget install 完成"

    # 等 service 起来（installer 会自动启动）
    for ($i = 0; $i -lt 60; $i++) {
        $svc = Get-Service $ServiceName -ErrorAction SilentlyContinue
        if ($svc -and $svc.Status -eq 'Running') { break }
        Start-Sleep -Seconds 1
    }
    if (-not $svc -or $svc.Status -ne 'Running') {
        throw "service $ServiceName 未在 60s 内起来"
    }
    Write-Ok "service $ServiceName running"
}

if (-not (Test-Path $PsqlExe)) { throw "未找到 psql.exe: $PsqlExe" }

# ============================================================
Write-Step "Step 2/5 创建业务角色 $AppRole (LOGIN + CREATEDB)"

$env:PGPASSWORD = $SuperPwd
try {
    $roleCheck = & $PsqlExe -h 127.0.0.1 -p $Port -U postgres -d postgres `
        -tAc "SELECT 1 FROM pg_roles WHERE rolname='$AppRole'"
    if ($LASTEXITCODE -ne 0) { throw "psql 连接 postgres 失败（密码/端口/服务？）" }

    if (($roleCheck | Out-String).Trim() -eq '1') {
        & $PsqlExe -h 127.0.0.1 -p $Port -U postgres -d postgres `
            -c "ALTER ROLE $AppRole LOGIN CREATEDB" | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "ALTER ROLE 失败" }
        Write-Skip "role $AppRole 已存在，已确保 LOGIN+CREATEDB"
    } else {
        & $PsqlExe -h 127.0.0.1 -p $Port -U postgres -d postgres `
            -c "CREATE ROLE $AppRole LOGIN CREATEDB" | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "CREATE ROLE 失败" }
        Write-Ok "role $AppRole 已建"
    }
} finally {
    Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
}

# ============================================================
Write-Step "Step 3/5 追加 pg_hba.conf trust 规则"

if (-not (Test-Path $PgHba)) { throw "pg_hba.conf 不存在：$PgHba" }

$marker     = "# super-agent-v3: trust for $AppRole"
$hbaContent = Get-Content -LiteralPath $PgHba -Raw

if ($hbaContent -match [regex]::Escape($marker)) {
    Write-Skip "pg_hba.conf 已有 trust 规则，跳过"
} else {
    $rules = @"
$marker
host    all             $AppRole        127.0.0.1/32            trust
host    all             $AppRole        ::1/128                 trust

"@
    $bak = "$PgHba.bak-$(Get-Date -Format yyyyMMddHHmmss)"
    Copy-Item -LiteralPath $PgHba -Destination $bak
    Write-Host "    -> backup: $bak"

    # 插在 "# IPv4 local connections:" 之前，保证顺序优先
    $needle = '# IPv4 local connections:'
    if ($hbaContent.Contains($needle)) {
        $hbaContent = $hbaContent.Replace($needle, ($rules + $needle))
    } else {
        $hbaContent = $hbaContent.TrimEnd() + "`r`n`r`n" + $rules
    }
    [IO.File]::WriteAllText($PgHba, $hbaContent, [Text.UTF8Encoding]::new($false))
    Write-Ok "pg_hba.conf 已更新"
}

# ============================================================
Write-Step "Step 4/5 重启 service $ServiceName"
Restart-Service $ServiceName
for ($i = 0; $i -lt 30; $i++) {
    $svc = Get-Service $ServiceName
    if ($svc.Status -eq 'Running') { break }
    Start-Sleep -Seconds 1
}
if ((Get-Service $ServiceName).Status -ne 'Running') { throw "service 重启后未 Running" }
Write-Ok "service restarted"

# ============================================================
Write-Step "Step 5/5 验证 $AppRole 连接"
$out = & $PsqlExe -h 127.0.0.1 -p $Port -U $AppRole -d postgres `
    -tAc "SELECT current_user || '|' || current_database()"
if ($LASTEXITCODE -ne 0) { throw "psql 验证失败" }
Write-Ok "psql 返回: $(($out | Out-String).Trim())"

# ============================================================
Write-Host ""
Write-Host "==============================================" -ForegroundColor Green
Write-Host "  PostgreSQL $Version ready" -ForegroundColor Green
Write-Host "  DATABASE_URL: postgres://$AppRole@127.0.0.1:$Port/super_agent_v3?sslmode=disable"
Write-Host "  postgres 超级账号密码: $SuperPwd"
Write-Host "==============================================" -ForegroundColor Green
