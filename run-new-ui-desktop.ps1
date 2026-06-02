# run-new-ui-desktop.ps1 - start the refactored frontend-app in a desktop Wails shell.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ProjectDir = $PSScriptRoot
$FrontendAppDir = Join-Path $ProjectDir 'frontend-app'
$NpmRegistry = if ($env:NPM_REGISTRY) { $env:NPM_REGISTRY } else { 'https://registry.npmmirror.com' }
$LocalCodexBinDir = Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\bin'

$script:ViteProcess = $null
$script:DesktopProcess = $null
$script:LocalPostgresStarted = $false
$script:RunLogDir = Join-Path $ProjectDir '.tmp\run-new-ui-desktop'

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
        Write-Host "  -> loaded $loaded entries from .env"
    }
}

function Set-DefaultEnv {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Value
    )

    $existing = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $existing -or $existing.Trim() -eq '') {
        Set-Item -Path "Env:$Name" -Value $Value
    }
}

function Ensure-DevControlSessionToken {
    if ($env:GO_AGENT_CTL_SESSION_TOKEN) { return }
    if ($env:GO_AGENT_MCP_SESSION_TOKEN) {
        $env:GO_AGENT_CTL_SESSION_TOKEN = $env:GO_AGENT_MCP_SESSION_TOKEN
        return
    }
    $env:GO_AGENT_CTL_SESSION_TOKEN = 'dev-new-ui-{0}-{1}' -f ([DateTimeOffset]::UtcNow.ToUnixTimeSeconds()), $PID
}

function Test-PortListening {
    param([Parameter(Mandatory)][int]$Port)

    try {
        $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
        return ($null -ne $conn)
    } catch {
        return $false
    }
}

function Get-ListeningPortDescription {
    param([Parameter(Mandatory)][int]$Port)

    try {
        $rows = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
        foreach ($row in $rows) {
            $processName = ''
            try {
                $processName = (Get-Process -Id $row.OwningProcess -ErrorAction SilentlyContinue).ProcessName
            } catch {}
            [pscustomobject]@{
                LocalAddress = $row.LocalAddress
                LocalPort = $row.LocalPort
                OwningProcess = $row.OwningProcess
                ProcessName = $processName
            }
        }
    } catch {}
}

function Assert-PortFree {
    param([Parameter(Mandatory)][string]$Address)

    $portText = ($Address -split ':')[-1]
    $port = [int]$portText
    if (-not (Test-PortListening -Port $port)) { return }

    Write-Host "XX port $port is already in use:"
    Get-ListeningPortDescription -Port $port | Format-Table -AutoSize | Out-String | Write-Host
    throw "port $port is already in use"
}

function Wait-ForHttp {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Label
    )

    for ($i = 0; $i -lt 50; $i++) {
        try {
            $null = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 1 -ErrorAction Stop
            Write-Host "  -> $Label ready: $Url"
            return
        } catch {
            Start-Sleep -Milliseconds 200
        }
    }

    throw "timed out waiting for $Label`: $Url"
}

function Resolve-NpmCommand {
    $cmd = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $cmd = Get-Command npm -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    throw 'npm was not found on PATH'
}

function Add-CodexCliToPath {
    $candidateDirs = New-Object System.Collections.Generic.List[string]
    if ($env:SUPER_DOLPHIN_CODEX_BIN_DIR) {
        $candidateDirs.Add($env:SUPER_DOLPHIN_CODEX_BIN_DIR)
    }
    if (Test-Path -LiteralPath $LocalCodexBinDir -PathType Container) {
        Get-ChildItem -LiteralPath $LocalCodexBinDir -Directory -ErrorAction SilentlyContinue |
            Sort-Object LastWriteTime -Descending |
            ForEach-Object {
                if (Test-Path -LiteralPath (Join-Path $_.FullName 'codex.exe') -PathType Leaf) {
                    $candidateDirs.Add($_.FullName)
                }
            }
    }
    foreach ($dir in ($candidateDirs | Select-Object -Unique)) {
        $exe = Join-Path $dir 'codex.exe'
        if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { continue }
        $previousErrorAction = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            & $exe app-server --help *> $null
            if ($LASTEXITCODE -eq 0) {
                $env:PATH = $dir + [IO.Path]::PathSeparator + $env:PATH
                $env:SUPER_DOLPHIN_CODEX_BIN_DIR = $dir
                Write-Host "  -> Codex CLI PATH: $dir"
                return
            }
        } finally {
            $ErrorActionPreference = $previousErrorAction
        }
    }
    Write-Host '  !! Codex CLI with app-server support was not found on the local OpenAI Codex bundle path'
}

function Ensure-NodeDeps {
    param([Parameter(Mandatory)][string]$Dir)

    $packageJson = Join-Path $Dir 'package.json'
    if (-not (Test-Path -LiteralPath $packageJson -PathType Leaf)) {
        throw "missing package.json: $Dir"
    }

    $npm = Resolve-NpmCommand
    $nodeModules = Join-Path $Dir 'node_modules'
    $packageLock = Join-Path $Dir 'package-lock.json'

    Push-Location -LiteralPath $Dir
    try {
        if (-not (Test-Path -LiteralPath $nodeModules -PathType Container)) {
            Write-Host "  -> npm ci ($Dir)"
            & $npm ci "--registry=$NpmRegistry"
            if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        } elseif ((Test-Path -LiteralPath $packageLock -PathType Leaf) -and
                  ((Get-Item -LiteralPath $packageLock).LastWriteTime -gt (Get-Item -LiteralPath $nodeModules).LastWriteTime)) {
            Write-Host '  -> npm ci (package-lock changed)'
            & $npm ci "--registry=$NpmRegistry"
            if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        } elseif ((Get-Item -LiteralPath $packageJson).LastWriteTime -gt (Get-Item -LiteralPath $nodeModules).LastWriteTime) {
            Write-Host '  -> npm install (package.json changed)'
            & $npm install "--registry=$NpmRegistry"
            if ($LASTEXITCODE -ne 0) { throw 'npm install failed' }
        } else {
            Write-Host '  -> dependencies unchanged'
        }
    } finally {
        Pop-Location
    }
}

function Ensure-PeerBinaries {
    $missing = $false
    foreach ($name in @('mcp-orch', 'mcp-lsp')) {
        if (-not (Test-Path -LiteralPath (Join-Path $ProjectDir "$name.exe") -PathType Leaf) -and
            -not (Test-Path -LiteralPath (Join-Path $ProjectDir $name) -PathType Leaf)) {
            $missing = $true
            break
        }
    }

    if (-not $missing) { return }

    Write-Host '  -> building peer binaries for new UI desktop'
    Push-Location -LiteralPath $ProjectDir
    try {
        & go build -o (Join-Path $ProjectDir 'mcp-orch.exe') './cmd/mcp-orch/'
        if ($LASTEXITCODE -ne 0) { throw 'go build mcp-orch failed' }
        & go build -o (Join-Path $ProjectDir 'mcp-lsp.exe') './cmd/mcp-lsp/'
        if ($LASTEXITCODE -ne 0) { throw 'go build mcp-lsp failed' }
    } finally {
        Pop-Location
    }
}

function Get-PostgresPlatformId {
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch -Regex ($arch) {
        'ARM64' { return 'windows-arm64' }
        default { return 'windows-amd64' }
    }
}

function Get-PostgresExecutablePath {
    param(
        [Parameter(Mandatory)][string]$BinDir,
        [Parameter(Mandatory)][string]$Name
    )

    foreach ($leaf in @($Name, "$Name.exe")) {
        $candidate = Join-Path $BinDir $leaf
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

function Test-PostgresBinDir {
    param([Parameter(Mandatory)][string]$BinDir)

    foreach ($name in @('postgres', 'initdb', 'pg_ctl', 'pg_config')) {
        if (-not (Get-PostgresExecutablePath -BinDir $BinDir -Name $name)) { return $false }
    }
    return $true
}

function Resolve-PostgresBinDir {
    $platform = Get-PostgresPlatformId
    $candidates = New-Object System.Collections.Generic.List[string]

    if ($env:SUPER_DOLPHIN_POSTGRES_BIN_DIR) { $candidates.Add($env:SUPER_DOLPHIN_POSTGRES_BIN_DIR) }
    if ($env:SUPER_DOLPHIN_POSTGRES_DIST) { $candidates.Add((Join-Path $env:SUPER_DOLPHIN_POSTGRES_DIST 'bin')) }
    $candidates.Add((Join-Path $ProjectDir "third_party\postgres\$platform\bin"))
    $candidates.Add((Join-Path $ProjectDir ".build-cache\postgres\16.14\$platform\bin"))

    foreach ($root in @(${env:ProgramFiles}, ${env:ProgramFiles(x86)})) {
        if (-not $root) { continue }
        foreach ($version in @('16', '15', '14')) {
            $candidates.Add((Join-Path $root "PostgreSQL\$version\bin"))
        }
    }

    $pgCtl = Get-Command pg_ctl.exe -ErrorAction SilentlyContinue
    if ($pgCtl) { $candidates.Add((Split-Path -Parent $pgCtl.Source)) }

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        if ($candidate -and (Test-PostgresBinDir -BinDir $candidate)) { return $candidate }
    }

    throw 'missing PostgreSQL runtime; set SUPER_DOLPHIN_POSTGRES_DIST or SUPER_DOLPHIN_POSTGRES_BIN_DIR'
}

function Resolve-PostgresShareDir {
    param([Parameter(Mandatory)][string]$BinDir)

    if ($env:SUPER_DOLPHIN_POSTGRES_SHARE_DIR) {
        if (Test-Path -LiteralPath (Join-Path $env:SUPER_DOLPHIN_POSTGRES_SHARE_DIR 'postgres.bki') -PathType Leaf) {
            return $env:SUPER_DOLPHIN_POSTGRES_SHARE_DIR
        }
        throw "SUPER_DOLPHIN_POSTGRES_SHARE_DIR missing postgres.bki: $($env:SUPER_DOLPHIN_POSTGRES_SHARE_DIR)"
    }

    $candidates = New-Object System.Collections.Generic.List[string]
    $pgConfig = Get-PostgresExecutablePath -BinDir $BinDir -Name 'pg_config'
    if ($pgConfig) {
        $sharedir = (& $pgConfig --sharedir 2>$null | Select-Object -First 1)
        if ($sharedir) { $candidates.Add($sharedir) }
    }

    $root = Split-Path -Parent $BinDir
    $distRoot = Split-Path -Parent $root
    foreach ($candidate in @(
        (Join-Path $root 'share'),
        (Join-Path $root 'share\postgresql'),
        (Join-Path $distRoot 'share'),
        (Join-Path $distRoot 'share\postgresql'),
        (Join-Path $distRoot 'share\postgresql\16'),
        (Join-Path $distRoot 'share\postgresql\14')
    )) {
        $candidates.Add($candidate)
    }

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        if ($candidate -and (Test-Path -LiteralPath (Join-Path $candidate 'postgres.bki') -PathType Leaf)) {
            return $candidate
        }
    }

    throw 'missing PostgreSQL share dir with postgres.bki; set SUPER_DOLPHIN_POSTGRES_SHARE_DIR'
}

function Get-LocalPostgresEndpoint {
    param([Parameter(Mandatory)][string]$DatabaseUrl)

    try {
        $uri = [Uri]$DatabaseUrl
        if ($uri.Scheme -notin @('postgres', 'postgresql')) { return $null }
        $hostName = $uri.Host.ToLowerInvariant()
        if ($hostName -notin @('localhost', '127.0.0.1')) { return $null }
        $port = if ($uri.IsDefaultPort -or $uri.Port -le 0) { 5432 } else { $uri.Port }
        return [pscustomobject]@{ Host = $uri.Host; Port = $port }
    } catch {
        return $null
    }
}

function Configure-DevPostgresRuntime {
    Set-DefaultEnv -Name 'SUPER_DOLPHIN_PROCESS_ROLE' -Value 'desktop'

    $databaseUrl = if ($env:DATABASE_URL) { $env:DATABASE_URL } else { $env:POSTGRES_CONNECTION_STRING }
    if ($databaseUrl) {
        $endpoint = Get-LocalPostgresEndpoint -DatabaseUrl $databaseUrl
        if ($null -eq $endpoint) { return }
        if (Test-PortListening -Port $endpoint.Port) {
            Write-Host "  -> using already-running local PostgreSQL at $($endpoint.Host):$($endpoint.Port)"
            return
        }
    }

    $binDir = Resolve-PostgresBinDir
    Set-DefaultEnv -Name 'SUPER_DOLPHIN_POSTGRES_BIN_DIR' -Value $binDir
    $shareDir = Resolve-PostgresShareDir -BinDir $env:SUPER_DOLPHIN_POSTGRES_BIN_DIR
    Set-DefaultEnv -Name 'SUPER_DOLPHIN_POSTGRES_SHARE_DIR' -Value $shareDir

    if (-not $databaseUrl) {
        Set-DefaultEnv -Name 'SUPER_DOLPHIN_EMBEDDED_POSTGRES' -Value 'true'
        Write-Host '  -> embedded PostgreSQL enabled for dev runtime'
    }
}

function Ensure-LocalPostgres {
    $databaseUrl = if ($env:DATABASE_URL) { $env:DATABASE_URL } else { $env:POSTGRES_CONNECTION_STRING }
    if (-not $databaseUrl) { return }

    $endpoint = Get-LocalPostgresEndpoint -DatabaseUrl $databaseUrl
    if ($null -eq $endpoint) { return }

    if (Test-PortListening -Port $endpoint.Port) {
        Write-Host "  -> local PostgreSQL already listening on $($endpoint.Host):$($endpoint.Port)"
        return
    }

    if (-not (Test-Path -LiteralPath (Join-Path $env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR 'PG_VERSION') -PathType Leaf)) {
        throw "DATABASE_URL points to local PostgreSQL ($($endpoint.Host):$($endpoint.Port)), but data dir is not initialized: $($env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR)"
    }

    New-Item -ItemType Directory -Force -Path $env:SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:SUPER_DOLPHIN_LOCAL_POSTGRES_LOG) | Out-Null

    $pgCtl = Get-PostgresExecutablePath -BinDir $env:SUPER_DOLPHIN_POSTGRES_BIN_DIR -Name 'pg_ctl'
    if (-not $pgCtl) { throw 'pg_ctl was not found' }

    Write-Host "  -> starting local PostgreSQL: $($endpoint.Host):$($endpoint.Port)"
    & $pgCtl -D $env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR `
        -l $env:SUPER_DOLPHIN_LOCAL_POSTGRES_LOG `
        -o "-h $($endpoint.Host) -p $($endpoint.Port)" `
        -w -t 30 start
    if ($LASTEXITCODE -ne 0) { throw 'failed to start local PostgreSQL' }
    $script:LocalPostgresStarted = $true
}

function Stop-StartedProcesses {
    if ($script:ViteProcess -and -not $script:ViteProcess.HasExited) {
        Write-Host "  -> stopping frontend-app vite (PID: $($script:ViteProcess.Id))"
        Stop-Process -Id $script:ViteProcess.Id -Force -ErrorAction SilentlyContinue
    }

    if ($script:DesktopProcess -and -not $script:DesktopProcess.HasExited) {
        Write-Host "  -> stopping new UI desktop backend (PID: $($script:DesktopProcess.Id))"
        Stop-Process -Id $script:DesktopProcess.Id -Force -ErrorAction SilentlyContinue
    }

    if ($script:LocalPostgresStarted) {
        $pgCtl = Get-PostgresExecutablePath -BinDir $env:SUPER_DOLPHIN_POSTGRES_BIN_DIR -Name 'pg_ctl'
        if ($pgCtl) {
            Write-Host '  -> stopping local PostgreSQL'
            & $pgCtl -D $env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR -w -t 30 stop -m fast *> $null
        }
    }
}

Import-DotEnvFile -Path (Join-Path $ProjectDir '.env')

Set-DefaultEnv -Name 'SUPER_DOLPHIN_HTTP_ADDR' -Value '127.0.0.1:4512'
Set-DefaultEnv -Name 'GO_AGENT_CTL_RPC_ADDR' -Value '127.0.0.1:8092'
Set-DefaultEnv -Name 'VITE_DEV_URL' -Value 'http://127.0.0.1:5175'
Set-DefaultEnv -Name 'FRONTEND_DEVSERVER_URL' -Value $env:VITE_DEV_URL

$viteUri = [Uri]$env:VITE_DEV_URL
if (-not $viteUri.Host -or $viteUri.Port -le 0 -or $viteUri.Authority -notmatch ':\d+$') {
    throw "VITE_DEV_URL must include host and port, got: $($env:VITE_DEV_URL)"
}
$ViteDevHost = $viteUri.Host
$ViteDevPort = $viteUri.Port

Set-DefaultEnv -Name 'GO_AGENT_PEER_BIN_DIR' -Value $ProjectDir
Set-DefaultEnv -Name 'SUPER_DOLPHIN_RUNTIME_MODE' -Value 'dev'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR' -Value $ProjectDir
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_ENTRYPOINT' -Value 'run-new-ui-desktop.ps1'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_HOME' -Value (Join-Path $env:USERPROFILE '.super-dolphin')
Set-DefaultEnv -Name 'CODEX_HOME' -Value (Join-Path $env:USERPROFILE '.codex')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR' -Value (Join-Path $ProjectDir '.tmp\pgdata')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR' -Value (Join-Path $ProjectDir '.tmp\pgsocket')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_LOCAL_POSTGRES_LOG' -Value (Join-Path $ProjectDir '.tmp\postgres.log')
Set-DefaultEnv -Name 'LOG_LEVEL' -Value 'debug'
Set-DefaultEnv -Name 'ENABLE_MEMORY_SYSTEM' -Value '1'
Set-DefaultEnv -Name 'ENABLE_MEMORY_TOOLS' -Value '1'
Set-DefaultEnv -Name 'MULTI_AGENT_MEMORY_FEATURE_TEAMMEM' -Value '1'
Set-DefaultEnv -Name 'CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME' -Value '1'

try {
    Add-CodexCliToPath
    Ensure-DevControlSessionToken
    Configure-DevPostgresRuntime
    Assert-PortFree -Address $env:SUPER_DOLPHIN_HTTP_ADDR
    Assert-PortFree -Address $env:GO_AGENT_CTL_RPC_ADDR
    Assert-PortFree -Address "$ViteDevHost`:$ViteDevPort"
    Ensure-LocalPostgres
    Ensure-NodeDeps -Dir $FrontendAppDir
    Ensure-PeerBinaries

    Write-Host '+-----------------------------------------+'
    Write-Host '|  Super Agent new UI desktop             |'
    Write-Host '+-----------------------------------------+'
    Write-Host "  frontend-app: $($env:VITE_DEV_URL)"
    Write-Host "  bridge:       $($env:SUPER_DOLPHIN_HTTP_ADDR)"
    Write-Host "  control rpc:  $($env:GO_AGENT_CTL_RPC_ADDR)"
    Write-Host "  peer bin dir: $($env:GO_AGENT_PEER_BIN_DIR)"
    Write-Host "  runtime:      $($env:SUPER_DOLPHIN_RUNTIME_MODE) ($($env:SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR))"

    New-Item -ItemType Directory -Force -Path $script:RunLogDir | Out-Null
    $viteOutLog = Join-Path $script:RunLogDir 'vite.out.log'
    $viteErrLog = Join-Path $script:RunLogDir 'vite.err.log'
    $desktopOutLog = Join-Path $script:RunLogDir 'desktop.out.log'
    $desktopErrLog = Join-Path $script:RunLogDir 'desktop.err.log'
    Remove-Item -LiteralPath $viteOutLog, $viteErrLog, $desktopOutLog, $desktopErrLog -Force -ErrorAction SilentlyContinue
    Write-Host "  logs:         $script:RunLogDir"

    $npm = Resolve-NpmCommand
    $script:ViteProcess = Start-Process -FilePath $npm `
        -ArgumentList @('run', 'dev', '--', '--host', $ViteDevHost, '--port', "$ViteDevPort", '--strictPort') `
        -WorkingDirectory $FrontendAppDir `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $viteOutLog `
        -RedirectStandardError $viteErrLog
    Wait-ForHttp -Url $env:VITE_DEV_URL -Label 'frontend-app vite'

    $script:DesktopProcess = Start-Process -FilePath 'go' `
        -ArgumentList @('run', './cmd/agent-terminal') `
        -WorkingDirectory $ProjectDir `
        -PassThru `
        -RedirectStandardOutput $desktopOutLog `
        -RedirectStandardError $desktopErrLog

    Wait-Process -Id $script:DesktopProcess.Id
    exit $script:DesktopProcess.ExitCode
} finally {
    Stop-StartedProcesses
}
