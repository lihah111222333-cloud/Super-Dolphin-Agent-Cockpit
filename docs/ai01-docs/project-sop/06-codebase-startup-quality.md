# Step 9-11：代码仓库结构、本地启动和质量门禁

## Step 9：代码仓库结构和核心模块

### 顶层结构

| 路径 | 说明 |
| --- | --- |
| `cmd/agent-terminal` | Wails 桌面宿主、HTTP server、RPC bridge、前端 asset 嵌入 |
| `cmd/mcp-orch` | 编排 peer，负责 agent lifecycle、DAG、cron、workspace 和工具资源 |
| `cmd/mcp-lsp` | 多语言 LSP peer，提供代码读取、xref、grep、edit 等工具 |
| `cmd/mcp-ida` | IDA MCP peer |
| `frontend-app` | 当前 React/Vite 新 UI |
| `cmd/agent-terminal/frontend` | 旧 Vue/Vite 嵌入式前端 |
| `internal/app` | 应用装配、生命周期、logger、preflight |
| `internal/contract` | 跨模块接口和 DTO |
| `internal/module` | 业务模块：thread、turn、prompt、skill、memory、cron、observability 等 |
| `internal/platform` | db、rpc、config、bus、runtime safety、toolbridge 等平台能力 |
| `internal/provider` | Codex、Claude CLI、DreamExec、unified provider |
| `internal/store` | sqlc store 和各业务 store |
| `migrations` | PostgreSQL schema migration |
| `sql` | sqlc queries 和补充 schema |
| `scripts` | 构建、打包、发布、校验脚本 |

### 核心模块阅读顺序

1. `README.md`：确认当前项目入口和命令。
2. `docs/doc/codemap/README.md`：确认相关子系统的 codemap 分卷。
3. `cmd/agent-terminal/main.go`：桌面主入口。
4. `internal/app/app.go`、`internal/app/modules.go`：Fx 装配和生命周期。
5. `internal/platform/config/config.go`：环境变量、dev entrypoint、路径规则。
6. `internal/platform/db/module.go`：数据库连接、自动迁移和 schema version。
7. `internal/platform/rpc/server.go`：RPC 注册、dispatch 和 websocket 推送。
8. `frontend-app/src/App.jsx`、`frontend-app/src/shared/api/backendApi.js`：当前 UI 页面和 RPC 面。
9. 任务相关模块，例如 `internal/module/thread`、`internal/module/prompt`、`cmd/mcp-orch/orchestration`。

## Step 10：本地启动项目并跑通核心流程

本次文档生成未实际启动应用。以下是接手时应执行的启动 SOP。

### 新 UI 推荐启动路径

从仓库根目录执行：

```powershell
bash ./run-new-ui-desktop.sh
```

脚本读取到的关键行为：

- 加载 `.env`。
- 确保或生成 `GO_AGENT_CTL_SESSION_TOKEN`。
- 检查 `frontend-app` 依赖。
- 确保或构建 `mcp-orch` 和 `mcp-lsp`。
- 在需要时启动本地 PostgreSQL。
- 启动 Vite，默认 `127.0.0.1:5175`。
- 启动后端 HTTP，默认 `127.0.0.1:4512`。
- 设置控制 RPC，默认 `127.0.0.1:8092`。
- 通过 `/metrics` 等待后端就绪。

### Windows debug 路径

```powershell
.\run-debug.ps1
```

已读取到的菜单语义：

- 选项 1：主分支调试。
- 选项 1 下的 server mode 可使用浏览器访问 `http://localhost:4511`。
- 脚本默认使用旧前端 Vite `cmd\agent-terminal\frontend`，端口 5173。
- 若无 `DATABASE_URL`，脚本会尝试使用 `postgres://postgres:123@127.0.0.1:5432/go_agent_v2?sslmode=disable`。

### 启动验收清单

| 检查项 | 命令或动作 | 期望结果 |
| --- | --- | --- |
| 后端指标 | `Invoke-WebRequest http://127.0.0.1:4512/metrics` 或脚本输出地址 | HTTP 200，返回 Prometheus 指标 |
| UI 页面 | 打开 Vite 或宿主地址 | 页面加载，无明显前端 fatal |
| RPC websocket | 访问 UI 并触发一次 API | 后端日志无 `/wails/ws` fatal |
| 数据库迁移 | 查看后端日志 | schema migration 完成且版本满足最低要求 |
| peer binaries | 查看 `.tmp` 或脚本输出 | `mcp-orch`、`mcp-lsp` 可启动 |
| 日志 | `.tmp/run-new-ui-desktop/backend.log`、`frontend.log` | 无启动 fatal 或端口冲突 |

### 核心流程 smoke 清单

1. 打开 Chat 页面。
2. 选择或确认当前 cwd 和 provider。
3. 创建一个 thread。
4. 发起一个简单 turn。
5. 确认 UI 能看到消息、状态或错误。
6. 打开 Observability 页面，查看 recent、slow、error 或 status。
7. 打开 Workflows 页面，读取 DAG 列表。
8. 打开 Prompts、Skills、Memory、Files 页面，确认基础列表请求不失败。
9. 如果 provider 依赖外部 CLI，确认登录态和权限。
10. 如失败，记录精确错误、日志路径、端口、环境变量和 commit。

## Step 11：测试体系和质量门禁

### Go 变更

单文件 guard：

```powershell
bash ./scripts/test_with_guard.sh internal/path/file.go
```

受影响包测试：

```powershell
bash ./scripts/test_with_guard.sh ./internal/module/thread -count=1
```

广泛 Go 变更：

```powershell
make test
make build-plain
```

### 前端新 UI 变更

```powershell
cd frontend-app
npm run lint
npm test
npm run build
```

### 旧前端变更

只在明确修改 `cmd/agent-terminal/frontend` 时运行：

```powershell
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

### 数据库和代码地图

```powershell
make sqlc-verify
make codemap-check
```

### Git hooks

当前读取到的 `core.hooksPath` 指向其他 worktree。接手当前 checkout 前应执行：

```powershell
make install-hooks
git config --get core.hooksPath
```

期望 hooksPath 指向当前仓库的 `.githooks` 绝对路径。

### 完成前检查

1. `git status --short`，确认只包含本任务应改文件。
2. `git diff -- <changed files>`，检查无无关格式化、日志、临时代码。
3. 对应测试、lint、build 或 smoke 命令通过。
4. 若跳过命令，说明原因和残留风险。
