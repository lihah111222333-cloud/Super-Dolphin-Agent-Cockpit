# MCP 独立服务契约

## 1. 目标态

V3 的目标契约是：

- `cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 是独立二进制。
- `cmd/mcp-orch` 在 P8 终态下是完全独立的编排服务，自带本地 `orchestration/*`、`store/*`、`store/sqlc/*`、tool registry、manifest 和 stdio server。
- `cmd/mcp-orch` 运行时只共享 `internal/platform/config`、`internal/platform/db`、`internal/contract/*`、`internal/dto/*`。
- `cmd/mcp-orch` 不再 import `internal/module/*`、`internal/store/*`、`internal/store/sqlc/*`。
- `cmd/mcp-orch/orchestration/*` 是一次性迁移源，P8 完成后删除。
- 候选 host store 包是否删除，必须先做 `lsp_xref(references)` 审计；有宿主消费者时只能“复制到 `cmd/mcp-orch` + 保留内部版本”。

### 1.1 当前代码现状

当前仓库还没有达到上述目标态：

- `cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 目前仍以骨架或过渡态实现为主。
- `cmd/mcp-orch/orchestration/*` 仍在核心层。
- `internal/store/{taskdag,workspace,prompt,commandcard,sharedfile,binding}` 仍被宿主模块消费。

因此，本文定义的是目标契约，不代表当前代码已经完整落地。

## 2. 架构边界

| 位置 | 职责 | 禁止事项 |
| --- | --- | --- |
| `cmd/mcp-orch` | 组装 orchestration family MCP binary | 不得依赖宿主 bridge，不得回头 import `internal/module/*` / `internal/store/*` |
| `cmd/mcp-orch/orchestration/*` | 承载 `orchestration_*` 与 `task_*` 的本地业务逻辑 | 不得再回指 `cmd/mcp-orch/orchestration/*` |
| `cmd/mcp-orch/store/sqlc/*` | orch-family 自持 sqlc 查询与生成代码 | 不得 import `internal/store/sqlc/*` |
| `cmd/mcp-orch/store/*` | orch-family 自持 store 层 | 不得 import `internal/store/*` |
| `internal/platform/{config,db}` | 共享配置与 DB 连接基础设施 | 不得回灌 family-specific tool/runtime |
| `internal/contract/*` / `internal/dto/*` | 共享数据契约 | 不得承载 MCP transport / handler |
| `internal/module/*` / `internal/store/*` | 宿主 / UI 核心层 | 不得作为 `cmd/mcp-orch` 的运行时依赖；`cmd/mcp-orch/orchestration/*` 在 P8 后必须删除 |

补充规则：

- `cmd/` 与 `internal/` 同属模块根 `github.com/anthropic-ai/super-agent-v3`，因此 `cmd/mcp-orch` 合法 import `internal/*`；这只是 Go `internal` 规则允许，不代表放宽耦合边界。
- `cmd/mcp-orch` 的 MCP runtime、registry、manifest、stdio server 也属于本地组件，不再依赖共享 MCP common 层。
- `cmd/mcp-orch` 只允许通过本地 orchestration/store/sqlc 执行工具调用，不存在宿主 control plane 通道。

## 3. 依赖方向

### 3.1 `cmd/mcp-orch`

允许依赖：

- `internal/platform/config`
- `internal/platform/db`
- `internal/contract/*`
- `internal/dto/*`
- `cmd/mcp-orch/orchestration/*`
- `cmd/mcp-orch/store/sqlc/*`
- `cmd/mcp-orch/store/*`
- `cmd/mcp-orch/tools/*`
- `cmd/mcp-orch` 本地 registry / manifest / stdio server 包

禁止依赖：

- 其他 `cmd/*`
- `internal/app`
- `internal/ui/*`
- `internal/module/*`
- `internal/store/*`
- `internal/store/sqlc/*`
- `internal/platform/rpc` 的宿主 server、push、notification bridge
- 任何宿主 `handler.Map` / `New*Handlers`

### 3.2 单向依赖与 cleanup 规则

- `internal/module/*`、`internal/store/*`、`internal/platform/*`、`internal/contract/*`、`internal/dto/*` 都不得反向 import `cmd/mcp-*`。
- `cmd/mcp-orch/orchestration/*` 是 orch-family runtime owner。`LaunchAgent`、`StopAgent`、`ListAgents`、`GetReport`、`CreateDAG`、`GetDAG`、`UpdateNodeStatus` 都在 MCP binary 内部执行。
- `workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 都通过本地 `cmd/mcp-orch/store/*` 执行。
- `cmd/mcp-orch/orchestration/*` 必删；候选 `internal/store/*` 包只有在 xref 证明“宿主零消费者”时才能删。
- 当前基线下，`taskdag`、`binding`、`workspace`、`prompt`、`commandcard`、`sharedfile` 都存在宿主消费者，因此迁移策略是 copy+keep。

## 4. MCP 单通道模型

`cmd/mcp-orch` 采用单通道：

- `stdio` 是对外唯一通道，负责 tool list、tool call、manifest 与响应编码。
- `cmd/mcp-orch/orchestration/*` 承接 `orchestration_*` 与 `task_*` 的执行。
- `cmd/mcp-orch/store/*` 承接 `workspace_*`、`command_*`、`prompt_*`、`shared_file_*`。
- `cmd/mcp-orch/store/sqlc/*` 承接本地 DB 查询。
- 不存在宿主回跳通道；`cmd/mcp-orch` 的执行面、数据层和查询层都在本地闭环。

## 5. 守卫标准

`cmd/mcp-orch` 的 hand-written 代码默认纳入仓库统一守卫：

- 单文件 `<=400` 行
- 单函数 `<=80` 行
- 圈复杂度 `<=10`
- 包非测试文件 `<=15`

这里没有“复制旧 MCP 代码可豁免”的口子。P8 的策略是迁移并本地化，不是保留长期过渡依赖。

## 6. 落地检查清单

- MCP tool 的 schema / manifest / handler 壳是否只存在于 `cmd/mcp-orch`。
- `cmd/mcp-orch` 是否只依赖 `internal/platform/{config,db}`、`internal/contract/*`、`internal/dto/*` 与本地包。
- `cmd/mcp-orch` 是否本地持有 orchestration runtime、store 层与 sqlc 层。
- `cmd/mcp-orch` 是否完全不 import `internal/module/*`、`internal/store/*`、`internal/store/sqlc/*`。
- `cmd/mcp-orch/orchestration/*` 是否已在 P8 完成后删除。
- 每个候选 store 包是否都先做了 xref 审计，再决定删除还是保留。
- 新写代码是否满足仓库守卫。
