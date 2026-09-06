# ST-11 send_message_v1 身份/授权 Profile

Status: Active

日期：2026-09-01；上级：[不可变命令意图](command-intent-storage.md)与[可靠命令实现切片](command-reliability-next-slice.md)。

## 本切片的实际能力

migration 131 新增 `orchestration_command_intent_profiles`，作为 migration126 基础 intent 的一对一、不可变、kind-specific 扩展。当前只注册 `send_message_v1`：其它 launch/fork/interrupt/stop/recover/Room kind 仍只能形成 non-authorizing base intent，不能借用 send grant 或 active-binding profile。

`SendProfileStore.Reserve` 在同一个 SQLite `BEGIN IMMEDIATE` transaction 中完成：

1. 创建新的 base intent，或找回同 caller-stable key 的原 base；
2. fresh 路径通过事务身份 reader 重读 source/target 最新 ID-1 binding；
3. migration trigger 同一线性化点复核 parent intent、active send grant revision、source/target 全字段 binding 和不存在更高 binding version；
4. 写入完整 CallerPrincipal profile/auth-context/expiry、authorization request/grant/revision/capability/time、两端full current-binding snapshot和domain-separated HMAC digest provenance；
5. migration132 trigger要求owner-issued active/live CONTENT ref，并在同一事务创建`held/non_terminal_command` deletion fence；
6. fresh返回、Get、GetByKey和replay均要求profile→fence→ref→object→reservation完整联接；任一步失败时base/profile/fence一起回滚。

同key exact replay返回首个canonical command ID和原profile/time；若base已由generic `Reserve`单独存在而profile缺失，则返回`ErrIncompleteIntent`，不补写历史认证事实。非空v131 profile升级后进入append-only legacy CONTENT quarantine，Get/GetByKey/replay返回`ErrIncompleteIntent`+`ErrLegacyContentProofMissing`，零自动ref/fence。profile任一字段或HMAC改变返回conflict。读取完整profile时重新验证HMAC与CONTENT relation；key/version不可读时fail closed。

## 字段来源与依赖方向

```text
common.RequireCallerPrincipal(ctx)
  -> commandintent.Assembler（组合层）
  -> accesswait.AuthorizeSend
  -> IdentityRepository.GetCurrent(source/target)
  -> CallerSnapshot + AuthorizationDecision + ActiveBindingSelector
  -> HMAC complete intent digest
  -> SendProfileStore BEGIN IMMEDIATE
  -> transaction current-binding reread + migration131 relation trigger
  -> migration132 CONTENT relation + deletion fence trigger
  -> immutable CompleteIntentRecord
```

`orchestrationcommand` 与 `accesswait` store 都不导入 transport common。只有未装配的 `cmd/mcp-orch/orchestration/commandintent` 组合层同时观察 transport principal、authorization、identity与CONTENT proof，并显式映射字段。caller candidate ref不再直接进入intent；只有owner-issued canonical ref才参与profile HMAC。旧 `accesswait.CommandSubmission/CommandResult/CommandAdmission`、`SubmitAuthorizedCommand` 及测试 fake `Accepted=true` 已删除，防止不具备 recovery receipt 的假成功面继续存在。

## 明确不具备的能力

- profile record 是历史 authorization-bound draft，不是 OUT `recovery_custody_pending`，也不是 accepted；command journal 行数保持0。当前无production CONTENT proof issuer，因此production连此draft也不能新建。
- `request_content_ref`只允许owner-issued canonical ref进入完整digest。migration132已有CONTENT-1 reservation/object/active-ref/fence候选，但production proof issuer、真实key provider、decrypt/erasure/custody和Fx/tool接线仍缺失，因此不能解读为可派发。
- 没有 RESOURCE policy/reservation/usage join，没有 RecoveryCustodyOwner、SecurityDomainSequencer、permit/nonce、attempt、provider receipt 或 reconcile。
- Assembler、SendProfileStore、digest key resolver 都没有 Fx/tool/manifest 注册；生产 direct-peer/wakeup/DAG/provider 调用链未接入。
- configured standalone principal 仍只是 local operator transport identity，不是 child/task principal issuer，也不自动获得 source/target send grant。

因此本切片成功只证明“完整 principal + grant decision + 双端 ID-1 binding 能原子持久化为 non-authorizing send draft”，不授权跨任务发送或外部 provider effect。

## 验证与证据等级

本机候选测试覆盖fresh原子写、canonical replay、base-only missing、legacy quarantine、逐字段/profile/HMAC conflict、grant/current-binding漂移回滚、CONTENT fence冲突整体回滚、并发单赢家、关闭重开、migration130→131空扩展、非空v131→132 quarantine升级、schema/trigger启动门禁、读写mapper field guard真实删字段RED→GREEN，以及journal/direct-peer零写入。

| 精确入口 | 当前结果 |
| --- | --- |
| `TestSendMessageProfileStoreContract` host-light | 历史 host-light 观测曾 exit 0（archtest166.682s，业务0.775s）；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |
| `TestCommandIntentAssemblerContract` host-light | 历史 host-light 观测曾 exit 0（archtest154.719s，业务0.125s）；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |
| `TestCommandIntentProfileMigrationContract` host-light | 历史 host-light 观测曾 exit 0（archtest132.001s，业务0.064s）；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |
| `TestContentSingleMigrationContract` host-light | 历史 host-light 观测曾 exit 0（archtest187.785s，业务1.342s）；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |
| 完整相关本机包 | orchestrationcommand、commandintent、accesswait、identity、eventstore、sqlite、platform/db 当前本机测试通过；不等于 host/release 证据 |
| sqlc稳定性 | 固定 v1.30.0 对两份 manifest 重生成；profile/model/query等四份哈希前后一致 |
| race | `--race-only` 在测试前被策略拒绝，要求受信 Ubuntu physical host + Ubuntu/Wine logical runtimes；未绕过，也没有 race PASS |

所有历史 host-light 与当前本机结果仍明确标记 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；发布制品、跨进程 crash、真实 CONTENT/RESOURCE/custody、race 和 provider E2E 均未取得。

## 下一依赖

1. 为已实现的CONTENT-1 store补真实key provider、commandintent/admissionproof adapters与生产composition；RESOURCE policy/reservation/usage owner仍待实现，没有authoritative producer时不接受默认/空ref。
2. 实现 RecoveryCustodyOwner 与恢复域外 availability/abort receipt，随后才允许把完整 draft 推进为 `recovery_custody_pending` submission fence。
3. 增加 command_submission 查询面、accepted finalize、attempt/permit/nonce/provider receipt/unknown reconcile；全程保持原 key 查询，不盲重发。
4. 只有上述依赖和发布门通过后，才把 composition assembler 接入 public cross-task send；Room 使用独立 profile/membership/dispatch generation，不能复用 `send_message_v1` target selector。

proof与custody的当前fail-closed接缝见[CONTENT/RESOURCE Proof 与 RecoveryCustody 合同](pre-admission-proof-and-custody-contract.md)。
