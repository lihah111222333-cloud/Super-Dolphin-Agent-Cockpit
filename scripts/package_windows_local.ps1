param(
    [ValidateSet('standard', 'full', 'all')]
    [string]$Target = $(if ($env:SUPER_DOLPHIN_PACKAGE_TARGET) { $env:SUPER_DOLPHIN_PACKAGE_TARGET } else { 'standard' }),
    [ValidateSet('all', 'installer', 'zip')]
    [string]$Artifact = $(if ($env:SUPER_DOLPHIN_WINDOWS_OUTPUT) { $env:SUPER_DOLPHIN_WINDOWS_OUTPUT } else { 'all' }),
    [switch]$KeepStage
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-RepoRoot() {
    $git = Get-Command git.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($git) {
        $previousErrorActionPreference = $ErrorActionPreference
        $root = ''
        $gitExitCode = 1
        try {
            $ErrorActionPreference = 'Continue'
            $root = (& $git.Source rev-parse --show-toplevel 2>$null)
            $gitExitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        if ($gitExitCode -eq 0 -and $root -and $root.Trim() -ne '') {
            return $root.Trim()
        }
    }
    $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
    $root = Split-Path -Parent $scriptDir
    if ((Test-Path -LiteralPath (Join-Path $root 'go.mod') -PathType Leaf) -and
        (Test-Path -LiteralPath (Join-Path $root 'frontend-app/package.json') -PathType Leaf)) {
        return $root
    }
    throw 'unable to resolve repository root; run from a git worktree or keep package_windows_local.ps1 under <repo>\scripts'
}

$Root = Resolve-RepoRoot
Set-Location -LiteralPath $Root

function Get-CommandSource() {
    param([Parameter(Mandatory)][string]$Name)
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { return $cmd.Source }
    return ''
}

function Require-NonEmptyEnv() {
    param([Parameter(Mandatory)][string]$Name)
    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $value -or $value.Trim() -eq '') {
        throw "$Name is required"
    }
    return $value
}

$GoOS = (& go env GOOS).Trim()

function Resolve-WindowsPackageArch() {
    $configured = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_ARCH', 'Process')
    $arch = if ($null -ne $configured -and $configured.Trim() -ne '') { $configured.Trim() } else { (& go env GOARCH).Trim() }
    switch ($arch) {
        'amd64' { return 'amd64' }
        'arm64' { return 'arm64' }
        default { throw "unsupported SUPER_DOLPHIN_WINDOWS_ARCH=$arch; expected amd64 or arm64" }
    }
}

$WindowsPackageArch = Resolve-WindowsPackageArch
$env:SUPER_DOLPHIN_WINDOWS_ARCH = $WindowsPackageArch
$Platform = "$GoOS-$WindowsPackageArch"
$RequestedWindowsOutput = $Artifact
$RequestedKeepStage = $KeepStage.IsPresent -or ([Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_KEEP_STAGE', 'Process') -eq '1')
$RelayUrl = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_CODEX_RELAY_BASE_URL', 'Process')
$BootstrapToken = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN', 'Process')
$PostgresDist = if ($env:SUPER_DOLPHIN_POSTGRES_DIST) { $env:SUPER_DOLPHIN_POSTGRES_DIST } else { Join-Path $Root "third_party/postgres/$Platform" }
$CodexBin = if ($env:SUPER_DOLPHIN_CODEX_ARTIFACT) { $env:SUPER_DOLPHIN_CODEX_ARTIFACT } else { Get-CommandSource 'codex.exe' }
$FFmpegBin = if ($env:SUPER_DOLPHIN_FFMPEG_BIN) { $env:SUPER_DOLPHIN_FFMPEG_BIN } else { Get-CommandSource 'ffmpeg.exe' }

if ($GoOS -ne 'windows') {
    throw "package_windows_local.ps1 must run on Windows; current GOOS=$GoOS"
}
if ($null -eq $BootstrapToken -or $BootstrapToken.Trim() -eq '') {
    throw 'SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required; packaging helpers do not prompt for or accept privileged API keys'
}
if ($null -eq $RelayUrl -or $RelayUrl.Trim() -eq '') {
    throw 'SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required'
}
if ([Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_CODEX_RELAY_API_KEY', 'Process')) {
    throw 'SUPER_DOLPHIN_CODEX_RELAY_API_KEY must not be set for packaging'
}
if (-not (Test-Path -LiteralPath $CodexBin -PathType Leaf)) {
    throw 'missing Codex artifact; set SUPER_DOLPHIN_CODEX_ARTIFACT'
}
if (-not (Test-Path -LiteralPath $PostgresDist -PathType Container)) {
    throw "missing PostgreSQL dist; set SUPER_DOLPHIN_POSTGRES_DIST: $PostgresDist"
}
if (-not (Test-Path -LiteralPath $FFmpegBin -PathType Leaf)) {
    throw 'missing ffmpeg.exe; install ffmpeg or set SUPER_DOLPHIN_FFMPEG_BIN'
}
& $FFmpegBin -version *> $null
if ($LASTEXITCODE -ne 0) {
    throw "ffmpeg smoke failed: $FFmpegBin -version"
}
$env:SUPER_DOLPHIN_FFMPEG_BIN = $FFmpegBin

function Package-One() {
    param([Parameter(Mandatory)][string]$Profile)

    $appName = if ($Profile -eq 'full') { 'super-dolphin-full-lsp' } else { 'super-dolphin' }
    $lspDir = if ($env:SUPER_DOLPHIN_LSP_BUNDLE_DIR) { $env:SUPER_DOLPHIN_LSP_BUNDLE_DIR } else { Join-Path $Root ".build-cache/lsp/$Profile/$Platform" }
    Write-Host "==> packaging Windows $Profile profile as $appName"

    $env:SUPER_DOLPHIN_LSP_PROFILE = $Profile
    $env:SUPER_DOLPHIN_LSP_BUNDLE_DIR = $lspDir
    & (Join-Path $Root 'scripts/prepare_lsp_bundle_windows.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'prepare_lsp_bundle_windows.ps1 failed' }

    $env:APP_NAME = $appName
    $env:SUPER_DOLPHIN_POSTGRES_DIST = $PostgresDist
    $env:SUPER_DOLPHIN_CODEX_ARTIFACT = $CodexBin
    $env:SUPER_DOLPHIN_CODEX_SHA256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $CodexBin).Hash.ToLowerInvariant()
    $env:SUPER_DOLPHIN_CODEX_VERSION = (& $CodexBin --version | Select-Object -First 1)
    $env:SUPER_DOLPHIN_CODEX_RELAY_BASE_URL = $RelayUrl
    $env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN = $BootstrapToken
    $env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF = if ($env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF) { $env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF } else { 'local-private-package' }
    Remove-Item Env:SUPER_DOLPHIN_CODEX_RELAY_API_KEY -ErrorAction SilentlyContinue

    $packageArgs = @{ Artifact = $RequestedWindowsOutput }
    if ($RequestedKeepStage) {
        $packageArgs.KeepStage = $true
    }
    & (Join-Path $Root 'scripts/package_windows.ps1') @packageArgs
    if ($LASTEXITCODE -ne 0) { throw 'package_windows.ps1 failed' }
    Write-Host "Windows package ready under: $(Join-Path $Root 'dist/package/windows')"
}

if ($Target -eq 'all') {
    Package-One -Profile 'standard'
    Package-One -Profile 'full'
} else {
    Package-One -Profile $Target
}

Write-Warning 'local package contains the provided relay bootstrap token in .env; do not distribute it.'
