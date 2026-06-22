# Workflow Runtime 状态契约

本文冻结当前 Workflow Runtime Kernel 的状态命名、持久化边界和展示派生规则。当前 runtime owner 是 `cmd/mcp-orch`。

## 当前存储映射

| 领域对象 | 当前映射 | 约束 |
| --- | --- | --- |
| Workflow Definition | `task_dags` | 模板草稿不直接等同可运行定义。 |
| Workflow Version | `task_dags.version` | `task_start_dag` 创建 run 时快照。 |
| Workflow Run | `task_dag_runs` | status 受 DB CHECK 约束。 |
| Workflow Node Run | `task_dag_nodes.run_id != NULL` | runtime node snapshot，不回退读模板节点。 |
| Wakeup | `task_dag_wakeups` | 没有 assignee 的 root 不入队。 |
| Lease | wakeup claim 字段、worker lease | 当前语义是 dispatch ownership，长 Agent ownership 后续收敛。 |

## 持久 Node 状态

`task_dag_nodes.status` 的当前可识别集合如下。`skipped` 与 `waiting_human` 只保留 legacy/预留语义，runtime 闭环前不得由新用户入口创建。

| 状态 | 含义 | 公开策略 |
| --- | --- | --- |
| `pending` | 等待上游完成 | 可公开、可持久 |
| `ready` | 依赖已满足，等待调度或等待 assignee | 可公开、可持久 |
| `running` | executor 已接管 | 可公开、可持久 |
| `retrying` | 等待重试 | 可公开、可持久 |
| `done` | 成功终态 | 可公开、可持久 |
| `failed` | 失败终态 | 可公开、可持久 |
| `cancelled` | run 终止导致取消 | 可公开、可持久 |
| `skipped` | legacy/保留跳过终态 | 可读展示，不对外新建 |
| `waiting_human` | legacy/保留 HITL 暂停 | 可读展示，不对外新建 |

`awaiting_verify` 是历史输出物化/回放路径中的 legacy 状态，不属于新的 canonical node lifecycle。新创建、更新和展示契约不得把它当作可执行闭环能力。

## 派生 Node 展示状态

以下状态不得写入 `task_dag_nodes.status`：

| 展示状态 | 来源 | 含义 |
| --- | --- | --- |
| `waiting_for_assignee` | `status == ready && assigned_to == ""`，或 `StartDAG.execution_state` | 依赖满足但缺执行者 |
| `waiting_timer` | wakeup `due_at` 在未来 | 等待定时触发 |
| `blocked_by_policy` | policy decision | 权限或风险策略阻塞 |
| `awaiting_review` | ReviewGate | 等待质量审查 |
| `awaiting_acceptance` | AcceptanceRecord | 等待验收 |

## 持久 Run 状态

`task_dag_runs.status` 必须对齐 DB CHECK：

| 状态 | 含义 |
| --- | --- |
| `running` | run 已创建，仍存在未完成节点、等待调度、等待分配或执行中 |
| `succeeded` | run 成功终止 |
| `failed` | 失败策略导致 run 失败 |
| `cancelled` | 用户或系统终止 |

## 派生 Run 展示状态

以下状态由 node、wakeup、policy、review 或 artifact 聚合得到，不写入 `task_dag_runs.status`：

| 展示状态 | 来源 |
| --- | --- |
| `waiting_for_assignee` | 存在 `ready + assigned_to == ""` 节点 |
| `waiting_timer` | 只剩未来 wakeup |
| `running_active` | 存在 `running` 节点 |
| `recoverable_failed` | `failed` run 存在恢复动作 |

## Node Transition Rules

当前 canonical transition matrix：

| From | To | 触发 |
| --- | --- | --- |
| `pending` | `ready` | 上游依赖全部满足 |
| `ready` | `running` | dispatcher 成功领取并开始执行 |
| `running` | `done` | executor/subscriber 完成 |
| `running` | `failed` | 不可重试失败 |
| `running` | `retrying` | 可重试失败且预算未耗尽 |
| `retrying` | `ready` | retry wakeup 到期 |
| `retrying` | `failed` | 重试预算耗尽 |
| `pending` | `cancelled` | run 被终止 |
| `ready` | `cancelled` | run 被终止 |
| `running` | `cancelled` | run 被终止 |
| `retrying` | `cancelled` | run 被终止 |

禁止迁移：

- `done -> *`
- `failed -> *`
- `cancelled -> *`
- `ready -> done`
- `pending -> running`
- `ready + assigned_to == "" -> running`
- `waiting_for_assignee -> *`，因为它不是持久状态
- `hybrid`、HITL、审批类状态在 runtime 闭环前不得创建为可执行节点或可执行状态

## Hybrid/HITL 边界

`hybrid` node type、`waiting_human`、`skipped`、`awaiting_verify` 当前不能作为新的用户可创建可执行能力。允许的行为只有：

- 读取和展示历史数据。
- 诊断历史 DAG 中的配置缺口。
- 在创建或修改入口 fail-fast。

不允许的行为：

- schema/UI 把 `hybrid` 展示为可新建可运行节点。
- `task_create_dag` 创建 `node_type=hybrid`。
- `task_dag_apply_ops` 新增 `node_type=hybrid`。
- 把 `waiting_for_assignee`、`awaiting_verify`、`waiting_human` 写成新的持久 node status。
