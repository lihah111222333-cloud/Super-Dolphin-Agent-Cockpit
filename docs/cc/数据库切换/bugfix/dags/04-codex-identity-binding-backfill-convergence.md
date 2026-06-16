# Task 04: CodexIdentity binding/backfill 收敛

## Agent Prompt

你负责修复 MR !52 P2 的 binding 和 auto-resume backfill 部分：已有 raw alias binding 在同一 identity tuple 下必须被修正为 canonical realpath；auto-resume backfill 必须写回 canonical identity；历史读取继续使用 binding 的 `CodexHome`，但任务验收要证明写入/回填后的 binding 已经 canonicalize。不要修改 thread lifecycle 持久化，那是 Task 03。

## Scope

依赖：`02-codex-identity-resume-request-canonicalization.md`。

可并行：Task 02 合并后，可与 `03-codex-identity-thread-persistence-convergence.md` 并行。

建议 task worktree：`.worktrees/mr52-bugfix-task-04-codex-binding-backfill`

## 源码追溯

- `internal/module/thread/binding_registration.go` 的 `bindingNeedsCodexIdentityUpdate()` 只允许空值首次填充，不修正已有 alias。
- `internal/module/thread/binding_registration.go` 的 `verifyThreadBinding()` 会用 expected 值校验 binding，需要 expected 保持 canonical。
- `internal/module/thread/lifecycle_helpers.go` 的 `readMessagesHistoryRequestForSession()` 使用 binding `CodexHome` 找历史，因此 binding 写入必须先收敛。
- `internal/provider/unified/session_resolver_auto_resume.go` 的 `backfillAutoResumeCodexIdentity()` 使用 resolved request/session identity 写回，需要回归测试锁住 canonical backfill。

## 修改点

- Modify: `internal/module/thread/binding_registration.go`
  - 允许把 existing raw alias 修正为 incoming canonical home，但只在 instance key/model provider 相同或 existing 对应字段为空时允许。
  - 不允许非空 instance key/model provider 被不同值覆盖。
  - `verifyThreadBinding()` 的 expected 值应保持 canonical。

- Test: `internal/module/thread/binding_registration_test.go`
  - 新增 existing alias + incoming canonical 的更新测试。
  - 新增 instance key/model provider 冲突不覆盖测试。
  - 新增 history read 输入测试或断言：写入后的 binding `CodexHome` 已是 canonical，因此 history request 使用 canonical home。

- Test: `internal/provider/unified/session_resolver_identity_test.go` 或相邻 auto-resume 测试文件
  - 新增 auto-resume backfill 测试：binding/runtime 提供 symlink alias，driver resolver 返回 canonical request，backfill 写入 canonical home。
  - 若测试证明当前代码已满足，只提交测试即可；若失败，只允许修改 `internal/provider/unified/session_resolver_auto_resume.go` 的 identity 选择顺序，不允许改 provider codexapp driver/pool。

## 不允许改

- 不要修改 `internal/module/thread/lifecycle.go`。
- 不要修改 `internal/module/thread/start_session.go` 或 `start_session_helpers.go`。
- 不要扫描或批量重写所有历史 binding。
- 不要在 history read 每次调用时临时 canonicalize 来掩盖 binding 未收敛。
- 不要允许 instance key/model provider 的非空冲突被覆盖。

## 性能要求

- alias 修正只在注册或更新当前 binding 时执行。
- auto-resume backfill 只处理当前 binding。
- 不允许为了比较 alias 扫描全表 binding。

## 验收方案

1. 每个 Go 文件修改后运行单文件 guard：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/thread/binding_registration.go
```

2. focused tests：

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/thread ./internal/provider/unified -run 'CodexIdentity|AutoResume|Binding|History' -count=1
```

3. 验收标准：

- existing raw alias binding 会被修正为 canonical realpath。
- instance key/model provider 冲突不被覆盖。
- auto-resume backfill 写回 canonical realpath。
- history read 使用的 binding 已是 canonical。
- 本任务 diff 不包含 lifecycle/start_session 和 provider codexapp 修改。

## Review Checklist

- 生产就绪性：旧 raw alias 不再长期留在 binding/backfill 状态。
- 性能：没有全表扫描或热路径 realpath。
- 风险：只修正等价 alias，不覆盖不同 identity。
- 可维护性：binding/backfill 不复制 provider driver 逻辑。
- 测试充分性：binding update、conflict、auto-resume backfill、history 输入覆盖到位。
