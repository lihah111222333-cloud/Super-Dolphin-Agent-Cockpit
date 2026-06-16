# 后端功能接口与前端对接扫描报告

日期：2026-06-03

## 1. 扫描范围与口径

本报告按当前仓库实际代码静态扫描生成，重点回答两个问题：

1. 后端对外提供哪些功能接口。
2. 当前 React/Vite 新 UI 是否已经和这些后端接口对接。

扫描范围：

- 当前前端：`frontend-app/`
- 桌面宿主与 UI RPC：`cmd/agent-terminal`、`internal/ui/wails`、`internal/platform/rpc`
- 后端业务 RPC：`internal/module/{thread,turn,uistate,dashboard,prompt,cron,memory,skill,feedback,observability}`
- 编排与 MCP peer：`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida`、`internal/platform/mcpcontrol`

不把 legacy Vue 页面当作当前前端对接面；`cmd/agent-terminal/frontend/vue-app` 只作为旧 UI/嵌入包路径参考。当前桌面新 UI 通过 `run-new-ui-desktop.sh` 启动 `frontend-app` Vite，再由 `cmd/agent-terminal` 代理 Wails/HTTP/RPC 能力。

## 2. 总体结论

- 当前 React 前端集中通过 `frontend-app/src/shared/api/backendApi.js` 声明 93 个 `RPC_METHODS`；另在 `frontend-app/src/shared/api/wailsBridge.js` 直接写了 8 个 `callAPI` / `METHOD_IDS.CALL_API` RPC 字面量，其中 6 个不在 `RPC_METHODS` 中。合并去重后，前端共扫描到 99 个 RPC 方法。
- 在后端 handler 注册文件中扫描到 187 个 JSON-RPC 方法名。前端声明或直接调用的 99 个 RPC 方法全部能在后端找到对应注册，未发现“前端调用但后端未注册”的 RPC 方法。
- 后端有一批接口没有被当前 React UI 直接调用，这不是缺口，主要属于 mcp-orch 编排 peer、MCP control plane、workspace run、legacy dashboard、thread 命令壳、测试/运维或 provider 内部回调。
- 少数原生能力不是通过 `backendApi.RPC_METHODS` 调用，而是走 Wails direct binding：`GetBuildInfo`、`SaveClipboardImage`、`SelectProjectDir`、`SelectFiles`。其中选择目录/文件在 direct binding 失败时会回退到 `ui/selectProjectDir`、`ui/selectFiles` RPC；`ui/buildInfo`、`ui/saveClipboardImage` RPC 后端存在但当前前端主路径未使用。

## 3. 后端接口传输模型

### 3.1 桌面 UI RPC

主调用链：

```text
frontend-app
  -> wailsBridge.callAPI(method, params)
  -> /wails/runtime.js runtime.Call.ByID(CALL_API, method, payload)
  -> internal/ui/wails.App.CallAPI
  -> internal/platform/rpc.Server.Dispatch
  -> Fx 聚合注册的 handler.Map
```

关键文件：

- `frontend-app/src/shared/api/wailsBridge.js`
- `frontend-app/src/shared/api/backendApi.js`
- `internal/ui/wails/binding.go`
- `internal/ui/wails/rpc.go`
- `internal/platform/rpc/server.go`
- `internal/platform/rpc/module.go`

浏览器调试入口：

- `internal/ui/wails/http_server.go` 同时提供静态资源和 `/wails/ws`。
- `/wails/ws` 由 `internal/platform/rpc/transport_ws.go` 把 WebSocket 桥接到同一组 jrpc2 handlers。

### 3.2 后端事件面

后端事件通过 `internal/ui/wails/bridge.go` 订阅事件总线，再发给前端 runtime event：

| 事件名 | 后端来源 | 前端订阅 | 作用 |
|---|---|---|---|
| `bridge-event` | `eventsurface.Bind(..., publish)` | `onBridgeEvent` | 标准 UI 事件、thread patch、memory/shared-files/prompts 等刷新事件 |
| `agent-event` | `bridge-event` 的兼容补发 | `onAgentEvent` | 兼容旧 agent 事件消费 |
| `files-dropped` | Wails window drop event | `onFilesDropped` | 文件拖拽到聊天/页面 |
| `app-will-quit` | Wails lifecycle | `onAppWillQuit` | 退出前 overlay/关闭流程 |

## 4. 当前 React UI 对接的后端 RPC 清单

### 4.1 Config / UI State / Projects / Preferences

后端入口：

- `internal/module/uistate/config_rpc.go`
- `internal/module/uistate/rpc.go`

| 方法 | 主要入参 | 功能 | 前端封装 |
|---|---|---|---|
| `config/read` | `{}` | 读取运行时配置、默认 provider/model/cwd 等 | `readConfig` |
| `config/lspPromptHint/read` | `cwd` | 读取项目 LSP prompt hint | `readLspPromptHint` |
| `config/lspPromptHint/write` | `cwd`, `hint` | 写入项目 LSP prompt hint | `writeLspPromptHint` |
| `config/builtinTools/read` | `cwd` | 读取内置工具开关 | `readBuiltinTools` |
| `config/builtinTools/write` | `cwd`, `id`, `enabled` | 写入内置工具开关 | `writeBuiltinTool` |
| `ui/state/get` | `cwd`, `threadId`, `includeDiff`, `knownDiffRevision` | 读取当前 thread/UI 状态 | `getThreadState` |
| `ui/sidebar/get` | `cwd` | 读取侧边栏 thread/project 状态 | `getSidebarState` |
| `ui/preferences/get` | `key`, `cwd?` | 读取单个或全部 preference | `getPreference` |
| `ui/preferences/getAll` | `cwd?` | 读取全部 preference | `getAllPreferences` |
| `ui/preferences/set` | `key`, `value`, `cwd?` | 写入 preference | `setPreference` |
| `ui/projects/get` | `cwd` | 读取项目列表与 active project | `getProjects` |
| `ui/projects/setActive` | `cwd`, `path` | 设置 active project | `setActiveProject` |
| `ui/projects/add` | `cwd`, `path` | 添加项目 | `addProject` |
| `ui/projects/remove` | `cwd`, `path` | 移除项目 | `removeProject` |

### 4.2 Wails 原生能力与代码预览

后端入口：

- `internal/ui/wails/rpc.go`
- `internal/ui/wails/binding.go`
- `internal/ui/wails/binding_native.go`
- `internal/ui/wails/code_preview.go`

| 方法/绑定 | 主要入参 | 功能 | 前端封装 |
|---|---|---|---|
| `ui/windowBootstrap/get` | `{}` | 读取一次性窗口 bootstrap snapshot | `getWindowBootstrap` |
| `ui/openNewWindow` | `cwd`, `group?`, `n?`, `snapshot?` | 打开新 Wails 窗口 | `openNewWindow` |
| `ui/code/locate` | `filePath`, `project?`, `projects?` | 在授权项目 scope 内定位文件候选 | `locateCodeFile` |
| `ui/code/open` | `filePath`, `line?`, `column?`, `project?` | 打开文件预览或编辑器 | `openCodeFile` |
| `ui/code/save` | `filePath`, `content`, `createNew?`, `project?` | 保存代码预览编辑内容 | `saveCodeFile` |
| `ui/copyText` | `text` | 原生剪贴板写文本 | `copyTextToClipboard` 的 native bridge 分支 |
| `ui/log` | `entries` 或单条日志字段 | 前端日志批量回传后端 logger/trace | `sendFrontendLogBatch` |
| `ui/selectProjectDir` | `defaultPath?` | 选择单个目录 | `selectProjectDir` fallback RPC |
| `ui/selectProjectDirs` | `defaultPath?` | 选择多个目录 | `selectProjectDirs` |
| `ui/selectFiles` | `defaultPath?` | 选择文件 | `selectFiles` fallback RPC |
| `ui/readDroppedTextFiles` | `files`, `targetId?` | 读取拖拽文本文件内容 | `readDroppedTextFiles` |
| `ui/saveTextFile` | `defaultPath`, `defaultFilename`, `content` | 选择目录并保存文本文件 | `saveTextFile` |
| Wails `GetBuildInfo` | direct binding | 获取版本/构建信息 | `getBuildInfo` |
| Wails `SaveClipboardImage` | direct binding, base64 | 保存剪贴板图片为临时 PNG | `saveClipboardImage` |
| Wails `SelectProjectDir` / `SelectFiles` | direct binding | 原生选择器快速路径 | `selectProjectDir` / `selectFiles` |

### 4.3 Thread / Turn / Chat

后端入口：

- `internal/module/thread/rpc.go`
- `internal/module/turn/rpc.go`
- `internal/contract/rpc_handler.go`

| 方法 | 主要入参 | 功能 | 前端封装/消费 |
|---|---|---|---|
| `thread/start` | `cwd`, `provider/modelProvider`, `model`, `effort`, `prompt_key`, `agent_key`, `defer_spawn?`, `toolSurfaceMode?` | 创建 thread，可延迟 spawn | `startThread`，chat/workflow/command/fork |
| `turn/start` | `cwd`, `threadId`, `prompt` 或 `input[]`, `attachments?` | 对 thread 发起一轮输入 | `startTurn` |
| `turn/interrupt` | `cwd`, `threadId`, `turnId`, `source?` | 中断运行中的 turn | `interruptTurn` |
| `thread/messages` | `threadId`, `limit?`, `before?` | 读取历史消息 | `getThreadMessages` |
| `thread/resolve` | `threadId` | 解析 thread 身份/快照 | `resolveThreadIdentity` |
| `thread/archive` | `threadId` | 归档 thread | `archiveThread` |
| `thread/unarchive` | `threadId` | 取消归档 | `unarchiveThread` |
| `thread/delete` | `threadId` | 删除 thread | `deleteThread` |
| `thread/config/get` | `threadId` | 读取 thread 运行配置 | `getThreadConfig` |
| `thread/config/set` | `threadId`, `model?`, `effort?` | 写入 thread 配置覆盖 | `setThreadConfig` |
| `thread/compact/start` | `cwd`, `threadId`, `args?` | 启动上下文压缩 | `compactThread` |
| `thread/recover` | `cwd`, `threadId` | 恢复 thread/session | `recoverThread` |
| `thread/name/set` | `threadId`, `name` | 重命名 thread | `renameThread` |

### 4.4 Dashboard / DAG / Logs / Shared Files

后端入口：

- `internal/module/dashboard/rpc.go`
- `internal/module/memory/ui_rpc_mutations.go`（shared file read/delete）

| 方法 | 主要入参 | 功能 | 前端封装/页面 |
|---|---|---|---|
| `ui/dashboard/get` | `cwd`, `page` | 读取页面级 dashboard 数据 | `getDashboardPage` |
| `dashboard/logs` | filter + `limit` | 查询系统/AI/UI 日志 | `listDashboardLogs` |
| `dashboard/prompts` | `cwd` | prompt dashboard fallback | `getDashboardPrompts` |
| `dashboard/sharedFiles` | `{}` | 列出 shared files/final output refs | `listSharedFiles` |
| `dashboard/dags` | `keyword?`, `status?`, `limit?` | DAG 列表 | `listDags` |
| `dashboard/dagDetail` | `dagKey` | DAG 详情与 nodes | `getDagDetail` |
| `dashboard/dagRuns` | `dagKey`, `status?`, `limit?` | DAG runs 列表 | `getDagRuns` |
| `dashboard/dagRun` | `runKey` | 单次 run 详情 | `getDagRun` |
| `dashboard/dagStart` | `dagKey`, `triggerSource?`, `idempotencyKey?` | 启动 DAG | `startDag` |
| `dashboard/dagTerminate` | `dagKey`, `runKey`, `reason?` | 停止 DAG run | `terminateDagRun` / `terminateDag` |
| `dashboard/dagDelete` | `dagKey` | 删除 DAG | `deleteDag` |
| `dashboard/dagApplyOps` | `dagKey`, `baseVersion`, `ops[]` | 应用 DAG 编辑 ops | `applyDagOps` |
| `ui/memory/shared-file/get` | `path` | 读取 shared file 内容 | `readSharedFile` |
| `ui/memory/shared-file/delete` | `path` | 删除 shared file | `deleteSharedFile` |

### 4.5 Prompt / Prompt Intent / Prompt Sections

后端入口：

- `internal/module/prompt/service_surface.go`

| 方法 | 主要入参 | 功能 | 前端封装/页面 |
|---|---|---|---|
| `prompt-assets/list` | `cwd` | 读取 prompt assets 页面模型 | `listPromptAssets` |
| `prompts/get` | `cwd`, `id` | 读取 prompt | `getPrompt` |
| `prompts/write` | `cwd`, `name`, `content?`, `scope?`, `tags?`, `match_when?` | 写入 prompt | `writePrompt` |
| `prompts/delete` | `cwd`, `id`, `scope?` | 删除 prompt | `deletePrompt` |
| `prompt-intents/draft` | `cwd`, `raw_input`, provider/model fields | AI 草拟 prompt intent | `draftPromptIntent` |
| `prompt-intents/commit` | `cwd`, `draft_key`, confirm fields | 提交 draft | `commitPromptIntent` |
| `prompt-intents/discard` | `cwd`, `draft_key` | 丢弃 draft | `discardPromptIntent` |
| `prompt-intents/dry-run` | `cwd`, `draft_key`, `question` | dry-run prompt intent | `dryRunPromptIntent` |
| `prompt-sections/list` | `cwd`, `prompt_id` | 列出 prompt sections | `listPromptSections` |
| `prompt-sections/write` | `cwd`, `prompt_id`, section fields | 写入 prompt section | `writePromptSection` |
| `prompt-sections/delete` | `cwd`, `prompt_id`, section id/path | 删除 prompt section | `deletePromptSection` |

### 4.6 Cron Job

后端入口：

- `internal/module/cron/rpc.go`

| 方法 | 主要入参 | 功能 | 前端封装/页面 |
|---|---|---|---|
| `cronjob/list` | `{}` | 列出定时任务 | `listCronJobs` |
| `cronjob/get` | `id` | 读取单个定时任务 | `getCronJob` |
| `cronjob/create` | job config | 创建定时任务 | `createCronJob` |
| `cronjob/update` | `id` + job config | 更新定时任务 | `updateCronJob` |
| `cronjob/delete` | `id` | 删除定时任务 | `deleteCronJob` |
| `cronjob/runOnce` | `id` | 手动运行一次 | `runCronJobOnce` |
| `cronjob/setEnabled` | `id`, `enabled` | 启停定时任务 | `setCronJobEnabled` |
| `cronjob/listRuns` | `job_id`, `limit?` | 查询运行历史 | `listCronJobRuns` |

### 4.7 Memory / Durable Memory

后端入口：

- `internal/module/memory/module.go`
- `internal/module/memory/ui_rpc.go`
- `internal/module/memory/ui_rpc_mutations.go`

| 方法 | 主要入参 | 功能 | 前端封装/页面 |
|---|---|---|---|
| `ui/memory/get` | `cwd` | 读取记忆中心快照 | `getMemorySnapshot` |
| `ui/memory/entry/get` | `cwd`, `target?`, `path` | 读取记忆条目 | `getMemoryEntry` |
| `ui/memory/entry/upsert` | `cwd`, `name`, `description`, `type`, `content`, `target?` | 新建/更新记忆条目 | `upsertMemoryEntry` |
| `ui/memory/entry/delete` | `cwd`, `target?`, `path` | 删除记忆条目 | `deleteMemoryEntry` |
| `ui/memory/auto-dream/set-intent` | `enabled` | 设置 auto-dream intent | `setMemoryAutoDreamIntent` |
| `ui/memory/entry/merge` | `cwd`, `targetA/pathA`, `targetB/pathB` | 合并相似记忆 | `mergeMemoryEntries` |
| `ui/memory/similarity/ignore` | `cwd`, `targetA/pathA`, `targetB/pathB` | 忽略相似记忆对 | `ignoreMemorySimilarity` |
| `ui/memory/similarity/consolidate-all` | `cwd` | 同步整合全部相似记忆 | `consolidateMemorySimilarities` |
| `ui/memory/similarity/consolidate-all/start` | `cwd`, provider/model fields | 异步启动相似记忆整合 | `startConsolidateMemorySimilarities` |
| `ui/memory/similarity/consolidate-all/status` | `cwd`, `jobId` | 查询异步整合状态 | `getMemoryConsolidationStatus` |

### 4.8 Skill

后端入口：

- `internal/module/skill/rpc.go`

| 方法 | 主要入参 | 功能 | 前端封装/页面 |
|---|---|---|---|
| `skills/local/read` | `cwd`, `path` | 读取 skill 文件 | `readSkill` |
| `skills/local/listFiles` | `cwd`, `dir` | 列出 skill 文件 | `listSkillFiles` |
| `skills/local/write` | `cwd`, `path`, `content`, `scope`, `personal_type?` | 写入本地 skill | `writeSkill` |
| `skills/local/importDir` | `cwd`, `paths[]`, `scope`, `personal_type?` | 导入 skill 目录 | `importSkillDirectories` |
| `skills/local/delete` | `cwd`, `name`, `scope`, `personal_type?` | 删除本地 skill | `deleteSkill` |
| `skills/create` | `cwd`, `name`, `content` | 创建项目级 skill | `createSkill` |
| `skills/summary/suggest` | `cwd`, skill metadata, provider/model fields | AI 生成 skill summary | `suggestSkillSummary` |
| `skills/resolution_list` | `cwd` | 列出 skill 冲突/解析项 | `listSkillResolutions` |
| `skills/resolution_preview` | `cwd`, resolution action fields | 预览解析动作 | `previewSkillResolution` |
| `skills/resolution_apply` | `cwd`, preview/action hash fields | 应用解析动作 | `applySkillResolution` |

### 4.9 Observability

后端入口：

- `internal/module/observability/rpc.go`
- `frontend-app/src/shared/api/wailsBridge.js` 的前端 trace flush

| 方法 | 主要入参 | 功能 | 前端封装/页面 |
|---|---|---|---|
| `observability/trace/get` | `traceId`, `limit?`, `includeTail?` | 查询单条 trace | `getObservabilityTrace` |
| `observability/thread/recent` | `threadId`, `limit?`, `includeTail?` | 查询 thread 最近 trace | `getObservabilityThreadRecent` |
| `observability/recent/list` | filters + `limit?` | 查询最近 trace | `listObservabilityRecent` |
| `observability/slow/list` | `component?`, `limit?` | 查询慢请求 | `listObservabilitySlow` |
| `observability/error/list` | `component?`, `limit?` | 查询错误请求 | `listObservabilityErrors` |
| `observability/status` | `{}` | 查询 observability 状态 | `getObservabilityStatus` |
| `observability/frontend/ingest` | `events[]` | 前端慢请求/错误 trace 上报 | `emitFrontendTraceEvent` 内部 flush |

## 5. 后端存在但当前 React UI 不直接调用的接口

这些接口在后端注册或通过 MCP 暴露，但不是当前 `frontend-app` 的直接 UI RPC 对接面。

### 5.1 Thread/Turn 扩展命令

入口：`internal/module/thread/rpc.go`、`internal/module/turn/rpc.go`

- `thread/list`
- `thread/loaded/list`
- `thread/read`
- `thread/resume`
- `thread/fork`
- `thread/handoff`
- `thread/stop`
- `thread/model/set`
- `thread/clear`
- `thread/personality/set`
- `thread/approvals/set`
- `thread/rollback`
- `thread/undo`
- `thread/backgroundTerminals/clean`
- `thread/mcp/list`
- `thread/skills/list`
- `thread/debugMemory`
- `thread/realtime/start`
- `thread/realtime/appendAudio`
- `thread/realtime/appendText`
- `thread/realtime/stop`
- `turn/steer`
- `turn/forceComplete`
- `approval/respond`

说明：一部分是 provider/session 命令壳或实时能力预留；当前 React UI 只对接聊天主路径、归档、配置、压缩、恢复、中断、历史读取和重命名。

### 5.2 Dashboard legacy/诊断接口

入口：`internal/module/dashboard/rpc.go`

- `dashboard/agentStatus`
- `dashboard/taskTraces`
- `dashboard/commandCards`
- `dashboard/skills`
- `dashboard/agent/detail`
- `dashboard/system/info`
- `dashboard/query`
- `dashboard/aiLogs`
- `dashboard/auditLogs`
- `dashboard/busLogs`
- `dashboard/aiLogs/recent`
- `dashboard/aiLogs/stats`

说明：当前 React UI 多数页面使用 `ui/dashboard/get` 或更具体的 DAG/logs/sharedFiles/prompts RPC；上述方法仍是后端可用接口，但不是当前集中 facade 的主路径。

### 5.3 Skill/Prompt/Memory 非 UI 主路径

入口：`internal/module/{skill,prompt,memory,feedback}`

- `command/exec`
- `skill/list`
- `skills/list`
- `skills/remote/list`
- `skills/remote/read`
- `skills/remote/export`
- `skills/remote/write`
- `skills/config/read`
- `skills/config/write`
- `skills/summary/write`
- `skills/match/preview`
- `prompts/list`
- `prompt-intents/e2e-health`（仅 `PROMPT_INTENT_E2E_DREAM_FIXTURE` 非空时注册）
- `memory/consolidate`
- `ui/memory/shared-file/cleanup-preview`
- `ui/memory/shared-file/cleanup-apply`
- `feedback/record`

说明：这些接口偏 CLI、远程 skill、测试 fixture、清理任务或非当前 UI 主路径。当前 React UI 已覆盖 prompt assets/get/write/delete、sections、intent draft/commit/discard/dry-run、memory center、skill local/resolution/create/summary suggest。

### 5.4 mcp-orch JSON-RPC peer 接口

入口：

- `cmd/mcp-orch/orchestration/rpc.go`
- `cmd/mcp-orch/workspace/rpc.go`

方法组：

- Agent 编排：`agent/launch`、`agent/submit`、`agent/submitPrompt`、`agent/stop`、`agent/list`、`agent/snapshot`、`agent/getState`、`agent/getReport`、`agent/reportEvent`、`agent/rememberReportRequest`
- DAG 编排：`task/dag/create`、`task/dag/get`、`task/dag/list`、`task/dag/delete`、`task/node/update`
- 编排报告：`orchestration/reportRuntime`、`orchestration/report`
- Workspace run：`workspace/run/create`、`workspace/run/get`、`workspace/run/list`、`workspace/run/merge`、`workspace/run/abort`、`workspace/run/files/list`、`workspace/run/file/get`

说明：这些是 mcp-orch peer / 编排侧车 RPC，不是 `frontend-app` 直接调用的桌面 UI RPC。

### 5.5 MCP control plane

入口：`internal/platform/mcpcontrol/handlers.go`

方法：

- `ctl/register`
- `ctl/heartbeat`
- `ctl/context`
- `ctl/event`
- `ctl/log`
- `ctl/approval/request`
- `ctl/hook/subscribe`
- `ctl/hook/resolve`
- `ctl/hook/pending`

说明：这些用于 MCP peer 注册、心跳、上下文、事件、日志、审批与 hook 协议。它们由 peer / toolbridge 使用，不由 React UI 直接调用。

### 5.6 MCP tools 面

#### mcp-lsp

入口：

- `cmd/mcp-lsp/tools.go`
- `cmd/mcp-lsp/fx.go`
- `internal/mcpserver/common/server.go`

传输：

- stdio MCP 默认开启。
- HTTP MCP 仅 peer mode 开启。
- 标准方法是 MCP `tools/list`、`tools/call`。

工具：

- `file`
- `inspect`
- `xref`
- `grep`
- `structure`
- `edit`
- `completion`

说明：`lsp_file`、`lsp_inspect` 等旧名称作为 alias 兼容到短名称。

#### mcp-orch

入口：`cmd/mcp-orch/tools/*`

工具组：

- Agent：`launch_agent`、`send_message`、`stop_agent`、`list_agents`、`get_agent_report`
- DAG：`task_create_dag`、`task_dag_apply_ops`、`task_update_node`、`task_dispatch_node`、`task_start_dag`、`task_terminate_dag`、`task_delete_dag`、`task_list_dags`、`task_get_dag`、`task_get_run`、`task_list_runs`
- Workspace：`workspace_create_run`、`workspace_get_run`、`workspace_list_runs`、`workspace_merge_run`、`workspace_abort_run`
- Prompt / command：`prompt_list`、`prompt_get`、`prompt_recall`、`command_list`、`command_get`
- Shared file / models：`shared_file_read`、`shared_file_write`、`shared_file_list`、`list_models`

说明：`cmd/mcp-orch/memory` 当前不注册 `memory_read` / `memory_write` 工具。

#### mcp-ida

入口：`cmd/mcp-ida`

当前能力：bootstrap-only；代码地图显示它没有本地 `tools/list` / `tools/call`、没有 schema/manifest/handler 映射。

## 6. 前端对接扫描结果

### 6.1 前端统一 API facade

入口：`frontend-app/src/shared/api/backendApi.js`

`backendApi.js` 的职责：

- 维护 `RPC_METHODS` 常量。
- 对入参做前端 fail-fast 校验，如 `cwd`、`threadId`、`id`、`path`、`enabled`、`content`。
- 把 camelCase/legacy 字段归一化为后端可接受字段，例如 `prompt_key`、`agent_key`、`job_id`、`model_provider`。
- 按功能域导出页面使用的函数，例如 `startThread`、`listDags`、`upsertMemoryEntry`、`writePrompt`、`listCronJobs`、`readSkill`。

### 6.2 Wails bridge

入口：`frontend-app/src/shared/api/wailsBridge.js`

`wailsBridge.js` 的职责：

- 动态加载 `/wails/runtime.js`。
- 通过 `runtime.Call.ByID(METHOD_IDS.CALL_API, method, payload)` 调用 `App.CallAPI`。
- 给每次 RPC 附加 `_aoClientKind`、`_aoClientRoute`、`_aoRequestId`、`_aoTraceparent` 等前端元字段；后端 `App.CallAPI` 会剥离 `_ao*` 元字段，避免 strict handler 因未知字段失败。
- 订阅 runtime events：`bridge-event`、`agent-event`、`files-dropped`、`app-will-quit`。
- 上报前端慢请求/错误 trace 到 `observability/frontend/ingest`。

### 6.3 页面/模块消费面

主要消费文件：

- Chat：`frontend-app/src/pages/chat/ChatPage.jsx`、`frontend-app/src/entities/client/model/useClientStore.js`
- Prompts：`frontend-app/src/features/prompts/PromptPageView.jsx`
- Workflows：`frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Tasks/Cron：`frontend-app/src/pages/tasks/TasksPage.jsx`
- Commands：`frontend-app/src/pages/commands/CommandsPage.jsx`
- Skills：`frontend-app/src/pages/skills/SkillsPage.jsx`
- Memory：`frontend-app/src/pages/memory/MemoryPage.jsx`
- Files：`frontend-app/src/pages/files/FilesPage.jsx`
- Settings：`frontend-app/src/pages/settings/SettingsPage.jsx`
- Observability：`frontend-app/src/pages/observability/ObservabilityPage.jsx`

## 7. 前后端接口一致性核对

静态核对脚本口径：

- 后端方法来源：指定 handler 注册文件中的 `handler.Map` key、`m[...] = ...` key，以及 `contract.ThreadRPC*`、`contract.TurnRPCStart`、`dto.Method*`、mcp-orch report 常量。
- 前端方法来源：`frontend-app/src/shared/api/backendApi.js` 的 93 个 `RPC_METHODS` 常量，以及 `frontend-app/src/shared/api/wailsBridge.js` 中直接传给 `callAPI` / `METHOD_IDS.CALL_API` 的 8 个字面量；其中 `thread/resolve`、`observability/frontend/ingest` 与常量重复，合并去重后为 99 个。

核对结果：

| 指标 | 数量 | 结论 |
|---|---:|---|
| 后端扫描到的 JSON-RPC 方法 | 187 | 覆盖桌面 UI、业务模块、mcp-orch、mcpcontrol |
| 前端扫描到的 RPC 方法 | 99 | 覆盖当前 React UI facade 与 bridge 内部上报 |
| 前端方法在后端找到注册 | 99 | 全部匹配 |
| 前端调用但后端未注册 | 0 | 未发现 |
| 后端注册但前端不直接调用 | 88 | 主要是 peer/control/legacy/internal 接口 |

前端已对接的 RPC 方法按前缀统计：

| 前缀 | 方法数 | 说明 |
|---|---:|---|
| `ui/*` | 35 | UI state、preferences、projects、code preview、memory、native RPC |
| `thread/*` | 11 | thread 创建、历史、归档、配置、恢复、重命名 |
| `dashboard/*` | 11 | DAG、logs、prompts、shared files |
| `skills/*` | 10 | local skill、create、resolution、summary suggest |
| `cronjob/*` | 8 | 定时任务 CRUD/运行/历史 |
| `observability/*` | 7 | trace 查询与前端 trace ingest |
| `config/*` | 5 | runtime config、LSP hint、builtin tools |
| `prompt-*` / `prompts/*` | 10 | prompt assets、prompt CRUD、sections、intent |
| `turn/*` | 2 | start / interrupt |

## 8. 注意事项与风险

- 本报告是静态扫描结果，确认“方法名注册是否匹配”；没有启动桌面应用做端到端 RPC 调用。
- 静态扫描不保证每个字段语义完全一致。参数 shape 已按 `backendApi.js` 前端校验和后端 typed params 做了人工抽样核对，未做逐字段自动 schema diff。
- `prompt-intents/e2e-health` 是环境变量门控测试接口，不应视为产品 UI 缺口。
- `cmd/mcp-orch` 和 `cmd/mcp-lsp` 是 peer/MCP 服务，和 `frontend-app` 的桌面 Wails RPC 面不同；它们未被 React UI 直接调用是预期行为。
- 当前 `ui/buildInfo`、`ui/saveClipboardImage` 后端 RPC 已注册，但前端主路径使用 direct Wails binding；这属于传输路径差异，不是后端缺失。

## 9. 验证记录

已执行：

- 阅读 `README.md`、`docs/doc/codemap/README.md`、`01-terminal-ui-go.md`、`01-terminal-ui-react.md`、`02-mcp-orch.md`、`03-mcp-lsp-ida.md`。
- 扫描后端 handler 注册：`rg` + 定向读取 `internal/module/*/rpc.go`、`internal/ui/wails/rpc.go`、`cmd/mcp-orch/*/rpc.go`、`internal/platform/mcpcontrol/handlers.go`。
- 扫描前端调用：`frontend-app/src/shared/api/backendApi.js`、`frontend-app/src/shared/api/wailsBridge.js` 及页面引用。
- 运行一次 Node 静态比对脚本，确认前端 99 个 RPC 方法全部有后端注册。

未执行：

- 未运行 Go/前端测试；本任务只新增接口扫描文档，没有改业务代码。
- 未启动 Wails 桌面端做运行时 RPC 冒烟；若后续要把本报告作为发布验收依据，建议补一次 `./run-new-ui-desktop.sh` 下的核心页面手动/自动 smoke。
