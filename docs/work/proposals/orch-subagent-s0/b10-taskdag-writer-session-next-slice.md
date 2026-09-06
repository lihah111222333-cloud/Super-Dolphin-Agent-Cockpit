# B10 taskdag writer session：下一切片候选（planning only）

Status: Candidate / isolated / not wired to production

日期：2026-09-03。本文是基于 2026-09-03 B10 只读 inventory 的实现规划，**不修改生产代码、不改变 Gate S0 状态，也不授予发布资格**。

## 1. 事实基线与问题边界

现有 B10 writer-epoch candidate 已定义 admission、DB-resident CAS、sticky stale 和
subprocess A/B 骨架，但明确没有接入 taskdag、Fx 或 shared product DB
（[b10-taskdag-writer-epoch-candidate.md](b10-taskdag-writer-epoch-candidate.md) §合同及
`cmd/mcp-orch/store/taskdagwriterfence/contract.go:1-45`、
`cmd/mcp-orch/store/taskdagwriterfence/owner.go:28-90`）。只读复核记录 taskdag 约有
42 个 mutation entrypoints；当前 `taskdag.Store`/Fx narrow ports 仍可能绕过候选，且
候选的普通 `BeginTx` 不等价于 taskdag 的 `BEGIN IMMEDIATE`/busy retry
（[candidate-revalidation-2026-09-02.md](../../plans/orch-subagent-upgrade/candidate-revalidation-2026-09-02.md#L90-L94)）。

当前 taskdag 的 composite `*store` 同时持有 `sqlc.DBTX` 与 query set，并由同一实例
提供多个窄端口（`cmd/mcp-orch/store/taskdag/store.go:21-61`、
`cmd/mcp-orch/store/taskdag/module.go:9-32`）。写事务通过
`sqlctx.WithImmediateTxOrReuse`/`WithImmediateTx` 进入 `BEGIN IMMEDIATE`，并由
`WithWriteRetry` 提供有界 busy/locked 重试（`cmd/mcp-orch/store/sqlctx/db.go:76-110`、
`cmd/mcp-orch/store/sqlctx/db.go:112-143`）。下一切片必须保留这些语义，而不是在每个方法
外面再包一层普通 `database/sql.BeginTx`。

产品表的现有边界是 `task_dags`、`task_dag_runs`、`task_dag_nodes`、
`task_dag_wakeups`、`task_dag_worker_leases`（`internal/platform/db/sqlite/migrations/001_baseline.sql:713-803`）。
RecoveryCustody 则已冻结为“外部单实例服务独占私有 SQLite DB”；orch 只能经认证协议
client 访问，不能打开、迁移或共享 authority 文件，也不能把该 DB 与 product DB 混用
（[recovery-custody-sqlite-authority.md](../../plans/orch-subagent-upgrade/recovery-custody-sqlite-authority.md#L9-L22)）。

## 2. 目标 seam：owner-aware、tx-aware writer session

### 2.1 形状（规划名）

引入一个 taskdag 内部的 `WriterSession` seam（名称可在实现评审时调整），其职责只有
三件事：

1. 在显式 admission 后读取并锁定 DB-resident `taskdag_writer_fence`，保存不可变
   `{schema_version, writer_epoch, deployment_fence}` token；缺失、未知版本、空白/NUL、
   非正 epoch 或 DB/config 不一致立即 fail-fast，禁止默认值。
2. 对每次 mutation 开启 `sqlctx.WithImmediateTx`，在业务 SQL 前于**同一连接、同一事务**
   执行精确 CAS：`singleton=1 AND schema_version=? AND writer_epoch=? AND
   deployment_fence=?`。影响行数不是 1 时返回 typed `ErrStaleWriter`，回滚且不调用
  业务 callback；实例转 sticky fenced/query-only。
3. 向 callback 暴露 tx-bound `sqlc.DBTX`/`*sqlc.Queries`，使已有
   `WithImmediateTxOrReuse` 在嵌套调用时复用同一事务；session 不重写 sqlc 语句，不
  引入第二个 store 或第二套 retry。

规划接口（非代码承诺）应表达“一个 owner、多个 transaction”：

```text
OpenWriterSession(ctx, db, admission) -> *WriterSession
WriterSession.WithMutation(ctx, func(tx TaskDAGTx) error) error
WriterSession.Advance(ctx, expected, next) error
```

`TaskDAGTx` 仅为内部构造 seam；它包装当前 `sqlc.DBTX`/`Queries`，并实现现有
composite store 的 tx-aware constructor。所有 direct entrypoint 都委托
`WithMutation`，而 composite operation（例如 assign+enqueue、retry+config patch、
complete+downstream、fail+cascade、terminate）在**一个** callback 内完成全部 SQL。

### 2.2 事务和重试不变量

- retry 的单位是完整的 `BEGIN IMMEDIATE -> fence CAS -> callback -> COMMIT`；只在
  `SQLITE_BUSY/LOCKED` 且 callback 尚未产生外部副作用时重试，达到有界次数或 context
  截止立即返回，不能无限等待。
- callback 只能进行 DB 读写；launcher/provider/网络/RecoveryCustody RPC 不得位于
  writer transaction 内。commit outcome unknown 后 session sticky query-only，禁止
  重跑业务 callback 或猜测提交结果。
- transaction reuse 必须保留现有 `*sql.Tx`/`immediateConnTx` 分支
  （`cmd/mcp-orch/store/sqlctx/db.go:59-104`）；不能把 tx-bound store 退化成裸
  `*sql.DB`，也不能在同一个 callback 再开第二个写事务。
- stale CAS 在 callback 前失败，所有业务表 zero rows；错误必须可由调用方按
  `errors.Is(err, ErrStaleWriter)` 判定，不能静默 fallback 到旧 writer。

## 3. migration / fence schema（设计输入，非本切片实现）

### 3.1 taskdag writer fence

下一 migration（编号待 release owner 裁决）应增加一个产品 shared DB 内的单例表，保持
B10 candidate 的名称和字段合同：

```text
taskdag_writer_fence(
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  schema_version INTEGER NOT NULL CHECK (schema_version = 1),
  writer_epoch INTEGER NOT NULL CHECK (writer_epoch > 0),
  deployment_fence TEXT NOT NULL CHECK (
    length(deployment_fence) BETWEEN 1 AND 512 AND
    trim(deployment_fence) = deployment_fence AND instr(deployment_fence, char(0)) = 0
  )
) STRICT
```

只允许一行；seed 必须在 migration owner 已取得强写锁后完成。`Open` 必须重读该行并
与 admission 完全比较；配置不能覆盖 DB 事实。`Advance` 以旧完整 token CAS 到严格更
高 epoch，旧 token 或重复/降级 epoch 返回 `ErrStaleWriter`/schema error。

### 3.2 migration leader / checkpoint

B10 接入不得掩盖 B18 migration 缺口。migration runner 需要单独的 bootstrap metadata
（不与 taskdag fence 混成业务表）：

```text
orchestration_migration_leader(
  singleton, schema_version, migration_name, script_sha256,
  owner_id, writer_epoch, lease_expires_at, checkpoint, updated_at
)
```

每个 step 在 `BEGIN IMMEDIATE` 内重读版本、脚本 hash、leader epoch 和 checkpoint；
stale/过期 leader 只能在确认旧连接释放后由 successor CAS 接管。脚本漂移、checkpoint
不明、commit unknown 或无法中断持锁进程均 fail-fast。该表只拥有 migration 进度，
不授权 taskdag mutation；taskdag writer fence 仍须在每一笔 mutation 中独立 CAS。

## 4. composite store 与动态 narrow ports 的收口

本轮先落地了一个不接线的 inventory 守卫：
[`cmd/mcp-orch/store/taskdag/writer_inventory.json`](../../../../cmd/mcp-orch/store/taskdag/writer_inventory.json)
与 `internal/archtest/TestTaskDAGWriterInventoryIsExactAndCandidateOnly` 双向冻结
M01–M42 的 production receiver/file/symbol 集合。该守卫只防止 writer entrypoint 漂移或
漏登记，清单状态仍为 `candidate_not_wired`，不表示下述 session 已经存在。

### 4.1 唯一 composite owner

`NewStoreFromDB`（`cmd/mcp-orch/store/taskdag/module.go:35-38`）在 production 图中改
为先构造一个 `WriterSession`，再由 `NewStoreFromWriterSession`（规划名）创建**唯一**
composite `*store`。`store` 不再把未受 fence 的裸 `*sql.DB` 作为可写依赖；读侧可以
继续使用同一查询集，但所有 42 个 mutation 必须经过 session。

`ProvideOrchestrationStore`、`ProvideNodeFlowStore`、`ProvideWakeupStore`、
`ProvideRunStore`、`ProvideScheduledStartStore`、`ProvideDispatchNodeStore`、spawn
recorder/lookup 等 provider 仍然只做同一 composite 的窄接口投影
（`cmd/mcp-orch/store/taskdag/module.go:53-87`）。禁止为每个窄口创建新的 DBTX、
session 或 writer epoch；动态 narrow port 的 type assertion 只允许指向该唯一实例。

### 4.2 42-entrypoint inventory（逻辑 mutation seam）

下表是本切片的封闭 inventory。每项在实现时必须登记 `owner=session`、事务入口、SQL
写表/查询、组合副作用和 stale 负测；新增或删除方法必须同时更新清单并使 static guard
失败，不能漏写“相似方法”。私有 helper 计入是为了防止 composite 操作绕过 session。

| 域 | M 编号与 entrypoint |
| --- | --- |
| DAG/template（12） | M01 `UpsertDAG`; M02 `DeleteDAG`; M03 `UpsertNode`; M04 `BatchUpsertNodes`; M05 `PatchNodeConfigIfUnchanged`; M06 `DeleteNode`; M07 `UpdateNodeStatus`; M08 `AssignNode`; M09 `AssignNodeAndEnqueueWakeup`; M10 `MarkDispatchIncompleteIfMissingWakeup`; M11 `UpdateDAGPatch`; M12 `BumpDAGVersion` |
| Run/node/event（9） | M13 `BindRunningNodeTurn`; M14 `TouchRunningNodeEvent`; M15 `UpdateRunningNodeStatus`; M16 `CompleteNode`; M17 `CompleteNodeAndScheduleDownstream`; M18 `ClaimNodeOutputMaterialization`; M19 `FailNodeAndCancelDownstream`; M20 `RecordNodeSpawn`; M21 `appendTaskDagRunEvent` |
| Run lifecycle（5） | M22 `CreateRun`; M23 `CloneNodesForRun`; M24 `PromoteRootNodesToReady`; M25 `TerminateRun`; M26 `UpdateScheduledDAGNextRun` |
| Wakeup（11） | M27 `EnqueueWakeup`; M28 `ClaimDueWakeups`; M29 `RenewWakeupLease`; M30 `MarkWakeupSent`; M31 `BindWakeupTurn`; M32 `RetryWakeup`; M33 `RetryWakeupWithNodeConfigPatch`; M34 `FailWakeup`; M35 `FailWakeupAndFailNodeAndCancelDownstream`; M36 `ReclaimStaleDispatchingWakeups`; M37 `MarkDispatchIncompleteNodesWithoutActiveWakeup` |
| Root scheduling（2） | M38 `ScheduleRootWakeups`; M39 `scheduleRootWakeup` |
| Worker lease（3） | M40 `AcquireWorkerLease`; M41 `RenewWorkerLease`; M42 `ReleaseWorkerLease` |

源码锚点：DAG/node 方法位于 `cmd/mcp-orch/store/taskdag/store.go:80-415`、
`cmd/mcp-orch/store/taskdag/store_dag_ops.go:56-129`；run/event 位于
`store_run.go:21-167`、`store_run_events.go:18-66`；wakeup 位于
`store_wakeup.go:48-499`；root/lease 位于 `store_root_wakeup.go:13-89`、
`store_lease.go:15-90`；downstream/spawn/output 位于
`store_complete_downstream.go:34-604`、`store_fail_downstream.go:27-223`、
`store_node_spawn.go:21-52`、`store.go:669-724`。实现前应以源码重新生成行号和 manifest，
不能把本表当作跳过精确审计的理由。

## 5. Fx graph 与 static bypass guard

### 5.1 Fx 接线目标

生产 `newMCPOrchApp` 当前同时装配 `platformdb.Module`、`canonicalOwnerModules()` 与
`taskdagstore.Module`（`cmd/mcp-orch/fx.go:42-75`）。下一切片的 Fx 设计要求：

```text
platformdb *sql.DB
  -> TaskDAGWriterAdmission (explicit config, no defaults)
  -> TaskDAGWriterSession (one owner)
  -> taskdag composite *store (one instance)
  -> dynamic narrow ports -> orchestration/dispatcher/run tools
```

`canonicalOwnerModules` 仍可构造 identity/event/wait owner
（`cmd/mcp-orch/fx_canonical_owners.go:11-18`），但不得产生第二个 taskdag writer。
RecoveryCustody client 只作为外部协议依赖；`sqliteauthority`/authority DB 不得出现在
该 Fx 图。

### 5.2 静态守卫（必须先 RED 后 GREEN）

实现批次必须新增架构守卫，至少检查：

- M01–M42 的调用图最终都到 `WriterSession.WithMutation`；生产 taskdag 文件不得直接
  调 `db.BeginTx`、`sqlctx.WithTx`、裸 `ExecContext` 写 product 表；允许的事务 helper
  只能在 session callback 或明确的 tx-bound adapter 内出现。
- 生产 Fx 中 `NewStoreFromDB`/`sqlc.New(*sql.DB)` 只能出现一次，且其返回值只能进入
  session-backed composite；禁止为 narrow port 重建 store/session。
- inventory manifest 与实际 receiver/SQL write symbol 双向相等：missing、duplicate、
  unknown、未登记私有 helper、绕过 composite 的 direct cast 均失败。
- `cmd/mcp-orch` 普通代码不得 import `recoverycustody/sqliteauthority`，不得通过路径、
  `ATTACH`、网络共享文件或 PostgreSQL 打开 authority DB；仅认证 RPC client 可被允许。
- 测试 fake 可以构造失败/竞态 fixture，但不能被 Fx production option 引用或把
  `Accepted=true` 当 writer proof。

## 6. subprocess stale epoch / hard cutover 验证

最小生成测试沿用候选 A/B 骨架：

1. 子进程 A 以 epoch N 打开 shared product DB，保留 RW connection 和 session。
2. 子进程 B 用完整 expected token `Advance` 到 N+1（或 freeze/revoke），提交后退出。
3. A 依次执行 M01–M42；每一项必须在 callback 前得到 `stale_writer`，业务 callback
   计数为 0，DAG/node/run/wakeup/lease/event 快照均 zero-row delta。
4. A 再次调用任一 mutation 必须 sticky `ErrAlreadyFenced`；不能通过旧 narrow port、
   新 goroutine、busy retry 或重开 `*sql.Tx` 继续写。
5. cutover 还须有 OS/file write-right revoke、旧进程 terminate 或新 DB namespace 的
   硬隔离证据。仅 TTL/active-binary lease、内存标志或“测试进程通常退出”不构成证明。

## 7. transaction parity / race acceptance matrix

| 验收面 | 必须覆盖 | 通过条件 |
| --- | --- | --- |
| 单项 parity | M01–M42 的成功、参数错误、not-found/CAS miss、affected-row 检查 | 与当前 sqlc 语义一致；提交后 read-back 相同；任何错误整笔回滚 |
| composite parity | assign+enqueue、retry+config patch、fail+wakeup+cancel、complete+downstream、terminate、scheduled start、batch upsert、run event append | 子步骤任一步失败都 zero partial rows；只使用一个 IMMEDIATE/fence transaction |
| tx reuse | `WithTx`、`WithDAGOpsTx`、`WithRunTx`、`WithScheduledStartTx` 及嵌套 `WithImmediateTxOrReuse` | 同连接/同事务；不死锁、不启动第二个 writer session；callback 只执行一次 |
| busy/retry | 两进程写压、`SQLITE_BUSY/LOCKED`、context deadline、commit unknown 注入 | 有界重试；超限 typed error；无重复业务写；连接和事务归还 |
| stale race | A(epoch N) 与 B bump N+1 并发覆盖所有 M01–M42 | A 全部 `stale_writer`、callback=0、zero-row delta；B 的 successor 写不被覆盖 |
| Go race/lifecycle | `go test -race`、并发 claim/lease、session close/shutdown、Fx start/stop | 无 data race、goroutine/连接泄漏；fenced session 只能 query/diagnostic |
| static/graph | inventory guard、Fx Validate/Populate、禁止 authority import/open | guard 与真实调用图双向通过；生产图只有一个 composite owner |

## 8. 失败与 NO-GO 边界

以下任一条件未满足，状态保持 `Candidate / NOT_VERIFIED`，不得接入 public tool、
dispatcher、provider 或 release gate：

- 42 项未全部经 session；任何“旧 store 作为 fallback”“只给 wakeup 加 fence”“仅
  包一层普通 BeginTx”的局部修补均 NO-GO。
- migration leader/fence schema 没有锁内重读、script hash/checkpoint、stale takeover
  和 crash/reclaim 证据；不得把 schema 创建成功当作 migration owner 完成。
- subprocess A/B 只测一个 mutation、只测内存 fake、只看错误字符串或只证明 lease
  过期，均不关闭 B10；必须有真实 shared SQLite、旧连接、全部 inventory 和 zero-row
  快照。
- Fx 存在第二个 taskdag composite、未受 fence 的 DBTX、动态 narrow port 绕过 guard，
  或 production 代码能直接打开 RecoveryCustody authority DB，立即 NO-GO。
- RecoveryCustody 外部 finalizer/verifier、签名 trust root、supervisor、网络/文件硬隔离
  尚未形成发布证据时，taskdag session 不能自行生成 custody authority，也不能把
  RecoveryCustody 私有 SQLite 与 product DB 合并来“简化事务”。两者是独立 owner、独立
  生命周期、独立故障域。

本规划完成后仍需 B10 blocker revalidation、真实跨进程硬切换、发布回滚与适用祖先 gate
证据；它不改变总计划 `Active`、B10 `needs_revalidation` 或任何 `NO-GO` 决策。
