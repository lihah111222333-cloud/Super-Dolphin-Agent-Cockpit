# Task 05: 集成验收与单评审

## Agent Prompt

你负责 MR !52 bugfix 的最终验收，不新增业务修复。必须在 Task 01、Task 03 和 Task 04 合并到 bugfix 集成 worktree 后执行，复核所有验收标准、运行 focused tests、检查 diff 范围，并组织一个 reviewagent 覆盖生产就绪性、性能、风险、安全、可维护性和测试充分性。

## Scope

依赖：

- `01-sqlite-open-failfast-diagnostics.md`
- `03-codex-identity-thread-persistence-convergence.md`
- `04-codex-identity-binding-backfill-convergence.md`

建议 task worktree：`.worktrees/mr52-bugfix-task-05-integration-acceptance`

本任务不应修改 Go 业务代码。若发现验收失败，停止交付并把失败反馈给对应任务重新修复。

## 必读材料

- `docs/cc/数据库切换/bugfix/2026-06-16-mr52-sqlite-codex-identity-bugfix-plan.md`
- `docs/cc/数据库切换/bugfix/dags/README.md`
- `docs/cc/数据库切换/bugfix/dags/01-sqlite-open-failfast-diagnostics.md`
- `docs/cc/数据库切换/bugfix/dags/02-codex-identity-resume-request-canonicalization.md`
- `docs/cc/数据库切换/bugfix/dags/03-codex-identity-thread-persistence-convergence.md`
- `docs/cc/数据库切换/bugfix/dags/04-codex-identity-binding-backfill-convergence.md`

## 验收步骤

- [ ] **Step 1: 检查 diff 范围**

```powershell
git status --short --untracked-files=all
git diff --stat
git diff --numstat
```

预期：

- 只包含 SQLite open/tests、thread CodexIdentity helpers/tests、必要 provider unified tests、bugfix docs。
- 不包含 generated sqlc、migration schema、guard threshold、baseline 更新。

- [ ] **Step 2: 运行 SQLite focused tests**

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/platform/db ./internal/platform/config -run 'NewDBRejects|ResolveSQLitePathRejectsParentFile|SQLitePath' -count=1
```

预期：exit 0。

- [ ] **Step 3: 运行 CodexIdentity focused tests**

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/thread ./internal/provider/unified ./internal/provider/codexapp -run 'CodexIdentity|Resume|AutoResume|Pool|Start|Binding|Persist|History' -count=1
```

预期：exit 0。Windows symlink 权限不足导致的 skip 必须只发生在 symlink 专用测试，不能掩盖非 symlink 断言。

- [ ] **Step 4: 运行静态差异检查**

```powershell
git diff --check
rg -n "chmod|fallback|default|CanonicalAppManagedCodexHome|EvalSymlinks|OpenFile" internal/platform/db/sqlite internal/module/thread internal/provider/unified internal/provider/codexapp
```

预期：

- 没有 whitespace error。
- `OpenFile` 写探测只在 SQLite open 期。
- `EvalSymlinks` 或 `ResolveCodexIdentity` 只在 start/resume/persist/backfill 冷路径。
- 没有新增 silent fallback 或自动 chmod 用户已有 DB/Codex home 的逻辑。

- [ ] **Step 5: 对照验收标准**

逐条确认总计划中的 P1/P2/P3 验收标准：

- P1：read-only DB 打开期 fail-fast，路径脱敏。
- P2：provider request、thread runtime config、binding 均为 canonical realpath；identity 冲突不可覆盖。
- P3：config 启动路径和 `NewDB()` 直调路径都能把 parent-file 错误定位到 parent redacted path。

- [ ] **Step 6: 单 review**

启动一个 reviewagent 审查最终 diff 和验收输出。该 reviewagent 必须回答：

- 生产就绪性：是否会进入半可用状态，是否 fail-fast。
- 性能：是否有热路径 filesystem realpath/open 探测。
- 风险：是否有身份误覆盖、路径泄露、权限自动修复或 fallback。
- 安全：是否泄露完整路径，是否扩大文件权限。
- 可维护性：helper 是否集中，是否复用 contract。
- 测试充分性：是否锁住 P1/P2/P3 原始症状和边界。

reviewagent 未通过时，本任务不允许交付。

## 最终报告要求

最终报告必须包含：

- 实际运行的命令和 exit code。
- 若有 skip，必须说明 skip 的具体测试名和原因。
- diff 范围摘要。
- reviewagent 的结论摘要。
- 未解决风险或明确“无未解决风险”。

## 不允许改

- 不要新增业务修复。
- 不要补写实现代码来绕过前序任务失败。
- 不要更新 generated 文件、migration schema、guard baseline。
- 不要使用 `--no-verify`、`git add .` 或宽泛提交。
