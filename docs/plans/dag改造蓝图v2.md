# Super-Dolphin DAG 改造蓝图 v2

> 状态：草案 / 等待动手
> 关系：本文是 `docs/plans/迁移/p23/` 的上位重排，不是替换；p23 各 Phase 的去留在第 11 节给出映射表。
> 历史：2026-05-09 初稿；2026-05-10 合并 14 处骨架补丁、4 阶段路径、所有讨论决策；2026-05-10 二修（用户场景查漏：4 处小补）；2026-05-10 三修（2-pass DAG 审查后同步：决策来源标记 + result 大小规则）

---

## 1. 一句话定位

**Super-Dolphin = 一个动态可重写 DAG 运行时**。所有"自动化"能力都收敛成 DAG 的一种节点类型，节点是 `agent / automation / hybrid` 三选一。UI 上 DAG 是一等公民，对应左侧栏 D 入口。

## 2. 三份素材产生的新认知（与 p23 写作时不同）

| 来源 | 关键观点 | 对 p23 的影响 |
|---|---|---|
| AI Agent Book Part 5/7 | 编排有 DAG/Swarm/Handoff/Supervisor 多种模式；Shannon 用复杂度路由分派 | p23/P12 SwarmArbiter 应砍，坚持 DAG 单模式 |
| Harness MCP 工具图 | 工具不全量塞，分层 + 动态最小集；ToolSearch 单入口 | DAG 工具集要保持极简 |
| "只保留 DAG" PDF | DAG 必须**动态可重写**，不是静态预设；"错误恢复逻辑从 prompt 移到基础设施" | p23/P11 DynamicNodeGrowth 优先级提到第二位 |
| LangGraph Command(goto) | 节点内决定下一跳 = 等价"运行时改图"，但实现简单得多 | P11 降维：先做"已完成节点后追加" |
| CC Agent Teams | 共享任务清单 + 文件锁是被官方采纳的同侪协作方式 | sharedfile 应显式挂到 DAG 节点 |
| AWS CAO | handoff/assign/send_message 三原语 | DAG 节点之间需要补 send_message 原语 |
| Temporal | event-sourced replay 让失败 workflow 能本地复现 | task_dag_runs 留 events 字段位 |

## 3. 当前现状（盘点结论，证据见会话记录与 p23/README.md）

后端能力金字塔：

```
MCP 工具层      ✅ 完整 (task_create_dag / get_dag / update_node)
服务层          ⚠️ 半接 (无 StartDAG，但 status=done 已接 CompleteNodeAndScheduleDownstream)
调度层          ✅ 已跑 (wakeup_dispatcher → 子 agent 拉起 → sharedfile 协作)
重试层          ✅ 半接 (RetryPolicy 已实现固定次数 + fail_fast，但不智能不分类)
持久层          ⚠️ 平表 (schedule/execution 塞 metadata；无 trigger/owner_id 一等字段)
前端            ⚠️ 占位 (DagsPage 33 行薄壳；DagDetailModal STUB；只挂自动续接开关)
```

七个真实限制：

1. DAG 创建即终点：无 `StartDAG/TriggerDAG/task_start_node` (`tools/task_tools.go:96`)
2. `auto_handoff_phase1` 写了但无消费者 (`task_tools.go:23,231-235`)
3. 静态 DAG：创建时一次性 upsert (`dag.go:109-131`)，无 `grow_subgraph`
4. `schedule` / `execution` 塞 metadata JSON
5. `trigger` 默认空，靠 `auto_handoff_phase1` 兼容映射
6. `owner_id` 不存在
7. 前端 `DagDetailModal` 是 STUB

## 4. 设计原则

1. **极简 over 全功能**：不引入"模板库 / fork preview / lineage / 多模式路由"。
2. **动态运行时 over 静态预设**：DAG 必须支持运行时追加节点。
3. **DAG 是统一抽象**：自动化任务、AI Agent、混合任务都是节点类型。
4. **后端能跑 → UI 翻译**：先把已经跑得起来的链路给用户看见。

## 5. 关键决策汇总（讨论中拍板）

决策来源标注：✅ = 用户明确拍板；🤖 = AI 推荐（用户未明确反对但也未明确发言，未来可调整）

| 决策点 | 选择 | 来源 | 理由 |
|---|---|---|---|
| DAG vs DAG run | **C 混合** | ✅ | DAG 主表是模板，引用 `task_dag_runs` 表存每次执行实例；node 加 `run_id` 维度。Temporal/Airflow/LangGraph 标准模型。 |
| 编辑粒度 | **C+ ops + 版本号 + 暴露 MCP** | ✅ | typed ops 数组（add/update/remove）+ `base_version` OCC；AI 和用户共用同一组动词 |
| node.config schema | **A 完全 typed** | ✅ | Go struct + JSON Schema 双向；AI 才能稳定输出符合契约的 ops |
| 编排模式 | **只保留 DAG** | ✅ | 砍 Swarm / Supervisor / 多策略路由 |
| 状态机 | **9 态** | 🤖 | pending / ready / running / retrying / done / failed / cancelled / skipped / waiting_human |
| 失败分类 | **7 类（含 infrastructure）** | 🤖 | transient / quota / validation / capability / hard / needs_human / infrastructure |
| on_failure 策略 | **按 class 分发** | 🤖 | by_class map + escalation_chain 字段位（功能阶段实现） |
| HITL | **enum 留位不实现** | 🤖 | `waiting_human` 入 enum，UI/通知/timeout 等加固阶段做 |
| Cron | **保留 P5 但骨架阶段只字段位 + Scheduler stub** | ✅ | 用户明确提出 Need 1 每日定时 |
| 节点隔离 | **`exec.isolation` 字段位** | 🤖 | 多并行任务 worktree 隔离（Cursor 同侪验证） |
| 重新规划策略 | **`OnFailureStrategy` 加 `replan`** | 🤖 | 失败时 spawn planner 改图，不止重试 |
| 输出校验 | **`outputs.schema` 字段位** | 🤖 | JSON Schema 自动校验，配合 `validation` 失败分类 |
| 实时进度 | **T 阶段先 polling，WS 升级放加固阶段** | 🤖 | 用户高频痛点；不补骨架 |
| **result vs sharedfile 边界** | **`to_node_result` 仅 < 4KB 摘要；大输出必须 `to_sharedfile`** | 🤖 | 防止 task_dag_nodes.result JSONB 列膨胀；F1.3 enforce |

## 6. 改造决策矩阵

### 6.1 留下（p23 已规划且高 ROI）
- P3 Explicit Start + Ownership：`trigger=manual/auto/scheduled` 一等字段；删 `auto_handoff_phase1`；补 `owner_id`。
- P10 真实 DAG UI（精简版）：节点列表 + 状态色 + Start 按钮 + 子 agent 链接。
- P11 Dynamic Node Growth（降维）：只做"已完成节点后追加"。

### 6.2 重新定义
- 节点类型统一接口：`agent / automation / hybrid` 三种共享同一调度路径。
- task_grow_subgraph 取消独立工具：合并到 `task_dag_apply_ops`，service 层校验"running 状态下只允许 add_node + depends_on 指向 done 节点"。

### 6.3 砍掉
- P9 Scale Scheduling (N>50)：节点数 < 20。
- P12 SwarmArbiter：与"只保留 DAG"主张冲突。
- P13 Strict JSON Output / 表单驱动：UI 用 typed schema 自动渲染表单，不需要单独 phase。
- P8 VerificationGate：用 `hybrid` 节点类型替代。

### 6.4 推迟
- P5 Cron / P6 External RPC：trigger 字段保留，先实现 manual + scheduled。
- P7 LivenessProbe：wakeup 已有重试，看生产数据再说。

### 6.5 加上（同侪验证、p23 没覆盖）
- 节点 → sharedfile 绑定 + lock_mode（CC Agent Teams 文件锁）。
- token budget 字段位（Shannon / Book Part 8）。
- inputs.summarization 字段位（Book Part 3 上下文管理）。
- task_dag_runs.events 字段位（Temporal event-sourcing）。
- lifecycle hooks 接口位（Book Part 2）。
- 节点级 allowed_tools 白名单（Harness MCP 工具图）。
- task_post_message 原语（CAO send_message，**功能阶段做 / 骨架不做**）。
- 节点级 worktree 隔离（Cursor 长跑 agent 模式，`exec.isolation` 字段位）。
- 失败重新规划策略（`OnFailureStrategy.replan`，spawn planner 改图）。
- 节点输出 JSON Schema 校验（`outputs.schema` 字段位，配合 `validation` failure class）。

## 7. 节点 config typed schema（骨架阶段锁死）

```jsonc
// node_type = "agent"
{
  "exec": {
    "provider": "claude",        // claude | codex
    "model": "opus",             // 任意 provider 支持的 model
    "agent_key": "architect",    // 查 prompt_templates 表
    "effort": "high",
    "language": "zh",
    "isolation": "shared",       // shared | worktree （worktree 模式下子 agent 在独立 git worktree）
    "allowed_tools": [],         // 白名单（可选，与 disabled_tools 共存）
    "disabled_tools": [],
    "budget_tokens": 50000,      // 字段位，骨架不 enforce
    "on_failure": {
      "default": "retry",
      "by_class": {
        "capability": "escalate_model",
        "validation": "append_error",
        "hard": "fail_fast"
      },
      "max_attempts": 3,
      "escalation_chain": ["sonnet", "opus"]
    }
    // 注：on_failure 策略 enum 含 retry / escalate_model / append_error / replan / skip / fail_fast / ask_human
  },
  "inputs": {
    "from_nodes": ["prev"],
    "from_sharedfiles": ["plan.md"],
    "summarization": {
      "strategy": "none",        // none | last_n | llm_summary
      "max_tokens": 4000
    }
  },
  "outputs": {
    "to_sharedfile": {
      "path": "report.md",
      "lock_mode": "exclusive"   // exclusive | append | shared
    },
    "to_node_result": true,        // 仅适合 < 4KB 摘要；大输出必须走 to_sharedfile
    "schema": {                   // 可选 JSON Schema：节点输出不符则归类为 validation failure
      "type": "object",
      "required": ["summary"],
      "properties": {"summary": {"type": "string"}}
    }
  },
  "first_turn": ""               // 可选：覆盖 agent_key 默认提示词
}

// node_type = "automation"
{
  "exec": {
    "command_ref": "build_app",
    "args": {},
    "budget_tokens": null,
    "on_failure": { ... }
  },
  "inputs": { ... },
  "outputs": { ... }
}

// node_type = "hybrid"
{
  "exec": {
    "automation": { ... },       // 先跑 automation
    "verifier": { ... }           // 再用 agent 验证
  },
  "inputs": { ... },
  "outputs": { ... }
}
```

## 8. 状态机（骨架阶段锁死）

九态：

| Status | 含义 | 入态条件 |
|---|---|---|
| pending | 上游未全 done | DAG 创建后默认 |
| ready | deps 满足，等 dispatcher pick | upstream 全 done |
| running | executor 已启动 | dispatcher pick |
| retrying | 失败但有 attempts 余量，等下次拉起 | running fail + retries left |
| done | 成功终态 | running success |
| failed | 失败终态 | running fail + no retries / hard fail |
| cancelled | 被上游 fail_fast 级联取消 | upstream failed + fail_fast=true |
| skipped | on_failure=skip 时跳过 | upstream failed + on_failure=skip |
| waiting_human | HITL 暂停（**enum 留位不实现**） | on_failure=ask_human |

七类失败：

| FailureClass | 例子 |
|---|---|
| transient | 网络抖动、CLI 启动失败、临时限流 |
| quota | token 超限、context 过长 |
| validation | 输出不符合 schema、JSON 解析失败 |
| capability | 模型能力不够（Haiku 搞不定复杂推理） |
| hard | 业务层认定不可恢复 |
| needs_human | 涉及不确定决策 |
| infrastructure | 外部服务挂了（Postgres 抽风等） |

## 9. ops typed payload（骨架阶段锁死）

```jsonc
// task_dag_apply_ops(dag_key, base_version, ops[]) -> {new_version}
[
  {"op": "update_dag", "patch": {"title": "...", "trigger": "scheduled", "cron_expr": "0 8 * * *"}},
  {"op": "add_node", "node": {"node_key": "n1", "title": "...", "node_type": "agent", "depends_on": [], "config": {...}}},
  {"op": "update_node", "node_key": "n1", "patch": {"config": {...}}},
  {"op": "remove_node", "node_key": "n1"}
]
```

ops 在不同 status 下允许的子集：

| status | 允许 ops |
|---|---|
| draft | 全部 |
| ready | 全部（用户审核期还能改） |
| running | 仅 add_node 且 depends_on 指向 done 节点（动态可重写） |
| done/failed/cancelled | 无（要改先 fork 或 reset） |

## 10. 14 处骨架补丁

| # | 主题 | 出处 | 内容 |
|---|---|---|---|
| 1 | NodeExecutor 接口 + 三 stub | 设计 | `Execute(node, runCtx) (NodeOutcome, error)` + Hooks() ；agent stub 包现有 wakeup 拉子 agent 逻辑 |
| 2 | service 层 StartDAG/TerminateDAG/ApplyOps + Scheduler | 设计 | 接口位定签名；Scheduler.Tick/Schedule stub |
| 3 | migration 字段位 | 设计 | task_dags 加 trigger/owner_id/cron_expr/next_run_at/version；新建 task_dag_runs；task_dag_nodes 加 run_id/reads/writes |
| 4 | typed ops payload | 设计 | update_dag / add_node / update_node / remove_node 四个动词 + base_version OCC |
| 5 | typed node.config schema | 设计 | 第 7 节三种 node_type 的 schema + Go struct 解码器（含 `exec.isolation` / `outputs.schema` 字段位） |
| 6 | UI 组件骨架占位 | 设计 | DagDetailModal 占位结构（节点列表 / Start 按钮 / 拓扑位），不接数据 |
| 7 | 状态机 9 态 | Q&A | 第 8 节九态 + 状态转移合法性表 |
| 8 | FailureClass + on_failure 策略 typed schema | Q&A | 第 8 节七类 + 策略 enum (含 `replan`) + by_class 分发 |
| 9 | NodeExecutor.Execute → NodeOutcome | Q&A | NodeOutcome{Status, Result, FailureClass, ErrorSummary, RetryHint} |
| **10** | lifecycle hooks 接口位 | Book Part 2 | HookPoint enum (before_execute/after_execute/on_state_change/on_failure) |
| **11** | inputs.summarization 字段位 | Book Part 3 | strategy: none/last_n/llm_summary + max_tokens |
| **12** | task_dag_runs.events 字段位 | Temporal | events JSONB DEFAULT '[]'，骨架不写入 |
| **13** | token budget 字段位 | Book Part 8 / Shannon | exec.budget_tokens；task_dag_runs.budget_used/budget_limit |
| **14** | allowed_tools 白名单 + sharedfile lock_mode | Harness 工具图 / CC Agent Teams | exec.allowed_tools[]；outputs.to_sharedfile.{path,lock_mode} |

**所有补丁的共同特征**：字段位 / 接口位 / Go enum / typed schema / 一份 ADR；**骨架阶段不写行为**。

## 11. 4 阶段落地路径

### 阶段 S 骨架（行为完全不变）
- S1-S9：补丁 1-14 全部到位
- S10：删除 `auto_handoff_phase1` 死代码
- S11：一份 ADR：NodeExecutor / Ops / Status / FailureClass 契约
- 验收：build/test/vet/scripts/test_with_guard.sh 全过；旧 DAG 100% 兼容；`auto_handoff_phase1` 全代码 0 命中
- 工作量比例：约 25%

### 阶段 T 工具（行为开始能用）
- T1：MCP `task_start_dag`
- T2：MCP `task_dag_apply_ops`（typed payload，draft/ready 可改）
- T3：MCP `task_get_run / task_list_runs`
- T4：MCP registry 工具：`list_models / list_prompt_templates / list_command_cards / list_sharedfiles`（给 AI 自助查可用资源）
- T5：UI `DagDetailModal` 接 `task_get_dag`，渲染节点列表 + 状态色 + Start 按钮（**节点状态先 polling 3-5s 刷新；WS 升级放加固阶段**）
- T6：UI 节点行展示 `exec.provider/model/agent_key`，跳到子 agent thread
- T7：UI 列表显示 `trigger / status / version / 最近 run 状态`（同 polling 策略）
- T8：UI「AI 帮你设计流程」按钮（spawn 新 thread + 注入 base 设计师 prompt 占位）
- 验收：端到端 `task_create_dag → UI 看到 → 点 Start → 看到节点状态变化` 通过
- 工作量比例：约 35%
- **流程合规**：所有前端改动按 `feedback/threadstore-whitelist-and-hmr.md`，方案先发用户确认

### 阶段 F 功能（行为兑现）
- F1 AgentExecutor 真实实现（解码 `node.config.exec` → 调 `orchestration_launch_agent`）
- F2 AutomationExecutor 真实实现（调 command card）
- F3 HybridExecutor 真实实现（automation 后跑 agent verifier）
- F4 ApplyOps 真实落地：add/update/remove + 环检测 + 版本号 OCC
- F5 **Scheduler 真实实现**（cron daemon 进程，tick 扫 next_run_at <= now → StartDAG）
- F6 Run 真实落地：StartDAG 创建 run + snapshot dag.version + node.run_id
- F7 **AI 设计师 prompt**：prompt_template 表新增条目（中英两版本）
- F8 UI 节点编辑表单（typed schema 自动渲染）
- F9 UI mermaid 拓扑图
- F10 UI run 历史时间轴
- F11 UI sharedfile 锁可视化（节点 reads/writes 联动）
- F12 智能重试 strategy dispatcher（by_class + escalation_chain + `replan` 策略：spawn planner agent 改图）
- F13 lifecycle hooks 真实触发
- 验收：端到端用例 ——「AI 帮我设计每天 8 点的报告生成 DAG → AI 设计 → 用户改一处 prompt → Start → 第一个 run 跑通 → 第二天 8 点自动起第二个 run → run 历史里看到两次执行 → 一次故意失败 → 状态正确反映」
- 工作量比例：约 30%

### 阶段 H 加固（按真实问题驱动）
- H1 错误信息人话翻译
- H2 节点级 retry / fallback 模型策略调优
- H3 大 DAG 性能（N>50 拆批，如有需要）
- H4 task_dag_revisions 表（编辑历史/回滚 UI）
- H5 multi-tenant / 权限（如有需要）
- H6 监控/告警（cron miss / run timeout）
- H7 inputs.summarization 真实实现
- H8 token budget enforcement
- H9 task_post_message 原语真实落地（如确认有需求）
- H10 waiting_human HITL 完整流程（如确认有需求）
- 工作量比例：约 10%（按问题增量）

## 12. 两个核心需求映射

| 需求 | 实现里程碑 |
|---|---|
| **每日定时任务** | T1+T3+T7（UI 看得到字段）→ F5+F6（Scheduler 真跑）= 功能阶段才完整 |
| **AI 帮你设计流程** | T4+T8（按钮 + registry）→ F7+F8（设计师 prompt + 表单）= 功能阶段才完整 |

## 13. 阶段间硬约束

| 依赖 | 含义 |
|---|---|
| S → T | 接口未定时不能开 T |
| T1-T4 → T5-T7 | UI 接通要等 MCP 工具就位 |
| T → F | F 是行为兑现，T 是骨架对外开口 |
| F1 → F2/F3 | Agent 类型最常用先做 |
| F5 (cron) | 与其他 F 可并行 |
| H | 完全按真实问题驱动 |

## 14. 与 p23 的关系（去留映射表）

| p23 Phase | 决策 | 落到 v2 哪里 |
|---|---|---|
| P0 DAGRuntimeSkeleton | 已实现 | — |
| P1 WakeupDispatcher + LaunchBinding | 已实现 (`wakeup_dispatcher.go`) | — |
| P2 NodeTerminalReconcile | 已实现 (`CompleteNodeAndScheduleDownstream`) | — |
| P3 ExplicitDAGStart + Ownership | 留 | S2 + S3 + 删 `auto_handoff_phase1` |
| P4 HostAgentTriggerSurface | 留（合并 P3） | T1（独立 `task_start_dag`） |
| P5 CronTriggerSurface | 留（轻量） | S2 Scheduler stub + S3 字段 + F5 真实跑 |
| P6 ExternalRPCTriggerSurface | 推迟 | trigger 字段位预留 |
| P7 LivenessProbe | 推迟 | — |
| P8 VerificationGate | 砍 | hybrid 节点类型替代 |
| P9 ScaleScheduling | 砍 | — |
| P10 TemplateAndUI | 留（精简） | T5-T8 + F8-F11 |
| P11 DynamicNodeGrowth | 留+降维 | T2 ops `add_node` + service 层校验 |
| P12 SwarmArbiter | 砍 | — |
| P13 StrictJSONOutput | 砍 | typed schema 已涵盖 |

## 15. 故意不加的清单

| 不加 | 理由 |
|---|---|
| 三层工具 L0/L1/L2 + ToolSearch | 属 Super-Dolphin 自身 MCP 工具治理，与 DAG 骨架无关 |
| 上下文 3 轮 / 熔断机制 | 属 harness 工具系统层 |
| OPA 完整策略治理 | 单用户工具过重 |
| WASI 沙箱 | 单用户不需要 |
| Multi-strategy 路由（Shannon） | 与"只保留 DAG"决策冲突 |
| Git snapshot 耦合 | 节点自己用 command_card 调 git 即可 |
| Feature flag 系统 | 单人项目过重 |
| 节点级向量记忆引用 | agent 自主调 memory_read |
| metrics / traces 完整可观测性 | 加固 H 阶段做 |
| task_post_message 持久化（骨架/工具阶段） | sharedfile 已能承担；功能阶段如确认需求再做 |
| 完整 HITL approve 流程 | waiting_human 留 enum 位即可 |
| 模板库 / fork preview / lineage | 极简原则 |
| 节点级 Skills 强制引用 | agent 自主调 skill_read_section |

## 16. 风险

- **R1**：删除 `auto_handoff_phase1` 影响生产。先扫数据库已有 DAG 是否带这个 metadata；写迁移把它映射到 `trigger='auto'`。
- **R2**：UI 改动黑屏风险。阶段 T 严格按 `threadstore-whitelist-and-hmr.md` 流程：方案先发用户确认。
- **R3**：`task_dag_apply_ops` 与并发 `update_node` 的事务边界。落地前先写 30 行事务设计纸条。
- **R4**：14 处补丁同时进入骨架，build 风险。S 阶段按 1-14 顺序小步提交，每个补丁独立可验收。
- **R5**：typed schema 锁死后扩展难。已通过预留字段位（events/budget/summarization）缓解，但仍要警惕"不在清单内的字段"诱惑。

## 17. 验收（整个蓝图完成后）

1. UI 上「DAG 管理」页能看到真实 DAG 列表与详情，点 Start 能跑
2. AI 帮设计流程：点按钮→新 thread→AI 输出 ops→DAG 创建→用户审核→Start→成功跑通
3. 每日 cron：DAG 配置 cron_expr→次日自动起 run→run 历史可见
4. 一次"代码生成→验证失败→追加 refinement→通过"端到端用例在 DAG 视图里完整复现
5. `auto_handoff_phase1` 全代码 0 命中
6. 节点能声明 reads/writes 共享文件，UI 上看到锁
7. 智能重试：节点失败按 FailureClass 分发策略，capability 类失败能升级 model 重跑
8. p23 各 Phase 状态在 README.md 更新为"v2 已合并 / v2 砍 / v2 推迟"

## 18. 下一步动作

执行入口：**S1（NodeExecutor 接口 + stub）→ S2（service 层 + Scheduler stub）→ S3（migration 字段位）→ S4-S9（typed schema / 状态机 / ops / hooks / budget / 等）→ S10（删 auto_handoff_phase1）→ S11（ADR）**。

每个子项落地前：
- `git fetch origin main` + 看双向差距
- 写一份"改动清单 + 验证计划"短纸条
- 跑完一轮验证（go test + vitest + scripts/test_with_guard.sh + go vet）才 commit
- 不主动 push，等用户明确指令
- 每个子项独立可验收，提交粒度按 `feedback/prefer-small-commits` 拆分

骨架阶段全部子项独立无强依赖（除 S3 migration 与其他 schema 改动需协调），可并行起 worker。
