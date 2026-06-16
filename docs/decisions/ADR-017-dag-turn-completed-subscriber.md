# ADR-017 v1.2：DAG turn.completed subscriber + thread.stopped fallback（A1）

> 状态：✅ Accepted（v1.2 reviewer 三审通过 + 2026-05-12 实装落地 + reviewer 二审复检通过）| 日期：2026-05-12 | 决策者：项目维护者
>
> **实装落地说明**（2026-05-12 W-A1 worker 实装 + 主 agent 接手补 commit 6/7）：10 commit `94ebdba4`..`71312fd2`。新建 `dag_turn_completed_subscriber.go`（主体 366 行）+ `dag_subscriber_module.go` + 3 metric 文件 + `hook_consumer.go` 锁外 fallback + dispatchAgent ready→running + 14+3 case 单测 + freeze 35→40。贴近 v1.2 §2.1-§2.9 描述。
>
> **3 reviewer 一审 + 二审反馈闭环**：一审揭 1 P0（dispatch_agent_running_test t.Parallel + 包级 counter 10/8 闪挂）+ 2 P1 + 3 P2 + 3 P3，主 agent commit `bcf68488` 一次收摆。二审复检：P0 20/20 PASS，0 新阻塞项，3 项非阻塞 follow-up（§5.2 e2e + §5.3 race C 时序模拟 + ProvideDAGSubscriberAgentThreadLookup nil 哨兵根治）全部记账 H13。
>
> **全量验证**：`go test ./cmd/mcp-orch/... -count=10` + `-race` + `scripts/test_with_guard.sh --guard-only` 全过。
> 相关：C-A 实施计划 §3.1 v2.5（`docs/plans/dag-lifecycle-c-a-implementation.md`）/ ADR-015 v4.1（C1+C2 provider 层 ev.Result 填充）/ ADR-016 v1.2（C3 stop_helper.StopSpawnedAgent 接口契约）/ ADR-018（A2 outputs 重做，下游消费契约）/ F1.x 审计 §1.5 + §1.6

## 1. 背景

C-A 路径阶段 A 的核心目标：**DAG 节点 lifecycle 真闭环**。

C 阶段完成后基础设施就位：
- ADR-015 v4.1：codex/claude `TurnCompleted.Result` 携带 child agent 真实回复（不再是 launch 元数据）
- ADR-016 v1.2：`stop_helper.StopSpawnedAgent(ctx, threadID)` 释放 spawned agent 资源

但 **DAG 节点状态机仍未推进**：
- F1-followup-3 痛点（节点卡 ready）未解决
- `dispatchAgent` 不写 status（与 dispatchAutomation 不对称）
- 没有任何 subscriber 把 TurnCompleted 事件链接到 DAG 节点状态机

ADR-017 立 A1 subscriber + 状态机推进 + thread.stopped fallback。

## 2. 决策（8 项拍板）

### 2.1 订阅范式 — 复用现有 `bus.ResilientSubscribe` + lifecycleCtx 模式（v1.1 修正）

> **v1.1 reviewer 揭出 fx OnStart ctx 使用错误**：v1 代码示例用 OnStart 参数 `ctx` 直接传给订阅 lambda — 这个 ctx 在 OnStart return 后被取消。对照 `service.go:256-294 RegisterTurnLifecycle` 真实范式：必须独立 `lifecycleCtx, lifecycleCancel = context.WithCancel(context.Background())`，否则 subscriber 启动后**立即所有 handler ctx 超时空转**。

```go
// internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go (新建)
func RegisterDAGTurnCompletedSubscriber(
    lc fx.Lifecycle,
    dispatcher *event.Dispatcher,
    deps DAGSubscriberDeps, // fx.In 结构，详 §2.9
    logger *slog.Logger,
) {
    var cancel func() = func() {}
    var (
        lifecycleCtx    context.Context
        lifecycleCancel context.CancelFunc
    )
    lc.Append(fx.Hook{
        OnStart: func(context.Context) error {
            // v1.1 修正：独立 lifecycleCtx，不复用 OnStart ctx（后者 return 即取消）
            lifecycleCtx, lifecycleCancel = context.WithCancel(context.Background())
            cancel = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
                if lifecycleCtx.Err() != nil {
                    return // OnStop 后丢事件
                }
                handleDAGTurnCompleted(lifecycleCtx, deps, logger, ev)
            }, logger)
            return nil
        },
        OnStop: func(context.Context) error {
            if lifecycleCancel != nil {
                lifecycleCancel()
            }
            cancel()
            return nil
        },
    })
}
```

**fx wiring 位置**：`internal/sidecar/orch/notify/module.go:138-151 registerDAGSubscriberLifecycle` 风格 — 新建 `internal/sidecar/orch/orchestration/dag_subscriber_module.go` 或就近 fx.Invoke 注入。

**与现有 TurnCompleted 订阅的关系**（v1.1 补 reviewer A-P1-2 揭出）：

- `service.go:269 RegisterTurnLifecycle` 已订阅 TurnCompleted（推进 agent runtime）
- `hook_consumer.go:297 handleTurnCompleted` 也消费 TurnCompleted（同样推进 agent runtime via handleTurnCompletedEvent）
- A1 新增 RegisterDAGTurnCompletedSubscriber 是**第三路径**

**v1.2 reviewer 已实证（收编结论）**：reviewer A 实地核 `turn_lifecycle.go:22-42 handleTurnCompletedEventWithCtx` 函数体 — 只调 `svc.CompleteTurn` + `svc.forceIdleAfterCompletionError`，**不触碰 DAG store**。三路径并发安全，A1 可放心落码。

具体路径：
- `service.go:269` 订阅 → `handleTurnCompletedEventWithCtx`（推进 agent runtime + svc.CompleteTurn）
- `hook_consumer.go:297 handleTurnCompleted` → 同样调 `handleTurnCompletedEvent`（同语义）
- A1 新增 `RegisterDAGTurnCompletedSubscriber` → 调 `handleDAGTurnCompleted`（推进 DAG store）

三路径分别推进 **agent runtime（前两路）+ DAG store（A1 一路）**，互不重叠，无并发风险。

### 2.2 反查 API — 新建 `LookupNodesBySpawningThread`

Explorer 调研揭示：migration 0083 已建 partial index `idx_task_dag_nodes_spawning_thread_id`，但 store 层**没有对应查询 API**。必须新建。

```go
// store/taskdag/contract.go 新增 narrow port
type NodeSpawningThreadLookup interface {
    LookupNodesBySpawningThread(ctx context.Context, threadID string) ([]Node, error)
}
```

**返回类型**：`[]Node` 不是 `*Node` — **N>1 在重试 / recovery 链下是常态**（v1.1 修正：migration 0083:56-58 partial index 无 UNIQUE 约束 + F1.5 写入端口非 single-writer，spawning_thread_id 可能短暂多挂；详 ADR-009 历史链事件）；逐条尝试推进 + warn log 兜底。

**SQL 实现**（新增到 `internal/sidecar/orch/sql/queries/task_dag_node_read.sql`）：
```sql
-- name: LookupNodesBySpawningThread :many
SELECT ... FROM task_dag_nodes
WHERE spawning_thread_id = $1
  AND spawning_thread_id IS NOT NULL  -- 命中 partial index
ORDER BY updated_at DESC;
```

sqlc 手维 marker 走 `sqlc.yaml:25-58` 现有约定（与 F1.5 spawning_thread_id 写入端口同源）。

### 2.3 SQL 白名单扩 — CompleteTaskDagNode 加 `'ready'`

Explorer 揭示 + ADR-016 reviewer 已核：`task_dag_node_runtime.sql:37-42 CompleteTaskDagNode` 当前 `IN ('running', 'awaiting_verify')`，**需扩为 `IN ('ready', 'running', 'awaiting_verify')`**。

**为什么必须扩**：dispatchAgent 写 running 之前 TurnCompleted 可能已到达（race window — §2.6 详述）；扩白名单是处理 race 的根本手段。

**FailNode 路径**：Explorer 揭示 `failNodeTx`（`store_fail_downstream.go:58-69`）走 `UpdateNodeStatusFlexible` **无前置状态约束** — 不需扩。

**兼容性**：现有 CompleteNode 调用者全是 running 状态过来，扩白名单只允许更多路径进入，不破坏旧调用。

### 2.4 ready→running 推进时机 — `dispatchAgent` 内 Execute 返回后（v1.1 SQL 选型修正）

> 拍板：**选项 C（参照 dispatchAutomation 范式）**
>
> **v1.1 reviewer 揭出 SQL 选型严重错误**：v1 代码示例用 `UpdateNodeStatusFlexible` 写 running — 该 SQL **无前置状态约束**（`store_fail_downstream.go:63` 同 SQL 被 FailNode 复用，可写任意状态）。这意味着若 subscriber 先推 done，dispatchAgent 后到的"写 running"会**反向覆盖 done → running**，节点永久卡 running。
>
> **v1.1 修正**：改用 `UpdateRunningTaskDagNodeStatus`（`task_dag_node_runtime.sql:210-216`，`WHERE status IN ('pending', 'ready')`），done/failed 状态自动被 SQL 拒绝（0 rows affected）。
>
> **v1.2 reviewer 揭出 sqlc 类型错位**：v1.1 代码示例用 `updated == nil` 判 0 rows，但 sqlc 真实签名（`task_dag_node_runtime.sql.go:226`）是 `(TaskDagNode, error)` **不是 `*TaskDagNode`** — 无法与 nil 比较，落码会编译失败。**正解**：通过 `errors.Is(err, pgx.ErrNoRows)` 判 0 rows（sqlc :one + RETURNING + 0 rows 会返 ErrNoRows）。
>
> **v1.2 reviewer 揭出虚构 metric API**：v1.1 代码用 `r.metric.IncDAGNodeRunningSkipped(...)` — 项目无此 helper，是 ADR 自造 API。**v1.2 修正**：暂用通用 `IncCounter(name, labels)` 的接口形态；落码时已按 ADR-016 v1.2 结论接入独立 metric collector。

```go
// internal/sidecar/orch/orchestration/node_router.go:291-296 dispatchAgent 改造
func (r *NodeExecutorRouter) dispatchAgent(ctx, node, runCtx) (NodeOutcome, error) {
    if r.agentExec == nil {
        return validationOutcome("..."), nil
    }
    outcome, err := r.agentExec.Execute(ctx, node, runCtx)
    if err != nil || outcome.Status == NodeStatusFailed {
        return outcome, err // launch 失败：不写 running
    }
    // launch 成功：推 running，让 subscriber 后续推 done
    // v1.2 修正：
    //   1) 用 UpdateRunningTaskDagNodeStatus（白名单 IN ('pending', 'ready')），
    //      不用 UpdateNodeStatusFlexible（无前置约束会反向覆盖 done → running）
    //   2) sqlc 真实签名 (TaskDagNode, error)（不是 *TaskDagNode），0 rows 通过
    //      errors.Is(err, pgx.ErrNoRows) 判断（参 task_dag_node_runtime.sql.go:226）
    _, updateErr := r.runtimeStore.UpdateRunningTaskDagNodeStatus(ctx, RunningStatusUpdate{
        DagKey:  node.DagKey,
        NodeKey: node.NodeKey,
        // 其它字段...
    })
    switch {
    case errors.Is(updateErr, pgx.ErrNoRows):
        // 0 rows affected — 节点已被 subscriber 先推到 done/failed（反向 race Window D，§2.6）
        // 正常路径（不算错），用 metric 区分 DB 错与 race
        // v1.2: metric 注册路径已由 ADR-016 v1.2 闭环，保持通用 collector + label 形态。
        r.metric.IncCounter("dag_node_running_skipped_already_terminal_total",
            map[string]string{"reason": "already_terminal"})
        r.logger.Debug("dispatch agent: node already terminal, skip running write",
            "dag_key", node.DagKey, "node_key", node.NodeKey)
    case updateErr != nil:
        // DB 错误（连接 / 事务）：log warn 不阻塞返回
        r.logger.Warn("dispatch agent: write running failed", "err", updateErr)
    default:
        r.metric.IncCounter("dag_node_running_written_total", nil)
    }
    return outcome, nil
}
```

**初始状态**：节点 enqueue 时仍是 ready（与 pending→ready promote 链路 ADR-009 一致），不改 dispatcher。

**Race 兜底**：
- Window A（subscriber 先到）：CompleteNode 白名单含 ready 兜底（§2.3）
- Window D（dispatchAgent 后写 running）：UpdateRunningTaskDagNodeStatus 白名单拒 done 状态（§2.6 + 上面 0 rows 处理）

### 2.5 thread.stopped fallback 挂载位置 — `handleThreadStopped` 内、`withAgentLocked` **之外**（v1.1 锁外修正）

> **v1.1 reviewer 揭出锁内 DB 风险**：v1 把 DAG 分支放在 `withAgentLocked` 闭包内。但该锁是 **per-agent in-memory mutex**（`factory.go:194-206`），现有 handleThreadStopped 锁内只做 in-memory state 翻转 + 事件投递。在锁内插入 `LookupNodesBySpawningThread` + `FailNodeAndCancelDownstream`（2 次 PG round-trip + 含事务 + 级联 update）会让锁持有从 µs 级跃升到 ms-100ms 级，**同 agent 高频 thread.stopped/turn 事件并发时会序列化阻塞 hookConsumer 所有路径**。
>
> **修正**：DAG 分支移到 `withAgentLocked` 之后（锁释放后同步调），与 agent runtime 推进解耦。

```go
// hook_consumer.go:269-295 handleThreadStopped 改造
func (c *hookConsumer) handleThreadStopped(ctx context.Context, ev threaddto.Stopped) {
    // 锁内：现有 agent runtime 推进（保留，只做 in-memory state + 事件投递）
    err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
        // ... 现有逻辑（保留 hookSyncForceStoppedLocked + publishAgentStopped）
        return nil
    })
    c.logUnexpectedHookError("thread stopped", ev.AgentID, ev.ThreadID, err)

    // === A1 v1.1 新增：DAG fallback 分支（withAgentLocked 之外，避免锁内 DB） ===
    if c.dagStore != nil {
        nodes, err := c.dagStore.LookupNodesBySpawningThread(ctx, ev.ThreadID)
        if err != nil {
            c.logger.Warn("dag fallback: lookup nodes failed",
                "thread_id", ev.ThreadID, "err", err)
            c.metric.IncDAGFallback("skipped_lookup_failed")
            // 不阻塞 notifyTap
        } else {
            for _, n := range nodes {
                // 应用层幂等：已 done/failed 跳过（§2.6 Race C）
                if isTerminalNodeStatus(n.Status) {
                    c.metric.IncDAGFallback("skipped_already_terminal")
                    continue
                }
                // 节点未推进（subscriber 主路径未生效）→ fallback 标 failed
                _, failErr := c.dagFlowStore.FailNodeAndCancelDownstream(ctx, FailNodeInput{
                    DagKey: n.DagKey, NodeKey: n.NodeKey,
                    Reason: "thread_stopped_fallback",
                })
                if failErr != nil {
                    c.logger.Warn("dag fallback: fail node failed",
                        "dag_key", n.DagKey, "node_key", n.NodeKey, "err", failErr)
                    c.metric.IncDAGFallback("failed")
                } else {
                    c.metric.IncDAGFallback("success")
                }
            }
        }
    }

    // 现有 notifyTap 路径（保留）
    if c.notifyTap != nil {
        c.notifyTap.OnThreadStopped(ctx, ev)
    }
}
```

**为什么挂在 handleThreadStopped 内**（v1.1 论证更新）：
- 复用 event_relay 桥（链路 B → 链路 A），不需要再独立订阅 thread.Stopped event bus
- thread state 与 DAG state 的更新顺序：先 agent runtime（锁内）→ 后 DAG（锁外）— DAG 是"反应式"兜底，**不需要**与 thread state 同锁原子
- 与现有 handleThreadStopped 失败兜底（log warn 不抛）语义一致

**为什么不放 withAgentLocked 内**（v1.1 新增）：
- 锁是 in-memory mutex，DB 操作放进去 = mutex hold 时间 = DB round-trip 时长
- 同 agent 高频事件并发（ADR-009 历史链短暂多挂场景）会序列化阻塞所有 hook 路径
- DAG 推进失败不应影响 agent runtime 状态机推进

**接受的边界**（v1.2 新增 — 详 §3.4）：锁内推进 agent runtime + 锁外推进 DAG 的顺序下，中间 crash 会留下短暂不一致状态（runtime stopped 但 DAG 仍 ready）。这是 fallback 的反应式语义可接受代价；下一次 thread.stopped / recovery / 手动 task_update_node 仍会触发兜底，不会永久丢失。

### 2.6 Race 窗口处理（v1.1 补 Window D）

> **v1.1 reviewer 揭出反向 race 漏列**：v1 §2.6 只列正向 race（TurnCompleted 早于 dispatchAgent）。但 §2.4 改用 `UpdateRunningTaskDagNodeStatus` 后，反向 race（subscriber 已推 done → dispatchAgent 后写 running 被 SQL 拒）成为常态路径，必须显式列出处理。

4 处 race 窗口 + 处理策略：

| Window | 场景 | 处理 |
|---|---|---|
| **A**: TurnCompleted 早于 dispatchAgent 写 running | spawned agent first_turn 极快（< dispatchAgent 同步写 running） | CompleteTaskDagNode 白名单含 ready 直接接受 ready→done（§2.3） |
| **B**: agent fast-path 自调 task_update_node | 已推迟 ADR-018 / A2 阶段 | A1 不预留接口；A2 实现时再设计 |
| **C**: TurnCompleted 与 thread.stopped 同时到 | grace stop 或 timeout 期间 | **应用层 + SQL 双层幂等**：subscriber 推 done 前 + fallback 推 failed 前都先检查 `isTerminalNodeStatus(n.Status)`；两条路径先到的赢；SQL 白名单兜底 |
| **D（v1.1 新增）**: subscriber 推 done 后 dispatchAgent 才写 running | spawned agent 极快返回 + dispatchAgent 写 running goroutine 调度延迟到 | `UpdateRunningTaskDagNodeStatus` 白名单 `IN ('pending','ready')` 自动拒（done/failed 不在内）；§2.4 0 rows 分支记 metric `dag_node_running_skipped_already_terminal_total` + Debug log |

**Race C 的 idempotency 实现**：
- 应用层：subscriber 内 `isTerminalNodeStatus(n.Status)` 短路（避免无谓 SQL 调用）
- SQL 层：CompleteTaskDagNode + UpdateRunningTaskDagNodeStatus 双白名单兜底（已 done/failed 状态不在白名单内，SQL 自身返 0 rows）
- Metric：`dag_node_status_idempotent_skipped_total`

### 2.7 result payload 写入 — A1 不拥有输出策略（最终由 ADR-018 定）

依据 Explorer Q7 决策：

- A1 subscriber **只写状态机**（status=done/failed），不写 result payload
- A1 初版仅把 ev.Result 作为 completion 输入透传给 `CompleteNodeAndScheduleDownstream(CompleteNodeInput{Result: ev.Result, ...})`，但不拥有 outputs 物化策略
- ADR-018 / A2 已落定最终形态：不做通用 jsonb merge / `_handshake` / 隐式 fallback 到 sharedfile；基于 `TurnCompleted.Result` + node `config.outputs` 构造 `node.result` / sharedfile 输出，sharedfile 路径加 `ClaimTaskDagNodeOutputMaterialization` fence

**过渡 result 形态**（仅指 A1 单独落地后、A2 落地前）：node.result = ev.Result 字符串（受 ADR-006 4KB cap，超 cap 由 store 层 validation 失败 → A1 metric `dag_node_complete_size_cap_exceeded_total`）。A2 落地后以下游输出形态以 ADR-018 为准。

### 2.8 stop_helper 调用 — done/failed 推进**之后**异步调

依据 ADR-016 v1.2 §3.2 5 条语义契约：

```go
// dag_turn_completed_subscriber.go 内
func handleDAGTurnCompleted(ctx, deps, logger, ev) {
    nodes, _ := deps.LookupStore.LookupNodesBySpawningThread(ctx, ev.ThreadID)
    for _, n := range nodes {
        if isTerminalNodeStatus(n.Status) { continue } // §2.6 race C 兜底

        // 推 done/failed
        var status string
        if ev.Success {
            status = "done"
            // 调 CompleteNodeAndScheduleDownstream
        } else {
            status = "failed"
            // 调 FailNodeAndCancelDownstream
        }

        // ADR-016 stop_helper 调用 — 推进之后、不在同事务
        // v1.1 注释：5 条契约（反查 / stop / 失败处理 / 空 agentID / 幂等识别）由
        //   stop_helper.StopSpawnedAgent 内部封装；A1 subscriber 只透传 threadID，
        //   失败不阻塞遍历下一个 node（契约 4：不向 subscriber 抛 error）
        if err := stopSpawnedAgent(ctx, deps.AgentThreads, deps.SvcStopper, ev.ThreadID); err != nil {
            // log warn + metric — 不阻塞 / 不传给 subscriber 上层 / 不 return（继续下一个 node）
            logger.Warn("stop spawned agent failed", "thread_id", ev.ThreadID, "err", err)
        }
    } // for nodes
}
```

**stop_helper 接口契约**（ADR-016 v1.2 §3.2 5 条语义约束）：
1. 反查路径走 `AgentThreadStore.GetByThreadID`
2. 调 `service.StopAgent(ctx, agentID)`
3. 失败 log.Warn + metric `dag_node_stop_spawned_agent_total{result="..."}`
4. 不向 subscriber 抛 error
5. 6 种错误分类（success / skipped_already_stopped / skipped_already_archived / skipped_binding_missing / skipped_no_thread_id / skipped_lookup_failed / failed）

**调用时机**：DB 推进 done/failed 之后（DB 是 source of truth；stop 失败不影响 DAG 节点状态）。

**Hard constraint（v1.2 reviewer B-P1 强制约束）**：**禁止 inline 实现 stop 调用**，A1 subscriber 必须整体调 `stop_helper.StopSpawnedAgent(ctx, threadID)` — 5 条契约的实现单测在 ADR-016 侧（`stop_helper_test.go`）全覆盖，**ADR-017 单测不再重测契约语义**，只验"调了就不抛 error"。这条约束防止落码人 inline 实现绕过 helper 导致 5 条契约漏 2 条（v1.1 §2.8 仅注释引用约束力不足）。

### 2.9 fx 注入端口 — narrow interface + fx.In 结构

```go
type DAGSubscriberDeps struct {
    fx.In

    LookupStore  taskdag.NodeSpawningThreadLookup   // §2.2 新建
    FlowStore    taskdag.NodeFlowStore              // CompleteNodeAndScheduleDownstream / FailNodeAndCancelDownstream
    AgentThreads agent.AgentThreadStore             // threadID→agentID 反查（ADR-016 复用）
    SvcStopper   StopAgentService                   // service.StopAgent 的窄端口（ADR-016 已 fx.Provide）
    Metric       DAGSubscriberMetric                // optional, fx.In 上加 optional:"true"
}
```

**fx.Provide 新增**（参照 `ProvideNodeSpawnRecorderStore` 范式）：
- `ProvideNodeSpawningThreadLookup` — 把 `*store` type-assert 为 `NodeSpawningThreadLookup`
- 现有 `OrchestrationStore` / `RunStore` / `DispatchNodeStore` 不需改

## 3. 与上下游边界

### 3.1 与 ADR-015 v4.1（C1+C2）的边界
- ADR-015 v4.1 保证 `TurnCompleted.Result` 携带完整内容（codex 累加器 + claude 实测）
- A1 只**消费**ev.Result，不补完 / 不变换形态
- **落码顺序约束**（v1.1 reviewer P1-4 揭出）：**C1+C2 必须先于 A1 落码**（A1 依赖 ev.Result 非空）
- 若 ev.Result 仍空（C1/C2 尚未落码即提前跑 A1），A1 会写空 result + metric `dag_node_complete_result_empty_total` 报警 — 不是 A2 兜底（A2 尚未起草）

### 3.2 与 ADR-016 v1.2（C3 stop_helper）的边界
- A1 落码**依赖** ADR-016 stop_helper.StopSpawnedAgent 已实现
- 调用契约：A1 严格按 ADR-016 §3.2 5 条语义约束调（§2.8 已列）
- 落地顺序：C3 必须先于 A1 完成（A1 落码时 stop_helper.go 必须存在）

### 3.3 与 ADR-018（A2 outputs 重做）的边界
- A1 只写状态机，**不拥有** outputs 物化策略
- A1 单独落码时 node.result 过渡形态 = ev.Result 字符串（A2 落地前的临时状态）
- A2 已由 ADR-018 落定：不做 `jsonb_set(..., '{_handshake}', ...)` 这类通用 merge；真实输出按 `config.outputs` 写 `node.result` / sharedfile，sharedfile 路径先 claim fence
- A1 单测**不验证** result payload 形态 — 只验证 status 推进
- **过渡期约束**（历史）：A1 落地后 ADR-018 起草前，**禁止其他 PR 消费 node.result 字段**；A2 落地后该约束由 ADR-018 的输出合同取代

### 3.4 与 hookConsumer 现有逻辑的边界（v1.2 与 §2.5 锁外修正同步）
- A1 thread.stopped fallback 挂在 `handleThreadStopped:269-295` 函数内，**保留**所有现有 agent runtime 推进逻辑
- **v1.2 关键修正**：新增 DAG 分支在 **`withAgentLocked` 闭包之外**（同步调，锁释放后立即调）— v1.1 §3.4 写"闭包内尾部"是字面残留，与 §2.5 拍板"锁外避免 PG 事务阻塞同 agent 所有 hook 路径"直接矛盾，v1.2 统一改为锁外
- 失败兜底：DAG 分支 log warn 不抛，与现有 hookConsumer 失败语义一致
- **顺序一致性边界（v1.2 新增承认）**：agent runtime state（锁内推进）与 DAG state（锁外推进）的更新顺序是"先 agent runtime → 后 DAG"。极端场景下两者之间 crash 会留下"runtime stopped 但 DAG 仍 ready"的不一致状态 — **接受**这条边界：DAG fallback 是反应式（不是事务保证），下一次 thread.stopped / recovery / 手动 task_update_node 仍会触发兜底；不允许把 DAG 推进进锁内"事务化"（会引入 §2.5 的 PG 阻塞问题）

## 4. 落地范围（按 §11 checklist，v1.1 工程量再上调）

> **v1.1 reviewer 揭出工程量低估**：v1 估 ~1310 行 / 4 commit，与历史 F1.x 30% 偏低规律一致（单测 9 case 用 ~45 行/case 但 A1 case 涉及反查/race/双 store mock/metric assert，实际 60-80 行/case）。同时 commit 3 单 commit ~730 行违反 prefer-small-commits（ADR-016 v1.1 同问题被 reviewer B 揭过）。

| 改动 | 文件 | v1 估 | v1.1 修订 |
|---|---|---|---|
| 新建 subscriber + handler（含 godoc / nil check / type assertion / fx.In 适配 / 6 错误分类 switch） | `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go` | +250 | **+330-400** |
| 新建 fx wiring 模块 | `internal/sidecar/orch/orchestration/dag_subscriber_module.go` | +50 | **+50**（保持）|
| store 新增 `LookupNodesBySpawningThread` API + SQL + sqlc 手维 | `store/taskdag/contract.go` + `sql/queries/task_dag_node_read.sql` + sqlc gen | +80 | **+80**（保持）|
| `CompleteTaskDagNode` SQL 白名单扩 + sqlc regen + **现有调用方 test fixture 同步**（v1.1 新增遗漏项）| `sql/queries/task_dag_node_runtime.sql` + sqlc gen + 多个 test fixture | +20 | **+40-60** |
| `dispatchAgent` 加 ready→running 推进（v1.1 改用 `UpdateRunningTaskDagNodeStatus` + 0 rows 分支） | `internal/sidecar/orch/orchestration/node_router.go:291-296` | +30 | **+40** |
| `handleThreadStopped` 加 DAG fallback 分支（v1.1 移出 withAgentLocked + 多 metric） | `internal/sidecar/orch/orchestration/hook_consumer.go:269-295` | +60 | **+80** |
| Metric 注册（dag_node_status_idempotent_skipped_total / dag_node_running_skipped_already_terminal_total / dag_node_thread_stopped_fallback_total{result=success/failed/skipped_already_terminal/skipped_lookup_failed} 等 8+ 计数器）| 复用 ADR-016 v1.2 metric collector 基础 | +40 | **+50-60** |
| 单测 `dag_turn_completed_subscriber_test.go`（v1.1: 60-80 行/case × 9 case + race A/D 双 case） | 新建 | +400 | **+540-720** |
| 单测 `handle_thread_stopped_dag_test.go`（v1.1: 60-80 行/case × 5 case + mock withAgentLocked / hookConsumer / dagStore / dagFlowStore 4 依赖）| 新建 | +200 | **+300-400** |
| 端到端测：2 节点 DAG 完整流 + fixture + 2 个 mock provider | 新建或扩 e2e | +180 | **+280-380** |

**A1 小计**（v1.1）：**~1790-2270 行 / 6 commit**（v1 估 ~1310 / 4 commit 偏低 ~480-960）

### 4.1 commit 拆分（v1.2 — prefer-small-commits 拆 7 commit）

> **v1.2 reviewer B 揭出 v1.1 commit 4 ~900-1100 行仍违反 prefer-small-commits**（ADR-016 v1.1 同问题被揭过）。v1.1 自称"内容耦合无法再拆"判断不成立 — 单测与实现完全可独立编译（subscriber + fx + metric 落地后，test 文件可用真实接口而非 mock 骨架）。**v1.2 拆 commit 4 → 4a/4b**：

1. `feat(taskdag): LookupNodesBySpawningThread store API + SQL + sqlc + 单测`（约 ~140 行）
2. `feat(taskdag): CompleteTaskDagNode SQL 白名单扩含 ready + sqlc regen + 现有调用方 fixture`（约 ~90-120 行）
3. `feat(orch): dispatchAgent 加 ready→running 推进（UpdateRunningTaskDagNodeStatus）+ 单测`（约 ~80-100 行）
4. **`feat(orch): dag_turn_completed_subscriber.go 骨架 + fx wiring + metric collector 注册`**（约 ~430-520 行 — **v1.2 新拆**）
5. **`test(orch): dag_turn_completed_subscriber 9 case 单测`**（约 ~540-720 行 — **v1.2 新拆**，依赖 commit 4 真实接口）
6. `feat(orch): hookConsumer thread.stopped DAG fallback 分支（withAgentLocked 之外）+ 5 case 单测`（约 ~380-480 行）
7. `test(orch/e2e): 2 节点 DAG spawned agent 完整流 + fixture + provider mock`（约 ~280-380 行）

**所有 commit ≤ 720 行**符合 prefer-small-commits。commit 4 (实现) + commit 5 (测试) 拆开后 commit 5 可独立 review（不混在 subscriber 骨架审查里）。

### 4.2 与 C-A v2.5 + 主实施计划同步（v1.2 修订）

按 §11.1 ADR-017 v1.x 模板 12 个同步点逐项验证（v1.2 落地 PR 必须修；v1.1 commit 时已完成大部分，本次 v1.2 修订 ✅ 标记如下）：

```
✅ ADR-017 §4 工程量表（v1.2 修订 ~1790-2270 行 / 7 commit）
✅ C-A 计划 line 6 文首"总工程量预估"（C-A v2.5 已升 情况 A ~3450-4170 / B ~3680-4400）
✅ C-A 计划 §1 总览表 A1 行（编号 X3 → ADR-017 v1.1，工程量 ~500 → ~1790-2270）
✅ C-A 计划 §1 总览"总计"行（情况 A/B 上调 ~810）
✅ C-A 计划 §3.1 A1 章节工程量描述（~1790-2270 / 6 commit ↓ 后续修 7 commit）
✅ C-A 计划 §6.3 "Phase 4 工程量"段（不计入 C/A 数字同步）
✅ C-A 计划 §9 工程量表 A1 行（~1790-2270 / 6 ↓ v1.2 改 7）
✅ C-A 计划 §9 工程量表"合计 A / 合计 B"两行
✅ C-A 计划 §9 修订说明段（v2.5 段已加）
✅ C-A 计划 §10 变更记录（v2.5 条目已加）
✅ C-A 计划 §11.5 历史教训表（v2.5 行已加）
✅ 主实施计划 dag改造实施计划.md line 237 F1.3 cell
```

**12/12 同步点全部完成**（含 §11.5 — v1.1 §4.2 漏列第 12 项 + 5 处 stale 数据已 v1.2 修正）。

**实际总计**（v1.2 reviewer 二审后 — 用 ADR-017 v1.1 估算）：
- C-A v2.4 总计：情况 A ~2160-2400 / 情况 B ~2390-2630
- ADR-017 A1 上调：~500 → ~1790-2270（+~1290-1770）
- **C-A v2.5 实际**：情况 A **~3450-4170** / 情况 B **~3680-4400**

**§11.5 历史教训表 v2.5 行**：
| v2.5 修订（ADR-017 v1.1）| A1 ~500 → ~1790-2270 行；情况 A 合计 ~3450-4170 |

**v1.2 元工作流改进备忘**（reviewer C P3 揭出）：本次 v1.1 修订时 §4 工程量升 v1.1 但 §4.2 同步段仍是 v1 stale — **ADR 自身内部漂移**，证明 §11 checklist 仅"逐项标记"还不够，缺**自动化载体**（pre-commit hook 验 ADR PR description 含 ✅ 计数 ≥ 12）。这条根因记入 C-A v2.5 §11.5 v2.5 行的反思部分。

## 5. 验收

### 5.1 单测覆盖（共 14 case 跨 2 测试文件）

**dag_turn_completed_subscriber_test.go** (9 case)：
1. happy path - done：spawn 后 TurnCompleted.Success=true → CompleteNodeAndScheduleDownstream → 节点 done + 下游 schedule
2. happy path - failed：TurnCompleted.Success=false → FailNodeAndCancelDownstream → 节点 failed + 下游 cancel
3. race A - TurnCompleted 早于 running：节点仍 ready，CompleteNode 白名单扩接受 ready→done
4. race C - 节点已 failed（fallback 先到）：subscriber 跳过 + metric idempotent_skipped
5. 反查空：threadID 不在 task_dag_nodes → log warn + metric skipped_no_node
6. 反查 N>1（脏数据）：每条都尝试推进 + metric dirty_data
7. 幂等 - 节点已 done（重复 TurnCompleted）：跳过 + metric idempotent_skipped
8. stop_helper 失败：done 推进成功 + stop 失败仅 log warn（不阻塞）
9. CompleteNode 超 4KB cap：返 validation error + metric size_cap_exceeded

**handle_thread_stopped_dag_test.go** (5 case)：
1. fallback 触发：thread.stopped 到达 + 节点 ready → FailNode + 下游 cancel
2. 节点已 done（subscriber 先到）：fallback 跳过 + metric idempotent_skipped
3. 反查失败：DB 错 → log warn + agent runtime 推进不受影响
4. DAG FailNode 失败：log warn + agent runtime publishAgentStopped 仍执行
5. 与 agent runtime 推进互不影响：双路径都执行 + 不互相阻塞

### 5.2 端到端

跑一个 2 节点 DAG（agent1 → agent2）：
1. agent1 spawn child thread → first_turn 完成 → TurnCompleted (Success=true, Result=...)
2. **验证**：node1.status=done + node1.result=Result + node2 status promote 到 ready
3. agent2 spawn → 同样流程 → node2.status=done
4. 完整链路在 < 3 秒内完成（无卡 ready）

### 5.3 race window 模拟（落码时手测）

- Window A：mock dispatchAgent 故意 sleep 100ms 后写 running，TurnCompleted 在 50ms 时到 → 验证 ready→done 路径
- Window C：mock TurnCompleted + thread.stopped 在 50ms 内串行到达 → 验证幂等只推一次

## 6. 不做的事

- **不**拥有 result payload 物化策略（由 ADR-018 处理；最终不做通用 jsonb merge / `_handshake`）
- **不**支持 agent fast-path（ADR-018 已明确 A2 也不做；未来如需另立任务）
- **不**在 subscriber 内调 stop_helper 同步阻塞等待（同步调但失败不抛）
- **不**改 dispatcher 调度逻辑（pending→ready promote 已在 F6.3 done）
- **不**改 service.go RegisterTurnLifecycle 现有订阅（A1 平行新增，不破坏 agent runtime 推进）

## 7. 开放问题

### 7.1 落码前需协同决策

- ~~Q1（已删 v1.1）~~：原 Q1 "Race C 检查位置" 与 §2.6 拍板"应用层 + SQL 双层兜底"双出违反 §11.3 元规则，删除
- **Q1**（原 Q2）：subscriber 内 stop_helper 调用是否需要 context.WithTimeout 防 IPC 慢？ADR-016 已决定同步调；若实测慢可加 timeout（独立 task）

### 7.2 A2（ADR-018）已落定后的边界

- **Q3（已由 ADR-018 关闭）**：fast-path / agent 自调 `task_update_node` 不纳入 A2；未来如需另立任务。
- **Q4（已由 ADR-018 关闭）**：node.result 最终形态由 A2 outputs 物化规则决定；默认写真实 `ev.Result`，sharedfile-only 时 `node.result` 只保留小引用 envelope，同时沿用 ADR-006 4KB cap。

## 8. 变更记录

- 2026-05-12 v1.2（reviewer 三审修订）：吸收 3 份独立 reviewer 反馈（11 处 must-fix：2 编译失败级 + 1 ADR 内部矛盾 + 5 §11 checklist 自身漂移 + 2 commit/约束 + 1 利好收编）：
  - **A-P0-1 新隐患 #1**（编译失败）：§2.4 代码示例改用 `errors.Is(err, pgx.ErrNoRows)` 判 0 rows（sqlc 真实签名 `(TaskDagNode, error)` 非 `*TaskDagNode`）
  - **A-P0-1 新隐患 #2**（虚构 API）：§2.4 metric 用通用 `IncCounter(name, labels)` 替代不存在的 `IncDAGNodeRunningSkipped` helper；落码时按 ADR-016 §7 Q2 决策再调整
  - **A-P0-4 §3.4 矛盾修正**：v1.1 §3.4 残留"DAG 分支在 withAgentLocked 闭包内尾部"与 §2.5 拍板"锁外"直接矛盾；v1.2 统一改为锁外 + 补承认"runtime/DAG crash 中间态不一致"边界
  - **A-P1-2 收编实证**：reviewer A 实地核 `turn_lifecycle.go:22-42 handleTurnCompletedEventWithCtx` 不触 DAG store；v1.2 §2.1 末尾从"落码前必核"升为"已实证收编结论"
  - **B-P0-commit**：§4.1 commit 4 拆 4a/4b → **7 commit**（commit 4 ~430-520 subscriber+fx+metric，commit 5 ~540-720 9 case 单测，独立 review）
  - **B-P1 §2.8 hard constraint**：禁止 inline 实现 stop 调用，必须整体调 helper；防止落码人绕过 helper 漏 2 条契约
  - **C-1~5 §11 checklist 自身漂移修正**：v1.1 §4 工程量升 v1.1 但 §4.2 同步段忘升（line 4 v2.4 / §4.2 标题 v2.4 / 11 个同步点 / stale ~2970-3210 / stale ~500→~1310 全部 v1.2 升 v1.1+v2.5 真实数字）+ ☐ → ✅ 12 项标记真证据
- 2026-05-12 v1.1（reviewer 二审修订）：吸收 3 份独立 reviewer 反馈（A 4 处 P0 设计层 + B 工程量上调 + C 跨文档）：
  - **A-P0-1**：§2.4 SQL 选型修正 — `UpdateNodeStatusFlexible` 无前置约束会反向覆盖 done→running；改用 `UpdateRunningTaskDagNodeStatus`（白名单 IN ('pending','ready')）+ 0 rows 分支处理（Window D）
  - **A-P0-2**：§2.6 Race 表补 Window D 反向 race（subscriber 推 done 后 dispatchAgent 才写 running）
  - **A-P0-3**：§2.1 fx OnStart ctx 改用 `lifecycleCtx = context.WithCancel(context.Background())` 范式（参照 service.go:256-294），避免 OnStart return 后 ctx 立即取消导致 subscriber 空转
  - **A-P0-4**：§2.5 DAG 分支移出 `withAgentLocked` — in-memory mutex 内做 PG 事务会让锁持有从 µs 跃升到 ms-100ms 级，阻塞同 agent 所有 hook 路径
  - **A-P1-2**：§2.1 补"三路径并发"警示（service.go:269 + hook_consumer.go:297 + A1 新订阅）+ 落码前必核 handleTurnCompletedEventWithCtx 不触碰 DAG
  - **B-P0**：§4 工程量 ~1310 → ~1790-2270 行（subscriber 250→330-400 / 单测 9 case 400→540-720 / handleStopped 单测 5 case 200→300-400 / e2e 180→280-380），commit 拆 4 → **6 commit**
  - **C-P0-3**：§7.1 Q1 删除（与 §2.6 拍板"应用层 + SQL 双层兜底"双出违反 §11.3）
  - **P1-1**：§2.2 N>1 表述改为"多挂常态"（partial index 无 UNIQUE + F1.5 写入端口非 single-writer）
  - **P3-1**：§3.1 补"C1+C2 必须先于 A1 落码"显式约束
  - **P3-2**：§3.3 删"< 1 周"假设，改为约束条款"禁止其他 PR 消费 node.result 字段"
  - **P2**：§2.8 加注释明示 5 条契约由 helper 封装、stop 失败不阻塞遍历下一个 node
- 2026-05-12 v1 初稿：基于 C-A 实施计划 §3.1 v2.4 + Explorer 调研结果（9 项事实 + 8 项必须拍板）。
