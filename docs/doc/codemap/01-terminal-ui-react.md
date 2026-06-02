# super-agent-v3 代码地图：终端入口与 UI 层（当前 React/Vite 新 UI）

> 范围：`frontend-app/`
> 关联后端卷：[`01-terminal-ui-go.md`](01-terminal-ui-go.md)；prompt/thread 写链看 [`07-module-write.md`](07-module-write.md) 与 [`11-prompt-thread.md`](11-prompt-thread.md)。
> 维护提示：这是当前桌面新 UI 的页面代码。`cmd/agent-terminal` 仍是 Wails/HTTP/RPC 宿主，但 `run-new-ui-desktop.sh` 会通过 `VITE_DEV_URL` 把页面代理到 `frontend-app` Vite dev server。

## 1. 当前入口与启动链

- 一键启动脚本：`run-new-ui-desktop.sh`
  - `FRONTEND_APP_DIR="$PROJECT_DIR/frontend-app"`
  - 启动 `frontend-app` 的 Vite dev server。
  - 再运行 `go run ./cmd/agent-terminal`，由 Wails host 代理 `VITE_DEV_URL`。
- 页面入口：`frontend-app/src/main.jsx`
- 应用主壳：`frontend-app/src/App.jsx`
- 样式入口：`frontend-app/src/styles.css`
- 旧 Vue 目录：`cmd/agent-terminal/frontend/vue-app/`，只作为 legacy/package-embed 路径处理。

## 2. 文件地图

| 路径 | 角色 | 修改时优先看 |
|---|---|---|
| `frontend-app/src/App.jsx` | React app shell、导航、chat workspace、composer、timeline、modal | 页面结构、交互、消息渲染、执行计划展示 |
| `frontend-app/src/entities/client/model/useClientStore.js` | Zustand store、bootstrap、thread list、composer、preferences、send actions | 状态流、历史恢复、`sendDraft()`、provider/cwd fail-fast |
| `frontend-app/src/shared/api/backendApi.js` | 后端 RPC facade 与 payload 校验 | RPC 方法名、参数 shape、cwd/threadId 校验 |
| `frontend-app/src/shared/api/wailsBridge.js` | Wails runtime bridge、event subscription、日志回传 | `/wails/runtime.js`、`callAPI()`、bridge event |
| `frontend-app/src/features/prompts/PromptPageView.jsx` | Prompt 页面视图 | prompt 列表、编辑、向导弹层 |
| `frontend-app/src/shared/ui/FocusTrapDialog.jsx` | 共享弹层 focus trap | modal 可访问性、Escape/Tab 行为 |
| `frontend-app/src/styles.css` | 全局视觉样式 | shell、toolbar、chat、timeline、弹层样式 |

## 3. 运行与验证命令

```bash
./run-new-ui-desktop.sh
```

当前新 UI 改动的前端验证：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

`cmd/agent-terminal/frontend` 的 `node scripts/size-guard.cjs` / `npx vitest run` / `npm run build` 只用于 legacy Vue/package-embed 前端改动。

## 4. Chat / Thread 当前链路

```mermaid
sequenceDiagram
    participant UI as App.jsx ComposerDock
    participant STORE as useClientStore.sendDraft
    participant API as backendApi
    participant BRIDGE as wailsBridge.callAPI
    participant RPC as Go RPC

    UI->>STORE: sendDraft()
    STORE->>API: startThread({ cwd, ...config })
    API->>BRIDGE: callAPI("thread/start", params)
    BRIDGE->>RPC: App.CallAPI
    RPC-->>STORE: threadId
    STORE->>API: startTurn({ threadId, input, cwd })
    API->>BRIDGE: callAPI("turn/start", params)
    BRIDGE->>RPC: App.CallAPI
```

关键点：

- blank-thread 首发仍是 `thread/start -> turn/start` 两段式。
- `backendApi.js` 对 cwd、threadId 等关键参数保持 fail-fast。
- 历史消息与 timeline 可见性在 `useClientStore.js` 标准化和过滤。
- 前端执行计划、reasoning/tool/timeline 渲染集中在 `App.jsx`。

## 5. 路径判断规则

- 问“当前页面在哪里改”：先看 `frontend-app/src/App.jsx`、`useClientStore.js`、`backendApi.js`。
- 问“Wails 桌面宿主怎么加载页面”：看 `run-new-ui-desktop.sh` 与 `internal/ui/wails/assets.go`。
- 问“没有 VITE_DEV_URL 时嵌入什么”：看 `cmd/agent-terminal/frontend.go` 和 legacy `cmd/agent-terminal/frontend/dist`。
- 不要因为桌面进程仍是 `cmd/agent-terminal`，就把当前 React 页面误判到 `cmd/agent-terminal/frontend/vue-app`。
