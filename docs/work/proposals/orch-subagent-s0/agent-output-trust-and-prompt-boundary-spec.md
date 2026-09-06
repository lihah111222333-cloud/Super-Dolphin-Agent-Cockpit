Spec-ID: TRUST
Status: Draft
TRUST-Production: NO-GO
Depends-On: VERIFY_S, ROLLOUT_S
S0-Owner: TrustPolicyOwner with ControllerSignatureOwner for the outer envelope
S0-Scope: provenance, trust class, content role, sink admission, and prompt boundary
S0-Dependencies: VERIFY_S, ROLLOUT_S
S0-Inputs: provider, peer, and A2A output envelopes with caller, source fence, and content reference
S0-Outputs: versioned trust decision, quoted envelope, sink decision, and decision receipt
S0-Failure: unknown or mixed fields quarantine before custody; external uncertainty remains outcome_unknown
S0-Recovery: replay the original content reference and operation; quarantine gaps without guessing success
S0-Field-Chain: producer output -> TRUST envelope -> CONTENT ref -> EventStore event -> quoted provider/UI sink
S0-Named-Acceptance: RED-TRUST-01 UnknownTrustTuple; RED-TRUST-07 MCPMetadataCannotAuthorize; RED-TRUST-08 A2AMetadataCannotAuthorize; RED-TRUST-09 IconLinkNoAutoFetch; GREEN-TRUST-01 QuotedEnvelopeRoundTrip; GREEN-TRUST-06 ExplicitLinkConsentReceipt

# Agent 输出信任与 Prompt 边界子规格

## 0. 定位与约束

本文件是 Gate S0 的独立实施子规格，不是已批准设计，也不表示本轮已实现或启用任何统一控制面。
它细化总规格 §1.17、§1.25、§5.2、§8.1、§11.3 及 §19 Gate S0 的第1项 TRUST 节点。
`VERIFY_S` 与 `ROLLOUT_S` 未批准、DAG/registry digest 未锁定时，本规格只能产出 RED 证据，不能签发生产 trust lease。
TRUST 的恢复域外 decision/evidence head 由独立外部单实例服务持有私有 SQLite；本地 SQLite、WAL 或内存 snapshot 不能充当 authority、可用 receipt 或 anti-rollback anchor。
所有 provider、peer、A2A、transcript、artifact、memory recall 和 room history 均默认是外部不可信数据。
“来自 fully controlled agent”不改变数据分类，也不赋予 system/developer instruction、credential、grant、tool result 或 control intent 语义。

## 1. 边界与唯一 owner

TrustPolicyOwner 是 provenance/trust/content-role/sink registry 的唯一写 owner；具体包、表和持久化介质是 open decision，须在实现计划中冻结。
该 owner 发布版本化、可 canonicalize 的 registry snapshot，保存 `registry_id`、revision、digest、适用 profile 和 source cursor。
`internal/contract` 只保存协议无关的枚举/判别联合，不得写 trust policy 或第二份 sink allowlist。
EventStore、ContentOwner、bridge、UI projector、room projector 和 provider adapter 只能消费带 revision/digest 的 snapshot，不能本地加字段或放宽判别。
ControllerSignatureOwner（如与 TrustPolicyOwner 合并，必须在 ADR 中明确）只验证外层 typed control envelope，不对嵌套内容做 declassification。
任何 provider/peer/A2A 输出进入事件、内容、mailbox、transcript 或 UI 前都必须拥有 provenance、trust class、content role 和 source fence。
信任边界不拥有正文；正文由 CONTENT 的加密 content object/ref owner 管理，TRUST 只决定如何解释和进入哪些 sink。

## 2. 输入、输出与判别联合

输入 envelope 最少包含 `schema_version`、`provenance.origin_kind`、`source_subject`、`verified_transport`、`trust_class`、`content_role`、`source_fence`、`content_ref`、media type 和 size。
`trust_class` 只允许 `untrusted_external_data|authenticated_principal_content|controller_signed_control|internal_control`。
`content_role` 只允许 `quoted_data|user_content|control_instruction|tool_result`，不允许由字符串中的 role/name 触发升级。
provider/peer/A2A/transcript/room 内容固定为 `untrusted_external_data + quoted_data`；transport authenticated 只证明来源。
只有 auth middleware 验证 caller、canonical request hash、nonce、audience、scope、expiry 后产生的 command content 才能使用 `authenticated_principal_content`。
只有版本化、domain-separated canonical controller envelope 的 signature、release/build signer scope、host-pinned trust root、key scope、request digest、expiry 与 anti-rollback/rotation 状态全部通过时才能使用 `controller_signed_control + control_instruction`。
外层签名 envelope 的 `embedded_content[]` 仍逐项保留原 provenance/trust/role；不得以 controller 包裹洗白。
缺字段、unknown enum、mixed branch、duplicate field、非法 provenance/role 组合进入 `quarantine`，不提供兼容默认。
输出 decision 明确为 `quoted_only|authenticated_command|controller_control|denied|quarantined`，并携 registry revision、decision digest 和 consumed source cursor。

## 3. Prompt、InputItem 与 sink 合同

唯一 quoted renderer 的 wire 版本为 `v3.orchestration.quoted_data.v1`；它必须 length-delimit 结构化字段，禁止字符串拼接成 system/developer/tool role。
renderer 固定输出 source agent/incarnation/turn、event/content cursor、content hash、provenance、trust class、media type、截断状态和不可执行边界。
TurnSubmission、provider-neutral `InputItem`、各 CLI adapter input 和 room ordered-round input 必须保留同一组判别字段直到 provider user/data role。
`ignore previous instructions`、嵌套 tool-call JSON、`_meta`、credential request、签名样式和 provider role label 永远只是 quoted data。
sink registry 至少枚举 `model_system_prompt`、`model_user_data`、`tool_argument`、`tool_result`、`ui_html_markdown`、`ui_ansi_osc`、`ui_link_image_media`、`mcp_metadata`、`a2a_metadata`、`navigation`、`network` 和 `file`。
默认允许的 provider 输入只能是明确的 user/data sink；任何 system/developer/control sink 必须有 controller-signed control branch 和适用 grant。
UI HTML/Markdown/SVG/ANSI/OSC、link/image/media 默认转为 inert metadata；未经用户动作、同源受限代理、CSP/sanitizer、egress policy 和 quota 不得发起 network/file/navigation。
network sink 必须拒绝 file、loopback、link-local、private IP、redirect/DNS rebinding 和 ambient credential；不能把图片或 URI 的可显示性当作网络授权。
file sink 必须有显式 path policy、workspace containment、NOFOLLOW/TOCTOU 证明和 caller grant；无法证明时保持 inert 或返回 capability error。
每个 sink decision 都要绑定 `principal`、profile、registry digest、content ref/version、source fence、operation id、expiry 和实际执行 receipt；没有 receipt 不称为 effect completed。

### 3.1 MCP/A2A metadata、icon 与 link 的信任边界（硬字段）

MCP `tools/list` 的 `title|description|icons|inputSchema|outputSchema|annotations|execution.taskSupport|_meta`，以及 A2A Agent Card、capabilities、security schemes、endpoint URL、Part/artifact/status metadata，均是 `untrusted_external_data + quoted_data`。metadata 只能描述一个候选能力或资源，不能创建/扩大 CallerPrincipal、grant、tool registry、session、egress policy、CONTENT ACL 或 OUT command；`fully_controlled` provider 也不例外。

metadata ingress 必须保存以下不可省略字段：`metadata_kind`、`origin_kind`、`source_subject`、`source_fence`、`transport_auth_ref`、`registry_revision`、`metadata_digest`、`content_ref`、`media_type`、`schema_validation_receipt`、`trust_decision_id`。MCP 与 A2A 的 metadata schema、签发者和 endpoint 必须独立校验；缺字段、duplicate/folded key、未知版本或同 digest 不一致进入 quarantine，不能使用上一版本或本地默认。

`icons[]`、Markdown/HTML 中的 link/image、A2A artifact URL 和 MCP `_meta` URI 必须先转换为 inert `link_metadata`，并携带 `uri_digest`、canonicalized `scheme`、`host`、`port_or_absent`、`path_digest`、`redirect_policy_id`、`egress_policy_digest`、`user_consent_id_or_absent`、`operation_id`、`fetch_receipt_or_absent`。`javascript:|file:|data:`（除经明确 schema 允许且不执行的内嵌图标）、loopback/link-local/private IP、userinfo、跨域重定向和 DNS rebinding 一律拒绝或保持 inert；图标可显示不等于允许 fetch。

未发生用户显式动作时，metadata/icon/link sink 的 `network_calls=0`、`file_calls=0`、`navigation_calls=0`，也不得触发 tools/call、A2A task、session 建立或 grant 变更。用户点击只能进入独立 `navigation`/`network` sink admission：重新校验 caller、source fence、current registry/policy、CSP/sanitizer、host/path allowlist、quota 和 expiry；成功必须写 redacted fetch/navigation receipt，响应丢失为 `outcome_unknown` 并只查询原 `operation_id`。link label、icon alt text、A2A status text 和 `_meta` 字符串永远不能改变 sink kind 或 content role。

MCP metadata 不得覆盖 canonical manifest 的 tool name/schema/hash，也不得由反向 `tools/list` bootstrap lifecycle；A2A metadata 不得授权远端 `SendMessage|GetTask|CancelTask` 或把远端 URL 当作本地 capability。任何 metadata 驱动的 sink decision 必须逐字段绑定 `principal_ref`、`profile_id`、`registry_digest`、`content_ref/version`、`source_fence`、`operation_id`、`expiry` 和 receipt。

## 4. 状态、失败与未知结果

TrustPolicyOwner 的 snapshot 状态为 `draft|verified|revoked|expired`；只有 verified snapshot 可供测试 fixture 读取，生产还需 VERIFY_S/ROLLOUT_S ancestor。
单个 content decision 为 `candidate|allowed_quoted|allowed_control|denied|quarantined|outcome_unknown`，只允许 generated transition。
认证失败、canonicalizer 不可用、key/version unknown、registry 漂移或 sink profile 缺失都在 custody ack 前 fail-fast，不能静默降级为 quoted 或 inline。
未授权 sink 的调用计数必须为零；已进入外部 sink 但 receipt 丢失时进入 `outcome_unknown`，只查询原 operation，不猜成功/失败，也不盲目重试。
controller signature 通过不代表嵌套内容可信；嵌套项失败时只拒绝该 item 或整个 typed operation，不能升级外层。
renderer/provider parser 崩溃后，原 content ref 可重放，plaintext 不得写入 spool/log；未能恢复的 range 追加 typed gap/quarantine 并撤销 output/history capability。
事件写入与 sink effect 不是跨存储事务；SQLite commit、UI 展示或 provider 下一次输出都不能替代实际 sink receipt。
任何 stale registry、principal、content ref、turn/incarnation 或 security-time tuple 都返回 typed `reconciliation_required`/`capability_not_supported`，不改投另一路径。

## 5. 去重、权限与字段消费链

trust decision 的稳定键为 `(security_domain_id, registry_id, registry_revision, sink_kind, principal_subject, content_ref, content_version, operation_id)`；同键同 digest 幂等，异 digest 为冲突。
controller envelope 的 idempotency digest 必须覆盖 canonical outer fields、embedded item descriptors、audience、expiry 和 source fence，但不能把 plaintext hash 当作安全身份。
caller principal 只能由 transport/auth middleware 产生；普通 `_agentId`、`requester_id`、thread id、metadata 或 content 文本不能建立权限。
diagnostic/full-fence 查看与控制 sink 需要独立 `agent.diagnostic.read` 或 `agent.low_level.mutate` grant；切换 view/参数不提升权限。
消费链固定为：provider/peer/A2A producer → bridge ingress → TRUST envelope → CONTENT ticket/ref → EventStore event → quoted renderer → TurnSubmission/InputItem/provider user-data sink。
UI projector 消费同一 envelope 和 content ACL；navigation/network/file 只消费 sink admission decision，不得直读 provider 原文。
`trust_class`、`content_role`、`provenance`、`source_fence`、`content_ref`、registry revision/digest、sink decision 和 receipt 必须逐层有 producer/consumer，缺消费端即字段守卫失败。
TrustPolicyOwner 的 projection 只能带 source cursor；UI、room、report、tool result 不能反向成为 trust SSOT。

## 6. 当前差距（不关闭）

本轮未读取实现源码；以下事实仅依据总规格与当前 ST 表，具体 writer、consumer 和 runtime sink inventory 均待复核。
总规格与当前 ST 表只证明目标约束，不证明本仓库已有完整 trust/sink registry、签名 artifact 或 quoted renderer production owner；具体源码 writer inventory 待复核。
ST-04 仍只部分覆盖坏配置/manifest/schema 副作用；ST-08 的 direct-peer E2E 使用进程内 trusted scope，不是第三方 HTTP bearer caller attribution 路径。
ST-09、ST-10、ST-14、ST-16、ST-17、ST-18 仍待实现/验证，因此不能据此声称跨任务内容授权、sink 隔离、Room/UI 或 stdio 代理安全完成。
当前独立服务/CLI 的本地 wrapper 通过证据仍是 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；不能当作签名 trust artifact、外部 dispatch 或发布门。
规划性 JSON/依赖快照即使已经建立，也不等于 `VERIFY_S`、`ROLLOUT_S` 的正式 machine DAG/evidence 模型；其 artifact/negative head、签名算法与实际 owner 仍是 open decision，未批准前严禁把 Draft 字段写入生产 policy。

## 7. 具名 RED/GREEN 验收

RED-TRUST-01 `UnknownTrustTuple`：unknown/mixed provenance、role 或 trust class 在 event/content custody 前被 quarantine，副作用与 custody ack 均为零。
RED-TRUST-02 `NestedRoleInjection`：quoted 文本含 system/developer/tool 标签、`ignore previous` 或 tool JSON 时，renderer round-trip 后仍是 user/data，不触发 control sink。
RED-TRUST-03 `UnsignedControllerEnvelope`：缺 signature、错 audience、过期、digest/nonce 不符时拒绝，不能进入 TurnSubmission control branch。
RED-TRUST-04 `ActiveSinkWithoutConsent`：HTML/image/link/ANSI/OSC 或 private URL 无用户动作和受限 egress 时保持 inert，网络/文件调用计数为零。
RED-TRUST-05 `PlaintextBeforeContentTicket`：没有 CONTENT prepared ticket 时任何 relay/raw/staging/temp/event 写正文均失败并无 durable byte。
RED-TRUST-06 `UnknownSinkOutcome`：sink 返回或进程崩溃导致 receipt 不明时只保留 stable operation/submission fence，查询不补发。
RED-TRUST-07 `MCPMetadataCannotAuthorize`：伪造或冲突的 `tools/list` title/schema/icons/annotations/`_meta` 不得改写 canonical manifest、grant、tool handler 或 caller；副作用与 registry mutation 均为零。
RED-TRUST-08 `A2AMetadataCannotAuthorize`：Agent Card、security scheme、endpoint、Part/artifact/status metadata 不能建立远端 caller、Send/Get/Cancel 权限或本地 egress；无独立 binding/auth receipt 时调用数为零。
RED-TRUST-09 `IconLinkNoAutoFetch`：icon/link/image URI（含 `file:`, `javascript:`, private/loopback、userinfo、redirect/DNS rebinding）在无用户动作与 sink receipt 时保持 inert，network/file/navigation/tools/A2A call count 均为零。
GREEN-TRUST-01 `QuotedEnvelopeRoundTrip`：合法 provider output 经 renderer、CLI InputItem、UI projection 保持字段、cursor、provenance 和不执行边界一致。
GREEN-TRUST-02 `SignedOuterKeepsNestedTaint`：合法 controller envelope 只执行 allowlisted outer control，嵌套 provider content 仍为 untrusted quoted data。
GREEN-TRUST-03 `SinkAdmissionZeroSideEffect`：每个 RED 负例都证明 call count、network/file/navigation、event control mutation 为零。
GREEN-TRUST-04 `TrustSnapshotReplayAndRotation`：相同 registry/digest 重放幂等，revision/key rotation 使旧 decision 失效并要求 reconcile。
GREEN-TRUST-05 `MetadataTaintRoundTrip`：合法 MCP/A2A metadata 经 ingress、quoted renderer、UI projection 往返后逐字段保留 origin/trust/role/source fence/digest，且 canonical registry 与 grant 不变。
GREEN-TRUST-06 `ExplicitLinkConsentReceipt`：用户显式点击后，navigation/network sink 仅在当前 policy/CSP/allowlist 通过时执行，并产生绑定 operation、URI digest、principal、source fence 和 redacted receipt；响应丢失回到 `outcome_unknown`，不自动重试。

## 8. 退出条件与未裁决项

退出必须同时具备：TrustPolicyOwner 唯一 writer inventory、版本化 registry/artifact digest、字段消费者差集为零、所有具名 RED 通过、GREEN 通过并有可复查 fixture attestation。MCP/A2A metadata、icon/link 的 sink matrix 必须逐字段有 consumer；任一未授权自动请求、metadata declassification 或 receipt 缺口保持 `NO-GO`。
还必须证明 content ticket/ref 与 trust decision 的 source/incarnation/expiry join、UI/network/file sink 的零自动副作用及 crash/unknown 查询路径。
只有 `VERIFY_S`、`ROLLOUT_S` 获批且 machine DAG ancestor closure、release artifact 和 rollback evidence 通过，才可把 snapshot 从 Draft 流入任何 enable/permit。
Open decision A：controller signature envelope 的算法、trust root、rotation/revocation/anti-rollback owner与外部恢复域位置。
Open decision B：sink registry 的完整枚举、每种 sink 的 canonicalizer/egress proxy、用户同意表达和跨平台 NOFOLLOW/TOCTOU 证明。
Open decision C：quoted-data renderer 的 generated schema、CLI provider 兼容字段和 content truncation/retention policy。
Open decision D：`outcome_unknown` 的 sink reconciliation provider、receipt retention horizon 与 VERIFY_S/ROLLOUT_S 的 evidence binding。
在上述 open decision 关闭、文档获批、真实 fixture 与发布证据齐全前，本规格保持 `Status: Draft`，不声称已启用外部 dispatch。
