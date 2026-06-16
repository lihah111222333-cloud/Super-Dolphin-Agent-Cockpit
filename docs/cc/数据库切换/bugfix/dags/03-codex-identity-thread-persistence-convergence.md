# Task 03: CodexIdentity thread 持久化收敛

## Agent Prompt

你负责修复 MR !52 P2 的 thread 持久化部分：start/resume 写入 thread state 和 runtime config 前，`CodexHome` 必须使用 Task 02 创建的 helper 收敛到 canonical realpath。不要修改 binding 注册，不要修改 provider unified backfill；这些属于 Task 04。

## Scope

依赖：`02-codex-identity-resume-request-canonicalization.md`。

可并行：Task 02 合并后，可与 `04-codex-identity-binding-backfill-convergence.md` 并行。

建议 task worktree：`.worktrees/mr52-bugfix-task-03-codex-thread-persistence`

## 源码追溯

- `internal/module/thread/lifecycle.go` 的 `resolveStartedSessionCodexIdentity()` 已经通过 `contract.ResolveCodexIdentity()` 得到 start canonical identity。
- `internal/module/thread/lifecycle.go` 的 `persistResumedSession()` 当前优先使用 `req.CodexHome`、`state.CodexHome`，可能保留 raw alias。
- `internal/module/thread/start_session_helpers.go` 的 `buildStartStoredThreadConfig()` 会把 session runtime identity 合入 stored runtime config，需要和持久化 identity 保持同值。

## 修改点

- Modify: `internal/module/thread/lifecycle.go`
  - 在 `persistResumedSession()` 生成 `threadState` 前，使用 Task 02 的 `canonicalizeCodexIdentityFields()` 规范化选中的 `codexHome/codexInstanceKey/codexModelProvider`。
  - 如果 req/state/session 中有 partial identity，返回错误，不持久化半规范化 thread state。
  - start 路径继续使用 `resolveStartedSessionCodexIdentity()` 的 canonical 结果，不新增第二套 start 解析逻辑。

- Test: `internal/module/thread/codex_identity_persist_test.go` 或 `internal/module/thread/resume_test.go`
  - 新增 resume 持久化测试：binding/state/runtime 提供 symlink alias，resume 后 thread runtime config `codexHome` 是 realpath。
  - 新增 start 持久化不回退测试：start runtime config 已有 explicit invalid/partial identity 时必须 fail-fast，不写入默认 Codex home。
  - Windows 下 `os.Symlink` 不可用时，只 skip symlink 专用用例；partial identity 和非 codex 用例不能 skip。

## 不允许改

- 不要修改 `internal/module/thread/binding_registration.go`。
- 不要修改 `internal/provider/unified/**`。
- 不要新增或修改 provider codexapp driver/pool 逻辑。
- 不要在 history read 每次调用时临时 canonicalize。
- 不要扩展 `internal/module/thread/codex_identity_canonical.go`；如果 Task 02 helper 不够用，退回 Task 02 修正后再继续。

## 性能要求

- 每次 start/resume 持久化最多 canonicalize 一次当前 identity。
- 不允许扫描 binding 表，不允许在列表、消息读取、历史读取热路径做 realpath。

## 验收方案

1. 每个 Go 文件修改后运行单文件 guard：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/thread/lifecycle.go
```

2. focused tests：

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/thread -run 'CodexIdentity|Resume|Start|Persist' -count=1
```

3. 验收标准：

- resume 后 thread runtime config `codexHome` 是 canonical realpath。
- start 路径保持使用 `resolveStartedSessionCodexIdentity()` 的 canonical 结果。
- partial identity fail-fast，不写半规范化 thread state。
- 本任务 diff 不包含 binding 注册和 provider unified 修改。

## Review Checklist

- 生产就绪性：thread state 不再长期保留 raw alias。
- 性能：没有热路径 realpath。
- 风险：没有绕过 Task 02 helper，没有覆盖不同 identity。
- 可维护性：持久化边界复用同一 contract helper。
- 测试充分性：resume persist、start persist、partial identity 都覆盖。
