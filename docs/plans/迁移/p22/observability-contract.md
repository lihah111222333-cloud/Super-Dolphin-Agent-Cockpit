# P22 observability 合同 — 审核现状 & 后续 slice 记账

> 创建时间：2026-04-24 | 状态：**审核完成；未实现；留给 P22 P4 S6 跟进**
> 对应 plan 锚点：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md` §319-324

## 背景

P4 §319-324 列出了 `bootstrap / gopls` 最低 observability contract，
并在 §324 要求"sidecar compatibility / hidden-contract 回滚时必须一并
保留这些 log / metric / trace 名称，避免运维面板断裂"。

本文档记录 2026-04-24 对这 10 个稳定符号的代码审核结果，以便下一轮
slice（暂命名 **P22 P4 S6 — observability stub**）按此基线落地，不再
重新推演。

## 审核方法

- `grep -rn '<symbol>' internal/ cmd/` 全仓扫描
- 同时把实际在 bootstrap / gopls 里 emit 的 log/字符串整理对比

## 审核结果

### §321 logs — 0 / 4 存在

| 期望 anchor | 代码里是否 emit |
|---|---|
| `bootstrap.hook_replay.begin` | ❌ 不存在 |
| `bootstrap.hook_replay.end` | ❌ 不存在 |
| `bootstrap.report_queue.drain` | ❌ 不存在 |
| `gopls.compat_fallback.hit` | ❌ 不存在 |

### §322 metrics — 0 / 3 存在

| 期望 metric | 现状 |
|---|---|
| `heartbeat_failures_total` | ❌ 不存在；`bootstrap heartbeat failed` 只走自由文本日志 |
| `report_queue_dropped_total` | ❌ 不存在；`bootstrap report replay dropped` 只走日志 |
| `reconnect_attempts_total` | ❌ 不存在；`bootstrap reconnect failed` 只走日志 |

### §323 traces — 0 / 3 存在

| 期望 span 名 | 现状 |
|---|---|
| `bootstrap.hook_replay` | ❌ 不存在 |
| `bootstrap.report_queue` | ❌ 不存在 |
| `gopls.transport.compat` | ❌ 不存在 |

### 结论

10 个稳定符号一个都没落地。§324 "回滚时保留名称" 无从遵守 —
名称从未进入过代码树。

## 现状日志面（实际在 emit，自由文本描述）

来自 `internal/mcpserver/common/bootstrap/*.go` + `cmd/mcp-lsp/gopls/*.go`
的扫描：

```
bootstrap audit fallback
bootstrap callback drain timed out
bootstrap connect/register failed
bootstrap disconnected
bootstrap final report failed
bootstrap heartbeat failed
bootstrap heartbeat lease refresh failed
bootstrap hook subscription replay failed
bootstrap hook subscription replay failed; retrying
bootstrap hook subscription replayed
bootstrap local log fallback
bootstrap notify dispatch failed
bootstrap reconnect failed
bootstrap reconnected
bootstrap registered
bootstrap report replay dropped
bootstrap start skipped: GO_AGENT_CTL_RPC_ADDR missing
gopls bootstrap skipped document
gopls recycle failed
recycling gopls process
```

这些是人类可读的 description，**不是稳定 identifier**，监控面板无法
按 event_name 维度 group/count。

## 下一轮 slice 需要做的事（P22 P4 S6）

按"最低门槛 → 高门槛"分三步，可单独合入也可打包：

### S6a — log event_name 稳定化（最便宜、阻塞最小）

在现有 `pkglogger.Warn / Info` 调用上加一个稳定 `event` 字段。保留原
描述文本；新增 `"event", "bootstrap.hook_replay.begin"` 之类的键值对。

影响面：
- `internal/mcpserver/common/bootstrap/hooks.go` `replayHookSubscriptions`
  在开始 / 成功 / 最终失败三个锚点加 `event` 字段（对应 begin / end）。
- `internal/mcpserver/common/bootstrap/report_queue.go` drain 路径加
  `event=bootstrap.report_queue.drain`。
- `cmd/mcp-lsp/gopls/transport.go` `dispatchCompatServerRequest` 命中
  `goplsCompatEmptyStructMethodSet` / `WorkspaceConfiguration` 时加
  `event=gopls.compat_fallback.hit`。

新增 `internal/archtest/observability_log_event_guard_test.go`：扫描
这三个文件，反向校验 4 个 event_name 仍然在 emit 列表里。

### S6b — metric counter 注册点（需决定基础设施）

依赖项目里目前**没有**统一的 metric provider，需要先选型：
- 若落 prometheus：加 `internal/platform/metrics` 小包封装 counter。
- 若走 otel：同理，走 `otel/metric` 包装。

三个 counter:
- `bootstrap_heartbeat_failures_total{binary_name,client_kind}`
  注入点 heartbeat.go 心跳失败分支
- `bootstrap_report_queue_dropped_total`
  注入点 report_queue.go 超容丢弃分支
- `bootstrap_reconnect_attempts_total{outcome}`
  注入点 reconnect.go 每次 attempt 递增，outcome=success/fail

### S6c — trace span（依赖 otel 基础设施）

- `bootstrap.hook_replay` 围绕 `replayHookSubscriptions`
- `bootstrap.report_queue` 围绕 drain 路径
- `gopls.transport.compat` 围绕 `dispatchCompatServerRequest`

如果 S6b 已经把 otel 引进来，S6c 基本免费；否则单独为 trace 引
`go.opentelemetry.io/otel` 不划算，可以延后。

## 验收标准

- 10 个稳定符号全部在代码里可查到定义点
- 新增 archtest 反向校验三个档位（log / metric / trace），防止未来
  silent delete
- `observability-contract.md` 本文档更新"状态"栏，标记 S6a/S6b/S6c
  各自落地 commit SHA
- 新增监控面板文档（可选）记录各符号的语义 + rollback 指引

## 本次 session 未涉及的相邻债务

- §328 `arch-import-direction.md` debt banner 刷新 — 未做
- §329 `codemap` 中 `ui/wails` / `toolbridge` / `orchestration` 稳定职责
  段落加 debt banner 或更新为 P22/P4 口径 — 零散改了 mcp-orch 的几行，
  未做系统扫更
- 全仓 `go test ./...` 绿一次 — 仅跑了触达包，未做一把梭

这几条单独记账，建议随 S6 或新开 slice 处理。
