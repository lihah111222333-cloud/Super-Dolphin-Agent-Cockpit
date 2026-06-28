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

function Copy-FrontendAppDistToEmbed() {
    $source = Join-Path $Root 'frontend-app/dist'
    $destination = Join-Path $Root 'cmd/agent-terminal/frontend/dist'
    if (-not (Test-Path -LiteralPath (Join-Path $source 'index.html') -PathType Leaf)) {
        throw "frontend-app/dist/index.html is missing before embed copy"
    }
    Assert-RepoChildPath -Label 'embedded frontend dist' -Path $destination
    if (Test-Path -LiteralPath $destination) {
        Remove-Item -LiteralPath $destination -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $destination | Out-Null
    Get-ChildItem -LiteralPath $source -Force | Copy-Item -Destination $destination -Recurse -Force
    if (-not (Test-Path -LiteralPath (Join-Path $destination 'index.html') -PathType Leaf)) {
        throw "cmd/agent-terminal/frontend/dist/index.html is missing after embed copy"
    }
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

Invoke-GoBuild -Output $mcpOrch -Package './cmd/mcp-orch'
Invoke-GoBuild -Output $mcpLSP -Package './cmd/mcp-lsp'
Invoke-GoBuild -Output $agentTerminal -Package './cmd/agent-terminal'

$sidecarPattern = 'Test(MCPToolsListIncludesPromptRecall|HandleScopedToolsCallWithCallerUsesTrustedScope|LSPToolManifestsExposeShortNames|ToolsListHidesSemanticLSPToolsWhenLanguageServersUnavailable)$'
Invoke-GuardedGoTest ./cmd/mcp-orch ./cmd/mcp-lsp -run $sidecarPattern -count=1

$cleanupPattern = 'Test(DiscoverProcessesReturnsBothMaps|FilterOrphanMCPProcessesSkipsPeerWithLiveParent|CleanOrphanedMCPProcessesSkipsSelf|KillMCPProcessRefusesPID1)$'
Invoke-GuardedGoTest ./internal/provider/codexapp -run $cleanupPattern -count=1
