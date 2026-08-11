[CmdletBinding(SupportsShouldProcess)]
param(
    [ValidateSet('all', 'installer', 'zip')]
    [string]$Artifact = $(if ($env:SUPER_DOLPHIN_WINDOWS_OUTPUT) { $env:SUPER_DOLPHIN_WINDOWS_OUTPUT } else { 'all' }),
    [switch]$KeepStage
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

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
    throw 'unable to resolve repository root; run from a git worktree or keep package_windows.ps1 under <repo>\scripts'
}

$Root = Resolve-RepoRoot
Set-Location -LiteralPath $Root

function Assert-SafeWindowsPackageIdentity() {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$ReleaseVersion
    )
    $namePattern = '^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$'
    $versionPattern = '^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
    if ($Name -notmatch $namePattern) {
        throw 'invalid APP_NAME: use 1-64 path-component characters and start with a letter or digit'
    }
    if ($Name.EndsWith(' ', [StringComparison]::Ordinal) -or $Name.EndsWith('.', [StringComparison]::Ordinal)) {
        throw 'invalid APP_NAME: Windows path components must not end with a space or dot'
    }
    $deviceStem = $Name.Split('.')[0].ToUpperInvariant()
    if ($deviceStem -match '^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$') {
        throw 'invalid APP_NAME: Windows reserved device names are forbidden'
    }
    if ($ReleaseVersion -notmatch $versionPattern) {
        throw 'invalid VERSION: expected SemVer with an optional v prefix'
    }
}

function Resolve-ContainedWindowsPackagePath() {
    param(
        [Parameter(Mandatory)][string]$DistRoot,
        [Parameter(Mandatory)][string]$Candidate
    )
    $separators = [char[]]@([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $canonicalRoot = [IO.Path]::GetFullPath($DistRoot).TrimEnd($separators)
    $canonicalCandidate = [IO.Path]::GetFullPath($Candidate)
    $requiredPrefix = $canonicalRoot + [IO.Path]::DirectorySeparatorChar
    if (-not $canonicalCandidate.StartsWith($requiredPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "package output escapes fixed dist root: $Candidate"
    }
    return $canonicalCandidate
}

$AppName = if ($env:APP_NAME) { $env:APP_NAME } else { 'super-dolphin' }
$Version = if ($env:VERSION) { $env:VERSION } else { '0.1.0' }
Assert-SafeWindowsPackageIdentity -Name $AppName -ReleaseVersion $Version
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

$CodexRelayBaseUrlEnv = 'SUPER_DOLPHIN_CODEX_RELAY_BASE_URL'
$CodexRelayBootstrapTokenEnv = 'SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN'
$CodexRelayBootstrapProofEnv = 'SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF'
$CodexRelayPrivilegedApiKeyEnv = 'SUPER_DOLPHIN_CODEX_RELAY_API_KEY'
$CodexArtifactEnv = 'SUPER_DOLPHIN_CODEX_ARTIFACT'
$CodexSHA256Env = 'SUPER_DOLPHIN_CODEX_SHA256'
$CodexVersionEnv = 'SUPER_DOLPHIN_CODEX_VERSION'
$VideoAPIKeyEnv = 'SILICONFLOW_API_KEY'
$FFmpegBinEnv = 'SUPER_DOLPHIN_FFMPEG_BIN'
$UpdateOverrideNames = @(
    'SUPER_DOLPHIN_UPDATE_ENABLED',
    'SUPER_DOLPHIN_UPDATE_MANIFEST_URL',
    'SUPER_DOLPHIN_UPDATE_GITHUB_REPO',
    'SUPER_DOLPHIN_UPDATE_PUBLIC_KEY',
    'SUPER_DOLPHIN_UPDATE_CHANNEL',
    'SUPER_DOLPHIN_UPDATE_STAGE_DIR',
    'SUPER_DOLPHIN_UPDATE_HELPER_PATH',
    'SUPER_DOLPHIN_UPDATE_TARGET_APP_PATH',
    'SUPER_DOLPHIN_UPDATE_PLATFORM',
    'SUPER_DOLPHIN_UPDATE_VERSION',
    'SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED',
    'SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER',
    'SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT'
)
$LSPBundleDirEnv = 'SUPER_DOLPHIN_LSP_BUNDLE_DIR'
$LSPManifestName = 'lsp-manifest.json'
$LSPChecksumsName = 'lsp-checksums.sha256'
$RequireBundledCodex = if ($env:SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX) { $env:SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX } else { '1' }
$PackagedRelayBaseUrl = ''
$PackagedRelayBootstrapToken = ''
$PackagedRelayBootstrapProof = ''
$PackagedVideoAPIKey = ''
$PackagedCodexArtifact = ''
$PackagedCodexSHA256 = ''
$PackagedCodexVersion = ''
$PackagedFFmpegBin = ''
$PackagedFFmpegRuntimeDir = ''
$PackagedLSPBundleDir = ''
$LSPProfile = if ($env:SUPER_DOLPHIN_LSP_PROFILE) { $env:SUPER_DOLPHIN_LSP_PROFILE } else { 'standard' }

if ($LSPProfile -notin @('standard', 'full')) {
    throw "unsupported SUPER_DOLPHIN_LSP_PROFILE=$LSPProfile; expected standard or full"
}

$LSPServerSpecs = @(
    'gopls|bin/gopls.exe|gopls.exe',
    'clangd|bin/clangd.exe|clangd.exe',
    'typescript-language-server|bin/typescript-language-server.cmd|typescript-language-server.cmd',
    'vscode-langservers-extracted|bin/vscode-css-language-server.cmd|vscode-css-language-server.cmd',
    'pyright|bin/pyright-langserver.cmd|pyright-langserver.cmd',
    'rust-analyzer|bin/rust-analyzer.exe|rust-analyzer.exe',
    'bash-language-server|bin/bash-language-server.cmd|bash-language-server.cmd',
    'sqruff|bin/sqruff.exe|sqruff.exe',
    'sg|bin/sg.exe|sg.exe',
    'go|bin/go.cmd|go.cmd'
)
if ([Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK', 'Process') -ne '1') {
    $LSPServerSpecs += 'shellcheck|bin/shellcheck.exe|shellcheck.exe'
}
if ($LSPProfile -eq 'full') {
    $LSPServerSpecs += 'java|bin/java.cmd|java.cmd'
    $LSPServerSpecs += 'jdtls|bin/jdtls.cmd|jdtls.cmd'
}
$LSPShadowExecs = @('python.cmd', 'python3.cmd')

function Get-EnvValue() {
    param([Parameter(Mandatory)][string]$Name)
    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ($null -eq $value) { return '' }
    return $value
}

function Validate-EnvFileValue() {
    param(
        [Parameter(Mandatory)][string]$Label,
        [AllowEmptyString()][string]$Value
    )
    if ($null -eq $Value -or $Value.Trim() -eq '') {
        throw "$Label is required and must not be whitespace-only"
    }
    if ($Value.Contains("`n") -or $Value.Contains("`r")) {
        throw "$Label must not contain newline characters"
    }
}

function Test-Truthy() {
    param([AllowEmptyString()][string]$Value)
    if ($null -eq $Value) { return $false }
    switch ($Value.Trim().ToLowerInvariant()) {
        '' { return $false }
        '0' { return $false }
        'false' { return $false }
        'no' { return $false }
        'off' { return $false }
        default { return $true }
    }
}

function Write-Utf8NoBom() {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][AllowEmptyString()][string]$Content
    )
    $dir = Split-Path -Parent $Path
    if ($dir) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
    [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false))
}

function ConvertTo-JsonString() {
    param([AllowEmptyString()][string]$Value)
    return ($Value | ConvertTo-Json -Compress)
}

function Normalize-RelPath() {
    param([Parameter(Mandatory)][string]$Path)
    return $Path.Replace('\', '/')
}

function Assert-RelativePackagePath() {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$Path
    )
    $normalized = Normalize-RelPath $Path
    if ($normalized.Trim() -eq '' -or $normalized.StartsWith('/') -or $normalized -match '^[A-Za-z]:') {
        throw "$Label must be a relative path inside the package: $Path"
    }
    $parts = $normalized.Split('/') | Where-Object { $_ -ne '' }
    if ($parts | Where-Object { $_ -eq '..' }) {
        throw "$Label must not escape the package root: $Path"
    }
}

function Get-SHA256File() {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "missing file for SHA-256: $Path"
    }
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-NodeVersionInput() {
    $version = (& node --version).Trim()
    if ($LASTEXITCODE -ne 0 -or $version -eq '') { throw 'node --version failed while calculating frontend cache key' }
    return $version
}

function Get-NPMVersionInput() {
    $version = (& npm --version).Trim()
    if ($LASTEXITCODE -ne 0 -or $version -eq '') { throw 'npm --version failed while calculating frontend cache key' }
    return $version
}

function Get-GitWorktreeModifiedInput() {
    $status = ((& git -C $Root status --porcelain=v1 --untracked-files=normal) -join "`n").Trim()
    if ($LASTEXITCODE -ne 0) { throw 'git status failed while calculating Go binary cache key' }
    if ($status -ne '') { return 'true' }
    return 'false'
}

function Invoke-BuildPhaseCache() {
    param(
        [Parameter(Mandatory)][ValidateSet('restore', 'save')][string]$Action,
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string[]]$Paths,
        [Parameter(Mandatory)][string[]]$Outputs,
        [string[]]$Inputs = @()
    )
    $arguments = @(
        'run',
        './scripts/build_phase_cache',
        '--action', $Action,
        '--root', $Root,
        '--name', $Name
    )
    foreach ($inputValue in $Inputs) {
        $arguments += @('--input', $inputValue)
    }
    foreach ($path in $Paths) {
        $arguments += @('--path', $path)
    }
    foreach ($output in $Outputs) {
        $arguments += @('--output', $output)
    }
    $hostGoEnvironment = @{}
    foreach ($variableName in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
        $hostGoEnvironment[$variableName] = [Environment]::GetEnvironmentVariable($variableName, 'Process')
        [Environment]::SetEnvironmentVariable($variableName, $null, 'Process')
    }
    $cacheLocationPushed = $false
    try {
        Push-Location -LiteralPath $Root
        $cacheLocationPushed = $true
        $result = ((& go @arguments) -join "`n").Trim()
        if ($LASTEXITCODE -ne 0) {
            throw "build phase cache $Action failed for $Name"
        }
        return $result
    } finally {
        if ($cacheLocationPushed) { Pop-Location }
        foreach ($variableName in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
            [Environment]::SetEnvironmentVariable($variableName, $hostGoEnvironment[$variableName], 'Process')
        }
    }
}

function Test-BuildPhaseCache() {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string[]]$Paths,
        [Parameter(Mandatory)][string[]]$Outputs,
        [string[]]$Inputs = @()
    )
    if ($env:SUPER_DOLPHIN_SKIP_BUILD_CACHE -eq '1') { return $false }
    if ($env:SUPER_DOLPHIN_RELEASE_BUILD -eq '1') { return $false }
    $result = Invoke-BuildPhaseCache -Action 'restore' -Name $Name -Paths $Paths -Inputs $Inputs -Outputs $Outputs
    if ($result -eq 'hit') {
        Write-Host "==> [$Name] shared artifact cache hit, restored"
        return $true
    }
    if ($result -eq 'miss') { return $false }
    throw "unexpected build phase cache restore result: $result"
}

function Save-BuildPhaseCache() {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string[]]$Paths,
        [Parameter(Mandatory)][string[]]$Outputs,
        [string[]]$Inputs = @()
    )
    if ($env:SUPER_DOLPHIN_SKIP_BUILD_CACHE -eq '1') { return }
    if ($env:SUPER_DOLPHIN_RELEASE_BUILD -eq '1') { return }
    $result = Invoke-BuildPhaseCache -Action 'save' -Name $Name -Paths $Paths -Inputs $Inputs -Outputs $Outputs
    if ($result -ne 'saved') {
        throw "unexpected build phase cache save result: $result"
    }
}

function Get-PEMachineType() {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "missing Windows native binary for architecture check: $Path"
    }
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -lt 0x40 -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) {
        throw "Windows native binary is not a PE file: $Path"
    }
    $peOffset = [BitConverter]::ToInt32($bytes, 0x3C)
    if ($peOffset -lt 0 -or $bytes.Length -lt ($peOffset + 6)) {
        throw "Windows PE header is truncated: $Path"
    }
    if ($bytes[$peOffset] -ne 0x50 -or $bytes[$peOffset + 1] -ne 0x45 -or $bytes[$peOffset + 2] -ne 0x00 -or $bytes[$peOffset + 3] -ne 0x00) {
        throw "Windows PE signature is missing: $Path"
    }
    $machine = [BitConverter]::ToUInt16($bytes, $peOffset + 4)
    switch ($machine) {
        0xAA64 { return 'arm64' }
        0x8664 { return 'amd64' }
        default { return ('0x{0:X4}' -f $machine) }
    }
}

function Assert-WindowsNativeArchitecture() {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$ExpectedArch,
        [Parameter(Mandatory)][string]$Label
    )
    $machine = Get-PEMachineType -Path $Path
    if ($machine -ne $ExpectedArch) {
        throw "$Label has Windows PE machine $machine, expected ${ExpectedArch}: $Path"
    }
}

function Assert-LSPBundleNativeArchitecture() {
    param([Parameter(Mandatory)][string]$BundleDir)
    $nativePaths = @(
        'bin/gopls.exe',
        'bin/rust-analyzer.exe',
        'bin/sg.exe',
        'bin/ast-grep.exe',
        'bin/vcruntime140.dll',
        'node/node.exe',
        'go/bin/go.exe'
    )
    if ([Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK', 'Process') -ne '1') {
        $nativePaths += 'bin/shellcheck.exe'
    }
    foreach ($relPath in $nativePaths) {
        $path = Join-Path $BundleDir $relPath
        Assert-WindowsNativeArchitecture -Path $path -ExpectedArch $WindowsPackageArch -Label "LSP bundle $relPath"
    }
    $javaPath = Join-Path $BundleDir 'jdk/bin/java.exe'
    if (Test-Path -LiteralPath $javaPath -PathType Leaf) {
        Assert-WindowsNativeArchitecture -Path $javaPath -ExpectedArch $WindowsPackageArch -Label 'LSP bundle jdk/bin/java.exe'
    }
}

function Assert-PackageNativeArchitecture() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    foreach ($file in Get-ChildItem -LiteralPath $PackageRoot -Recurse -File -ErrorAction SilentlyContinue) {
        $ext = $file.Extension.ToLowerInvariant()
        if ($ext -in @('.exe', '.dll')) {
            Assert-WindowsNativeArchitecture -Path $file.FullName -ExpectedArch $WindowsPackageArch -Label "packaged $($file.Name)"
        }
    }
}

function Invoke-WindowsGoBuild() {
    param(
        [Parameter(Mandatory)][string]$Output,
        [Parameter(Mandatory)][string]$Package,
        [string]$LdFlags = ''
    )
    $oldGOOS = $env:GOOS
    $oldGOARCH = $env:GOARCH
    try {
        $env:GOOS = 'windows'
        $env:GOARCH = $WindowsPackageArch
        if ($LdFlags.Trim() -ne '') {
            & go build -ldflags $LdFlags -o $Output $Package
        } else {
            & go build -o $Output $Package
        }
        if ($LASTEXITCODE -ne 0) { throw "go build $Package failed" }
    } finally {
        if ($null -eq $oldGOOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGOOS }
        if ($null -eq $oldGOARCH) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGOARCH }
    }
    Assert-WindowsNativeArchitecture -Path $Output -ExpectedArch $WindowsPackageArch -Label $Package
}

function Copy-DirectoryClean() {
    param(
        [Parameter(Mandatory)][string]$Source,
        [Parameter(Mandatory)][string]$Destination,
        [string[]]$PreserveNames = @()
    )
    if (-not (Test-Path -LiteralPath $Source -PathType Container)) {
        throw "missing directory: $Source"
    }
    $preserve = @{}
    foreach ($name in $PreserveNames) {
        $preserve[$name] = $true
    }
    if (Test-Path -LiteralPath $Destination) {
        Get-ChildItem -LiteralPath $Destination -Force | ForEach-Object {
            if (-not $preserve.ContainsKey($_.Name)) {
                Remove-Item -LiteralPath $_.FullName -Recurse -Force
            }
        }
    } else {
        New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    }
    Get-ChildItem -LiteralPath $Source -Force | Copy-Item -Destination $Destination -Recurse -Force
}

function New-WindowsZip() {
    param(
        [Parameter(Mandatory)][string]$Stage,
        [Parameter(Mandatory)][string]$ZipPath
    )
    $stageName = Split-Path -Leaf $Stage
    $dist = Split-Path -Parent $ZipPath
    $tar = Get-Command tar.exe -ErrorAction SilentlyContinue | Select-Object -First 1

    Push-Location -LiteralPath $dist
    try {
        if ($tar) {
            & $tar.Source -a -cf $ZipPath $stageName
            if ($LASTEXITCODE -ne 0) { throw 'tar.exe zip failed' }
            return
        }
        Compress-Archive -Path $stageName -DestinationPath $ZipPath -Force
    } finally {
        Pop-Location
    }
}

function Resolve-PackagedRelayEnv() {
    if ((Get-EnvValue $CodexRelayPrivilegedApiKeyEnv).Trim() -ne '') {
        throw "privileged Codex relay API key env is not allowed for Windows packaging; set $CodexRelayBootstrapTokenEnv instead of $CodexRelayPrivilegedApiKeyEnv"
    }
    $script:PackagedRelayBaseUrl = Get-EnvValue $CodexRelayBaseUrlEnv
    $script:PackagedRelayBootstrapToken = Get-EnvValue $CodexRelayBootstrapTokenEnv
    $script:PackagedRelayBootstrapProof = Get-EnvValue $CodexRelayBootstrapProofEnv
    Validate-EnvFileValue -Label $CodexRelayBaseUrlEnv -Value $script:PackagedRelayBaseUrl
    Validate-EnvFileValue -Label $CodexRelayBootstrapTokenEnv -Value $script:PackagedRelayBootstrapToken
    Validate-EnvFileValue -Label $CodexRelayBootstrapProofEnv -Value $script:PackagedRelayBootstrapProof
}

function Resolve-PackagedVideoEnv() {
    if ((Get-EnvValue $VideoAPIKeyEnv).Trim() -ne '') {
        throw "$VideoAPIKeyEnv must not be set for Windows packaging"
    }
    $script:PackagedVideoAPIKey = ''
}

function Write-PackagedRelayEnv() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $envPath = Join-Path $BundleRoot '.env'
    $contentLines = [Collections.Generic.List[string]]::new()
    $contentLines.Add("$CodexRelayBaseUrlEnv=$PackagedRelayBaseUrl")
    $contentLines.Add("$CodexRelayBootstrapTokenEnv=$PackagedRelayBootstrapToken")
    $contentLines.Add("$CodexRelayBootstrapProofEnv=$PackagedRelayBootstrapProof")
    $content = $contentLines -join "`n"
    Write-Utf8NoBom -Path $envPath -Content ($content + "`n")
}

function Assert-PackagedEnvHasNoSensitiveKeys() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $envPath = Join-Path $BundleRoot '.env'
    if (-not (Test-Path -LiteralPath $envPath -PathType Leaf)) { return }
    $blockedKeys = @($VideoAPIKeyEnv, $CodexRelayPrivilegedApiKeyEnv)
    foreach ($line in Get-Content -LiteralPath $envPath) {
        $trimmed = $line.Trim()
        if ($trimmed -eq '' -or $trimmed.StartsWith('#') -or -not $trimmed.Contains('=')) { continue }
        $key = $trimmed.Substring(0, $trimmed.IndexOf('=')).Trim()
        if ($key -in $blockedKeys) {
            throw "packaged .env must not contain sensitive key $key"
        }
    }
}

function Invoke-WindowsPackageWhatIf() {
    Resolve-UpdateConfig
    Resolve-PackagedVideoEnv
    $requiredFiles = @(
        (Join-Path $Root 'go.mod'),
        (Join-Path $Root 'frontend-app/package.json'),
        (Join-Path $Root 'frontend-app/package-lock.json'),
        (Join-Path $Root 'frontend-app/vite.config.js'),
        (Join-Path $Root 'frontend-app/index.html'),
        (Join-Path $Root 'scripts/verify_packaged_app_windows.ps1')
    )
    foreach ($path in $requiredFiles) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "WhatIf validation missing required file: $path"
        }
    }
    $requiredDirs = @(
        (Join-Path $Root 'frontend-app/src'),
        (Join-Path $Root 'frontend-app/public'),
        (Join-Path $Root 'internal/platform/db/sqlite/migrations')
    )
    foreach ($path in $requiredDirs) {
        if (-not (Test-Path -LiteralPath $path -PathType Container)) {
            throw "WhatIf validation missing required directory: $path"
        }
    }
    [void](Get-NodeVersionInput)
    [void](Get-NPMVersionInput)
    Write-Host "Windows package WhatIf validation complete"
}

function Resolve-UpdateConfig() {
    foreach ($name in $UpdateOverrideNames) {
        if ($null -ne [Environment]::GetEnvironmentVariable($name, 'Process')) {
            throw "package-owned updates are unsupported for $Platform; reject $name"
        }
    }
}

function Resolve-PackagedCodexArtifact() {
    $script:PackagedCodexArtifact = Get-EnvValue $CodexArtifactEnv
    $script:PackagedCodexSHA256 = Get-EnvValue $CodexSHA256Env
    $script:PackagedCodexVersion = Get-EnvValue $CodexVersionEnv
    if ($script:PackagedCodexArtifact.Trim() -eq '') {
        if ($RequireBundledCodex -eq '1') {
            throw "packaged Codex CLI artifact is required; set $CodexArtifactEnv to codex.exe and $CodexSHA256Env to its trusted SHA-256"
        }
        Write-Warning "Codex CLI artifact not bundled because SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=$RequireBundledCodex"
        return
    }
    if (-not (Test-Path -LiteralPath $script:PackagedCodexArtifact -PathType Leaf)) {
        throw "packaged Codex CLI artifact does not exist: $script:PackagedCodexArtifact"
    }
    if ([IO.Path]::GetExtension($script:PackagedCodexArtifact).ToLowerInvariant() -ne '.exe') {
        throw "packaged Codex CLI artifact must be a Windows .exe: $script:PackagedCodexArtifact"
    }
    if ($script:PackagedCodexSHA256.Trim() -eq '') {
        throw "packaged Codex CLI checksum is required; set $CodexSHA256Env from a trusted release manifest or signature verification"
    }
    if ($script:PackagedCodexSHA256 -notmatch '^[0-9A-Fa-f]{64}$') {
        throw "$CodexSHA256Env must be a 64-character hex SHA-256"
    }
    if ($script:PackagedCodexVersion.Trim() -eq '') {
        throw "packaged Codex CLI version is required; set $CodexVersionEnv"
    }
    Validate-EnvFileValue -Label $CodexSHA256Env -Value $script:PackagedCodexSHA256
    Validate-EnvFileValue -Label $CodexVersionEnv -Value $script:PackagedCodexVersion
    $expected = $script:PackagedCodexSHA256.ToLowerInvariant()
    $actual = Get-SHA256File $script:PackagedCodexArtifact
    if ($actual -ne $expected) {
        throw "Codex CLI artifact checksum mismatch: $script:PackagedCodexArtifact expected=$expected actual=$actual"
    }
    Assert-WindowsNativeArchitecture -Path $script:PackagedCodexArtifact -ExpectedArch $WindowsPackageArch -Label 'Codex CLI artifact'
    $script:PackagedCodexSHA256 = $expected
    Write-Host "Codex CLI artifact checksum verified: $script:PackagedCodexArtifact"
}

function Copy-PackagedCodex() {
    param(
        [Parameter(Mandatory)][string]$BundleRoot,
        [Parameter(Mandatory)][string]$Destination
    )
    if ($PackagedCodexArtifact.Trim() -eq '') { return }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Destination) | Out-Null
    Copy-Item -LiteralPath $PackagedCodexArtifact -Destination $Destination -Force
}

function Resolve-PackagedFFmpeg() {
    $configured = Get-EnvValue $FFmpegBinEnv
    if ($configured.Trim() -ne '') {
        $candidate = $configured
    } else {
        $cmd = Get-Command ffmpeg.exe -ErrorAction SilentlyContinue | Select-Object -First 1
        $candidate = if ($cmd) { $cmd.Source } else { '' }
    }
    if ($candidate.Trim() -eq '') {
        throw "packaged ffmpeg is required for video tools; install ffmpeg.exe or set $FFmpegBinEnv"
    }
    if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        throw "packaged ffmpeg does not exist: $candidate"
    }
    if ([IO.Path]::GetExtension($candidate).ToLowerInvariant() -ne '.exe') {
        throw "packaged ffmpeg must be a Windows .exe: $candidate"
    }
    $script:PackagedFFmpegBin = $candidate
    $script:PackagedFFmpegRuntimeDir = Split-Path -Parent $candidate
    & $script:PackagedFFmpegBin -version *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "ffmpeg smoke failed: $script:PackagedFFmpegBin -version"
    }
    Assert-WindowsNativeArchitecture -Path $script:PackagedFFmpegBin -ExpectedArch $WindowsPackageArch -Label 'ffmpeg'
    Write-Host "ffmpeg verified: $script:PackagedFFmpegBin"
}

function Copy-PackagedFFmpeg() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    if ($PackagedFFmpegBin.Trim() -eq '') {
        throw "packaged ffmpeg was not resolved; call Resolve-PackagedFFmpeg before staging"
    }
    if ($PackagedFFmpegRuntimeDir.Trim() -eq '') {
        throw "packaged ffmpeg runtime dir was not resolved; call Resolve-PackagedFFmpeg before staging"
    }
    $binDir = Join-Path $BundleRoot 'bin'
    $dest = Join-Path $binDir 'ffmpeg.exe'
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    Copy-Item -LiteralPath $PackagedFFmpegBin -Destination $dest -Force
    Get-ChildItem -LiteralPath $script:PackagedFFmpegRuntimeDir -Filter '*.dll' -File |
        ForEach-Object {
            Assert-WindowsNativeArchitecture -Path $_.FullName -ExpectedArch $WindowsPackageArch -Label 'ffmpeg runtime DLL'
            Copy-Item -LiteralPath $_.FullName -Destination $binDir -Force
        }
    Assert-WindowsNativeArchitecture -Path $dest -ExpectedArch $WindowsPackageArch -Label 'packaged ffmpeg'
    & $dest -version *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "packaged ffmpeg smoke failed: $dest -version"
    }
}

function Get-LSPManifestEntry() {
    param(
        [Parameter(Mandatory)]$Manifest,
        [Parameter(Mandatory)][string]$ServerId
    )
    $prop = $Manifest.servers.PSObject.Properties | Where-Object { $_.Name -eq $ServerId } | Select-Object -First 1
    if ($null -eq $prop) { return $null }
    return $prop.Value
}

function Get-EntryField() {
    param(
        [Parameter(Mandatory)]$Entry,
        [Parameter(Mandatory)][string]$Name
    )
    $prop = $Entry.PSObject.Properties | Where-Object { $_.Name -eq $Name } | Select-Object -First 1
    if ($null -eq $prop) { return $null }
    return $prop.Value
}

function Convert-LSPLanguagesArray() {
    param(
        [Parameter(Mandatory)]$Value,
        [Parameter(Mandatory)][string]$Context
    )
    if ($Value -is [array]) {
        $languages = [string[]]$Value
    } elseif ($Value -is [string]) {
        $languages = [string[]]@($Value)
    } else {
        throw "LSP manifest languages for $Context must be a JSON array"
    }
    if ($languages.Count -eq 0) {
        throw "LSP manifest languages for $Context must be a non-empty JSON array"
    }
    foreach ($language in $languages) {
        if ($language.Trim() -eq '') {
            throw "LSP manifest languages for $Context must not contain empty values"
        }
    }
    return $languages
}

function Verify-LSPChecksumsFile() {
    param([Parameter(Mandatory)][string]$BundleDir)
    $checksums = Join-Path $BundleDir $LSPChecksumsName
    foreach ($line in Get-Content -LiteralPath $checksums) {
        $text = $line.Trim()
        if ($text -eq '') { continue }
        if ($text -notmatch '^([0-9A-Fa-f]{64})\s+\*?(.+)$') {
            throw "invalid LSP checksums line: $line"
        }
        $expected = $Matches[1].ToLowerInvariant()
        $relPath = $Matches[2].Trim()
        Assert-RelativePackagePath -Label 'LSP checksum path' -Path $relPath
        $actual = Get-SHA256File (Join-Path $BundleDir $relPath)
        if ($actual -ne $expected) {
            throw "packaged LSP bundle checksum mismatch: $BundleDir\$LSPChecksumsName"
        }
    }
}

function Resolve-PackagedLSPBundle() {
    $script:PackagedLSPBundleDir = Get-EnvValue $LSPBundleDirEnv
    if ($script:PackagedLSPBundleDir.Trim() -eq '') {
        throw "packaged LSP bundle is required; set $LSPBundleDirEnv to a prepared $LSPProfile Windows bundle containing $LSPManifestName and $LSPChecksumsName"
    }
    if (-not (Test-Path -LiteralPath $script:PackagedLSPBundleDir -PathType Container)) {
        throw "packaged LSP bundle does not exist: $script:PackagedLSPBundleDir"
    }
    $manifestPath = Join-Path $script:PackagedLSPBundleDir $LSPManifestName
    $checksumsPath = Join-Path $script:PackagedLSPBundleDir $LSPChecksumsName
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "packaged LSP bundle missing manifest: $manifestPath"
    }
    if (-not (Test-Path -LiteralPath $checksumsPath -PathType Leaf)) {
        throw "packaged LSP bundle missing checksums: $checksumsPath"
    }
    Verify-LSPChecksumsFile -BundleDir $script:PackagedLSPBundleDir
    $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    foreach ($spec in $LSPServerSpecs) {
        $parts = $spec.Split('|')
        $serverId = $parts[0]
        $relPath = $parts[1]
        $entry = Get-LSPManifestEntry -Manifest $manifest -ServerId $serverId
        if ($null -eq $entry) { throw "LSP manifest missing path for ${serverId}: $manifestPath" }
        $manifestRelPath = [string](Get-EntryField -Entry $entry -Name 'path')
        Assert-RelativePackagePath -Label "LSP manifest path for $serverId" -Path $manifestRelPath
        if ((Normalize-RelPath $manifestRelPath) -ne $relPath) {
            throw "LSP manifest path mismatch for ${serverId}: expected $relPath, got $manifestRelPath"
        }
        $version = [string](Get-EntryField -Entry $entry -Name 'version')
        if ($version.Trim() -eq '') { throw "LSP manifest missing version for ${serverId}: $manifestPath" }
        $expectedSHA = [string](Get-EntryField -Entry $entry -Name 'sha256')
        if ($expectedSHA -notmatch '^[0-9A-Fa-f]{64}$') {
            throw "LSP manifest sha256 for $serverId must be a 64-character hex SHA-256"
        }
        $src = Join-Path $script:PackagedLSPBundleDir $relPath
        if (-not (Test-Path -LiteralPath $src -PathType Leaf)) {
            if ($serverId -eq 'go') {
                throw "packaged LSP bundle missing Go toolchain executable: $src"
            }
            throw "packaged LSP bundle missing executable ${serverId}: $src"
        }
        $actualSHA = Get-SHA256File $src
        if ($actualSHA -ne $expectedSHA.ToLowerInvariant()) {
            throw "packaged LSP bundle checksum mismatch for ${serverId}: $src"
        }
    }
    foreach ($shadowExec in $LSPShadowExecs) {
        $shadowPath = Join-Path $script:PackagedLSPBundleDir "bin/$shadowExec"
        if (-not (Test-Path -LiteralPath $shadowPath -PathType Leaf)) {
            throw "packaged LSP bundle missing Python shadow executable: $shadowPath"
        }
    }
    Assert-LSPBundleNativeArchitecture -BundleDir $script:PackagedLSPBundleDir
    Write-Host "LSP bundle checksums verified: $script:PackagedLSPBundleDir"
}

function Write-RootLSPLauncher() {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Target
    )
    $content = @"
@echo off
call "%~dp0..\lsp\$Target" %*
"@
    Write-Utf8NoBom -Path $Path -Content $content
}

function Copy-PackagedLSPBundle() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $destRoot = Join-Path $BundleRoot 'lsp'
    Copy-DirectoryClean -Source $PackagedLSPBundleDir -Destination $destRoot
    New-Item -ItemType Directory -Force -Path (Join-Path $BundleRoot 'bin') | Out-Null
    foreach ($spec in $LSPServerSpecs) {
        $parts = $spec.Split('|')
        $serverId = $parts[0]
        $relPath = $parts[1]
        $exposedName = $parts[2]
        $source = Join-Path $destRoot $relPath
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "packaged LSP bundle did not copy executable ${serverId}: $source"
        }
        $linkPath = Join-Path (Join-Path $BundleRoot 'bin') $exposedName
        if ($exposedName.ToLowerInvariant().EndsWith('.cmd')) {
            Write-RootLSPLauncher -Path $linkPath -Target $relPath.Replace('/', '\')
        } else {
            Copy-Item -LiteralPath $source -Destination $linkPath -Force
        }
        if (-not (Test-Path -LiteralPath $linkPath -PathType Leaf)) {
            throw "packaged LSP bundle did not expose executable ${serverId}: $linkPath"
        }
    }
}

function Write-LSPManifest() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $sourceManifestPath = Join-Path $BundleRoot "lsp/$LSPManifestName"
    if (-not (Test-Path -LiteralPath $sourceManifestPath -PathType Leaf)) {
        throw "missing copied LSP manifest before package manifest write: $sourceManifestPath"
    }
    $sourceManifest = Get-Content -Raw -LiteralPath $sourceManifestPath | ConvertFrom-Json
    $servers = [ordered]@{}
    foreach ($spec in $LSPServerSpecs) {
        $parts = $spec.Split('|')
        $serverId = $parts[0]
        $relPath = $parts[1]
        $sourceEntry = Get-LSPManifestEntry -Manifest $sourceManifest -ServerId $serverId
        if ($null -eq $sourceEntry) {
            throw "LSP manifest missing entry for $serverId before package manifest write: $sourceManifestPath"
        }
        $version = [string](Get-EntryField -Entry $sourceEntry -Name 'version')
        if ($version.Trim() -eq '') { throw "LSP manifest missing version for $serverId before package manifest write: $sourceManifestPath" }
        $languagesValue = Get-EntryField -Entry $sourceEntry -Name 'languages'
        if ($null -eq $languagesValue) { throw "LSP manifest missing languages for $serverId before package manifest write: $sourceManifestPath" }
        $languages = Convert-LSPLanguagesArray -Value $languagesValue -Context $serverId
        $serverPath = "lsp/$relPath"
        $servers[$serverId] = [ordered]@{
            path = $serverPath
            version = $version
            sha256 = Get-SHA256File (Join-Path $BundleRoot $serverPath)
            languages = @($languages)
        }
    }
    $manifest = [ordered]@{
        schema_version = 1
        bundle_path = 'lsp'
        profile = $LSPProfile
        servers = $servers
    }
    Write-Utf8NoBom -Path $sourceManifestPath -Content (($manifest | ConvertTo-Json -Depth 8) + "`n")
}

function Write-CodexManifest() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    if ($PackagedCodexArtifact.Trim() -eq '') { return }
    $manifest = [ordered]@{
        codex = [ordered]@{
            path = 'bin/codex.exe'
            version = $PackagedCodexVersion
            source_sha256 = $PackagedCodexSHA256
            package_sha256 = Get-SHA256File (Join-Path $BundleRoot 'bin/codex.exe')
        }
    }
    Write-Utf8NoBom -Path (Join-Path $BundleRoot 'codex-manifest.json') -Content (($manifest | ConvertTo-Json -Depth 6) + "`n")
}

function Copy-ModelRegistry() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $src = Join-Path $Root 'cmd/mcp-orch/tools/modelregistry/models.yaml'
    if (-not (Test-Path -LiteralPath $src -PathType Leaf)) {
        throw "missing model registry: $src"
    }
    Copy-Item -LiteralPath $src -Destination (Join-Path $BundleRoot 'models.yaml') -Force
}

function Copy-SQLiteMigrations() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $source = Join-Path $Root 'internal/platform/db/sqlite/migrations'
    $destination = Join-Path $BundleRoot 'internal/platform/db/sqlite/migrations'
    if (-not (Test-Path -LiteralPath $source -PathType Container)) {
        throw "missing SQLite migrations directory: $source"
    }
    $firstMigration = Get-ChildItem -LiteralPath $source -Recurse -File -Force | Select-Object -First 1
    if ($null -eq $firstMigration) {
        throw "missing SQLite migration files under $source"
    }
    Copy-DirectoryClean -Source $source -Destination $destination
}

function Write-RuntimeManifest() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $manifest = [ordered]@{
        bundled_codex_path = 'bin/codex.exe'
        bundled_gopls_path = 'bin/gopls.exe'
        lsp_bundle_path = 'lsp'
        lsp_manifest_path = 'lsp/lsp-manifest.json'
        model_registry_path = 'models.yaml'
    }
    Write-Utf8NoBom -Path (Join-Path $BundleRoot 'runtime-manifest.json') -Content (($manifest | ConvertTo-Json -Depth 3) + "`n")
}

function Get-RequiredFrontendEntries() {
    $manifest = Join-Path $Root 'frontend-app/required-dist-entries.txt'
    if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) {
        throw "frontend required entries manifest is missing: $manifest"
    }
    $entries = @(Get-Content -LiteralPath $manifest)
    if ($entries.Count -eq 0) {
        throw "frontend required entries manifest is empty: $manifest"
    }
    $seen = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    foreach ($entry in $entries) {
        if ($entry -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') {
            throw "invalid frontend required entry '$entry': $manifest"
        }
        if (-not $seen.Add($entry)) {
            throw "duplicate frontend required entry '$entry': $manifest"
        }
    }
    return $entries
}

function Assert-RequiredFrontendEntries() {
    param(
        [Parameter(Mandatory)][string]$Directory,
        [Parameter(Mandatory)][string]$Label
    )
    foreach ($entry in (Get-RequiredFrontendEntries)) {
        $entryPath = Join-Path $Directory $entry
        if (-not (Test-Path -LiteralPath $entryPath -PathType Leaf)) {
            throw "$Label missing required entry $($entry): $entryPath"
        }
    }
}

function Build-CurrentFrontendApp() {
    if ($env:SUPER_DOLPHIN_SKIP_FRONTEND_BUILD -ne '1') {
        $frontendCachePaths = @(
            (Join-Path $Root 'frontend-app/package.json'),
            (Join-Path $Root 'frontend-app/package-lock.json'),
            (Join-Path $Root 'frontend-app/vite.config.js'),
            (Join-Path $Root 'frontend-app/index.html'),
            (Join-Path $Root 'frontend-app/recovery.html'),
            (Join-Path $Root 'frontend-app/required-dist-entries.txt'),
            (Join-Path $Root 'frontend-app/public'),
            (Join-Path $Root 'frontend-app/src'),
            (Join-Path $Root 'scripts/package_windows.ps1'),
            (Join-Path $Root 'scripts/build_phase_cache/main.go')
        )
        $frontendCacheInputs = @(
            "NODE_VERSION=$(Get-NodeVersionInput)",
            "NPM_VERSION=$(Get-NPMVersionInput)"
        )
        $frontendCacheOutputs = @((Join-Path $Root 'frontend-app/dist'))
        if (-not (Test-BuildPhaseCache -Name 'frontend' -Paths $frontendCachePaths -Inputs $frontendCacheInputs -Outputs $frontendCacheOutputs)) {
            Push-Location -LiteralPath (Join-Path $Root 'frontend-app')
            try {
                & npm ci
                if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
                & npm run build
                if ($LASTEXITCODE -ne 0) { throw 'npm run build failed' }
                Assert-RequiredFrontendEntries -Directory (Join-Path $Root 'frontend-app/dist') -Label 'frontend dist'
                Save-BuildPhaseCache -Name 'frontend' -Paths $frontendCachePaths -Inputs $frontendCacheInputs -Outputs $frontendCacheOutputs
            } finally {
                Pop-Location
            }
        }
    }
    Assert-RequiredFrontendEntries -Directory (Join-Path $Root 'frontend-app/dist') -Label 'frontend dist'
    Copy-DirectoryClean -Source (Join-Path $Root 'frontend-app/dist') -Destination (Join-Path $Root 'cmd/agent-terminal/web-dist') -PreserveNames @('.gitkeep')
    Assert-RequiredFrontendEntries -Directory (Join-Path $Root 'cmd/agent-terminal/web-dist') -Label 'embedded frontend dist'
}

function Write-RunScripts() {
    param([Parameter(Mandatory)][string]$BundleRoot)
    $requiredExecutables = @(
        'mcp-orch.exe',
        'mcp-lsp.exe',
        'mcp-ida.exe',
        'codex.exe',
        'ffmpeg.exe'
    )
    foreach ($spec in $LSPServerSpecs) {
        $requiredExecutables += $spec.Split('|')[2]
    }
    $requiredExecutableList = $requiredExecutables -join ' '
    $runCmd = @'
@echo off
setlocal
set "here=%~dp0"
for %%I in ("%here%.") do set "here=%%~fI"
set "SUPER_DOLPHIN_PACKAGE_ROOT=%here%"
set "PROJECT_ROOT=%here%"
set "SUPER_DOLPHIN_MODEL_REGISTRY=%here%\models.yaml"
set "FFMPEG_PATH=%here%\bin\ffmpeg.exe"
set "PATH=%here%\bin;%here%\lsp\bin;%here%\lsp\node;%here%\lsp\node_modules\.bin;%SystemRoot%\System32;%SystemRoot%;%PATH%"
set "GO_AGENT_PEER_BIN_DIR=%here%\bin"
set "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1"
set "SUPER_DOLPHIN_LSP_BUNDLE_DIR=%here%\lsp"
set "SUPER_DOLPHIN_LSP_MANIFEST=%here%\lsp\lsp-manifest.json"
set "SUPER_DOLPHIN_RUNTIME_MODE=packaged"
set "SUPER_DOLPHIN_PACKAGED_LAUNCHER=1"
for %%E in (__REQUIRED_EXES__) do (
  if not exist "%here%\bin\%%E" (
    echo missing bundled executable: %here%\bin\%%E >&2
    exit /b 1
  )
)
"%here%\bin\agent-terminal.exe" %*
exit /b %ERRORLEVEL%
'@
    $runCmd = $runCmd.Replace('__PLATFORM__', $Platform)
    $runCmd = $runCmd.Replace('__REQUIRED_EXES__', $requiredExecutableList)
    Write-Utf8NoBom -Path (Join-Path $BundleRoot 'run.cmd') -Content $runCmd

    $runPs1 = @'
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$env:SUPER_DOLPHIN_PACKAGE_ROOT = $Here
$env:PROJECT_ROOT = $Here
$env:SUPER_DOLPHIN_MODEL_REGISTRY = Join-Path $Here 'models.yaml'
$env:FFMPEG_PATH = Join-Path $Here 'bin\ffmpeg.exe'
$env:Path = ((Join-Path $Here 'bin'), (Join-Path $Here 'lsp\bin'), (Join-Path $Here 'lsp\node'), (Join-Path $Here 'lsp\node_modules\.bin'), (Join-Path $env:SystemRoot 'System32'), $env:SystemRoot, $env:Path) -join [IO.Path]::PathSeparator
$env:GO_AGENT_PEER_BIN_DIR = Join-Path $Here 'bin'
$env:SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX = '1'
$env:SUPER_DOLPHIN_LSP_BUNDLE_DIR = Join-Path $Here 'lsp'
$env:SUPER_DOLPHIN_LSP_MANIFEST = Join-Path $Here 'lsp\lsp-manifest.json'
$env:SUPER_DOLPHIN_RUNTIME_MODE = 'packaged'
$env:SUPER_DOLPHIN_PACKAGED_LAUNCHER = '1'
& (Join-Path $Here 'bin\agent-terminal.exe') @args
exit $LASTEXITCODE
'@
    $runPs1 = $runPs1.Replace('__PLATFORM__', $Platform)
    Write-Utf8NoBom -Path (Join-Path $BundleRoot 'run.ps1') -Content $runPs1
}

function Resolve-InnoCompiler() {
    $configured = [Environment]::GetEnvironmentVariable('INNO_SETUP_ISCC', 'Process')
    if ($null -ne $configured -and $configured.Trim() -ne '') {
        if (-not (Test-Path -LiteralPath $configured -PathType Leaf)) {
            throw "INNO_SETUP_ISCC points to a missing file: $configured"
        }
        return $configured
    }

    $cmd = Get-Command iscc.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { return $cmd.Source }

    $candidates = @(
        (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
        (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe'),
        (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 7\ISCC.exe'),
        (Join-Path $env:ProgramFiles 'Inno Setup 7\ISCC.exe')
    )
    foreach ($candidate in $candidates) {
        if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            return $candidate
        }
    }
    return ''
}

function Try-BuildInstaller() {
    param(
        [Parameter(Mandatory)][string]$Stage,
        [Parameter(Mandatory)][string]$Dist
    )
    $iscc = Resolve-InnoCompiler
    if ($iscc -eq '') {
        throw 'Windows installer requested but Inno Setup iscc.exe was not found'
    }
    $setupName = "SuperDolphinSetup-$Version-$Platform"
    $iss = Join-Path $Dist "$setupName.iss"
    $sourceDir = $Stage.Replace('\', '\\')
    $outputDir = $Dist.Replace('\', '\\')
    $installerCompression = if ($env:SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION) { $env:SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION } else { 'lzma' }
    $installerSolidCompression = if ($env:SUPER_DOLPHIN_WINDOWS_INSTALLER_SOLID_COMPRESSION) { $env:SUPER_DOLPHIN_WINDOWS_INSTALLER_SOLID_COMPRESSION } else { 'yes' }
    $issContent = @"
[Setup]
AppId={{7F3310EC-5B1A-442B-AF31-52DF0A97BFD0}}
AppName=Super Dolphin
AppVersion=$Version
DefaultDirName={autopf}\Super Dolphin
DisableProgramGroupPage=yes
OutputDir=$outputDir
OutputBaseFilename=$setupName
Compression=$installerCompression
SolidCompression=$installerSolidCompression

[Files]
Source: "$sourceDir\*"; DestDir: "{app}"; Flags: recursesubdirs ignoreversion

[Icons]
Name: "{autoprograms}\Super Dolphin"; Filename: "{app}\bin\agent-terminal.exe"; WorkingDir: "{app}"
Name: "{autodesktop}\Super Dolphin"; Filename: "{app}\bin\agent-terminal.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked
"@
    Write-Utf8NoBom -Path $iss -Content $issContent
    & $iscc '/Qp' $iss
    if ($LASTEXITCODE -ne 0) { throw 'Inno Setup installer build failed' }
    Write-Host "Windows installer ready: $(Join-Path $Dist "$setupName.exe")"
}

function Package-WindowsMain() {
    if ($WhatIfPreference) {
        Invoke-WindowsPackageWhatIf
        return
    }

    if ($GoOS -ne 'windows') {
        throw "package_windows.ps1 must run on Windows; current GOOS=$GoOS"
    }

    Resolve-PackagedRelayEnv
    Resolve-PackagedVideoEnv
    Resolve-UpdateConfig
    Resolve-PackagedCodexArtifact
    Resolve-PackagedLSPBundle
    Resolve-PackagedFFmpeg

    $dist = Join-Path $Root 'dist/package/windows'
    $stage = Join-Path $dist "$AppName-$Version-$Platform"
    $zipPath = Join-Path $dist "$AppName-$Version-$Platform.zip"
    $setupPath = Join-Path $dist "SuperDolphinSetup-$Version-$Platform.exe"
    $issPath = Join-Path $dist "SuperDolphinSetup-$Version-$Platform.iss"
    $stage = Resolve-ContainedWindowsPackagePath -DistRoot $dist -Candidate $stage
    $zipPath = Resolve-ContainedWindowsPackagePath -DistRoot $dist -Candidate $zipPath
    $setupPath = Resolve-ContainedWindowsPackagePath -DistRoot $dist -Candidate $setupPath
    $issPath = Resolve-ContainedWindowsPackagePath -DistRoot $dist -Candidate $issPath
    $keepStageRequested = $KeepStage.IsPresent -or ([Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_KEEP_STAGE', 'Process') -eq '1')
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    if ($Artifact -in @('all', 'zip') -and (Test-Path -LiteralPath $zipPath)) { Remove-Item -LiteralPath $zipPath -Force }
    if ($Artifact -in @('all', 'installer') -and (Test-Path -LiteralPath $setupPath)) { Remove-Item -LiteralPath $setupPath -Force }
    if ($Artifact -in @('all', 'installer') -and (Test-Path -LiteralPath $issPath)) { Remove-Item -LiteralPath $issPath -Force }
    New-Item -ItemType Directory -Force -Path (Join-Path $Stage 'bin') | Out-Null

    Build-CurrentFrontendApp

    Push-Location -LiteralPath $Root
    try {
        $windowsGuiLdFlags = '-H=windowsgui'
        $appCommit = (& git rev-parse HEAD).Trim()
        $schemaBuildIdentityLdFlag = "-X github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema.buildAppCommit=$appCommit"
        $windowsSchemaLdFlags = "$windowsGuiLdFlags $schemaBuildIdentityLdFlag"
        $goInputs = @(
            'GOOS=windows',
            "GOARCH=$WindowsPackageArch",
            "GOVERSION=$((& go env GOVERSION).Trim())",
            "GOFLAGS=$((& go env GOFLAGS).Trim())",
            "GOEXPERIMENT=$((& go env GOEXPERIMENT).Trim())",
            "GOAMD64=$((& go env GOAMD64).Trim())",
            "GOARM64=$((& go env GOARM64).Trim())",
            "GOARM=$((& go env GOARM).Trim())",
            "CC=$((& go env CC).Trim())",
            "CXX=$((& go env CXX).Trim())",
            "CGO_ENABLED=$env:CGO_ENABLED",
            "CGO_CPPFLAGS=$env:CGO_CPPFLAGS",
            "CGO_CFLAGS=$env:CGO_CFLAGS",
            "CGO_CXXFLAGS=$env:CGO_CXXFLAGS",
            "CGO_LDFLAGS=$env:CGO_LDFLAGS",
            "WINDOWS_GUI_LDFLAGS=$windowsGuiLdFlags",
            "APP_COMMIT=$appCommit",
            "VCS_MODIFIED=$(Get-GitWorktreeModifiedInput)"
        )
        $goBinaryCachePaths = @(
            (Join-Path $Root 'cmd'),
            (Join-Path $Root 'internal'),
            (Join-Path $Root 'pkg'),
            (Join-Path $Root 'go.mod'),
            (Join-Path $Root 'go.sum'),
            (Join-Path $Root 'scripts/package_windows.ps1'),
            (Join-Path $Root 'scripts/build_phase_cache/main.go')
        )
        $goBinaryCacheOutputs = @(
            (Join-Path $Root 'bin/agent-terminal.exe'),
            (Join-Path $Root 'bin/mcp-orch.exe'),
            (Join-Path $Root 'bin/mcp-lsp.exe'),
            (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe'),
            (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe.manifest.json'),
            (Join-Path $Root 'bin/mcp-ida.exe')
        )
        if (-not (Test-BuildPhaseCache -Name 'go-binaries' -Paths $goBinaryCachePaths -Inputs $goInputs -Outputs $goBinaryCacheOutputs)) {
            Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-orch.exe') -Package './cmd/mcp-orch' -LdFlags $windowsGuiLdFlags
            Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-lsp.exe') -Package './cmd/mcp-lsp' -LdFlags $windowsGuiLdFlags
            Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe') -Package './cmd/mcp-schema-compiler-helper' -LdFlags $windowsSchemaLdFlags
            & go run ./cmd/mcp-schema-compiler-helper --write-package-manifest -helper (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe') -output (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe.manifest.json') -app-commit $appCommit -go-version ((& go env GOVERSION).Trim()) -goos windows -goarch $WindowsPackageArch
            if ($LASTEXITCODE -ne 0) { throw 'schema helper manifest generation failed' }
            Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/agent-terminal.exe') -Package './cmd/agent-terminal' -LdFlags $windowsSchemaLdFlags
            Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-ida.exe') -Package './cmd/mcp-ida' -LdFlags $windowsGuiLdFlags
            Save-BuildPhaseCache -Name 'go-binaries' -Paths $goBinaryCachePaths -Inputs $goInputs -Outputs $goBinaryCacheOutputs
        }
    } finally {
        Pop-Location
    }
    Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/agent-terminal.exe') -ExpectedArch $WindowsPackageArch -Label 'agent-terminal'
    Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-orch.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-orch'
    Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-lsp.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-lsp'
    Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-schema-compiler-helper'
    Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-ida.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-ida'

    Copy-Item -LiteralPath (Join-Path $Root 'bin/agent-terminal.exe') -Destination (Join-Path $Stage 'bin/agent-terminal.exe') -Force
    Copy-Item -LiteralPath (Join-Path $Root 'bin/mcp-orch.exe') -Destination (Join-Path $Stage 'bin/mcp-orch.exe') -Force
    Copy-Item -LiteralPath (Join-Path $Root 'bin/mcp-lsp.exe') -Destination (Join-Path $Stage 'bin/mcp-lsp.exe') -Force
    Copy-Item -LiteralPath (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe') -Destination (Join-Path $Stage 'bin/mcp-schema-compiler-helper.exe') -Force
    Copy-Item -LiteralPath (Join-Path $Root 'bin/mcp-schema-compiler-helper.exe.manifest.json') -Destination (Join-Path $Stage 'bin/mcp-schema-compiler-helper.exe.manifest.json') -Force
    Copy-Item -LiteralPath (Join-Path $Root 'bin/mcp-ida.exe') -Destination (Join-Path $Stage 'bin/mcp-ida.exe') -Force
    Copy-SQLiteMigrations -BundleRoot $Stage
    Copy-PackagedLSPBundle -BundleRoot $Stage
    Copy-PackagedCodex -BundleRoot $Stage -Destination (Join-Path $Stage 'bin/codex.exe')
    Copy-PackagedFFmpeg -BundleRoot $Stage
    Write-CodexManifest -BundleRoot $Stage
    Write-LSPManifest -BundleRoot $Stage
    Copy-ModelRegistry -BundleRoot $Stage
    Write-PackagedRelayEnv -BundleRoot $Stage
    Assert-PackagedEnvHasNoSensitiveKeys -BundleRoot $Stage
    Write-RuntimeManifest -BundleRoot $Stage
    Write-RunScripts -BundleRoot $Stage
    Assert-PackageNativeArchitecture -PackageRoot $Stage

    & (Join-Path $Root 'scripts/verify_packaged_app_windows.ps1') $Stage
    if ($LASTEXITCODE -ne 0) { throw 'Windows package verifier failed' }

    if ($Artifact -in @('all', 'zip')) {
        New-WindowsZip -Stage $Stage -ZipPath $zipPath
        Write-Host "Windows zip ready: $zipPath"
    }
    if ($Artifact -in @('all', 'installer')) {
        Try-BuildInstaller -Stage $Stage -Dist $dist
    }
    if ($keepStageRequested) {
        Write-Host "Windows package stage kept: $stage"
    } else {
        Remove-Item -LiteralPath $stage -Recurse -Force
        Write-Host "Windows package stage cleaned: $stage"
    }
    Write-Host "Windows package artifacts ready under: $dist"
}

Package-WindowsMain
