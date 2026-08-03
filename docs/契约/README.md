# super-agent-v3 契约文档

本目录记录当前仍生效的工程契约。回答实现行为时，事实来源顺序是源码与测试、LSP 证据、状态为 Accepted 的 `docs/adr`、本目录契约、`docs/架构` 骨架，再用 `docs/doc/codemap` 缩小阅读范围。

`docs/plans/**`、`docs/迁移/**`、`docs/superpowers/plans/**` 和旧报告只作为历史规划材料；除非追溯来源，不应把它们当成当前规范入口。

## 阅读顺序

| 主题 | 文档 | 用途 |
| --- | --- | --- |
| 修复闭环 | `fix-workflow-convention.md` | 统一复现、RED/GREEN、回归、验证和报告口径 |
| 提交边界 | `atomic-commit-convention.md` | 拆分提交、脏工作树和验证证据规则 |
| Fail-Fast | `fail-fast-convention.md` | 禁止默认兜底、吞错和静默降级 |
| 模块边界 | `modularity-convention.md`、`onion-architecture-convention.md` | 依赖方向、MCP binary、核心层和洋葱分层 |
| Fx / runner | `fx-convention.md`、`rungroup-convention.md` | 依赖注入、生命周期、长跑 actor 编排 |
| RPC / MCP | `jrpc2-convention.md`、`mcp-service-convention.md` | JSON-RPC、stdio MCP、控制面注册和 manifest |
| Store / SQL | `sqlc-convention.md` | SQLite migration、sqlc 生成物和 store 边界 |
| 状态 / 事件 | `statemachine-event-convention.md`、`workflow-runtime-state-contract.md`、`recall-topic-naming.md` | 状态机、事件 payload 和运行态命名 |
| 远程 CI | `remote-ci-eci-imagecache-contract.md` | ECI ImageCache 唯一路径、两小时 SQLite 抢占、动态分片和精确耗时账本 |

## 维护规则

1. 改实现契约时，同步更新本目录和相邻 `docs/架构/*.md`。
2. 涉及生成地图时，优先修生成器或运行统一刷新入口，不手改 `docs/doc/codemap` 生成物。
3. 文档本身改动至少跑 `git diff --check`；架构守卫、codemap 或生成物相关改动追加对应 `make guard`、`make codemap-check`、`make project-map-check` 或 `make capcontract-check`。
