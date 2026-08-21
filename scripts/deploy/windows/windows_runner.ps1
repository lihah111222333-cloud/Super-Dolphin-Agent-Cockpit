[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$RepoPath,
    [string]$Command,
    [string]$CommandBase64,
    [Parameter(Mandatory = $true)][Alias('RunnerId')][string]$NodeId,
    [string]$RepositoryId,
    [string]$RepositoryRoot,
    [Parameter(Mandatory = $true)][string]$ObjectStoreRoot,
    [Parameter(Mandatory = $true)][string]$WorktreeRoot,
    [Parameter(Mandatory = $true)][string]$CacheRoot,
    [Parameter(Mandatory = $true)][string]$ToolchainIdentity,
    [Parameter(Mandatory = $true)][string]$GOOS,
    [Parameter(Mandatory = $true)][string]$GOARCH,
    [Parameter(Mandatory = $true)][ValidateSet('0', '1')][string]$CGOEnabled,
    [Parameter(Mandatory = $true)][string]$ModuleIdentity,
    [Parameter(Mandatory = $true)][string]$WorkloadIdentity,
    [Parameter(Mandatory = $true)][string]$SourceCommit,
    [Parameter(Mandatory = $true)][string]$SourceTree,
    [int]$TimeoutSeconds = 120
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Stop-Fast([string]$Message) { throw "windows_runner: $Message" }

function Assert-IdentityField {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ([string]::IsNullOrWhiteSpace($Value)) { Stop-Fast "$Label must be explicit and non-empty" }
    if ($Value.Length -gt 512) { Stop-Fast "$Label is too long" }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) { Stop-Fast "$Label contains a control character" }
    }
    return $Value
}

function ConvertTo-Sha256Identity {
    param([Parameter(Mandatory = $true)][string]$Value)

    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($Value)
        $digest = $sha256.ComputeHash($bytes)
    }
    finally {
        $sha256.Dispose()
    }
    return 'sha256:' + ([BitConverter]::ToString($digest).Replace('-', '').ToLowerInvariant())
}

function Assert-NoReparsePoints {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [switch]$AllowMissing
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $root = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::IsNullOrWhiteSpace($root)) { Stop-Fast 'path has no filesystem root' }
    $rootItem = Get-Item -LiteralPath $root -Force -ErrorAction Stop
    if (($rootItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        Stop-Fast "path contains a reparse point: $Path"
    }

    $current = $root
    $relative = $fullPath.Substring($root.Length)
    foreach ($segment in ($relative -split '[\\/]+')) {
        if ([string]::IsNullOrEmpty($segment)) { continue }
        $current = [System.IO.Path]::Combine($current, $segment)
        $item = Get-Item -LiteralPath $current -Force -ErrorAction SilentlyContinue
        if ($null -eq $item) {
            if ($AllowMissing) { return }
            Stop-Fast "path does not exist: $Path"
        }
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Stop-Fast "path contains a reparse point: $Path"
        }
    }
}

function Resolve-ExplicitDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$Create
    )

    if ([string]::IsNullOrWhiteSpace($Path)) { Stop-Fast "$Label must be explicit" }
    foreach ($character in $Path.ToCharArray()) {
        if ([char]::IsControl($character)) { Stop-Fast "$Label contains a control character" }
    }
    if (-not [System.IO.Path]::IsPathRooted($Path)) { Stop-Fast "$Label must be absolute" }
    try {
        $fullPath = [System.IO.Path]::GetFullPath($Path)
    }
    catch {
        Stop-Fast "$Label must be a valid absolute path"
    }
    $root = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::IsNullOrWhiteSpace($root) -or $fullPath.TrimEnd('\', '/') -ieq $root.TrimEnd('\', '/')) {
        Stop-Fast "$Label must not be a filesystem root"
    }

    if (-not (Test-Path -LiteralPath $fullPath -PathType Container)) {
        if (-not $Create) { Stop-Fast "$Label directory does not exist" }
        Assert-NoReparsePoints -Path $fullPath -AllowMissing
        New-Item -ItemType Directory -Path $fullPath -Force -ErrorAction Stop | Out-Null
    }
    $item = Get-Item -LiteralPath $fullPath -ErrorAction Stop
    if ($item.PSProvider.Name -ne 'FileSystem' -or -not ($item -is [System.IO.DirectoryInfo])) {
        Stop-Fast "$Label must be a filesystem directory"
    }
    Assert-NoReparsePoints -Path $fullPath
    return [System.IO.Path]::GetFullPath([string]$item.FullName)
}

function Assert-NodeLocalFixedPath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ($Path.StartsWith('\\', [System.StringComparison]::Ordinal)) {
        Stop-Fast "$Label must not use a UNC or network-share path"
    }
    $root = [System.IO.Path]::GetPathRoot($Path)
    if ($root -notmatch '^[A-Za-z]:\\$') { Stop-Fast "$Label must belong to one explicit local drive" }
    $drive = [System.IO.DriveInfo]::new($root)
    if (-not $drive.IsReady -or $drive.DriveType -ne [System.IO.DriveType]::Fixed) {
        Stop-Fast "$Label must belong to a ready fixed local drive"
    }
}

function Assert-RootsDisjoint {
    param([Parameter(Mandatory = $true)][string[]]$Roots)

    for ($index = 0; $index -lt $Roots.Count; $index++) {
        for ($other = $index + 1; $other -lt $Roots.Count; $other++) {
            $left = $Roots[$index].TrimEnd('\', '/') + '\'
            $right = $Roots[$other].TrimEnd('\', '/') + '\'
            if ($left.StartsWith($right, [System.StringComparison]::OrdinalIgnoreCase) -or
                $right.StartsWith($left, [System.StringComparison]::OrdinalIgnoreCase)) {
                Stop-Fast 'repository object-store, worktree, and cache roots must be disjoint'
            }
        }
    }
}

function Get-GitValue {
    param(
        [Parameter(Mandatory = $true)][string]$GitExecutable,
        [Parameter(Mandatory = $true)][string]$Worktree,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $output = (& $GitExecutable -C $Worktree @Arguments 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($output) -or $output.Contains("`n") -or $output.Contains("`r")) {
        Stop-Fast "$Label could not be uniquely verified from the explicit Git root"
    }
    return $output
}

function Assert-GitObjectId {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$ObjectFormat,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $pattern = if ($ObjectFormat -ceq 'sha1') { '^[a-f0-9]{40}$' } elseif ($ObjectFormat -ceq 'sha256') { '^[a-f0-9]{64}$' } else { Stop-Fast 'Git object format is unsupported' }
    if ($Value -cnotmatch $pattern) { Stop-Fast "$Label does not match the Git object format" }
}

function Get-CacheManifest {
    param(
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Expected
    )

    $manifestExists = Test-Path -LiteralPath $ManifestPath -PathType Any -ErrorAction SilentlyContinue
    if ($manifestExists -and -not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) {
        Stop-Fast 'cache manifest path is not a regular file'
    }
    if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) { return $false }
    Assert-NoReparsePoints -Path $ManifestPath
    try {
        $manifest = [string]::new([System.IO.File]::ReadAllText($ManifestPath)) | ConvertFrom-Json
    }
    catch {
        Stop-Fast 'cache manifest is not valid JSON'
    }
    $expectedNames = @($Expected.Keys | Sort-Object)
    $actualNames = @($manifest.PSObject.Properties.Name | Sort-Object)
    if (($actualNames -join "`n") -cne ($expectedNames -join "`n")) {
        Stop-Fast 'cache manifest fields are not exact'
    }
    foreach ($name in $expectedNames) {
        if ([string]$manifest.$name -cne [string]$Expected[$name]) {
            Stop-Fast "cache manifest identity drift detected for $name"
        }
    }
    return $true
}

function Write-CacheManifest {
    param(
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Manifest
    )

    $json = $Manifest | ConvertTo-Json -Compress -Depth 3
    $temporary = "$ManifestPath.$PID.tmp"
    try {
        [System.IO.File]::WriteAllText($temporary, $json, [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $temporary -Destination $ManifestPath -Force -ErrorAction Stop
    }
    catch {
        if (Test-Path -LiteralPath $temporary -PathType Any) { Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue }
        Stop-Fast 'cache manifest atomic write failed'
    }
}

function Get-NodeExecutorMutexName {
    param([Parameter(Mandatory = $true)][string]$NodeIdentity)

    $nodeDigest = (ConvertTo-Sha256Identity -Value $NodeIdentity).Substring(7)
    $mutexName = 'Global\SuperDolphin-Heteroci-NodeExecutor-v2-' + $nodeDigest
    if ($mutexName.Length -gt 260) { Stop-Fast 'node executor mutex name exceeds the Windows limit' }
    return $mutexName
}
if ($env:OS -ne 'Windows_NT') { Stop-Fast 'runner only runs on Windows' }
if (-not [System.IO.Path]::IsPathRooted($RepoPath)) { Stop-Fast '-RepoPath must be absolute' }
if (-not (Test-Path -LiteralPath $RepoPath -PathType Container)) { Stop-Fast "repository does not exist: $RepoPath" }
if ($TimeoutSeconds -le 0 -or $TimeoutSeconds -gt 120) { Stop-Fast 'TimeoutSeconds must be in 1..120' }
if ([string]::IsNullOrWhiteSpace($Command) -eq [string]::IsNullOrWhiteSpace($CommandBase64)) {
    Stop-Fast 'exactly one of -Command or -CommandBase64 is required'
}
if (-not [string]::IsNullOrWhiteSpace($CommandBase64)) {
    try {
        $Command = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($CommandBase64))
    }
    catch {
        Stop-Fast 'CommandBase64 is not valid UTF-16LE base64'
    }
}
if ([string]::IsNullOrWhiteSpace($Command)) { Stop-Fast 'decoded command must not be empty or whitespace' }

[void](Assert-IdentityField -Value $NodeId -Label 'NodeId')
[void](Assert-IdentityField -Value $ToolchainIdentity -Label 'ToolchainIdentity')
[void](Assert-IdentityField -Value $GOOS -Label 'GOOS')
[void](Assert-IdentityField -Value $GOARCH -Label 'GOARCH')
[void](Assert-IdentityField -Value $ModuleIdentity -Label 'ModuleIdentity')
[void](Assert-IdentityField -Value $WorkloadIdentity -Label 'WorkloadIdentity')
[void](Assert-IdentityField -Value $SourceCommit -Label 'SourceCommit')
[void](Assert-IdentityField -Value $SourceTree -Label 'SourceTree')

$RepoPath = Resolve-ExplicitDirectory -Path $RepoPath -Label 'RepoPath'
$git = Get-Command git.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if ($null -eq $git) { $git = Get-Command git -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1 }
if ($null -eq $git) { Stop-Fast 'git executable is required to verify the explicit repository root' }
$gitExecutable = [string]$git.Source
$gitRootRaw = Get-GitValue -GitExecutable $gitExecutable -Worktree $RepoPath -Arguments @('rev-parse', '--show-toplevel') -Label 'Git worktree root'
$gitRootCanonical = Resolve-ExplicitDirectory -Path $gitRootRaw -Label 'Git worktree root'
if (-not [string]::IsNullOrWhiteSpace($RepositoryRoot)) {
    $RepositoryRoot = Resolve-ExplicitDirectory -Path $RepositoryRoot -Label 'RepositoryRoot'
    if ($RepositoryRoot.TrimEnd('\', '/') -ine $gitRootCanonical.TrimEnd('\', '/')) {
        Stop-Fast 'RepositoryRoot does not match the uniquely verified Git worktree root'
    }
}
else {
    $RepositoryRoot = $gitRootCanonical
}
$gitCommonRaw = Get-GitValue -GitExecutable $gitExecutable -Worktree $RepoPath -Arguments @('rev-parse', '--git-common-dir') -Label 'Git common directory'
if (-not [System.IO.Path]::IsPathRooted($gitCommonRaw)) {
    $gitCommonRaw = Join-Path $RepoPath $gitCommonRaw
}
$gitCommonRoot = Resolve-ExplicitDirectory -Path $gitCommonRaw -Label 'Git common directory'
$objectFormat = Get-GitValue -GitExecutable $gitExecutable -Worktree $RepoPath -Arguments @('rev-parse', '--show-object-format') -Label 'Git object format'
Assert-GitObjectId -Value $SourceCommit -ObjectFormat $objectFormat -Label 'SourceCommit'
Assert-GitObjectId -Value $SourceTree -ObjectFormat $objectFormat -Label 'SourceTree'
$worktreeStatus = (& $gitExecutable -C $RepoPath status --porcelain=v1 --untracked-files=all --ignored=matching 2>$null | Out-String).Trim()
if ($LASTEXITCODE -ne 0) { Stop-Fast 'Git worktree cleanliness could not be verified before exact commit materialization' }
if (-not [string]::IsNullOrWhiteSpace($worktreeStatus)) { Stop-Fast 'Git worktree contains drift before exact commit materialization' }
$actualCommit = Get-GitValue -GitExecutable $gitExecutable -Worktree $RepoPath -Arguments @('rev-parse', 'HEAD') -Label 'Git source commit'
if ($actualCommit -cne $SourceCommit) {
    $checkoutErrorAction = $ErrorActionPreference
    $checkoutExitCode = -1
    try {
        $ErrorActionPreference = 'Continue'
        & $gitExecutable -c 'advice.detachedHead=false' -C $RepoPath checkout --detach $SourceCommit 2>$null | Out-Null
        $checkoutExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $checkoutErrorAction
    }
    if ($checkoutExitCode -ne 0) { Stop-Fast 'Exact source commit could not be materialized' }
    $actualCommit = Get-GitValue -GitExecutable $gitExecutable -Worktree $RepoPath -Arguments @('rev-parse', 'HEAD') -Label 'materialized Git source commit'
}
$actualTree = Get-GitValue -GitExecutable $gitExecutable -Worktree $RepoPath -Arguments @('rev-parse', 'HEAD^{tree}') -Label 'Git source tree'
if ($actualCommit -cne $SourceCommit) { Stop-Fast 'SourceCommit does not match the explicit Git worktree HEAD' }
if ($actualTree -cne $SourceTree) { Stop-Fast 'SourceTree does not match the explicit Git worktree tree' }
$worktreeStatus = (& $gitExecutable -C $RepoPath status --porcelain=v1 --untracked-files=all --ignored=matching 2>$null | Out-String).Trim()
if ($LASTEXITCODE -ne 0) { Stop-Fast 'Git worktree cleanliness could not be verified' }
if (-not [string]::IsNullOrWhiteSpace($worktreeStatus)) { Stop-Fast 'Git worktree contains tracked, untracked, or ignored drift' }
if ([string]::IsNullOrWhiteSpace($RepositoryId)) {
    $RepositoryId = 'git-common-dir/' + (ConvertTo-Sha256Identity -Value ($objectFormat + '|' + $gitCommonRoot)).Substring(7)
}
[void](Assert-IdentityField -Value $RepositoryId -Label 'RepositoryId')

$ObjectStoreRoot = Resolve-ExplicitDirectory -Path $ObjectStoreRoot -Label 'ObjectStoreRoot'
$WorktreeRoot = Resolve-ExplicitDirectory -Path $WorktreeRoot -Label 'WorktreeRoot'
$CacheRoot = Resolve-ExplicitDirectory -Path $CacheRoot -Label 'CacheRoot' -Create
[void](Assert-NodeLocalFixedPath -Path $ObjectStoreRoot -Label 'ObjectStoreRoot')
[void](Assert-NodeLocalFixedPath -Path $WorktreeRoot -Label 'WorktreeRoot')
[void](Assert-NodeLocalFixedPath -Path $CacheRoot -Label 'CacheRoot')
$nodeObjectStoreRoot = $ObjectStoreRoot
$nodeWorktreeRoot = $WorktreeRoot
if ($nodeWorktreeRoot.TrimEnd('\', '/') -ine $gitRootCanonical.TrimEnd('\', '/')) {
    Stop-Fast 'WorktreeRoot must equal the uniquely verified Git worktree root'
}
if ($nodeObjectStoreRoot.TrimEnd('\', '/') -ine $gitCommonRoot.TrimEnd('\', '/')) {
    Stop-Fast 'ObjectStoreRoot must equal the uniquely verified Git common directory'
}
$nodeCacheRoot = $CacheRoot
Assert-RootsDisjoint -Roots @($nodeObjectStoreRoot, $nodeCacheRoot)
Assert-RootsDisjoint -Roots @($nodeWorktreeRoot, $nodeCacheRoot)

$cacheIdentityMaterial = [ordered]@{
    schema_version = 'windows-node-local-cache/v1'
    cache_scope = 'windows_node_local'
    node_id = $NodeId
    repository_id = $RepositoryId
    object_format = $objectFormat
    toolchain_identity = $ToolchainIdentity
    goos = $GOOS
    goarch = $GOARCH
    cgo_enabled = $CGOEnabled
    module_identity = $ModuleIdentity
}
$cacheIdentityJson = [string]($cacheIdentityMaterial | ConvertTo-Json -Compress -Depth 3)
$cacheIdentity = ConvertTo-Sha256Identity -Value $cacheIdentityJson
$cacheIdentityRoot = Resolve-ExplicitDirectory -Path (Join-Path $nodeCacheRoot ('i-' + $cacheIdentity.Substring(7))) -Label 'cache identity root' -Create
$goModCacheRoot = Resolve-ExplicitDirectory -Path (Join-Path $cacheIdentityRoot 'gomodcache') -Label 'GOMODCACHE root' -Create
$goCacheRoot = Resolve-ExplicitDirectory -Path (Join-Path $cacheIdentityRoot 'gocache') -Label 'GOCACHE root' -Create
$goTmpRoot = Resolve-ExplicitDirectory -Path (Join-Path $cacheIdentityRoot 'gotmp') -Label 'GOTMPDIR root' -Create
$cacheManifestPath = Join-Path $cacheIdentityRoot 'cache-manifest.json'
$cacheManifest = [ordered]@{
    schema_version = 'windows-node-local-cache/v1'
    cache_scope = 'windows_node_local'
    node_id = $NodeId
    repository_id = $RepositoryId
    identity = $cacheIdentity
    toolchain_identity = $ToolchainIdentity
    goos = $GOOS
    goarch = $GOARCH
    cgo_enabled = $CGOEnabled
    module_identity = $ModuleIdentity
}
$cacheReady = Get-CacheManifest -ManifestPath $cacheManifestPath -Expected $cacheManifest
if ($cacheReady -and (-not (Test-Path -LiteralPath $goModCacheRoot -PathType Container) -or
        -not (Test-Path -LiteralPath $goCacheRoot -PathType Container) -or
        -not (Test-Path -LiteralPath $goTmpRoot -PathType Container))) {
    Stop-Fast 'cache manifest is ready but one or more cache directories are missing'
}
$cacheSeedState = if ($cacheReady) { 'ready' } else { 'cold' }

function ConvertTo-StrictPositiveUInt64 {
    param(
        [Parameter(Mandatory = $true)][object]$Value,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ($null -eq $Value) { Stop-Fast "$Label is missing" }
    $text = [string]$Value
    if ($text -notmatch '^[1-9][0-9]*$') {
        Stop-Fast "$Label must be a positive integer"
    }
    try {
        $parsed = [uint64]$text
    }
    catch {
        Stop-Fast "$Label is outside the uint64 range"
    }
    if ($parsed -le 0 -or $parsed -gt [uint64][long]::MaxValue) {
        Stop-Fast "$Label is outside the supported positive range"
    }
    return $parsed
}

$computerRecords = @(Get-CimInstance -ClassName Win32_ComputerSystem -Property NumberOfLogicalProcessors,TotalPhysicalMemory -ErrorAction Stop)
if ($computerRecords.Count -ne 1) {
    Stop-Fast 'Win32_ComputerSystem probe must return exactly one record'
}
$computer = $computerRecords[0]
$logicalProcessorsProperty = $computer.PSObject.Properties['NumberOfLogicalProcessors']
$physicalMemoryProperty = $computer.PSObject.Properties['TotalPhysicalMemory']
if ($null -eq $logicalProcessorsProperty -or $null -eq $physicalMemoryProperty) {
    Stop-Fast 'Win32_ComputerSystem probe omitted a required total resource fact'
}
$totalLogicalProcessors = ConvertTo-StrictPositiveUInt64 -Value $logicalProcessorsProperty.Value -Label 'Win32_ComputerSystem.NumberOfLogicalProcessors'
$totalPhysicalMemoryBytes = ConvertTo-StrictPositiveUInt64 -Value $physicalMemoryProperty.Value -Label 'Win32_ComputerSystem.TotalPhysicalMemory'

# Local Windows execution is useful for development probing only.  It cannot
# produce remote CI PASS/evidence, so make the boundary visible to the child
# process and to the single JSON result without adding command-line parameters.
$env:LOCAL_NON_AUTHORITATIVE = '1'
$env:NOT_VERIFIED = '1'
$env:SUPER_DOLPHIN_AUTHORITY = 'LOCAL_NON_AUTHORITATIVE'
$env:SUPER_DOLPHIN_RESULT_STATUS = 'NOT_VERIFIED'
$env:SUPER_DOLPHIN_RUNTIME = 'windows_native'
$env:SUPER_DOLPHIN_CACHE_SCOPE = 'windows_node_local'
$env:SUPER_DOLPHIN_NODE_ID = $NodeId
$env:SUPER_DOLPHIN_REPOSITORY_ID = $RepositoryId
$env:SUPER_DOLPHIN_CACHE_IDENTITY = $cacheIdentity
$env:SUPER_DOLPHIN_CACHE_SEED_STATE = $cacheSeedState
$env:SUPER_DOLPHIN_WORKLOAD_IDENTITY = $WorkloadIdentity
$env:SUPER_DOLPHIN_OBJECT_STORE_ROOT = $nodeObjectStoreRoot
$env:SUPER_DOLPHIN_WORKTREE_ROOT = $nodeWorktreeRoot
$env:SUPER_DOLPHIN_CACHE_ROOT = $nodeCacheRoot
$env:SUPER_DOLPHIN_GATE_GIT = $gitExecutable
$env:GOMODCACHE = $goModCacheRoot
$env:GOCACHE = $goCacheRoot
$env:GOTMPDIR = $goTmpRoot
$env:GOOS = $GOOS
$env:GOARCH = $GOARCH
$env:CGO_ENABLED = $CGOEnabled

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;

public static class SuperDolphinWindows {
  public const uint CREATE_SUSPENDED = 0x00000004;
  const uint CREATE_UNICODE_ENVIRONMENT = 0x00000400;
  const uint CREATE_NO_WINDOW = 0x08000000;
  const int STARTF_USESTDHANDLES = 0x00000100;
  const int STD_INPUT_HANDLE = -10;
  const int STD_OUTPUT_HANDLE = -11;
  const int STD_ERROR_HANDLE = -12;
  const uint WAIT_OBJECT_0 = 0x00000000;
  const uint WAIT_TIMEOUT = 0x00000102;
  const uint WAIT_FAILED = 0xffffffff;
  const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000;
  const int JobObjectBasicAccountingInformation = 1;
  const int JobObjectExtendedLimitInformation = 9;

  [StructLayout(LayoutKind.Sequential)]
  struct JOBOBJECT_BASIC_ACCOUNTING_INFORMATION {
    public long TotalUserTime;
    public long TotalKernelTime;
    public long ThisPeriodTotalUserTime;
    public long ThisPeriodTotalKernelTime;
    public uint TotalPageFaultCount;
    public uint TotalProcesses;
    public uint ActiveProcesses;
    public uint TotalTerminatedProcesses;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct JOBOBJECT_BASIC_LIMIT_INFORMATION {
    public long PerProcessUserTimeLimit;
    public long PerJobUserTimeLimit;
    public uint LimitFlags;
    public UIntPtr MinimumWorkingSetSize;
    public UIntPtr MaximumWorkingSetSize;
    public uint ActiveProcessLimit;
    public UIntPtr Affinity;
    public uint PriorityClass;
    public uint SchedulingClass;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct IO_COUNTERS {
    public ulong ReadOperationCount;
    public ulong WriteOperationCount;
    public ulong OtherOperationCount;
    public ulong ReadTransferCount;
    public ulong WriteTransferCount;
    public ulong OtherTransferCount;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
    public JOBOBJECT_BASIC_LIMIT_INFORMATION BasicLimitInformation;
    public IO_COUNTERS IoInfo;
    public UIntPtr ProcessMemoryLimit;
    public UIntPtr JobMemoryLimit;
    public UIntPtr PeakProcessMemoryUsed;
    public UIntPtr PeakJobMemoryUsed;
  }

  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct STARTUPINFO {
    public int cb;
    public IntPtr lpReserved;
    public IntPtr lpDesktop;
    public IntPtr lpTitle;
    public int dwX;
    public int dwY;
    public int dwXSize;
    public int dwYSize;
    public int dwXCountChars;
    public int dwYCountChars;
    public int dwFillAttribute;
    public int dwFlags;
    public short wShowWindow;
    public short cbReserved2;
    public IntPtr lpReserved2;
    public IntPtr hStdInput;
    public IntPtr hStdOutput;
    public IntPtr hStdError;
  }

  [StructLayout(LayoutKind.Sequential)]
  struct PROCESS_INFORMATION {
    public IntPtr hProcess;
    public IntPtr hThread;
    public uint dwProcessId;
    public uint dwThreadId;
  }

  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern IntPtr CreateJobObjectW(IntPtr attributes, string name);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool SetInformationJobObject(
      IntPtr job,
      int infoClass,
      ref JOBOBJECT_EXTENDED_LIMIT_INFORMATION info,
      uint length);

  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern bool CreateProcessW(
      string applicationName,
      StringBuilder commandLine,
      IntPtr processAttributes,
      IntPtr threadAttributes,
      bool inheritHandles,
      uint creationFlags,
      IntPtr environment,
      string currentDirectory,
      ref STARTUPINFO startupInfo,
      out PROCESS_INFORMATION processInformation);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern IntPtr GetStdHandle(int standardHandle);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern uint ResumeThread(IntPtr thread);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool TerminateJobObject(IntPtr job, uint exitCode);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool TerminateProcess(IntPtr process, uint exitCode);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool GetExitCodeProcess(IntPtr process, out uint exitCode);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool QueryInformationJobObject(
      IntPtr job,
      int informationClass,
      out JOBOBJECT_BASIC_ACCOUNTING_INFORMATION information,
      uint informationLength,
      IntPtr returnLength);

  [DllImport("kernel32.dll", SetLastError = true)]
  static extern bool CloseHandle(IntPtr handle);

  public sealed class Launch {
    public IntPtr ProcessHandle { get; internal set; }
    public IntPtr ThreadHandle { get; internal set; }
    public uint ProcessId { get; internal set; }
  }

  public static IntPtr CreateJob() {
    IntPtr job = CreateJobObjectW(IntPtr.Zero, null);
    if (job == IntPtr.Zero) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateJobObjectW failed");
    }
    var info = new JOBOBJECT_EXTENDED_LIMIT_INFORMATION();
    info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if (!SetInformationJobObject(
        job,
        JobObjectExtendedLimitInformation,
        ref info,
        (uint)Marshal.SizeOf(typeof(JOBOBJECT_EXTENDED_LIMIT_INFORMATION)))) {
      int error = Marshal.GetLastWin32Error();
      bool closed = CloseHandle(job);
      if (!closed) {
        int closeError = Marshal.GetLastWin32Error();
        throw new AggregateException(
            "SetInformationJobObject failed and the Job handle could not be closed",
            new Win32Exception(error, "SetInformationJobObject failed"),
            new Win32Exception(closeError, "CloseHandle(Job Object) failed"));
      }
      throw new Win32Exception(error, "SetInformationJobObject failed");
    }
    return job;
  }

  public static string BuildPowerShellCommandLine(string executable, string command) {
    if (String.IsNullOrWhiteSpace(executable) || !System.IO.Path.IsPathRooted(executable)) {
      throw new ArgumentException("PowerShell executable is invalid", "executable");
    }
    if (command == null || command.Length == 0) {
      throw new ArgumentException("PowerShell command is empty", "command");
    }
    if (command.IndexOf('\0') >= 0) {
      throw new ArgumentException("PowerShell command contains a NUL character", "command");
    }
    return QuoteWindowsArgument(executable) + " -NoProfile -Command " + QuoteWindowsArgument(command);
  }

  // Quote one argument using the CommandLineToArgvW escaping rules.  This
  // preserves the existing -Command string semantics while using CreateProcessW.
  static string QuoteWindowsArgument(string argument) {
    var result = new StringBuilder(argument.Length + 2);
    result.Append('"');
    int backslashes = 0;
    foreach (char current in argument) {
      if (current == '\\') {
        backslashes++;
        continue;
      }
      if (current == '"') {
        result.Append('\\', backslashes * 2 + 1);
        result.Append('"');
        backslashes = 0;
        continue;
      }
      result.Append('\\', backslashes);
      result.Append(current);
      backslashes = 0;
    }
    result.Append('\\', backslashes * 2);
    result.Append('"');
    return result.ToString();
  }

  public static Launch CreateSuspended(string executable, string commandLine, string currentDirectory) {
    if (String.IsNullOrWhiteSpace(executable) || !System.IO.Path.IsPathRooted(executable)) {
      throw new ArgumentException("CreateProcessW executable is invalid", "executable");
    }
    if (String.IsNullOrWhiteSpace(commandLine)) {
      throw new ArgumentException("CreateProcessW command line is empty", "commandLine");
    }
    var startup = new STARTUPINFO();
    startup.cb = Marshal.SizeOf(typeof(STARTUPINFO));
    startup.dwFlags = STARTF_USESTDHANDLES;
    startup.hStdInput = RequiredStandardHandle(STD_INPUT_HANDLE, "stdin");
    startup.hStdOutput = RequiredStandardHandle(STD_OUTPUT_HANDLE, "stdout");
    startup.hStdError = RequiredStandardHandle(STD_ERROR_HANDLE, "stderr");
    var mutableCommandLine = new StringBuilder(commandLine);
    PROCESS_INFORMATION processInformation;
    bool created = CreateProcessW(
        executable,
        mutableCommandLine,
        IntPtr.Zero,
        IntPtr.Zero,
        true,
        CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT | CREATE_NO_WINDOW,
        IntPtr.Zero,
        currentDirectory,
        ref startup,
        out processInformation);
    if (!created) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateProcessW failed");
    }
    return new Launch {
      ProcessHandle = processInformation.hProcess,
      ThreadHandle = processInformation.hThread,
      ProcessId = processInformation.dwProcessId,
    };
  }

  static IntPtr RequiredStandardHandle(int identifier, string label) {
    IntPtr handle = GetStdHandle(identifier);
    if (handle == IntPtr.Zero || handle == new IntPtr(-1)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "GetStdHandle(" + label + ") failed");
    }
    return handle;
  }

  public static void Assign(IntPtr job, IntPtr process) {
    if (job == IntPtr.Zero || process == IntPtr.Zero) {
      throw new ArgumentException("Job and process handles are required");
    }
    if (!AssignProcessToJobObject(job, process)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "AssignProcessToJobObject failed");
    }
  }

  public static void Resume(IntPtr thread) {
    if (thread == IntPtr.Zero) {
      throw new ArgumentException("Process thread handle is required");
    }
    uint previousSuspendCount = ResumeThread(thread);
    if (previousSuspendCount == 0xffffffff) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "ResumeThread failed");
    }
    if (previousSuspendCount != 1) {
      throw new InvalidOperationException(
          "ResumeThread returned an unexpected previous suspend count: " + previousSuspendCount);
    }
  }

  public static bool WaitForProcess(IntPtr process, uint milliseconds) {
    return WaitForHandle(process, milliseconds, "process");
  }

  public static bool WaitForJobEmpty(IntPtr job, uint milliseconds) {
    if (job == IntPtr.Zero) {
      throw new ArgumentException("Job Object handle is required");
    }
    var stopwatch = Stopwatch.StartNew();
    while (true) {
      JOBOBJECT_BASIC_ACCOUNTING_INFORMATION information;
      if (!QueryInformationJobObject(
          job,
          JobObjectBasicAccountingInformation,
          out information,
          (uint)Marshal.SizeOf(typeof(JOBOBJECT_BASIC_ACCOUNTING_INFORMATION)),
          IntPtr.Zero)) {
        throw new Win32Exception(Marshal.GetLastWin32Error(), "QueryInformationJobObject failed");
      }
      if (information.ActiveProcesses == 0) {
        return true;
      }
      if (stopwatch.ElapsedMilliseconds >= milliseconds) {
        return false;
      }
      System.Threading.Thread.Sleep(10);
    }
  }

  static bool WaitForHandle(IntPtr handle, uint milliseconds, string description) {
    if (handle == IntPtr.Zero) {
      throw new ArgumentException(description + " handle is required");
    }
    uint result = WaitForSingleObject(handle, milliseconds);
    if (result == WAIT_OBJECT_0) {
      return true;
    }
    if (result == WAIT_TIMEOUT) {
      return false;
    }
    if (result == WAIT_FAILED) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "WaitForSingleObject(" + description + ") failed");
    }
    throw new InvalidOperationException("WaitForSingleObject(" + description + ") returned " + result);
  }

  public static uint GetExitCode(IntPtr process) {
    uint exitCode;
    if (!GetExitCodeProcess(process, out exitCode)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "GetExitCodeProcess failed");
    }
    return exitCode;
  }

  // Terminate through the Job when assignment succeeded.  Assignment failure
  // leaves the suspended root outside the Job, so terminate that exact process
  // handle as well.  Both the root and Job are waited before their handles close.
  public static void TerminateAndWait(IntPtr job, IntPtr process, bool assigned, uint milliseconds) {
    var errors = new List<Exception>();
    bool jobTerminationSucceeded = false;
    if (job != IntPtr.Zero) {
      try {
        if (!TerminateJobObject(job, 1)) {
          throw new Win32Exception(Marshal.GetLastWin32Error(), "TerminateJobObject failed");
        }
        jobTerminationSucceeded = true;
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    // A failed Job termination must not leave the suspended/root process
    // running.  TerminateProcess is also required when assignment failed.
    if (process != IntPtr.Zero && (!assigned || !jobTerminationSucceeded)) {
      try {
        if (!TerminateProcess(process, 1)) {
          int terminateError = Marshal.GetLastWin32Error();
          uint waitResult = WaitForSingleObject(process, 0);
          if (waitResult != WAIT_OBJECT_0) {
            throw new Win32Exception(terminateError, "TerminateProcess failed");
          }
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (process != IntPtr.Zero) {
      try {
        if (!WaitForHandle(process, milliseconds, "process")) {
          errors.Add(new TimeoutException("process did not terminate within the cleanup window"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (job != IntPtr.Zero) {
      try {
        if (!WaitForJobEmpty(job, milliseconds)) {
          errors.Add(new TimeoutException("Job Object did not become empty within the cleanup window"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (errors.Count != 0) {
      throw new AggregateException("process tree termination did not complete", errors);
    }
  }

  public static void CloseLaunch(Launch launch) {
    if (launch == null) {
      return;
    }
    var errors = new List<Exception>();
    IntPtr thread = launch.ThreadHandle;
    if (thread != IntPtr.Zero) {
      try {
        if (CloseHandle(thread)) {
          launch.ThreadHandle = IntPtr.Zero;
        } else {
          errors.Add(new Win32Exception(Marshal.GetLastWin32Error(), "CloseHandle(thread) failed"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    IntPtr process = launch.ProcessHandle;
    if (process != IntPtr.Zero) {
      try {
        if (CloseHandle(process)) {
          launch.ProcessHandle = IntPtr.Zero;
        } else {
          errors.Add(new Win32Exception(Marshal.GetLastWin32Error(), "CloseHandle(process) failed"));
        }
      }
      catch (Exception error) {
        errors.Add(error);
      }
    }
    if (errors.Count != 0) {
      throw new AggregateException("process handle cleanup failed", errors);
    }
  }

  public static IntPtr CloseJob(IntPtr job) {
    if (job == IntPtr.Zero) {
      return IntPtr.Zero;
    }
    if (!CloseHandle(job)) {
      throw new Win32Exception(Marshal.GetLastWin32Error(), "CloseHandle(Job Object) failed");
    }
    return IntPtr.Zero;
  }
}
'@

function New-RunnerAggregate([string]$Message, [object[]]$Errors) {
    $exceptions = @(
        foreach ($error in @($Errors)) {
            if ($null -eq $error) { continue }
            if ($error -is [System.Management.Automation.ErrorRecord]) {
                $error.Exception
            }
            elseif ($error -is [System.Exception]) {
                $error
            }
            else {
                [System.Exception]::new([string]$error)
            }
        }
    )
    if ($exceptions.Count -eq 0) {
        return [System.Exception]::new($Message)
    }
    return [System.AggregateException]::new($Message, [System.Exception[]]$exceptions)
}

function Close-Resources([ref]$Job, [object]$Launch) {
    $cleanupErrors = @()
    try {
        if ($null -ne $Launch) { [SuperDolphinWindows]::CloseLaunch($Launch) }
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    try {
        if ($Job.Value -ne [IntPtr]::Zero) {
            $Job.Value = [SuperDolphinWindows]::CloseJob($Job.Value)
        }
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    if ($cleanupErrors.Count -ne 0) {
        throw (New-RunnerAggregate -Message 'windows_runner: handle cleanup failed' -Errors $cleanupErrors)
    }
}

function Stop-And-Reap([ref]$Job, [object]$Launch, [bool]$Assigned) {
    $cleanupErrors = @()
    $processHandle = [IntPtr]::Zero
    if ($null -ne $Launch) { $processHandle = $Launch.ProcessHandle }
    try {
        [SuperDolphinWindows]::TerminateAndWait($Job.Value, $processHandle, $Assigned, 5000)
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    try {
        Close-Resources -Job $Job -Launch $Launch
    }
    catch {
        $cleanupErrors += $_.Exception
    }
    if ($cleanupErrors.Count -ne 0) {
        throw (New-RunnerAggregate -Message 'windows_runner: termination/reap cleanup failed' -Errors $cleanupErrors)
    }
}

$powerShellExecutable = [System.Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
if ([string]::IsNullOrWhiteSpace($powerShellExecutable) -or -not [System.IO.Path]::IsPathRooted($powerShellExecutable)) {
    Stop-Fast 'current PowerShell executable path is invalid'
}
$commandLine = [SuperDolphinWindows]::BuildPowerShellCommandLine($powerShellExecutable, $Command)
$job = [IntPtr]::Zero
$launch = $null
$assigned = $false
$processExited = $false
$cleanupDone = $false
$processExitCode = $null
$primaryError = $null
$cleanupError = $null
$mutexName = Get-NodeExecutorMutexName -NodeIdentity $NodeId
$mutex = $null
$mutexHeld = $false
$mutexWasAbandoned = $false
$mutexReleaseError = $null
$mutexDisposeError = $null
$mutexCleanupError = $null

try {
    try {
        $mutex = [System.Threading.Mutex]::new($false, $mutexName)
    }
    catch {
        Stop-Fast 'unable to create node executor mutex'
    }

    try {
        $mutexHeld = [bool]$mutex.WaitOne(0)
    }
    catch [System.Threading.AbandonedMutexException] {
        $mutexHeld = $true
        $mutexWasAbandoned = $true
        Write-Verbose 'node executor mutex was abandoned; continuing with the acquired lock'
    }
    catch {
        Stop-Fast 'unable to acquire node executor mutex'
    }
    if (-not $mutexHeld) { Stop-Fast 'another heteroci node executor is already running' }

    # The Job exists before CreateProcessW so no user code can run outside it.
    $job = [SuperDolphinWindows]::CreateJob()
    $launch = [SuperDolphinWindows]::CreateSuspended($powerShellExecutable, $commandLine, $RepoPath)
    [SuperDolphinWindows]::Assign($job, $launch.ProcessHandle)
    $assigned = $true
    [SuperDolphinWindows]::Resume($launch.ThreadHandle)
    if (-not [SuperDolphinWindows]::WaitForProcess($launch.ProcessHandle, [uint32]($TimeoutSeconds * 1000))) {
        Stop-Fast "command exceeded ${TimeoutSeconds}s watchdog"
    }
    $processExited = $true
    $processExitCode = [SuperDolphinWindows]::GetExitCode($launch.ProcessHandle)
    if (-not [SuperDolphinWindows]::WaitForJobEmpty($job, 5000)) {
        $processExited = $false
        Stop-Fast 'root process exited while Job Object descendants remained active'
    }
}
catch {
    $primaryError = $_.Exception
}
finally {
    if (-not $cleanupDone) {
        try {
            if ($processExited) {
                Close-Resources -Job ([ref]$job) -Launch $launch
            }
            else {
                Stop-And-Reap -Job ([ref]$job) -Launch $launch -Assigned $assigned
            }
            $cleanupDone = $true
        }
        catch {
            $firstCleanupError = $_.Exception
            try {
                if ($processExited) {
                    Close-Resources -Job ([ref]$job) -Launch $launch
                }
                else {
                    Stop-And-Reap -Job ([ref]$job) -Launch $launch -Assigned $assigned
                }
                $cleanupDone = $true
                $cleanupError = $firstCleanupError
            }
            catch {
                $cleanupError = New-RunnerAggregate -Message 'windows_runner: cleanup retry failed' -Errors @($firstCleanupError, $_.Exception)
            }
        }
    }
    if ($null -ne $mutex -and $mutexHeld) {
        try {
            $mutex.ReleaseMutex()
        }
        catch {
            $mutexReleaseError = $_.Exception
        }
    }
    if ($null -ne $mutex) {
        try {
            $mutex.Dispose()
        }
        catch {
            $mutexDisposeError = $_.Exception
        }
    }
    $mutexCleanupExceptions = @()
    if ($null -ne $mutexReleaseError) { $mutexCleanupExceptions += $mutexReleaseError }
    if ($null -ne $mutexDisposeError) { $mutexCleanupExceptions += $mutexDisposeError }
    if ($mutexCleanupExceptions.Count -ne 0) {
        $mutexCleanupError = New-RunnerAggregate -Message 'windows_runner: node executor mutex cleanup failed' -Errors $mutexCleanupExceptions
    }
}

if ($null -ne $primaryError) {
    $failureExceptions = @($primaryError)
    if ($null -ne $cleanupError) { $failureExceptions += $cleanupError }
    if ($null -ne $mutexCleanupError) { $failureExceptions += $mutexCleanupError }
    if ($failureExceptions.Count -gt 1) {
        throw (New-RunnerAggregate -Message 'windows_runner: primary operation failed and cleanup failed' -Errors $failureExceptions)
    }
    throw $primaryError
}
if ($null -ne $processExitCode -and $processExitCode -ne 0) {
    $childExitError = [System.Exception]::new("windows_runner: child exited with code $processExitCode")
    $postExitExceptions = @($childExitError)
    if ($null -ne $cleanupError) { $postExitExceptions += $cleanupError }
    if ($null -ne $mutexCleanupError) { $postExitExceptions += $mutexCleanupError }
    if ($postExitExceptions.Count -gt 1) {
        throw (New-RunnerAggregate -Message 'windows_runner: child exited and cleanup failed' -Errors $postExitExceptions)
    }
    exit $processExitCode
}
if ($null -ne $cleanupError -and $null -ne $mutexCleanupError) {
    throw (New-RunnerAggregate -Message 'windows_runner: process and mutex cleanup failed' -Errors @($cleanupError, $mutexCleanupError))
}
if ($null -ne $cleanupError) {
    throw $cleanupError
}
if ($null -ne $mutexCleanupError) {
    throw $mutexCleanupError
}
[void](Write-CacheManifest -ManifestPath $cacheManifestPath -Manifest $cacheManifest)
[pscustomobject]@{
	runner_id = $NodeId
	node_id = $NodeId
    repository_id = $RepositoryId
    source_commit = $SourceCommit
    source_tree = $SourceTree
    cache_scope = 'windows_node_local'
	cache_seed_state = $cacheSeedState
	cache_identity = $cacheIdentity
	cache = [pscustomobject]@{
		seed_state = $cacheSeedState
		identity = $cacheIdentity
		gomodcache = $goModCacheRoot
		gocache = $goCacheRoot
	}
	authority = 'LOCAL_NON_AUTHORITATIVE'
	runtime = 'windows_native'
	total_logical_processors = $totalLogicalProcessors
    total_physical_memory_bytes = $totalPhysicalMemoryBytes
    status = 'NOT_VERIFIED'
    local_outcome = 'LOCAL_EXECUTED'
} | ConvertTo-Json -Compress
