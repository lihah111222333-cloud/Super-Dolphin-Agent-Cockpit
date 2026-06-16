# 架构合规：JSON Wire Format 一致性

## 结论

- 当前 wire format **未统一**。`rpc_types.go` 这一层不是单一的 camelCase，也不是单一的 snake_case，而是 **camelCase / snake_case / 单词小写** 混用。
- 事件 DTO 基本已经收敛：`internal/dto/**/event.go` 中，对外 JSON 字段的多词命名统一为 **snake_case**，未看到 camelCase 泄漏。
- `AgentSnapshot` 已与 V2 `AgentInfo` 的 JSON 字段形状对齐。
- 双格式兼容层只覆盖了 **6 个参数类型**，覆盖面偏窄；`thread` / `workspace` / DAG 相关参数仍大量只接受 camelCase。
- 返回值 shape 抽查 5 个后，只有 2 个完全对齐 V2，1 个条件性偏离，2 个明确不一致。
- `json.RawMessage` 的正确语义依赖 `nil`。`nil` 会序列化为 `null`（或在 `omitempty` 下直接省略），零长但非 `nil` 的 `json.RawMessage{}` 会直接 marshal 失败。

## 1. RPC 参数 json tag：全量扫描 `rpc_types.go`

验证范围：

- `internal/sidecar/orch/orchestration/rpc_types.go`
- `internal/module/thread/rpc_types.go`
- `internal/module/turn/rpc_types.go`
- `internal/module/skill/rpc_types.go`
- `internal/module/workspace/rpc_types.go`

结论：**整体不统一**。严格说不是“camelCase 还是 snake_case”的二选一，而是三类并存：

- camelCase：如 `agentId` / `threadId` / `dagKey` / `runKey`
- snake_case：如 `agent_id` / `event_type` / `command_template`
- 单词小写：如 `name` / `prompt` / `status` / `key`

按文件看：

| 文件 | 观察 | 结论 |
| --- | --- | --- |
| `internal/sidecar/orch/orchestration/rpc_types.go` | 同时出现 camelCase（`agentId`/`parentId`/`dagKey`/`nodeKey`/`createdBy`/`nodeType`/`assignedTo`/`dependsOn`/`commandRef`）和 snake_case（`agent_id`/`selected_skills`/`manual_skill_selection`/`output_schema`/`worker_id`/`sender_id`/`event_type`/`event_data`） | **混用最严重** |
| `internal/module/thread/rpc_types.go` | 多词字段偏 camelCase（`threadId`/`approvalPolicy`），其余单词字段小写 | **偏 camelCase** |
| `internal/module/turn/rpc_types.go` | `threadId` / `turnId` 用 camelCase；`call_id` / `request_id` 用 snake_case | **混用** |
| `internal/module/skill/rpc_types.go` | 多词字段统一 snake_case（`command_template`/`args_schema`/`risk_level`/`created_by`/`updated_by`） | **偏 snake_case** |
| `internal/module/workspace/rpc_types.go` | 多词字段统一 camelCase（`runKey`/`updatedBy`/`dryRun`/`deleteRemoved`/`dagKey`） | **偏 camelCase** |

补充：

- `internal/module/workspace/rpc_types.go:3` 的 `createRunParams` 是 `CreateRunRequest` 别名，真实 tag 在 `internal/module/workspace/contract.go:24-36`，同样是 camelCase：`runKey` / `dagKey` / `sourceRoot` / `workspacePath` / `createdBy` / `updatedBy` / `finishedAt`。

## 2. 事件 DTO json tag：`dto/*/event.go` 是否统一 snake_case

验证范围：

- `internal/dto/agent/event.go`
- `internal/dto/provider/event.go`
- `internal/dto/shared/event.go`
- `internal/dto/task/event.go`
- `internal/dto/tool/event.go`
- `internal/dto/turn/event.go`
- `internal/dto/ui/event.go`
- `internal/dto/workspace/event.go`

结论：**是，已统一为 snake_case（多词字段）+ 单词小写（单词字段）**。

观察点：

- `internal/dto/shared/event.go:42-127` 的公共 header 全部是 `thread_id` / `agent_id` / `session_id` / `turn_id` / `call_id` / `tool_name` / `approval_id` / `dag_key` / `node_key` / `wakeup_id` / `run_key`。
- `internal/dto/task/event.go:14-35` 全部是 `assigned_to` / `old_status` / `new_status` / `active_turn_id` / `active_wakeup_id` / `target_agent_id` / `bound_turn_id`。
- `internal/dto/tool/event.go:6-33` 全部是 `request_id` / `arguments_preview` / `elapsed_ms` / `reviewed_by`。
- `internal/dto/turn/event.go:23-48` 全部是 `stalled_ms` / `input_type` / `request_id`。
- `internal/dto/ui/event.go:11-27` 全部是 `item_id` / `item_kind` / `request_id` / `input_tokens` / `output_tokens` / `total_tokens` / `context_window_tokens`。
- `internal/dto/workspace/event.go:5-46` 全部是 `source_root` / `workspace_path` / `created_by` / `old_status` / `new_status` / `updated_by` / `merged_file_count`。

例外：

- `internal/dto/provider/event.go:3-10` 只是 driver 内部中转 DTO，没有 JSON tag，不属于对外 wire DTO。

判定：**事件 DTO 层是当前最干净的一层，可以视为 snake_case 基线。**

## 3. `AgentSnapshot` json tag：是否全部 snake_case，且与 V2 一致

V3 定义：

- `internal/sidecar/orch/orchestration/contract.go:49-59`

V2 对应：

- `go-agent-v2/internal/runner/manager.go:147-157`
- `go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:279-282`

结论：**是，全部 snake_case，并且与 V2 一致。**

字段逐项对比：

| V3 `AgentSnapshot` | V2 `AgentInfo` | 结果 |
| --- | --- | --- |
| `id` | `id` | 一致 |
| `name` | `name` | 一致 |
| `parent_id,omitempty` | `parent_id,omitempty` | 一致 |
| `port` | `port` | 一致 |
| `thread_id` | `thread_id` | 一致 |
| `cwd` | `cwd` | 一致 |
| `state` | `state` | 一致 |
| `provider,omitempty` | `provider,omitempty` | 一致 |
| `last_report,omitempty` | `last_report,omitempty` | 一致 |

补充：

- V3 的 `State` 是 `string`，V2 的 `State` 是 `AgentState`，但 JSON 形状都是字符串，不影响 wire。

## 4. 兼容层 `UnmarshalJSON`：哪些类型有双格式兼容，哪些缺

已实现双格式兼容的类型：

| 类型 | 主格式 | 兼容格式 | 证据 |
| --- | --- | --- | --- |
| `agentIDParams` | `agentId` | `agent_id` | `internal/sidecar/orch/orchestration/rpc_types.go:19-40` |
| `submitParams` | `agent_id` / `selected_skills` / `manual_skill_selection` / `output_schema` | `agentId` / `selectedSkills` / `manualSkillSelection` / `outputSchema`，以及旧 `input` | `internal/sidecar/orch/orchestration/rpc_types.go:70-116` |
| `reportParams` | `agent_id` | `agentId` | `internal/sidecar/orch/orchestration/rpc_types.go:118-140` |
| `rememberReportRequestParams` | `worker_id` / `sender_id` | `agentId` / `requesterId`，以及 `agent_id` / `requester_id` | `internal/sidecar/orch/orchestration/rpc_types.go:142-170` |
| `reportEventParams` | `agent_id` / `event_type` / `event_data` | `agentId` / `eventType` / `eventData` | `internal/sidecar/orch/orchestration/rpc_types.go:172-204` |
| `approvalRespondParams` | `call_id` / `request_id` | `callId` / `requestId` | `internal/module/turn/rpc_types.go:31-69` |

缺口：

| 类型/组 | 现状 | 缺失 |
| --- | --- | --- |
| `launchParams` | 只接受 `agentId` / `parentId` | 没有 `agent_id` / `parent_id` 兼容，也不接受 V2 的 `id` |
| DAG 参数（`dagKeyParams` / `dagNodeParams` / `createDAGParams` / `createDAGNodeParams`） | 只接受 `dagKey` / `nodeKey` / `createdBy` / `nodeType` / `assignedTo` / `dependsOn` / `commandRef` | 没有 snake_case 兼容 |
| `thread` 模块参数 | 只接受 `threadId` / `approvalPolicy` | 没有 `thread_id` / `approval_policy` 兼容 |
| `turnStartParams` / `turnSteerParams` / `turnInterruptParams` / `threadIDOnlyParams` | 只接受 `threadId` | 没有 `thread_id` 兼容 |
| `turnStartResult` | 返回 `turnId` | 与 snake_case `turn_id` 不兼容 |
| `workspace` 模块参数 | 只接受 `runKey` / `dagKey` / `updatedBy` / `dryRun` / `deleteRemoved` | 没有 snake_case 兼容 |
| `workspace.CreateRunRequest`（被 `createRunParams` 复用） | 只接受 `runKey` / `dagKey` / `sourceRoot` / `workspacePath` / `createdBy` / `updatedBy` / `finishedAt` | 没有 snake_case 兼容 |

判定：**兼容层目前是“点状补丁”，不是系统性兼容策略。**

## 5. 返回值 shape：抽查 5 个与 V2 wire 的一致性

对比基线：

- V2 返回 shape：`go-agent-v2/internal/apiserver/methods_orchestration.go`
- V2 shape guard：`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:259-282`

抽查结果：

| 返回类型 / 方法 | V3 | V2 期望 | 判定 |
| --- | --- | --- | --- |
| `AgentSnapshot` / `agent.list` | `internal/sidecar/orch/orchestration/contract.go:49-59`，handler 直接返回 `[]AgentSnapshot`（`internal/sidecar/orch/orchestration/rpc.go:43-45`） | `[]runner.AgentInfo`，keys 为 `cwd/id/last_report/name/parent_id/port/provider/state/thread_id` | **一致** |
| `AgentStateResult` / `agent.getState` | `{agent_id,state}`，见 `internal/sidecar/orch/orchestration/contract.go:61-64`、`internal/sidecar/orch/orchestration/report.go:51-60` | `{agent_id,state}`，见 V2 `methods_orchestration.go:166-176` | **一致** |
| `AgentReportResult` / `agent.getReport` | 基础 shape 是 `{agent_id,report,state}`，但在存在 requester 时会额外带 `metadata.requester_ids`，见 `internal/sidecar/orch/orchestration/contract.go:70-75`、`internal/sidecar/orch/orchestration/report.go:140-151` | `{agent_id,report,state}`，见 V2 `methods_orchestration.go:122-135` 和 shape guard `259-262` | **条件性偏离** |
| `RememberReportRequestResult` / `agent.rememberReportRequest` | `{success,agent_id,requester_id}`，见 `internal/sidecar/orch/orchestration/contract.go:82-86`、`internal/sidecar/orch/orchestration/report.go:73-95` | `{success,sender_id,worker_id}`，见 V2 `methods_orchestration.go:137-159` 和 shape guard `269-271` | **不一致** |
| `ReportEventResult` / `agent.reportEvent` | 基础字段 `{success,agent_id,event_type}`，但还可能带 `report` 与 `notified_requester_ids`，见 `internal/sidecar/orch/orchestration/contract.go:95-101`、`internal/sidecar/orch/orchestration/report.go:98-133` | `{success,agent_id,event_type}`，见 V2 `methods_orchestration.go:185-208` 和 shape guard `274-276` | **不一致** |

额外发现：

- `agent.launch` 在 V3 handler 中直接 `return nil, svc.LaunchAgent(...)`，即成功时 RPC 结果是 `null`，见 `internal/sidecar/orch/orchestration/rpc.go:17-19`。
- V2 `agent.launch` 返回 `{agent_id,name,status}`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:39-71` 与 shape guard `252-257`。
- 这不是“类型 tag”问题，而是**整条返回 wire 缺对象**。

## 6. `null` vs empty：`json.RawMessage` 的序列化行为

用一个最小 Go 片段实测：

```go
type S struct {
    A json.RawMessage `json:"a"`
    B json.RawMessage `json:"b,omitempty"`
}
```

实际结果：

- `S{}` / `A:nil` => `{"a":null}`
- `B:nil` 且 `omitempty` => `b` 被省略
- `json.RawMessage{}` => marshal 失败，报 `unexpected end of JSON input`
- `json.RawMessage("null")` => 正常输出 `null`

因此：

1. `nil` 和 `empty` 不是一回事。
2. 对 `json.RawMessage` 来说，**nil 是安全值**，零长非 `nil` 切片不是。
3. 当前代码大量使用 `append(json.RawMessage(nil), raw...)` 复制 `RawMessage`，例如：
   - `internal/sidecar/orch/orchestration/rpc.go:102,175,185,200,215`
   - `internal/sidecar/orch/orchestration/dag.go:99,133,165,200,219,220`
   - `internal/module/turn/rpc.go:92`
4. 这种写法会把“零长输入”归一化成 `nil`，从而得到两种可预期行为：
   - 无 `omitempty` 时输出 `null`
   - 有 `omitempty` 时直接省略字段

判定：**当前 `RawMessage` 复制策略是对的；如果后续有人直接构造 `json.RawMessage{}`，会引入真实的 wire/marshal 风险。**

## 建议

1. 先定外部 wire 基线。若目标是 V2 兼容，则外部 JSON 应以 snake_case 为准，camelCase 仅作为 `UnmarshalJSON` 兼容入口。
2. 先修返回值偏差最大的 3 处：`agent.launch`、`RememberReportRequestResult`、`ReportEventResult`。
3. 把兼容层从“逐个补洞”改成“按模块成套收口”：
   - `thread`
   - `workspace`
   - orchestration DAG
4. 保持 `internal/dto/**/event.go` 和 `AgentSnapshot` 这两层不动，它们目前是最接近统一基线的部分。
