# Super-Dolphin 项目交接 SOP 文档包

生成日期：2026-06-10

本文档包用于把 `Super-Dolphin` 项目的交接、启动、架构、数据、接口、质量门禁、运维和长期知识库维护流程固化为可执行 SOP。

## 使用范围

- 适用于新成员接手项目、跨团队交接、Release 前评审、线上或本地故障复盘。
- 以当前仓库代码、脚本、迁移、CI 配置和本地环境读取结果为主要依据。
- 不替代实时运行验证。启动、核心流程、CI/CD 和部署项仍需按清单在目标机器或目标环境执行。

## 文档索引

| Step | 主题 | 文档 |
| --- | --- | --- |
| Step 1 | 明确交接目标 | `01-handoff-goal-and-intake.md` |
| Step 2 | 收集项目文档、代码、环境、权限 | `01-handoff-goal-and-intake.md` |
| Step 3 | 梳理业务目标、用户角色、核心流程 | `02-business-requirements-agile.md` |
| Step 4 | 梳理需求、用户故事、验收标准 | `02-business-requirements-agile.md` |
| Step 5 | 绘制系统上下文图 | `03-architecture-diagrams.md` |
| Step 6 | 绘制容器图和组件图 | `03-architecture-diagrams.md` |
| Step 7 | 梳理数据库 ER 图和数据字典 | `04-data-model.md` |
| Step 8 | 梳理核心接口和关键时序图 | `05-interfaces-and-sequences.md` |
| Step 9 | 阅读代码仓库结构和核心模块 | `06-codebase-startup-quality.md` |
| Step 10 | 本地启动项目并跑通核心流程 | `06-codebase-startup-quality.md` |
| Step 11 | 检查测试体系和质量门禁 | `06-codebase-startup-quality.md` |
| Step 12 | 梳理 CI/CD、部署、回滚流程 | `07-ci-cd-ops-incident.md` |
| Step 13 | 梳理监控、日志、告警、故障处理 | `07-ci-cd-ops-incident.md` |
| Step 14 | 整理 Agile Backlog、Sprint、Release 流程 | `02-business-requirements-agile.md` |
| Step 15 | 输出交接文档包 | `08-delivery-review-knowledge-base.md` |
| Step 16 | 组织交接评审会议 | `08-delivery-review-knowledge-base.md` |
| Step 17 | 根据评审意见修订文档 | `08-delivery-review-knowledge-base.md` |
| Step 18 | 沉淀为长期维护的项目知识库 | `08-delivery-review-knowledge-base.md` |

## 本次读取的主要证据

- 项目入口和模块说明：`README.md`、`go.mod`、`Makefile`、`docs/doc/codemap/README.md`
- 启动脚本：`run-new-ui-desktop.sh`、`run-new-ui-desktop.ps1`
- 桌面和后端装配：`cmd/agent-terminal/main.go`、`internal/app/app.go`、`internal/app/modules.go`
- 配置和数据库：`internal/platform/config/config.go`、`internal/platform/db/module.go`、`internal/store/module.go`
- HTTP/RPC 桥：`internal/ui/wails/http_server.go`、`internal/platform/rpc/server.go`
- MCP 和工具面：`cmd/mcp-orch/**`、`cmd/mcp-lsp/**`、`internal/mcpserver/common/http_transport.go`
- 当前 React UI：`frontend-app/package.json`、`frontend-app/src/App.jsx`、`frontend-app/src/shared/api/backendApi.js`
- 数据模型：`sqlc.yaml`、`internal/platform/db/sqlite/migrations/001_baseline.sql` 及后续 SQLite 迁移
- CI/CD 和发布：`docs/契约/remote-ci-eci-imagecache-contract.md`、`.github/workflows/release.yml`、`scripts/package_windows.ps1`、`scripts/package_macos.sh`、`scripts/package_linux.sh`、`scripts/publish_github_release.sh`
- 本地观测栈：`deploy/elk/README.md`、`deploy/elk/docker-compose.yml`、`scripts/elk-local.ps1`

## 本次未执行的动作

- 未启动本地桌面应用和 Vite 开发服务。
- 未跑完整 Go、前端或 CI 测试套件。
- 未执行打包、发布、迁移或回滚命令。
- 未读取或验证外部权限状态，例如 GitHub Release 权限、Claude CLI 登录态、Codex CLI 登录态、签名证书和生产数据库权限。

这些动作在文档中均以执行清单形式沉淀，交接评审或真实接手时需要逐项复核。
