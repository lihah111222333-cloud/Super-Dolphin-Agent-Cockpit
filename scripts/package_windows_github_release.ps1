param(
    [string]$StageDir = $(if ($env:SUPER_DOLPHIN_RELEASE_STAGE_DIR) { $env:SUPER_DOLPHIN_RELEASE_STAGE_DIR } else { '' })
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-RepoRoot() {
    $root = (& git rev-parse --show-toplevel 2>$null)
    if ($LASTEXITCODE -eq 0 -and $root -and $root.Trim() -ne '') {
        return $root.Trim()
    }
    throw 'unable to resolve repository root; run from a git worktree'
}

function Require-Env() {
    param([Parameter(Mandatory)][string]$Name)
    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $value -or $value.Trim() -eq '') {
        throw "$Name is required"
    }
    return $value
}

function Get-EnvOrDefault() {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Default
    )
    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $value -or $value.Trim() -eq '') {
        return $Default
    }
    return $value
}

$Root = Resolve-RepoRoot
Set-Location -LiteralPath $Root

if ((& go env GOOS).Trim() -ne 'windows') {
    throw 'package_windows_github_release.ps1 must run on Windows'
}

$Version = Require-Env -Name 'VERSION'
$PackageVersion = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_PACKAGE_VERSION' -Default $Version.TrimStart('v', 'V')
$SigningKey = Require-Env -Name 'SUPER_DOLPHIN_UPDATE_SIGNING_KEY'
$PublicKey = Require-Env -Name 'SUPER_DOLPHIN_UPDATE_PUBLIC_KEY'
$GitHubReleaseRepo = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_UPDATE_GITHUB_REPO' -Default 'xiaoxiaotest9527-bit/-'
$Channel = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_UPDATE_CHANNEL' -Default 'gray'
$MinimumVersion = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION' -Default '0.0.0'
if ($StageDir.Trim() -eq '') {
    $StageDir = Join-Path $Root "dist/release/github/$Version"
}

$env:SUPER_DOLPHIN_WINDOWS_ARCH = 'arm64'
$env:SUPER_DOLPHIN_WINDOWS_OUTPUT = 'installer'
$env:SUPER_DOLPHIN_UPDATE_ENABLED = '1'
$env:SUPER_DOLPHIN_UPDATE_GITHUB_REPO = $GitHubReleaseRepo
$env:SUPER_DOLPHIN_UPDATE_PUBLIC_KEY = $PublicKey
$env:SUPER_DOLPHIN_UPDATE_CHANNEL = $Channel
$env:SUPER_DOLPHIN_UPDATE_VERSION = $PackageVersion
$env:VERSION = $PackageVersion

& go run .\cmd\super-dolphin-release-manifest `
    -check-key `
    -signing-key $SigningKey `
    -public-key $PublicKey
if ($LASTEXITCODE -ne 0) { throw 'release manifest signing key check failed' }

& (Join-Path $Root 'scripts\package_windows_local.ps1') standard -Artifact installer
if ($LASTEXITCODE -ne 0) { throw 'Windows local release package failed' }

$sourceInstaller = Join-Path $Root "dist/package/windows/SuperDolphinSetup-$PackageVersion-windows-arm64.exe"
if (-not (Test-Path -LiteralPath $sourceInstaller -PathType Leaf)) {
    throw "missing Windows ARM installer artifact: $sourceInstaller"
}

New-Item -ItemType Directory -Force -Path $StageDir | Out-Null
$assetName = 'Super-Dolphin-windows-arm64.exe'
$manifestName = 'Super-Dolphin-windows-arm64.update.json'
$artifact = Join-Path $StageDir $assetName
$manifest = Join-Path $StageDir $manifestName
$artifactUrl = "https://github.com/$GitHubReleaseRepo/releases/download/$Version/$assetName"
Copy-Item -LiteralPath $sourceInstaller -Destination $artifact -Force

& go run ./cmd/super-dolphin-release-manifest `
    -artifact $artifact `
    -artifact-url $artifactUrl `
    -app-id super-dolphin `
    -channel $Channel `
    -version $Version `
    -minimum-version $MinimumVersion `
    -platform windows-arm64 `
    -signing-key $SigningKey `
    -public-key $PublicKey `
    -out $manifest
if ($LASTEXITCODE -ne 0) { throw 'Windows update manifest generation failed' }

& go run ./cmd/super-dolphin-release-manifest `
    -verify-manifest $manifest `
    -artifact $artifact `
    -artifact-url $artifactUrl `
    -app-id super-dolphin `
    -channel $Channel `
    -version $Version `
    -minimum-version $MinimumVersion `
    -platform windows-arm64 `
    -public-key $PublicKey
if ($LASTEXITCODE -ne 0) { throw 'Windows update manifest verification failed' }

Write-Host "Windows GitHub release assets ready under: $StageDir"
