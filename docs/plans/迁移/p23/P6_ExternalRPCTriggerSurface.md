# P23.6: 外部 RPC 触发面 + AuthN/AuthZ/rate/quota/audit

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P3**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

把现有 TCP JSON-RPC `task/*` 方法挂上调用身份提取层、tenant 鉴权、rate limit、配额、审计日志。**不**在本期升级到 REST / gRPC，传输层维持现状。

## 现状校准（事实层）

- 现有 TCP JSON-RPC：`internal/platform/config/config.go:45-47`（默认 `127.0.0.1:8090`）、`internal/platform/rpc/server.go:291-299`
- DAG 已暴露的 RPC method：`cmd/mcp-orch/orchestration/rpc.go:127-137`（`task/dag/create|get|list`、`task/node/update`）
- 当前无 auth：`internal/mcpserver/common/server.go:221-264`、`internal/platform/rpc/transport_ws.go:20-40`
- Wails WS：`internal/ui/wails/http_server.go:15,39-46`（`127.0.0.1:4511 /wails/ws`）
- MCP HTTP：`internal/mcpserver/common/http_transport.go:38-53,73-79`（`/mcp`，10MB body limit）
- config 结构无 auth/rate/quota：`internal/platform/config/config.go:33-41`

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- 新增 `WithCallerIdentity` middleware：从 RPC header / Wails session / MCP context 提取 caller identity，注入 ctx
- AuthN：API key / mTLS / JWT 三选一（v1 推荐 API key + 显式 opt-in）
- AuthZ：基于 caller identity + DAG `owner_id / tenant_id` 做操作授权
- Rate limit：每 caller / 每 tenant 双层限流
- Quota：DAG 数 / node 数 / launch 数 三类配额
- 审计日志：所有 `task/*` RPC 调用落 `dag_audit_log` 表（caller / method / params hash / result / latency）
- archtest：`dag_external_rpc_authn` 守 `cmd/mcp-orch/orchestration/rpc.go` 必须经 middleware

## DDL / SQL

**新增表**：
- `dag_audit_log`（caller_id, tenant_id, method, params_hash, result_code, latency_ms, called_at）
- 也可复用现有 `0065_dag_owner_tenant.sql` 同 PR 一并加

## 依赖

- P3 已合入（owner_id / tenant_id 字段已就位）
- config 结构需新增 `auth / rate / quota / audit` 字段（`internal/platform/config/config.go:33-41`）

## 风险

- 默认绑定不变（`127.0.0.1:8090`）：未明确 opt-in `0.0.0.0` 之前**不算对外服务**
- 任何把端口公网化的 PR 视为越界
- AuthN 失败必须 `jrpc2.InvalidParams`，不能 silent ignore
- audit log 写盘失败时降级（不阻断 RPC，但要 alert）

## 必测项

- AuthN 缺 caller identity → `ErrCallerIdentityRequired`
- AuthZ：caller `owner_id != dag.owner_id` 拒绝
- rate limit 触发后返 429
- quota 超额阻断创建
- audit log 落表完整字段

## 输入材料

- README §"实施路线图" P6 行
- README §"非目标"（不在本期升级 REST/gRPC）
- `dag-entry-audit` 报告（外部暴露缺口列表）


