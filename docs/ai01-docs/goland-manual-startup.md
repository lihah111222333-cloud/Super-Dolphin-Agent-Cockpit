# GoLand 手动启动指南

本文档用于不依赖根启动脚本时，在 GoLand 里手动启动当前 React/Vite 新 UI 桌面开发环境。

## 目标进程

- 前端：`frontend-app` 的 Vite dev server，固定使用 `http://127.0.0.1:5175`。
- 后端/桌面宿主：GoLand 启动 `cmd/agent-terminal`。
- peer 二进制：手动构建 `mcp-orch` 和 `mcp-lsp`，通过 `GO_AGENT_PEER_BIN_DIR` 交给桌面宿主。

## 1. 启动前端

在仓库根目录执行：

```bash
cd frontend-app
npm install
npm run dev
```

确认 Vite 输出的地址是 `http://127.0.0.1:5175`。如果端口被占用，先关闭旧进程，不要改成随机端口；后端环境变量会固定指向这个地址。

## 2. 构建 peer 二进制

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force -Path .tmp\goland\peers | Out-Null
go build -o .tmp\goland\peers\mcp-orch.exe .\cmd\mcp-orch
go build -o .tmp\goland\peers\mcp-lsp.exe .\cmd\mcp-lsp
```

macOS：

```bash
mkdir -p .tmp/goland/peers
go build -o .tmp/goland/peers/mcp-orch ./cmd/mcp-orch
go build -o .tmp/goland/peers/mcp-lsp ./cmd/mcp-lsp
```

当 `cmd/mcp-orch`、`cmd/mcp-lsp`、`internal`、`pkg` 或 `go.mod/go.sum` 有变化时，重新执行本步骤。

## 3. 配置 GoLand

新建 Go Build 配置：

- Run kind：`Package`
- Package path：`github.com/anthropic-ai/super-agent-v3/cmd/agent-terminal`
- Working directory：仓库根目录
- Program arguments：留空

环境变量按系统填写。

Windows 示例：

```text
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4512
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8092
VITE_DEV_URL=http://127.0.0.1:5175
FRONTEND_DEVSERVER_URL=http://127.0.0.1:5175
GO_AGENT_PEER_BIN_DIR=<repo>\.tmp\goland\peers
SUPER_DOLPHIN_RUNTIME_MODE=dev
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=<repo>
SUPER_DOLPHIN_DEV_ENTRYPOINT=goland
CODEX_HOME=%USERPROFILE%\.codex
SUPER_DOLPHIN_HOME=<repo>\.tmp\goland\super-dolphin-home
SUPER_DOLPHIN_DEV_PROVIDER=codex
SUPER_DOLPHIN_DEV_CODEX_MODEL=gpt-5.5
SUPER_DOLPHIN_DEV_CODEX_EFFORT=xhigh
SUPER_DOLPHIN_DEV_CODEX_HOME=%USERPROFILE%\.codex
SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY=default
SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER=openai
LOG_LEVEL=debug
ENABLE_MEMORY_SYSTEM=1
ENABLE_MEMORY_TOOLS=1
MULTI_AGENT_MEMORY_FEATURE_TEAMMEM=1
CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1
GO_AGENT_CTL_SESSION_TOKEN=dev-goland-local
```

macOS 示例：

```text
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4512
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8092
VITE_DEV_URL=http://127.0.0.1:5175
FRONTEND_DEVSERVER_URL=http://127.0.0.1:5175
GO_AGENT_PEER_BIN_DIR=<repo>/.tmp/goland/peers
SUPER_DOLPHIN_RUNTIME_MODE=dev
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=<repo>
SUPER_DOLPHIN_DEV_ENTRYPOINT=goland
CODEX_HOME=$HOME/.codex
SUPER_DOLPHIN_HOME=<repo>/.tmp/goland/super-dolphin-home
SUPER_DOLPHIN_DEV_PROVIDER=codex
SUPER_DOLPHIN_DEV_CODEX_MODEL=gpt-5.5
SUPER_DOLPHIN_DEV_CODEX_EFFORT=xhigh
SUPER_DOLPHIN_DEV_CODEX_HOME=$HOME/.codex
SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY=default
SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER=openai
LOG_LEVEL=debug
ENABLE_MEMORY_SYSTEM=1
ENABLE_MEMORY_TOOLS=1
MULTI_AGENT_MEMORY_FEATURE_TEAMMEM=1
CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1
GO_AGENT_CTL_SESSION_TOKEN=dev-goland-local
```

把 `<repo>` 替换成当前仓库绝对路径。Windows 路径示例：`D:\project\Super-Dolphin-worktrees\feature-integration-20260618`。
Windows 下 `SUPER_DOLPHIN_HOME` 需要通过 owner-only ACL 校验；如果手动创建该目录，请使用当前用户、Administrators 和 SYSTEM 可写，关闭继承，并避免 Users/Everyone 等宽写入权限。

## 4. 启动顺序

1. 先启动 `frontend-app` 的 `npm run dev`。
2. 再运行 GoLand 的 `cmd/agent-terminal` 配置。
3. 如果改了 peer 相关 Go 代码，停止 GoLand 进程，重新构建 peer 二进制，再启动。

## 排查

- `5175` 被占用：关闭旧 Vite 进程后重启前端。
- `4512` 或 `8092` 被占用：关闭旧 `agent-terminal` / peer 进程。
- 页面空白：确认 `FRONTEND_DEVSERVER_URL` 和 `VITE_DEV_URL` 都是 `http://127.0.0.1:5175`。
- 工具桥启动失败：确认 `GO_AGENT_PEER_BIN_DIR` 目录里有 `mcp-orch` 和 `mcp-lsp`，Windows 下应为 `.exe`。
