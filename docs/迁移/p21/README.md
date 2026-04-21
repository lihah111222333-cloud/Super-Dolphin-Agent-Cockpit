# P21 架构演进与能力补齐总览

基于 Hermes Agent 源码审查，为 `super-agent-v3` 补齐能力差距的整体路线图。
本批文档在审查后统一对齐当前仓库的真实基线：`fx + jrpc2 RPC handlers + event bus + provider/unified + Postgres/sqlc + migrations`。

## 当前基线约束

- 宿主 UI / 管理 API 优先落在 `internal/module/*`，并通过 `rpc.HandlerMapResult` 暴露；但 Agent 工具执行、DAG、workspace、长期编排默认仍属于 `cmd/mcp-orch` 的 MCP 工具执行面，不应强行塞回 core RPC。
- `internal/mcpserver/common/bootstrap/*` 是当前合法的工具侧 lifecycle client，可复用；禁止的是把业务能力继续堆到 common 旁路。
- 关系型持久化统一走 `migrations/` + `sql/queries/` + store + sqlc，但当前存在 core 与 `cmd/mcp-orch` 两套 sqlc 生成面；新增表/查询时必须同步维护对应的 `sqlc.yaml`。
- Skill、memory 等内容型数据已有文件型持久化例外；不要为了“统一”把 `SKILL.md` 之类强行改落 Postgres。
- 长生命周期 side effect 优先做成 bus 订阅者，使用 `bus.ResilientSubscribe(...)` + `fx.Lifecycle` 管理，参考 `internal/module/memory/module.go`。
- `bus.ResilientSubscribe(...)` 只负责订阅与 panic recover，不提供异步队列；HTTP、LLM、重 DB 操作必须出 handler、进 worker，不能在订阅回调里直接阻塞执行。
- Provider 自定义配置优先复用 `thread/start` 已有的 `config` 透传链路；实例键应放在专用 config 字段里，而不是继续复用 top-level `modelProvider` 做 provider instance 路由。
- `thread/start` 的 `config` 是 host-side runtime config / driver 输入，不等于所有 key 都会透传到远端 provider RPC。P1a 这类实例选择必须在 driver 侧消费并持久化恢复信息。

## 实施路线图

| 优先级 | 特性 | 描述 | 对标 Hermes Python 代码 | 预计 Go 实现代码 | 预估工时 | 当前状态 |
|---|---|---|---|---|---|---|
| **[P0](P0_SelfLearningSkill.md)** | 自学习 Skill 闭环 | Agent 自动将成功经验提炼为可复用 Skill | ~2,200 行 | ~900 行 | 3-4 天 | ⏳ 已有 Skill 写入与事件总线，缺 agent-friendly create 与自动提炼闭环 |
| **[P1a](P1a_MultiProviderCodex.md)** | 多 Provider Codex 实例 | 按 `CODEX_HOME`/`instanceKey` 隔离接入 GLM5.1、Qwen3.6 等 | ~3,000 行 | ~300 行 | 1-2 天 | ⏳ 现有 `codexapp` 是单 shared app-server，需改为按实例键池化 |
| **[P1b](P1b_CronScheduledTasks.md)** | Cron 定时任务 | Agent 可调度定时循环执行任务 | ~1,500 行 | ~800 行 | 3-4 天 | 🔲 未开动 |
| **[P2](P2_MultiPlatformNotifications.md)** | 多平台通知 | 结果通过 Webhook/Bot 自动推送 | ~3,000 行 | ~500 行 | 2-3 天 | 🔲 未开动 |
| **[P3](P3_SessionInsights.md)** | Session Insights 遥测 | 会话使用分析与 Skill 命中率统计 | ~1,000 行 | ~400 行 | 1-2 天 | 🔲 未开动 |

## 落地顺序建议

1. 先做 `P0a`：agent-friendly skill create / scope-safe 写入；`P0b` 自动提炼闭环建议放到 `P3` 最小 trajectory/insight collector 就绪后，避免重复造聚合器。
2. `P1a` 与 `P1b` 可以并行设计，但 `P1a` 要先明确 app-server 多实例边界，否则 Cron 后台任务的 provider 选择会被卡死。
3. `P2` 与 `P3` 都应采用事件订阅式落地，避免继续把 side effect 塞进 `turn/service.go` 等执行主链。

**总计**：约 12-14 天，预估新增约 3k 行 Go 代码。真正的复杂度不在代码量，而在保持现有模块边界、store 约定和 provider 生命周期不退化。
