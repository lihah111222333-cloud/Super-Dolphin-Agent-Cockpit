# 跨进程升级与回滚子规格

Spec-ID: ROLLOUT

Status: Draft

Depends-On: none
S0-Owner: independent rollout owner with database, recovery-custody, and runtime-effect owner joins
S0-Scope: writer cutover, migration, restore, production-open heads, rollout epochs, canary, and rollback
S0-Dependencies: none
S0-Inputs: compatibility manifest, writer epoch, deployment fence, recovery tail, restore attempt, and eligibility snapshot
S0-Outputs: checkpoint/restore receipts, current-head decisions, candidate/epoch assignment, and rollback terminal
S0-Failure: stale writer, missing head, incompatible manifest, or unknown restore remains fenced and cannot reopen effects
S0-Recovery: replay the original restore attempt and tail, reconcile current heads, and require a new fenced successor
S0-Field-Chain: manifest -> writer/deployment head -> checkpoint/tail -> restore_apply -> open head -> effect/commit boundary
S0-Named-Acceptance: TestStaleWriterCannotMutateAfterCutover; TestRestoreAttemptCannotReuseOldOpenReceipt

日期：2026-09-01。所属：[Gate S0 工作包](../../plans/orch-subagent-upgrade/README.md)。

## 1. 范围与非目标

本规格负责升级、恢复和撤销授权的跨进程边界，不拥有消息语义、Room 成员或 CLI 业务实现。
总规格依据：§1、§5.2、§17～§19 的 Cross-process rollout/rollback 硬门。
[原始总规格](../../../superpowers/specs/2026-07-11-unified-native-subagent-control-plane-design.md)中的数值约束不因本草案而放宽。
本轮不实施新数据库迁移，不操作用户数据库，不执行升级/回滚或签发 production permit。

独立 `mcp-orch serve` 已有单实例内核锁与锁接管证据；该进程锁不等于数据库 writer epoch、已启动 CLI 的撤权或业务恢复。
旧报告文件可读、应用重建成功、provider resume 成功都不能替代硬崩溃恢复矩阵。

## 2. Owner 与依赖

ROLLOUT_S 是版本/owner/runner 输入及回滚骨架获批，只为根规格提供兼容性约束。
PRECANARY_DONE 必须等待适用领域 owner 的真实 mixed-version/migration/restore/rollback attestation。
该完成门不能作为自身基础 schema 的前置，避免循环依赖。

| 对象 | 唯一 owner | 必须约束 |
| --- | --- | --- |
| durable compatibility registry | 版本选择 owner | 所有活跃 reader/writer 范围与 rollback floor |
| migration leader/step checkpoint | 数据库迁移 owner | 单调 fence、强事务、脚本 hash |
| ordinary recovery custody | 外部单实例服务（私有SQLite唯一writer） | 稠密持续tail、外部evidence/anchor、可验签读回receipt；PostgreSQL排除 |
| security journal/side-effect authorization | 各 security-domain owner | 不被旧 checkpoint 回退 |
| recovery_apply_head | 全 security-domain 单链 owner | deployment/attempt scope 隔离 |
| production_open_head | 全 security-domain 单链 owner | absent 先 initialize_revoked，不默认为可写 |
| rollout family/candidate/epoch | 独立 rollout owner | stable family、不跨 build 重抽样 |
| active-process/runtime-effect/binding | 各自原子 owner | 先撤权/停执行，再关闭绑定 |

## 3. 版本与写入合同

durable blob 先验证稳定外层 `record_kind,envelope_version,min_reader,min_writer,payload_length,payload_hash`，之后才解业务 payload。
活跃版本 lease 只作准入证据；实际 mutation 必须在同一数据库事务内 CAS `writer_epoch/deployment_fence`。
旧进程即使还握有 RW connection，也只能收到 stale_writer/zero rows；外部硬隔离不能被 TTL 代替。
版本交集为空时停止写入；交集多值时按 registry/rollback floor 选择共同支持的最高版本，不猜默认。
legacy_v1 与 unified_v2 分 profile/process/endpoint 排空迁移，已有连接不原地换 schema。
未知版本、缺兼容证据、过期身份或内容 hash/length 不符均在 payload 解码前拒绝。

## 4. Migration 与故障恢复

采用 expand -> backfill -> read-switch -> contract；破坏性 contract 必须等 rollback window 关闭。
fresh DB 用不依赖业务 schema 的 bootstrap 锁建立 migration metadata，再由 DB-resident leader+monotonic fence 管理。
每个强写事务必须有界，进入与实际提交边界重读 marker/version/script hash/fence/security time。
过期 leader 必须回滚并释放连接；successor 取得更高 fence 前必须确认旧写入已经停止。
如果平台不能在截止时间内 interrupt/terminate 持锁者，返回明确 rollout abort，不能无限等待并宣称可接管。
每步 checkpoint 与 schema/script hash 同步持久；不通过补默认列或忽略未知状态“恢复”。
本工作包没有宣布新的迁移机制已实现。

## 5. Checkpoint、restore 与重新开放

1. 提升 deployment fence，停止新 accept/claim，对每个 bridge/source 取得明确 barrier receipt。
2. source 无法暂停时，active segment 原子 seal 并生成受 custody 保护的 successor；不能对仍可追加文件计算快照真值。
3. checkpoint manifest 绑定 DB/event/lane/content/keyring/profile、完整身份和 critical receipts；当时的水位不是未来恢复上限。
4. 冷启动先在 absent key 上唯一建立 production_open 的 revoked floor，再按 expected-current 取得全域稳定 recovery-apply grant。
5. apply 只允许指定 deployment/restore_attempt 和隔离 namespace，业务 sink 不可达；持续追尾恢复域外 ordinary/security/authorization heads。
6. 读回验证后写 immutable restore_apply receipt，精确绑定 attempt、前后 grant、manifest/tail/namespace/read-back root/count。
7. apply grant 收口后，production-open candidate/redeem/initial-or-advance/local-bind 按独立 receipt 链进行；旧 attempt 不得开放新 attempt。
8. response unknown 只能查询同 operation id，不能创建第二次 initial、并行 renewal 或另一个恢复 attempt 代替原结果。
9. 任一 current head/epoch 变化、grant 到期或必要证据不可读，立即撤权并回到 catch-up。
10. 所有 effect 仍需 current global open/authorization/runtime-effect/binding 向量一致；“刚恢复过”不是持续运行授权。

## 6. 停止与替换

stop 先撤销 runtime-effect 与 active-process heads，再收口 binding/event。
active-source recover 必须走同一 stable family 的 replacement，不能把新进程身份覆盖到旧执行句柄。
replacement 的 add-P2 和 remove-P1 各自有 operation、授权、nonce、物理 receipt、cleanup 和终态；最后独立 finalize。
等待模型返回或本地标记 stopping 不是物理撤权证明；必须确认进程树、继承资源、provider/network/storage 权限的实际状态。
总规格要求 T0+5 后旧执行不再产生物理副作用；无法持续围住某 CLI/平台时，该 profile 标 unsupported。
单实例锁释放只表示新 owner 可以竞争，不证明旧 descendants 或未知命令已处置。

## 7. 灰度、回滚与证据

stable rollout-family key 不含 artifact/build/epoch；seed 只提交一次，禁止通过换 build 重抽 cohort。
inventory owner 与 assignment/evaluator 分离，固定 eligibility unit/snapshot/subject mapping；不能自报分母。
candidate 与 epoch 的操作要求 head/history/immutable operation query 原子一致。
只有 typed terminal 和真实 non-empty delta 才能推进新 candidate/epoch；新 epoch 从 5% 重走。
5/25/100/GA 的输入和完整观测窗口由 VERIFY 定义；negative 不进入前驱链，只触发撤销/回滚。
rollback binary 必须 reader/schema 兼容，否则进入受 fenced restore；不能在旧 endpoint 上静默切回 legacy。

## 8. 字段消费链与验收

生产链：owner schema -> compatibility/migration/receipt writer -> checkpoint -> restore decoder -> current-head reconcile -> decision/permit -> 实际 effect/commit boundary。
每一条链验证 missing/unknown/stale、完整判别联合、预期 generation 和可复算 digest；字段注册不替代实际 writer 与 consumer 测试。

规划中的 head/receipt field registry（本轮仅登记，不能授予 open/permit）如下：

| Object | Required fields and CAS rule | Producer -> consumer |
| --- | --- | --- |
| `writer_epoch` | `security_domain`, `deployment_id`, `epoch`, `previous_epoch`, `owner`, `issued_at`, `expiry`, `digest`; successor requires `previous_epoch + 1` | migration leader -> every mutation owner |
| `deployment_fence` | `deployment_id`, `candidate_id`, `writer_epoch`, `reader/writer range`, `manifest_digest`, `security_time`, `digest`; CAS rejects stale epoch | rollout owner -> checkpoint/restore decoder |
| `checkpoint_manifest` / `recovery_tail` | `manifest_id`, DB/event/lane/content/keyring roots, `tail_cursor`, `tail_digest`, `deployment_id`, `restore_attempt_id`, `source_head_generation` | checkpoint writer -> restore_apply verifier |
| `restore_apply` | `restore_attempt_id`, `manifest_digest`, `tail_digest`, `namespace`, `previous_apply_head`, `new_apply_head`, `readback_root/count`, `receipt_digest`; one attempt, one terminal | restore owner -> current-head reconcile |
| `production_open_head` / `recovery_apply_head` | `security_domain`, `generation`, `previous_generation`, `state`, `candidate/attempt`, `receipt_digest`, `security_time`; genesis is generation 1 with previous 0, successor must use previous+1 | head owner -> decision/permit/effect boundary |

Head genesis is explicit `generation=1, previous_generation=0`; a successor may only CAS from the exact current generation and writes `previous_generation=current`. Renewal is not a new generation: it requires `renewal_id`, `renewal_digest`, `expected_previous`, and a security-time deadline no later than 5 seconds; stale/late renewal is rejected. Missing or duplicated fields, wrong predecessor, rollback, cross-deployment reuse, and unknown receipt are fenced by `TestRolloutHeadGenesisAndSuccessorCAS` and `TestRolloutRenewalRequiresBoundedDeadline`.

### 8.1 Planning-only receipt artifact and registry contract

VERIFY 与 ROLLOUT 共用一个未来的 planning-only evidence artifact 形状；本轮
只冻结字段和 fail-closed 规则，不新增 machine input、生成文件、签名或生产
consumer。顶层必须声明 `schema_version=1`、`authority=planning_only`、稳定
`artifact_id`、`source_spec`、`graph_ref`、`registry_ref`、`graph_digest`、
`registry_digest`、
`status=needs_revalidation` 和 `decision=NO-GO`，并带有
`cross_task_receipts`、`singleton_receipts`、`sqlite_receipts`、
`field_consumers`、`negative_proofs` 五个数组。数组中的状态只能是
`planned|not_verified|needs_revalidation`；任何 `approved`、`available`、
`accepted`、`completed`、`GO`、enable 或 permit 字段均非法。

`graph_ref`、`registry_ref` 与各 receipt 的 evidence/guard 引用必须先解析为
root 内普通文件，再按实际字节重算 digest；不能用路径字符串或同目录关系推断
内容完整性。`cross_task_receipts` 必须绑定 requirement/scope、candidate、epoch、
operation、request digest 和 evidence digest；不得用 Room aggregate、legacy
`send_message` 或另一 provider 的记录填充。`singleton_receipts` 必须绑定
security-domain、owner generation、精确 predecessor、operation 和 receipt
digest；本地进程锁只可作为未验证定位信息。`sqlite_receipts` 必须绑定
authority/deployment/database scope、schema revision、writer epoch、operation、
request digest、previous-head digest 和 receipt digest；产品 SQLite、WAL、同盘
backup 或本机提交永远不能升级为 RecoveryCustody terminal receipt。

`field_consumers` 逐项列出 canonical field 的 producer、全部 consumer、所需
状态和 guard 路径；`negative_proofs` 逐项列出 scope、触发条件、预期
`indeterminate|NO-GO`、zero-effect counter 及 evidence digest。五个数组都要求
精确键集合、无重复键/ID、无 null/未知字段，且每个引用为 root 内普通 UTF-8
文件；绝对路径、越界或自引用、symlink alias、digest 不匹配、cross-deployment
或 generation 回退都必须阻断。receipt `state`/`status` 只接受规划枚举，禁止
把 `available`、`accepted`、`completed` 等生产终态写入候选 artifact。规划 digest 使用排序键紧凑 JSON SHA-256，明确
不是 JCS 或签名；文件内容、路径、scope 或状态变更必须重新生成 artifact，不能
在旧 digest 上追加“补充说明”。

该 artifact 是 VERIFY evaluator 的输入候选和 ROLLOUT owner 的字段索引，不是
SQLite writer、head owner 或 runtime permit。未来生成器/validator 即使成功
round-trip，也只能报告 planning consistency；在外部 EvidenceFinalizer、
anti-rollback anchor、独立 supervisor 和真实多进程证据齐全前，ROLLOUT_S、
PRECANARY_DONE 与所有 production-open 状态继续 `pending`/`NO-GO`。

具名验收目标（本轮仅登记，尚无 GREEN）：

- `TestStaleWriterCannotMutateAfterCutover`：旧连接所有 mutation owner 写入为零。
- `TestMigrationLeaderCrashAndScriptDrift`：双进程竞争、mid-step kill、脚本漂移、超时 leader 无并行写。
- `TestRestoreAttemptCannotReuseOldOpenReceipt`：同 deployment 不同 attempt 与跨 deployment 不能复用授权。
- `TestUnknownRenewalQueriesOriginalOperation`：丢响应后同 key 查询，禁止并行 renewal。
- `TestReplacementAllCrashPhasesReconcile`：每个 add/remove/finalize 崩溃点无双执行或旧身份复活。
- `TestRevokedRuntimeHasZeroEffectsAfterDeadline`：暂停进程/打开事务/派生子进程等真实边界全部覆盖。
- `TestMaximumEnvelopeRecoveryDrill`：固定 deployment/attempt/manifest/tail/eligibility fixture，连续三次完整恢复；每次必须满足：旧 writer mutation=0、restore_apply readback root/count 精确相等、open/recovery head generation 单调、effect side-effect count=0 直到独立 open receipt、P99 恢复时延在目标环境预算内。任一指标缺失或 unknown 即 `indeterminate`，不承诺恢复指标。
- `TestRolloutPlanningSingletonReceiptSchemaRoundTrip`、
  `TestRolloutPlanningSQLiteReceiptBindsWriterEpoch`、
  `TestRolloutPlanningFieldConsumerAndNegativeProofCoverage`：未来专属
  validator 的规划索引；它们必须证明路径/digest/status/前驱字段缺失或篡改时
  zero decision/permit side effect，不能把 fixture 通过写成真实 rollout GREEN。

## 9. 开放裁决与退出条件

RecoveryCustody数据库方向已冻结为外部单实例服务独占私有SQLite，PostgreSQL排除；当前已有local schema/state-machine、revision-3（含external-service mode）DB-resident writer_epoch/takeover、`KernelAuthorityLease`、typed observation/同key reconcile和认证loopback HTTP transport候选。仍需实现独立服务/supervisor生产接线、network FS合同、真实外部EvidenceFinalizer/anti-rollback anchor、可发布验签terminal proof、durable coordinator job/journal/startup scan、custody/security heads部署与信任边界，以及平台supervisor持续effect fencing能力。
无法满足 strict 物理边界的平台不得通过“本地快照+进程锁”获得同等声明。
ROLLOUT_S 必须在上述骨架有精确记录且获批后关闭；PRECANARY_DONE 必须有真实多进程证据。
当前所有门均 pending，草案不对现有 standalone 工作树给生产许可。
