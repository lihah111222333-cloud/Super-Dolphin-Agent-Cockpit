# Frontend-App 腾讯 RUM 扫描与闭环修复历史记录

> 当前状态（2026-06-01 后续同步）：腾讯云 RUM / `aegis-web-sdk` 已从 `frontend-app` 运行时代码中移除。本文以下内容保留为历史审查记录，不再代表当前接入状态；当前代码不再读取 `VITE_TENCENT_RUM_*`，不再生成 Aegis chunk，也不再包含 `tencentRum.*` 模块。

日期：2026-06-01
分支：`main`
基准提交：`4ab689be`
工作区状态：有未提交修改，本报告记录本轮扫描、审查、修复与验证证据。

## 规则与范围

- 请求路径 `docs/审查报告/前端项目资料/md/evaluation-iteration-closed-loop-v1.1.md` 当前不存在。
- 实际采用规则：`docs/审查报告/前端项目资料/审查文档/evaluation-iteration-closed-loop-v1.1.md`。
- 范围限定为 `frontend-app`：RUM 接入、运行时扫描、UI 误操作、provider 配置 fail-fast、可访问性阻塞点。
- 腾讯 RUM 使用 `aegis-web-sdk`。本轮不接真实腾讯云应用 ID，采用本地 collector 模拟上报，避免把测试数据送到真实云端。
- `mcp-go-agent-orchestration` 工具未暴露在当前可调用工具清单中；本轮使用当前会话可用的 4 个子代理身份执行并记录审查生命周期。

## Benchmark Manifest

| 项目 | 结果 |
| --- | --- |
| `git diff --check` | 通过 |
| `cd frontend-app && npm run lint` | 通过 |
| `cd frontend-app && npm test` | 通过，7 files / 248 tests |
| `cd frontend-app && npm run build` | 通过 |
| `cd frontend-app && npm audit --omit=dev` | 通过，0 vulnerabilities |
| RUM 本地浏览器扫描 | 通过，pageerror 0，requestfailed 0 |

Build 体积摘要：

- `dist/assets/index-DaGbKLbc.js`: gzip 152.50 kB
- `dist/assets/aegis.min-BAo6YE6d.js`: gzip 41.61 kB，独立动态 chunk
- `dist/assets/index-CBEJYu5r.css`: gzip 15.44 kB
- Vite 仍提示主 chunk 超过 500 kB；本轮记录为性能残留，不阻塞 RUM 闭环。

RUM runtime scan：

- 启动：`VITE_TENCENT_RUM_ENABLED=true VITE_TENCENT_RUM_ID=local-rum-scan VITE_TENCENT_RUM_HOST_URL=http://127.0.0.1:18188 npx vite --host 127.0.0.1 --port 5185 --strictPort`
- 浏览器：Playwright + `/usr/bin/google-chrome`
- 页面：`http://127.0.0.1:5185/`
- 请求：27，总状态 `200: 24`、`204: 3`
- Console：仅 Vite dev server debug 与 React DevTools info
- Aegis chunk：已请求 `aegis-web-sdk`
- 本地 collector：收到 3 条上报请求，未发现 `access_token`、`refresh_token`、`id_token`、`api_key`、`authorization`、`secret`、`password` 泄露。

## 多 Agent Review Report

Agent A：RUM / 可观测性（Kierkegaard）

- P1：`reportApiSpeed` 缺少 API URL 级别 `urlHandler`，已补到 `reportApiSpeed.urlHandler`。
- P1：敏感参数脱敏过窄，已覆盖 token/key/secret/password/authorization/session/code/jwt/auth 等形态。
- P2：trace 白名单过宽时可能污染 RUM 自身上报，已默认忽略配置的 RUM host。
- P2：host 文档从非默认示例修正为 SDK 默认 `https://rumt-zh.com`。
- P2：本地 file/Windows/POSIX 路径脱敏增强。

Agent B：状态 / API / 桥接（Nietzsche）

- P0：provider runtime config 不再用隐式默认值。缺少 active/model/effort/Codex identity 时 fail-fast。
- P1：新建线程后第一轮 `turn/start` 失败会清理 provisional backend thread，避免空线程残留。
- P1：Aegis 只能覆盖 fetch/XHR；Wails `/wails/ws` WebSocket RPC 不能声明完整链路贯通，已列为架构限制。
- P2：Settings 使用哨兵项目路径发偏好 RPC 的风险保留到下一轮设置页专项。

Agent C：UI / UX / 可访问性（Hypatia）

- P1：左右栏与工具面板 resizer 已改为可聚焦 `role="separator"`，支持方向键、Home、End。
- P1：输入框/菜单/dialog 内 Escape 不再触发全局 interrupt。
- P2：后端未就绪或无项目时，provider 切换、发送、附件、权限和模型控件均在 UI 层禁用。
- P2：设置页关键控件使用可访问名称锁定回归。
- 残留：部分弹层还没有完整 focus trap，列入下一轮 UI 可访问性专项。

Agent D：构建 / 性能 / 运行时（Russell）

- P2：favicon 404 噪声已修复。
- P2：RUM SDK 已拆为动态 chunk，未配置 ID 时不进入主路径。
- P2：缺少 runtime scan 脚本，本轮使用一次性 Playwright 扫描并记录证据。
- P3：主 bundle 超过 Vite 500 kB 告警，建议下一轮按页面/功能切 chunk。

## Arbiter Findings

P0 已修复：

- Provider runtime 配置禁止隐式默认值。缺少必需偏好时阻断启动或发送，保持 fail-fast。

P1 已修复：

- RUM API speed URL 经过同一套脱敏处理。
- 新线程首轮发送失败后清理 provisional thread。
- Resizer 支持键盘调整并暴露正确 separator 语义。
- 输入态 Escape 不再误中断运行中的线程。

P2 已修复：

- RUM trace header 仅在显式 `VITE_TENCENT_RUM_TRACE_URLS` 配置后启用。
- RUM host 自动进入 trace ignore，避免观测上报自我污染。
- 未就绪项目动作在 UI 层阻止，不调用无效 RPC。
- favicon 资源噪声消除。
- 文档与 `.env.example` 对齐真实 RUM host 默认值。

## ADR / 决策记录

1. RUM SDK 采用动态 import，只有 `VITE_TENCENT_RUM_ID` 存在时加载。
2. `VITE_TENCENT_RUM_ENABLED=true` 但缺少 ID 时 fail-fast，不静默降级。
3. trace header 只对白名单 URL 注入；默认不注入，避免 CORS 和第三方请求污染。
4. Wails WebSocket RPC 不声明为 Aegis 完整前后端 trace 贯通。要贯通必须由桥接层和后端显式传递 trace context。
5. provider runtime 偏好必须来自后端配置；UI 可以展示占位或现有配置，但发送/切换不能替用户填默认值。

## Change Record

- `frontend-app/src/shared/monitoring/tencentRum.js`
  - 腾讯 RUM 动态加载、env 解析、trace 白名单、host ignore、URL 脱敏、fail-fast。
- `frontend-app/src/shared/monitoring/tencentRum.test.js`
  - 覆盖未配置 ID、强制启用缺 ID、trace regex、RUM host ignore、API speed URL 脱敏。
- `frontend-app/src/entities/client/model/useClientStore.js`
  - provider runtime 偏好 fail-fast；新线程首轮失败后清理 provisional thread。
- `frontend-app/src/entities/client/model/useClientStore.test.js`
  - 覆盖 provider 偏好缺失、发送配置缺失、provisional thread 清理。
- `frontend-app/src/App.jsx`
  - 项目动作禁用态、Escape 保护、键盘 resizer、bootstrap failure 可见反馈。
- `frontend-app/src/App.test.jsx`
  - 覆盖未就绪误操作、键盘 separator、provider fail-fast 配套 mock。
- `frontend-app/src/styles.css`
  - resizer focus-visible 状态。
- `frontend-app/src/main.jsx`
  - 初始化 RUM 包装模块。
- `frontend-app/README.md`、`frontend-app/.env.example`
  - 腾讯 RUM env、trace 限制、隐私说明。
- `frontend-app/index.html`、`frontend-app/public/favicon.svg`
  - favicon。
- `frontend-app/package.json`、`frontend-app/package-lock.json`
  - 增加 `aegis-web-sdk`。

## 验证矩阵

| 风险 | 验证 |
| --- | --- |
| 未配置 RUM ID 仍加载 SDK | `tencentRum.test.js` 验证不实例化 |
| 强制启用缺 ID 被吞掉 | `tencentRum.test.js` 验证 fail-fast |
| trace header 默认注入 | `tencentRum.test.js` 验证无白名单不注入 |
| RUM 上报 URL 被 trace 污染 | `tencentRum.test.js` 验证 RUM host ignore |
| API speed URL 泄露 token/path | `tencentRum.test.js` 验证脱敏 |
| provider 配置隐式默认值 | `useClientStore.test.js` 验证缺失偏好 reject |
| 新线程首轮失败残留空线程 | `useClientStore.test.js` 验证 delete provisional thread |
| 未就绪仍可发送/切换 | `App.test.jsx` 验证按钮、点击、Enter 均不触发 RPC |
| Resizer 键盘不可达 | `App.test.jsx` 验证 `separator` 与方向键调整 |
| Runtime 页面错误 | Playwright RUM scan：pageerror 0，requestfailed 0 |

## 残留 Top 3

1. Wails `/wails/ws` 是 WebSocket RPC，Aegis `injectTraceHeader` 只覆盖 fetch/XHR；完整前后端链路需要桥接层和后端协议改造。
2. 主 JS chunk 仍超过 Vite 500 kB 提醒线；RUM SDK 已拆出，但主应用还需要页面级切分。
3. 部分弹层还缺完整 focus trap；当前已修复误中断和 resizer 键盘阻塞，下一轮建议做统一弹层可访问性基建。

## 后续脱离记录

- 已删除 `frontend-app/src/shared/monitoring/tencentRum.js` 与 `frontend-app/src/shared/monitoring/tencentRum.test.js`。
- 已删除 `aegis-web-sdk` 依赖，并从 `frontend-app/src/main.jsx` 移除 RUM 初始化入口。
- 已从 `frontend-app/README.md` 移除腾讯 RUM 启用说明；当前不再暴露 `VITE_TENCENT_RUM_*` 配置契约。
- 当前代码扫描确认 `frontend-app/src`、`frontend-app/README.md`、`frontend-app/package.json`、`frontend-app/package-lock.json`、`frontend-app/vite.config.js` 无 Tencent / RUM / Aegis 运行时命中。
- 当前构建确认不再输出 `aegis.min-*` 或其他 Aegis 动态 chunk。

## 参考

- 腾讯云 RUM 全链路与 `injectTraceHeader` 说明：https://cloud.tencent.com/document/product/248/87108
