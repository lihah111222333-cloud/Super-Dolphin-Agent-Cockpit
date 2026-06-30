#requires -Version 5.1
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$GuardArgs
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$PSNativeCommandUseErrorActionPreference = $false

$ScriptDir = Split-Path -Parent $PSCommandPath
$RootDir = [System.IO.Path]::GetFullPath((Join-Path $ScriptDir '..'))
$script:LastStatus = 0

function Write-Usage {
    @'
Usage:
  scripts/test_with_guard.ps1 [go test args...]
  scripts/test_with_guard.ps1 <file.go> [more.go...]
  scripts/test_with_guard.ps1 --guard-only
  scripts/test_with_guard.ps1 --help

Examples:
  scripts/test_with_guard.ps1 internal/app/app.go
  scripts/test_with_guard.ps1 ./internal/provider/claudecli/... -count=1
  scripts/test_with_guard.ps1 -run TestFoo ./internal/module/thread/...
  scripts/test_with_guard.ps1 --guard-only
'@
}

function Write-Stderr {
    param([string]$Message)
    [Console]::Error.WriteLine($Message)
}

function Resolve-ExistingPath {
    param([string]$Path)
    return (Get-Item -LiteralPath $Path -ErrorAction Stop).FullName
}

function Test-SamePath {
    param(
        [string]$Left,
        [string]$Right
    )
    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) {
        return $false
    }
    $leftFull = [System.IO.Path]::GetFullPath($Left)
    $rightFull = [System.IO.Path]::GetFullPath($Right)
    return [StringComparer]::OrdinalIgnoreCase.Equals($leftFull, $rightFull)
}

function Convert-ToRepoRelativePath {
    param([string]$Path)
    $full = [System.IO.Path]::GetFullPath($Path)
    $root = [System.IO.Path]::GetFullPath($RootDir).TrimEnd(
        [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::AltDirectorySeparatorChar
    )
    foreach ($separator in @([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)) {
        $prefix = $root + $separator
        if ($full.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            return $full.Substring($prefix.Length)
        }
    }
    return $full
}

function Resolve-RealGo {
    $wrapperCandidates = @(
        (Join-Path $RootDir 'scripts/go'),
        (Join-Path $RootDir 'scripts/go.cmd'),
        (Join-Path $RootDir 'scripts/go.ps1')
    )

    $globalWrapper = ''
    if (-not [string]::IsNullOrWhiteSpace($env:GLOBAL_GO_WRAPPER)) {
        if (-not (Test-Path -LiteralPath $env:GLOBAL_GO_WRAPPER -PathType Leaf)) {
            Write-Stderr "GLOBAL_GO_WRAPPER does not exist: $env:GLOBAL_GO_WRAPPER"
            return ''
        }
        $globalWrapper = Resolve-ExistingPath $env:GLOBAL_GO_WRAPPER
    }

    if (-not [string]::IsNullOrWhiteSpace($env:REAL_GO_BIN)) {
        if (-not [System.IO.Path]::IsPathRooted($env:REAL_GO_BIN)) {
            Write-Stderr "REAL_GO_BIN must be an absolute path: $env:REAL_GO_BIN"
            return ''
        }
        if (-not (Test-Path -LiteralPath $env:REAL_GO_BIN -PathType Leaf)) {
            Write-Stderr "REAL_GO_BIN is not executable or does not exist: $env:REAL_GO_BIN"
            return ''
        }
        $realGo = Resolve-ExistingPath $env:REAL_GO_BIN
        foreach ($wrapper in $wrapperCandidates) {
            if ((Test-Path -LiteralPath $wrapper -PathType Leaf) -and (Test-SamePath $realGo (Resolve-ExistingPath $wrapper))) {
                Write-Stderr "REAL_GO_BIN points at the repository go wrapper, not the real go binary: $realGo"
                return ''
            }
        }
        if ($globalWrapper -and (Test-SamePath $realGo $globalWrapper)) {
            Write-Stderr "REAL_GO_BIN points at GLOBAL_GO_WRAPPER, not the real go binary: $realGo"
            return ''
        }
        return $realGo
    }

    foreach ($command in (Get-Command go -All -ErrorAction SilentlyContinue)) {
        if ($command.CommandType -ne 'Application') {
            continue
        }
        $source = $command.Source
        if ([string]::IsNullOrWhiteSpace($source) -or -not (Test-Path -LiteralPath $source -PathType Leaf)) {
            continue
        }
        $candidate = Resolve-ExistingPath $source
        $isWrapper = $false
        foreach ($wrapper in $wrapperCandidates) {
            if ((Test-Path -LiteralPath $wrapper -PathType Leaf) -and (Test-SamePath $candidate (Resolve-ExistingPath $wrapper))) {
                $isWrapper = $true
                break
            }
        }
        if ($isWrapper) {
            continue
        }
        if ($globalWrapper -and (Test-SamePath $candidate $globalWrapper)) {
            continue
        }
        return $candidate
    }

    Write-Stderr 'real go binary not found. Set REAL_GO_BIN to an absolute path, or install Go and make it visible on PATH.'
    return ''
}

function Test-AllowlistedRawGoTestScanTarget {
    param([string]$Path)
    $allowlist = @(
        (Join-Path $RootDir 'scripts/test_with_guard.sh'),
        (Join-Path $RootDir 'scripts/test_with_guard.ps1'),
        (Join-Path $RootDir 'scripts/go_with_guard.sh'),
        (Join-Path $RootDir 'scripts/activate_guard_env.sh'),
        (Join-Path $RootDir 'scripts/forbid_raw_go_test.sh')
    )
    foreach ($allowed in $allowlist) {
        if ((Test-Path -LiteralPath $allowed -PathType Leaf) -and (Test-SamePath $Path (Resolve-ExistingPath $allowed))) {
            return $true
        }
    }
    return $false
}

function Get-RawGoTestScanTargets {
    $targets = New-Object System.Collections.Generic.List[string]
    $makefile = Join-Path $RootDir 'Makefile'
    if (Test-Path -LiteralPath $makefile -PathType Leaf) {
        $targets.Add((Resolve-ExistingPath $makefile))
    }

    $workflowDir = Join-Path $RootDir '.github/workflows'
    if (Test-Path -LiteralPath $workflowDir -PathType Container) {
        Get-ChildItem -LiteralPath $workflowDir -Recurse -File -Include '*.yml', '*.yaml' |
            ForEach-Object { $targets.Add($_.FullName) }
    }

    $scriptsDir = Join-Path $RootDir 'scripts'
    if (Test-Path -LiteralPath $scriptsDir -PathType Container) {
        Get-ChildItem -LiteralPath $scriptsDir -Recurse -File -Include '*.sh', '*.ps1' |
            ForEach-Object { $targets.Add($_.FullName) }
    }

    return $targets | Sort-Object -Unique
}

function Invoke-RawGoTestGuard {
    $violations = New-Object System.Collections.Generic.List[string]
    foreach ($target in (Get-RawGoTestScanTargets)) {
        if (Test-AllowlistedRawGoTestScanTarget $target) {
            continue
        }
        $lineNo = 0
        foreach ($line in (Get-Content -LiteralPath $target)) {
            $lineNo += 1
            if ($line.TrimStart().StartsWith('#')) {
                continue
            }
            if ([regex]::IsMatch($line, '(^|[\s;|&])go\s+test([\s]|$)')) {
                $relative = Convert-ToRepoRelativePath $target
                $violations.Add(('{0}:{1}:{2}' -f $relative, $lineNo, $line))
            }
        }
    }

    if ($violations.Count -gt 0) {
        Write-Stderr ("raw go test entry guard found {0} violation(s)" -f $violations.Count)
        Write-Stderr ''
        Write-Stderr 'Use ./scripts/test_with_guard.sh, .\scripts\test_with_guard.ps1, or ./scripts/go_with_guard.sh instead of raw go test.'
        Write-Stderr ''
        Write-Stderr 'Violations:'
        foreach ($violation in $violations) {
            Write-Stderr ("  - {0}" -f $violation)
        }
        $script:LastStatus = 1
        return
    }

    [Console]::Out.WriteLine('raw go test entry guard passed')
    $script:LastStatus = 0
}

function Invoke-Guard {
    param([string]$realGo)
    Push-Location $RootDir
    try {
        Invoke-RawGoTestGuard
        if ($script:LastStatus -ne 0) {
            return
        }
        & $realGo run ./scripts/code_size_guard.go
        if ($LASTEXITCODE -ne 0) {
            $script:LastStatus = $LASTEXITCODE
            return
        }
        & $realGo test ./internal/archtest -count=1
        $script:LastStatus = $LASTEXITCODE
    } finally {
        Pop-Location
    }
}

function Invoke-GoTest {
    param(
        [string]$realGo,
        [string[]]$GuardArgs
    )
    Push-Location $RootDir
    try {
        & $realGo test @GuardArgs
        $script:LastStatus = $LASTEXITCODE
    } finally {
        Pop-Location
    }
}

function Test-AllArgsAreGoFiles {
    param([string[]]$Args)
    if ($Args.Count -eq 0) {
        return $false
    }
    foreach ($arg in $Args) {
        if (-not $arg.EndsWith('.go', [System.StringComparison]::OrdinalIgnoreCase)) {
            return $false
        }
    }
    return $true
}

function Invoke-SingleFileGuard {
    param(
        [string]$realGo,
        [string[]]$GoFiles
    )
    $cwd = (Get-Location).ProviderPath
    $resolvedGoFiles = @()
    foreach ($file in $GoFiles) {
        if ([System.IO.Path]::IsPathRooted($file)) {
            $resolvedGoFiles += [System.IO.Path]::GetFullPath($file)
        } else {
            $resolvedGoFiles += [System.IO.Path]::GetFullPath((Join-Path $cwd $file))
        }
    }

    Push-Location $RootDir
    $stderr = [System.IO.Path]::GetTempFileName()
    try {
        & $realGo run ./scripts/code_size_guard.go -- @resolvedGoFiles 2> $stderr
        $status = $LASTEXITCODE
        if ($status -ne 0) {
            foreach ($line in (Get-Content -LiteralPath $stderr)) {
                if ($line -notmatch '^exit status [0-9]+$') {
                    Write-Stderr $line
                }
            }
        }
        $script:LastStatus = $status
    } finally {
        Remove-Item -LiteralPath $stderr -Force -ErrorAction SilentlyContinue
        Pop-Location
    }
}

function Main {
    if ($GuardArgs.Count -eq 0) {
        Write-Usage
        exit 1
    }

    switch ($GuardArgs[0]) {
        '--help' {
            Write-Usage
            exit 0
        }
        '-h' {
            Write-Usage
            exit 0
        }
        '--guard-only' {
            $realGo = Resolve-RealGo
            if ([string]::IsNullOrWhiteSpace($realGo)) {
                exit 1
            }
            Invoke-Guard -realGo $realGo
            $status = $script:LastStatus
            exit $status
        }
        '--' {
            if ($GuardArgs.Count -le 1) {
                Write-Usage
                exit 1
            }
            $remaining = @($GuardArgs[1..($GuardArgs.Count - 1)])
            $realGo = Resolve-RealGo
            if ([string]::IsNullOrWhiteSpace($realGo)) {
                exit 1
            }
            Invoke-Guard -realGo $realGo
            $status = $script:LastStatus
            if ($status -ne 0) {
                exit $status
            }
            Invoke-GoTest -realGo $realGo -GuardArgs $remaining
            $status = $script:LastStatus
            exit $status
        }
        default {
            $realGo = Resolve-RealGo
            if ([string]::IsNullOrWhiteSpace($realGo)) {
                exit 1
            }
            if (Test-AllArgsAreGoFiles -Args $GuardArgs) {
                Invoke-SingleFileGuard -realGo $realGo -GoFiles $GuardArgs
                $status = $script:LastStatus
                exit $status
            }
            Invoke-Guard -realGo $realGo
            $status = $script:LastStatus
            if ($status -ne 0) {
                exit $status
            }
            Invoke-GoTest -realGo $realGo -GuardArgs $GuardArgs
            $status = $script:LastStatus
            exit $status
        }
    }
}

Main
