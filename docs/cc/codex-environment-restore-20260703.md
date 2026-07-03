# Codex 开发环境迁移恢复指南

生成时间：2026-07-03
源机器工作区：`/home/l4p/Super-Dolphin`
目标：把 Super-Dolphin 的主力开发环境迁移到另一台机器，并让新机器上的 Codex 能按清单恢复。

## 1. 当前环境快照

### 1.1 仓库状态

- 当前分支：`main`
- 当前 HEAD：`57fc561ade1913b5ea9c6867480c4601cddcabe4`
- 远端：`https://github.com/lihah111222333-cloud/Super-Dolphin.git`
- 当前未提交变更：`.codex/config.toml`
- `.codex/config.toml` 的本地差异：只新增了 `GOCACHE = "/tmp/super-dolphin-go-build"`。
- `.codex/mcp/playwright/` 是被忽略的本地依赖目录，不会随 Git clone 恢复。

### 1.2 工具版本

| 工具 | 当前版本或路径 |
| --- | --- |
| Go | `go1.25.7 linux/amd64` |
| Go path | `/usr/bin/go` |
| GOROOT | `/home/l4p/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.7.linux-amd64` |
| GOPATH | `/home/l4p/go` |
| Node | `v24.14.0` |
| Node path | `/home/l4p/.nvm/versions/node/v24.14.0/bin/node` |
| npm | `11.9.0` |
| Codex CLI | `@openai/codex@0.142.5`, `codex-cli 0.142.5` |
| Codex path | `/home/l4p/.nvm/versions/node/v24.14.0/bin/codex` |
| Claude Code | `@anthropic-ai/claude-code@2.1.153`, `2.1.153 (Claude Code)` |
| Claude path | `/home/l4p/.nvm/versions/node/v24.14.0/bin/claude` |
| ripgrep | `15.1.0` |
| GNU Make | `4.3` |
| gopls | `v0.22.0` |
| ast-grep | `0.44.0` |

当前全局 npm 包里与本项目直接相关的是：

```text
@openai/codex@0.142.5
@anthropic-ai/claude-code@2.1.153
@ast-grep/cli@0.44.0
typescript-language-server@5.3.0
```

### 1.3 仓库依赖

- Go 模块声明：`go 1.25.7`
- 前端：`frontend-app/package.json`
- 前端包管理：`npm ci`，因为 `frontend-app/package-lock.json` 已纳入 Git。
- 关键前端版本：React `^19.0.0`、Vite `^8.0.12`、Vitest `^4.1.8`、Playwright `^1.60.0`。
- 本地 Playwright MCP：源机器 `.codex/mcp/playwright/package.json` 依赖 `@playwright/mcp@^0.0.75`，但 `/.codex/*` 默认被 Git 忽略，新机器需要重建目录和 package 文件。

## 2. 必须恢复

### 2.1 源码与 Git 状态

新机器先 clone 仓库并切到同一提交或最新 `main`：

```bash
git clone https://github.com/lihah111222333-cloud/Super-Dolphin.git
cd Super-Dolphin
git checkout main
git pull --ff-only origin main
```

如果要完全延续当前源机器状态，需要额外处理当前未提交差异：

```bash
git diff -- .codex/config.toml
```

当前只有 `GOCACHE = "/tmp/super-dolphin-go-build"` 这一行未提交。新机器可以选择手动加回，或者先保持 Git 版本不动。

### 2.2 基础工具链

推荐在新机器安装或对齐：

```bash
# Node 版本建议至少满足 README 的 Node 20+，要完全对齐则用 24.14.0。
npm install -g @openai/codex@0.142.5
npm install -g @anthropic-ai/claude-code@2.1.153
npm install -g @ast-grep/cli@0.44.0

# Go 需要 1.25.7。
go version
node --version
npm --version
codex --version
claude --version
```

Codex 必须在新机器重新登录：

```bash
codex login
```

Claude 只有在 legacy/provider-integration 工作需要时才必须恢复：

```bash
claude login
```

不要把 `~/.codex/auth.json` 或 `~/.claude` 里的认证材料当作普通项目文件提交或粘贴给 Codex。跨机器迁移认证状态时，只在可信、加密的离线备份路径中处理；默认方案是重新登录。

### 2.3 项目初始化

```bash
make install-hooks

cd frontend-app
npm ci
cd ..

make build-peer-binaries

mkdir -p .codex/mcp/playwright
cat > .codex/mcp/playwright/package.json <<'JSON'
{
  "dependencies": {
    "@playwright/mcp": "^0.0.75"
  }
}
JSON
(cd .codex/mcp/playwright && npm install)
```

说明：

- `make build-peer-binaries` 会生成 `bin/mcp-orch` 和 `bin/mcp-lsp`。当前源机器这两个文件存在，但 `bin/` 不纳入 Git。
- `.codex/mcp/playwright/package.json` 和 `node_modules` 都是本地状态；当前 node_modules 约 18M，不应依赖 Git 恢复。
- `frontend-app/node_modules` 也不应迁移，使用 `npm ci` 重建。

## 3. 必须按新机器重写的路径

项目内 `.codex/config.toml` 当前包含源机器绝对路径：

```toml
[mcp_servers.playwright]
command = "/home/l4p/.nvm/versions/node/v24.14.0/bin/node"
args = [
    "/home/l4p/Super-Dolphin/.codex/mcp/playwright/node_modules/@playwright/mcp/cli.js",
]

[mcp_servers.lsp]
command = "/home/l4p/Super-Dolphin/bin/mcp-lsp"
cwd = "/home/l4p/Super-Dolphin"

[mcp_servers.lsp.env]
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = "/home/l4p/Super-Dolphin"
GO_AGENT_LSP_ROOT = "/home/l4p/Super-Dolphin"
GO_AGENT_LSP_ROOTS = '["/home/l4p/Super-Dolphin"]'
GOCACHE = "/tmp/super-dolphin-go-build"
```

新机器恢复后必须把这些路径改为新机器真实路径。例如：

```toml
[mcp_servers.playwright]
command = "/absolute/path/to/node"
args = [
    "/absolute/path/to/Super-Dolphin/.codex/mcp/playwright/node_modules/@playwright/mcp/cli.js",
]

[mcp_servers.lsp]
command = "/absolute/path/to/Super-Dolphin/bin/mcp-lsp"
cwd = "/absolute/path/to/Super-Dolphin"

[mcp_servers.lsp.env]
SUPER_DOLPHIN_RUNTIME_MODE = "dev"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = "/absolute/path/to/Super-Dolphin"
GO_AGENT_LSP_ROOT = "/absolute/path/to/Super-Dolphin"
GO_AGENT_LSP_ROOTS = '["/absolute/path/to/Super-Dolphin"]'
GOCACHE = "/tmp/super-dolphin-go-build"
```

可用下面命令取新路径：

```bash
pwd
command -v node
```

## 4. 建议恢复

### 4.1 Codex 个人配置

源机器 `~/.codex` 中值得迁移或重建的文件类型：

- `~/.codex/config.toml`：模型、插件、项目配置。当前模型为 `gpt-5.5`，reasoning effort 为 `xhigh`。
- `~/.codex/hooks.json`：当前指向 `/mnt/c/Users/19065/.codeisland/codeisland-bridge.exe` 和 Windows 路径。只有新机器也有 CodeIsland bridge 时才启用。
- `~/.codex/rules/default.rules`：已有允许规则包括 `cp`、`./scripts/test_with_guard.sh`、`git commit`。
- `~/.codex/prompts/`：当前有 `opsx-explore.md`、`opsx-apply.md`、`opsx-propose.md`、`opsx-archive.md`。
- `~/.codex/memories/`：如果需要延续 Codex 记忆，可迁移；如果希望干净重建，可不迁移。

不建议直接迁移：

- `~/.codex/auth.json`：认证状态，默认重新 `codex login`。
- `~/.codex/logs_*.sqlite`、`state_*.sqlite`、`history.jsonl`、`sessions/`：只在确实需要历史会话或诊断时再迁移。
- `~/.codex/models_cache.json`、`version.json`：缓存文件，可以由 Codex 重新生成。
- `~/.codex/cache/`、`plugins/cache/`：插件缓存，优先让新 Codex 重新安装或拉取。

### 4.2 Super-Dolphin 运行数据

当前源机器上发现的运行数据：

- 默认新 UI dev home：`/tmp/sd-new-ui-l4p/super-dolphin-home`
- 默认新 UI dev SQLite：`/tmp/sd-new-ui-l4p/super-dolphin-home/super-dolphin.db`
- repo 内本地 SQLite：`/home/l4p/Super-Dolphin/.super-dolphin/super-dolphin.db`
- `~/.super-dolphin` 当前不存在。

`run-new-ui-desktop.sh` 的默认值是：

```bash
SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/super-dolphin-home}"
SUPER_DOLPHIN_SQLITE_PATH="${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}"
SUPER_DOLPHIN_DEV_PROVIDER="${SUPER_DOLPHIN_DEV_PROVIDER:-codex}"
SUPER_DOLPHIN_DEV_CODEX_MODEL="${SUPER_DOLPHIN_DEV_CODEX_MODEL:-gpt-5.5}"
SUPER_DOLPHIN_DEV_CODEX_EFFORT="${SUPER_DOLPHIN_DEV_CODEX_EFFORT:-xhigh}"
SUPER_DOLPHIN_DEV_CODEX_HOME="${SUPER_DOLPHIN_DEV_CODEX_HOME:-$HOME/.codex}"
```

如果不需要旧会话、旧线程、旧本地设置，新机器可以不迁移 SQLite，让应用自动建库。
如果需要保留旧应用数据，停止旧机器上的 Super-Dolphin 进程后，再迁移 `.db`、`.db-wal`、`.db-shm` 三类文件，避免 WAL 丢失。

### 4.3 技能与 provider mirror

项目技能的规范入口是：

```text
<repo>/.agents/skills/*/SKILL.md
```

这些文件已纳入 Git，clone 仓库即可恢复。

运行时 provider mirror 规则需要注意：

- Codex 的 personal mirror 是 `~/.agents/skills`，不是 `~/.codex`。
- Codex 的 project mirror 是 `<repo>/.agents/skills`。
- Claude 的 personal mirror 是 `~/.claude/skills`。
- `personal/hub` 只是目录索引，不要当作普通个人技能扫描或迁移。

源机器 `~/.agents/skills` 当前只有 `.super-dolphin-skill-mirror.json`，属于生成镜像状态，不是必须迁移的 canonical source。

## 5. 新机器恢复顺序

1. 安装 Git、Go 1.25.7、Node 20+ 或 Node 24.14.0、npm、ripgrep、make。
2. 安装 Codex CLI：`npm install -g @openai/codex@0.142.5`。
3. 运行 `codex login`，确认 `codex --version` 成功。
4. clone `https://github.com/lihah111222333-cloud/Super-Dolphin.git`。
5. 在仓库根目录运行 `make install-hooks`。
6. 运行 `cd frontend-app && npm ci && cd ..`。
7. 运行 `make build-peer-binaries`。
8. 重建 Playwright MCP：创建 `.codex/mcp/playwright/package.json`，写入 `@playwright/mcp@^0.0.75`，再运行 `npm install`。
9. 修改项目 `.codex/config.toml` 中所有源机器绝对路径。
10. 如果要恢复 Codex 个人偏好，迁移或手工重建 `~/.codex/config.toml`、`hooks.json`、`rules/`、`prompts/`、`memories/`，但不要提交认证文件。
11. 如果要恢复 Super-Dolphin 本地应用数据，迁移 SQLite 三件套；否则让应用新建。
12. 运行验证命令。

## 6. 验证命令

基础工具验证：

```bash
go version
node --version
npm --version
codex --version
git status --short
```

依赖和构建验证：

```bash
make build-peer-binaries
make frontend-app-build
```

前端完整验证：

```bash
cd frontend-app
npm run lint
npm test
npm run build
cd ..
```

后端基础验证：

```bash
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/provider/codexapp -count=1
```

启动验证：

```bash
./run-new-ui-desktop.sh
```

启动后检查输出中的：

- `peer bin dir`
- `runtime`
- `home`
- `sqlite`
- `frontend-app`
- `bridge`
- `control rpc`

这些路径必须指向新机器，不应再出现 `/home/l4p/Super-Dolphin` 或 `/home/l4p/.nvm/...`，除非新机器路径恰好相同。

## 7. 交给新机器 Codex 的恢复提示词

在新机器打开 Codex 后，可以直接给它这段任务：

```text
你正在恢复 Super-Dolphin 开发环境。请读取 docs/cc/codex-environment-restore-20260703.md，并按文档执行。

要求：
1. 先只读检查当前机器状态：pwd、git status --short、go/node/npm/codex/claude 版本、command -v node、command -v codex。
2. 不要读取、打印或提交 ~/.codex/auth.json、~/.claude 认证文件或任何 API key。
3. 如果仓库 .codex/config.toml 里还有旧机器绝对路径，请改成当前机器路径。
4. 运行 make install-hooks、frontend-app/npm ci、make build-peer-binaries；如果 .codex/mcp/playwright 不存在，先按文档创建 package.json，再 npm install。
5. 按文档运行验证命令。若需要联网安装依赖，先明确说明需要网络。
6. 若测试失败，先报告失败命令和首个根因，不要静默降级。
7. 最后总结：已恢复项、未恢复项、仍需用户手动登录或授权的项。
```

## 8. 当前已知风险

- 源机器项目 `.codex/config.toml` 使用绝对路径，新机器必须改。
- 源机器个人 `~/.codex/hooks.json` 绑定 CodeIsland bridge 路径，新机器没有同一路径时会失败或拖慢工具调用。
- `codex --version` 在当前沙箱里曾提示无法创建 PATH aliases：`Read-only file system`。新机器应在正常用户环境中运行一次 `codex login` 或 Codex 初始化，确认它能写自己的 home。
- LSP 工具在本次盘点中 `document_symbol`、`inspect`、`xref`、`diagnostics` 曾因语言服务器索引超时返回 `lsp_timeout`。新机器恢复后应先确认 `bin/mcp-lsp` 已构建，并在 Codex MCP 中重新启动 LSP server。
- 如果迁移 SQLite，必须一起迁移 `*.db`、`*.db-wal`、`*.db-shm`，并保证旧进程已停止。
