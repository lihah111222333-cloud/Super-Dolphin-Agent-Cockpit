# ADR 0002: Session Ports And Prefix Stability

日期：2026-06-27

状态：Proposed

## 背景

Reasonix 的架构里有值得吸收的边界设计：session-facing ports、稳定 event wire、prompt prefix 观测、desktop dependency 隔离、MCP tool namespace 生命周期。super-agent-v3 不能照搬 Reasonix 的全局 Controller，因为当前系统以 Fx modules、owner modules、独立 mcp-orch runtime 为边界。

## 决策

1. 只吸收边界模式，不引入全局 Controller。
2. session ports 第一阶段只覆盖 lifecycle/read surface，并要求 `thread/start` 字段无损映射。
3. event wire 必须演进现有 `eventsurface.Notification` 和 `ExpandNotifications`。
4. prefix shape 必须来自当前 prompt assembly 事实源：`Boundary`、`ResolvedSections`、`Snapshot`、`SuppressedTools`。
5. MCP lifecycle 只有在状态 owner 和来源明确后才能进入生产 filtering。
6. desktop dependency guard 限定 `internal/module`、`internal/provider`、`internal/platform` 非 UI 子包；`internal/app` 允许装配 Wails。

## 执行门槛

- 在干净隔离 worktree 上执行，或明确列出当前 dirty 文件并只 stage 本计划文件。
- Phase 1 三份 spike 文档完成前，不允许修改 prompt/provider/toolbridge/event production code。
- 每个 phase 结束必须运行本计划列出的验证命令。
- 如果 `internal/archtest/baseline.json` 变化，必须在报告中解释 diff。

## 回滚

- 每个 phase 独立提交。
- 若 session port 接入后 parity test 失败，回滚 Phase 2，不继续 Phase 3。
- 若 event method golden 与 frontend 消费不一致，保留现有 `ExpandNotifications` 行为并停止迁移。
