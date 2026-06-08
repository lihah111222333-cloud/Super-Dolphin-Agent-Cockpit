# Frontend RPC 1:1 集成改动说明

日期：2026-06-08

## 1. 分支与工作台

- 集成分支：`integration/frontend-rpc-1to1-implementation-20260608`
- 集成工作台：`.worktrees/frontend-rpc-1to1-integration-20260608`
- 基线：已 rebase 到最新 `origin/main`
- 当前状态：集成分支相对 `origin/main` ahead 8
- 处理原则：不推送远端，不修改本地 `main`，不合并到 `main`

## 2. 集成提交顺序

本次集成按前端 RPC 1:1 修复计划要求，从 `origin/main` 吸收 7 个任务分支并追加 1 个集成修正提交：

1. `0abd5185 前端RPC视频API门面`
2. `dfcd613e 前端RPC契约矩阵`
3. `d37ff6f7 前端RPC原始桥接守卫`
4. `92ae1a07 前端RPC消费者守卫`
5. `3dd9680b 前端RPC门面契约覆盖`
6. `e5f5aeda 后端RPC严格字段契约`
7. `4350ba56 桌面RPC冒烟命令`
8. `39776a44 集成前端RPC一对一实现`

7 个任务 worktree 均已整理为干净状态，并重新 rebase 到最新 `origin/main`。每个任务分支当前相对 `origin/main` ahead 1，`git diff --check` 无输出。

## 3. 前端 RPC 门面改动

### 3.1 Video API facade

涉及文件：

- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- `frontend-app/src/pages/settings/SettingsPage.test.jsx`

核心变化：

- `setVideoApiKey` 改为通过 `videoApiKeyPayload(params)` 构造 payload。
- 发送给后端的 payload 严格限制为 `{ apiKey: normalized }`。
- `apiKey` 会执行 trim 归一化。
- 空字符串、空对象、非字符串等 invalid input 会 fail-fast，并保证不调用 `callAPI`。
- 不再透传 UI-only 或额外字段到后端。

新增测试覆盖：

- 成功 payload 只包含 `apiKey`。
- `getVideoApiKey` 和 `setVideoApiKey` 的 response passthrough。
- 后端 reject 原样透传给调用方。
- invalid input 不增加 `callAPI` 调用次数。
- Settings 视频卡保存失败显示 `保存失败：...`，并通过 `role=alert` 可观测。

## 4. RPC 契约矩阵与静态守卫

### 4.1 显式 contract registry

涉及文件：

- `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`

核心变化：

- 将 `backendApi.contractMatrix.js` 从自动推导矩阵重做为显式 `RPC_CONTRACT_REGISTRY`。
- 每个 `RPC_METHODS` entry 必须有对应 registry entry。
- 每个 registry entry 必须包含：
  - `key`
  - `method`
  - `facade`
  - `level`
  - `backendOwner`
  - `tests`
  - `rawLiteralRpc`
  - `notes`

风险分级变化：

- `UI_VIDEO_SET_API_KEY` 标记为 `P0`。
- `UI_VIDEO_GET_API_KEY` 标记为 `P1`，原因是凭据配置读取。
- P1 read families 已显式覆盖：
  - thread config/read
  - dashboard detail
  - prompt read
  - memory read
  - observability query

测试覆盖：

- registry keys 与 `RPC_METHODS` keys 完全一致。
- 所有 entry 字段完整。
- 禁止 `rawLiteralRpc` 默认放行。
- 校验 video 风险等级。
- 校验 P1 read family 覆盖。
- 校验少数 `params:{}`、custom decoder、passthrough response 备注。

### 4.2 L0 raw bridge surface guard

涉及文件：

- `frontend-app/src/shared/api/backendApi.surface.test.js`

核心变化：

- L0 扫描范围扩展为整个 `frontend-app/src`。
- 排除范围仅保留 `shared/api/**` 和测试文件。
- 删除 SettingsPage video allowlist。
- 删除已知 gap seed。
- 静态检测 raw `callAPI`、`callBackend` import/call，包括 alias 与 namespace 形式。

### 4.3 L2 consumer guard

涉及文件：

- `frontend-app/src/pages/backendApiConsumer.surface.test.js`

核心变化：

- 不再把字符串包含当作 consumer 证据。
- 删除 SettingsPage raw video allowlist。
- 保留静态 guard：业务页面、features、entities 不允许直接 import raw bridge。
- 关键 consumer 通过页面测试和 named facade 调用证明，而不是通过 RPC 字符串存在证明。

## 5. L1 facade 测试补强

涉及文件：

- `frontend-app/src/shared/api/backendApi.test.js`

核心变化：

- 新增统一 helper：`expectInvalidInputDoesNotCall(callAPI, action, message)`。
- invalid input 场景统一断言 `callAPI` 调用次数不变。
- 对新增与高风险 facade 补 response passthrough 或明确 normalized response 断言。

代表样本覆盖：

- video get/set。
- `thread/start` canonical payload 与 response passthrough。
- `turn/start` representative call passthrough。
- DAG dispatch/apply invalid no-call 与 response passthrough。
- observability query response passthrough。

## 6. Go 侧严格 RPC 契约

### 6.1 thread/start dispatch 必填字段

涉及文件：

- `internal/module/thread/rpc.go`
- `internal/module/thread/service_handlers_test.go`
- `internal/module/thread/start_session_guard_test.go`

核心变化：

- `thread/start` handler 增加 `validateStartParams(p startParams)`。
- dispatch 层要求：
  - `cwd` 必填。
  - `provider` 或 `modelProvider` 至少一个必填。
- 缺必填字段时在调用 service 前拒绝。

测试覆盖：

- `thread/start` success sample 使用当前前端 canonical payload：
  - `cwd`
  - `name`
  - `modelProvider`
  - `prompt_key`
  - `agent_key`
  - `toolSurfaceMode`
  - `defer_spawn`
  - `launchIntentId`
- 缺 provider/modelProvider 时拒绝，并断言 service 未调用。
- 缺 cwd 时拒绝，并断言 service 未调用。
- 既有 invalid provider guard 用例补充 `cwd`，避免被新必填校验提前截断。

### 6.2 dashboard dispatch unknown-field 拒绝

涉及文件：

- `internal/module/dashboard/rpc.go`
- `internal/module/dashboard/query_test.go`

核心变化：

- `dagDispatchNodeParams` 增加严格 `UnmarshalJSON`。
- 未声明字段会返回 unknown field 错误。
- dashboard dispatch 在 service 调用前拒绝未知字段。

测试覆盖：

- 直接 `json.Unmarshal` 能看到具体 unknown field。
- RPC dispatch 返回 `invalid parameters`。
- unknown-field 输入不会调用 service。

### 6.3 turn/interrupt alias 兼容

涉及文件：

- `internal/module/turn/rpc_types.go`
- `internal/module/turn/interrupt_rpc_test.go`

核心变化：

- 保留合法 alias：
  - `thread_id`
  - `threadId`
  - `threadID`
- 其它未知字段仍拒绝。

## 7. L4 smoke 改动

### 7.1 RPC smoke 保留并明确命名

涉及文件：

- `frontend-app/package.json`
- `frontend-app/scripts/desktop-smoke.mjs`
- `frontend-app/scripts/desktop-smoke.test.mjs`

核心变化：

- `smoke:desktop` 保留为 `smoke:desktop:rpc` 的 alias。
- 默认 smoke 明确为 RPC smoke，不宣称完整浏览器/Wails UX 覆盖。
- `SUPER_DOLPHIN_DESKTOP_SMOKE_TURN=1` 仍保留为显式 full smoke 开关，不作为默认 quick smoke。

RPC smoke 覆盖：

- 启动 `run-new-ui-desktop.sh`。
- 等待后端 `/metrics` ready。
- 调用代表 RPC：
  - `ui/sidebar/get`
  - `ui/dashboard/get`
  - `observability/status`
  - `thread/start`
  - `observability/frontend/ingest`

### 7.2 新增 Playwright UX smoke

涉及文件：

- `frontend-app/package.json`
- `frontend-app/package-lock.json`
- `frontend-app/playwright.desktop.config.js`
- `frontend-app/scripts/desktop-ux-smoke.mjs`
- `frontend-app/scripts/desktop-ux-smoke.test.mjs`
- `frontend-app/tests/e2e/desktop-ux.spec.js`
- `frontend-app/vite.config.js`

核心变化：

- 新增 `npm run smoke:desktop:ux`。
- 脚本启动 `run-new-ui-desktop.sh`。
- 使用独立端口：
  - HTTP bridge：`127.0.0.1:4513`
  - Vite：`http://127.0.0.1:5176`
  - control RPC：`127.0.0.1:8093`
  - PostgreSQL：`127.0.0.1:55434`
- PostgreSQL socket 使用短 `/tmp/sd-pw-pg-*` 路径。
- 默认要求系统 Chrome：
  - 若 `/usr/bin/google-chrome` 存在则可使用。
  - 也可显式设置 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`。
  - 找不到可执行文件时 fail-fast。
- 退出时清理前端、后端与 PostgreSQL 进程。

UX smoke 覆盖：

- React UI 首屏加载。
- Chat 首屏和 composer 输入。
- 右侧栏切换。
- Observability 页面查询最新日志。
- Settings 页面 Provider Sandbox 卡和 Video 卡。
- 页面运行期间无 console page error。

`vite.config.js` 已排除 `tests/e2e/**`，避免 Vitest 全量测试误跑 Playwright spec。

## 8. 依赖变化

涉及文件：

- `frontend-app/package.json`
- `frontend-app/package-lock.json`

新增 dev dependency：

- `@playwright/test`

用途：

- 仅用于 `smoke:desktop:ux` 的浏览器 UX smoke。
- 不进入生产运行时依赖。

## 9. 验证记录

### 9.1 Focused frontend

已执行并通过：

```bash
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/shared/api/backendApi.surface.test.js src/shared/api/backendApi.contractMatrix.test.js src/pages/backendApiConsumer.surface.test.js src/pages/settings/SettingsPage.test.jsx scripts/desktop-smoke.test.mjs scripts/desktop-ux-smoke.test.mjs
```

结果：

- 7 files passed
- 74 tests passed
- 3 skipped

已执行并通过：

```bash
cd frontend-app
npm test -- src/pages/settings/SettingsPage.test.jsx src/SettingsPage.test.jsx src/App.test.jsx
```

结果：

- 3 files passed
- 232 tests passed
- 8 skipped

### 9.2 Focused Go

已执行并通过：

```bash
./scripts/test_with_guard.sh internal/module/thread/rpc_types.go
./scripts/test_with_guard.sh internal/module/turn/rpc_types.go
./scripts/test_with_guard.sh internal/module/dashboard/query_test.go
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/turn ./internal/module/dashboard -count=1
```

结果：

- `internal/archtest` passed
- `internal/module/thread` passed
- `internal/module/turn` passed
- `internal/module/dashboard` passed

改过的 Go 文件也按仓库规则跑过单文件守卫，包括：

- `internal/module/thread/rpc.go`
- `internal/module/thread/service_handlers_test.go`
- `internal/module/thread/start_session_guard_test.go`
- `internal/module/dashboard/rpc.go`
- `internal/module/dashboard/query_test.go`

### 9.3 Final integration verification

rebase 到最新 `origin/main` 后已重新执行并通过：

```bash
cd frontend-app && npm run lint
cd frontend-app && npm test
cd frontend-app && npm run build
cd frontend-app && npm run smoke:desktop:rpc
cd frontend-app && PLAYWRIGHT_CHROMIUM_EXECUTABLE=/usr/bin/google-chrome npm run smoke:desktop:ux
make guard
git diff --check
```

结果摘要：

- `npm run lint`：通过。
- `npm test`：24 files passed，650 tests passed，10 skipped。
- `npm run build`：通过，有既有大 chunk warning。
- `npm run smoke:desktop:rpc`：通过。
- `npm run smoke:desktop:ux`：Playwright 1 test passed。
- `make guard`：通过。
- `git diff --check`：无输出。

## 10. Smoke 限制与风险

- `smoke:desktop:rpc` 只验证桌面启动后的代表 RPC 可调用，不代表完整浏览器 UX。
- `smoke:desktop:ux` 覆盖核心 UI 路由与交互，但不执行真实 provider turn。
- 完整 provider 路径仍需要显式设置 `SUPER_DOLPHIN_DESKTOP_SMOKE_TURN=1` 才会进入，不作为默认 quick smoke。
- `npm run build` 仍提示部分 chunk 大于 500 kB；这是构建 warning，不是本次集成引入的失败。
- Playwright UX smoke 使用系统 Chrome；如果目标机器没有 `/usr/bin/google-chrome`，需要设置 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`。

## 11. 当前交付状态

- 集成分支已完成代码集成与验证。
- 集成分支工作树在写入本文档前为干净状态。
- 本文档保存于集成分支工作台：

```text
ai01-docs/前端测试文档/2026-06-08-frontend-rpc-1to1-integration-change-summary.md
```

- 未推送远端。
- 未修改本地 `main`。
