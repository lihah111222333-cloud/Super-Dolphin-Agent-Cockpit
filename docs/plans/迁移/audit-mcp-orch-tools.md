# MCP 编排工具族审查

## 0. 审查范围与纠偏

- 用户指定的 `go-agent-v2/internal/mcp/orch/` 路径在当前仓库中不存在；V2 MCP server 的实际实现位于 `go-agent-v2/internal/mcp/`，其中编排 HTTP 代理在 `go-agent-v2/internal/mcp/orchestration_http.go:17-35`，运行时注册入口在 `go-agent-v2/internal/mcp/runtime.go:84-108`。
- V2 真正把编排族工具挂到运行时的入口不是 `internal/mcp/orch/`，而是 `tooladapter` 把 `ResourceTools` 与 `OrchestrationTools` 一起追加到公共工具列表：`go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-170`。
- V2 MCP server 启动时会创建 `runtime` 并调用 `register()`，从而把上述工具真正注册进 stdio MCP server：`go-agent-v2/internal/mcp/server.go:51-59`、`go-agent-v2/internal/mcp/runtime.go:105-108`。

## 1. V2 编排工具完整清单

### 1.1 总览

- V2 的编排工具族由两组组成：`ResourceTools` 和 `OrchestrationTools`。`ResourceTools` 返回 DAG / command / prompt / shared_file / workspace 工具；`OrchestrationTools` 返回 agent orchestration 工具。入口分别在 `go-agent-v2/pkg/toolsdk/tools/resource.go:16-20`、`go-agent-v2/pkg/toolsdk/tools/orchestration.go:228-313`。
- `resourceToolSpecs()` 依次拼接 `task`、`command/prompt`、`shared_file`、`workspace` 四组 spec，因此 V2 资源侧总计 15 个工具：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:56-63`。
- `OrchestrationTools()` 直接定义 5 个 agent 编排工具，因此 V2 编排族总计 20 个 MCP tool：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:228-313`。
- 其中 `workspace_*` 是条件注册；`buildResourceTools()` 会在 `provider.WorkspaceOps() == nil` 时跳过 `workspaceOnly` spec，而 `cmd/mcp-server` 会在 workspace manager 不可用时进入 restricted mode：`go-agent-v2/pkg/toolsdk/tools/resource.go:29-39`、`go-agent-v2/cmd/mcp-server/main.go:76-83`。

### 1.2 V2 全量工具名

#### A. Agent Orchestration

| Tool Name | 定义位置 |
|---|---|
| `orchestration_list_agents` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:232-240` |
| `orchestration_send_message` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:245-258` |
| `orchestration_launch_agent` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:262-278` |
| `orchestration_stop_agent` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:282-294` |
| `orchestration_get_agent_report` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:298-310` |

#### B. DAG / Task

| Tool Name | 定义位置 |
|---|---|
| `task_create_dag` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:45-53`，常量名定义在 `go-agent-v2/pkg/toolsdk/tools/resource.go:63-65` |
| `task_get_dag` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:69-78` |
| `task_update_node` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:82-96` |
| `task_start_node` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:100-111` |

#### C. Command Card / Prompt

| Tool Name | 定义位置 |
|---|---|
| `command_list` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:120-128` |
| `command_get` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:132-141` |
| `prompt_list` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:145-153` |
| `prompt_get` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:157-166` |

#### D. Shared File

| Tool Name | 定义位置 |
|---|---|
| `shared_file_read` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:175-184` |
| `shared_file_write` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:188-199` |

#### E. Workspace

| Tool Name | 定义位置 |
|---|---|
| `workspace_create_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:208-227` |
| `workspace_get_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:231-241` |
| `workspace_list_runs` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:245-256` |
| `workspace_merge_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:260-273` |
| `workspace_abort_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:277-289` |

### 1.3 V2 处理函数定位

- `command_list` / `command_get` 的 handler 在 `go-agent-v2/pkg/toolsdk/tools/resource.go:253-284`。
- `prompt_list` / `prompt_get` 的 handler 在 `go-agent-v2/pkg/toolsdk/tools/resource.go:286-317`。
- `shared_file_read` / `shared_file_write` 的 handler 在 `go-agent-v2/pkg/toolsdk/tools/resource.go:319-357`。
- workspace 工具的 spec 在 `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:204-292`，provider 通过 `mcpResourceProvider` 从 stores/manager 暴露到 MCP 运行时：`go-agent-v2/internal/mcp/runtime.go:226-285`。
- orchestration 工具通过 HTTP RPC provider 代理到主 apiserver：`go-agent-v2/internal/mcp/orchestration_http.go:23-44`、`go-agent-v2/internal/mcp/orchestration_http.go:173-241`。
- 用户要求核查的 `agent_` 前缀在 V2 中不是 MCP tool 前缀；V2 暴露的是 `orchestration_*`，而 `agent.*` 仅作为内部 RPC method 名存在于 HTTP provider 中：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:228-313`、`go-agent-v2/internal/mcp/orchestration_http.go:46-107`、`go-agent-v2/internal/mcp/orchestration_http.go:179-241`。

## 2. V3 当前编排工具清单

### 2.1 `cmd/mcp-orch` 当前状态

- `cmd/mcp-orch/main.go` 只有 `main -> run()` 的薄入口，没有任何 tool wiring：`cmd/mcp-orch/main.go:8-12`。
- `cmd/mcp-orch/fx.go` 当前只执行 `fx.New(fx.NopLogger)`，没有 `fx.Provide(...)`、`fx.Invoke(...)`、也没有导入 orchestration/workspace module，因此当前注册工具数为 0：`cmd/mcp-orch/fx.go:5-12`。

### 2.2 V3 代码库内存在的“非 MCP tool”对应能力

- V3 已有 orchestration RPC handler 映射，但它们只在 `internal/sidecar/orch/orchestration` 模块内定义为 app/RPC handler，不是 `cmd/mcp-orch` 暴露出来的 MCP tool：`internal/sidecar/orch/orchestration/module.go:15-23`、`internal/sidecar/orch/orchestration/rpc.go:15-77`。
- V3 已有 workspace RPC handler 映射，但同样只在 `internal/module/workspace` 模块内定义，不是 `cmd/mcp-orch` 当前暴露出来的 MCP tool：`internal/module/workspace/module.go:9-14`、`internal/module/workspace/rpc.go:13-24`。
- 这两个 module 目前被桌面 app 总装配进 `internal/app/modules.go`，而不是被 `cmd/mcp-orch` 组装：`internal/app/modules.go:28-37`。
- V3 `internal/mcpserver/common` 目前只有未被消费的 manifest type 和一个只等待 `ctx.Done()` 的空 server；没有任何 family-specific tool 注册逻辑：`internal/mcpserver/common/manifest.go:3-12`、`internal/mcpserver/common/server.go:8-20`。

### 2.3 结论

- 以“当前已注册的 mcp-orch 工具”为标准，V3 现状是空实现，当前没有任何已注册的 orchestration / task / workspace / command / prompt / shared_file MCP tool：`cmd/mcp-orch/fx.go:5-12`、`internal/mcpserver/common/server.go:8-20`。

## 3. 重点检查：命令卡 + 提示词 + 共享文件

### 3.1 Prompt 工具

- V2 的 `prompt_list` / `prompt_get` 是 MCP tool，定义在 `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:145-166`，handler 在 `go-agent-v2/pkg/toolsdk/tools/resource.go:286-317`。
- V2 中没有名为 `prompt_create` / `prompt_update` / `prompt_delete` 的 MCP tool 定义；底层只有 store CRUD 能力，接口在 `go-agent-v2/pkg/toolsdk/tools/providers.go:186-192`，实现为 `Save` / `Get` / `List` / `SetEnabled` / `Delete`：`go-agent-v2/internal/store/prompt_template.go:15-73`。
- V3 中 prompt 也只有 store 层，没有对应 MCP tool 或 module handler。store contract 位于 `internal/store/prompt/contract.go:10-13`，实现位于 `internal/store/prompt/store.go:16-72`；`cmd/mcp-orch` 没有接入任何 prompt tool：`cmd/mcp-orch/fx.go:5-12`。

### 3.2 Command Card 工具

- V2 的 `command_list` / `command_get` 是 MCP tool，定义在 `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:120-141`，handler 在 `go-agent-v2/pkg/toolsdk/tools/resource.go:253-284`。
- V2 中没有名为 `command_create` / `command_card_create` / `command_card_*` 的 MCP tool 定义；底层只有 store CRUD 能力，接口在 `go-agent-v2/pkg/toolsdk/tools/providers.go:178-184`，实现为 `Save` / `Get` / `List` / `SetEnabled` / `Delete`：`go-agent-v2/internal/store/command_card.go:15-75`。
- `command_run` 也不是 V2 的真实 MCP tool；当前代码里它只出现在 prefix 分组测试中，用来验证 `command_` 前缀归类：`go-agent-v2/legacy-agentsdk/claude/cc_guardrail_test.go:59-77`。
- V3 中 command card 同样只有 store 层，没有对应 MCP tool；store contract 位于 `internal/store/commandcard/contract.go:10-15`，实现位于 `internal/store/commandcard/store.go:16-86`，而 `cmd/mcp-orch` 仍未接入任何 command tool：`cmd/mcp-orch/fx.go:5-12`。

### 3.3 Shared File 工具

- V2 的 `shared_file_read` / `shared_file_write` 是 MCP tool，定义在 `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:175-199`，handler 在 `go-agent-v2/pkg/toolsdk/tools/resource.go:319-357`。
- V2 中没有名为 `shared_file_list` 的 MCP tool；底层只有 `FileStore.List` / `Delete` 能力，接口在 `go-agent-v2/pkg/toolsdk/tools/providers.go:194-199`，实现位于 `go-agent-v2/internal/store/shared_file.go:35-46`。
- V3 中 shared file 也只有 store 层，没有对应 MCP tool；store contract 位于 `internal/store/sharedfile/contract.go:9-11`，实现位于 `internal/store/sharedfile/store.go:17-52`，而 `cmd/mcp-orch` 未接入任何 shared file tool：`cmd/mcp-orch/fx.go:5-12`。

## 4. V2 / V3 逐一对照表

状态定义：

- `✅`：V3 当前已有同类 MCP tool 注册。
- `❌`：V3 当前没有同类 MCP tool 注册；`V3 对应` 一栏仅注明“底层服务/RPC/store 是否存在”。

| V2 Tool Name | V2 文件 | V3 对应 | 状态 |
|---|---|---|---|
| `orchestration_list_agents` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:232-240` | RPC `agent.list`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:43-45`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `orchestration_send_message` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:245-258` | 近似 RPC `agent.submit` / `agent.submitPrompt`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:20-39`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `orchestration_launch_agent` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:262-278` | RPC `agent.launch`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:17-19`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `orchestration_stop_agent` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:282-294` | RPC `agent.stop`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:40-42`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `orchestration_get_agent_report` | `go-agent-v2/pkg/toolsdk/tools/orchestration.go:298-310` | RPC `agent.getReport` / `orchestration/report`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:52-54`、`internal/sidecar/orch/orchestration/rpc.go:73-75`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `task_create_dag` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:45-53` | RPC `task/dag/create`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:61-63`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `task_get_dag` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:69-78` | RPC `task/dag/get`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:64-66`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `task_update_node` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:82-96` | RPC `task/node/update`，但未接入 `cmd/mcp-orch`：`internal/sidecar/orch/orchestration/rpc.go:70-72`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `task_start_node` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:100-111` | 未找到对应 RPC / tool；`cmd/mcp-orch` 也未注册任何 tool：`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `command_list` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:120-128` | 仅 store list 能力：`internal/store/commandcard/contract.go:10-15`、`internal/store/commandcard/store.go:76-86`；无 MCP tool | ❌ |
| `command_get` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:132-141` | 仅 store get 能力：`internal/store/commandcard/contract.go:10-15`、`internal/store/commandcard/store.go:16-23`；无 MCP tool | ❌ |
| `prompt_list` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:145-153` | 仅 store list 能力：`internal/store/prompt/contract.go:10-13`、`internal/store/prompt/store.go:62-72`；无 MCP tool | ❌ |
| `prompt_get` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:157-166` | 仅 store get 能力：`internal/store/prompt/contract.go:10-13`、`internal/store/prompt/store.go:16-23`；无 MCP tool | ❌ |
| `shared_file_read` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:175-184` | 仅 store get 能力：`internal/store/sharedfile/contract.go:9-11`、`internal/store/sharedfile/store.go:30-37`；无 MCP tool | ❌ |
| `shared_file_write` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:188-199` | 仅 store upsert 能力：`internal/store/sharedfile/contract.go:9-11`、`internal/store/sharedfile/store.go:17-28`；无 MCP tool | ❌ |
| `workspace_create_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:208-227` | RPC `workspace/run/create`，但未接入 `cmd/mcp-orch`：`internal/module/workspace/rpc.go:15-16`、`internal/module/workspace/rpc.go:26-37`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `workspace_get_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:231-241` | RPC `workspace/run/get`，但未接入 `cmd/mcp-orch`：`internal/module/workspace/rpc.go:16-17`、`internal/module/workspace/rpc.go:39-50`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `workspace_list_runs` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:245-256` | RPC `workspace/run/list`，但未接入 `cmd/mcp-orch`：`internal/module/workspace/rpc.go:17-18`、`internal/module/workspace/rpc.go:52-60`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `workspace_merge_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:260-273` | RPC `workspace/run/merge`，但未接入 `cmd/mcp-orch`：`internal/module/workspace/rpc.go:19-20`、`internal/module/workspace/rpc.go:75-95`、`cmd/mcp-orch/fx.go:5-12` | ❌ |
| `workspace_abort_run` | `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:277-289` | RPC `workspace/run/abort`，但未接入 `cmd/mcp-orch`：`internal/module/workspace/rpc.go:20-21`、`internal/module/workspace/rpc.go:97-111`、`cmd/mcp-orch/fx.go:5-12` | ❌ |

## 5. 缺失工具完整参数签名

说明：

- 下列全部工具在 V2 已存在，但 V3 当前没有以 MCP tool 形式注册到 `cmd/mcp-orch`；因此全部列为“缺失工具”。V2 参数签名取自对应 `DynamicTool.InputSchema` 定义：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:228-313`、`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:1-292`。

### 5.1 Orchestration

#### `orchestration_list_agents`

```json
{}
```

定义：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:232-240`

#### `orchestration_send_message`

```json
{
  "agent_id": "string",
  "message": "string"
}
```

必填：`agent_id`, `message`。定义：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:245-258`

#### `orchestration_launch_agent`

```json
{
  "name": "string",
  "prompt": "string, optional",
  "cwd": "string, optional",
  "provider": "string, optional"
}
```

必填：`name`。定义：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:262-278`

#### `orchestration_stop_agent`

```json
{
  "agent_id": "string"
}
```

必填：`agent_id`。定义：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:282-294`

#### `orchestration_get_agent_report`

```json
{
  "agent_id": "string"
}
```

必填：`agent_id`。定义：`go-agent-v2/pkg/toolsdk/tools/orchestration.go:298-310`

### 5.2 DAG / Task

#### `task_create_dag`

```json
{
  "dag_key": "string",
  "title": "string",
  "description": "string, optional",
  "metadata": {
    "auto_handoff_phase1": "boolean, optional"
  },
  "schedule": "object",
  "nodes": [
    {
      "node_key": "string",
      "title": "string",
      "node_type": "string, optional",
      "assigned_to": "string, optional",
      "depends_on": ["string"],
      "command_ref": "string, optional",
      "execution": "object, optional"
    }
  ]
}
```

必填：`dag_key`, `title`, `schedule`。`nodes[]` 内最小必填为 `node_key`, `title`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:5-53`

#### `task_get_dag`

```json
{
  "dag_key": "string"
}
```

必填：`dag_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:69-78`

#### `task_update_node`

```json
{
  "dag_key": "string",
  "node_key": "string",
  "status": "pending | running | done | failed",
  "result": "string, optional"
}
```

必填：`dag_key`, `node_key`, `status`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:82-96`

#### `task_start_node`

```json
{
  "dag_key": "string",
  "node_key": "string"
}
```

必填：`dag_key`, `node_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:100-111`

### 5.3 Command / Prompt

#### `command_list`

```json
{
  "keyword": "string, optional"
}
```

定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:120-128`

#### `command_get`

```json
{
  "card_key": "string"
}
```

必填：`card_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:132-141`

#### `prompt_list`

```json
{
  "keyword": "string, optional"
}
```

定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:145-153`

#### `prompt_get`

```json
{
  "prompt_key": "string"
}
```

必填：`prompt_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:157-166`

### 5.4 Shared File

#### `shared_file_read`

```json
{
  "path": "string"
}
```

必填：`path`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:175-184`

#### `shared_file_write`

```json
{
  "path": "string",
  "content": "string"
}
```

必填：`path`, `content`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:188-199`

### 5.5 Workspace

#### `workspace_create_run`

```json
{
  "run_key": "string, optional",
  "dag_key": "string, optional",
  "source_root": "string",
  "created_by": "string, optional",
  "files": ["string"],
  "metadata": "object, optional"
}
```

必填：`source_root`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:208-227`

#### `workspace_get_run`

```json
{
  "run_key": "string"
}
```

必填：`run_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:231-241`

#### `workspace_list_runs`

```json
{
  "status": "string, optional",
  "dag_key": "string, optional",
  "limit": "number, optional"
}
```

定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:245-256`

#### `workspace_merge_run`

```json
{
  "run_key": "string",
  "updated_by": "string, optional",
  "dry_run": "boolean, optional",
  "delete_removed": "boolean, optional"
}
```

必填：`run_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:260-273`

#### `workspace_abort_run`

```json
{
  "run_key": "string",
  "updated_by": "string, optional",
  "reason": "string, optional"
}
```

必填：`run_key`。定义：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:277-289`

## 6. 最终结论

- V2 真正的 MCP 编排工具族不是来自 `go-agent-v2/internal/mcp/orch/`，而是由 `go-agent-v2/pkg/toolsdk/tools/resource.go:16-20`、`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:56-292`、`go-agent-v2/pkg/toolsdk/tools/orchestration.go:228-313` 定义，再由 `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-170` 注册进 MCP runtime。
- V2 当前可确认的编排族 MCP tool 共 20 个：5 个 orchestration、4 个 DAG/task、4 个 command/prompt、2 个 shared_file、5 个 workspace：`go-agent-v2/pkg/toolsdk/tools/resource_specs.go:56-292`、`go-agent-v2/pkg/toolsdk/tools/orchestration.go:228-313`。
- V2 中用户特别点名的 `prompt_create` / `prompt_update` / `prompt_delete`、`command_create` / `command_run` / `command_card_*`、`shared_file_list` 都不是已暴露的 MCP tool；它们至多对应到底层 store CRUD 能力：`go-agent-v2/pkg/toolsdk/tools/providers.go:178-199`、`go-agent-v2/internal/store/command_card.go:15-75`、`go-agent-v2/internal/store/prompt_template.go:15-73`、`go-agent-v2/internal/store/shared_file.go:18-46`。
- V3 当前 `cmd/mcp-orch` 还是空壳，现阶段没有任何已注册的 MCP orchestration family tool；虽然代码库已经有 orchestration / workspace 的 RPC handler 和 command/prompt/sharedfile 的 store 层，但都没有被组装进 `cmd/mcp-orch`：`cmd/mcp-orch/fx.go:5-12`、`internal/sidecar/orch/orchestration/rpc.go:15-77`、`internal/module/workspace/rpc.go:13-24`、`internal/store/commandcard/contract.go:10-15`、`internal/store/prompt/contract.go:10-13`、`internal/store/sharedfile/contract.go:9-11`。
