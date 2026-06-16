# t1-mcp-orch-linux 二修 TDD 验证报告

日期：2026-05-28
工作区：`/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-mcp-orch-linux`

## 本轮最终落盘改动文件

- `scripts/package_linux.sh`
  - 将原顶层执行流程收进 `package_linux_main`，只在直接执行脚本时运行。
  - 保持 Linux 打包行为：复制 `models.yaml`、生成 `run.sh` 并 export `SUPER_DOLPHIN_MODEL_REGISTRY`、manifest 相关环境由既有实现 passthrough。
  - 允许 Go 测试通过 `source` 执行 `copy_model_registry` 的缺失文件 fail-fast 分支。
- `scripts/package_linux_guard_test.go`
  - 新增 `TestPackageLinuxCopyModelRegistryFailsFastWhenSourceMissing` 行为测试。
  - 测试在临时 `root/stage` 下真正执行 `copy_model_registry`，断言非 0 exit、精确错误信息、且不会产出 `stage/models.yaml`。
- `docs/reviews/t1-mcp-orch-linux-second-fix-tdd-report-2026-05-28.md`
  - 本报告。

本轮还对 `cmd/mcp-orch/runtime.go`、`internal/contract/manifest.go`、`scripts/package_linux.sh` 做过可逆 mutation RED 验证；mutation 已恢复，最终状态由 GREEN 命令确认。

## RED 证据摘要

### 1. Linux `copy_model_registry` 缺失文件行为测试（新增测试先失败）

命令：

```bash
go test ./scripts -run TestPackageLinuxCopyModelRegistryFailsFastWhenSourceMissing -count=1
```

原始输出摘要：

```text
--- FAIL: TestPackageLinuxCopyModelRegistryFailsFastWhenSourceMissing (0.08s)
    package_linux_guard_test.go:52: copy_model_registry output = "package_linux.sh must run with GOOS=linux; current GOOS=darwin", want "missing model registry: /var/folders/.../cmd/mcp-orch/tools/modelregistry/models.yaml"
FAIL
FAIL	github.com/anthropic-ai/super-agent-v3/scripts	0.728s
FAIL
```

结论：新增测试在修复前没有到达目标 fail-fast 分支，RED 可审计。

### 2. mcp-orch schema gate mutation RED

临时 mutation：让 `verifyMCPOrchDatabaseReady` 忽略 `platformdb.VerifyMinSchemaVersion` 返回错误。

命令：

```bash
go test ./cmd/mcp-orch -run 'Schema' -count=1
```

原始输出摘要：

```text
rlimit: NOFILE already at 1048576 (max 1048576), no change needed
--- FAIL: TestVerifyMCPOrchDatabaseSchemaMissingFailsFast (0.00s)
    runtime_test.go:151: verifyMCPOrchDatabaseReady() error = <nil>, want schema_migrations error
--- FAIL: TestVerifyMCPOrchDatabaseSchemaVersionBelowMinimumFailsFast (0.00s)
    runtime_test.go:167: verifyMCPOrchDatabaseReady() error = nil, want below-minimum schema failure
FAIL
FAIL	github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch	0.405s
FAIL
```

结论：schema gate 测试能抓到缺失 `VerifyMinSchemaVersion` fail-fast。

### 3. manifest `SUPER_DOLPHIN_MODEL_REGISTRY` passthrough mutation RED

临时 mutation：从 `mcpPassthroughEnvKeys` 移除 `SUPER_DOLPHIN_MODEL_REGISTRY`。

命令：

```bash
go test ./internal/contract -run ModelRegistry -count=1
```

原始输出摘要：

```text
--- FAIL: TestBuildManifestPassesModelRegistryEnvFromProcessToStdioBinaries (0.00s)
    manifest_test.go:22: binary lsp SUPER_DOLPHIN_MODEL_REGISTRY = "", want /bundle/models.yaml
FAIL
FAIL	github.com/anthropic-ai/super-agent-v3/internal/contract	0.459s
FAIL
```

结论：manifest passthrough 测试能抓到 registry env 丢失。

### 4. Linux `run.sh` registry export mutation RED

临时 mutation：将 `run.sh` 的 `SUPER_DOLPHIN_MODEL_REGISTRY` export 改名。

命令：

```bash
go test ./scripts -run TestPackageLinuxScriptBundlesModelRegistry -count=1
```

原始输出摘要：

```text
--- FAIL: TestPackageLinuxScriptBundlesModelRegistry (0.00s)
    package_linux_guard_test.go:20: script missing "export SUPER_DOLPHIN_MODEL_REGISTRY="$here/models.yaml""
FAIL
FAIL	github.com/anthropic-ai/super-agent-v3/scripts	0.263s
FAIL
```

结论：Linux `run.sh` registry export 测试能抓到 export 丢失。

## GREEN 证据摘要

### 新增行为测试修复后单测

```bash
go test ./scripts -run TestPackageLinuxCopyModelRegistryFailsFastWhenSourceMissing -count=1
```

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	0.515s
```

### 用户指定 GREEN 命令

```bash
go test ./cmd/mcp-orch -run 'FailFast|Schema|Pool' -count=1
```

```text
ok  	github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch	0.615s
```

```bash
go test ./scripts -run 'PackageLinux|PackageMacOS|VerifyPackagedApp' -count=1
```

```text
ok  	github.com/anthropic-ai/super-agent-v3/scripts	0.308s
```

```bash
go test ./internal/contract -run ModelRegistry -count=1
```

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/contract	0.427s
```

```bash
go test ./internal/platform/db -run 'SchemaVersion|MinRequired|VerifyMinSchemaVersion' -count=1
```

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/platform/db	0.522s
```

## guard 输出

```bash
make guard
```

原始输出摘要：

```text
./scripts/test_with_guard.sh --guard-only
✅ 入口守卫: 未发现裸跑 go test 入口。
📏  文件≤600 函数≤80 嵌套≤4 CC≤10 下划线≤3 包文件≤30 包行≤10000
📊  生产 baseline 棘轮通过 — 81 个文件冻结中
📊  测试 baseline 棘轮通过 — 32 个文件冻结中
✅  代码守卫: 全部通过
# github.com/anthropic-ai/super-agent-v3/internal/archtest.test
ld: warning: object file (...) was built for newer 'macOS' version (26.0) than being linked (11.0)
ok  	github.com/anthropic-ai/super-agent-v3/internal/archtest	1.733s
```

结论：guard exit code 0；存在 macOS linker warning，但未导致失败。

## 未覆盖项

- 未执行实际 Linux tarball 打包全流程；本轮覆盖的是脚本契约、`copy_model_registry` 缺失文件行为、`run.sh` env、manifest passthrough 与 DB schema gate。
- 未执行 `make test`、`make build-plain`、前端 `npm run build`；本轮按二修要求只运行了指定 GREEN 命令和 `make guard`。
- 未 commit，未 push。
