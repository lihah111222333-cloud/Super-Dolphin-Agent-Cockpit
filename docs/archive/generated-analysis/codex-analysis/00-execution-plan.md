# 阶段 00：执行计划与仓库初识

## 1. 本阶段目标

建立 Super-Dolphin 仓库分析上下文，判断项目类型、技术栈、优先阅读文件与后续阶段适用性。

## 2. 已读取文件

- `README.md`
- `AGENTS.md`
- `CLAUDE.md`
- `docs/doc/codemap/README.md`
- `go.mod`
- `Makefile`
- `frontend-app/package.json`
- `cmd/agent-terminal/frontend/package.json`
- `.env.example`
- `.github/workflows/ci.yml`
- `docker-compose.elk.yml`

## 3. 关键发现

- 项目类型：AI 辅助开发的多代理编排桌面/本地平台，包含桌面宿主、MCP sidecar、React 新 UI、legacy Vue 前端、PostgreSQL 持久化、LSP 工具链和本地可观测性。
- 主要语言与运行时：Go `1.25.7`、Node.js 20+、React 19、Vite、PostgreSQL、Wails v3、jrpc2、pgx、sqlc、Uber Fx。
- 仓库包含前端、后端、数据库迁移、测试、部署/观测配置和脚本；未发现 Kubernetes/Helm 目录作为主部署路径。
- 当前扫描输出目录为 `docs/codex-analysis/`；本次只生成文档，不修改业务代码、配置、依赖、迁移或 CI/CD。

## 4. 证据说明

| 结论 | 文件 |
|---|---|
| 项目是多代理编排平台 | `README.md` |
| 桌面宿主、mcp-orch、mcp-lsp、React 新 UI 是核心入口 | `README.md`、`docs/doc/codemap/README.md` |
| Go/Wails/jrpc2/pgx/fx/stateless/Prometheus 是后端关键依赖 | `go.mod` |
| React/Vite/Zustand/TanStack Query 是新 UI 技术栈 | `frontend-app/package.json` |
| legacy Vue 前端仍保留独立包和 size guard | `cmd/agent-terminal/frontend/package.json` |
| CI 覆盖 commit guard、Go vet/build/test、golangci-lint、frontend lint/test/build | `.github/workflows/ci.yml` |

## 5. 风险与问题

- P1：前端存在超大文件，`frontend-app/src/styles.css`、`App.test.jsx`、`ChatPage.jsx`、`useClientStore.js` 均超过 5000 行，维护和回归风险较高。
- P1：数据库迁移数量多，`internal/platform/db/module.go` 依赖最低 migration 版本硬门槛，部署或本地环境若未迁移到位会 fail-fast。
- P2：legacy Vue 前端仍存在，可能造成当前 UI 修改路径判断混淆。

## 6. 无法判断的信息

- 无法判断生产部署拓扑、生产数据库备份策略、生产 TLS/鉴权策略；仓库中本次只看到本地 ELK compose 和 CI。
- 无法判断当前线上版本与本地 `origin/main` 的差异。

## 7. 下一阶段建议

继续阶段 01-12 全量扫描，重点关注业务边界、前后端调用链、DB 数据流、安全/性能风险、测试覆盖和运维缺口。
