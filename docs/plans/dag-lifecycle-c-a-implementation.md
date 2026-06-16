# DAG agent 节点 lifecycle 闭环 — C-A 实施计划

> 日期：2026-05-12 | 范围：F1-followup-3 + F1.3-rework 合并 ticket
> 前置：`docs/架构决策/设计审计/F1-lifecycle-audit-2026-05-12.md`（4 轮实证 + 跨 4 层缺陷盘点）
> 路径：**C 阶段（provider 层基础设施）→ A 阶段（DAG lifecycle 层）**
> 总工程量预估（v2.9 Phase 4 dogfood 同步）：情况 A **~3730-4600 行 / 5 ADR / 20-24 commit**；情况 B **~3960-4830 行 / 5 ADR / 22-26 commit**（详 §9 工程量盘点；Phase 4 dogfood 触发 2 个 runtime follow-up + 1 个 harness fix）

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
| **C-A**（本计划） | ✅ 采纳；C1/C2/C3/A1/A2 全部落地（commit `f923ebd7`/`cddb3ea2`/`00864aa7`/`3e70e468` + A2 review-fix `02009e22` 升 ADR 状态）；Phase 4 dogfood 已通过并补 runtime follow-up `b9e8269d` / `ddf1b16f` + harness fix `019cf8a5`；final output 后端 MVP `362be7f0` 已将显式 final node 产物升格到 run metadata，H14 UI 入口 `253e7244` + merge `c772b6ac` 已让 run/detail 与 Shared Files 消费该索引 |

---

## 1. 阶段划分总览

| 阶段 | 子项 | ADR | 主要文件 | 工程量 |
|---|---|---|---|---|
| **C1**：codex provider 补完 TurnCompleted.Result | provider 层 | ADR-015 v4.1 | `internal/provider/codexapp/session_approval.go` + turn output 累加器 + cleanup hooks | ~320-380 行 |
| **C2**：claude provider 长内容完整性核验 + 必要补完 | provider 层 | ADR-015 v4.1（共享）| `internal/provider/claudecli/event_map.go` + 端到端测 | 情况 A 0 行代码 + e2e 脚手架；情况 B ~220-280 行 |
| **C3**：codex/claude spawned agent 自动 stop | service 层 | ADR-016 v1.2 | 拆 stop_helper.go（方案 P1 已采纳；3 commit：sentinel / stop_helper+单测 / metric+e2e）| ~550-700 行（v1.2 reviewer 三审修订）|
| **A1**：DAG subscriber 订阅 TurnCompleted 推进 lifecycle | DAG 层 | ADR-017 v1.2 | `dag_turn_completed_subscriber.go` + `hook_consumer.go` fallback（移出 withAgentLocked）+ 扩 SQL 白名单（CompleteTaskDagNode）+ dispatchAgent ready→running（用 UpdateRunningTaskDagNodeStatus）| ~1790-2270 行（v2.5 reviewer 二审修订；4 处 P0 设计层修正 + 工程量上调 360%）|
| ~~**A2**~~ ✅ done：F1.3 outputs 重做（真实输出物化）| DAG 层 | ADR-018 Accepted（`3e70e468` + review-fix `02009e22`） | `executor_agent.go` + A1 subscriber outputs 落地；复用 `CompleteNodeAndScheduleDownstream` 的 result 更新，sharedfile 路径加 `ClaimTaskDagNodeOutputMaterialization` fence | ~680-780 行（含 review-fix） |
| ~~Phase 4 dogfood~~ ✅ done | 验收 | — | 10 节点 DAG 端到端；hook-delivered turn completion + sharedfile 防覆盖 + runtime hints follow-up 已补 | ~80 行脚本 + runtime follow-up |
| **Final output MVP** ✅ done | 结果入口 | — | `task_dags.metadata.final_node_key` 显式指定最终节点；run 成功终态时把 final node 的 sharedfile/text/json 结果升格到 `task_dag_runs.metadata.final_output`，`task_get_run` 通过既有 Run.Metadata 暴露 | 1 commit `362be7f0` |
| ~~**H14 final output UI**~~ ✅ done | 结果入口 | — | dashboard `dagRuns` RPC + DAG detail final_output 展示 + Shared Files final_output 高亮/筛选；仍以 sharedfile 存 file body，以 `metadata.final_output` 做 run-level index | 1 commit `253e7244` + merge `c772b6ac` |
| **H15 retention/delete guard 首切** ✅ done | 产物保留 | — | dashboard memory payload 增加 `sharedFileRetention` 预览；`ui/memory/shared-file/delete` 拒绝删除被 DAG run `metadata.final_output` 引用的文件；Shared Files 页禁用 final_output 删除按钮。TTL / 批量清理 / pinned / running run 保护继续留 H15 后续 | 1 atomic slice |

**总计**（v2.9 Phase 4 dogfood 同步）：情况 A **~3730-4600 行 / 5 ADR / 20-24 commit**；情况 B **~3960-4830 行 / 5 ADR / 22-26 commit**（详 §9 工程量盘点；Phase 4 runtime follow-up 不回写 C/A 估算行数）。

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

**ADR-015 v4.1 拍板项**：
1. C1-a 或 C1-b 二选一（推荐 C1-a）
2. **累加器挂载点**：`session_approval.go` 的 `onNotification` 入口（在 suppress 之前 sniff；v4.1 reviewer 修订）
3. **buffer 内存上限策略**：**provider 层不做 4KB 业务截断**（让 TurnCompleted.Result 携带完整 N KB），由 A2 按 ADR-006 决定 `node.result` 4KB validation failure 或显式 sharedfile 主路径；仅保留单 turn 1MB hard cap 防 OOM，超限标 `truncated=true`
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

#### 步骤 0：实测基础设施核对（**ADR-015 v4.1 起草前必做，已随 C2 落地闭环**）

> **2026-05-12 reviewer 修订**：独立 reviewer 揭出项目搜不到 `internal/provider/claudecli/*_e2e_test.go` 类基础设施。让 claude spawned agent 真回复 3KB 需要 mock CLI 或真启 claude（依赖环境 + API key + 网络稳定性）。这是**未核就写决策**的隐患重现。

落码前必核：
- `find internal/provider/claudecli/ -name "*_e2e_test.go"` 看是否有 e2e 测试基础设施
- 看 `internal/provider/codexapp/` 是否有 mock CLI 测试可参考
- 如果都没有：**C2 工程量必须扩到"建 e2e 基础设施 + 实测"**，~80-120 行额外
- ADR-015 v4.1 拍板项加入：**C2 实测方案**（真启 claude / mock CLI / 跳过 e2e 仅做单测）

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

**ADR-015 v4.1 包含 claude 部分**：与 codex 同 ADR，因为是同一问题（provider 层 TurnCompleted 内容填充）。

**验收**：
- 单测：mock claude 流式 delta → 断言 TurnCompleted.Result
- 端到端测试：claude spawned agent 长回复 3KB / 8KB / 16KB → Result 完整

### 2.3 C3：codex spawned agent 自动 stop

**当前事实**（实证 #1 揭出）：
- codex CLI 是 stateful 长连接进程
- spawned agent first_turn 完成后**不自动 stop**，CLI 进程保持 idle
- 10 节点 multi-agent-node DAG 跑完会留 10 个挂着的 codex CLI 子进程（资源泄漏）

**API 签名**（reviewer 修订）：
- 真实 API：`service.StopAgent(ctx, agentID)`（`internal/sidecar/orch/orchestration/service.go:414-416`）— **接收 agentID 不是 threadID**
- subscriber 从 `task_dag_nodes.spawning_thread_id` 反查到 **threadID**，需要再做一步 threadID → agentID 反查（codex 路径下两者不一定相等，见 `event_map.go:30 FirstNonEmpty(agentID, payloadThreadID)`）

**修法**：
- 在 A1 subscriber 推进 done 之后：
  1. 从 task_dag_nodes 拿 spawning_thread_id（已有索引）
  2. 通过 thread 表反查 agentID（**ADR-016 拍板项**：是查 thread 表的 agent_id 列还是其他路径）
  3. 调 `service.StopAgent(ctx, agentID)`
- thread.stopped fallback 路径**不需要**额外 stop（thread 本来就停了）
- ADR-016 决定的边界：
  - 推进 done 后立即 stop（避免 race）
  - stop 失败不阻塞 done 推进（log warn，与 dispatchAutomation 同策略）
  - 单测覆盖：spawned agent stop 后 thread 进入 `archived` 状态 + child agent 进程退出

**工程量**：**~550-700 行 / 3 commit**（v2.4 修订 — 详 ADR-016 v1.2 §4：stop_helper.go ~100-150 + errAgentNotRunning sentinel ~20-30 + reason 透传 ~5-10 + metric 从零自建 collector ~80-120 + 9 case 单测 ~300-450 + 2 节点 DAG e2e ~150-200 + fx wiring ~10-20 + ADR-017 接口适配 ~15-25）

**ADR-016 拍板项**：
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
1. 新建 `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`
2. 通过 `bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted){...})` 订阅
3. 反查 `task_dag_nodes WHERE spawning_thread_id = ev.ThreadID`
4. 命中 + 节点 status ∈ {running, ready}：
   - `ev.Success=true` → 调 store CompleteNodeAndScheduleDownstream，result merge（详 A2）
   - `ev.Success=false` → 调 store FailNodeAndCancelDownstream，result merge ev.Error
   - 推进 done/failed 后调 `service.Stop(threadID)` 释放 codex 子进程（C3）
5. 节点已终态 → 跳过 + metric

**fallback 路径**（thread.stopped）：
- 复用 `internal/sidecar/orch/orchestration/hook_consumer.go handleProcessExitTopic`
- 在 `handleThreadStopped` 之后挂独立闭包（不进 withAgentLocked）
- 反查 spawning_thread_id → 仍 running/ready → FailNodeAndCancelDownstream

**dispatchAgent 状态推进**：
- `node_router.go:dispatchAgent` 在 `r.agentExec.Execute` 返回 Done 后调 `UpdateNodeStatusFlexible(ready → running)`
- 区别"等 dispatcher 拾起"的 ready 和"agent 正在跑"的 running

**race 闭环方案**（reviewer 修订）：

> 初稿提两种修法（新 SQL 或扩白名单）但未拍板 — 这是"未决细节藏在风险段"的反模式。本节强制拍板。

- **采纳方案**：直接**扩 `CompleteTaskDagNode` 白名单为 `IN ('ready', 'running', 'awaiting_verify')`**（`internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql:37-42`）
  - **事实核对**：现状白名单**已经是** `IN ('running', 'awaiting_verify')`，本次改动**只新增 `ready` 一态**（不是从单态扩成三态）— 防止落码 worker 误读为重写整个白名单
  - **对称扩 FailNode 路径**：subscriber `ev.Success=false` 走 `FailNodeAndCancelDownstream` → `FailTaskDagNode` SQL（同文件附近），同样需要核对当前白名单 + 同步加 `ready`，否则失败路径 race 仍会卡 ready；ADR-017 拍板项加入"FailNode 白名单对称扩"
- **拒绝方案**：新增 `FailReadyOrRunningTaskDagNode` SQL — 多一份手维护负担，与扩白名单效果等价但维度更碎
- **race 场景覆盖**：
  - 正常路径：ready → running（dispatchAgent 写）→ done/failed（subscriber 写）
  - race 路径：spawn 极快 + first_turn 极短 → TurnCompleted 早于 running 写入到达 → 节点仍 ready → SQL 白名单含 ready → 直接 ready→done/failed
- **工程量**：扩白名单 SQL 改动 ~5 行 + sqlc 手维 ~30 行 + race 单测 ~50 行 = **~85 行**（已计入 A1 估算）

**ADR-017 拍板项**：
1. subscriber 注入哪些端口（store + Dispatcher + StopService）
2. 多 turn 场景：first turn 视为完成（spawned agent 默认单 turn）；second TurnCompleted 反查命中已 done 节点 → skip + log
3. fallback 路径 ev.Reason → NodeStatus 映射（保守：全部映射 failed，避免 user_stop 误判 done）
4. result payload 边界（详 A2）
5. metric 命名规范（对齐项目惯例）

**工程量**：**~1790-2270 行 / 6 commit**（v2.5 修订 — 详 ADR-017 v1.1 §4：subscriber 330-400 + fx wiring 50 + LookupNodesBySpawningThread 80 + SQL 白名单扩 40-60 + dispatchAgent 40 + handleStopped fallback 80 + metric 50-60 + 9 case subscriber 单测 540-720 + 5 case handleStopped 单测 300-400 + e2e 280-380）（含 subscriber + fallback + dispatchAgent 改动 + 单测）

### 3.2 A2：F1.3 outputs 重做（真实输出物化）

**前置**：A1 subscriber 框架就绪

**核心**：
1. **SQL / sqlc 范围收敛**：不做通用 `MergeTaskDagNodeResult`，不新增 migration / schema column；node.result 复用 A1 已调用的 `CompleteNodeAndScheduleDownstream` result 更新路径，sharedfile 路径因外部副作用新增窄 `ClaimTaskDagNodeOutputMaterialization` fence。
2. **launch 阶段改动**（`executor_agent.go finalizeAgentOutcome`）：
   - launch 成功不再把 `{thread_id, agent_key}` 等 launch metadata 作为 outputs 写入 `sharedfile` / `node.result`
   - traceability 继续走既有 `spawning_thread_id`，不把 handshake 当下游业务输出
3. **turn-completed 落地**（subscriber 内）：
   - 基于 `ev.Result` + node `config.outputs` 构造 result payload，再随 `CompleteNodeAndScheduleDownstream` 一次性写入 `node.result`
   - 默认无 outputs 时为兼容写 `node.result`
   - `outputs.to_node_result=true` 写 `node.result`
   - `outputs.to_sharedfile` 显式配置且 `to_node_result=false` 时真实输出写 sharedfile，`node.result` 只保留小 sharedfile 引用 envelope（不重复写大 payload）
   - 两者可同时配置，同一份真实输出同时进入 `node.result` 与 sharedfile
4. **cap enforcement**：沿用 ADR-006。`to_node_result` 超 4KB 是 validation failure，不隐式 fallback 到 sharedfile，不 truncate，不升级字段对象。

**ADR-018 拍板项**：
1. A2 只负责 outputs 物化；A1 只负责 lifecycle/status/stop。
2. `CompleteNodeAndScheduleDownstream` result 参数能否承载 A2 MVP；若代码验证不成立，回 ADR-018 修订后再扩大 SQL/sqlc 范围。
3. `SharedFileWriter` 端口怎么注入 subscriber（fx 单例注入 / 复用现有 sharedfile adapter）。
4. 旧 F1.3 单测改写为验证真实 `ev.Result`，而非 launch metadata。

**明确不做**：automation outputs 改造、agent fast-path/self `task_update_node`、历史 backfill、`outputs.to_node_result` bool 升对象、H12/H13 补测阻塞 A2。

**工程量**：原始 A2 ~200-260 行（executor 移除 launch-time outputs + subscriber 物化 + sharedfile 端口接入 + 单测改写）；review-fix 为 sharedfile materialization claim fence 追加 `02009e22`（482 insertions / 48 deletions）。

---

## 4. ADR 列表（5 份）

| ADR | 范围 | 阶段 | 状态 |
|---|---|---|---|
| **ADR-015 v4.1**（复用编号） | provider 层 TurnCompleted.Result 补完（codex + claude）| C1 + C2 | ✅ Accepted（`f923ebd7`）|
| **ADR-016 v1.2** | codex/claude spawned agent 自动 stop | C3 | ✅ Accepted（`cddb3ea2`）|
| **ADR-017 v1.2** | DAG turn.completed subscriber + thread.stopped fallback | A1 | ✅ Accepted（`00864aa7`）|
| **ADR-018** | F1.3 outputs 重做（agent 真实输出物化）| A2 | ✅ Accepted（`3e70e468` + review-fix `02009e22`） |
| **ADR-006** | to_node_result 4KB cap + bool 字段形态 | A2 同步 | ✅ 沿用现行 Accepted 决议；不升对象，不做 fallback |

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
- **C3 切分方案**（二选一，ADR-016 拍板）：
  - **方案 P1**：C3 拆出独立 `stop_helper.go`（在 `internal/sidecar/orch/orchestration/`），A1 subscriber 调用 helper — C3 可与 A1 并行
  - **方案 P2**：C3 移到 A1 之后（与 A1 同 worker，作为 A1 工程量的子项）— 工程量不变但失去并行优势
- **A1 + A2 必须串行**（A2 依赖 A1 subscriber 框架；MVP 复用 `CompleteNodeAndScheduleDownstream` result 更新，不新增 jsonb merge SQL）

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
- **单节点 result > 4KB 用例**：选一个 prompt_template seed 让 agent 真回复 > 4KB；显式 `to_sharedfile` 且不配置 `to_node_result` 时通过，配置 `to_node_result=true` 时按 ADR-006 validation failure（验证 C1-a 不截断 + A2 不隐式 fallback）
- **metric 验证**：跑完后从 metric 端点读 `dispatch_failed_total` / `retry_count_per_node`（F15.1 已 done）
- **工程量**：~80 行（端到端 dogfood 脚本 + 验收 checklist + 失败重跑机制），独立计入

**Phase 4 工程量 ~80 行单列**，不计入 C/A 阶段 ~3300-4080 行（情况 A）/ ~3530-4310 行（情况 B）（详 §9）。2026-05-13 dogfood 实跑额外揭示并修复 3 个集成缺陷：hook-delivered `turn.completed` 未复用 DAG completion path（`b9e8269d`）、configured sharedfile 已由 agent 写入时被短 summary 覆盖（`b9e8269d`）、agent runtime hints 未完整传到 remote `thread/start`（`ddf1b16f`）。harness 代理绕过与 metric fallback 口径由 `019cf8a5` 收口。

### 6.4 Final output MVP — 用户可收最终产物入口

2026-05-14 后端 MVP `362be7f0` 落地以下边界：
- `sharedfile` 是文件存储与协作空间，也可以承载用户最终产物；问题不在 sharedfile 本身，而在无差别列表会把最终产物淹没在中间产物里。
- DAG 模板通过 `task_dags.metadata.final_node_key` 显式声明最终节点，避免隐式猜测“最后完成的节点”。
- run 成功终态时，`FinalizeTaskDagRunIfAllNodesTerminal` 将 final node 的结果索引到 `task_dag_runs.metadata.final_output`：sharedfile 引用变为 `kind=file/path`，小 JSON 变为 `kind=json/result`，JSON string 变为 `kind=text/text`。
- 缺 `final_node_key`、final node 不存在、失败/取消 run 均保持 run metadata 不变；非 object run metadata 以 `{}` 作为 merge base，避免历史脏数据回滚 finalization。
- H14 UI 已在 DAG detail 与 Shared Files 页面共同使用 `metadata.final_output` 作为“最终产物索引”：file 输出显示 path 并可按现有 shared file get 读取，text/json 直接展示摘要；Shared Files 支持 final_output 高亮/筛选。
- 清理策略另立 H15：未被 final_output 引用的 working/debug sharedfiles 可按 TTL / run 状态清理；final_output 引用文件按用户可收产物保留或走更长 TTL。

---

## 7. 风险与未解问题

### 7.1 已知风险
1. **C1 方案 C1-a buffer 内存上限**：已由 ADR-015 v4.1 拍板为 provider 层不做 4KB 业务截断 + 单 turn 1MB hard cap；后续只剩 H7 summarization / 超长输出体验优化
2. **claude CLI 长内容截断**：C2 实测可能揭示需要补 provider 层（情况 B 增 ~100 行）
3. **C3 stop API race**：subscriber 推 done 与 service.Stop 之间的事务边界 — ADR-016 拍板
4. **A2 sharedfile 端口接入 + fence**：已由 `DAGSubscriberDeps.SharedFileWriter optional` + 现有 sharedfile adapter 接入（`3e70e468`）；review-fix `02009e22` 在 sharedfile 写入前增加 DB claim fence，缺 writer / 写失败走 fail-node reason 前缀

### 7.2 未解问题
1. **F2.2 automation outputs 是否同步改造**：当前 automation 是 Execute 同步写 outputs；ADR-018 明确 A2 不改 automation，后续如需统一另立任务
2. **多 turn 场景的 result 形状**：second TurnCompleted 到达时 ev.Result 是覆盖还是 append 到 first turn？
3. **dogfood v4 旧卡死节点 backfill**：本计划默认不 backfill（由用户手动重跑或 task_update_node）
4. **final output retention / notification**：H14 UI 已让 DAG detail 与 Shared Files 页面基于 final_output 筛选/高亮最终产物；H15 首切已补 final_output 删除保护与 retention 元数据。TTL / 批量清理 / pinned / running run 保护和通知入口仍待后续产品化时确认。

---

## 8. 推进流程

### Phase 0：立 ADR-015 v4.1（C1+C2 共享）

ADR-015 v4.1 已起草、reviewer 二审通过并随 C1/C2 落地，回答 §2.1 + §2.2 的拍板项。

### Phase 1：C1 + C2 + C3 并行落地

启 3 个 worktree worker：
- W-C1：codex provider 累加器（含 ADR-015 v4.1 codex 部分）
- W-C2：claude provider 实测 + 必要补完
- W-C3：spawned agent 自动 stop（含 ADR-016）

合并：1 个 reviewer agent 检查 3 个 worktree 的接口一致性 + 合并冲突。

### Phase 2：立 ADR-017 + W-A1 落地

起草 ADR-017；reviewer 审；落 A1 subscriber。

### Phase 3：立 ADR-018 + W-A2 落地

ADR-018 已升 Accepted（`3e70e468` + review-fix `02009e22`）；A2 outputs 重做已落地并通过 reviewer 复核。

### Phase 4：端到端 dogfood

已跑 10 节点 multi-agent-node DAG（用 F7.3 prompt_template seed 库的 morning_briefer / paper_summarizer 等），下游 agent 节点通过 inputs.from_nodes 拿到上游内容；正向 run 10/10 done，负向 `to_node_result=true` 大结果按 ADR-006 validation failure，metrics 端点读取通过。

---

## 9. 工程量与时间盘点

| 阶段 | 工程量（reviewer 修订）| ADR | commit | 并行度 |
|---|---|---|---|---|
| C1 | ~250 行（session 层累加器）| 1（含 C2）| 1-2 | 与 C2 并行 |
| C2 | ~150-200 行（情况 B：复用接口语义新建 session 累加器） | 同上 | 1-2 | 与 C1 并行 |
| C2 e2e 基础设施前置 | ~80-120 行（若需要新建） | — | 1 | C2 起步前置 |
| C3 | ~550-700 行（含 threadID→agentID 反查 + errAgentNotRunning sentinel + 6 种错误分类（含 is stopping）+ metric 从零自建 collector + 9 case 单测 + 2 节点 DAG e2e + fx wiring + ADR-017 接口适配）| 1 | 3 | 方案 P1 拆 stop_helper.go 与 A1 并行 |
| A1 | ~1790-2270 行（含 subscriber + fallback 锁外 + dispatchAgent 用 UpdateRunningTaskDagNodeStatus + 9 case subscriber 单测 + 5 case handleStopped 单测 + 2 节点 DAG e2e + metric + 扩白名单 SQL + sqlc 手维；v2.5 reviewer 4 处 P0 设计层修正）| 1 | 6 | 单 worker |
| A2 | ~680-780 行（复用 `CompleteNodeAndScheduleDownstream` result 更新；sharedfile materialization claim fence；不新增 migration，不修订 ADR-006） | 1 | 2（`3e70e468` + `02009e22`） | 单 worker |
| Phase 4 dogfood | ~80 行脚本 + dogfood 暴露的 runtime follow-up（hook completion / sharedfile preserve / runtime hints） | — | 3（`b9e8269d` / `ddf1b16f` / `019cf8a5`） | 单 worker + 2 reviewer |
| **合计**（情况 A）| **~3730-4600 行** | **5 份** | **20-24 commit** | 跨 4 层 |
| **合计**（情况 B）| **~3960-4830 行** | **5 份** | **22-26 commit** | 跨 4 层 |

> **2026-05-12 reviewer 修订**：初稿估算 ~1050-1150 行 / 7-11 commit。reviewer 指出 F1.x 历史每次估算偏低 30%（F1.5/F1.3 类似规模都超 500 行）。修订后 ~1530-1620 行 / 9-13 commit 更稳。
>
> **2026-05-12 v2.2 ADR-015 v4.1 reviewer 二审同步**：C1 ~250 → ~320-380 行；C2 情况 B ~150-200 → ~220-280；e2e + 单测兜底 +30。情况 A 合计 ~1730-1820 / 11-15 commit；情况 B 合计 ~1960-2050 / 13-17 commit。
>
> **2026-05-12 v2.3 ADR-016 v1.1 reviewer 二审三修**：揭出 ADR-016 v1 三处事实层错误（StopAgent 非幂等 / AgentThreadStore.GetByThreadID 命中率不是 100% / reclaim cron 兜底虚指）+ 5 处工程量低估（项目无通用 metric IncCounter framework / stop_helper 真实 90-130 行 / 单测 200-280 行）。C3 ~120 → ~450-550 行 / 2-3 commit。情况 A 合计 ~2060-2250 / 12-16 commit；情况 B 合计 ~2290-2480 / 14-18 commit。
>
> **2026-05-12 v2.5 ADR-017 v1.1 reviewer 二审修订**：v1 → v1.1 二审 reviewer 揭出 4 处 P0 设计层错误（§2.4 SQL 选错 UpdateNodeStatusFlexible → UpdateRunningTaskDagNodeStatus / §2.6 漏反向 race Window D / §2.1 fx OnStart ctx 错用导致 subscriber 立即超时空转 / §2.5 withAgentLocked 内做 PG 事务阻塞同 agent 所有 hook 路径）+ 工程量低估（subscriber ~250→330-400 / 单测 ~600→840-1120 / e2e ~180→280-380），commit 拆 4 → 6。A1 ~500 → ~1790-2270 行 / 6 commit（**单 ADR 工程量增 ~290%**）。情况 A 合计 ~2160-2400 → ~3450-4170 / 18-23 commit；情况 B 合计 ~2390-2630 → ~3680-4400 / 20-25 commit。
>
> **2026-05-12 v2.4 ADR-016 v1.2 reviewer 三审修订**：v1.1 → v1.2 二审 reviewer C2 揭出**跨文档工程量数字漂移在 v2.3 又复发**（v2.1 修过一次的 BUG），4 处必修：C-A line 6 文首 + line 305 §6.3 + line 143 §2.3 + 主实施计划 line 237 F1.3 cell。同时 reviewer B2 揭出 v1.1 工程量仍偏低 30%（单测应 9 case 全覆盖 + metric 零基建从零自建 collector + fx wiring + ADR-017 接口适配遗漏项）。C3 ~450-550 → ~550-700 行 / 3 commit（v1.1 拆 2 commit 单 commit 440+ 行违反 prefer-small-commits）。情况 A 合计 ~2160-2400 / 12-17 commit；情况 B 合计 ~2390-2630 / 14-19 commit。**根因预防**：本次同 PR 新增 §11 "跨文档同步 must-check 清单"，固化避免漂移反复复发。
>
> **2026-05-13 v2.6 ADR-018 初稿同步**：A2 改为最小 MVP：不新增 `MergeTaskDagNodeResult`，不改 sqlc，复用 `CompleteNodeAndScheduleDownstream` 的 result 更新；ADR-006 沿用 bool + 4KB validation failure，不升对象、不 fallback。A2 ~350 → ~200-260 行；情况 A 合计 ~3300-4080 / 18-23 commit；情况 B 合计 ~3530-4310 / 20-25 commit。（v2.8 已因 sharedfile 外部副作用补充 claim fence 例外。）
>
> **2026-05-13 v2.7 A2 Accepted 同步**：A2 实装 commit `3e70e468`，ADR-018 升 Accepted；当时口径为实际未新增 SQL/sqlc，单 commit 完成实现 + ADR 初稿。commit 估算下调：情况 A 17-21，情况 B 19-23。（v2.8 已修订为窄 claim fence。）
>
> **2026-05-13 v2.8 A2 review-fix 同步**：review-fix `02009e22` 揭示 sharedfile 写入是 DB 外部副作用，A2 最终新增窄 `ClaimTaskDagNodeOutputMaterialization` SQL/sqlc fence；ADR-018 从“完全不改 SQL/sqlc”修订为“不新增通用 merge/backfill/migration，只加 sharedfile materialization claim fence”。commit 估算上调：情况 A 18-22，情况 B 20-24。
>
> **2026-05-13 v2.9 Phase 4 dogfood 同步**：10 节点 dogfood 实跑通过，同时暴露真实 runtime 集成缺陷并在同轮修复：hook-delivered `turn.completed` 未推进 DAG 节点、configured sharedfile agent 已写入内容被 subscriber 覆盖、provider/model/effort/disabled_tools runtime hints 未完整传到 remote launcher。Phase 4 从 1 个 harness commit 上调为 3 个 follow-up commit（`b9e8269d` / `ddf1b16f` / `019cf8a5`）；情况 A commit 估算 18-22 → 20-24，情况 B 20-24 → 22-26。

**关键里程碑**：阶段 C 完成 → DAG layer 改造工程量大幅降低（A 阶段不再需要订阅 TurnOutputDelta 自带累加器，节省 ~200 行）。

---

## 11. 跨文档同步 must-check 清单（v2.4 新增 — 防漂移元工作流）

> **背景**：v2.1 reviewer C2-1/2/3 揭出过工程量数字跨文档漂移 → v2.1 修订声称"统一"→ v2.2 漏改 line 6 / line 305 → v2.3 又漏改 line 143 + 主实施计划 F1.3 cell → v2.4 ADR-016 v1.2 reviewer C2 第三次揭出**完全相同的漂移**。
>
> 根因：缺少结构化跨文档同步清单。每次"我以为改完了"实际只改了几处显眼位置。**本 checklist 固化所有同步点**，每次修订必走。

### 11.1 工程量数字同步点（C/A 阶段任一份 ADR 修订时必检）

修订 ADR-015 v4.x / ADR-016 v1.x / ADR-017 / ADR-018 任一份后，必须同步以下所有位置：

**ADR-016 v1.x 修订时检查清单**（适用 ADR-015 v4.x 修订时类比）：

```
□ ADR-016 §4 工程量表（C3 小计 + 各分项）
□ C-A 计划 line 6 文首"总工程量预估"
□ C-A 计划 §1 总览表 C3 行（line 33 附近）
□ C-A 计划 §1 总览"总计"行（line 38 附近）
□ C-A 计划 §2.3 工程量描述（line 143 附近）
□ C-A 计划 §6.3 "Phase 4 工程量"段（line 305 附近）
□ C-A 计划 §9 工程量表 C3 行（line 360 附近）
□ C-A 计划 §9 工程量表"合计 A / 合计 B"两行（line 364-365 附近）
□ C-A 计划 §9 修订说明段（line 371 附近，加新版本说明）
□ C-A 计划 §10 变更记录（加新版本条目）
□ 主实施计划 dag改造实施计划.md line 237 F1.3 cell（"详 `docs/plans/dag-lifecycle-c-a-implementation.md`" 后的工程量数字）
```

**11 个同步点**。任一点漏改即跨文档漂移。

**ADR-017 v1.x 修订时检查清单**（v2.5 新增，针对 A1 阶段；与 ADR-016 模板同构）：

```
□ ADR-017 §4 工程量表（A1 小计 + 各分项）
□ C-A 计划 line 6 文首"总工程量预估"
□ C-A 计划 §1 总览表 A1 行（line 34 附近）
□ C-A 计划 §1 总览"总计"行（line 38 附近）
□ C-A 计划 §3.1 A1 章节工程量描述（line 195 附近）
□ C-A 计划 §6.3 "Phase 4 工程量"段（line 305 附近）
□ C-A 计划 §9 工程量表 A1 行（line 360 附近）
□ C-A 计划 §9 工程量表"合计 A / 合计 B"两行
□ C-A 计划 §9 修订说明段（line 371 附近，加新版本说明）
□ C-A 计划 §10 变更记录（加新版本条目）
□ C-A 计划 §11.5 历史教训表加行（v2.5 新增同步点）
□ 主实施计划 dag改造实施计划.md line 237 F1.3 cell
```

**12 个同步点**（含 §11.5 历史表）。

**ADR-018 v1.x 修订时检查清单**（v2.6 新增，针对 A2 阶段；与 ADR-016 / ADR-017 模板同构）：

```
□ ADR-018 §2 决策项（A1/A2 分工、无通用 result merge/backfill/migration，仅 sharedfile materialization claim fence、ADR-006 沿用、明确不做）
□ C-A 计划 line 6 文首"总工程量预估"
□ C-A 计划 §1 总览表 A2 行
□ C-A 计划 §1 总览"总计"行
□ C-A 计划 §3.2 A2 章节核心 / 拍板项 / 工程量
□ C-A 计划 §4 ADR 列表（ADR-X5 → ADR-018；ADR-006 不修订）
□ C-A 计划 §5 依赖关系（A2 串行但不依赖 jsonb merge SQL）
□ C-A 计划 §6.2 / §6.3 验收（真实输出 + >4KB sharedfile/validation 语义）
□ C-A 计划 §7 风险与未解问题（automation/H12/H13 非阻塞）
□ C-A 计划 §9 工程量表 A2 行 + 合计
□ C-A 计划 §10 变更记录
□ C-A 计划 §11.5 历史教训表
□ 主实施计划 dag改造实施计划.md F1.3 cell
□ 主实施计划 dag改造实施计划.md F7.3 dependency cell
□ docs/decisions/README.md Accepted 清单
```

**15 个同步点**。ADR-018 初稿阶段允许用 ☐；代码落地 PR 必须在 commit message 或 PR description 内逐项给出 ✅ 证据。

> **v2.6 元规则补充**：ADR-018 初稿决定 ADR-006 不修订。后续若代码验证推翻该决策，必须先修 ADR-018，再按本清单同步 C-A / 主计划 / README，不能在代码 PR 中静默引入 SQL/sqlc 或字段形态升级。

### 11.2 ADR 编号引用同步点

修订时确认占位编号已实化：

```
□ ADR-015 v4.x 编号在 C-A §1 + §4 ADR 列表 + ADR-016/017/018 配套段都已实化（不是占位编号）
□ ADR-016 编号同上（v1.x 起立时检查，确保 X4 占位已删）
□ ADR-017 起立时 X3 占位应实化
□ ADR-018 起立时 X5 占位应实化
□ ADR-006 保持 Accepted 且不修订；若未来要升对象，必须先修 ADR-018
```

### 11.3 决策项同步点

当 ADR §2.x 拍板新决策时，确保不在 §7 OPEN 仍以"Q-OPEN"形式残留：

```
□ §2.x 新拍板决策不应同时出现在 §7 Q-OPEN（避免 v1.1 反复犯的 Q1/Q2/Q4 已拍板还留 OPEN 形态）
□ §7 OPEN 仅保留真正未决的问题（无既定倾向 / 跨 ADR 协同需要 / 落码前实测才能拍）
□ §5 单测描述与 §2.x metric / status 标签术语对齐（避免 v1.1 §5.1 用 "success" 而 §2.5 用 "skipped_already_stopped" 的术语漂移）
```

### 11.4 元规则

1. **任何 ADR 修订前**：先打印本 §11 checklist 到当前对话，逐项标记 ☐ → ✅
2. **每次 reviewer 反馈中包含"跨文档漂移"问题时**：必须扩充 §11 checklist 增加新的同步点
3. **C-A 计划修订到 v2.x 时**：§11 也要更新最新行号引用（如果文档结构变动）

### 11.5 历史教训表

| 版本 | 揭出问题 | 漏修位置数 | 教训 |
|---|---|---|---|
| v2.1 reviewer C2-1/2/3 | 文首 / §1 总览 / §6.3 工程量数字 | 3 | "我以为改完了"但只改了一处显眼位置 |
| v2.2 修订（ADR-015 v4.1 同步）| 揭出 line 6 / line 305 / §10 变更记录数字不一致 | 2 | 同根因 |
| v2.3 修订（ADR-016 v1.1 同步）| 主实施计划 F1.3 cell 漂移（4ae5b671 v2.2 修过又退化） | 1 | 跨文件同步比同文件难 |
| v2.4 修订（ADR-016 v1.2 同步）| 重新揭出 line 6 / line 305 / line 143 + 主计划 F1.3 cell | 4 | 必须有结构化 checklist |
| v2.5 修订（ADR-017 v1.1 同步）| 4 处 P0 设计层错误（SQL 选型 / fx ctx / 锁内 DB / 漏 race Window）+ 6 处 ADR-X3 残留 + 工程量 290% 上调 | 4+6 = 10 | §11.1 模板必须扩展到每个新 ADR 阶段 |
| **v2.5 → v2.5'（ADR-017 v1.2 反思）** | **§11 checklist 生效但 ADR-017 自身 §4.2 同步段又漂移**（v1 → v1.1 升级时 §4 升了但 §4.2 stale 数据未升）+ ADR 内部矛盾（§2.5 锁外 vs §3.4 锁内） | 5 处 stale 同步 + 1 处内部矛盾 | **真根因揭露：§11 checklist 缺自动化载体，仅靠人工自律不可持续** |
| v2.6 修订（ADR-018 初稿同步）| A2 从 jsonb merge SQL/sqlc 扩面收敛为复用 `CompleteNodeAndScheduleDownstream` result 更新；同步 F1.3/F7.3 依赖口径 | 15 个同步点初稿建账 | A2 MVP 决策必须把"不做"写清，否则很容易回到 SQL/sqlc 扩面 |
| v2.7 修订（A2 Accepted 同步）| ADR-018 升 Accepted + A2 hash `3e70e468` 回填；README 从 Proposed 挪到 Accepted | 15 个同步点闭账 | 先实现 commit，再做 hash 回填 commit，避免自引用 hash |
| v2.8 修订（A2 review-fix 同步）| review-fix `02009e22` 新增 sharedfile materialization claim fence 后，ADR-018 / C-A / 主计划曾残留旧 SQL/sqlc 口径 | 3 组同步点 | 外部副作用 race 修复会改变原 ADR 的 scope 事实，review-fix 后必须回写 ADR/计划 |

五 + 一轮反复证明：**无 checklist = 漂移必复发**。**v2.5 元规则强化**：每个新 ADR 阶段（A1/A2/...）落地必须扩 §11.1 对应模板段。

**v2.5'（ADR-017 v1.2 反思后）更深的根因**：reviewer C 揭出"§11.1 第一次实际应用部分成功（12 同步点 11/12 闭环），但 §11.4 元规则 #1（先打印 checklist 到对话逐项 ☐→✅）未执行，且 ADR-017 自身 §4 vs §4.2 漂移"— 这证明 **结构化 checklist 必要但不充分，缺自动化载体（pre-commit hook 验 ADR PR description 含 ✅ 计数 ≥ N）就仍依赖人工自律**。

**建议下一轮元工作流增强**（不阻塞当前 v2.5 落地，作 H 阶段元工作流改进）：
1. **pre-commit hook**：检测 docs/decisions/ADR-*.md 改动时，强制要求 commit message 含 "✅ §11.1 checklist 12/12" 类标记
2. **CI 检查**：扫 ADR §4.x 同步段，验证版本号（v1.x）与工程量数字与 ADR §4 主体一致（防内部漂移）
3. **新 ADR 模板**：要求 §4.x 同步段引用 §4 锚点而非复制（消除 stale 风险）

立 H 阶段 ticket：`docs(meta): ADR 跨文档一致性自动化（§11 checklist 机制化）` — 不阻塞 C-A 推进。

---

## 10. 变更记录

- 2026-05-14 v3.1（同步 H14 UI 与 UI 决策台账）：
  - **H14 UI 已落地**：DAG detail 与 Shared Files 已消费 `task_dag_runs.metadata.final_output`；file 输出显示 path 并可读取，text/json 展示摘要，Shared Files 支持最终产物高亮/筛选。
  - **UI 决策台账**：新增 `docs/plans/dag-ui-decision-ledger.md`，集中记录 T5/T6/T7/T8/F8/F9/F10/F11/H15 等前端项的已锁边界、待用户拍板项和推荐实现顺序。
- 2026-05-14 v3.0（同步 final output 后端 MVP）：
  - **最终产物入口后端 MVP**：commit `362be7f0` 在 run finalization 同事务内读取 `task_dags.metadata.final_node_key`，把 final node 的 sharedfile/text/json 结果索引到 `task_dag_runs.metadata.final_output`；`task_get_run` 通过既有 Run.Metadata 暴露，不新增 migration/UI。
  - **Shared Files 定位收口**：sharedfile 是文件存储与协作空间，也可承载最终产物；`final_output` 负责标记/索引哪些 sharedfile/text/json 是本次 run 的最终交付物，Shared Files 页面后续应基于该索引高亮最终产物并折叠中间产物。
  - **边界与验证**：缺 key/缺节点/失败 run no-op，非 object run metadata 用 `{}` merge base；`go test ./cmd/mcp-orch/... -count=1`、关键 archtest、临时单 query `sqlc compile` 均通过；全目录 `sqlc compile` 仍受既有 `0083 spawning_thread_id` schema list 缺口阻塞。
- 2026-05-13 v2.9（同步 Phase 4 dogfood 通过 + runtime follow-up）：
  - **Phase 4 dogfood 通过**：10 节点 DAG 正向 run 10/10 done；负向 `to_node_result=true` 大结果按 ADR-006 validation failure；metrics 端点读取通过
  - **dogfood 暴露的 runtime follow-up 已修复**：hook-delivered `turn.completed` 复用 DAG completion path；configured sharedfile 已存在时保留 agent-authored 内容；provider/model/effort/disabled_tools 通过 remote launcher config 传递
  - **harness 收口**：local loopback MCP/metrics URL 绕过环境代理，remote URL 保留代理；metric family check 接受 sample-backed overflow collector
  - **工程量同步**：Phase 4 从 1 个 harness commit 上调为 3 个 follow-up commit（`b9e8269d` / `ddf1b16f` / `019cf8a5`）；总计情况 A 18-22 → 20-24 commit，情况 B 20-24 → 22-26 commit
- 2026-05-13 v2.6（同步 ADR-018 初稿）：
  - **ADR-X5 实化为 ADR-018**：新增 Proposed ADR，明确 A2 负责 agent 真实输出物化，A1 只负责 lifecycle/status/stop
  - **A2 范围收敛（初稿口径）**：不新增 `MergeTaskDagNodeResult`，不改 sqlc；复用 `CompleteNodeAndScheduleDownstream` 的 result 更新（v2.8 已补 sharedfile fence 例外）
  - **ADR-006 沿用**：`outputs.to_node_result` 保持 bool；4KB cap 超限是 validation failure，不 fallback 到 sharedfile
  - **工程量同步**：A2 ~350 → ~200-260 行；总计情况 A ~3300-4080 / 18-23 commit，情况 B ~3530-4310 / 20-25 commit
  - **§11.1 新增 ADR-018 v1.x 修订检查清单**（15 个同步点），覆盖 C-A、主实施计划和 ADR README
- 2026-05-13 v2.7（同步 A2 Accepted）：
  - ADR-018 状态 Proposed → Accepted，实装 commit `3e70e468`
  - C-A §1 / §4 / §9 与主实施计划 F1.3 行回填 A2 落地状态
  - A2 当时按 1 commit 完成记账，合计 commit 估算情况 A 18-23 → 17-21；情况 B 20-25 → 19-23（v2.8 已修订为 2 实现 commit）
- 2026-05-13 v2.8（同步 A2 review-fix）：
  - ADR-018 补记 review-fix `02009e22`，将旧 SQL/sqlc 口径修订为“仅新增 sharedfile materialization claim fence”
  - C-A §1 / §3.2 / §4 / §7 / §9 与主实施计划 F1.3/F1.4 行回填最终 fence 事实
  - A2 实际实现 commit 从 1 → 2，合计 commit 估算情况 A 17-21 → 18-22；情况 B 19-23 → 20-24
- 2026-05-12 v2.5（同步 ADR-017 v1.1 reviewer 二审修订 + §11 新增 ADR-017 v1.x 修订模板）：
  - **跨文档 X3 占位清理**：全文 6 处 ADR-X3 → ADR-017
  - **§11.1 新增 ADR-017 v1.x 修订检查清单**（12 个同步点，含 §11.5 历史表）+ v2.5 元规则补充
  - **工程量大幅上调**：A1 ~500 → ~1790-2270 行 / 6 commit（reviewer 揭出 4 处 P0 设计层修正 + 单测低估）
  - **§1 总览 / §9 工程量表 / 合计**：情况 A ~2160-2400 → ~3450-4170 / 18-23 commit；情况 B ~2390-2630 → ~3680-4400 / 20-25 commit
  - **§9 修订说明**加 v2.5 段
  - **§11.5 历史教训表**加 v2.5 行
- 2026-05-12 v2.4（同步 ADR-016 v1.2 reviewer 三审修订 + 新增 §11 跨文档同步 checklist）：
  - **跨文档漂移修复**：line 6 文首 / line 305 §6.3 / line 143 §2.3 三处工程量数字（v2.3 漏修）+ 主实施计划 line 237 F1.3 cell（commit 4ae5b671 v2.2 修过一次，v2.3 又漏）
  - **工程量再上调**：随 ADR-016 v1.2 工程量从 ~450-550 升到 ~550-700（吸收 9 case 单测全覆盖 + metric 零基建 collector + fx wiring + ADR-017 接口适配）；C3 commit 从 2 拆 3
  - **§1 总览 / §9 工程量表 / 合计**：情况 A ~2060-2250 → ~2160-2400 / 12-17 commit；情况 B ~2290-2480 → ~2390-2630 / 14-19 commit
  - **新增 §11 跨文档同步 must-check 清单**：固化"工程量数字 / 编号引用 / 决策项跨文档"三类检查项，防止 v2.1/v2.2/v2.3 反复复发的漂移问题。每次修订 ADR / C-A / 主计划任一份，必须按 §11 走 checklist。
- 2026-05-12 v2.3（同步 ADR-016 v1.1 reviewer 二审三修）：
  - 全文 9 处 ADR-X4 占位符替换为 ADR-016
  - §1 总览表 C3 行：~120 → ~450-550 行；方案 P1 已采纳
  - §1 总计：情况 A ~1730-1820 → ~2060-2250；情况 B ~1960-2050 → ~2290-2480
  - §9 工程量表 C3 行 + 合计同步上调
  - §9 修订说明加 v1.1 ADR-016 二审说明
  - 整体上调约 19%——源自 ADR-016 v1.1 事实层修正（StopAgent 幂等 / AgentID binding 子查询 / reclaim cron 虚指）+ 工程量低估纠正
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
  - P1-6: C3 与 A1 共用文件冲突 → 拆 stop_helper.go（方案 P1）或并入 A1（方案 P2），ADR-016 拍板
  - P1-7: Phase 4 dogfood 工程量独立列出（~80 行）+ M3 ≥10 节点验收方法明示
  - P2-8: 工程量估算上调 30%（~1050-1150 → ~1530-1620 行；~7-11 commit → ~9-13 commit）
- 2026-05-12：初稿。基于 F1.x lifecycle 设计审计（4 轮实证 + 跨 4 层缺陷盘点）+ 删除 ADR-015 v3 / ADR-016 后落定 C-A 路径。
