Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Resolve-RepoRoot() {
    $gitRoot = (& git rev-parse --show-toplevel 2>$null)
    if ($LASTEXITCODE -eq 0 -and $gitRoot -and $gitRoot.Trim() -ne '') {
        return $gitRoot.Trim()
    }
    $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
    return (Split-Path -Parent $scriptDir)
}

function Invoke-Checked() {
    param(
        [Parameter(Mandatory)][string]$File,
        [Parameter(ValueFromRemainingArguments)][string[]]$Arguments
    )
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$File $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Resolve-BinaryPath() {
    param([Parameter(Mandatory)][string]$Name)
    $fileName = if ($IsWindows) { "$Name.exe" } else { $Name }
    return (Join-Path $Root "bin/$fileName")
}

function Assert-RepoChildPath() {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$Path
    )
    $resolvedRoot = (Resolve-Path -LiteralPath $Root).Path.TrimEnd('\', '/')
    $parent = Split-Path -Parent $Path
    $resolvedParent = (Resolve-Path -LiteralPath $parent).Path.TrimEnd('\', '/')
    if (-not $resolvedParent.StartsWith($resolvedRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "$Label must stay under repository root: $Path"
    }
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

function Copy-FrontendAppDistToEmbed() {
    $source = Join-Path $Root 'frontend-app/dist'
    $destination = Join-Path $Root 'cmd/agent-terminal/web-dist'
    Assert-RequiredFrontendEntries -Directory $source -Label 'frontend dist'
    Assert-RepoChildPath -Label 'embedded frontend dist' -Path $destination
    if (Test-Path -LiteralPath $destination) {
        Get-ChildItem -LiteralPath $destination -Force | Where-Object { $_.Name -ne '.gitkeep' } | Remove-Item -Recurse -Force
    } else {
        New-Item -ItemType Directory -Force -Path $destination | Out-Null
    }
    Get-ChildItem -LiteralPath $source -Force | Copy-Item -Destination $destination -Recurse -Force
    Assert-RequiredFrontendEntries -Directory $destination -Label 'embedded frontend dist'
}

function Invoke-GoBuild() {
    param(
        [Parameter(Mandatory)][string]$Output,
        [Parameter(Mandatory)][string]$Package
    )
    Write-Host "go build -o $Output $Package"
	Invoke-Checked go build -o $Output $Package
}

function Invoke-GuardedGoTest() {
	param([Parameter(ValueFromRemainingArguments)][string[]]$Arguments)
	$guard = Join-Path $Root 'scripts/test_with_guard.ps1'
	Write-Host "scripts/test_with_guard.ps1 $($Arguments -join ' ')"
	& $guard @Arguments
	if ($LASTEXITCODE -ne 0) {
		throw "scripts/test_with_guard.ps1 $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
	}
}

$Root = Resolve-RepoRoot
Set-Location -LiteralPath $Root

if ($env:SUPER_DOLPHIN_SKIP_FRONTEND_BUILD -ne '1') {
    Push-Location -LiteralPath (Join-Path $Root 'frontend-app')
    try {
        Invoke-Checked npm ci
        Invoke-Checked npm run build
    } finally {
        Pop-Location
    }
}
Copy-FrontendAppDistToEmbed

New-Item -ItemType Directory -Force -Path (Join-Path $Root 'bin') | Out-Null
$mcpOrch = Resolve-BinaryPath -Name 'mcp-orch'
$mcpLSP = Resolve-BinaryPath -Name 'mcp-lsp'
$agentTerminal = Resolve-BinaryPath -Name 'agent-terminal'
$updater = Resolve-BinaryPath -Name 'super-dolphin-updater'

Invoke-GoBuild -Output $mcpOrch -Package './cmd/mcp-orch'
Invoke-GoBuild -Output $mcpLSP -Package './cmd/mcp-lsp'
Invoke-GoBuild -Output $agentTerminal -Package './cmd/agent-terminal'
Invoke-GoBuild -Output $updater -Package './cmd/super-dolphin-updater'

$sidecarPattern = 'Test(MCPToolsListIncludesPromptRecall|HandleScopedToolsCallWithCallerUsesTrustedScope|LSPToolManifestsExposeShortNames|ToolsListHidesSemanticLSPToolsWhenLanguageServersUnavailable)$'
Invoke-GuardedGoTest ./cmd/mcp-orch ./cmd/mcp-lsp -run $sidecarPattern -count=1

$cleanupPattern = 'Test(DiscoverProcessesReturnsBothMaps|FilterOrphanMCPProcessesSkipsPeerWithLiveParent|CleanOrphanedMCPProcessesSkipsSelf|KillMCPProcessRefusesPID1)$'
Invoke-GuardedGoTest ./internal/provider/codexapp -run $cleanupPattern -count=1

$updateSidecarPattern = 'Test(ServiceNeverCallsDarwinSidecarOutsideDarwin|SidecarIsExplicitlyUnsupportedOutsideDarwin|ParseInstallRequestAcceptsLogPathAndWaitPID|ValidateInstallRequestRejectsMissingOrInvalidGeneration)$'
Invoke-GuardedGoTest ./cmd/super-dolphin-updater ./internal/module/appupdate ./internal/platform/appupdatefailure -run $updateSidecarPattern -count=1
