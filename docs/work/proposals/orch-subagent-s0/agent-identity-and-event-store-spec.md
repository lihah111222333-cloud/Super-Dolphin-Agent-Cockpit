Spec-ID: ID
Status: Draft
Depends-On: TRUST
S0-Owner: IdentityRepository and EventStore as the sole identity and append owners
S0-Scope: logical agent identity, execution incarnation, current binding, event lanes, and cursors
S0-Dependencies: TRUST
S0-Inputs: reservations, source-fenced identity observations, and canonical ingress envelopes
S0-Outputs: identity bindings, append-only events, dedup tombstones, and materialized cursors
S0-Failure: stale or malformed writes quarantine or enter reconciliation_required with zero projection mutation
S0-Recovery: replay the original source lane and sequence after restart; never renumber or create an identity
S0-Field-Chain: reservation -> execution identity -> incarnation fence -> event envelope -> projection cursor
S0-Named-Acceptance: RED-ID-01 DuplicateIdentityOrIncarnation; GREEN-ID-02 EventDenseAppendCrashReplay

# Agent 身份与追加式事件存储子规格

## 0. 定位与边界

本文件是 Gate S0 的独立实施子规格，不是已批准实现，不表示当前 worktree 已具备 all-kind identity 或可恢复事件闭环。
它细化总规格 §1.1、§1.6、§1.8、§1.9、§5.1/5.2、§7、§8 及 §19 Gate S0 的第2项 ID 节。
TRUST 未批准时，本规格只能保留 provenance/trust/content-role 字段，不得把任何 provider 输出升级为 control event。
目标是让 logical `agent_id`、不可变 external execution incarnation、current binding、append-only event 和 ingress cursor 各自只有一个可写 owner。
SQLite transaction 只覆盖同一 owner 的事实；bridge、provider、UI、A2A 和远程恢复域不被描述成跨进程原子事务。

## 1. Owner、存储与禁止旁路

IdentityRepository 是 reservation、execution identity/incarnation、binding preparation、all-kind current binding 和 close/finalize/abort CAS 的唯一写 owner。
EventStore 是 canonical event append、ingress source state、dedup/tombstone、materialization cursor 和 source-lane epoch 的唯一写 owner。
ContentOwner 写 encrypted content object/ref；ID 只保存 `content_ref` 与 CONTENT ticket/version，不写正文、DEK 或明文 staging。
Resource/OUT/Room owner 写各自事实并通过 typed event/cursor 消费 ID；node、report、UI、legacy binding 和 `agent_provider_binding` 都是只读投影。
任何 bridge/gateway/host callback 只能提交经过 source fence 的候选 observation 或事件请求，不能直接写 incarnation/current binding。
每个 owner 的 transaction 进入和 COMMIT 前都校验 writer/deployment epoch、security-time 和 expected generation；未知或 stale 立即 fail closed。
具体表名迁移、sqlc 入口及旧 writer inventory 必须由 implementation plan 冻结；本 Draft 不假设现有旧表已经满足 owner 合同。

### 1.1 Migration、fresh bootstrap 与 storage commit fence

迁移/启动输入必须显式携带 `database_id`、`schema_version_from/to`、migration script digest、`writer_epoch`、owner lease、security-time generation、deployment epoch 和稳定 `migration_operation_id`。fresh bootstrap 先在不依赖业务表的 bootstrap metadata 中建立单例 migration leader/fence，再创建 ID/EventStore 表；不得把空表或首次 `CREATE TABLE` 的成功当成 identity/event owner 已可用。已有数据库必须锁内重读当前 schema marker、script digest、writer epoch 和上一个 committed head，版本回退、script digest 漂移、foreign owner 或缺失 marker 均 fail-fast。

每个 migration step 和每批 event/identity 写入都使用 `BEGIN IMMEDIATE`（或等价强写锁）及生成式上限（rows、bytes、wall time）；输入超过任一上限立即回滚并返回 typed `storage_transaction_bound_exceeded`，不得拆成无 fence 的隐式续写。事务在进入和实际 storage COMMIT 边界都重读 writer/deployment epoch、schema marker、security-time 和 expected generation，并写 `storage_commit_fence={database_id,writer_epoch,transaction_generation,pre_head,post_head,script_digest}`。只有带该 fence 的 durable commit receipt 才能推进 migration checkpoint、source watermark 或 identity current；普通 SQLite `COMMIT`、WAL 可见或文件可读都不是提交证明。

输出必须是可按原 operation 查询的 `migration_receipt`：`status=committed|aborted|outcome_unknown`、from/to version、script digest、checkpoint、pre/post head、storage commit fence digest、availability/abort receipt。`committed` 才开放后续 writer；`aborted` 必须证明未留下新 schema/head；`outcome_unknown` 只允许查询原 operation，不得重新 claim、重复执行或猜测结果。

故障处理固定为：并发 migrator 只有一个 leader，其他请求在业务分配前返回 `migration_leader_conflict`；script/hash/schema drift 不执行 DDL；writer 在 prepare 后被 `SIGSTOP`/`SIGKILL` 时，successor 必须先取得更高 writer epoch，并证明旧连接已停止、未提交事务已由存储回滚，再从精确 checkpoint 重放。若无法在 bounded deadline 内证明 paused writer 已停止或回滚，返回 `migration_takeover_blocked`，不无限等待、不宣称接管。COMMIT 响应不明时保留旧 head 或查询原 receipt，禁止部分 marker、sequence hole、重复 migration 或跨 owner 双写。

## 2. Identity schema 与状态

logical reservation 先写 `agent_id`、immutable parent/root/kind/workspace/run scope、creation origin、reservation version 和 typed launch/source fence。
`execution_identity` 按 kind 为 `provider_session|native_subagent|managed_agent|a2a_remote_agent` 判别联合；identity 到 `execution_incarnation_id` 只能由 IdentityRepository 建立。
provider/native 分支固定 provider instance/session/epoch 及 child lifecycle 字段，managed 分支固定 V3 run、persisted runtime instance 与 supervisor，A2A 分支固定 gateway、remote identity/task/session 与 remote epoch。
native child 复用必须有 provider durable creation/lifecycle id 或可验证 source sequence；不能用本地计数、展示时间或 child id 猜新 epoch。
managed agent 在 spawn 前写 `persisted_runtime_instance_id`；provider 返回的 thread/session id 是 evidence，不替换该 identity。
所有 bound event、command、message、room item 和 projection 都携带完整 `execution_fence`；不适用的 kind 字段必须 absent。
`source_fence` 区分 `provider_bridge`、`managed_runtime`、`a2a_gateway`、`internal_owner`，并包含对应 generation/epoch；bridge generation 不是稳定 identity。
reservation 状态固定 `reserved|claimed|identity_pending|activation_pending|outcome_unknown|activated|rejected|failed|expired`，terminal 状态不可逆。
`agent.state` 只使用 canonical AgentState；`control_status` 和 `resource_status` 是正交投影，不能从 reservation status 猜 state。
provider 自发 child 先写 identity observation；无法安全关联 recover intent 时新建 agent，或进入 `provisioning/degraded/quarantined`，不得写 active incarnation。

## 3. Binding activation 三段协议

普通 launch/recover/replace 必须按 `binding_activation_preparation` 先写不可授权 `binding_prepared_non_authorizing`，不得直接改 current binding 或 activation event。
active-process owner 消费 prepared receipt 执行 `bind_current`，返回带 active-process head、process start identity、measurement 和 execution fence 的 current receipt。
IdentityRepository 随后在同一 SQLite transaction 重读 expected previous binding、active-current receipt 和 source fence，原子写 current binding、activation/close event，并把 preparation 标记 finalized。
finalize 与 `abort_binding` 只能竞争同一 preparation generation；abort 需要 active-process revoke/cleanup receipt，不能用超时猜测未绑定。
如果 previous binding、incarnation、active-process head 或 source fence 漂移，当前 transaction 不写 current；只有权威 revoke/supersede receipt 可进入 typed abort。
prepared、active-current 未 finalized、identity outcome unknown 均不授权 prompt/message/effect；可幂等 reconcile，不可由 node projection 补全。
首次 launch、provider discovery、active recover、closed relaunch 和 failed reservation retry 使用不同 source discriminator；nullable 字段不能用来猜分支。
stop 先收口 runtime-effect lease 和 active-process `current|prepared_non_authorizing -> revoking -> revoked`，再清 current binding 并写 last-closed；不能只删 binding。
active-source recover 必须完整走 same-family replacement；last-closed、failed reservation 和 first launch 才能走新 family prepare/bind/finalize。

## 4. Event envelope、append 与 cursor

canonical envelope 固定 `schema_version`、cursor、event_id、root agent、event type、source/ingress lane、source sequence、provenance/trust/content-role、content ref/size/media type、source/ execution fence 和 observed time。
provider timestamp 缺失时 `provider_at=null`，不能用 `time.Now()` 伪造；`observed_at` 只表达 sidecar 接收时间。
bound execution event 必须引用完整 incarnation fence；pre-incarnation reservation/observation event 沿用 reservation/observation discriminator，不能回填未来 fence。
EventStore 为每个 source/session epoch 建立独立 `control|output|content` lane；`source_sequence` 只由一个 durable dense allocator/log owner 分配。
serialized event envelope 上限为 256KiB；staging、batch、chunk 和磁盘配额必须由生成式 policy/RESOURCE snapshot 固定，禁止无界增长。
raw ingress 首要唯一键是 `(ingress_source_id, source_sequence)`；同 key 不同 envelope hash 是 protocol violation，低于 dedup floor 的 replay 拒绝。
append 必须原子持久化 sequence、hash、content ticket/ciphertext mapping、provider evidence mapping 与 next cursor；counter、spool 和 mapping 不能分离造成 hole。
只有连续 prefix 已被恢复域外 `recovery_custody/{security_domain_id}` 接管并取得 availability receipt 才能返回 custody ack；本机 SQLite/WAL 不足以证明 RPO=0。
materialized cursor 只能在按 lane 连续 materialization transaction 提交后推进；高 sequence hole 只能 staging，不得越洞发布事件。
projector/report/UI 以 source cursor 重放；不能按 wall clock、最后写入或 node cache 静默裁决冲突。
第一版 source staging 的 4096 rows/16MiB、全局 65536 rows/256MiB、30 秒 oldest-hole 上限按先到者施加；越界 backpressure/quarantine，control gap 绝不跳洞。

## 5. Event 类型与投影消费链

identity 生命周期至少消费 `agent.reserved|reservation_claimed|identity_pending|activation_pending|outcome_unknown|activated|rejected|failed|expired`。
绑定与运行时消费 `agent.incarnation_activated|incarnation_closed|identity_unverified|reconciled|control_degraded|reconciliation_required|lost` 以及 capability/measurement/effect lease events。
命令/消息/内容/资源/Room 事件只能引用 ID 已确认的 reservation/incarnation/fence；ID 不为这些 owner 生成同义 terminal 状态。
EventStore canonical chain 为：producer observation → ingress custody/staging → dense append → content materialization → event append → DB projection/report → UI/`get_agent_events`/`wait_agents`。
`state_source_cursor`、`control_status_source_cursor`、`resource_status_source_cursor` 和 `last_event_cursor` 必须分别回显，不能用单一最新 cursor 假装所有投影同水位。
`agent.incarnation_activated` 必须同 reservation activated 及 binding finalization transaction 写入，包含 result incarnation、binding fence、activated cursor 和 CAS outcome。
`agent.incarnation_closed` 只能由 expected-active CAS-applied transaction 写入；CAS mismatch 只追加审计，不能清 active id 或重写旧事件。
message/report/output正文通过 CONTENT ref 读取，ID event 不内联正文；TRUST 字段完整贯穿，不得在 projection 中改变 trust class 或 role。

## 6. 崩溃、重放与未知结果

append 前崩溃留下完整 durable record 或什么都没有；不允许永久 hole、sequence 重用、mapping 无 record 或 record 无 mapping。
append receipt 已返回但 materialization 未完成时，恢复只按原 `(source,lane,sequence)` 查询并重放，不能重新编号或创建新 identity。
bridge/provider ack、binding finalize、current-binding CAS 或 event commit 结果不明时，operation 进入 `outcome_unknown`/`reconciliation_required`，只查询原 operation id。
旧 incarnation 的迟到 ack/completion 仍可写旧 command/audit，若 fence 匹配；它不能推进 active node、current binding、new attempt 或重试。
control-lane gap、identity/capability/receipt gap 立即 quarantine source、写 degraded/reconciliation_required 并撤销写能力；不能只 warning 后继续。
output/content gap 只能由 EventStore owner 的版本化 gap adjudication 写 `output.gap`，同时撤销 output history/live cursor capability；普通 bridge/projector 不能跳洞。
sidecar 重启前后保留 lane epoch、dedup floor、hash tombstone 和 recovery-custody cursor；旧 epoch 迟到 frame 只能 quarantine。
任何 provider/bridge disconnect 不删除新 generation；transport close 按 exact source fence 幂等 evict，sweeper 仅兜底。

## 7. 去重、授权与字段守卫

identity mutation 的 stable key 必须包含 caller principal、kind-specific source/reservation、expected fence、idempotency key 和 canonical intent digest；同 key 异 digest 为 conflict。
event append 的 idempotency key 是 source/lane/sequence 加 envelope hash；provider event id 只能辅助 evidence，不能替代 relay sequence。
写 identity/event 需要 authenticated principal 或 generated internal owner scope；agent_id、thread id、普通 metadata 不能伪造 caller。
diagnostic/full fence/read source 需要独立 grant；普通 list/read 不因 `view` 参数切换而获得 provider-specific identity。
字段消费链固定覆盖 `agent_id→reservation→execution_identity→incarnation→binding_fence→event→projection/cursor→command/content/resource/OUT/Room`，每个字段须有明确 producer/consumer。
`TrustPolicyOwner` 的 provenance/trust/content-role revision、`ContentOwner` 的 content ref/version、`Resource` 的 reservation fence 和 `OUT` 的 command fence 必须逐字段匹配。
unknown/missing/duplicate/stale field、跨 workspace/run、跨 incarnation、future source sequence 和旧 writer epoch 均 fail-fast；不使用默认 agent、默认 cursor 或默认 scope。

## 8. 当前差距（不关闭）

当前已新增[task attribution/current task principal/canonical task event source候选](../../plans/orch-subagent-upgrade/task-principal-and-event-source.md)：transport-private attribution可绑定完整execution/issuer fence，resolver重读reservation/incarnation/current binding，event source固定`task/{agent_id}`与incarnation epoch。仍无transport auth binder、真实event producer或`unified_v2` public list/wait/send；完整writer/projection/production custody消费者仍未具备。
总规格要求的 all-kind tables、binding preparation、active-process prepare/bind/finalize、stable replacement head 和 full event custody，当前 ST 表没有完整 GREEN 证据，源码 writer inventory待复核。
现有 `agent_provider_binding`、`agent_threads`、report 和 node 仍可能是 legacy/兼容投影；不能把已存在的行当成 current-binding/event SSOT。
ST-08 只证明 direct-peer 最小消息、cursor 和 outbox 片段；完整 receipts、Room、cross-incarnation fence 和恢复域外 custody 仍未完成。
ST-12 的受控 Fx rehydrate 不是独立 serve binary SIGKILL 接管；ST-17 与 ST-18 仍待实现/验证，旧 adapter/stdio 代理边界未关闭。
当前本地 wrapper 的 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 不能关闭身份、append crash、远程 recovery 或发布证据缺口。

## 9. 具名 RED/GREEN 验收

RED-ID-01 `DuplicateIdentityOrIncarnation`：相同 external identity replay 复用同一 incarnation；不同 identity 竞争 active 或 source discriminator 改变时零新增 current/事件。
RED-ID-02 `BindingPreparedCannotAuthorize`：prepared 或 bind-current 未 finalize 时 prompt/message/effect 全拒绝，不能由 projection 激活。
RED-ID-03 `FinalizeAbortRace`：finalize 与 abort 同 generation 只能一个赢家；输家不能改 current binding、activation event 或 reservation terminal。
RED-ID-04 `StaleIncarnationEvent`：旧 execution fence 的 event/ack 只落旧审计，不能推进新 node、turn、binding 或重试。
RED-ID-05 `DenseAppendHoleAndReplay`：乱序、高 sequence 越洞、同序异 hash、重启重放均不推进 cursor；同一合法 frame 只 materialize 一次。
RED-ID-06 `AckBeforeRecoveryCustody`：未取得恢复域外 availability receipt 不返回 custody ack，不释放唯一 spool/replay 责任。
RED-ID-07 `ControlGapFailClosed`：control lane gap/quarantine 撤写能力；不能把 missing event 标作 warning 或 latest。
RED-ID-08 `LegacyProjectionCannotWrite`：旧 binding/report/node writer 被调用时 fail-fast，不能与新 owner 双写。
RED-ID-09 `FreshBootstrapMigrationLeader`：空数据库只能由 bootstrap metadata 建立单一 migration leader；并发 fresh bootstrap 只有一个 committed schema/head，输家在任何业务表分配前返回 `migration_leader_conflict`。
RED-ID-10 `MigrationScriptDigestDrift`：锁内发现 script/schema marker 漂移时拒绝 DDL，数据库版本、业务表和 source head 均保持原值。
RED-ID-11 `BoundedStorageTransaction`：超过 rows/bytes/wall-time 任一上限的 migration 或 append 以 `storage_transaction_bound_exceeded` 回滚，不能留下半批 checkpoint、identity 或 event。
RED-ID-12 `PausedWriterKillRollback`：writer 在 prepare 后被 SIGSTOP/SIGKILL，successor 未取得更高 writer epoch 或无法证明旧事务回滚前不得写；接管后从精确 checkpoint 单次重放，无半 schema、sequence hole 或重复 head。
RED-ID-13 `StorageCommitFenceMismatch`：COMMIT 前 writer/deployment/security-time/generation 漂移使 storage commit fence 校验失败，事务回滚且不返回 committed/availability receipt。
GREEN-ID-01 `IdentityBindingFinalizeVsAbort`：launch/recover/replace 正例完成三段 binding，并可在每个 crash window 按原 id 重放。
GREEN-ID-02 `EventDenseAppendCrashReplay`：三 lane 取得连续 custody/materialized cursor、dedup tombstone、gap adjudication 和 source epoch 恢复证据。
GREEN-ID-03 `ProjectionRebuildFromCursor`：删除内存 runtime 后从 event cursor 重建 node/report/control/resource projection，结果与 source roots 一致。
GREEN-ID-04 `StopThenRecoverFence`：stop 先 revoke runtime/active-process，再 close binding；recover 不复用旧 incarnation，迟到旧 event 不越界。
GREEN-ID-05 `FreshBootstrapAndMigrationReplay`：fresh bootstrap、滚动迁移与中途 crash 均按 migration operation/checkpoint/storage commit fence 恢复，最终 schema/head 与单次 migration receipt 一致。
GREEN-ID-06 `PausedWriterTakeoverAfterRollback`：暂停/杀死旧 writer 后确认回滚，再以 successor writer epoch 取得唯一 commit fence；重放同 operation 只返回原 receipt，不新增 identity/event。

## 10. 退出条件与 open decision

退出必须具备唯一 owner/writer inventory、all-kind discriminator/schema、DB writer fence、dense allocator、recovery custody、dedup/hash tombstone 和字段守卫差集为零。
所有具名 RED/GREEN、binding crash window、unknown query、projection rebuild、control gap 和权限负测都要有可复查 fixture attestation。
必须证明 ID 与 TRUST/CONTENT/RESOURCE/OUT 的 fence、cursor、ref、digest、expiry 在一个明确的 source/consumer chain 中被逐字段消费。
Open decision A：all-kind 表/迁移版本、IdentityRepository 与 EventStore 是否同一 sidecar owner，若分离则 recovery/cross-owner compare-key 方案。
Open decision B：RecoveryCustody数据库方向已冻结为外部单实例服务独占私有SQLite，PostgreSQL排除；local kernel lease、revision-3（含external-service mode）DB writer_epoch/takeover与HTTP transport候选已存在，仍待独立服务、外部EvidenceFinalizer/anti-rollback anchor、验签terminal proof、failover/retention与`security_domain_id`生命周期。
Open decision C：provider child lifecycle/adoption 的最小 conformance evidence，以及无 continuity 时的统一 degraded/quarantine policy。
Open decision D：SQLite writer/migration fence、projection rebuild 的具体 generator 与 legacy 双读退出版本。
未完成上述决策、TRUST 获批、真实 fixture 和 release evidence 前，本文件保持 `Status: Draft`，不启用 external dispatch 或 unified native child。
