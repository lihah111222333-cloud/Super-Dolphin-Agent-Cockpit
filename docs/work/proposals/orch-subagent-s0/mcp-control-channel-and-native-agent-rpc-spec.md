# MCP Control Channel 与 Native-Agent RPC 子规格
Spec-ID: CTL
Status: Draft
Depends-On: VERIFY_S, ROLLOUT_S
S0-Owner: standalone control-plane owner for server sessions, leases, and native-agent RPC
S0-Scope: MCP control/data channel separation, profiles, session lifecycle, cancellation, and RPC framing
S0-Dependencies: VERIFY_S, ROLLOUT_S
S0-Inputs: server-owned principal, profile/endpoint/boot identity, connection lease, and bounded requests
S0-Outputs: authenticated session, lease receipts, typed RPC result, and capability error
S0-Failure: carrier/profile/lease mismatch rejects before handler side effects and closes unsafe sessions
S0-Recovery: reinitialize with the same boot-bound provisional namespace and query original operations after reconnect
S0-Field-Chain: server transport -> principal/profile -> session lease -> RPC request -> typed result/receipt
S0-Named-Acceptance: CTL-P01 SessionInitializeSingleWinner; CTL-N01 MissingLeaseHasZeroSideEffect

负责人：standalone control-plane owner；本规格冻结连接、租约、取消、会话和 native-agent RPC 的评审边界。
用户批准继续执行只是工作授权，不表示本草案已 Accepted，也不表示任何 provider mutation 已启用。

## 1. 目标与通道边界

CTL 让 standalone orch 在不依赖 V3 host 的情况下，为 bridge 提供版本化生命周期和 native-agent 控制通道。
工具执行通道与控制通道必须各有唯一 owner：stdio/HTTP MCP 处理 tool call，jrpc2/TCP 处理 `ctl/*`。
控制通道不是 MCP server，不接受标准 `tools/list`、`tools/call` 或把 MCP method 反向塞入 ctl callback。
`ctl/native-agent/*` 是 `ctl/*` 的版本化扩展族，不是第三条传输；同一 bridge 可同时持有 MCP 与 CTL 连接但不得混责。
standalone composition root 是本规格的 control owner；不得隐式要求 V3 app、V3 数据库、V3 runtime 环境或宿主 UI。

## 2. 依赖与 owner 职责

CTL 依赖 `VERIFY_S` 的 machine-DAG、wire-field mapper、foreign LeaseKey 和 crash/replay 验证骨架。
CTL 依赖 `ROLLOUT_S` 的 release/config digest、deployment epoch、trust root、anti-rollback 和 evidence cursor 约束。
control connection owner 独占 transport admission、principal、connection identity、LeaseKey 和 session registry。
lease owner 独占 `LeaseKey{instance_id,generation}` 的发行、撤销、sweeper 和 durable recovery cursor。
native-agent RPC owner 只校验 command/observation/event 的 source fence，并调用 identity、content、resource、outbox owner。
router 负责 `ctl/config/changed`、`ctl/shutdown` fanout；网络 I/O 不得持有 registry 锁，worker 与单 peer timeout 有界。
bridge 不能自签 lease、直接写 identity/current binding、跳过 command intent，或用 ctl 结果替代 provider receipt。

## 3. Profile 与版本隔离

profile registry 至少区分 `legacy_v1`、`standalone_http_local_v1`、`strict_unified_v2` 和 `ctl/native-agent/v1`。
`legacy_v1` 保留现有 MCP schema 与兼容 `mcp/*` 读取，仅作迁移回归；不能加载 unified tool schema。
`standalone_http_local_v1` 是当前 loopback bearer/有限 session profile，不能宣称完整 Streamable HTTP replay 或 strict long-follow。
`strict_unified_v2` 必须绑定 exact MCP core revision、server profile、tool manifest 和 lifecycle policy snapshot。
`ctl/native-agent/v1` 只在 authenticated jrpc2 peer 上协商；未知 revision、mixed profile、旧 generation 一律拒绝。
每个 MCP profile 对应独立 endpoint/process/connection 语义；HTTP、legacy、strict 不共享 session、cursor、tool hash 或 lease。
profile 变化先撤销旧 admission、标记 stale 并 drain；不能在现有连接上动态删 tool 或降级字段后继续服务。

## 4. 输入、输出与注册 DTO 合同

注册沿用 `ctl/register`、`ctl/heartbeat`、`ctl/context`、`ctl/event`、`ctl/log`、`ctl/approval/request`。
还包括 `ctl/report`、`ctl/shutdown`、`ctl/config/changed` 九个基线方法；hook 扩展须另行协商。
`RegisterRequest` 至少带 `instance_id`、`binary_name`、`agent_id`、`pid`、`boot_id`、`client_kind`、`peer_kind`。
它可带 `session_token`、`capabilities_offered/required`、`subscriptions`、`resume_from_generation`，不得带秘密到日志。
响应返回 server-issued `instance_id`、`generation`、`accepted_generation`、negotiated/rejected capability 和 heartbeat 参数。
注册完成后的每个 DTO 都把 `instance_id`、`generation` 作为顶层字段；禁止恢复裸 `lease_id` 或嵌套 `lease`。
`peer_kind` 限定为 `tool|ui|shared-service`；native bridge 不因自报 metadata 获得 UI 或 controller 权限。

native-agent v1 的每个请求固定 `{protocol_revision,request_id,operation_kind,instance_id,generation,subject,fence}`。
`subject` 是 `provider_session|execution_incarnation|turn|identity_observation` 判别联合，不得以裸 agent id 代替。
`fence` 必须绑定 provider/runtime identity、session epoch、incarnation、turn、source cursor、capability evidence 和 deadline。
有副作用的请求另带 `command_id`、`command_version`、`idempotency_namespace/key_digest`、content refs 和 resource reservation。
输出响应固定 `{request_id,status,source_cursor,provider_request_id_or_absent,recovery_receipt_digest_or_absent}` 及 typed error。
receipt 不确定只能返回 `outcome_unknown`/submission fence；不得把 transport ACK 写成 provider acknowledged 或 execution success。

## 5. 连接与请求状态机

连接状态为 `new -> authenticated -> registered -> active -> stale -> draining -> closed`。
拒绝状态为 `rejected|quarantined`；注册前除认证/注册外的业务方法零 domain allocation、零副作用。
同一连接只允许一次 active registration；重复、foreign LeaseKey、boot/profile/peer 不匹配必须 fail-fast。
断线或 heartbeat 超时使 lease stale，sweeper 撤销写 admission；旧连接不能凭 timeout 自动复活。
重连必须新建 connection identity 和 generation，并以 `resume_from_generation` 查询同一 source cursor；旧 generation 只可审计重放。
native-agent 请求状态为 `pending -> claimed -> running -> response_queued -> completed`。
合法中断为 `running -> cancelled`；超时是 typed timeout，不能同时标成 cancelled 再回包。
provider 已可能执行而 receipt 未知时进入 `outcome_unknown -> reconciled|failed`，只查询原 request/command。
session 删除/expiry 先取消仍 running 的请求，queued result 标记 discarded；HTTP 断流只 detach delivery，不取消 execution。

## 6. HTTP method-specific carrier

strict profile 在分配 session、worker 或 body reader 前解析 Origin、Accept、MCP-Session-Id、MCP-Protocol-Version、Last-Event-ID。
标量 carrier 要求按方法 exact-one/absent；重复、折叠、逗号列表、空值与多 canonical 值均拒绝。
`initialize` 必须通过 allowlisted Origin 和 `Accept: application/json`+`text/event-stream`，不接受客户端 session id。
initialize 成功后再校验 exact protocol revision、formal session、principal、profile 和 boot epoch；失败零 session/worker allocation。
`GET listen` 要求 Last-Event-ID absent 并创建新 SSE stream；`GET resume` 要求同 session 同 stream 的 exact-one opaque event id。
POST/DELETE 出现 Last-Event-ID 返回 method carrier mismatch；DELETE 不套用 POST 双 Accept，但仍强制 Origin/auth。
正式 session id 必须 crypto-random、全局唯一、visible ASCII，并绑定 principal、endpoint/profile、boot epoch、created/expiry。
无法证明 session/resume identity 的 HTTP endpoint 不得承载 strict unified long-follow、同 session cancellation 或 native mutation。

### 6.1 Accept canonicalization 与 zero-allocation preflight

strict HTTP 的 `Accept` 是有界的 media-range 列表：解析前只读取 method carrier 允许的单一 header，拒绝空值、畸形 token、重复 `q`、非有限或不在 `[0,1]` 的 q 值。canonical tuple 固定为小写 type/subtype、去 OWS 后按名称排序的参数和规范化 q（缺省 q=1）；完全相同的 canonical media-range 只保留一份并按相同语义合并，canonical tuple 相同但 q/参数冲突则返回 `duplicate_accept_media_range`，不得按出现顺序或“最后一个”猜测。

`*/*` 与 `type/*` 只能表示相应范围的 q 值，不能替 strict profile 所需的 exact media type。`initialize` 必须同时存在 `application/json;q>0` 与 `text/event-stream;q>0`；任一 exact range 为 q=0，或仅有 wildcard，即返回 `accept_media_type_not_acceptable`。`GET listen|resume` 必须有 `text/event-stream;q>0`，POST 必须有 `application/json;q>0`；DELETE 不套用 POST 的双 Accept 要求，但仍执行 Origin/auth/method carrier 校验。额外 range 可被 canonicalize 但不改变上述 exact requirement，列表长度与 header bytes 超限返回 typed carrier error。

method-specific preflight、Accept canonicalization、Origin、session-id 和 protocol revision 必须在创建 session、worker、body reader、SSE stream、registry/lease entry、domain request 或 goroutine 前完成；任一缺失/重复/wildcard-only/q=0/冲突 Accept 都是 zero-allocation（zero domain allocation）拒绝，响应不得留下 session、lease、cursor、buffer 或 cleanup debt。只有 preflight 全部通过后才允许分配 provisional session，并继续既有的 initialize provisional→formal promotion。

### 无 formal session 的初始化边界（规划字段）

在 formal session 尚未建立时，server 只允许以 `server_only_principal`、profile、endpoint、boot epoch 和临时 namespace 处理 `initialize`；临时 namespace 必须是 boot-bound、不可由客户端选择，且不能访问旧 session/cursor/lease。`InitializeCandidate` 固定 `candidate_id`、request digest、Origin/Accept/protocol tuple、server principal、profile/endpoint、boot epoch、provisional namespace、expiry 和 expected generation。

并发 initialize 以 candidate digest 和 expected generation 做单赢家 CAS：成功者一次性 promote 为 formal session，并生成 session/lease receipt；失败者必须返回同一 winner 或 typed `initialize_conflict`，不得创建第二 session。promote 前失败、超时或响应未知只释放临时 namespace；若已写入 durable candidate，则追加不可授权 tombstone 并按原 candidate 查询，不能把网络断流解释为 session 成功。`TestCTLConcurrentInitializeSingleWinner`、`TestCTLInitializeFailureReleasesProvisionalNamespace` 和 `TestCTLFormalSessionPromotionIsAtomic` 仅登记这些规划不变量，不能作为已实现证据。

## 7. 字段消费链

transport auth 先生成不可由工具参数覆盖的 CallerPrincipal 和 immutable connection context。
context 经 `ctl/register` 绑定 LeaseKey、boot epoch、peer kind、profile、capabilities 和 server heartbeat policy。
native-agent handler 以 LeaseKey 反查当前 registry，再校验 request fence、subject、generation、scope、expiry 和 capability lease。
command branch 把 intent 交给 OUT；resource/content owner分别验证 reservation/ref；identity owner消费 reservation/incarnation。
provider bridge 执行 claim 后回传 provider request/receipt；event/report 进入持久 event owner，projection 只读重建。
`ctl/context` 仅允许 `agent.runtime|thread.binding|workspace.run|config.snapshot`；不借 context 偷扩 turn/wakeup。
`ctl/event` 是审计/遥测 fire-and-forget，`ctl/log` 是带 level/seq 的结构化日志，`ctl/report` 只做状态迁移。
field mapper 必须拒绝 unknown/duplicate/mixed branch；output、receipt、cancel 和 error 的 schema 由同一 registry 生成。

## 8. 权限与恢复

register token、CallerPrincipal、capability grant、session identity 和 command fence 各自独立，不能由 `agent_id` 猜出。
bridge 只能使用自己拥有的 peer/source fence；controller-only 来源不能伪造 child principal 或 provider incarnation。
cancel 只接受同 connection/session、同 direction、同 request/operation fence 的 running 请求；initialized 不能取消。
ctl v1 保持当前“无通用 operation cancel”语义；只有协商 `ctl/native-agent/v1` 才启用 versioned cancel，禁止跨版本猜测。
shutdown 只由 router 下发，peer 停 heartbeat、补最终 completion report、断连，由 sweeper 负责异常退出清理。
恢复读取 durable lease/recovery head、receipt、cursor 和 profile digest；foreign、gap、rollback、unknown 都进入 reconcile_required。
恢复完成前禁止新 observation、command、capability decision；不能以 boot snapshot、UX cache 或旧 ACK 授权。
response unknown、late ACK、旧 generation event 只写原 operation audit，不修改 current binding、不重新派发。

## 9. 当前 standalone 证据与缺口

ST-05 的 Codex app-server `list_agents` 证明真实客户端工具连通，不证明 ctl/native-agent full contract。
ST-06 的 Claude 连接与 tool-call 证明独立 MCP 接入，但未覆盖 method-specific SSE replay、cancel 和完整 profile。
ST-07 的 Claude/Codex managed 双轮 binary E2E 证明部分 MCP→managed path，仍缺发布 provenance、远程和崩溃接管。
ST-08 的双向 direct-peer E2E 使用 trusted scope，不是第三方 HTTP bearer agent principal、receipt 和跨 incarnation 完整链。
现行 MCP 契约已确认 stdio/jrpc2 双通道、LeaseKey、heartbeat、fanout/sweeper 边界，但本规格所需修订尚未 Accepted。
仍缺 `ctl/native-agent/v1` 的真实 method/DTO/ack-replay/cancel/reconnect/size/shutdown stateful fixture 和外部签名 attestation。
ST-09/11/14/16～18 尚待实现或验证；不能以 initialize、list_agents 或用户批准开始执行关闭 CTL 缺口。

## 10. 具名验收与退出条件

CTL-P01：同一连接单 LeaseKey、重复 register/foreign generation/跨 principal request id 均按 named typed error 裁决。
CTL-P02：断线、heartbeat timeout、重连、新 generation、source cursor replay 和 stale writer crash 窗可重放且不越权。
CTL-P03：严格覆盖 initialize/listen/resume/POST/DELETE 的 carrier、Origin、Accept、session 和 zero-allocation 负测。
CTL-P04：native-agent command 的 accepted、provider ACK、completion、cancel、timeout、unknown 与 reconcile 可逐字段闭环。
CTL-P05：ctl v1 与 native-agent v1 混用、reverse MCP、旧 token、旧 profile、旧 LeaseKey 必须零副作用。
CTL-P06：Accept canonicalizer 对 OWS/case/parameter 顺序和完全重复 range 产生稳定 canonical digest；initialize/listen/resume/POST 只在所需 exact media-range 的 q>0 时建立 provisional session。
CTL-N01：空 session、跨 principal JSON-RPC id、duplicate carrier、q=0/missing Accept、foreign Last-Event-ID 均拒绝。
CTL-N02：把 transport ACK/heartbeat/config response/用户批准写成 provider success 或 protocol Accepted 必须被守卫拦截。
CTL-N03：control peer 通过 stdio 执行工具、bridge 自签 lease、ctl 绕过 OUT/identity/resource owner 必须 fail closed。
CTL-N04：`*/*` 或 `application/*` 不能满足 strict initialize/listen 的 exact Accept；缺 exact q>0 返回 `accept_media_type_not_acceptable` 且 domain allocation=0。
CTL-N05：同一 canonical media-range 的相同重复只允许幂等合并；不同 q/参数、重复 q 或 list conflict 返回 `duplicate_accept_media_range`，不得按顺序覆盖。
CTL-N06：q=0、非法 q、空/超限 Accept、缺失 method carrier 在 session/worker/body reader/registry/lease 分配前拒绝，且无 watcher、buffer、cursor 或 cleanup side effect。
CTL-N07：POST/DELETE 携带错误 Last-Event-ID 或 DELETE 被错误套用 POST 双 Accept 时返回 method carrier mismatch；仍保持 zero domain allocation。
退出条件：版本化 DTO/registry、权限/恢复状态机、正负 fixture、bounded limits、receipt/cursor 和 verifier evidence 全部可复查。
在正式 contract revision、DAG ancestor 和运行 evidence 获批前，本文件保持 Draft，不能作为 production enable 开关。
