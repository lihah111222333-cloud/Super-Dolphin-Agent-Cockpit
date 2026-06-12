# Super Dolphin 前后端接口对齐审计

日期：2026-06-12
工作区：`D:\project\Super-Dolphin-worktrees\screenshot-ui-redesign`
分支：`codex/screenshot-ui-redesign`
审计范围：`frontend-app` 当前 React/Vite 前端、Wails/HTTP 桥接层、后端 RPC handler 注册面。

## 结论

1. 生产前端当前可触达的 RPC/bridge 方法与后端注册面静态对齐，未发现方法名缺失。
2. 常规 facade/bridge 调用合计 107 个唯一方法；另外 `observability/frontend/ingest` 与 `ui/log` 是 telemetry 专用通道，通过 `runtime.Call.ByID` 调用。把这两个计入后，当前生产前端接口面是 109 个唯一 RPC 方法。
3. 后端对应 handler 均已注册；验证覆盖了 `backendApi` facade、`wailsBridge` 直连 helper、Wails `CallAPI`、WebSocket `/wails/ws` 与 `platform/rpc.Server.Dispatch`。
4. 运行中的桌面桥接服务可用：`http://127.0.0.1:4512/metrics` 返回 200，真实 WebSocket 调用 `ui/sidebar/get`、`ui/dashboard/get`、`observability/status`、`config/read`、`ui/projects/get` 全部成功。
5. 未把破坏性动作全部真实执行，例如 app update install、DAG delete、cron delete、prompt/skill 写入、thread/turn lifecycle mutation；这些按静态注册和单元测试覆盖确认接口存在，不在本次做副作用验证。

## 关键源码入口

| 层 | 文件 | 作用 |
| --- | --- | --- |
| 前端 RPC facade | `frontend-app/src/shared/api/backendApi.js:22` | 定义 `RPC_METHODS`，将页面能力收敛到命名 facade 方法。 |
| 前端桥接层 | `frontend-app/src/shared/api/wailsBridge.js:801` | `callAPI(method, params)` 统一调用 Wails/WS 后端。 |
| 前端 telemetry | `frontend-app/src/shared/api/wailsBridge.js:494` | 批量发送 `observability/frontend/ingest`。 |
| 前端日志 | `frontend-app/src/shared/api/wailsBridge.js:827` | `sendFrontendLogBatch` 通过 `ui/log` 上报。 |
| Wails 绑定 | `internal/ui/wails/binding.go:49` | `App.CallAPI` 接收前端方法名和 JSON 参数，并 dispatch 到 RPC server。 |
| HTTP/WS 桥 | `internal/ui/wails/http_server.go:32` | 注册 `/wails/ws` 到 RPC WebSocket handler。 |
| RPC server | `internal/platform/rpc/server.go:259` | 聚合注册 handler map。 |
| RPC dispatch | `internal/platform/rpc/server.go:269` | 本地执行指定 method。 |
| 严格 handler | `internal/platform/rpc/strict.go:10` | typed handler 的对象参数严格解码。 |

## 后端能力分组

| 能力域 | 前端使用数 | 后端注册数 | 说明 |
| --- | ---: | ---: | --- |
| app | 4 | 4 | 更新检查、下载、安装、安装最新版。 |
| approval | 1 | 1 | runtime approval 响应。 |
| config | 5 | 5 | 配置读取、LSP prompt hint、builtin tools 配置。 |
| cronjob | 8 | 8 | 定时任务 CRUD、启停、运行一次、运行记录。 |
| dashboard | 12 | 25 | 前端使用 DAG、日志、prompts、shared files；后端还保留 agent/system/query 等面板接口。 |
| observability | 6 | 7 | trace/recent/slow/error/status；另有 frontend ingest telemetry。 |
| prompt-assets | 1 | 1 | prompt 资源列表。 |
| prompt-intents | 4 | 5 | draft、dry-run、commit、discard；后端另有 e2e health。 |
| prompt-sections | 3 | 3 | section list/write/delete。 |
| prompts | 3 | 4 | get/write/delete；后端另有 prompts/list。 |
| skills | 10 | 19 | 本地 skill、resolution、summary suggest；后端还保留 remote/config/match 等接口。 |
| thread | 11 | 32 | start、messages、resolve、archive、config、compact、recover、rename 等；后端还保留 stop/fork/handoff/realtime 等接口。 |
| turn | 3 | 5 | start、interrupt、forceComplete；后端还保留 steer/output delta。 |
| ui | 36 | 41 | sidebar/state/projects/preferences/memory/code/video/window/dialog/file helpers。 |

后端还存在前端当前不直接使用的域：`command`、`feedback`、`memory`、`orchestration`、`skill`、`tools`、`workspace`。这些不是缺口，而是后端额外暴露给其他运行面或历史兼容面的 handler。

## 前端当前使用的方法清单

| 域 | 方法 |
| --- | --- |
| app | `app/update/check`, `app/update/download`, `app/update/install`, `app/update/installLatest` |
| approval | `approval/respond` |
| config | `config/builtinTools/read`, `config/builtinTools/write`, `config/lspPromptHint/read`, `config/lspPromptHint/write`, `config/read` |
| cronjob | `cronjob/create`, `cronjob/delete`, `cronjob/get`, `cronjob/list`, `cronjob/listRuns`, `cronjob/runOnce`, `cronjob/setEnabled`, `cronjob/update` |
| dashboard | `dashboard/dagApplyOps`, `dashboard/dagDelete`, `dashboard/dagDetail`, `dashboard/dagDispatchNode`, `dashboard/dagRun`, `dashboard/dagRuns`, `dashboard/dagStart`, `dashboard/dagTerminate`, `dashboard/dags`, `dashboard/logs`, `dashboard/prompts`, `dashboard/sharedFiles` |
| observability | `observability/error/list`, `observability/frontend/ingest`, `observability/recent/list`, `observability/slow/list`, `observability/status`, `observability/thread/recent`, `observability/trace/get` |
| prompt | `prompt-assets/list`, `prompt-intents/commit`, `prompt-intents/discard`, `prompt-intents/draft`, `prompt-intents/dry-run`, `prompt-sections/delete`, `prompt-sections/list`, `prompt-sections/write`, `prompts/delete`, `prompts/get`, `prompts/write` |
| skills | `skills/create`, `skills/local/delete`, `skills/local/importDir`, `skills/local/listFiles`, `skills/local/read`, `skills/local/write`, `skills/resolution_apply`, `skills/resolution_list`, `skills/resolution_preview`, `skills/summary/suggest` |
| thread | `thread/archive`, `thread/compact/start`, `thread/config/get`, `thread/config/set`, `thread/delete`, `thread/messages`, `thread/name/set`, `thread/recover`, `thread/resolve`, `thread/start`, `thread/unarchive` |
| turn | `turn/forceComplete`, `turn/interrupt`, `turn/start` |
| ui | `ui/code/locate`, `ui/code/open`, `ui/code/save`, `ui/copyText`, `ui/dashboard/get`, `ui/log`, `ui/memory/auto-dream/set-intent`, `ui/memory/entry/delete`, `ui/memory/entry/get`, `ui/memory/entry/merge`, `ui/memory/entry/upsert`, `ui/memory/get`, `ui/memory/shared-file/delete`, `ui/memory/shared-file/get`, `ui/memory/similarity/consolidate-all`, `ui/memory/similarity/consolidate-all/start`, `ui/memory/similarity/consolidate-all/status`, `ui/memory/similarity/ignore`, `ui/openNewWindow`, `ui/preferences/get`, `ui/preferences/getAll`, `ui/preferences/set`, `ui/projects/add`, `ui/projects/get`, `ui/projects/remove`, `ui/projects/setActive`, `ui/readDroppedTextFiles`, `ui/saveTextFile`, `ui/selectFiles`, `ui/selectProjectDir`, `ui/selectProjectDirs`, `ui/sharedFile/open`, `ui/sidebar/get`, `ui/state/get`, `ui/video/getApiKey`, `ui/video/setApiKey`, `ui/windowBootstrap/get` |

## 直连 bridge helper

这些方法不走 `backendApi` 的普通 `callBackend(RPC_METHODS.X)` wrapper，而是由 `wailsBridge` 直接调用 `callAPI` 或 Wails `runtime.Call.ByID`。后端均有对应 handler。

| 方法 | 前端调用点 | 后端 handler |
| --- | --- | --- |
| `ui/selectProjectDir` | `frontend-app/src/shared/api/wailsBridge.js:842` | `internal/ui/wails/rpc.go:131` |
| `ui/selectProjectDirs` | `frontend-app/src/shared/api/wailsBridge.js:873` | `internal/ui/wails/rpc.go:138` |
| `ui/selectFiles` | `frontend-app/src/shared/api/wailsBridge.js:884` | `internal/ui/wails/rpc.go:145` |
| `ui/readDroppedTextFiles` | `frontend-app/src/shared/api/wailsBridge.js:916` | `internal/ui/wails/rpc.go:152` |
| `ui/saveTextFile` | `frontend-app/src/shared/api/wailsBridge.js:948` | `internal/ui/wails/rpc.go:118` |
| `ui/sharedFile/open` | `frontend-app/src/shared/api/wailsBridge.js:969` | `internal/ui/wails/rpc.go:125` |
| `ui/copyText` | `frontend-app/src/shared/api/wailsBridge.js:978` | `internal/ui/wails/rpc.go:105` |
| `thread/resolve` | `frontend-app/src/shared/api/wailsBridge.js:1126` | `internal/module/thread/rpc.go:46` |
| `observability/frontend/ingest` | `frontend-app/src/shared/api/wailsBridge.js:494` | `internal/module/observability/rpc.go:119` |
| `ui/log` | `frontend-app/src/shared/api/wailsBridge.js:835` | `internal/ui/wails/rpc.go:128` |

## 代表性后端注册点

| 前端域 | 后端注册点 |
| --- | --- |
| config/uistate/projects/preferences/video | `internal/module/uistate/rpc.go:47`, `internal/module/uistate/config_rpc.go:73` |
| desktop UI helper | `internal/ui/wails/rpc.go:95` |
| dashboard/DAG/log/shared files | `internal/module/dashboard/rpc.go:245`, `internal/module/dashboard/rpc.go:360` |
| observability | `internal/module/observability/rpc.go:113` |
| prompt/prompt-intents/sections | `internal/module/prompt/service_surface.go:267` |
| memory/shared files | `internal/module/memory/ui_rpc.go:301`, `internal/module/memory/ui_rpc_mutations.go:57` |
| skills | `internal/module/skill/rpc.go:135`, `internal/module/skill/rpc.go:146` |
| cronjob | `internal/module/cron/rpc.go:85` |
| thread | `internal/contract/rpc_handler.go:49`, `internal/module/thread/rpc.go:26` |
| turn/approval | `internal/contract/rpc_handler.go:53`, `internal/module/turn/rpc.go:19`, `internal/module/turn/rpc.go:23` |
| app update | `internal/module/appupdate/rpc.go:13` |

## 验证记录

### 静态接口对比

方法：从生产前端递归扫描 `callBackend(RPC_METHODS.X)`、字符串字面量 `callBackend("...")`、字符串字面量 `callAPI("...")`，并补入 telemetry 专用 `runtime.Call.ByID` 方法；从后端生产 Go 文件扫描 `handler.Map`、`m["method"] = ...`、`contract.ThreadRPCStart` / `contract.TurnRPCStart` 等常量注册点。

结果：

- `RPC_METHODS` 常量：102 个。
- 普通生产 `callBackend` 使用点：101 个。
- 生产 `callAPI` 字面量直连点：8 个。
- telemetry 专用直连点：2 个。
- 当前生产前端唯一 RPC 方法数：109 个。
- 后端生产注册 handler 数：176 个。
- 前端方法缺失后端 handler：0 个。

### 前端契约测试

命令：

```powershell
cd D:\project\Super-Dolphin-worktrees\screenshot-ui-redesign\frontend-app
npm test -- src/shared/api/backendApi.contractMatrix.test.js src/shared/api/backendApi.surface.test.js src/pages/backendApiConsumer.surface.test.js src/shared/api/backendApi.test.js src/shared/api/wailsBridge.test.js
```

结果：5 个测试文件通过，75 个测试通过。

覆盖点：

- `RPC_METHODS` 每个 key 都有契约矩阵条目。
- 页面、feature、entity 不直接导入 raw `callAPI` / `callBackend`。
- 已迁移页面通过 service/facade 使用 backend API。
- `wailsBridge` telemetry、日志、runtime bridge 行为有单元覆盖。

### 后端核心包测试

命令：

```powershell
go test ./internal/platform/rpc ./internal/ui/wails ./internal/module/dashboard ./internal/module/observability ./internal/module/prompt ./internal/module/skill ./internal/module/cron ./internal/module/memory ./internal/module/thread ./internal/module/turn -count=1
```

结果：全部通过。

覆盖点：

- RPC dispatch、WS transport、strict handler。
- Wails desktop helper handlers。
- dashboard、observability、prompt、skill、cron、memory、thread、turn 相关 handler/service 测试。

### 运行时桥接验证

当前运行服务：

- `http://127.0.0.1:4512/metrics` 返回 200。
- TCP listener：`127.0.0.1:4512`，PID `36780`。
- TCP listener：`127.0.0.1:5175`，PID `34696`。

通过 `ws://127.0.0.1:4512/wails/ws` 发起 JSON-RPC 调用：

| 方法 | 结果摘要 |
| --- | --- |
| `ui/sidebar/get` | OK，返回 `agentRuntimeById`, `agents`, `groups`, `recent_turns` 等字段。 |
| `ui/dashboard/get` | OK，返回 `agents`, `commandCards`, `dags`, `memory`, `prompts` 等字段。 |
| `observability/status` | OK，返回 `enabled`, `schema_version`, `sink_events_written` 等字段。 |
| `config/read` | OK，返回 `approvalPolicy`, `config`, `cwd`, `model` 等字段。 |
| `ui/projects/get` | OK，返回 `active`, `projects`。 |

### 页面级 e2e

命令：

```powershell
cd D:\project\Super-Dolphin-worktrees\screenshot-ui-redesign\frontend-app
$env:SUPER_DOLPHIN_DESKTOP_UX_BASE_URL='http://127.0.0.1:4512'
$env:PLAYWRIGHT_CHROMIUM_EXECUTABLE='C:\Program Files\Google\Chrome\Application\chrome.exe'
npx playwright test tests/e2e/desktop-ux.spec.js --config=playwright.desktop.config.js
```

结果：`desktop new UI core UX smoke` 通过，1 个测试通过。

第一次尝试误用了环境变量名 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`，配置实际要求 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`；修正后通过。

## 验证阻塞与非本次问题

1. `bash ./scripts/test_with_guard.sh ...` 在 WSL wrapper 内失败，错误为未找到真实 Go 二进制，需要设置 `REAL_GO_BIN=/absolute/path/to/go` 或在该环境安装 Go。PowerShell 里的 `go version` 正常返回 `go1.26.3 windows/amd64`，所以本次改用 Windows `go test` 验证后端包。
2. 直接运行更宽的后端包集合时，`internal/module/uistate` 有既有 Windows 路径测试失败：`TestUIVideoSetAPIKeyPersistsWithoutExplicitSuperDolphinHome` 期望写入 `$HOME/Library/Application Support/Super Dolphin/video.env`，但 Windows 环境下未生成该路径。
3. 同一宽集合里 `internal/module/appupdate` 有既有安装 helper 测试失败：两个测试依赖 `/bin/sh`，一个 Windows installer fixture 不是有效 Windows 可执行文件。
4. 上述失败与“前端方法名是否有后端 handler”不是同一类问题；它们应单独作为 Windows 测试兼容性问题跟进。

## 风险与建议

1. 建议把本次静态抽取逻辑固化为测试或脚本，避免后续新增 `RPC_METHODS` 后忘记注册后端 handler。现有 `backendApi.contractMatrix.test.js` 已覆盖前端契约矩阵，但尚未直接扫描 Go handler 注册面。
2. 对破坏性接口建议保留当前策略：用单元测试验证参数和 handler，避免在普通审计中真实执行 `delete/install/start/dispatch` 等动作。
3. `observability/frontend/ingest` 与 `ui/log` 是特殊 telemetry 通道，应在契约矩阵注释里继续明确它们不是普通 facade `callBackend` 路径，避免被误判为未使用常量。
4. Windows 下的 `test_with_guard.sh` wrapper 和 appupdate/uistate 测试兼容性会影响后续完整验证，需要单独修复或在仓库文档中明确 Windows 验证命令替代路径。
