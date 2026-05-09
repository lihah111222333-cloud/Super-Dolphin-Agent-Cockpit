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

骨架阶段 `Hooks()` 返回 nil，dispatcher 不调用；F13.1 真实触发。

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

### 2.7 与现有 `dag_retry_policy.go` 的协调（M-6）

现有 `cmd/mcp-orch/orchestration/dag_retry_policy.go` 定义了：
- `DAGSchedulePolicy { DefaultRetry, FailFast }` ← DAG metadata 的 schedule 子树
- `NodeExecutionPolicy { Retry, HasRetry }` ← node.config 的 execution 子树
- `RetryPolicy { MaxAttempts, FailFast }` ← dispatcher 派生的最终决策

新 `nodeexec.OnFailureConfig` 与现有 typed struct **共存**，不互相替换：

| 来源 | 定位 |
|---|---|
| `dag_retry_policy.go::RetryPolicy` | dispatcher 路径（生产 wakeup_dispatcher → store.UpdateRunningNodeStatus）的最终决策；不接 by_class 分发 |
| `nodeexec.OnFailureConfig` | service 路径 + ApplyOps + AgentExecutor.on_failure 的 typed schema；含 by_class / escalation_chain |

**F 阶段统一时机**（不在骨架阶段做）：当 dispatcher 重做时（与 S2.4 一并），把两条路径合到 `nodeexec.OnFailureConfig`，淘汰 `dag_retry_policy.go::RetryPolicy`，或反之。决策延迟到 F-stage ADR 0002。

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
- 与 dag_retry_policy.go 的协调路径明确，避免重复造轮子

### 负面后果
- `nodeexec.OnFailureConfig` 与 `dag_retry_policy.RetryPolicy` 短期共存增加认知负担（F-stage ADR 0002 收敛）
- dispatcher fast-lane（外部 API 与内部 dispatcher 状态机不一致）短期存在；用户在 UI 上看不到 `ready` 状态，看到的永远是 pending → running（F-stage 修复）
- 包文件数 30/30 顶到上限；后续新 typed 文件必须放 `nodeexec/` 子包或合并到现有文件

### 已知风险（已在审查中识别）
- B-14 节点完成后下游 status 不自动 promote → 推迟到 F 阶段
- S5.1 与 dag_retry_policy.go 协调策略未最终定，依赖 F-stage ADR 0002
- 状态机校验（S7.2）只覆盖外部 API，dispatcher 路径不校验

## 5. 引用

- 蓝图：`docs/plans/dag改造蓝图v2.md`（决策与设计）
- 实施计划：`docs/plans/dag改造实施计划.md`（task 清单）
- 审查报告（sharedfile，不入 git）：`handoff/audit-{pass1-structural,pass1-executability,pass1-synthesis,pass2-blindspot,final-report}.md`
- 同侪研究 / 外部资料：会话历史中的 AI Agent Book / Harness MCP 工具图 / "只保留 DAG" PDF / LangGraph Command / Shannon / CC Agent Teams / AWS CAO / oh-my-hermes / Cursor / Temporal
