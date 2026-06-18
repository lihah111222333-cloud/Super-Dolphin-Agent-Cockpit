# ADR 0001: DAG v2 骨架阶段契约

> 状态：Accepted（骨架阶段固化）
> 日期：2026-05-10
> 关联：`docs/plans/dag改造蓝图v2.md` / `docs/plans/dag改造实施计划.md`
> 作用：骨架阶段所有 typed 契约的 single source of truth。
> 修改：契约变更必须通过新 ADR（0002+）补充或替换；不要原地改本文。

---

## 1. 背景

DAG v2 改造把"自动化任务"收敛为 DAG 一种节点类型，统一抽象成"动态可重写 DAG 运行时"。骨架阶段（S 阶段）目标是**只搭抽象、不改行为**，给 T/F 阶段稳定的接口表面。

骨架阶段产出 14 处补丁，全是 typed schema / 接口位 / Go enum，分布在两个 Go 包：
- `cmd/mcp-orch/orchestration/nodeexec/` — 执行层抽象（NodeExecutor / 三 stub / typed ops / typed config / 状态机校验 / 失败策略 lookup）
- `cmd/mcp-orch/orchestration/` — 编排层抽象（service.StartDAG/TerminateDAG/ApplyOps stub / Scheduler stub）

本 ADR 把所有契约固化为不可随意修改的协议——任何 T/F 阶段实现必须遵守这些契约。

## 2. 决策

### 2.1 NodeExecutor 接口（S1.1）

```go
type NodeExecutor interface {
    Execute(ctx context.Context, node Node, runCtx RunContext) (NodeOutcome, error)
    Hooks() map[HookPoint]HookHandler
}
```

**关键约束**：
- 失败也是正常返回（`NodeOutcome.Status=failed` + `FailureClass`），只有框架级错误（panic / context cancel）才走 `error` 通道
- 三种实现 (`AgentExecutor` / `AutomationExecutor` / `HybridExecutor`) 共享同一调度路径
- `Node` 是执行视图（不含 `DependsOn` / `Status`），与持久化层 `taskdag.Node` 解耦
- `RunContext` 骨架阶段最小占位（`DagKey / NodeKey / RunID`）；F1.x 真实使用时补 inputs/sharedfile/budget tracker

### 2.2 NodeStatus 状态机（S7.1）

九态 + 完整 from→to 转移矩阵：

```
pending  → ready          (deps done)
pending  → cancelled      (upstream fail_fast)
ready    → running        (dispatcher pick)
ready    → cancelled      (upstream fail_fast)
running  → done           (success)
running  → failed         (no retries / hard fail)
running  → retrying       (fail with retries left)
running  → skipped        (on_failure=skip)
running  → waiting_human  (on_failure=ask_human, HITL 留位)
retrying → ready          (退避结束)
retrying → failed         (放弃)
waiting_human → ready     (用户 approve)
waiting_human → failed    (用户 reject / timeout)
```

**终态封闭**：`done / failed / cancelled / skipped` 无 outgoing 转移；修改终态节点必须经 fork/reset。

**约束**：
- 同态（from==to）一律拒绝：上层应去重，禁止 idempotent update
- 空字符串 from/to 视为非法
- `nodeexec.IsTerminal(s)` 判定终态；`nodeexec.ValidateTransition(from, to)` 校验合法性

### 2.3 FailureClass 7 类 + OnFailureStrategy 7 项（S1.2）

| FailureClass | 例子 |
|---|---|
| transient | 网络抖动 / 临时限流 |
| quota | token 超限 |
| validation | 输出不符 schema |
| capability | 模型能力不够 |
| hard | 业务层不可恢复 |
| needs_human | 需人决策 |
| infrastructure | 外部服务挂了 |

| OnFailureStrategy | 含义 |
|---|---|
| retry | 简单重试 |
| escalate_model | 升级 model 重跑 |
| append_error | 错误注入 prompt 重跑 |
| replan | spawn planner 改图 |
| skip | 跳过节点 |
| fail_fast | 立即失败 |
| ask_human | 转 waiting_human |

**Lookup 优先级**（`nodeexec.ResolveOnFailureStrategy`）：
1. `cfg.ByClass[class]` 命中（且非空字符串）
2. `cfg.Default`（非空字符串）
3. `OnFailureRetry` 兜底

### 2.4 HookPoint 4 个 + HookHandler 接口（S1.2）

```go
HookBeforeExecute  // Execute 调用前
HookAfterExecute   // Execute 调用后（无论成败）
HookOnStateChange  // status 转换时
HookOnFailure      // 终态失败时

type HookHandler interface {
    Handle(ctx, point, node, outcome) error
}
```

F13.1 后，`NodeExecutorRouter` 负责真实触发：

- `before_execute`：调用 `NodeExecutor.Execute` 前；
- `after_execute`：`Execute` 返回后（含 failed outcome / framework error）；
- `on_state_change`：确认节点状态已推进后触发；当前覆盖 agent `ready/pending → running`、agent subscriber `→ done/failed`、automation `→ done`、dispatcher 终态失败 `→ failed`；
- `on_failure`：`FailNodeAndCancelDownstream` 成功后触发；仍会重试的非终态失败不触发。

生产 wiring 通过 `ProvideNodeLifecycleHooks` 给 agent / automation executor 注入默认 structured-log hook map；测试或后续审计实现可在构造 executor 时替换为自定义 `HookHandler`。hook-consumer 侧收到的 bootstrap `turn.completed` / `turn.interrupted` 复用同一 `DAGSubscriberDeps.NodeRouter`，与 in-process bus subscriber 保持一致。

Hook 是 bounded best-effort lifecycle side effect：handler error / panic 仅 Warn/Error log，不改写 executor outcome / wakeup retry / node status；dispatcher 只短等待 `lifecycleHookDispatchWait`，慢 hook 会带独立 bounded context 转异步继续，并在 `lifecycleHookExecutionTimeout` 后取消，避免审计类 hook 故障或卡顿造成重复 launch、lease 过期、资源悬挂或状态回滚。`RetryWakeup` SQL hard-cap fallback 转终态时，也必须先成功写 `FailWakeup`，再 `FailNodeAndCancelDownstream` 并触发 terminal failure hooks。

### 2.5 typed ops payload（S4.1+S4.2）

4 个 sealed 动词 + base_version OCC：

```go
type Op interface { Kind() OpKind }

OpUpdateDAG   { Patch DAGPatch }                                  // op="update_dag"
OpAddNode     { Node NodeSpec }                                   // op="add_node"
OpUpdateNode  { NodeKey string; Patch NodePatch }                 // op="update_node"
OpRemoveNode  { NodeKey string }                                  // op="remove_node"

OpsRequest  { DagKey; BaseVersion int64; Ops []Op }
OpsResponse { NewVersion int64 }
```

**JSON 形状**：每条 op 的 wire 格式带 `"op": <kind>` discriminator；`Ops` 类型自定义 (Un)MarshalJSON 做 typed dispatch。

**三方映射关系（`NodeSpec ↔ taskdag.Node ↔ nodeexec.Node`）**：
- `NodeSpec` （ops.go）是“编辑视图”：含 `DependsOn`，用于 add_node ops 表达节点依赖关系。
- `taskdag.Node` （store/taskdag）是“持久化视图”：含主键/状态/时间戳/`DependsOn`。
- `nodeexec.Node` （types.go）是“执行视图”：不含 `DependsOn` (调度器已解析) / `Status` (由 NodeOutcome 表达)。
- 数据流：`add_node ops 中的 NodeSpec` → dispatcher 写入 `taskdag.Node` → 调度时映射为 `nodeexec.Node` 交 `NodeExecutor.Execute`。
- 骨架阶段未提供统一 mapping function；F 阶段调度器重做时加一处 `nodeexecNodeFromStore(taskdag.Node) nodeexec.Node`。

**约束**：
- 未知 op kind / 缺 discriminator → fail-fast 报错
- `NodePatch.DependsOn` 用 `*[]string` 区分三态（nil 不改 / `*[]` 清空 / `*[a,b]` 设置）

**ops 在不同 status 下允许子集**（service 层校验，F4.x 实现）：

| status | 允许 ops |
|---|---|
| draft | 全部 |
| ready | 全部（用户审核期可改） |
| running | 仅 add_node 且 depends_on 指向 done 节点（动态可重写约束） |
| retrying / waiting_human | TBD（功能阶段定） |
| done / failed / cancelled / skipped | 无（要改先 fork/reset） |

### 2.6 typed node.config schema（S5.1+S5.2）

按 `node_type` 分三种顶层 config：

```go
AgentNodeConfig      { Exec AgentExecConfig;      Inputs; Outputs; FirstTurn }
AutomationNodeConfig { Exec AutomationExecConfig; Inputs; Outputs }
HybridNodeConfig     { Exec HybridExecConfig;     Inputs; Outputs }
```

**核心 exec 字段**：

| 字段 | 类型 | 含义 |
|---|---|---|
| `exec.provider` | string | claude / codex |
| `exec.model` | string | opus / sonnet / haiku / ... |
| `exec.agent_key` | string | 查 prompt_templates 表 |
| `exec.effort` | string | xhigh/high/medium/low |
| `exec.language` | string | zh / en |
| `exec.isolation` | string | shared (默认) / worktree |
| `exec.allowed_tools` | []string | 白名单 |
| `exec.disabled_tools` | []string | 黑名单 |
| `exec.budget_tokens` | int64 | 字段位（H8 enforce） |
| `exec.on_failure` | *OnFailureConfig | 见 §2.3 |

**Inputs/Outputs 共享**：

```go
InputsConfig  { FromNodes []; FromSharedfiles []; Summarization *SummarizationConfig }
OutputsConfig { ToSharedfile *SharedfileTarget; ToNodeResult bool; Schema RawMessage }

SharedfileTarget { Path; LockMode }   // exclusive | append | shared
```

**约束**：
- `to_sharedfile` 锁死 object 形状（不接受字符串简写）
- `to_node_result` **仅适合 < 4KB 摘要**；大输出必须走 `to_sharedfile`（F1.3 enforce）
- `outputs.schema` JSON Schema 校验，不符归类为 `validation` failure
- 空 raw → 返回 zero-value config（旧 DAG 兼容）
- 未知 node_type → `ErrUnknownNodeType`

### 2.7 与现有 dispatcher retry strategy 的协调（M-6）

现有 `cmd/mcp-orch/orchestration/retry_strategy.go` 定义了：
- `DAGSchedulePolicy { DefaultRetry, FailFast }` ← DAG metadata 的 schedule 子树
- `NodeExecutionPolicy { Retry, HasRetry }` ← node.config 的 execution 子树
- `RetryPolicy { MaxAttempts, FailFast }` ← dispatcher 派生的最终决策

新 `nodeexec.OnFailureConfig` 与现有 typed struct **共存**，不互相替换：

| 来源 | 定位 |
|---|---|
| `retry_strategy.go::RetryPolicy` | dispatcher fallback：节点未配置 `exec.on_failure`、非 DAG wakeup、或 framework/legacy error 时，继续按 DAG `metadata.schedule.default_retry/fail_fast` 与 node `execution.retry` 做 bounded retry |
| `nodeexec.OnFailureConfig` | dispatcher node-level override：节点配置 `exec.on_failure` 且 executor 返回 `NodeOutcome{Status: failed, FailureClass: ...}` 时，按 `by_class`/`default` 分发智能重试策略 |

**F12.1 收敛结果**：
- `retry`：沿用 `RetryWakeup`，仍受 SQL attempt_count<8 paranoid hard-cap 保护。
- `escalate_model`：仅支持 agent 节点；先执行 `RetryWakeup`，fence 成功后再走 `PatchTaskDagNodeConfigIfUnchanged` 窄口 patch `node.config.exec.model` 为 escalation_chain 下一档，避免 stale lease 或并发 `apply_ops` 被整行 `UpsertNode` 覆盖。
- `append_error`：仅支持 agent 节点；先执行 `RetryWakeup`，fence 成功后再走 `PatchTaskDagNodeConfigIfUnchanged` 把上一轮 validation 诊断追加进 `first_turn`。
- `replan`：在 `max_attempts` 尚未耗尽时 spawn `dag_designer` planner agent，并在 prompt 中要求使用 `task_dag_apply_ops` 做最小改图。
- `fail_fast`：立即 `FailWakeup` + `FailNodeAndCancelDownstream(fail_fast=true)`。
- `skip` / `ask_human`：状态机枚举保留，但 dispatcher 业务语义未落地前 fail-closed，不 silent fallback 到 retry。
- `hard` / `needs_human` 未显式映射到非重跑策略时保持永久失败，不被 `on_failure.max_attempts` 或默认 retry 误重试。

**范围边界**：F12.1 只覆盖 dispatcher-time `NodeExecutor.Execute` 返回的失败；`dag_turn_completed_subscriber.go` 的 output/materialization validation 失败仍按当前终态失败处理。若后续真实 dogfood 需要“完成后输出校验失败再 append_error/replan”，另立 follow-up，不混入本 ADR 基线。

### 2.8 dispatcher fast-lane（S2.4 推迟说明）

**现状**：`task_dag_node_runtime.sql:28` 硬约束 `WHERE status IN ('pending')`，dispatcher 走 `pending → running` 直跳，不经 `ready` 中转。

**骨架阶段并存**：
- 外部 API（`task_update_node` / `service.UpdateNodeStatus`）：遵守完整 9 态机，`pending → done` 跳态被 `nodeexec.ValidateTransition` 拒
- 内部 dispatcher：走 fast-lane `pending → running`

**F 阶段一并修复**：dispatcher 重做时同步：
1. `task_dag_node_runtime.sql:28` 接受 ready: `status IN ('pending', 'ready')`
2. 同步更新 sqlc 生成代码
3. `CompleteNodeAndScheduleDownstream` 同事务修下游 status 为 `ready`

详见实施计划 §1 S2.4 推迟说明。

### 2.9 ErrXxxNotImplemented sentinel 约定

骨架阶段 stub 方法返回 sentinel error（errors.Is 可用）：

| 方法 | sentinel |
|---|---|
| `service.StartDAG` / `TerminateDAG` / `ApplyOps` | `ErrLifecycleNotImplemented` |
| `Scheduler.Tick` / `Schedule` | `ErrSchedulerNotImplemented` |
| `ParseNodeConfig` 未知 node_type | `ErrUnknownNodeType` |

调用方：
```go
if errors.Is(err, ErrLifecycleNotImplemented) { /* 骨架阶段未接通，跳过/退化 */ }
```

### 2.10 DB 不变量基线规则 / DB Invariant Baseline Rules

本节沉淀 0072–0081 一批 schema-tightening migration 的共同模式，作为未来
新建 DAG 相关表的必过 baseline。三条规则都以“应用层已约定的不变量，DB 层必下沉”为
原则，避免仅靠 service 代码拦截。

This section captures the shared pattern from migrations 0072–0081 as a mandatory
baseline for any future DAG-related table. All three rules push application-layer
invariants down to the DB so we don't rely solely on service-layer validation.

1. **枚举字段必加 CHECK / Enum-like columns MUST have CHECK**
   - 中文：Text 类型代表枚举的列必加 `CHECK (col IN (...))` 锁定全集。
   - English: any TEXT column that represents an enum MUST carry a `CHECK
     (col IN (...))` constraint pinning the full value set.
   - 案例 / Case studies: `0080` (`task_dag_runs.status` enum: running |
     succeeded | failed | cancelled) 、`0081` (`task_dags.trigger` enum:
     manual | auto | scheduled | external)。

2. **jsonb 列必加 jsonb_typeof CHECK / jsonb columns MUST have shape CHECK**
   - 中文：jsonb 列若期望 array 必加
     `CHECK (jsonb_typeof(col) = 'array')`; 期望 object 同理。
   - English: jsonb columns expected to hold an array MUST add
     `CHECK (jsonb_typeof(col) = 'array')`; object-shaped values use the
     same pattern with `'object'`.
   - 案例 / Case studies: `0078` (`task_dag_nodes.depends_on` array) 、
     `0079` (`task_dag_nodes.reads` / `task_dag_nodes.writes` arrays)。

3. **跨行业务唯一性必下沉到 partial unique index /
   Cross-row uniqueness MUST be a partial unique index**
   - 中文：“任意时刻 ≤ 1 个 X”的应用层约束必下沉到 DB 层 partial
     unique，避免 TOCTOU race。
   - English: any application-level "≤ 1 row matching X at any time"
     invariant MUST be enforced by a partial unique index, never by
     check-then-write logic in service code (TOCTOU race).
   - 案例 / Case studies: `0076` `task_dag_runs` one running run per
     `dag_key` (partial unique `WHERE status='running'`)。

**适用范围 / Applicability**：未来新建 DAG 相关表必先过这 3 条 baseline
检查，并在该表首次 migration 里同步加入。代码 review 发现缺失时直接裁决
为“未满足 0001 §2.10”。
Any new DAG-related table must clear these 3 baseline checks before merge;
the first migration that creates the table is the right place to add them.
Reviewers may reject a change as "violates ADR 0001 §2.10" without further
discussion.

## 3. 状态

**Accepted** — 骨架阶段所有 11 个 commit 已对齐本 ADR；T/F 阶段实现以本 ADR 为契约依据。

骨架阶段完成的契约：

| 契约 | Commit | 文件 |
|---|---|---|
| NodeExecutor 接口 + NodeOutcome / RetryHint | `5e1c731e` | `nodeexec/types.go` |
| FailureClass 7 类 / OnFailureStrategy 7 项 / HookPoint 4 个 / HookHandler | `5e1c731e` | `nodeexec/types.go` |
| 三 stub: AgentExecutor / AutomationExecutor / HybridExecutor | `5de5dd44` | `nodeexec/stubs.go` |
| 包重构 → nodeexec 子包 | `9dda3a41` | `nodeexec/` |
| typed ops payload + OpsRequest/Response | `89073074` | `nodeexec/ops.go` |
| NodeStatus 9 态 + ValidateTransition + IsTerminal | `af542629` | `nodeexec/status.go` |
| service.UpdateNodeStatus 接通 ValidateTransition | `c972b3f1` | `orchestration/dag.go` |
| service.StartDAG/TerminateDAG + Scheduler stub | `c504441e` | `orchestration/dag.go` / `scheduler.go` |
| typed node.config schema + ParseNodeConfig | `0883254b` | `nodeexec/config.go` |
| OnFailureConfig 解码 + by_class lookup | `61bff08b` | `nodeexec/on_failure.go` |
| service.ApplyOps stub | `da79df11` | `orchestration/dag.go` |
| p23 deprecation 提示 | `66c42c82` | `docs/plans/迁移/p23/README.md` |
| S2.4 推迟说明 | `84c5b0da` | `dag.go` / 实施计划 |

骨架阶段未做（按计划推迟）：
- S2.4 节点 done 后下游自动 promote ready → 推迟到 F 阶段（与 dispatcher 重做一并）
- S3.x migration → 待 PG 测试环境就位后做
- S6.x UI 占位 → 前端按 `feedback/threadstore-whitelist-and-hmr.md` 流程，方案先发用户
- S15.1 删 `auto_handoff_phase1` → 依赖 S3.4 兼容映射 migration 先过

## 4. 后果

### 正面后果
- T/F 阶段任意 worker 拿到本 ADR 即可独立动手，不需要再读跨多个 commit 的设计上下文
- 状态转移矩阵 / 失败分类 / ops 动词等被代码（含单测）和文档双向锁定
- 与 retry_strategy.go 的协调路径明确，避免重复造轮子

### 负面后果
- `nodeexec.OnFailureConfig` 与 `retry_strategy.RetryPolicy` 仍共存，但边界已收敛为 node-level override 与 fallback 的关系
- dispatcher fast-lane（外部 API 与内部 dispatcher 状态机不一致）短期存在；用户在 UI 上看不到 `ready` 状态，看到的永远是 pending → running（F-stage 修复）
- 包文件数 30/30 顶到上限；后续新 typed 文件必须放 `nodeexec/` 子包或合并到现有文件

### 已知风险（已在审查中识别）
- B-14 节点完成后下游 status 不自动 promote → 推迟到 F 阶段
- subscriber/output materialization validation 失败暂不进入 F12.1 智能重试，后续按真实需求另立 follow-up
- 状态机校验（S7.2）只覆盖外部 API，dispatcher 路径不校验

## 5. 引用

- 蓝图：`docs/plans/dag改造蓝图v2.md`（决策与设计）
- 实施计划：`docs/plans/dag改造实施计划.md`（task 清单）
- 审查报告（sharedfile，不入 git）：`handoff/audit-{pass1-structural,pass1-executability,pass1-synthesis,pass2-blindspot,final-report}.md`
- 同侪研究 / 外部资料：会话历史中的 AI Agent Book / Harness MCP 工具图 / "只保留 DAG" PDF / LangGraph Command / Shannon / CC Agent Teams / AWS CAO / oh-my-hermes / Cursor / Temporal
