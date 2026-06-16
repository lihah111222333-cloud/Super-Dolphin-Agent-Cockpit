# LSP shellcheck 运行时诊断修复记录（2026-06-05）

## 结论

PR 77 已经把 `.sh` 接到 `shellscript` / `bash-language-server` 链路，但没有保证 `shellcheck` 存在。实际运行时如果只有 `bash-language-server`，`.sh` 不再返回 `language_unsupported`，但会返回 0 条 diagnostics；`bash-language-server` stderr 里会出现 `ShellCheck: disabling linting as no executable was found at path 'shellcheck'`。

这不是“shell 脚本已可靠诊断”，而是“shell 语言不再被拒绝，但关键诊断器缺失时静默变成空结果”。本次修复把 `shellcheck` 作为 shell 诊断的运行时必需 companion 处理：开发态 installer、打包 bundle、package 校验、运行入口预检、packaged app verifier 都必须包含并验证 `shellcheck`。

## 根因

- `bash-language-server` 对 shell 语法/静态问题的核心诊断依赖 `shellcheck`。
- 原 installer 只检查主二进制 `bash-language-server`，发现它在 PATH 后直接返回成功。
- 原 `setupInstaller()` 只执行 `npm install -g bash-language-server`，不会安装 `shellcheck`。
- 原 prepare/package/verify 脚本只把 `bash-language-server` 纳入 LSP bundle，不包含 `shellcheck`。
- 因此运行时会把“缺少 shellcheck”表现为 diagnostics 空结果，而不是 fail-fast。

## 修复范围

### 开发态运行时

- `internal/sidecar/lsp/installer/installer.go`
  - `InstallerConfig` 新增 `RequiredBinaries []RequiredBinary`。
  - 主二进制存在时也会验证 companion。
  - companion 缺失或健康检查失败时触发安装。
  - 安装后再次验证 companion；仍不可用则返回错误，不再静默成功。

- `cmd/mcp-lsp/runtime.go`
  - `shellscript` 安装命令改为 `npm install -g bash-language-server shellcheck`。
  - `shellcheck` 必须通过 `shellcheck --version` 健康检查。

### 打包态运行时

- `scripts/prepare_lsp_bundle_macos.sh`
- `scripts/prepare_lsp_bundle_linux.sh`
  - npm bundle 安装增加 `shellcheck`。
  - prepare 阶段执行 `node_modules/.bin/shellcheck --version`，强制下载/预热原生 shellcheck binary。
  - 写入 `bin/shellcheck` wrapper，目标为 `node_modules/shellcheck/bin/shellcheck`。
  - manifest/checksum 增加 `shellcheck|bin/shellcheck|["shellcheck"]`。

- `scripts/package_macos.sh`
- `scripts/package_linux.sh`
  - `lsp_server_specs` 增加 `shellcheck|bin/shellcheck`，包构建会校验、复制、暴露 symlink，并重写 packaged LSP manifest。
  - Linux 启动脚本 `bundled_execs` 增加 `shellcheck`，缺失时启动前 fail-fast。

- `scripts/verify_packaged_app_macos.sh`
- `scripts/verify_packaged_app_linux.sh`
  - LSP manifest verifier 增加 `shellcheck`。
  - required executable 增加 packaged `bin/shellcheck`。
  - smoke args 对 `shellcheck` 执行 `--version`。

## RED 证据

### 开发态 installer 红测

命令：

```bash
go test ./cmd/mcp-lsp -run TestSetupInstallerInstallsShellcheckWhenShellServerAlreadyExists -count=1
```

失败要点：

```text
shellcheck dependency was not installed: stat .../shellcheck: no such file or directory
```

含义：`bash-language-server` 已在 PATH 时，旧 installer 直接成功返回，没有补装 `shellcheck`。

### 打包链路红测

命令：

```bash
go test ./scripts -run 'Shellcheck|RequiresVerifiedLSPBundle|RunScriptPrefersBundledCodexBin|WriteLSPManifest|VerifyPackagedApp(MacOS|Linux)' -count=1
```

失败要点：

```text
script missing "\"shellcheck|bin/shellcheck\""
script missing "bundled_execs=(... bash-language-server shellcheck sg)"
packaged LSP manifest missing server shellcheck
script missing "'shellcheck|bin/shellcheck|[\"shellcheck\"]'"
```

含义：prepare、package、verify、manifest 生成和 Linux 启动预检都没有把 `shellcheck` 纳入 packaged runtime。

## GREEN 证据

### 聚焦 installer 测试

```bash
go test ./cmd/mcp-lsp -run 'TestSetupInstaller(RegistersShellLanguageServer|InstallsShellcheckWhenShellServerAlreadyExists)' -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp	1.788s
```

### 打包链路守卫

```bash
go test ./scripts -run 'Shellcheck|RequiresVerifiedLSPBundle|RunScriptPrefersBundledCodexBin|WriteLSPManifest|VerifyPackagedApp(MacOS|Linux)' -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	8.870s
```

### 真实运行时 e2e

命令：

```bash
MCP_LSP_REAL_SHELL_E2E=1 go test -tags=e2e ./cmd/mcp-lsp -run TestMcpLSPBinaryShellDiagnosticsUsesShellcheck_E2E -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp	8.252s
```

该 e2e 会临时 `npm install --prefix <tmp> bash-language-server shellcheck`，预热 `shellcheck --version`，启动真实 `cmd/mcp-lsp` 二进制，对缺失 `fi` 的 `broken.sh` 调用 `file diagnostics`。修复后 structured rows 包含 `src=shellcheck` 和 `code=SC...`，证明不是只“不报 unsupported”，而是在运行时产生 shellcheck 诊断。

### 最终相关面验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp	16.214s
ok  	github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/installer	4.353s
ok  	github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/manager	2.406s
ok  	github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/multilsp	9.177s
ok  	github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/tools	3.758s
```

命令期间 `internal/archtest` 构建输出了 macOS object version warning，但命令 exit code 为 0。

```bash
go test ./scripts -run 'Package|PrepareLSP|Verify' -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	30.228s
```

```bash
bash -n scripts/package_macos.sh scripts/package_linux.sh scripts/prepare_lsp_bundle_macos.sh scripts/prepare_lsp_bundle_linux.sh scripts/verify_packaged_app_macos.sh scripts/verify_packaged_app_linux.sh
```

结果：exit code 0。

## 剩余风险

- `shellcheck` npm 包会在 `shellcheck --version` 首次执行时下载平台原生 binary；prepare/e2e 阶段已显式预热，开发态 installer 也用健康检查触发预热。没有网络或上游下载失败时会 fail-fast。
- 新增真实 e2e 需要 `MCP_LSP_REAL_SHELL_E2E=1` 才运行，避免普通 e2e 在 CI 中隐式依赖 npm 网络。
- 本次没有提交，也没有改动旧主 checkout。
