# testsync

Migration from go-agent-v2, started 2026-03-19.

## Clone 后必做

```bash
make install-hooks
```

这会把本仓库的 `core.hooksPath` 指到 `.githooks`，让本地 `git commit` / `git push` 自动跑 pre-commit 与 pre-push 检查。紧急绕过只能用 `--no-verify`，且违反常态规约，必须事后补检查。

## 代码地图

完整代码地图见 [`docs/doc/codemap/README.md`](docs/doc/codemap/README.md)。常用入口：

- [终端入口与 UI 层](docs/doc/codemap/01-terminal-ui.md)
- [MCP Orchestration](docs/doc/codemap/02-mcp-orch.md)
- [App 核心与契约层](docs/doc/codemap/04-app-contract.md)
- [业务模块层](docs/doc/codemap/07-module.md)
- [Platform 基础设施层](docs/doc/codemap/08-platform.md)
- [Provider 集成层](docs/doc/codemap/09-provider.md)
