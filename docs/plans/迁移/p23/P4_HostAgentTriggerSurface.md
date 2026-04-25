# P23.4: 主 agent 触发面

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P3**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

MCP 工具 `task_create_dag` 接受新可选参数 `auto_start: true`（语法糖：创建后立即调用 `cmd/mcp-orch/orchestration/dag_start.go:StartDAG`）；DAG terminal 事件通过既有 hook consumer 链路回到主 agent；100% 向后兼容旧调用方。

## 现状校准（事实层）

- 主 agent 当前触发能力：`cmd/mcp-orch/tools/task_tools.go:64-70,94-105`（`task_create_dag` / `task_get_dag` / `task_update_node`）
- 主 agent 状态查询：`cmd/mcp-orch/tools/task_tools.go:74-80`（poll `task_get_dag`）
- 主 agent 收事件路径：复用 P21 hook consumer（`hook_consumer.go:96-220`）；agent 通过 `agent.turn.after / progress` 间接收 DAG 推进事件

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- `task_create_dag` schema 加 `auto_start: bool` 字段；server 端创建成功后立即调 `StartDAG(ctx, dagKey, triggerMeta)`，trigger 默认 `auto`
- 不引入新 hook event；DAG terminal 通过既有 hook consumer 回流到 agent
- 旧调用方不传 `auto_start` 仍按当前隐式期望工作（旧 DAG 默认 `auto`）

## DDL / SQL

- 不新增表
- 不改 SQL

## 依赖

- P3 已合入（trigger 枚举 + `cmd/mcp-orch/orchestration/dag_start.go:StartDAG` 共享入口）

## 风险

- `auto_start: true` 与 `trigger: manual` 互斥：schema 校验冲突时报 `ErrInvalidTriggerCombo`
- 旧调用方升级路径：UI / agent prompt 中 deprecation 提示

## 必测项

- `auto_start: true` 创建后立即推进
- `auto_start: false` + `trigger: manual` 显式 start
- 旧调用方（无 `auto_start`）按 `auto` 兼容

## 输入材料

- README §实施路线图 P4 行
- README §"未来扩展边界" 主 agent 段相关上下文


