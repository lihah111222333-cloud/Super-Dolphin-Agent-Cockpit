param(
    [Parameter(Mandatory)][string]$Target
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$LSPServerSpecs = @(
    'gopls|bin/gopls.exe',
    'typescript-language-server|bin/typescript-language-server.cmd',
    'vscode-langservers-extracted|bin/vscode-css-language-server.cmd',
    'pyright|bin/pyright-langserver.cmd',
    'rust-analyzer|bin/rust-analyzer.exe',
    'bash-language-server|bin/bash-language-server.cmd',
    'sql-language-server|bin/sql-language-server.cmd',
    'sg|bin/sg.exe',
    'go|bin/go.cmd'
)
if ([Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK', 'Process') -ne '1') {
    $LSPServerSpecs += 'shellcheck|bin/shellcheck.exe'
}
$FullLSPServerSpecs = @(
    'java|bin/java.cmd',
    'jdtls|bin/jdtls.cmd'
)
$WindowsPackagePlatform = if ($env:SUPER_DOLPHIN_WINDOWS_PACKAGE_PLATFORM) { $env:SUPER_DOLPHIN_WINDOWS_PACKAGE_PLATFORM } else { '' }

function Resolve-WindowsPackageArch() {
    $configured = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_ARCH', 'Process')
    $configuredArch = ''
    if ($null -ne $configured -and $configured.Trim() -ne '') {
        switch ($configured.Trim()) {
            'amd64' { $configuredArch = 'amd64' }
            'arm64' { $configuredArch = 'arm64' }
            default { throw "unsupported SUPER_DOLPHIN_WINDOWS_ARCH=$configured; expected amd64 or arm64" }
        }
    }
    if ($script:WindowsPackagePlatform -match '^windows-(amd64|arm64)$') {
        $platformArch = $Matches[1]
        if ($configuredArch -ne '' -and $configuredArch -ne $platformArch) {
            throw "SUPER_DOLPHIN_WINDOWS_ARCH=$configuredArch conflicts with Windows package platform $script:WindowsPackagePlatform"
        }
        return $platformArch
    }
    if ($configuredArch -ne '') { return $configuredArch }
    throw 'unable to infer Windows package architecture; set SUPER_DOLPHIN_WINDOWS_ARCH or use a package root name ending in windows-amd64 or windows-arm64'
}

function Infer-WindowsPackagePlatform() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    if ($script:WindowsPackagePlatform -match '^windows-(amd64|arm64)$') {
        return
    }
    $leaf = Split-Path -Leaf $PackageRoot
    if ($leaf -match 'windows-(amd64|arm64)$') {
        $script:WindowsPackagePlatform = $Matches[0]
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

function Assert-PackageNativeArchitecture() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    $expectedArch = Resolve-WindowsPackageArch
    foreach ($file in Get-ChildItem -LiteralPath $PackageRoot -Recurse -File -ErrorAction SilentlyContinue) {
        $ext = $file.Extension.ToLowerInvariant()
        if ($ext -in @('.exe', '.dll')) {
            Assert-WindowsNativeArchitecture -Path $file.FullName -ExpectedArch $expectedArch -Label "packaged $($file.Name)"
        }
    }
}

function Normalize-RelPath() {
    param([Parameter(Mandatory)][string]$Path)
    return $Path.Replace('\', '/')
}

function Get-SHA256File() {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "missing file for SHA-256: $Path"
    }
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Assert-RelativePackagePath() {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$Path
    )
    $normalized = Normalize-RelPath $Path
    if ($normalized.Trim() -eq '' -or $normalized.StartsWith('/') -or $normalized -match '^[A-Za-z]:') {
        throw "$Label must be a relative path under Windows package root: $Path"
    }
    $parts = $normalized.Split('/') | Where-Object { $_ -ne '' }
    if ($parts | Where-Object { $_ -eq '..' }) {
        throw "$Label must not escape the Windows package root: $Path"
    }
}

function Read-JsonFile() {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "missing JSON file: $Path"
    }
    return Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json
}

function Get-JsonProperty() {
    param(
        [Parameter(Mandatory)]$Object,
        [Parameter(Mandatory)][string]$Name
    )
    $prop = $Object.PSObject.Properties | Where-Object { $_.Name -eq $Name } | Select-Object -First 1
    if ($null -eq $prop) { return $null }
    return $prop.Value
}

function Get-DotEnvValue() {
    param(
        [Parameter(Mandatory)][string]$EnvFile,
        [Parameter(Mandatory)][string]$Name
    )
    if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
        return ''
    }
    foreach ($line in Get-Content -LiteralPath $EnvFile) {
        $trimmed = $line.Trim()
        if ($trimmed -eq '' -or $trimmed.StartsWith('#')) {
            continue
        }
        $idx = $line.IndexOf('=')
        if ($idx -lt 0) {
            continue
        }
        $key = $line.Substring(0, $idx)
        if ($key -eq $Name) {
            return $line.Substring($idx + 1)
        }
    }
    return ''
}

function Require-DotEnvValue() {
    param(
        [Parameter(Mandatory)][string]$EnvFile,
        [Parameter(Mandatory)][string]$Name
    )
    $value = Get-DotEnvValue -EnvFile $EnvFile -Name $Name
    if ($value.Trim() -eq '') {
        throw "packaged update .env missing non-empty $Name"
    }
    return $value
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

function Assert-UpdatePublicKey() {
    param([Parameter(Mandatory)][string]$PublicKey)
    try {
        $decoded = [Convert]::FromBase64String($PublicKey)
    } catch {
        throw 'SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be valid base64'
    }
    if ($decoded.Length -ne 32) {
        throw 'decoded SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be 32 bytes'
    }
}

function Test-PlaceholderUpdateRepo() {
    param([Parameter(Mandatory)][string]$Repo)
    return $Repo.Trim() -eq 'xiaoxiaotest9527-bit/-'
}

function Assert-UpdateWindowsThumbprint() {
    param([Parameter(Mandatory)][string]$Thumbprint)
    $normalized = ($Thumbprint.Trim() -replace '[ :]', '').ToUpperInvariant()
    if ($normalized -notmatch '^[0-9A-F]{40}$') {
        throw 'SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT must be a 40-character certificate thumbprint'
    }
}

function Verify-UpdateEnv() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    $envFile = Join-Path $PackageRoot '.env'
    if (-not (Test-Truthy -Value (Get-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_ENABLED'))) {
        return
    }
    $manifestURL = Get-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_MANIFEST_URL'
    $githubRepo = Get-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_GITHUB_REPO'
    if ($manifestURL.Trim() -eq '' -and $githubRepo.Trim() -eq '') {
        throw 'SUPER_DOLPHIN_UPDATE_MANIFEST_URL or SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required when app update is enabled'
    }
    if ($manifestURL.Trim() -ne '' -and $githubRepo.Trim() -ne '') {
        throw 'SUPER_DOLPHIN_UPDATE_MANIFEST_URL and SUPER_DOLPHIN_UPDATE_GITHUB_REPO are mutually exclusive'
    }
    $source = ''
    if ($manifestURL.Trim() -ne '') {
        if ($manifestURL -notmatch '^https://[^/?#]+($|[/?#])') {
            throw 'SUPER_DOLPHIN_UPDATE_MANIFEST_URL must be an HTTPS URL with a host'
        }
        $source = "manifest:$manifestURL"
    }
    if ($githubRepo.Trim() -ne '') {
        if ($githubRepo -notmatch '^[^/\s]+/[^/\s]+$') {
            throw 'SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be owner/repo without whitespace'
        }
        if (Test-PlaceholderUpdateRepo -Repo $githubRepo) {
            throw 'known placeholder update repo is not allowed'
        }
        $source = "github:$githubRepo"
    }
    $publicKey = Require-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_PUBLIC_KEY'
    $channel = Require-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_CHANNEL'
    $version = Require-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_VERSION'
    $windowsPublisher = Require-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER'
    $windowsThumbprint = Require-DotEnvValue -EnvFile $envFile -Name 'SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT'
    Assert-UpdatePublicKey -PublicKey $publicKey
    Assert-UpdateWindowsThumbprint -Thumbprint $windowsThumbprint
    Write-Host "packaged app update env verified: source=$source channel=$channel version=$version publisher=$windowsPublisher"
}

function Assert-JsonStringArray() {
    param(
        [Parameter(Mandatory)]$Value,
        [Parameter(Mandatory)][string]$Label
    )
    if ($Value -is [array]) {
        $items = [string[]]$Value
    } elseif ($Value -is [string]) {
        $items = [string[]]@($Value)
    } else {
        throw "$Label must be a JSON array"
    }
    if ($items.Count -eq 0) {
        throw "$Label must be a non-empty JSON array"
    }
    foreach ($item in $items) {
        if ($item.Trim() -eq '') {
            throw "$Label must not contain empty values"
        }
    }
}

function Resolve-ManifestPath() {
    param(
        [Parameter(Mandatory)][string]$PackageRoot,
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$RelPath,
        [Parameter(Mandatory)][string]$Expected
    )
    Assert-RelativePackagePath -Label "runtime manifest $Label" -Path $RelPath
    if ((Normalize-RelPath $RelPath) -ne $Expected) {
        throw "runtime manifest $Label mismatch: expected $Expected, got $RelPath"
    }
    return Join-Path $PackageRoot $RelPath
}

function Require-ManifestPath() {
    param(
        [Parameter(Mandatory)][string]$PackageRoot,
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$RelPath,
        [Parameter(Mandatory)][string]$Expected,
        [Parameter(Mandatory)][string]$Kind
    )
    $resolved = Resolve-ManifestPath -PackageRoot $PackageRoot -Label $Label -RelPath $RelPath -Expected $Expected
    switch ($Kind) {
        'exec' {
            if (-not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
                throw "runtime manifest $Label points to missing executable: $resolved"
            }
            $ext = [IO.Path]::GetExtension($resolved).ToLowerInvariant()
            if ($ext -notin @('.exe', '.cmd', '.bat', '.ps1')) {
                throw "runtime manifest $Label points to non-Windows executable path: $resolved"
            }
        }
        'file' {
            if (-not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
                throw "runtime manifest $Label points to missing file: $resolved"
            }
        }
        'dir' {
            if (-not (Test-Path -LiteralPath $resolved -PathType Container)) {
                throw "runtime manifest $Label points to missing directory: $resolved"
            }
        }
        default { throw "unknown manifest path kind: $Kind" }
    }
}

function Verify-RuntimeManifest() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    $manifestPath = Join-Path $PackageRoot 'runtime-manifest.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "missing runtime manifest: $manifestPath"
    }
    $manifest = Read-JsonFile $manifestPath
    Require-ManifestPath -PackageRoot $PackageRoot -Label 'bundled_codex_path' -RelPath ([string]$manifest.bundled_codex_path) -Expected 'bin/codex.exe' -Kind 'exec'
    Require-ManifestPath -PackageRoot $PackageRoot -Label 'bundled_gopls_path' -RelPath ([string]$manifest.bundled_gopls_path) -Expected 'bin/gopls.exe' -Kind 'exec'
    Require-ManifestPath -PackageRoot $PackageRoot -Label 'lsp_bundle_path' -RelPath ([string]$manifest.lsp_bundle_path) -Expected 'lsp' -Kind 'dir'
    Require-ManifestPath -PackageRoot $PackageRoot -Label 'lsp_manifest_path' -RelPath ([string]$manifest.lsp_manifest_path) -Expected 'lsp/lsp-manifest.json' -Kind 'file'
    Require-ManifestPath -PackageRoot $PackageRoot -Label 'model_registry_path' -RelPath ([string]$manifest.model_registry_path) -Expected 'models.yaml' -Kind 'file'
    Write-Host 'runtime manifest verified'
}

function Verify-CodexManifest() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    $manifestPath = Join-Path $PackageRoot 'codex-manifest.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "missing Codex manifest: $manifestPath"
    }
    $manifest = Read-JsonFile $manifestPath
    $codex = $manifest.codex
    if ($null -eq $codex) { throw "Codex manifest missing codex object: $manifestPath" }
    $relPath = [string]$codex.path
    Assert-RelativePackagePath -Label 'Codex manifest path' -Path $relPath
    if ((Normalize-RelPath $relPath) -ne 'bin/codex.exe') {
        throw "Codex manifest path mismatch: expected bin/codex.exe, got $relPath"
    }
    $codexPath = Join-Path $PackageRoot $relPath
    if (-not (Test-Path -LiteralPath $codexPath -PathType Leaf)) {
        throw "Codex manifest points to missing executable: $codexPath"
    }
    foreach ($field in @('source_sha256', 'package_sha256')) {
        $value = [string](Get-JsonProperty -Object $codex -Name $field)
        if ($value -notmatch '^[0-9A-Fa-f]{64}$') {
            throw "Codex manifest $field must be a 64-character SHA-256 hex digest"
        }
    }
    $actual = Get-SHA256File $codexPath
    if ($actual -ne ([string]$codex.package_sha256).ToLowerInvariant()) {
        throw "Codex packaged digest mismatch: $codexPath"
    }
    Write-Host 'codex.exe app-server --help'
    & $codexPath app-server --help *> $null
    if ($LASTEXITCODE -ne 0) { throw "Codex app-server smoke failed: $codexPath app-server --help" }
    Write-Host 'Codex CLI smoke verified'
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

function LSP-VersionArgs() {
    param([Parameter(Mandatory)][string]$ServerId)
    switch ($ServerId) {
        'gopls' { return @('version') }
        'go' { return @('version') }
        'rust-analyzer' { return @('--version') }
        'bash-language-server' { return @('--version') }
        'shellcheck' { return @('--version') }
        'sg' { return @('--help') }
        'java' { return @('-version') }
        'jdtls' { return @('-version') }
        default { return @() }
    }
}

function Expected-LSPServerSpecs() {
    param([Parameter(Mandatory)]$Manifest)
    $specs = @($script:LSPServerSpecs)
    $profile = [string](Get-JsonProperty -Object $Manifest -Name 'profile')
    if ($profile.Trim() -eq 'full') {
        $specs += $script:FullLSPServerSpecs
    }
    return $specs
}

function Verify-LSPManifest() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    $manifestPath = Join-Path $PackageRoot 'lsp/lsp-manifest.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "missing LSP manifest: $manifestPath"
    }
    $manifest = Read-JsonFile $manifestPath
    foreach ($spec in (Expected-LSPServerSpecs -Manifest $manifest)) {
        $parts = $spec.Split('|')
        $serverId = $parts[0]
        $relPath = $parts[1]
        $entry = Get-LSPManifestEntry -Manifest $manifest -ServerId $serverId
        if ($null -eq $entry) { throw "LSP manifest missing path for ${serverId}: $manifestPath" }
        $manifestPathValue = [string]$entry.path
        Assert-RelativePackagePath -Label "LSP server $serverId path" -Path $manifestPathValue
        $expected = "lsp/$relPath"
        if ((Normalize-RelPath $manifestPathValue) -ne $expected) {
            throw "LSP manifest path mismatch for ${serverId}: expected $expected, got $manifestPathValue"
        }
        $version = [string]$entry.version
        if ($version.Trim() -eq '') { throw "LSP manifest missing version for ${serverId}: $manifestPath" }
        $languages = Get-JsonProperty -Object $entry -Name 'languages'
        if ($null -eq $languages) { throw "LSP manifest missing languages for ${serverId}: $manifestPath" }
        Assert-JsonStringArray -Value $languages -Label "LSP server $serverId languages"
        $expectedSHA = [string]$entry.sha256
        if ($expectedSHA -notmatch '^[0-9A-Fa-f]{64}$') {
            throw "LSP server $serverId digest must be a 64-character SHA-256 hex digest"
        }
        $resolved = Join-Path $PackageRoot $manifestPathValue
        if (-not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
            throw "LSP server $serverId points to missing executable: $resolved"
        }
        $actualSHA = Get-SHA256File $resolved
        if ($actualSHA -ne $expectedSHA.ToLowerInvariant()) {
            throw "LSP packaged digest mismatch: $resolved"
        }
        $args = @(LSP-VersionArgs -ServerId $serverId)
        if ($args.Count -gt 0) {
            & $resolved @args *> $null
            if ($LASTEXITCODE -ne 0) {
                throw "LSP server $serverId version smoke failed: $resolved $($args -join ' ')"
            }
            Write-Host "LSP server smoke verified: $serverId"
        } else {
            Write-Host "LSP server executable verified: $serverId"
        }
    }
    foreach ($shadow in @('python.cmd', 'python3.cmd', 'go.cmd', 'ast-grep.exe', 'vcruntime140.dll')) {
        $path = Join-Path $PackageRoot "lsp/bin/$shadow"
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "missing LSP bundled helper: $path"
        }
    }
    Write-Host 'LSP manifest verified'
}

function Verify-FFmpeg() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    $path = Join-Path $PackageRoot 'bin/ffmpeg.exe'
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "missing packaged ffmpeg executable: $path"
    }
    & $path -version *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "packaged ffmpeg smoke failed: $path -version"
    }
    Write-Host 'packaged ffmpeg smoke verified'
}

function Verify-RequiredFiles() {
    param([Parameter(Mandatory)][string]$PackageRoot)
    foreach ($rel in @(
        'bin/agent-terminal.exe',
        'bin/mcp-orch.exe',
        'bin/mcp-lsp.exe',
        'bin/mcp-ida.exe',
        'bin/codex.exe',
        'bin/ffmpeg.exe',
        'bin/gopls.exe',
        'runtime-manifest.json',
        'codex-manifest.json',
        'lsp/lsp-manifest.json',
        'models.yaml',
        '.env',
        'run.cmd',
        'run.ps1'
    )) {
        $path = Join-Path $PackageRoot $rel
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "missing packaged file: $path"
        }
    }
    $sqliteMigrationsDir = Join-Path $PackageRoot 'internal/platform/db/sqlite/migrations'
    if (-not (Test-Path -LiteralPath $sqliteMigrationsDir -PathType Container)) {
        throw "missing SQLite migration files under $sqliteMigrationsDir"
    }
    $firstMigration = Get-ChildItem -LiteralPath $sqliteMigrationsDir -Recurse -File -Force | Select-Object -First 1
    if ($null -eq $firstMigration) {
        throw "missing SQLite migration files under $sqliteMigrationsDir"
    }
}

function Resolve-PackageRoot() {
    param([Parameter(Mandatory)][string]$InputPath)
    if (Test-Path -LiteralPath $InputPath -PathType Leaf) {
        if (-not $InputPath.ToLowerInvariant().EndsWith('.zip')) {
            throw "Windows verifier input must be a stage directory or .zip artifact: $InputPath"
        }
        $temp = Join-Path ([IO.Path]::GetTempPath()) ("super-dolphin-windows-verify-" + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $temp | Out-Null
        try {
            & tar.exe -xf $InputPath -C $temp
            if ($LASTEXITCODE -ne 0) {
                throw "zip extraction failed: $LASTEXITCODE"
            }
            $roots = @(Get-ChildItem -LiteralPath $temp -Directory)
            if ($roots.Count -ne 1) {
                throw "Windows package zip must contain exactly one package root"
            }
            $script:CleanupDir = $temp
            return $roots[0].FullName
        } catch {
            Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
            throw
        }
    }
    if (Test-Path -LiteralPath $InputPath -PathType Container) {
        return (Resolve-Path -LiteralPath $InputPath).Path
    }
    throw "Windows verifier input does not exist: $InputPath"
}

$script:CleanupDir = ''
try {
    $packageRoot = Resolve-PackageRoot -InputPath $Target
    Infer-WindowsPackagePlatform -PackageRoot $packageRoot
    Verify-RequiredFiles -PackageRoot $packageRoot
    Verify-UpdateEnv -PackageRoot $packageRoot
    Verify-RuntimeManifest -PackageRoot $packageRoot
    Assert-PackageNativeArchitecture -PackageRoot $packageRoot
    Verify-CodexManifest -PackageRoot $packageRoot
    Verify-LSPManifest -PackageRoot $packageRoot
    Verify-FFmpeg -PackageRoot $packageRoot
    Write-Host "Windows package verified: $packageRoot"
} finally {
    if ($script:CleanupDir -and (Test-Path -LiteralPath $script:CleanupDir)) {
        Remove-Item -LiteralPath $script:CleanupDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
