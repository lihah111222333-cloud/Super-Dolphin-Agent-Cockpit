# ADR-009：DAG node ↔ child thread 双向追溯（spawning_thread_id）

> 状态：✅ Accepted | 日期：2026-05-11（Proposed）→ 2026-05-12（Accepted，F1.5 落地 + DTO 透出 + archtest 守护） | 决策者：项目维护者 | 相关：T0.7 / PD-2（骨架审查 finding）、F1.5（本 ADR 实装位）、T6.1 / T8.1（UI 节点行 → 子 agent thread 跳转）、ADR 0001 §2 三层架构

## 1. 背景

三层架构定位：
- **上层**：harness（orchestration agent / thread）
- **中层**：DAG（编排层，task_dag_nodes / task_dag_runs）
- **下层**：child agent（AgentExecutor spawn 出的子 agent thread）

骨架阶段二次审查 finding **PD-2** 指出：DAG 编排层 spawn 出 child agent thread 后，DB 没有硬关联回 DAG node。当前路径只有 `node.result` jsonb 里**可能**含 thread_id 字符串（软关联），导致：

1. **UI 看不到下层在跑什么**：T6.1「UI 节点行 → 子 agent thread 跳转」无法稳定拼链接，只能字符串解析 result；
2. **AI 设计师按钮无回路**：T8.1 spawn 出新 thread 后，thread → DAG → 节点的反向回溯靠 thread metadata 杂物；
3. **重试场景丢历史**：F1.4 重试失败节点会 spawn 新 thread，旧 thread_id 没地方放，只能往 `run.events` 塞。

T0.7 原推迟到 T8.1「AI 设计师按钮一并做」，**这是反的**——T8.1 是消费方，应该先有字段位才能让 UI 接通。本 ADR 把 T0.7 前置到 F 阶段，立 **F1.5** 字段位 + 写入逻辑。

## 2. 候选方案

### 方案 A：`task_dag_nodes.spawning_thread_id TEXT NULL`（推荐）

加一列直存最近一次 spawn 的 child thread_id。

- 优点：查询零跳转（`task_get_dag` 节点行直接带）；migration 简单；重试覆盖语义清晰
- 缺点：历史 thread_id 丢失（只剩最新一个）

历史 thread 走 `task_dag_runs.events` jsonb 追加（已有字段），形如：
```json
{"kind": "node_spawn", "node_key": "N2", "thread_id": "agent-xxx", "ts": "..."}
```

### 方案 B：新建关联表 `task_dag_node_threads`

```sql
CREATE TABLE task_dag_node_threads (
  node_id BIGINT,
  thread_id TEXT,
  spawned_at TIMESTAMPTZ,
  PRIMARY KEY (node_id, thread_id)
);
```

- 优点：1:N 完整历史；可查每次重试 spawn 出的 thread
- 缺点：多一张表 + 多一次 JOIN；当前没用例真要查完整历史链

### 方案 C：只用 `node.result` jsonb 字段（现状）

- 优点：零改动
- 缺点：软关联 / 解析不稳 / 与 ADR-006 size_cap 冲突（result jsonb 受 4KB 限）

## 3. 决策

**选方案 A**：

1. **migration 0083**：`ALTER TABLE task_dag_nodes ADD COLUMN spawning_thread_id TEXT NULL` + index（按 thread_id 反查节点）
2. **写入时机**：`nodeexec/executor_agent.go` 在 `orchestration_launch_agent` 成功返回后立即 `UPDATE ... SET spawning_thread_id = $1 WHERE id = $2`
3. **重试覆盖语义**：重试 spawn 新 thread 时直接覆盖（保留最近一次）；旧 thread_id 追加到 `task_dag_runs.events` 形成历史
4. **读取入口**：
   - `task_get_dag`：节点行响应增加 `spawning_thread_id` 字段
   - `task_get_run`：节点级响应增加同字段
   - T6.1 / T8.1 UI 直接取该字段拼链接，**不解析 result jsonb**

## 4. 触发条件

本 ADR 必须在 **F1.5 开工前**拍板（F1.5 = spawning_thread_id 字段位 + spawn 时写入）。

T6.1 / T8.1 UI 任务允许在字段位就位后开始（不必等 F1.5 全部 done）。

## 5. Open Questions

- **Q1**：thread_id 是否走外键？`agent_threads` 表由 thread 子系统维护（migration 0012），与DAG 编排层处于不同语义域。跨域外键会放大编排层的耦合面（任何 thread 生命周期变动都需同步到 DAG store）。倾向 **不加外键**，只做软引用 + index。
- **Q2**：thread 被归档/删除后字段是否清空？倾向 **不主动清空**——历史 DAG 仍要能追溯曾经的 thread_id。UI 端取 `agent_threads.status` 判定（migration 0012 已具备 status 字段），status 非 running 显示「已归档」。
- **Q3**：与 ADR-006 `to_node_result.size_cap` 是否冲突？**本ADR 独立列与 ADR-006 size_cap 不冲突**（spawning_thread_id 走 task_dag_nodes 独立列，不进 result jsonb）。但另见 Q4——重试历史落 `task_dag_runs.events` 可能独立吃掉另一个阈值。
- **Q4**（审查补）：`task_dag_runs.events` jsonb **未加 size_cap CHECK**（grep events 无约束）。F12.1 智能重试「关键节点 replan」会 spawn 多 planner thread，每次 append `{kind, node_key, thread_id, ts}` 约 80~150B。2026-05-11 返修审查推算：M3 用例「≥10 节点 + replan 触发」场景可能在 ADR-006 size_cap 之前先把 events 列吻爆。倾向：F1.5 实装对 `events` 列 `node_spawn` 类目设**独立环形 cap**（只保最近 N 条 node_spawn），**与 ADR-006 的 4KB result cap 不共用**。具体阈值（N=20？50？）待 F1.5 实装时定；M3 实测后迭代。

## 6. 实装登记

| 项 | 状态 | 位置 |
|---|---|---|
| migration 0083 | ⏳ 待 F1.5 | `migrations/0083_dag_v2_spawning_thread_id.sql` |
| AgentExecutor 写入 | ⏳ 待 F1.5 | `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go` |
| task_get_dag 返回字段 | ⏳ 待 F1.5 | `internal/sidecar/orch/orchestration/dag_query.go` |
| task_get_run 返回字段 | ⏳ 待 F1.5 | 同上 |
| events node_spawn 环形 cap（Q4） | ⏳ 待 F1.5 | `internal/sidecar/orch/store/taskdag/` events append 路径 |
| UI T6.1 消费 | ⏳ 待 T6.1 | `components/DagDetailModal.js` |
| UI T8.1 消费 | ⏳ 待 T8.1 | `pages/DagsPage.js` |
