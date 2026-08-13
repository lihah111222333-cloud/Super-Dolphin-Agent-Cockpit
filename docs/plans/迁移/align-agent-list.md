# V2↔V3 1:1 对齐：`agent.list` + `agent.snapshot` + `agent.getState` + `agent.getReport`

审查时间：2026-03-21
阅读方式：LSP `workspace_symbol` / `read_file` / `references`；未使用 `grep/find/cat/sed/awk`

## 范围

本次只对照这两组实现：

- V2：
  - `go-agent-v2/internal/apiserver/methods_orchestration.go`
  - `go-agent-v2/internal/runner/manager.go`
  - `go-agent-v2/internal/runner/manager_lifecycle.go`
  - `go-agent-v2/internal/runner/manager_event.go`
- V3：
  - `cmd/mcp-orch/orchestration/rpc.go`
  - `cmd/mcp-orch/orchestration/contract.go`
  - `cmd/mcp-orch/orchestration/service.go`
  - `cmd/mcp-orch/orchestration/helpers.go`
  - `cmd/mcp-orch/orchestration/report.go`
  - `internal/module/thread/lifecycle.go`
  - `internal/dto/agent/state.go`

## 总结

| 方法 | 结论 | 结论摘要 |
| --- | --- | --- |
| `agent.list` | ⚠️ | wire shape 和排序规则基本对齐，但 `state` 值域已不是 V2 五态；`port/provider` 在 V3 依赖 launch 请求推断，不是 runtime 实测值 |
| `agent.snapshot` | ❌ | V2 没有对外 `agent.snapshot`；只有内部 `Snapshot() []*AgentProcess`，语义是 shutdown/force-kill 用的全量原始进程快照 |
| `agent.getState` | ⚠️ | 外层返回 shape 对齐，但 V3 直接暴露扩展状态机；V2 缺失 agent 时返回空状态，V3 会报错 |
| `agent.getReport` | ⚠️ | 核心字段对齐，但 V2 会在 `LastReport` 为空时回退 `LastMessage`，V3 不会；V3 缺失 agent 也会报错，并新增可选 `metadata` |

## 逐项对比

### 1. `agent.list`

V2 依据：

- `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27` 注册了 `agent.list`
- `go-agent-v2/internal/apiserver/methods_orchestration.go:110-116` 直接返回 `s.mgr.List()`
- `go-agent-v2/internal/runner/manager.go:147-157` `AgentInfo` 使用 snake_case json tag
- `go-agent-v2/internal/runner/manager.go:278-312` `List()` 组装并按 `ID -> Name -> Port` 做 `sort.SliceStable`

V3 依据：

- `cmd/mcp-orch/orchestration/rpc.go:15-76` 注册了 `agent.list`
- `cmd/mcp-orch/orchestration/service.go:189-207` `ListAgents()` 返回 `[]AgentSnapshot`，并按 `ID -> Name -> Port` 做 `sort.SliceStable`
- `cmd/mcp-orch/orchestration/contract.go:49-59` `AgentSnapshot` 使用 snake_case json tag

对比：

- 返回 shape：
  - V2：`[]AgentInfo`
  - V3：`[]AgentSnapshot`
  - 字段名一一对应：`id/name/parent_id/port/thread_id/cwd/state/provider/last_report`
- 字段完整性：
  - `last_report`：两边都是内存里的最近结构化报告字段
  - `port/provider`：
    - V2 来自 runtime：`proc.Client.GetPort()` 和 `proc.Provider`
    - V3 来自 launch 请求推断：`launchPort(req)` / `launchProvider(req)`，见 `cmd/mcp-orch/orchestration/helpers.go:34-50,233-253`
    - `internal/module/thread/lifecycle.go:326-337` 的默认 `buildLaunchRequest()` 只传 `Command: []string{exe}`，不传 `Env`，所以 thread 生命周期走默认启动路径时，V3 的 `port/provider` 很容易落成 `0` / `""`
- 排序稳定性：
  - 两边一致，都是稳定排序，比较键一致
- 语义差异：
  - V2 `state` 是 `idle/thinking/running/stopped/error` 五态，见 `go-agent-v2/internal/runner/manager.go:17-25`
  - V3 `state` 是扩展状态机：`provisioning/turn_queued/turn_starting/turn_running/awaiting_user_input/recovering/stopping/stopped/failed`，见 `internal/dto/agent/state.go:8-19`
  - 所以 shape 对齐，不代表状态语义 1:1 对齐

结论：⚠️
原因：`agent.list` 的 wire 和排序已经很接近，但 `state` 值域已经换代；`port/provider` 也不是 V2 那种 runtime 实测语义。

### 2. `agent.snapshot`

V2 依据：

- `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27` 没有注册 `agent.snapshot`
- `go-agent-v2/internal/runner/manager_lifecycle.go:139-149` 只有内部 `Snapshot() []*AgentProcess`
- 注释明确说明其用途是 shutdown 场景下 `StopAll` 超时后的强杀快照，不是对外 DTO
- `go-agent-v2/internal/runner/manager.go:32-61` `AgentProcess` 没有 json tag，且暴露的是内部 runtime 字段

V3 依据：

- `cmd/mcp-orch/orchestration/rpc.go:46-48` 注册了 `agent.snapshot`
- `cmd/mcp-orch/orchestration/service.go:209-232` `Snapshot(ctx, agentID)` 返回单 agent `AgentSnapshot`
- `cmd/mcp-orch/orchestration/contract.go:49-59` `AgentSnapshot` 是 snake_case DTO

对比：

- 返回 shape：
  - V2：没有对外 RPC；内部方法是 `[]*AgentProcess`
  - V3：对外 RPC，返回单个 `AgentSnapshot`
- json tag：
  - V2 内部 `AgentProcess` 没有 snake_case tag
  - V3 `AgentSnapshot` 明确是 snake_case
- 语义：
  - V2 `Snapshot()`：全量、原始、shutdown/force-kill 辅助数据
  - V3 `agent.snapshot`：单 agent、对外可序列化、用于查询当前 agent 视图
- 字段完整性：
  - V3 `port/provider/last_report` 的字段集合是完整的
  - 但 `port/provider` 仍受上面 `launchPort/launchProvider` 推断逻辑限制，不具备 V2 runtime snapshot 的强语义
- 排序稳定性：
  - 不适用；V3 返回单对象，V2 内部方法返回切片但无排序语义

结论：❌
原因：这不是“同一方法的新实现”，而是“V3 新增了一个对外 DTO 方法；V2 只有内部 shutdown snapshot”。shape、可见性、粒度、用途全部不同。

### 3. `agent.getState`

V2 依据：

- `go-agent-v2/internal/apiserver/methods_orchestration.go:23,162-177` 注册并返回 `{agent_id,state}`
- `go-agent-v2/internal/runner/manager.go:498-506` `GetState(id)` 返回 `effectiveState(proc)`；缺失 agent 时返回空字符串

V3 依据：

- `cmd/mcp-orch/orchestration/rpc.go:49-51` 注册并返回 `svc.GetState(...)`
- `cmd/mcp-orch/orchestration/contract.go:61-64` `AgentStateResult` 使用 snake_case tag
- `cmd/mcp-orch/orchestration/report.go:51-60` `GetState()` 直接返回 `{agent_id,state}`；缺失 agent 时返回错误

对比：

- 返回 shape：
  - V2：`map[string]any{"agent_id","state"}`
  - V3：`AgentStateResult{agent_id,state}`
  - 核心 wire shape 对齐，都是 snake_case
- 字段完整性：
  - 本方法只返回 `agent_id/state`，`port/provider/lastReport` 不适用
- 排序稳定性：
  - 不适用
- 语义差异：
  - V2：缺失 agent 时成功返回空 `state`
  - V3：缺失 agent 时直接报 `agent not found`
  - V2 状态值域是五态；V3 直接暴露扩展状态机，没有向 V2 五态折叠

结论：⚠️
原因：外层 shape 已对齐，但错误语义和状态值域都不再是 V2。

### 4. `agent.getReport`

V2 依据：

- `go-agent-v2/internal/apiserver/methods_orchestration.go:20,118-135` 注册并返回 `{agent_id,report,state}`
- `go-agent-v2/internal/runner/manager.go:462-477` `GetReport()` 走 `GetCompletionSummary()`
- `go-agent-v2/internal/runner/manager_report_summary_test.go:5-32` 明确验证：
  - 优先 `LastReport`
  - `LastReport` 为空时回退 `LastMessage`

V3 依据：

- `cmd/mcp-orch/orchestration/rpc.go:52-54` 注册并返回 `svc.GetReport(...)`
- `cmd/mcp-orch/orchestration/contract.go:70-75` `AgentReportResult` 使用 snake_case tag，新增可选 `metadata`
- `cmd/mcp-orch/orchestration/report.go:62-71,140-151` `GetReport()` 只返回 `lastReport`，不回退别的文本

对比：

- 返回 shape：
  - V2：`{agent_id,report,state}`
  - V3：`{agent_id,report,state,metadata?}`
  - V3 是向后兼容的“超集 shape”，核心字段仍是 snake_case
- 字段完整性：
  - 本方法不返回 `port/provider/last_report`
  - `report` 语义不等价：
    - V2：`LastReport || LastMessage`
    - V3：只取 `lastReport`
- 排序稳定性：
  - 不适用
- 语义差异：
  - V2 缺失 agent 时返回空 `report/state`
  - V3 缺失 agent 时报错
  - V3 多出 `metadata.requester_ids`

结论：⚠️
原因：wire 主体兼容，但 report 解析策略和缺失 agent 行为都变了；这会直接影响依赖“无 structured report 也能拿到 completion 文本”的调用方。

## `getState` vs `snapshot` 的语义差异

这个点在 V2 和 V3 不是同一层面的区别：

- V2：
  - `GetState(id)` 是对外查询接口，返回某个 agent 的当前状态
  - `Snapshot()` 是内部 shutdown 工具，返回全量 `[]*AgentProcess` 原始指针快照
  - 两者不是同一抽象层，也不是同一粒度
- V3：
  - `agent.getState` 是最小状态查询，只返回 `{agent_id,state}`
  - `agent.snapshot` 是单 agent 的完整序列化视图，返回 `AgentSnapshot`
  - 两者形成了清晰的“轻量 getter vs 完整 snapshot DTO”分层

结论：

- 如果迁移目标是“V2 内部 `Snapshot()` 语义搬到 V3”，当前不是 1:1
- 如果迁移目标是“对外新增一个单-agent snapshot RPC”，V3 已经做了，但这属于新增能力，不是 V2 对齐

## 最终判断

当前这 4 项里，没有一项可以直接打成“完全 1:1 对齐”：

- `agent.list`：⚠️
- `agent.snapshot`：❌
- `agent.getState`：⚠️
- `agent.getReport`：⚠️

最关键的断点有 3 个：

1. `agent.snapshot` 在 V2 根本不是对外同名能力，V3 是新设计。
2. V3 没有把扩展状态机折叠回 V2 五态，所以 `list/getState/getReport` 的 `state` 语义都不是严格兼容。
3. V3 的 `port/provider` 不是 runtime 实测值，而是 launch 参数推断值；`getReport` 也失去了 V2 的 `LastMessage` fallback。
