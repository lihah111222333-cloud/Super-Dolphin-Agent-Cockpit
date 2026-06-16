# 性能与扩展性分析

## 1. 本阶段目标

分析前端加载、API 响应、数据库查询、缓存、批量任务、并发、外部 provider 调用和水平扩展风险。

## 2. 已读取文件

- `frontend-app/src/App.jsx`
- `frontend-app/src/styles.css`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/entities/client/model/useClientStore.js`
- `frontend-app/src/shared/api/wailsBridge.js`
- `internal/platform/db/module.go`
- `sql/queries/*.sql`
- `migrations/*`
- `internal/sidecar/orch/tools/orchestration_tools.go`
- `internal/sidecar/orch/tools/task_tools.go`
- `internal/provider/codexapp/*`
- `internal/provider/claudecli/*`
- `internal/platform/metrics/*`

## 3. 关键发现

- 前端最大风险来自超大 CSS、App、Chat 和 store 文件；这会影响构建、测试、认知负担和局部回归范围。
- React Query 默认 dashboard stale time 为 30 秒，gc time 为 10 分钟；部分页面自行控制刷新和缓存。
- `wailsBridge.js` 有 RPC 慢阈值、pending trace、trace batch/queue 限制，能识别前端/RPC慢调用。
- DB pool `MaxConns=100`；多处查询包含 limit 和索引，`dbquery` 会自动补 limit。
- mcp-orch agent launch 是异步，避免 MCP tool call 被 app-server timeout 卡死。
- provider 层有 Codex WS recovery、Claude CLI restart/interrupt/forceComplete 等机制。

## 4. 证据说明

| 结论 | 文件 |
|---|---|
| 前端超大文件存在 | 文件行数统计：`frontend-app/src/styles.css`、`App.test.jsx`、`ChatPage.jsx`、`useClientStore.js` |
| 前端 trace 队列和慢阈值 | `frontend-app/src/shared/api/wailsBridge.js` |
| DB pool 固定 MaxConns=100 | `internal/platform/db/module.go` |
| 查询包含 limit 和索引 | `sql/queries/*.sql`、`migrations/*.sql` |
| mcp-orch launch agent 异步返回 | `internal/sidecar/orch/tools/orchestration_tools.go` |
| metrics 包含 skill/DAG/http 指标 | `internal/platform/metrics/*` |

## 5. 风险与问题

- P1：前端大文件会降低构建和测试定位效率，复杂页面性能风险需要浏览器实测。
- P1：单机桌面 + 本地 Postgres + provider 进程模型不等同于水平扩展架构。
- P1：外部 provider 的速率限制、超时、重试成本不在仓库配置中完全可见。
- P2：DB 查询有索引和 limit，但缺少本次实测慢查询证据。

## 6. 无法判断的信息

- 无法判断真实数据量下的慢查询、N+1、锁等待、Postgres 连接池耗尽情况。
- 无法判断前端首屏 bundle、渲染耗时和内存占用；本次未运行性能工具。

## 7. 下一阶段建议

继续技术债与可维护性分析，围绕大文件、模块耦合、测试缺口、文档缺口和重构优先级整理。
