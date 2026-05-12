# DAG agent 节点 lifecycle 闭环 — C-A 实施计划

> 日期：2026-05-12 | 范围：F1-followup-3 + F1.3-rework 合并 ticket
> 前置：`docs/design/F1-lifecycle-audit-2026-05-12.md`（4 轮实证 + 跨 4 层缺陷盘点）
> 路径：**C 阶段（provider 层基础设施）→ A 阶段（DAG lifecycle 层）**
> 总工程量预估：**~1530-1620 行 / 4-5 ADR / 9-13 commit / 跨 4 层**（v2 reviewer 修订后值，详 §9）

---

## 0. 为什么是 C-A

F1.x 5 轮 ADR 迭代失败的真因：**问题跨 4 层但 ADR 只在第 4 层（DAG orchestration）解**，导致始终绕不开 provider 层的物理缺陷（codex 不发 Result、claude 可能截断）。

C-A 策略：**先把基础设施（provider 层 + spawned agent 资源管理）打牢，DAG 层再做就只剩"订阅 + 落地"的简单工作**。

### C-A 与已弃路径的对比

| 路径 | 状态 |
|---|---|
| ADR-015 v1（thread.stopped-driven） | ❌ 推翻：ev.Reason 字符串语义模糊，user_stop / crashed 不分 |
| ADR-015 v2（双驱动 + fast-path）| ❌ 推翻：fast-path 物理基础不存在（dag_key/node_key 没注入 prompt）+ 双链路理解错（event_relay 桥）|
| ADR-015 v3（turn.completed-driven）+ ADR-016 | ❌ 删除：ev.Result 在 codex 侧不发，4 轮实证证伪 |
| **C-A**（本计划） | ✅ 采纳 |

---

## 1. 阶段划分总览

| 阶段 | 子项 | ADR | 主要文件 | 工程量 |
|---|---|---|---|---|
| **C1**：codex provider 补完 TurnCompleted.Result | provider 层 | ADR-X1 | `internal/provider/codexapp/session_dispatch.go` + session 累加器 | ~250 行 |
| **C2**：claude provider 长内容完整性核验 + 必要补完 | provider 层 | ADR-X1（共享）| `internal/provider/claudecli/event_map.go` + 端到端测 | ~150-200 行（情况 B；情况 A 仅 0 行）+ ~80-120 行 e2e 基础设施前置 |
| **C3**：codex/claude spawned agent 自动 stop | service 层 | ADR-X4 | 见 §5 拍板（方案 P1 拆 stop_helper.go / P2 并入 A1）| ~120 行 |
| **A1**：DAG subscriber 订阅 TurnCompleted 推进 lifecycle | DAG 层 | ADR-X3 | `dag_turn_completed_subscriber.go` + `hook_consumer.go` fallback + 扩 SQL 白名单 | ~500 行 |
| **A2**：F1.3 outputs 重做（jsonb merge）| DAG 层 | ADR-X5 | `executor_agent.go` + 新 SQL `MergeTaskDagNodeResult` + sqlc 手维 | ~350 行 |
| Phase 4 dogfood | 验收 | — | 10 节点 DAG 端到端 | ~80 行 |

**总计**：**~1530-1620 行 / 5 ADR / 9-13 commit**（v2 reviewer 修订值；详 §9 工程量盘点）。

---

## 2. 阶段 C — provider 层基础设施

### 2.1 C1：codex TurnCompleted.Result 补完

**目标**：codex spawned agent first_turn 完成时，发出的 TurnCompleted 事件 Result 字段携带 agent 真实回复内容。

**当前事实**（实证 #1 已核）：
- `internal/provider/codexapp/event_map.go:171-177` 只填 Success/Error/Status/Reason
- 真实内容在 `TurnOutputDelta` 流式事件中（`stream="message"` + `Delta=stringValue(payload, "delta", "content")`）

**修法方案**（候选）：

#### 方案 C1-a：session 层累加器（推荐）

> **2026-05-12 reviewer 修订**：初稿写"transport 或 session"，独立 reviewer 揭出 `transport.go:26-39` 是单连接复用不绑 turn，**累加器必须挂 session 层**（`session.go:44 activeTurnID` + `session_dispatch.go:103 takeTurn` 才有 per-turn 状态）。且 `session_dispatch.go:140` 把 raw event 直接转 unified dispatcher，TurnOutputDelta 不经过 `s.turns` map — 不是简单加 buffer，而是要插一段 raw event 预处理（sniff `item/agentMessage/delta` 累加 + 在 `turn/completed` 时回填 payload）。

- 修改 `internal/provider/codexapp/session_dispatch.go` 的事件路由：在 raw event 落 unified dispatcher 前 sniff `item/agentMessage/delta`，按 turn-id 累加 `delta` 字段
- TurnCompleted 触发时（`factory.go:156-163 isTurnTerminalEvent`）从 per-session 累加器取结果回填 payload，最终 `event_map.go:171-177` 取出填 `Result`
- 释放：TurnCompleted 之后清空对应 turn-id 的 buffer
- 多 turn 并发安全：以 turn-id 为 map key，防止多 turn 交错
- 优点：累加在 provider 内部，对外接口稳定（TurnCompleted.Result 始终是完整内容）
- 工程量：**~250 行**（含 session_dispatch.go 事件预处理 + per-turn buffer 状态机 + 并发安全 + 单测）

#### 方案 C1-b：service 层累加 + 反查
- 在 mcp-orch service 层新加 turn-content 累加器
- TurnCompleted 接收后从 service 累加器取
- 优点：累加在更高层，可统一 codex/claude 两侧
- 缺点：跨模块依赖，TurnCompleted 事件本身仍不携带内容（语义不干净）
- 工程量：~250 行

**ADR-X1 拍板项**：
1. C1-a 或 C1-b 二选一（推荐 C1-a）
2. **累加器挂载点**：session_dispatch.go 事件预处理路径（不是 transport.go；reviewer 修订）
3. **buffer 内存上限策略**：**provider 层不截断**（让 TurnCompleted.Result 携带完整 N KB），由 A2 在 jsonb merge 时决定 fallback to sharedfile（reviewer 修订：4KB 上限在 provider 层会与 M3 验收硬阈值「单节点 result > 4KB」倒置，蓝图 §3 line 280）。仅保留 hard cap 防 OOM（候选：单 turn 1MB 极限上限，超限 log + fail）
4. 释放策略（TurnCompleted 后立即清 / lazy 清 / TTL）
5. 多 turn 重复 turn-id 的处理（codex turn-id 在同 thread 内单调递增？）

**验收**：
- 单测：mock 一系列 TurnOutputDelta + 一个 TurnCompleted → 断言 Result = 拼接后内容
- 端到端：mcp-orch 真启 codex spawned agent + 跑 first_turn → 观察事件 bus 上 TurnCompleted.Result 非空且匹配预期

### 2.2 C2：claude TurnCompleted.Result 完整性核验

**当前事实**（实证 #2 已核）：
- `claudecli/event_map.go:130` 直接读 raw JSON `result` 字段，**provider 层不截断**
- BUT 依赖 claude CLI 二进制行为 — 长内容（>1KB）可能被 CLI 截断成 preview

**修法路径**：

#### 步骤 0：实测基础设施核对（**ADR-X1 起草前必做**）

> **2026-05-12 reviewer 修订**：独立 reviewer 揭出项目搜不到 `internal/provider/claudecli/*_e2e_test.go` 类基础设施。让 claude spawned agent 真回复 3KB 需要 mock CLI 或真启 claude（依赖环境 + API key + 网络稳定性）。这是**未核就写决策**的隐患重现。

落码前必核：
- `find internal/provider/claudecli/ -name "*_e2e_test.go"` 看是否有 e2e 测试基础设施
- 看 `internal/provider/codexapp/` 是否有 mock CLI 测试可参考
- 如果都没有：**C2 工程量必须扩到"建 e2e 基础设施 + 实测"**，~80-120 行额外
- ADR-X1 拍板项加入：**C2 实测方案**（真启 claude / mock CLI / 跳过 e2e 仅做单测）

#### 步骤 1：实测验证
- 写一个端到端测试：让 claude spawned agent 回复 3KB 内容（要求 agent 输出特定长字符串）
- 抓取 TurnCompleted.Result 看是不是完整 3KB
- 测试位置：`internal/provider/claudecli/turn_complete_e2e_test.go`（新建）

#### 步骤 2（按实测结果分支）：

**情况 A**：claude CLI 不截断 → 端到端测试通过 → C2 完成（0 行代码改动）

**情况 B**：claude CLI 截断 → 需要补 provider 层逻辑
- 复用 C1-a 的**累加器接口语义**（不能复用代码：reviewer 揭出 codex 走 websocket+JSON-RPC，claude 走 CLI stdout JSONL，事件传输层根本不同构）
- claude 累加点：监听 `assistant:message_delta` 事件（`claudecli/event_map.go:103-115`），按 turn-id 累加到 per-session 状态
- 工程量：~150-200 行（含 session 层累加器 + 单测，**比 codex 略多因为需要新建 session 状态机**）

**ADR-X1 包含 claude 部分**：与 codex 同 ADR，因为是同一问题（provider 层 TurnCompleted 内容填充）。

**验收**：
- 单测：mock claude 流式 delta → 断言 TurnCompleted.Result
- 端到端测试：claude spawned agent 长回复 3KB / 8KB / 16KB → Result 完整

### 2.3 C3：codex spawned agent 自动 stop

**当前事实**（实证 #1 揭出）：
- codex CLI 是 stateful 长连接进程
- spawned agent first_turn 完成后**不自动 stop**，CLI 进程保持 idle
- 10 节点 multi-agent-node DAG 跑完会留 10 个挂着的 codex CLI 子进程（资源泄漏）

**API 签名**（reviewer 修订）：
- 真实 API：`service.StopAgent(ctx, agentID)`（`cmd/mcp-orch/orchestration/service.go:414-416`）— **接收 agentID 不是 threadID**
- subscriber 从 `task_dag_nodes.spawning_thread_id` 反查到 **threadID**，需要再做一步 threadID → agentID 反查（codex 路径下两者不一定相等，见 `event_map.go:30 FirstNonEmpty(agentID, payloadThreadID)`）

**修法**：
- 在 A1 subscriber 推进 done 之后：
  1. 从 task_dag_nodes 拿 spawning_thread_id（已有索引）
  2. 通过 thread 表反查 agentID（**ADR-X4 拍板项**：是查 thread 表的 agent_id 列还是其他路径）
  3. 调 `service.StopAgent(ctx, agentID)`
- thread.stopped fallback 路径**不需要**额外 stop（thread 本来就停了）
- ADR-X4 决定的边界：
  - 推进 done 后立即 stop（避免 race）
  - stop 失败不阻塞 done 推进（log warn，与 dispatchAutomation 同策略）
  - 单测覆盖：spawned agent stop 后 thread 进入 `archived` 状态 + child agent 进程退出

**工程量**：~120 行（含 threadID→agentID 反查 + stop 调用 + 单测，**比初稿多 20 行因为多一步反查**）

**ADR-X4 拍板项**：
1. **API 签名澄清**：用 StopAgent(agentID) 接口；threadID → agentID 反查路径明示
2. stop 调用时机（done 写回之前 / 之后 / 同事务）
3. stop 失败兜底（log / metric / 重试）
4. **claude spawned agent 是否需要主动 stop**（reviewer 揭出 §7.1 风险 3 提到但初稿未拍板）— 若需要，C3 工程量再扩 ~50 行做 claude provider 端验证；若 claude 自然退出则保持当前估算
5. **文件归属**：subscriber 推 done 后调 stop 的代码挂在 dag_turn_completed_subscriber.go（与 A1 同文件）还是独立 stop-helper 文件 — 见 §5 并行结构修订

---

## 3. 阶段 A — DAG lifecycle 层（基于 C 完成后才启动）

### 3.1 A1：DAG subscriber 订阅 TurnCompleted

**前置**：C1 + C2 完成（TurnCompleted.Result 真携带 agent 完整回复）+ C3 完成（spawned agent 推进 done 后自动 stop）

**核心**：
1. 新建 `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
2. 通过 `bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted){...})` 订阅
3. 反查 `task_dag_nodes WHERE spawning_thread_id = ev.ThreadID`
4. 命中 + 节点 status ∈ {running, ready}：
   - `ev.Success=true` → 调 store CompleteNodeAndScheduleDownstream，result merge（详 A2）
   - `ev.Success=false` → 调 store FailNodeAndCancelDownstream，result merge ev.Error
   - 推进 done/failed 后调 `service.Stop(threadID)` 释放 codex 子进程（C3）
5. 节点已终态 → 跳过 + metric

**fallback 路径**（thread.stopped）：
- 复用 `cmd/mcp-orch/orchestration/hook_consumer.go handleProcessExitTopic`
- 在 `handleThreadStopped` 之后挂独立闭包（不进 withAgentLocked）
- 反查 spawning_thread_id → 仍 running/ready → FailNodeAndCancelDownstream

**dispatchAgent 状态推进**：
- `node_router.go:dispatchAgent` 在 `r.agentExec.Execute` 返回 Done 后调 `UpdateNodeStatusFlexible(ready → running)`
- 区别"等 dispatcher 拾起"的 ready 和"agent 正在跑"的 running

**race 闭环方案**（reviewer 修订）：

> 初稿提两种修法（新 SQL 或扩白名单）但未拍板 — 这是"未决细节藏在风险段"的反模式。本节强制拍板。

- **采纳方案**：直接**扩 `CompleteTaskDagNode` 白名单为 `IN ('ready', 'running', 'awaiting_verify')`**（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:37-42`）
  - **事实核对**：现状白名单**已经是** `IN ('running', 'awaiting_verify')`，本次改动**只新增 `ready` 一态**（不是从单态扩成三态）— 防止落码 worker 误读为重写整个白名单
  - **对称扩 FailNode 路径**：subscriber `ev.Success=false` 走 `FailNodeAndCancelDownstream` → `FailTaskDagNode` SQL（同文件附近），同样需要核对当前白名单 + 同步加 `ready`，否则失败路径 race 仍会卡 ready；ADR-X3 拍板项加入"FailNode 白名单对称扩"
- **拒绝方案**：新增 `FailReadyOrRunningTaskDagNode` SQL — 多一份手维护负担，与扩白名单效果等价但维度更碎
- **race 场景覆盖**：
  - 正常路径：ready → running（dispatchAgent 写）→ done/failed（subscriber 写）
  - race 路径：spawn 极快 + first_turn 极短 → TurnCompleted 早于 running 写入到达 → 节点仍 ready → SQL 白名单含 ready → 直接 ready→done/failed
- **工程量**：扩白名单 SQL 改动 ~5 行 + sqlc 手维 ~30 行 + race 单测 ~50 行 = **~85 行**（已计入 A1 估算）

**ADR-X3 拍板项**：
1. subscriber 注入哪些端口（store + Dispatcher + StopService）
2. 多 turn 场景：first turn 视为完成（spawned agent 默认单 turn）；second TurnCompleted 反查命中已 done 节点 → skip + log
3. fallback 路径 ev.Reason → NodeStatus 映射（保守：全部映射 failed，避免 user_stop 误判 done）
4. result payload 边界（详 A2）
5. metric 命名规范（对齐项目惯例）

**工程量**：~400 行（含 subscriber + fallback + dispatchAgent 改动 + 单测）

### 3.2 A2：F1.3 outputs 重做（jsonb merge）

**前置**：A1 subscriber 框架就绪

**核心**：
1. **新 SQL**（`cmd/mcp-orch/sql/queries/task_dag_node_merge.sql`，新建）：
   ```sql
   -- name: MergeTaskDagNodeResult :one
   UPDATE task_dag_nodes
   SET result = COALESCE(result, '{}'::jsonb) || $2::jsonb,
       updated_at = NOW()
   WHERE dag_key = $1 AND node_key = $3
   RETURNING ...;
   ```
2. **sqlc 手维**：参照 `cmd/mcp-orch/sqlc.yaml:25-58` 的手维 marker 机制（实证 #3 已核），把生成的 Go binding 加入手维列表
3. **store API**：`NodeFlowStore.MergeNodeResult(ctx, input)` 端口（`store/taskdag/contract.go`）
4. **launch 阶段改动**（`executor_agent.go finalizeAgentOutcome`）：
   - 移除 launch-time `writeAgentSharedfile` 调用
   - 改写 `_handshake` 字段进 node.result（用 MergeNodeResult，不是 overwrite）
5. **turn-completed 落地**（subscriber 内）：
   - merge `{result, summary, completed_at, closed_by}` 进 node.result（_handshake 自然保留）
   - `outputs.to_sharedfile.path` 非空 → 写 sharedfile（subscriber 需注入 SharedFileWriter 端口）
   - cap enforcement：merge 后整体 jsonb 长度 > 4KB → fallback to sharedfile / truncate 标志位

**ADR-X5 拍板项**：
1. SharedFileWriter 端口怎么注入 subscriber（fx 单例注入 / 反查 RunContext）
2. cap 超限处理（validation failure 还是 truncate 还是 fallback）
3. F2.2 automation outputs 是否同步改造（保持语义统一 vs 接受分裂）
4. outputs.to_node_result 字段升级 `bool → {enabled, size_cap_bytes}` 对象（与 ADR-006 §5.2 现行 bool 决议冲突 → 同步修订 ADR-006）
5. 旧 F1.3 单测（`executor_agent_outputs_test.go`）的破坏面（6 个断言全删重写）

**工程量**：~300 行（含 SQL + sqlc 手维 + store API + executor 改动 + subscriber 落地 + 单测重写）

---

## 4. ADR 列表（5 份）

| ADR | 范围 | 阶段 | 状态 |
|---|---|---|---|
| **ADR-X1**（编号 ADR-015 v4，复用编号） | provider 层 TurnCompleted.Result 补完（codex + claude）| C1 + C2 | ⏳ 待立 |
| **ADR-X4**（编号 ADR-016）| codex/claude spawned agent 自动 stop | C3 | ⏳ 待立 |
| **ADR-X3**（编号 ADR-017）| DAG turn.completed subscriber + thread.stopped fallback | A1 | ⏳ 待立 |
| **ADR-X5**（编号 ADR-018）| F1.3 outputs 重做（jsonb merge + turn-completed-time）| A2 | ⏳ 待立 |
| **ADR-006 修订** | to_node_result 字段升对象 `{enabled, size_cap_bytes}` | A2 同步 | ⏳ 待修 |

> 编号说明：ADR-015 / ADR-016 之前的内容已删，编号回收复用。

---

## 5. 依赖关系与并行结构

```
                    C1 (codex Result)
                       │
                       ▼
                    C2 (claude 核验) ──┐
                                        │
                    C3 (auto stop) ────┤
                                        ▼
                                       A1 (subscriber)
                                        │
                                        ▼
                                       A2 (outputs 重做)
```

**并行机会**（reviewer 修订）：

> 初稿把 C3 列在 C 阶段并行项是**错的**：C3 的 stop 调用在 `dag_turn_completed_subscriber.go` 内，该文件由 A1 创建 — C3 worker 写的是 A1 之后才存在的文件。

- **C1 + C2 真并行**（两者修改 codex / claude 两个独立 provider 包）
- **C3 切分方案**（二选一，ADR-X4 拍板）：
  - **方案 P1**：C3 拆出独立 `stop_helper.go`（在 `cmd/mcp-orch/orchestration/`），A1 subscriber 调用 helper — C3 可与 A1 并行
  - **方案 P2**：C3 移到 A1 之后（与 A1 同 worker，作为 A1 工程量的子项）— 工程量不变但失去并行优势
- **A1 + A2 必须串行**（A2 依赖 A1 subscriber 框架 + jsonb merge SQL 改动）

**worktree 并行方案**（采纳用户授权的 worktree 工作流）：
- 波次 1：W-C1 + W-C2 两个 worktree 并行（**C3 视拍板分流**：方案 P1 → W-C3 加入波次 1；方案 P2 → C3 并入波次 2 W-A1）
- 合并审查：1 个 reviewer agent 检查 worktree 的接口一致性
- 波次 2：W-A1 单 worktree（依赖 C 阶段全部 merge；若 C3 走方案 P2 则 A1 工程量扩到 ~520 行）
- 波次 3：W-A2 单 worktree（依赖 A1 merge）

---

## 6. 验收标准（与 M3 验收硬阈值对齐）

### 6.1 阶段 C 验收
- C1：codex spawned agent first_turn 完成 → TurnCompleted.Result 携带完整回复（端到端测）
- C2：claude spawned agent 长回复 3KB+ → TurnCompleted.Result 完整（端到端测）
- C3：spawned agent 推进 done 后 thread 进入 archived 状态 + 子进程退出（端到端测）

### 6.2 阶段 A 验收
- A1：dogfood 一个 2 节点 DAG（agent1 → agent2），上游完成下游能 promote
- A2：dogfood 一个 3 节点 DAG，下游通过 inputs.from_nodes 拿到上游真产出（不是 _handshake 元数据）

### 6.3 Phase 4 — M3 验收硬阈值 dogfood（独立工程量，reviewer 修订）

> 初稿在 §6.2 末尾仅一句"总：≥10 节点 DAG"，无具体方法 + 无工程量盘点。reviewer 揭出 Phase 4 dogfood 真包含 F7.3 prompt_template seed 库验证 + 10 节点 DAG 构造（蓝图 line 263），是独立工程量。

- **F7.3 seed 库前置**：10-15 张 prompt_template seed 必须已 enabled = TRUE（依赖蓝图 F7.3 task，本计划不接，等 F7.3 独立完成）
- **10 节点 DAG 构造**：用 AI 设计师按钮（蓝图 F7.1 ✅ done）+ prompt_template 菜单组装，至少 3 个不同 prompt_template 串联（典型：`source_monitor → topic_curator → email_drafter` 等）
- **单节点 result > 4KB 用例**：选一个 prompt_template seed 让 agent 真回复 > 4KB（验证 C1-a 不截断 + A2 jsonb merge fallback to sharedfile）
- **metric 验证**：跑完后从 metric 端点读 `dispatch_failed_total` / `retry_count_per_node`（F15.1 已 done）
- **工程量**：~80 行（端到端 dogfood 脚本 + 验收 checklist + 失败重跑机制），独立计入

**Phase 4 工程量 ~80 行单列**，不计入 C/A 阶段 ~1530-1620 行（详 §9）。

---

## 7. 风险与未解问题

### 7.1 已知风险
1. **C1 方案 C1-a buffer 内存上限**：单 turn 真有大回复（>4KB）时累加器策略未定 — ADR-X1 拍板
2. **claude CLI 长内容截断**：C2 实测可能揭示需要补 provider 层（情况 B 增 ~100 行）
3. **C3 stop API race**：subscriber 推 done 与 service.Stop 之间的事务边界 — ADR-X4 拍板
4. **A2 ADR-006 修订冲突**：升对象后需要同步改 typed schema + config.go + 现有单测

### 7.2 未解问题
1. **F2.2 automation outputs 是否同步改造**：当前 automation 是 Execute 同步写 outputs（与 F1.3 同），ADR-X5 拍板
2. **多 turn 场景的 result 形状**：second TurnCompleted 到达时 ev.Result 是覆盖还是 append 到 first turn？
3. **dogfood v4 旧卡死节点 backfill**：本计划默认不 backfill（由用户手动重跑或 task_update_node）

---

## 8. 推进流程

### Phase 0：立 ADR-X1（C1+C2 共享）

起草 ADR-X1，回答 §2.1 + §2.2 的拍板项；reviewer 审；用户拍板。

### Phase 1：C1 + C2 + C3 并行落地

启 3 个 worktree worker：
- W-C1：codex provider 累加器（含 ADR-X1 codex 部分）
- W-C2：claude provider 实测 + 必要补完
- W-C3：spawned agent 自动 stop（含 ADR-X4）

合并：1 个 reviewer agent 检查 3 个 worktree 的接口一致性 + 合并冲突。

### Phase 2：立 ADR-X3 + W-A1 落地

起草 ADR-X3；reviewer 审；落 A1 subscriber。

### Phase 3：立 ADR-X5 + W-A2 落地

起草 ADR-X5（含 ADR-006 修订）；reviewer 审；落 A2 outputs 重做。

### Phase 4：端到端 dogfood

跑一个 10 节点 multi-agent-node DAG（用 F7.3 prompt_template seed 库的 morning_briefer / paper_summarizer 等），验证下游 agent 节点 inputs.from_nodes 真拿到上游内容。

---

## 9. 工程量与时间盘点

| 阶段 | 工程量（reviewer 修订）| ADR | commit | 并行度 |
|---|---|---|---|---|
| C1 | ~250 行（session 层累加器）| 1（含 C2）| 1-2 | 与 C2 并行 |
| C2 | ~150-200 行（情况 B：复用接口语义新建 session 累加器） | 同上 | 1-2 | 与 C1 并行 |
| C2 e2e 基础设施前置 | ~80-120 行（若需要新建） | — | 1 | C2 起步前置 |
| C3 | ~120 行（含 threadID→agentID 反查） | 1 | 1-2 | 方案 P1 与 A1 并行 / P2 并入 A1 |
| A1 | ~500 行（含扩白名单 SQL + sqlc 手维 + race 单测） | 1 | 2-3 | 单 worker |
| A2 | ~350 行（含 ADR-006 修订 + jsonb merge SQL + 6 单测重写） | 1（+ ADR-006 修订）| 2-3 | 单 worker |
| Phase 4 dogfood | ~80 行（10 节点 DAG 验收脚本） | — | 1 | 单 worker |
| **合计** | **~1530-1620 行** | **4-5 份** | **9-13 commit** | 跨 4 层 |

> **2026-05-12 reviewer 修订**：初稿估算 ~1050-1150 行 / 7-11 commit。reviewer 指出 F1.x 历史每次估算偏低 30%（F1.5/F1.3 类似规模都超 500 行）。修订后 ~1530-1620 行 / 9-13 commit 更稳。

**关键里程碑**：阶段 C 完成 → DAG layer 改造工程量大幅降低（A 阶段不再需要订阅 TurnOutputDelta 自带累加器，节省 ~200 行）。

---

## 10. 变更记录

- 2026-05-12 v2.2（同步 ADR-015 v4.1 reviewer 二审修订）：
  - §1 总览表：C1 ~250 → ~320-380 行；C2 情况 B ~150-200 → ~220-280 行；C2 情况 A 加单测兜底 ~30 行
  - §9 工程量表：合计情况 A **~1730-1820 行 / 11-15 commit**；情况 B **~1960-2050 行 / 13-17 commit**
  - 文首 + §6.3 工程量数字同步上调
  - 整体上调约 13%——源自 ADR-015 v4.1 落地细化（onNotification 挂载点 + 三处清理 hook + 硬 cap）
- 2026-05-12 v2.1（reviewer 二审修订）：吸收 A2/B2/C2 三份二审 reviewer 反馈：
  - A2-1: §3.1 race 闭环段补"现状白名单已含 awaiting_verify，本次只新增 ready"事实澄清 + FailNode 对称扩白名单（防止失败路径卡 ready）
  - C2-1/2/3: 修复工程量漂移 — 文首 / §1 总览 / §6.3 三处数字统一为 ~1530-1620 行 / 9-13 commit
- 2026-05-12 v2（修订）：吸收 3 份独立 reviewer 反馈。修订 5 处 P0 + 2 处 P1 + 1 处 P2：
  - P0-1: C1-a 累加器挂载点从 transport 层改为 **session 层**（`session_dispatch.go` 事件预处理路径）
  - P0-2: provider 层累加器**不截断**（避免与 M3 size_cap 验收硬阈值倒置，仅保留 1MB 防 OOM 极限上限）
  - P0-3: C2 加**实测基础设施前置核对**（避免"未核就写决策"重现）
  - P0-4: C3 stop API 签名纠正为 `StopAgent(ctx, agentID)`，明示 threadID→agentID 反查路径 + claude 路径拍板
  - P0-5: A1 race 闭环方案拍板为**扩 CompleteTaskDagNode 白名单含 ready**（不新增 SQL）
  - P1-6: C3 与 A1 共用文件冲突 → 拆 stop_helper.go（方案 P1）或并入 A1（方案 P2），ADR-X4 拍板
  - P1-7: Phase 4 dogfood 工程量独立列出（~80 行）+ M3 ≥10 节点验收方法明示
  - P2-8: 工程量估算上调 30%（~1050-1150 → ~1530-1620 行；~7-11 commit → ~9-13 commit）
- 2026-05-12：初稿。基于 F1.x lifecycle 设计审计（4 轮实证 + 跨 4 层缺陷盘点）+ 删除 ADR-015 v3 / ADR-016 后落定 C-A 路径。

