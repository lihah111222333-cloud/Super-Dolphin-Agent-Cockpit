# Step 8：核心接口和关键时序图

## 核心接口面

| 接口面 | 入口 | 说明 |
| --- | --- | --- |
| Wails/HTTP | `internal/ui/wails/http_server.go` | 提供 `/`、`/wails/ws`、`/metrics` |
| JSON-RPC dispatch | `internal/platform/rpc/server.go` | 模块注册 handler，前端通过 CallAPI 或 WebSocket 调用 |
| 前端 API | `frontend-app/src/shared/api/backendApi.js` | 汇总当前 UI 使用的 RPC method |
| MCP HTTP legacy | `internal/mcpserver/common/http_transport.go` | 保留 `POST /mcp`，当前工具执行优先使用 stdio sidecar |
| mcp-orch RPC | `cmd/mcp-orch/orchestration/rpc.go`、`cmd/mcp-orch/workspace/rpc.go` | agent、DAG、workspace 运行控制 |
| mcp-lsp tools | `cmd/mcp-lsp/tools.go` | file、inspect、xref、grep、structure、edit、completion |

## 前端 RPC 分组

| 分组 | 代表方法 |
| --- | --- |
| 配置 | `config/read`、`config/lspPromptHint/read`、`config/builtinTools/write` |
| UI 状态 | `ui/windowBootstrap/get`、`ui/state/get`、`ui/sidebar/get`、`ui/preferences/*` |
| 线程和 turn | `thread/start`、`thread/messages`、`turn/start`、`turn/interrupt`、`approval/respond` |
| Dashboard | `dashboard/prompts`、`dashboard/dags`、`dashboard/dagDetail`、`dashboard/sharedFiles` |
| DAG | `dashboard/dagStart`、`dashboard/dagDispatchNode`、`dashboard/dagTerminate`、`dashboard/dagApplyOps` |
| Cron | `cronjob/list`、`cronjob/create`、`cronjob/update`、`cronjob/runOnce`、`cronjob/setEnabled` |
| Prompt | `prompts/get`、`prompts/write`、`prompt-sections/list`、`prompt-intents/draft` |
| Skill | `skills/local/read`、`skills/local/write`、`skills/create`、`skills/summary/suggest` |
| Memory/File | `ui/memory/get`、`ui/memory/shared-file/get`、`ui/memory/shared-file/delete` |
| Observability | `observability/status`、`observability/trace/get`、`observability/slow/list`、`observability/error/list` |
| App update | `app/update/check`、`app/update/download`、`app/update/installLatest` |

## 关键时序图：本地启动

```mermaid
sequenceDiagram
  actor User as 用户
  participant Script as run-new-ui-desktop.sh
  participant Vite as frontend-app Vite
  participant DB as SQLite
  participant Peers as mcp-orch / mcp-lsp
  participant Host as cmd/agent-terminal
  participant Metrics as /metrics

  User->>Script: 执行启动脚本
  Script->>Script: 加载 .env 并设置 dev 环境变量
  Script->>DB: 检查本地数据目录可写
  Script->>Peers: 构建或确认 peer binaries
  Script->>Vite: 启动 127.0.0.1:5175
  Script->>Host: 启动后端宿主 127.0.0.1:4512
  Host->>DB: 打开数据库文件并运行迁移
  Script->>Metrics: 轮询 /metrics
  Metrics-->>Script: 返回成功
  Script-->>User: 输出可访问地址和日志路径
```

## 关键时序图：线程发起 turn

```mermaid
sequenceDiagram
  actor User as 用户
  participant UI as ChatPage
  participant API as backendApi
  participant RPC as platform/rpc
  participant Thread as module/thread + turn
  participant Provider as provider unified
  participant Store as internal/store
  participant DB as SQLite
  participant Events as event bridge

  User->>UI: 输入任务并提交
  UI->>API: 调用 thread/start 或 turn/start
  API->>RPC: CallAPI / JSON-RPC
  RPC->>Thread: Dispatch 到 handler
  Thread->>Store: 写入 thread / interaction / turn 状态
  Store->>DB: SQL 执行
  Thread->>Provider: 启动 provider 执行
  Provider-->>Thread: 返回事件、工具调用或完成状态
  Thread->>Events: 推送 UI 事件
  Events-->>UI: 刷新消息和状态
```

## 关键时序图：DAG 创建和调度

```mermaid
sequenceDiagram
  actor User as 用户或工具调用方
  participant UI as Workflow 页面 / MCP tool
  participant Orch as mcp-orch
  participant Store as DAG store
  participant DB as SQLite
  participant Agent as Provider agent
  participant Node as DAG node state

  User->>UI: 创建 DAG 或启动已有 DAG
  UI->>Orch: task_create_dag / dashboard DAG RPC
  Orch->>Store: 保存 task_dags 和 task_dag_nodes
  Store->>DB: 写入定义
  User->>UI: 启动 DAG
  UI->>Orch: task_start_dag 或 dashboard/dagStart
  Orch->>DB: 创建 task_dag_runs
  Orch->>Agent: launch_agent / submit
  Agent-->>Orch: 执行结果或中间事件
  Orch->>Node: task_update_node
  Node->>DB: 更新节点状态和输出
```

## 关键时序图：观测和故障定位

```mermaid
sequenceDiagram
  participant UI as 前端
  participant RPC as observability RPC
  participant Log as logger / system_logs
  participant Metrics as Prometheus metrics
  participant ELK as 本地 ELK
  participant Operator as 故障处理者

  UI->>RPC: observability/frontend/ingest
  RPC->>Log: 写入前端观测事件
  RPC->>Metrics: 记录计数或耗时
  Log->>ELK: Logstash tail .tmp/**/*.log
  Operator->>Metrics: GET /metrics
  Operator->>RPC: 查询 slow/error/recent/trace
  Operator->>ELK: Kibana 搜索日志
```

## 接口变更规则

1. 新增前端 RPC method 时，同时更新 `backendApi.js` 和对应 Go handler 测试。
2. 新增 MCP tool 时，明确 tool name、输入 schema、输出 schema、失败语义和权限边界。
3. 修改 thread、turn、DAG、cron、provider 行为时，必须补充回归测试或 smoke 清单。
4. RPC 不应吞错或静默返回默认值；配置缺失和状态不一致应 fail-fast。
5. 外部可见 method name 改名必须提供迁移说明，避免旧 UI 或脚本调用断裂。
