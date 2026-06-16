# 前端全量扫描测试结果

**日期:** 2026-06-08
**范围:** `frontend-app` 当前 React/Vite 新 UI、`backendApi.js`、`wailsBridge.js`、前端实际使用的 Go RPC handler 注册面
**分支:** `integration/frontend-api-test-plan-20260608`
**产物性质:** 扫描报告，不实现测试、不修改 CI、不改运行时代码

## 结论摘要

本项目的前端接口边界不是 HTTP URL，而是 Wails/JRPC：

```text
React page/store
  -> frontend-app/src/shared/api/backendApi.js
  -> frontend-app/src/shared/api/wailsBridge.js callAPI(method, params)
  -> Go handler.Map / StrictHandler
```

当前扫描结论：

- `frontend-app/src` 未发现直接 `fetch(`、`axios`、`XMLHttpRequest`、`dangerouslySetInnerHTML`、`innerHTML`、`eval(`、`new Function`、`document.cookie` 使用。
- `frontend-app/src/shared/api/backendApi.js` 是主要接口 facade：当前有 100 个 `RPC_METHODS` 常量、114 个导出 facade/native helper。
- `frontend-app/src/shared/api/backendApi.test.js` 已直接引用并断言 82 个 `RPC_METHODS` 常量，主链路 `thread/start`、`turn/start`、`turn/interrupt`、DAG、prompt、skill、memory 多数核心写接口已有 facade payload 测试。
- 仍有一个高风险接口绕过 1to1 规范：`SettingsPage.jsx` 直接调用 `callBackend('ui/video/getApiKey')` 和 `callBackend('ui/video/setApiKey')`，对应 Go handler 已存在，但前端没有 `RPC_METHODS` 常量、没有具名 facade、没有 `backendApi.test.js` 合约块。
- 构建、lint、测试、依赖审计均通过；生产构建有 Vite 大 chunk warning，最大 chunk 为 `593.66 kB`，另有 `537.17 kB` 主 chunk，需要作为性能风险跟踪。

## 扫描依据与命令证据

参考材料：

- `docs/审查报告/前端项目资料/前端测试文档/2026-06-08-frontend-rpc-1to1-interface-test-design.md`
- `/home/ai01@f666.com/文档/Obsidian Vault/前端项目“全量扫描”测试：必要测试与非必要测试.md`
- `docs/契约/jrpc2-convention.md`
- `README.md`
- `docs/doc/codemap/README.md`
- `docs/doc/codemap/01-terminal-ui-react.md`

执行过的关键命令：

| 命令 | 结果 | 说明 |
|---|---:|---|
| `git fetch origin main` | 通过 | 同步 `origin/main` |
| `git rebase origin/main` | 通过 | 当前分支 rebase 到 `8a032692` 后只 ahead 1 |
| `npm run lint` | 初次失败 | 本地未安装依赖，`eslint: not found` |
| `npm ci` | 通过 | 安装 332 packages，audit 333 packages，0 vulnerabilities |
| `npm run lint` | 通过 | `eslint .` exit 0 |
| `npm test` | 通过 | 19 files passed，623 passed，10 skipped |
| `npm run build` | 通过，有 warning | Vite chunk size warning，最大 chunk `593.66 kB` |
| `npm audit --audit-level=moderate` | 通过 | 0 vulnerabilities |

本轮没有运行 Go 测试，因为只读取 Go RPC 注册面，不修改 Go 代码。

## 项目入口与构建脚本

`frontend-app/package.json` 当前脚本：

| 脚本 | 命令 | 扫描结论 |
|---|---|---|
| `dev` | `vite --host 127.0.0.1 --port 5175 --strictPort` | 当前新 UI dev server |
| `desktop:dev` | 通过 `VITE_DEV_URL` 启动 `cmd/agent-terminal` | 桌面宿主对接当前 React UI |
| `desktop:hot` | `./run-new-ui-desktop-hot.sh` | HMR + Go backend restart |
| `build` | `vite build` | 已通过，有大 chunk warning |
| `lint` | `eslint .` | 已通过 |
| `test` | `vitest run` | 已通过 |

缺口：

- 没有 `typecheck` 脚本；当前是 JS/JSX 项目，不存在 TS 类型检查准入。
- 没有 `format:check` 脚本；格式准入主要依赖现有 lint/人工 diff discipline。
- 没有前端 bundle size 阻断脚本；当前只有 Vite warning。

## 接口入口清单

### 统一入口

| 层级 | 文件 | 当前事实 | 风险 |
|---|---|---|---|
| RPC 常量/facade | `frontend-app/src/shared/api/backendApi.js` | `RPC_METHODS` 从 `config/read` 到 `approval/respond` 覆盖 100 个方法；`createBackendApi()` 组合 config、app update、observability/memory、prompt/DAG、cron、code、skill、thread、native helper | 低，入口清晰 |
| 桥接层 | `frontend-app/src/shared/api/wailsBridge.js` | `callAPI(method, params)` 负责 normalize、trace、runtime invoke、失败继续抛出 | 中，需要避免页面绕过 facade |
| 页面/store 消费 | `frontend-app/src/pages/**`、`frontend-app/src/entities/client/model/useClientStore.js` | 页面和 store 基本导入 `backendApi.js` 的具名方法 | 低到中 |
| 例外直连 | `frontend-app/src/pages/settings/SettingsPage.jsx` | `VideoSettingsCard` 直接调用 `callBackend('ui/video/getApiKey')`、`callBackend('ui/video/setApiKey')` | 高 |

### HTTP 入口扫描

| 扫描项 | 结果 |
|---|---|
| `fetch(` | 未发现于 `frontend-app/src` |
| `axios` | 未发现 |
| `XMLHttpRequest` | 未发现 |
| `request`/HTTP client | 未发现独立 HTTP client |
| `callAPI(` 产品代码直连 | 仅 `shared/api` 内部使用；页面/store 没有直接 import bridge `callAPI` |
| `callBackend(` 产品代码直连 | `SettingsPage.jsx` 视频 API Key 区域存在字符串 RPC 直连 |

### 1to1 接口缺口矩阵

字段含义：

- `RPC method`: 当前应替代 HTTP method/path 的契约标识。
- `统一入口`: 是否通过 `RPC_METHODS -> backendApi facade`。
- `L1`: 是否在 `backendApi.test.js` 中直接使用 `RPC_METHODS.X` 做 facade payload 断言。
- `Mock`: 当前主要使用 `vi.fn()` mock `callAPI` 或 mock `backendApi.js` exports，不适用 MSW。
- `Schema`: 是否有运行时 response schema 校验。当前主要是 facade/page normalization，不是 Zod/OpenAPI。

| 模块 | RPC method / facade | 统一入口 | L1 | Mock | Schema | 风险 | 建议补齐 |
|---|---|---:|---:|---:|---:|---:|---|
| Video settings | `ui/video/getApiKey`、`ui/video/setApiKey` | 否 | 否 | 页面测试有卡片入口，但缺 facade 合约 | 否 | P0 | 加入 `RPC_METHODS`、`readVideoApiKey`/`writeVideoApiKey` facade、`backendApi.test.js` success/fail-fast/不泄露 payload 测试 |
| Thread/turn 主链路 | `thread/start`、`turn/start`、`turn/interrupt`、`turn/forceComplete` | 是 | 是 | `createBackendApi({ callAPI: vi.fn() })` | 部分 payload normalization | P0 | 保持现状；新增 L0 静态 guard 防止新增直连 |
| DAG 写链路 | `dashboard/dagStart`、`dashboard/dagDispatchNode`、`dashboard/dagTerminate`、`dashboard/dagDelete`、`dashboard/dagApplyOps` | 是 | 是 | `vi.fn()` + 页面 mock | 部分 payload normalization | P0 | 后续补 L3 Go dispatch samples |
| Prompt 写链路 | `prompts/write`、`prompts/delete`、`prompt-intents/*` | 是 | 是 | `vi.fn()` | 部分 payload normalization | P0 | 保持 P0，补 response shape/错误路径 |
| Prompt sections | `prompt-sections/list/write/delete` | 是 | 否 | 页面层未形成清晰覆盖 | 否 | P1 | 补 `backendApi.test.js` 合约块，尤其 `write/delete` |
| Skill 写链路 | `skills/create`、`skills/local/write`、`skills/local/delete`、`skills/local/importDir`、`skills/resolution_apply` | 是 | 是 | `vi.fn()` + page tests | 部分 payload normalization | P0 | 后续补 L3 Go dispatch samples |
| Memory 写链路 | `ui/memory/entry/upsert/delete/merge`、similarity consolidate/ignore | 是 | 是 | `vi.fn()` + page tests | 部分 payload normalization | P0 | 保持现状；补关键失败响应展示 |
| Project context | `ui/projects/get/setActive/add/remove` | 是 | 否 | store/page 侧有消费 | 否 | P1 | 补 facade payload 与 cwd/path fail-fast 测试 |
| Preferences/config | `ui/preferences/*`、`config/read`、`config/*` | 是 | 部分缺失 | Settings page tests 较多 | 否 | P1 | 补缺失常量的 L1；保留页面级 provider/config 测试 |
| Thread read/history | `thread/messages`、`thread/resolve` | 是 | 否 | `useClientStore` history tests 覆盖消费面 | 部分 normalization | P1 | 补 L1 payload 和 response pagination 约束 |
| UI bootstrap/sidebar/state | `ui/windowBootstrap/get`、`ui/sidebar/get`、`ui/state/get`、`ui/dashboard/get` | 是 | 否 | App/store tests 覆盖启动链 | 部分 normalization | P1 | 补 L1 smoke contract，避免启动 payload drift |
| Observability | `observability/*` | 是 | 是 | `vi.fn()` + page tests | 部分 page normalization | P1 | 保持，补 trace query 错误样例 |
| App update | `app/update/*` | 是 | 是 | `vi.fn()` + settings page tests | 否 | P2 | 低风险，现状可接受 |
| Native helpers | `selectFiles`、`saveTextFile`、`openSharedFile`、clipboard | 经 facade native helper | 部分 | bridge helper tests | 否 | P2 | 保持，新增时同步 bridge tests |

`backendApi.test.js` 尚未直接引用的 `RPC_METHODS` 常量：

```text
CONFIG_READ
UI_WINDOW_BOOTSTRAP_GET
UI_STATE_GET
UI_SIDEBAR_GET
UI_LOG
UI_PROJECTS_GET
UI_PROJECTS_SET_ACTIVE
UI_PROJECTS_ADD
UI_PROJECTS_REMOVE
UI_PREFERENCES_GET
UI_PREFERENCES_GET_ALL
UI_PREFERENCES_SET
UI_DASHBOARD_GET
PROMPT_SECTIONS_LIST
PROMPT_SECTIONS_WRITE
PROMPT_SECTIONS_DELETE
THREAD_MESSAGES
THREAD_RESOLVE
```

注意：这些不是全部未测试功能。它们只是“未在 `backendApi.test.js` 里直接通过 `RPC_METHODS.X` 做 L1 facade 断言”的方法；部分已经有页面/store 测试间接覆盖。

## 后端 RPC 注册面对照

| 模块 | 代表注册文件 | 前端相关方法 |
|---|---|---|
| Thread | `internal/module/thread/rpc.go`、`internal/contract/rpc_handler.go` | `thread/start`、`thread/messages`、`thread/resolve`、`thread/config/*`、`thread/archive`、`thread/delete` |
| Turn | `internal/module/turn/rpc.go`、`internal/module/turn/rpc_types.go` | `turn/start`、`turn/interrupt`、`turn/forceComplete` |
| UI state/config/projects/video | `internal/module/uistate/rpc.go`、`internal/module/uistate/config_rpc.go` | `ui/state/get`、`ui/sidebar/get`、`ui/preferences/*`、`ui/projects/*`、`ui/video/*`、`config/*` |
| Wails native UI | `internal/ui/wails/rpc.go` | `ui/code/*`、`ui/copyText`、`ui/select*`、`ui/saveTextFile`、`ui/windowBootstrap/get` |
| Prompt | `internal/module/prompt/service_surface.go` | `prompts/*`、`prompt-assets/list`、`prompt-sections/*`、`prompt-intents/*` |
| Skill | `internal/module/skill/rpc.go` | `skills/local/*`、`skills/create`、`skills/resolution_*`、`skills/summary/suggest` |
| Memory | `internal/module/memory/ui_rpc.go`、`internal/module/memory/ui_rpc_mutations.go` | `ui/memory/*`、`ui/memory/shared-file/*` |
| Dashboard/DAG/shared files | `internal/module/dashboard/rpc.go` | `dashboard/dags`、`dashboard/dag*`、`dashboard/sharedFiles` |
| Observability | `internal/module/observability/rpc.go` | `observability/*` |
| App update | `internal/module/appupdate/rpc.go` | `app/update/*` |
| Cron | `internal/module/cron/rpc.go` | `cronjob/*` |

## 页面与路由矩阵

当前不是 `react-router` 配置文件，而是 `App.jsx` 内的 page id/path 映射。

| Route | Page | 是否需要登录 | 权限要求 | 接口依赖 | 高风险操作 | 已有测试 | 建议类型 | 优先级 |
|---|---|---:|---:|---:|---:|---:|---|---:|
| `/`、`/chat` | `ChatPage.jsx` | 否 | 项目 cwd / thread 状态 | 是 | 发起 thread/turn、停止、强制完成、文件编辑保存 | 是 | unit + integration + smoke | P0 |
| `/skills` | `SkillsPage.jsx` | 否 | cwd / scope | 是 | 新建、编辑、删除、导入、解决冲突 | 是 | integration + facade contract | P0 |
| `/prompts` | `PromptPage.jsx` / `PromptPageView.jsx` | 否 | cwd / scope | 是 | prompt 写入、删除、intent commit/discard | 是 | integration + facade contract | P0 |
| `/dags`、`/workflows` | `WorkflowPage.jsx` | 否 | cwd / DAG version | 是 | run/stop/delete/schedule/edit/apply ops | 是 | integration + selected L3 | P0 |
| `/memory`、`/memory-center` | `MemoryPage.jsx` | 否 | cwd / memory target | 是 | upsert/delete/merge/ignore/consolidate | 是 | integration + facade contract | P0 |
| `/files`、`/shared-files` | `FilesPage.jsx` | 否 | shared file path | 是 | open/export/delete/continue chat | 是 | integration + bridge helper | P1 |
| `/observability` | `ObservabilityPage.jsx` | 否 | trace/log filters | 是 | 查询/复制 trace | 是 | unit + integration | P1 |
| `/settings` | `SettingsPage.jsx` | 否 | provider/config/project scope | 是 | provider 配置、内置工具、LSP prompt、video API key | 是，但 video RPC 合约缺失 | integration + facade contract | P0/P1 |

未发现专用 404/403 页面；未知路径会回落到默认 `chat` 页面语义。当前桌面应用也没有传统登录态/公开页/受保护页分层。

## 表单与高风险操作扫描

| 文件 | 表单/操作 | 字段/输入 | 提交 RPC/facade | 当前测试 | 风险 | 建议 |
|---|---|---|---|---|---:|---|
| `ChatPage.jsx` | Composer 发送 | text、attachments、model/effort、fork files | `startThread`、`startTurn`、`saveCodeFile` | App/Chat/store tests | P0 | 保持；补桌面 L4 smoke |
| `PromptPageView.jsx` | Prompt editor | name、description、whenToUse、content、enabled、agentType、priority、match_when JSON、tags | `writePrompt`、`deletePrompt`、intent APIs | PromptPageView/backendApi tests | P0 | 补 prompt-sections L1 |
| `WorkflowPage.jsx` | DAG run/stop/schedule/edit | dagKey、runKey、nodeKey、assignee、cron/time、node config | DAG facade group | Workflow/App tests | P0 | 补 L3 dispatch samples |
| `MemoryPage.jsx` | Memory editor/similarity | target、path、name、description、type、content | memory facade group | Memory/App/backendApi tests | P0 | 补失败响应回显矩阵 |
| `SkillsPage.jsx` | Skill create/edit/import/delete | name、content、scope、path、import dirs、resolution action | skill facade group | Skills/App/backendApi tests | P0 | 补外部导入失败矩阵 |
| `FilesPage.jsx` | Shared file operations | path、content、category/filter | shared-file facade/native helper | Files/App/backendApi tests | P1 | 保持 path fail-fast |
| `ObservabilityPage.jsx` | Trace search form | status、component、method/keyword、limit | observability facade | Observability/backendApi tests | P1 | 补边界 limit/empty trace |
| `SettingsPage.jsx` | Provider/config forms | provider、model、effort、sandbox roots、thresholds、LSP prompt、builtin tools | preferences/config facade | Settings tests | P1 | 补缺失 L1 常量 |
| `SettingsPage.jsx` | Video API Key | API key password input | `callBackend('ui/video/*')` | 页面卡片有测试入口，但缺 1to1 contract | P0 | 必须收口到 facade 并测试 |

高风险字段：

- API Key：`SettingsPage.jsx` 的 SiliconFlow API Key，输入为 password，保存后清空本地输入并显示 masked 值。
- JSON：Prompt `match_when JSON`，需要继续保持非法 JSON 的 fail-fast/错误展示。
- 路径：cwd、shared file path、skill path、writable/readable roots、code file path。
- DAG 版本/节点：`dagKey`、`baseVersion`、`nodeKey`、`runKey`，需要避免 stale run/detail 解锁写操作。

## 状态管理扫描

| 工具 | 文件 | 状态内容 | 持久化 | 风险 | 当前测试 |
|---|---|---|---|---:|---|
| Zustand | `frontend-app/src/entities/client/model/useClientStore.js` | active page/thread/project、threads、timeline、runtime activity、warnings、log level、composer、preferences | `agent-orchestrator.log.level` | P0 | `useClientStore.test.js`、`App.test.jsx` |
| React Query | `App.jsx`、pages | dashboard/memory/DAG/skills/files/observability 查询缓存 | 内存缓存 | P1 | page tests + query invalidation tests |
| localStorage | `App.jsx` | `super-dolphin-theme` | 是 | P3 | `App.test.jsx` |
| localStorage | `wailsBridge.js` | `observability.frontend.debug` | 是 | P2 | `wailsBridge.test.js` |
| localStorage | `PromptPageView.jsx` | `super-dolphin.promptDebug` | 是 | P2 | prompt tests 间接覆盖 |

状态测试缺口：

- 缺少一条明确的 L0 guard：普通页面/store 不应导入 bridge-level `callAPI`，新增 RPC 不应通过字符串 `callBackend` 绕过 `RPC_METHODS`。
- 对 React Query 大量页面缓存，已有页面测试覆盖成功/失败/重试较多，但没有统一缓存失效矩阵。

## 权限与认证扫描

当前 `frontend-app` 是桌面本地 UI，没有传统登录页、token refresh、401/403 全局拦截、角色菜单权限。Obsidian 文档中的 auth/header 维度在本项目应改写为：

| 通用 Web 维度 | 本项目对应风险 | 当前状态 | 优先级 |
|---|---|---|---:|
| token/header 注入 | Wails/RPC trace/client meta 注入 | `wailsBridge.callAPI` 注入 trace/client metadata | P1 |
| 401/403 | JSON-RPC/backend error 显示 | 页面各自展示 alert/retry 状态 | P1 |
| 路由守卫 | 项目 cwd / active thread / stopped thread action gate | store/page 已有较多 gating 测试 | P0/P1 |
| 菜单权限 | 桌面功能入口可见性 | 暂无角色权限模型 | P3 |
| 按钮权限 | 高风险操作 disabled/loading | 多数页面有 disabled/loading 测试 | P0/P1 |

## 安全扫描

| 风险类型 | 证据 | 等级 | 结论/建议 |
|---|---|---:|---|
| 硬编码密钥 | 未发现真实 token/key；只发现字段名、测试字符串、placeholder | P2 | 保持不输出真实密钥 |
| API Key 输入 | `SettingsPage.jsx` 使用 password input，保存后本地 state 清空并显示 masked | P0 | RPC 入口需收口到 facade，避免字符串直连漏测 |
| 敏感日志 | `wailsBridge.js` 有 forbidden key/redaction list：`secret`、`token`、`password`、`api_key`、`authorization` 等 | P1 | 保持，新增敏感字段时扩展测试 |
| XSS/HTML 注入 | 未发现 `dangerouslySetInnerHTML`、`innerHTML` | P1 | 继续禁止 |
| localStorage 敏感信息 | localStorage 只看到 theme/debug/log level | P2 | 不存 token/key，现状可接受 |
| 外链打开 | Chat markdown link 使用 `window.open(href, '_blank', 'noreferrer')` | P2 | 建议后续增加 URL allow/deny 单元测试 |
| `.env` 误提交 | `frontend-app` 未发现 `.env` 文件 | P2 | 保持 lockfile 和 env example 扫描 |
| 依赖漏洞 | `npm audit --audit-level=moderate` 0 vulnerabilities | P2 | 保持 PR/发布前审计 |

## 性能扫描

构建通过，但 Vite warning 指出部分 chunk minified 后大于 500 kB。代表性大 chunk：

| Chunk | Size | Gzip | 风险 |
|---|---:|---:|---:|
| `chunk-NNHCCRGN-*.js` | 593.66 kB | 137.73 kB | P1 |
| `index-*.js` | 537.17 kB | 154.61 kB | P1 |
| `cytoscape.esm-*.js` | 434.29 kB | 137.58 kB | P2 |
| `katex-*.js` | 258.88 kB | 77.46 kB | P2 |
| `react-core-*.js` | 181.78 kB | 57.19 kB | P2 |

现状已有缓解：

- `vite.config.js` 已把 React、TanStack/Zustand、lucide icons 拆到 manual chunks。
- Mermaid 在 `ChatPage.jsx` 中通过动态 `import('mermaid')` 懒加载。

建议：

- P1：把构建 warning 写入扫描报告即可，暂不作为阻断；后续若首屏加载变慢，再对 markdown/diagram 相关依赖继续拆分。
- P2：增加 bundle size budget 脚本或 CI artifact 记录，不要直接改构建配置。

## 可访问性扫描

正向发现：

- `FocusTrapDialog.jsx` 提供 dialog、`aria-modal`、Escape 关闭、Tab 焦点循环，并有 `FocusTrapDialog.test.jsx`。
- 主要导航按钮、图标按钮、附件按钮、runtime panel、diff actions 大量使用 `aria-label`、`aria-expanded`、`aria-busy`、`role=alert/status/tab/tablist`。
- 图片预览和 Mermaid 图有 `alt` 或 `aria-label`。
- 页面测试大量使用 Testing Library `getByRole`/`findByRole`，对可访问名称有间接约束。

缺口：

- 未引入 axe/自动 a11y 扫描。
- 大型自定义区域如 runtime panel、markdown citation chips、DAG editor、Skills editor 依赖人工和行为测试组合，缺少统一 a11y checklist。
- 不是所有 icon-only button 都有集中测试；当前靠分散页面测试兜底。

建议优先级：

- P1：保留 `FocusTrapDialog` 和关键按钮的 role/name 测试。
- P2：后续可增加一条轻量 a11y smoke，而不是对所有页面做像素/视觉回归。

## 当前测试覆盖概览

测试文件扫描结果：

- `frontend-app` 当前有 19 个 Vitest 测试文件通过。
- 重点覆盖面包括：
  - `backendApi.test.js`: facade payload、fail-fast、字段映射。
  - `wailsBridge.test.js`: Wails bridge、trace、event、clipboard/shared-file/native helper。
  - `useClientStore.test.js`: store、thread/timeline、runtime 行为。
  - `App.test.jsx`: app shell、跨页面集成、高风险 workflow/shared-file/skills/settings 行为。
  - 页面测试：Chat、Files、Memory、Observability、Settings、Skills、Workflow、Prompt。
  - `styles.test.js`: theme/layout/style contracts。

当前 Vitest 结果：

```text
Test Files  19 passed (19)
Tests       623 passed | 10 skipped (633)
```

## 测试缺口矩阵

| 排序 | 模块 | 文件 | 当前测试类型 | 缺失测试 | 风险 | 是否必要 | 建议测试文件 |
|---:|---|---|---|---|---:|---:|---|
| 1 | Video API Key RPC | `SettingsPage.jsx`、`internal/module/uistate/rpc.go` | 页面入口/Go handler 测试 | `RPC_METHODS` + facade + L1 payload/fail-fast | P0 | 必要 | `backendApi.test.js`、`SettingsPage.test.jsx` |
| 2 | RPC surface drift guard | `backendApi.js`、product pages/store | 无统一静态 guard | 禁止页面/store 直连 bridge `callAPI` 或字符串 `callBackend` | P0 | 必要 | 新增 L0 script 或 Vitest |
| 3 | Prompt sections | `backendApi.js`、`PromptPageView.jsx` | 部分页面行为 | `prompt-sections/list/write/delete` L1 | P1 | 必要 | `backendApi.test.js` |
| 4 | Project context | `backendApi.js`、`useClientStore.js` | store/page 间接覆盖 | `ui/projects/*` cwd/path payload/fail-fast | P1 | 必要 | `backendApi.test.js` |
| 5 | Preferences/config | `backendApi.js`、`SettingsPage.jsx` | Settings tests | `config/read`、`ui/preferences/*` L1 | P1 | 必要 | `backendApi.test.js` |
| 6 | Thread history/resolve | `backendApi.js`、`useClientStore.js` | store 行为测试 | `thread/messages`、`thread/resolve` L1 + pagination response assumptions | P1 | 必要 | `backendApi.test.js`、`useClientStore.test.js` |
| 7 | UI bootstrap/state/sidebar | `App.jsx`、`backendApi.js` | App/store 集成 | `ui/windowBootstrap/get`、`ui/state/get`、`ui/sidebar/get` L1 | P1 | 必要 | `backendApi.test.js` |
| 8 | Response schema/normalization | pages/store | 分散 normalization | 统一列出哪些 response pass-through、哪些 normalize、哪些 reject | P1 | 必要 | 文档矩阵 + focused tests |
| 9 | Bundle size | Vite build | build warning | budget 记录或阻断策略 | P2 | 条件必要 | 独立 size guard |
| 10 | a11y smoke | UI pages | 分散 role/name 测试 | axe 或轻量 smoke | P2 | 条件必要 | page-level smoke |
| 11 | 所有页面 E2E | 全前端 | 无 | 全量 E2E | P3 | 非必要 | 不建议本阶段补 |
| 12 | 100% coverage | 全前端 | 无 | 行覆盖率追满 | P3 | 非必要 | 不建议 |

## P0/P1 补测建议

P0 立即补齐：

1. `ui/video/getApiKey` / `ui/video/setApiKey`
   - 增加 `RPC_METHODS.UI_VIDEO_API_KEY_GET`、`RPC_METHODS.UI_VIDEO_API_KEY_SET`。
   - 增加 `readVideoApiKey()`、`writeVideoApiKey({ apiKey })` facade。
   - `backendApi.test.js` 覆盖 success、空 `apiKey` fail-fast、payload 不带 UI-only 字段。
   - `SettingsPage.jsx` 改为导入具名 facade，不再调用字符串 `callBackend`。

2. L0 静态接口守卫
   - 扫描 `frontend-app/src` 中 `from './shared/api/wailsBridge'` / `callAPI(`。
   - 扫描页面/store 中 `callBackend('literal'`。
   - 允许 `shared/api` 内部和测试文件，禁止产品页面新增裸 RPC 字符串。

P1 后续补齐：

1. `prompt-sections/*` L1 facade tests。
2. `ui/projects/*`、`ui/preferences/*`、`ui/dashboard/get`、`ui/state/get` L1 facade tests。
3. `thread/messages`、`thread/resolve` L1 tests，并明确分页/identity response assumptions。
4. 为 P0 写接口抽样补 Go dispatch-level L3 contract samples。

## 非必要测试边界

本阶段不建议做：

- 给每个页面补完整 E2E。
- 为 100 个 RPC 全部补桌面 smoke。
- 为所有组件做 snapshot。
- 引入 MSW 模拟 HTTP，因为当前核心风险不是 HTTP URL/header。
- 引入 OpenAPI/Zod 作为默认改造，因为当前后端契约源是 Go typed handler 和 JRPC object params。
- 追求 100% 覆盖率。
- 做像素级视觉回归。

## 建议的后续执行顺序

1. 先修 P0 video RPC 1to1 缺口。
2. 增加 L0 静态守卫，防止新增接口绕过 `RPC_METHODS`。
3. 补齐 `backendApi.test.js` 中 18 个未直接引用常量的 L1 覆盖，按 P1/P2 分批。
4. 对 P0 写接口补 3-5 个 Go dispatch-level contract samples。
5. 将 bundle warning 和 a11y smoke 纳入后续质量计划，但不阻塞当前 docs-only 分支。
