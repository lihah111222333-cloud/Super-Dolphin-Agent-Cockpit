# P23.4: 主 agent 触发面

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P3**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

MCP 工具 `task_create_dag` 接受新可选参数 `auto_start: true`（语法糖：创建后立即调用 `internal/sidecar/orch/orchestration/dag_start.go:StartDAG`）；DAG terminal 事件通过既有 hook tap registry / hook consumer 链路回到主 agent；100% 向后兼容旧调用方。

## 现状校准（事实层）

- 主 agent 当前触发能力：`internal/sidecar/orch/tools/task_tools.go:64-70,94-105`（`task_create_dag` / `task_get_dag` / `task_update_node`）
- 主 agent 状态查询：`internal/sidecar/orch/tools/task_tools.go:74-80`（poll `task_get_dag`）
- 主 agent 收事件路径：复用 P21 hook consumer + `dag_terminal_tap` registry（`internal/sidecar/orch/orchestration/hook_consumer.go:96-220`）；agent 通过 `agent.turn.after / progress` 间接收 DAG 推进事件

## 推荐架构

`task_create_dag(auto_start=true)` 是 host/MCP 触发语法糖：创建 DAG 后立即构造 `trigger_source='host'` 的 StartDAG 请求。caller identity 必须来自 MCP server authenticated context（P6 的 MCP HTTP identity adapter 或本地 host session），例如 `authenticated_host_actor_id`，不是 tool params 中的 `agent_id`。`agent_id` 仅保留为 legacy/display/assigned-agent hint，不能写成 owner/tenant 授权依据。

host auto-start 使用 P3 统一 `dag_start_requests` schema：`trigger_instance_key = host_tool_call_id`（没有 tool call id 时由 server 生成 request id 并返回），`params_hash = canonical(auto_start + dag_key + trigger meta)`。重复请求同 hash 返回同一 start 结果，不同 hash conflict。缺 authenticated caller hard fail + audit。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| MCP tool schema provider | `internal/sidecar/orch/tools/dag_schema_registry.go` + host trigger provider [NEW] | 注册 `task_create_dag.auto_start`、`idempotency_key/request_id`；`task_tools.go` 只消费 registry，避免热文件并行改；`agent_id` 标注 legacy/display-only |
| MCP caller adapter | `internal/mcpserver/common/server.go` / `internal/mcpserver/common/http_transport.go`（扩展） | 从 authenticated MCP context 注入 `CallerIdentity`；缺失时写 audit 并拒绝写操作 |
| host trigger handler | `internal/sidecar/orch/tools/task_tools.go`（薄 handler） + `internal/sidecar/orch/orchestration/dag_start.go` | handler 只做 registry 校验与服务调用；create 成功后调用 `StartDAG(ctx, dagKey, TriggerMeta{source:host,...})`；不从 params 推导 owner/tenant |
| Start idempotency | `internal/sidecar/orch/store/dagstart/*.go`（复用 P3） | host `trigger_instance_key` 与 `params_hash` 去重；冲突返回 conflict |
| hook 回流 | `internal/sidecar/orch/orchestration/dag_terminal_tap.go` / `hook_tap_registry.go`（复用） | 终态事件沿既有 P21 hook consumer 的 tap registry 回主 agent，不直接扩主 hook 分发，不新增平行 event bus |

**已知关键改动方向**：
- 通过 `internal/sidecar/orch/tools/dag_schema_registry.go` 的 host provider 给 `task_create_dag` schema 加 `auto_start: bool` 字段；server 端创建成功后立即调 `StartDAG(ctx, dagKey, triggerMeta)`，trigger 默认 `auto`
- 不引入新 hook event；DAG terminal 通过 `dag_terminal_tap` registry 回流到 agent，主 hook consumer 保持 bounded fanout/enqueue-only
- 旧调用方不传 `auto_start` 仍按当前隐式期望工作（旧 DAG 默认 `auto`）

## DDL / SQL

- 不新增表
- 不改 SQL

## 依赖

- P3 已合入（trigger 枚举 + `internal/sidecar/orch/orchestration/dag_start.go:StartDAG` 共享入口）

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


