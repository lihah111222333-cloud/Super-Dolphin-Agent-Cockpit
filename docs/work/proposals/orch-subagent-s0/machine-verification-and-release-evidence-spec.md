# 机器验证与发布证据子规格

Spec-ID: VERIFY

Status: Draft

Depends-On: none
S0-Owner: trusted verification/evidence owner with independent assignment and decision evaluators
S0-Scope: planning DAG, candidate/epoch SSOT, artifact evidence, measurements, ancestor closure, and stage gates
S0-Dependencies: none
S0-Inputs: DAG/registry, artifact bundle, evidence heads, measurement heads, eligibility snapshot, and admission window
S0-Outputs: non-authorizing validation projection, attestation decision, negative proof, and stage evidence
S0-Failure: missing/tampered/unknown artifact, evidence, measurement, predecessor, or head fails closed and cannot become GO
S0-Recovery: query the original run/operation and current heads; re-admit only with a fenced successor, never by changing scope
S0-Field-Chain: requirements -> candidate/epoch/family -> artifact/evidence/measurement heads -> admission window -> evaluator -> decision
S0-Named-Acceptance: VERIFY-P01 PlanningSchemaRoundTrip; VERIFY-N01 MissingOrTamperedEvidenceFailsClosed

日期：2026-09-01。所属：[Gate S0 工作包](../../plans/orch-subagent-upgrade/README.md)。

## 1. 决策与范围

本规格定义“什么证据允许什么结论”的唯一计算边界，不拥有 provider 执行器。
用户批准推进 S0，不等于本规格已获内容批准。
总规格依据：§1、§5.2、§17～§19、§21。完整原文见[统一总规格](../../../superpowers/specs/2026-07-11-unified-native-subagent-control-plane-design.md)。
普通编译、单包测试、CLI 能连接、模型回答一次均不能被投影为整体 GO。

本轮实现范围仅为离线规划清单校验器：严格 JSON、依赖 DAG、动态 blocker/spec 集合、字段/边完整性及派生物漂移。
运行 attestation、证据签名、批准记录、GA evaluator 和 runtime permit 不在本轮实现内。

## 2. Owner 与阶段拆分

| 对象 | 唯一写入 owner | 消费端 |
| --- | --- | --- |
| 规划节点、typed edge/provider join | 本工作包 DAG 清单 | 离线 validator、派生图 |
| 历史 blocker requirement | blocker registry | 需求/状态派生表 |
| 子规格批准与精确 revision | 后续独立审批记录 owner，尚待裁决 | ancestor evaluator |
| 运行证据与不可变 attestation | 受信验证运行器，不能是被测进程自报 | verification decision |
| namespace/lineage/build 负证据 | 各 scope 的证据 head owner | stage/GA evaluator |
| 当前进程 measurement | 独立 trusted measurement owner | active-process、enable/current permit |

VERIFY_S 是本规格的精确 schema/owner/runner 输入骨架获批，不是整套验证实现通过。
VERIFY_DONE 必须等待适用领域规格和对应真实运行证据；不能反向成为 TRUST/ID 的前置，形成审批循环。
DOMAIN_DONE 的 umbrella 全量 join 与单 provider scoped enable 必须分开计算。

## 3. 本轮规划输入与派生输出

输入为 `release-gate-dag.json` 与 `current-blocker-registry.json`，两者均声明 `authority=planning_only`。
节点记录 id/title/kind/document/status，边记录 from/to/kind/providers；blocker 保存历史基线、来源要求、关闭要求和负责规格。
所有键必填；禁止未知/重复键、null、错类型、空身份、缺文件、目录逃逸、自环、环、漏依赖和未知引用。
spec 与 blocker 编号集合必须从总规格动态枚举，再对照 JSON，禁止依靠人工维护计数证明完整。
子规格 `Depends-On` 与 gate 合同逐节点校验输入边；provider-enable join 不能被普通 prerequisite 或另一 provider 代替。
派生物完整携带两份输入的确定性内容、摘要与计数；不省略已登记字段。

VERIFY 的后续 generated evidence schema（本轮仅规划，不是签名或发布授权）固定 `candidate_id`, `rollout_family`, `epoch`, `dag_digest`, `target_node`, `ancestor_closure_digest`, `artifact_root`, `evidence_head`, `measurement_head`, `negative_head`, `eligibility_unit_snapshot`, `assignment_snapshot`, `exposure_snapshot`, `timing_window`, `predecessor`, `admission_window`, `scope`, `result` 与 `decision_digest`。candidate/epoch/rollout-family 由独立 SSOT owner 写入；evaluator 只能消费并 CAS 校验，不可自报 cohort、分母或 head。

三类 head 必须逐字段绑定同一 candidate/epoch/scope：artifact/evidence head（文件与 attestation roots）、measurement head（process-start/runtime measurement）和 negative head（namespace/lineage/build negative proof）。`admission_window` 固定 `window_id`, `start_security_time`, `cutoff_security_time`, `required_sources`, `expected_root/count`, `observed_root/count`, `predecessor_window_id`；window 前驱、scope 或 head 任一不匹配即 `indeterminate`。具名正例 `VERIFY-P01`～`VERIFY-P05` 必须同时满足 pass 条件（所有 required fields、签名/摘要重算、ancestor closure、current head 与 exact exposure）；具名负例 `VERIFY-N01`～`VERIFY-N05` 必须证明 fail 条件（缺失/篡改/跨 scope/回滚/重复或 unknown 时 zero decision/permit side effect）。

规划 digest 使用排序键、固定分隔符的 UTF-8 JSON 后 SHA-256；这是本工具确定性编码，不声明 RFC JCS。
生产 release envelope 的 JCS、签名、key rotation、trust root 与 anti-rollback 是后续独立合同，不得复用未签名规划 digest。

## 4. 完整验证流水线合同

生产流向固定为 requirements -> hermetic run/artifact bundle -> immutable attestation -> verification decision。
decision 不反向写 requirements、输入 artifact 或自己的前驱证据。
所有 requirements/decision 绑定 `dag_digest,target_node,ancestor_closure_digest`；适用 provider、workspace、namespace、lineage 不得缺失或跨域复用。
artifact global roots 与 runtime closure partition 要精确绑定；相同文件内容不等于相同安装、进程或执行身份。
资格单元使用稳定 eligibility unit、单链 snapshot、logical runtime subject 与实际 process-start identity。
stage/GA 的不可变 decision 不嵌可过期的短 TTL measurement；实际许可必须重新比较 current measurement/active-process head。

build evidence、namespace negative、lineage negative 分开持久，换 build/epoch/new lineage 不能清掉适用负证据。
negative 的 scope 和适用判断由唯一 evaluator 计算，不允许被测任务选择忽略某项失败。
缺 artifact、缺证据、未知维度、签名不明、祖先未闭合或 current head 变化均 fail-closed。
本地 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 证据仍可用于开发回归，但不能当发布 attestation。

## 5. Stage 与 GA 输入不变量

- 5%、25%、100%、GA 单向推进；每个 stage 必须引用相同 candidate/build/eligibility epoch/cohort commit 的 canonical 全前驱集合。
- stage admission 先固定 plan，再取得全部 pre-T0 source snapshot receipt，再取得严格后继的 security-time start allocation；不能伪装成跨 owner 单数据库事务。
- admission compare group 一次性冻结完整 initial expectation member/root/count、required source scopes、exclusive start 集合和 pending window。
- cutoff 前仅 all-open expectation + pending window + 当前 security time 小于 cutoff 可用于阶段许可；不得留 TTL 尾窗。
- cutoff 后 observed/expected leaf root/count 必须逐字相等，missing/duplicate/replay/gap 必须全部为 canonical empty/zero。
- 只有 attested terminal + 每个完整 source-owner-generation scope 的 window complete 才能进入下一阶段；negative 只能终止/回滚。
- GA 必须携带同一 epoch 的 [5,25,100] 集合与 100% 完整后观测窗口，不能只引用最近一阶段。
- 阶段时长、最低 distinct exposure 和 required stratum 服从总规格固定值，不能为使本地小样本通过而降低。
- planning validator 不实现上述 production evaluator；图里存在 stage 节点不等于节点具有运行权限。

## 6. 字段消费与守卫

规划链：dataclass/schema 动态字段 -> 严格 decoder -> 图/文档/引用 validator -> canonical JSON/hash -> 派生 Markdown -> 只读 drift check。
关键行为字段还要单独语义负测，不能以“完整 JSON 附录包含字段”替代 from/to、owner、provider、status 的实际消费证明。
后续 production 链：owner schema -> DTO -> writer -> artifact/attestation -> evaluator -> enable/current permit -> effect consume。
每一方向需要动态差集、one-hot round-trip、unknown/missing/stale consumer 负测；render/hash 显示完整不等于运行时字段守卫完成。

### 6.1 Planning-only evidence artifact（候选 schema）

为避免把 cross-task 规划表、单实例锁记录或本地 SQLite 提交误读成真实
attestation，本工作包只登记一个未来生成器可消费的 artifact 形状；本轮不
生成、不签名、不写入 runtime，也不把它作为 gate 输入。顶层固定为：

```json
{
  "schema_version": 1,
  "authority": "planning_only",
  "artifact_id": "VERIFY_PLAN_20260903",
  "source_spec": "docs/work/proposals/orch-subagent-s0/machine-verification-and-release-evidence-spec.md",
  "graph_ref": "docs/work/plans/orch-subagent-upgrade/release-gate-dag.json",
  "registry_ref": "docs/work/plans/orch-subagent-upgrade/current-blocker-registry.json",
  "graph_digest": "<64 lowercase hex>",
  "registry_digest": "<64 lowercase hex>",
  "status": "needs_revalidation",
  "decision": "NO-GO",
  "cross_task_receipts": [],
  "singleton_receipts": [],
  "sqlite_receipts": [],
  "field_consumers": [],
  "negative_proofs": []
}
```

上例中的 digest 仅表示字段形状，不能原样作为输入；生成器必须从
`graph_ref`/`registry_ref` 的当前字节计算真实值，无法重算时立即失败。

数组记录是“待证明的 receipt 形状”，不是 receipt 本身。每条记录只能有
规划状态 `planned|not_verified|needs_revalidation`，顶层 `decision` 只能为
`NO-GO`；禁止 `approved`、`available`、`accepted`、`completed`、`GO` 或
任何 enable/permit 字段。所有对象都要求精确键集合、无重复键、无 null、无
未知字段和正确的判别联合；标识符必须稳定且唯一，digest 必须是小写 64 位
hex。`graph_ref`/`registry_ref` 与所有 evidence/guard 引用都必须是 root 内的普通文件。
`graph_digest`/`registry_digest` 使用本工具排序键紧凑 JSON 的 SHA-256，
明确不是 JCS、签名或生产 trust root。

五个 registry 的最小字段和消费边界如下：

| Registry | 最小 required fields | 规划期 producer -> consumer |
| --- | --- | --- |
| `cross_task_receipts` | `requirement_id`, `scope`, `candidate_id`, `epoch`, `operation_id`, `request_digest`, `receipt_kind`, `evidence_ref`, `evidence_digest`, `status` | cross-task owner -> VERIFY evaluator；不能由 Room aggregate 或 legacy `send_message` 填充 |
| `singleton_receipts` | `singleton_id`, `security_domain`, `owner_generation`, `operation_id`, `predecessor_generation`, `state`, `receipt_kind`, `evidence_ref`, `evidence_digest`, `status` | singleton owner -> head/decision evaluator；进程锁只能是 planned evidence |
| `sqlite_receipts` | `authority_id`, `deployment_id`, `database_scope`, `schema_revision`, `writer_epoch`, `operation_id`, `request_digest`, `previous_head_digest`, `receipt_digest`, `status` | 外部 RecoveryCustody owner -> restore/current-head evaluator；产品 SQLite/WAL 不能代替它 |
| `field_consumers` | `field_id`, `producer`, `consumers`, `required_states`, `guard_ref`, `status` | schema owner -> DTO/writer/evaluator guard；每个 consumer 必须可定位 |
| `negative_proofs` | `proof_id`, `scope`, `trigger`, `expected_state`, `zero_effect_counter`, `evidence_ref`, `evidence_digest`, `status` | scope evaluator -> stage/GA evaluator；negative 不得被 candidate 选择忽略 |

`evidence_ref`/`guard_ref` 必须是 workspace-relative、解析后仍在 root 内的普通
文件，禁止绝对路径、目录、越界 symlink、自引用 graph/registry/generated map；
`evidence_digest` 绑定引用文件的实际 UTF-8 字节，路径或内容改变就必须重算。
`receipt_kind` 与 `state` 只能使用已登记的规划枚举；未知值、空值或把生产终态
（如 `available`、`accepted`、`completed`）写入 receipt 都必须拒绝。
`scope` 至少含 owner/security-domain、candidate/epoch、namespace/lineage/build
中适用维度；跨 scope、跨 deployment、generation 回退、重复 operation 或
digest 不一致一律 `indeterminate`/`NO-GO`，不能由文字说明修补。SQLite 记录
还必须逐字绑定 `schema_revision`、`writer_epoch`、`previous_head_digest`，并
明确本地 commit 仅能说明 candidate/pending，不是外部 custody terminal。

该 schema 的唯一用途是让未来 validator 做路径、digest、字段消费和负证据差集
检查；artifact 被渲染或被测试 fixture 读取，不产生授权副作用，也不改变当前
`release-gate-dag.json`、blocker registry 或任何 runtime composition。

## 7. 验收与退出条件

当前具名目标测试：

- `test_rejects_duplicate_json_keys`、`test_rejects_unknown_missing_and_null_fields`：真实输入严格解析。
- `test_rejects_missing_dependency`、`test_rejects_cycles`、`test_rejects_provider_join_drift`：拒绝图漂移。
- `test_spec_and_blocker_sets_follow_source`：从源清单动态发现集合缺口。
- `test_render_is_deterministic`、`test_check_rejects_generated_drift`：确定性与无静默刷新。
- 字段 fail-first 必须临时删除真实 JSON 字段或依赖边，实际命令返回非零；恢复后同命令通过。
- 未来专属 validator 必须覆盖 `VERIFY-P06 PlanningEvidenceArtifactRoundTrip`、
  `VERIFY-N06 PlanningArtifactUnknownOrMissingFieldFailsClosed`、
  `VERIFY-N07 PlanningArtifactPathDigestStatusTamperFailsClosed`、
  `VERIFY-N08 PlanningArtifactCannotAuthorize` 和
  `VERIFY-N09 PlanningArtifactCrossScopeSQLiteReplayFailsClosed`；这些是规划
  测试索引，当前没有对应 GREEN 或 runtime receipt。

生产验收还需 artifact tamper/自引用、跨域 evidence replay、source completeness、current-head rotation、missing exposure、negative scope reset 全矩阵；本轮无 GREEN 声明。
VERIFY_S 关闭条件是精确审批与证据模型获批且 validator 本体通过；VERIFY_DONE/GA 需要独立真实执行证明。

## 8. 必须裁决的开放项

1. 子规格审批记录如何绑定用户批准、文档 digest、被替代 revision 和审批撤销。
2. trusted measurement/evidence 签发者的独立部署、信任根和权限分离。
3. 机器验收 requirements 与 field-consumer/negative-proof/metric registries 的最终 schema。
4. 当前离线规划清单如何单向迁入正式批准模型；不得在原模型里加入无证据的 `approved=true`。
