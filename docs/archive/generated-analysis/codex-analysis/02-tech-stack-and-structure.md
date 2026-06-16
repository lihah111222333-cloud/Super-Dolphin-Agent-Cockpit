# 技术栈与目录结构

## 1. 本阶段目标

识别前端、后端、数据层、工程化、部署与目录职责。

## 2. 已读取文件

- `README.md`
- `go.mod`
- `Makefile`
- `frontend-app/package.json`
- `cmd/agent-terminal/frontend/package.json`
- `sqlc.yaml`
- `.github/workflows/ci.yml`
- `.env.example`
- `docs/doc/codemap/README.md`

## 3. 关键发现

| 类别 | 技术/工具 | 证据文件 |
|---|---|---|
| 后端语言 | Go `1.25.7` | `go.mod` |
| 依赖注入/生命周期 | Uber Fx、oklog/run | `go.mod`、`internal/app/modules.go` |
| 桌面宿主 | Wails v3 | `go.mod`、`cmd/agent-terminal/main.go` |
| RPC | jrpc2、WebSocket route `/ws` | `go.mod`、`internal/platform/rpc/module.go` |
| 数据库 | PostgreSQL、pgx、sqlc | `go.mod`、`sqlc.yaml` |
| 新前端 | React 19、Vite、Zustand、TanStack Query | `frontend-app/package.json` |
| legacy 前端 | Vite/Vitest/Playwright、Vue compiler、Mermaid | `cmd/agent-terminal/frontend/package.json` |
| 可观测性 | JSONL trace、Prometheus metrics、ELK 本地 compose | `internal/platform/observability/*`、`internal/platform/metrics/*`、`docker-compose.elk.yml` |
| CI | GitHub Actions、golangci-lint、frontend lint/test/build | `.github/workflows/ci.yml` |

## 4. 证据说明

- `cmd/agent-terminal` 是 Wails 桌面宿主和 HTTP/RPC bridge。
- `cmd/mcp-orch` 是 agent lifecycle、DAG、cron、toolbridge 的 MCP peer。
- `cmd/mcp-lsp` 是多语言 LSP peer。
- `internal/app` 是 Fx 根组装层。
- `internal/module` 承载业务模块。
- `internal/platform` 承载 db/rpc/config/runtime/toolbridge 等基础设施。
- `internal/provider` 承载 Claude/Codex provider 适配。
- `internal/store` 和 `sql/queries`/`migrations` 承载持久化。
- `frontend-app` 是当前 React/Vite UI；`cmd/agent-terminal/frontend` 是 legacy/package-embed 前端。

## 5. 风险与问题

- P1：当前 UI 与 legacy UI 并存，路径误判会导致改错包。
- P1：`frontend-app/src/styles.css`、`ChatPage.jsx`、`useClientStore.js` 过大，修改成本和回归风险高。
- P2：`Makefile` 的默认 build 会触发前端依赖/构建；文档或只读分析不应误跑。

## 6. 无法判断的信息

- 无法判断是否有单独的生产 IaC 仓库。
- 无法判断依赖漏洞状态；本次未执行依赖审计命令。

## 7. 下一阶段建议

进入系统上下文分析，画出 UI、RPC、module、provider、MCP peer、DB 的协作关系。
