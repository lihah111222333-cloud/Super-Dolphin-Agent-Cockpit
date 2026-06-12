# 00 Source Trace Risk Review

本文件是 15 个 SQLite 切换任务的二次评审结论。目的不是新增实现任务，而是给执行 agent 一个可复核的源码追溯入口：每个 P0/P1 风险都必须能从当前源码到达，并确认没有上层防护已经消除该风险。

## 裁决

- 风险可达：确认。当前产品运行时、mcp-orch、store SQL、dev/packaging 脚本仍直接依赖 PostgreSQL DSN、pgx/pgxpool、embedded PostgreSQL、PG-only SQL grammar、PG 行锁/advisory lock/jsonb semantics。
- 无上层防护：确认。现有防护大多是 PG schema version、PG generated sqlc guard、打包资源 guard、regex read-only query guard；它们不会把 SQLite 缺失的跨进程 claim/lock、JSON array append、只读 authorizer、PG env 透传风险变成安全行为。
- 文档修正：Task 02 必须显式以 `requiredBaselineTables` 作为 schema 下限，并通过 query 反推补齐额外 runtime 表；README 必须把本文件列入执行前置材料。

## 逐任务追溯

| 任务 | 可达源码 | 上层防护裁决 | 文档要求 |
| --- | --- | --- | --- |
| 01 SQLite 配置与平台运行时 | `internal/platform/config/config.go` 读取 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING`，调用 `embeddedpg.ResolveFromEnvironment` 并在缺省时 `exportDatabaseURLIfMissing`；`internal/platform/db/module.go` 的 `NewPool`、`registerLifecycle` 仍启动 embedded PG。 | 无。配置与 DB module 是启动根路径，后续 store 只消费其产物；当前 fail-fast 只保证 PG DSN 不为空，不会阻止 PG runtime 继续成为产品依赖。 | Task 01 必须移除产品 DB 对 PG env 的依赖，SQLite path/PRAGMA/schema gate 必须 fail-fast。 |
| 02 SQLite baseline schema | `internal/platform/db/module.go` 的 `requiredBaselineTables` 是当前 baseline 下限；根 `sqlc.yaml`、`cmd/mcp-orch/sqlc.yaml`、`sql/queries/**`、`cmd/mcp-orch/sql/queries/**` 还引用更多持久化表。 | 无。`VerifyMinSchemaVersion` 只看版本；PG baseline partial check 不能证明 SQLite 表、索引、约束完整。 | Task 02 必须从源码反推表全集，`requiredBaselineTables` 是最低集合，不允许按手写清单静默遗漏。 |
| 03 sqlc 与 DB 边界 | `internal/store/module.go`、`internal/platform/db/tx.go`、`cmd/mcp-orch/store/sqlctx/db.go` 仍以 pgx/pgxpool/pgtype 为主接口；generated sqlc 文件包含 PG bind/cast 类型。 | 无。store 调用层没有通用 SQLite adapter；绕过 sqlc 或手写 string SQL 会破坏现有 guard。 | Task 03 必须定义唯一 SQLite DB/Tx/sqlc 边界、`BEGIN IMMEDIATE`、busy/locked bounded retry、JSON/timestamp helper。 |
| 04 日志/状态/偏好 store | `sql/queries/system_log.sql`、`audit_log.sql`、`bus_log.sql`、`ai_log.sql`、`ui_preference.sql` 使用 `NOW()`、`ILIKE`、`regexp_match`、`::text/jsonb`。 | 无。sqlc 生成和运行期都会碰到 PG-only grammar；上层 UI/API 不能兜住查询失败。 | Task 04 必须重写这些 query 并保持大表 projection/index。 |
| 05 prompt/command/shared/feedback store | `sql/queries/prompt_template.sql`、`command_card.sql`、`shared_file.sql`、`agent_feedback.sql` 等使用 jsonb、tags `?`、`ILIKE`、`NOW()`。 | 无。prompt/command 结果 shape 是上层契约，但不提供 DB 方言兼容层。 | Task 05 必须把 JSON text 校验、scope 查询、LIKE 转义和资源 SQL 同步写清。 |
| 06 thread/binding/cwd/turn store | `internal/store/thread/module.go` 从 `pgxpool.Pool` 创建 store；`sql/queries/agent_thread.sql`、`cwd_lock.sql` 使用 jsonb、`ANY($1::text[])`、`NOW()` 和时间窗口条件。 | 无。线程/绑定/锁是启动与会话绑定的核心路径，没有跨进程上层互斥替代 DB 原子性。 | Task 06 必须覆盖 snapshot、binding、cwd lock、turn dedupe 的条件写和 retry。 |
| 07 cron claim 并发 | `sql/queries/cron_job.sql` 的 `ClaimDueJobsForUpdate` 依赖 `FOR UPDATE SKIP LOCKED`；`internal/module/cron/scheduler.go` 注释和调用路径明确依赖该语义。 | 无。scheduler 层依赖 DB claim 保证多进程不重复执行；执行后去重不能阻止并发领取。 | Task 07 必须用 SQLite 原子 UPDATE/RETURNING 或事务等价实现，并做多 worker 压测。 |
| 08 hook/interaction/dbquery | `internal/store/hookstore/hookstore.go` 已是 generated sqlc backed；`internal/archtest/migration_sqlc_guard_test.go` 锁定 `TestHookstoreUsesGeneratedSQLC`；`internal/store/dbquery/executor.go` 仅用 regex/allowed table 校验并调用 `OpenReadOnlyRows`。 | hookstore 有 generated-sqlc guard；dbquery 无完整上层防护。regex 没有 SQLite authorizer，不能充分阻断 `ATTACH`、危险 PRAGMA、函数副作用或解析绕过。 | Task 08 必须保留 hookstore sqlc guard，并把 dbquery 改成 SQLite authorizer + allowlist + fail-closed。 |
| 09 prompt recall topic lock | `sql/queries/prompt_template_sections.sql` 的 `LockRecallTopicInCWD` 使用 `pg_advisory_xact_lock(hashtextextended(...))`；`internal/module/prompt/template_sections.go` 在 recall 写路径调用。 | 无。应用层没有跨进程 topic mutex；没有等价锁会导致同一 CWD/topic 重复扫描或重复 upsert。 | Task 09 必须用 SQLite lock row/unique claim 等价替代 advisory lock，并验证跨 goroutine/进程。 |
| 10 mcp-orch runtime SQLite | `cmd/mcp-orch/runtime.go` 直接创建 `*pgxpool.Pool`，调用 `platformdb.NewPool`、`VerifyMinSchemaVersion`，并把 pool 注入 sqlc queries。 | 无。mcp-orch 是独立进程/sidecar，不能依赖主进程 store 已经安全。 | Task 10 必须让 mcp-orch 无 `DATABASE_URL` 启动并共享同一 SQLite 文件与 schema gate。 |
| 11 mcp-orch DAG 核心 store | `cmd/mcp-orch/sql/queries/task_dag_*.sql`、`workspace_run.sql`、`prompt_template.sql`、`command_card.sql`、`shared_file.sql`、`task_ack.sql` 使用 jsonb、`ILIKE`、`NOW()`、PG casts；store 层直接消费 generated sqlc。 | 无。工具/RPC 输出 shape 不是 DB 方言防护；若 query 未迁移，DAG 核心读写会直接失败或语义漂移。 | Task 11 必须覆盖 DAG/node/workspace/resource/task_ack 全部 query，并保持 root/mcp-orch 同步契约。 |
| 12 mcp-orch wakeup/lease/events/locks | `task_dag_wakeup_dispatch.sql` 依赖 `FOR UPDATE SKIP LOCKED`；`task_dag_dag.sql` 有 `pg_try_advisory_lock`；`task_dag_run.sql` 用 `jsonb_build_array`、`jsonb_array_length` 保证 events 追加和截尾。 | 无。fencing 字段只保护 claim 后更新，不能替代选择阶段的原子 claim；JSON event guard 锁的是 PG 语义，不保护 SQLite text append。 | Task 12 必须在 Task 10/11 后实现 claim/lease/advisory/event 的 SQLite 等价和高并发回归。 |
| 13 PG runtime 与打包移除 | `run-new-ui-desktop.sh`、`scripts/package_linux.sh`、`scripts/package_macos.sh`、`scripts/package_windows.ps1`、`verify_packaged_app_*`、`.env.packaging.example`、`internal/platform/runtimeenv/**` 仍写入/验证 embedded PG resource、`SUPER_DOLPHIN_POSTGRES_*`、`pg_ctl`、`initdb`。 | 无。打包 guard 当前会要求 PG 资源存在；这不是 SQLite 安全网，而是发布阻断残留。 | Task 13 必须移除 PG bundle/runtime manifest/env leakage，并更新 dev script、README/codemap/reset 文档。 |
| 14 发布 gate 与回归压测 | 现有 `.github`/`scripts` 搜索未发现 `sqlite-release-gates`；现有 package guard 大量验证 PG bundle。 | 无。没有跨 OS SQLite package smoke、mixed write pressure、query plan/N+1 gate 时，性能和打包回归可直接进入 release。 | Task 14 必须新增 CI gate、G11 mixed write、online backup/integrity/query plan/package smoke。 |
| 15 集成扫描与最终验证 | 全仓仍可通过 `rg` 找到 PG env、pgx、embedded PG、PG-only SQL、package PG manifest 等残留。 | 无。单任务验收只能覆盖局部，最终需要仓库级残留扫描和真实 OS packaging evidence。 | Task 15 必须汇总源码扫描、codemap、release gate artifact、path redaction 与 packaging smoke，不允许跳过。 |

## 执行约束

- 任何任务若发现本表中的源码路径已经变化，必须先重新追溯当前源码，再修改实现；不得把本文件当作过期豁免。
- 如果某个风险被声称已有上层防护，PR 必须给出源码路径、并发/失败模式测试和为什么能覆盖跨进程场景的说明；否则按未防护处理。
- 任务实现不得新增“静默兼容 PG”或“SQLite 失败后回落 PG”逻辑；迁移目标是产品运行时硬切 SQLite。
