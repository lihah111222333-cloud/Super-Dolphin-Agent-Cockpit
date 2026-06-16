# ADR-016 v1.2：DAG agent 节点完成后 spawned agent 自动 stop（C3）

> 状态：✅ Accepted（v1.2 reviewer 三审通过 + 2026-05-12 实装落地）| 日期：2026-05-12 | 决策者：项目维护者
>
> **实装落地说明**（2026-05-12 W-C3 worker 实装）：4 commit `9ff6059f`/`6e6ad8e2`/`9030a6ca`/`a5345514`。新建 `internal/sidecar/orch/orchestration/stop_helper.go`（`StopSpawnedAgent` 5 条语义契约 / 7 StopResult 分支）+ `stop_metric.go`（`dag_node_stop_spawned_agent_total{result}` atomic.Int64 counter）+ `stop_helper_test.go` 7 分支 10 case 全 PASS + `freeze_registry.go` baseline 33→35。贴近 v1.2 §2.1-§2.6 描述；grep 揭出：dispatcher/metric.go 不存在（是 F15.1 将来新建位），worker 改照 `notify/subscribers.go` 范式。A1 subscriber 调 `StopSpawnedAgent` 大写函数（hard constraint §2.8）未 inline 。
> 相关：C-A 实施计划 §2.3（`docs/plans/dag-lifecycle-c-a-implementation.md`）/ ADR-015 v4.1（C1+C2 TurnCompleted.Result 补完，配套）/ ADR-017（A1 DAG subscriber，调用方）/ F1.x 审计 §8.2 实证 #1（codex CLI 长进程不自动退出）
> 编号说明：ADR-016 编号被废弃的 v1/v2 占用过（从未 git-tracked），本次复用编号为 C-A 路径下的全新决策。

## 1. 背景

C-A 路径 Explorer 实证揭示：

- **codex CLI 是 stateful 长连接进程**（`internal/provider/codexapp/session.go` + `recovery.go`）— spawned agent 完成 first_turn 后**保持 idle**，不自动退出
- **claude CLI 同样不自动退出**（v4.1 Explorer 修正 — `internal/provider/claudecli/session.go:299-330 stop()`）— claude transport 也是 OS 子进程，与 codex 行为对等
- **后果**：10 节点 multi-agent-node DAG 跑完会留下 10 个挂着的 codex / claude 子进程（OS 进程 + 内存 + fd），长跑后 OOM / fd 耗尽

C-A 路径 §2.3 决定：A1 subscriber（ADR-017）推进 DAG 节点 done/failed 后**主动调** `service.StopAgent(ctx, agentID)` 释放 spawned agent。本 ADR 拍板具体接口边界。

## 2. 决策

### 2.1 API 选用 — `service.StopAgent(ctx, agentID)`

签名（`internal/sidecar/orch/orchestration/service.go:414-416`）：
```go
func (s *service) StopAgent(ctx context.Context, agentID string) error {
    return s.stopAgentViaLauncher(ctx, agentID, "user_requested")
}
```

**为什么用 StopAgent 而不是 ArchiveAgent**：
- StopAgent = graceful stop（仅停止进程），适合"DAG 节点完成 → 自动释放运行时进程"语义
- ArchiveAgent（`internal/sidecar/orch/orchestration/archive.go:25`）= stop + 标记 thread/binding archived（UI recycle-bin），是用户主动操作语义
- C3 是后台自动清理，不该出现在 recycle-bin

**Reason 字段**：`stopAgentViaLauncher` 接 reason string 参数。C3 调用方应传 `"dag_node_completed"`（新 reason，与 `"user_requested"` 区分），便于审计和 metric 分类。

### 2.2 threadID → agentID 反查路径（v1.1 修订）

> **v1.1 reviewer 揭出事实层错误**：v1 称"codex 线程总是写 thread.agent_id，命中率 100%"。真相是 `agent_threads` 表**没有** `agent_id` 列（只有 `parent_agent_id`），`PersistedThread.AgentID` 来自 `internal/sidecar/orch/store/agent/store.go:91-103` 的 sqlc 行扫描，**通过 `agent_provider_binding` 左 join 子查询派生**。binding 缺失或 archived 时 AgentID 返空串。

**真实反查路径**：

```go
// A1 subscriber 内的反查流程
spawningThreadID := node.SpawningThreadID  // 从 task_dag_nodes.spawning_thread_id 反查（A1 已做）

persistedThread, err := s.agentThreads.GetByThreadID(ctx, spawningThreadID)
if err != nil {
    return // log warn + metric skipped_lookup_failed
}
if persistedThread == nil || persistedThread.AgentID == "" {
    // 新增分支：thread 存在但 binding 缺失/archive → AgentID 空串
    return // log warn + metric skipped_binding_missing
}
agentID := persistedThread.AgentID
```

API：`AgentThreadStore.GetByThreadID(ctx, threadID) (*PersistedThread, error)`（`internal/sidecar/orch/orchestration/persistent_store_types.go:51-55`）。

**命中率分支**（v1.1 新增）：

| 场景 | AgentID | C3 行为 |
|---|---|---|
| thread + binding 都存在（主路径） | 非空 | 调 StopAgent |
| thread 存在 / binding 未建 | 空串 | metric `skipped_binding_missing` + log warn，不调 stop |
| thread 存在 / binding 已 archive | 空串 | 同上 |
| thread 不存在（已被回收） | API 返 nil | metric `skipped_no_thread_id` + log warn |
| 反查 IPC / DB 失败 | error 返回 | metric `skipped_lookup_failed` + log warn |

**为什么不用 binding 路径直接查**：
- binding 路径需要 provider thread_id（而我们只有 spawning_thread_id）
- 左 join 已是 sqlc 生成 SQL 内部完成，无需调用方再做反查
- 失败模式少：单次查询 + 已有索引

### 2.3 调用时机 — 不同事务，stop 失败不阻塞

A1 subscriber 推进 DAG 节点 done/failed **之后**：

```go
// ADR-017 A1 subscriber 内（伪代码）
if err := flow.CompleteNodeAndScheduleDownstream(ctx, input); err != nil {
    // log warn + metric，不阻塞
    return
}

// done/failed 已写 DB 后，独立调 stop（不在同一事务）
if err := stopSpawnedAgent(ctx, s.agentThreads, s.svc, node.SpawningThreadID); err != nil {
    metrics.IncCounter("dag_node_stop_spawned_agent_failed", ...)
    logger.Warn("dag node done but stop spawned agent failed", ...)
    // 不返回错误 — DAG 节点已 done，stop 失败不影响 DAG 状态
}
```

**为什么不放同一事务**：
- DAG store 是 PG 事务边界，spawned agent stop 走 RPC / 进程信号，跨事务边界
- 同事务会让 PG 锁等待 IPC 完成，无意义阻塞
- DAG 节点 done 是 source of truth；spawned agent 残留是资源问题不是数据正确性问题

### 2.4 StopAgent 幂等性（v1.1 reviewer 修正）

> **v1.1 reviewer 揭出事实层错误**：v1 称"StopAgent 内部应幂等返 nil"。真相是 `service_launcher_bridge.go:354-355 prepareLauncherStop` 内 `agentRunningLocked` 失败返 `fmt.Errorf("agent %q is not running", agent.id)` — **普通错误，非 sentinel**；`helpers.go:196` 本地路径同样返 `"is not running"` 普通错误。`StopAllAgents()` (`service.go:427-430`) 必须显式 `errors.Is(err, errAgentNotFound)` 才忽略，"is not running" 无此宽容。

**C3 调用方必须自己识别 "已退出" 场景**：

```go
err := svc.StopAgent(ctx, agentID)
switch {
case err == nil:
    metric.success += 1
case errors.Is(err, errAgentNotFound):
    metric.skipped_already_archived += 1  // agent 已被回收
case strings.Contains(err.Error(), "is not running"):
    // string-match 兜底（直到 service 层引入 errAgentNotRunning sentinel）
    metric.skipped_already_stopped += 1
case strings.Contains(err.Error(), "is stopping"):
    // v1.2 补充：helpers.go:199 还有此分支（agent 正在 stop 中）— 同样视作幂等
    metric.skipped_already_stopped += 1
default:
    metric.failed += 1
    logger.Warn("stop spawned agent failed", ...)
}
```

**字符串匹配是临时方案**：依赖错误消息文本不稳定。v1.1 建议同步起一个独立任务：在 service 层引入 `errAgentNotRunning` sentinel，让 C3 + 现有 `StopAllAgents` 都能 `errors.Is` 统一识别（不阻塞 C3 落地，可与 ADR-016 同 PR 一起改 helpers.go + service_launcher_bridge.go）。

### 2.5 失败兜底 — log warn + metric，不重试

stop 失败的真实场景：
- spawned agent 已退出 — 走 §2.4 string-match 兜底，视作幂等成功
- 进程僵死 / RPC 超时 — log warn + 由 OS 最终清理（机器重启 / OOM killer）+ 手动 mcp-orch 重启
- StopAgent 内部 panic — recover + log error

**v1.1 reviewer 修正：reclaim cron 兜底虚指**

v1 称"由 OS / reclaim cron 兜底（H6b 监控范围）"。真相：
- 项目 `wakeup_reclaim.go` **仅回收 wakeup lease**（task_dag wakeup 调度记录），**与 spawned agent 进程无关**
- H6b（`docs/plans/dag改造实施计划.md:299`）范围是 **cron miss / run timeout** 监控，**不含** spawned agent 进程残留 reclaim

**v1.1 真相**：**目前没有 spawned agent 进程级 reclaim cron**。失败的真实兜底是：
- OS 进程清理（机器重启 / OOM killer）
- 手动 mcp-orch 重启（重启时 spawned 子进程也会被 wait / kill）

若 stop 失败率显著，需要立独立 ADR 把 spawned agent reclaim 加入 H 阶段（不在 C3 范围）。

**不重试理由**：
- 重试增加复杂度；spawned agent 是非托管资源，DAG 不为它兜底
- DAG 节点 done 后语义自洽，spawned agent 状态对 DAG 透明

**Metric 标签**（v1.1 修订 — 与 F15.1 命名约定对齐 + 删 reason 标签避免恒等于 1 值）：
- `dag_node_stop_spawned_agent_total{result}` 标签值：
  - `success`
  - `skipped_already_stopped`（agent 已退出，string-match 兜底）
  - `skipped_already_archived`（errAgentNotFound）
  - `skipped_no_thread_id`（thread 不存在）
  - `skipped_binding_missing`（thread 存在但 binding 缺失）
  - `skipped_lookup_failed`（反查 IPC / DB 错）
  - `failed`（非幂等错）
- **不加** `{reason}` 标签：C3 只有 `dag_node_completed` 一个 reason，加了恒为 1
- **不加** `{provider}` 标签：双侧同 API 不需要区分；想看分布 join thread 表

**v1.1 reviewer 修正**：v1 称"由 ADR-015 v4.1 / F15.1 metric framework 复用"。真相是项目**没有通用 `metrics.IncCounter` API**，F15.1 是 `internal/sidecar/orch/orchestration/dispatcher/metric.go` 的具体 counter 而非框架。C3 metric 注册需要：
- 决定挂载点：扩 `dispatcher/metric.go` 还是新建 `internal/sidecar/orch/orchestration/stop_metric.go`
- 是 prometheus collector 还是 fx provider 扩展
- ADR-016 §4 工程量需上调 ~30-60 行（不是 ~10 行）

### 2.5 文件归属 — 方案 P1：独立 `stop_helper.go`

依据 Explorer 调研项目现有命名约定（`archive.go` 单职责先例）：

新建 `internal/sidecar/orch/orchestration/stop_helper.go`：
- `StopSpawnedAgent(ctx, agentThreads, svc, threadID) error` 函数封装：threadID 反查 → 拿 agentID → 调 StopAgent
- 与 A1 subscriber 解耦，**C3 可与 A1 并行 worktree 落地**（C-A §5 P1 方案）

**拒绝方案 P2**（并入 A1）：
- 与 archive.go 命名风格不一致
- subscriber 文件会过长（A1 已 ~500 行）
- 失去 C3 独立单测能力

### 2.6 codex + claude 双侧同 API

依据 Explorer 调研事实修正（v4 误判 claude 自然退出）：

- `service.StopAgent` 内部走 `stopAgentViaLauncher`，自动判别本地（codex 直 stopProcess）vs 远端（launcher RPC）路径（`service_launcher_bridge.go:271, 333-346`）
- claude CLI 也是 OS 子进程（`internal/provider/claudecli/session.go:299-330 stop()`），与 codex 同路径
- C3 不需要 provider 分支判别，**单 API 覆盖两侧**

工程量**无需扩**（原估 ~120 行不变）。

## 3. 与上下游边界

### 3.1 与 ADR-015 v4.1（C1+C2）的边界

- C1/C2 改 provider 内部累加器（session 状态），不触发 stop
- C3 在 DAG layer（subscriber 之后）触发 stop
- 不共享代码 / 文件 / mutex；唯一关联是 stop_helper.go 调用 service.StopAgent 时被 stopAgentViaLauncher 路由到 provider 层

### 3.2 与 ADR-017（A1 subscriber）的边界（v1.1 修订 — 语义契约）

- ADR-017 决定 subscriber 整体框架（订阅 TurnCompleted / 反查 spawning_thread_id / 推进 lifecycle）
- ADR-016 决定 subscriber 推进 done 后**这一步**怎么调 stop

**接口语义契约**（v1.1 — 不固定具体函数签名，让 ADR-017 自由选 free function / method）：

ADR-017 实现 stop 调用时，必须满足：

1. **反查路径**：threadID → agentID 通过 `AgentThreadStore.GetByThreadID(ctx, threadID)` 取 `PersistedThread.AgentID`（不允许直接查 binding 表）
2. **stop 调用**：调 `service.StopAgent(ctx, agentID)`（不允许调 ArchiveAgent / 直接调 stopProcess）
3. **失败处理**：失败时 `log.Warn` + metric `dag_node_stop_spawned_agent_total{result="..."}`，**不向 subscriber 抛 error**（subscriber 已是 ResilientSubscribe 异步上下文）
4. **空 agentID 处理**：反查返空串时跳过 stop + metric `skipped_binding_missing`
5. **幂等识别**：errAgentNotFound + "is not running" string-match 两种均视作 skipped_already_*，不计入 failed

具体函数签名（free function `StopSpawnedAgent(ctx, ...)` vs subscriber method `s.stopSpawnedAgent(ctx, threadID)`）由 ADR-017 决定。

### 3.3 与 thread.stopped fallback 路径的边界

- A1 subscriber 的 thread.stopped fallback 路径（hookConsumer 加 dag-aware 分支）**不需要**额外 stop — thread 本来就已经 stopped
- C3 stop_helper.go 只在 turn.completed 主路径触发

## 4. 落地范围（v1.1 工程量大幅上调）

> **v1.1 reviewer 揭出严重低估**：v1 估 ~155 行，对比项目内类似单职责文件 `archive.go` (254 行) + `stop_test.go` (365 行 / 4 case = 90 行/case) + F1.5 历史 commits（f111c12b +255 / edc22076 +502），真实工程量翻倍。

| 改动 | 文件 | v1 估算 | v1.2 修正 |
|---|---|---|---|
| 新建 `stop_helper.go`（StopSpawnedAgent + threadID→agentID 反查 + 6 种错误分类 + log 上下文 + nil 检查 + godoc + 辅助方法封装让 5 分支可测） | `internal/sidecar/orch/orchestration/stop_helper.go` | +60 | **+100-150** |
| service 层引入 `errAgentNotRunning` sentinel + helpers.go / service_launcher_bridge.go 改返 sentinel（与 ADR-016 同 PR，§2.4 v1.1） | `internal/sidecar/orch/orchestration/{helpers,service_launcher_bridge}.go` | 0 | **+20-30** |
| stopAgentViaLauncher 加 `"dag_node_completed"` reason 常量定义 + log/metric 透传 | `internal/sidecar/orch/orchestration/service_launcher_bridge.go:266-286` | +5 | **+5-10** |
| Metric 注册（项目无通用 IncCounter framework，从零自建 collector + label 注册 + fx provider wiring + `/metrics` 端点挂载 + 测试 hook） | 待 §2.5 决策 | +10 | **+80-120** |
| 单测 `stop_helper_test.go`（v1.2: 9 case 全覆盖 — 4 主路径 + 5 反查命中率分支 × ~50-80 行/case，table-driven 折半参照 stop_test.go 范式） | 新建 | +60 | **+300-450** |
| 端到端测：2 节点 DAG + spawned agent stop 验证（含 DAG fixture + provider mock 或真启 + setup / assert / teardown） | 新建或扩 `*_e2e_test.go` | ~20 | **+150-200**（10 节点资源泄漏验证留 M3 dogfood 复用，但 2 节点验证 fixture ~80-100 行 + spawn 触发 + stop 验证 ~80 行） |
| **遗漏项**：fx wiring（metric collector + stop_helper 注入 subscriber 需改 factory.go / service.go）| `internal/sidecar/orch/orchestration/factory.go` 或 `service.go` | 0 | **+10-20** |
| **遗漏项**：ADR-017 接口对接 boilerplate（§3.2 5 条语义契约的接口适配） | `dag_turn_completed_subscriber.go`（由 ADR-017 落地，C3 提供 helper） | 0 | **+15-25** |

**C3 小计**（v1.2 修正 — 吸收 reviewer B2 揭出的 4 处遗漏 + F1.x 30% 漂移规律）：
- **保守估算**：~680 行 / 3 commit
- **激进估算**：~1005 行 / 4 commit
- **建议盘点值**：**~550-700 行 / 3 commit**（取保守值，落地超出时再依实际情况调整）

### 4.1 commit 拆分建议（v1.2 — prefer-small-commits 拆 3 commit）

按 reviewer B2 建议拆 **3 commit**（v1.1 的 2 commit 单 commit 440+ 行违反 prefer-small-commits）：

1. `feat(orch): service 层 errAgentNotRunning sentinel + helpers.go / service_launcher_bridge.go 改造`（约 ~50 行 + 单测）— 独立基础设施改动
2. `feat(orch): stop_helper.go + StopSpawnedAgent + 反查路径 + 9 case 单测`（约 ~400-500 行）— 核心实现
3. `feat(orch/metric): dag_node_stop_spawned_agent_total 计数器 + fx wiring + 2 节点 DAG e2e`（约 ~250-300 行）— metric + 集成测试

reason 字符串常量并入 commit 1（太小不单独成 commit）。fx wiring + ADR-017 interface adapter 并入 commit 3。

### 4.2 与 C-A 实施计划工程量数字同步（v1.2 修订）

> v1.1 揭出 9 处 ADR-X4 占位符 + v2.3 修订；v1.2 二审 reviewer C2 揭出 4 处工程量数字跨文档漂移（v2.1 修过又复发）+ ADR-016 工程量再上调到 ~550-700。

ADR-016 v1.2 落地同 PR 必须修 C-A 计划 → v2.4：
- **C-A line 6 文首**：`~1530-1620 行 / 9-13 commit` → 情况 A `~2160-2400 行 / 12-17 commit`；情况 B `~2390-2630 行 / 14-19 commit`
- **C-A line 305 §6.3**：`~1530-1620 行` → 同上
- **C-A line 143 §2.3**：`~120 行` → `~550-700 行 / 3 commit`
- **C-A §1 总览 C3 行**：`~450-550 行` → `~550-700 行`
- **C-A §9 工程量表 C3 行 + 合计**：同步上调
- **C-A §10 变更记录**：加 v2.4 条目（说明 v1.1→v1.2 二审揭出工程量再上调）

**主实施计划 line 237 F1.3 cell 必须同步**（reviewer C2 揭出跨文档孤立引用）：
- 现状：`~1530-1620 行 / 9-13 commit，v2.1 reviewer 修订值`
- 改为：`情况 A ~2160-2400 行 / 12-17 commit；情况 B ~2390-2630 行 / 14-19 commit，v2.4 ADR-016 v1.2 reviewer 二审三修后值`

**反复复发的跨文档漂移**根因（v2.1 / v2.2 / v2.3 反复揭过）：缺少跨文档同步 checklist。v1.2 同 PR 增加 C-A 计划 §11 新章节固化"跨文档同步 must-check 清单"。

## 5. 验收

### 5.1 单测（stop_helper_test.go）

- happy path: threadID 反查到 agentID → StopAgent 成功 → metric `result="success"`
- threadID 不在 AgentThreadStore（thread 已被回收）: log warn + metric `result="skipped_no_thread_id"`，不返错
- AgentID 空串（binding 缺失/archive，§2.2 v1.1 新增分支）: log warn + metric `result="skipped_binding_missing"`，不调 stop
- 反查 IPC / DB 失败: log warn + metric `result="skipped_lookup_failed"`，不返错
- StopAgent 返 errAgentNotFound（agent 已被回收）: metric `result="skipped_already_archived"`，不返错
- StopAgent 返 "is not running" string-match（agent 已 stop）: metric `result="skipped_already_stopped"`（v1.2 修订：与 §2.5 标签对齐，不再标记为 success）
- StopAgent 返 "is stopping" string-match（agent 正在 stop，helpers.go:199 v1.2 补充分支）: metric `result="skipped_already_stopped"`（与已 stop 合并归类）
- StopAgent 返非幂等错: metric `result="failed"` + log warn，不抛 error 给 subscriber

### 5.2 端到端

跑一个 2 节点 DAG（agent1 → agent2）：
1. agent1 spawn child thread → first_turn 完成 → TurnCompleted 触发 subscriber
2. subscriber 推 agent1 节点 done → 调 StopSpawnedAgent(threadID1)
3. **验证**：codex/claude child thread 进入 archived 状态 + 子进程退出（`ps -ef | grep` 看不到）
4. agent2 promote → spawn → 同样验证 stop

### 5.3 资源泄漏验证（10 节点 DAG）

跑 M3 验收 10 节点 DAG（C-A §6.3）+ ps 监控：
- 节点全 done 后子进程数应为 0（dispatcher 本身除外）
- 内存峰值 < spawned agent 数 × 单 agent 内存（说明 stop 真生效）

## 6. 不做的事

- **不**在 thread.stopped fallback 路径调 stop（thread 已 stopped）
- **不**重试 stop 失败（资源问题，由 OS / 手动 mcp-orch 重启兜底；详 §2.5 v1.1 已删除 reclaim cron 虚指）
- **不**用 ArchiveAgent（语义错位，archive 是用户主动回收）
- **不**做 provider 分支判别（codex/claude 走同 API）
- **不**在 stop 后等待子进程退出确认（同步等会阻塞 subscriber；stop 是 fire-and-forget）

## 7. 开放问题与决策项（v1.1 整理）

### 7.1 已升为决策项（v1.1 — reviewer 揭出原 Q1/Q2/Q4 应在 §2.x 拍板而非 OPEN）

- **D1（原 Q1 实证后升决策）— reason 字段会进 UI 通知**：
  - 实证：`hook_consumer.go:279 agent.stopReason = ev.Reason` + `notify/turn.go:89,109` 把 Reason 拼进通知 body + AgentStopped 事件 Reason 字段
  - **"dag_node_completed" 会作为通知文案直达用户 UI**，不是纯 metadata
  - **决策**：reason 值保持英文 `"dag_node_completed"`（便于 grep / metric 分类）；用户 UI 本地化映射（"DAG 节点完成自动清理"）由独立 task 处理，**不在 ADR-016 范围**
  - 必要时 notify 层加 reason 白名单 skip 用户通知（避免后台清理消息打扰用户），由 ADR-016 落地后单独 task

- **D2（原 Q2 升决策）— metric 不区分 "真 stop" vs "已 stopped"**：
  - §2.5 已明文 metric 标签 `result` 含 `success / skipped_already_stopped / skipped_already_archived` 等
  - 倾向**合并**：C3 不关心 stop 时机，只关心终态（spawned 子进程是否真退出）
  - 已在 §2.4 / §2.5 拍板，无需再 OPEN

- **D3（原 Q3 升决策）— multi-turn 场景下 stop 是 ADR-017 的"first turn = 节点完成"决策的副作用**：
  - ADR-017 决定（待落地）："spawned agent first turn 完成即视为节点完成"
  - C3 在 ADR-017 done 后立即 stop，second turn 若存在会被打断 — 这是 ADR-017 决策的**承诺**："DAG 节点完成 = 立即 stop，承诺单 turn 语义"
  - 若需要 multi-turn 节点，应改 ADR-017 而非 ADR-016（C3 严格按 ADR-017 信号 stop）

- **D4（原 Q4 升决策）— 同步调 stop**：
  - §2.5 已明文"不阻塞 done"+ §6 "不在 stop 后等待子进程退出"
  - subscriber 已是 `ResilientSubscribe` 异步上下文，单 stop 调用阻塞不影响其他 subscriber
  - **决策**：同步调；若实测 stop IPC 慢，加 context.WithTimeout（独立 task，不在 ADR-016 范围）

### 7.2 落码时已闭环

- **Q1（已随 C3 落地闭环）**：A1 subscriber 路径需要的 `errAgentNotRunning` sentinel 改造已与 ADR-016 同批落地，避免 ADR-017 起草窗口继续依赖 string-match 兜底。
- **Q2（已随 C3 落地闭环）**：metric 挂载点采用新建 `stop_metric.go`，与 dispatcher metric 解耦，C3 独立可测。

## 8. 变更记录

- 2026-05-12 v1.2（reviewer 三审修订）：吸收 3 份独立 reviewer 三审反馈（A2 3 处事实层局部一致性 + B2 工程量再上调 + C2 跨文档漂移复发）：
  - **A2-1**：§6 删除"reclaim cron 兜底"残留（与 §2.5 v1.1 修正冲突）
  - **A2-2**：§5.1 单测描述对齐 §2.5 metric 标签（"成功 metric success+=1" 改为 `skipped_already_stopped`）
  - **A2-3**：§2.4 补 `"is stopping"` string-match 分支（helpers.go:199 还有此错误，v1.1 漏覆盖）
  - **B2-1**：§4 工程量 ~450-550 → **~550-700 行**（吸收单测 9 case 全覆盖 + metric 从零自建 collector + fx wiring + ADR-017 接口适配遗漏项）
  - **B2-2**：§4.1 commit 拆 2 → **3 commit**（v1.1 单 commit 440+ 行违反 prefer-small-commits）
  - **C2-1~4**：§4.2 跨文档同步说明 — C-A 计划 line 6 / 305 / 143 + 主实施计划 line 237 F1.3 cell 四处工程量漂移必修
  - **新增 §11 元工作流**：将由 C-A 计划补"跨文档同步 must-check 清单"，固化避免漂移反复复发
- 2026-05-12 v1.1（reviewer 二审修订）：吸收 3 份独立 reviewer 反馈（3 致命事实层 + 5 工程量低估 + 3 跨文档 + 4 设计澄清）：
  - **F-1（致命）**：§2.4 v1.1 修正 — StopAgent 对 "agent X is not running" 返普通错误**非幂等**（`service_launcher_bridge.go:354-355` + `helpers.go:196`），C3 需 string-match 兜底 + 建议同 PR 引入 `errAgentNotRunning` sentinel
  - **F-2（致命）**：§2.2 v1.1 修正 — `AgentThreadStore.GetByThreadID.AgentID` 来自 `agent_provider_binding` **左 join 子查询**（不是 agent_threads 表字段），binding 缺失/archive 时返空串；新增 `skipped_binding_missing` 分支
  - **F-3（致命）**：§2.5 v1.1 修正 — `wakeup_reclaim.go` 是 wakeup lease 回收**不是进程级 reclaim**；H6b 范围是 cron miss / run timeout 不含 spawned agent 残留；删除虚指承认无 reclaim cron
  - **E-1~5（工程量）**：§4 v1.1 上调 ~155 → **~450-550 行 / 2-3 commit**（stop_helper.go 60→90-130；单测 60→200-280；e2e 20→80-150；metric 10→30-60 — 项目无通用 IncCounter framework；service 层 errAgentNotRunning sentinel 改造 +20-30）
  - **D-1/2（跨文档）**：§4.2 v1.1 加 C-A 计划同 PR 同步要求 — 替换 9 处 ADR-X4 → ADR-016 + 工程量 120 → 450-550 + §9 合计上调
  - **D-3（接口契约）**：§3.2 v1.1 改为语义化契约 — 删 4 参数固定签名，明列 5 条 ADR-017 必须满足的语义约束（反查 / stop / 失败处理 / 空 agentID / 幂等识别）
  - **Q-1~4（决策化）**：§7 v1.1 — D1（reason 进 UI 通知）/ D2（metric 合并）/ D3（multi-turn 单 turn 语义）/ D4（同步调）四项升决策项；Q1/Q2 后续已随 v1.2/C3 落码闭环
- 2026-05-12 v1 初稿：基于 C-A 实施计划 §2.3 v2.2 拍板项 + Explorer 单 agent 调研（含 3 处事实层错误 + 工程量低估 50-100%）。
- 编号说明：ADR-016 编号曾被废弃的 lifecycle-thread-stopped-driven ADR 占用过（从未 git-tracked，已删）。本 ADR 是 C-A 路径下 C3 决策的首份记录。
