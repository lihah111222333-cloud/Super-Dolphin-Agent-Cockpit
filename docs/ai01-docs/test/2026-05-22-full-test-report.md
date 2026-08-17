# Super-Dolphin 全量测试报告（2026-05-22）

## 结论摘要

- 测试目标：在 `D:\project\Super-Dolphin` 当前 `origin/main` 代码上执行白盒、黑盒、灰度/本地 canary、Playwright UX 与真实 provider 最小端到端验证。
- 当前代码：`main...origin/main`，测试前 `HEAD=d7b070e2ee36fbb1e399ac52838e9aea4ab44a03`，测试前工作区干净。
- 测试数据库：`super_agent_v3_ai01_fulltest_20260522`，由 `psql.exe` 重建；启动后 `schema_migrations` 共 73 条，最大版本 91。
- 启动状态：`run-debug.ps1` 菜单 `1 -> 2` 成功拉起后端和 MCP peer；`4511/8090` 由 `super-agent-debug.exe` 监听，`mcp-orch.exe` 与 `mcp-lsp.exe` 存活。
- 主要阻塞：
  - `make` 不在 PATH，所有 Makefile 包装命令无法直接执行。
  - `make test` 的等价 fallback 暴露 Go 测试失败，主要集中在 `cmd/mcp-lsp` 可信 workspace root/Windows path 处理，以及 provider mirror/home 路径相关测试。
  - 前端 size guard 有 31 处违规，阻断 `vitest`、`npm run build`、mock E2E、real E2E。
  - `make codemap-check` fallback 显示 codemap 生成物陈旧。
  - SQLC fallback 生成过程使 `internal/store/sqlc` 下多份跟踪文件变脏（主要表现为 LF/CRLF 触碰），已恢复，说明 Windows 当前配置下 SQLC 生成不满足干净校验。
  - 真实 Codex/Claude provider 调用未进入外部模型，均被本机 `C:\Users\ai01\.super-dolphin\owner_identity.salt` 权限校验挡住：`owner identity salt permissions -rw-rw-rw- are not 0600`。
- 报告之外没有保留源码改动；测试期间生成的缓存、日志、截图均在临时目录或已忽略目录中。

## 测试环境

- 仓库：`D:\project\Super-Dolphin`
- Git HEAD：`d7b070e2ee36fbb1e399ac52838e9aea4ab44a03`
- 分支状态：`## main...origin/main`
- `.env`：保留，`DATABASE_URL=postgres://postgres:123456@127.0.0.1:5432/super_agent_v3?sslmode=disable`
- 本次启动覆盖：
  - `DATABASE_URL=postgres://postgres:123456@127.0.0.1:5432/super_agent_v3_ai01_fulltest_20260522?sslmode=disable`
  - `PROJECT_ROOT=D:\project\Super-Dolphin`
  - `GO_AGENT_LSP_ROOT=D:\project\Super-Dolphin`
  - `GO_AGENT_LSP_ROOTS=["D:\\project\\Super-Dolphin"]`
  - `GO_AGENT_CTL_SESSION_TOKEN` / `GO_AGENT_MCP_SESSION_TOKEN`：本次临时 token
  - `AUTO_CODEMAP_REFRESH=0`
- 日志根目录：`C:\Users\ai01\AppData\Local\Temp\super-dolphin-fulltest-20260522`
- App 日志目录：`C:\Users\ai01\.super-dolphin\log\Super-Dolphin`

## 命令矩阵

| 类别 | 命令 | 结果 | 关键证据 |
|---|---|---:|---|
| 预检 | `git status --short --branch` | PASS | 测试前为 `## main...origin/main` |
| 预检 | `git rev-parse HEAD` | PASS | `d7b070e2ee36fbb1e399ac52838e9aea4ab44a03` |
| 预检 | `npx playwright --version` | PASS | `Version 1.58.2` |
| 预检 | 检查 `C:\Program Files\PostgreSQL\16\bin\psql.exe` | PASS | 文件存在且可用 |
| DB | 重建 `super_agent_v3_ai01_fulltest_20260522` | PASS | `db-recreate.log` |
| Guard | `make guard` | FAIL | `make` 未安装：`make_guard.log` |
| Guard fallback | `go run ./scripts/code_size_guard.go --strict` | PASS | `guard_fallback__go_run_code_size_guard_--strict.log` |
| Guard fallback | `go test -run TestCodeSizeGuard ./internal/archtest/... -count=1` | PASS | `guard_fallback__go_test_TestCodeSizeGuard.log` |
| Go | `go vet ./...` | PASS | `go_vet_._....log` |
| Go | `make test` | FAIL | `make` 未安装：`make_test.log` |
| Go fallback | `go test ... -race` | FAIL | `-race requires cgo`：`make_test_fallback__*.log` |
| Go fallback | `CGO_ENABLED=1 go test ... -race` | FAIL | 缺少 `gcc`：`race_preflight__CGO_ENABLED_1_go_test_provider_deferred.log` |
| Go fallback | `go test` 非 provider 包 no-race | FAIL | LSP/Windows path root 相关失败：`go_test_non-deferred_packages_no-race_fallback.log` |
| Go fallback | `go test ./internal/provider/claudecli ./internal/provider/codexapp` | FAIL | provider mirror/home/path 相关失败：`go_test_deferred_provider_packages_no-race_fallback.log` |
| Build | `make build-plain` | FAIL | `make` 未安装：`make_build-plain.log` |
| Build fallback | `go build ./...` | PASS | `make_build-plain_fallback__go_build_._....log` |
| SQLC | `make sqlc-verify` | FAIL | `make` 未安装：`make_sqlc-verify.log` |
| SQLC fallback | `sqlc generate` + `git diff --quiet -- internal/store/sqlc` | FAIL | 生成后 `internal/store/sqlc` 出现多文件 tracked diff，已恢复；见 `git_status_after_backend_checks.log` |
| Codemap | `make codemap-check` | FAIL | `make` 未安装：`make_codemap-check.log` |
| Codemap fallback | `go run scripts/codemap_index.go --check` | FAIL | `ai-index.json` 与 `README.md` 陈旧：`codemap_check_fallback__go_run_scripts_codemap_index.go_--check.log` |
| Frontend | `node scripts/size-guard.cjs` | FAIL | 31 处违规：`frontend_size_guard.log` |
| Frontend | `npx vitest run` | FAIL | global setup 被 size guard 阻断：`frontend_vitest.log` |
| Frontend | `npm run build` | FAIL | `prebuild` 被 size guard 阻断：`frontend_npm_run_build.log` |
| 启动 | `run-debug.ps1` 菜单 `1 -> 2` | PARTIAL PASS | 后端/MCP 存活；初始 Vite 后续退出；见 `run-debug-isolated-state.log` |
| HTTP | `http://127.0.0.1:4511/metrics` | PASS | HTTP 200，内容长度约 9730 |
| HTTP | `http://localhost:5173` | PARTIAL PASS | run-debug 初始可达；后续 Vite 退出，测试期间手动补启动 |
| Playwright | `npm run test:e2e` | FAIL | size guard 阻断：`playwright_mock_e2e.log` |
| Playwright | `npm run test:e2e:real -- --skip-build --base-url=http://127.0.0.1:4511` | INVALID PASS | 包装脚本只输出开始执行后返回 0，未展示实际 Playwright 结果：`playwright_real_e2e.log` |
| Playwright | 直接运行 `playwright.cmd test -c playwright.real.config.js ...` | FAIL | size guard 阻断：`playwright_real_e2e_direct.log` |
| Playwright UX | 系统 Chrome 探测 `http://localhost:5173` | PASS WITH WARNINGS | 页面 200、截图非空、控制台 1 个 404 + 多条 warning：`playwright_ux_probe_system_chrome.log` |
| Provider | Codex 最小真实调用 | BLOCKED | `owner_identity.salt` 权限不满足 0600：`provider_live_minimal.log` |
| Provider | Claude 最小真实调用 | BLOCKED | 同上 |

## 白盒测试详情

### Guard 与 Go 构建

- `make guard`、`make test`、`make build-plain`、`make sqlc-verify`、`make codemap-check` 均未能执行，因为当前 shell 中 `make` 不可用。
- 已执行 fallback：
  - `go run ./scripts/code_size_guard.go --strict` 通过。
  - `go test -run TestCodeSizeGuard ./internal/archtest/... -count=1` 通过。
  - `go vet ./...` 通过。
  - `go build ./...` 通过。

### Go 测试失败摘要

`go test` no-race fallback 失败集中在两类：

- `cmd/mcp-lsp` 及其子包：
  - `GO_AGENT_LSP_ROOTS` / trusted workspace root 在 Windows 路径下出现 `\\C:\... is outside workspace roots`。
  - 多个测试报 `strict context enforcement: missing tool scope CWD`。
  - 部分 symlink 用例因 Windows 权限失败：`A required privilege is not held by the client`。
  - 相关日志：`go_test_non-deferred_packages_no-race_fallback.log`。
- `internal/provider/claudecli` 与 `internal/provider/codexapp`：
  - home/mirror/path normalization、additional workspace roots、pool spawn LSP config 等测试失败。
  - 相关日志：`go_test_deferred_provider_packages_no-race_fallback.log`。

### SQLC 与 Codemap

- SQLC fallback 生成后，`internal/store/sqlc` 下多份 tracked 文件被标记为修改。日志显示 Git 提示这些文件会被 LF/CRLF 转换触碰；测试结束前已执行恢复，避免保留源码改动。
- Codemap fallback 明确失败：
  - `docs\doc\codemap\ai-index.json differs from generated output`
  - `docs\doc\codemap\README.md differs from generated output`
  - 建议命令：`make codemap-refresh`（当前环境需先解决 `make` 缺失）。

## 前端与 Playwright

### Frontend 守卫

`node scripts/size-guard.cjs` 报 31 处违规，直接阻断：

- `npx vitest run`
- `npm run build`
- `npm run test:e2e`
- 直接执行 `playwright.real.config.js` 的真实 E2E

代表性违规：

- `stores\projects.js::useProjectStore` 嵌套深度 6，超过上限 5。
- `utils\citation-preview-utils.js::resolveCitationImagePreview` 嵌套深度 6，超过上限 5。
- `composables\useAutoScroll.js::useAutoScroll` 289 有效行，超过上限 250。
- `pages\MemoryCenterPage.js::setup` 268 有效行，超过上限 250。
- `pages\SystemPromptPage.js::setup` 286 有效行，超过上限 250。
- 多个 `assistant-markdown*.js`、`thread-live-patch.js`、`SystemPromptPage.js` 文件存在嵌套三元表达式违规。

### Playwright 环境

- `npx playwright --version` 可用，版本为 `1.58.2`。
- 首次 UX 探测失败，因为 Playwright 浏览器缓存缺失：
  - `Executable doesn't exist at C:\Users\ai01\AppData\Local\ms-playwright\chromium_headless_shell-1208\...`
- 执行 `npx playwright install chromium` 时下载到 100% 后卡住并超时，留下安装锁；确认残留安装进程后已终止并清理锁。
- 最终使用系统 Chrome：
  - `C:\Program Files\Google\Chrome\Application\chrome.exe`

### UX 探测

通过系统 Chrome 对 `http://localhost:5173` 做页面加载与截图：

- HTTP 状态：200。
- 标题：`Agent Orchestrator`。
- 页面主体非空，`bodyLength=179`。
- UI 结构：18 个 button，2 个输入控件，2 个导航区域，2 个 shell 候选节点。
- 截图：`C:\Users\ai01\AppData\Local\Temp\super-dolphin-fulltest-20260522\ux-home-1440x1000.png`，大小 66528 字节。
- 控制台事件：1 个 404 error，多条 `[AO]` / `[TRACE-CWD]` warning。
- 页面文本中出现 `正在退出...`，说明当前 UI 状态对用户不够明确，需要后续结合 runtime 状态确认是否为误提示或真实退出状态。

## 黑盒与灰度/本地 Canary

### 启动与端口

`run-debug.ps1` 菜单 `1 -> 2` 通过临时覆盖项启动。当前进程与端口：

| 端口/进程 | 状态 |
|---|---|
| `4511` | `super-agent-debug.exe` PID 31012 监听 |
| `8090` | `super-agent-debug.exe` PID 31012 监听 |
| `5173` | run-debug 启动的 Vite 后续退出；测试期间补启动 `npx vite --port 5173 --strictPort`，PID 25588 |
| `mcp-orch.exe` | PID 11432 存活 |
| `mcp-lsp.exe` | PID 3356 存活 |

HTTP 结果：

- `http://127.0.0.1:4511/metrics` 返回 200。
- `http://localhost:5173` 在 run-debug 初始阶段返回 200；后续 Vite 退出导致拒绝连接；补启动后返回 200。
- `http://127.0.0.1:4511/` 在 Vite 恢复前返回 502，恢复后返回 200。

日志结果：

- 当前 `agent-terminal-2026-05-22-13.log` 长度为 0，未捕获到本次 `db pool ready` / `http asset server listening` 文本。
- MCP peer fallback 日志存在本次记录：
  - `mcp-orch.exe-2026-05-22.log` 中 PID 11432 对应 `mcp http: started`。
  - `mcp-lsp.exe-2026-05-22.log` 中 PID 3356 对应 `mcp http: started`。
- 因 app 主日志为空，本次日志验收对 `db pool ready` 和 `http asset server listening` 只能由端口、metrics、DB schema 状态间接证明，不能标为日志文本 PASS。

### 数据库

- `schema_migrations_count=73`
- `schema_migrations_max=91`
- `pg_stat_activity`：`super_agent_v3_ai01_fulltest_20260522|postgres|idle|8`

## 真实 Provider 测试

执行路径：

- 页面入口：`http://127.0.0.1:4511`
- API 调用：通过 `/vue-app/services/api.js` 的 `callAPI`，走 debug shim 的 Wails RPC。
- Codex payload：`provider=codex`、`model=gpt-5.5`、`modelProvider=openai`、`approvalPolicy=never`。
- Claude payload：`provider=claude`、`model=sonnet`、`approvalPolicy=never`。

结果：

- Codex：`thread/start` 失败，未进入 `turn/start`，未调用外部模型。
- Claude：`thread/start` 失败，未进入 `turn/start`，未调用外部模型。
- 共同错误：
  - `owner identity salt permissions -rw-rw-rw- are not 0600`
- 代码定位：
  - `internal/module/skill/scope_model.go` 中校验 owner identity salt 必须是 regular file 且权限为 `0600`。
- 本机文件状态：
  - 路径：`C:\Users\ai01\.super-dolphin\owner_identity.salt`
  - 长度：32
  - Owner：`F666\ai01`
  - ACL 包含 `win11-01\CodexSandboxUsers:ReadAndExecute`、`SYSTEM:FullControl`、`Administrators:FullControl`、`F666\ai01:FullControl`

判定：真实 provider 端到端被本机身份 salt 权限前置校验阻塞，不能判定 Codex/Claude 模型、认证或外部服务是否可用。

## 主要风险与建议

1. 优先修复环境缺口：
   - 安装或固定 `make`。
   - 若需要 race 测试，安装可用 C toolchain（例如 MinGW-w64），并显式验证 `CGO_ENABLED=1 go test -race`。
   - 修复 Playwright browser install 卡住问题，或在 Playwright 配置中明确使用系统 Chrome。

2. 优先处理当前会阻断所有前端验证的 size guard：
   - 先修 31 处违规，再重跑 `npx vitest run`、`npm run build`、`npm run test:e2e`、真实 E2E。

3. 修复 Go 测试中的 Windows path/trusted root 问题：
   - 重点看 `cmd/mcp-lsp`、`cmd/mcp-lsp/manager`、`cmd/mcp-lsp/multilsp`、`cmd/mcp-lsp/search`、`cmd/mcp-lsp/tools`。
   - 多数失败指向 Windows 路径被归一化为 `\\C:\...` 后与 workspace roots 比较失败。

4. 修复 provider mirror/home/path 测试：
   - 重点看 `internal/provider/claudecli` 与 `internal/provider/codexapp`。
   - 当前失败会影响真实 provider 启动、home/mirror reconciliation、LSP roots 注入。

5. 修复 `owner_identity.salt` 权限问题后再重跑真实 provider：
   - 当前错误发生在项目自己的 fail-fast 校验，不能绕过当作外部 provider 波动。
   - 修复前无需做更多真实模型调用，避免无效费用风险。

6. 修复 `run-debug.ps1` 的 Vite 生命周期可观测性：
   - 本次后端/MCP 存活，但 Vite 进程退出后没有主日志说明，导致 `4511/` 一度 502。
   - 建议将 Vite stdout/stderr 或退出码写入稳定日志，避免 UX 黑盒测试只能靠端口推断。

7. 修复 `scripts/run-real-e2e.cjs` 的假阳性风险：
   - 本次包装脚本只打印“开始执行真实环境 E2E”后返回 0，没有实际 Playwright 用例输出。
   - 直接执行 `playwright.real.config.js` 会被 size guard 阻断，说明包装脚本结果不可作为通过依据。

## 原始证据路径

- 临时日志目录：`C:\Users\ai01\AppData\Local\Temp\super-dolphin-fulltest-20260522`
- 关键日志：
  - `db-recreate.log`
  - `go_test_non-deferred_packages_no-race_fallback.log`
  - `go_test_deferred_provider_packages_no-race_fallback.log`
  - `frontend_size_guard.log`
  - `frontend_vitest.log`
  - `frontend_npm_run_build.log`
  - `playwright_mock_e2e.log`
  - `playwright_real_e2e.log`
  - `playwright_real_e2e_direct.log`
  - `playwright_ux_probe_system_chrome.log`
  - `provider_live_minimal.log`
  - `run-debug-isolated-state.log`
  - `manual-vite-output.log`
- 截图：
  - `C:\Users\ai01\AppData\Local\Temp\super-dolphin-fulltest-20260522\ux-home-1440x1000.png`
- App 日志：
  - `C:\Users\ai01\.super-dolphin\log\Super-Dolphin\agent-terminal-2026-05-22-13.log`
  - `C:\Users\ai01\.super-dolphin\log\Super-Dolphin\peer-fallback\mcp-orch.exe-2026-05-22.log`
  - `C:\Users\ai01\.super-dolphin\log\Super-Dolphin\peer-fallback\mcp-lsp.exe-2026-05-22.log`
