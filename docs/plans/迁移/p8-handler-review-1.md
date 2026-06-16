# P8 Handler Review 1

## 审查范围

- `internal/sidecar/orch/tools/workspace_tools.go`
- `internal/sidecar/orch/tools/prompt_tools.go`
- `internal/sidecar/orch/tools/command_tools.go`
- `internal/sidecar/orch/tools/shared_file_tools.go`
- `internal/sidecar/orch/tools/registry.go`
- `internal/sidecar/orch/tools/orchestration_tools.go`
- `internal/sidecar/orch/tools/task_tools.go`

## 1. 19 工具注册完整性表

结论：`registry.go` 已注册全部 19 个目标工具，且 19 个工具都能闭合到具体 handler。

| # | 工具名 | 注册 | handler 存在 | 注册来源 |
|---|---|---|---|---|
| 1 | `orchestration_launch_agent` | 是 | 是 | `NewRegistry -> orchestrationToolDefinitions -> HandleLaunchAgent` |
| 2 | `orchestration_send_message` | 是 | 是 | `NewRegistry -> orchestrationToolDefinitions -> HandleSendMessage` |
| 3 | `orchestration_stop_agent` | 是 | 是 | `NewRegistry -> orchestrationToolDefinitions -> HandleStopAgent` |
| 4 | `orchestration_list_agents` | 是 | 是 | `NewRegistry -> orchestrationToolDefinitions -> HandleListAgents` |
| 5 | `orchestration_get_agent_report` | 是 | 是 | `NewRegistry -> orchestrationToolDefinitions -> HandleGetAgentReport` |
| 6 | `task_create_dag` | 是 | 是 | `NewRegistry -> taskToolDefinitions -> HandleCreateDAG` |
| 7 | `task_get_dag` | 是 | 是 | `NewRegistry -> taskToolDefinitions -> HandleGetDAG` |
| 8 | `task_update_node` | 是 | 是 | `NewRegistry -> taskToolDefinitions -> HandleUpdateNode` |
| 9 | `workspace_create_run` | 是 | 是 | `NewRegistry -> workspaceToolDefinitions -> HandleWorkspaceCreateRun` |
| 10 | `workspace_get_run` | 是 | 是 | `NewRegistry -> workspaceToolDefinitions -> HandleWorkspaceGetRun` |
| 11 | `workspace_list_runs` | 是 | 是 | `NewRegistry -> workspaceToolDefinitions -> HandleWorkspaceListRuns` |
| 12 | `workspace_merge_run` | 是 | 是 | `NewRegistry -> workspaceToolDefinitions -> HandleWorkspaceMergeRun` |
| 13 | `workspace_abort_run` | 是 | 是 | `NewRegistry -> workspaceToolDefinitions -> HandleWorkspaceAbortRun` |
| 14 | `prompt_list` | 是 | 是 | `NewRegistry -> promptToolDefinitions -> HandlePromptList` |
| 15 | `prompt_get` | 是 | 是 | `NewRegistry -> promptToolDefinitions -> HandlePromptGet` |
| 16 | `command_list` | 是 | 是 | `NewRegistry -> commandToolDefinitions -> HandleCommandList` |
| 17 | `command_get` | 是 | 是 | `NewRegistry -> commandToolDefinitions -> HandleCommandGet` |
| 18 | `shared_file_read` | 是 | 是 | `NewRegistry -> sharedFileToolDefinitions -> HandleSharedFileRead` |
| 19 | `shared_file_write` | 是 | 是 | `NewRegistry -> sharedFileToolDefinitions -> HandleSharedFileWrite` |

补充：

- `NewRegistry` 的组装顺序为 `orchestration/task -> workspace -> prompt -> command -> shared_file`。
- `Registry.Lookup/List` 只是索引与只读视图，不参与额外过滤，因此只要进入 `tools` 切片就视为已注册。

## 2. 依赖检查结果

### 2.1 import 约束

按要求在 `internal/sidecar/orch/tools/` 范围搜索 import 依赖：

- `internal/module/`：`0`
- `internal/store/`：`0`

结果：通过。

当前依赖边界符合要求：

- 允许依赖 `internal/sidecar/orch/store/*`
- 允许依赖 `internal/contract`
- 未发现直接越层依赖 `internal/module/*`
- 未发现直接依赖旧的 `internal/store/*`

### 2.2 实际依赖形态

| 文件 | 主要依赖 | 结果 |
|---|---|---|
| `registry.go` | `internal/sidecar/orch/store/commandcard`, `internal/sidecar/orch/store/prompt`, `internal/sidecar/orch/store/sharedfile`, `internal/contract` | 通过 |
| `workspace_tools.go` | `internal/sidecar/orch/store/workspace` | 通过 |
| `prompt_tools.go` | `internal/sidecar/orch/store/prompt` | 通过 |
| `command_tools.go` | `internal/sidecar/orch/store/commandcard` | 通过 |
| `shared_file_tools.go` | `internal/sidecar/orch/store/sharedfile` | 通过 |
| `orchestration_tools.go` | `internal/contract`, `internal/dto/shared` | 通过 |
| `task_tools.go` | `internal/contract` | 通过 |

## 3. DTO 编码检查结果

### 3.1 JSON 字段名

结论：整体通过。资源层和 contract/store 暴露的返回类型均带 `json` tag，未见直接暴露 Go 大写字段名。

| 区域 | 返回类型 | JSON tag 情况 | 结果 |
|---|---|---|---|
| `prompt_tools.go` | `promptTemplateDTO` | 全量 snake_case | 通过 |
| `command_tools.go` | `commandCardDTO` | 全量 snake_case | 通过 |
| `shared_file_tools.go` | `sharedFileDTO` | 全量 snake_case | 通过 |
| `workspace_tools.go` | `WorkspaceMergeRunResult`, `WorkspaceMergeFileResult` | 全量 snake_case | 通过 |
| `workspace_tools.go` | `*workspacestore.WorkspaceRun`, `[]workspacestore.WorkspaceRun` | store 层结构体自带 snake_case tag | 通过，但存在边界暴露问题 |
| `orchestration_tools.go` | `successResult` / `contract.AgentSnapshot` / `contract.AgentReportResult` | 结果字段为 snake_case | 通过 |
| `task_tools.go` | `contract.DAGDetail`, `contract.DAGNode` | contract 层结构体自带 snake_case tag | 通过 |

### 3.2 DTO 边界

结论：编码层面没有大写字段泄露，但 DTO 边界不完全一致。

表现：

- `prompt/command/shared_file` 都有 tool-local DTO 映射。
- `workspace_merge_run` 也有 tool-local DTO。
- 但 `workspace_create_run/get_run/list_runs/abort_run` 直接返回 `internal/sidecar/orch/store/workspace.WorkspaceRun`。
- `task_*` 和部分 `orchestration_*` 直接返回 `internal/contract` 结构体。

判断：

- 从 JSON 编码角度看没有问题。
- 从分层边界看，`workspace` 组存在 store 层结构体直出，不如 `prompt/command/shared_file` 那样收敛。

## 4. 守卫检查结果

### 4.1 规模守卫

结论：通过。

| 文件 | 行数 | 结果 |
|---|---:|---|
| `registry.go` | 44 | 通过 |
| `workspace_tools.go` | 269 | 通过 |
| `prompt_tools.go` | 138 | 通过 |
| `command_tools.go` | 162 | 通过 |
| `shared_file_tools.go` | 122 | 通过 |
| `orchestration_tools.go` | 187 | 通过 |
| `task_tools.go` | 297 | 通过 |

按 `document_symbol` 范围核对，未发现单个函数超过 80 行。

### 4.2 handler 模式

结论：主体模式一致。

统一点：

- 所有 handler 都是 `HandleX(...) ToolHandler`
- 所有 handler 都先 `decodeInput(input, &in)`
- 所有注册都走 `ToolDefinition{Name, Description, InputSchema, Handler}`

统一入口：

- `ToolHandler`：`func(ctx context.Context, input json.RawMessage) (any, error)`
- `decodeInput`：空输入会归一为 `{}` 再反序列化

### 4.3 依赖判空守卫

结论：不完全一致。

| 组别 | 判空守卫 | 结果 |
|---|---|---|
| `workspace_*` | `if store == nil { ... }` | 通过 |
| `prompt_*` | `if store == nil { ... }` | 通过 |
| `command_*` | `if store == nil { ... }` | 通过 |
| `shared_file_*` | `if store == nil { ... }` | 通过 |
| `orchestration_*` | 未见 `if svc == nil { ... }` | 不通过 |
| `task_*` | 未见 `if svc == nil { ... }` | 不通过 |

影响：

- `Dependencies.Orchestration` 若未注入，`orchestration/task` 工具会在 `svc.*` 调用处 panic。
- 其它资源工具则会返回明确的配置错误。

### 4.4 输入守卫

结论：不完全一致。

做得较好的部分：

- `prompt_get`、`command_get`、`shared_file_read/write`、`workspace_*` 多数入口使用 `requireTrimmed(...)`
- `workspace_list_runs` 对 `limit` 做了 `normalizeWorkspaceListLimit`
- `workspace_create_run` 对 `files` 做了 `trimNonEmpty`

不足的部分：

- `orchestration_*` 和 `task_*` 主要依赖 `InputSchema.required/enum`
- 但 `decodeInput` 只做 JSON 反序列化，不校验 schema
- 因此空白字符串在代码层仍可能通过

具体表现：

- `launchRequestFromInput` 未对 `name` 做非空校验
- `submissionFromMessage` 未对 `agent_id` / `message` 做非空校验
- `createDAGRequestFromInput` 未对 `dag_key` / `title` 做非空校验
- `createDAGNodesFromInput` 未对 `node_key` / `title` / `depends_on` 元素做清洗
- `updateNodeRequestFromInput` 未对 `dag_key` / `node_key` / `status` 做非空校验，也未在代码层复核 `status` 枚举

### 4.5 not-found / nil 结果守卫

结论：多数资源工具处理清晰，workspace store 行为可接受。

| 场景 | 行为 | 结果 |
|---|---|---|
| `prompt_get` | `nil -> errors.New("prompt template not found")` | 通过 |
| `command_get` | `nil -> errors.New("command card not found")` | 通过 |
| `shared_file_read` | `nil -> errors.New("shared file not found")` | 通过 |
| `shared_file_write` | `nil -> errors.New("shared file write returned no result")` | 通过 |
| `workspace_get_run` | 依赖 store `GetRun` 返回 error，不直接放出 nil | 通过 |
| `workspace_abort_run` | 依赖 store `AbortRun` 语义返回 error，不直接放出 nil | 通过 |

## 5. 问题清单

| # | 问题 | 严重度 | 建议 |
|---|---|---|---|
| 1 | `orchestration_*` 与 `task_*` 缺少 `svc == nil` 守卫，和其它资源 handler 的 `store == nil` 守卫不一致。`Dependencies.Orchestration` 未配置时会直接在 `svc.LaunchAgent / SubmitTurn / StopAgent / ListAgents / GetReport / CreateDAG / GetDAG / UpdateNodeStatus` 处触发空指针。 | 高 | 在全部 `HandleLaunchAgent/HandleSendMessage/HandleStopAgent/HandleListAgents/HandleGetAgentReport/HandleCreateDAG/HandleGetDAG/HandleUpdateNode` 入口统一补 `svc == nil` 判空，并返回固定错误文案。 |
| 2 | `workspace_create_run/get_run/list_runs/abort_run` 直接暴露 `internal/sidecar/orch/store/workspace.WorkspaceRun`，而不是 tool-local DTO；同组中的 `workspace_merge_run` 却使用了 `WorkspaceMergeRunResult`，导致 workspace 资源层边界不一致。 | 中 | 为 `workspace` 组补一套工具层 DTO，统一由 tools 包负责映射返回，避免 store 层结构体成为稳定外部协议。 |
| 3 | `orchestration_*` 与 `task_*` 的 required/enum 约束只存在于 `InputSchema`，但工具层 `decodeInput` 只做 `json.Unmarshal`，并不执行 schema 校验。空白 `name`、`agent_id`、`message`、`dag_key`、`title`、`node_key`、`status` 仍可下传到 service。 | 中 | 抽取通用 guard helper，复用 `requireTrimmed`，并在 `launchRequestFromInput`、`submissionFromMessage`、`createDAGRequestFromInput`、`createDAGNodesFromInput`、`updateNodeRequestFromInput` 中把必填和枚举校验落到代码。 |
| 4 | `createDAGNodesFromInput` 对 `DependsOn` 直接 `append([]string(nil), node.DependsOn...)`，未做 `trimNonEmpty` 级别的清洗，可能把空白依赖名原样带入 contract。 | 中 | 对 `depends_on` 做与 workspace `files` 一致的 trim + empty 过滤。 |
| 5 | 成功返回形状在不同 handler 组之间并不统一：`orchestration_launch/send/stop` 返回 `{success, agent_id}`，`task_*` 返回原始 contract DTO，`workspace_*` 有的返回 store struct，有的返回 tool DTO，`prompt/command/shared_file` 则返回资源 DTO。这不影响注册，但会提高上层适配复杂度。 | 低 | 如果目标是统一资源 handler 协议，建议明确“全部返回资源 DTO”或“全部 mutation 返回 success envelope + payload”中的一种。 |

## 6. 结论

总体结论：

- 注册完整性：通过，19/19 全部注册且 handler 存在。
- 依赖边界：通过，未发现 `internal/module/*` 或 `internal/store/*` 违规依赖。
- DTO 编码：通过，未发现 Go 大写字段直接泄露。
- 规模守卫：通过，所有目标文件 <= 400 行，所有函数 <= 80 行。
- handler 模式：主体一致，均采用统一 `ToolDefinition + ToolHandler + decodeInput` 模式。
- 主要问题：集中在守卫和边界一致性，不在注册缺失。

建议优先级：

1. 先补 `orchestration/task` 的 `nil service guard`
2. 再补 `orchestration/task` 的代码级 required/enum guard
3. 最后收敛 `workspace` 返回 DTO，消除 store 结构体直出

如果按风险排序，本轮最应先修的是问题 1 和问题 3。
