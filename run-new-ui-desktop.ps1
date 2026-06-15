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
$script:CleanupDone = $false

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

function Add-ProcessPathEntry {
    param([Parameter(Mandatory)][string]$PathEntry)

    $entry = $PathEntry.Trim()
    if (-not $entry) { return }
    try {
        if (-not (Test-Path -LiteralPath $entry -PathType Container)) { return }
    } catch {
        Write-Host "  -> skipping invalid PATH entry: $entry"
        return
    }

    $separator = [IO.Path]::PathSeparator
    $parts = @()
    if ($env:PATH) {
        $parts = @($env:PATH -split [regex]::Escape([string]$separator) | Where-Object { $_ })
    }
    foreach ($part in $parts) {
        if ([string]::Equals($part.TrimEnd('\'), $entry.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)) {
            return
        }
    }
    $env:PATH = $entry + $separator + $env:PATH
}

function Add-InstalledEnvironmentPath {
    foreach ($scope in @('Machine', 'User')) {
        $pathValue = [Environment]::GetEnvironmentVariable('Path', $scope)
        if (-not $pathValue) { continue }
        foreach ($entry in ($pathValue -split [regex]::Escape([string][IO.Path]::PathSeparator))) {
            if (-not $entry) { continue }
            Add-ProcessPathEntry -PathEntry $entry
        }
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

function Get-ProcessCommandLine {
    param([Parameter(Mandatory)][int]$ProcessId)

    try {
        $proc = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
        if ($proc) { return $proc.CommandLine }
    } catch {}
    return ''
}

function Get-ProcessWorkingDirectory {
    param([Parameter(Mandatory)][int]$ProcessId)

    try {
        $proc = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
        if ($proc -and $proc.Path) {
            return Split-Path -Parent $proc.Path
        }
    } catch {}
    return ''
}

function Stop-ProcessTree {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][int]$ProcessId
    )

    try {
        $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId = $ProcessId" -ErrorAction SilentlyContinue)
        foreach ($child in $children) {
            Stop-ProcessTree -Label $Label -ProcessId ([int]$child.ProcessId)
        }

        $proc = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
        if (-not $proc) { return }
        Write-Host "  -> stopping $Label (PID: $ProcessId)"
        Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
    } catch {}
}

function Stop-StaleViteForPort {
    param([Parameter(Mandatory)][int]$Port)

    try {
        $listeners = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
        foreach ($listener in $listeners) {
            $pidValue = [int]$listener.OwningProcess
            $commandLine = Get-ProcessCommandLine -ProcessId $pidValue
            $workingDir = Get-ProcessWorkingDirectory -ProcessId $pidValue
            $isVite = $commandLine -match 'vite(\.js)?' -or $commandLine -match 'npm(\.cmd)?\s+run\s+dev'
            $isFrontendApp = $commandLine -like "*frontend-app*" -or $workingDir -eq $FrontendAppDir
            if ($isVite -and $isFrontendApp) {
                Stop-ProcessTree -Label "stale frontend-app vite on port $Port" -ProcessId $pidValue
            }
        }
    } catch {}
}

function Get-LogTail {
    param(
        [Parameter(Mandatory)][string]$Path,
        [int]$Count = 80
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return }
    Write-Host "  -> log tail: $Path"
    Get-Content -LiteralPath $Path -Tail $Count -ErrorAction SilentlyContinue | ForEach-Object { Write-Host $_ }
}

function Get-BackendLogTail {
    Get-LogTail -Path $env:SUPER_DOLPHIN_BACKEND_LOG
    if ($env:SUPER_DOLPHIN_BACKEND_ERR_LOG) { Get-LogTail -Path $env:SUPER_DOLPHIN_BACKEND_ERR_LOG }
}

function Get-FrontendLogTail {
    Get-LogTail -Path $env:SUPER_DOLPHIN_FRONTEND_LOG
    if ($env:SUPER_DOLPHIN_FRONTEND_ERR_LOG) { Get-LogTail -Path $env:SUPER_DOLPHIN_FRONTEND_ERR_LOG }
}

function Wait-ForHttp {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Label
    )

    $attempts = Get-PositiveIntegerEnv -Name 'SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS' -DefaultValue 300
    $pollMilliseconds = Get-PositivePollMilliseconds
    for ($i = 0; $i -lt $attempts; $i++) {
        try {
            $null = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 1 -ErrorAction Stop
            Write-Host "  -> $Label ready: $Url"
            return
        } catch {
            Start-Sleep -Milliseconds $pollMilliseconds
        }
    }

    throw "timed out waiting for $Label`: $Url"
}

function Wait-ForBackend {
    $url = "http://$($env:SUPER_DOLPHIN_HTTP_ADDR)/metrics"
    $attempts = Get-PositiveIntegerEnv -Name 'SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS' -DefaultValue 300
    $pollMilliseconds = Get-PositivePollMilliseconds
    for ($i = 0; $i -lt $attempts; $i++) {
        try {
            $null = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 1 -ErrorAction Stop
            Write-Host "  -> desktop backend ready: $url"
            return
        } catch {
            if ($script:DesktopProcess -and $script:DesktopProcess.HasExited) {
                Write-Host "XX desktop backend exited before readiness: $url"
                Get-BackendLogTail
                throw 'desktop backend exited before readiness'
            }
            Start-Sleep -Milliseconds $pollMilliseconds
        }
    }

    Write-Host "XX timed out waiting for desktop backend: $url"
    Get-BackendLogTail
    throw "timed out waiting for desktop backend: $url"
}

function Get-PositiveIntegerEnv {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][int]$DefaultValue
    )

    $raw = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $raw -or $raw.Trim() -eq '') { return $DefaultValue }
    $value = 0
    if (-not [int]::TryParse($raw, [ref]$value) -or $value -le 0) {
        throw "$Name must be a positive integer, got: $raw"
    }
    return $value
}

function Get-PositivePollMilliseconds {
    $raw = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS', 'Process')
    if ($null -eq $raw -or $raw.Trim() -eq '') { return 200 }
    $value = 0.0
    if (-not [double]::TryParse($raw, [Globalization.NumberStyles]::Float, [Globalization.CultureInfo]::InvariantCulture, [ref]$value) -or $value -le 0) {
        throw "SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS must be a positive number, got: $raw"
    }
    return [Math]::Max(1, [int]($value * 1000))
}

function Wait-ForAnyProcessExit {
    while ($true) {
        if ($script:DesktopProcess -and $script:DesktopProcess.HasExited) {
            if ($script:DesktopProcess.ExitCode -ne 0) {
                Get-BackendLogTail
            }
            exit $script:DesktopProcess.ExitCode
        }
        if ($script:ViteProcess -and $script:ViteProcess.HasExited) {
            if ($script:ViteProcess.ExitCode -ne 0) {
                Get-FrontendLogTail
            }
            exit $script:ViteProcess.ExitCode
        }
        Start-Sleep -Milliseconds 500
    }
}

function Resolve-NpmCommand {
    if ($env:SUPER_DOLPHIN_NPM_CMD -and (Test-Path -LiteralPath $env:SUPER_DOLPHIN_NPM_CMD -PathType Leaf)) {
        return $env:SUPER_DOLPHIN_NPM_CMD
    }

    $cmd = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    $candidateDirs = New-Object System.Collections.Generic.List[string]
    if ($env:SUPER_DOLPHIN_NODE_BIN_DIR) {
        $candidateDirs.Add($env:SUPER_DOLPHIN_NODE_BIN_DIR)
    }
    foreach ($dir in @(
        (Join-Path $env:ProgramFiles 'nodejs'),
        (Join-Path ${env:ProgramFiles(x86)} 'nodejs'),
        (Join-Path $env:LOCALAPPDATA 'Programs\nodejs')
    )) {
        if ($dir) { $candidateDirs.Add($dir) }
    }

    $wingetPackages = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
    if (Test-Path -LiteralPath $wingetPackages -PathType Container) {
        Get-ChildItem -LiteralPath $wingetPackages -Directory -Filter 'OpenJS.NodeJS*' -ErrorAction SilentlyContinue |
            ForEach-Object {
                Get-ChildItem -LiteralPath $_.FullName -Recurse -Filter 'npm.cmd' -ErrorAction SilentlyContinue |
                    ForEach-Object { $candidateDirs.Add($_.DirectoryName) }
            }
    }

    foreach ($dir in ($candidateDirs | Select-Object -Unique)) {
        if (-not $dir) { continue }
        $candidate = Join-Path $dir 'npm.cmd'
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            Add-ProcessPathEntry -PathEntry $dir
            return $candidate
        }
    }

    $cmd = Get-Command npm -CommandType Application -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    throw 'npm.cmd was not found; install Node.js LTS or set SUPER_DOLPHIN_NPM_CMD to npm.cmd'
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
        Set-DefaultEnv -Name 'DATABASE_URL' -Value "postgres://super_dolphin@127.0.0.1:$($env:SUPER_DOLPHIN_LOCAL_POSTGRES_PORT)/super_dolphin?sslmode=disable"
        Set-DefaultEnv -Name 'DEV_LOCAL_POSTGRES_MANAGED' -Value '1'
        Write-Host '  -> local PostgreSQL enabled for dev runtime'
    }
}

function Initialize-LocalPostgresDataDir {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR) | Out-Null
    Write-Host "  -> initializing local PostgreSQL data dir: $($env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR)"

    $initdb = Get-PostgresExecutablePath -BinDir $env:SUPER_DOLPHIN_POSTGRES_BIN_DIR -Name 'initdb'
    if (-not $initdb) { throw 'initdb was not found' }

    & $initdb -D $env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR `
        -U super_dolphin `
        -L $env:SUPER_DOLPHIN_POSTGRES_SHARE_DIR `
        '--locale=C' `
        '--auth=trust' `
        '--encoding=UTF8' *> $null
    if ($LASTEXITCODE -ne 0) { throw 'failed to initialize local PostgreSQL data dir' }
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
        if ($env:DEV_LOCAL_POSTGRES_MANAGED -eq '1') {
            Initialize-LocalPostgresDataDir
        } else {
            throw "DATABASE_URL points to local PostgreSQL ($($endpoint.Host):$($endpoint.Port)), but data dir is not initialized: $($env:SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR)"
        }
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

function Seed-DevPreferences {
    $seedPreference = if ($env:SUPER_DOLPHIN_SEED_DEV_PREFERENCES) { $env:SUPER_DOLPHIN_SEED_DEV_PREFERENCES } else { '1' }
    switch ($seedPreference) {
        { $_ -in @('0', 'false', 'FALSE', 'no', 'NO') } {
            Write-Host '  -> dev provider preference seed skipped'
            return
        }
    }

    if ($env:DEV_LOCAL_POSTGRES_MANAGED -ne '1') { return }

    foreach ($name in @(
        'SUPER_DOLPHIN_DEV_PROVIDER',
        'SUPER_DOLPHIN_DEV_CODEX_MODEL',
        'SUPER_DOLPHIN_DEV_CODEX_EFFORT',
        'SUPER_DOLPHIN_DEV_CODEX_HOME',
        'SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY',
        'SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER'
    )) {
        if (-not [Environment]::GetEnvironmentVariable($name, 'Process')) {
            throw 'dev provider preferences require non-empty SUPER_DOLPHIN_DEV_PROVIDER, SUPER_DOLPHIN_DEV_CODEX_MODEL, SUPER_DOLPHIN_DEV_CODEX_EFFORT, SUPER_DOLPHIN_DEV_CODEX_HOME, SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY, and SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER'
        }
    }

    if ($env:SUPER_DOLPHIN_DEV_PROVIDER -ne 'codex') {
        throw "run-new-ui-desktop.ps1 only seeds codex dev provider preferences; got SUPER_DOLPHIN_DEV_PROVIDER=$($env:SUPER_DOLPHIN_DEV_PROVIDER)"
    }

    $psql = Get-PostgresExecutablePath -BinDir $env:SUPER_DOLPHIN_POSTGRES_BIN_DIR -Name 'psql'
    if (-not $psql) { throw "missing PostgreSQL psql binary: $($env:SUPER_DOLPHIN_POSTGRES_BIN_DIR)\psql.exe" }

    $sql = @"
INSERT INTO ui_preferences (cwd, key, value)
VALUES
  ('', 'settings.provider.active', to_jsonb(:'active_provider'::text)),
  ('', 'settings.provider.codex.model', to_jsonb(:'codex_model'::text)),
  ('', 'settings.provider.codex.effort', to_jsonb(:'codex_effort'::text)),
  ('', 'settings.provider.codex.codexHome', to_jsonb(:'codex_home'::text)),
  ('', 'settings.provider.codex.codexInstanceKey', to_jsonb(:'codex_instance_key'::text)),
  ('', 'settings.provider.codex.codexModelProvider', to_jsonb(:'codex_model_provider'::text))
ON CONFLICT (cwd, key) DO NOTHING;
"@

    $sqlFile = Join-Path $script:RunLogDir 'seed-dev-preferences.sql'
    New-Item -ItemType Directory -Force -Path $script:RunLogDir | Out-Null
    Set-Content -LiteralPath $sqlFile -Value $sql -Encoding UTF8
    $psqlArgs = @(
        '--set=ON_ERROR_STOP=1',
        "--set=active_provider=$($env:SUPER_DOLPHIN_DEV_PROVIDER)",
        "--set=codex_model=$($env:SUPER_DOLPHIN_DEV_CODEX_MODEL)",
        "--set=codex_effort=$($env:SUPER_DOLPHIN_DEV_CODEX_EFFORT)",
        "--set=codex_home=$($env:SUPER_DOLPHIN_DEV_CODEX_HOME)",
        "--set=codex_instance_key=$($env:SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY)",
        "--set=codex_model_provider=$($env:SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER)",
        '-d',
        $env:DATABASE_URL,
        '-f',
        $sqlFile
    )
    & $psql @psqlArgs *> $null
    if ($LASTEXITCODE -ne 0) { throw 'failed to seed dev provider preferences' }
    Write-Host '  -> dev provider preferences ready'
}

function Stop-StartedProcesses {
    if ($script:CleanupDone) { return }
    $script:CleanupDone = $true

    if ($script:ViteProcess -and -not $script:ViteProcess.HasExited) {
        Stop-ProcessTree -Label 'frontend-app vite' -ProcessId $script:ViteProcess.Id
    }

    if ($script:DesktopProcess -and -not $script:DesktopProcess.HasExited) {
        Stop-ProcessTree -Label 'new UI desktop backend' -ProcessId $script:DesktopProcess.Id
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
Add-InstalledEnvironmentPath

Set-DefaultEnv -Name 'SUPER_DOLPHIN_HTTP_ADDR' -Value '127.0.0.1:4512'
Set-DefaultEnv -Name 'GO_AGENT_CTL_RPC_ADDR' -Value '127.0.0.1:8092'
Set-DefaultEnv -Name 'VITE_DEV_URL' -Value 'http://127.0.0.1:5175'
Set-DefaultEnv -Name 'FRONTEND_DEVSERVER_URL' -Value $env:VITE_DEV_URL
if ($env:FRONTEND_DEVSERVER_URL -ne $env:VITE_DEV_URL) {
    throw "FRONTEND_DEVSERVER_URL must match VITE_DEV_URL for Wails readiness, got FRONTEND_DEVSERVER_URL=$($env:FRONTEND_DEVSERVER_URL) VITE_DEV_URL=$($env:VITE_DEV_URL)"
}

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
Set-DefaultEnv -Name 'SUPER_DOLPHIN_BACKEND_LOG' -Value (Join-Path $script:RunLogDir 'backend.log')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_FRONTEND_LOG' -Value (Join-Path $script:RunLogDir 'frontend.log')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_BACKEND_ERR_LOG' -Value (Join-Path $script:RunLogDir 'backend.err.log')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_FRONTEND_ERR_LOG' -Value (Join-Path $script:RunLogDir 'frontend.err.log')
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_PROVIDER' -Value 'codex'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_CODEX_MODEL' -Value 'gpt-5.5'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_CODEX_EFFORT' -Value 'xhigh'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_CODEX_HOME' -Value $env:CODEX_HOME
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY' -Value 'default'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER' -Value 'openai'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_LOCAL_POSTGRES_PORT' -Value '55433'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS' -Value '300'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS' -Value '300'
Set-DefaultEnv -Name 'SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS' -Value '0.2'
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
    Stop-StaleViteForPort -Port $ViteDevPort
    Assert-PortFree -Address "$ViteDevHost`:$ViteDevPort"
    Assert-PortFree -Address $env:SUPER_DOLPHIN_HTTP_ADDR
    Assert-PortFree -Address $env:GO_AGENT_CTL_RPC_ADDR
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
    Write-Host "  home:         $($env:SUPER_DOLPHIN_HOME)"
    Write-Host "  logs:         $($env:SUPER_DOLPHIN_BACKEND_LOG)"

    New-Item -ItemType Directory -Force -Path $script:RunLogDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:SUPER_DOLPHIN_BACKEND_LOG) | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:SUPER_DOLPHIN_FRONTEND_LOG) | Out-Null
    New-Item -ItemType Directory -Force -Path $env:SUPER_DOLPHIN_HOME | Out-Null
    Remove-Item -LiteralPath $env:SUPER_DOLPHIN_BACKEND_LOG, $env:SUPER_DOLPHIN_BACKEND_ERR_LOG, $env:SUPER_DOLPHIN_FRONTEND_LOG, $env:SUPER_DOLPHIN_FRONTEND_ERR_LOG -Force -ErrorAction SilentlyContinue

    $npm = Resolve-NpmCommand
    $script:ViteProcess = Start-Process -FilePath $npm `
        -ArgumentList @('run', 'dev', '--', '--host', $ViteDevHost, '--port', "$ViteDevPort", '--strictPort') `
        -WorkingDirectory $FrontendAppDir `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $env:SUPER_DOLPHIN_FRONTEND_LOG `
        -RedirectStandardError $env:SUPER_DOLPHIN_FRONTEND_ERR_LOG
    Wait-ForHttp -Url $env:FRONTEND_DEVSERVER_URL -Label 'frontend-app vite'

    $script:DesktopProcess = Start-Process -FilePath 'go' `
        -ArgumentList @('run', './cmd/agent-terminal') `
        -WorkingDirectory $ProjectDir `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $env:SUPER_DOLPHIN_BACKEND_LOG `
        -RedirectStandardError $env:SUPER_DOLPHIN_BACKEND_ERR_LOG

    Wait-ForBackend
    Seed-DevPreferences

    Wait-ForAnyProcessExit
} finally {
    Stop-StartedProcesses
}
