# B09 legacy wakeup payload migration candidate

Status: Candidate / planning-only / not wired to production

本候选是一次性、显式的 `task_dag_wakeups.prompt_payload` 迁移规划器，位于
`cmd/mcp-orch/store/taskdag/legacywakeupmigration`。它不接入 Fx、provider、
RecoveryCustody、dispatcher，也不包含 PostgreSQL 适配器；生产启动路径不会自动调用它。

## 边界与决策

`BuildPlan` 接收 transport-neutral 的行投影，先按 `id` 排序，再只依据数据库
`source_payload_schema_version`、`wakeup_kind` 和行上的 DAG 身份选择分支：source
version `0` 只允许 legacy payload，source version `1` 只允许严格 canonical payload；
`start` 才能产生 `legacy_launch`，
`node_start`/`manual_dispatch` 才能产生 `dag_downstream`。历史 JSON 允许明确列出的
Go/camelCase 别名（例如 `AgentID`、`agentId`、`Cwd`），重复别名、未知键、跨分支
字段、类型/null/重复 JSON 键、损坏 JSON、超限字段均进入 typed quarantine；绝不按
payload 形状猜测，也不填充默认 target。

目标 agent 必须非空、无首尾空白且与 payload `agent_id` 完全一致。DAG 分支还要求正的 `run_id`
以及非空 `dag_key`/`node_key`；launch 分支携带任何 DAG 身份则 quarantine。已有且
严格有效的 versioned payload 是 `unchanged`，但仍验证分支、target 和 DAG 身份；
versioned 无效行同样 quarantine。

## 应用、CAS 与重复运行

计划中的 `Mutation` 携带 `row_id`、按原始 payload 每次重算的源 payload digest（不信任行上外部 digest）、`updated_at` 期望值、显式
`new_payload_schema_version=1`，以及稳定的 `legacy-wakeup-migration:v1:<row_id>:<digest>`
幂等键。`Applier` 实现必须在
一个 SQLite transaction 内：

1. 以 `row_id AND updated_at AND source_digest` 做 CAS；零行必须报告 stale/CAS conflict。
2. 仅按 Mutation 携带的显式 version=1 写入 canonical payload/kind，并以唯一幂等键记录该次迁移；禁止从 payload 形状隐式猜 version。
3. 对 quarantine 以同一事务标记源行 failed/quarantined 并保存 `QuarantineRecord`。

`Plan.Apply` 只执行已审核的计划，首个错误即停止，不重试、不调用 launcher、不静默
降级。迁移后再次扫描会看到严格有效的 versioned 行并产生 `unchanged`；相同输入的
`RunDigest` 稳定且绑定完整 source identity/kind/target/run/updated/CAS 以及 mutation
的 new kind/new payload digest/version，可作为 operator review/approval 边界。
quarantine 记录携带 source payload version；当前 migration 134 的 legacy-only
quarantine applier 仅接受 source version `0`，其它版本只可停留在审核计划中，不能
写入不兼容的表。应用前还会校验原始 payload digest 与
`legacy-wakeup-migration:v1:<row_id>:<digest>` 稳定幂等键的绑定，重复运行不会产生无法关联的副作用。

## 操作员审核边界

候选只输出 `migrate`、`quarantine`、`unchanged` 三类动作。上线前必须由操作员审核
`RunDigest`、每条 mutation 的 target/digest/CAS 值及 quarantine 原因；审核结果由
外层 SQLite applier 持久化。本包没有自动发现、批量启动、Fx/provider wiring 或远程
数据库能力，因此不能被误当作生产 dispatcher 或 RecoveryCustody owner。
