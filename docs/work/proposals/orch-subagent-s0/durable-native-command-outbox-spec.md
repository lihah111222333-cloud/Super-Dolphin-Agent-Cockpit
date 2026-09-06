Spec-ID: OUT
Status: Draft
OUT-Production: NO-GO
Depends-On: ID, CONTENT, RESOURCE
S0-Owner: CommandOwner with SecurityDomainSequencer and RecoveryCustodyOwner for command facts and receipts
S0-Scope: durable command intent, outbox attempts, effect permits, provider receipts, terminal state, and reconcile
S0-Dependencies: ID, CONTENT, RESOURCE
S0-Inputs: caller-stable mutation request, target fence, content ref, resource reservation, and idempotency key
S0-Outputs: command/attempt records, submission fence, effect receipt, provider receipt, and terminal outcome
S0-Failure: invalid joins reject before dispatch; uncertain external effects remain outcome_unknown and are not retried
S0-Recovery: query the original operation, nonce, and attempt; cleanup or reconcile without a second effect
S0-Field-Chain: caller request -> command/message/mutation intent -> generated profile -> attempt -> permit/nonce -> provider receipt -> terminal event
S0-Named-Acceptance: RED-OUT-01 IntentBeforeDispatch; RED-OUT-10 EffectProfileUnknownBeforeAuthorization; RED-OUT-14 MessageDeliveryUnionMixedFields; RED-OUT-19 OrdinaryMutationPendingBlocksDispatch; GREEN-OUT-01 AcceptSubmissionAndCommandWait; GREEN-OUT-06 GeneratedEffectProfileRoundTrip; GREEN-OUT-09 MessageAssociationRoundTrip; GREEN-OUT-12 OrdinaryMutationPostOrNonCommit

# Durable Native Command Outbox 子规格

## 0. 定位、依赖与明确非声明

本文件是 Gate S0 的独立实施子规格，不是已批准实现，也不声称本轮已启用任何外部 dispatch。
它细化总规格 §1.1/§1.2/§1.4、§5.2、§7.1、§8.2、§9、§10.5、§11.1、§16、§17 和 §19 Gate S0 的第5项 OUT 节。
ID 提供 stable agent/incarnation/source fence；CONTENT 提供 encrypted body/ref；RESOURCE 提供 durable policy/reservation/usage；三者缺任一都不能 dispatch。
本规格的目标是 durable intent、单一 outbox/attempt owner、receipt-backed phase、可查询 unknown 和不重复 external effect。
本地 SQLite transaction 只覆盖同一 owner；provider/bridge、recovery custody、keystore 和外部 runtime 不是跨进程原子事务。
Outbox 提供 durable intent、受控重试与审计，不承诺跨 provider exactly-once；不能把 message accepted、command accepted 或本地 commit解释为 provider已执行。

## 1. Owner 与 command boundary

CommandOwner 是 `orchestration_commands`、attempt、idempotency、generated terminal registry 和 command wait projection 的唯一写 owner。
SecurityDomainSequencer 独占恢复域外 `side_effect_authorization_head/{security_domain_id}`；它是 authorization generation/state root 的唯一 current owner。
RecoveryCustodyOwner 独占 `recovery_custody/{security_domain_id}` phase availability/abort receipt；它不拥有业务 command 查询状态。
RoomOperationOwner 只拥有 room 顶层 operation；wakeup、scheduler、cron、room worker 和 provider bridge 都是 command producer/consumer，不得建立第二 dispatch owner。
dispatcher 只能消费 CommandOwner 已 accepted 且 recovery phase available 的 command；在任何 external call 前必须取得本 attempt 的 dispatch receipt、resource claim、call-permit/nonce gate。
provider terminal、event、message receipt 和 resource/content release 只能通过生成式 owner API 进入，不能由 worker 直接改表或 UI 直接投影为完成。

## 2. Command schema 与 caller-stable idempotency

每个 public side-effect mutation（launch/fork/send/interrupt/stop/recover、room target broadcast/round）必须有 caller-stable `idempotency_key`，缺失是 schema error。
command 固定 `command_id/version`、typed kind/payload discriminator、target logical agent、完整 target `execution_fence` 或 launch reservation/source fence。
同时保存 expected turn fence、caller principal/auth context/expiry、authorization decision/grant/revocation epoch、capability evidence cursor、policy/reservation/usage refs。
prompt/message/tool/broadcast body 只保存 CONTENT `content_ref/version`、keyed intent digest、digest domain/algorithm/key id/version、size/media/retention，不复制明文。
idempotency namespace 按 kind 固定：已有 execution 使用 principal+operation+target agent/incarnation+key；recover 加 recover source；launch 加 requested kind/creation/scope/source fence。
room create/join/leave/close/broadcast/round 使用各自 room scope、membership revision 或 dispatch generation namespace，不能套通用 tuple。
canonical intent digest 必须从真实 request、typed discriminator、stable fence、delivery/busy policy 和 content ref 计算；不能把 server-resolved current state 变化悄悄纳入 client digest。
同 principal/namespace/key 同 digest 永远返回同 operation/command/reservation fence；同 key 异 digest、source kind/id/fence 或 payload 改变为 `idempotency_conflict`。
idempotency tombstone horizon 至少覆盖 provider guarantee、retry/reconcile、audit、backup/rollback 和所有 non-terminal/outcome-unknown external execution；过 horizon 返回 `idempotency_expired`，不能创建新副作用。

## 3. Accept resolution 与 submission wait

local accept transaction 先验证 principal/grant、ID target/source fence、TRUST role、CONTENT ref/ticket、RESOURCE snapshot/reservation，以及 writer/deployment/security-time fence。
command 初始保存 `accept_resolution=pending`、stable `submission_fence` 和 recovery batch digest；此时不写 public `command.accepted`，不 claim provider，不调用 dispatcher。
submission fence 固定 `{command_id,command_version,subject_selector,idempotency_namespace,key_digest,pending_cursor}`，是 non-authorizing 查询句柄，不是 command wait fence。
RecoveryCustodyOwner 以 batch digest 推进恢复域外 head并取得 availability receipt，或以同 batch 的 abort tombstone 证明迟到 append 永不发生；receipt/abort 只能一方获胜。
receipt winner 后 CommandOwner 必须在同 transaction CAS `pending -> accepted` 并写 `command.accepted`/`message.accepted`，返回完整 `command_fence`（含 `recovery_receipt_digest`）。
accepted local finalize 重新校验 current intent/resource/content/room；若已 stale，必须同 transaction 写 terminal `command.rejected`、适用 reservation/resource/ref cleanup，provider call=0，不把 receipt 改成 aborted。
远端 CAS 结果不明、等待超时或本地崩溃时只返回 `status=indeterminate,error=command_accept_pending` 与 stable submission fence；`command_submission` wait 只能查询 accepted/aborted。
只有 recovery owner abort CAS/tombstone 成功后，CommandOwner 才能 CAS `pending -> aborted`，写 `command.accept_aborted` 和 authoritative rejected；不能用本机 timeout 或 worker failure 猜 abort。
`command_submission` accepted 返回完整 command fence，aborted 返回 typed reject，pending 保持 indeterminate；它绝不能等待 execution terminal 或交给 dispatcher。

## 4. Attempt、permit 与 intent-before-dispatch

dispatcher claim 只接受 `accept_resolution=accepted, phase=accepted, recovery_phase=available` 且所有 ID/CONTENT/RESOURCE/authorization/current policy joins exact 的 command。
claim transaction 创建单调 `attempt_generation`、lease owner/expiry，并先写 dispatch custody pending；新 attempt 不得覆盖旧 attempt 或重用旧 receipt。
该 attempt 必须分别取得 `dispatch_started` 与 `resource_claimed` 恢复域外 receipt；未有 receipt 的 worker lease 到期才可能安全 reclaim，其他情况先 outcome-unknown。
local phase finalize 固化 stable `call_permit_id` candidate、command/attempt/dispatch receipt、完整 current generation vector、bridge/process/start identity、effect-request profile/digest 和 generated transition exact-set。
fresh launch 的 transition set 必须含 same-family runtime-effect `launch_pending -> launch_current`；已有 execution/descendant 必须引用当前 runtime-effect lease，不能 fresh genesis。
SecurityDomainSequencer 以 expected current head create-if-absent candidate；同 canonical digest 幂等，异 digest 冲突，response unknown 只 query 原 id。
bridge 只有在 candidate current、transition pending=0、cleanup set尚未移交且所有 current receipts 可读时才能 redeem permit；redeem 只创建 `effect_authorization_pending`、transition tickets 与 `abort_cleanup_set`，不签 token、不调用 provider。
每个 transition/reservation/resource/runtime lease/content/ref 依赖必须有 generated cleanup item、expected pre/post generation/root、dispatch receipt 和 cleanup ticket；worker 不得临时枚举清理集合。
全部 authorized transition receipt 清零后才可用独立 issuance id issue 短期 effect fence token；issue 与新的 mutation pending 同一 owner 单赢家。
actual effect boundary 必须用绑定 effect-request profile 从真实 method/params/spawn request/content ref/DB commit set 重算 digest，在线比较当前 lease/authorization/resource/content/security-time，并原子消费 nonce+启动 effect。
runtime-effect lease 使用 stable family key，base state 只允许 `launch_pending -> launch_current -> conversion_pending -> execution_current -> revoked`；same-state renewal 必须逐字保持 subject、process-tree、authority root 和 current tuple。
launch conversion 的 prepare/commit、authority/descendant successor、stop revoke 和 process replacement 都是不同 generated operation；authority 变化必须有 actual post-state receipt，不能借 renewal 扩权或换 process identity。
same-family replacement 先写只含 base generation/计划的 reservation，再以真实 P2 start 与独立 add-P2 phase receipt、remove-P1 phase receipt、same-root renewal ancestry 和最终独立 digest 串联 stable replacement head。
add/remove phase 各自拥有 recovery owner、authorization/issuance/nonce/cleanup/physical receipt 与 immutable terminal record；任一 nonterminal phase 由 stop/recover 查询并收口，不能另建 family 或只依赖 public P2 handle。
provider child fork/exec/delegation 先保持 suspended/escrowed，经 authority successor、measurement、binding preparation/bind/finalize 后才 resume；无法持续 fence 时只 quarantine/unsupported。

### 4.1 Generated effect-request profile registry（真实 schema 与边界）

`effect_request_profile` 是构建/发布时生成、内容寻址、运行时只读的 registry；不得由请求、provider bridge 或 worker 临时发明 profile。唯一查找键是精确的 `(effect_kind, boundary_profile_id)`，没有默认 profile、模糊匹配或跨 provider 继承。`ProfileRegistryOwner` 是归属 CommandOwner 的 build-time generator/release role，只生成 immutable entry 与 registry head，不是第二个 runtime writer，也不拥有 command、authorization、RecoveryCustody 或 provider receipt。每个 entry 的真实记录固定为：

```text
effect_request_profile = {
  effect_kind, boundary_profile_id, boundary_id, boundary_audience,
  profile_generation, profile_schema_version, request_schema_digest,
  canonicalizer_id, canonicalizer_version, canonicalizer_digest,
  hash_algorithm, hash_domain, key_profile_or_absent,
  json_duplicate_policy, env_duplicate_policy, header_duplicate_policy,
  list_order_policy, set_sort_policy,
  unicode_policy, path_policy, url_policy, fd_policy, db_set_policy,
  allowed_fields_digest, profile_payload_digest, availability_receipt
}
```

`profile_payload_digest` 是 `hash_algorithm` 对 domain-separated `hash_domain || JCS(entry_without_profile_payload_digest,availability_receipt)` 的摘要；`canonicalizer_digest` 是生成该 profile 的 canonicalizer 实现/规则的摘要，不能只记录版本字符串。`key_profile_or_absent` 明确表示是否需要受批准的 keyring profile；为 `absent` 时不得偷偷选用默认 key。`profile_generation`、`profile_schema_version`、`request_schema_digest`、`allowed_fields_digest` 和 `availability_receipt` 必须逐字段可查询，缺任一字段即 registry 不可用。registry head 另外保存 `registry_generation`、`registry_root`、`generator_id/version`、`profile_set_count` 和 head `availability_receipt`；entry 不得跨 head 重用或覆盖。

本节后文的 `effect_request_profile_digest` 就是该 entry 的 `profile_payload_digest`（不是 canonical request digest）；任何 owner 不得以版本号、名称或局部字段替代它。

Profile entry 的策略值不是说明文字，而是 generated enum/判别联合。最小规范化规则如下；未列出的字段、策略或输入形态均为 unknown 并在授权前拒绝。

| 输入域 | 固定规则 |
| --- | --- |
| effect/boundary/schema token | 仅接受 registry 枚举中的 ASCII、大小写精确值；`effect_kind`、`boundary_profile_id`、schema/version 不得别名或自动升级。 |
| JSON | 严格 UTF-8、RFC 8785 JCS 数字与字符串规则；对象重复 key 在解析时拒绝（禁止 last-wins/合并），未知 key、`null`/缺省分支混用、NaN/Infinity 和非法数字拒绝。 |
| env | 按 profile 指定的名称大小写规则比较；同一 canonical name 重复一律拒绝，禁止 last-wins、隐式合并或按进程环境覆盖；名称和值分别执行 Unicode/字节策略。 |
| header | 名称按 profile 指定的 ASCII 大小写折叠后比较；重复仅在 profile 明确声明 `multivalue_ordered` 时允许，并保留声明顺序，否则 exact/case-insensitive duplicate 拒绝；OWS、值编码和排序不得由调用方自选。 |
| list/set | list 按 `list_order_policy` 保留输入顺序或执行唯一声明的 typed sort；set 按 `set_sort_policy` 对 typed canonical tuple 排序，重复 member（即使字节相同）拒绝而不是去重；root/count 必须由同一 generator 计算。 |
| Unicode | 文本必须是合法 UTF-8，并按 profile（默认 NFC）规范化；非法序列、禁止控制/非字符或把 ASCII token 做大小写折叠均拒绝。opaque bytes、digest 和 content hash 只能在 profile 明确声明时按原字节处理。 |
| path | 仅接受 profile 声明的相对 component/vector；拒绝 NUL、绝对路径、`.`/`..`、空组件、未声明分隔符、symlink/magic-link 和 root 逃逸。读取以已绑定 directory-fd 逐 component `NOFOLLOW`/beneath traversal，并用 `fstat` 核对 device/inode/mount；不得按原 path 二次 open。 |
| URL/URI | 按 profile 的 RFC3986 解析与 scheme/host/port/percent 规则生成唯一表示；未声明的 scheme、userinfo、控制字符、模糊 host/port、禁止 origin 或私网/本机目标拒绝，不能用字符串拼接或 DNS/redirect 结果替代 canonical input。 |
| FD/handle | 请求只携 stable inventory handle（role、object identity、generation、CLOEXEC/rights），不携裸数字 fd；未知、已关闭、重复 identity、type/mount 不匹配或未登记继承 fd/socket/handle 拒绝，set 由 profile 规定的 typed sort 排序。 |
| DB mutation/commit set | 以 `(db_schema_version, table, primary_key, operation, expected_generation, value_digest)` typed tuple 表示；表/列/操作必须在 allowlist，重复 key、隐式全表/未排序迭代、SQLite `now`/随机值、缺 commit fence 或 root/count 不一致拒绝；DB-set root 只由 generated set canonicalizer 计算。 |

`CommandOwner` 是 profile 的第一运行时 consumer：local finalize 必须按真实 command/attempt、method/params、authority-complete spawn request、content ref/hash 或 DB mutation/commit set 查 registry，并把 `effect_request_profile_digest`、`canonical_effect_request_digest`、`boundary_profile_id`、`boundary_id/audience` 和 canonical payload/root 写入 non-authorizing candidate；调用方不能省略、替换或增加 `authorized_transition_set`。`SecurityDomainSequencer` 仅消费并逐字段比较 registry/profile digest、authorization head 和 candidate，不生成 profile；`provider bridge`/actual boundary 是第二运行时 consumer，必须调用与 candidate 相同的 generated canonicalizer；wait、UI、room、wakeup、RecoveryCustody 只能读取这些字段/receipt，不能重算或拥有 registry。RecoveryCustody 继续由外部单实例服务独占私有 SQLite，OUT 只经认证 client 读取 availability/abort receipt，绝不打开、迁移或共享其 authority 文件。

candidate 与 actual 必须满足同一 exact tuple：`(effect_kind, boundary_profile_id, profile_generation, profile_schema_version, request_schema_digest, canonicalizer_digest, hash_algorithm, hash_domain, key_profile_or_absent, boundary_id, boundary_audience, canonical_effect_request_digest, canonical payload/root, content ref/hash 或 DB-set root, 完整 generation vector/逐源 receipt)` 逐字段相等。candidate 生成成功只表示 non-authorizing intent；在 `effect_authorization_pending` 之前必须完成 registry lookup、schema/duplicate/normalization 校验和 digest 计算。actual boundary 到达后从真实输入再次生成同一 tuple，比较失败、profile/head 不可读或 response unknown 均不得 redeem/issue/consume 或调用 provider；不得 fallback、重排、重新解释或以旧 profile 继续。只有 profile/候选已授权后发生的 nonce/provider response unknown 才按第6节保持 `effect_outcome_unknown` 并只查询原 id，不能把 pre-authorization 的 unknown 当作可授权。

### 4.2 Generated message/reply association profile（§10.5 gap）

消息和回复也必须经过 generated profile，不能把 wire 上的裸 `correlation_id`、`reply_to` 或可猜测 message id 当作关联事实。`ProfileRegistryOwner` 为 `message|reply` 生成 immutable profile；`CommandOwner` 是 message/reply command、idempotency 和 receipt 的唯一写 owner，`IdentityOwner`/`RoomOperationOwner` 只验证其各自 fence/scope，ContentOwner 只拥有 content ref，bridge 只消费已授权 command。每条 reply 都是独立的幂等 message command，有自己的 `command_id`、`attempt`、`idempotency_key`、content ref、dispatch/receipt/terminal 链；回复关联不创建第二 transcript 或第二 outbox owner。

profile entry 的真实 schema 固定为：

```text
message_association_profile = {
  message_kind, profile_generation, profile_schema_version,
  canonicalizer_id, canonicalizer_version, canonicalizer_digest,
  delivery_union_schema, busy_policy_schema_or_absent,
  source_principal_schema, source_fence_schema_or_controller_only,
  target_fence_schema, association_object_schema,
  association_scope_schema, cardinality_policy, intent_hash_domain,
  idempotency_namespace_rule, allowed_fields_digest,
  profile_payload_digest, availability_receipt
}
```

实例中的 `association_profile_digest` 必须逐字等于该 entry 的 `profile_payload_digest`；它不是 `intent_hash`，也不能由 wire 名称或 parent object digest 代替。

实例记录必须只含 profile 允许的判别分支：

```text
message_association = {
  association_profile_digest, message_kind,
  delivery, busy_policy_or_absent,
  source_principal_digest, source_execution_fence_or_controller_only,
  target_execution_fence, association_object,
  association_scope, cardinality, intent_hash,
  content_ref, idempotency_namespace, idempotency_key,
  command_id, command_version
}
```

`delivery` 只允许 `mid_turn|next_boundary|new_turn`。只有 `new_turn` 可出现 `busy_policy.action=fail|queue|interrupt_then_send`；其余分支必须 absent，混合字段、未知 action 或未知 delivery 直接拒绝。`source_principal_digest` 始终来自 transport-authenticated CallerPrincipal；`controller_only` source 不伪造 source incarnation。target 必须带完整 execution/turn handle、incarnation、lease/room generation、capability 和 expiry fence，不能只带 agent/thread id。`association_object` 是 generated typed object reference（message/reply、object id、object generation、object digest、owner kind），reply 必须恰好引用一个已授权的 parent message object；`association_scope` 固定 workspace/run/task-tree/room 及其 generation，不能由同目录、共同 parent 或 token 推断。`cardinality` 是 registry 声明的判别值（至少 `one_parent_per_reply` 与 `one_command_per_reply`），实例必须逐字段满足，不得把一条 command 拆成隐式多 reply 或把多个 parent 合并。

`intent_hash` 由同一 generated canonicalizer 对 source principal、target fence、delivery/busy union、typed association object/scope/cardinality、content ref/version 和 idempotency namespace/key 计算；不把 server-resolved latest projection 偷塞进 client hash。JSON object duplicate key、header/env duplicate、未知字段、裸 `correlation_id`/`reply_to`、缺 object/scope/cardinality/intent hash、parent digest/generation 不匹配、非 canonical Unicode/URL/path 或 target/source stale/revoked 都必须在 authorization 前 fail-fast。不得 last-wins、静默补字段、把 `reply_to` 映射成 object id 或把 Room aggregate receipt 冒充 target reply accepted。

send 与 reply 的 candidate/actual 也要 exact match：CommandOwner 在 local finalize 时按同一 association profile 生成并持久化完整实例和 `intent_hash`；bridge/actual boundary 必须用相同 generator 重算 delivery union、source/target fence、object/scope/cardinality、content ref 和 hash，逐字段及 digest 相等后才可 redeem/issue/consume。profile/read/authorization response unknown、duplicate 或 canonicalization error 在授权前均不得创建 attempt、receipt 或 provider call；授权后的 command/nonce unknown 只能按原 command/submission fence 查询。重启、late receipt 或 old worker 只允许回读原 `command_id`/idempotency key，绝不为同一 reply 生成第二 command；未经 discover/read/send 独立 grant 或 stale/revoked fence 的 reply 必须零副作用且不泄漏目标存在性。

### 4.3 Generated ordinary-mutation profile 与 pre/post root

普通 source mutation（例如 runtime-effect lease、resource/content release、room/wakeup 状态变更）不能借用 effect profile 的部分字段，也不能把普通 SQLite commit 当作权威线性化点。`MutationProfileRegistryOwner` 是归属 CommandOwner 的 build-time generator/release role，只生成 immutable `mutation_profile`，不是第二个 runtime writer；`SecurityDomainSequencer` 是 `mutation_pending` head 的唯一 CAS owner，具体 source owner 只提交带 ticket 的真实 payload/current state，CommandOwner 负责把 mutation 与 command/attempt/receipt 绑定。RecoveryCustody 仍是外部单实例服务独占私有 SQLite；它只返回恢复域外 availability/non-commit/post-state receipt，不能写 ordinary mutation 或打开 OUT 的数据库。

generated `mutation_profile` entry 固定为：

```text
mutation_profile = {
  mutation_kind, boundary_profile_id, mutation_schema_version,
  request_schema_digest, canonicalizer_id, canonicalizer_version,
  mutation_canonicalizer_profile_digest, hash_algorithm, hash_domain,
  key_profile_or_absent, json_duplicate_policy, list_order_policy,
  set_sort_policy, unicode_policy, path_policy, url_policy,
  db_set_policy, pre_root_algorithm, post_root_algorithm,
  allowed_fields_digest, profile_payload_digest, availability_receipt
}
```

每个普通 mutation 先由 sequencer `create-if-absent` 安装唯一 pending record，字段固定为：

```text
mutation_pending = {
  mutation_id, mutation_kind, mutation_schema_version,
  canonical_mutation_request_digest,
  mutation_canonicalizer_profile_digest, source_id,
  boundary_profile_id, boundary_id,
  pre_generation, expected_post_generation,
  expected_pre_state_root, expected_post_state_root,
  source_receipt_status, owner_generation,
  command_id, command_version, attempt_generation,
  dispatch_receipt_digest, availability_receipt
}
```

`pre_generation`/`expected_pre_state_root` 必须是 source ticket 线性化点读到的精确 current；`expected_post_generation` 只能是 generated transition 规定的后继（通常为 `pre_generation+1`），`expected_post_state_root` 必须由同一 profile 从真实 post payload/current state 计算。`canonical_mutation_request_digest` 覆盖真实 typed request、判别 kind、stable source/boundary、delivery policy 和 profile digest；不能把未来 receipt 或 server latest projection 混入。`dispatch_receipt_digest` 只允许引用当前 command/attempt 的 dispatch custody receipt，不能用普通 DB commit、message accepted 或 provider ack 冒充。

`source_receipt_status` 是 generated enum，仅允许 `pending|available|post_state_finalized|non_commit_aborted|outcome_unknown`；状态转换必须由对应 owner 的 availability、post-state 或 non-commit receipt 驱动，不能由 lease expiry/本机 timeout 代填。

source owner 在持有 generated ticket 后，于真实 source 线性化点重算 request digest、pre root 与 post root，并取得恢复域外 availability receipt；receipt 必须 exact 绑定 `(mutation_id, mutation_kind, canonical_mutation_request_digest, boundary_id, pre_generation, expected_post_generation, expected_pre_state_root, expected_post_state_root)`。同 mutation id+digest 幂等返回原 pending/terminal，异 digest 冲突；unknown 只 query 原 mutation id。任何 duplicate/unknown field、profile/schema/canonicalizer 不可读、输入不可 canonicalize、root/count 不一致或 source receipt 不匹配都在 authorization/redeem 前 fail-fast。

只有两条收口路径：

1. **post-state finalize**：权威 post-state receipt 逐字段等于 pending 的 expected post generation/root、request digest、boundary 和 source receipt；sequencer 才能 CAS 清除 pending，并由 CommandOwner 在同一 command/attempt 链写 generated mutation terminal/receipt。
2. **pre-state non-commit abort**：恢复 owner 提供权威 non-commit proof，且 source 仍逐字段等于 pending 的 pre generation/root；sequencer 才能 CAS abort。仅看到 generation 到达、worker lease 过期、TTL 到期、checkpoint 回滚或本机 response unknown，均不能清 pending、accepted command、claim attempt、redeem/issue permit 或 dispatch。

post generation 正确但 post root/digest/boundary 不同，或只读到部分 receipt 时，pending 必须保持并撤销授权、进入 reconcile/unknown；successor 必须继承该 pending 与 consume floor，不能 fresh genesis。`mutation_pending` 未经 post-state finalize 或 pre-state non-commit abort 前，关联 command 不得变为 accepted/dispatchable，attempt 不得创建新的 effect permit，wakeup/room/worker 也不得旁路。这样形成 `source payload/current state -> mutation_profile/canonical request -> mutation_pending(pre generation/root, expected post generation/root) -> source availability receipt -> command/attempt/dispatch receipt -> post-state finalize 或 non-commit abort -> terminal/wait` 的单向字段消费链。

## 5. Terminal、abort cleanup 与 receipt mapping

command phase 使用 generated registry：`recovery_custody_pending -> accepted -> dispatch_custody_pending -> dispatched -> effect_authorization_pending -> effect_token_issued -> external_effect_started -> provider_acknowledged -> completed`。
允许的终止分支为 `dispatch_aborted_before_call`、`effect_aborted_before_issue`、`effect_aborted_before_start`、`abort_cleanup_pending`、`outcome_unknown`、`rejected|failed`；禁止自造同义状态。
`abort_cleanup_set` 初始 `armed`，状态只允许 `armed|cleanup_pending|cleanup_cleared|transferred_to_effect_lifecycle|effect_outcome_unknown`，计数单调且 root/count 与 item set 一致。
permit 从未 redeem 且 recovery non-commit proof 齐全时，才能直接写 `command.dispatch_aborted_before_call + command.rejected`，证明 call count=0。
redeem 后 token 未 issue 只能先写 `effect_aborted_before_issue`，issue 后只能由 actual-boundary nonce slot 的 non-consume winner 写 `effect_aborted_before_start`；两者都进入 cleanup，不得立即 terminal。
abort/non-consume winner 与 cleanup set `armed -> cleanup_pending`、全部 dormant item 安装必须同一 sequencer CAS；原 message receipt 保持 `accepted`。
cleanup worker 逐项按 expected-current ticket 撤销 lease、释放 RESOURCE usage、释放 CONTENT ref、收口 reservation 并取得恢复 receipt；already superseded 只接受 generated 等价安全 terminal。
所有 cleanup item read-back 为 cleared 后，CommandOwner 才能同 transaction CAS `cleanup_cleared`，写专用 abort event、`command.abort_cleanup_completed` 与 authoritative `command.rejected`，wait 才 terminal。
缺 physical zero-effect proof、cleanup receipt 或 current root 时保持 `abort_cleanup_pending|effect_outcome_unknown`，不补 permit/token/call，不改 generation 重发。

## 6. Nonce slot、provider receipt 与 unknown

issued token 的 actual consume/start、boundary non-consume 与 security-time `expire_or_abandon` 共用 `{effect_authorization_id,issuance_id,one_use_nonce,effect_fence_generation}` slot，CAS 恰一赢家。
actual consume 只可在 `must_start_effect_by` 前按真实 boundary 完成；security-time owner 只能在严格晚于 deadline 且 slot 空时签 non-consume tombstone。
slot response unknown 只查询原 nonce/issuance/effect authorization；不可读时 command 与 cleanup 可保持 unknown，并阻断第二 token。
provider ack 只有具有关联 command/message 的稳定 receipt 才写 `provider_acknowledged`；下一次 output、时间经过或父 agent 行为不是 consumed/observed proof。
receipt 状态固定 `accepted|provider_acknowledged|observed_in_context|rejected|outcome_unknown`；`accepted` 只代表持久化接收，不能冒充 provider receipt 或 completed。
provider 明确接受但 identity/CAS 未完成时写 reservation identity pending；dispatch 后 receipt 丢失或 provider 可能已 effect 时写 `outcome_unknown`，不把 lease expiry 当未执行。
同时拥有 provider stable idempotency key 与 receipt query 时，只有对应 conformance 证明作用域/持久性，才能按原 key 做受控重试；只有 receipt query 时只查询不重放。
没有 key 和 receipt query 时返回 `command_outcome_unknown|delivery_outcome_unknown`，并撤销 fully-controlled/ack capability；不能自动再发。
迟到 ack/completion 若 attempt/incarnation fence 匹配，写原 command/incarnation audit；已 terminal 的 command 不回退、不创建新 attempt、不推进 active node。

## 7. Wait、权限与字段消费链

`command` wait 必须携完整 command fence，并逐字段回验 command id/version、subject selector、accepted cursor、recovery receipt digest、target execution fence、attempt/lease/terminal cursor。
`command_submission` wait 只接受 submission fence，返回 accepted command fence 或 authoritative abort；不接受裸 agent/thread id 或 latest projection。
caller principal 由 transport/auth middleware 构造；idempotency key 仅防重复，不建立权限；diagnostic/full fence 需要独立 grant。
command owner 字段消费链固定为：caller principal → canonical intent digest → TRUST content role → CONTENT ref/ticket → RESOURCE reservation/policy/usage → accept custody receipt → command fence → attempt/dispatch receipt → permit/redeem/issue/nonce → provider receipt/event → terminal/cleanup → wait/projection。
每个字段必须有唯一 producer/consumer：recovery receipt、reservation version、content ref/version、effect profile/digest、generation vector、cleanup root/count、nonce status 不能被 UI/room/legacy writer 丢弃或重算。
wakeup/room/internal producer 只能按 canonical idempotency key 找回同一 command；不得先 LaunchAgent、后 mark sent，也不得以 Room receipt 冒充 target accepted。
command/event/message/output 内容均保持 TRUST 的 untrusted quoted-data；OUT 不因拥有 command owner 身份而提升正文信任。

## 8. 崩溃、reclaim 与恢复

accept receipt、local finalize、attempt claim、permit redeem、issue、nonce consume、provider ack 和 cleanup 每个边界都必须有 deterministic crash fixture。
accept response unknown 只查询原 recovery batch/command id；dispatch receipt 或 permit 任一可能存在时 lease 到期先 outcome-unknown，再按 provider contract reconcile。
只有 `dispatch_started=false`、dispatch phase receipt 不存在、recovery owner 明确 non-commit 且 call permit 从未 redeem 的 attempt 才可安全 CAS 新 generation reclaim。
provider success 到 mark/event 前崩溃不得由 wakeup reclaim 再次 launch；旧 attempt 的 fence/receipt 必须保持可查并使 duplicate call RED。
Content/Resource cleanup response unknown 时，CommandOwner 不删除 ref、不释放 usage、不写 terminal；由对应 owner 按原 cleanup id 重放并回读 receipt。
daemon/bridge 重启只重建 worker、queue、process guard 和 connection；command/event/content/resource truth 必须从 durable records/cursors rehydrate，reconcile 完成前禁止写操作。
security-time、writer/deployment、ID incarnation、CONTENT ref 和 RESOURCE policy 任一 current drift 都撤销 permit/claim；不能用缓存或“尚未收到响应”继续执行。

## 9. 当前差距（不关闭）

当前源码已复核：migration126 提供不可变 base intent，migration127 提供 append-only 非授权生命周期日志；migration131 又为 `send_message_v1` 提供一对一 CallerPrincipal/authorization/双端 current-binding profile、原子 store 与未装配 assembler。旧 fake Accepted seam 已删除。它们都不是 production admission、recovery custody 或 dispatcher 接线。
总规格要求的 CONTENT/RESOURCE join、accepted full fence、`command_submission` 分支、attempt receipt、generated permit/nonce、cleanup set、provider outcome unknown 与 wakeup 单 owner，当前 ST-11 仍明确待实现/验证。
ST-08 已实现 direct-peer 的部分持久消息、event/outbox、claim/lease/expired takeover/projected ACK，但不等于统一 command/outbox、完整 receipt、Room 或 recovery-domain 证明。
ST-13 只部分证明 Codex fork/interrupt/stop/recover，Claude 能力错误仍存在；ST-14/16/18 未完成，不能从局部 CLI 结果外推 OUT。
ST-07 的真实 Codex/Claude managed task 与 ST-05/06 客户端接入仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不构成 external dispatch、发布制品或 provider idempotency conformance。
当前 standalone direct-peer 成功只说明既有局部路径，不证明 `caller-stable key+digest`、intent-before-dispatch、effect nonce slot、完整 cleanup 或 cross-incarnation fence 已由源码/测试闭环。
RecoveryCustody已有[local SQLite authority/kernel lease/HTTP transport candidates](../../plans/orch-subagent-upgrade/recovery-custody-sqlite-authority.md)、revision-3（含external-service mode）DB writer_epoch/takeover和typed observation/同key reconcile，但没有独立服务进程、真实外部EvidenceFinalizer/anchor/签名trust root、durable coordinator job/journal/startup scan或production orch client/Fx/OUT接线。SecurityDomainSequencer、generated terminal/effect profile、真实provider receipt query/side-effect boundary和完整RESOURCE/CONTENT owner也未实现或接线；不能因本地候选存在而推断它们通过。

## 10. 具名 RED/GREEN 验收

RED-OUT-01 `DispatchBeforeIntent`：没有 accepted command、recovery receipt、resource/content fence 和 attempt dispatch receipt 时 provider call count=0。
RED-OUT-02 `CallerKeyDigestConflict`：同 principal/key 异 canonical digest、target/incarnation/source/delivery 改变时返回 conflict，不新增 command/reservation。
RED-OUT-03 `AcceptUnknownCannotDispatch`：accept CAS/receipt 不明只返回稳定 submission fence，`command_submission` 查询前不得 claim、redeem 或调用外部。
RED-OUT-04 `WakeupReclaimAfterProviderSuccess`：provider 成功但 mark 前杀进程，wakeup reclaim 不得重复 launch；旧 command 进入 receipt/audit 或 unknown。
RED-OUT-05 `AttemptUnknownNoBlindResend`：dispatch/permit/nonce/provider receipt 不明时只 query 原 id；没有 conformance key+query 不得自动重发。
RED-OUT-06 `PostRedeemAbortNeedsCleanup`：pre-issue/post-issue abort 保持 message accepted，cleanup 未清零前 wait 不 terminal、不得再签 token。
RED-OUT-07 `NonceSingleWinner`：actual consume、boundary non-consume、expiry/abandon 在同 slot 恰一赢家，输家不得启动第二 effect。
RED-OUT-08 `StaleFenceZeroEffect`：旧 ID incarnation、RESOURCE policy、CONTENT ref、writer/security-time 或 caller grant 在 actual boundary 漂移时 call count=0。
RED-OUT-09 `RoomReceiptNotTargetAccepted`：room aggregate receipt 不能冒充 per-target command accepted；target pending/rejected/unknown 必须逐项保留。
RED-OUT-10 `EffectProfileUnknownBeforeAuthorization`：`(effect_kind,boundary_profile_id)`、profile/schema/canonicalizer 或 registry head 不存在/不可读时，在 authorization 前拒绝，permit/nonce/provider call 均为0。
RED-OUT-11 `EffectProfileDuplicateInput`：JSON/env/header/set duplicate、未知字段或 mixed union 分支不得 last-wins/合并，必须在 candidate authorization 前返回 typed schema error。
RED-OUT-12 `EffectProfileCanonicalizationFailure`：非法 Unicode、path escape/symlink、URL/FD/DB-set 不可 canonicalize 或 root/count 不一致时，authorization、redeem、issue 和 provider call 均为0。
RED-OUT-13 `EffectCandidateActualMismatch`：actual boundary 的 profile key/version/digest、algorithm/domain/key profile、normalized payload、boundary/audience、content/DB-set root 或 generation vector 任一与 candidate 不同，拒绝 consume/start 且不得调用 provider。
RED-OUT-14 `MessageDeliveryUnionMixedFields`：`delivery` 非法或 `new_turn` 以外携带 `busy_policy`（或 `new_turn` 缺失/重复 busy action）时，消息/回复 command 不创建。
RED-OUT-15 `MessageAssociationUnknownDuplicate`：association profile/object/scope/cardinality/intent hash 缺失、未知、duplicate，或出现裸 `correlation_id`/`reply_to` 时，在授权前拒绝且不泄漏目标存在性。
RED-OUT-16 `ReplyWithoutIndependentCommand`：回复复用 parent command/attempt、缺独立 idempotency key 或把 Room aggregate receipt 当 target accepted 时，provider call=0，parent receipt 不被覆盖。
RED-OUT-17 `ReplyUnauthorizedOrStaleFence`：discover/read/send grant 缺失，source/target incarnation、scope、generation 或 expiry stale/revoked 时，回复零副作用并返回 typed unauthorized/stale。
RED-OUT-18 `ReplyRestartNoDuplicate`：reply dispatch/receipt response lost、daemon restart 或 old worker reclaim 后，不得创建第二 reply command/effect，只能 query 原 command/submission fence。
RED-OUT-19 `OrdinaryMutationPendingBlocksDispatch`：ordinary `mutation_pending` 的 pre/post root、source receipt 或 response unknown 未收口时，不得 accepted、claim、redeem、issue 或 dispatch。
RED-OUT-20 `OrdinaryMutationWrongPostRoot`：generation 到达但 post root/digest/boundary 不同，或缺 post-state receipt 时，pending 保持并撤权；不得猜测 finalize 或创建 successor。
GREEN-OUT-01 `AcceptSubmissionAndCommandWait`：crash/response lost 后原 submission fence 可查询 accepted/aborted，accepted 逐字段返回 recovery-bound command fence。
GREEN-OUT-02 `IntentAttemptPermitLifecycle`：accept→attempt→candidate→redeem→issue→nonce→provider receipt 的每阶段有唯一 root/generation/receipt 和 crash replay。
GREEN-OUT-03 `AbortCleanupSaga`：两类 post-redeem abort 逐项撤销 lease、释放 resource/content/ref、cleanup root/count 清零后一次性 terminal reject。
GREEN-OUT-04 `ProviderUnknownReconcile`：provider outcome unknown 只按稳定 id/query 或 typed unsupported 收口，重启、late receipt、old worker 不重复 effect。
GREEN-OUT-05 `WakeupAndRoomUseCanonicalCommand`：wakeup 与 room fan-out 都能找回同 command/attempt fence，Room close drain 按每目标 terminal truth 完成。
GREEN-OUT-06 `GeneratedEffectProfileRoundTrip`：registry generator、CommandOwner candidate 与 bridge actual 使用同 profile generation/version/canonicalizer digest/algorithm-domain-key policy，round-trip digest 与字段逐字一致。
GREEN-OUT-07 `EffectProfileCanonicalDeterminism`：允许的 JSON/env/header/list/set/Unicode/path/URL/FD/DB-set 输入在 candidate 与 actual 两处得到同一 canonical bytes/root/count；duplicate/unknown/invalid 输入均命中对应 RED。
GREEN-OUT-08 `EffectBoundaryExactMatch`：真实 method/params、authority-complete spawn、content ref/hash 或 DB commit set 经 actual generator 重算后与 candidate exact tuple 相等，才由同 nonce slot consume+start。
GREEN-OUT-09 `MessageAssociationRoundTrip`：message/reply generated association profile 对 delivery union、source/target fence、typed object/scope/cardinality/content ref/intent hash 做 candidate→actual 逐字段 round-trip，任何 mixed-field 都被拒绝。
GREEN-OUT-10 `ReplyIdempotentRestart`：每 reply 独立 command/idempotency key；重复请求返回同 command，丢响应/重启/late receipt/old worker 只 query 原 fence，最终最多一个 accepted/provider effect。
GREEN-OUT-11 `ReplyAuthorizationAndScope`：跨 task/tree 的 discover/read/send 分离授权、Room 不扩权、controller-only 不伪造 incarnation；合法 source/target fence 成功，越权/撤权路径零副作用。
GREEN-OUT-12 `OrdinaryMutationPostOrNonCommit`：真实 source ticket 生成 exact pre/post generation/root 与 availability receipt；权威 post-state receipt finalize，或权威 pre-state non-commit proof abort，二者单赢家且 command/attempt/terminal 链可重建。
GREEN-OUT-13 `OrdinaryMutationUnknownRecovery`：source/receipt/worker/recovery response unknown、TTL、lease expiry、checkpoint rollback 均保持 pending/unknown 并可按原 mutation id 查询；无第二 permit、issue、dispatch 或 fresh genesis。

## 11. 退出条件与 open decision

退出必须具备 CommandOwner、SecurityDomainSequencer、RecoveryCustodyOwner、RoomOperationOwner 的 writer inventory、generated phase/terminal registry 和字段消费者差集为零。
必须证明 caller-stable key/digest、ID/CONTENT/RESOURCE joins、intent-before-dispatch、accepted/submission wait、attempt receipts、nonce single-winner、cleanup saga 和 unknown query 的具名 RED/GREEN 全部通过。
必须有真实 deterministic fault fixture 覆盖 provider 已受理、本地响应丢失、daemon/worker crash、lease expiry、late receipt、recovery takeover、content/key/object unknown 和 resource tighten。
provider call 仍只能在已批准 bridge profile、actual effect boundary、runtime-effect lease、active-process/binding fence 和发布 evidence 全部可读时出现；本 Draft 不授权该调用。
Open decision A：完整生产 `orchestration_commands`/attempt/terminal registry 的 DDL、generated schema/canonicalizer、writer epoch 与 production adapter；migration126/127/131 与 sqlc 仅是未发布内部候选基础。
Open decision B：RecoveryCustody数据库方向已冻结为外部单实例服务独占私有SQLite，PostgreSQL排除；仍待裁决/实现production lease、认证协议、恢复域外EvidenceFinalizer/availability、anti-rollback/security-time、签名trust root、receipt retention和single sequencer HA。
Open decision C：每个 provider 的 idempotency key/query/receipt conformance、actual boundary instrumentation、effect profile 和 unsupported/error mapping。
Open decision D：abort cleanup item 的 generated dependency graph、CONTENT/RESOURCE release receipts、Room/wakeup migration以及 ST-08/11/14/16/18 的发布级证据。
在 ID、CONTENT、RESOURCE 获批，以上决策关闭，所有 ancestors/RED/GREEN/release rollback evidence 完成前，本文件保持 `Status: Draft`，不声称本轮已启用外部 dispatch。
