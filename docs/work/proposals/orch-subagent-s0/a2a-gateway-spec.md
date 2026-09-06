Spec-ID: A2A
Status: Draft
Depends-On: ID, CONTENT, RESOURCE, OUT, TRUST, ROOM
S0-Owner: A2A gateway owner for remote wire/auth/egress; local owners retain local facts and authorization
S0-Scope: strict A2A wire profile, Agent Card, remote task mapping, egress, cancellation, and reconnect
S0-Dependencies: ID, CONTENT, RESOURCE, OUT, TRUST, ROOM
S0-Inputs: remote Agent Card, version carrier, CallerPrincipal/grant, content ref, target fence, and remote epoch
S0-Outputs: typed Task/Message/stream result, remote receipt, snapshot/cursor, and capability/error detail
S0-Failure: version/carrier/part/auth/egress mismatch rejects or remains outcome_unknown without a second remote task
S0-Recovery: query the original remote operation/epoch and rehydrate snapshot; no lossless cursor is claimed without proof
S0-Field-Chain: Card/version -> gateway principal -> local command/content/resource refs -> remote receipt -> untrusted projection
S0-Named-Acceptance: TestA2AIdempotentSendAfterLostResponse; TestA2AUnknownRemoteOutcomeIsNotRetried

# A2A Gateway 子规格

负责人：A2A gateway/remote integration owner；创建日期：2026-09-01。

本文承接总规格 §10.5、§13、§19 Phase 5，定义跨 orch owner/host/security domain 的远程边界。
Draft 期间不得启用 `a2a_remote_agent` launch、远程 Room extension 或把本地 peer 路径改为 A2A。
同一 orch owner/security domain 内的 Claude/Codex direct peer 与 Room 不强制经过 A2A。

## 目标与非目标

目标是发布 V3 Agent Card，将 binding-neutral A2A Task/Message/stream 映射为本地 command/event/receipt。
目标还包括 strict binding carrier、版本拒绝、远端 authn/authz、egress、限流、断线 snapshot rehydrate 和 unknown recovery。
非目标是 provider CLI-native hook transport、V3 本地 message receipt/turn fence 的替代品，或本地 workspace 默认权限。
非目标是把 A2A `CancelTask` 说成强 interrupt，把 remote erasure 说成已擦除，或把 best-effort 流恢复说成 lossless cursor。
非目标是为同域 Claude/Codex 增加不必要的 gateway hop、第二数据库或第二 transcript 事实源。

## 依赖与边界

依赖方向固定为 `A2A <- ID, CONTENT, RESOURCE, OUT, TRUST, ROOM`。
ID 生产 `a2a_remote_agent` 的 logical agent、execution incarnation、remote identity/task/session/epoch fence。
CONTENT 生产加密 content ref、remote artifact/cache state、trust/taint 和 erase receipt 引用。
RESOURCE 生产 gateway/remote scope 的 reservation、rate、concurrency、size、wall-time、token/cost cap。
OUT 生产 launch/send/wait/cancel command、attempt、idempotency、provider receipt、outbox lease 和 reconcile record。
TRUST 生产 gateway CallerPrincipal、auth generation、Agent Card provenance、egress policy 与 untrusted remote output class。
ROOM 生产本地 membership/snapshot/receipt；A2A 只消费获批 versioned room extension，不把远端成员加入本地默认 Room。
A2A gateway 是远程协议 owner；本地 identity/event/content/resource/outbox owner 仍裁决本地事实与授权。

## Wire profile 与字段生产消费链

wire version 固定为规范值 `1.0`；基于 A2A 1.0.0 的其他版本或 absent 不自动降级。
JSON-RPC 与 HTTP+JSON strict profile 从 generated binding matrix 读取 exactly-one、大小写不敏感的 `A2A-Version` header。
gRPC strict profile 从 version-pinned metadata 读取 version；request/query parameter 只属于显式兼容 profile。
compat profile 必须有独立名称、schema、exact-one/dual-match 规则，不能冒充 strict conformance。
generated extractor 在 operation decode 前拒绝 duplicate header、逗号 folded/list、重复 query、empty+nonempty 或同 carrier dual conflict。
absent、empty、`0.3`、`1.0.0`、unsupported/wrong value 与 silent downgrade 统一为 VersionNotSupportedError。
其 wire 映射固定为 JSON-RPC `-32009`、gRPC `FAILED_PRECONDITION`、HTTP 400 和同一 typed detail。

Agent Card 生产 gateway identity、interface/version、skills、security requirements、extension URI 和 remote agent provenance。
ID consumer 只把 Card 及远端返回的 stable identity/task/session/epoch 绑定为 execution fence，不能由 Card 猜 control level。
`execution_fence` 固定携 gateway_instance_id、gateway_auth_generation、remote_agent_identity_or_stable_hash、
remote_task_or_session_id、remote_epoch、logical agent/incarnation 和可选 turn；字段不适用时 absent。
OUT consumer 用该 fence 固定 command intent、attempt、receipt 与 late status；远端 task id 变化不得重绑旧 incarnation。
CONTENT consumer 只发送获批 encrypted ref/quoted data；remote output/artifact 回来后仍标为 `untrusted_external_data`。
RESOURCE consumer 为 gateway/remote scope 单独计量；本地 workspace quota 不能因远端 Card 自称无限而放宽。
TRUST consumer 绑定 transport/auth/egress generation，并将 Agent Card、Part、artifact、status text 保持不可信 taint。
ROOM consumer 仅在双方协商且授权的 extension 下映射 room metadata；缺 extension 返回 capability error。

## 操作、DTO 与输出

gateway 对外发布 `SendMessage`、`SendStreamingMessage`、`GetTask`、`SubscribeToTask`、`CancelTask` 与 Agent Card。
Send 映射本地 launch/send command；stream 映射 provider output/event；GetTask 映射 current task snapshot；Subscribe 映射 snapshot 后续流。
本地 receipt 状态仍只有 `accepted|provider_acknowledged|observed_in_context|rejected|outcome_unknown`。
远端接受 CancelTask 最多生成 `provider_acknowledged`，不能直接写 stopped；必须等待同 task/session、remote epoch、attempt fence 的权威 terminal。
远端 terminal status 迟到时只能补写原 incarnation；replacement incarnation 不得被旧 status 影响。
Task、TaskStatus、TaskState、Message、Part、StreamResponse、error、security requirement 全部从 version-pinned proto/OpenAPI generated。
unknown Task state 不能静默映射成 working/input-required；schema 不完整或版本漂移时拒绝并保留 typed detail。

Part 判别联合在 strict 1.0 profile 固定 one-hot：`text|raw|url|data`；duplicate/empty/dual fields 先拒绝。
只有 URL/URI 分支可进入 egress evaluator；text/raw/data 不能通过字段伪装成 URL，也不能被当成可执行控制 payload。
URL/URI 默认只是 untrusted metadata；实际 fetch 还必须有显式 policy、无 ambient credential 的 egress client 和 redacted fetch receipt。
V3 特有 `a2a.v3.output.live_cursor`、interrupt、room extension 必须带版本 URI 并 capability-negotiated。
对端未声明 mid-turn、live cursor、interrupt 或 room extension 时返回 `capability_not_supported`，不得改走隐藏 fallback。
核心 SubscribeToTask 重连只承诺 current Task snapshot 后的后续事件，不承诺断线窗口内 lossless replay。

## 授权、egress 与安全

gateway 使用独立 remote authn/authz，构造 `issuer_binding=a2a_gateway` 的 CallerPrincipal 和 auth generation。
跨 workspace、跨 owner、跨 host、跨 security domain 必须有显式 grant、audience、scope、expiry 和 revocation；Card 不能代替 grant。
远端默认没有本地文件、命令、provider secret 或完整 transcript 权限；本地 peer/Room membership 不自动外推到远端。
每次 send/get/subscribe/cancel 都重验 principal、target fence、remote epoch、grant、resource policy、content ACL 和 gateway lease。
不同 gateway/auth generation、remote identity 或 task epoch 不能复用旧 handle、attempt 或 operation；stale 进入 reconcile。

strict egress 默认只接受 peer-origin HTTPS；DNS resolve 与 connect 做双重 IP 校验并 pin DNS 结果。
禁止 localhost、loopback、link-local、private、cloud metadata、file/unix 等 scheme/地址；每次 redirect 都重验 origin、scheme、IP 和 policy。
跨 origin 时剥离 Authorization、cookie、mTLS identity 等 ambient credential；压缩内容在解压后执行 size/time cap。
DNS rebinding、public-to-private redirect、非 HTTP scheme、credential cross-origin、压缩炸弹必须在危险请求/解压前拒绝。
URL Part、Agent Card icon/link、artifact URI 和 remote status 中的 Markdown/HTML/ANSI/OSC 都不自动 fetch/render active content。
remote erasure 只报告本地 cache/reference 删除结果和远端 receipt/capability；没有权威 receipt 就标记 unverified。

## 崩溃、断线与未知结果

每个 send/launch/cancel 先写 durable idempotent command/outbox；相同 principal、operation、target epoch、intent 和 key 只返回原 operation。
本地 response 丢失或 gateway 重启时，按原 operation/attempt 查询 remote task/session/epoch；不得因 timeout 自动重发或创建第二个 task。
若远端 status 查询不可用，保持 `outcome_unknown`，保留 last cursor、gateway generation 和 recovery fence，交由 reconcile owner 查询。
远端 status 已 terminal 后迟到不改变本地已 finalized state；若本地尚未 finalize，只能在原 fence 下补齐。
Subscribe 重连先取得 current snapshot；只有对端声明并验证 `a2a.v3.output.live_cursor` 扩展，才可恢复 extension cursor。
没有 lossless cursor 时，UI/consumer 必须显示 snapshot-only 和潜在 gap，不能伪造完整历史。
gateway auth generation、remote epoch、Agent Card interface 或 binding schema 改变时，旧 lease 失效并阻断新 side effect。
remote CancelTask timeout 不得写 stopped；无强制 stop extension 时返回 outcome_unknown/capability error，不杀本地无关进程。

## 错误与可观测性

使用总规格 catalog 的 `capability_not_supported`、`provider_bridge_unavailable`、`delivery_outcome_unknown`、`command_outcome_unknown`。
gateway 专属错误包括 `mcp_method_carrier_mismatch` 对应的 A2A carrier variant、`gateway` auth/version/egress/remote task subject。
版本错误必须是 VersionNotSupportedError；Part/DTO 重复、缺失、unknown state、security requirement mismatch 不能泛化成 internal。
错误 subject 必须只包含 gateway、remote execution、task/attempt、cursor 或 content ref 的适用分支，不泄漏无权远端存在性。
所有 outbound request、redirect、fetch、remote receipt 与 erasure evidence 只记录 redacted digest/ref，不记录 token、cookie、provider secret 或正文明文。

## 当前缺口与测试

当前计划 Phase 5 A2A 尚未实现，ST-10 的跨 run/跨 owner 授权、ST-11 的 unknown/reconcile、ST-12 的硬崩溃恢复和 ST-16 的资源/断线门仍未完成。
ST-14 Room 也待实现；已有同域 Claude/Codex direct-peer E2E 使用 trusted scope，不能当作 A2A bearer gateway 证据。
当前文档尚无 generated binding matrix、Agent Card、remote fixture、egress negative、versioned extension 或发布证据。

正向测试名：

- `TestA2AGatewayStrictJSONRPCVersionCarrier`
- `TestA2AGatewayGRPCMetadataVersion`
- `TestA2ACardAndTaskMapping`
- `TestA2ASnapshotRehydrateAfterReconnect`
- `TestA2AIdempotentSendAfterLostResponse`
- `TestA2ATaskCancelRequiresTerminalRemoteStatus`
- `TestA2ARoomExtensionRequiresNegotiation`
- `TestA2ACrossOwnerPrincipalAndEpochFence`
- `TestSameDomainClaudeCodexUsesDirectPeerWithoutA2A`

负向测试名：

- `TestA2ARejectsMissingDuplicateFoldedVersion`
- `TestA2ARejects03And100Version`
- `TestA2ARejectsQueryVersionOutsideCompatProfile`
- `TestA2ARejectsPartDuplicateOrUnknownState`
- `TestA2ACancelAckCannotStop`
- `TestA2AUnknownRemoteOutcomeIsNotRetried`
- `TestA2AURLHasNoAmbientCredential`
- `TestA2ADNSRebindingAndPrivateRedirectRejected`
- `TestA2AUnsupportedMidTurnReturnsTypedCapabilityError`
- `TestA2ARemoteErasureWithoutReceiptIsUnverified`

## Gate S0 退出条件与未决项

退出条件是 generated binding matrix、strict/compat profile、Agent Card、全部 DTO/Part/error/security union 和 field guard 获批并具备 digest。
必须通过真实 JSON-RPC/HTTP+JSON/gRPC carrier 正负测试、remote auth/binding fixture、idempotency/reconcile、snapshot reconnect 和 egress 安全测试。
必须证明 remote output untrusted、resource 按 gateway/remote scope 计量、CancelTask 不冒充 interrupt、erase 不冒充 erased、旧 epoch 不影响 replacement。
必须证明同域 direct peer/Room 可独立验收，A2A 未实现不阻断同域协作；反之 A2A 不能绕过本地 ID/CONTENT/RESOURCE/OUT/TRUST/ROOM 授权。
在退出前 `a2a_remote_agent`、远程 Room extension、A2A live cursor/interrupt 都保持 disabled，缺 capability 只返回 typed error。

以下事项仍是 Draft，不能启用：选用的 A2A 1.0.0 proto/OpenAPI revision/digest、Agent Card 字段最小集和 extension URI 注册表。
还未冻结各 binding 的 metadata canonicalization、remote rate-limit headers、snapshot gap 文案、remote receipt retention 和跨 gateway HA redemption。
request/query parameter compat profile、lossless cursor、强制 remote stop、remote erasure guarantee 都不在本 Draft 中默认为开启。
