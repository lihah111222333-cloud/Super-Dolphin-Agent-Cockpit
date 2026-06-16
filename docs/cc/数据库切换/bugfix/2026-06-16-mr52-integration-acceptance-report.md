# MR !52 Bugfix Task05 集成验收报告

日期：2026-06-16  
执行者：xhight-codeagent  
Worktree：`C:\Users\ai03\Desktop\Super-Dolphin\.worktrees\mr52-bugfix-task-05-integration-acceptance`  
分支：`codex/mr52-bugfix-task-05-integration-acceptance`  
HEAD：`057b4f9e`

## 任务范围

本次 Task05 只执行集成验收与记录，不新增业务 Go 修复，不提交、不合并、不推送。本报告是 Task05 新增的唯一文件。

Task05 原文预期 final diff 不包含 migration/generated；但 Task04 review 后确认生产 SQLite 旧库 trigger 问题为真阳性，前序任务已通过新增 `106_agent_provider_binding_codex_home_alias_repair.sql`、真实 store 升级测试和 sqlc 注释同步闭环。因此本报告把 migration/generated/sqlc 相关 diff 标为 Task04 review 后的范围扩展，不视为 Task05 新增业务修复。

## 实际运行命令

1. `git status --short --untracked-files=all`
   - Exit code：0
   - 摘要：无输出；生成本报告前 worktree 无未提交/未跟踪文件。

2. `git diff --stat origin/main...HEAD`
   - Exit code：0
   - 摘要：30 files changed, 2634 insertions(+), 311 deletions(-)。

3. `git diff --numstat origin/main...HEAD`
   - Exit code：0
   - 摘要：覆盖 bugfix docs、SQLite open/test、thread CodexIdentity helper/resume/persist/binding/tests、provider unified tests、Task04 扩展的 migration/sqlc/store binding 范围。

4. `& 'C:\Program Files\Go\bin\go.exe' test ./internal/platform/db ./internal/platform/config -run 'NewDBRejects|ResolveSQLitePathRejectsParentFile|SQLitePath' -count=1`
   - Exit code：0
   - 摘要：`internal/platform/db` 2.752s；`internal/platform/config` 1.618s。

5. `& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/thread ./internal/provider/unified ./internal/provider/codexapp -run 'CodexIdentity|Resume|AutoResume|Pool|Start|Binding|Persist|History' -count=1`
   - Exit code：0
   - 摘要：`internal/module/thread` 1.939s；`internal/provider/unified` 1.139s；`internal/provider/codexapp` 2.340s。

6. `git diff --check origin/main...HEAD`
   - Exit code：0
   - 摘要：无 whitespace error。

7. `rg -n "chmod|fallback|default|CanonicalAppManagedCodexHome|EvalSymlinks|OpenFile" internal/platform/db/sqlite internal/module/thread internal/provider/unified internal/provider/codexapp`
   - Exit code：0
   - 摘要：命令因 `default/fallback` 覆盖面较宽产生既有匹配；未发现新增 `chmod`。补充 diff-only 扫描显示新增 `OpenFile` 写探测在 `internal/platform/db/sqlite/open.go` 打开期，`EvalSymlinks` 只出现在 Codex identity 冷路径注释/测试和 provider 既有边界，未发现为通过验收新增 silent fallback。

8. `& 'C:\Program Files\Go\bin\go.exe' test ./internal/platform/db/sqlite ./internal/store/binding ./internal/store/sqlc -run 'RunMigrations|SQLiteUpsert|CodexIdentity|Alias|Immutable|Codex' -count=1`
   - Exit code：0
   - 摘要：`internal/platform/db/sqlite` 2.808s；`internal/store/binding` 2.184s；`internal/store/sqlc` 2.222s。

9. `C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/db ./internal/platform/db/sqlite ./internal/contract ./internal/store/binding ./internal/store/sqlc ./internal/module/thread ./internal/provider/unified ./internal/provider/codexapp -run 'NewDB|SQLitePath|RunMigrations|SQLiteUpsert|CodexIdentity|Resume|AutoResume|Pool|Start|Binding|Persist|History|Alias|Immutable|Codex|ResolveSQLitePath' -count=1`
   - Exit code：0
   - 摘要：raw go test entry guard passed；代码守卫全部通过；`internal/archtest` 通过；所有列出的目标包测试通过。

10. `& 'C:\Program Files\Go\bin\go.exe' test ./internal/platform/db ./internal/platform/config ./internal/module/thread ./internal/provider/unified ./internal/provider/codexapp ./internal/platform/db/sqlite ./internal/store/binding ./internal/store/sqlc -run 'NewDBRejects|ResolveSQLitePathRejectsParentFile|SQLitePath|CodexIdentity|Resume|AutoResume|Pool|Start|Binding|Persist|History|RunMigrations|SQLiteUpsert|Alias|Immutable|Codex' -count=1 -v`
    - Exit code：0
    - 摘要：辅助提取 skip；发现 7 个 skip，详见下一节。

11. `git diff --name-only origin/main...HEAD | rg -n "baseline|guard|generated|migrations|sqlc|sql/queries"`
    - Exit code：0
    - 摘要：命中 migrations、sqlc、sql query；未命中 guard baseline。

12. `git diff -U0 origin/main...HEAD -- internal/platform/db/sqlite internal/module/thread internal/provider/unified internal/provider/codexapp | rg -n "chmod|fallback|default|CanonicalAppManagedCodexHome|EvalSymlinks|OpenFile"`
    - Exit code：0
    - 摘要：diff-only 复核未发现新增 chmod；`OpenFile` 为 SQLite 打开期写探测；`CanonicalAppManagedCodexHome` 为既有默认 Codex identity 注入路径重排，不用于吞掉显式 invalid identity。

13. `git diff --name-only origin/main...HEAD | rg -n "internal/archtest|baseline\.json|guardlib|generated"`
    - Exit code：1
    - 摘要：无匹配；本分支 diff 不含 guard/archtest baseline。

## Skip 情况

有 skip。

| 测试 | 包 | 原因 | 结论 |
| --- | --- | --- | --- |
| `TestNewDBRejectsUnwritableExistingParentWithRedaction` | `internal/platform/db` | Windows writable 行为由 ACL 检查和 read-only file attribute 覆盖。 | 平台差异 skip，未掩盖 P1 read-only DB 文件打开期测试。 |
| `TestServiceStartAllowsDeferredClaudeProviderUUID` | `internal/module/thread` | 当前环境 Claude disabled。 | 非 CodexIdentity 核心验收项，由 broad `Start` 正则匹配到。 |
| `TestPersistResumedSessionCanonicalizesStoredCodexIdentity` | `internal/module/thread` | Windows 缺少 Developer Mode 或 `SeCreateSymbolicLinkPrivilege`。 | symlink alias 专项 skip，符合任务允许的 Windows 权限 skip。 |
| `TestResumeSessionCanonicalizesCodexIdentityBeforeProvider` | `internal/module/thread` | Windows 缺少 Developer Mode 或 `SeCreateSymbolicLinkPrivilege`。 | symlink alias 专项 skip，符合任务允许的 Windows 权限 skip。 |
| `TestResolveResumeRequestCanonicalizesCodexIdentityFromSymlinkSources` | `internal/module/thread` | Windows 缺少 Developer Mode 或 `SeCreateSymbolicLinkPrivilege`。 | symlink alias 专项 skip，符合任务允许的 Windows 权限 skip。 |
| `TestStartSessionNormalizesExplicitCodexHomeBeforeMirrorAndPool` | `internal/provider/codexapp` | Windows symlink privilege unavailable。 | provider 边界 symlink 专项 skip。 |
| `TestBuildPoolSpawnCmdUsesAbsoluteSystemShellForFDLimit` | `internal/provider/codexapp` | Windows 不使用 Unix fd-limit wrapper。 | 非本 MR 验收项，由 broad `Pool/Start` 正则匹配到。 |

## Diff 范围摘要

- 文档：新增/更新 MR !52 bugfix 总计划和 DAG 任务文档。
- SQLite：`internal/platform/db/sqlite/open.go` 增加已有 DB 打开期写探测；`internal/platform/db/module_config_test.go` 增强 P1/P3 回归。
- Thread CodexIdentity：新增 `codex_identity_canonical.go` 和 resume/persist/binding 相关测试；修改 start/resume/persist/binding 入口，使 Codex identity 在冷路径收敛。
- Provider unified：新增 auto-resume/backfill Codex identity 覆盖。
- Store/SQLite migration/sqlc：Task04 review 后扩展，包含 `106_agent_provider_binding_codex_home_alias_repair.sql`、`001_baseline.sql` trigger 同步、`sql/queries/agent_provider_binding.sql` 注释同步、sqlc generated 文件同步和真实 store 升级测试。
- 未发现 guard threshold、`internal/archtest/baseline.json` 或 guard baseline 变更。

## P1 验收结论

1. `NewDB(&config.Config{SQLitePath: existingReadOnlyDB})` 返回非 nil error。  
   结论：通过。SQLite focused tests exit 0；`open.go` 在打开期通过 `probeExistingDatabaseWritable()` 使用 `os.OpenFile(path, os.O_RDWR, 0)` 探测已有 DB。

2. error 不包含完整 DB path，只包含 redacted basename。  
   结论：通过。SQLite 错误路径使用 `redactPath()` 与 `securefs.SafeErrorForPath()`；相关 redaction 测试随 `NewDBRejects` 正则通过。

3. 错误发生在 `sqlite.Open()` 打开期，不依赖 migration 或业务 SQL。  
   结论：通过。写探测位于 `prepareFilesystem()` / `validateExistingDatabasePath()`，发生在 `sql.Open()`、PRAGMA 与 migration 前。

4. Windows ACL/只读测试继续通过；Unix/macOS 通过显式 `O_RDWR` 探测。  
   结论：通过，但本机有一个 Windows parent-writable 平台差异 skip；read-only DB 文件和 focused tests 均通过。

## P2 验收结论

1. 使用真实目录和 symlink alias 作为 `codexHome` 时，provider 前 `CodexHome` 为 realpath。  
   结论：有条件通过。代码通过 thread helper 复用 `contract.ResolveCodexIdentity()`；本机 symlink 专项测试因 Windows 权限 skip，非 symlink canonical/clean alias 测试通过。建议在具备 symlink 权限的环境补跑专项测试。

2. resume 的 `req.Config["codexHome"]`、thread runtime config、binding `CodexHome` 收敛到同一 canonical realpath。  
   结论：通过。thread focused tests 和 guard wrapper exit 0；相关覆盖包括 resume config、persisted runtime config、binding repair、history input。

3. 已有 binding 存 raw alias 且 instance key/model provider 相同时，会被修正为 canonical realpath。  
   结论：通过。`TestBindingRegistrationPersistsCodexIdentity`、`TestSQLiteUpsertAllowsCodexHomeAliasRepairForSameTuple` 及 guard wrapper 覆盖通过。

4. 已有 binding 的 instance key/model provider 与 incoming 不同时，保持不可变冲突，不被覆盖。  
   结论：通过。`TestBindingRegistrationRejectsCodexIdentityTupleConflict`、`TestSQLiteUpsertRejectsCodexTupleConflicts` 覆盖通过。

5. history 读取继续使用 binding 中的 `CodexHome`，且 binding 写入或 backfill 时已 canonicalize。  
   结论：通过。`TestBindingRegistrationHistoryInputUsesCanonicalCodexHome` 和 auto-resume backfill 覆盖通过。

## P3 验收结论

1. `config.New()` 的 `SUPER_DOLPHIN_SQLITE_PATH=parentFile/secret.db` 仍报 parent redacted 错误。  
   结论：通过。`ResolveSQLitePathRejectsParentFile` focused tests exit 0。

2. 直接 `NewDB(&config.Config{SQLitePath: parentFile/secret.db})` 也报 parent redacted 错误。  
   结论：通过。`NewDBRejectsParentFile` focused tests exit 0。

3. error 不包含完整 parent path，不包含完整 leaf DB path。  
   结论：通过。redaction 测试随 SQLite focused tests 通过。

4. DB path 本身为目录时仍报 leaf DB path redacted 错误，不被父路径检查误吞。  
   结论：通过。`NewDBRejects` focused tests 覆盖通过。

## hight-reviewagent 评审状态

独立 `hight-reviewagent` 结论：PASS。

Findings：无生产阻断发现；允许 Task05 报告补录 PASS 后提交。

`hight-reviewagent` 验收摘要：

- P1：PASS。`NewDB()` 打开期进入 `sqlite.Open()`，已有 DB 通过 `os.OpenFile(path, os.O_RDWR, 0)` 探测可写性，错误经 `redactPath`/`SafeErrorForPath` 脱敏；未发现生产路径 chmod 或 fallback。
- P3：PASS。config 路径和 `NewDB()` 直调路径均覆盖父级普通文件，错误定位到 parent redacted path。
- P2：PASS。`ResolveCodexIdentity` 要求完整三元组并 canonical realpath；resume/start/persist/binding/backfill 均在冷路径写回 canonical identity，history/message 读取未新增 realpath 热路径。
- Task04：PASS。旧库 trigger 通过 `106_agent_provider_binding_codex_home_alias_repair.sql` 增量迁移闭环，真实 SQLite 测试覆盖旧 trigger 拒绝、迁移 marker、迁移后 alias repair 成功。
- Task05：PASS。报告准确记录命令、exit code、skip、diff 范围和 Task04 范围扩展。

Task05 review checklist 自检结果：

- 生产就绪性：SQLite 已有 DB 不可写在打开期 fail-fast；Codex identity partial/invalid 不被默认值静默吞掉。
- 性能：新增文件系统探测集中在 SQLite open、resume/start/persist/binding/backfill 冷路径，未看到 message/history 热路径扫描。
- 风险：binding tuple 冲突保持 immutable；alias repair 仅限同一 identity；未发现自动 chmod。
- 安全：错误路径使用 redaction；未发现新增完整路径泄露证据。
- 可维护性：thread 侧集中 helper 复用 `contract.ResolveCodexIdentity()`。
- 测试充分性：P1/P2/P3 focused tests、Task04 migration/store/sqlc 追加测试和 guard wrapper 均 exit 0；symlink 专项受 Windows 权限限制 skip。

## 未解决风险

已知环境限制：

1. Windows 当前缺少 symlink 创建权限，P2 的真实 symlink alias 专项测试在本机 skip；建议在启用 Developer Mode/`SeCreateSymbolicLinkPrivilege` 的 Windows 环境，或 Linux/macOS 环境补跑 symlink 专项测试。

未发现代码验收层面的阻塞风险；上述风险来自当前平台限制。
