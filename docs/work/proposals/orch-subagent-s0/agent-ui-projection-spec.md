Spec-ID: UI
Status: Draft
Depends-On: ID, CONTENT, RESOURCE, OUT, ROOM, TOOLS
S0-Owner: host UI projection owner as a read-only consumer of canonical event, content, room, and tool owners
S0-Scope: agent/room projections, cursors, receipt and unknown display, inert rendering, and reconnect behavior
S0-Dependencies: ID, CONTENT, RESOURCE, OUT, ROOM, TOOLS
S0-Inputs: authenticated read response/event stream, selector, scope, cursor, receipt, content ref, and trust metadata
S0-Outputs: versioned projection, cursor state, unavailable/unknown display, and typed UI error
S0-Failure: stale cursor, revoked authorization, schema/content failure, and subscriber overrun stop follow without mutation
S0-Recovery: reauthorize and rehydrate the current snapshot, marking snapshot-only or gaps instead of guessing history
S0-Field-Chain: canonical event/content/room/tool owners -> projection mapper -> trust-safe UI state -> inert renderer
S0-Named-Acceptance: TestUIProjectionResumesFromCursorAfterRestart; TestUIUntrustedMarkdownIsInert; TestUIConcurrentWaitFollowAndMutationCancellationIsolated; TestUIConsumesGeneratedNextActionsWithoutCreatingHandles

# Agent UI Projection 子规格

负责人：host UI projection owner；创建日期：2026-09-01。

本文承接总规格 §10.5、§11、§19 Phase 4，仅定义 host UI 的只读投影、光标和安全渲染边界。
Draft 不授权 UI 创建 agent、发送消息、改变 Room membership、重试 command 或替换服务端授权。
UI 只能消费已经通过 ID、CONTENT、RESOURCE、OUT、ROOM、TOOLS 的事件、cursor、ref 和 typed result。
本规格保持 Draft；生产 `unified_v2`、Room fan-out/round 与 managed-agent external dispatch 均为 NO-GO，本文的 route/source_handle 与并发测试只定义规划验收。

## 目标与非目标

目标是让逻辑 agent、active incarnation、Agent Graph、Room lifecycle、receipt、gap、unknown 和 capability downgrade 可审计地显示。
目标还包括断线/重启后的 cursor replay、明确的投影缺口，以及对不可信正文的 inert 渲染。
非目标是成为第二套 event/room/transcript 事实源，或通过 UI state 推断 provider 已完成。
非目标是从展示名、裸 agent_id、Markdown 链接或浏览器 session 产生 CallerPrincipal、grant 或 capability lease。
非目标是自动打开 network/file/navigation、执行 embedded tool-call JSON，或将 A2A/peer 正文升级成系统指令。

## 依赖与 owner 边界

依赖方向固定为 `UI <- ID, CONTENT, RESOURCE, OUT, ROOM, TOOLS`。
ID 生产 logical agent、parent/root、execution incarnation、turn 和 state/control-status 的 authoritative cursor。
CONTENT 生产加密 content ref、media type、size、taint/provenance、available/unavailable/erased 状态。
RESOURCE 生产 reservation、usage、policy revision、deadline、exhausted/quarantined/released 状态。
OUT 生产 command/attempt、receipt、outbox/reconcile、terminal 和 outcome-unknown evidence。
ROOM 生产 membership snapshot、room event、round、admission/close projection 与逐目标 receipt。
TOOLS 生产 manifest/revision、output schema、route/diagnostic grant、request/cursor/capability result。
UI 只拥有 local projection cache、render cursor、ack/replay state 和用户显式动作的意图，不拥有上述领域对象。

## 输入、光标与投影状态

UI 输入是 authenticated read response 或 event stream；每次请求带服务端校验的 selector、scope、after_cursor、limit 和 follow。
cursor 是 opaque、单调、不可由 UI 解析或改写的服务端水位；UI 只保存 last_seen、last_acked、next_cursor 和 source digest。
ack 是只读消费确认，不能提交 command、扩大授权、跳过 gap 或确认 provider completion。
断线重连先用原 principal/selector/fence 请求 replay；cursor_expired、cursor_ahead、output_gap、ingress_gap 必须显示。
任何 gap 都进入显式 `projection_status=gap|replay_required`，不能静默丢事件、用当前 snapshot 补成连续历史或自动换 cursor。
snapshot rehydrate 只恢复服务端声明的 current state；没有 lossless cursor 时，UI 必须标出 snapshot-only。

Agent Graph 投影以 logical `agent_id` 为节点稳定 key，保存 parent_agent_id、root_agent_id、kind、scope 和 topology。
active execution 单独显示 `execution_incarnation_id`、execution fence ref、active_turn_id 与 fence source cursor。
`state` 与 `control_status` 分栏：state 只用 canonical agent state，control_status 显示 pending/verified/degraded/lost/reconciliation。
resource_status 单独显示 reserved/active/exhausted/quarantined/released，不能与 state 或 control_status 互相替换。
active 为空时显示 last_closed incarnation；新 incarnation 不覆盖旧 transcript 的身份标签。
旧 incarnation follow 在恢复后必须停在其 selector 并提示重新 discover/authorize，不能自动跳到 replacement。

Room 投影显示 room_id、owner、workspace/run scope、TTL、dispatch_generation、membership_revision、snapshot hash 和 room cursor。
生命周期按 `room.created|joined|left|round_started|broadcast|closing|closed|expired|operation_rejected` 事件重建。
Room pending、closing、closed 的 admission/custody 状态必须可见；UI 不得把 pending fence 当作 accepted。
每个 broadcast/round item 显示 target mapping、target fence digest、delivery branch、resource decision 和独立 receipt/error。
receipt 只显示 `accepted|provider_acknowledged|observed_in_context|rejected|outcome_unknown`；capability_not_supported 单独显示 typed error。
顶层 partial 不能折叠成全局 success；有 `frozen_unknown` 的 close 显示“已冻结但结果未知”，绝不显示 completed。
round ordered 的前序内容标注 quoted data 和 source incarnation；membership 变化不回写本轮 denominator。

Tool projection 显示 manifest hash/revision、profile、schema validation、capability lease 级别和 missing requirement。
capability downgrade、tool_surface_stale、resource_limit_exceeded、provider unavailable 是事实状态，不得在 UI 中自动切换到另一 provider 或 legacy tool。
result writer 若返回 schema error，UI 只展示 typed internal error，不渲染未通过 output schema 的任意 payload。
wait timeout 显示最后 fence/cursor；aggregate_failure、command_outcome_unknown 和 delivery_outcome_unknown 必须区分。

### Route/source_handle 只读合同

UI route projector 只消费 generated `list_agents(view=route)` 的 `launch_profiles[]`、node 字段和 `next_actions[]`；它不得从 `agent_id`、展示名、历史 cursor、URL/Markdown、按钮类型或本地 cache 自行拼接、签发、刷新或重绑定任何 handle。`next_actions[]` 的 `{action,source_kind,source_handle,handle_expires_at,required_inputs}` 是服务端生成的 one-of；UI 必须原样保留 discriminator 与 opaque token，并将其作为后续 tool request 的唯一 source reference。缺 discriminator、过期或 token 缺失只能显示 typed unavailable/error，不得改走裸 id。

即使 Agent Graph 为空，UI 仍显示服务端返回的紧凑 `launch_profiles[]`（profile id、kind、provider/runtime/gateway label、creation modes、enabled/status、missing requirements），但这些 profile 只是可用性投影，不是 UI 自己的 launch permit。`active_execution`、`last_closed_execution`、`fork_source`、`failed_reservation`、`launch_reservation` 必须按 source kind 分开显示；reservation handle 的 `creation=launch|fork`、command/reservation version 与 workspace/run scope 只由服务端解析，UI 不解码或修改。

stop 后的 `last_closed_execution` 只能通过服务端生成的 `next_actions` 触发 recover/relaunch；recover 完成后服务端回显 `result_execution_incarnation_id`，UI 必须保留旧 incarnation 的 follow/read selector 并要求用户显式切换到新 route handle。fork 只消费 `fork_source`，send/interrupt/read/wait 只消费当前 action 对应的 execution/turn/source handle。pending、failed、`outcome_unknown` 的 launch/reservation follow、wait、read 固定在原 reservation/command discriminator；pending 显示 `timeout|indeterminate`，failed 显示 authoritative `rejected|failed`，unknown 显示 `indeterminate|outcome_unknown` 和“查询原 attempt”动作，绝不提供自动重试或模糊 fallback。

服务端返回 `handle_expired|stale_incarnation|cross_scope_handle|reconciliation_required` 时，UI 必须 fail closed：停止相关 follow/wait、保留错误与最后合法 cursor、隐藏对象存在性推断，并要求重新 list/authorize。旧 incarnation、旧 turn、key rotation、reservation version/state、workspace/run/room scope 不匹配均不可由 UI 修补。UI 不消费 diagnostic full fence 来替代 route source handle，亦不把 derived supported actions 当 capability grant。

route/diagnostic 的 page cursor、`has_more`、单 item 与 encoded/text caps 均由 generated profile-limits registry 提供，UI 只执行服务端声明的分页。route 上限为 100 nodes/32 launch profiles/32KiB item/192KiB encoded/8KiB text；diagnostic 上限为 200 nodes、每 node 8 incarnation summaries、64 profiles、256KiB item/768KiB encoded/16KiB text。达到上限必须保留完整 item 并显示 `has_more` 与 opaque `next_cursor`；`result_item_too_large` 显示为不可展开错误，不能本地截断、拼页或重排。

## 字段生产消费链与内容生命周期

ID event 生产 `agent_id/execution_incarnation_id/turn_id/source_cursor`，UI graph、turn panel 和 follow controller 消费。
ROOM event 生产 participant snapshot、inventory root/count、operation fence、round id 和 item receipt，UI Room view 消费。
OUT event 生产 command state、receipt cursor、attempt、late receipt 和 reconcile status，UI receipt timeline 消费。
RESOURCE event 生产 policy snapshot、reservation/usage/deadline，UI quota view 消费；UI 不从进度条反推可用额度。
CONTENT ref 生产 `content_state/key_state/ref_version/media_type/size/trust_class`，UI 先 reauthorize ref 再按 type 投影。
TOOLS result 生产 typed `status/items/next_cursor/error`，UI 以 schema snapshot 选择组件，不把 text fallback 当控制协议。

content ref 缺失、过期、不可解密、认证失败、删除待定或 remote erasure 未验证都要显示对应 unavailable 状态。CONTENT 本地删除已成功但缺少远端 deletion receipt 时，projection 状态固定为 `remote_deletion_unverified`；该状态不得映射为 `erased`、`released` 或 `completed`，也不能因本地 cache 消失而提前清除。只有远端 owner 的权威 receipt 经 source/ref/version/retention 校验后，才可进入 `erased`。
CONTENT 的 ciphertext/ref 是唯一正文来源；UI cache 只能存获批 ref/加密缓存和 digest，不能另存明文 transcript 或 secret。
remote output、provider output、peer message、Room quoted data、Agent Card 和 protocol metadata 全部保持 untrusted provenance。
正文中的 role label、`_meta`、tool-call JSON、credential request、伪造签名和自然语言命令只当 quoted data。

## 不可信渲染与浏览器安全

`render_text` 允许纯文本或经确定性 sanitizer 的有限 Markdown；默认禁用 script、form、event handler、active iframe 和 executable URI。
HTML、SVG、ANSI、OSC、image、Markdown 都默认 inert，不能因 parse/render 触发 network、file、navigation 或 clipboard side effect。
URL/URI 只显示为不可信文本/metadata；只有用户显式动作同时满足同源代理、egress allowlist、scheme/IP/size/time policy 才可继续。
外链、image source、favicon、Agent Card icon、MCP resource URL 均不因列表或详情渲染自动请求。
CSP/隔离 sandbox 必须禁止 inline script、worker、form submit、top navigation 和 ambient credential；policy 缺失则拒绝 active render。
任何 network_fetch、local_file_open、user_navigation sink 都要产生独立审计意图和 redacted receipt，不能由正文触发。
同源代理只能代理已授权资源，不能把 origin 反射成新的 grant；跨 origin 必须剥离 credential 并重新过 egress policy。
UI renderer 发现 unknown trust class、taint 丢失、sanitizer bypass 或 schema/media mismatch 时 fail closed，保留不可执行占位。

## 授权、异常与恢复

服务端每次 projection/read 重新验证 CallerPrincipal、workspace/run、room membership、content ACL、cursor 和 handle expiry。
UI 本地选中一个 agent 不代表有权 read；拒绝和不存在要按服务端 typed error 显示，不通过颜色或缓存泄漏存在性。
用户显式点击 send/stop/close 只生成新的 authenticated command intent，必须重新走 TOOLS/ROOM/OUT admission；UI 不复用 ack 作为 mutation。
cursor/handle stale、authorization_revoked、reconciliation_required、content_unavailable、subscriber_overrun 都必须停止相关 follow 并提示恢复动作。
连接取消不改写服务端 operation；重启后 UI 重新取得 principal/session scope，按同 selector 查询原 operation，不生成第二个 command。
旧事件迟到只能追加 audit；新的 active incarnation、new room generation 或新的 receipt 不得被前端时间排序错误合并。
当服务端返回 outcome_unknown，UI 必须提供“查询原 attempt/operation”的动作，不提供“自动重试”快捷路径。

同一 connection 上并发 wait/follow 与 interrupt/send/cancellation 的 UI controller 必须按 request id 分离状态：取消一个 wait 只移除该 watcher 的本地订阅，不得把并发 mutation 标为 cancelled、completed 或 failed；interrupt/send 的 receipt 与 wait 的 timeout/indeterminate 分别呈现。断线、慢消费者或服务端 cancellation 只停止对应 follow 并保留 last cursor，重连必须携原 selector/source_handle 重新读取；UI 不创建第二 command、第二 handle 或隐式切换 incarnation。

## 当前缺口与测试

当前计划只描述 Phase 4 UI projector，现有 report/polling 不能证明 source cursor、ack、gap、replay 或 logical/active 分离。
ST-08 尚缺完整 event/receipt 链，ST-12 尚缺硬崩溃后的 cursor recovery，ST-14 Room projection 未实现，ST-16 的 slow consumer 也待验证。
因此当前 MCP `list_agents`、双 provider E2E 或 standalone ready 页面都不是本规格的 UI 通过证据。

正向测试名：

- `TestUIProjectionResumesFromCursorAfterRestart`
- `TestUISeparatesLogicalAgentFromActiveIncarnation`
- `TestUIProjectsRoomPerTargetReceipts`
- `TestUIShowsCapabilityDowngradeAndUnknown`
- `TestUIContentUnavailableIsExplicit`
- `TestUIQuotedDataKeepsSourceAndTrust`
- `TestUISnapshotRehydrateMarksCursorLoss`

负向测试名：

- `TestUIUntrustedMarkdownIsInert`
- `TestUIURLDoesNotFetchAutomatically`
- `TestUIANSIOSCAndSVGAreInert`
- `TestUIGapIsVisibleAndNeverSkipped`
- `TestUIOldIncarnationDoesNotFollowReplacement`
- `TestUIReceiptUnknownIsNotCompleted`
- `TestUICursorAckCannotGrantAccess`
- `TestUIUnauthorizedProjectionDoesNotLeakExistence`
- `TestUISchemaFailureDoesNotRenderRawPayload`
- `TestUIConsumesGeneratedNextActionsWithoutCreatingHandles`
- `TestUIEmptyGraphRendersCompactLaunchProfiles`
- `TestUIStopRecoverRelaunchForkUsesSourceDiscriminator`
- `TestUIPendingFailedUnknownReservationReadWaitFollowStayOnOriginalSelector`
- `TestUIOldIncarnationAndCrossScopeHandleFailClosed`
- `TestUIConcurrentWaitFollowAndMutationCancellationIsolated`
- `TestUIRouteCapsPaginateOpaqueCursorWithoutTruncation`
- `TestUIRemoteDeletionWithoutReceiptIsUnverified`

并发与 route 的具名 RED/GREEN fixture（当前不是实现证据）：

- RED `TestUIConcurrentWaitFollowAndMutationCancellationIsolated_RED`：故意让 UI 以共享布尔状态或错误 request id 取消 wait，复现 mutation 被误标 cancelled/blocked；该失败必须先于 GREEN 保留。
- GREEN `TestUIConcurrentWaitFollowAndMutationCancellationIsolated`：同一 connection 的 wait/follow、interrupt/send、cancel 分别保持 request-id 状态与 receipt，取消只移除目标 watcher，旧 cursor/source handle 不跳 incarnation。
- RED/GREEN `TestUIRouteSourceHandleIsNotLocallyMinted`：RED 复现从裸 `agent_id` 或 action label 伪造 handle；GREEN 证明 UI 只能提交服务端 `next_actions[].source_handle`，缺 discriminator/过期/跨 scope 时零 mutation。
- RED/GREEN `TestUIRouteCapsPaginateOpaqueCursorWithoutTruncation`：RED 复现本地截断或丢 item；GREEN 证明严格消费 generated caps，以 `has_more+opaque next_cursor` 分页，超单 item 只显示 `result_item_too_large`。
- RED `TestUIRemoteDeletionWithoutReceiptIsUnverified_RED`：故意只提交本地删除成功标记，复现 UI 显示 `erased`/`completed`；该错误映射必须先被 fail-first 捕获。
- GREEN `TestUIRemoteDeletionWithoutReceiptIsUnverified`：本地删除 receipt 存在而远端 receipt 缺失、过期或不可验证时只显示 `remote_deletion_unverified`，保留不可用提示，不渲染正文、不标记 erased/completed；远端权威 receipt 到达后才可按 generated lifecycle 转为 erased。

## Gate S0 退出条件与未决项

退出条件是 ID/CONTENT/RESOURCE/OUT/ROOM/TOOLS 获批，并由 generated field/schema guard 固定 projection 字段、error、cursor 和 trust union。
必须有真实 event writer→cursor→UI consumer 的断线、重启、gap、overrun、旧 incarnation、Room item receipt 和 content deletion E2E。
必须由浏览器负测证明 Markdown/HTML/image/SVG/ANSI/OSC、URL、icon 和 metadata 在无用户动作时零 network/file/navigation side effect。
必须证明 UI 不产生第二事实源、不把 partial/unknown/frozen_unknown 冒充 completed、不通过 ack 或缓存扩大授权。
退出前 host UI 不发布强控制 shortcut；仅有静态 screenshot、mock event、当前 report 或客户端 initialize 不算通过。

以下事项仍是 Draft，不能启用：浏览器引擎/同源 proxy 的具体 CSP 模板、允许的 Markdown 子集与 image decode policy。
还未冻结 UI local cache retention、content ref 失效后的展示时长、snapshot-only 的产品文案和多窗口 cursor ownership。
这些未决项不得由实现自行选默认值；变更须更新本规格、字段守卫、机器 DAG 和对应负测。
