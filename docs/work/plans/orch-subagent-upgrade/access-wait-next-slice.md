# AccessGrant/EventWaitHandle：授权与可恢复等待下一切片

Status: Active

日期：2026-09-04；承接 [Gate S0](README.md)、[身份/事件下一切片](identity-event-next-slice.md) 与 [Canonical EventStore](event-store-next-slice.md)。
本文件只记录 migration 130、`cmd/mcp-orch/store/accesswait` 和内部聚合等待收口器的 owner/API 边界。owner 已进入 standalone/legacy Fx 构造图，但尚未发布或启用 `wait_agents`、跨任务授权或 Room 协议；本机 fixture 也不等于生产验收。`tasktransport`/`unifiedv2` 的新增授权与 target-source 语义现已有 additive durable-owner 适配，但仍未接入 public tool 或 production task listener。

> 2026-09-04 增量：AccessWait 已增加 transaction-bound Tx owner seam，内部
> [wait-operation 下一切片](wait-operation-next-slice.md) 已完成 migration 136 的 Plan/target
> provisioning candidate。下方较早的验证表保留作历史观测；最新命令结果以
> [current snapshot（2026-09-04）](current-snapshot-2026-09-04.md)为准。

## 2026-09-04 Tx 增量（仍为 candidate）

- `CreateGrantTx`、`GetGrantTx`、`CreateWaitTx`、`GetWaitByKeyTx`、`GetWaitByIDTx`、`CancelWaitTx`、
  `RevokeGrantTx` 只接受 transaction-bound `DBTX`，拒绝裸/typed-nil/未知实现，不开启嵌套
  事务；非 Tx API 复用同一 owner 实现。
- `CreateWaitTx` 在新幂等键插入前于同一 transaction-bound `DBTX` 重读 canonical source
  watermark/status，拒绝 missing、cursor-ahead、dedup-floor、gap/quarantined/closed source；
  已存在且身份完全匹配的 exact replay 仍可只读返回原句柄。
- `retentioncandidate.Verifier` 作为独立 candidate 读取 pending wait handles 和最新
  wait-operation target heads；只有 `after_source_sequence == expected_floor` 的非终态行会
  阻断 tombstone，扫描超过 32 项或生命周期/身份异常则 fail-closed。该 verifier 尚未接入
  EventStore Fx 或 production retention index/GC。
- wait-operation `ProvisionTarget` 在一个 SQLite `BEGIN IMMEDIATE` 中按 durable
  `issued_at/deadline` 创建 grant、wait 和 append-only target facts；并发重放只有一个
  grant/wait，失败整体回滚，重启后按原 idempotency tuple 查询。
- migration 136 的 selector JSON 与 AccessWait 既有 header-only 表示保持一致，`wait_status`
  使用 canonical `pending`；source/target fence、cleanup continuity、aggregate stability 和
  exact marker 均由 SQL/Go 双重校验。
- 这些接口未进入 Fx/provider/public registry；Resume/Cleanup 已有内部 owner candidate，
  wait-operation marker 也仅为 opt-in schema/owner candidate；retention、真实跨重启
  commit-unknown receipt 和 aggregate `wait_agents` 仍未验收。

## Owner 与持久化边界

`accesswait.Store` 是 `AccessGrant` 与 `EventWaitHandle` 的唯一候选写 owner，并由 `canonicalOwnerModules` 在两个 orch Fx 图中构造；`internal/platform/db/sqlite/migrations/130_orchestration_access_wait.sql` 建立两张 `STRICT` 表及其索引、外键和触发器：

- `orchestration_access_grants` 是不可变授权快照。它固定 caller principal/scope、source/target agent fence（reservation、incarnation、binding version/digest、source fence ref）、`can_read`/`can_send` 方向、幂等摘要、request digest 和 issued/expiry 时间窗。metadata、capability 和身份 fence 不能原地修改；撤销/过期只能用 expected-revision CAS，历史行不能删除。
- `orchestration_event_wait_handles` 是不可变 selector/cursor 的 durable 生命周期。它绑定 grant revision、caller/scope、target fence、单一 `source_id/source_epoch/lane`、排序后的 event types、`after_sequence/after_cursor` 和 deadline；匹配事件只保存 event id、source sequence、canonical cursor，不保存正文。`pending` 只能 CAS 到 `satisfied`、`timed_out`、`cancelled`、`revoked` 或 `expired`，历史行不能删除。
- 两表 FK 与 relation trigger 确认 ID-1 reservation/incarnation 的组合关系；`accesswait.Store` 构造时强制要求支持同一 DB 事务连接的 transactional `IdentityReader`，并在授权线性化点逐字段重读 active current binding，同时通过带 source-status 的 `EventReader` 读取 canonical dense EventStore。FK、非空 evidence ref 或句柄 ID 都不是授权凭证。

该 owner 不写 `orchestration_identity_*`、canonical event、direct-peer message/event/outbox、terminal outcome、DAG/UI/bus 或 CONTENT 正文表；也不签发 permit/token、消费 nonce、提交 provider effect 或生成 delivery receipt。

## API 语义

### AccessGrant

1. `CreateGrant` 先校验有界文本、双端 bound fence、至少一个方向、幂等摘要和时间窗，再重读 source/target current binding；同幂等 namespace/key 且同 request digest 只返回原记录，摘要不同返回 `ErrAccessConflict`。申请本身不代表已授权。
2. `GetGrant`/`GetGrantByKey` 只是读取快照，不会从 `agent_id`、目录、展示名或 wait handle 猜测权限。
3. `Authorize` 每次受保护 read/send 都重读 grant 和两端当前 binding，依次检查 caller principal、scope、capability、active/expiry、完整 fence 相等性；revoked/expired/stale incarnation 一律 fail-closed，不返回可升级的隐式权限。
4. `RevokeGrant` 带 expected revision 在同一写事务中撤销 grant，并把该 grant 下仍为 pending 的 wait handles 收敛为 `revoked`；`ExpireGrant` 由显式安全时间 owner 以 CAS 将 grant 和 pending handles 收敛为 `expired`。旧 revision 重放不得复活 grant。

### EventWaitHandle

1. `CreateWait` 只接受 active grant 的当前 revision 和 read capability；selector 必须固定一个 source/epoch/lane/target，event types 严格排序且去重，`after_sequence>0` 时必须携带 canonical `after_cursor`。deadline 有 60 秒上限，服务端可生成 opaque handle id。
2. `GetWait`、`PollWait`、`CancelWait` 每次都重新检查 caller/scope/grant revision 与 target current fence；句柄本身不是 bearer capability，撤权后不能继续读。
3. `PollWait` 只通过带 source-status 的 `EventReader` 查 canonical dense event（单页最多 200 条、总扫描页数有界），从 `after_sequence` 之后选择第一个 selector 匹配事件，并用 handle revision CAS 固化唯一 `satisfied` 结果和 `matched_canonical_cursor`。缺失 source 返回 `ErrWaitNotFound`；`after_sequence` 超过 materialized watermark 或低于 dedup floor 分别返回 `ErrWaitCursorAhead`/`ErrWaitReplayExpired`；没有事件且未到 deadline 返回 `pending`；到期 CAS 为 `timed_out`；canonical gap/quarantine 映射为稳定的 `ErrWaitGap`/`ErrWaitQuarantined`，不能当 warning 继续。
4. `Wait` 是有界轮询（25ms 间隔、最多至句柄 deadline）；重启后用同一 `PollRequest` 读取 durable handle，不依赖进程内 waiter。`CancelWait` 只能结束 pending，terminal 句柄重放返回既有结果，不能改写事件事实。

持久化恢复还会逐项比较 selector JSON、规范化列和 selector digest；任一不一致都立即返回 `ErrInvalidWait`，不能靠列覆盖或重新计算摘要吞掉篡改。

### 内部聚合等待

`cmd/mcp-orch/orchestration/waitaggregate` 是只依赖 `Waiter` 窄端口的内存收口器，不是第二个持久化 owner，也不注册 public tool。它并发等待最多 32 个固定 target，要求所有 target 使用相同 caller/scope，支持 `any`、`all`、`quorum` 和带 typed predicate 的 `first_success`。predicate 只检查 event type 与 canonical content metadata（不读取正文、不执行表达式）；未知错误、gap、quarantine 和扫描上限归为 `indeterminate`，明确 revoked/expired/terminal 才能进入 definitive failure。达到条件时只取消兄弟 context，不调用 `CancelWait`，因此 durable handle 可由调用方用原 cursor 继续查询。

聚合结果严格区分 `satisfied`、`timeout`、`aggregate_failure` 和 `indeterminate`；timeout 不是业务失败，pending item 保留原句柄。该包当前只有内部合同测试和固定缓存证据，不能替代 unified `wait_agents` 的 selector/fence、command-submission 分支、content ACL、HTTP/stdio watcher 或 public manifest 验收。

## 字段生产消费链与禁止越界

```text
configured local operator issuer -> per-initialize CallerPrincipal session snapshot
  -> AccessGrant request + source/target current fence
  -> orchestration_access_grants (CAS lifecycle)
  -> EventWaitHandle selector + canonical cursor
  -> canonical EventStore EventReader
  -> satisfied/timeout/cancel result
  -> report/UI/wait projection (待接入)
```

`caller_principal_ref` 必须来自受验证的 principal owner。production standalone 配置现在强制提供 canonical `principal_ref/scope_ref`，在读取 credential、获取单实例锁、打开数据库或发布 ready 前校验；同一 server-boot issuer 使用可失败的加密随机 auth context，并为每个 initialize 签发新的短期 CallerPrincipal session 快照。HTTP 客户端 metadata 不能改变该快照，bearer 内容、路径、agent/thread ID 也不参与身份推导。该 issuer 只证明 local operator transport identity，不签发 child/task 专属身份，也不会自动把 operator 扩权为任意 agent。事件有正文时只能携带 CONTENT `content_ref/content_size/media_type`，不得由 wait 读取器直接拼正文。若等待后触发命令，必须另经 OUT 的 canonical command intent、receipt、permit/nonce 和 unknown-effect 处理；AccessGrant 或 EventWaitHandle 不得代替 OUT。Room 必须另有成员/方向/撤权/partial/关闭和恢复合同，共享 daemon、共享 workspace 或一个 grant 不扩大 Room 私有历史权限。

本轮 `unifiedv2` wait candidate 将 target selector 的 `SourceID/SourceEpoch` 绑定到
`grant.Target` 的 canonical source，并保留 caller/source fence 的独立校验；
`accesswait.Store` 现允许两种明确形状：兼容的 `grant.Source` selector，或仅对
`CanRead=true`、完整 bound target fence 且 target tuple 精确匹配的 target selector。
每次授权/建句柄/轮询仍重读 caller/source/target current binding，伪造 source、target、
epoch、subject、撤权或 rollover 在 EventReader 前 fail-closed。该适配只解决 owner
合同冲突，不代表 public `wait_agents`、child credential、CONTENT/OUT/custody 或
跨任务生产能力已启用。

旧 `CommandAdmission/CommandResult`、`SubmitAuthorizedCommand` 和测试 fake `Accepted=true` 已删除。`accesswait` 现在只产出完整 `AccessAuthorization`；未装配的 `commandintent.Assembler` 在组合层消费 transport principal、该判决和 ID binding，再交给 migration131 profile store。没有 Fx/public tool、recovery receipt 或 `command_submission` 查询面，不能称为 canonical OUT admission。

## 验证状态（本机候选，非发布授权）

| 检查 | 本次文档更新时的实际状态 |
| --- | --- |
| `TestAccessWaitMigrationContract`（migration 130 schema/trigger/CAS） | 本机 SQLite 测试 exit 0；relation trigger 与全终态 CAS 已覆盖。当前 host wrapper 新一轮因宿主 CPU 门拒绝，未取得受信 Ubuntu/Wine 证据；仍标 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。 |
| `TestAccessWaitStoreContract`（幂等、principal/fence、wait restart/gap/cancel、旧表零写） | 本机固定缓存测试 exit 0；覆盖 omitted-id replay、>200 分页扫描、event-type filter、deadline、grant expiry、stale target、target-source selector、unknown source、cursor watermark、transactional identity 和 cancel CAS race。旧 fake command-admission matrix 已删除；当前 host wrapper 新一轮因宿主 CPU 门拒绝，未把本机结果提升为发布 PASS。 |
| `TestWaitAggregateContract`（any/all/quorum/first_success、predicate、timeout/indeterminate、兄弟取消） | 最终实现的本机固定缓存与 race 测试、代码守卫均通过；此前 host light 业务 run 在最后一次 runtime-safe 启动入口微调前通过，未将其当作当前版本的 host PASS。证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，仅证明内部内存收口器，不证明 durable watcher、public tool 或 provider 事件接线。 |
| `CallerPrincipal` / local HTTP session binding | 历史 host-light 观测曾 exit 0（archtest 189.280s、业务 0.016s、资源门 low/low/low）；当前本机 common/standalone 定向测试仍通过，但官方 wrapper 受未提交 baseline drift 阻断。该 issuer 仍只是 local operator，不是 child/task principal。 |
| SQLC/静态守卫 | 固定 sqlc v1.30.0 的生成与 verify-shape exit 0；`make sqlc-verify-worktree` 已生成/后处理但 exact Git tracked-state 检查受宿主权限与 visible-but-untracked 输出阻断；light guard 最终为 31 项 production-ratchet findings。 |
| canonical owner Fx/build | `go build -p=1 ./cmd/mcp-orch` exit 0；Fx Validate/Populate 构造测试本机通过。production serve 已把 configured issuer 注入 LocalHTTPProfile；仍无 event producer、CONTENT/OUT receipt 或 public accesswait tool。 |
| standalone child principal、CONTENT/OUT/Room、跨进程 recovery E2E | 尚未接入或尚未执行；不得写成 ST-08/ST-10/ST-12 已通过。 |

所有可执行的本机尝试均应标记 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，除非未来取得受信环境、真实 binary/provider receipt 和可追溯发布制品证据。`indeterminate`、gap、quarantine、缺 principal 或缺 canonical event 都必须保持 fail-closed，不能用 mock/fake provider 补齐证据。

## 生产退出条件与当前缺口

本切片仍为 Active。退出前必须同时证明：

- standalone production Fx 已显式装配同一 `IdentityRepository`、canonical `EventStore` 和 `accesswait.Store`，但仍需证明没有 legacy writer 或第二套 waiter owner；
- child/task principal issuer 将受验证 child connection/credential 映射为不可伪造的 `caller_principal_ref`，并覆盖伪造身份、跨 run/incarnation、过期/撤权 fence 的零副作用负测；local operator issuer 不能替代该项；
- migration131 profile 与未装配 assembler 已完整传递 caller/authorization/source/target/idempotency draft；仍须补 CONTENT/RESOURCE/custody producer、production adapter 与 `command_submission` 查询面；内部候选不满足退出项；
- CONTENT 提供正文引用的保密性、生命周期与擦除 receipt；OUT 提供 command/permit/nonce、provider receipt、unknown effect 与恢复域外 custody；Room 提供逐目标授权、fanout/partial、撤权、关闭和恢复；
- canonical event cursor 能跨 daemon crash/restart 恢复，旧 incarnation follow、gap quarantine 和 custody ack 均有真实跨进程证据；
- 远程/发布环境和签名制品完成对应 ST-08、ST-10、ST-12 验收。

在这些条件满足前，AccessGrant/EventWaitHandle 只能作为内部候选 owner；不得启用 `wait_agents`、跨任务 send、Room fanout 或把本机 wrapper 的结果提升为生产能力。Room 目前仅由 concrete `DisabledRoomCapabilityGate` 提供 fail-closed 接缝，未注册任何 Room tool。

## 到期 owner candidate（2026-09-04）

`accesswait.Store` 另提供 candidate-only 的有界到期扫描：调用方显式传入经安全时间
owner 验证的 UTC instant，SQLC 只读取 `active` grant 的 `grant_id/revision/expires_at`
并强制有限 `LIMIT`，随后复用 `ExpireGrant` 的 revision CAS 逐条收敛 grant 与 pending
wait。竞争中的 revoke/expiry 只做只读终态复核，不重试写入；取消、deadline、坏时钟和
坏行均 fail-fast。扫描与 CAS 均可在 SQLite close/reopen 后继续，不依赖内存 waiter。

`GrantExpiryRunner` 仅由显式构造器接收 `Clock`，没有 Fx、`unified_v2` 或 public tool
接线，也不提供 wall-clock fallback。当前生产仍缺少 RecoveryCustody 提供的可认证、可
持久化 security-time owner；该 runner 和 `Clock` 注入因此只能作为本机候选证据，不能
宣称统一安全时间或发布就绪。
