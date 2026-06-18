# ADR-018：agent 节点真实输出物化（A2）

> 状态：✅ Accepted | 日期：2026-05-13 | 决策者：Codex A2 implementation + reviewer 复核
> 实装：`3e70e468`（feat(orch): materialize agent outputs on turn completion）+ review-fix `02009e22`（fix(dag): fence output materialization races）
>
> 相关：ADR-015（provider `TurnCompleted.Result` 补完）/ ADR-016（spawned agent stop）/ ADR-017（DAG turn.completed subscriber）/ ADR-006（`node.result` 4KB cap）/ C-A 实施计划 §3.2。

## 1. 背景

A1 已负责 DAG agent 节点 lifecycle：订阅 `TurnCompleted`，反查 `spawning_thread_id`，推进节点 done/failed，并释放 spawned agent。A2 只补齐另一半：把 agent first turn 的真实产出物化到 DAG 节点输出面，让下游 `inputs.from_nodes` 读到的是 agent 回复，而不是 launch metadata。

旧 F1.3 的问题是 launch 成功时同步写了 `{thread_id, agent_key}` 等 metadata 到 `sharedfile` / `node.result`，这不是 agent 真实产出。ADR-015 已把真实回复放进 `ev.Result`；ADR-017 已把 `TurnCompleted` 接到 DAG lifecycle；A2 应在这个事件点落输出，不再在 launch 点伪造 outputs。

## 2. 决策

### 2.1 分工边界

- **A1**：只负责 lifecycle/status/stop。它推进 done/failed、schedule downstream、调用 stop helper；不拥有 outputs 物化策略。
- **A2**：负责 agent 节点真实输出物化。输入来自 `TurnCompleted.Result`，配置来自 node `config.outputs`。
- 不做 agent fast-path / self `task_update_node`；不把外部 webhook / command card 作为 agent outputs 路径；automation outputs 不纳入本 ADR。

### 2.2 SQL / sqlc 范围

A2 不新增通用 `MergeTaskDagNodeResult`，也不新增 migration / schema column。

原始落地复用现有 `CompleteNodeAndScheduleDownstream` 的 `result=jsonb` 更新路径：subscriber 在完成节点时一次性传入按 outputs 规则构造好的 result payload。

review-fix 后，sharedfile 路径新增一条窄 SQL/sqlc fence：`ClaimTaskDagNodeOutputMaterialization`。原因是 sharedfile 写入是 DB 外部副作用，必须先通过 `ready/running/awaiting_verify` 状态 fence claim 节点，再写 sharedfile，避免迟到 duplicate `turn.completed` 在 DB complete 被拒后仍写外部文件。`awaiting_verify` 允许“sharedfile 已写、CompleteNode 临时失败”后的重放恢复。

### 2.3 launch 成功不写 outputs

`AgentExecutor` launch 成功只表达“spawned agent 已启动 / first turn 已提交”。它不再把 launch metadata 作为 outputs 写入 `sharedfile` 或 `node.result`。

允许保留 launch 追踪所需字段在已有 trace 字段中（例如 `spawning_thread_id`），但不得把 `{thread_id, agent_key}` 当作下游业务输出。

### 2.4 TurnCompleted 时物化

`TurnCompleted` subscriber 基于：

- `ev.Result`：ADR-015 提供的真实 agent 回复；
- node `config.outputs`：决定写 `node.result`、`sharedfile` 或两者；

构造最终输出：

- **无 `outputs` 配置**：兼容旧行为，默认写 `node.result`；
- **`outputs.to_node_result=true`**：写 `node.result`；
- **`outputs.to_sharedfile` 显式配置且 `to_node_result=false`**：真实输出写 sharedfile，`node.result` 只保留小的 sharedfile 引用 envelope（不重复写大 payload）；
- 两者可同时配置，此时同一份真实输出同时进入 `node.result` 与 sharedfile。

### 2.5 4KB cap 沿用 ADR-006

所有写入 `node.result` 的 agent 输出沿用 ADR-006 的 4KB cap。超限是 `validation` failure，不隐式 fallback 到 sharedfile，不 truncate，不写 overflow metadata。

如果用户需要大输出，必须显式配置 `outputs.to_sharedfile`，且不要同时要求 `to_node_result=true`。`to_sharedfile` 主路径不受 `node.result` 4KB cap 约束。

### 2.6 字段形态保持 bool

`outputs.to_node_result` 保持 `bool`，不升级为 `{enabled, size_cap_bytes}` 对象。cap 继续由代码常量统一管理，与 ADR-006 §5.2 一致。

### 2.7 明确不做

- 不做 automation outputs 改造；
- 不做历史 DAG / 已完成节点 backfill；
- 不新增通用 `MergeTaskDagNodeResult` SQL；
- 不新增 migration / schema column；
- 不做 agent fast-path / self `task_update_node`；
- 不把 launch metadata 写成 outputs；
- 不因 H12/H13 未完成而阻塞 A2。H12 claude long-result e2e 与 H13 A1 e2e/race 补测仍是非阻塞 follow-up。

## 3. 失败语义

| 场景 | 行为 |
|---|---|
| `ev.Success=false` | 节点按 A1 失败路径推进；A2 不写成功输出 |
| `ev.Result` 为空 | 按真实结果写空内容；是否视为业务失败由上游 agent / future validation 决定 |
| `to_node_result` 超 4KB | 走 fail-node，reason 带 `validation:` / ADR-006 语义；不 fallback |
| sharedfile writer 缺失 / 写失败 | 走 fail-node，reason 带 `infrastructure:` 语义 |
| 同时配置 node.result + sharedfile | 两边都写；任一路失败按 reason 前缀暴露 validation/infrastructure 语义 |
| duplicate / stale `turn.completed` | sharedfile 写入前先 claim；claim 被 terminal fence 拒绝时不写 sharedfile，不推进 CompleteNode |

> 当前 `FailNodeAndCancelDownstream` 只接收 `Reason`，没有结构化 `FailureClass` 字段；A2 不扩失败合同。新增的 store 面仅限 sharedfile materialization claim fence，不承担通用 result merge / backfill。

## 4. 验收

- launch 成功不再产生 sharedfile / `node.result` outputs；
- agent first turn completed 后，`node.result` 写入 `ev.Result` 真实内容；
- 显式 `to_sharedfile` 写入 sharedfile；sharedfile-only 时 `node.result` 只保留小引用 envelope；
- 同时配置 `to_node_result` + `to_sharedfile` 时两边都有真实内容；
- 默认无 outputs 配置时仍写 `node.result`；
- 4096 byte `node.result` 通过，4097 byte validation failure；
- 大输出显式 sharedfile 且未配置 `to_node_result` 时通过；
- 下游 `inputs.from_nodes` 读到上游真实 agent 输出，而不是 launch metadata。

## 5. 落地状态

本 ADR 已随 A2 implementation 落地并升 Accepted：

- 实装 commit：`3e70e468`；review-fix commit：`02009e22`；
- 代码验证确认 `CompleteNodeAndScheduleDownstream` 的 result 参数可以承载 A2 MVP，sharedfile 外部副作用需要额外 claim fence；
- reviewer 复核确认无 import cycle / fx 基础编译失败，sharedfile-only reference envelope、`awaiting_verify` replay 与文档一致；
- 后续若要扩展结构化 FailureClass、automation outputs 或历史 backfill，另立任务，不回填到本 A2 MVP。
