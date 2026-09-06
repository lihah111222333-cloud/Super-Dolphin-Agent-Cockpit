Spec-ID: ROOM
Status: Draft
Depends-On: ID, CONTENT, RESOURCE, OUT, TRUST
S0-Owner: Room domain owner for membership snapshots, fan-out inventory, receipts, close, and recovery
S0-Scope: room lifecycle, membership authorization, per-target delivery, flow control, close/drain, and rounds
S0-Dependencies: ID, CONTENT, RESOURCE, OUT, TRUST
S0-Inputs: CallerPrincipal, room operation, membership revision, content ref, target fences, and resource reservations
S0-Outputs: room snapshot, frozen inventory, per-target receipt/error, operation cursor, and close state
S0-Failure: unauthorized, stale, closing, inventory, and receipt conflicts reject with zero target side effects
S0-Recovery: replay the original room operation and frozen inventory; unknown targets remain frozen_unknown until reconciled
S0-Field-Chain: principal -> room snapshot -> delivery plan -> target command -> receipt -> room cursor/projection
S0-Named-Acceptance: TestRoomFanoutCloseSingleWinner; TestRoomFrozenUnknownCannotReportCompleted

# Agent Room 授权与流控子规格

负责人：Room domain owner；创建日期：2026-09-01。

本文是 Gate S0 的独立子规格草案，承接总规格 §10.5、§11、§19 Phase 4；它不能替代 ID、CONTENT、RESOURCE、OUT、TRUST 的已批准契约。
Draft 期间不得由本文件授权 Room mutation、broadcast、round 或 close 的生产入口。

## 目标与非目标

目标是为持久 Room 固定唯一 owner、成员快照、逐目标投递、授权、回执、关闭和恢复边界；并让广播/round 与 close 在同一个 admission 链上竞争、可由事件和 receipt 重建。
非目标是模拟群聊、复制 provider transcript、绕过 command outbox、替 provider 改写 command terminal，或把 Room membership 当作 direct peer send、私有历史读取、跨 workspace 授权；Room 不代替 A2A，同一 orch owner/security domain 的 Claude/Codex 不强制经过 A2A。

## 依赖与 owner 边界

依赖方向固定为 `ROOM <- ID, CONTENT, RESOURCE, OUT, TRUST`，不能反向创造这些 owner。ID 生产 logical agent、execution/incarnation fence、caller source、membership subject；CONTENT 生产授权的加密 content ref、版本、taint/provenance、可用性状态；RESOURCE 生产 per-target reservation、policy snapshot、deadline、usage、emergency reserve。
OUT 生产 command、attempt、receipt、outbox lease、terminal/reconcile；TRUST 生产 CallerPrincipal、issuer binding、security-time lineage、quoted-data trust class。ROOM 只拥有 room operation、dispatch admission head、inventory、membership projection、room event；不持 provider secret、不复制 execution identity 或正文明文。

## 持久对象与字段生产消费链

`room` 由 Room owner 生产 `room_id/owner/workspace_id/run_scope/ttl/dispatch_generation` 及 `membership_revision/participant_role/snapshot_hash/shared_event_cursor/close_reason`；ID consumer 用 logical `agent_id` 做拓扑引用、用 active `execution_incarnation_id` 做当前 fence。membership revision 由 join/leave 递增，不回写 dispatch generation，也不使已固化 command 失效。

`room_dispatch_admission_head/{room_id}` 是 fan-out 与 close 的 single-chain SSOT，字段至少为 `admission_generation/previous_generation/admission_operation_id/admission_digest`、`dispatch_generation/previous_inventory_root+count/command_inventory_root+count`；`state` 仅 `open|closing_pending|closed`，`custody_resolution` 仅 `pending|available|aborted`。
closing 分支额外携 closing operation/version/intent、security domain/lineage/generation、drain deadline；available 必有 availability receipt 且无 abort tombstone，aborted 反之并保留前后 inventory root/count。新 Room 的 absent head 只能由 create ledger 解释，不能把 absent 猜成 open。

`room_operation_recovery_phase` 由 OUT/ROOM 共同消费并由 ROOM owner 持久化，固定 operation/version/kind、`phase_kind`、`phase_generation`、predecessor、request digest、custody、data generation/range/root、recovery receipt-or-abort、local-finalize cursor。普通 create/join/leave/broadcast/round 只生成 `apply:1`，close/expire 只生成 `closing:1 -> terminal:1..N`；相同 key 异 digest、跳 phase、并行 terminal 一律拒绝。

每个 inventory member 固定保存 room/dispatch generation、operation/version/kind、target mapping、membership snapshot digest、selector digest、target command/version/intent digest、plan item digest；member key 按规范化 JCS tuple 做 domain-separated SHA-256，排序后形成唯一 root/count。delivery plan 为 `uniform|per_target` 判别联合，uniform 也展开为不可变逐目标 item；per-target item 生产完整 execution fence、delivery 分支、capability cursor、resource reservation、command ref。

drain member 只允许 `completed|rejected|failed|frozen_unknown`；前三类必须引用 authoritative command terminal record，`failed` 不得重写成 rejected。`frozen_unknown` 必须引用一次性 frozen record、观察 cursor、snapshot root/count、security-time receipt、cleanup set；observed state 只可来自 accept_pending、accepted_not_redeemed、redeemed_pre_issue、issued_pre_start、effect_started_outcome_unknown、abort_cleanup_pending，字段 presence 必须逐状态一致。

## 状态、输入与输出

输入入口为 `manage_agent_room(create|join|leave|get|close)`、`broadcast_message`、`run_agent_round`；create 使用 workspace/run principal+key，join/leave 绑定 expected membership revision，close 绑定 expected dispatch generation。broadcast/round 先固定 participant snapshot、selector、delivery、completion condition、plan、idempotency key；所有 mutation 先写不可见 composite pending intent，取得恢复域外 receipt 后才由指定 local-finalize transaction 公开。

只有 `open+(available|aborted)` 是接受新 operation 的 safe floor；pending、closing、closed 均 deny-first。fan-out 与 close 对同一 safe floor 做 CAS：fan-out 写 `open+pending` provisional inventory，close 写 `closing_pending+pending`；close 不冻结 provisional inventory，target accept/claim 不能绕过 closing head。receipt available 后只能按原 operation/digest commit；available 后永不改判 aborted。abort tombstone 先赢则恢复 previous inventory、拒绝 operation、完成 target cleanup，dispatch count 为零。

广播每目标独立返回 `accepted|provider_acknowledged|observed_in_context|rejected|outcome_unknown`；`capability_not_supported` 是 typed error，不是第六种 receipt，aggregate 不得覆盖 item receipt。部分目标成功时顶层为 `status=partial` 并逐项带 receipt/error；`applied` 只表示 Room apply saga finalize，不表示 provider 完成。pending 只返回 non-authorizing `room_operation_submission_fence`，receipt-backed finalize 才返回 operation fence，rejected operation 不返回 target command fence。
Room get 是唯一 public room cursor reader，支持固定 selector、after_cursor、limit<=200、follow、timeout<=60000；follow 复用 watcher cancellation、buffer/slow-consumer、768KiB 上限，wait_agents 不伪装 Room wait。

round 的 parallel 使用 any/all/quorum/first_success，ordered 依次读取前序 projection；每轮先写 `room.round_started`，固定 round id、start cursor、participant snapshot、plan、reservation、timeout、denominator。ordered 输入必须经 `v3.orchestration.quoted_data.v1` renderer，正文是 quoted data，不能提升为 system/tool 指令；成员中途加入只参加下一轮，离开/失败/capability error 都是明确 item 结果。

## 授权、内容与流控

transport/auth middleware 先构造不可由参数覆盖的 CallerPrincipal，ROOM 不接受 unsigned agent/thread/requester metadata；actor_principal 为 `authenticated_subject|system_scheduler`，scheduler 只能 expire/cleanup。provider_bridge、managed_runtime、a2a_gateway、controller_connection 各有 issuer binding，进入领域后统一映射 principal/grant。discover/read/send 独立授权；agent_id、同目录、共同 parent、同 Room 都不产生 direct peer grant。Room 只授予授权区间内 shared read/broadcast/round，不授予加入前 transcript、私有历史、direct peer send；跨 run/workspace/domain 必须显式 grant，每次 get 重验 principal、membership、content ACL、cursor scope。

Room 每目标独立 RESOURCE reservation，aggregate fan-out 不能绕过 depth/count/fan-out/concurrency/wall-time/token/cost cap；policy drift 在 claim 前按当前 snapshot 收紧，dispatch 后超限进 exhausted/quarantined 并用 emergency reserve stop/reconcile。content 先由 CONTENT 加密 ingest 并取得 ref，Room event/projection 只存 ref/hash/size/media type；provider/peer/A2A 正文保持 `untrusted_external_data`、`quoted_data` taint，广播不得 declassify。

## Close、drain 与崩溃恢复

close/expire 以 expected generation 和 safe floor 单赢家安装 `closing_pending+pending`，保存 previous inventory 与 exact frozen root/count；closing receipt available 后才公开 `room.closing`、递增 public dispatch generation、绑定 security-time/drain deadline。receipt 已 available 而本地崩溃时按冻结 head/inventory 重放，mutable membership drift 只能 reconcile，不能 reject/reopen。drain 按统一 command 状态机处理未 redeem、redeemed-pre-issue、issued-pre-start、effect-started；已 redeem 不能直接 reject，effect/nonce/cleanup 不确定保持 outcome-unknown 且禁止重试。deadline 后仅 security-time owner 的同 lineage 观察或有效 durable high-water successor 可创建 frozen record，缺字段返回 `room_frozen_observation_invalid`。

每个 inventory key 必须恰好一个 drain member；漏项、重复、inventory 外 later command、alias mapping 阻止 terminal，coverage root/count 必须逐字等于 frozen inventory，terminal 只能以前一 finalized closing phase 为 predecessor。terminal receipt unknown 只查询原 attempt；authoritative abort 才能 generation+1 retry，禁止 reopen/并行 retry/换 payload。全覆盖可写 `room.closed|room.expired`，但含 frozen_unknown 时 completion 保持 indeterminate，不能写 completed/target accepted；迟到 receipt 只追加 audit，不能改变 denominator/reopen。pending、断线、重启按原 key/digest/phase 查询，旧 incarnation follow 不自动跳新 incarnation，恢复要重新 discover/authorize 并显式切换。

### provider `command.failed` 到 failed drain 的映射

Room 不消费 provider 原始错误来猜测 drain 结果；唯一输入是 OUT/command owner 的 canonical terminal `command.failed`。映射输入必须带 `room_id/dispatch_generation/inventory_member_key`、command/attempt/operation fence、target execution fence、failure code/reason、provider receipt 或 outcome evidence digest、source cursor，以及 cleanup set root/count/state 和每项 reservation/resource/content/runtime release receipt。Room local-finalize 重读 frozen inventory 与 target command digest，确认 terminal record 属于该 member 且 cleanup state 为 `cleanup_cleared`，或 effect-started 分支已取得 `transferred_to_effect_lifecycle` 的 authoritative effect terminal receipt；随后才把该 drain member 写成 `failed`，保留原 failure code/reason、attempt 和 receipt digest。

缺 terminal receipt、只有 transport/provider ACK、cleanup set 仍 `armed|cleanup_pending|effect_outcome_unknown`、cleanup item 缺失/重复、fence 或 source cursor 漂移时，不得写 `failed` 或 `rejected`；member 保持 `frozen_unknown`/`abort_cleanup_pending`，close 也不能宣称覆盖完成。`command.failed` 永不重写成 `rejected`，`rejected` 只接受 authoritative `command.rejected`；迟到的 cleanup/failed receipt 只能按原 attempt 追加 audit 并重试同一 finalize，不能改变 denominator、reopen Room 或生成第二 target command。顶层 `partial|failed` 只由逐 member 结果聚合，不能用 `provider_acknowledged` 覆盖某一项的 `failed`。

## 错误与可观测性

错误只使用总规格 catalog：`room_not_found`、`room_access_denied`、`room_closing`、`room_closed`、`room_operation_stale`、`room_dispatch_admission_conflict`、`room_dispatch_inventory_mismatch`、`room_dispatch_genesis_conflict`、`room_recovery_phase_pending/conflict/sequence_conflict`、`caller_principal_missing`、`caller_scope_mismatch`。`room_closing_reconcile_required`、`room_frozen_observation_invalid`、`command_outcome_unknown` 必须保留 subject/cursor/operation fence；无权 caller 不得获知存在性，wire result 必须 generated schema 校验。

## 当前缺口与测试

当前 ST-08 只有 direct-peer 进程内 trusted scope，尚非 Room receipt/HTTP bearer 证据；ST-09、ST-10、ST-11、ST-14、ST-16 待实现/验证，ST-12 仅有受控 rehydrate，尚非独立 serve binary 硬崩溃接管、Room cursor、旧 incarnation fence 证据。
因此现有 standalone binary、Codex/Claude 双向 E2E、报告投影都不能把本规格标为实现完成。

正向测试名（需真实 durable owner、真实 receipt 和可重放记录）：

- `TestRoomBroadcastReturnsPerTargetReceipts`
- `TestRoomFanoutCloseSingleWinner`
- `TestRoomAbortRestoresPreviousInventory`
- `TestRoomPendingReplayReturnsSameOperation`
- `TestRoomCloseDrainsFrozenInventory`
- `TestRoomCloseReconcileAfterReceipt`
- `TestRoomMembershipRevisionKeepsRoundDenominator`
- `TestRoomSameDomainClaudeCodexNeedsNoA2A`
- `TestRoomCommandFailedMapsToFailedDrainWithCleanup`
- `TestRoomFailedDrainPreservesCommandFailure`

负向测试名：

- `TestRoomCloseCannotClaimProvisionalFanout`
- `TestRoomFrozenUnknownCannotReportCompleted`
- `TestRoomMissingOrExtraDrainMemberRejected`
- `TestRoomLateReceiptCannotReopenClosedRoom`
- `TestRoomRoomMembershipDoesNotGrantPrivatePeerRead`
- `TestRoomPendingFenceCannotBeAcceptedByWaitAgents`
- `TestRoomTargetAcceptBlockedAfterClosingCAS`
- `TestRoomDuplicateCreateHasSingleGenesis`
- `TestRoomUnauthorizedCrossWorkspaceHasZeroSideEffect`
- `TestRoomCommandFailedWithoutTerminalReceiptStaysFrozenUnknown`
- `TestRoomFailedDrainBeforeCleanupReceiptBlocked`
- `TestRoomProviderAckCannotMapToFailed`
- `TestRoomFailedIsNotRewrittenRejected`
- `TestRoomLateCleanupCannotReopenClosedRoom`

## Gate S0 退出条件与未决项

退出条件是依赖五份子规格获批，字段/phase/receipt/error catalog 进入 generated schema、field guard、machine DAG；通过上述正负测试、ST-09～ST-14/16 适用门、崩溃重放、receipt-backed Room get（静态文档、mock、initialize 不算）。必须证明 fan-out/close single winner、逐目标 receipt、exact frozen coverage、frozen_unknown 非完成、unauthorized zero-side-effect；在退出前 Room public tool/round/close 和 `a2a_remote_agent` branch 全 disabled，失败统一 typed capability/not-ready。

以下事项仍是 Draft，不能启用：connection-bound signed token 是否升级 HA durable redemption、security-time successor schema、aggregate completion 标签、Room transcript retention、per-target cancellation 预算、跨实例 Room extension 映射。
第一版最多 16 个活动 participant；扩大上限必须先有压测和新规格批准，不得由实现自行放宽。
