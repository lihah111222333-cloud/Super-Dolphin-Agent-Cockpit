# V2↔V3 1:1 行为+容错核对 — 终极报告

> 生成时间：2026-03-21
> 核查 Agent：21 个 Codex Agent（20 初始核对 + 1:5 交叉互审）
> 汇总 Agent：2 个 Codex Agent
> V2 路径：/Users/mima0000/Desktop/wj/go-agent-v2/
> V3 路径：/Volumes/bot/super-agent-v3/

---

## 1. 总体结论

| 统计 | 数量 |
|---|---:|
| ✅ 行为一致 | **9** |
| ⚠️ 部分对齐 | **40** |
| ❌ 行为不一致 | **56** |
| 🆕 互审新增 | **~25** |
| Agent 最终裁定 ❌ | **21/21** |

**结论：V3 当前未达到 V2 语义 1:1。** 多数差异属于设计演进（框架替代、协议简化），但安全退化、能力缺失和事件面收缩需要修复。

---

## 2. 终极对比总表

| 模块 | ✅ | ⚠️ | ❌ | 核心问题 | 裁定 |
|---|---:|---:|---:|---|---|
| thread-start | 0 | 0 | 5 | start 返回/config/messages/fork 均有差异 | ❌ |
| thread-config | 1 | 2 | 2 | config/set 仍是 TODO，model/set 走 command shell | ❌ |
| thread-messages | 0 | 1 | 4 | 分页 cursor 漂移，sidebar 合同缩水 | ❌ |
| turn-lifecycle | 0 | 2 | 3 | steer 变新 turn，interrupt 返回简化 | ❌ |
| approval | 1 | 0 | 4 | 无默认 timeout，pending 可无限悬挂 | ❌ |
| orch-agent | 0 | 1 | 4 | stop 不级联，进程清理异步 | ❌ |
| orch-submit | 1 | 3 | 1 | DAG 锁模型变弱，stopped 早于真实退出 | ❌ |
| orch-statemachine | 0 | 3 | 2 | approval 重复发布，状态泄漏 | ❌ |
| workspace | 0 | 3 | 2 | merge 不落盘，delete_removed 语义反转 | ❌ |
| skill-exec | 1 | 1 | 3 | card/run 绕过 guard，abort 不宽松 | ❌ |
| skill-fs | 2 | 3 | 0 | 路径越界，importDir 覆盖同名 | ❌ |
| dashboard | 1 | 2 | 2 | TaskWakeup 未接调度，approvals/set 收紧 | ❌ |
| uistate | 0 | 0 | 5 | sidebar/preferences/events 全面缩水 | ❌ |
| provider-unified | 0 | 2 | 3 | WSHandler 未接线，manifest 丢字段 | ❌ |
| provider-codex | 0 | 1 | 4 | reconnect 弱化，history trim 退化 | ❌ |
| provider-claude | 1 | 3 | 1 | reconnect 后不 reinitialize，EOF 不 finish | ❌ |
| rpc-push | 0 | 2 | 3 | push 面大幅缩小，tokenusage 不桥接 | ❌ |
| bus-event | 0 | 2 | 3 | agent-event 消失，命名漂移 | ❌ |
| store-db | 1 | 2 | 2 | thread/read 折叠，workspace payload 缩水 | ❌ |
| wails-ui | 0 | 4 | 1 | agent-event 通道消失，preference 副作用丢 | ❌ |
| fx-runner | 0 | 3 | 2 | reportRuntime 未闭环，中间件链变薄 | ❌ |

---

## 3. ❌ 关键行为不一致清单（按优先级）

### P0 安全退化（必须立即修）
1. skills/local/* 可越 root 读写任意路径
2. command/card/run 绕过危险命令拦截
3. command/exec denylist 可绕过
4. skills/local/importDir 可覆盖同名目标

### P1 核心功能缺失（P7.5/P8 修）
5. thread/config/set 仍是 TODO/command shell
6. turn/steer 变成新 turn（不再是运行中追加）
7. turn/interrupt 返回简化为 {ok:true}
8. workspace merge 不落盘（只改状态/哈希）
9. workspace delete_removed 语义反转
10. approval 无默认 timeout/auto-decline
11. recover 不 replay active turns
12. agent.stop 不级联清理进程资源
13. submit 到未 ready session 直接失败（V2 会等待）

### P2 协议/事件面收缩（P8 后补）
14. rpc push 面大幅缩小（缺 item/*、approval、plan、error 等）
15. typed event 覆盖不足（V2 ~30 种 → V3 ~11 种桥接）
16. agent-event 通道消失
17. uistate/sidebar 返回模型缩水
18. provider reconnect/recovery 弱化
19. WSHandler 存在但未接线

### P3 设计演进（可接受差异，记录备案）
20. 分页从 message ID cursor 改为时间 cursor
21. thread/start 返回 envelope 简化
22. effectiveState 二次投影 → 单一状态机
23. fx/run.Group 替代手动 shutdown
24. sqlc 替代 BaseStore 手写 SQL
25. kelindar/event 替代字符串 topic

---

## 4. 🆕 互审发现的新问题

| # | 问题 | 严重度 |
|---|---|---|
| 1 | approval 事件可能被重复发布两次 | High |
| 2 | agent.stop 先发 stopped 事件再异步杀进程，时序不安全 | High |
| 3 | agent.stop/handleProcessExit 双重 removeSession 可能误删新 session | High |
| 4 | orchestration queue 浅拷贝，调用方后改对象污染已入队任务 | High |
| 5 | thread/status/changed 泄漏成 agent state | Medium |
| 6 | runtime port/provider sticky，进程退出后 snapshot 暴露旧数据 | Medium |
| 7 | skills/local/importDir RemoveAll(target) 覆盖同名 | Medium |
| 8 | thread/name/set 只改本地 store 不下沉 provider | Medium |
| 9 | Claude EOF 路径直接返回不 finish active turn | Medium |
| 10 | Codex reconnect 后不重做 initialize | Medium |
| 11 | WSHandler 存在但没接线 | Low |
| 12 | reportRuntime 没有真实生产者调用 | Low |
| 13 | tokenusage 常量存在但选择不桥接 | Low |

---

## 5. 修复优先级建议

### 波次 1（P7.5 收尾，4 Agent）
- P0 安全退化 4 项（skill 路径越界 + command guard）
- P1 核心功能 thread/config/set + approval timeout

### 波次 2（P8 并行，6 Agent）
- workspace merge 落盘 + delete_removed 修复
- turn/steer + interrupt 语义恢复
- agent.stop 级联 + submit session ready 等待
- recover replay

### 波次 3（P8 后，4 Agent）
- rpc push 面扩展
- typed event 补全
- provider reconnect 加强
- uistate/sidebar 字段补齐

### 波次 4（P9+，延后）
- 设计演进差异（分页、envelope、shutdown 等）记录备案不修

---

## 6. 与现有计划的关系

| 现有计划 | 本报告关联项 |
|---|---|
| P7.5 桥接校准 | 安全退化 4 项 + approval timeout |
| P8 编排工具 | workspace/DAG/submit 语义恢复 |
| P9 LSP 工具 | rpc push 扩展 + provider 加强 |
| P10 工厂 | 设计演进差异备案 |
