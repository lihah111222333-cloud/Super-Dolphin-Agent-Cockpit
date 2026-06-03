# frontend-app 与 cmd/agent-terminal 后端接口对接差异报告

日期：2026-06-03

## 1. 扫描范围与口径

本报告对照两个目录与后端接口的实际对接方式：

- `frontend-app/`：当前 React/Vite 新 UI。接口主入口是 `frontend-app/src/shared/api/backendApi.js` 与 `frontend-app/src/shared/api/wailsBridge.js`。
- `cmd/agent-terminal/`：桌面 Go/Wails 宿主与 legacy/package-embed 前端。Go 侧入口在 `cmd/agent-terminal/main.go`、`cmd/agent-terminal/frontend.go`，真正可与 `frontend-app` 做前端调用对照的是 `cmd/agent-terminal/frontend/vue-app/`。

统计口径：

- React 侧统计 `backendApi.js` 的 `RPC_METHODS` facade 和 `wailsBridge.js` 中直接通过 `callAPI` / `METHOD_IDS.CALL_API` 调用的 RPC 方法。
- legacy Vue 侧统计 `cmd/agent-terminal/frontend/vue-app` 生产代码中的 `callAPI(...)` 方法字符串，包括 `callAPI(archived ? 'thread/archive' : 'thread/unarchive', ...)` 这类动态表达式。
- 过滤测试、`node_modules`、`dist`、`.build-cache`、`.vite-cache`、playwright/test report 等非生产调用面。
- 本报告是静态扫描，不代表页面运行时一定覆盖到每个 facade 方法。

## 2. 总体结论

`cmd/agent-terminal` 不是另一套后端接口调用方，而是当前 React 新 UI 与 legacy Vue 共同使用的 Wails/RPC 宿主：

```text
前端页面
  -> Wails runtime Call.ByID(CALL_API, method, params)
  -> internal/ui/wails.App.CallAPI
  -> internal/platform/rpc.Server.Dispatch
  -> 后端 handler.Map
```

核心差异：

| 对照项 | `frontend-app` React 新 UI | `cmd/agent-terminal/frontend/vue-app` legacy Vue |
|---|---|---|
| RPC 方法数 | 99 | 83 |
| 共有 RPC | 81 | 81 |
| 独有 RPC | 18 | 2 |
| 原生 Wails ByID | 5 个，和 legacy 一致 | 5 个，和 React 一致 |
| 接口组织 | `backendApi.js` 集中枚举 `RPC_METHODS`，按领域拆 facade，并在前端做入参校验 | `services/api.js` 只做通用 bridge，业务 RPC 分散在页面、store、composable、少量 wrapper |
| 观测能力 | 新增前端 trace 队列与 `observability/*` 查询/上报接口 | 只有日志回传与事件采样，没有 `observability/*` facade |
| 项目状态 | 使用 `ui/projects/*` 读取、增删、切换项目 | 主要依赖本地 `projects` store、bootstrap 和目录选择，不直接调用 `ui/projects/*` |
| 线程归档 | React facade 中有 `archiveThread` / `unarchiveThread` | legacy 也调用 `thread/archive` / `thread/unarchive`，但以动态 ternary 写在 store helper 中 |
| 旧 turn/approval | 不直接调用 `approval/respond`、`turn/forceComplete` | legacy timeline 和 thread helper 仍调用这两个后端 RPC |

## 3. 目录职责边界

### 3.1 `frontend-app`

React 新 UI 的后端对接面集中在：

- `frontend-app/src/shared/api/backendApi.js`
  - `RPC_METHODS` 声明当前 UI facade 覆盖的 RPC 方法。
  - `createConfigProjectApi`、`createObservabilityApi`、`createMemoryApi`、`createPromptDagApi`、`createCronApi`、`createSkillApi`、`createThreadApi` 等按领域拆分。
  - 前端先做 `cwd`、`threadId`、`key`、`boolean`、列表、枚举等参数校验，再调用后端。
- `frontend-app/src/shared/api/wailsBridge.js`
  - 负责 Wails runtime 加载、`METHOD_IDS.CALL_API`、事件订阅、文件/目录选择、剪贴板、前端日志、前端 trace。
  - 额外直接调用 `ui/log`、`observability/frontend/ingest`、`ui/selectProjectDir`、`ui/selectProjectDirs`、`ui/selectFiles`、`ui/readDroppedTextFiles`、`ui/saveTextFile`、`ui/copyText`、`thread/resolve` 等桥接 RPC。

### 3.2 `cmd/agent-terminal`

Go 宿主：

- `cmd/agent-terminal/main.go` 只设置桌面进程环境并调用 `app.RunDesktop(frontendDistFS())`。
- `cmd/agent-terminal/frontend.go` 只嵌入 `frontend/dist`，这是 legacy/package-embed 资源路径。
- `internal/ui/wails/assets.go` 在 `VITE_DEV_URL` 存在时反代到 Vite dev server，否则使用嵌入 dist。
- `internal/ui/wails/binding.go` 的 `App.CallAPI` 是两套前端共用的 RPC 入口。

legacy Vue 前端：

- `cmd/agent-terminal/frontend/vue-app/services/api.js` 是通用 bridge。
- `cmd/agent-terminal/frontend/vue-app/services/skills-api.js`、`services/cron-api.js` 是技能和 cron 的薄 wrapper。
- 其他业务调用分散在 `pages/`、`stores/`、`composables/`、`components/`。

## 4. RPC 方法集合对照

### 4.1 React-only：18 个

这些方法只在 `frontend-app` 的当前 facade/bridge 中出现，legacy Vue 生产调用面未扫描到。

| 领域 | 方法 | 差异说明 | 后端注册位置 |
|---|---|---|---|
| Observability | `observability/trace/get` | React 观测页按 trace 查询 | `internal/module/observability/rpc.go` |
| Observability | `observability/thread/recent` | React 查询 thread 最近观测记录 | `internal/module/observability/rpc.go` |
| Observability | `observability/recent/list` | React 查询最近观测列表 | `internal/module/observability/rpc.go` |
| Observability | `observability/slow/list` | React 查询慢调用列表 | `internal/module/observability/rpc.go` |
| Observability | `observability/error/list` | React 查询错误列表 | `internal/module/observability/rpc.go` |
| Observability | `observability/status` | React 读取观测服务状态 | `internal/module/observability/rpc.go` |
| Observability | `observability/frontend/ingest` | React bridge 上报前端 trace 事件 | `internal/module/observability/rpc.go` |
| Dashboard | `dashboard/dags` | React facade 有独立 DAG 列表接口 | `internal/module/dashboard/rpc.go` |
| Dashboard | `dashboard/logs` | React facade 有统一日志查询接口 | `internal/module/dashboard/rpc.go` |
| Dashboard | `dashboard/sharedFiles` | React facade 有 shared files/final output 汇总接口 | `internal/module/dashboard/rpc.go` |
| Skill | `skills/create` | React 技能页提供创建技能 facade | `internal/module/skill/rpc.go` |
| Memory | `ui/memory/similarity/consolidate-all/start` | React 支持异步启动相似记忆合并 | `internal/module/memory/ui_rpc_mutations.go` |
| Memory | `ui/memory/similarity/consolidate-all/status` | React 支持查询异步合并状态 | `internal/module/memory/ui_rpc_mutations.go` |
| Preferences | `ui/preferences/getAll` | React 一次性读取偏好快照 | `internal/module/uistate/rpc.go` |
| Projects | `ui/projects/get` | React 直接读取项目列表/active project | `internal/module/uistate/rpc.go` |
| Projects | `ui/projects/setActive` | React 直接切换 active project | `internal/module/uistate/rpc.go` |
| Projects | `ui/projects/add` | React 直接添加项目 | `internal/module/uistate/rpc.go` |
| Projects | `ui/projects/remove` | React 直接移除项目 | `internal/module/uistate/rpc.go` |

### 4.2 Legacy-only：2 个

这些方法只在 `cmd/agent-terminal/frontend/vue-app` 生产调用面出现，React 新 UI 未扫描到直接 facade/调用。

| 方法 | legacy 调用点 | 作用 | 后端注册位置 |
|---|---|---|---|
| `approval/respond` | `components/timeline/useApprovalActions.js` | timeline 审批按钮提交同意/拒绝 | `internal/module/turn/rpc.go` |
| `turn/forceComplete` | `stores/thread-actions-helpers.js` | 强制完成/兜底结束 turn | `internal/module/turn/rpc.go` |

### 4.3 共有 RPC：81 个

两套前端都对接的后端 RPC 按领域分组如下。

| 领域 | 共有方法 |
|---|---|
| Config | `config/read`, `config/lspPromptHint/read`, `config/lspPromptHint/write`, `config/builtinTools/read`, `config/builtinTools/write` |
| UI shell / native RPC | `ui/windowBootstrap/get`, `ui/openNewWindow`, `ui/log`, `ui/selectProjectDir`, `ui/selectProjectDirs`, `ui/selectFiles`, `ui/readDroppedTextFiles`, `ui/saveTextFile`, `ui/copyText` |
| UI state / preferences | `ui/state/get`, `ui/sidebar/get`, `ui/preferences/get`, `ui/preferences/set` |
| Code preview | `ui/code/locate`, `ui/code/open`, `ui/code/save` |
| Dashboard / DAG | `ui/dashboard/get`, `dashboard/prompts`, `dashboard/dagDetail`, `dashboard/dagRuns`, `dashboard/dagRun`, `dashboard/dagStart`, `dashboard/dagTerminate`, `dashboard/dagDelete`, `dashboard/dagApplyOps` |
| Prompt | `prompt-assets/list`, `prompts/get`, `prompts/write`, `prompts/delete`, `prompt-intents/draft`, `prompt-intents/commit`, `prompt-intents/discard`, `prompt-intents/dry-run`, `prompt-sections/list`, `prompt-sections/write`, `prompt-sections/delete` |
| Cron | `cronjob/list`, `cronjob/get`, `cronjob/create`, `cronjob/update`, `cronjob/delete`, `cronjob/runOnce`, `cronjob/setEnabled`, `cronjob/listRuns` |
| Skill | `skills/local/read`, `skills/local/listFiles`, `skills/local/write`, `skills/local/importDir`, `skills/local/delete`, `skills/summary/suggest`, `skills/resolution_list`, `skills/resolution_preview`, `skills/resolution_apply` |
| Thread / turn | `thread/start`, `thread/messages`, `thread/resolve`, `thread/archive`, `thread/unarchive`, `thread/delete`, `thread/config/get`, `thread/config/set`, `thread/compact/start`, `thread/recover`, `thread/name/set`, `turn/start`, `turn/interrupt` |
| Memory | `ui/memory/get`, `ui/memory/entry/get`, `ui/memory/entry/upsert`, `ui/memory/entry/delete`, `ui/memory/auto-dream/set-intent`, `ui/memory/entry/merge`, `ui/memory/similarity/ignore`, `ui/memory/similarity/consolidate-all`, `ui/memory/shared-file/get`, `ui/memory/shared-file/delete` |

## 5. Wails direct binding 对照

两套前端使用的 Wails native ByID 集合一致：

| Binding ID | React 新 UI | legacy Vue | 说明 |
|---|---|---|---|
| `CALL_API` | 有 | 有 | 统一 RPC 入口，最终到 `App.CallAPI` |
| `GET_BUILD_INFO` | 有 | 有 | 获取构建信息 |
| `SAVE_CLIPBOARD_IMAGE` | 有 | 有 | 保存剪贴板图片 |
| `SELECT_FILES` | 有 | 有 | 原生文件选择 |
| `SELECT_PROJECT_DIR` | 有 | 有 | 原生目录选择 |

差异不在 native binding 数量，而在 bridge 行为：

- React `wailsBridge.js` 会为 RPC 生成 trace context，并按规则把前端 RPC、慢 patch/render、错误事件上报到 `observability/frontend/ingest`。
- legacy `services/api.js` 会把 `code -32001` / `server overloaded` 归一成 `Server overloaded; retry later.`，但不具备前端 trace 上报 facade。
- legacy 文件顶部明确要求系统能力必须走 Wails bridge，不允许 browser-native file/system fallback；React 侧复制文本存在 native RPC 失败后的浏览器剪贴板/`execCommand` UI fallback，文件和目录能力仍走 Wails/RPC。

## 6. 封装与参数约束差异

| 差异点 | React 新 UI | legacy Vue |
|---|---|---|
| 方法声明 | `RPC_METHODS` 集中枚举，调用从 `backendApi` facade 出口发起 | 没有全局 RPC 常量清单，业务方法多处分散调用 |
| 参数校验 | `requireCwd`、`requireThreadId`、`requireKey`、`requireBoolean`、payload normalizer 等前置校验较多 | 主要靠业务调用点手写 payload，统一 bridge 只校验 params 是对象 |
| 作用域处理 | 项目、偏好、cwd 多数通过 facade 显式要求或标准化 | legacy 通过 store/composable 的 `withPreferenceScope`、`withCwd`、项目 store 组合传入 |
| 错误处理 | `backendApi` 倾向 fail-fast；bridge 会记录 trace/log | `services/api.js` 对 overloaded 做统一归一化，日志回传失败会吞掉以避免递归风暴 |
| 可审计性 | 方法集合更容易从 `backendApi.js` 静态枚举 | 需要跨 `services`、`pages`、`stores`、`composables`、`components` 搜索 |
| 当前功能面 | 覆盖项目管理、全量偏好、观测、异步记忆合并、技能创建等新能力 | 保留审批响应、force complete 等旧交互入口 |

## 7. 后端注册验证

本次对照中的独有方法均能在后端找到注册点：

- React-only `observability/*`：`internal/module/observability/rpc.go`。
- React-only `ui/projects/*`、`ui/preferences/getAll`：`internal/module/uistate/rpc.go`。
- React-only `dashboard/dags`、`dashboard/logs`、`dashboard/sharedFiles`：`internal/module/dashboard/rpc.go`。
- React-only `ui/memory/similarity/consolidate-all/start`、`ui/memory/similarity/consolidate-all/status`：`internal/module/memory/ui_rpc_mutations.go`。
- React-only `skills/create`：`internal/module/skill/rpc.go`。
- legacy-only `approval/respond`、`turn/forceComplete`：`internal/module/turn/rpc.go`。

因此，本次静态对照没有发现“前端调用但后端未注册”的差异项。真正的差异是两套前端选择暴露/使用的后端功能面不同。

## 8. 结论与迁移关注点

1. 当前新 UI 的接口面比 legacy Vue 更偏“集中 facade + 可观测 + 项目状态显式管理”，React-only 的 18 个方法主要服务于新 UI 的观测、项目、偏好、shared files、DAG 和异步记忆合并能力。
2. legacy Vue 仍保留 `approval/respond` 与 `turn/forceComplete` 两个旧交互入口。如果 React 新 UI 需要完全替代 legacy timeline/turn 控制能力，需要确认这两个功能是否仍是产品需求，再决定是否迁移到 `backendApi.js`。
3. 两套前端共用同一套 Wails native binding 和 `App.CallAPI` 后端入口；`cmd/agent-terminal` Go 目录本身不是差异来源，差异主要来自 `frontend-app/src/shared/api/*` 与 `cmd/agent-terminal/frontend/vue-app/**` 的前端封装方式和调用覆盖面。
4. 后续若继续收敛接口，建议以 `frontend-app/src/shared/api/backendApi.js` 作为当前 UI 的单一 RPC 清单，并把 legacy-only 能力按需求决定“迁移、保留在 legacy、或废弃”。
