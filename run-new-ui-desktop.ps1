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

function Wait-ForBackend {
    $url = "http://$($env:SUPER_DOLPHIN_HTTP_ADDR)/metrics"
    for ($i = 0; $i -lt 100; $i++) {
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
            Start-Sleep -Milliseconds 200
        }
    }

    Write-Host "XX timed out waiting for desktop backend: $url"
    Get-BackendLogTail
    throw "timed out waiting for desktop backend: $url"
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

function Ensure-SqliteRuntime {
    Set-DefaultEnv -Name 'SUPER_DOLPHIN_PROCESS_ROLE' -Value 'desktop'
    Set-DefaultEnv -Name 'SUPER_DOLPHIN_SQLITE_PATH' -Value (Join-Path $env:SUPER_DOLPHIN_HOME 'super-dolphin.db')
    if (-not $env:SUPER_DOLPHIN_SQLITE_PATH -or $env:SUPER_DOLPHIN_SQLITE_PATH.Trim() -eq '') {
        throw 'SUPER_DOLPHIN_SQLITE_PATH must not be empty'
    }
    $parent = Split-Path -Parent $env:SUPER_DOLPHIN_SQLITE_PATH
    if (-not $parent) {
        throw "SUPER_DOLPHIN_SQLITE_PATH must include a parent directory: $($env:SUPER_DOLPHIN_SQLITE_PATH)"
    }
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
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

}

Import-DotEnvFile -Path (Join-Path $ProjectDir '.env')
Add-InstalledEnvironmentPath

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
Set-DefaultEnv -Name 'LOG_LEVEL' -Value 'debug'
Set-DefaultEnv -Name 'ENABLE_MEMORY_SYSTEM' -Value '1'
Set-DefaultEnv -Name 'ENABLE_MEMORY_TOOLS' -Value '1'
Set-DefaultEnv -Name 'MULTI_AGENT_MEMORY_FEATURE_TEAMMEM' -Value '1'
Set-DefaultEnv -Name 'CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME' -Value '1'

try {
    Add-CodexCliToPath
    Ensure-DevControlSessionToken
    Ensure-SqliteRuntime
    Stop-StaleViteForPort -Port $ViteDevPort
    Assert-PortFree -Address "$ViteDevHost`:$ViteDevPort"
    Assert-PortFree -Address $env:SUPER_DOLPHIN_HTTP_ADDR
    Assert-PortFree -Address $env:GO_AGENT_CTL_RPC_ADDR
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
    Write-Host "  sqlite:       $($env:SUPER_DOLPHIN_SQLITE_PATH)"
    Write-Host "  logs:         $($env:SUPER_DOLPHIN_BACKEND_LOG)"

    New-Item -ItemType Directory -Force -Path $script:RunLogDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:SUPER_DOLPHIN_BACKEND_LOG) | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:SUPER_DOLPHIN_FRONTEND_LOG) | Out-Null
    New-Item -ItemType Directory -Force -Path $env:SUPER_DOLPHIN_HOME | Out-Null
    Remove-Item -LiteralPath $env:SUPER_DOLPHIN_BACKEND_LOG, $env:SUPER_DOLPHIN_BACKEND_ERR_LOG, $env:SUPER_DOLPHIN_FRONTEND_LOG, $env:SUPER_DOLPHIN_FRONTEND_ERR_LOG -Force -ErrorAction SilentlyContinue

    $script:DesktopProcess = Start-Process -FilePath 'go' `
        -ArgumentList @('run', './cmd/agent-terminal') `
        -WorkingDirectory $ProjectDir `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $env:SUPER_DOLPHIN_BACKEND_LOG `
        -RedirectStandardError $env:SUPER_DOLPHIN_BACKEND_ERR_LOG

    Wait-ForBackend

    $npm = Resolve-NpmCommand
    $script:ViteProcess = Start-Process -FilePath $npm `
        -ArgumentList @('run', 'dev', '--', '--host', $ViteDevHost, '--port', "$ViteDevPort", '--strictPort') `
        -WorkingDirectory $FrontendAppDir `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $env:SUPER_DOLPHIN_FRONTEND_LOG `
        -RedirectStandardError $env:SUPER_DOLPHIN_FRONTEND_ERR_LOG
    Wait-ForHttp -Url $env:VITE_DEV_URL -Label 'frontend-app vite'

    Wait-ForAnyProcessExit
} finally {
    Stop-StartedProcesses
}
