# P23.6: 外部 RPC 触发面 + AuthN/AuthZ/rate/quota/audit

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P3**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

把现有 TCP JSON-RPC / Wails WS / MCP HTTP 上的所有写 RPC 挂上调用身份提取层、tenant 鉴权、rate limit、配额、审计日志。覆盖范围是同 transport 上全部写面：`agent.launch`、`agent.submit*`、`agent.stop`、`reportRuntime` / report event、`task/dag/create`、`dag/start`、`task/node/update` 等；**不能只保护 `task/*`**。**不**在本期升级到 REST / gRPC，传输层维持现状。localhost 也视为 untrusted local attack surface。

## 现状校准（事实层）

- 现有 TCP JSON-RPC：`internal/platform/config/config.go:45-47`（默认 `127.0.0.1:8090`）、`internal/platform/rpc/server.go:291-299`
- DAG 已暴露的 RPC method：`internal/sidecar/orch/orchestration/rpc.go:127-137`（`task/dag/create|get|list`、`task/node/update`）
- 当前无 auth：`internal/mcpserver/common/server.go:221-264`、`internal/platform/rpc/transport_ws.go:20-40`
- Wails WS：`internal/ui/wails/http_server.go:15,39-46`（`127.0.0.1:4511 /wails/ws`）
- MCP HTTP：`internal/mcpserver/common/http_transport.go:38-53,73-79`（`/mcp`，10MB body limit）
- config 结构无 auth/rate/quota：`internal/platform/config/config.go:33-41`

## 推荐架构

三入口先做 transport identity adapter，再进入同一 service-level guard：AuthN → AuthZ/tenant filter → rate → quota → idempotency → audit/spool → handler。`authenticated_host_actor_id` / `cron_job_owner_identity` / `external_caller_id` 等 authenticated principal 是唯一身份来源；`agent_id`、display name、`created_by`、RPC params 中的 `tenant_id/owner_id` 都只能做一致性校验或显示，不能作为授权来源；真实 `caller_identity` 必须来自 authenticated context，缺失 hard fail + audit。

文件级落点：
- TCP JSON-RPC：`internal/platform/rpc/server.go` / `internal/platform/rpc/transport_tcp.go`（如存在）提取 API key/session/mTLS claims；`internal/sidecar/orch/orchestration/rpc.go` 只接收已注入 ctx。
- Wails WS：`internal/ui/wails/http_server.go`、`internal/ui/wails/bridge.go`、`internal/platform/rpc/transport_ws.go` 增 Origin allowlist、CSRF token、session binding、optional API key；桌面 localhost 同样视为 untrusted local attack surface，不因 `127.0.0.1` 跳过 AuthN。
- MCP HTTP：`internal/mcpserver/common/http_transport.go` / `internal/mcpserver/common/server.go` 提取 Authorization/API key/session，注入 `CallerIdentity`，并对 MCP tools 写操作套同一 guard。

service-level guard 放在共享包（建议 `internal/sidecar/orch/orchestration/security_guard.go` 或平台级 `internal/platform/rpc/security_guard.go`，由 owner 按 import 方向选择），每个写 RPC 注册时声明 method class、resource resolver、quota class、idempotency requirement、audit class。RPC 注册拆到 `registerDAGSecurityRPC`，`internal/sidecar/orch/orchestration/rpc.go` 只保留顶层 wiring，避免 P3/P6/P10/P11 同时撞热文件。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| TCP identity adapter | `internal/platform/rpc/server.go` + transport 文件 | 从 API key/JWT/mTLS/session 提取 `CallerIdentity`；localhost 不跳过；失败 audit |
| Wails WS identity adapter | `internal/ui/wails/http_server.go`、`internal/ui/wails/bridge.go`、`internal/platform/rpc/transport_ws.go` | Origin allowlist、CSRF/session binding、可选 API key；WS upgrade 前校验 |
| MCP HTTP identity adapter | `internal/mcpserver/common/http_transport.go`、`internal/mcpserver/common/server.go` | Authorization/API key/session → ctx；MCP tools 写操作同 guard |
| service guard registry | `internal/sidecar/orch/orchestration/security_guard.go` [NEW] 或 platform 等价落点 | method matrix：AuthN/AuthZ/rate/quota/idempotency/audit，覆盖 agent/report/task/dag 写 RPC |
| RPC registrar | `internal/sidecar/orch/orchestration/rpc_security.go` [NEW] | `registerDAGSecurityRPC` 注册/包裹安全 guard；`rpc.go` 仅调用 registrar |
| Start idempotency | `internal/sidecar/orch/store/dagstart/*.go`（复用 P3） | `dag_start_requests` for `dag/start`；external 使用 client idempotency key |
| audit/spool | `internal/sidecar/orch/store/dagaudit/*.go` [NEW] + SQL | 写 RPC fail-closed 或 durable spool；read-sensitive redacted audit |
| config | `internal/platform/config/config.go`（扩展） | auth/rate/quota/audit/origin allowlist/api key 配置；默认安全关闭外部写面 |
| archtest | `internal/archtest/dag_external_rpc_guard_test.go` [NEW] | 三入口 adapter + service guard matrix；禁止只守 `task/*` |

**已知关键改动方向**：
- 新增 `WithCallerIdentity` middleware：从 RPC header / Wails session / MCP context 提取 caller identity，注入 ctx
- AuthN：API key / mTLS / JWT 三选一（v1 推荐 API key + 显式 opt-in）
- AuthZ：基于 caller identity + DAG `owner_id / tenant_id` 做操作授权
- Rate limit：每 caller / 每 tenant 双层限流
- Quota：DAG 数 / node 数 / launch 数 三类配额
- 审计日志：所有写 RPC（`agent.launch`、`agent.submit*`、`agent.stop`、report runtime/event、`task/dag/create`、`dag/start`、`task/node/update` 等）落 `dag_audit_log` 表（caller / method / params hash / result / latency）；read-sensitive 也落 redacted audit
- archtest：`dag_external_rpc_guard` 守 TCP JSON-RPC / Wails WS / MCP HTTP 三入口 identity adapter + service 层 AuthN/AuthZ/tenant/rate/quota/audit/idempotency guard

## DDL / SQL

**新增表**：
- `dag_audit_log`（caller_id, tenant_id, method, params_hash, result_code, latency_ms, called_at, prev_hash, row_hash, hash_alg, chain_scope）
- `dag_audit_spool` / durable outbox（spool_id, status, method, params_hash, replay_after, attempt_no, last_error, created_at, processed_at）：外部写操作在 audit 主表不可写时必须 fail-closed，除非 spool insert 成功
- 也可复用现有 `0066_dag_owner_tenant.sql` 同 PR 一并加

## 依赖

- P3 已合入（owner_id / tenant_id 字段已就位）
- config 结构需新增 `auth / rate / quota / audit` 字段（`internal/platform/config/config.go:33-41`）

## 风险

- 默认绑定即使仍是 `127.0.0.1:8090` 也按 untrusted local attack surface 处理；loopback 只降低网络暴露半径，不降低 AuthN/AuthZ/CSRF/session/API key 要求
- 任何把端口公网化的 PR 视为越界
- AuthN 失败必须 `jrpc2.InvalidParams`，不能 silent ignore
- audit log 写盘失败按操作/环境分级：prod、外部 RPC 写操作、financial preset、verdict/schema validation 必须 fail-closed 或先写 durable spool；dev/只读 RPC 可降级但必须 alert + 可重放

## 必测项

- AuthN 缺 caller identity → `ErrCallerIdentityRequired`
- AuthZ：caller `owner_id != dag.owner_id` 拒绝
- rate limit 触发后返回统一 `ErrRateLimited{retry_after}`；HTTP transport 映射 429，JSON-RPC 映射固定 error code `ErrCodeRateLimited = -32029` + `data.retry_after`
- quota 超额阻断创建
- audit log 落表完整字段

## 输入材料

- README §"实施路线图" P6 行
- README §"非目标"（不在本期升级 REST/gRPC）
- `dag-entry-audit` 报告（外部暴露缺口列表）

## 外部 RPC endpoint matrix（需求补全仲裁）

| Method | 写入性 | AuthZ / tenant | Idempotency | Rate/quota | Audit | 特殊约束 |
|---|---|---|---|---|---|---|
| `agent.launch` | write | authenticated host actor 可在 tenant/scope 内 launch；DAG-bound launch 还需 owner/assigned policy | 必填，scope=`tenant_id+caller_id+agent_launch+key` | launch + provider concurrency + spend quota | fail-closed/spool | 不得以 params.agent_id 充当 caller；DAG launch 必须带 dag/node fence |
| `agent.submit*` | write | caller 必须是 owning host actor、assigned agent controller 或 tenant admin；DAG repair/resubmit 需 active_turn/attempt fence | 必填；同 prompt hash 重放返回同一 submission | turn + token/spend quota | fail-closed/spool | 同 agent 再投 turn 只走 `SubmitTurn` / `submitTurnViaLauncher`，不能绕 launcher queue |
| `agent.stop` | write | owner/tenant admin 或 owning host actor | 必填；stop reason 入 params_hash | stop quota + rate | fail-closed/spool | 不能跨 tenant stop；DAG-bound stop 写 reason/audit |
| `reportRuntime` / report event | write | runtime reporter identity 必须绑定 agent/session/tenant | event id / `(agent_id,seq)` 去重 | report rate + payload cap | fail-closed/spool for terminal/security events | report params 只能一致性校验；terminal/report security event 不可 drop |
| `task/dag/create` / `dag/create` | write | tenant creator / owner | 必填，scope=`tenant_id+caller_id+method+key` | DAG + node quota | fail-closed/spool | `tenant_id` 必须来自 authenticated caller authorized tenant；client params 只能一致性校验；compat method 必须映射到同一 guard matrix |
| `dag/start` | write | owner / tenant admin | 必填，scope=`tenant_id+caller_id+dag_key+method+key` | launch + DAG start quota | fail-closed/spool | 只能调 `StartDAG` |
| `dag/edit_dag` / `dag/schedule` | write | owner / tenant admin | 必填；params hash 冲突返 conflict | edit quota | fail-closed/spool | 只允许 draft/not_started |
| `dag/edit_node` | write | owner / tenant admin | 必填 | edit quota | fail-closed/spool | node 必须 `pending` |
| `dag/spawn` | write | assigned agent / owner | 必填；重复返回同一 spawned keys | growth + spend quota | fail-closed/spool | 必经 `SpawnChildNodes` |
| `task/node/update` | write-compat | owner / assigned agent | 必填；strict DAG 默认拒绝 status 推进 | strict compat quota | fail-closed/spool | 不得绕过 CAS / active_turn fence |
| `task/dag/get` / `task/dag/list` / `dag/get/list` | read | tenant filter required | 可选 | read rate | read audit 可降级 | legacy method names 映射到同一 read guard；默认不含 raw result |
| audit query | read-sensitive | tenant + audit role | 可选 | read rate | self-audit | redacted only |

### tenant 空值 rollout

新 strict DAG / 写 RPC / StartDAG 的 `tenant_id` 禁空；历史空值只允许在 backfill 窗口读兼容。service guard 遇到空 tenant 必须 fail-closed + audit，不得落到 default tenant。backfill 完成后追加 DB `CHECK (tenant_id <> '')`（可先 `NOT VALID` 再 validate）；P6 guard 与 P3 owner/tenant migration 必须共享同一 rollout 口径。

### audit hash-chain base contract

`dag_audit_log`、`dag_audit_spool` 以及后段 arbiter/swarm/output validation audit 表使用同一 hash-chain：row canonicalization 固定为 RFC 8785 JCS；`row_hash` 覆盖除 `row_hash` 自身外的稳定字段（含 `prev_hash`、`chain_scope`、tenant/resource/method、params_hash、result_code、latency、called_at 的规范表示）；`chain_scope` 最小粒度为 `tenant_id + logical_stream`，需要 DAG 级串行时用 `tenant_id + dag_key + logical_stream`。同一 chain_scope 写入必须 DB 层串行化（advisory lock 或 chain head row `FOR UPDATE`）；审计表 DB 层禁止 UPDATE/DELETE，纠错只能 append compensating row。

### 两层安全模型

Transport 层（TCP JSON-RPC / Wails WS / MCP HTTP）只负责提取 identity 到 ctx、Origin/CSRF/session/API key 检查；service 层统一执行 AuthN/AuthZ、tenant filter、rate/quota、idempotency、audit。archtest 不能只守 `rpc.go` middleware，必须覆盖三入口 adapter + service enforcement。


### StartDAG idempotency 与外部 key

外部 `dag/start` 必须使用 P3 统一 `dag_start_requests` schema：`trigger_source='external'`，`trigger_instance_key` 来自 authenticated caller 提供的 idempotency key（无 key 则拒绝写入，不自动用随机 key），`params_hash` 采用 canonical JSON。该 scope 与 `host/cron/manual` 互斥；如需要跨来源 active run 互斥，必须在 StartDAG 内增加 DAG active-run lease/CAS。

### 错误语义

服务层统一返回 `ErrRateLimited{retry_after}`；HTTP transport 映射 429，JSON-RPC 映射固定 error code `ErrCodeRateLimited = -32029` + `data.retry_after`，不得在 TCP JSON-RPC 文档里只写 HTTP 429。
