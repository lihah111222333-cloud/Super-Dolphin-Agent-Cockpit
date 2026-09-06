# Cross-task requirements acceptance matrix（planning-only）

Status: Planning-only — cross-task send/Room `NO-GO`

Authority: `planning_only`

Source-Spec: [统一 native-subagent control plane 总规格](../../../superpowers/specs/2026-07-11-unified-native-subagent-control-plane-design.md) §10.5、§11、§14.4

本文件是 `release-gate-dag.json` 中 cross-task requirements 的可追踪验收规划记录。当前 DAG 实际登记八项：原有七项跨任务能力，以及追加的 `message_reply_association` 关联完整性项；每项都必须有独立记录，不能漏项。本文登记能力边界、字段链、owner/祖先门、具名 RED/GREEN fixture 和 guard/evidence 路径；它不注册工具、不签发 grant、不产生 receipt、不改变运行时 composition。所有 fixture 和引用都只是 planned/observed 的 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED` 记录。

## Canonical surface 与 legacy 边界

- `canonical_surface` 只允许 `unified_v2` 或 `room`，并且必须与 DAG 中同 ID 的值逐字相同。
- `unified_v2` 的跨任务链沿用 `list_agents(view=route) -> mutation(handle) -> wait_agents/get_agent_events`；消息、事件、命令、内容和资源分别由 ID/CONTENT/RESOURCE/OUT owner 产生可重建事实。
- `room` 只提供在授权时间区间内的成员读取、broadcast 和 round。Room membership、aggregate receipt 或 participant snapshot 都不能升级 direct-peer authority 或私有历史读取权。
- **legacy `send_message` 不是 canonical cross-task surface。** 它仅保留为兼容/operator 管理路径；legacy handler、direct-peer 候选或本机 E2E 不得填充下列任一 `unified_v2`/`room` GREEN 结论。未完成全部 owner、祖先 gate、stateful fixture 和发布证据前，八项均保持 `pending` 或 `needs_revalidation`，决策为 `NO-GO`。

## Common field and evidence rules

每一项验收都必须把以下字段分成 caller input、server-resolved fence 和 durable evidence 三类，禁止从普通 metadata、可猜测 ID、同目录关系或 Room aggregate 推断权限：

| 类别 | 最小 required fields |
| --- | --- |
| caller/auth | `caller_principal`、`auth_context_id`、`grant_id`、`grant_revision`、`revocation_epoch`、`grant_expiry`、`source_scope` |
| task identity | `source_task_id`、`target_task_id`、`target_execution_incarnation_id`；涉及 turn 时必须有 `target_turn_id`/`turn_handle` 与 `execution_fence` |
| command/message | `command_id`、`command_version`、typed `delivery`、`idempotency_namespace`、`idempotency_key_digest`、`content_ref`、`resource_reservation_ref` |
| acceptance/recovery | `accept_resolution`、`accepted_cursor` 或 `pending_cursor`、`recovery_receipt_digest`、`attempt_generation`、`dispatch_started` receipt |
| outcome/cursor | typed receipt (`accepted|provider_acknowledged|observed_in_context|rejected|outcome_unknown`)、`source_cursor`/`after_cursor`、`next_cursor`、`selector_snapshot` |
| room (only when applicable) | `room_id`、`membership_revision`、`dispatch_generation`、frozen participant snapshot/hash、operation/item command mapping |

任意 required field 缺失、重复、未知、scope/fence 不一致、非 canonical 编码或 response unknown 都必须 fail-fast；provider call、attempt、grant、reservation、event/content 写入计数为零，或保持原 command 的 indeterminate state。GREEN 只有在 stateful fixture、owner receipt、guard 输出和适用祖先 gate 全部可复查后才允许重新裁决；本文件当前不作该裁决。

## Requirement records

### CT_DELIVERY_UNION — Direct peer message delivery union

- **Capability / canonical surface:** `direct_peer_delivery_union` / `unified_v2`
- **Owners:** `ID`, `CONTENT`, `RESOURCE`, `OUT`, `BRIDGE`, `TOOLS`
- **Dependencies:** `ID`, `CONTENT`, `RESOURCE`, `OUT`, `BRIDGE`, `TOOLS`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** authenticated source/target task and execution fences；`delivery` exactly one of `mid_turn|next_boundary|new_turn`；仅 `new_turn` 可带 `busy_policy.action=fail|queue|interrupt_then_send`；`content_ref`、reservation/ref、command fence、acceptance cursor、provider receipt/outcome 和 stable idempotency tuple。回复另需独立 message command、source principal、association object/scope/cardinality/intent hash；不得使用裸 `correlation_id`/`reply_to`。
- **RED fixtures:** `RED-CT-DELIVERY-MIXED-FIELDS`（delivery union 混合或 duplicate busy policy，授权前 provider/attempt=0）；`RED-CT-DELIVERY-STALE-FENCE`（target incarnation/turn 在 claim 前变化，零 provider effect）；`RED-CT-DELIVERY-LEGACY-SEND-NONCANONICAL`（仅 legacy `send_message`/direct-peer handler 证据，拒绝把它映射为 unified_v2）。
- **GREEN fixtures:** `GREEN-CT-DELIVERY-UNION-ROUNDTRIP`（独立 root 双向 mid_turn/next_boundary/new_turn 逐字段 round-trip）；`GREEN-CT-DELIVERY-REPLY-INDEPENDENT`（reply 独立 command/idempotency，原 fence 重放最多一个 accepted effect）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/durable-native-command-outbox-spec.md`、`docs/work/plans/orch-subagent-upgrade/send-message-intent-profile.md`、`docs/work/plans/orch-subagent-upgrade/directpeer-duplicate-repro.gotxt`；runtime observation=`docs/work/plans/orch-subagent-upgrade/verification-notes.md`。这些路径不能单独证明 GREEN。
- **Current status / decision:** `needs_revalidation`; `NO-GO`（缺 production CONTENT/RESOURCE/OUT/bridge 接线及发布证据）。

### CT_GRANT_SPLIT — Strict discover/read/send grant separation

- **Capability / canonical surface:** `strict_discover_read_send_grant` / `unified_v2`
- **Owners:** `TRUST`, `ID`, `TOOLS`, `VERIFY`
- **Dependencies:** `TRUST`, `ID`, `TOOLS`, `VERIFY`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** action discriminator `discover|read|send`；source `CallerPrincipal`/issuer/audience；target task selector、scope、execution/incarnation fence；独立 `grant_id`/revision/revocation/expiry、capability evidence cursor；read 的 `after_cursor`/selector snapshot；send 的 delivery union、content/ref、command and idempotency tuple。grant 缺失或跨 action 复用必须在对象存在性查询和副作用前拒绝。
- **RED fixtures:** `RED-CT-GRANT-ACTION-REUSE`（discover grant 被拿来 read/send，零 object disclosure/side effect）；`RED-CT-GRANT-METADATA-FORGE`（客户端 metadata、可猜测 agent id 或同父目录伪造 caller/grant，零作用）；`RED-CT-GRANT-REVOKED-STALE`（revocation/expiry/generation stale，provider/command=0）。
- **GREEN fixtures:** `GREEN-CT-GRANT-THREE-WAY-SEPARATION`（三 action 各有独立 grant，跨树合法 read/send 仅在 exact scope 成功）；`GREEN-CT-GRANT-REVOCATION-REPLAY`（撤权、重启和 late receipt 只 query 原 fence，不创建新 effect）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/agent-output-trust-and-prompt-boundary-spec.md`、`docs/work/proposals/orch-subagent-s0/agent-identity-and-event-store-spec.md`、`docs/work/proposals/orch-subagent-s0/versioned-tool-surface-rollout-spec.md`；observation=`docs/work/plans/orch-subagent-upgrade/access-wait-next-slice.md`。
- **Candidate implementation (LOCAL_NON_AUTHORITATIVE):** `cmd/mcp-orch/orchestration/tasktransport/authorization.go` 与 `authorization_validation.go` 提供 action/parent/grant/revocation/cursor 的纯形状判定和 zero-effect `EffectCounters`；它不读取 durable grant、当前索引或 EventStore，也不注册 `unified_v2`。
- **Current status / decision:** `pending`; `NO-GO`（独立 grant repository、真实 caller attribution 和 public unified_v2 tool profile 尚未获批）。

### CT_MESSAGE_REPLY_ASSOCIATION — Message/reply association integrity

- **Capability / canonical surface:** `message_reply_association` / `unified_v2`
- **Owners:** `ID`, `CONTENT`, `OUT`, `TOOLS`, `UI`
- **Dependencies:** `ID`, `CONTENT`, `OUT`, `TOOLS`, `UI`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** generated association profile/version、typed object/scope/cardinality、source principal and target execution/incarnation/turn fence、canonical client intent hash；message 与 reply 各自的 `command_id`/`command_version`、stable idempotency namespace/key digest、content ref、acceptance/dispatch cursor 和 receipt。`correlation_id`/`reply_to` 只能作为 profile 内已授权 typed association 的派生字段，不能单独建立关联、权限或第二 transcript；UI projection 必须携带 source cursor。
- **RED fixtures:** `RED-CT-ASSOCIATION-MISSING-PROFILE`（association profile/object/scope/cardinality/intent hash 缺失、unknown 或 duplicate，授权前 command/provider=0）；`RED-CT-ASSOCIATION-REUSE-PARENT`（reply 复用 parent command/attempt 或缺独立 idempotency key，不创建第二 effect且 parent receipt 不被覆盖）；`RED-CT-ASSOCIATION-BARE-CORRELATION`（裸 `correlation_id`/`reply_to`、跨 object/scope 或 target fence 不匹配，拒绝且不泄漏目标存在性）。
- **GREEN fixtures:** `GREEN-CT-ASSOCIATION-CANDIDATE-ACTUAL-ROUNDTRIP`（generated profile 在 candidate/actual/UI projection 之间逐字段、digest、scope/cardinality round-trip）；`GREEN-CT-ASSOCIATION-REPLY-RESTART`（独立 reply command 在丢响应、重启、late receipt 后按原 fence 查询，最多一个 accepted/provider effect）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/durable-native-command-outbox-spec.md`、`docs/work/plans/orch-subagent-upgrade/send-message-intent-profile.md`、`docs/work/proposals/orch-subagent-s0/agent-ui-projection-spec.md`；observation=`docs/work/plans/orch-subagent-upgrade/verification-notes.md`。
- **Candidate implementation (LOCAL_NON_AUTHORITATIVE):** `cmd/mcp-orch/orchestration/messageassociation` 提供仅 DTO/validator 的 typed object/scope/cardinality、`SourcePrincipalRef`、source/target execution+turn fence、canonical client-intent digest、独立 message/reply command 与 idempotency namespace/key、content ref，以及 duplicate/unknown/null/bare `correlation_id`/`reply_to` strict JSON 拒绝；intent 预检还限制为无自定义 `json.Marshaler`、1MiB payload、16 层深度和对象/数组各 64 members。该包不生成 principal，也不证明 object/scope generation、授权 parent object、delivery/busy union、acceptance/dispatch cursor 或 receipt 的 owner 绑定；这些仍由 generated profile、OUT/TOOLS owner 负责。它不拥有 delivery/cursor/receipt，不注册 transport/provider/Fx、不写 OUT/DB、不产生 provider effect；候选不能替代 production evidence。
- **Current status / decision:** `pending`; `NO-GO`（association profile generator、独立 reply outbox、UI projection 和发布证据尚未完成）。

### CT_ROOM_NO_ESCALATION — Room membership does not escalate direct-peer authority

- **Capability / canonical surface:** `room_no_escalation` / `room`
- **Owners:** `TRUST`, `ID`, `CONTENT`, `RESOURCE`, `OUT`, `ROOM`
- **Dependencies:** `TRUST`, `ID`, `CONTENT`, `RESOURCE`, `OUT`, `ROOM`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** authenticated caller and explicit room grant；`room_id`、workspace/run scope、`membership_revision`、`dispatch_generation`、participant snapshot/hash；broadcast/round 的 typed delivery、completion condition、operation idempotency key；每个 target 的独立 execution/incarnation fence、target grant、content/ref、reservation、command fence、item receipt；room cursor 与 target cursor 必须分离。
- **RED fixtures:** `RED-CT-ROOM-MEMBERSHIP-ESCALATES-DIRECT`（join/member snapshot 被用于私有 direct-peer send/read，零 target command）；`RED-CT-ROOM-AGGREGATE-AS-TARGET`（aggregate receipt 冒充 per-target accepted，逐项 target 仍 pending/rejected/unknown）；`RED-CT-ROOM-STALE-MEMBERSHIP`（membership/dispatch generation drift，拒绝 fan-out 且清理未公开 intent）。
- **GREEN fixtures:** `GREEN-CT-ROOM-SNAPSHOT-FANOUT`（冻结 snapshot/plan 后每 target 独立授权、command 和 receipt，Room event 可重放）；`GREEN-CT-ROOM-REVOKE-AND-DRAIN`（leave/close/revoke 不扩权，逐 item terminal truth 收口，unknown 保留可查询）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/agent-room-authz-and-flow-control-spec.md`、`docs/work/plans/orch-subagent-upgrade/room-contract-candidate.md`、`docs/work/plans/orch-subagent-upgrade/content-resource-custody-decision-gate.md`；observation=`docs/work/plans/orch-subagent-upgrade/verification-notes.md`。
- **Current status / decision:** `needs_revalidation`; `NO-GO`（Room event bridge、逐 target outbox/receipt 和 custody/release 尚未完成）。

### CT_CURSOR_RESUME — Cursor resume after disconnect or restart

- **Capability / canonical surface:** `cursor_resume` / `unified_v2`
- **Owners:** `ID`, `TOOLS`, `UI`, `VERIFY`
- **Dependencies:** `ID`, `TOOLS`, `UI`, `VERIFY`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** selector discriminator（execution/active/last-closed/reservation/observation/command）；acceptance-time `selector_snapshot`；opaque `after_cursor`/`next_cursor`、cursor domain/source owner/generation；同 principal/profile/session/boot binding；bounded follow parameters、`has_more`、item error；resume 时原 execution/incarnation/stream fence、event/content cursor 分离。cursor 不能跨新 incarnation、target、session 或 stream 迁移。
- **RED fixtures:** `RED-CT-CURSOR-FOREIGN-STREAM`（foreign/unknown/cross-stream cursor，body/worker/handler allocation=0）；`RED-CT-CURSOR-OLD-INCARNATION`（recover 后旧 cursor 试图读取新 incarnation，拒绝且不泄漏新对象）；`RED-CT-CURSOR-DUPLICATE-CARRIER`（Last-Event-ID/after_cursor duplicate 或 method mismatch，零 watcher/side effect）。
- **GREEN fixtures:** `GREEN-CT-CURSOR-RESTART-RESUME`（断连、daemon restart 后从原 cursor 继续，事件不重放/不丢失）；`GREEN-CT-CURSOR-MULTI-TARGET-BOUNDED`（多 target deficit-round-robin、独立 next cursor 和 byte/count cap 可持续续读）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/agent-identity-and-event-store-spec.md`、`docs/work/proposals/orch-subagent-s0/versioned-tool-surface-rollout-spec.md`、`docs/work/proposals/orch-subagent-s0/agent-ui-projection-spec.md`；observation=`docs/work/plans/orch-subagent-upgrade/access-wait-next-slice.md`。
- **Current status / decision:** `pending`; `NO-GO`（生产 event source、HTTP session/resume profile 与发布级 cursor evidence 未完成）。

### CT_IDEMPOTENCY — Idempotent delivery and replay

- **Capability / canonical surface:** `idempotency` / `unified_v2`
- **Owners:** `ID`, `OUT`, `TOOLS`, `VERIFY`
- **Dependencies:** `ID`, `OUT`, `TOOLS`, `VERIFY`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** caller-stable `idempotency_namespace`/`idempotency_key_digest`、canonical client-intent hash；完整 source/target incarnation/turn/room fence；command/message/operation id/version；content/ref and reservation digest；accept/attempt generation；provider key/query/receipt capability；tombstone horizon and terminal cursor。same key+digest 必须返回同一 command/operation，异 digest、scope、fence 或 payload 为 `idempotency_conflict`，不得依赖本地 cache 或 provider ACK 猜测 exactly-once。
- **RED fixtures:** `RED-CT-IDEMPOTENCY-DIGEST-CONFLICT`（同 key 改 payload/fence/source，拒绝且不新建 command/reservation）；`RED-CT-IDEMPOTENCY-RESTART-DUPLICATE`（response lost/restart/old worker reclaim，不能 second dispatch）；`RED-CT-IDEMPOTENCY-HORIZON-REUSE`（tombstone horizon 内 key reuse，拒绝且无副作用）。
- **GREEN fixtures:** `GREEN-CT-IDEMPOTENCY-SAME-FENCE-REPLAY`（同 principal/namespace/key/digest 始终返回同 command fence，最多一个 effect）；`GREEN-CT-IDEMPOTENCY_PROVIDER-QUERY`（有 conformance 的 provider key/query 按原 key 对账，无条件自动重发被禁止）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/agent-identity-and-event-store-spec.md`、`docs/work/proposals/orch-subagent-s0/durable-native-command-outbox-spec.md`、`docs/work/plans/orch-subagent-upgrade/command-intent-storage.md`、`docs/work/plans/orch-subagent-upgrade/command-lifecycle-journal.md`；observation=`docs/work/plans/orch-subagent-upgrade/candidate-revalidation-2026-09-02.md`。
- **Current status / decision:** `needs_revalidation`; `NO-GO`（OUT receipt/reconcile、provider conformance 和 production custody 未完成）。

### CT_UNKNOWN_OUTCOME — Unknown outcome remains indeterminate

- **Capability / canonical surface:** `unknown_outcome` / `unified_v2`
- **Owners:** `ID`, `RESOURCE`, `OUT`, `TOOLS`, `VERIFY`
- **Dependencies:** `ID`, `RESOURCE`, `OUT`, `TOOLS`, `VERIFY`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** original command/submission fence；attempt/lease generation；dispatch/recovery receipt state；provider receipt/query capability and provider request id；resource reservation/usage watermark；`outcome_unknown` status/error; reconcile cursor, late receipt audit cursor and idempotency tombstone。unknown 只能 query 原 operation/key；不能换 key、补 permit/token、重新 claim、自动重放或改成 definitive failure。
- **RED fixtures:** `RED-CT-UNKNOWN-BLIND-RESEND`（provider/response/worker unknown 后换 key 重发，必须拦截且 provider call 不增加）；`RED-CT-UNKNOWN-ACK-WITHOUT-RECEIPT`（无稳定 provider receipt 却写 acknowledged/completed，拒绝 terminal transition）；`RED-CT-UNKNOWN-RESOURCE-RELEASE`（unknown 时提前释放 reservation/usage，拒绝并保留 reconcile custody）。
- **GREEN fixtures:** `GREEN-CT-UNKNOWN-RECONCILE-ORIGINAL-ID`（重启、late receipt、old worker 只凭原 id/query 收口）；`GREEN-CT-UNKNOWN-TYPED-WIRE`（wire 明确 `command_outcome_unknown`/`delivery_outcome_unknown`，wait 返回 indeterminate 与可续 cursor）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/durable-native-command-outbox-spec.md`、`docs/work/proposals/orch-subagent-s0/durable-resource-admission-and-budget-spec.md`、`docs/work/proposals/orch-subagent-s0/provider-cli-native-bridge-profiles-spec.md`；observation=`docs/work/plans/orch-subagent-upgrade/command-lifecycle-journal.md`、`docs/work/plans/orch-subagent-upgrade/verification-notes.md`。
- **Current status / decision:** `pending`; `NO-GO`（跨进程 custody/reconcile 和 provider query conformance 尚无发布证据）。

### CT_UNAUTHORIZED_ZERO_EFFECT — Unauthorized requests have zero side effects

- **Capability / canonical surface:** `unauthorized_zero_side_effect` / `unified_v2`
- **Owners:** `TRUST`, `ID`, `CONTENT`, `RESOURCE`, `OUT`, `TOOLS`, `ROOM`, `VERIFY`
- **Dependencies:** `TRUST`, `ID`, `CONTENT`, `RESOURCE`, `OUT`, `TOOLS`, `ROOM`, `VERIFY`
- **Ancestor gates:** `VERIFY_S`, `ROLLOUT_S`, `DOMAIN_DONE`, `VERIFY_DONE`, `PRECANARY_DONE`
- **Required fields:** transport-authenticated `CallerPrincipal` and immutable auth context；action-specific grant/capability and revocation/expiry；source/target scope plus execution/incarnation/turn/room fences；canonical request/intent digest；content prepared ticket/ref；resource reservation/permit；command/operation id/version and idempotency key；preflight decision, zero-effect counters and typed error/audit cursor。工具参数、客户端 metadata、Room membership 或 legacy send 都不能补齐这些字段。
- **RED fixtures:** `RED-CT-UNAUTH-PREAUTH-FAILFAST`（缺/错 principal、grant、scope 或 duplicate/unknown fields，body/DB/event/content/reservation/provider call 全为零）；`RED-CT-UNAUTH-ROOM-MEMBERSHIP`（仅 Room membership 或 aggregate receipt 访问 direct peer/private history，零 target effect）；`RED-CT-UNAUTH-LEGACY-SEND`（legacy `send_message` 旁路试图建立 canonical grant，拒绝且不暴露目标存在性）。
- **GREEN fixtures:** `GREEN-CT-UNAUTH-EXACT-GRANT`（合法 caller/grant/fence 在同一 admission snapshot 后才形成 command intent，效果可审计）；`GREEN-CT-UNAUTH-NEGATIVE-MATRIX`（越权、撤权、stale、unknown、metadata injection 全部 typed reject，所有 side-effect counters=0）。
- **Guard/evidence refs:** guard=`scripts/orch_upgrade_gate_guard_test.go`；planned owner evidence=`docs/work/proposals/orch-subagent-s0/agent-output-trust-and-prompt-boundary-spec.md`、`docs/work/proposals/orch-subagent-s0/agent-identity-and-event-store-spec.md`、`docs/work/proposals/orch-subagent-s0/durable-native-command-outbox-spec.md`、`docs/work/proposals/orch-subagent-s0/agent-room-authz-and-flow-control-spec.md`；observation=`docs/work/plans/orch-subagent-upgrade/pre-admission-proof-and-custody-contract.md`、`docs/work/plans/orch-subagent-upgrade/content-resource-custody-decision-gate.md`。
- **Candidate implementation (LOCAL_NON_AUTHORITATIVE):** `tasktransport.AuthorizationRequest/Evaluate/RevokeGrantRequest` 只产生 zero-effect preflight/纯函数撤权快照；不写 grant、cursor、content/resource、command、event 或 provider，也不能替代真实授权 owner。
- **Current status / decision:** `needs_revalidation`; `NO-GO`（production auth/custody adapters、Room bridge、public tool profile 和 release evidence 未完成）。

## Machine traceability and status contract

每个记录中的 `CT_*`、`RED-CT-*`、`GREEN-CT-*` token 是后续验收 runner 的规划索引；runner 必须按 ID、fixture revision、source/target fence、owner receipt 和 guard output 逐项绑定，不能因 token 出现而标记通过。`release-gate-dag.json` 的三类 evidence refs 统一指向本文件；记录内部的 supporting paths 只供定位，不改变其 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED` 性质。

本矩阵不覆盖 Gate S0 spec-completeness validator，也不替代十四份子规格的内容评审。任一 owner spec、ancestor gate、required field、RED/GREEN fixture、guard/evidence receipt 缺失或漂移，均保持对应项 `pending`/`needs_revalidation` 与整体 `NO-GO`；不得将规划文档、生成 dependency map、本机测试、legacy handler 或 action count 写成 `GREEN`、`closed`、production enabled 或 GA。
