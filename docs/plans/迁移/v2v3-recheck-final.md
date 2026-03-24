# V2↔V3 核对终极结论

> 日期：2026-03-25
> 核对 Agent：20 个 Codex Agent（初始核对 + 1:10 互审 + 二次审修正）
> 汇总 Agent：2 个（Codex Summary-A + Claude Summary-B）
> V2 路径：/Users/mima0000/Desktop/wj/go-agent-v2/
> V3 路径：/Volumes/bot/super-agent-v3/
> 排除范围：P9 LSP MCP、IDA 模块

---

## 1. 总体结论

| 指标 | 上次（03-21） | 本次（03-25） | 变化 |
|------|-------------|-------------|------|
| ✅ 1:1 完成 | 0 / 21 | 0 / 20 | — |
| ⚠️ 部分对齐 | 0 / 21 | **12 / 20** | 🟢 大幅改善 |
| ❌ 仍不一致 | 21 / 21 | **8 / 20** | 🟢 从全❌→60%⚠️ |
| 已修复子项 | — | **23 个** | P7.5/P8 成果 |
| 仍未修复子项 | 56❌ | **35 个** | 下降 37% |
| 新发现 | — | **4 个** | 互审补出 |

**结论：V3 已大幅接近 V2 语义，但仍未达到 1:1。最紧急的不再是 skill 安全面（已全修），而是 thread-start guard + approval fail-closed。**

---

## 2. 模块级总览

| # | 模块 | ✅ | ⚠️ | ❌ | 裁定 |
|---|------|---:|---:|---:|------|
| 01 | thread-start | 2 | 1 | 5 | ❌ |
| 02 | thread-config | 0 | 3 | 1 | ⚠️ |
| 03 | thread-messages | 0 | 1 | 4 | ❌ |
| 04 | turn-lifecycle | 2 | 2 | 2 | ⚠️ |
| 05 | approval | 2 | 1 | 1 | ⚠️ |
| 06 | orch-agent | 2 | 1 | 1 | ⚠️ |
| 07 | orch-submit | 1 | 2 | 1 | ⚠️ |
| 08 | orch-statemachine | 3 | 0 | 1 | ⚠️ |
| 09 | workspace | 1 | 1 | 1 | ⚠️ |
| 10 | skill-exec | 2 | 1 | 0 | ⚠️ |
| 11 | skill-fs | 2 | 0 | 1 | ⚠️ |
| 12 | dashboard | 0 | 1 | 3 | ❌ |
| 13 | uistate | 0 | 1 | 2 | ❌ |
| 14 | provider-unified | 1 | 2 | 1 | ⚠️ |
| 15 | provider-codex | 1 | 1 | 1 | ⚠️ |
| 16 | provider-claude | 1 | 0 | 2 | ❌ |
| 17 | rpc-push | 1 | 1 | 2 | ⚠️ |
| 18 | bus-event | 0 | 2 | 2 | ❌ |
| 19 | store-db | 0 | 1 | 2 | ❌ |
| 20 | wails-fx | 2 | 4 | 4 | ❌ |

---

## 3. P0 安全退化（3 项，必须立即修）

| # | 模块 | 问题 | V2 行为 | V3 行为 | 工作量 |
|---|------|------|---------|---------|--------|
| P0-1 | thread-start | 默认值/guard 链不对齐 | `danger-full-access⇒approvalPolicy=never`、enum guard、sandbox 容错、`cwd="."` fallback | 仅默认 `provider=codex`，无等价 guard | 中 |
| P0-2 | thread-start | binding/thread-id 一致性退化 | 首写 authoritative、幂等、backfill、mismatch 拒绝、public/provider id 分层 | `persistThreadState()` 直接 Upsert，id 混写 | 大 |
| P0-3 | approval | fail-closed/auto-decline 缺失 | 无前端⇒拒绝、agent gone⇒deny、policy=never⇒auto-respond | bridge==nil 时直接返回不 decline，只能等 5 分钟 timeout | 中 |

> 上次 4 个 skill 类 P0 已全部修复 ✅

---

## 4. P1 核心功能缺失（30 项，修复优先级排序）

### 第一梯队：高影响（15-20 人日）

| 优先级 | 项 | 模块 | 工作量 |
|--------|---|------|--------|
| 🟠 1 | interrupt→complete 闭环 | turn-lifecycle | 中 |
| 🟠 2 | StopAllAgents 统一停机 | orch-agent | 中 |
| 🟠 3 | claude reconnect/reinitialize | provider-claude | 中 |
| 🟠 4 | codex 进程启动/停止 | provider-codex | 大 |
| 🟠 5 | DAG 锁/wakeup fencing | orch-submit | 大 |
| 🟠 6 | uistate 事件投影 | uistate | 大 |
| 🟠 7 | dashboard 日志/DAG 面 | dashboard | 大 |

### 第二梯队：协议/数据面（10-15 人日）

P1-05 config/read、P1-06~09 thread-messages 系列、P1-10 turn/interrupt envelope、P1-21 preferences 合同、P1-26 claude turn finish、P1-27~28 store DTO

### 第三梯队：次优先（8-12 人日）

P1-01~04 thread-start 功能、P1-12 approval live replay、P1-15 execute-time ready wait、P1-17~18 workspace dry-run/merge、P1-19~20 dashboard 补全、P1-23 WSHandler、P1-29~30 wails desktop

---

## 5. P2 协议/事件面收缩（12 项，5-8 人日；注：Summary-A 列 11 项，缺 02-thread-config P2 条目）

thread/start 返回 envelope、config 语义变更、messages before 类型、turn payload 缩水、dashboard 收窄、uistate 缩水、manifest env、claude translator、rpc-push method 缺失、raw passthrough 收紧、bus naming 漂移、workspace tool DTO

---

## 6. P3 设计演进（7 项，可接受差异）

statemachine 单一化、session/binding 分拆、typed event bus、sqlc 替代 BaseStore、fx+run.Group 替代手写 shutdown、dashboard/system/info V3-only、skill-fs 更严格沙箱

---

## 7. 跨模块根因

| 根因 | 涉及模块 | 修复方向 |
|------|---------|---------|
| A. session-centric 架构 | 01,02,03,13 | 恢复 thread-scoped config/state resolver |
| B. orchestration 终态闭环不完整 | 04,06,08 | 扩展事件消费面 + 统一 stop |
| C. provider 进程生命周期薄弱 | 06,15,16 | 定义统一 provider 进程契约 |
| D. RPC 返回体未系统性迁移 | 01,03,04,12,17,19 | 建立 V2 RPC golden 测试集 |
| E. approval/guard 链路分散 | 01,05,08 | 补 fail-closed + guard 链 |

---

## 8. 已修复项（23 个，P7.5/P8 成果）

- skill 安全：command/exec ✅、skills/local 越 root ✅、importDir 同名 ✅
- orchestration：agent.stop 等待 ✅、queue 深拷贝 ✅、approval 双发 ✅、状态泄漏 ✅
- turn：steer 运行中追加 ✅、interrupt 本地清理 ✅
- approval：timeout ✅、重复发布防护 ✅
- workspace：delete_removed ✅
- provider：codex history trim ✅、claude EOF finish ✅、SessionManager.CloseAll ✅
- rpc-push：大量 push method ✅
- wails-fx：agent-event ✅、reportRuntime 闭环 ✅

---

## 9. 新发现（4 项，互审补出）

1. thread-messages：V3 历史读取强依赖 active session（P1）
2. skill-fs：importLocalDir 不再强制 SKILL.md（P1）
3. wails-fx：preferences/set 副作用仅部分恢复（修正为 ⚠️）
4. bus-event：消失的是 internal bus envelope，UI 通道仍在（修正表述）

---

## 10. 修复路线建议

```
本周:  P0-3 approval auto-decline → P0-1 thread guard → P0-2 binding id
第2周: 根因B orchestration终态 → 根因C provider进程
第3周: 根因D RPC golden测试集 → 根因E approval/guard
第4周: 根因A session解耦 → dashboard + uistate
```

总工作量：**43-63 人日**（P0: 5-8, P1高: 15-20, P1协议: 10-15, P1次优: 8-12, P2: 5-8）

---

## 11. 互审置信度

- 200 次交叉抽查：✅准确 92.5% / ⚠️有遗漏 6.5% / ❌重大错误 1%
- 二次审修正后核心结论稳定
- **总体置信度：高**

---

## 12. 关联文档

| 文档 | 路径 |
|------|------|
| 20 份核对报告 | docs/plans/迁移/v2v3-recheck/01~20-*.md |
| 汇总 A（按模块） | docs/plans/迁移/v2v3-recheck-summary-A.md |
| 汇总 B（按严重度+根因） | docs/plans/迁移/v2v3-recheck-summary-B.md |
| 上次核对报告 | docs/plans/迁移/v2v3-final-report.md |
| MCP 服务契约 | docs/契约/mcp-service-convention.md |
| 会话摘要 | docs/plans/迁移/session-summary.md |
