# lsp shell packaged-chain TDD evidence (2026-06-04)

## Scope

- Agent: Codex provider, packaged-chain only.
- Allowed production surface: runtimeenv packaged LSP fallback map, prepare bundle scripts, package scripts, packaged app verifier scripts.
- Explicitly out of scope: runtime adapter/installer source, `cmd/mcp-lsp/**`, shellcheck integration.

## RED

Only tests and this evidence file were changed before RED verification.

### Command: runtimeenv fallback map

```bash
go test ./internal/platform/runtimeenv -run 'DefaultLSPLanguages' -count=1
```

Exit code: 1

Key failure:

```text
--- FAIL: TestDefaultLSPLanguagesMapsBashLanguageServerToShellscript (0.00s)
    runtimeenv_test.go:267: defaultLSPLanguages(bash-language-server) = [], want [shellscript]
FAIL	github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv	0.634s
```

### Command: packaged-chain guards

```bash
go test ./scripts -run 'Package|PrepareLSP|Verify' -count=1
```

Exit code: 1

Key failures:

```text
--- FAIL: TestPackageLinuxScriptBundlesVerifiedLSPBundle (0.00s)
    package_linux_guard_test.go:54: script missing "bash-language-server"
--- FAIL: TestPackageLinuxRunScriptPrefersBundledCodexBin (0.00s)
    package_linux_guard_test.go:93: script missing "bundled_execs=(mcp-orch mcp-lsp mcp-ida gopls go typescript-language-server vscode-css-language-server pyright-langserver rust-analyzer bash-language-server sg)"
--- FAIL: TestPackageLinuxScriptRequiresAndCopiesBashLanguageServer (0.00s)
    package_linux_guard_test.go:104: script missing "\"bash-language-server|bin/bash-language-server\""
--- FAIL: TestVerifyPackagedAppLinuxChecksBundledBashLanguageServer (0.00s)
    package_linux_guard_test.go:272: script missing "\"bash-language-server|bin/bash-language-server\""
--- FAIL: TestPackageMacOSScriptRequiresVerifiedLSPBundle (0.00s)
    package_macos_guard_test.go:100: script missing "bash-language-server"
--- FAIL: TestPackageMacOSScriptRequiresAndCopiesBashLanguageServer (0.00s)
    package_macos_guard_test.go:117: script missing "\"bash-language-server|bin/bash-language-server\""
--- FAIL: TestPackageScriptsWriteLSPManifestAcceptsMinifiedSourceWithReorderedFields (1.01s)
    --- FAIL: TestPackageScriptsWriteLSPManifestAcceptsMinifiedSourceWithReorderedFields/macos (0.57s)
        package_macos_guard_test.go:403: packaged LSP manifest missing server bash-language-server
    --- FAIL: TestPackageScriptsWriteLSPManifestAcceptsMinifiedSourceWithReorderedFields/linux (0.44s)
        package_macos_guard_test.go:403: packaged LSP manifest missing server bash-language-server
--- FAIL: TestPackageScriptsWriteLSPManifestPreservesLanguages (0.87s)
    --- FAIL: TestPackageScriptsWriteLSPManifestPreservesLanguages/macos (0.41s)
        package_macos_guard_test.go:427: packaged LSP manifest missing server bash-language-server
    --- FAIL: TestPackageScriptsWriteLSPManifestPreservesLanguages/linux (0.46s)
        package_macos_guard_test.go:427: packaged LSP manifest missing server bash-language-server
--- FAIL: TestVerifyPackagedAppMacOSChecksBundledBashLanguageServer (0.00s)
    package_macos_release_guard_test.go:96: script missing "\"bash-language-server|bin/bash-language-server\""
--- FAIL: TestPrepareLSPBundleScriptsInstallBashLanguageServer (0.00s)
    --- FAIL: TestPrepareLSPBundleScriptsInstallBashLanguageServer/prepare_lsp_bundle_macos.sh (0.00s)
        prepare_lsp_bundle_guard_test.go:23: script missing "bash-language-server"
    --- FAIL: TestPrepareLSPBundleScriptsInstallBashLanguageServer/prepare_lsp_bundle_linux.sh (0.00s)
        prepare_lsp_bundle_guard_test.go:23: script missing "bash-language-server"
--- FAIL: TestPrepareLSPBundleScriptsIncludeBashLanguageServerInManifestAndChecksums (0.00s)
    --- FAIL: TestPrepareLSPBundleScriptsIncludeBashLanguageServerInManifestAndChecksums/prepare_lsp_bundle_macos.sh (0.00s)
        prepare_lsp_bundle_guard_test.go:47: script missing "'bash-language-server|bin/bash-language-server|[\"shellscript\"]'"
    --- FAIL: TestPrepareLSPBundleScriptsIncludeBashLanguageServerInManifestAndChecksums/prepare_lsp_bundle_linux.sh (0.00s)
        prepare_lsp_bundle_guard_test.go:47: script missing "'bash-language-server|bin/bash-language-server|[\"shellscript\"]'"
FAIL	github.com/anthropic-ai/super-agent-v3/scripts	23.064s
```

RED result: failures are the expected missing packaged bash-language-server / shellscript support.

## GREEN

Minimal production implementation was added after RED: runtimeenv fallback language mapping, prepare bundle scripts, package scripts, and packaged app verifier scripts now include `bash-language-server` as `shellscript`. No runtime adapter/installer source was touched.

### Command: runtimeenv fallback map

```bash
go test ./internal/platform/runtimeenv -run 'DefaultLSPLanguages' -count=1
```

Exit code: 0

Key pass output:

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv	0.487s
```

### Command: packaged-chain guards

```bash
go test ./scripts -run 'Package|PrepareLSP|Verify' -count=1
```

Exit code: 0

Key pass output:

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	19.161s
```


## Final verification

### Command: gofmt

```bash
gofmt -w internal/platform/runtimeenv/runtimeenv.go internal/platform/runtimeenv/runtimeenv_test.go scripts/prepare_lsp_bundle_guard_test.go scripts/package_macos_guard_test.go scripts/package_linux_guard_test.go scripts/package_macos_release_guard_test.go scripts/package_guard_helpers_test.go
```

Exit code: 0

### Command: runtimeenv package

```bash
go test ./internal/platform/runtimeenv -count=1
```

Exit code: 0

Key pass output:

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv	0.380s
```

### Command: packaged-chain guards

```bash
go test ./scripts -run 'Package|PrepareLSP|Verify' -count=1
```

Exit code: 0

Key pass output:

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	19.025s
```

### Command: shell syntax

```bash
bash -n scripts/package_macos.sh scripts/package_linux.sh scripts/prepare_lsp_bundle_macos.sh scripts/prepare_lsp_bundle_linux.sh scripts/verify_packaged_app_macos.sh scripts/verify_packaged_app_linux.sh
```

Exit code: 0

### Command: diff whitespace check

```bash
git diff --check
```

Exit code: 0

### Command: final status

```bash
git status --short
```

Exit code: 0

Output:

```text
 M internal/platform/runtimeenv/runtimeenv.go
 M internal/platform/runtimeenv/runtimeenv_test.go
 M scripts/package_guard_helpers_test.go
 M scripts/package_linux.sh
 M scripts/package_linux_guard_test.go
 M scripts/package_macos.sh
 M scripts/package_macos_guard_test.go
 M scripts/package_macos_release_guard_test.go
 M scripts/prepare_lsp_bundle_guard_test.go
 M scripts/prepare_lsp_bundle_linux.sh
 M scripts/prepare_lsp_bundle_macos.sh
 M scripts/verify_packaged_app_linux.sh
 M scripts/verify_packaged_app_macos.sh
?? docs/cc/lsp-shell-tdd-packaged-evidence-20260604.md
```

## REJECT 后补救 TDD

评审 REJECT 风险：prepare 脚本用 `node_modules/.bin/bash-language-server` 生成 wrapper；package 脚本 `rsync -aL` 会解引用 npm `.bin` symlink，打包后 wrapper 再用相对 `.bin` 路径会丢失真实包上下文。

### 真实入口证据

```bash
npm view bash-language-server bin version --json
```

Exit code: 0

```json
{
  "bin": {
    "bash-language-server": "out/cli.js"
  },
  "version": "5.6.0"
}
```

结论：prepare 脚本应指向包内真实入口 `node_modules/bash-language-server/out/cli.js`，不应指向 npm `.bin` symlink。

### RED: wrapper guard 先失败

先把 `scripts/prepare_lsp_bundle_guard_test.go` 的 bash-language-server guard 改成禁止 `.bin` wrapper，并要求真实包入口：

```bash
go test ./scripts -run 'PrepareLSP.*Bash|Bash.*Prepare|Wrapper' -count=1
```

Exit code: 1

关键失败输出：

```text
--- FAIL: TestPrepareLSPBundleScriptsInstallBashLanguageServer (0.00s)
    --- FAIL: TestPrepareLSPBundleScriptsInstallBashLanguageServer/prepare_lsp_bundle_macos.sh (0.00s)
        prepare_lsp_bundle_guard_test.go:24: script still contains "node_modules/.bin/bash-language-server"
    --- FAIL: TestPrepareLSPBundleScriptsInstallBashLanguageServer/prepare_lsp_bundle_linux.sh (0.00s)
        prepare_lsp_bundle_guard_test.go:24: script still contains "node_modules/.bin/bash-language-server"
FAIL
FAIL	github.com/anthropic-ai/super-agent-v3/scripts	4.237s
FAIL
```

### GREEN: 最小生产修复后通过

生产改动仅把两个 prepare 脚本的 wrapper 目标从 `.bin` symlink 改为真实入口：

```text
write_path_wrapper bash-language-server node_modules/bash-language-server/out/cli.js
```

然后运行评审要求的 GREEN/验证命令：

```bash
go test ./scripts -run 'PrepareLSP|Package|Verify|Bash|Wrapper' -count=1
```

Exit code: 0

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	22.831s
```

```bash
go test ./internal/platform/runtimeenv -count=1
```

Exit code: 0

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv	0.528s
```

```bash
bash -n scripts/package_macos.sh scripts/package_linux.sh scripts/prepare_lsp_bundle_macos.sh scripts/prepare_lsp_bundle_linux.sh scripts/verify_packaged_app_macos.sh scripts/verify_packaged_app_linux.sh
```

Exit code: 0

### 补救后 diff whitespace check

```bash
git diff --check
```

Exit code: 0
