# F1.x agent 节点 lifecycle 设计审计

> 日期：2026-05-12 | 范围：F1.1 / F1.2 / F1.3 / F1.5 + dispatchAgent + thread/turn 事件链
> 触发：F1-followup-3（节点卡 ready）→ ADR-015 v1/v2 五轮失败 → 揭示问题在更高一层
> 这份文档**不预设结论**，只盘点真实代码现状 + 设计层未决问题 + 候选闭环路径

---

## 0. 审计动机

第七轮 DAG dogfood (`dag-validation-v4-2026-05-12`) 跑通 M3 端到端：spawn 出 child thread + first_turn 真执行。但**节点 status 卡在 `ready`，下游永不 promote**。

ADR-015 经历 v1 → v1 自审 → v2 → reviewer 审 → 双链路核对 → claude 路径质疑 → event_relay 桥发现 — **五轮迭代仍在错**。

根因不是审查方法不够，是**我们在解的不是真问题**：F1-followup-3 揭示的是整组 F1.x lifecycle 所有权未定 + outputs 设计在 launch 时就 finalize 的根本缺陷。本审计强制回答 5 个核心问题，落定后再写新 ADR。

---

## 1. F1.x 现状盘点（源码核对）

> ⚠️ **Snapshot date: 2026-05-12**：本节描述的 5 处 F1.x 现状（F1.1 / F1.2 / F1.3 / F1.5 / dispatchAgent / TurnCompleted 订阅）反映 C-A 实施前的代码状态。C-A 实施后（详 §8 + `docs/plans/dag-lifecycle-c-a-implementation.md`）部分事实将失效（特别是 §1.3 F1.3 launch-time 落地 + §1.5 dispatchAgent 不写 DB）。

### 1.1 F1.1 — AgentExecutor.Execute（`executor_agent.go:141-223`）

链路：
1. `ParseAgentConfig` 解码 node.config → typed `AgentNodeConfig`
2. `validateAgentOutputs` 校验 outputs 禁忌键（webhook_url / command_ref 等）
3. `validateAgentOutputs` 检查 agent_key 非空
4. `assembleInputs` 拉 prev results + sharedfile → prompt 前缀
5. `buildLaunchRequestFromAgentConfig` 构造 LaunchRequest（AgentID / Name / AgentKey / Language / Prompt / AgentType）
6. `e.launcher.LaunchAgent(ctx, req)` → 返 `(threadID, err)`
7. launch 成功 → `spawnWriteback`（F1.5 写 spawning_thread_id）→ `finalizeAgentOutcome`
8. launch 失败 → `classifyAgentLaunchError` → `NodeOutcome{Status: failed, FailureClass: ...}`

**关键事实**：Execute **同步返回**，返回时 child thread **才刚 spawn**，没跑过任何 turn。返回的 `NodeOutcome{Status: Done}` 语义是「spawn 成功」，不是「任务完成」。

### 1.2 F1.2 — assembleInputs（`inputs.go:106-279`）

prompt 前缀拼装：
- `inputs.from_nodes[*]` → 读 `RunContext.PrevResults[node_key]` → section `## node:<key>\n<result>`
- `inputs.from_sharedfiles[*]` → 读 `RunContext.SharedFileReader(path)` → section `## sharedfile:<path>\n<content>`
- 最终 prompt = `inputsPrefix + "\n\n[first_turn]\n" + cfg.FirstTurn`

**关键缺漏**：
- 当前节点自己的 `dag_key` / `node_key` 字面值**从未注入 prompt**
- `RunContext.DagKey/NodeKey` 字段只在 store 层和 F1.5 spawnWriteback 内部消费
- → spawned agent 不知道自己是哪个 DAG / 哪个 node，**无法调 `task_update_node` 自报状态**

### 1.3 F1.3 — outputs 处理（`executor_agent.go:247-271 finalizeAgentOutcome` + `executor_agent.go:273-299 writeAgentSharedfile`，2026-05-12 reviewer 修正行号偏差）

`finalizeAgentOutcome` 在 launch 成功**同步**执行：
```go
result := agentLaunchResult{
    ThreadID: threadID,
    AgentKey: cfg.Exec.AgentKey,
}
payload, _ := json.Marshal(result)
writeAgentSharedfile(ctx, cfg, runCtx, payload)  // sharedfile 写 {thread_id, agent_key}
outcome := NodeOutcome{Status: NodeStatusDone, ErrorSummary: errorSummary}
if shouldEmitNodeResult(cfg.Outputs) {
    if failure := enforceNodeResultSizeCap(payload); failure != nil { ... }
    outcome.Result = payload  // node.result 也是 {thread_id, agent_key}
}
```

**关键缺陷**：outputs.to_sharedfile 和 outputs.to_node_result 在 **launch 完成瞬间**就被 finalize，写入的是 launch 元数据 `{thread_id, agent_key}`，**不是 thread 实际跑出的内容**。

后续 thread 真正生成的回复内容**没有任何路径写回 sharedfile 或 result**——除非 agent 自己显式调外部工具，但 F1.3 ADR 明确禁止 agent outputs 触发外部 webhook（ADR-011 §4 Q3）。

→ **F1.3 的设计与"agent 节点产出"语义不匹配**。它写的是"launch handshake 凭证"，不是"agent 输出"。

### 1.4 F1.5 — spawnWriteback（`executor_agent.go:313-329` + `NodeSpawnRecorder`）

链路：
1. launch 成功 → `resolveSpawnKeys(node, runCtx)` 拿 dagKey/nodeKey
2. `recorder.RecordNodeSpawn(ctx, dagKey, nodeKey, threadID)` 写 `task_dag_nodes.spawning_thread_id`
3. ADR-009 软引用 + partial index 支持反查

**关键事实**：F1.5 完整落地 + dogfood v4 验证字段写回成功。但 spawning_thread_id 只是**索引位**，没有触发任何 lifecycle 推进逻辑。

### 1.5 dispatchAgent（`node_router.go:291-296`）

```go
func (r *NodeExecutorRouter) dispatchAgent(ctx, node, runCtx) (NodeOutcome, error) {
    if r.agentExec == nil {
        return validationOutcome("..."), nil
    }
    return r.agentExec.Execute(ctx, node, runCtx)
}
```

**关键缺陷**：
- 拿到 `NodeOutcome{Status: Done}` **直接丢弃**，不调任何 DB 写回函数
- 对比 `dispatchAutomation:298-314` 有 `completeAutomationNode` → 调 `CompleteNodeAndScheduleDownstream`
- → DB 中节点 status 永远停在 `ready`

### 1.6 turn.TurnCompleted 事件（`internal/dto/turn/event.go:11-21`）

```go
type TurnCompleted struct {
    shared.TurnHeader  // 含 AgentID + ThreadID + TurnID + Timestamp
    Success    bool    `json:"success"`
    Error      string  `json:"error,omitempty"`
    Status     string  `json:"status,omitempty"`
    Reason     string  `json:"reason,omitempty"`
    Result     string  `json:"result,omitempty"`
    Summary    string  `json:"summary,omitempty"`
    Message    string  `json:"message,omitempty"`
    StopReason string  `json:"stop_reason,omitempty"`
}
```

**这是真正的 lifecycle 锚点**——携带：
- **`Success bool`**：显式成功/失败信号（不像 thread.Stopped.Reason 模糊不清）
- **`Result/Summary/Message string`**：thread 实际跑出的内容（事件本身就有，无需跨模块查 thread 历史）

**现有订阅**：`internal/sidecar/orch/orchestration/service.go:269-274` 通过 `bus.ResilientSubscribe` 订阅，但**只用来推进 agent 状态机**（`handleTurnCompletedEventWithCtx` → `svc.CompleteTurn`），**不推进 DAG 节点状态**。

→ event_relay 也桥接：`Stopped → TopicProcessExit` + `TurnCompleted → TopicTurnAfter`（`event_relay.go:64-70`），所以 hookConsumer 通过 hook topic 也能看到 TurnCompleted。

### 1.7 task_update_node MCP 工具（`tools/task_tools.go:400-405` + `nodeexec/status.go:7`）

MCP 工具已注册：
```
{dag_key, node_key, status, result}
```
9 态状态机 ValidateTransition 阻挡跳态（pending→done 非法）。

**问题**：agent prompt 里没有 dag_key/node_key 注入（§1.2），agent 不知道自己该填什么。理论上 agent 可以「猜」（如果 node_key 命名规律可推断），但是脆弱设计。

### 1.8 一图总结现状

```
                       [DAG dispatcher]
                              │
                              ▼ 拾起 ready 节点
                       [dispatchAgent]
                              │
                              ▼ 不写 DB ❌
                    [AgentExecutor.Execute]
                              │
                              ▼ 调 launcher
              ┌─── [LaunchAgent] ───┐
              │                      │
              ▼                      ▼
       [F1.5 spawn]          [F1.3 outputs]
       spawning_thread_id    sharedfile 写 {thread_id, agent_key}
       回写 task_dag_nodes   node.result 同上 ❌ 不是 thread 真产出
              │                      │
              └──────────┬───────────┘
                         ▼
              NodeOutcome{Status: Done}  ❌ 被 dispatchAgent 丢弃
                         │
                         ▼
              [child thread 跑 turn]
                         │
                         ▼
              TurnCompleted{Success, Result, ...}  ❌ 没人接给 DAG
                         │
                         ▼
              [agent stop / archive / process exit]
                         │
                         ▼
              ThreadStopped{Reason}  ❌ 没人接给 DAG
                         │
                         ▼
              hookConsumer 推进 agent runtime  ✅
              （但不推进 DAG node）
```

---

## 2. 五个核心问题

### 2.1 Q1：agent 节点的"完成"语义到底是什么？

**当前代码里有四个候选定义，互相冲突**：

| 候选 | 出处 | 优点 | 缺点 |
|---|---|---|---|
| **A. launch 成功 = 完成** | AgentExecutor.Execute 返 Done | 最简单 / 同步 | 下游拿不到 thread 真产出 |
| **B. turn 完成 = 完成** | TurnCompleted 事件 | Success 显式 / 带 Result | 一个 thread 可能多 turn |
| **C. thread 停止 = 完成** | ThreadStopped 事件 | 时序最清晰 | Reason 字符串不区分 success / kill |
| **D. agent 自报 = 完成** | task_update_node MCP 工具 | 显式语义 / 带 result | 依赖 agent 自律 + prompt 注入 |

**互相冲突表现**：F1.1 用 A（返 Done），F1.3 也是 A（launch 时写 outputs），F1.5 是 A（写 spawning_thread_id 同步），但 dispatchAgent 不接 A 的 Done。

**审计判断**：A 是当前实现的事实但不完整；B 是事件层最自然的锚点；C 是 ADR-015 的失败方向；D 是 fast-path 候选但需要 prompt 改造。

**未决**：F1.x 整组到底打算用哪个？现在的代码是"半 A 半未定"，dispatcher 没接 A 的 Done。

### 2.2 Q2：thread 产出（实际回复内容）什么时候写、谁负责写到下游可读形态？

**当前事实**：没有任何一处把 thread 跑出的 turn.Result/Summary/Message 写到 DAG 的 outputs（sharedfile / node.result）。F1.3 写的是 launch 元数据。

**含义**：DAG 多节点链下游目前**无法**从上游 agent 节点拿到实际产出。整个 F1.x outputs 体系是断的。

**候选位置**：
- 在 turn.completed 订阅者里：拿 ev.Result + 反查 spawning_thread_id → 写 sharedfile / node.result
- 在 agent 自报路径里：agent 调 task_update_node 时塞 result 进去
- 在 hookConsumer 路径里：跟 ADR-015 v1/v2 同构（但 thread.stopped 不带内容）

**未决**：F1.x 设计意图是 thread 产出"必须显式写"还是"应自动落地"？现有 ADR 没说。

### 2.3 Q3：lifecycle 所有权归谁？

四方都沾边但没人闭环：

| 主体 | 当前角色 | 该负责 lifecycle 推进吗？ |
|---|---|---|
| AgentExecutor | 同步返 NodeOutcome | ❓ 当前事实是返 Done，但 dispatcher 不接 |
| dispatchAgent (Router) | 转发 Execute 返回值 | ❓ dispatchAutomation 路径上推进，agent 路径上不推 |
| dispatcher（wakeup_dispatcher） | enqueue / pickup | ❌ 仅调度，不负责终态推进 |
| hookConsumer | 推进 agent runtime 状态机 | ❌ 与 DAG 解耦 |
| service.TurnCompleted 订阅 | 推进 agent runtime 状态机 | ❌ 与 DAG 解耦 |
| agent 自报（task_update_node） | MCP 工具入口已存在 | ❓ 设计意图模糊 |
| 反查 subscriber（v1/v2 提议） | 不存在 | ❓ 候选方向但有架构争议 |

**未决**：谁是 lifecycle 推进的"主"，谁是"辅"？

### 2.4 Q4：F1.5 spawning_thread_id 的设计意图（双重用途冲突）

ADR-009 明文说 spawning_thread_id 是为：
- UI 节点行 → 子 agent thread 跳转（T6.1）
- AI 设计师按钮 spawn 后反向回溯（T8.1）
- 重试场景写 history events（partial index 设计配合）

但 ADR-015 v1/v2 把它当 lifecycle 反查锚点。两种用途的设计约束不一样：
- **UI/重试视角**：partial index、覆盖语义（最新一次 spawn）就够
- **lifecycle 视角**：需要确保事件链反查 → 推进 → 幂等保护的完整链路

**未决**：F1.5 是不是要升级为 lifecycle 一等公民？还是新加一个 spawning_agent_id 字段位作为 turn-level 锚点？

### 2.5 Q5：与现有 ADR 的内部一致性

| ADR | 与 F1.x lifecycle 的潜在冲突 |
|---|---|
| ADR-004 dispatcher 无 assignee 策略 | 当前 dispatcher 拾起 agent 节点后路径不闭环，与 ADR-004 "对 assigned_to 非空节点 enqueue wakeup" 配合需要 lifecycle 反推路径补完 |
| ADR-006 node.result 4KB cap | 如果 turn.Result 字段直接落 node.result，会超 cap（thread 最后一条回复经常 >4KB），必须配合 outputs.to_sharedfile 强制路径 |
| ADR-009 spawning_thread_id 软引用 | §5 Q1 明文「不跨模块读 thread 历史」——但 turn.Result 是 turn 事件层数据（不是 thread 历史），需明确这条边界是否扩展到事件层 |
| ADR-011 hybrid v2（已 ⏸ 降 H） | agent→automation 链路依赖 agent outputs 落地，F1.x outputs 设计若不修，agent→automation 链路不可用 |
| ADR-014 prompt_template-first | 系统注入 dag_key/node_key 到 prompt 是否符合 prompt_template-first 设计意图？需要新增一类 system block |

---

## 3. 三条候选闭环路径（带工程估算）

### 3.A 路径 A：turn.completed-driven lifecycle（设计上最干净）

**核心思想**：DAG agent 节点的"完成" = turn 完成事件到达。

**链路**：
1. 新加 `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`：订阅 `turndto.TurnCompleted`
2. 反查 `task_dag_nodes WHERE spawning_thread_id = ev.ThreadID`
3. ev.Success=true → `CompleteNodeAndScheduleDownstream`，result merge 含 ev.Result（受 ADR-006 cap）
4. ev.Success=false → `FailNodeAndCancelDownstream`，result merge 含 ev.Error
5. ev.Result > 4KB → 自动落 sharedfile（升级 F1.3 行为）
6. F1.3 重做：launch 时不写 outputs 元数据；turn.completed 才落地

**优点**：
- 事件携带完整信息（Success + Result），不需要 prompt 注入也不需要 agent 自律
- 与 ADR-009 软引用边界一致（turn 事件层 vs thread 历史）
- 解决 F1.3 "launch 时 finalize outputs" 的根本缺陷
- 不需要 ev.Reason 映射表的繁琐讨论（Success 是布尔）

**缺点**：
- F1.3 需要重做（不是叠加 patch，是从 launch-time-finalize 改成 turn-completed-finalize）
- 一个 thread 可能多 turn 的场景需澄清（哪一个 turn 算"节点完成"？默认 first turn？）
- spawned agent 跑死/被 kill 不会发 turn.completed，需要 thread.stopped 兜底（仍需 ADR-015 但作 fallback）

**工程估算**：
- 新 subscriber + reverse-lookup 端口：~150 行
- F1.3 重做：~300 行（移除 launch-time writeAgentSharedfile + 新 turn-completed-finalize 路径）
- ev.Result → outputs 映射 + sharedfile 强制路径：~100 行
- 单测：~250 行
- thread.stopped fallback（简化版 ADR-015）：~150 行
- **合计 ~950 行，3-5 commit**

### 3.B 路径 B：agent-self-report-driven（agent 主动）

**核心思想**：DAG agent 节点的"完成" = agent 显式调 task_update_node。

**链路**：
1. F1.2 改造：prompt 前缀注入 `## dag_key: <key>\n## node_key: <key>\n`
2. seed 一条 dag-spawned-agent prompt template，教 agent「完成时必须调 task_update_node(status='done', result=...)」
3. agent 跑完显式调 task_update_node → 9 态状态机推 running→done
4. agent 没调（跑死 / 被 kill / 忘了）→ thread.stopped 兜底标 failed
5. F1.3 含义改成：agent 显式 result 走 task_update_node 入参；F1.3 outputs.to_sharedfile 只在 agent 显式调 mcp 写文件时落地

**优点**：
- agent 完全掌控 lifecycle 语义（包括"任务完成但失败"这种细分）
- 与 ADR-014 prompt_template-first 路线一致
- result 形状由 agent 自定义（不受 turn.Result 字符串限制）

**缺点**：
- 依赖 prompt 自律（agent 忘了就走 fallback，但 fallback 默认 failed 会误判正常完成）
- F1.2 改造前置（注入 dag_key/node_key）
- prompt template 改造 + 用户教育成本
- 与 ADR-015 v1/v2 已被自审 P0-1 揭示为不可行的同路径（除非先做 F1.2 改造）

**工程估算**：
- F1.2 改造：~80 行
- prompt template seed migration：~60 行
- task_update_node 路径补强（9 态状态机 + 测试覆盖）：~100 行
- thread.stopped fallback：~150 行
- 单测：~250 行
- **合计 ~640 行，2-3 commit**

### 3.C 路径 C：双驱动（A + B 组合）

**核心思想**：turn.completed 是主路径（自动闭环），agent self-report 是 fast-path（agent 显式控制 result 形状）。

**链路**：
1. F1.2 改造（注入 dag_key/node_key）：~80 行
2. turn.completed subscriber：~150 行
3. task_update_node fast-path：~100 行
4. F1.3 重做：~300 行
5. 双层幂等保护（store SQL 守卫 + 应用层 skip）：~80 行
6. thread.stopped fallback：~150 行
7. 单测：~400 行
- **合计 ~1260 行，4-6 commit**

**优点**：覆盖所有场景（agent 主动声明、turn 正常完成、turn 失败、thread 异常退出）

**缺点**：复杂度最高 / race 窗口最多 / 工程量大

### 3.D 路径 D：放弃 outputs 真闭环（最简）

**核心思想**：承认 agent 节点是 fire-and-forget，下游节点不依赖上游 agent 真产出。

**链路**：
1. dispatchAgent 对称 dispatchAutomation：launch 成功 → completeAgentNode → status=done
2. F1.3 现状保留（只写 launch 元数据）
3. 下游 agent 需要上游产出 → 用户在 DAG 设计时显式给 agent 加 sharedfile 读写工具，agent 自己负责
4. thread.stopped fallback 不需要（done 已经在 launch 后立即推进）

**优点**：
- 工程量最小（~50 行）
- 与 ADR-014 prompt_template-first 一致（agent 是"小服务"而不是 DAG 编排实体）
- 不需要新订阅器 / 新事件链 / prompt 改造

**缺点**：
- F1.3 outputs 自动落地的"承诺"破灭（用户需要手动配置 agent 工具）
- DAG 多节点链下游拿不到上游产出（除非用户显式配）
- 与蓝图 §5 关键决策"agent 节点应能产出供下游消费"冲突

**工程估算**：~50 行，1 commit

---

## 4. 审计结论与建议

### 4.1 必须承认的事实

1. **F1.1 / F1.3 / F1.5 / dispatchAgent / lifecycle 所有权五处设计意图相互冲突且都不完整**——不是哪一处单点 bug
2. **F1.3 outputs 在 launch 时 finalize 是根本缺陷**——任何 lifecycle 修法都得回答"产出怎么落地"
3. **turn.TurnCompleted 是被错过的最佳锚点**——事件层已有 Success + Result，ADR-015 v1/v2 绕远路去碰 thread.stopped
4. **agent prompt 缺 dag_key/node_key 注入**——任何依赖 agent 主动调 task_update_node 的路径都需要前置 F1.2 改造

### 4.2 推荐路径 A（turn.completed-driven）— M3 验收硬阈值要求

> ⚠️ **已被 §8 证伪（2026-05-12 v2 修订）**：本节整段"必须走 A"论证基于一个未实证假设 `ev.Result = thread 真产出`。4 轮实证（详 §8.1）证伪：codex 侧 `event_map.go:171-177` 根本不发 Result 字段。**A 路径不能落地**。新方向走 C-A 组合路径，详 §8.3 + `docs/plans/dag-lifecycle-c-a-implementation.md`。本节保留作过程文档，下面所有"必须走 A"论述都已过时。
>
> **历史修订（已弃）**：初稿曾试探性建议 D 路径（fire-and-forget），被用户当场纠正——D 与蓝图实施计划 §3 M3 验收硬阈值冲突。

#### 4.2.1 为什么必须走 A — 实施计划里的硬证据

**证据 1：M3 验收硬阈值（`docs/plans/dag改造实施计划.md` §3 line 280-283）**
> - 用例必须覆盖 **DAG ≥ 10 节点跑通**
> - 用例必须覆盖单节点 result > 4KB（验证 ADR-006 size_cap + summarization 触发）
> - 用例必须能在 metric 端点读取 `dispatch_failed_total` / `retry_count_per_node` 计数（F15.1）

10 节点 DAG **必然**是 multi-agent-node 链路。这是硬验收指标。

**证据 2：F7.3 prompt_template seed 库设计（line 263）**

10-15 张通用 AI 微技能（`source_monitor` / `topic_curator` / `email_drafter` / `paper_summarizer` / `pr_summarizer` / `data_inspector` / `learning_card` ...）的存在意义就是 AI 设计师**组装 multi-agent-node DAG**：
- `source_monitor → topic_curator → email_drafter`（拉源 → 选题 → 写邮件）
- `data_inspector → paper_summarizer → pr_summarizer`（拉数据 → 总结 → 发 PR）
- `paper_summarizer → learning_card`（论文摘要 → 学习卡）

下游 agent 节点**必然需要拿到上游 agent 真产出**。

**证据 3：F7.3 依赖列明文写着 F1.3 是 multi-agent 链路红线（line 263）**
> **前置顺序**：0086 列 → UpsertPromptTemplate store 扩 → **F1.3 AgentExecutor outputs**（agent 节点 result 落 sharedfile，是 seed 卡跑通后下游拿数据的红线）→ 0087 seed

蓝图作者已经明确 F1.3 是「下游拿数据的红线」——但当前 F1.3 实现写的是 launch 元数据，不是 thread 真产出。**这条红线已经断了而 F1.3 还标 ✅ done**。

**证据 4：F11.1 sharedfile 锁可视化（line 268）**
> UI sharedfile 锁可视化（节点 reads/writes 联动）

reads/writes 联动 = 多节点共享 sharedfile = 必然 multi-agent-node。

#### 4.2.2 推荐路径 A 的理由

- **是 M3 验收硬阈值的必要条件**（不是工程美学）
- 最少改动达到完整闭环（含 F1.3 修法）
- 不依赖 agent 自律（区别于 B/C 的 fast-path）
- 不需要 ev.Reason 字符串映射表的繁琐讨论
- 与现有事件订阅链复用（`service.go:269` 已订阅 TurnCompleted）
- 把 ADR-015 简化为「thread.stopped 是 fallback 兜底」而不是主路径

#### 4.2.3 其它路径的真实定位

- **路径 B**：作为 A 之上的可选 fast-path（agent 想自定义 result 形状时用），不是主路径
- **路径 C**：复杂度收益比不划算，A 已经覆盖正常路径
- **路径 D（撤回）**：与 M3 验收硬阈值冲突；初稿的 D 推荐基于「单节点 dogfood = 用例参考」的方法论错误（dogfood v4 是技术里程碑不是业务终态），已撤回

#### 4.2.4 F1.3 当前状态必须降级（事实仍对，§8 沿用此结论）

F1.3 在 `b0fcf77b`（2026-05-12 第四轮 worktree 并行 + 互审）标 ✅ done，但单测只验证「sharedfile 能写」+「4KB size_cap」+「禁忌键拒绝」——**没验证「写的是 thread 真产出」**。Wave 2 互审未识别此语义缺陷。

**降级动作**：
- 实施计划 §3 表把 F1.3 从 ✅ done 改为 🟡 部分完成
- F 阶段 done 计数同步 20 → 19
- F1-followup-3 升级为 **F1.3-rework + F1-followup-3 合并 ticket**（不是孤立 ticket）

### 4.3 落地前置 ADR 需要做的事

> ⚠️ **已被 §8 证伪（2026-05-12）**：本节假设走路径 A 落地，需要 ADR-015 v3 + ADR-016。两份 ADR 已于 4 轮实证后删除。新方向走 C-A 实施计划（5 个 ADR：X1/X4/X3/X5 + ADR-006 修订），详 §8.3 + `docs/plans/dag-lifecycle-c-a-implementation.md`。

如果选路径 A：
1. **新 ADR-015 v3（推翻 v1/v2）**：从 thread.stopped-driven 改为 turn.completed-driven，thread.stopped 降为 fallback
2. **新 ADR-016（或修订 F1.3）**：outputs 落地时机从 launch-time 改为 turn-completed-time；ev.Result > 4KB 强制走 sharedfile
3. **ADR-009 修订**：明确事件层数据（turn.Result）与 thread 历史的边界——事件层 OK，thread 历史 NOK

如果选路径 B：先做 F1.2 改造（独立 PR，不需 ADR），再考虑 ADR。

### 4.4 不做的事

- **不**在缺乏审计结论的情况下继续修订 ADR-015 v3（这是导致五轮失败的根因）
- **不**直接落码任何 F1-followup-3 修复（lifecycle 所有权未定就动代码会再返工）
- **不**把 F1.3 当成稳定基线（已知缺陷，必须重做）

---

## 5. 开放问题（落地前需用户拍板）

> ⚠️ **已被 §8 证伪（2026-05-12）**：本节 5 个 Q-OPEN 在初稿假设 A/B/C/D 四选一框架下展开。4 轮实证证伪 A 路径后，新方向是 C-A 组合路径，详 §8.3 + `docs/plans/dag-lifecycle-c-a-implementation.md` §0「为什么是 C-A」+ §1「阶段划分」。本节 Q-OPEN 保留作过程文档，已不是当前未决项。

### Q-OPEN-1：选哪条闭环路径？
A / B / C / D 四选一，决定后续 ADR 数量和落地工程量。**建议 A**。

### Q-OPEN-2：F1.3 是否重做？
- 选 A / C → 必须重做（launch-time finalize 是错的）
- 选 B → 部分重做（agent 显式调 task_update_node 时落地）
- 选 D → 不重做（接受现状 fire-and-forget）

### Q-OPEN-3：一个 thread 多 turn 场景下，哪个 turn 算"节点完成"？
- 默认 first turn（spawned agent 通常只跑一轮就 stop）
- 显式 by config（agent_node_config.completion_turn_strategy）
- 等 thread.stopped 才算

### Q-OPEN-4：spawned agent 跑死 / 被 kill 怎么处理？
- 选 A → thread.stopped fallback 标 failed（简化版 ADR-015）
- 选 B → 同上 + agent 自报 failed 是 fast-path
- 选 C → 同上 + 双层
- 选 D → 不需要（done 在 launch 后立即推进）

### Q-OPEN-5：dogfood v4 旧节点 backfill
现有 `dag-validation-v4-2026-05-12` 卡 ready 的节点，新代码上线后是否 backfill？
- 选项：(a) 不 backfill，旧节点保持卡死（让用户手动 task_update_node）；(b) 一次性脚本 backfill；(c) 自动按 spawning_thread_id 反查重新推进
- 建议 (a)：避免 backfill 脚本在生产中误推未真完成的节点

---

## 6. 落码前必须的代码核对清单（路径 A 视角）

> ⚠️ **已被 §8 证伪（2026-05-12）**：本节核对清单按路径 A 视角列出，A 路径已被实证证伪。新核对清单见 `docs/plans/dag-lifecycle-c-a-implementation.md` §7「风险与未解问题」+ §2.2「C2 步骤 0 实测基础设施核对」。本节保留作过程文档。

1. ✅ `internal/dto/turn/event.go:11-21` TurnCompleted 字段位完整
2. ✅ `internal/sidecar/orch/orchestration/service.go:269` 已有 TurnCompleted 订阅基础设施
3. ✅ `task_dag_nodes.spawning_thread_id` partial index 已就位（migration 0083）
4. ⏳ **必须核对**：`turn.Result` 字段在 child agent thread 完成时是否真的被填充（codex / claude provider 两侧都需核）
5. ⏳ **必须核对**：spawned agent 完成时是否会触发 service.CompleteTurn → 发布 TurnCompleted 事件（端到端测试）
6. ⏳ **必须核对**：`CompleteTaskDagNode` SQL 守卫是白名单还是黑名单（ADR-015 v1/v2 reviewer 已揭示是白名单 `IN ('running','awaiting_verify')`）→ 路径 A 需要先把节点推到 running 才能 complete
7. ⏳ **必须核对**：fx wiring 顺序保证 subscriber 注册早于 dispatcher.Start

---

## 7. 变更记录

- 2026-05-12：初稿。基于 ADR-015 v1/v2 五轮失败 + 用户质疑 claude 路径揭出 event_relay 桥架构事实后，全面盘点 F1.x 现状写就。不预设结论，要求用户在 §5 Q-OPEN-1 拍板后再启动 ADR-015 v3 / ADR-016。

---

## 附录 A：dogfood v4 实测路径回放（参考）

```
DAG: dag-validation-v4-2026-05-12
2026-05-12 19:20:38: dispatchAgent 路由到 AgentExecutor.Execute
  ↓
LaunchAgent 成功 → threadID = "agent_1778584828878143000"
  ↓
spawnWriteback 写 spawning_thread_id 成功
  ↓
finalizeAgentOutcome 返 NodeOutcome{Status: Done, Result: {thread_id, agent_key}}
  ↓
writeAgentSharedfile 写 sharedfile = {thread_id, agent_key}  ❌ 不是 turn 产出
  ↓
dispatchAgent 丢弃 NodeOutcome  ❌ 不写 DB
  ↓
DB 节点 status 永远停在 ready  ❌ 卡死
  ↓
spawned thread 跑 first_turn 真回复「✅ M3 DAG 端到端跑通 — service validate gate fix 生效」
  ↓
turn 完成 → TurnCompleted 事件发布（含 Result = 上面那串回复）
  ↓
service.go:269 TurnCompleted 订阅者接收 → 推进 agent runtime 状态
  ↓
（DAG 节点没人推 — 这是 F1-followup-3 痛点的实地证据）
```


---

## 8. 2026-05-12 修订记录：§4.2 推荐 A 被实证证伪

> 本文档 §4.2 初稿推荐路径 A（turn.completed-driven）。基于该推荐起草了 ADR-015 v3 + ADR-016，经独立 reviewer 审 + 4 轮实证证伪 — `ev.Result` 在 codex 侧根本不发，在 claude 侧依赖底层 CLI 二进制行为。ADR-015 v3 + ADR-016 已删除。新方向走 C-A 实施计划（`docs/plans/dag-lifecycle-c-a-implementation.md`）。

### 8.1 4 轮实证关键事实

| 实证 | 关键事实 |
|---|---|
| #1 codex TurnCompleted | `internal/provider/codexapp/event_map.go:171-177` 只填 Success/Error/Status/Reason 四字段，**Result/Summary/Message/StopReason 全是零值**；agent 真实内容在 TurnOutputDelta 流式事件里 |
| #2 claude Result | `claudecli/event_map.go:130` 直接读 raw JSON `result` 字段不截断，但**依赖 claude CLI 二进制行为**（长内容可能被 CLI 截断成 preview） |
| #3 jsonb merge SQL | 当前 5 条写 result 列的 SQL 全是 overwrite；PostgreSQL `||` 可用，已有先例 `task_dag_run.sql.go:258-259` 的 `events || jsonb_build_array(...)` |
| #4 final-answer 累加器 | UI 层 `internal/module/uistate/projector.go:374-392 mergeLastMessage` 字符串 append + 240 字符上限 + 不持久化 + 不可跨模块复用；DAG 层无累加器；无 `FinalAnswerCompleted` 类完整事件 |

### 8.2 §4.2 推荐 A 失败的根因

A 路径假设 `ev.Result = thread 真产出` 是单一事件层假设，但实际真相是 **DAG agent 节点 lifecycle 闭环跨 4 层**：

| 层 | 缺陷 |
|---|---|
| provider 层（codex / claude） | codex 不发 Result；claude 可能被 CLI 截断 |
| service / event 层 | 无 final-answer 完整事件 |
| uistate 层 | 累加器存在但不可跨模块复用 |
| DAG orchestration 层 | 无累加 / 无反查机制 |

ADR-015 v3 试图只在第 4 层解决 — 方向上不可能。

### 8.3 §3 候选方案的真实定位（再校准）

| 路径 | §3 描述 | 实证后真实定位 |
|---|---|---|
| A. turn.completed-driven | 推荐 | ❌ 假设破产（ev.Result 在 codex 侧不发） |
| B. agent self-report | A 之上 fast-path | 🟡 留 H 阶段（依赖 F1.2 注入 dag_key/node_key 改造） |
| C. dispatcher 轮询 | 拒绝 | ❌ 仍拒绝（破坏事件驱动 + 跨模块查表） |
| D. fire-and-forget | 撤回 | ❌ 仍拒绝（与 M3 验收硬阈值冲突） |
| **C-A（新）** | **未在 §3 列出** | ✅ 当前路径（先 provider 层 → 再 DAG 层；物理基础 5 处待 ADR-X1~X5 拍板，详 `dag-lifecycle-c-a-implementation.md` §0-§3） |

C-A 路径详见 `docs/plans/dag-lifecycle-c-a-implementation.md`。

### 8.4 本审计文档保留理由

- §1 现状盘点（F1.1 / F1.2 / F1.3 / F1.5 / dispatchAgent / TurnCompleted 现状）都是真实事实，对 C-A 阶段 2 落地仍有参考价值
- §2 五个核心问题（"完成"语义 / 产出落地 / lifecycle 所有权 / spawning_thread_id 用途 / ADR 一致性）的提问对 C-A 各阶段仍有效
- §4.2 推荐 A 已证伪但保留作为**过程文档**（避免后人翻到 §3 候选方案表时误以为 A 还是推荐方向）
