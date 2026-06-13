# run-debug.ps1 — PowerShell port of run-debug.sh for Windows.
#
# 与 sh 原版相比的取舍：
#   - Frida / IDA 在 Windows 不支持：选项 1 子菜单的 "含 Frida" 分支被移除，
#     所有 debug 编译都走 `-gcflags='-N -l'` 且不加 `-tags ida,frida`。
#   - 其余特性（git worktree [选项 2] / git tag [选项 5] / run-only [选项 4] /
#     codemap 索引刷新 / code_size_guard 预检 / 三级 npm 安装 / Vue template
#     预检 / vite 热更新 / 退出清理）均已移植。
#   - 不显式设置 CGO_ENABLED=1（sh 里 debug 会强制开 CGO）：Windows 上经常没有
#     MinGW，显式开反而会让 build 直接炸。如需 CGO，在调用脚本前自己 `$env:CGO_ENABLED='1'`。
#
# 依赖：PowerShell 5.1+、Go toolchain、Node.js (npm + npx)、Git、curl.exe
# (Windows 10+ 自带；本脚本实际用 Invoke-WebRequest，可不依赖 curl)。

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ============================================================
# 常量
# ============================================================
$ProjectDir         = $PSScriptRoot
$WorktreeDir        = Join-Path $ProjectDir '.worktrees\test'
$FrontendDir        = Join-Path $ProjectDir 'cmd\agent-terminal\frontend'
$NpmRegistry        = 'https://registry.npmmirror.com'
$ForceNpmReinstall  = $false
$AutoCodemapRefresh = if ($null -ne $env:AUTO_CODEMAP_REFRESH) { $env:AUTO_CODEMAP_REFRESH } else { '1' }

$env:GO_GUARD_ALLOW_RAW = 'run-debug.ps1'

# vite dev server 状态（供 finally 清理）
$script:ViteDevPid = $null
$script:ViteDevUrl = $null
$script:AgentExit  = $null

# ============================================================
# 工具函数
# ============================================================

function Get-FileMd5Hex {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return 'nohash' }
    (Get-FileHash -Algorithm MD5 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-StringMd5Hex {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
    $ms = [IO.MemoryStream]::new($bytes)
    try {
        (Get-FileHash -Algorithm MD5 -InputStream $ms).Hash.ToLowerInvariant()
    } finally {
        $ms.Dispose()
    }
}

# 模拟 sh: `find ... | sort | xargs md5 -q | md5 -q`
function Get-AggregateMd5 {
    param([Parameter(Mandatory)][AllowEmptyCollection()][string[]]$Files)
    if (-not $Files -or $Files.Count -eq 0) { return 'nohash' }
    $hashes = $Files |
        Where-Object { Test-Path -LiteralPath $_ } |
        Sort-Object -Unique |
        ForEach-Object { (Get-FileHash -Algorithm MD5 -LiteralPath $_).Hash }
    if (-not $hashes -or $hashes.Count -eq 0) { return 'nohash' }
    Get-StringMd5Hex (($hashes -join ''))
}

function Write-HashFile {
    param([string]$Path, [string]$Value)
    $dir = Split-Path -Parent $Path
    if ($dir) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
    [IO.File]::WriteAllText($Path, $Value, [Text.UTF8Encoding]::new($false))
}

function Read-HashFile {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return '' }
    (Get-Content -Raw -LiteralPath $Path).Trim()
}

function Invoke-CodemapRefresh {
    param([Parameter(Mandatory)][string]$BuildDir)
    if ($AutoCodemapRefresh -ne '1') {
        Write-Host "[pre-build] 跳过代码地图索引刷新 (AUTO_CODEMAP_REFRESH=$AutoCodemapRefresh)"
        return
    }
    $CodemapSrc      = Join-Path $BuildDir 'scripts\codemap_index.go'
    $CodemapDir      = Join-Path $BuildDir 'docs\doc\codemap'
    $CodemapReadme   = Join-Path $CodemapDir 'README.md'
    $CodemapMakefile = Join-Path $BuildDir 'Makefile'
    $CacheDir        = Join-Path $BuildDir '.build-cache'
    $CodemapBin      = Join-Path $CacheDir 'codemap-index.exe'
    $HashFile        = Join-Path $CacheDir 'codemap-index.srchash'

    if (-not (Test-Path $CodemapSrc)      -or -not (Test-Path $CodemapDir) -or
        -not (Test-Path $CodemapReadme)   -or -not (Test-Path $CodemapMakefile)) {
        Write-Host '[pre-build] 跳过代码地图索引刷新 (缺少 codemap 所需文件)'
        return
    }

    Write-Host '[pre-build] 刷新代码地图索引 (ai-index.json / README.md)...'
    New-Item -ItemType Directory -Force -Path $CacheDir | Out-Null
    $CurHash = Get-FileMd5Hex $CodemapSrc

    $needsBuild = (-not (Test-Path $CodemapBin)) -or (-not (Test-Path $HashFile)) -or
                  ((Read-HashFile $HashFile) -ne $CurHash)
    if ($needsBuild) {
        Write-Host '  -> 编译 codemap_index...'
        & go build -o $CodemapBin $CodemapSrc
        if ($LASTEXITCODE -ne 0) {
            Write-Host '  !! codemap_index 编译失败，跳过索引刷新并继续启动'
            return
        }
        Write-HashFile -Path $HashFile -Value $CurHash
    } else {
        Write-Host '  -> codemap_index 缓存命中，跳过编译'
    }

    & $CodemapBin $BuildDir
    if ($LASTEXITCODE -eq 0) {
        Write-Host '  OK ai-index.json / README.md 已刷新'
    } else {
        Write-Host '  !! 代码地图索引刷新失败，跳过并继续启动'
    }
}

function Stop-ByProcessName {
    param([string[]]$Names)
    foreach ($n in $Names) {
        Get-Process -Name $n -ErrorAction SilentlyContinue |
            Stop-Process -Force -ErrorAction SilentlyContinue
    }
}

function Stop-BuildBinaryProcesses {
    param(
        [Parameter(Mandatory)][string]$BuildDir,
        [Parameter(Mandatory)][string[]]$Names
    )
    $targets = @{}
    foreach ($name in $Names) {
        $leaf = if ([IO.Path]::GetExtension($name)) { $name } else { "$name.exe" }
        $targets[(Join-Path $BuildDir $leaf).ToLowerInvariant()] = $true
    }
    foreach ($name in $Names) {
        $leaf = if ([IO.Path]::GetExtension($name)) { $name } else { "$name.exe" }
        Get-CimInstance Win32_Process -Filter "Name='$leaf'" -ErrorAction SilentlyContinue |
            Where-Object {
                $_.ExecutablePath -and $targets.ContainsKey($_.ExecutablePath.ToLowerInvariant())
            } |
            ForEach-Object {
                Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
            }
    }
}

function Stop-ByPort {
    param([int[]]$Ports)
    foreach ($port in $Ports) {
        $conns = @()
        try {
            $conns = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
        } catch {}
        foreach ($c in $conns) {
            Stop-Process -Id $c.OwningProcess -Force -ErrorAction SilentlyContinue
        }
    }
}

# Wails v3 在 Windows 上的 WebView2 用户目录位于 %LOCALAPPDATA%\<app>。
function Clear-WebviewCache {
    $paths = @(
        (Join-Path $env:LOCALAPPDATA 'agent-terminal'),
        (Join-Path $env:LOCALAPPDATA 'com.multi-agent.agent-terminal')
    )
    foreach ($p in $paths) {
        if (Test-Path -LiteralPath $p) {
            Remove-Item -LiteralPath $p -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

function Test-HttpReady {
    param([string]$Url, [int]$TimeoutSec = 1)
    try {
        $null = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec $TimeoutSec -ErrorAction Stop
        return $true
    } catch {
        return $false
    }
}

function Test-CodexCli {
    param([Parameter(Mandatory)][string]$Command)
    try {
        & $Command --version *> $null
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    }
}

function Add-CodexCliToPath {
    # WindowsApps exposes a codex.exe that can be discovered by `where`,
    # but this machine may deny direct execution. Prefer the usable local
    # Codex bundle that the desktop app places under LOCALAPPDATA.
    if (Test-CodexCli -Command 'codex') { return }

    $candidateDirs = @()
    if ($env:LOCALAPPDATA) {
        $bundleRoot = Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\bin'
        if (Test-Path -LiteralPath $bundleRoot) {
            $candidateDirs += Get-ChildItem -LiteralPath $bundleRoot -Directory -ErrorAction SilentlyContinue |
                Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName 'codex.exe') } |
                Sort-Object LastWriteTime -Descending |
                ForEach-Object { $_.FullName }
        }
    }

    foreach ($dir in $candidateDirs) {
        $exe = Join-Path $dir 'codex.exe'
        if (Test-CodexCli -Command $exe) {
            $env:Path = $dir + [IO.Path]::PathSeparator + $env:Path
            Write-Host "  -> Codex CLI PATH 已修正: $dir"
            return
        }
    }

    Write-Host '  !! 未找到可执行的 Codex CLI；Codex provider 对话可能无法启动'
}

function ConvertFrom-DotEnvLine {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Line)
    $line = $Line.Trim().TrimStart([char]0xFEFF)
    if (-not $line -or $line.StartsWith('#')) { return $null }
    if ($line.StartsWith('export ')) {
        $line = $line.Substring(7).TrimStart()
    }
    $eq = $line.IndexOf('=')
    if ($eq -le 0) { return $null }
    $key = $line.Substring(0, $eq).Trim()
    if (-not $key -or $key -match '\s') { return $null }
    $value = $line.Substring($eq + 1).Trim()
    if (($value.StartsWith('"') -and $value.EndsWith('"')) -or
        ($value.StartsWith("'") -and $value.EndsWith("'"))) {
        $value = $value.Substring(1, $value.Length - 2)
    }
    [pscustomobject]@{ Key = $key; Value = $value }
}

function Import-DotEnvFile {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return }
    $loaded = 0
    foreach ($line in Get-Content -LiteralPath $Path) {
        $entry = ConvertFrom-DotEnvLine -Line $line
        if ($null -eq $entry) { continue }
        $existing = [Environment]::GetEnvironmentVariable($entry.Key, 'Process')
        if ($null -ne $existing -and $existing.Trim() -ne '') { continue }
        Set-Item -Path "Env:$($entry.Key)" -Value $entry.Value
        $loaded++
    }
    if ($loaded -gt 0) {
        Write-Host "  -> 已从 .env 加载 $loaded 项本地配置"
    }
}

function Ensure-DevControlSessionToken {
    $canonical = if ($null -ne $env:GO_AGENT_CTL_SESSION_TOKEN) { $env:GO_AGENT_CTL_SESSION_TOKEN.Trim() } else { '' }
    if ($canonical) { return }

    $legacy = if ($null -ne $env:GO_AGENT_MCP_SESSION_TOKEN) { $env:GO_AGENT_MCP_SESSION_TOKEN.Trim() } else { '' }
    if ($legacy) {
        $env:GO_AGENT_CTL_SESSION_TOKEN = $legacy
        Write-Host '  -> GO_AGENT_MCP_SESSION_TOKEN 已兼容提升为 GO_AGENT_CTL_SESSION_TOKEN'
        return
    }

    $env:GO_AGENT_CTL_SESSION_TOKEN = 'dev-local-{0}-{1}-{2}' -f `
        ([DateTimeOffset]::UtcNow.ToUnixTimeSeconds()), $PID, ([Guid]::NewGuid().ToString('N'))
    Write-Host '  -> GO_AGENT_CTL_SESSION_TOKEN 已为本地调试生成'
}

function Get-CurrentUserSid {
    $rows = whoami /user /fo csv | ConvertFrom-Csv
    if ($rows -and $rows[0].SID) { return $rows[0].SID }
    throw '无法读取当前 Windows 用户 SID'
}

function Get-SuperDolphinHome {
    if ($env:SUPER_DOLPHIN_HOME -and $env:SUPER_DOLPHIN_HOME.Trim()) {
        return $env:SUPER_DOLPHIN_HOME.Trim()
    }
    return (Join-Path $env:USERPROFILE '.super-dolphin')
}

function Protect-OwnerIdentitySalt {
    $sdHome = Get-SuperDolphinHome
    $salt = Join-Path $sdHome 'owner_identity.salt'
    if (-not (Test-Path -LiteralPath $salt)) { return }
    if (-not (Test-Path -LiteralPath $salt -PathType Leaf)) {
        throw "owner identity salt 不是普通文件: $salt"
    }

    $sid = Get-CurrentUserSid
    & icacls $salt /inheritance:r *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "修复 owner identity salt ACL 失败: icacls /inheritance:r $salt"
    }
    & icacls $salt /grant:r "*$sid`:(R,W)" "*S-1-5-18:(F)" "*S-1-5-32-544:(F)" *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "修复 owner identity salt ACL 失败: icacls /grant:r $salt"
    }
    Write-Host "  -> owner identity salt ACL 已加固: $salt"
}

function Start-ViteDev {
    param([Parameter(Mandatory)][string]$FrontDir)

    if (-not (Test-Path (Join-Path $FrontDir 'node_modules')) -or
        -not (Test-Path (Join-Path $FrontDir 'package.json'))) {
        Write-Host '  -> 跳过 vite（缺 node_modules 或 package.json），使用 dist/ 静态资源'
        return
    }

    $TemplateCheck = Join-Path $FrontDir 'scripts\check-templates.cjs'
    if (Test-Path $TemplateCheck) {
        Write-Host '  -> 预检 Vue template（runtime-compiler）...'
        Push-Location $FrontDir
        try {
            & node $TemplateCheck
            $ok = ($LASTEXITCODE -eq 0)
        } finally {
            Pop-Location
        }
        if (-not $ok) {
            Write-Host ''
            Write-Host '  XX Vue template 预检失败 —— 启动 webview 会直接黑屏'
            Write-Host '  修复上面列出的 template 再重启；按 Enter 忽略继续（不推荐），Ctrl+C 中止'
            [void](Read-Host)
            Write-Host '  -> 已忽略 template 守卫'
        }
    }

    Stop-ByPort -Ports @(5173)
    Stop-ByProcessName -Names @('esbuild')

    Write-Host '  -> 启动 vite dev server (端口 5173)...'
    $proc = Start-Process -FilePath 'npx.cmd' `
        -ArgumentList 'vite', '--port', '5173', '--strictPort' `
        -WorkingDirectory $FrontDir -PassThru -WindowStyle Hidden
    $script:ViteDevPid = $proc.Id

    for ($i = 0; $i -lt 30; $i++) {
        if ($proc.HasExited) { break }
        if (Test-HttpReady -Url 'http://localhost:5173' -TimeoutSec 1) {
            $script:ViteDevUrl = 'http://localhost:5173'
            $env:VITE_DEV_URL  = $script:ViteDevUrl
            Write-Host "  -> vite dev server 已就绪 OK (PID: $($proc.Id))"
            return
        }
        Start-Sleep -Milliseconds 300
    }

    Write-Host '  !! vite dev server 启动失败（端口冲突或 vite 进程已退出）'
    Write-Host '      将退回 dist/ 静态资源，若 dist 陈旧则会黑屏'
}

function Stop-ViteDev {
    if ($script:ViteDevPid) {
        $p = Get-Process -Id $script:ViteDevPid -ErrorAction SilentlyContinue
        if ($p) {
            Write-Host "  -> 停止 vite dev server (PID: $($script:ViteDevPid))..."
            Stop-Process -Id $script:ViteDevPid -Force -ErrorAction SilentlyContinue
        }
        $script:ViteDevPid = $null
    }
    # 双保险：端口上可能还有 npx/node 残留
    Stop-ByPort -Ports @(5173)
}

function Open-BrowserDelayed {
    param([string]$Url, [int]$DelaySec = 1)
    Start-Job -ScriptBlock {
        param($u, $d)
        Start-Sleep -Seconds $d
        Start-Process $u -ErrorAction SilentlyContinue
    } -ArgumentList $Url, $DelaySec | Out-Null
}

# ============================================================
# 菜单
# ============================================================
Import-DotEnvFile -Path (Join-Path $ProjectDir '.env')
Ensure-DevControlSessionToken

Write-Host '+----------------------------------+'
Write-Host '|    agent-terminal 编译工具       |'
Write-Host '+----------------------------------+'
Write-Host ''
Write-Host '  [1] 主分支编译 debug (无 Frida)'
Write-Host '  [2] test 分支编译 debug'
Write-Host '  [3] 正常编译 (release)'
Write-Host '  [4] 直接启动已编译二进制 (debug + vite HMR)'
Write-Host '  [5] 按 git tag 编译 debug'
Write-Host ''
$choice = Read-Host '选择 (1/2/3/4/5)'

Add-CodexCliToPath

$BuildDir  = $null
$Mode      = $null
$Label     = $null
$UseServer = $false

switch ($choice) {
    '1' {
        $BuildDir = $ProjectDir
        $Mode = 'debug'
        Write-Host ''
        Write-Host '  debug 子选项:'
        Write-Host '    [1] 普通 debug (无 IDA/Frida)'
        Write-Host '    [2] Server 模式 (浏览器访问 http://localhost:4511)'
        Write-Host ''
        $sub = Read-Host '  选择 (1/2)'
        switch ($sub) {
            '1' { $Label = 'main + debug (no Frida)' }
            '2' { $UseServer = $true; $Label = 'main + debug (server mode)' }
            default {
                Write-Host 'XX 无效选择'
                exit 1
            }
        }
    }
    '2' {
        $BuildDir = $WorktreeDir
        $Mode = 'debug'
        $Label = 'test + debug'
    }
    '3' {
        $BuildDir = $ProjectDir
        $Mode = 'normal'
        $Label = 'main + normal'
    }
    '4' {
        $BuildDir = $ProjectDir
        $Mode = 'run-only'
        $Label = '直接启动 (debug)'
    }
    '5' {
        $rawTags = & git -C $ProjectDir tag --sort=-version:refname
        $tags = @($rawTags | Where-Object { $_ -and $_.Trim() })
        if ($tags.Count -eq 0) {
            Write-Host 'XX 未找到 git tag'
            exit 1
        }
        Write-Host ''
        Write-Host '可用 git tag:'
        for ($i = 0; $i -lt $tags.Count; $i++) {
            Write-Host ('  [{0}] {1}' -f ($i + 1), $tags[$i])
        }
        Write-Host ''
        $tagChoice = Read-Host '选择 tag 序号'
        if ($tagChoice -notmatch '^\d+$' -or
            [int]$tagChoice -lt 1 -or
            [int]$tagChoice -gt $tags.Count) {
            Write-Host 'XX 无效 tag 选择'
            exit 1
        }
        $selectedTag = $tags[[int]$tagChoice - 1]
        $tagSafe = $selectedTag -replace '[\\/\s]', '_'
        $BuildDir = Join-Path $ProjectDir ".worktrees\tag-$tagSafe"
        $Mode = 'debug'
        $Label = "tag $selectedTag + debug"

        if ((Test-Path $BuildDir) -and -not (Test-Path (Join-Path $BuildDir '.git'))) {
            Write-Host "XX 目录已存在且不是 git worktree: $BuildDir"
            exit 1
        }
        New-Item -ItemType Directory -Force -Path (Join-Path $ProjectDir '.worktrees') | Out-Null
        if (Test-Path (Join-Path $BuildDir '.git')) {
            & git -C $BuildDir checkout --detach $selectedTag | Out-Null
        } else {
            & git -C $ProjectDir worktree add --detach $BuildDir $selectedTag | Out-Null
        }
    }
    default {
        Write-Host 'XX 无效选择'
        exit 1
    }
}

Write-Host ''
Write-Host "> 模式: $Label"
Write-Host "> 目录: $BuildDir"
Write-Host '------------------------------------'

$env:SUPER_DOLPHIN_RUNTIME_MODE = 'dev'
$env:SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = $BuildDir
$env:SUPER_DOLPHIN_DEV_ENTRYPOINT = 'run-debug.ps1'
if (-not $env:SUPER_DOLPHIN_SQLITE_PATH) {
    $env:SUPER_DOLPHIN_SQLITE_PATH = Join-Path (Get-SuperDolphinHome) 'super-dolphin.db'
}
$sqliteParent = Split-Path -Parent $env:SUPER_DOLPHIN_SQLITE_PATH
if (-not $sqliteParent) { throw "SUPER_DOLPHIN_SQLITE_PATH must include a parent directory: $($env:SUPER_DOLPHIN_SQLITE_PATH)" }
New-Item -ItemType Directory -Force -Path $sqliteParent | Out-Null
Write-Host "> SQLite DB: $($env:SUPER_DOLPHIN_SQLITE_PATH)"
Write-Host '[0/4] Pre-flight 守卫...'
Protect-OwnerIdentitySalt

# memory 子系统默认开关（与 sh 一致，避免内存中心 UI 显示 "system off" 横幅）
if (-not $env:ENABLE_MEMORY_SYSTEM)               { $env:ENABLE_MEMORY_SYSTEM = '1' }
if (-not $env:ENABLE_MEMORY_TOOLS)                { $env:ENABLE_MEMORY_TOOLS  = '1' }
if (-not $env:MULTI_AGENT_MEMORY_FEATURE_TEAMMEM) { $env:MULTI_AGENT_MEMORY_FEATURE_TEAMMEM = '1' }

# Codex legacy default-home opt-in（与 sh 一致）。P21 P1a 把 codex identity
# 缺失改成硬报错；前端 thread/start payload 没显式传 codexHome 时，后端
# injectDefaultCodexIdentityForStart 只在该 env 为 "1" 时回落 ~/.codex。
# 默认 opt-in 让 dev 启动 GUI 后能直接对话；想关闭走 P1a 严格模式时
# `$env:CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME = '0'` 即可。
if (-not $env:CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME) { $env:CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME = '1' }

$ExtraArgs = $args

try {
    # ========================================================
    # run-only 分支：跳过编译，直接启动
    # ========================================================
    if ($Mode -eq 'run-only') {
        $BinPath = Join-Path $BuildDir 'super-agent-debug.exe'
        if (-not (Test-Path $BinPath)) {
            Write-Host "XX 未找到已编译的二进制: $BinPath"
            Write-Host '   请先使用选项 1/2/3/5 编译'
            exit 1
        }
        Write-Host '[1/3] 停止旧进程...'
        Stop-ByProcessName -Names @('super-agent-debug', 'esbuild')
        Stop-BuildBinaryProcesses -BuildDir $BuildDir -Names @('mcp-orch', 'mcp-lsp')
        Stop-ByPort -Ports @(4510, 4511, 5173)
        Start-Sleep -Milliseconds 500

        Write-Host '[2/3] 清理 webview 缓存...'
        Clear-WebviewCache

        Write-Host '[3/3] 启动 vite dev server...'
        Start-ViteDev -FrontDir (Join-Path $BuildDir 'cmd\agent-terminal\frontend')

        Write-Host ''
        Write-Host '===================================='
        Write-Host '> 直接启动已编译二进制 (debug)...'
        $sha = (Get-FileHash -Algorithm SHA256 -LiteralPath $BinPath).Hash.ToLowerInvariant()
        Write-Host "  sha256: $sha"
        if ($script:ViteDevUrl) {
            Write-Host "> 前端热更新 -> $($script:ViteDevUrl)"
        }
        Open-BrowserDelayed -Url 'http://localhost:4511' -DelaySec 1

        # 与 sh 对齐：子进程 CWD 必须是 BuildDir，否则 `resolveProjectRoot`
        # 用的是用户 shell 的 CWD，会导致 autoMigrate 找不到 migrations/
        # 并且 applyBaselineIfMissing 会静默跳过（bug：只在 ReadFile 成功时
        # 执 SQL，但 migration bookkeeping 是无条件的），把库留成
        # "baseline 已 applied 但没有表" 的坏状态。
        Set-Location -LiteralPath $BuildDir
        $env:PROJECT_ROOT = $BuildDir

        & $BinPath --debug @ExtraArgs
        $script:AgentExit = $LASTEXITCODE
        return
    }

    # ========================================================
    # 1) 前端
    # ========================================================
    $Front = Join-Path $BuildDir 'cmd\agent-terminal\frontend'

    if (Test-Path (Join-Path $Front 'package.json')) {
        Write-Host '[1/4] 前端处理...'
        Push-Location $Front
        try {
            $nodeModules = Join-Path $Front 'node_modules'
            $pkgLock     = Join-Path $Front 'package-lock.json'
            $pkgJson     = Join-Path $Front 'package.json'

            # 三级依赖安装策略（速度优先）
            $needCi  = $false
            $needAdd = $false
            if ((-not (Test-Path $nodeModules)) -or $ForceNpmReinstall) {
                $needCi = $true
            } elseif ((Test-Path $pkgLock) -and
                      ((Get-Item $pkgLock).LastWriteTime -gt (Get-Item $nodeModules).LastWriteTime)) {
                $needCi = $true
            } elseif ((Get-Item $pkgJson).LastWriteTime -gt (Get-Item $nodeModules).LastWriteTime) {
                $needAdd = $true
            }

            if ($needCi) {
                Write-Host '  -> npm ci (首次/全量安装)...'
                & npm.cmd ci --registry=$NpmRegistry
                if ($LASTEXITCODE -ne 0) { throw 'npm ci 失败' }
            } elseif ($needAdd) {
                Write-Host '  -> npm install (增量追加新依赖)...'
                & npm.cmd install --registry=$NpmRegistry
                if ($LASTEXITCODE -ne 0) { throw 'npm install 失败' }
            } else {
                Write-Host '  -> 依赖无变化，跳过安装'
            }

            # Vue template 预检（根治 webview 黑屏）
            if (Test-Path 'scripts\check-templates.cjs') {
                Write-Host '  -> 预检 Vue template（runtime-compiler）...'
                & node 'scripts\check-templates.cjs'
                if ($LASTEXITCODE -ne 0) {
                    Write-Host ''
                    Write-Host '  XX Vue template 预检失败 —— 启动 webview 会直接黑屏'
                    Write-Host '  修复上面列出的 template 再重启；按 Enter 忽略继续（不推荐），Ctrl+C 中止'
                    [void](Read-Host)
                    Write-Host '  -> 已忽略 template 守卫'
                }
            }

            if ($Mode -eq 'debug') {
                # debug：启动 vite dev server（毫秒级热更新，不做 vite build）
                Stop-ByPort -Ports @(5173)
                Stop-ByProcessName -Names @('esbuild')
                Write-Host '  -> 启动 vite dev server (端口 5173)...'
                $proc = Start-Process -FilePath 'npx.cmd' `
                    -ArgumentList 'vite', '--port', '5173', '--strictPort' `
                    -WorkingDirectory $Front -PassThru -WindowStyle Hidden
                $script:ViteDevPid = $proc.Id
                for ($i = 0; $i -lt 30; $i++) {
                    if ($proc.HasExited) { break }
                    if (Test-HttpReady -Url 'http://localhost:5173' -TimeoutSec 1) {
                        $script:ViteDevUrl = 'http://localhost:5173'
                        $env:VITE_DEV_URL  = $script:ViteDevUrl
                        Write-Host "  -> vite dev server 已就绪 OK (PID: $($proc.Id))"
                        break
                    }
                    Start-Sleep -Milliseconds 300
                }
            } else {
                # release：完整 vite build（带 hash 缓存）
                $cacheDir = Join-Path $Front '.build-cache'
                $hashFile = Join-Path $cacheDir 'frontend-src.hash'
                New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null

                $srcFiles = @()
                foreach ($p in @('src', 'vite.config.js', 'index.html')) {
                    $full = Join-Path $Front $p
                    if (Test-Path $full -PathType Container) {
                        $srcFiles += Get-ChildItem -LiteralPath $full -Recurse -File |
                            ForEach-Object { $_.FullName }
                    } elseif (Test-Path $full -PathType Leaf) {
                        $srcFiles += $full
                    }
                }
                $curHash = Get-AggregateMd5 -Files $srcFiles
                $skip = $false
                if ((Test-Path $hashFile) -and
                    ((Read-HashFile $hashFile) -eq $curHash) -and
                    (Test-Path (Join-Path $Front 'dist'))) {
                    Write-Host '  -> 前端源码无变化，跳过 vite build OK'
                    $skip = $true
                }
                if (-not $skip) {
                    & npm.cmd run build
                    if ($LASTEXITCODE -eq 0) {
                        Write-HashFile -Path $hashFile -Value $curHash
                    } else {
                        Write-Host ''
                        Write-Host '!! 前端构建/守卫未通过！按 Enter 跳过继续编译，Ctrl+C 中止'
                        [void](Read-Host)
                        Write-Host '  -> 已跳过前端报错，继续...'
                    }
                }
            }
        } finally {
            Pop-Location
        }
    } else {
        Write-Host '[1/4] 跳过前端 (无 package.json)'
    }

    # ========================================================
    # 2) 清理 webview 缓存
    # ========================================================
    Write-Host '[2/4] 清理 webview 缓存...'
    Clear-WebviewCache

    # ========================================================
    # 3) 后端：codemap 刷新 + 代码守卫 + 编译
    # ========================================================
    Push-Location $BuildDir
    try {
        Invoke-CodemapRefresh -BuildDir $BuildDir

        Write-Host '[3/4] 后端代码守卫检查...'
        $guardCacheDir = Join-Path $BuildDir '.build-cache'
        $guardBin      = Join-Path $guardCacheDir 'code-size-guard.exe'
        $guardHashFile = Join-Path $guardCacheDir 'code-size-guard.srchash'
        $guardSrcMain  = Join-Path $BuildDir 'scripts\code_size_guard.go'
        $archtestDir   = Join-Path $BuildDir 'internal\archtest'
        New-Item -ItemType Directory -Force -Path $guardCacheDir | Out-Null

        $guardSrc = @()
        if (Test-Path $guardSrcMain) { $guardSrc += $guardSrcMain }
        if (Test-Path $archtestDir) {
            $guardSrc += Get-ChildItem -LiteralPath $archtestDir -Recurse -Filter '*.go' |
                ForEach-Object { $_.FullName }
        }
        $guardCurHash = Get-AggregateMd5 -Files $guardSrc

        $needGuardBuild = (-not (Test-Path $guardBin)) -or
                          (-not (Test-Path $guardHashFile)) -or
                          ((Read-HashFile $guardHashFile) -ne $guardCurHash)
        if ($needGuardBuild) {
            Write-Host '  -> 编译 code_size_guard...'
            & go build -o $guardBin $guardSrcMain
            if ($LASTEXITCODE -ne 0) { throw 'code_size_guard 编译失败' }
            Write-HashFile -Path $guardHashFile -Value $guardCurHash
        } else {
            Write-Host '  -> code_size_guard 缓存命中，跳过编译'
        }

        & $guardBin
        if ($LASTEXITCODE -ne 0) {
            Write-Host ''
            Write-Host '!! 代码守卫检查未通过！按 Enter 跳过继续编译，Ctrl+C 中止'
            [void](Read-Host)
            Write-Host '  -> 已跳过守卫检查，继续编译...'
        }

        # ---- 编译 ----
        $outBin  = Join-Path $BuildDir 'super-agent-debug.exe'
        $outOrch = Join-Path $BuildDir 'mcp-orch.exe'
        $outLsp  = Join-Path $BuildDir 'mcp-lsp.exe'

        if ($Mode -eq 'debug') {
            if ($UseServer) {
                Write-Host "[3/4] 编译后端 (debug server 模式: -gcflags='-N -l')..."
            } else {
                Write-Host "[3/4] 编译后端 (debug: -gcflags='-N -l')..."
            }
            & go build '-gcflags=-N -l' -o $outBin  './cmd/agent-terminal'
            if ($LASTEXITCODE -ne 0) { throw 'go build super-agent-debug 失败' }
            & go build '-gcflags=-N -l' -o $outOrch './cmd/mcp-orch'
            if ($LASTEXITCODE -ne 0) { throw 'go build mcp-orch 失败' }
            & go build '-gcflags=-N -l' -o $outLsp  './cmd/mcp-lsp'
            if ($LASTEXITCODE -ne 0) { throw 'go build mcp-lsp 失败' }
        } else {
            Write-Host '[3/4] 编译后端 (release)...'
            & go build -o $outBin  './cmd/agent-terminal'
            if ($LASTEXITCODE -ne 0) { throw 'go build super-agent-debug 失败' }
            & go build -o $outOrch './cmd/mcp-orch'
            if ($LASTEXITCODE -ne 0) { throw 'go build mcp-orch 失败' }
            & go build -o $outLsp  './cmd/mcp-lsp'
            if ($LASTEXITCODE -ne 0) { throw 'go build mcp-lsp 失败' }
        }

        foreach ($tuple in @(
            @{ Name = 'super-agent-debug.exe'; Path = $outBin  },
            @{ Name = 'mcp-orch.exe';          Path = $outOrch },
            @{ Name = 'mcp-lsp.exe';           Path = $outLsp  }
        )) {
            $sha = (Get-FileHash -Algorithm SHA256 -LiteralPath $tuple.Path).Hash.ToLowerInvariant()
            Write-Host ('  OK {0,-24} sha256: {1}' -f $tuple.Name, $sha)
        }
    } finally {
        Pop-Location
    }

    # ========================================================
    # 4) 停旧进程
    # ========================================================
    Write-Host '[4/4] 停止旧进程...'
    Stop-ByProcessName -Names @('super-agent-debug', 'esbuild')
    Stop-BuildBinaryProcesses -BuildDir $BuildDir -Names @('mcp-orch', 'mcp-lsp')
    Stop-ByPort -Ports @(4510, 4511)
    Start-Sleep -Milliseconds 500

    # ========================================================
    # 启动
    # ========================================================
    Write-Host ''
    Write-Host '===================================='

    # 与 sh 对齐：子进程 CWD 必须是 BuildDir（sh 原版用 `cd $BUILD_DIR` 是持久
    # 的；我们前面的 Push/Pop-Location 已经把 CWD 弹回了用户目录）。否则
    # `config.resolveProjectRoot` 会读到错误的 CWD，后续 autoMigrate 找不到
    # migrations/ 并把 baseline 错误地标记为 applied。
    Set-Location -LiteralPath $BuildDir
    $env:PROJECT_ROOT = $BuildDir

    $LaunchBin = Join-Path $BuildDir 'super-agent-debug.exe'
    if ($Mode -eq 'debug') {
        if ($script:ViteDevUrl) {
            Write-Host "> 启动 debug 模式 (前端热更新 -> $($script:ViteDevUrl))..."
        } else {
            Write-Host '> 启动 debug 模式...'
        }
        Open-BrowserDelayed -Url 'http://localhost:4511' -DelaySec 1
        & $LaunchBin --debug @ExtraArgs
    } else {
        Write-Host '> 启动正常模式...'
        & $LaunchBin @ExtraArgs
    }
    $script:AgentExit = $LASTEXITCODE

} finally {
    Stop-ViteDev
}

if ($null -ne $script:AgentExit) { exit $script:AgentExit }
