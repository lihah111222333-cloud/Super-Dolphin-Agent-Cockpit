# Frontend-App RUM 闭环第二轮历史审查记录

> 当前状态（2026-06-01 后续同步）：腾讯云 RUM / `aegis-web-sdk` 已从 `frontend-app` 脱离。本文以下内容是第二轮历史闭环记录；当前构建不再包含 `aegis.min-*` chunk，运行时不再读取 `VITE_TENCENT_RUM_*`，Wails trace metadata 仅保留为本地 RPC 日志关联能力，不代表腾讯 RUM / APM 接入。

日期：2026-06-01
Benchmark：`FE-RUM-BENCH-2026-06-01-v2`
产品：`frontend-app`
分支：`main`
基线提交：`a5c5cc9a`
After commit：本报告随第二轮本地提交一起进入 `main`；最终 SHA 无法自包含在同一提交内，以 `git log -1 --oneline` 和执行回执为准。
规则文档：`docs/ai01-docs/审查文档/evaluation-iteration-closed-loop-v1.1.md`
缺失路径：`docs/ai01-docs/md/evaluation-iteration-closed-loop-v1.1.md`
环境：Linux `6.8.0-117-generic`，Node `v22.20.0`，npm `10.9.3`，Chrome `148.0.7778.178`，Playwright CLI `1.60.0`，时区 `Asia/Shanghai`
开始工作区：`main...origin/main [ahead 1]`，含本轮未提交修改。

## Workflow

按闭环协议执行五段流程：

1. 复建 benchmark：先跑 `npm run build`，确认主 chunk 仍有 `Some chunks are larger than 500 kB` warning。
2. 四维只读评审：Observability/Backend、Build/Perf、UI/A11y、Runtime/Test Evidence。
3. Arbiter 合并：只批准残留 Top 3，禁止搭车修改。
4. TDD 修复：先加失败测试，再实现 trace、chunk、focus trap。
5. 同一 benchmark 复测，并整理本报告。

`mcp-go-agent-orchestration` 未在当前可调用工具清单中暴露；本轮使用当前会话可用的 4 个子代理完成只读审查并记录生命周期。

## Agent Review Reports

| Agent | Scope | Status | Findings |
| --- | --- | --- | --- |
| A | RUM / Wails trace / backend | PASS | 前端已有 `_aoClientKind/_aoClientRoute/_aoRequestId`，缺 W3C trace metadata；后端必须先解析 trace 再 strip；非法 traceparent 必须 fail-fast；不要宣称 Aegis 自动贯通 WebSocket。 |
| B | Build / perf | PASS | RUM SDK 已动态 chunk；主入口约 513 kB raw 触发 warning；建议显式拆 `react-core`、`query-state`、`icons`，不提高 warning 阈值。 |
| C | UI / a11y | PASS | 所有 modal `aria-modal` 缺 focus trap；Escape 语义会在焦点逃出时分裂；建议统一组件并覆盖 App 与 Prompt 弹层，model dropdown 作为 popover 不纳入本轮。 |
| D | Runtime / evidence | PASS | 第二轮报告必须证据优先：记录 runtime scan、collector payload、Aegis chunk、敏感字段扫描、verification commands 和 Top 3 降级状态。 |

## Arbiter ADR

ADR-1：Wails RPC trace correlation

- 前端 `callAPI()` 为每次业务 RPC 生成 W3C `traceparent`，并随 payload 注入 `_aoTraceparent/_aoTraceId/_aoSpanId`。
- 后端 `internal/ui/wails/binding.go` 先解析并校验 trace metadata，再写入 `pkg/logger.WithTraceContext(ctx, traceID, spanID, "")`。
- 业务 handler 参数 shape 不变；非 `ui/log` 路由继续剥离全部 `_ao*`，`ui/log` 只保留 client meta 并消费 trace meta。
- 非法 `_aoTraceparent`、`_aoTraceId/_aoSpanId` 不一致时 fail-fast。
- 边界：这只是 Wails RPC 可关联日志 trace context，不声明腾讯 Aegis 自动贯通 WebSocket。

ADR-2：Bundle warning

- 使用 Vite/Rolldown `manualChunks` 显式拆分 `react-core`、`query-state`、`icons`。
- 保留 RUM SDK 动态加载路径，不做 catch-all vendor，不提高 `chunkSizeWarningLimit`。
- 验收以 `npm run build` 无 500 kB warning 为准。

ADR-3：Modal focus trap

- 新增 `FocusTrapDialog` 统一 modal overlay 与 `role="dialog"`。
- 打开后聚焦首个可聚焦元素，Tab/Shift+Tab 循环，Escape 关闭，关闭后恢复触发元素焦点。
- `saving/deleting/importing/merging/working` 状态通过 `closeDisabled` 阻止 Escape 与 overlay close。
- 不覆盖 `model-dropdown`，因为它是 popover/dropdown，不是 modal overlay；保留为 P3 另项。

## Change Record

- `frontend-app/src/shared/api/wailsBridge.js`
  - `callAPI()` 生成 trace id/span id/traceparent，写入 payload 和 `api.rpc.start/done/failed` 日志。
- `internal/ui/wails/binding.go`
  - 解析 `_aoTraceparent`，校验 W3C 格式和 metadata 一致性，写入 logger context，再剥离 trace/frontend meta。
- `frontend-app/public/wails/runtime.js`
  - 开发 shim 对 `ui/log` 保留 client meta，但剥离 trace metadata，贴近后端 binding 行为。
- `frontend-app/vite.config.js`
  - 增加 `react-core`、`query-state`、`icons` 手动分包。
- `frontend-app/src/shared/ui/FocusTrapDialog.jsx`
  - 新增共享 focus trap dialog 组件。
- `frontend-app/src/App.jsx`
  - 附件、自动化、技能、共享文件、记忆等 modal 统一使用 `FocusTrapDialog`。
- `frontend-app/src/features/prompts/PromptPageView.jsx`
  - Prompt 编辑器和向导 modal 接入 focus trap，保留 overlay close 能力。
- Tests
  - `wailsBridge.test.js`：trace payload 与 RPC 日志。
  - `binding_id_test.go`：trace context 注入、strict params 剥离、非法/不一致 metadata fail-fast、`ui/log` trace 消费。
  - `App.test.jsx`：附件、共享文件删除 pending、Prompt 编辑器和向导 focus trap。

## Verification Commands

| Command | Result |
| --- | --- |
| `git diff --check` | PASS |
| `./scripts/test_with_guard.sh ./internal/ui/wails ./pkg/logger -count=1` | PASS；guard、archtest、`internal/ui/wails`、`pkg/logger` 全过 |
| `cd frontend-app && npm run lint` | PASS |
| `cd frontend-app && npm test` | PASS；7 files / 254 tests |
| `cd frontend-app && npm run build` | PASS；无 500 kB chunk warning |
| `cd frontend-app && npm audit --omit=dev` | PASS；0 vulnerabilities |

Build 体积摘要：

| Asset                     |       Raw |     Gzip |
| ------------------------- | --------: | -------: |
| `index-BJ9gltsN.js`       | 281.54 kB | 80.58 kB |
| `react-core-Be7ANt6U.js`  | 181.78 kB | 57.18 kB |
| `aegis.min-BHQRIVNU.js`   | 129.20 kB | 41.62 kB |
| `query-state-u8UpDdbk.js` |  34.24 kB | 10.37 kB |
| `icons-M6RkBtzg.js`       |  17.23 kB |  6.56 kB |

## Runtime Scan Evidence

Run ID：`FE-RUM-RUNTIME-2026-06-01-ITER2-001`
Command：

```bash
VITE_TENCENT_RUM_ENABLED=true \
VITE_TENCENT_RUM_ID=local-rum-scan \
VITE_TENCENT_RUM_HOST_URL=http://127.0.0.1:18188 \
npx vite --host 127.0.0.1 --port 5185 --strictPort
```

Browser：`/usr/bin/google-chrome`
URL：`http://127.0.0.1:5185/`
Artifacts：

- Collector payload dump：`/tmp/frontend-app-rum-collector-iteration-2.jsonl`
- Scan summary：`/tmp/frontend-app-rum-scan-iteration-2.json`

| Metric | Target | Result | Status |
| --- | ---: | ---: | --- |
| `pageerror_count` | 0 | 0 | PASS |
| `requestfailed_count` | 0 | 0 | PASS |
| `console_error_count` | 0 | 0 | PASS |
| `console_warn_count` | 0 | 0 | PASS |
| `aegis_dynamic_chunk_loaded` | true | true | PASS |
| `rum_collector_request_count` | >= 1 | 5 | PASS |
| `pii_leak_count` | 0 | 0 | PASS |
| `body_ready` | true | true | PASS |

Runtime request status summary：`200: 25`，`204: 5`。
Console 样本仅包含 Vite dev-server debug 与 React DevTools info。
敏感字段扫描词：`access_token`、`refresh_token`、`id_token`、`api_key`、`authorization`、`secret`、`password`；命中 0。

## Acceptance / Residual Top 3

| Residual | Iteration 2 result | Status |
| --- | --- | --- |
| Wails `/wails/ws` trace context 不可关联 | 已补 Wails RPC payload trace metadata 与后端 logger context；非法 trace fail-fast；仍不声明 Aegis 自动贯通 WebSocket。 | Verified, 降级 P3 架构边界 |
| Vite 主 JS chunk > 500 kB warning | `npm run build` 无 `Some chunks are larger than 500 kB`；主入口 gzip 80.58 kB。 | Verified |
| Modal 缺统一 focus trap | App 与 Prompt modal 已统一 `FocusTrapDialog`；测试覆盖 Tab/Shift+Tab、Escape、focus restore、pending 删除不关闭。 | Verified |

## Residual Backlog

1. P3：`model-dropdown` 仍是 popover/dropdown，不是 modal；建议后续补 Escape close、focus restore 或改成更明确的 popover/listbox 语义。
2. P3：Wails WebSocket 到腾讯 APM 的完整 span 系统需要后端协议与 APM collector 设计，本轮只做日志可关联 trace context。
3. P3：可把 runtime scan 固化成脚本，避免后续依赖一次性命令与 `/tmp` artifact。

## 后续脱离记录

- 已删除 `frontend-app/src/shared/monitoring/tencentRum.js` 与 `frontend-app/src/shared/monitoring/tencentRum.test.js`。
- 已删除 `aegis-web-sdk` 依赖，并从 `frontend-app/src/main.jsx` 移除 RUM 初始化入口。
- 已从 `frontend-app/README.md` 移除腾讯 RUM 启用说明；当前不再暴露 `VITE_TENCENT_RUM_*` 配置契约。
- 当前代码扫描确认 `frontend-app/src`、`frontend-app/README.md`、`frontend-app/package.json`、`frontend-app/package-lock.json`、`frontend-app/vite.config.js` 无 Tencent / RUM / Aegis 运行时命中。
- 当前构建确认不再输出 `aegis.min-*` 或其他 Aegis 动态 chunk。

## 参考

- 腾讯云 RUM 全链路与 `injectTraceHeader` 说明：https://cloud.tencent.com/document/product/248/87108
