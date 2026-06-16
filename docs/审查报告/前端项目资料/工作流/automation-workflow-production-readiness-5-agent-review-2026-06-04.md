# 自动化工作流生产就绪性 5 Agent 审查报告

日期：2026-06-04

范围：当前分支的自动化工作流前后端代码，只读审查，未修改业务代码。重点覆盖 `run-new-ui-desktop.sh` / `run-new-ui-desktop-hot.sh`、`cmd/agent-terminal` 与 `internal/ui/wails` 桌面宿主、`internal/platform/rpc` Wails/HTTP bridge、`frontend-app` 自动化页面与 RPC facade、git hooks、CI、Linux/macOS 打包脚本。

目标基线：

- CWD：`/home/ai01@f666.com/桌面/project/Super-Dolphin`
- 目标分支：`main`
- 上游目标分支：`origin/main`
- BASE：`01f792b7b61051fd5076f6788bf9b83e31c207ff`
- HEAD：`01f792b7b61051fd5076f6788bf9b83e31c207ff`
- 初始工作区：干净，`git status --short` 无输出
- 用户未指定 diff 或其他目标分支，因此按当前 `HEAD == origin/main` 做生产就绪性审查。

已读取项目地图与索引：

- `README.md`
- `docs/doc/codemap/README.md`
- `docs/doc/codemap/01-terminal-ui-react.md`
- `docs/doc/codemap/01-terminal-ui-go.md`
- `docs/doc/codemap/ai-index.json`
- `docs/doc/codemap/project-map/AI_PROJECT_MAP.md`
- `docs/decisions/*.md`、`docs/adr/*.md`、`docs/契约/*.md` 中与自动化、fail-fast、RPC、Wails、DAG 相关的约束通过定向 `rg` 复核。

Agent 调度说明：

- 已拉起 5 个只读审查 agent。
- 当前会话可用的是 `multi_agent_v1` 子代理工具；仓库 AGENTS.md 要求的 `mcp-go-agent-orchestration` 生命周期工具未暴露在本轮工具面，因此本次以 `multi_agent_v1` 完成并在主会话中复核证据。
- 5 个 agent 分工：
  - Agent 1：shell 启动、热重载、devserver readiness。
  - Agent 2：Go/Wails 后端、HTTP/RPC bridge、生命周期。
  - Agent 3：React 自动化页、Wails bridge、状态事件。
  - Agent 4：测试、guard、git hooks、CI、打包自动化。
  - Agent 5：安全、容量、timeout、fail-fast 交叉审查。

## 总结

VALID 风险：11 项。其中发布阻断：8 项；非阻断但建议修复：3 项。

INVALID 已有防护：11 项。INVALID 条目不是风险，它们是被复核后确认已有上层防护或路径不可达的审查假设。

## VALID Findings

### F-001 - VALID - 桌面冷启动顺序与 Wails dev preRun 存在真实竞态

证据：

- `run-new-ui-desktop.sh:725-731` 先 `start_desktop_backend`、`wait_for_backend`、`seed_dev_preferences`，然后才启动 `frontend-app` Vite 并等待 `VITE_DEV_URL`。
- `internal/app/new_ui_scripts_test.go:69-70` 用字符串顺序测试锁定了“后端先于 Vite”的顺序。
- Wails dev build 的 `preRun()` 会等待前端 dev server 最多 10 次、每次 500ms，然后 fatal：`github.com/wailsapp/wails/v3@v3.0.0-alpha.74/pkg/application/application_dev.go:14-37`。
- `wailsApp.Run()` 在 `preRun()` 之后继续：`github.com/wailsapp/wails/v3@v3.0.0-alpha.74/pkg/application/application.go:553-556`。
- 桌面 app 在 Run 前创建窗口和 asset 配置：`internal/ui/wails/module.go:124-147`，窗口 URL 来自 `FRONTEND_DEVSERVER_URL`：`internal/ui/wails/window.go:118-145`。

调用链：

`run-new-ui-desktop.sh` -> `start_desktop_backend` -> `go run ./cmd/agent-terminal` -> `app.RunDesktop` -> `wailsApp.Run()` -> Wails dev `preRun()` 等待前端 -> 此时脚本尚未启动 Vite。

风险影响：

在 dev/验收路径中，后端进程可能在 Vite 尚未启动时因 Wails devserver 等待超时退出。脚本随后只看到桌面后端提前退出，用户得到冷启动失败，而不是稳定的一键启动流程。

是否阻断发布：是。如果 `run-new-ui-desktop.sh` 是当前 React 自动化 UI 的验收或发布前冒烟入口，此项阻断。

### F-002 - VALID - `FRONTEND_DEVSERVER_URL` 与 `VITE_DEV_URL` 可分叉，导致 false-ready

证据：

- `run-new-ui-desktop.sh:649-650` 允许 `FRONTEND_DEVSERVER_URL` 单独覆盖，默认才等于 `VITE_DEV_URL`。
- 脚本只等待 `VITE_DEV_URL`：`run-new-ui-desktop.sh:731`。
- 实际 Wails 窗口 URL 读取 `FRONTEND_DEVSERVER_URL`：`internal/ui/wails/window.go:118-145`。
- `windowURL()` 遇到无法解析的 base 时直接返回 base，不报错：`internal/ui/wails/window.go:133-135`。

调用链：

`.env` 或 shell 设置 `FRONTEND_DEVSERVER_URL=http://127.0.0.1:5174`，同时 `VITE_DEV_URL=http://127.0.0.1:5175` -> 脚本启动并等待 5175 -> Wails 窗口加载 5174。

风险影响：

启动脚本可报告 `frontend-app vite ready`，但桌面窗口加载另一个地址、旧服务或不可达服务。该路径没有同源一致性校验，也没有在 readiness 中验证实际窗口目标。

是否阻断发布：是。它会让自动化 UI 的本地验收出现 false-ready。

### F-003 - VALID - Vite 外部 bind 可绕过 Go HTTP loopback 防护并代理 `/wails/ws`

证据：

- `run-new-ui-desktop.sh:651-657` 对 `VITE_DEV_URL` 只校验 host/port 非空。
- `run-new-ui-desktop.sh:729` 把解析出的 `VITE_DEV_HOST` 直接传给 Vite `--host`。
- `frontend-app/vite.config.js:33-40` 将 `/wails/ws` 和 `/generated-image` 代理到后端地址。
- dev runtime shim 连接当前页面 host 的 `/wails/ws`：`frontend-app/public/wails/runtime.js:64-70`。
- Go HTTP asset server 自身有 loopback 防护：`internal/ui/wails/http_server.go:104-115`，但该防护只约束直连后端，不约束 Vite。
- `/wails/ws` 注册到 WebSocket RPC handler：`internal/ui/wails/http_server.go:29-32`。
- WebSocket RPC 最终执行 registered methods：`internal/platform/rpc/server.go:269-308`。

调用链：

`.env` 设置 `VITE_DEV_URL=http://0.0.0.0:5175` 或 LAN host -> Vite 对外监听 -> 外部浏览器访问 Vite -> `/wails/ws` 被 Vite 代理到 loopback 后端 -> 后端 RPC 被调用。

风险影响：

`validateHTTPAssetAddr()` 保护的是 Go 端口，但外部可达 Vite 会成为代理入口，扩大本地自动化后端的可访问面。该路径尤其影响自动化 DAG 启停、文件、日志、代码打开等 Wails RPC surface。

是否阻断发布：是，除非明确要求 devserver 对外暴露并另有认证/授权保护。

### F-004 - VALID - 本地 PostgreSQL 端口未做数值校验，可进入 `pg_ctl -o` 参数串

证据：

- `SUPER_DOLPHIN_LOCAL_POSTGRES_PORT` 只有默认值，没有数值校验：`run-new-ui-desktop.sh:682`。
- 默认 `DATABASE_URL` 直接拼接该端口：`run-new-ui-desktop.sh:575`。
- `postgres_is_local_database_url()` 从 authority 中切出 port，但不校验数字或范围：`run-new-ui-desktop.sh:529-557`。
- `ensure_local_postgres()` 将 port 用于 `lsof` 和 `pg_ctl -o`：`run-new-ui-desktop.sh:604`、`run-new-ui-desktop.sh:621-624`。

调用链：

`SUPER_DOLPHIN_LOCAL_POSTGRES_PORT='55433 -c max_connections=1'` -> 拼接进本地 `DATABASE_URL` -> `POSTGRES_URL_PORT` 保留额外内容 -> `pg_ctl -o "-h $host -p $port -k ..."`。

风险影响：

这是配置校验和 fail-fast 缺口。错误或恶意端口值不会在入口被阻断，可能导致 PostgreSQL 以非预期参数启动，或让脚本进入难诊断的启动状态。

是否阻断发布：是。自动化工作流依赖本地数据库启动时，应先校验端口为合法整数和范围。

### F-005 - VALID - 自动化页面的状态变更 RPC 缺少统一客户端 timeout

证据：

- `withTimeout()` 已存在：`frontend-app/src/pages/shared/pageShared.js:14-22`。
- 自动化 dashboard 读请求使用了 timeout：`frontend-app/src/pages/workflows/WorkflowPage.jsx:52-57`。
- 状态变更动作没有包 timeout：启动 DAG `frontend-app/src/pages/workflows/WorkflowPage.jsx:906`，保存计划 `frontend-app/src/pages/workflows/WorkflowPage.jsx:983`，切换计划 `frontend-app/src/pages/workflows/WorkflowPage.jsx:1008`。
- DAG API facade 转到后端 RPC：`frontend-app/src/shared/api/backendApi.js:858-865`。
- `callAPI()` 调用 `invokeRuntimeByID()`：`frontend-app/src/shared/api/wailsBridge.js:598-620`。
- `invokeRuntimeByID()` 直接 `await runtime.Call.ByID(...)`，没有 abort、timeout 或 watchdog：`frontend-app/src/shared/api/wailsBridge.js:402-424`。

调用链：

用户点击启动/保存/切换自动化 -> `WorkflowPage` 设置 `actioning` -> `startDag` 或 `applyDagOps` -> `callAPI` -> Wails `Call.ByID` promise 若卡住则一直不返回。

风险影响：

自动化核心操作可能无限挂起，按钮/状态保持 actioning，错误状态不会落地。已有 timeout 只覆盖 dashboard 查询，不覆盖 state-changing workflow action。

是否阻断发布：是。自动化启停和调度保存属于核心用户路径。

### F-006 - VALID - DAG 状态事件的 fail-fast 校验被订阅包装层吞掉

证据：

- DAG 节点状态事件要求 `dag_key`、`node_key`、`new_status`、run identity：`frontend-app/src/entities/client/model/useClientStore.js:175-183`。
- `bridgeRevisionKey()` 对 `task/node/statuschanged` 调用该校验：`frontend-app/src/entities/client/model/useClientStore.js:185-193`。
- bridge event handler 在事件入口调用 `bridgeRevisionKey()`：`frontend-app/src/entities/client/model/useClientStore.js:3489-3496`。
- 订阅入口是 `onBridgeEvent(runtime.handleBridgeEvent)`：`frontend-app/src/entities/client/model/useClientStore.js:3594-3597`。
- 但 `subscribeRuntimeEvent()` 包装 callback 时 catch 后只写日志，不重新抛出：`frontend-app/src/shared/api/wailsBridge.js:174-184`。

调用链：

后端推送 malformed `task/node/statuschanged` -> store 校验抛错 -> runtime subscription wrapper catch -> 只记录 `runtime.callback.failed` -> revision 不递增，UI 不刷新。

风险影响：

表面上存在 fail-fast 校验，实际被 bridge 包装层吞掉。自动化运行状态可能停留在旧值，用户误判 DAG 运行或节点状态。

是否阻断发布：是。自动化运行状态的可信度是核心能力。

### F-007 - VALID - 当前 React `frontend-app` 没有进入 git hooks 和 CI 的前端校验

证据：

- README 明确 `frontend-app/` 是当前 React/Vite 新 UI：`README.md:11-12`。
- `frontend-app/package.json:10-13` 定义了 `build`、`lint`、`test`。
- `pre-commit` 的前端路径只匹配 legacy `cmd/agent-terminal/frontend/*`：`.githooks/pre-commit:41-60`。
- `pre-commit` 前端测试只进入 legacy 目录：`.githooks/pre-commit:298-318`。
- `pre-push` 的前端路径只匹配 legacy `cmd/agent-terminal/frontend/*`：`.githooks/pre-push:119-139`。
- `pre-push` 前端测试只进入 legacy 目录：`.githooks/pre-push:239-262`。
- CI 只跑 Go vet/build/test 和 golangci-lint，没有 `frontend-app` lint/test/build：`.github/workflows/ci.yml:42-68`。

调用链：

修改 `frontend-app/src/pages/workflows/WorkflowPage.jsx` 或 `frontend-app/src/shared/api/backendApi.js` -> git hooks 不标记前端变更 -> CI 不跑 npm 校验 -> React 自动化 UI regressions 可进入主线。

风险影响：

当前自动化前端的 lint、unit test、build 不在提交/推送/CI 防线内。该风险已被多个 agent 独立命中。

是否阻断发布：是。

### F-008 - VALID - Linux 打包仍构建 legacy Vue 前端，未嵌入当前 React `frontend-app`

证据：

- `scripts/package_linux.sh:642-646` 进入 `cmd/agent-terminal/frontend` 后执行 `npm ci` 和 `npm run build`。
- 随后 `scripts/package_linux.sh:648-652` 调用 `make build-agent-terminal-plain`。
- `Makefile:10` 将 `FRONTEND_DIR` 固定为 legacy `cmd/agent-terminal/frontend`，`frontend-build` 也只构建该目录：`Makefile:40-49`。
- macOS 打包已经构建 `frontend-app` 并 rsync 到 embed dist：`scripts/package_macos.sh:1206-1218`。
- 现有测试只锁了 macOS 新前端嵌入路径：`scripts/package_macos_guard_test.go:166-175`。

调用链：

Linux release 打包 -> build legacy Vue frontend -> build `cmd/agent-terminal` embed dist -> Linux 包包含旧 UI，而非当前 React 自动化 UI。

风险影响：

Linux 产物与当前桌面新 UI 不一致，自动化工作流页面可能发布为旧界面或缺失新功能。

是否阻断发布：是，至少阻断 Linux 发布。

### F-009 - VALID - `/wails/ws` WebSocket bridge 缺少消息大小和连接容量限制

证据：

- WebSocket upgrader 是默认值：`internal/platform/rpc/transport_ws.go:21`。
- `WSHandler()` 每个连接创建 jrpc2 server，没有看到连接数上限：`internal/platform/rpc/transport_ws.go:25-60`。
- `wsChannel.Recv()` 直接 `ReadMessage()`，未设置 `SetReadLimit()`：`internal/platform/rpc/transport_ws.go:275-284`。
- HTTP server 有 read/write/idle timeout：`internal/ui/wails/http_server.go:65-70`，但这些不等价于 WebSocket message size limit。

调用链：

本机页面或通过 F-003 的 Vite 外部代理连接 `/wails/ws` -> 发送超大 JSON-RPC frame -> `ReadMessage()` 读取并交给 jrpc2 dispatch。

风险影响：

容量限制缺口。直接后端端口有 loopback 防护，但一旦 Vite 外部暴露或本机进程滥用，WebSocket frame 可造成内存压力。

是否阻断发布：否，作为独立项不阻断；与 F-003 叠加时建议升为发布前修复。

### F-010 - VALID - 后端 hot reload watch override 没有容量上限或最小轮询间隔

证据：

- watch paths 与 poll interval 都可由环境变量覆盖：`run-new-ui-desktop.sh:668-669`。
- `snapshot_backend_watch_state()` 逐词遍历 `SUPER_DOLPHIN_HOT_WATCH_PATHS` 并递归 `find`：`run-new-ui-desktop.sh:310-324`。
- supervisor 循环直接 `sleep "$interval"`，没有最小值或数值校验：`run-new-ui-desktop.sh:329-360`。

调用链：

`SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1 SUPER_DOLPHIN_HOT_POLL_INTERVAL=0 SUPER_DOLPHIN_HOT_WATCH_PATHS=".." ./run-new-ui-desktop-hot.sh` -> 高频扫描大目录 -> CPU/IO 压力。

风险影响：

dev hot path 容量风险。默认路径较窄，但 override 缺少 fail-fast 校验和文件数量/路径边界。

是否阻断发布：否。建议作为开发体验与资源保护修复。

### F-011 - VALID - 稳态等待进程退出时，`set -e` 会跳过失败日志尾部

证据：

- 脚本启用 `set -euo pipefail`：`run-new-ui-desktop.sh:3`。
- `wait_for_any_process_exit()` 在失败进程路径里先执行 `wait "$PID"`，再读取 `$?` 和打印日志：`run-new-ui-desktop.sh:233-249`。
- hot supervisor 也先 `wait "$PID"` 再取状态和打印日志：`run-new-ui-desktop.sh:337-351`。
- 日志尾部函数存在：`run-new-ui-desktop.sh:196-210`。

调用链：

前端或后端在 readiness 之后以非 0 状态退出 -> `process_exited` 为真 -> `wait "$PID"` 返回非 0 -> 在 `set -e` 下 shell 可能立即退出 -> `status="$?"` 和 log tail 不执行。

风险影响：

不改变失败事实，但会削弱故障诊断，尤其影响自动化工作流本地验收失败时的可维护性。

是否阻断发布：否。

## INVALID Findings / 已有防护

### I-001 - INVALID - “stale Vite 或端口冲突会静默覆盖”

防护函数：

- `stop_stale_vite_for_port`
- `fail_if_port_busy`
- `wait_for_port_free`
- `stop_process_tree`

证据：

- stale Vite 只在命令行匹配 `frontend-app/node_modules/.bin/vite` 且 cwd 等于 `FRONTEND_APP_DIR` 时停止：`run-new-ui-desktop.sh:180-194`。
- 端口占用会 fail-fast：`run-new-ui-desktop.sh:101-108`、`run-new-ui-desktop.sh:700-703`。
- backend restart 等待端口释放：`run-new-ui-desktop.sh:294-303`。

风险影响：无真实风险，已有防护。

是否阻断发布：否。

### I-002 - INVALID - “`VITE_DEV_URL` 缺 host/port 仍会半启动”

防护函数/路径：

- `VITE_DEV_URL` host/port validation in `run-new-ui-desktop.sh`

证据：

- `run-new-ui-desktop.sh:651-659` 解析并校验 host 和 port，缺失时直接退出。

风险影响：无真实风险。注意：该防护只处理缺 host/port，不处理 F-003 的外部 host 安全边界。

是否阻断发布：否。

### I-003 - INVALID - “启动脚本没有 readiness timeout”

防护函数：

- `wait_for_http`
- `wait_for_backend`
- `ensure_local_postgres`

证据：

- 前端 readiness 有约 10s 超时和进程提前退出检测：`run-new-ui-desktop.sh:70-99`。
- 后端 `/metrics` readiness 有约 20s 超时和 backend 退出检测：`run-new-ui-desktop.sh:213-230`。
- PostgreSQL 启动使用 `pg_ctl -w -t 30`：`run-new-ui-desktop.sh:621-624`。

风险影响：无“完全没有 timeout”的风险。F-001/F-011 是更窄的真实问题。

是否阻断发布：否。

### I-004 - INVALID - “Go HTTP asset server 可直接绑定公网”

防护函数：

- `validateHTTPAssetAddr`

证据：

- `validateHTTPAssetAddr()` 只允许 `localhost`、`127.0.0.1`、`::1`：`internal/ui/wails/http_server.go:104-115`。
- server `Run()` 启动前先调用该校验：`internal/ui/wails/http_server.go:57-60`。
- 测试覆盖非 loopback 拒绝：`internal/ui/wails/http_server_test.go:53-90`。

风险影响：直连 Go HTTP server 的公网绑定风险 INVALID。F-003 是 Vite 代理旁路，不被此函数覆盖。

是否阻断发布：否。

### I-005 - INVALID - “桌面关闭或后端失败没有上层生命周期防护”

防护函数：

- `reportRuntimeExit`
- `watchFXShutdown`
- `WailsLifecycle.requestBackendShutdown`
- `WailsLifecycle.armShutdownTimer`
- `stopFXApp`
- `preDrainDesktopRuntime`

证据：

- runtime run group 结束后报告错误并请求 shutdown：`internal/app/runner.go:158-170`。
- runtime 非预期退出通知 lifecycle：`internal/app/runner.go:206-214`。
- FX shutdown watcher 会通知前端后端失败：`internal/app/app.go:303-319`。
- Wails lifecycle 有 15s hard deadline：`internal/ui/wails/lifecycle.go:14-20`、`internal/ui/wails/lifecycle.go:245-258`。
- FX stop 和 runtime drain 都使用 `platformconfig.WithTimeout`：`internal/app/app.go:270-286`。

风险影响：无真实风险。

是否阻断发布：否。

### I-006 - INVALID - “dashboard 自动化列表无容量限制”

防护函数/常量：

- `dashboardPageDefaultLimit`
- `group.SetLimit`

证据：

- dashboard 默认列表 limit 为 100：`internal/module/dashboard/ui_page.go:17-23`。
- dashboard DAG page 使用该 limit：`internal/module/dashboard/ui_page.go:126-131`。
- latest run lookup 并发限制为 4：`internal/module/dashboard/ui_page.go:145-165`。

风险影响：对 dashboard page 的“无界查询”判断 INVALID。

是否阻断发布：否。

### I-007 - INVALID - “React RPC facade 没有参数校验”

防护函数：

- `assertPlainObject`
- `requireCwd`
- `requireThreadId`
- `requireKey`
- `requireNumber`
- `dashboardDagStartPayload`
- `dashboardDagApplyOpsPayload`

证据：

- 通用对象、cwd、threadId、key 校验：`frontend-app/src/shared/api/backendApi.js:134-193`。
- DAG start 校验 `dagKey`：`frontend-app/src/shared/api/backendApi.js:396-402`。
- DAG apply ops 校验 `dagKey`、`baseVersion`、`ops` 数组：`frontend-app/src/shared/api/backendApi.js:455-468`。
- 测试覆盖 DAG/cron/config 参数拒绝：`frontend-app/src/shared/api/backendApi.test.js:418-475`。

风险影响：无真实风险。F-005 指向 timeout，不是否定参数校验。

是否阻断发布：否。

### I-008 - INVALID - “Wails runtime 不可用时前端静默成功”

防护函数：

- `invokeRuntimeByID`
- `callAPI`

证据：

- runtime 缺 `Call.ByID` 时 `invokeRuntimeByID()` 抛出 `Wails runtime bridge not ready`：`frontend-app/src/shared/api/wailsBridge.js:411-420`。
- `callAPI()` catch 后附加 trace 并继续抛出：`frontend-app/src/shared/api/wailsBridge.js:598-620`。
- 测试覆盖 backend unavailable reject 与日志记录：`frontend-app/src/shared/api/wailsBridge.test.js:188-216`。

风险影响：无“静默成功”风险。

是否阻断发布：否。

### I-009 - INVALID - “Wails 代码文件操作存在路径穿越”

防护函数：

- `scopedCandidate`
- `secureRelativeToRoot`
- `requestScopeRoots`
- `locateScopedFile`

证据：

- 候选路径必须通过 `secureRelativeToRoot()`：`internal/ui/wails/code_scope.go:286-306`。
- `secureRelativeToRoot()` 拒绝 `.`、`..` 和 root 外路径：`internal/ui/wails/code_scope.go:309-325`。
- 打开文件大小上限为 10 MiB：`internal/ui/wails/code_preview.go:21`。

风险影响：路径穿越判断 INVALID。

是否阻断发布：否。

### I-010 - INVALID - “打开编辑器存在 shell command injection”

防护函数：

- `openCodeEditor`
- `codeOpenArgs`
- `openSystemPath`

证据：

- VS Code 路径通过 `exec.Command(command, codeOpenArgs(...))` 作为 argv 传递：`internal/ui/wails/code_preview.go:309-333`。
- macOS/Linux 系统打开也通过 `exec.Command(binary, path)` 传参：`internal/ui/wails/code_preview.go:495-500`。

风险影响：未发现 shell 拼接执行路径，command injection 判断 INVALID。

是否阻断发布：否。

### I-011 - INVALID - “前端 trace/log 会泄露 prompt 或 tool result”

防护函数：

- `sanitizeFrontendTraceEvent`
- `safeTraceMetadata`
- `containsForbiddenTraceText`
- `enqueueFrontendTraceEvent`
- `recordCallAPITrace`

证据：

- frontend trace 禁止 prompt/content/tool result 等 key：`frontend-app/src/shared/api/wailsBridge.js:29-43`。
- error 和 metadata 会去敏：`frontend-app/src/shared/api/wailsBridge.js:259-343`。
- trace queue 有 500 条上限：`frontend-app/src/shared/api/wailsBridge.js:386-399`。
- 后端 CallAPI trace 测试确认不会记录 raw params：`internal/ui/wails/binding_id_test.go:200-222`。

风险影响：该泄露路径 INVALID。仍需警惕非 trace 日志的独立泄露面，本项未发现真实可达风险。

是否阻断发布：否。

## 测试覆盖观察

- `internal/app/new_ui_scripts_test.go` 覆盖了脚本文本契约、readiness 日志、polling 默认值、stale Vite 清理、本地 Postgres 默认路径、provider preference seed。
- 覆盖缺口：
  - 未执行 shell 行为模拟，无法捕获 F-001 的 Wails dev preRun 顺序竞态。
  - 未覆盖 `FRONTEND_DEVSERVER_URL` 与 `VITE_DEV_URL` 分叉。
  - 未覆盖 Vite 外部 bind + `/wails/ws` proxy 旁路。
  - 未覆盖 `SUPER_DOLPHIN_LOCAL_POSTGRES_PORT` 非数值 fail-fast。
  - 未覆盖 React workflow state-changing RPC timeout。
  - 未覆盖 malformed DAG event 是否真正 fail-fast。
  - 未覆盖 Linux package 嵌入 `frontend-app`。
  - git hooks 和 CI 未覆盖 `frontend-app`。

## 发布建议

阻断发布的优先修复顺序：

1. 统一 `run-new-ui-desktop.sh` 的前端先启动/实际窗口 URL readiness，或明确禁用 Wails dev preRun 对未启动 Vite 的 fatal 路径。
2. 强制 `FRONTEND_DEVSERVER_URL` 与 `VITE_DEV_URL` 一致，或分别校验并等待实际窗口 URL。
3. 限制 `VITE_DEV_URL` host 为 loopback，或为 Vite proxy 加认证/禁用外部代理。
4. 对 `SUPER_DOLPHIN_LOCAL_POSTGRES_PORT` 做数字和范围校验，异常立即 fail-fast。
5. 为自动化 state-changing RPC 加统一 timeout/abort 与 UI recovery。
6. 让 malformed DAG bridge event 的校验错误进入可见失败路径，而不是只被 subscription wrapper 记录。
7. 将 `frontend-app` 纳入 pre-commit、pre-push、CI 的 lint/test/build。
8. 修正 Linux package，使其和 macOS 一样构建并嵌入 `frontend-app`，并添加对应 guard test。

本次报告本身为 docs-only 产物；没有改动业务代码。
