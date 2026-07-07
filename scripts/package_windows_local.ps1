param(
    [ValidateSet('standard', 'full', 'all')]
    [string]$Target = $(if ($env:SUPER_DOLPHIN_PACKAGE_TARGET) { $env:SUPER_DOLPHIN_PACKAGE_TARGET } else { 'standard' }),
    [ValidateSet('all', 'installer', 'zip')]
    [string]$Artifact = $(if ($env:SUPER_DOLPHIN_WINDOWS_OUTPUT) { $env:SUPER_DOLPHIN_WINDOWS_OUTPUT } else { 'all' }),
    [switch]$KeepStage,
    [switch]$Fast
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

function Test-TruthyEnv() {
    param([Parameter(Mandatory)][string]$Name)
    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $value) { return $false }
    switch ($value.Trim().ToLowerInvariant()) {
        '' { return $false }
        '0' { return $false }
        'false' { return $false }
        'no' { return $false }
        'off' { return $false }
        default { return $true }
    }
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
$ConfiguredWindowsOutput = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_OUTPUT', 'Process')
$WindowsOutputConfigured = $PSBoundParameters.ContainsKey('Artifact') -or ($null -ne $ConfiguredWindowsOutput -and $ConfiguredWindowsOutput.Trim() -ne '')
$RequestedWindowsOutput = $Artifact
$RequestedKeepStage = $KeepStage.IsPresent -or (Test-TruthyEnv 'SUPER_DOLPHIN_WINDOWS_KEEP_STAGE')
$ConfiguredLSPBundleDir = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_LSP_BUNDLE_DIR', 'Process')
$FastMode = $Fast.IsPresent -or (Test-TruthyEnv 'SUPER_DOLPHIN_WINDOWS_FAST')
if ($FastMode -and -not $WindowsOutputConfigured) {
    $RequestedWindowsOutput = 'installer'
}
if ($FastMode) {
    $RequestedKeepStage = $true
    if (-not $env:SUPER_DOLPHIN_REUSE_LSP_BUNDLE) {
        $env:SUPER_DOLPHIN_REUSE_LSP_BUNDLE = '1'
    }
    if (-not $env:SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION) {
        $env:SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION = 'zip'
    }
    if (-not $env:SUPER_DOLPHIN_WINDOWS_INSTALLER_SOLID_COMPRESSION) {
        $env:SUPER_DOLPHIN_WINDOWS_INSTALLER_SOLID_COMPRESSION = 'no'
    }
    Write-Host '==> fast Windows packaging enabled: installer-only default, reusable LSP bundle, fast installer compression'
}
$RelayUrl = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_CODEX_RELAY_BASE_URL', 'Process')
$BootstrapToken = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN', 'Process')
$UpdateManifestURL = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_UPDATE_MANIFEST_URL', 'Process')
$UpdateGitHubRepo = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_UPDATE_GITHUB_REPO', 'Process')
$UpdateWindowsPublisher = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER', 'Process')
$UpdateWindowsThumbprint = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT', 'Process')
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
if (-not (Test-Path -LiteralPath $FFmpegBin -PathType Leaf)) {
    throw 'missing ffmpeg.exe; install ffmpeg or set SUPER_DOLPHIN_FFMPEG_BIN'
}
& $FFmpegBin -version *> $null
if ($LASTEXITCODE -ne 0) {
    throw "ffmpeg smoke failed: $FFmpegBin -version"
}
$env:SUPER_DOLPHIN_FFMPEG_BIN = $FFmpegBin

function Forward-UpdateEnv() {
    if (($null -ne $script:UpdateManifestURL -and $script:UpdateManifestURL.Trim() -ne '') -or
        ($null -ne $script:UpdateGitHubRepo -and $script:UpdateGitHubRepo.Trim() -ne '')) {
        $env:SUPER_DOLPHIN_UPDATE_ENABLED = '1'
    }
    if ($null -ne $script:UpdateManifestURL) {
        $env:SUPER_DOLPHIN_UPDATE_MANIFEST_URL = $script:UpdateManifestURL
    }
    if ($null -ne $script:UpdateGitHubRepo) {
        $env:SUPER_DOLPHIN_UPDATE_GITHUB_REPO = $script:UpdateGitHubRepo
    }
    if (-not $env:SUPER_DOLPHIN_UPDATE_CHANNEL) {
        $env:SUPER_DOLPHIN_UPDATE_CHANNEL = 'gray'
    }
    if (-not $env:SUPER_DOLPHIN_UPDATE_VERSION -and $env:VERSION) {
        $env:SUPER_DOLPHIN_UPDATE_VERSION = $env:VERSION
    }
    if ($null -ne $script:UpdateWindowsPublisher) {
        $env:SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER = $script:UpdateWindowsPublisher
    }
    if ($null -ne $script:UpdateWindowsThumbprint) {
        $env:SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT = $script:UpdateWindowsThumbprint
    }
    if ($env:SUPER_DOLPHIN_UPDATE_PUBLIC_KEY) {
        $env:SUPER_DOLPHIN_UPDATE_PUBLIC_KEY = $env:SUPER_DOLPHIN_UPDATE_PUBLIC_KEY
    }
}

function Resolve-LSPBundleDir() {
    param([Parameter(Mandatory)][string]$Profile)
    if ($null -ne $script:ConfiguredLSPBundleDir -and $script:ConfiguredLSPBundleDir.Trim() -ne '') {
        return $script:ConfiguredLSPBundleDir.Trim()
    }
    $cacheDir = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_PACKAGE_CACHE_DIR', 'Process')
    if ($script:FastMode -and $null -ne $cacheDir -and $cacheDir.Trim() -ne '') {
        return Join-Path $cacheDir.Trim() "lsp/$Profile/$Platform"
    }
    return Join-Path $Root ".build-cache/lsp/$Profile/$Platform"
}

function Test-ExistingLSPBundle() {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Profile
    )
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        return $false
    }

    $requiredFiles = @(
        'lsp-manifest.json',
        'lsp-checksums.sha256',
        'node/node.exe',
        'bin/gopls.exe',
        'bin/typescript-language-server.cmd',
        'bin/vscode-css-language-server.cmd',
        'bin/pyright-langserver.cmd',
        'bin/rust-analyzer.exe',
        'bin/bash-language-server.cmd',
        'bin/sql-language-server.cmd',
        'bin/sg.exe',
        'bin/go.cmd'
    )
    if (-not (Test-TruthyEnv 'SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK')) {
        $requiredFiles += 'bin/shellcheck.exe'
    }
    if ($Profile -eq 'full') {
        $requiredFiles += @('bin/java.cmd', 'bin/jdtls.cmd')
    }

    foreach ($relativePath in $requiredFiles) {
        if (-not (Test-Path -LiteralPath (Join-Path $Path $relativePath) -PathType Leaf)) {
            return $false
        }
    }
    return $true
}

function Package-One() {
    param([Parameter(Mandatory)][string]$Profile)

    $appName = if ($Profile -eq 'full') { 'super-dolphin-full-lsp' } else { 'super-dolphin' }
    $lspDir = Resolve-LSPBundleDir -Profile $Profile
    Write-Host "==> packaging Windows $Profile profile as $appName"

    $env:SUPER_DOLPHIN_LSP_PROFILE = $Profile
    $env:SUPER_DOLPHIN_LSP_BUNDLE_DIR = $lspDir
    if ((Test-TruthyEnv 'SUPER_DOLPHIN_REUSE_LSP_BUNDLE') -and (Test-ExistingLSPBundle -Path $lspDir -Profile $Profile)) {
        Write-Host "==> reusing existing Windows LSP bundle: $lspDir"
    } else {
        & (Join-Path $Root 'scripts/prepare_lsp_bundle_windows.ps1')
        if ($LASTEXITCODE -ne 0) { throw 'prepare_lsp_bundle_windows.ps1 failed' }
    }

    $env:APP_NAME = $appName
    $env:SUPER_DOLPHIN_CODEX_ARTIFACT = $CodexBin
    $env:SUPER_DOLPHIN_CODEX_SHA256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $CodexBin).Hash.ToLowerInvariant()
    $env:SUPER_DOLPHIN_CODEX_VERSION = (& $CodexBin --version | Select-Object -First 1)
    $env:SUPER_DOLPHIN_CODEX_RELAY_BASE_URL = $RelayUrl
    $env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN = $BootstrapToken
    $env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF = if ($env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF) { $env:SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF } else { 'local-private-package' }
    Remove-Item Env:SUPER_DOLPHIN_CODEX_RELAY_API_KEY -ErrorAction SilentlyContinue
    Forward-UpdateEnv

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
