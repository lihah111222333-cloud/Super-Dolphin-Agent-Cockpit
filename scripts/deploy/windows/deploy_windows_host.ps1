[CmdletBinding()]
param(
    [switch]$Yes,
    [Parameter(Mandatory = $true)][string]$RepoPath,
    [Parameter(Mandatory = $true)][string]$Command,
    [string]$InstallPath = 'C:\ProgramData\SuperDolphin\heteroci',
    [Parameter(Mandatory = $true)][Alias('NodeId')][ValidateNotNullOrEmpty()][ValidatePattern('^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62}[A-Za-z0-9])?$')][string]$RunnerId,
    [Parameter(Mandatory = $true)][ValidateNotNullOrEmpty()][string]$RepositoryId,
    [Parameter(Mandatory = $true)][string]$ObjectStoreRoot,
    [Parameter(Mandatory = $true)][string]$WorktreeRoot,
    [Parameter(Mandatory = $true)][string]$CacheRoot,
    [Parameter(Mandatory = $true)][ValidateNotNullOrEmpty()][string]$ToolchainIdentity,
    [Parameter(Mandatory = $true)][ValidateNotNullOrEmpty()][string]$GOOS,
    [Parameter(Mandatory = $true)][ValidateNotNullOrEmpty()][string]$GOARCH,
    [Parameter(Mandatory = $true)][ValidateSet('0', '1')][string]$CGOEnabled,
    [Parameter(Mandatory = $true)][ValidateNotNullOrEmpty()][string]$ModuleIdentity,
    [Parameter(Mandatory = $true)][ValidateNotNullOrEmpty()][string]$WorkloadIdentity
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Stop-Fast {
    param([Parameter(Mandatory = $true)][string]$Message)
    throw "deploy_windows_host: $Message"
}

function Assert-DeploymentIdentity {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Length -gt 512) {
        Stop-Fast "$Label must be explicit and bounded"
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) { Stop-Fast "$Label contains a control character" }
    }
}

function Assert-DeploymentNodeLocalPath {
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

function Assert-NoReparsePoints {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$AllowMissing
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $root = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::IsNullOrWhiteSpace($root)) { Stop-Fast "$Label has no filesystem root" }

    $rootItem = Get-Item -LiteralPath $root -ErrorAction Stop
    if (($rootItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        Stop-Fast "$Label contains a reparse point"
    }

    $current = $root
    $relative = $fullPath.Substring($root.Length)
    foreach ($segment in ($relative -split '[\\/]+')) {
        if ([string]::IsNullOrEmpty($segment)) { continue }
        $current = [System.IO.Path]::Combine($current, $segment)
        $item = Get-Item -LiteralPath $current -ErrorAction SilentlyContinue
        if ($null -eq $item) {
            if ($AllowMissing) { return }
            Stop-Fast "$Label does not exist"
        }
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Stop-Fast "$Label contains a reparse point"
        }
    }
}

function Get-CanonicalAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$AllowMissing
    )

    if ([string]::IsNullOrWhiteSpace($Path)) { Stop-Fast "$Label must not be empty" }
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
    if (-not [System.IO.Path]::IsPathRooted($fullPath)) { Stop-Fast "$Label must be absolute" }

    $root = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::IsNullOrWhiteSpace($root)) { Stop-Fast "$Label has no filesystem root" }
    while (($fullPath.Length -gt $root.Length) -and ($fullPath.EndsWith('\') -or $fullPath.EndsWith('/'))) {
        $fullPath = $fullPath.Substring(0, $fullPath.Length - 1)
    }

    $exists = Test-Path -LiteralPath $fullPath -PathType Any
    if (-not $exists) {
        if (-not $AllowMissing) { Stop-Fast "$Label does not exist" }
        Assert-NoReparsePoints -Path $fullPath -Label $Label -AllowMissing
        return $fullPath
    }

    $item = Get-Item -LiteralPath $fullPath -ErrorAction Stop
    if ($item.PSProvider.Name -ne 'FileSystem') { Stop-Fast "$Label is not on the filesystem provider" }
    $canonical = [System.IO.Path]::GetFullPath([string]$item.FullName)
    Assert-NoReparsePoints -Path $canonical -Label $Label
    return $canonical
}

function Resolve-SecurityIdentifierValue {
    param([Parameter(Mandatory = $true)][object]$Identity)

    $identityText = [string]$Identity
    if ($identityText -match '^S-[0-9]+-[0-9]+(?:-[0-9]+)+$') { return $identityText }
    try {
        $account = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $identityText
        return $account.Translate([System.Security.Principal.SecurityIdentifier]).Value
    }
    catch {
        Stop-Fast "unable to resolve ACL identity: $identityText"
    }
}

function Assert-InstallPathAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    $acl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $allowedSids = @('S-1-5-18', 'S-1-5-32-544')
    $administratorSid = 'S-1-5-32-544'
    $systemSid = 'S-1-5-18'
    $ownerSid = Resolve-SecurityIdentifierValue -Identity $acl.Owner
    if ($allowedSids -notcontains $ownerSid) {
        Stop-Fast "install path owner is not Administrators or SYSTEM"
    }

    $writeMask = [int]([System.Security.AccessControl.FileSystemRights]::WriteData -bor
        [System.Security.AccessControl.FileSystemRights]::AppendData -bor
        [System.Security.AccessControl.FileSystemRights]::WriteExtendedAttributes -bor
        [System.Security.AccessControl.FileSystemRights]::WriteAttributes -bor
        [System.Security.AccessControl.FileSystemRights]::Delete -bor
        [System.Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor
        [System.Security.AccessControl.FileSystemRights]::ChangePermissions -bor
        [System.Security.AccessControl.FileSystemRights]::TakeOwnership)
    $fullControl = [int][System.Security.AccessControl.FileSystemRights]::FullControl
    $administratorControls = $false
    $systemControls = $false

    foreach ($rule in @($acl.Access)) {
        $sid = Resolve-SecurityIdentifierValue -Identity $rule.IdentityReference
        $rights = [int]$rule.FileSystemRights
        $isAllow = ([string]$rule.AccessControlType -eq 'Allow')
        $grantsWrite = (($rights -band $writeMask) -ne 0)
        if ($isAllow -and $grantsWrite -and ($allowedSids -notcontains $sid)) {
            Stop-Fast 'install path is writable by a non-Administrators/SYSTEM principal'
        }
        if ($isAllow -and (($rights -band $fullControl) -eq $fullControl)) {
            if ($sid -eq $administratorSid) { $administratorControls = $true }
            if ($sid -eq $systemSid) { $systemControls = $true }
        }
    }
    if (-not $administratorControls -or -not $systemControls) {
        Stop-Fast 'install path must grant full control to both Administrators and SYSTEM'
    }
}

function ConvertTo-VerifiedBase64Token {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $token = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Value))
    if (($token.Length -eq 0) -or ($token -notmatch '^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$')) {
        Stop-Fast "$Label did not produce a safe base64 token"
    }
    try {
        $roundTrip = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($token))
    }
    catch {
        Stop-Fast "$Label base64 token failed validation"
    }
    if ($roundTrip -cne $Value) { Stop-Fast "$Label base64 token failed round-trip validation" }
    return $token
}

function Get-DeploymentMutexName {
    $mutexName = 'Global\SuperDolphin-Heteroci-Deploy-v2'
    if ($mutexName.Length -gt 260) { Stop-Fast 'deployment mutex name exceeds the Windows limit' }
    return $mutexName
}

function ConvertTo-PowerShellSingleQuotedLiteral {
    param([Parameter(Mandatory = $true)][string]$Value)
    return "'" + ($Value -replace "'", "''") + "'"
}

function Get-DefenderExclusionSnapshot {
    param([Parameter(Mandatory = $true)][string]$Label)

    $preference = & $readDefender.Name -ErrorAction Stop
    $rawPaths = @($preference.ExclusionPath | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_) } | Select-Object -Unique)
    $canonicalPaths = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($rawPath in $rawPaths) {
        $canonicalPath = Get-CanonicalAbsolutePath -Path ([string]$rawPath) -Label "$Label Defender exclusion" -AllowMissing
        [void]$canonicalPaths.Add($canonicalPath)
    }
    return @($canonicalPaths)
}

function Assert-ScheduledTaskIdentity {
    param(
        [Parameter(Mandatory = $true)][object]$Task,
        [Parameter(Mandatory = $true)][string]$ExpectedTaskName,
        [Parameter(Mandatory = $true)][string]$ExpectedExecute,
        [Parameter(Mandatory = $true)][string]$ExpectedArguments,
        [Parameter(Mandatory = $true)][string]$ExpectedTaskPath,
        [Parameter(Mandatory = $true)][string]$ExpectedPrincipalUserId,
        [Parameter(Mandatory = $true)][string]$ExpectedPrincipalLogonType,
        [Parameter(Mandatory = $true)][string]$ExpectedPrincipalRunLevel,
        [Parameter(Mandatory = $true)][string]$ExpectedMultipleInstances
    )

    if ([string]$Task.TaskName -cne $ExpectedTaskName) {
        Stop-Fast 'existing scheduled task name identity drift detected'
    }
    if ([string]$Task.TaskPath -cne $ExpectedTaskPath) {
        Stop-Fast 'existing scheduled task path identity drift detected'
    }
    $actions = @($Task.Actions)
    if ($actions.Count -ne 1) { Stop-Fast 'existing scheduled task has an unexpected action count' }
    if ([string]$actions[0].Execute -ine $ExpectedExecute -or [string]$actions[0].Arguments -cne $ExpectedArguments) {
        Stop-Fast 'existing scheduled task identity drift detected'
    }
    if ($null -eq $Task.Principal) {
        Stop-Fast 'existing scheduled task principal identity drift detected'
    }
    if ([string]$Task.Principal.UserId -cne $ExpectedPrincipalUserId -or
        [string]$Task.Principal.LogonType -cne $ExpectedPrincipalLogonType -or
        [string]$Task.Principal.RunLevel -cne $ExpectedPrincipalRunLevel) {
        Stop-Fast 'existing scheduled task principal identity drift detected'
    }
    $triggers = @($Task.Triggers)
    if ($triggers.Count -ne 1) {
        Stop-Fast 'existing scheduled task trigger identity drift detected'
    }
    $triggerClassName = [string]$triggers[0].CimClass.CimClassName
    if ($triggerClassName -cne 'MSFT_TaskLogonTrigger' -or
        [string]$triggers[0].UserId -cne $ExpectedPrincipalUserId -or
        $triggers[0].Enabled -ne $true) {
        Stop-Fast 'existing scheduled task trigger identity drift detected'
    }
    if ($null -eq $Task.Settings -or [string]$Task.Settings.MultipleInstances -cne $ExpectedMultipleInstances) {
        Stop-Fast 'existing scheduled task multiple-instance policy drift detected'
    }
}

if (-not $Yes) { Stop-Fast 'deployment is non-interactive; pass -Yes' }
if ($env:OS -ne 'Windows_NT') { Stop-Fast 'this installer only runs on Windows' }
if ($RunnerId -notmatch '^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62}[A-Za-z0-9])?$') { Stop-Fast '-RunnerId contains unsupported characters' }
if ([string]::IsNullOrWhiteSpace($Command)) { Stop-Fast '-Command must not be empty' }
[void](Assert-DeploymentIdentity -Value $RepositoryId -Label 'RepositoryId')
[void](Assert-DeploymentIdentity -Value $ToolchainIdentity -Label 'ToolchainIdentity')
[void](Assert-DeploymentIdentity -Value $GOOS -Label 'GOOS')
[void](Assert-DeploymentIdentity -Value $GOARCH -Label 'GOARCH')
[void](Assert-DeploymentIdentity -Value $ModuleIdentity -Label 'ModuleIdentity')
[void](Assert-DeploymentIdentity -Value $WorkloadIdentity -Label 'WorkloadIdentity')

$RepoPath = Get-CanonicalAbsolutePath -Path $RepoPath -Label 'RepoPath'
if (-not (Test-Path -LiteralPath $RepoPath -PathType Container)) { Stop-Fast 'RepoPath must name a directory' }
$InstallPath = Get-CanonicalAbsolutePath -Path $InstallPath -Label 'InstallPath' -AllowMissing
$ObjectStoreRoot = Get-CanonicalAbsolutePath -Path $ObjectStoreRoot -Label 'ObjectStoreRoot'
$WorktreeRoot = Get-CanonicalAbsolutePath -Path $WorktreeRoot -Label 'WorktreeRoot'
$CacheRoot = Get-CanonicalAbsolutePath -Path $CacheRoot -Label 'CacheRoot' -AllowMissing
[void](Assert-DeploymentNodeLocalPath -Path $ObjectStoreRoot -Label 'ObjectStoreRoot')
[void](Assert-DeploymentNodeLocalPath -Path $WorktreeRoot -Label 'WorktreeRoot')
[void](Assert-DeploymentNodeLocalPath -Path $CacheRoot -Label 'CacheRoot')
if (-not (Test-Path -LiteralPath $ObjectStoreRoot -PathType Container)) { Stop-Fast 'ObjectStoreRoot must name the existing Git common directory' }
if (-not (Test-Path -LiteralPath $WorktreeRoot -PathType Container)) { Stop-Fast 'WorktreeRoot must name the existing Git worktree root' }

$git = Get-Command git.exe -ErrorAction SilentlyContinue
if ($null -eq $git) { Stop-Fast 'git.exe is required' }
$gitRootRaw = (& $git.Source -C $RepoPath rev-parse --show-toplevel 2>$null | Out-String).Trim()
if (($LASTEXITCODE -ne 0) -or [string]::IsNullOrWhiteSpace($gitRootRaw)) { Stop-Fast 'RepoPath is not a Git worktree' }
$gitRootCanonical = Get-CanonicalAbsolutePath -Path $gitRootRaw -Label 'Git worktree root'
if ($WorktreeRoot.TrimEnd('\', '/') -ine $gitRootCanonical.TrimEnd('\', '/')) {
    Stop-Fast 'WorktreeRoot must equal the uniquely verified Git worktree root'
}
$gitCommonRaw = (& $git.Source -C $RepoPath rev-parse --git-common-dir 2>$null | Out-String).Trim()
if (($LASTEXITCODE -ne 0) -or [string]::IsNullOrWhiteSpace($gitCommonRaw)) { Stop-Fast 'Git common directory is unavailable' }
if (-not [System.IO.Path]::IsPathRooted($gitCommonRaw)) { $gitCommonRaw = Join-Path $RepoPath $gitCommonRaw }
$gitCommonCanonical = Get-CanonicalAbsolutePath -Path $gitCommonRaw -Label 'Git common directory'
if ($ObjectStoreRoot.TrimEnd('\', '/') -ine $gitCommonCanonical.TrimEnd('\', '/')) {
    Stop-Fast 'ObjectStoreRoot must equal the uniquely verified Git common directory'
}

$computer = Get-CimInstance -ClassName Win32_ComputerSystem -Property NumberOfLogicalProcessors,TotalPhysicalMemory -ErrorAction Stop
$totalLogicalProcessors = [uint64]$computer.NumberOfLogicalProcessors
if ($totalLogicalProcessors -le 0) { Stop-Fast 'Win32_ComputerSystem.NumberOfLogicalProcessors is not positive' }
$totalPhysicalMemoryBytes = [uint64]$computer.TotalPhysicalMemory
if ($totalPhysicalMemoryBytes -le 0) { Stop-Fast 'physical memory probe returned zero' }

$currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
if ($null -eq $currentIdentity -or $null -eq $currentIdentity.User) { Stop-Fast 'current Windows identity is unavailable' }
$expectedTaskPrincipalUserId = [string]$currentIdentity.User.Value
if ($expectedTaskPrincipalUserId -notmatch '^S-[0-9]+-[0-9]+(?:-[0-9]+)+$') {
    Stop-Fast 'current Windows identity has no canonical SID'
}
$principal = New-Object Security.Principal.WindowsPrincipal($currentIdentity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { Stop-Fast 'administrator privileges are required' }
$addDefender = Get-Command Add-MpPreference -ErrorAction SilentlyContinue
$readDefender = Get-Command Get-MpPreference -ErrorAction SilentlyContinue
$removeDefender = Get-Command Remove-MpPreference -ErrorAction SilentlyContinue
if ($null -eq $addDefender -or $null -eq $readDefender -or $null -eq $removeDefender) { Stop-Fast 'Defender exclusion cmdlets are unavailable' }

$source = Get-CanonicalAbsolutePath -Path (Join-Path $PSScriptRoot 'windows_runner.ps1') -Label 'runner script'
$sourceItem = Get-Item -LiteralPath $source -ErrorAction Stop
if (-not ($sourceItem -is [System.IO.FileInfo])) { Stop-Fast 'runner script is not a regular file' }

$mutexName = Get-DeploymentMutexName
$mutex = $null
$mutexHeld = $false
$mutexWasAbandoned = $false
$primaryException = $null
$rollbackException = $null
$releaseException = $null
$disposeException = $null

try {
    try {
        $mutex = [System.Threading.Mutex]::new($false, $mutexName)
    }
    catch {
        Stop-Fast 'unable to create deployment mutex'
    }

    try {
        $mutexHeld = [bool]$mutex.WaitOne(0)
    }
    catch [System.Threading.AbandonedMutexException] {
        $mutexHeld = $true
        $mutexWasAbandoned = $true
        Write-Verbose 'deployment mutex was abandoned; continuing with the acquired lock'
    }
    catch {
        Stop-Fast 'unable to acquire deployment mutex'
    }
    if (-not $mutexHeld) { Stop-Fast 'another deployment is already mutating this runner installation' }

if (-not (Test-Path -LiteralPath $InstallPath -PathType Container)) {
    New-Item -ItemType Directory -Path $InstallPath -ErrorAction Stop | Out-Null
}
$InstallPath = Get-CanonicalAbsolutePath -Path $InstallPath -Label 'InstallPath'
Assert-InstallPathAcl -Path $InstallPath

$runnerPath = Get-CanonicalAbsolutePath -Path (Join-Path $InstallPath 'windows_runner.ps1') -Label 'installed runner' -AllowMissing
if (Test-Path -LiteralPath $runnerPath -PathType Any) {
    Assert-NoReparsePoints -Path $runnerPath -Label 'installed runner'
    Assert-InstallPathAcl -Path $runnerPath
}

$commandBase64 = ConvertTo-VerifiedBase64Token -Value $Command -Label 'Command'
$runnerInvocation = '& ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $runnerPath) +
    ' -RepoPath ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $RepoPath) +
    ' -NodeId ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $RunnerId) +
    ' -RepositoryId ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $RepositoryId) +
    ' -ObjectStoreRoot ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $ObjectStoreRoot) +
    ' -WorktreeRoot ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $WorktreeRoot) +
    ' -CacheRoot ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $CacheRoot) +
    ' -ToolchainIdentity ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $ToolchainIdentity) +
    ' -GOOS ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $GOOS) +
    ' -GOARCH ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $GOARCH) +
    ' -CGOEnabled ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $CGOEnabled) +
    ' -ModuleIdentity ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $ModuleIdentity) +
    ' -WorkloadIdentity ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $WorkloadIdentity) +
    ' -CommandBase64 ' + (ConvertTo-PowerShellSingleQuotedLiteral -Value $commandBase64)
$actionToken = ConvertTo-VerifiedBase64Token -Value $runnerInvocation -Label 'ScheduledTask action'
$actionArguments = "-NoProfile -ExecutionPolicy Bypass -EncodedCommand $actionToken"
if ($actionArguments -notmatch '^-NoProfile -ExecutionPolicy Bypass -EncodedCommand [A-Za-z0-9+/]+={0,2}$') {
    Stop-Fast 'scheduled task action arguments are not a validated token sequence'
}

$powershell = Get-Command powershell.exe -CommandType Application -ErrorAction SilentlyContinue
if ($null -eq $powershell) { Stop-Fast 'powershell.exe is required' }
$powershellPath = Get-CanonicalAbsolutePath -Path $powershell.Source -Label 'powershell.exe'
$action = New-ScheduledTaskAction -Execute $powershellPath -Argument $actionArguments
$expectedTaskPath = '\'
$expectedTaskPrincipalLogonType = 'InteractiveToken'
$expectedTaskPrincipalRunLevel = 'Highest'
$expectedTaskMultipleInstances = 'IgnoreNew'
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $expectedTaskPrincipalUserId -LogonType Interactive -RunLevel Highest -ErrorAction Stop
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $expectedTaskPrincipalUserId -ErrorAction Stop
$trigger.UserId = $expectedTaskPrincipalUserId
$trigger.Enabled = $true
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ErrorAction Stop
$taskName = 'SuperDolphin-HeterociNode-v1'
$legacyTaskPrefix = 'SuperDolphin-Heteroci-'
$nodeTaskPrefix = 'SuperDolphin-HeterociNode-'

try {
    $allTasks = @(Get-ScheduledTask -ErrorAction Stop)
}
catch {
    Stop-Fast 'unable to inspect existing scheduled tasks'
}

$legacyTasks = @($allTasks | Where-Object {
    $candidateName = [string]$_.TaskName
    $candidateName.StartsWith($legacyTaskPrefix, [System.StringComparison]::OrdinalIgnoreCase) -and
        -not $candidateName.Equals($taskName, [System.StringComparison]::OrdinalIgnoreCase)
})
if ($legacyTasks.Count -gt 0) {
    $legacyNames = @($legacyTasks | ForEach-Object { [string]$_.TaskName } | Sort-Object -Unique) -join ', '
    Stop-Fast "legacy dynamic scheduled task(s) must be removed before node deployment: $legacyNames"
}

$conflictingNodeTasks = @($allTasks | Where-Object {
    $candidateName = [string]$_.TaskName
    $candidateName.StartsWith($nodeTaskPrefix, [System.StringComparison]::OrdinalIgnoreCase) -and
        -not $candidateName.Equals($taskName, [System.StringComparison]::OrdinalIgnoreCase)
})
if ($conflictingNodeTasks.Count -gt 0) {
    $conflictingNames = @($conflictingNodeTasks | ForEach-Object { [string]$_.TaskName } | Sort-Object -Unique) -join ', '
    Stop-Fast "conflicting node scheduled task(s) detected: $conflictingNames"
}

$existingTasks = @($allTasks | Where-Object { [string]$_.TaskName -ieq $taskName })
if ($existingTasks.Count -gt 1) { Stop-Fast 'multiple scheduled tasks share the requested identity' }
$existingTask = if ($existingTasks.Count -eq 1) { $existingTasks[0] } else { $null }
if ($null -ne $existingTask) {
    Assert-ScheduledTaskIdentity -Task $existingTask -ExpectedTaskName $taskName -ExpectedExecute $powershellPath -ExpectedArguments $actionArguments -ExpectedTaskPath $expectedTaskPath -ExpectedPrincipalUserId $expectedTaskPrincipalUserId -ExpectedPrincipalLogonType $expectedTaskPrincipalLogonType -ExpectedPrincipalRunLevel $expectedTaskPrincipalRunLevel -ExpectedMultipleInstances $expectedTaskMultipleInstances
}

$defenderExclusionAdded = $false
try {
    $beforeDefender = @(Get-DefenderExclusionSnapshot -Label 'before deployment')
    $hasCanonicalExclusion = $false
    foreach ($path in $beforeDefender) {
        if ([string]$path -ieq $InstallPath) { $hasCanonicalExclusion = $true; break }
    }
    if (-not $hasCanonicalExclusion) {
        & $addDefender.Name -ExclusionPath $InstallPath -ErrorAction Stop
        $defenderExclusionAdded = $true
    }

    $afterDefender = @(Get-DefenderExclusionSnapshot -Label 'after deployment')
    $readbackMatches = @($afterDefender | Where-Object { [string]$_ -ieq $InstallPath } | Select-Object -Unique)
    if ($readbackMatches.Count -ne 1) { Stop-Fast 'Defender exclusion canonical readback did not contain exactly one install path' }

    [System.IO.File]::Copy($source, $runnerPath, $true)
    $runnerPath = Get-CanonicalAbsolutePath -Path $runnerPath -Label 'installed runner'
    $runnerItem = Get-Item -LiteralPath $runnerPath -ErrorAction Stop
    if (-not ($runnerItem -is [System.IO.FileInfo])) { Stop-Fast 'installed runner is not a regular file' }
    Assert-InstallPathAcl -Path $runnerPath

    if ($null -eq $existingTask) {
        Register-ScheduledTask -TaskName $taskName -TaskPath $expectedTaskPath -Action $action -Trigger $trigger -Settings $settings -Principal $taskPrincipal -ErrorAction Stop | Out-Null
    }
}
catch {
    $primaryException = $_.Exception
    if ($defenderExclusionAdded) {
        try {
            & $removeDefender.Name -ExclusionPath $InstallPath -ErrorAction Stop
            $rollbackDefender = @(Get-DefenderExclusionSnapshot -Label 'rollback')
            $rollbackMatches = @($rollbackDefender | Where-Object { [string]$_ -ieq $InstallPath } | Select-Object -Unique)
            if ($rollbackMatches.Count -ne 0) {
                Stop-Fast 'Defender exclusion rollback readback still contained the install path'
            }
        }
        catch {
            $rollbackException = $_.Exception
        }
    }
}
}

catch {
    if ($null -eq $primaryException) {
        $primaryException = $_.Exception
    }
}
finally {
    if ($null -ne $mutex -and $mutexHeld) {
        try {
            $mutex.ReleaseMutex()
        }
        catch {
            $releaseException = $_.Exception
        }
    }
    if ($null -ne $mutex) {
        try {
            $mutex.Dispose()
        }
        catch {
            $disposeException = $_.Exception
        }
    }
}

if ($null -ne $primaryException) {
    $failureExceptions = [System.Collections.Generic.List[System.Exception]]::new()
    [void]$failureExceptions.Add($primaryException)
    if ($null -ne $rollbackException) {
        [void]$failureExceptions.Add($rollbackException)
    }
    if ($null -ne $releaseException) { [void]$failureExceptions.Add($releaseException) }
    if ($null -ne $disposeException) { [void]$failureExceptions.Add($disposeException) }
    if ($failureExceptions.Count -gt 1) {
        $failureMessage = 'deployment failed and cleanup also failed'
        if ($null -ne $rollbackException -and $null -eq $releaseException -and $null -eq $disposeException) {
            $failureMessage = 'deployment failed and Defender exclusion rollback failed'
        }
        throw [System.AggregateException]::new(
            $failureMessage,
            [System.Exception[]]$failureExceptions)
    }
    throw $primaryException
}

$cleanupExceptions = [System.Collections.Generic.List[System.Exception]]::new()
if ($null -ne $releaseException) { [void]$cleanupExceptions.Add($releaseException) }
if ($null -ne $disposeException) { [void]$cleanupExceptions.Add($disposeException) }
if ($cleanupExceptions.Count -gt 0) {
    throw [System.AggregateException]::new(
        'deployment mutex cleanup failed',
        [System.Exception[]]$cleanupExceptions)
}
[pscustomobject]@{
    authority = 'LOCAL_NON_AUTHORITATIVE'
    status = 'NOT_VERIFIED'
    runner_id = $RunnerId
    node_id = $RunnerId
    cache_scope = 'windows_node_local'
    repository_id = $RepositoryId
    repository = $RepoPath
    object_store_root = $ObjectStoreRoot
    worktree_root = $WorktreeRoot
    cache_root = $CacheRoot
    install_path = $InstallPath
    total_logical_processors = $totalLogicalProcessors
    total_physical_memory_bytes = $totalPhysicalMemoryBytes
    scheduled_task = $taskName
} | ConvertTo-Json -Compress
