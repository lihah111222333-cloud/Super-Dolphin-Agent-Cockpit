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
    throw 'unable to resolve repository root; run from a git worktree or keep prepare_lsp_bundle_windows.ps1 under <repo>\scripts'
}

$Root = Resolve-RepoRoot
Set-Location -LiteralPath $Root

if (-not ('SuperDolphin.WindowsNativeIdentity' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

namespace SuperDolphin {
    public static class WindowsNativeIdentity {
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        public struct RTL_OSVERSIONINFOEXW {
            public uint dwOSVersionInfoSize;
            public uint dwMajorVersion;
            public uint dwMinorVersion;
            public uint dwBuildNumber;
            public uint dwPlatformId;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string szCSDVersion;
            public ushort wServicePackMajor;
            public ushort wServicePackMinor;
            public ushort wSuiteMask;
            public byte wProductType;
            public byte wReserved;
        }

        [DllImport("ntdll.dll", CharSet = CharSet.Unicode)]
        public static extern int RtlGetVersion(ref RTL_OSVERSIONINFOEXW version);

        [DllImport("kernel32.dll", SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        public static extern bool IsWow64Process2(IntPtr process, out ushort processMachine, out ushort nativeMachine);

        [DllImport("kernel32.dll")]
        public static extern IntPtr GetCurrentProcess();

        public static void ThrowLastWin32Error(string action) {
            throw new Win32Exception(Marshal.GetLastWin32Error(), action);
        }
    }
}
'@
}

# Get-WindowsHostIdentity reads the real Windows version, build, process
# architecture, and native architecture without Go or environment inference.
function Get-WindowsHostIdentity() {
    if (-not [Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([Runtime.InteropServices.OSPlatform]::Windows)) {
        throw 'prepare_lsp_bundle_windows.ps1 must run on Windows'
    }

    $version = New-Object SuperDolphin.WindowsNativeIdentity+RTL_OSVERSIONINFOEXW
    $version.dwOSVersionInfoSize = [Runtime.InteropServices.Marshal]::SizeOf($version)
    $status = [SuperDolphin.WindowsNativeIdentity]::RtlGetVersion([ref]$version)
    if ($status -ne 0 -or $version.dwMajorVersion -eq 0 -or $version.dwBuildNumber -eq 0) {
        throw "RtlGetVersion failed with NTSTATUS=$status"
    }

    [UInt16]$processMachine = 0
    [UInt16]$nativeMachine = 0
    $currentProcess = [SuperDolphin.WindowsNativeIdentity]::GetCurrentProcess()
    if (-not [SuperDolphin.WindowsNativeIdentity]::IsWow64Process2($currentProcess, [ref]$processMachine, [ref]$nativeMachine)) {
        [SuperDolphin.WindowsNativeIdentity]::ThrowLastWin32Error('IsWow64Process2 failed')
    }

    $nativeArch = switch ($nativeMachine) {
        0xAA64 { 'arm64' }
        0x8664 { 'amd64' }
        0x014C { 'x86' }
        default { throw ('unsupported native Windows IMAGE_FILE_MACHINE=0x{0:X4}' -f $nativeMachine) }
    }
    $effectiveProcessMachine = if ($processMachine -eq 0) { $nativeMachine } else { $processMachine }
    $processArch = switch ($effectiveProcessMachine) {
        0xAA64 { 'arm64' }
        0x8664 { 'amd64' }
        0x014C { 'x86' }
        default { throw ('unsupported process Windows IMAGE_FILE_MACHINE=0x{0:X4}' -f $effectiveProcessMachine) }
    }

    return [PSCustomObject]@{
        Version = "$($version.dwMajorVersion).$($version.dwMinorVersion)"
        Build = [UInt32]$version.dwBuildNumber
        NativeArch = $nativeArch
        ProcessArch = $processArch
    }
}

$WindowsHost = Get-WindowsHostIdentity
$GoOS = 'windows'

function Resolve-WindowsPackageArch() {
    $configured = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_ARCH', 'Process')
    $detected = $WindowsHost.NativeArch
    if ($null -eq $configured -or $configured.Trim() -eq '') {
        return $detected
    }
    # Keep aliases synchronized with installer.NormalizeWindowsArchitectureAlias; the Go guard checks both owners for drift.
    $requested = switch ($configured.Trim().ToLowerInvariant()) {
        { $_ -in @('arm64', 'aarch64', 'armv8', 'arm64-v8a') } { 'arm64'; break }
        { $_ -in @('amd64', 'x64', 'x86_64', 'x86-64') } { 'amd64'; break }
        { $_ -in @('386', 'x86', 'i386', 'i486', 'i586', 'i686', 'x86-32', 'ia32') } { 'x86'; break }
        default { throw "unsupported SUPER_DOLPHIN_WINDOWS_ARCH=$configured; expected arm64, x64/amd64, or x86/386" }
    }
    if ($requested -ne $detected) {
        throw "cross-architecture Windows LSP packaging is forbidden: requested=$requested native=$detected"
    }
    return $detected
}

$WindowsPackageArch = Resolve-WindowsPackageArch
$env:SUPER_DOLPHIN_WINDOWS_ARCH = $WindowsPackageArch
$Platform = "$GoOS-$WindowsPackageArch"
if ($WindowsHost.Version -ne '10.0' -or $WindowsHost.Build -lt 19041) {
    throw "unsupported Windows host $($WindowsHost.Version) build $($WindowsHost.Build); require Windows 10.0 build 19041 or newer"
}
Write-Host "==> detected Windows $($WindowsHost.Version) build $($WindowsHost.Build), native=$($WindowsHost.NativeArch), process=$($WindowsHost.ProcessArch)"

$LSPProfile = if ($env:SUPER_DOLPHIN_LSP_PROFILE) { $env:SUPER_DOLPHIN_LSP_PROFILE } else { 'standard' }
if ($LSPProfile -notin @('standard', 'full')) {
    throw "unsupported SUPER_DOLPHIN_LSP_PROFILE=$LSPProfile; expected standard or full"
}

$LspDir = if ($env:SUPER_DOLPHIN_LSP_BUNDLE_DIR) { $env:SUPER_DOLPHIN_LSP_BUNDLE_DIR } else { Join-Path $Root ".build-cache/lsp/$LSPProfile/$Platform" }
$DefaultNodeDist = ''
$nodeCmd = Get-Command node.exe -ErrorAction SilentlyContinue | Select-Object -First 1
if ($nodeCmd) { $DefaultNodeDist = Split-Path -Parent $nodeCmd.Source }
$NodeSrc = if ($env:SUPER_DOLPHIN_NODE_DIST) { $env:SUPER_DOLPHIN_NODE_DIST } else { $DefaultNodeDist }
$NpmBin = if ($env:SUPER_DOLPHIN_NPM_BIN) { $env:SUPER_DOLPHIN_NPM_BIN } else {
    $cmd = Get-Command npm.cmd -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { $cmd.Source } else { '' }
}
$GoToolchainSrc = if ($env:SUPER_DOLPHIN_GO_TOOLCHAIN_DIR) { $env:SUPER_DOLPHIN_GO_TOOLCHAIN_DIR } else { (& go env GOROOT).Trim() }
$GoplsBin = if ($env:SUPER_DOLPHIN_GOPLS_BIN) { $env:SUPER_DOLPHIN_GOPLS_BIN } else {
    $cmd = Get-Command gopls.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { $cmd.Source } else { '' }
}
$ClangdBin = if ($env:SUPER_DOLPHIN_CLANGD_BIN) { $env:SUPER_DOLPHIN_CLANGD_BIN } else {
    $cmd = Get-Command clangd.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { $cmd.Source } else { '' }
}
$RustAnalyzerBin = if ($env:SUPER_DOLPHIN_RUST_ANALYZER_BIN) { $env:SUPER_DOLPHIN_RUST_ANALYZER_BIN } else {
    $cmd = Get-Command rust-analyzer.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { $cmd.Source } else { '' }
}
$SqruffBin = if ($env:SUPER_DOLPHIN_SQRUFF_BIN) { $env:SUPER_DOLPHIN_SQRUFF_BIN } else {
    $cmd = Get-Command sqruff.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { $cmd.Source } else { '' }
}
$ConfiguredMSVCRuntimeDir = if ($env:SUPER_DOLPHIN_MSVC_RUNTIME_DIR) { $env:SUPER_DOLPHIN_MSVC_RUNTIME_DIR.Trim() } else { '' }
$ShellcheckBin = if ($env:SUPER_DOLPHIN_SHELLCHECK_BIN) { $env:SUPER_DOLPHIN_SHELLCHECK_BIN } else { '' }
$OmitShellcheck = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK', 'Process') -eq '1'
$JDTLSHome = if ($env:SUPER_DOLPHIN_JDTLS_HOME) { $env:SUPER_DOLPHIN_JDTLS_HOME } else { '' }
$JDKHome = if ($env:SUPER_DOLPHIN_JDK_HOME) { $env:SUPER_DOLPHIN_JDK_HOME } elseif ($env:JAVA_HOME) { $env:JAVA_HOME } else { '' }
$LSPNpmPackages = @(
    'typescript-language-server@5.3.0',
    'typescript@5.9.3',
    'vscode-langservers-extracted@4.10.0',
    'vscode-markdown-languageservice@0.5.0-alpha.11',
    'markdown-it@14.2.0',
    'pyright@1.1.412',
    'yaml-language-server@1.24.0',
    '@vue/language-server@3.3.9',
    'svelte-language-server@0.18.4',
    'intelephense@1.18.5',
    'dockerfile-language-server-nodejs@0.15.0',
    'graphql-language-service-cli@3.5.0',
    '@prisma/language-server@31.11.0',
    'bash-language-server@5.6.0',
    '@ast-grep/cli@0.43.0'
)
if (-not $OmitShellcheck -and $ShellcheckBin.Trim() -eq '' -and $WindowsPackageArch -ne 'arm64') {
    $LSPNpmPackages += 'shellcheck@4.1.0'
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

function Get-SHA256File() {
    param([Parameter(Mandatory)][string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-NpmPackageVersion() {
    param(
        [Parameter(Mandatory)][string]$PackageName,
        [Parameter(Mandatory)][string]$ExpectedVersion
    )
    $packageJsonPath = Join-Path $LspDir "node_modules/$PackageName/package.json"
    Require-File -Path $packageJsonPath -Message "missing installed npm package metadata: $packageJsonPath"
    $package = Get-Content -Raw -LiteralPath $packageJsonPath | ConvertFrom-Json
    $version = [string]$package.version
    if ($version.Trim() -eq '') {
        throw "npm package $PackageName has no version in $packageJsonPath"
    }
    if ($version.Trim() -ne $ExpectedVersion) {
        throw "npm package $PackageName resolved to $version; expected exact version $ExpectedVersion"
    }
    return $version.Trim()
}

function Get-ExecutableVersion() {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string[]]$Arguments,
        [Parameter(Mandatory)][string]$Label
    )
    Require-File -Path $Path -Message "missing executable for version discovery: $Path"
    $output = (& $Path @Arguments 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "$Label version probe failed with exit code $LASTEXITCODE"
    }
    $match = [regex]::Match($output, '(?m)(?:^|[^0-9])(?:v|go)?([0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[-+._][0-9A-Za-z.-]+)?)')
    if (-not $match.Success) {
        throw "$Label version probe returned no semantic version: $output"
    }
    return $match.Groups[1].Value
}

function Get-JDTLSVersion() {
    $pluginsDir = Join-Path $LspDir 'jdtls/plugins'
    $jar = Get-ChildItem -LiteralPath $pluginsDir -Filter 'org.eclipse.jdt.ls.core_*.jar' -File | Select-Object -First 1
    if (-not $jar) {
        throw "missing jdtls core plugin for version discovery under $pluginsDir"
    }
    $match = [regex]::Match($jar.Name, 'org\.eclipse\.jdt\.ls\.core_([0-9][0-9A-Za-z._-]*)\.jar$')
    if (-not $match.Success) {
        throw "unable to discover jdtls version from $($jar.Name)"
    }
    return $match.Groups[1].Value
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
        0x014C { return 'x86' }
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

function Copy-DirectoryClean() {
    param(
        [Parameter(Mandatory)][string]$Source,
        [Parameter(Mandatory)][string]$Destination
    )
    if (-not (Test-Path -LiteralPath $Source -PathType Container)) { throw "missing directory: $Source" }
    if (Test-Path -LiteralPath $Destination) { Remove-Item -LiteralPath $Destination -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Get-ChildItem -LiteralPath $Source -Force | Copy-Item -Destination $Destination -Recurse -Force
}

function Require-File() {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$Message
    )
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw $Message }
}

# Get-WindowsVCLibsDesktopAsset returns the one locked Microsoft Appx for the detected native Windows architecture.
function Get-WindowsVCLibsDesktopAsset() {
    switch ($WindowsPackageArch) {
        'arm64' {
            return [PSCustomObject]@{
                Url = 'https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.arm64.14.00.Desktop.appx'
                SHA256 = '9a7f6d69ea6cf042ea8680b7cd0bfaa9c04f0f6cc89055d43f7f6cd0250508d3'
                ManifestArchitecture = 'arm64'
            }
        }
        'amd64' {
            return [PSCustomObject]@{
                Url = 'https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.x64.14.00.Desktop.appx'
                SHA256 = 'b56a9101f706f9d95f815f5b7fa6efbac972e86573d378b96a07cff5540c5961'
                ManifestArchitecture = 'x64'
            }
        }
        'x86' {
            return [PSCustomObject]@{
                Url = 'https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.x86.14.00.Desktop.appx'
                SHA256 = 'a7fb9d76e07b36d868179eb53ffd13740c25242176fa363f154798cf34edd4a9'
                ManifestArchitecture = 'x86'
            }
        }
        default { throw "unsupported native Windows architecture for VCLibs Desktop: $WindowsPackageArch" }
    }
}

# Assert-WindowsVCLibsDesktopDirectory verifies the locked Appx identity and the PE architecture of every required DLL.
function Assert-WindowsVCLibsDesktopDirectory() {
    param(
        [Parameter(Mandatory)][string]$Directory,
        [Parameter(Mandatory)][string]$ManifestArchitecture,
        [Parameter(Mandatory)][bool]$RequireManifest
    )
    if (-not (Test-Path -LiteralPath $Directory -PathType Container)) {
        throw "Windows VCLibs Desktop directory does not exist: $Directory"
    }
    if ($RequireManifest) {
        $manifestPath = Join-Path $Directory 'AppxManifest.xml'
        Require-File -Path $manifestPath -Message "missing Windows VCLibs Desktop AppxManifest: $manifestPath"
        [xml]$manifest = Get-Content -Raw -LiteralPath $manifestPath
        $identity = $manifest.Package.Identity
        if ([string]$identity.Name -ne 'Microsoft.VCLibs.140.00.UWPDesktop' -or
            [string]$identity.Version -ne '14.0.33321.0' -or
            [string]$identity.Publisher -ne 'CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US' -or
            [string]$identity.ProcessorArchitecture -ne $ManifestArchitecture) {
            throw "Windows VCLibs Desktop Appx identity mismatch: name=$($identity.Name) version=$($identity.Version) architecture=$($identity.ProcessorArchitecture)"
        }
    }
    foreach ($dllName in @('concrt140.dll', 'msvcp140.dll', 'msvcp140_1.dll', 'msvcp140_2.dll', 'msvcp140_atomic_wait.dll', 'msvcp140_codecvt_ids.dll', 'vcruntime140.dll')) {
        $dllPath = Join-Path $Directory $dllName
        Require-File -Path $dllPath -Message "missing Windows VCLibs Desktop runtime DLL: $dllPath"
        Assert-WindowsNativeArchitecture -Path $dllPath -ExpectedArch $WindowsPackageArch -Label "Windows VCLibs Desktop $dllName"
    }
}

# Resolve-WindowsVCLibsDesktopDirectory keeps explicit configuration fail-fast and atomically provisions a private Appx cache when needed.
function Resolve-WindowsVCLibsDesktopDirectory() {
    $asset = Get-WindowsVCLibsDesktopAsset
    if ($ConfiguredMSVCRuntimeDir -ne '') {
        Assert-WindowsVCLibsDesktopDirectory -Directory $ConfiguredMSVCRuntimeDir -ManifestArchitecture $asset.ManifestArchitecture -RequireManifest $false
        return (Resolve-Path -LiteralPath $ConfiguredMSVCRuntimeDir).Path
    }
    $systemRuntimeDir = Join-Path $env:WINDIR 'System32'
    if (Test-Path -LiteralPath (Join-Path $systemRuntimeDir 'vcruntime140.dll') -PathType Leaf) {
        Assert-WindowsVCLibsDesktopDirectory -Directory $systemRuntimeDir -ManifestArchitecture $asset.ManifestArchitecture -RequireManifest $false
        return (Resolve-Path -LiteralPath $systemRuntimeDir).Path
    }

    $version = '14.0.33321.0'
    $assetRoot = Join-Path $Root ".build-cache/lsp/windows-vclibs-desktop-app-local/$version/$WindowsPackageArch/$($asset.SHA256)"
    New-Item -ItemType Directory -Force -Path $assetRoot | Out-Null
    $readyDir = Join-Path $assetRoot 'ready'
    $payloadPath = Join-Path $assetRoot 'Microsoft.VCLibs.Desktop.appx'
    $lockPath = Join-Path $assetRoot '.lock'
    $lockStream = $null
    $lockDeadline = [DateTime]::UtcNow.AddMinutes(10)
    while ($null -eq $lockStream) {
        try {
            $lockStream = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        } catch [IO.IOException] {
            if ([DateTime]::UtcNow -ge $lockDeadline) { throw "timed out waiting for Windows VCLibs Desktop cache lock: $lockPath" }
            Start-Sleep -Milliseconds 200
        }
    }
    try {
        if (Test-Path -LiteralPath $readyDir -PathType Container) {
            try {
                Assert-WindowsVCLibsDesktopDirectory -Directory $readyDir -ManifestArchitecture $asset.ManifestArchitecture -RequireManifest $true
                return (Resolve-Path -LiteralPath $readyDir).Path
            } catch {
                Write-Warning "removing invalid Windows VCLibs Desktop cache entry: $($_.Exception.Message)"
                Remove-Item -LiteralPath $readyDir -Recurse -Force
            }
        }
        if (Test-Path -LiteralPath $payloadPath -PathType Leaf) {
            if ((Get-SHA256File -Path $payloadPath) -ne $asset.SHA256) {
                Remove-Item -LiteralPath $payloadPath -Force
            }
        }
        if (-not (Test-Path -LiteralPath $payloadPath -PathType Leaf)) {
            $temporaryPayload = Join-Path $assetRoot ('.payload-' + [Guid]::NewGuid().ToString('N'))
            try {
                Invoke-WebRequest -UseBasicParsing -Uri $asset.Url -OutFile $temporaryPayload
                if ((Get-Item -LiteralPath $temporaryPayload).Length -gt 64MB) { throw 'Windows VCLibs Desktop Appx exceeds the 64 MiB download limit' }
                $downloadedHash = Get-SHA256File -Path $temporaryPayload
                if ($downloadedHash -ne $asset.SHA256) { throw "Windows VCLibs Desktop SHA-256 mismatch: got=$downloadedHash want=$($asset.SHA256)" }
                Move-Item -LiteralPath $temporaryPayload -Destination $payloadPath
            } finally {
                if (Test-Path -LiteralPath $temporaryPayload) { Remove-Item -LiteralPath $temporaryPayload -Force }
            }
        }

        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $temporaryReady = Join-Path $assetRoot ('.ready-' + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $temporaryReady | Out-Null
        try {
            $archive = [IO.Compression.ZipFile]::OpenRead($payloadPath)
            try {
                $destinationPrefix = [IO.Path]::GetFullPath($temporaryReady) + [IO.Path]::DirectorySeparatorChar
                [Int64]$expandedBytes = 0
                if ($archive.Entries.Count -gt 512) { throw 'Windows VCLibs Desktop Appx exceeds the 512-entry archive limit' }
                foreach ($entry in $archive.Entries) {
                    $entryDestination = [IO.Path]::GetFullPath((Join-Path $temporaryReady $entry.FullName))
                    if (-not $entryDestination.StartsWith($destinationPrefix, [StringComparison]::OrdinalIgnoreCase)) {
                        throw "Windows VCLibs Desktop Appx entry escapes extraction root: $($entry.FullName)"
                    }
                    $expandedBytes += [Int64]$entry.Length
                    if ($expandedBytes -gt 512MB) { throw 'Windows VCLibs Desktop Appx exceeds the 512 MiB expansion limit' }
                }
            } finally {
                $archive.Dispose()
            }
            [IO.Compression.ZipFile]::ExtractToDirectory($payloadPath, $temporaryReady)
            Assert-WindowsVCLibsDesktopDirectory -Directory $temporaryReady -ManifestArchitecture $asset.ManifestArchitecture -RequireManifest $true
            Move-Item -LiteralPath $temporaryReady -Destination $readyDir
        } finally {
            if (Test-Path -LiteralPath $temporaryReady) { Remove-Item -LiteralPath $temporaryReady -Recurse -Force }
        }
        return (Resolve-Path -LiteralPath $readyDir).Path
    } finally {
        $lockStream.Dispose()
    }
}

function Write-NoSystemPythonStub() {
    param([Parameter(Mandatory)][string]$Name)
    $content = @'
@echo off
echo Packaged Super Dolphin does not bundle a Python interpreter; skipping system interpreter fallback. 1>&2
exit /b 127
'@
    Write-Utf8NoBom -Path (Join-Path $LspDir "bin/$Name") -Content $content
}

function Write-NodeCmdWrapper() {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Target
    )
    $content = @"
@echo off
setlocal
set "HERE=%~dp0"
set "PATH=%HERE%..\node;%PATH%"
"%HERE%..\node\node.exe" "%HERE%..\node_modules\$Target" %*
exit /b %ERRORLEVEL%
"@
    Write-Utf8NoBom -Path (Join-Path $LspDir "bin/$Name") -Content $content
}

function Write-CmdWrapper() {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Target
    )
    Require-File -Path (Join-Path $LspDir $Target) -Message "missing bundled executable target: $(Join-Path $LspDir $Target)"
    $content = @"
@echo off
setlocal
set "HERE=%~dp0"
set "PATH=%HERE%..\node;%PATH%"
"%HERE%..\$Target" %*
exit /b %ERRORLEVEL%
"@
    Write-Utf8NoBom -Path (Join-Path $LspDir "bin/$Name") -Content $content
}

function Write-GoToolchainWrapper() {
    $content = @'
@echo off
setlocal
set "HERE=%~dp0"
set "GOROOT=%HERE%..\go"
if "%GOTOOLCHAIN%"=="" set "GOTOOLCHAIN=local"
"%GOROOT%\bin\go.exe" %*
exit /b %ERRORLEVEL%
'@
    Write-Utf8NoBom -Path (Join-Path $LspDir 'bin/go.cmd') -Content $content
    & (Join-Path $LspDir 'bin/go.cmd') env GOROOT | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'bundled Go toolchain wrapper failed' }
}

function Write-JavaRuntimeWrapper() {
    $content = @'
@echo off
setlocal
set "HERE=%~dp0"
set "JAVA_HOME=%HERE%..\jdk"
"%JAVA_HOME%\bin\java.exe" %*
exit /b %ERRORLEVEL%
'@
    Write-Utf8NoBom -Path (Join-Path $LspDir 'bin/java.cmd') -Content $content
    & (Join-Path $LspDir 'bin/java.cmd') -version *> $null
    if ($LASTEXITCODE -ne 0) { throw 'bundled Java runtime wrapper failed' }
}

function Write-JDTLSWrapper() {
    $content = @'
@echo off
setlocal
set "HERE=%~dp0"
set "JAVA_HOME=%HERE%..\jdk"
set "PATH=%JAVA_HOME%\bin;%PATH%"
set "JDTLS_HOME=%HERE%..\jdtls"
set "CONFIG_DIR=%JDTLS_HOME%\config_win"
if not exist "%CONFIG_DIR%" (
  echo missing bundled jdtls config: %CONFIG_DIR% 1>&2
  exit /b 1
)
if exist "%JDTLS_HOME%\plugins\org.eclipse.equinox.launcher.jar" (
  set "LAUNCHER=%JDTLS_HOME%\plugins\org.eclipse.equinox.launcher.jar"
) else (
  for %%F in ("%JDTLS_HOME%\plugins\org.eclipse.equinox.launcher_*.jar") do set "LAUNCHER=%%~fF"
)
if not exist "%LAUNCHER%" (
  echo missing bundled jdtls equinox launcher under %JDTLS_HOME%\plugins 1>&2
  exit /b 1
)
if "%~1"=="-version" (
  "%JAVA_HOME%\bin\java.exe" -version
  exit /b %ERRORLEVEL%
)
if "%~1"=="--version" (
  "%JAVA_HOME%\bin\java.exe" -version
  exit /b %ERRORLEVEL%
)
set "CACHE_ROOT=%LOCALAPPDATA%\Super Dolphin\jdtls"
set "DATA_DIR=%CACHE_ROOT%\workspace"
"%JAVA_HOME%\bin\java.exe" -Djdk.xml.maxGeneralEntitySizeLimit=0 -Djdk.xml.totalEntitySizeLimit=0 -Declipse.application=org.eclipse.jdt.ls.core.id1 -Dosgi.bundles.defaultStartLevel=4 -Declipse.product=org.eclipse.jdt.ls.core.product -Dosgi.checkConfiguration=true -Dosgi.sharedConfiguration.area="%CONFIG_DIR%" -Dosgi.sharedConfiguration.area.readOnly=true -Dosgi.configuration.cascaded=true -Xms1G --add-modules=ALL-SYSTEM --add-opens java.base/java.util=ALL-UNNAMED --add-opens java.base/java.lang=ALL-UNNAMED -jar "%LAUNCHER%" -data "%DATA_DIR%" %*
exit /b %ERRORLEVEL%
'@
    Write-Utf8NoBom -Path (Join-Path $LspDir 'bin/jdtls.cmd') -Content $content
    & (Join-Path $LspDir 'bin/jdtls.cmd') -version *> $null
    if ($LASTEXITCODE -ne 0) { throw 'bundled jdtls wrapper failed' }
}

function Prune-LSPBundleRuntimeOnlyArtifacts() {
    $keepPrefix = switch ($WindowsPackageArch) {
        'arm64' { 'win32-arm64' }
        'amd64' { 'win32-x64' }
        'x86' { 'win32-ia32' }
        default { throw "unsupported Windows LSP bundle architecture: $WindowsPackageArch" }
    }
    $prebuildRoot = Join-Path $LspDir 'node_modules'
    if (Test-Path -LiteralPath $prebuildRoot -PathType Container) {
        Get-ChildItem -LiteralPath $prebuildRoot -Directory -Recurse -ErrorAction SilentlyContinue |
            Where-Object { $_.FullName -match '\\prebuilds\\' -and -not $_.Name.StartsWith($keepPrefix) } |
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
    }
    foreach ($rel in @('jdk/jmods', 'jdk/demo', 'jdk/include')) {
        $path = Join-Path $LspDir $rel
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Recurse -Force }
    }
}

function Assert-LSPBundleNativeArchitecture() {
    param([Parameter(Mandatory)][string]$BundleDir)
    $nativePaths = @(
        'bin/gopls.exe',
        'bin/clangd.exe',
        'bin/rust-analyzer.exe',
        'bin/sg.exe',
        'bin/ast-grep.exe',
        'bin/concrt140.dll',
        'bin/msvcp140.dll',
        'bin/msvcp140_1.dll',
        'bin/msvcp140_2.dll',
        'bin/msvcp140_atomic_wait.dll',
        'bin/msvcp140_codecvt_ids.dll',
        'bin/vcruntime140.dll',
        'node/node.exe',
        'go/bin/go.exe'
    )
    if (-not $OmitShellcheck) {
        $nativePaths += 'bin/shellcheck.exe'
    }
    foreach ($relPath in $nativePaths) {
        Assert-WindowsNativeArchitecture -Path (Join-Path $BundleDir $relPath) -ExpectedArch $WindowsPackageArch -Label "LSP bundle $relPath"
    }
    $javaPath = Join-Path $BundleDir 'jdk/bin/java.exe'
    if (Test-Path -LiteralPath $javaPath -PathType Leaf) {
        Assert-WindowsNativeArchitecture -Path $javaPath -ExpectedArch $WindowsPackageArch -Label 'LSP bundle jdk/bin/java.exe'
    }
}

function Write-LSPManifestAndChecksums() {
    $specs = @(
        [ordered]@{ id = 'gopls'; path = 'bin/gopls.exe'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/gopls.exe') -Arguments @('version') -Label 'gopls'); languages = @('go', 'gomod', 'gosum', 'gowork') },
        [ordered]@{ id = 'clangd'; path = 'bin/clangd.exe'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/clangd.exe') -Arguments @('--version') -Label 'clangd'); languages = @('c', 'cpp', 'objective-c', 'objective-cpp', 'mql', 'mql4', 'mql5', 'mq4', 'mq5', 'mqh') },
        [ordered]@{ id = 'typescript-language-server'; path = 'bin/typescript-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'typescript-language-server' -ExpectedVersion '5.3.0'); languages = @('javascript', 'javascriptreact', 'typescript', 'typescriptreact') },
        [ordered]@{ id = 'vscode-langservers-extracted'; path = 'bin/vscode-css-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'vscode-langservers-extracted' -ExpectedVersion '4.10.0'); languages = @('css') },
        [ordered]@{ id = 'vscode-html-language-server'; path = 'bin/vscode-html-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'vscode-langservers-extracted' -ExpectedVersion '4.10.0'); languages = @('html') },
        [ordered]@{ id = 'vscode-json-language-server'; path = 'bin/vscode-json-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'vscode-langservers-extracted' -ExpectedVersion '4.10.0'); languages = @('json') },
        [ordered]@{ id = 'vscode-markdown-language-server'; path = 'bin/vscode-markdown-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'vscode-langservers-extracted' -ExpectedVersion '4.10.0'); languages = @('markdown') },
        [ordered]@{ id = 'pyright'; path = 'bin/pyright-langserver.cmd'; version = (Get-NpmPackageVersion -PackageName 'pyright' -ExpectedVersion '1.1.412'); languages = @('python') },
        [ordered]@{ id = 'yaml-language-server'; path = 'bin/yaml-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'yaml-language-server' -ExpectedVersion '1.24.0'); languages = @('yaml') },
        [ordered]@{ id = 'vue-language-server'; path = 'bin/vue-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName '@vue/language-server' -ExpectedVersion '3.3.9'); languages = @('vue') },
        [ordered]@{ id = 'svelteserver'; path = 'bin/svelteserver.cmd'; version = (Get-NpmPackageVersion -PackageName 'svelte-language-server' -ExpectedVersion '0.18.4'); languages = @('svelte') },
        [ordered]@{ id = 'intelephense'; path = 'bin/intelephense.cmd'; version = (Get-NpmPackageVersion -PackageName 'intelephense' -ExpectedVersion '1.18.5'); languages = @('php') },
        [ordered]@{ id = 'docker-langserver'; path = 'bin/docker-langserver.cmd'; version = (Get-NpmPackageVersion -PackageName 'dockerfile-language-server-nodejs' -ExpectedVersion '0.15.0'); languages = @('dockerfile') },
        [ordered]@{ id = 'graphql-lsp'; path = 'bin/graphql-lsp.cmd'; version = (Get-NpmPackageVersion -PackageName 'graphql-language-service-cli' -ExpectedVersion '3.5.0'); languages = @('graphql') },
        [ordered]@{ id = 'prisma-language-server'; path = 'bin/prisma-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName '@prisma/language-server' -ExpectedVersion '31.11.0'); languages = @('prisma') },
        [ordered]@{ id = 'bash-language-server'; path = 'bin/bash-language-server.cmd'; version = (Get-NpmPackageVersion -PackageName 'bash-language-server' -ExpectedVersion '5.6.0'); languages = @('shellscript') },
        [ordered]@{ id = 'rust-analyzer'; path = 'bin/rust-analyzer.exe'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/rust-analyzer.exe') -Arguments @('--version') -Label 'rust-analyzer'); languages = @('rust') },
        [ordered]@{ id = 'sqruff'; path = 'bin/sqruff.exe'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/sqruff.exe') -Arguments @('--version') -Label 'sqruff'); languages = @('sql') },
        [ordered]@{ id = 'sg'; path = 'bin/sg.exe'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/sg.exe') -Arguments @('--version') -Label 'ast-grep'); languages = @('ast-grep') },
        [ordered]@{ id = 'go'; path = 'bin/go.cmd'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/go.cmd') -Arguments @('version') -Label 'Go toolchain'); languages = @('go-toolchain') }
    )
    if (-not $OmitShellcheck) {
        $specs += [ordered]@{ id = 'shellcheck'; path = 'bin/shellcheck.exe'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/shellcheck.exe') -Arguments @('--version') -Label 'shellcheck'); languages = @('shellcheck') }
    }
    if ($LSPProfile -eq 'full') {
        $specs += [ordered]@{ id = 'java'; path = 'bin/java.cmd'; version = (Get-ExecutableVersion -Path (Join-Path $LspDir 'bin/java.cmd') -Arguments @('-version') -Label 'Java runtime'); languages = @('java-runtime') }
        $specs += [ordered]@{ id = 'jdtls'; path = 'bin/jdtls.cmd'; version = (Get-JDTLSVersion); languages = @('java') }
    }
    $helperPaths = @(
        'bin/ast-grep.exe',
        'bin/concrt140.dll',
        'bin/msvcp140.dll',
        'bin/msvcp140_1.dll',
        'bin/msvcp140_2.dll',
        'bin/msvcp140_atomic_wait.dll',
        'bin/msvcp140_codecvt_ids.dll',
        'bin/vcruntime140.dll'
    )
    $servers = [ordered]@{}
    $checksumLines = New-Object System.Collections.Generic.List[string]
    foreach ($spec in $specs) {
        $serverId = [string]$spec.id
        $relPath = [string]$spec.path
        $version = [string]$spec.version
        $languages = [string[]]$spec.languages
        if ($version.Trim() -eq '' -or $version -eq 'bundled') { throw "LSP manifest version for $serverId must be a discovered non-placeholder version" }
        if ($languages.Count -eq 0) { throw "LSP manifest languages for $serverId must be a non-empty JSON array" }
        $fullPath = Join-Path $LspDir $relPath
        Require-File -Path $fullPath -Message "missing LSP manifest executable: $fullPath"
        $digest = Get-SHA256File $fullPath
        $servers[$serverId] = [ordered]@{
            path = $relPath
            version = $version
            sha256 = $digest
            languages = $languages
        }
        $checksumLines.Add("$digest  $relPath") | Out-Null
    }
    foreach ($relPath in $helperPaths) {
        $fullPath = Join-Path $LspDir $relPath
        Require-File -Path $fullPath -Message "missing LSP helper for checksums: $fullPath"
        $checksumLines.Add("$(Get-SHA256File $fullPath)  $relPath") | Out-Null
    }
    $manifest = [ordered]@{
        schema_version = 1
        bundle_path = 'lsp'
        profile = $LSPProfile
        servers = $servers
    }
    Write-Utf8NoBom -Path (Join-Path $LspDir 'lsp-manifest.json') -Content (($manifest | ConvertTo-Json -Depth 8) + "`n")
    Write-Utf8NoBom -Path (Join-Path $LspDir 'lsp-checksums.sha256') -Content (($checksumLines -join "`n") + "`n")
}

function Resolve-ShellcheckExecutable() {
    if ($OmitShellcheck) {
        Write-Warning 'SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK=1; shellcheck will not be included in this Windows LSP bundle'
        return ''
    }
    if ($ShellcheckBin.Trim() -ne '') {
        Require-File -Path $ShellcheckBin -Message 'missing shellcheck executable; set SUPER_DOLPHIN_SHELLCHECK_BIN to a Windows native shellcheck.exe'
        Assert-WindowsNativeArchitecture -Path $ShellcheckBin -ExpectedArch $WindowsPackageArch -Label 'shellcheck'
        & $ShellcheckBin --version *> $null
        if ($LASTEXITCODE -ne 0) { throw 'provided SUPER_DOLPHIN_SHELLCHECK_BIN failed --version smoke' }
        return $ShellcheckBin
    }
    if ($WindowsPackageArch -eq 'arm64') {
        throw 'missing ARM64 shellcheck.exe; shellcheck npm package does not publish win32-arm64. Set SUPER_DOLPHIN_SHELLCHECK_BIN to a Windows ARM64 shellcheck.exe.'
    }
    $shellcheckJS = Join-Path $LspDir 'node_modules/shellcheck/bin/shellcheck.js'
    Require-File -Path $shellcheckJS -Message "missing shellcheck npm launcher: $shellcheckJS"
    & (Join-Path $NodeSrc 'node.exe') $shellcheckJS --version *> $null
    if ($LASTEXITCODE -ne 0) { throw 'shellcheck npm launcher failed to prepare bundled executable' }
    $shellcheck = Join-Path $LspDir 'node_modules/shellcheck/bin/shellcheck.exe'
    Require-File -Path $shellcheck -Message "missing shellcheck executable: $shellcheck"
    Assert-WindowsNativeArchitecture -Path $shellcheck -ExpectedArch $WindowsPackageArch -Label 'shellcheck'
    return $shellcheck
}

Write-Host "==> preparing $LSPProfile Windows LSP bundle: $LspDir"
if (Test-Path -LiteralPath $LspDir) { Remove-Item -LiteralPath $LspDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path (Join-Path $LspDir 'bin') | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $LspDir 'node') | Out-Null

Require-File -Path (Join-Path $NodeSrc 'node.exe') -Message "missing node.exe; set SUPER_DOLPHIN_NODE_DIST"
Assert-WindowsNativeArchitecture -Path (Join-Path $NodeSrc 'node.exe') -ExpectedArch $WindowsPackageArch -Label 'Node runtime'
Copy-Item -LiteralPath (Join-Path $NodeSrc 'node.exe') -Destination (Join-Path $LspDir 'node/node.exe') -Force
Require-File -Path $NpmBin -Message 'missing npm; set SUPER_DOLPHIN_NPM_BIN'

Write-Host "==> installing Node-based LSP packages with host npm: $NpmBin"
$oldPath = $env:Path
try {
    $env:Path = "$NodeSrc;$oldPath"
    & $NpmBin install --prefix $LspDir --save-exact @LSPNpmPackages
    if ($LASTEXITCODE -ne 0) { throw 'npm install --prefix $LspDir failed' }
} finally {
    $env:Path = $oldPath
}

$expectedNpmPackageVersions = [ordered]@{
    'typescript-language-server' = '5.3.0'
    'typescript' = '5.9.3'
    'vscode-langservers-extracted' = '4.10.0'
    'vscode-markdown-languageservice' = '0.5.0-alpha.11'
    'markdown-it' = '14.2.0'
    'pyright' = '1.1.412'
    'yaml-language-server' = '1.24.0'
    '@vue/language-server' = '3.3.9'
    'svelte-language-server' = '0.18.4'
    'intelephense' = '1.18.5'
    'dockerfile-language-server-nodejs' = '0.15.0'
    'graphql-language-service-cli' = '3.5.0'
    '@prisma/language-server' = '31.11.0'
    'bash-language-server' = '5.6.0'
    '@ast-grep/cli' = '0.43.0'
}
if (-not $OmitShellcheck -and $ShellcheckBin.Trim() -eq '' -and $WindowsPackageArch -ne 'arm64') {
    $expectedNpmPackageVersions['shellcheck'] = '4.1.0'
}
foreach ($packageName in $expectedNpmPackageVersions.Keys) {
    [void](Get-NpmPackageVersion -PackageName $packageName -ExpectedVersion $expectedNpmPackageVersions[$packageName])
}

Write-NoSystemPythonStub -Name 'python.cmd'
Write-NoSystemPythonStub -Name 'python3.cmd'
Write-NodeCmdWrapper -Name 'typescript-language-server.cmd' -Target 'typescript-language-server\lib\cli.mjs'
Write-NodeCmdWrapper -Name 'vscode-css-language-server.cmd' -Target 'vscode-langservers-extracted\bin\vscode-css-language-server'
Write-NodeCmdWrapper -Name 'vscode-html-language-server.cmd' -Target 'vscode-langservers-extracted\bin\vscode-html-language-server'
Write-NodeCmdWrapper -Name 'vscode-json-language-server.cmd' -Target 'vscode-langservers-extracted\bin\vscode-json-language-server'
Write-NodeCmdWrapper -Name 'vscode-markdown-language-server.cmd' -Target 'vscode-langservers-extracted\bin\vscode-markdown-language-server'
Write-NodeCmdWrapper -Name 'pyright-langserver.cmd' -Target 'pyright\langserver.index.js'
Write-NodeCmdWrapper -Name 'yaml-language-server.cmd' -Target 'yaml-language-server\bin\yaml-language-server'
Write-NodeCmdWrapper -Name 'vue-language-server.cmd' -Target '@vue\language-server\bin\vue-language-server.js'
Write-NodeCmdWrapper -Name 'svelteserver.cmd' -Target 'svelte-language-server\bin\server.js'
Write-NodeCmdWrapper -Name 'intelephense.cmd' -Target 'intelephense\lib\intelephense.js'
Write-NodeCmdWrapper -Name 'docker-langserver.cmd' -Target 'dockerfile-language-server-nodejs\bin\docker-langserver'
Write-NodeCmdWrapper -Name 'graphql-lsp.cmd' -Target 'graphql-language-service-cli\bin\graphql.js'
Write-NodeCmdWrapper -Name 'prisma-language-server.cmd' -Target '@prisma\language-server\dist\bin.js'
Write-NodeCmdWrapper -Name 'bash-language-server.cmd' -Target 'bash-language-server\out\cli.js'

$shellcheck = Resolve-ShellcheckExecutable
if ($shellcheck.Trim() -ne '') {
    Copy-Item -LiteralPath $shellcheck -Destination (Join-Path $LspDir 'bin/shellcheck.exe') -Force
}
$sg = Join-Path $LspDir 'node_modules/@ast-grep/cli/sg.exe'
Require-File -Path $sg -Message "missing ast-grep sg executable: $sg"
Assert-WindowsNativeArchitecture -Path $sg -ExpectedArch $WindowsPackageArch -Label 'ast-grep sg'
Copy-Item -LiteralPath $sg -Destination (Join-Path $LspDir 'bin/sg.exe') -Force
$astGrep = Join-Path $LspDir 'node_modules/@ast-grep/cli/ast-grep.exe'
Require-File -Path $astGrep -Message "missing ast-grep executable: $astGrep"
Assert-WindowsNativeArchitecture -Path $astGrep -ExpectedArch $WindowsPackageArch -Label 'ast-grep'
Copy-Item -LiteralPath $astGrep -Destination (Join-Path $LspDir 'bin/ast-grep.exe') -Force
$MSVCRuntimeDir = Resolve-WindowsVCLibsDesktopDirectory
foreach ($runtimeDLLName in @('concrt140.dll', 'msvcp140.dll', 'msvcp140_1.dll', 'msvcp140_2.dll', 'msvcp140_atomic_wait.dll', 'msvcp140_codecvt_ids.dll', 'vcruntime140.dll')) {
    Copy-Item -LiteralPath (Join-Path $MSVCRuntimeDir $runtimeDLLName) -Destination (Join-Path $LspDir "bin/$runtimeDLLName") -Force
}
& (Join-Path $LspDir 'bin/sg.exe') --help *> $null
if ($LASTEXITCODE -ne 0) { throw 'bundled ast-grep smoke failed; verify sg.exe, ast-grep.exe, and the app-local Windows VCLibs DLLs match the native package architecture' }

Write-Host '==> copying native LSP servers'
Require-File -Path $GoplsBin -Message 'missing gopls; set SUPER_DOLPHIN_GOPLS_BIN'
Require-File -Path $ClangdBin -Message 'missing clangd; set SUPER_DOLPHIN_CLANGD_BIN'
Require-File -Path $RustAnalyzerBin -Message 'missing rust-analyzer; set SUPER_DOLPHIN_RUST_ANALYZER_BIN'
Require-File -Path $SqruffBin -Message 'missing sqruff; set SUPER_DOLPHIN_SQRUFF_BIN'
Assert-WindowsNativeArchitecture -Path $GoplsBin -ExpectedArch $WindowsPackageArch -Label 'gopls'
Assert-WindowsNativeArchitecture -Path $ClangdBin -ExpectedArch $WindowsPackageArch -Label 'clangd'
Assert-WindowsNativeArchitecture -Path $RustAnalyzerBin -ExpectedArch $WindowsPackageArch -Label 'rust-analyzer'
Assert-WindowsNativeArchitecture -Path $SqruffBin -ExpectedArch $WindowsPackageArch -Label 'sqruff'
Copy-Item -LiteralPath $GoplsBin -Destination (Join-Path $LspDir 'bin/gopls.exe') -Force
Copy-Item -LiteralPath $ClangdBin -Destination (Join-Path $LspDir 'bin/clangd.exe') -Force
Copy-Item -LiteralPath $RustAnalyzerBin -Destination (Join-Path $LspDir 'bin/rust-analyzer.exe') -Force
Copy-Item -LiteralPath $SqruffBin -Destination (Join-Path $LspDir 'bin/sqruff.exe') -Force
& (Join-Path $LspDir 'bin/clangd.exe') --version *> $null
if ($LASTEXITCODE -ne 0) { throw 'bundled clangd failed --version smoke' }

Require-File -Path (Join-Path $GoToolchainSrc 'bin/go.exe') -Message "missing Go toolchain: $(Join-Path $GoToolchainSrc 'bin/go.exe')"
Assert-WindowsNativeArchitecture -Path (Join-Path $GoToolchainSrc 'bin/go.exe') -ExpectedArch $WindowsPackageArch -Label 'Go toolchain'
Copy-DirectoryClean -Source $GoToolchainSrc -Destination (Join-Path $LspDir 'go')
$goObj = Join-Path $LspDir 'go/pkg/obj'
if (Test-Path -LiteralPath $goObj) { Remove-Item -LiteralPath $goObj -Recurse -Force }
Write-GoToolchainWrapper

if ($LSPProfile -eq 'full') {
    Write-Host '==> copying Java runtime and jdtls'
    if ($JDTLSHome.Trim() -eq '') { throw 'missing jdtls; set SUPER_DOLPHIN_JDTLS_HOME' }
    if ($JDKHome.Trim() -eq '') { throw 'missing JDK; set SUPER_DOLPHIN_JDK_HOME or JAVA_HOME' }
    Require-File -Path (Join-Path $JDKHome 'bin/java.exe') -Message "missing JDK java: $(Join-Path $JDKHome 'bin/java.exe')"
    Assert-WindowsNativeArchitecture -Path (Join-Path $JDKHome 'bin/java.exe') -ExpectedArch $WindowsPackageArch -Label 'JDK java'
    Copy-DirectoryClean -Source $JDTLSHome -Destination (Join-Path $LspDir 'jdtls')
    Copy-DirectoryClean -Source $JDKHome -Destination (Join-Path $LspDir 'jdk')
    Write-JavaRuntimeWrapper
    Write-JDTLSWrapper
}

Prune-LSPBundleRuntimeOnlyArtifacts
Assert-LSPBundleNativeArchitecture -BundleDir $LspDir

Write-Host '==> writing LSP manifest and checksums'
Write-LSPManifestAndChecksums

foreach ($line in Get-Content -LiteralPath (Join-Path $LspDir 'lsp-checksums.sha256')) {
    if ($line.Trim() -eq '') { continue }
    if ($line -notmatch '^([0-9A-Fa-f]{64})\s+(.+)$') { throw "invalid checksum line: $line" }
    $expected = $Matches[1].ToLowerInvariant()
    $relPath = $Matches[2]
    $actual = Get-SHA256File (Join-Path $LspDir $relPath)
    if ($actual -ne $expected) { throw "checksum mismatch for $relPath" }
}

Write-Host "==> Windows LSP bundle ready: $LspDir"
