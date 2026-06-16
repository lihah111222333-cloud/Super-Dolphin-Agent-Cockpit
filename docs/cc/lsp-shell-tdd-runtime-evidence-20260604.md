# lsp shell runtime-chain TDD evidence (2026-06-04)

## Scope

- Runtime-chain only: language detection, language adapter/config, non-packaged installer/runtime registration, and diagnostics bootstrap routing.
- Packaged scripts and runtimeenv are intentionally out of scope for this task.
- No commit or push.

## RED

Command:

```bash
go test ./internal/sidecar/lsp/manager ./internal/sidecar/lsp/multilsp ./cmd/mcp-lsp ./internal/sidecar/lsp/tools ./internal/platform/config -run "TestDetectLanguageIDShellExtensionsUseShellscript|TestLanguageAdapterRegistryFromConfigRegistersShellAdapter|TestRuntimePrimaryLanguageIDsIncludeShellscript|TestSetupInstallerRegistersShellLanguageServer|TestDiagnosticsShellScriptBootstrapsShellscriptLanguage|TestNew_DefaultsLSPConfig" -count=1
```

Exit code: 1

Key failures:

- `TestDetectLanguageIDShellExtensionsUseShellscript`: `DetectLanguageID("script.ksh") = "ksh", want "shellscript"`.
- `TestLanguageAdapterRegistryFromConfigRegistersShellAdapter`: `missing shellscript adapter`.
- `TestRuntimePrimaryLanguageIDsIncludeShellscript`: `runtimePrimaryLanguageIDs() = []string{"go", "javascript", "python", "css", "rust", "java", "markdown"}, missing shellscript`.
- `TestSetupInstallerRegistersShellLanguageServer`: `EnsureInstalledDetailed(shellscript) error = no installer config found for language: shellscript`.
- `TestDiagnosticsShellScriptBootstrapsShellscriptLanguage`: diagnostics returned `unsupported language for LSP toolchain` for `broken.sh`.
- `TestNew_DefaultsLSPConfig`: `LSP project adapters missing shell`.

Failure reason is the expected missing shell runtime-chain support, not a test compile/spelling error.

## GREEN

Command:

```bash
go test ./internal/sidecar/lsp/manager ./internal/sidecar/lsp/multilsp ./cmd/mcp-lsp ./internal/sidecar/lsp/tools ./internal/platform/config -run "TestDetectLanguageIDShellExtensionsUseShellscript|TestLanguageAdapterRegistryFromConfigRegistersShellAdapter|TestRuntimePrimaryLanguageIDsIncludeShellscript|TestSetupInstallerRegistersShellLanguageServer|TestDiagnosticsShellScriptBootstrapsShellscriptLanguage|TestNew_DefaultsLSPConfig" -count=1
```

Exit code: 0

Passing packages:

- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/manager`
- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/multilsp`
- `github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp`
- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/tools`
- `github.com/anthropic-ai/super-agent-v3/internal/platform/config`

## Final verification

Commands:

```bash
gofmt -w internal/sidecar/lsp/manager/language_id_test.go internal/sidecar/lsp/manager/registry.go internal/sidecar/lsp/multilsp/language_service_config.go internal/sidecar/lsp/multilsp/language_service_config_test.go cmd/mcp-lsp/runtime.go cmd/mcp-lsp/runtime_test.go internal/sidecar/lsp/tools/tool_diagnostics_test.go internal/contract/config.go internal/platform/config/lsp.go internal/platform/config/config_test.go
```

Exit code: 0

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./internal/platform/config -count=1
```

First attempt exit code: 1. Guard caught a new test complexity violation:

- `internal/sidecar/lsp/multilsp/language_service_config_test.go:43 TestLanguageAdapterRegistryFromConfigRegistersShellAdapter(): CC 11 > 上限 10`

Fix: split shell adapter assertions into small test helpers; did not freeze or weaken guard thresholds.

Rerun exit code: 0. Passing packages included:

- `github.com/anthropic-ai/super-agent-v3/internal/archtest`
- `github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp`
- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/installer`
- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/manager`
- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/multilsp`
- `github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/tools`
- `github.com/anthropic-ai/super-agent-v3/internal/platform/config`

Linker emitted macOS version warnings while archtest built, but the command exited 0 on rerun.

```bash
git diff --check
```

Exit code: 0

```bash
git status --short
```

Output:

```text
 M internal/sidecar/lsp/manager/language_id_test.go
 M internal/sidecar/lsp/manager/registry.go
 M internal/sidecar/lsp/multilsp/language_service_config.go
 M internal/sidecar/lsp/multilsp/language_service_config_test.go
 M cmd/mcp-lsp/runtime.go
 M cmd/mcp-lsp/runtime_test.go
 M internal/sidecar/lsp/tools/tool_diagnostics_test.go
 M internal/contract/config.go
 M internal/platform/config/config_test.go
 M internal/platform/config/lsp.go
?? docs/cc/lsp-shell-tdd-runtime-evidence-20260604.md
```

Observed files are limited to the requested runtime-chain/evidence scope.
