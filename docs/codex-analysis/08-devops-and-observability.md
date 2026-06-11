# 运维部署与可观测性分析

## 1. 本阶段目标

分析本地启动、构建、部署、环境变量、日志、健康检查、监控、告警和回滚能力。

## 2. 已读取文件

- `README.md`
- `Makefile`
- `run-new-ui-desktop.sh`
- `run-new-ui-desktop.ps1`
- `run-debug.ps1`
- `.env.example`
- `.github/workflows/ci.yml`
- `docker-compose.elk.yml`
- `deploy/elk/README.md`
- `internal/platform/observability/*`
- `internal/platform/metrics/*`
- `pkg/logger/logger.go`

## 3. 关键发现

- 本地新 UI 主入口是 `./run-new-ui-desktop.sh`，会启动 `frontend-app` Vite，并运行 `cmd/agent-terminal`。
- Windows 调试入口是 `run-debug.ps1`，包含 codemap 刷新、npm 安装/构建、预检、热更新和退出清理等流程。
- 构建入口包括 `make build-plain`、`make build-agent-terminal`、`make build-peer-binaries`、平台 package 脚本。
- 环境变量集中在 `.env.example` 和 `internal/platform/config/config.go`；包括 DB、RPC、LOG_LEVEL、LSP、ELK、notify 等。
- 可观测性包括结构化 logger、JSONL trace、frontend trace ingest、Prometheus metrics、ELK 本地 compose。
- CI 不部署，只 build/test/lint；仓库内未发现生产发布流水线。

## 4. 证据说明

| 结论 | 文件 |
|---|---|
| 新 UI 桌面启动依赖 Vite + Go backend | `run-new-ui-desktop.sh` |
| Windows 调试脚本负责多项预检和清理 | `run-debug.ps1` |
| 可观测性 service 写 JSONL trace 并支持 tail reader | `internal/platform/observability/module.go` |
| 日志采用 slog，并映射 ECS 字段 | `pkg/logger/logger.go` |
| 本地 ELK compose 关闭安全，仅限本地开发 | `docker-compose.elk.yml` |
| CI 只覆盖检查，不含部署 | `.github/workflows/ci.yml` |

## 5. 风险与问题

- P1：生产部署、备份、告警、回滚流程在仓库内证据不足。
- P1：本地 ELK compose 明确关闭安全，不能直接用于生产。
- P2：启动脚本会自动安装/构建依赖，文档扫描和只读分析需要避免误运行。

## 6. 无法判断的信息

- 无法判断生产监控指标、告警规则、SLO、灰度发布和回滚策略。
- 无法判断发布包签名和自动更新完整链路是否在外部系统补齐。

## 7. 下一阶段建议

进入安全分析，重点覆盖 secrets、输入校验、RPC 权限、日志脱敏、数据库只读查询、CORS/CSRF/SSRF 等。
