Spec-ID: RESOURCE
Status: Draft
Depends-On: ID
S0-Owner: ResourcePolicyRepository and AdmissionRepository as the sole policy and reservation writers
S0-Scope: policy snapshots, admission reservations, usage accounting, claims, renewals, release, and quarantine
S0-Dependencies: ID
S0-Inputs: authenticated principal, canonical AdmissionRequest, policy snapshot, and target execution fence
S0-Outputs: ReservationReceipt, ClaimReceipt, ReleaseReceipt, typed admission decision, and usage cursor
S0-Failure: missing or stale policy, budget conflict, and unknown claim/release resolve to typed reject or quarantine
S0-Recovery: query the original reservation and receipt; reclaim only with a proof that no dispatch started
S0-Field-Chain: policy source -> snapshot/hash -> AdmissionRequest -> reservation -> claim/usage -> release receipt
S0-Named-Acceptance: RED-RESOURCE-01 MissingPolicySnapshot; GREEN-RESOURCE-01 AdmissionReservationRoundTrip

# Durable Resource Admission 与 Budget 子规格

## 0. 定位与约束

本文件是 Gate S0 的独立实施子规格，不是已批准资源实现，也不表示当前 provider/room dispatch 已通过预算门。
它细化总规格 §1.16、§5.2、§7.1、§8.2/§8.5、§9.3、§11.1/§11.3 和 §19 Gate S0 的第4项 RESOURCE 节。
ID 是前置：reservation 必须绑定稳定 logical agent、parent chain、execution/incarnation 或 provider observation fence。
资源 admission 是 external side effect 的必要门，不是 launcher、room worker、provider memory counter 或 UI projection 的附加建议。
缺 policy、unknown dimension、无法 canonicalize、无法计量或任何 current fence 不可读时必须 fail closed，不允许默认值或静默降级。

## 1. 唯一 owner 与策略 snapshot

ResourcePolicyRepository 是 policy snapshot、revision/hash、precedence trace 和 effective policy 的唯一写 owner。
AdmissionRepository 是 reservation、usage ledger、claim/renew/release、emergency reserve 和 quarantine transition 的唯一写 owner。
Event projector、command/outbox、room service、provider bridge、scheduler、launcher 和 UI 只读 reservation/usage projection，不能各自记账或释放。
policy snapshot 固定 `scope_kind/scope_id`、revision、canonical hash、policy JSON、effective time、owner generation、security-time 和 writer/deployment fence。
effective policy 必须由 global hard cap、workspace/run、principal/grant、provider/runtime、parent/room policy 求逐维最严格交集，并保存 precedence trace。
客户端声明只能进一步收紧，不得覆盖 server cap；loosen 只影响新 reservation，v1 不自动扩大既有 reservation。
具体 policy table、生成器、hash canonicalizer 和跨 owner compare-key 方案是 open decision，不能把当前配置文件当作权威 snapshot。

## 2. Reservation、usage 与预算字段

每个 external launch/fork/recover/respawn、provider child 和 room target 都先创建 durable reservation，再允许 claim 或调用 provider。
reservation 至少固定 `reservation_id/version`、principal subject、workspace/run、provider/runtime profile、parent/root/depth、requested kind、command id/fence。
资源维度固定 active/in-flight agent count、fan-out、concurrency、wall-time deadline、token budget、cost budget、content/ingress bytes 与 provider-specific quota。
同时保存 policy snapshot id/revision/hash、effective limit vector、reservation idempotency digest、security-time generation、usage watermark、release condition 和 owner generation。
usage ledger 是 append-only，record 固定 reservation/command/attempt fence、dimension、delta/cumulative value、measurement source、observed time、receipt digest 和 policy revision。
emergency stop/reconcile reserve 必须拥有独立 durable limit/usage/release ledger；不能以无界内存或“紧急模式”旁路硬 cap。
room broadcast/round 必须为每个有效 target 建独立 reservation、delivery plan 和 command mapping；aggregate reservation 不能替代 per-target cap。
state projection 固定 `resource_status=reserved|active|exhausted|quarantined|released`；未登记状态或 projection/ledger 冲突立即阻断 mutation。

## 3. Admission 与生命周期

规划中的 generated receipt schema（本轮仅登记，尚无 runtime writer）固定以下字段；每个 receipt 必须 one-hot 匹配其 operation 与 predecessor：

| Receipt | Required fields | Producer -> consumer / terminal |
| --- | --- | --- |
| `AdmissionRequest` | `request_id`, `operation`, `principal`, `workspace_id`, `run_scope`, `target_fence`, `policy_snapshot_id/revision/hash`, `requested_limits`, `idempotency_key/digest`, `expected_generation` | caller -> AdmissionRepository -> `admission_rejected` or `ReservationReceipt` |
| `ReservationReceipt` | `reservation_id/version`, `request_digest`, `policy_hash`, `effective_limits`, `owner_generation`, `security_time`, `expected_generation`, `receipt_digest` | AdmissionRepository -> OUT/ID/Room -> `reserved` or typed reject |
| `ClaimReceipt` | `reservation_id/version`, `command_id`, `attempt_id`, `claim_generation`, `usage_watermark`, `policy_hash`, `expected_generation`, `receipt_digest` | AdmissionRepository -> OUT/bridge -> `active` or `claim_rejected` |
| `ReleaseReceipt` | `reservation_id/version`, `terminal_digest`, `usage_settlement`, `release_generation`, `expected_generation`, `receipt_digest` | AdmissionRepository -> OUT/ID/Room/UI -> `released` or `release_reconcile_required` |

每个 transition record 另固定 `operation_id`, `operation_digest`, `predecessor`, `phase`, `expected_generation`, `receipt_or_terminal`, `source_fence`；缺字段、重复字段、跨 reservation、跳 phase、generation 不匹配或 receipt/terminal 双分支均拒绝。`reserved -> claimed -> released` 是唯一正常链；`reserved -> rejected|quarantined` 和 `claimed -> exhausted|quarantined -> released` 是显式终止分支，非法跳转由 `TestResourceTransitionRegistryRejectsIllegalJump` 负测锁定。

local command accept transaction 必须读取 current policy snapshots，计算 depth、active/in-flight、fan-out、concurrency、wall-time、token/cost 余量，并以 expected generation 创建 reservation。
reservation `reserved` 只表示额度已占用，不表示 provider process 或 external effect 已启动；OUT command 的 accepted 也不改变此语义。
claim transaction 再次比较 policy revision/hash、ID execution fence、principal grant、command/attempt fence 和 usage ledger，成功后写 `resource.claimed`/`resource_status=active`。
任何维度 unknown、过期或超限时，必须在 external call 前写 command/resource rejected 或 `resource_limit_exceeded|budget_exhausted`，provider call count=0。
provider-originated child 即使在 admission 前已发生，也必须提交 identity observation、占用 usage，并在超限时进入 `quarantined`，不能拒绝登记以制造影子 agent。
超限 child 只能通过 emergency control reserve 创建 fenced stop/reconcile；在收口前阻断 prompt、message、继续派生和普通 resource release。
正常释放要求 matching reservation/execution/command fence、terminal outcome 或 provider stop/reconcile receipt 以及 usage settlement；只因 worker 退出或 lease expiry 不得释放。
`released` 不可重新激活；新的 execution/incarnation 必须创建新 reservation/version，旧 usage/tombstone 保留到 retention horizon。

## 4. Policy 漂移、时钟与未知结果

claim 前发现 policy drift，AdmissionRepository 必须从 current snapshots 重算最严格交集并 CAS reservation；tighten 后超限则 external side effect 前 rejected，仍满足则只可收紧并记录 old/new trace。
dispatch 已开始后的 tighten 不能伪装成未执行、撤销历史 usage 或释放额度；必须追加 `resource.exhausted|resource.quarantined`，阻断新 turn/spawn 并触发 emergency stop/reconcile。
loosen 不改变已写的 limit、budget 或 command digest；future resize 必须是独立 versioned resource operation/CAS，不由客户端重试隐式完成。
跨进程/重启的 wall-time、lease expiry、usage deadline 使用统一 security-time high-water/boot epoch/max skew；回拨、source 不一致或 freshness 不明立即失效。
claim、usage append、release 或 policy compare 的 response unknown 只查询原 reservation/usage/release operation id，不另建 reservation 或从旧值猜释放。
worker 崩溃后 external execution 可能仍存活；除 `dispatch_started=false` 且 recovery receipt 明确证明未提交外，不可安全 reclaim，需进入 outcome-unknown/reconcile。
任何 stale writer/deployment epoch、旧 policy cursor、旧 incarnation 或计数不一致都 fail-fast，不能按 timestamp/last writer 静默裁决。

## 5. 权限、去重与字段消费链

admission 的 caller principal 由 transport/auth middleware 提供，必须绑定 workspace/run、grant、auth context、expiry、ID execution/source fence；普通 agent_id、thread id 或请求字段不能提升额度。
稳定 idempotency key 至少包含 principal、operation/command kind、target agent/incarnation、reservation scope 和 client key；同 key 同 canonical digest 返回同 reservation，异 digest 返回 conflict。
usage record 的稳定键包含 reservation、attempt、dimension、source sequence/measurement id；同一 receipt 不得重复计量，异 hash 只能 protocol error/quarantine。
字段消费链固定为：policy source → canonical snapshot/revision/hash/effective vector → command accept → reservation/fence → claim/usage → provider/room actual observation → resource events → OUT terminal/reconcile → projection/release。
`depth` 必须由 ID immutable parent chain 计算；`active_count/in_flight` 必须由 reservation/incarnation truth 计算；fan-out/concurrency/wall-time/token/cost 必须由 usage ledger 与 snapshot 计算。
ResourceStatus、effective limits、usage cursor、release condition 和 quarantine reason 必须被 OUT/ID/Room/TOOL/UI 逐字段消费，缺 consumer 的字段不能授权。
任何 direct peer、Room、wakeup、scheduler 或 internal producer 都必须走同一 AdmissionRepository，不能用自有计数绕过 per-target admission。

## 6. 当前差距（不关闭）

批准前源码审计曾确认不存在ResourcePolicyRepository、AdmissionRepository、durable reservation/usage/release ledger或effect permit。2026-09-02后续已实现[RESOURCE-1 reserved-only候选](../../plans/orch-subagent-upgrade/resource-1-reserved-skeleton.md)：migration133 effective policy/reservation/future usage schema与`resourceadmission` non-authorizing store。它只校验单reservation vector并固定空usage；aggregate counters、usage writer、claim/permit/release/provider接线仍不存在。ID reservation仍不授权provider execution；DAG budget只透传，prompt/tool-result/LSP budget仍分别是软提示、内存裁剪或中间件限制，均不是RESOURCE owner。
真实 launch/standalone路径仍可直接到 AgentLauncher/CLI，没有 durable resource claim；总规格要求的 policy snapshot unique writer、durable reservation/usage/release、emergency reserve、provider child quarantine 和 room per-target budget仍无完整 GREEN 证据。
ST-08 已有 direct-peer 最小消息/outbox，但不证明 resource reservation、usage ledger、cross-incarnation fence 或 recovery custody；ST-09、ST-14、ST-16 仍待实现/验证。
ST-04 的空凭据/宽权限/manifest/schema side-effect 审计未完成，不能把 standalone config fail-fast 当资源 admission 通过。
当前真实 Codex/Claude provider 和 direct-peer wrapper 证据均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；不能据此声称 token/cost 计量或负载 SLA 达标。
具体 provider telemetry 的可信来源、token/cost 单位、overage 处理、硬 caps、跨故障域usage evidence和emergency reserve数值仍是open decision；RecoveryCustody数据库方向已冻结为外部单实例服务独占私有SQLite并排除PostgreSQL，但production服务、anchor与验签proof尚未具备。见[生产决策门](../../plans/orch-subagent-upgrade/content-resource-custody-decision-gate.md)。

## 7. 具名 RED/GREEN 验收

RED-RESOURCE-01 `MissingPolicyNoDefault`：缺 policy、unknown dimension、坏 canonical JSON 或 scope policy 不可读时，reservation/command/provider call 全为零。
RED-RESOURCE-02 `OverLimitBeforeDispatch`：active/count/fan-out/concurrency/wall-time/token/cost 任一超限在 claim 前被拒绝，且没有 agent、attempt 或 external process 副作用。
RED-RESOURCE-03 `DepthAndFanoutBypass`：伪造 parent/depth、aggregate fan-out 或另一个 worker 计数不能逃逸 immutable ID parent chain/per-target reservation。
RED-RESOURCE-04 `ProviderChildOverLimitQuarantine`：已观察的 provider child 仍登记并占用 usage，超限进入 quarantine/stop-reconcile，不能隐藏或普通 release。
RED-RESOURCE-05 `TightenAfterDispatch`：dispatch 后 policy tighten 不会把 usage 改成零或声称未执行，必须 exhausted/quarantined 并触发 fenced reconcile。
RED-RESOURCE-06 `CrashUnknownKeepsReservation`：worker/claim/release response 丢失或进程崩溃时只按原 id reconcile，不能重复 claim、重复计量或提前释放。
RED-RESOURCE-07 `RoomAggregateCannotBypassTarget`：Room aggregate 未能覆盖某 target 的独立 cap 时只产生 item rejection/partial，不影响其他 target，也不扩容。
RED-RESOURCE-08 `StalePolicyOrWriterFence`：旧 revision、security time、ID incarnation 或 writer epoch 写入在副作用前 fail-first，零 usage mutation。
GREEN-RESOURCE-01 `CanonicalPolicyIntersection`：从所有适用 scope 快照重算同一 effective vector、precedence trace、revision/hash 与 deterministic digest。
GREEN-RESOURCE-02 `ReservationSingleWinnerAndSettlement`：相同 key 幂等，竞争同 generation 只有一个赢家，claim/usage/terminal/release 能从事件和 ledger 重建。
GREEN-RESOURCE-03 `ProviderObservationQuarantine`：provider-originated child 的 observation、quarantine、emergency stop/reconcile 与 usage settlement 具有完整 fences/receipts。
GREEN-RESOURCE-04 `PolicyDriftAndRoomIsolation`：tighten、room per-target、断线/重启和 old-worker reconcile 均维持额度安全及不重复 external effect。

## 8. 退出条件与 open decision

退出必须具备两个 owner 的 writer inventory、generated policy/effective snapshot、reservation/usage state graph、security-time/writer fence 和字段消费者差集为零。
所有具名 RED/GREEN、负载隔离、provider observation、room per-target、crash/unknown/reconcile fixture 要有真实可复查 attestation。
必须证明 ID parent/incarnation、CONTENT bytes/ref、TRUST sink、OUT command/attempt 与 RESOURCE reservation/usage/release 的 digest/root/expiry 全链路一致。
Open decision A：每个资源维度的 canonical unit、provider token/cost telemetry、sampling/rounding、hard cap 与 overage policy。
Open decision B：policy snapshot table/precedence generator、revision effective time、跨 owner compare-key/CAS 与 security-time owner。
Open decision C：usage ledger接入外部单实例SQLite custody服务后的真实EvidenceFinalizer/readback、anti-rollback anchor、验签receipt、retention、worker lease reclaim和emergency reserve数值；PostgreSQL不是候选authority。
Open decision D：provider-originated child admission/observation schema、room fan-out limit、测试压力门及 release evidence 的目标环境。
在 ID 获批、上述决策关闭、具名证据与发布/回滚门齐全前，本文件保持 `Status: Draft`，不允许由资源层单独启用 external dispatch。
