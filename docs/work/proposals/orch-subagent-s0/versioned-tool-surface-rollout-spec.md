# Versioned Tool Surface 与 Rollout 子规格
Spec-ID: TOOLS
Status: Draft
Depends-On: CTL, PROBE, ID, RESOURCE, OUT, TRUST, ROOM
Provider-Join: BRIDGE
S0-Owner: tool-surface/rollout owner with the generated registry as schema source of truth
S0-Scope: versioned MCP profiles, manifest/policy snapshots, provider joins, handlers, wait/cancel, and rollout
S0-Dependencies: CTL, PROBE, ID, RESOURCE, OUT, TRUST, ROOM
S0-Inputs: generated tool registry, signed manifest, policy snapshot, CTL attestation, and provider join evidence
S0-Outputs: effective snapshot, connection lease, typed tool response, cursor, and lifecycle decision
S0-Failure: schema/profile/hash/capability mismatch drains or rejects before mutation and never deletes enum branches
S0-Recovery: reinitialize the profile and query the original command/cursor; stale snapshots drain before replacement
S0-Field-Chain: registry -> manifest/policy hash -> CTL join -> effective snapshot -> handler -> structured result
S0-Named-Acceptance: TOOLS-P01 ManifestSchemaRoundTrip; TOOLS-P04 HandleFenceAndRoomDeny; TOOLS-P07 RouteLifecycleAndConcurrentMutation; TOOLS-N04 DuplicateToolNameRejected; TOOLS-N05 SourceHandleFailClosed

负责人：tool-surface/rollout owner；本文件冻结 generated MCP schema、profile、policy、连接租约和发布收口。
用户批准推进计划不等于本草案 Accepted；现有 initialize/list_agents 证据不等于 unified_v2 已发布。
本规格保持 Draft；`unified_v2`、Room fan-out/round 与 managed-agent external dispatch 在生产环境均为 NO-GO，以下合同和 fixture 只定义 Gate S0 规划验收。

## 1. 范围与边界

TOOLS 只拥有公开 MCP tool surface、canonical manifest、workspace lifecycle policy 和其版本化 rollout。
identity、resource、outbox、trust、room、CTL 和 provider bridge 仍由各自 owner 写入事实，TOOLS 只消费有界投影。
BRIDGE 不是普通祖先；它仅作为每个 provider 的 enable join gate，不能改写 tool schema 或强制删除未支持 kind。
`launch_agent(kind=managed_agent)` 是 managed execution；它不能因为通过 MCP surface 就变成 native_subagent 或 fully_controlled。
`observed`/`managed`/`fully_controlled` 由 capability requirement join 得出，不能由工具名称、UI label 或 provider 名称猜测。

## 2. 依赖与 owner 职责

TOOLS 依赖 CTL 的 profile/session/cancel/size contract 和 server-issued connection LeaseKey。
TOOLS 依赖 PROBE 的 provider capability evidence，依赖 ID/RESOURCE/OUT/TRUST/ROOM 的可消费 owner API。
generated schema owner 从唯一 registry 生成 tool definitions、input/output schema、enum 和 manifest digest。
release signer 生成 domain-separated、host-pinned trust root 下的 non-authorizing manifest artifact，并维护 rotation/revocation/floor。
workspace lifecycle owner 只生成 policy snapshot、decision digest 和 admission；不能生成第二套 schema。
sidecar surface owner 原子提交 effective snapshot/connection lease；UX 只能通过只读 projection 展示。
response assembler 按同一 registry 校验所有 success、partial、timeout 和 typed error 的 structuredContent。

generated registry 以 canonical `(profile, lifecycle, name)` 作为唯一 tool identity；同一 profile 内重复 name、跨 profile 同义 collision、空/大小写折叠 collision、缺 domain separator 或 manifest hash 不一致都在 registry 初始化时 fail-fast。不同 profile 的同名工具必须绑定不同 `profile_id`/`tool_domain`，不能由 UX label、反向 `tools/list` 或运行时发现结果覆盖 canonical entry。`TOOLS-N04 DuplicateToolNameRejected` 需验证重复/折叠/跨域 collision 均零 registry、零 manifest、零 handler 副作用；这是规划验收名称，不代表当前 registry 已获批准。

## 3. Profile 与版本隔离

`legacy_v1` 保留当前兼容 schema 与回归入口；旧 `mcp/*` 只作明确 deprecated alias，不加载 unified tools。
`standalone_http_local_v1` 是当前 loopback bearer/有限 session 形态；尚未证明 strict Streamable HTTP 全量 replay/cancel。
`strict_unified_v2` 固定 MCP core revision、server profile、workspace policy、完整 tool manifest 和 native exact-set。
stdio 每个 profile 对应一条 connection；HTTP 每个 profile 对应独立 authenticated endpoint/deployment，不能共用 session。
同一 profile 不得同时暴露 legacy 与 unified 同义工具；不同 API version、manifest hash 或 policy revision 不得复用连接。
profile 不匹配、协议回退、旧 generation 或 lifecycle snapshot stale 会停止新 mutation admission 并 drain，而非动态删工具。

## 4. Canonical 工具与输入输出

unified_v2 固定暴露 `launch_agent`、`send_message`、`interrupt_agent`、`stop_agent`、`recover_agent`。
它还固定暴露 `list_agents`、`get_agent_events`、`wait_agents`、`manage_agent_room`、`broadcast_message`、`run_agent_round`。
schema 始终保留完整 kind enum；缺 capability 时 handler 返回 typed `capability_not_supported`，不按 provider 动态删 kind。
AI 默认链为 `list_agents(view=route) -> mutation(handle) -> wait_agents/get_agent_events`，route 不暴露 full fence。
`view=diagnostic`、`kind=full_fence|full_source` 分别需要独立 `agent.diagnostic.read`/`agent.low_level.mutate` grant。
route 返回 logical/topology、kind、state、control status/level、missing requirements、opaque handle 和 last cursor。
diagnostic 才返回 applicable scoped lease/evidence/incarnation summary；secret、credential、raw provider receipt 永不返回。
mutation handle 在 acceptance 重新解析 AEAD/server record、expiry、scope、incarnation/turn fence；derived field 不授权。

### 4.1 Generated route/source-handle contract

`list_agents(view=route)` 必须由 generated registry 生成稳定 route schema；任何手写 DTO、运行时 `tools/list` 反向推断或 UI 私有扩展均不构成 schema。即使 Agent Graph 为空，顶层仍返回紧凑 `launch_profiles[]`，每项固定 `profile_id`、`kind`、provider/runtime/gateway label、`creation_modes`、`enabled/status` 与 `missing_requirements`。route page 的 node 只允许 logical/topology、display label、canonical `state`、`control_status`、`control_level`、derived supported actions、missing requirements、nullable `execution_handle`/`turn_handle`、last cursor 和：

```json
{
  "next_actions": [{
    "action": "fork|recover|relaunch|read|wait|send|interrupt|stop",
    "source_kind": "active_execution|last_closed_execution|fork_source|failed_reservation|launch_reservation",
    "source_handle": "opaque-server-token",
    "handle_expires_at": "server-time",
    "required_inputs": ["... generated names ..."]
  }]
}
```

`next_actions[].source_handle` 与 `source_kind` 是 generated one-of；需要来源的 action 缺任一字段是 schema error，多余或混合 branch 也必须拒绝。`launch_reservation` 是独立的 launch reservation handle branch，至少绑定 reservation id/version、command id/version、`creation=launch|fork`、requested kind 与 workspace/run scope；`failed_reservation` 与 `launch_reservation` 不能按 nullable 字段互猜。handle wire value 必须是随机 nonce 的 versioned AEAD sealed token 或 server-side 随机 id+bounded-TTL repository，payload 绑定 profile、workspace/run、logical agent、incarnation/turn 或 reservation discriminator、source cursor、binding role、issued/expiry 与 key id，不能从 token 解出 provider/thread/session fence。

所有 source handle 只由 server 在 route、launch、stop 或 mutation response 中签发；它不是 grant、principal、capability 或 full fence。handler 在 acceptance snapshot 中按 `source_kind` 解析并重新 join ID/OUT/RESOURCE/TOOLS/ROOM 原始 owner，dispatcher 再次校验持久化 full source。过期、key rotation、profile/API revision、workspace/run/room scope、binding role、source cursor、old incarnation/turn 或 reservation version/state 不一致，统一返回 typed `handle_expired|stale_incarnation|cross_scope_handle|reconciliation_required`（具体 code 由 generated error registry 固定），不查询模糊 `agent_id`、不泄漏对象存在性、不产生副作用。

route schema 同时冻结 opaque page cursor 与分层 wire caps：route 每页最多 100 nodes、`launch_profiles` 32、单 node 32KiB、encoded `CallToolResult` 192KiB、兼容 text 8KiB；diagnostic 每页最多 200 nodes、每 node incarnation summary 8、launch/runtime profiles 64、单 item 256KiB、encoded result 768KiB、兼容 text 16KiB。cursor 必须绑定 profile/view、scope、filter、page/order digest，达到 cap 只返回 `has_more` 与对应 opaque `next_cursor`，不得截断或跨页重排 item；超过单 item cap 返回 `result_item_too_large`。这些上限由 generated profile-limits registry 供 handler、response assembler 与 UI 共同消费。

## 5. Manifest、policy 与 rollout 状态机

manifest 状态为 `unverified -> signature_verified -> anti_rollback_checked -> lifecycle_seeded`。
policy 状态为 `unseeded -> policy_signed -> ctl_joined -> effective_snapshot_committed`。
native exact-set join 后才进入 `serving`；撤销、断连、过期、hash drift 进入 `stale -> draining -> revoked`。
任何 `missing|unknown|tampered|profile_unverified|reconcile_required` 状态均禁止公开 strict mutation。
安装/升级顺序固定：验证 manifest signature/floor → seed/reconcile tool ids → sign policy → join CTL → join sidecar → expose。
manifest artifact 的 payload 以 RFC 8785 JCS 或等价 canonical bytes 计算 `build_artifact_root`/`tool_manifest_hash`。
hash 覆盖 `name/title/description/icons/inputSchema/outputSchema/annotations/execution.taskSupport/_meta` 及未来 wire 字段。
签名 envelope 绑定 release/build namespace、signer key id/scope、profile/binary digest、issued/expiry、revocation epoch。
host 只信任预置或安全轮换的 trust root 与 monotonic release floor；artifact 内嵌公钥和 self-sign 一律拒绝。
新 tool 必须有 lifecycle decision；旧 tool 删除必须显式记录，不能以 reverse `tools/list` 或 UX cache 补 schema。

## 6. Effective snapshot 与 per-provider join

effective snapshot 的 canonical inputs 固定为 workspace、MCP revision、server profile、完整 manifest hash。
它还包括 policy revision/hash、适用 CTL peer attestation、native suppression evidence 和 connection instance/epoch。
snapshot hash 不含 snapshot id、issued_at、expiry；相同 inputs 必须重算相同 hash，connection epoch 变化立即失效。
sidecar 只有在 full policy、manifest、CTL attestation 和 provider-independent mandatory bundle 齐全时才提交 snapshot。
`native_tool_suppression.exact_set` 必须证明 MCP tools/list 与 provider/model 同义 agent tools 均被抑制或隔离。
BRIDGE 通过 PROBE 的 per-provider capability join 后，才可为该 provider 发 unique enable decision；其余 provider 仍独立裁决。
bridge 证据不能代替 tool manifest/policy hash，也不能反向给 agent capability；一 provider unsupported 不会改变其他 provider schema。
缺 exact-set 时只允许 legacy/observed/managed 接入，strict unified profile 必须保持 disabled 或 drain。

## 7. Tool call、等待、取消与资源上限

handler 先校验 CallerPrincipal、profile/session/connection lease、tool lifecycle decision、capability、resource 和 content ACL。
所有 arguments 使用 bounded decode；单 frame/body、arguments、content、targets、result 和兼容 text 均受 profile 上限约束。
response 状态为 `running -> response_queued -> completed`，或 `cancelled|timeout|rejected|subscriber_overrun|indeterminate`。
`wait_agents/get_agent_events` 是 bounded long-poll；watcher/worker/response buffer 有 connection、principal、process 级上限。
`notifications/cancelled` 只匹配同 session/direction 且仍 running 的 request；initialize 不可取消，unknown/late cancel 只审计。
HTTP 断流只 detach delivery；session DELETE/expiry 才撤销 running 并丢弃未交付结果，不能把网络抖动写成 provider failure。
timeout 是非 terminal 的 tool response（除非底层 predicate 已明确 terminal），不能把超时重派成另一 command。
同 agent/incarnation 的 mutation 仍由 OUT 串行；读侧 watcher 不得阻塞 interrupt、stop、send_message 或 list_agents。

同一 connection 的 wait/follow、interrupt、send_message 和 cancellation 必须共享有界 request registry，但使用独立 worker/优先级与 request id；只读 watcher 不持有 mutation lock。一个 wait/follow 被取消、超时、断流或慢消费者逐出时，只结束该 request 的 delivery/execution 分支，不能取消或重写并发 mutation；interrupt/send 的 accepted、rejected、unknown 也不能被 wait/follow 的 timeout 当作成功。取消必须精确匹配同 session、direction、JSON-RPC id 与仍 running request；late/foreign/unknown cancellation 零 domain side effect。

`launch_agent`、`stop_agent`、`recover_agent` 与 `creation=fork` 形成显式 source-discriminator 生命周期：stop 先收口 runtime/active-process/binding 并产生 `last_closed_execution` source handle；recover 只消费 `active_execution|last_closed_execution|failed_reservation` 的对应 source handle，按 source-specific CAS 创建新 incarnation；relaunch 只消费 `last_closed_execution|failed_reservation|launch_reservation`，fork 只消费 `fork_source`（或具备同等 source branch 的 diagnostic full source）。旧 execution handle/follow 永不跳转到新 incarnation，调用方必须由 route 重新 list、重新授权并显式切换。launch/stop/recover/relaunch/fork 的 pending、failed、`outcome_unknown` 都必须保留同一 reservation/command discriminator 与 source handle：`get_agent_events`、`wait_agents` 和 read 只能在该固定 selector 上查询；activated result 明确回显新 `result_execution_incarnation_id`，不得把 reservation 状态猜成 runtime success。response unknown 只 query 原 operation/submission/attempt，不生成 fresh handle 或第二 command。

对于 pending/failed/unknown launch，route 与 mutation response 均使用 `source_kind=launch_reservation|failed_reservation` 的 reservation handle；`wait_agents` 的 `command_submission|command|event` fence 必须与该 reservation/command id、version、namespace、key digest 和 pending cursor exact 相等。pending 只能返回 `timeout|indeterminate`，failed 只能返回 authoritative `rejected|failed`，unknown 只能返回 `indeterminate|outcome_unknown` 并提示查询原 attempt；三者均禁止模糊 fallback、自动重试或把 provider ACK 当 terminal。

## 8. 字段消费链

generated registry → canonical rendered definitions → signed manifest artifact → anti-rollback trust verification。
manifest hash + policy snapshot → CTL peer attestation → sidecar effective snapshot → connection tool-surface lease。
initialize/session → CallerPrincipal/connection epoch → tool handler → ID/RESOURCE/OUT/TRUST/ROOM scoped owner。
mutation result → response assembler 读取 canonical outputSchema → structuredContent/error validation → single writer。
event/content cursor 与 page cursor 分离；UX projection 只能消费 sidecar/事件只读流，不能成为 schema 或 auth SSOT。
字段 guard 必须追踪 kind、creation、delivery、busy policy、handle、fence、cursor、receipt、error 和 room selector 全链。
unknown/duplicate/mixed branch、schema id drift、additional field、result overrun 或 missing decision 在 writer 前 fail-fast。

## 9. 权限、Room 与恢复

route、diagnostic、low-level mutation、peer/read/send、room membership 和 content read 是独立授权，切换 view 不升权。
共同 Room 只授权显式 room read/broadcast/round 时间范围，不授予私有历史或 direct peer send；Room owner/ROOM 负责其 head/receipt。
跨 run、跨 workspace、跨 incarnation、过期 handle、stale cursor、foreign principal 均拒绝且不泄露对象存在性。
recover 后旧 selector/follow 不跳新 incarnation；调用方必须重新 list、重新授权并显式切换 handle。
snapshot stale 或连接断开时，accepted/dispatched command 由 OUT 保留并收口；新 connection 必须重新 initialize/policy/exact-set。
manifest/policy/bridge hash drift、trust revoke、release rollback、CTL gap 或 effective snapshot 不可读均进入 fail-closed drain。
恢复只 query 同 operation/idempotency key，不能凭 cached projection、UX cache 或 provider ACK 重签 permit。

## 10. 当前 standalone 证据与缺口

ST-05 真实 Codex app-server MCP `list_agents` 已通过，证明连接/工具调用，不证明 unified manifest/strict policy。
ST-06 真实 Claude MCP 连接与 tool-call 已通过，仍未覆盖完整 HTTP session/replay/cancel 和发布环境。
ST-07 独立 binary 的 Codex/Claude managed 双轮已通过本地 wrapper，不能作为 native child 或 production rollout evidence。
ST-08 direct-peer 双向本地证据仍用 trusted scope；Room、完整 receipt、第三方 bearer grant 和跨 incarnation 未闭合。
当前 tool mapper/registry 的静态回归可证明部分生成链，但未证明签名 manifest、trust rotation、anti-rollback 和 native exact-set。
ST-09/11/14/16～18 仍待实现或验证；HTTP full replay、bounded cancel、unknown outcome、Room、旧 adapter/stdio proxy均不能宣称完成。
所有上述本地结果均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不改变 strict profile 的 disabled/未发布结论。

## 11. 具名验收与退出条件

TOOLS-P01：同一 registry 生成完整 input/output schema 与 manifest hash，未来 wire 字段变化能触发 hash/guard。
TOOLS-P02：签名、trust root、revocation、anti-rollback、fresh install、new-tool seed/reconcile 全链可重放且无 reverse bootstrap。
TOOLS-P03：legacy、standalone_http_local、strict unified、stdio 的 profile/session/cursor/tool hash 隔离通过 mixed-version 负测。
TOOLS-P04：route/diagnostic grant、handle/fence、CallerPrincipal、Room 不扩权、跨 run/incarnation deny 均有零副作用负例。
TOOLS-P05：success/partial/timeout/error 结果均在 writer 前通过 outputSchema；long-poll、cancel、slow consumer、byte cap 有 bounded evidence。
TOOLS-P06：每 provider 的 BRIDGE join 是独立 enable gate；managed 创建不升级 native_subagent，observed 不升级 fully_controlled。
TOOLS-P07：generated route 在 Agent Graph 为空时仍返回紧凑 `launch_profiles[]`，并由 `next_actions[].source_handle` 驱动 stop→recover/relaunch/fork 及 pending/failed/unknown 的原 selector 查询；同 connection wait/follow 与 interrupt/send/cancel 并发不互相阻塞。
TOOLS-N01：self-signed/replayed/mixed binary-manifest、动态删 tool、reverse tools/list、UX cache 补 id 必须 fail closed。
TOOLS-N02：缺 policy、CTL attestation、native exact-set、provider capability、decision、receipt 或 connection epoch 时不得 serving。
TOOLS-N03：把 MCP initialize、用户批准、tool ACK、route label 或本地测试 exit 0 写成 rollout Accepted 必须被守卫拒绝。
TOOLS-N05：route/source handle 缺 discriminator、过期、旧 incarnation/turn、跨 workspace/run/room scope、reservation version/state 或 key rotation 时必须零副作用 fail closed；UI/handler 不得按裸 agent_id fallback 或自行生成 handle。

规划中的具名 RED/GREEN fixture（当前不是实现证据）：

- RED `TestToolsConcurrentWaitFollowAndMutation_RED`：故意让 wait/follow 持有 mutation 锁或让 cancellation 使用错误 request namespace；fixture 必须先证明 interrupt/send 被阻塞或错误取消可发生，作为 fail-first 基线。
- GREEN `TestToolsConcurrentWaitFollowAndMutation`：同一 authenticated connection 并行启动 bounded `wait_agents`/`get_agent_events(follow=true)`，同时发起 `interrupt_agent`、`send_message`，再精确取消其中一个 wait；控制操作在 SLA 内完成，取消只结束目标 request，response 不串线，watcher/worker/buffer 最终归零。
- GREEN `TestToolsRouteEmptyGraphReturnsLaunchProfiles`：空 Agent Graph 仍返回 generated 紧凑 `launch_profiles[]`，每个 launch action 具 typed source discriminator 或明确 `required_inputs`。
- GREEN `TestToolsLaunchReservationSourceHandleBranches`：launch pending/failed/unknown 的 reservation handle 可按原 command/submission fence follow/wait/read，activated 显式返回新 incarnation；不得 fallback 到 agent id。
- RED/GREEN `TestToolsRouteHandleScopeAndIncarnationFailClosed`：过期、旧 incarnation/turn、key rotation、reservation version/state 与跨 workspace/run/room handle 均先由 RED 复现误接受，再由 GREEN 证明 typed error、provider/command call=0。
- RED/GREEN `TestToolsRouteCapsNoTruncate`：超 route/diagnostic page、profile、item 或 encoded/text cap 先由 RED 复现截断/无界分配，再由 GREEN 证明 `has_more+opaque next_cursor` 与 `result_item_too_large`，不截断 item。

退出条件：manifest/policy/effective snapshot、签名与 floor、profile isolation、field mapper、limits、授权/恢复和 named fixture 全部可复查。
在 TOOLS 与其全部祖先规格、DAG、发布/回滚 evidence 获批前，本文件保持 Draft，不开启 unified_v2 production rollout。
