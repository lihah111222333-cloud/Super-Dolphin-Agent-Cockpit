# 自动化工作流 Finding 10 Agent 裁决报告

生成日期：2026-06-04

裁决范围：仅统计本轮指定的 C01-C16 Findings，审查当前项目自动化工作流相关前后端代码。旧编号、未完成报告、无证据结论、INVALID 历史假设和夹带的新问题均未计票。

输入材料：

- `docs/审查报告/前端项目资料/工作流/run-new-ui-desktop-production-readiness-review-2026-06-04.md`
- `docs/审查报告/前端项目资料/工作流/automation-workflow-production-readiness-5-agent-review-2026-06-04.md`

## 调度与计票口径

用户要求的拓扑是 5 个裁决子 Agent，每个再拉起 2 个审查 Agent，共 10 个审查 Agent。实际执行中，5 个父级裁决 Agent 均返回 `BLOCKED_NESTED`：其子会话内没有可用的 subagent / `mcp-go-agent-orchestration` 工具，因此未能再拉起 2 个子 Agent。父级阻塞输出不计票，也不作为证据。

为满足“10 个审查 Agent、至少 7 票成立”的核心裁决门槛，随后直接拉起 10 个独立 leaf 审查 Agent。10 个 leaf Agent 均已完成；所有未完成、阻塞、旧编号或不在 C01-C16 范围内的内容均剔除。

计票规则：

- `VALID`：源码路径真实可达，且未发现足以覆盖该风险的上层防护。
- `INVALID`：源码不可达、证据不足，或已有防护覆盖；必须注明防护函数。
- `EXCLUDE`：不是本轮 C01-C16，或来自旧编号、未完成报告、夹带内容。
- 10 个审查 Agent 中至少 7 票 `VALID` 才成立。
- 成立项还必须通过本地源码复核；可达且无上层防护进入修复，有防护则进入误判注释。

发布阻断口径：会造成安全暴露、错误制品、验证链失效、状态变更不可恢复挂起，或当前自动化工作流无法可靠启动的项标为阻断；文档不一致、诊断质量下降、直接 dev 路径或可选 hot reload 风险标为非阻断但仍进入修复清单。

## 10 Agent 裁决矩阵

| Finding | 本轮裁决主题 | VALID 票 | INVALID 票 | EXCLUDE 票 | 是否成立 | 源码门禁 | 阻断发布 |
| --- | --- | ---: | ---: | ---: | --- | --- | --- |
| C01 | 桌面冷启动顺序与 Wails dev preRun 竞态 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C02 | 测试锁定后端先于 Vite 的错误顺序 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C03 | Vite polling / CHOKIDAR 配置未 fail-fast | 10 | 0 | 0 | 是 | 进入修复 | 否 |
| C04 | Vite 外部 bind 可经 `/wails/ws` proxy 绕过 Go loopback 假设 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C05 | `FRONTEND_DEVSERVER_URL` 与 `VITE_DEV_URL` 可分叉导致 false-ready | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C06 | 固定 readiness 窗口误判慢但健康的启动 | 10 | 0 | 0 | 是 | 进入修复 | 否 |
| C07 | `set -e` 下 `wait` 非零退出跳过稳态失败日志尾部 | 10 | 0 | 0 | 是 | 进入修复 | 否 |
| C08 | `frontend-app` 直接 `npm run dev` 未覆盖 polling / ENOSPC mitigation | 10 | 0 | 0 | 是 | 进入修复 | 否 |
| C09 | `frontend-app` README 启动顺序与脚本实际相反 | 10 | 0 | 0 | 是 | 进入修复 | 否 |
| C10 | 本地 Postgres 端口未数值校验，可进入 `pg_ctl -o` 参数串 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C11 | 自动化页面 state-changing RPC 缺少统一客户端 timeout | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C12 | DAG 状态事件 fail-fast 校验被订阅包装层吞掉 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C13 | 当前 React `frontend-app` 未进入 git hooks / CI 前端校验 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C14 | Linux package 仍构建 legacy frontend，未嵌入 `frontend-app` | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C15 | `/wails/ws` 缺消息大小和连接容量限制 | 10 | 0 | 0 | 是 | 进入修复 | 是 |
| C16 | 后端 hot reload watch override 无容量上限或最小轮询间隔 | 10 | 0 | 0 | 是 | 进入修复 | 否 |

## 源码复核结果

### C01 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:725-731` 先启动 `cmd/agent-terminal` 后等待后端，再启动 Vite；Wails dev `preRun` 在 `github.com/wailsapp/wails/v3@v3.0.0-alpha.74/pkg/application/application_dev.go:14-37` 期望前端 dev server 已可连，最多等待 10 次 500ms，失败后 fatal；`application.go:469-471` fatal 后 `os.Exit(1)`。

风险影响：冷启动时后端进程可能先因 Wails preRun 找不到前端退出，脚本随后把真实前端启动放在退出之后，形成真实竞态。

源码门禁：未发现上层防护覆盖该顺序竞态。进入修复。阻断发布：是。

### C02 - VALID - 进入修复

证据：`internal/app/new_ui_scripts_test.go:47-71` 的 `TestNewUIDesktopScriptWaitsForBackendBeforeVite` 显式断言 `start_desktop_backend`、`wait_for_backend`、`seed_dev_preferences` 都在 `npm run dev` 前。

风险影响：当前测试把 C01 的错误顺序锁定为期望行为，后续修复 C01 时测试会阻止正确启动顺序落地。

源码门禁：测试只做文本顺序断言，未模拟 Wails preRun 语义。进入修复。阻断发布：是。

### C03 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:262-278` 对 `SUPER_DOLPHIN_VITE_USE_POLLING` 只识别真值，其它值直接进入 native fs events 分支；如果已有 `CHOKIDAR_USEPOLLING`，脚本仅改变展示文案。Vite / Chokidar 在 `frontend-app/node_modules/vite/dist/node/chunks/node.js:9439-9445` 会按 `CHOKIDAR_USEPOLLING` 改写实际 polling 行为。

风险影响：配置层显示的 watch 模式与 Vite 实际模式可分叉，ENOSPC 或 CPU 保护无法 fail-fast 校验。

源码门禁：未发现对 `SUPER_DOLPHIN_VITE_USE_POLLING` 和 `CHOKIDAR_USEPOLLING` 的一致性校验。进入修复。阻断发布：否。

### C04 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:651-657` 只校验 `VITE_DEV_URL` host / port 非空，`run-new-ui-desktop.sh:729` 将 host 传给 Vite；`frontend-app/vite.config.js:33-40` 将 `/wails/ws` 代理到后端；`frontend-app/public/wails/runtime.js:64-70` 用当前页面 host 拼接 WebSocket URL。

风险影响：如果 Vite 被配置为外部 bind，浏览器可经 Vite proxy 访问 `/wails/ws`，绕过 Go HTTP asset server 的 loopback 假设。

源码门禁：`internal/ui/wails/http_server.go:104-115` 的 `validateHTTPAssetAddr` 只保护 Go HTTP asset server 直接 bind，不覆盖 Vite proxy 暴露路径。进入修复。阻断发布：是。

### C05 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:649-650` 允许 `FRONTEND_DEVSERVER_URL` 与 `VITE_DEV_URL` 分别设置；`run-new-ui-desktop.sh:731` readiness 只等待 `VITE_DEV_URL`；`internal/ui/wails/window.go:118-145` 实际窗口 URL 使用 `FRONTEND_DEVSERVER_URL`。

风险影响：脚本可等待一个健康 URL，却把 Wails 窗口指向另一个未校验 URL，造成 false-ready 和启动失败。

源码门禁：未发现两个 URL 必须相等、或对实际 `FRONTEND_DEVSERVER_URL` 做 readiness 的防护。进入修复。阻断发布：是。

### C06 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:70-99` 前端 readiness 固定 50 次、每次 0.2s；`run-new-ui-desktop.sh:213-230` 后端 readiness 固定 100 次、每次 0.2s；`run-new-ui-desktop.sh:281-290` 后端通过 `go run ./cmd/agent-terminal` 冷启动。

风险影响：慢机器、首次构建、依赖初始化或本地 IO 抖动时，健康启动可能被固定窗口误判为失败。

源码门禁：存在 timeout，但没有配置化窗口、冷启动预算或健康进程延长策略。进入修复。阻断发布：否。

### C07 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:3` 启用 `set -euo pipefail`；`run-new-ui-desktop.sh:233-249` 和 `run-new-ui-desktop.sh:329-351` 在进程已退出后先执行 `wait "$PID"`，再读取 status 并打印日志。`wait` 非零会在 `set -e` 下提前终止函数。

风险影响：稳态阶段进程失败时，脚本可能直接退出，跳过后端或前端日志尾部，降低失败定位能力。

源码门禁：readiness 分支有 `wait "$PID" || true`，但稳态等待分支没有同等防护。进入修复。阻断发布：否。

### C08 - VALID - 进入修复

证据：`frontend-app/package.json:7` 的 `dev` 脚本只是 `vite --host 127.0.0.1 --port 5175 --strictPort`；`frontend-app/vite.config.js:30-42` 未设置 `server.watch.usePolling`；`frontend-app/README.md:9-12` 文档化了直接 `npm run dev`。

风险影响：直接运行当前 React UI dev server 时不会获得根脚本中的 polling / ENOSPC mitigation，开发者路径与自动化脚本路径行为不一致。

源码门禁：未发现 Vite config 或 package script 层面的统一 watch 配置。进入修复。阻断发布：否。

### C09 - VALID - 进入修复

证据：`frontend-app/README.md:21-22` 写明脚本先启动 Vite 再启动 `cmd/agent-terminal`；实际 `run-new-ui-desktop.sh:725-731` 是后端、后端 readiness、seed、再启动 Vite。

风险影响：README 描述与真实顺序相反，会误导故障诊断和后续修复 C01 的测试/文档预期。

源码门禁：文档无运行时防护。进入修复。阻断发布：否。

### C10 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:529-557` 从 `DATABASE_URL` 解析 host / port，但未校验 port 为数字且在合法范围；`run-new-ui-desktop.sh:604` 将 port 传给 `lsof`；`run-new-ui-desktop.sh:621-624` 又把 port 拼入 `pg_ctl -o "-h $host -p $port -k ..."` 参数串。

风险影响：本地数据库配置错误不会 fail-fast，异常 port 可进入 PostgreSQL 启动参数串，造成难诊断失败或参数注入面。

源码门禁：未发现 port numeric / range 校验函数。进入修复。阻断发布：是。

### C11 - VALID - 进入修复

证据：`frontend-app/src/pages/shared/pageShared.js:14-22` 已有 `withTimeout`，`frontend-app/src/pages/workflows/WorkflowPage.jsx:52-58` 仅对 dashboard 读请求使用；自动化状态变更在 `WorkflowPage.jsx:898-917`、`920-937`、`940-965`、`972-995`、`998-1010` 直接 await `startDag`、`terminateDagRun`、`deleteDag`、`applyDagOps`。底层 `frontend-app/src/shared/api/wailsBridge.js:402-424` 直接 await `runtime.Call.ByID`，无统一 timeout。

风险影响：状态变更 RPC 如果后端或 Wails bridge 卡住，按钮 actioning 状态可能长期不恢复，用户无法可靠判断任务是否已启动、停止或保存。

源码门禁：未发现 state-changing workflow RPC 的统一 timeout / abort 包装。进入修复。阻断发布：是。

### C12 - VALID - 进入修复

证据：`frontend-app/src/entities/client/model/useClientStore.js:175-193` 对 `task/node/statuschanged` payload 缺失字段会抛错；`useClientStore.js:3489-3497` 事件处理会调用该校验；`useClientStore.js:3594-3597` 通过 `onBridgeEvent` 订阅；`frontend-app/src/shared/api/wailsBridge.js:174-184` 的订阅包装层 catch callback error 后只写 `runtime.callback.failed` 日志。

风险影响：DAG 状态事件的 fail-fast 校验被订阅包装层吞掉，坏事件不会升级为用户可见失败或阻断状态，可能导致自动化 UI 状态静默过期。

源码门禁：未发现订阅错误升级、断开事件流、或全局失败态防护。进入修复。阻断发布：是。

### C13 - VALID - 进入修复

证据：`README.md:15` 标明 `frontend-app` 是当前 React/Vite new UI；`.githooks/pre-commit:41-60` 和 `.githooks/pre-push:119-139` 的前端路径只匹配 `cmd/agent-terminal/frontend/*`；`.githooks/pre-commit:298-318` 与 `.githooks/pre-push:239-262` 只在 legacy frontend 目录运行 size guard / vitest；`.github/workflows/ci.yml:42-68` 只覆盖 Go vet/build/test 和 golangci-lint。

风险影响：当前自动化工作流 UI 的 lint/test/build 不在 hooks 或 CI 中，前端回归可绕过常规验证链。

源码门禁：未发现 `frontend-app` 的 hooks / CI 校验入口。进入修复。阻断发布：是。

### C14 - VALID - 进入修复

证据：`scripts/package_linux.sh:642-646` 构建的是 `cmd/agent-terminal/frontend`；`scripts/package_linux.sh:648-651` 随后调用 `make build-agent-terminal-plain`；`Makefile:10` 将 `FRONTEND_DIR` 固定为 legacy frontend，`Makefile:48-49` 的 `frontend-build` 也只构建该目录。作为对照，`scripts/package_macos.sh:1206-1218` 构建 `frontend-app` 并 rsync 到 embedded dist。

风险影响：Linux package 可能嵌入 legacy UI，而不是当前 React 自动化 UI，形成平台制品不一致。

源码门禁：未发现 Linux package 中等价于 macOS 的 `frontend-app` 构建与 rsync 防护。进入修复。阻断发布：是。

### C15 - VALID - 进入修复

证据：`internal/platform/rpc/transport_ws.go:21` 使用默认 `websocket.Upgrader`；`transport_ws.go:31-44` 每个升级连接直接创建 jrpc2 server；`internal/platform/rpc/server.go:465-471` 只把 active server 记录进 map；`transport_ws.go:275-284` `ReadMessage()` 前未设置 `SetReadLimit`。

风险影响：`/wails/ws` 缺少消息大小限制和连接容量限制，可能被大消息或大量连接耗尽内存、goroutine 或 RPC 资源。

源码门禁：未发现连接数上限、消息大小上限或背压策略。进入修复。阻断发布：是。

### C16 - VALID - 进入修复

证据：`run-new-ui-desktop.sh:668-669` 允许覆盖 `SUPER_DOLPHIN_HOT_WATCH_PATHS` 和 `SUPER_DOLPHIN_HOT_POLL_INTERVAL`；`run-new-ui-desktop.sh:310-324` 对 watch paths 递归 `find`；`run-new-ui-desktop.sh:329-360` 直接 `sleep "$interval"`，没有数值校验、最小轮询间隔、路径数量或文件数量上限。

风险影响：开启后端 hot reload 时，错误配置可导致超大目录递归扫描、极低轮询间隔或 sleep 参数异常，造成本地资源耗尽或 supervisor 失效。

源码门禁：未发现 hot reload override 的容量限制、路径白名单或最小间隔校验。进入修复。阻断发布：否。

## 误判注释

本轮 C01-C16 中，没有达到 7 票后又因源码复核降级为 INVALID 的项。

已确认存在但不足以覆盖本轮风险的防护：

- `validateHTTPAssetAddr`：见 `internal/ui/wails/http_server.go:104-115`，只限制 Go HTTP asset server 直接绑定 loopback；不覆盖 C04 的 Vite proxy 暴露路径。
- `wait_for_http` / `wait_for_backend`：见 `run-new-ui-desktop.sh:70-99` 和 `run-new-ui-desktop.sh:213-230`，确实存在 timeout；但它们是固定窗口，不能覆盖 C06 的慢但健康启动误判。
- readiness 失败分支的 `wait "$PID" || true`：见 `run-new-ui-desktop.sh:78-89`，只覆盖 readiness 阶段；不覆盖 C07 的稳态 `wait_for_any_process_exit` 和 hot supervisor 分支。

## 剔除内容

未计入本轮票数的内容：

- 输入报告中的旧编号和历史 INVALID 假设。
- 未落在 C01-C16 范围内的新增或夹带问题。
- 没有文件行号证据的纯结论。
- 5 个父级裁决 Agent 的 `BLOCKED_NESTED` 输出。
- 仍未完成或未返回的报告。实际可计票 leaf Agent 为 10 个，均已完成。

## 结论

C01-C16 全部以 10/10 VALID 票通过 7 票门槛；源码复核未发现足以覆盖这些具体风险的上层防护，因此全部进入修复清单。

阻断发布项：C01、C02、C04、C05、C10、C11、C12、C13、C14、C15。

非阻断但应修复项：C03、C06、C07、C08、C09、C16。

本报告为文档裁决产物，未修改业务代码。
