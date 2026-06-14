# SQLite 切换集成分支收口评审

评审日期：2026-06-14

评审对象：`codex/sqlite-switch-integration`

评审 HEAD：`32ee0ef112412042312b8228e7a7734129358060`

评审 worktree：`C:\Users\ai03\Desktop\Super-Dolphin\.worktrees\sqlite-switch-readiness-doc-final`

## 裁决

当前集成分支不能裁定为发布 / RC 就绪，也不能声明“全面切换 SQLite 且无应用功能失效”。

可以确认的正向事实：

- `task-01` 到 `task-15` 的分支 HEAD 均已进入当前集成分支历史；`task02` 是主干直接提交，其余任务大体保留为 merge 侧枝。
- 最新集成 HEAD 包含运行时 SQL 修复提交 `32ee0ef1 sqlite: unblock runtime SQL writes`。
- 针对该修复面的 Go 回归验证通过：`go test ./internal/store/thread ./internal/store/prompt ./internal/store/commandcard ./internal/store/sqlc ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration -count=1`。
- 当前 HEAD 重新运行 SQLite release gate 后，G1-G11、G13、G14 为 PASS。

仍然阻断完成裁决的事实：

- P0 gate G12 仍为 FAIL；不能把本地 release gate 结果记为全绿。
- 仓库 guard 仍失败：`internal/module/prompt` 包文件数为 31，超过上限 30。
- `docs/cc/数据库切换/task15-integration-final-scan-report.md` 仍不存在；虽然 task15 分支 HEAD 已进入集成历史，但最终扫描报告工件没有落盘到预期路径。
- 当前只取得本地 Windows/amd64 证据，没有 Windows/macOS/Linux 三平台 packaging smoke artifact。

## 当前验证证据

| 命令 | 结果 | 说明 |
|---|---|---|
| `go mod download` | PASS | 新建文档收口 worktree 后的依赖准备。 |
| `go run ./scripts/sqlite_release_gates -logs .tmp/final-release-logs -timeout 10m` | FAIL | G12 为 FAIL，其余 gate 见 `sqlite-release-gate-report.md`。 |
| `go test ./internal/store/thread ./internal/store/prompt ./internal/store/commandcard ./internal/store/sqlc ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration -count=1` | PASS | 覆盖 `32ee0ef1` 修复涉及的 store/sqlc/taskdag/orchestration 面。 |
| `go run ./scripts/code_size_guard.go` | FAIL | `internal/module/prompt: 31 个 > 上限 30`。 |
| `Test-Path docs/cc/数据库切换/task15-integration-final-scan-report.md` | FALSE | 最终扫描报告工件缺失。 |
| `git merge-base --is-ancestor codex/sqlite-switch-task-15-integration-final-scan HEAD` | PASS | task15 分支 HEAD 已在当前集成分支历史中。 |

## G12 失败归因

G12 命令：

```bash
go test -v ./scripts -run TestPackageLinux\|TestPackageMacOS\|TestMacOS\|TestPackageWindows\|TestSQLiteReleaseGatePackageSmokeRuntime\|TestSQLiteReleaseGatePackageSmokeCommands -count=1
```

在文档收口 worktree 中失败，主要错误为 WSL 内的 git 无法识别 Windows git worktree 的 `.git` 指针：

```text
fatal: not a git repository: /mnt/c/.../sqlite-switch-readiness-doc-final/C:/Users/ai03/Desktop/Super-Dolphin/.git/worktrees/sqlite-switch-readiness-doc-final
```

最小复现：

```bash
wsl.exe bash -lc "pwd; git rev-parse --show-toplevel"
```

在 Windows 创建的 git worktree 中，`.git` 文件内容是：

```text
gitdir: C:/Users/ai03/Desktop/Super-Dolphin/.git/worktrees/sqlite-switch-readiness-doc-final
```

WSL 内的 git 将 `C:/...` 当作相对路径拼接，导致 shell packaging 测试在真正进入脚本逻辑前失败。

为排除“只是该 worktree 坏了”，又在普通本地 clone 中运行同一个 G12 命令：

```text
C:\Users\ai03\Desktop\Super-Dolphin\.tmp\sqlite-g12-normal-clone-20260614224240
```

普通 clone 中不再出现 worktree `.git` 指针错误，Linux/macOS/Windows packaging 多数子测试通过，`TestSQLiteReleaseGatePackageSmokeRuntime` 也通过；但仍有一个 Linux packaging guard 测试因 WSL 启动横幅 / 编码噪声前缀导致严格输出断言失败：

```text
TestPackageLinuxCopyModelRegistryFailsFastWhenSourceMissing
```

因此，当前判断为：

- G12 的本地失败不是 SQLite runtime 回落 PostgreSQL 的证据。
- Windows package runtime smoke 子测试已经创建 SQLite 并通过。
- 但 G12 作为 P0 gate 的形式结果仍是 FAIL；在修复测试环境适配、剥离 WSL 横幅噪声，或在 CI/非 worktree 环境取得干净 PASS 前，不能裁定 release gates 全绿。

## 任务与提交拓扑

当前集成分支保留了任务 worktree 的提交记录：

| Task | 分支 HEAD | 当前状态 |
|---|---:|---|
| task01 | `7fdcb61e` | 已进入集成历史，merge 侧枝。 |
| task02 | `dddbbdaf` | 已进入集成历史，主干直接提交。 |
| task03 | `fab67985` | 已进入集成历史，merge 侧枝。 |
| task04 | `47a8994b` | 已进入集成历史，merge 侧枝。 |
| task05 | `0f04d164` | 已进入集成历史，merge 侧枝。 |
| task06 | `e689f2f2` | 已进入集成历史，merge 侧枝。 |
| task07 | `2263ec8f` | 已进入集成历史，merge 侧枝。 |
| task08 | `65fded5f` | 已进入集成历史，merge 侧枝。 |
| task09 | `78b7a230` | 已进入集成历史，merge 侧枝。 |
| task10 | `6ee043a2` | 已进入集成历史，merge 侧枝。 |
| task11 | `5161e6c7` | 已进入集成历史，merge 侧枝。 |
| task12 | `825e1b20` | 已进入集成历史，merge 侧枝。 |
| task13 | `e6b0717e` | 已进入集成历史，merge 侧枝。 |
| task14 | `9fb1e382` | 已进入集成历史，merge 侧枝。 |
| task15 | `378d2a22` | 已进入集成历史，分支指向 Task14 merge 后的集成点。 |

PR 合并时如果要保留该“蜈蚣腿”拓扑，应使用 merge commit。Squash 会压扁任务记录，rebase 会改写拓扑。

## 仍需处理

1. 处理 G12 的本地 Windows/WSL 测试适配问题，或在 CI/干净普通 clone/目标平台取得 G12 PASS 证据。
2. 明确 `task15-integration-final-scan-report.md` 是否仍是必须交付物；如果是，需要补落盘并评审。
3. 处理 `internal/module/prompt` 包文件数 guard，或由仓库维护者明确批准规则/结构调整。
4. 取得 `make build-plain` 或等价构建证据。
5. 补齐 Windows/macOS/Linux 三平台 packaging smoke artifacts。
6. 在所有 P0 关闭后重新运行 release gates，并用干净 HEAD 更新 `sqlite-release-gate-report.md`。

## 当前结论

当前集成分支已经包含 SQLite 切换的主体实现和最新 runtime SQL 修复，但发布证据链尚未闭合。可以继续作为集成分支推进；不应作为“全任务完成、无应用功能失效、可进入 RC”的最终结论提交。
