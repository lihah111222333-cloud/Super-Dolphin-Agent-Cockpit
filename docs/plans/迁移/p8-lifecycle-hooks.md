# P8 Lifecycle Hooks — 核心层执行拦截接口

> 生成时间：2026-03-23
> 状态：按 13 项共识修订
> 前置：P8.5 `ctl/*` v1 基线控制面 + `ctl/hook/*` v2 扩展

---

## §0 定位

P8 的 hooks 必须被定义为：

- hooks 是核心层提供的执行拦截接口，不是 `cmd/mcp-orch` 内部适配层
- hooks 的 owner 是核心层状态机
- hooks 的 transport 是 `ctl/hook/*`
- hooks 的消费者不预设唯一答案；DAG 只是一个可选消费者，不是 hooks 存在的前提
- agent 不能自主宣布完成；所有完成态都必须先经过 hook 审查

### §0.1 核心 hooks 与 MCP 本地 adapter 是两层抽象

必须明确区分：

- 核心层 hooks
  - 核心状态机拦截点
  - `ctl/hook/*` 回调与 resolve 通道
  - 决策合并、TTL、屏障态、状态提交边界
- MCP 侧 lifecycle adapter
  - 消费 `ctl/*` / `ctl/hook/*` 的领域逻辑
  - 例如 DAG gate、Command Validation、HTTP 策略桥接、Prompt 审核、Reviewer Agent 验收

两者不能混写：

- 核心层只提供能力，不实现具体业务规则
- MCP 侧可以实现任意策略，但不能篡改核心状态机

### §0.2 三大职责

1. **事前拦截（Before）**
   - 在 session 启动、turn 开始、task 启动、tool 调用前拦截
   - 返回 `allow / deny / wait / modify`
   - 可附带工具可见性约束

2. **事中监控（During）**
   - 在状态跃迁、周期进度、关键里程碑、tool 调用后触发
   - 返回 `continue / warn / abort`
   - 默认不阻塞主执行太久

3. **事后审查（After）**
   - 在 turn 完成、任务声称完成、失败、进程退出时拦截
   - 返回 `approve / reject / escalate`
   - `escalate` 后必须通过 `ctl/hook/resolve` 回传最终决策

### §0.3 核心与 MCP 的关系

```text
agent 状态机关键点
  -> core hook point
  -> ctl/hook/* 回调已订阅的 MCP 工具
  -> MCP 工具返回决策
  -> core 决定是否放行 / 中断 / 等待 / 提交 / 进入 pending_hook_review
```

边界固定为：

- 核心层负责“在哪里拦截”
- MCP 工具负责“拦截后怎么判断”
- 核心层不实现 DAG 规则、命令校验规则、审批规则、偏离检测规则
- MCP 工具不直接提交核心终态，只能通过 hook decision 影响提交

### §0.4 三层身份

hooks 文档必须写死三层身份，避免把 binary 身份和 agent 路由混淆：

| 层次 | 主键 | 含义 |
| --- | --- | --- |
| lease 身份 | `LeaseKey{instance_id, generation}` | 标识一个已注册的 MCP binary 实例 |
| agent-scoped payload | `agent_id`、`thread_id`、`turn_id`、`wakeup_id` | 标识当前 hook 操作的是哪个 agent / turn / 任务上下文 |
| bootstrap hint | `GO_AGENT_CTL_AGENT_ID` 等 env | 仅用于 bootstrap 提示，不是运行时路由主键 |

规则：

- lease 永远表示 binary 身份，不表示某个具体 agent
- 运行时路由只看 hook payload 中的 `agent_id` / `thread_id` / `turn_id`
- bootstrap env 的 `agent_id` 只是 boot hint，不能当作控制面路由主键

### §0.5 一个关键约束

必须写死下面这条规则：

- **agent 说“我完成了”不等于任务已经完成**

正确语义是：

1. agent 提交完成意图
2. 核心层触发 `After` hook
3. 订阅该 hook 的 MCP 工具审查结果
4. 若返回 `approve`，核心层才提交完成态
5. 若返回 `escalate`，核心层进入 `pending_hook_review`
6. 只有收到 `ctl/hook/resolve(approve|reject)`，核心层才结束该 pending review

---

## §1 三种状态的 Hook

### §1.1 事前（Before）

Before hook 是开始前的 gate，核心层在动作尚未提交前同步拦截。

主要 hook 点：

- `agent.session.start`
- `agent.turn.before`
- `agent.tool.before`
- `agent.task.starting`

语义：

- `agent.session.start`
  - session / 进程启动前初始化工作环境
  - 适合做目录准备、环境变量注入、凭据校验、缓存预热
- `agent.turn.before`
  - turn 即将开始前拦截
  - 适合做前置条件检查、预算检查、agent 归属检查、工具可见性过滤
- `agent.tool.before`
  - tool 调用前拦截
  - 适合做命令审批、路径白名单、资源额度检查、白名单外工具阻断
- `agent.task.starting`
  - 语义化任务启动入口
  - 可映射到 `agent.turn.before`，但保留独立 topic 供外部控制器消费

`BeforeHookResponse` 扩展为：

```text
Decision:     allow | deny | wait | modify
AllowedTools: [工具白名单]
DeniedTools:  [工具黑名单]
Patch:        可选参数修改
Reason:       可选理由
```

典型用途：

- 初始化工作环境
- 前置条件校验
- 权限 / 风险校验
- 工具白名单过滤
- 脚本阻断（Command Validation）

#### §1.1.1 工具可见性控制

Before hook 必须支持工具过滤能力。

场景：

- 系统里有 100 个工具
- 某个 agent 在某个 turn 或任务里只需要 3 个工具
- 其余 97 个工具对这个 agent 应当不可见

标准流程：

1. 外部控制器定义 `required_tools: [lsp_grep, lsp_file, lsp_edit]`
2. 核心层触发 `agent.turn.before`
3. 某个订阅该 hook 的 MCP 工具返回 `allowed_tools: [lsp_grep, lsp_file, lsp_edit]`
4. 核心层构建受限 manifest 或受限工具视图
5. agent 在当前 turn 只看到这 3 个工具
6. 若 agent 尝试调用 `code_run`
7. 核心层触发 `agent.tool.before`
8. Hook 返回 `deny`
9. 本次工具调用被阻断

### §1.2 事中（During）

During hook 是执行过程中的监控面。

主要 hook 点：

- `agent.turn.progress`
- `agent.state.change`
- `agent.tool.after`

语义：

- `agent.state.change`
  - 事件驱动
  - 只在状态跃迁时触发
- `agent.turn.progress`
  - 周期驱动或关键里程碑驱动
  - 默认周期 `30s`
- `agent.tool.after`
  - 单次工具调用后的局部后置检查
  - 适合过程观测、偏离检测、tool trace 收集

During 的所有检查统一走：

- `ctl/hook/check`

默认行为：

- 单次 callback 默认超时 `5s`
- `ctl/hook/check` 超时默认 `continue`
- 默认不因单次检查超时而卡死 agent 主路径

过程 hooks（Process Hooks）重点回答两类问题：

- 当前已经完成了什么任务
- 当前已经调用了什么工具

推荐最小载荷：

- `completed_tasks`
- `tool_calls`
- `tool_results`
- `artifacts`
- `files_changed`

### §1.3 事后（After）

After hook 是完成后的最终 gate。

主要 hook 点：

- `agent.turn.after`
- `agent.task.completing`
- `agent.turn.failed`
- `agent.process.exit`

语义：

- `agent.turn.after`
  - turn 结束后的总体审查
- `agent.task.completing`
  - 任务声称完成时的主 gate
  - agent 不能绕过它自报完成
- `agent.turn.failed`
  - 失败后的补偿、重试、人工接管决策
- `agent.process.exit`
  - 进程退出后的恢复、补偿、失败归因入口

After 返回值：

- `approve`
- `reject`
- `escalate`

`escalate` 的含义必须非常明确：

- 它不是完成
- 它不是 approve 的别名
- 它表示“核心层先不要提交终态，等待外部审查结果”
- 之后必须通过 `ctl/hook/resolve` 回传最终 `approve / reject`

### §1.4 Hook 点清单

| Hook 点 | 阶段 | 说明 |
| --- | --- | --- |
| `agent.session.start` | Before | 会话/进程启动前的环境初始化 gate |
| `agent.turn.before` | Before | turn 开始前 gate，也可返回工具白名单/黑名单 |
| `agent.tool.before` | Before | tool 调用前 gate，也负责对白名单外工具做二次拦截 |
| `agent.task.starting` | Before | 语义化任务启动入口 |
| `agent.turn.progress` | During | 周期进度 / 关键里程碑检查 |
| `agent.state.change` | During | 状态跃迁检查 |
| `agent.tool.after` | During | 单次工具调用后的局部检查 |
| `agent.turn.after` | After | turn 结束后的总体审查 |
| `agent.task.completing` | After | 任务声称完成时的最终 gate |
| `agent.turn.failed` | After | 失败后的审查与补偿 |
| `agent.process.exit` | After | 核心检测到进程退出后的恢复 / 补偿入口 |

### §1.5 AllowedTools 的合并规则与作用域

`allowed_tools` / `denied_tools` 是 Before hook response 的附加约束。

固定规则：

- 白名单合并使用交集
- 黑名单合并使用并集
- `agent.session.start` 的工具约束作用于整个 session
- `agent.turn.before` 的工具约束只作用于当前 turn
- turn 级白名单必须与 session 级白名单取交集，不能放宽 session 级限制
- `agent.tool.before` 不声明大范围工具可见性，只负责运行时细筛

因此工具控制分两层：

- 工具列表 / manifest 过滤 = 粗筛
- `agent.tool.before` = 细筛

### §1.6 During 的默认触发与能力协商

固定规则：

- `agent.state.change` 是事件驱动
- `agent.turn.progress` 是周期驱动或里程碑驱动
- 两者统一走 `ctl/hook/check`
- per-callback timeout 默认 `5s`
- 超时默认 `continue`

与 report 的关系：

- `ctl/report(progress)`、`ctl/report(diagnostic)` 默认不启用
- 只有协商到 `ctl.report.progress` / `ctl.report.diagnostic` capability 后才允许启用
- `ctl/hook/check` 和 `ctl/report(progress|diagnostic)` 是两层机制，不可混写

### §1.7 Turn 事件入口路径

turn hooks 的入口必须是核心层主动回调，不是 MCP 轮询，也不是额外旁路推送通道。

固定路径：

1. 核心层检测到 turn 即将开始、状态跃迁、完成、失败或进程退出
2. 核心层发起 `ctl/hook/before`、`ctl/hook/check` 或 `ctl/hook/after`
3. 已订阅的 MCP 工具返回决策
4. 核心层据此决定是否启动、继续、挂起、失败或完成

补充约束：

- MCP 工具不轮询 turn 事件
- 不新增独立的 hook 推送总线
- 对 wakeup dispatch 场景，MCP 工具只能通过 hook 返回里的 `mutations.dispatch_intent` 表达启动意图
- 实际启动 agent turn 的责任在核心层，不在 MCP 工具
- MCP 是被动服务，不主动发起 `SubmitTurn`

### §1.8 SubmitTurn 的 owner 与 `dispatch_intent`

`SubmitTurn` 的 owner 必须写死为核心层内部 orchestration service。

固定规则：

- MCP binary 不能主动发起 `SubmitTurn`
- `stdio` tool call 结果不是 turn 启动入口
- `ctl/report` 不是 dispatch 命令通道；它仍只负责 runtime / completion，以及协商后的 progress / diagnostic
- 若 hook 消费方希望驱动下一次 turn，只能在 `ctl/hook/before` 或 `ctl/hook/after` 返回的 `mutations.dispatch_intent` 中表达意图
- 核心层拿到 `dispatch_intent` 后，先做幂等校验、持久化和背压控制，再调用内部 `SubmitTurn`

`dispatch_intent` 最小字段：

- `target_agent_id`
- `thread_id`
- `wakeup_id`
- `submission` 或 `prompt_payload`
- `idempotency_key`

---

## §2 `ctl/*` 协议扩展

现有 v1 基线 9 个 `ctl/*` 方法不够支撑 hooks。

原因不是它们无用，而是它们没有提供“在状态提交前同步拿外部决策”和“在 `escalate` 后异步回传最终 verdict”的能力。

### §2.1 方法总览

v1 基线 9 个方法：

- `ctl/register`
- `ctl/heartbeat`
- `ctl/context`
- `ctl/event`
- `ctl/log`
- `ctl/approval/request`
- `ctl/report`
- `ctl/shutdown`
- `ctl/config/changed`

v2 hook 扩展 5 个方法：

- `ctl/hook/subscribe`
- `ctl/hook/before`
- `ctl/hook/check`
- `ctl/hook/after`
- `ctl/hook/resolve`

当前协议启用 hooks 时：

- 总方法数为 14
- 未协商到 `ctl.hook` 时，只有基线 9 个方法可用

### §2.2 与 v1 基线 9 个方法的关系

现有方法继续保留，各自职责不变：

- `ctl/register`、`ctl/heartbeat`
  - 仍负责 lease 与活体
- `ctl/context`
  - 仍只负责 lease-scoped runtime/config snapshot
  - hooks 不扩展 `ctl/context` scope
  - turn / wakeup 恢复必须由 MCP 工具自己的本地 store 按 `turn_id` / `wakeup_id` 处理
- `ctl/event`、`ctl/log`
  - 仍负责遥测 / 日志
- `ctl/report`
  - 仍负责 durable report
  - completion report 真正落终态前，必须先过 `After` hook
- `ctl/approval/request`
  - 仍可作为 hook 逻辑内部的审批手段
- `ctl/shutdown`、`ctl/config/changed`
  - 仍负责管理面回调

### §2.3 回调请求与返回的最小语义

`ctl/hook/subscribe` 最小语义：

- `lease`
- `subscription_id`
- `topics`
- `scope`
- `filters`
- `mode`

`ctl/hook/before` / `ctl/hook/check` / `ctl/hook/after` 最小语义：

- `hook_call_id`
- `subscriber_lease`
- `topic`
- `agent_id`
- `thread_id`
- `turn_id`
- `wakeup_id`
- `session_context`
- `task_context`
- `tool_context`
- `state_snapshot`
- `progress_snapshot`
- `completed_tasks`
- `tool_calls`
- `artifacts`
- `payload`
- `deadline_ms`

返回值最小语义：

- `decision`
- `reason`
- `patch` 或 `mutations`
- `allowed_tools`
- `denied_tools`
- `mode`
- `dispatch_intent`
- `retry_after_ms`
- `severity`
- `ttl_ms`

补充约束：

- `allowed_tools` / `denied_tools` 只对 Before 生效
- `ttl_ms` 主要用于 `After -> escalate` 场景
- `dispatch_intent` 只能作为 hook 返回中的 mutation 交给核心层
- 真正的 `SubmitTurn` 永远由核心层执行，MCP 工具不主动发起

### §2.4 多订阅方的合并规则

固定优先级：

- Before：`deny > wait > modify > allow`
- During：`abort > warn > continue`
- After：`reject > escalate > approve`

AllowedTools 合并：

- 白名单取交集
- 黑名单取并集
- `agent.turn.before` 不能放宽 `agent.session.start` 的工具限制

默认超时策略：

- `Before` / `After` 默认 fail-closed
- `During` 默认 fail-open 到 `continue`
- `ctl/hook/check` 的默认 callback timeout 是 `5s`

粗筛 / 细筛关系：

- 工具列表过滤是粗筛
- `agent.tool.before` 是细筛

### §2.5 `ctl/hook/resolve`

`ctl/hook/resolve` 是 `escalate` 之后的正式回传通道。

最小语义：

- `hook_call_id`
- `decision`：`approve | reject`
- `reason`
- `idempotency_key`
- `resolved_by`
- `resolved_at`

固定规则：

- 每个 pending hook review 都必须带 `TTL`
- `TTL` 到期后的默认决策是 `reject`
- 默认策略是 fail-closed
- `hook_call_id + idempotency_key` 构成 resolve 幂等键

恢复语义：

- 如果 resolve 到达时目标 session 已断开，核心层仍要把决策挂在 `hook_call_id` 上
- 如果 subscriber 断开后重新注册了新的 lease，核心层可按订阅关系重新 dispatch pending hook
- 如果直到 TTL 过期都没有有效 resolve，核心层自动按 `reject` 落终态

### §2.6 `process.exit` 的事件来源

`process.exit` 不是由 MCP 工具自己上报的 hook 事件。

固定来源：

- 核心层 agent 管理模块检测到进程退出
- 核心层据此生成 `agent.process.exit`
- 再通过 `ctl/hook/after` 回调订阅者

因此：

- `process.exit` 的 owner 是核心层
- 它属于 After 阶段的异常终态来源
- 订阅方可据此决定补偿、回收、重试或失败

### §2.7 `dispatch_intent` 的来源与执行边界

`dispatch_intent` 不是新 RPC 方法，而是 hook callback 返回里保留的标准 mutation。

固定来源：

- `ctl/hook/before` 返回 `modify + mutations.dispatch_intent`
- `ctl/hook/after` 返回 `approve|escalate + mutations.dispatch_intent`

固定边界：

- MCP 工具只能表达“希望核心启动一个 turn”
- 核心层才有权把 `dispatch_intent` 翻译成内部 `SubmitTurn`
- `dispatch_intent` 必须先落 durable 记录，再进入 dispatch 队列
- 同一 `idempotency_key` 的重复 dispatch 只能生效一次

---

## §3 核心层实现

### §3.1 核心层负责什么

核心层只负责 hook 基础设施，不负责 hook 业务判断。

核心层职责：

1. 在 agent 状态机关键位置放置 hook 拦截点
2. 维护 hook 订阅注册表
3. 通过 `ctl/hook/*` 回调已注册的 MCP 工具
4. 根据返回决策决定是否提交、挂起、中止或继续状态迁移
5. 对 `escalate` 进入 `pending_hook_review`
6. 对 `resolve`、TTL 和重连恢复做仲裁

### §3.2 Hook 拦截点应放在哪些位置

至少包括：

- session / process 启动前
- turn 开始前
- tool 调用前
- tool 调用后
- 状态跃迁时
- 周期进度点
- 完成前
- 失败前
- process exit 检测后

对当前实现的直接含义：

- `internal/platform/mcpcontrol/report_handlers.go` 不能再把 completion report 直接当成终态提交
- `OrchestrationService.CompleteTurn` 一类完成入口前必须先过 `After` gate
- `agent.process.exit` 必须由核心进程管理模块触发，不依赖工具自报
- `OrchestrationService.SubmitTurn` 只能由核心消费 `dispatch_intent` 后调用，不由 MCP binary 主动发起

### §3.3 代码放置

核心层 hook 基础设施固定放在：

- `internal/platform/hooks/`

建议职责拆分：

- `registry.go`
- `dispatcher.go`
- `merge.go`
- `points.go`
- `manager.go`
- `resolver.go`

对外契约放在：

- `internal/contract/hooks.go`

协议 DTO 扩展放在：

- `internal/dto/mcp/`

### §3.4 一个必要的状态语义

因为 `After` hook 会阻止终态直接提交，核心层必须支持“终态待审”的屏障态。

最少要有：

- `pending_hook_review`
- `awaiting_hook_decision`
- `subscriber_lost`

没有这层屏障，`escalate` 就会变成语义空洞。

---

## §4 MCP 工具侧消费

### §4.1 谁可以消费 hooks

可能的消费者包括：

- DAG 控制器
- Command Hook 执行器
- HTTP 策略桥接器
- Prompt 审核器
- Reviewer Agent / 验收 Agent 启动器
- 审计与合规采集器

任意一个 MCP 工具只要需要介入 agent 生命周期，都可以：

- `ctl/hook/subscribe`
- 在回调里读取自己需要的上下文
- 返回 allow / abort / approve 等决策
- 必要时再通过 `ctl/hook/resolve` 回传最终 verdict

### §4.2 Command Hooks（命令钩子）

定义：

- hook 被触发后，MCP 工具执行一个本地 Bash 脚本
- 例如运行 `npm run lint`
- 脚本返回非零退出码时，直接打断工具调用

推荐落点：

- `agent.tool.before`

### §4.3 HTTP Hooks（WebHook）

定义：

- hook 被触发后，MCP 工具向外部 URL 发送 JSON
- 外部系统返回决策
- MCP 工具把结果映射成 hook decision

注意：

- HTTP Hook 是 MCP 侧处理器模式，不是核心层直接出网

### §4.4 Prompt Hooks（提示词验证）

定义：

- hook 被触发后，MCP 工具把当前状态组装成 Prompt
- 发给大模型
- 大模型只回答 `Yes` 或 `No`
- MCP 工具再把 `Yes/No` 映射成 hook decision

约束：

- 只能作为 MCP 侧验证器
- 不得绕过核心层 hook gate
- 输出必须收敛成有限决策，不是自由文本

### §4.5 Agent Hooks（子代理验证）

定义：

- hook 被触发后，MCP 工具临时启动一个隔离的小 Agent
- 该 Agent 只负责验证
- 只有它验收通过，主 Agent 才继续或被认定完成

适用场景：

- 子代理验收
- 大改动独立复核
- 二次测试 / 二次安全检查

### §4.6 Process Hooks

过程 hooks 关注：

- 完成了什么任务
- 调用了什么工具

典型载荷：

- `completed_tasks`
- `tool_calls`
- `tool_results`
- `artifacts`
- `files_changed`

### §4.7 `ctl/context` 边界

hooks 文档必须保持下面这条边界：

- `ctl/context` 不因为 hooks 扩 scope
- `ctl/context` 仍只提供 lease-scoped runtime/config snapshot
- turn / task / wakeup 语义优先来自 hook payload，而不是 `ctl/context`
- 若某个 MCP 工具需要按 `turn_id` / `wakeup_id` 恢复关联，必须从自己的本地 store 恢复

---

## §5 代码组织

- `internal/platform/hooks/`
  - 核心层 hook 基础设施
- `internal/contract/hooks.go`
  - hook 接口定义
- `internal/dto/mcp/`
  - hook DTO 与 `ctl/hook/*` 常量扩展
- `cmd/mcp-orch/lifecycle/`
  - MCP 侧 hook 消费逻辑

补充约束：

- `internal/platform/hooks/` 不能依赖 `cmd/mcp-orch`
- `cmd/mcp-orch/lifecycle/` 不能反向定义核心 hook 接口
- `internal/platform/mcpcontrol/` 只做协议接线和 lease/peer 校验

---

## §6 与 DAG 的集成（可选消费场景）

DAG 只是 hooks 的一个消费场景，不是 hooks 能力存在的前提。

### §6.1 DAG node 开始

```text
node ready
  -> core 触发 agent.session.start
  -> core 触发 agent.task.starting / agent.turn.before
  -> DAG hook 检查依赖、工具白名单、审批要求
  -> allow 后才真正开始 turn
```

### §6.2 DAG node 执行中

```text
turn running
  -> core 周期触发 agent.turn.progress
  -> core 在状态跃迁时触发 agent.state.change
  -> DAG hook 检查是否按目标执行
```

### §6.3 DAG node 完成

```text
agent 说“任务完成”
  -> core 触发 agent.task.completing / agent.turn.after
  -> DAG hook 决定 approve / reject / escalate
  -> 若 escalate，等待 ctl/hook/resolve
  -> approve 后 core 才提交完成态
```

### §6.4 如果消费方是 `cmd/mcp-orch` DAG runtime，现有 SQL / store 接口需要重写

以下接口名和当前语义之间存在明显错位：

- `BindRunningTaskDagNodeTurn` / `BindRunningNodeTurn`
  - 当前要求节点已经是 `running`
  - 目标语义应改成在绑定 turn 时完成 `pending -> running`
- `UpdateRunningTaskDagNodeStatus` / `UpdateRunningNodeStatus`
  - 当前 SQL 实际在做 `pending -> running`
  - 名称与语义不一致，应拆成 dispatch/activate 和 running update 两类接口
- `CompleteTaskDagNode` / `CompleteNode`
  - 必须增加 `active_turn_id = $turn_id` 等保护
  - 未通过 After 审查前不得直接提交 `done|failed`
- `UpdateAwaitingVerifyTaskDagNodeStatus` / `UpdateAwaitingVerifyNodeStatus`
  - 应承接 `pending_hook_review` / `awaiting_verify`
  - 它是审查暂态，不是最终完成态
- `TouchRunningTaskDagNodeEvent` / `TouchRunningNodeEvent`
  - 继续作为 progress heartbeat 更新点
  - 但必须只对当前绑定的 `turn_id` 生效
- `ClaimDueTaskDagWakeups` / `ClaimDueWakeups`
  - 必须纳入全局 dispatch 背压
  - 不得在超出并发上限时继续 claim 新 wakeup
- `MarkTaskDagWakeupSent` / `MarkWakeupSent`
  - 只表示 `dispatch_intent` 已被核心接收
  - 不表示 turn 已经真正启动
- `BindTaskDagWakeupTurn` / `BindWakeupTurn`
  - 必须在核心 `SubmitTurn` 成功后再绑定
  - 必须带 `target_agent_id + wakeup_id + turn_id` 一致性保护
- `ListSentUnboundTaskDagWakeups` / `ListSentUnboundWakeups`
  - 只用于恢复 sent 但尚未绑定 turn 的 dispatch
  - 不应被当作常态轮询启动通道
- `ReclaimStaleDispatchingTaskDagWakeups` / `ReclaimStaleDispatchingWakeups`
  - 需要与 `max_concurrent_dispatches` 和 dispatch timeout 一起定义
  - 避免 sent / dispatching wakeup 永久悬挂

### §6.5 wakeup dispatch 背压

如果某个消费者采用 wakeup dispatch 模型，必须加背压：

- `max_concurrent_dispatches` 默认 `8`
- 超过上限时，新 dispatch 进入等待队列
- 背压 owner 是核心层 dispatch 队列，不是 MCP binary
- 只有拿到可用 dispatch 槽位后，核心层才消费 `dispatch_intent` 并调用 `SubmitTurn`
- 背压状态必须可观测

---

## §7 守卫和风险

### §7.1 同步 hook 可能阻塞状态机

风险：

- `Before` 和 `After` 都是同步 gate

守卫：

- 所有 hook callback 都必须带 deadline
- `Before` / `After` 默认 fail-closed

### §7.2 多个订阅方可能给出冲突决策

风险：

- 多个 MCP 工具同时订阅同一 hook 点

守卫：

- 固定优先级合并
- 每次决策都要记录来源

### §7.3 `After` 的 `escalate` 需要状态屏障

风险：

- 如果没有屏障态，`escalate` 没有落点

守卫：

- 引入 `pending_hook_review`
- 未通过审查前禁止提交终态

### §7.4 `During` hook 不能无限高频

风险：

- 高频 callback 带来很高成本

守卫：

- `agent.turn.progress` 默认 `30s`
- `agent.state.change` 只在跃迁时触发
- `ctl/hook/check` 超时默认 `continue`

### §7.5 hook 逻辑与核心层职责混淆

风险：

- 业务规则被写回核心层

守卫：

- 核心层只保留接口与调度
- 业务规则全部留在 MCP 工具侧

### §7.6 lease 与 pending hook 的关联丢失

风险：

- subscriber 断连
- lease 过期
- 某个 `pending_hook_review` 还在等待 resolve

守卫：

- 核心层必须把 pending hook 与 subscriber lease 显式关联
- subscriber 丢失时，把 pending hook 标记为 `subscriber_lost`
- subscriber 重连后允许重新 dispatch
- 永远不回来时，TTL 到期默认 `reject`

---

## 结论

P8 的 hooks 必须被定义为：

- 核心层提供的执行拦截接口
- 通过 `ctl/hook/*` 暴露给任意需要控制 agent 生命周期的 MCP 工具
- 以 Before / During / After 三种状态贯穿执行全过程
- `escalate` 后必须通过 `ctl/hook/resolve` 回传最终 verdict
- 工具可见性控制属于 Before 的正式能力
- `ctl/context` 不因 hooks 扩 scope
- MCP 工具是被动服务，不主动发起 turn

如果这些约束不先写死，后续实现就会再次滑回“在某个 MCP binary 本地补一层 adapter”的旧方向；那样得到的只是业务适配代码，不是真正的核心层 lifecycle hooks。
