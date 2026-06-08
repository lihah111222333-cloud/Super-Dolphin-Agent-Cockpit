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
if ($GoOS -ne 'windows') {
    throw "prepare_lsp_bundle_windows.ps1 must run on Windows; current GOOS=$GoOS"
}

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
$RustAnalyzerBin = if ($env:SUPER_DOLPHIN_RUST_ANALYZER_BIN) { $env:SUPER_DOLPHIN_RUST_ANALYZER_BIN } else {
    $cmd = Get-Command rust-analyzer.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { $cmd.Source } else { '' }
}
$MSVCRuntimeDir = if ($env:SUPER_DOLPHIN_MSVC_RUNTIME_DIR) { $env:SUPER_DOLPHIN_MSVC_RUNTIME_DIR } else { Join-Path $env:WINDIR 'System32' }
$ShellcheckBin = if ($env:SUPER_DOLPHIN_SHELLCHECK_BIN) { $env:SUPER_DOLPHIN_SHELLCHECK_BIN } else { '' }
$OmitShellcheck = [Environment]::GetEnvironmentVariable('SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK', 'Process') -eq '1'
$JDTLSHome = if ($env:SUPER_DOLPHIN_JDTLS_HOME) { $env:SUPER_DOLPHIN_JDTLS_HOME } else { '' }
$JDKHome = if ($env:SUPER_DOLPHIN_JDK_HOME) { $env:SUPER_DOLPHIN_JDK_HOME } elseif ($env:JAVA_HOME) { $env:JAVA_HOME } else { '' }
$LSPNpmPackages = @(
    'typescript-language-server@5.3.0',
    'typescript@6.0.3',
    'vscode-langservers-extracted@4.10.0',
    'pyright@1.1.410',
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
        'bin/rust-analyzer.exe',
        'bin/sg.exe',
        'bin/ast-grep.exe',
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
        'gopls|bin/gopls.exe|["go","gomod","gosum","gowork"]',
        'typescript-language-server|bin/typescript-language-server.cmd|["javascript","javascriptreact","typescript","typescriptreact"]',
        'vscode-langservers-extracted|bin/vscode-css-language-server.cmd|["css"]',
        'pyright|bin/pyright-langserver.cmd|["python"]',
        'rust-analyzer|bin/rust-analyzer.exe|["rust"]',
        'bash-language-server|bin/bash-language-server.cmd|["shellscript"]',
        'sg|bin/sg.exe|["ast-grep"]',
        'go|bin/go.cmd|["go-toolchain"]'
    )
    if (-not $OmitShellcheck) {
        $specs += 'shellcheck|bin/shellcheck.exe|["shellcheck"]'
    }
    if ($LSPProfile -eq 'full') {
        $specs += 'java|bin/java.cmd|["java-runtime"]'
        $specs += 'jdtls|bin/jdtls.cmd|["java"]'
    }
    $helperPaths = @(
        'bin/ast-grep.exe',
        'bin/vcruntime140.dll'
    )
    $servers = [ordered]@{}
    $checksumLines = New-Object System.Collections.Generic.List[string]
    foreach ($spec in $specs) {
        $parts = $spec.Split('|')
        $serverId = $parts[0]
        $relPath = $parts[1]
        $languages = [string[]](ConvertFrom-Json -InputObject $parts[2])
        if ($languages.Count -eq 0) { throw "LSP manifest languages for $serverId must be a non-empty JSON array" }
        $fullPath = Join-Path $LspDir $relPath
        Require-File -Path $fullPath -Message "missing LSP manifest executable: $fullPath"
        $digest = Get-SHA256File $fullPath
        $servers[$serverId] = [ordered]@{
            path = $relPath
            version = 'bundled'
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
    & $NpmBin install --prefix $LspDir @LSPNpmPackages
    if ($LASTEXITCODE -ne 0) { throw 'npm install --prefix $LspDir failed' }
} finally {
    $env:Path = $oldPath
}

Write-NoSystemPythonStub -Name 'python.cmd'
Write-NoSystemPythonStub -Name 'python3.cmd'
Write-NodeCmdWrapper -Name 'typescript-language-server.cmd' -Target 'typescript-language-server\lib\cli.mjs'
Write-NodeCmdWrapper -Name 'vscode-css-language-server.cmd' -Target 'vscode-langservers-extracted\bin\vscode-css-language-server'
Write-NodeCmdWrapper -Name 'pyright-langserver.cmd' -Target 'pyright\langserver.index.js'
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
$vcruntime140 = Join-Path $MSVCRuntimeDir 'vcruntime140.dll'
Require-File -Path $vcruntime140 -Message 'missing MSVC runtime vcruntime140.dll; install the Microsoft Visual C++ Redistributable on the Windows packaging host or set SUPER_DOLPHIN_MSVC_RUNTIME_DIR'
Assert-WindowsNativeArchitecture -Path $vcruntime140 -ExpectedArch $WindowsPackageArch -Label 'MSVC runtime vcruntime140.dll'
Copy-Item -LiteralPath $vcruntime140 -Destination (Join-Path $LspDir 'bin/vcruntime140.dll') -Force
& (Join-Path $LspDir 'bin/sg.exe') --help *> $null
if ($LASTEXITCODE -ne 0) { throw 'bundled ast-grep smoke failed; verify sg.exe, ast-grep.exe, and vcruntime140.dll are compatible with the Windows package architecture' }

Write-Host '==> copying native LSP servers'
Require-File -Path $GoplsBin -Message 'missing gopls; set SUPER_DOLPHIN_GOPLS_BIN'
Require-File -Path $RustAnalyzerBin -Message 'missing rust-analyzer; set SUPER_DOLPHIN_RUST_ANALYZER_BIN'
Assert-WindowsNativeArchitecture -Path $GoplsBin -ExpectedArch $WindowsPackageArch -Label 'gopls'
Assert-WindowsNativeArchitecture -Path $RustAnalyzerBin -ExpectedArch $WindowsPackageArch -Label 'rust-analyzer'
Copy-Item -LiteralPath $GoplsBin -Destination (Join-Path $LspDir 'bin/gopls.exe') -Force
Copy-Item -LiteralPath $RustAnalyzerBin -Destination (Join-Path $LspDir 'bin/rust-analyzer.exe') -Force

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
