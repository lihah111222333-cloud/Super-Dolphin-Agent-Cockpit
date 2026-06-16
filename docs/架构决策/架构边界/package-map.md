# Super-Dolphin 包职责地图

日期: 2026-06-16

本文是当前仓库的权威包职责索引，和 `docs/契约/onion-architecture-convention.md`、`docs/契约/modularity-convention.md` 一起使用。若历史计划、代码地图或审计文档与本文冲突，先以源码、测试、守卫脚本和本文为准。

## 总原则

依赖方向必须由外向内收敛: 入口和适配器可以调用应用能力，平台只能提供基础设施，业务模块不反向依赖入口、provider、MCP server 或 UI。任何包如果不能用一句话说明职责，就应该继续拆分或改名。

## 入口层

### `cmd/agent-terminal`

Responsibility: 桌面主进程入口，负责启动 Wails/桌面后端、读取启动参数、交给 `internal/app` 完成运行时装配。

Allowed imports: `internal/app`、`internal/platform/*` 中的启动必需能力、`pkg/logger`、标准库。

Forbidden imports: 业务模块具体 service、store 具体实现、provider 内部实现细节、UI 组件内部状态。

Public entry points: `main` 包只暴露进程入口，不向其他包提供可复用 API。

### `cmd/mcp-lsp`

Responsibility: LSP sidecar 进程根入口，负责 MCP/LSP peer 的启动、运行时参数、信号处理和顶层 wiring。

Allowed imports: `internal/sidecar/lsp/*`、`internal/platform/*`、`internal/mcpserver/runtime`、`internal/contract`、`internal/dto`、`pkg/logger`。

Forbidden imports: `internal/module/*` 的具体业务实现、`internal/provider/*`、`internal/ui/*`。

Public entry points: `main`、Fx module/provider、sidecar runtime wiring。根目录文件必须保持薄入口，不继续堆业务逻辑。

### `internal/sidecar/lsp/*`

Responsibility: 承载 LSP sidecar 内部库能力，包括 `manager`、`multilsp`、`protocol`、`tools`、`search`、`format`、`edit`、`installer` 和 middleware。

Allowed imports: 同一 sidecar 内的相邻包、`internal/sidecar/lsp/internal/*`、`internal/platform/*`、`internal/mcpserver/runtime`、`internal/contract`、`internal/dto`、`pkg/logger`。

Forbidden imports: `cmd/*`、`internal/sidecar/orch/*`、`internal/provider/*`、`internal/ui/*`、`internal/module/*`、宿主 `internal/store/*` 具体实现。

Notes: `cmd/mcp-lsp/*` 非根 Go 包不再允许存在；边界守卫会直接失败。`internal/sidecar/lsp` 是 sidecar 边界层，允许进程运行日志，但不得反向依赖宿主业务层。

### `cmd/mcp-orch`

Responsibility: Orchestration sidecar 进程根入口，负责 DAG/MCP 编排服务启动、独立 store 装配、任务调度 runner 和 peer 通信。

Allowed imports: `internal/sidecar/orch/*`、`internal/platform/*`、`internal/contract`、`internal/dto`、`pkg/logger`。

Forbidden imports: `internal/provider/*` 的宿主实现细节、`internal/ui/*`、桌面主进程私有状态。

Public entry points: `main`、Fx module/provider、sidecar runtime wiring。根目录文件只做进程级装配，业务流程进入 `internal/sidecar/orch/*`。

### `internal/sidecar/orch/*`

Responsibility: 承载 orchestration、DAG、wakeup、notify、sidecar store、MCP tools、workspace 等编排服务内部能力。

Allowed imports: 同一 sidecar 内相邻包、`internal/platform/*`、`internal/contract`、`internal/dto`、`pkg/*` 中明确允许的稳定 API。

Forbidden imports: `cmd/*`、`internal/sidecar/lsp/*`、`internal/ui/*`、`internal/module/*`、`internal/provider/*` 的宿主具体实现、桌面主进程装配对象、宿主 `internal/store/*` 具体实现。

Notes: `cmd/mcp-orch/*` 非根 Go 包不再允许存在；边界守卫会直接失败。`internal/sidecar/orch` 是 sidecar 边界层，允许进程运行日志；持久化实现保留在 `internal/sidecar/orch/store/*`，不能混入根 `internal/store/*` 的桌面主进程 store。

### `cmd/mcp-ida`

Responsibility: IDA sidecar 入口和工具服务装配。

Allowed imports: IDA sidecar 私有实现、`internal/platform/*`、`internal/contract`、`internal/dto`、`pkg/logger`。

Forbidden imports: 与 IDA 无关的 LSP/Orch sidecar 内部库、桌面 UI 内部状态、provider 私有实现。

## 装配层

### `internal/app`

Responsibility: 桌面后端 Fx 总装配点，聚合 platform、store、module、provider、mcpserver、ui 的运行时依赖。

Allowed imports: `internal/platform/*`、`internal/store/*`、`internal/module/*`、`internal/provider/*`、`internal/mcpserver/*`、`internal/ui/*`、`internal/contract`、`internal/dto`。

Forbidden imports: `cmd/*`、sidecar 根入口、测试夹具、dev scripts。`internal/app` 不写业务规则，不直接解析外部协议 payload。

Public entry points: Fx modules、启动 runner、桌面应用构造函数。

## 业务层

### `internal/module/*`

Responsibility: 业务用例和领域规则，包含 thread、turn、memory、skill、prompt、cron、dashboard、uistate 等上下文。

Allowed imports: `internal/contract`、`internal/dto`、必要的 `internal/platform` 叶子能力（日志仅通过 `internal/platform/logging`）、同包子目录、标准库。

Forbidden imports: `cmd/*`、`internal/provider/*`、`internal/mcpserver/*`、`internal/ui/*`、store 具体 SQL 类型、数据库驱动、直接日志实现（如 `pkg/logger`）。

Public entry points: Service 接口、命令/查询对象、结果 DTO、Fx `Module`。具体 service、缓存、worker 状态默认不导出。

Notes: 当前 `internal/module/*` 仍混合了 domain 与 application 职责，但持久化访问已收敛到 `internal/contract` port。新增或大规模迁移时，继续在业务包内定义/复用 port，在 `internal/app` 装配层绑定 store 实现，并拆出纯规则和 use case；不得重新让业务模块直接导入具体 store 包、扩大单包文件数或新增 store 反向耦合。

## 持久化防腐层

### `internal/store/*`

Responsibility: 将数据库、sqlc、文件或缓存模型转换为业务模块接口需要的 DTO/契约对象。

Allowed imports: `internal/contract`、`internal/dto`、`internal/platform/db`、生成的 sqlc 包、标准库和数据库驱动。文件型持久化适配器可额外依赖 `internal/platform/sharedfilefs`、`internal/platform/sharedfilepath`、`internal/platform/sharedfilegitignore`；仅 Fx provider 装配文件可读取 `internal/platform/config`。

Forbidden imports: `cmd/*`、`internal/provider/*`、`internal/mcpserver/*`、`internal/ui/*`、业务 use case 编排逻辑。store 不启动 goroutine，不拼装 service。

Public entry points: Store 接口实现、Fx provider、事务/查询方法。

## 平台层

### `internal/platform/*`

Responsibility: 配置、数据库池、runner、RPC、事件总线、metrics、observability、runtime env、toolbridge、文件安全等基础设施。

Allowed imports: 标准库、第三方基础设施库、`internal/contract` 或纯 DTO 中的稳定契约；日志适配通过 `internal/platform/logging` 承载。

Forbidden imports: `internal/module/*` 业务实现、`internal/provider/*`、`internal/mcpserver/*`、`internal/ui/*`、`cmd/*`。

Public entry points: Fx `Module`、基础设施接口、生命周期 runner、配置类型、健康/观测能力。

### `internal/platform/kernel`

Responsibility: 低依赖平台原语，承载字符串/上下文规范化、ID/路径辅助、重试和兼容 wrapper，替代历史根 `internal/util` 泛工具包。

Allowed imports: 标准库、`internal/contract`、纯 DTO、明确职责的 `internal/util/*` 叶子包、`pkg/logger` 兼容入口。

Forbidden imports: `internal/module/*` 业务实现、`internal/store/*`、`internal/provider/*`、`internal/mcpserver/*`、`internal/ui/*`、`cmd/*`。

Public entry points: `FirstNonEmpty`、`FirstTrimmed`、`ClampLimit`、`NonNilContext`、路径/ID/JSON/重试辅助函数。

### `internal/platform/mcpwire`

Responsibility: MCP JSON-RPC wire 层基础能力，包含 stdio transport framing 和 structuredContent 规范化，供 MCP server、sidecar 和 toolbridge client 复用。

Allowed imports: 标准库、低依赖日志兼容入口。

Forbidden imports: `internal/module/*` 业务实现、`internal/store/*`、`internal/provider/*`、`internal/mcpserver/*`、`internal/ui/*`、`cmd/*`。该包不注册工具、不启动服务、不承载 provider 私有行为。

Public entry points: `StdioTransport`、`NewStdioTransport`、`StructuredContentForToolResult`、`StructuredContentFromRaw`。

## 外部适配层

### `internal/mcpserver/*`

Responsibility: MCP server 协议适配、工具注册、请求/响应映射、通用 server bootstrap。

Allowed imports: `internal/module/*` 的公开接口、`internal/platform/*`、`internal/contract`、`internal/dto`、`pkg/logger`。

Forbidden imports: provider 私有实现、UI 内部状态、store SQL 类型。

### `internal/provider/*`

Responsibility: Codex、Claude CLI、Dream 执行器等外部宿主/模型运行时适配，把外部协议映射为内部契约。

Allowed imports: `internal/contract`、`internal/dto`、`internal/platform/*`、需要调用的业务公开接口、`pkg/logger`。

Forbidden imports: `internal/ui/*` 的展示状态、MCP server 内部工具实现、store SQL 类型。provider 之间避免横向调用，跨 provider 共性放入明确职责的子包。

### `internal/ui/*`

Responsibility: Wails/RPC 展示适配、前端可调用 API 映射、桌面 UI 状态桥接。

Allowed imports: `internal/module/*` 公开接口、`internal/platform/rpc`、`internal/contract`、`internal/dto`、`pkg/logger`。

Forbidden imports: provider 私有实现、store SQL 类型、sidecar 内部库。

## 契约与 DTO

### `internal/contract`

Responsibility: 跨模块稳定接口、错误、事件和 capability 契约。

Allowed imports: 标准库、轻量无副作用 DTO；历史兼容型事件订阅辅助可临时依赖 `pkg/logger`，新增契约不得扩大该例外。

Forbidden imports: platform、store、provider、mcpserver、ui、cmd。契约包不能偷偷承载运行时逻辑。

### `internal/dto/*`

Responsibility: 跨边界传输对象和协议字段定义。

Allowed imports: 标准库、必要的纯类型依赖。

Forbidden imports: 运行时服务、数据库驱动、logger、配置、外部协议 client。

## `pkg/*`

`pkg` 只允许承载确实需要被仓库外部或 sidecar 共享的稳定 API。不能把 `pkg` 当作绕过 `internal` 边界的 common 区。

Allowed current packages:

- `pkg/logger`: 当前日志实现的兼容入口。业务模块不得直接依赖它，应通过 `internal/platform/logging`；`pkg/logger` 仅保留给入口、sidecar、适配层和日志平台 facade 过渡使用。
- `pkg/dagmetrics`、`pkg/dreammetrics`、`pkg/skillmetrics`: 低依赖指标定义和记录辅助。不得依赖业务模块或适配器。

## 守卫和索引

### `.agents/skills/guarding-go-projects`

Responsibility: 仓库本地守卫脚本、基线和提交规则。当前 `.agents` 文件已被 Git 跟踪，不是本地临时目录；守卫脚本的修改必须随代码一起审查。

Rules:

- `.project-map/` 是生成索引，必须保持忽略且不提交。
- 修改 Go 代码后运行 `make guard-change`。
- 提交或交接前运行 `make guard-commit`。
- 每处理一个包，应删除该包已经修复的基线项，不得把新增违规写入基线来绕过守卫。

### `.project-map/`

Responsibility: 本地 Go AST 索引，辅助查找包和符号。

Rules:

- 查询 Go 符号前先用 `.agents/skills/mapping-go-projects/scripts/query_project_map.py`。
- 索引不是事实来源；修改或说明代码前必须打开源码文件。
- 索引输出不提交。
