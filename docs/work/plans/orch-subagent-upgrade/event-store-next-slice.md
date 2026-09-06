# ID-2 Canonical dense EventStore：内部实现切片

Status: Active

日期：2026-09-01；承接 [身份/事件下一切片](identity-event-next-slice.md)。
本文件描述已验证的 canonical event owner 候选，不把旧 direct-peer/terminal/DAG 事件表或内存 bus 当作同一事实源。

## Owner 与数据边界

`cmd/mcp-orch/store/eventstore` 是独立的 dense event writer。它持久化 source/lane watermark、ingress receipt、canonical event、dedup tombstone 和 projection cursor；不写 `orchestration_direct_peer_*`、terminal outcome、DAG ring、report 或 UI 表。
`FenceVerifier` 是构造器必需的只读依赖，负责把 reservation、incarnation、binding 的真实 fence 与事件信封逐字段比较；没有默认成功实现。`IdentityFenceVerifier` 现在由 canonical owner Fx 模块构造并直接读取 ID-1 owner；`taskeventsource.Producer` 也已作为唯一 event appender 窄口构造，但 runtime event producer 调用链、receipt/custody 仍未接入。
事件正文、prompt、凭据和 provider 配置不进入表；有内容的事件只保存 CONTENT `content_ref/content_size/media_type`，`content_role=none` 必须明确为空。

为后续原子 caller 评审，`eventstore.Store` 另提供未接线的 Tx-compatible seam：
`GetSourceStateTx` 只读取调用方事务中的既有水位，`AppendTx` 直接复用同一事务内的
canonical append 状态机，`WithTransaction` 提供 IMMEDIATE owner callback 和 callback
成功后的 commit-unknown replay barrier。`sqlctx.RequireTransactionDBTX` 只接受真实
`*sql.Tx`/内部 IMMEDIATE transaction，拒绝裸/typed-nil/未知 DBTX；该 seam 不分配首个
sequence、不跳过 identity fence，也不接 Fx、provider、custody 或 public wait。

## Dense append 语义

每个 `(source_id, source_epoch, lane)` 有独立 `next_sequence`、`materialized_sequence`、`dedup_floor` 和 revision。
新 source 从 `next_sequence=1/materialized_sequence=0` 开始；同一 source_id 看到未注册的其它 epoch 直接返回 fence 错误，不自动迁移或猜测 epoch。

```text
seq=2  -> ingress receipt(status=staged), source(status=gap), no canonical event
seq=1  -> seq=1 + staged seq=2 原子 materialize, source next=3/materialized=2/active
same tuple + same hash -> original receipt/event replay, no new row
same tuple + different hash -> receipt/source quarantine, original event never overwritten
```

append 以 SQLite `BEGIN IMMEDIATE` 串行化；receipt、canonical event、receipt status、source watermark 在同一事务中提交。连续 drain 有 1024 条上限，超出时保留 staged/gap，不盲目扩大事务。
低于 dedup floor 的请求先查 tombstone：同 hash 返回 `ErrEventReplayExpired`，异 hash 返回 conflict；不因仍保留的旧 receipt 而重新 materialize。

## Cursor 与 tombstone

projection cursor 需要显式 `EnsureCursor`，缺 source 不补建。`AdvanceCursor` 带 expected revision，只能推进到 source 已 materialize 的序列，旧 revision 或越过 source 水位均 fail-fast。
`RecordTombstone` 只按 `dedup_floor+1` 连续推进，必须回读对应 materialized receipt 的 event/hash/cursor；它保留哈希和稳定 cursor，但本切片不执行物理 GC，因为内容保留/擦除属于 CONTENT owner。

2026-09-04 增量：EventStore 增加 opt-in、transaction-bound `RetentionPinVerifier` seam。
`RecordTombstone` 在同一 `BEGIN IMMEDIATE`/`DBTX` 中先调用 verifier，再插入 tombstone 和推进
`dedup_floor`；verifier 错误整体回滚并 fail-closed。`retentioncandidate.Verifier` 以 SQLC
bounded query 读取 pending AccessWait handles 与最新 wait-operation target heads，校验
floor equality、状态和 overflow；现有 constructor 不自动装配 verifier，仍没有 production
retention index、物理 GC、跨 daemon owner 或 Fx wiring，因此只是候选边界。

## 字段生产消费链

```text
ID-1 reservation/incarnation/current binding
  -> FenceVerifier
  -> EventEnvelope (source fence + trust/content refs)
  -> IngressFrame (source epoch/lane/sequence/hash/cursor)
  -> ingress receipt
  -> canonical event
  -> source watermark / projection cursor / tombstone
```

row mapper 在公开读取时验证 lane、版本、时间、哈希、NULL incarnation 形状、content 引用和水位关系；字段守卫动态比较 JSON、SQLC row、schema 列和 mapper 生产位置。
哈希格式和稳定 cursor 已强制，但本切片不实现 JCS canonicalization、签名、恢复域外 custody 或 provider event-id conformance；`envelope_hash` 仍是上游 canonicalizer 提供的摘要输入。

## 已执行验证

| 验证 | 实际结果 |
| --- | --- |
| `TestEventStoreMigrationContract` | 历史 host-light 观测曾 exit 0（架构前置136.085s、业务0.062s）；当前本机迁移测试通过；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段。覆盖 129 五表列/PK/FK/STRICT、NULL/绑定形状、同序 hash、canonical 不变、gap/tombstone/cursor 约束 |
| `TestEventStoreContract` | 历史 host-light 观测曾 exit 0（架构前置240.708s、业务1.524s）；当前本机候选测试通过；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段。覆盖 seq2→seq1 drain、same/different hash、writer/status/source-epoch fence、ID-1 真实 fence verifier、event-id conflict、重开、跨连接并发、cursor CAS/同值重放、dedup floor/tombstone 重放、旧投影零写入和 mapper guard |
| SQLC | 固定 sqlc v1.30.0；`make sqlc-generate-worktree`/`verify-shape` 通过，生成 event/identity/retention 查询无残留非法 `sqlc.arg`；exact tracked verify 仍受宿主 GitRunner/dirty output 边界阻断 |
| 规划/结构 | `make orch-upgrade-check` 当前 exit 0（Python 29 tests 与 dependency-map check）、LSP diagnostics、`git diff --check` 当前通过；官方 `--light-guard-only` 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |

上述均是隔离 worktree 的本机 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 证据；没有远程恢复域、签名发布制品或真实 provider effect 调用。
本次重跑使用工作树专用 `GOCACHE/GOTMPDIR`，避开一次共享 Go cache 中间文件缺失；该环境故障没有计入业务失败。

## 当前未闭合项与下一步

1. EventStore 仍未接 direct-peer/wakeup/DAG/terminal 生产写链；旧 writer 仍可独立写旧表，不能宣称重复 provider effect 已修复。Fx 只完成 owner 构造，不自动把旧 writer 升级为 canonical producer。
2. 需要将 `orchestration_agent_events` 接到 ID-1 finalize/close event 的同一 owner seam，或实现跨 owner compare/receipt；当前 SQLite 表仅提供 FK，不能假装跨 owner 原子。
3. 需要真实 recovery custody、source epoch rollover、JCS/hash key 管理、CONTENT/RESOURCE policy、gap quarantine/egress capability 撤销和跨进程 crash/replay 证据。
4. 在这些祖先完成前，不启用 `wait_agents`、跨任务授权、Room fanout 或 native provider bridge；它们只能消费 canonical event cursor，不能另建 outbox owner。
5. 独立 `retentioncandidate.Verifier` 已能在同一事务中读取 pending AccessWait handles 与
   wait-operation target heads，证明 floor/pin equality、bounded scan、pending unknown 保留和
   terminal release；仍不能把它或 `RetentionPinReader` 接口当作 production pin 事实源，
   还缺 production index/GC、跨 daemon owner 和 Fx wiring。

## 2026-09-04 watcher/index 增量（candidate）

`eventstore.Watcher` 现提供显式有界 registration/buffer。匹配事件只能是已
`materialized` 且通过 frame 校验的 canonical event；通知采用非阻塞发送，订阅 buffer 溢出即
关闭并返回 `ErrWatcherOverflow`，调用方必须回到 durable cursor/poll 路径，不得把丢失通知当作
事件丢失或成功。watcher 不启动无主 goroutine，也不成为持久化、transport 或 wait owner。

`retentioncandidate.Verifier` 及 `BuildIndexSnapshotTx` 在 EventStore tombstone/floor
推进的同一 transaction-bound DBTX 中读取 AccessWait 与 wait-operation candidate，执行
bounded count、source/epoch/lane/floor、生命周期和 overflow 校验；它只能返回 pinned 或
unverified blocking plan，不持久化 index、不执行物理 GC、不签发 custody/checkpoint receipt。
构造器仍未自动装配 retention verifier，当前状态为 `Draft`、`LOCAL_NON_AUTHORITATIVE`
`NOT_VERIFIED`、`NO-GO` for production retention wiring。
