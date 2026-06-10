# Step 5-6：系统上下文图、容器图和组件图

## Step 5：系统上下文图

```mermaid
flowchart LR
  User["本地用户 / 操作者"]
  Reviewer["Reviewer / Release 负责人"]
  Desktop["Super-Dolphin Desktop Host\ncmd/agent-terminal"]
  UI["React 新 UI\nfrontend-app"]
  Orch["mcp-orch\nAgent/DAG/Cron 编排"]
  LSP["mcp-lsp\n代码导航与编辑工具"]
  IDA["mcp-ida\nIDA 集成"]
  Providers["Provider 后端\nCodex / Claude CLI / DreamExec"]
  DB["PostgreSQL\nmigrations + sqlc"]
  Logs["日志与指标\n/metrics + Observability + ELK"]
  GitHub["GitHub\nCI / Release"]
  FS["本地文件系统\nworkspace / shared files / skills"]

  User --> UI
  UI --> Desktop
  Desktop --> DB
  Desktop --> Providers
  Desktop --> Orch
  Desktop --> LSP
  Desktop --> IDA
  Desktop --> FS
  Orch --> DB
  Orch --> Providers
  Orch --> FS
  Desktop --> Logs
  Orch --> Logs
  Reviewer --> GitHub
  GitHub --> Desktop
```

### 上下文说明

- 用户主要通过 `frontend-app` 进入系统。
- 桌面宿主 `cmd/agent-terminal` 提供 Wails 桥、HTTP asset server、JSON-RPC dispatch、模块装配和 provider 适配。
- `mcp-orch` 是独立 peer，用于 agent lifecycle、DAG、cron、toolbridge 和资源工具。
- `mcp-lsp` 是独立 peer，用于代码级工具调用。
- PostgreSQL 是状态源，schema 由 migrations 管理，查询由 sqlc 生成。
- 观测由 `/metrics`、Observability RPC、本地日志和可选 ELK 组成。

## Step 6：容器图

```mermaid
flowchart TB
  subgraph Browser["桌面 WebView / Browser UI"]
    React["frontend-app\nReact + Vite + React Query"]
    Bridge["wailsBridge / backendApi\nCallAPI + WebSocket"]
  end

  subgraph Host["cmd/agent-terminal"]
    App["internal/app\nFx 装配和生命周期"]
    HTTP["internal/ui/wails\nHTTP server / assets / /metrics"]
    RPC["internal/platform/rpc\njrpc2 registry + Dispatch"]
    Modules["internal/module/*\nthread / prompt / skill / memory / cron / observability"]
    Provider["internal/provider/*\nCodex / Claude / unified"]
    Store["internal/store\nsqlc stores"]
  end

  subgraph Peers["MCP peers"]
    MCPOrch["cmd/mcp-orch\nDAG / agent / cron / tools"]
    MCPLSP["cmd/mcp-lsp\nfile / inspect / xref / grep / edit"]
    MCPIDA["cmd/mcp-ida"]
  end

  PG["PostgreSQL"]
  LocalFS["Workspace / Skills / Shared files"]
  Logs["Logs / Metrics / ELK"]

  React --> Bridge
  Bridge --> HTTP
  Bridge --> RPC
  App --> HTTP
  App --> RPC
  RPC --> Modules
  Modules --> Store
  Store --> PG
  Modules --> Provider
  Provider --> Peers
  MCPOrch --> PG
  MCPOrch --> LocalFS
  MCPLSP --> LocalFS
  HTTP --> Logs
  Modules --> Logs
```

## 组件图：桌面宿主

```mermaid
flowchart LR
  Main["cmd/agent-terminal/main.go"]
  RuntimeEnv["runtimeenv\npackaged/dev env"]
  AppRun["internal/app.RunDesktop"]
  Fx["Fx modules\ninternal/app/modules.go"]
  Config["platform/config"]
  DB["platform/db"]
  Bus["platform/bus"]
  RPC["platform/rpc"]
  UI["ui/wails"]
  Modules["business modules"]
  Provider["provider unified"]

  Main --> RuntimeEnv
  Main --> AppRun
  AppRun --> Fx
  Fx --> Config
  Fx --> DB
  Fx --> Bus
  Fx --> RPC
  Fx --> UI
  Fx --> Modules
  Fx --> Provider
```

组件边界：

- `cmd/agent-terminal/main.go` 只做进程角色、运行时环境和桌面入口调用。
- `internal/app/app.go` 负责前置检查、Wails 生命周期、logger 和 Fx app。
- `internal/app/modules.go` 是模块装配中心。注意桌面 app 不内嵌 orchestration module，编排由 `mcp-orch` 负责。

## 组件图：mcp-orch

```mermaid
flowchart TB
  OrchMain["cmd/mcp-orch"]
  AgentRPC["agent RPC\nlaunch / submit / stop / snapshot"]
  TaskRPC["task RPC\nDAG / node / run"]
  Tools["tools\nprompt / shared file / workspace / video / command"]
  Cron["cron integration"]
  Store["store + SQL"]
  ProviderSidecar["provider sidecar / agents"]

  OrchMain --> AgentRPC
  OrchMain --> TaskRPC
  OrchMain --> Tools
  TaskRPC --> Cron
  TaskRPC --> Store
  AgentRPC --> ProviderSidecar
  Tools --> Store
```

## 组件图：mcp-lsp

```mermaid
flowchart LR
  LSP["cmd/mcp-lsp"]
  Manager["manager / registry"]
  Tools["tools.go\nfile / inspect / xref / grep / structure / edit / completion"]
  Servers["language servers\ngopls / rust-analyzer / etc"]
  Repo["workspace source code"]

  LSP --> Manager
  LSP --> Tools
  Manager --> Servers
  Tools --> Repo
  Tools --> Servers
```

## 组件图：前端

```mermaid
flowchart TB
  App["frontend-app/src/App.jsx"]
  Pages["pages\nchat / prompts / workflows / skills / memory / files / observability / settings"]
  API["shared/api/backendApi.js\nRPC_METHODS"]
  Bridge["shared/api/wailsBridge.js"]
  State["entities + features\nstores / hooks"]
  Host["Wails CallAPI / WebSocket"]

  App --> Pages
  Pages --> API
  Pages --> State
  API --> Bridge
  Bridge --> Host
```
