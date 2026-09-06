# RESOURCE-1 reserved-only 持久化骨架

Status: Active

日期：2026-09-02；上级：[生产决策门](content-resource-custody-decision-gate.md)与[RESOURCE完整草案](../../proposals/orch-subagent-s0/durable-resource-admission-and-budget-spec.md)。

本记录只描述已批准RESOURCE方向的第一持久化切片。它不实现claim、effect permit、provider调用、usage writer、settlement、release、emergency reserve或Room per-target admission，也不把RESOURCE完整规格标成完成。

## 已实现范围

migration133新增三张`STRICT`表：

1. `orchestration_resource_effective_policy_snapshots`：版本化effective policy、applicability/source-set/precedence证据、完整十维limit、owner/writer/deployment/security-time fence、publish operation/receipt；
2. `orchestration_resource_reservations`：只允许`reservation_version=1`、`resource_status=reserved`、`requested_kind=send_message`，绑定command/profile、双端ID-1 current binding、CONTENT ref/object proof、当前policy和完整十维requested vector；
3. `orchestration_resource_usage_ledger`：预留未来telemetry schema，但schema133的INSERT/UPDATE/DELETE均由trigger物理拒绝。

关闭的十维及单位直接编码在typed字段/列名中：

```text
agent/count
in_flight/count
fanout/count
concurrency/count
wall_time/ms
input_tokens/token
output_tokens/token
cost/microunit
content_bytes/byte
ingress_bytes/byte
```

policy limit和reservation requested十维全部必须`>0`。本切片不以0表示zero cap；缺policy或缺任一维就是deny。hard cap的具体数值没有默认值，也未写入production配置。

## Owner与事务

`cmd/mcp-orch/store/resourceadmission`只暴露：

- `PublishEffectivePolicy`
- `Reserve`
- `Get`
- `ResolveReservedSnapshot`

不存在`AppendUsage`、claim、permit、effect或release方法。

`PublishEffectivePolicy`在`BEGIN IMMEDIATE`中按幂等key裁决winner，要求revision连续、selector/applicability不漂移、完整PolicyOwnerFence恒等，且新revision的effective time不得早于前驱。policy request、operation、canonical policy、limits、snapshot和receipt均使用独立domain digest。

`Reserve`在唯一INSERT前依次重读并验证：

```text
command + send_message_v1 profile
  -> source/target latest active full ID-1 binding
  -> held command CONTENT fence
  -> live ref + active/key-active object + promoted ingest
  -> created-at时最高eligible effective policy
  -> requested十维逐一<=policy limit
  -> reserved snapshot INSERT
```

SQLite trigger再次完成同一relation闭包。缺policy、profile/ID/CONTENT漂移、过期proof、旧policy或任一维超限均在首个reservation写入前失败。相同key/exact request返回首个candidate ID/time；异request conflict。

## Usage与release边界

reservation固定：

```text
usage_digest_domain = orch.resource.usage-ledger/v1
usage_watermark     = 0
usage_digest        = SHA-256("orch.resource.usage-ledger/v1\nempty\n")
                    = c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31
```

任何usage row都会使当前binary的reserved snapshot读取fail-closed。`release_condition_digest`由effective policy显式提供并复制到reservation，只是non-authorizing proof metadata；当前没有release状态或方法。

现有DAG budget、prompt token budget、tool-result裁剪、provider/UI token/cost counters均未被复用为RESOURCE authority。它们缺reservation/attempt/source-sequence/receipt和crash-safe ledger语义。

## 已验证

- migration133：132→133 additive零回填、重复幂等、三表列/PK/FK/index/trigger、十维positive/overlimit、完整ID/CONTENT/current policy关系、backdated revision拒绝、immutability和usage三写禁用；
- schema gate：三表142列与9个trigger动态双向登记，缺表/列/trigger、BLOB和敏感payload/credential/secret列均阻断；合法token metadata不误报；
- policy store：完整vector/digest、每维zero拒绝、canonical replay/conflict、revision gap/owner/time fence；
- reservation store：missing policy零reservation/usage、十维逐一overlimit、candidate ID归一、profile/双binding/CONTENT关系、关闭重开；
- proof-grade resolve：policy、binding或CONTENT漂移后fail-closed，历史`Get`仍只用于审计/reconcile；
- 字段/架构守卫：policy和reservation的request→snapshot→sqlc params→SQLite row→snapshot双向映射，十维digest one-hot，禁止provider import及usage/claim/permit/release写面。

本机精确 `resourceadmission` 包通过（1.146s），`-count=10` 通过（10.597s）；
`internal/platform/db`、`internal/platform/db/sqlite` 整包、定向 vet 和 mcp-orch build
通过。统一 `--light-guard-only` 的历史结果不能作为当前 PASS：官方 wrapper 当前在
production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段；host-light 业务当时也曾因宿主 CPU/未暂存候选在 admission 前
退出 2。全部结果仍是 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。

## 明确未实现

- 多scope policy source的真实生成器、precedence intersection authority与发布制品；当前只持久化typed effective snapshot和外部source/trace摘要；
- aggregate active/in-flight/fanout/concurrency的跨reservation计数；本切片只校验单reservation requested vector不超过policy；
- provider token/cost telemetry来源、采样、舍入、overage、measurement receipt与usage digest chain；
- claim-time policy tighten、attempt/effect permit/nonce、dispatch-started/outcome-unknown；
- exhausted/quarantined/emergency stop、terminal settlement、release/reconcile；
- RESOURCE到`admissionproof.ResourceSnapshot`的production adapter、Fx/tool/runtime接线；
- launcher/provider/Room真实call-count和跨进程/发布/回滚证据。

因此RESOURCE-1只是non-authorizing reserved proof骨架。它不能启用provider调用、cross-task send或Room；RESOURCE完整规格继续保持Draft，总计划继续保持Active。
