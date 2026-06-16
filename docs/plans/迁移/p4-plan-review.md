# P4 拆分方案审查报告

## 1. 总体评价

方案方向基本成立，核心分层也与 `v3-migration-plan.md` §3、§4.5 一致：统一 provider 语义层、保留两套 provider-specific driver、把 turn prepare 和 thread/history 逻辑从 adapter 中抬出。

当前方案存在 4 个实质性缺口：

- 覆盖面口径不稳定。`49 文件 / 13,231 行` 只接近一个较窄的 provider/unified 来源口径，不覆盖 T6/T7 在摘要里已经承诺的全部 turn/thread service 迁移面。
- 接口归宿不完整。`SendDynamicToolResult`、`RespondError`、`SetEventHandler`、`Running`、`Kill`、`GetPort` 在 V3 接口草图中没有明确落点。
- 代码量目标偏乐观。`provider/codexapp`、`codexadapter` 分流、`module/turn`/`module/thread` 的文档目标值，整体上明显高于 `4,000-5,500`。
- 依赖规则存在自相矛盾。`module/turn` 被要求不 import `provider/*`，但 `v3-module-migration-details.md` 又写明它复用 `provider/unified` 的 `TurnRequest`。

结论：该拆分方案可执行，但按当前摘要直接开工会在接口层和工作量评估上产生返工；需要先补齐 contract 和范围基线。

## 2. V2 文件覆盖完整性

### 已覆盖

LSP 已确认题面列出的代表性 V2 文件真实存在，包括：

- `go-agent-v2/legacy-agentsdk/agentcore/client.go`
- `go-agent-v2/legacy-agentsdk/agentcore/types.go`
- `go-agent-v2/legacy-agentsdk/claude/client.go`
- `go-agent-v2/legacy-agentsdk/claude/client_cli_transport.go`
- `go-agent-v2/legacy-agentsdk/claude/client_cli_events.go`
- `go-agent-v2/legacy-agentsdk/claude/capabilities.go`
- `go-agent-v2/legacy-agentsdk/claude/session.go`
- `go-agent-v2/legacy-agentsdk/claude/history_backend.go`
- `go-agent-v2/legacy-agentsdk/codex/client.go`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver.go`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_health.go`
- `go-agent-v2/legacy-agentsdk/codex/rollout_reader.go`
- `go-agent-v2/internal/apiserver/codexadapter/adapter.go`

按 LSP + 生产文件行数统计，有 3 个不同口径：

| 口径 | 文件数 | 行数 | 说明 |
|---|---:|---:|---|
| `v3-migration-plan.md` §4.5 高层 P4 source：`agentcore/*` + `claude/*` + `codex/*` + `codexadapter/*` + `provider_registry.go` | 43 | 11,018 | 这是高层迁移批次写法 |
| `v3-module-migration-details.md` 中 `provider/unified` 迁移来源，排除 `doc.go`：再加 `commonadapter/*` + `service/lifecycle/*` + `service/runtime/*` | 49 | 13,040 | 这是最接近摘要 `49 文件` 的实际口径 |
| 若把摘要里的 T7 真正算进去，再加 `service/archive/command/history/listing/messages/rollout`，排除 `doc.go` | 59 | 15,803 | 这才接近 8 子任务实际承诺的范围 |

关键结论：

- `49 文件` 这个数字并非完全错误，但它只对应 `provider/unified` 的窄口径。
- 一旦把 T6/T7 摘要里已经声明的 turn/thread service 迁移一起算入，实际范围明显超过 49 文件。

### 遗漏文件

相对摘要式拆分，以下 V2 生产文件没有被明确点名，但从 V2 实码和迁移文档看，不能省略：

- Claude 侧遗漏：
  - `go-agent-v2/legacy-agentsdk/claude/session_log_watcher.go`
- Codex 侧遗漏：
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_jsonrpc_id.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_filter.go`
  - `go-agent-v2/legacy-agentsdk/codex/events.go`
  - `go-agent-v2/legacy-agentsdk/codex/interface.go`
- T6 相关但摘要未展开的 turn/common 源：
  - `go-agent-v2/internal/apiserver/commonadapter/common.go`
  - `go-agent-v2/internal/apiserver/commonadapter/skills.go`
  - `go-agent-v2/legacy-agentsdk/service/common/turn_common_paths.go`
  - `go-agent-v2/legacy-agentsdk/service/prompt/turn_prompt_core.go`
  - `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go`
  - `go-agent-v2/legacy-agentsdk/service/support/interrupt_state.go`
  - `go-agent-v2/legacy-agentsdk/service/support/prompt_match.go`
  - `go-agent-v2/legacy-agentsdk/service/tracker/*.go`
- T7 摘要只写了 `history/archive/listing/command`，但 V2 实际还需要：
  - `go-agent-v2/legacy-agentsdk/service/messages/thread_messages_logic.go`
  - `go-agent-v2/legacy-agentsdk/service/rollout/thread_messages_hydration_core.go`
  - `go-agent-v2/legacy-agentsdk/service/rollout/thread_messages_rollout_core.go`

补充统计：

- 上述未展开的 T6 turn/common 补充源，排除 `doc.go` 后还有 11 个生产文件、2,250 行。
- 上述 T7 额外遗漏的 `messages + rollout`，排除 `doc.go` 后还有 3 个生产文件、670 行。

## 3. 接口设计可行性

### V2 Client 方法归宿表

| V2 `agentcore.Client` 方法 | 摘要中的 V3 归宿 | 评估 |
|---|---|---|
| `SpawnAndConnect` | `Driver.StartSession` | 可行。实际 V2 的 `prompt` 在 Claude/Codex 启动阶段都未承担稳定业务语义，V3 应把 prompt 从 session 启动中移除，保留在 turn 层。 |
| `Submit` | `Session.StartTurn` | 可行。 |
| `SendCommand` | `Session.Interrupt` ? | 不完整。V2 里它至少承载 `/interrupt`、`/compact`、Claude `/model`；单一 `Interrupt` 无法覆盖。需要显式 `SessionControl`/`Command` 子接口，或把 `compact`/`model` 收敛成结构化 thread config。 |
| `SendDynamicToolResult` | 未定义 | 不可行。V2 在 `internal/apiserver/server_dynamic_tools.go` 有真实调用；V3 必须提供 tool-call result responder。 |
| `RespondError` | 未定义 | 不可行。V2 在 `internal/apiserver/server_dynamic_tools.go` 有真实调用；V3 必须提供 tool-call error responder。 |
| `ListThreads` | `Session.ListThreads` | 语义存疑。Claude 侧当前直接 `unsupported`；Codex 侧只返回当前已绑定 thread，并不是全局 provider listing。可做 optional capability，但不能替代 thread service 自身的 store 聚合逻辑。 |
| `ResumeThread` | `Driver.ResumeSession` | 可行。 |
| `ForkThread` | `Session.ForkThread` | 形式上可行，但证据偏弱。当前 Claude foundation batch 和 Codex app-server 都是 `unsupported`；应作为 optional capability，而不是 P4 核心 happy path。 |
| `Shutdown` | `Session.Close` | 只覆盖了一半。 |
| `Kill` | `Session.Close` ? | 不可直接合并。V2 recovery 路径先 `Shutdown` 再 fallback `Kill`；V3 至少要保留内部 hard-stop 语义。 |
| `Running` | 未定义 | 不可行。V2 `internal/runner/manager.go`、`manager_submission.go` 依赖它做状态收敛和同步恢复。该能力不一定属于公共 provider contract，但必须有 runtime/orchestration 侧归宿。 |
| `SetEventHandler` | 未定义 | 不可行。V2 `internal/runner/manager_launch.go`、`manager_recover.go` 明确依赖它接管 provider 事件。V3 必须改成构造期注入 dispatcher、事件流句柄，或 `TurnHandle`/`Session` 暴露订阅面。 |
| `GetPort` | 未定义 | 不可直接丢失。V2 runner 列表和 recovery 日志使用它；应迁到 orchestration/runtime metadata，而不是塞进 provider 业务接口。 |
| `GetThreadID` | `Session.ThreadID` | 可行。 |

### 问题点

- 事件模型缺口是当前最大 blocker。V2 用 `SetEventHandler` 推送统一事件，摘要里的 `Driver/Session` 草图没有任何事件出口，`TurnHandle` 也未定义。
- tool-call 回包缺口是第二个 blocker。`SendDynamicToolResult` 和 `RespondError` 在 V2 不是死代码，而是 dynamic tool 主路径的一部分。
- `Kill` 与 `Running` 不适合继续暴露在统一业务接口上，但它们不能消失，必须下沉到 orchestration/runtime actor。
- `SendCommand` 不应该原样保留为字符串命令包；但把它只收敛成 `Interrupt` 会丢掉 `compact` 和 `model` 调整能力。
- `SpawnAndConnect` 的 `prompt` 已被实码证明不是稳定的 session 启动字段，V3 继续把它放在 `StartSessionRequest` 会污染边界。

## 4. 代码量预估

### 合理项

- `Claude` 侧缩减方向本身合理。V2 Claude 生产代码共 2,835 行；其中 capability、event map、history、session watcher 都可拆出。`v3-module-migration-details.md` 给 `provider/claudecli` 的目标是 `800-1100`，说明压缩是可行的。
- `49 文件` 口径若仅指 `provider/unified` 的窄来源，当前实测为 49 个非 `doc.go` 生产文件、13,040 行，与摘要 `13,231` 基本同量级，只是代码已有轻微漂移。

### 过于乐观项

- `Claude driver V2 2,520 行 -> V3 <= 800 行`：
  - 如果 `<= 800` 指整个 `provider/claudecli` 包，则比模块迁移文档给的 `800-1100` 还更激进，处于下边界。
  - 如果 `<= 800` 只指 driver 核心，不含 `history`/`event_map`/session watcher，则需要在任务定义中写清楚，否则会产生统计口径歧义。
- `Codex driver V2 2,424 行 -> V3 <= 900 行`：
  - V2 Codex 生产代码实际为 3,747 行；即使剔除 `event_map` 和 `history`，驱动/transport/protocol/runtime 核心仍然远高于 900。
  - 模块迁移文档给 `provider/codexapp` 的目标是 `1400-2000`；`<= 900` 只可能在非常窄的子任务口径下成立，不能代表 P4 的 Codex 迁移总量。
- `codexadapter V2 3,868 行 -> V3 ~600 行`：
  - 3,868 行这个 V2 值是准确的，正好对应 `codexadapter` 非 `doc.go` 生产代码。
  - 但 `v3-migration-plan.md` §6.1 已明确，`codexadapter` 会分流到 `provider/unified`、`module/thread`、`module/orchestration`、`platform/config`、`platform/rpc` 等多个包。
  - 因此 `~600` 只可能对应其中一个切片，不能代表 `codexadapter` 整体替代成本。
- `service V2 4,113 行 -> V3 ~500 行`：
  - 按 turn 相关真实来源估算，`commonadapter/common+skills`、`service/common`、`prompt`、`runtime`、`lifecycle`、`interrupt`、`support`、`tracker` 合计至少 21 个非 `doc.go` 文件、4,370 行。
  - 模块迁移文档给 `module/turn` 的目标是 `900-1300`，不是 `~500`。
  - 线程服务相关 `archive/command/history/listing/messages/rollout` 还有额外 10 个非 `doc.go` 文件、2,763 行。
- `P4 总量 4,000-5,500 行` 与模块迁移文档冲突：
  - 文档目标和至少为：`provider/unified 1400-2000` + `provider/claudecli 800-1100` + `provider/codexapp 1400-2000` + `module/thread 700-1000` + `module/turn 900-1300` + `module/orchestration 2200-3200`。
  - 合计区间是 `7,400-10,600`，明显高于 `4,000-5,500`。

## 5. 依赖方向可行性

### 可行项

- `provider/claudecli` 不 import `store/*` / `module/*` / `tool/*` 是可行的。V2 legacy claude provider slice 实际上已经没有 `internal/store/*`、`internal/module/*`、`pkg/toolsdk/*` 依赖，主要是 CLI transport、事件解析和 history backend。
- `provider/unified` 不 import `claudecli` / `codexapp` 也是可行的。V2 已有对象式 `ProviderRegistry` 先例；V3 可以通过 `fx` value-group 或 map builder 构造 driver registry，而不是在 unified 包里直接引用具体 driver 包。
- `module/turn` 不 import `provider/*` 在技术上可行。V2 `service/runtime/turn_runtime_adapters.go` 已证明 turn 层可以只依赖一组注入函数和窄接口，而不是直接 import provider 实现。

### 需要调整项

- `module/turn` 当前文档写法与此规则冲突。`v3-module-migration-details.md` 第 2 节明确写了“复用 `provider/unified` 的统一 `TurnRequest`”；如果继续这样设计，`module/turn` 必然 import `provider/unified`。
- 解决方式应为：把 `TurnRequest`、`CapabilitySet`、provider error 分类、session-control contract 上提到 `internal/contract` 或 `internal/dto`，让 `provider/unified` 和 `module/turn` 共同依赖，而不是互相依赖。
- `thread/compact/start`、`thread/undo`、realtime 一类 provider-specific 会话控制，不能直接塞回 `module/thread`；应通过 contract-level `SessionControl` facade 下沉到 `provider/unified`。
- manifest 与 tool registry 必须同源决策。`v3-module-migration-details.md` 已明确“manifest/tool list 与 registry 的边界必须一致”，否则 provider 看到的工具面会与实际二进制能力漂移。

## 6. 工厂模式评估

### 可行项

- 当前仓库已经有 `internal/platform/bus/typed.go` 这种基于泛型的 `TypedEmitter[T event.Event]` 包装，说明“泛型 + `kelindar/event`”这条路线本身没有问题。
- driver registry 采用“名称 -> factory”映射也没有问题，只要它是构造期生成的对象，而不是全局可变注册表。

### 风险项

- `EventMapper[R, T]` 以“单一 `T` 输出类型”建模并不适合当前事件面：
  - Claude `system:init` 一次会映射出多个事件。
  - Claude/Codex 的一个 raw event 可能落到不同 typed event struct。
  - `kelindar/event` 的匹配键是 `Type() uint32`，不是字符串 topic。
  - 更合适的抽象是 `Map(raw R) []event.Event`，或 `Map(raw R, emit func(event.Event)) error`。
- 如果 `EventMapper` 输出仍然是 V2 风格的 `agentcore.Event{Type string, Data json.RawMessage}`，就会直接偏离 P2/P4 的 typed event 总约束。
- `RegisterDriver(name, factory)` 若实现成包级全局注册，会与 `fx` 注入、测试隔离、生命周期管理冲突。推荐做法是：
  - 各 driver 模块通过 `fx.Out` 输出 `DriverFactory`。
  - `unified.Module` 构造只读 registry。
- `BuildManifest(enabled []ToolFamily)` 的签名过静态：
  - 文档要求 `cmd/mcp-ida` 只按线程能力挂载。
  - `provider/unified` 文档又明确依赖 `thread config resolver`。
  - 当前 Claude 的 MCP config 构造还依赖 `agentID`、`cwd`、tool names/groups、可选 `AGENT_APISERVER_URL`。
  - 因此 manifest builder 至少应接收 session/thread 级上下文，而不是只接收 `[]ToolFamily`。

## 7. 结论与调整建议

### Blocker

- 在开始 T1 之前，先补一版完整 contract，明确以下缺口的 V3 归宿：
  - 事件出口，替代 `SetEventHandler`
  - tool-call result/error responder，替代 `SendDynamicToolResult` / `RespondError`
  - runtime liveness 与 hard-stop，替代 `Running` / `Kill`
  - runtime metadata，替代 `GetPort`
- 重新定义统计口径：
  - 如果继续使用 `49 文件 / 13k 行`，必须明确这是 `provider/unified` 的窄口径。
  - 如果 T6/T7 仍是 P4 交付范围，则总源面必须按更大的口径重算。
- 修正文档冲突：
  - `module/turn` 不 import `provider/*`
  - `TurnRequest`/`CapabilitySet` 不再放在 `provider/unified`
- 把 `messages`、`rollout` 明确纳入 T7；否则 thread history 迁移不完整。

### Improvement

- 把 T4 从“Codex app-server driver <= 900 行”改为更清晰的三段：
  - transport/protocol
  - recovery/health
  - history
- 把 T6 进一步显式化，至少单列以下来源，不再只写“两层 turn 构造”：
  - `commonadapter`
  - `service/common`
  - `service/prompt`
  - `service/runtime`
  - `service/interrupt`
  - `service/support`
  - `service/tracker`
- 把 driver registry 设计为 `fx` 组装配，而不是包级 `RegisterDriver`。
- 把 `EventMapper` 改为“异构 typed event 发射器”，不要强行统一成单一泛型输出类型。
- 把 manifest builder 设计成 `BuildManifest(ctx ManifestContext)` 或等价形式，显式接收线程能力、cwd、二进制定位和必要 env。

## 8. 修正版复审（`p4-execution-plan.md`）

## 8.1 总体结论

修正版已吸收大部分上一轮审查意见：

- B2、B3、B4 已基本修正。
- I1-I5 已全部纳入执行方案。
- 主要残留问题集中在 B1 的 tool-call responder 设计，以及统计口径里仍混有旧的行数计数方式。

当前结论不是“方案仍不可用”，而是：

- 可以作为新的 P4 执行基线继续细化。
- 但在开始实现前，仍应先修正 2 个文档级残留：
  - `ToolCallResponder` 不能只用 `callID` 建模回包。
  - T7 的逐文件行数和小计需要按当前 LSP 口径刷新一次。

## 8.2 B1-B4 复核

| 项 | 复核结论 | 说明 |
|---|---|---|
| B1 接口缺口 | 部分修正 | `SetEventHandler`、`Running`、`Kill`、`GetPort`、`SendCommand` 都给了结构化归宿；但 `ToolCallResponder.RespondResult/RespondError` 只接收 `callID`，未覆盖 V2 的 `requestID`/response-id 语义。 |
| B2 统计口径 | 基本修正 | 已明确废弃 `49 文件 / 13,231 行` 总口径，并改为 `59 文件 / 15,803 行` 执行口径；T6/T7 也已显式展开。 |
| B3 依赖方向 | 已修正 | 通过把 provider-neutral DTO 上提到 `internal/dto/provider/`，消除了 `module/turn` 依赖 `provider/unified` 的旧矛盾。 |
| B4 代码量口径 | 已修正 | 已区分“模块迁移文档对齐口径”和“本批直接改动口径”，旧版 `4,000-5,500` 已作废。 |

B1 的残留点需要单独指出：

- V2 `agentcore.Event` 自带 `RequestID *int64`、`RespondFunc`、`RespondResultFunc`。
- V2 成功回包路径是 `SendDynamicToolResult(callID, output, requestID *int64)`。
- V2 失败回包路径是 `RespondError(id int64, code, message)`，这里的 `id` 是 transport response id，不是 `callID`。
- 修正版把两者统一成：
  - `RespondResult(ctx, callID, output)`
  - `RespondError(ctx, callID, code, msg)`
- 这个抽象会丢掉 request-scoped response handle，无法完整表达 Codex JSON-RPC 的 error/result 回包路径。

建议把 B1 再收紧为以下任一形式：

- `ToolCallResponder.RespondResult(ctx, callID string, output string, resp ResponseRef) error`
- `ToolCallResponder.RespondError(ctx, resp ResponseRef, code int, msg string) error`
- 或把 request-scoped error responder 与 tool-call result responder 拆成两组 contract。

## 8.3 I1-I5 复核

| 项 | 复核结论 | 说明 |
|---|---|---|
| I1 EventMapper 改异构发射器 | 已纳入 | 已改为 `EventTranslator func(raw RawProviderEvent, publish func(ev event.Event))`，不再假设单 raw 对单 typed 输出。 |
| I2 driver registry 改 fx 组装 | 已纳入 | 已明确 `fx.Out`/`group:"drivers"`，不再使用包级 `RegisterDriver`。 |
| I3 manifest builder 改 `ManifestContext` | 已纳入 | 已把 `AgentID`、`CWD`、`ThreadCaps`、`BinaryDir`、`Env` 纳入 builder 输入。 |
| I4 Codex driver 拆三段 | 已纳入 | T4a/T4b/T4c 已显式化，transport/recovery/history 边界清晰。 |
| I5 T7 补入 `messages + rollout` | 已纳入，但表内行数需刷新 | 范围已纳入，但 `§4.5` 的逐文件行数与当前 V2 文件实际总行数不一致。 |

I5 的当前 LSP 总行数如下：

| V2 文件 | `p4-execution-plan.md` 写值 | 当前 LSP 总行数 |
|---|---:|---:|
| `service/messages/thread_messages_logic.go` | 157 | 191 |
| `service/rollout/thread_messages_hydration_core.go` | 281 | 163 |
| `service/rollout/thread_messages_rollout_core.go` | 232 | 319 |
| 小计 | 670 | 673 |

结论：

- I5 在“是否纳入 scope”这个问题上已修正。
- 但在“逐文件行数是否准确”这个问题上仍有残留误差。

## 8.4 统计口径复核

文件集合层面，修正版已经与上一轮审查结论一致：

- `49 文件` 只保留为 `provider/unified` 窄口径。
- P4 总执行面使用 `59 文件`。
- T6 显式覆盖 19 个 turn/common 源文件。
- T7 显式覆盖 10 个 thread/history 源文件。

行数层面，仍有一个口径残留：

- 文档中的 `15,803`、`2,763`、`670` 延续的是上一轮基于 newline-count 的汇总结果。
- 本轮按 LSP 读取到的当前文件总行数，T7 的 10 个显式文件小计为 2,773，不是 2,763。
- 因此“文件范围已修正”可以判定为成立，但“所有行数都与当前 LSP 口径严格一致”不能判定为完全成立。

建议：

- 保留 `59 文件` 作为执行范围基线。
- 在实现前再做一次统一的 LSP 行数重算，把 `15,803` 及各子任务小计刷新到同一口径。

## 8.5 依赖方向复核

当前方案中的依赖方向已无上一轮那种直接矛盾：

- `module/turn` 改为只依赖 `contract` + `dto/provider`，不再直接依赖 `provider/unified`。
- `provider/unified` 不再承担 provider-neutral DTO 的宿主角色。
- `provider/unified` 通过 `fx` 注入收集 driver，不需要直接 import `claudecli` / `codexapp`。
- `dto/provider` 被明确要求不 import `provider/*` 或 `module/*`。

本轮未发现新的文档级依赖冲突。

## 8.6 代码量复核

修正版已经与 `v3-module-migration-details.md` 的包级目标对齐：

| V3 包 | 模块迁移文档 | 修正版 |
|---|---:|---:|
| `provider/unified` | `1400-2000` | `1400-2000` |
| `provider/claudecli` | `800-1100` | `800-1100` |
| `provider/codexapp` | `1400-2000` | `1400-2000` |
| `module/turn` | `900-1300` | `900-1300` |
| `module/thread` | `700-1000` | `700-1000` |
| `module/orchestration` | `2200-3200` | `2200-3200`（全包归宿口径） |

结论：

- 包级总归宿口径 `7,400-10,600` 与模块迁移文档一致。
- 直接改动口径 `6,000-8,700` 也不再与包级总量冲突。
- Agent 分配表的总量 `~8,000` 落在直接改动口径内部，整体合理。

## 8.7 复审结论

修正版相较旧版已明显收敛，当前状态可概括为：

- 结构性问题已基本解决：B2、B3、B4 通过。
- Improvement 已全部进入执行方案：I1-I5 均有明确落点。
- 剩余问题主要是 2 个文档修订项，不再是整案级返工：
  - B1 的 `ToolCallResponder` 需要补回 request-id/response-ref 语义。
  - 统计表的若干行数需要按当前 LSP 口径刷新。

建议判定：

- 可继续作为 P4 实施方案。
- 在进入实现前，先做一次小修订，补齐 B1 responder 标识语义，并刷新 T7 及总量统计表。

---

## 9. 波次 2 审查（T3 Claude + T4 Codex）

### 编译与守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过（`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.061s`）

### import 方向

注：第 2 条白名单未包含 `go.uber.org/fx`。本节按第 8 条将 `module.go` 记为“条件通过”，单独在 `fx 范围` 小节复核；除此之外仍按白名单字面检查。

| 文件 | 违规 import | 状态 |
|---|---|---|
| `internal/provider/claudecli/config.go` | 无 | 通过 |
| `internal/provider/claudecli/driver.go` | 无 | 通过 |
| `internal/provider/claudecli/event_map.go` | 无 | 通过 |
| `internal/provider/claudecli/history.go` | 无 | 通过 |
| `internal/provider/claudecli/history_model.go` | 无 | 通过 |
| `internal/provider/claudecli/module.go` | `go.uber.org/fx`（仅第 8 条例外） | 条件通过 |
| `internal/provider/claudecli/session.go` | 无 | 通过 |
| `internal/provider/claudecli/session_events.go` | 无 | 通过 |
| `internal/provider/claudecli/transport.go` | 无 | 通过 |
| `internal/provider/claudecli/transport_config.go` | 无 | 通过 |
| `internal/provider/codexapp/driver.go` | 无 | 通过 |
| `internal/provider/codexapp/event_map.go` | 无 | 通过 |
| `internal/provider/codexapp/history.go` | 无 | 通过 |
| `internal/provider/codexapp/history_rollout.go` | 无 | 通过 |
| `internal/provider/codexapp/module.go` | `go.uber.org/fx`（仅第 8 条例外） | 条件通过 |
| `internal/provider/codexapp/recovery.go` | 无 | 通过 |
| `internal/provider/codexapp/session.go` | 无 | 通过 |
| `internal/provider/codexapp/support.go` | 无 | 通过 |
| `internal/provider/codexapp/transport.go` | `github.com/gorilla/websocket` | 违规 |
| `internal/provider/codexapp/transport_helpers.go` | 无 | 通过 |

结论：按白名单字面执行，`codexapp/transport.go` 明确违规；`module.go` 是否计入违规取决于是否接受第 8 条对第 2 条的例外解释。

### 行数

| 文件 | 行数 | 最长函数 | 状态 |
|---|---:|---|---|
| `internal/provider/claudecli/config.go` | 94 | `stringMap`（21 行） | 通过 |
| `internal/provider/claudecli/driver.go` | 127 | `(*driver).start`（48 行） | 通过 |
| `internal/provider/claudecli/event_map.go` | 131 | `translateTurnEvent`（31 行） | 通过 |
| `internal/provider/claudecli/history.go` | 135 | `(*historyBackend).ReadHistory`（30 行） | 通过 |
| `internal/provider/claudecli/history_model.go` | 26 | 无函数 | 通过 |
| `internal/provider/claudecli/module.go` | 8 | 无函数 | 通过 |
| `internal/provider/claudecli/session.go` | 364 | `(*session).stop`（36 行） | 通过 |
| `internal/provider/claudecli/session_events.go` | 265 | `decodeAssistantEvent`（30 行） | 通过 |
| `internal/provider/claudecli/transport.go` | 220 | `newTransport`（31 行） | 通过 |
| `internal/provider/claudecli/transport_config.go` | 227 | `buildCLIArgs`（28 行） | 通过 |
| `internal/provider/codexapp/driver.go` | 178 | `startRemoteThread`（21 行） | 通过 |
| `internal/provider/codexapp/event_map.go` | 297 | `translateTurnEvent`（37 行） | 通过 |
| `internal/provider/codexapp/history.go` | 40 | `(*rolloutReader).ReadHistory`（21 行） | 通过 |
| `internal/provider/codexapp/history_rollout.go` | 98 | `readLocalRollout`（23 行） | 通过 |
| `internal/provider/codexapp/module.go` | 8 | 无函数 | 通过 |
| `internal/provider/codexapp/recovery.go` | 45 | `(*recoveryManager).Reconnect`（17 行） | 通过 |
| `internal/provider/codexapp/session.go` | 336 | `(*session).ListThreads`（25 行） | 通过 |
| `internal/provider/codexapp/support.go` | 35 | `withTimeout`（11 行） | 通过 |
| `internal/provider/codexapp/transport.go` | 327 | `(*transport).stopProcess`（22 行） | 通过 |
| `internal/provider/codexapp/transport_helpers.go` | 50 | `jsonRPCIDKey`（11 行） | 通过 |

### 工厂模式

- `claudecli/event_map.go` 通过 `dispatcher.Register(translateClaudeEvent)` 注册 `EventTranslator`，provider 侧未直接调用 `event.Publish`。
- `codexapp/event_map.go` 也定义了 `RegisterTranslators`，但 LSP 全局搜索未见任何调用；当前 translator 工厂已写出、未接线。
- `claudeCapabilities` 与 `codexCapabilities` 都使用 `dto.CapabilitySet` map，未使用 `iota`，该项通过。
- 以当前 LSP 抽查结果看，未发现 4 处以上应立即抽成统一工厂的长段重复块；重复主要停留在 provider-local 小辅助函数级别，不构成当前主阻塞项。

### 契约实现

- `claudecli/driver.go` 已实现 `contract.Driver`：`Name`、`StartSession`、`ResumeSession` 齐全，且有 `var _ contract.Driver = (*driver)(nil)`。
- `codexapp/driver.go` 具备 `Name`、`StartSession`、`ResumeSession` 三个方法；`go build` 已证明其方法集满足 `contract.Driver`，但未加显式编译期断言。
- `claudecli/session.go` 已实现 `contract.Session` 全部方法，且有 `var _ contract.Session = (*session)(nil)`。
- `codexapp/session.go` 已实现 `ThreadID`、`Capabilities`、`StartTurn`、`Interrupt`、`ListThreads`、`ForkThread`、`Configure`、`Close`、`ForceStop`；`go build` 证明方法集满足 `contract.Session`，但未加显式编译期断言。
- 不支持能力处理存在问题：`claudecli/session.go` 的 `ListThreads` / `ForkThread` 返回的是通用 `ErrNotSupported`，不是 capability-specific error。全仓 LSP 搜索未见 `CapabilityError` / `ErrCapability` / `unsupported capability` 之类统一错误类型，因此该条未满足硬约束。

### V2 对照

| V2 关键点 | V3 对应 | 结论 |
|---|---|---|
| Claude `client.go` `SpawnAndConnect` / `spawnWithResume` | `claudecli/driver.go` `StartSession` + `start`，以及 `transport_config.go` `launchCLI` | 核心覆盖：仍然负责启动 CLI、处理 resume、拉起读循环；差异是 V3 改为 manifest 驱动，不再走 V2 的 DynamicTools 注入路径。 |
| Claude `client_cli_transport.go` CLI 参数组装 | `claudecli/transport_config.go` `buildCLIArgs` | 核心覆盖：`-p`、`stream-json`、`model`、`system-prompt`、`permission-mode`、`effort`、`mcp-config`、`disallowedTools` 均保留；DynamicTools MCP 构建已被移除，符合第 7 条目标。 |
| Claude `client_cli_events.go` 事件解析 | `claudecli/session_events.go` + `claudecli/event_map.go` | 部分覆盖：核心 `system/assistant/user/result` 已覆盖为 raw event + typed turn/tool/agent 事件；V2 的 `rate_limit_event` 与 token telemetry 未迁入。 |
| Codex `client_appserver_transport.go` JSON-RPC 连接 | `codexapp/transport.go` | 核心覆盖：本地 `codex app-server` 拉起、WebSocket 连接、RPC call/notify、read loop、reconnect 都在 V3 中具备。 |
| Codex `client_appserver_events.go` 事件解析 | `codexapp/event_map.go` | 未接线：translator 映射已写，但 driver/session 未注册使用；运行时当前只在 `session.go` 中消费 `turn/completed`、`turn/aborted`、`connection.dead`，不能视为 V2 事件解析能力已完整落地。 |
| Codex `client_appserver_health.go` health check | `codexapp/recovery.go` | 部分覆盖：V3 只有 `app/list` 健康探针 + retry reconnect；V2 的 health snapshot、circuit breaker、`not_initialized` 跟踪、respawn 偏好均未迁入。 |
| Codex `rollout_reader.go` history | `codexapp/history.go` + `codexapp/history_rollout.go` | 基本覆盖：本地 rollout 定位、逐行解析、RPC fallback 已具备；但 V2 的 metadata 提取与 injected-noise trimming 未迁入。 |

### DynamicTools

- 对 `internal/provider/**/*.go` 的 `DynamicTools` 全仓 LSP 搜索结果为 0。
- V2 `go-agent-v2/legacy-agentsdk/codex/client_appserver_filter.go` 中的 `filterDynamicTools` deny-list 逻辑，在 V3 `internal/provider/` 下未发现对应实现；对 `filterDynamicTools`、`denyList`、`filtered denied dynamic tool` 的 LSP 搜索结果均为 0。
- 该项通过。

### fx 范围

- 在本次审查范围内，`go.uber.org/fx` 只出现在 `internal/provider/claudecli/module.go` 和 `internal/provider/codexapp/module.go`。
- `claudecli` / `codexapp` 其余 `.go` 文件零 `fx` import。
- 该项通过。

### 结论

需修正。

行动项：

- 修正 `internal/provider/codexapp/transport.go` 的白名单外依赖问题；若保留 `github.com/gorilla/websocket`，需要同步更新第 2 条白名单或重新下沉到允许层。
- 把 `codexapp/event_map.go` 的 translator 真正接入运行时，或删除这层未使用抽象并明确由其他层负责事件翻译；当前不能算完成 V2 事件解析迁移。
- 为 `claudecli` 的不支持能力返回明确 capability error，而不是通用 `ErrNotSupported`。
- 决定是否补齐 V2 丢失能力：Claude 的 `rate_limit_event` / token telemetry，Codex 的 health snapshot / circuit breaker / `not_initialized` 跟踪，以及 rollout metadata / injected-noise trimming。

---

## 10. 波次 2 返工后深度复审

### 编译与守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过（`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.450s`）

### 返工项复核

| # | 项 | 状态 | 备注 |
|---|---|---|---|
| C1 | gorilla/websocket 移除 | 通过 | `internal/provider/codexapp/transport.go` 已不再 import `github.com/gorilla/websocket`，现改为 `golang.org/x/net/websocket`。 |
| C2 | codex event_map 接线 | 通过 | `internal/provider/codexapp/module.go` 已有 `fx.Invoke(RegisterTranslators)`。 |
| C3 | codex 编译期断言 | 通过 | `internal/provider/codexapp/driver.go` 有 `var _ contract.Driver = (*driver)(nil)`；`internal/provider/codexapp/session.go` 有 `var _ contract.Session = (*session)(nil)`。 |
| C4 | claude CapabilityError | 通过 | `internal/provider/claudecli/session.go` 的 `ListThreads` / `ForkThread` 已返回 `dto.NewCapabilityError(...)`。 |
| C5 | claude event_map 接线 | 通过 | `internal/provider/claudecli/module.go` 已有 `fx.Invoke(RegisterTranslators)`。 |

注：C2/C5 只证明 `module.go` 已注册 translator，不等于运行时事件总线链路已打通；运行时接线问题见 “kelindar/event 契约”。

### import 全面扫描

注：`module.go` 中的 `go.uber.org/fx` 记为模块装配例外，不作为本节违规；其余按白名单字面检查。

| 文件 | import 列表 | 违规 |
|---|---|---|
| `internal/provider/claudecli/config.go` | `os[std], os/exec[std], path/filepath[std], strings[std], internal/dto/provider[ok], internal/platform/shared[ok]` | 无 |
| `internal/provider/claudecli/driver.go` | `context[std], log/slog[std], strings[std], internal/contract[ok], internal/dto/provider[ok], internal/provider/unified[ok]` | 无 |
| `internal/provider/claudecli/event_map.go` | `time[std], internal/dto/agent[ok], internal/dto/provider[ok], internal/dto/shared[ok], internal/dto/tool[ok], internal/dto/turn[ok], internal/provider/unified[ok]` | 无 |
| `internal/provider/claudecli/history.go` | `bufio[std], context[std], encoding/json[std], errors[std], fmt[std], os[std], path/filepath[std], strings[std]` | 无 |
| `internal/provider/claudecli/history_model.go` | 无 | 无 |
| `internal/provider/claudecli/module.go` | `go.uber.org/fx[module例外]` | 无 |
| `internal/provider/claudecli/session.go` | `context[std], encoding/json[std], errors[std], log/slog[std], reflect[std], strings[std], sync[std], syscall[std], internal/contract[ok], internal/dto/provider[ok], internal/platform/shared[ok], internal/provider/unified[ok]` | 无 |
| `internal/provider/claudecli/session_events.go` | `context[std], encoding/json[std], errors[std], io[std], strings[std], internal/dto/provider[ok]` | 无 |
| `internal/provider/claudecli/transport.go` | `bufio[std], bytes[std], errors[std], io[std], os[std], os/exec[std], sync[std], syscall[std], time[std]` | 无 |
| `internal/provider/claudecli/transport_config.go` | `encoding/json[std], fmt[std], os[std], strings[std], internal/dto/provider[ok]` | 无 |
| `internal/provider/codexapp/driver.go` | `context[std], encoding/json[std], errors[std], log/slog[std], os[std], strings[std], time[std], internal/contract[ok], internal/dto/provider[ok]` | 无 |
| `internal/provider/codexapp/event_map.go` | `encoding/json[std], strconv[std], strings[std], time[std], internal/dto/agent[ok], internal/dto/provider[ok], internal/dto/shared[ok], internal/dto/tool[ok], internal/dto/turn[ok], internal/provider/unified[ok]` | 无 |
| `internal/provider/codexapp/history.go` | `context[std], encoding/json[std], fmt[std], time[std]` | 无 |
| `internal/provider/codexapp/history_rollout.go` | `bufio[std], encoding/json[std], fmt[std], os[std], path/filepath[std], sort[std], strings[std]` | 无 |
| `internal/provider/codexapp/module.go` | `go.uber.org/fx[module例外]` | 无 |
| `internal/provider/codexapp/recovery.go` | `context[std], errors[std], log/slog[std], time[std], internal/platform/shared[ok]` | 无 |
| `internal/provider/codexapp/session.go` | `context[std], encoding/json[std], errors[std], log/slog[std], path/filepath[std], strings[std], sync[std], time[std], internal/contract[ok], internal/dto/provider[ok]` | 无 |
| `internal/provider/codexapp/support.go` | `context[std], encoding/json[std], strings[std], time[std]` | 无 |
| `internal/provider/codexapp/transport.go` | `context[std], encoding/json[std], errors[std], fmt[std], io[std], net[std], os[std], os/exec[std], strconv[std], strings[std], sync[std], sync/atomic[std], time[std], internal/platform/shared[ok], golang.org/x/net/websocket[非白名单]` | `golang.org/x/net/websocket` |
| `internal/provider/codexapp/transport_helpers.go` | `encoding/json[std], fmt[std], net[std], net/url[std], strconv[std], strings[std]` | 无 |

补充复扫结果：

- `internal/store/*` / `internal/module/*` / `internal/tool/*`：在 `claudecli` / `codexapp` 下均为 0 匹配。
- `claudecli` ↔ `codexapp` 互相 import：0 匹配。

### 行数与复杂度

| 文件 | 行数 | 最长函数 | 状态 |
|---|---:|---|---|
| `internal/provider/claudecli/config.go` | 94 | `stringMap`（21 行） | 通过 |
| `internal/provider/claudecli/driver.go` | 127 | `(*driver).start`（48 行） | 通过 |
| `internal/provider/claudecli/event_map.go` | 131 | `translateTurnEvent`（31 行） | 通过 |
| `internal/provider/claudecli/history.go` | 135 | `(*historyBackend).ReadHistory`（30 行） | 通过 |
| `internal/provider/claudecli/history_model.go` | 26 | 无函数 | 通过 |
| `internal/provider/claudecli/module.go` | 9 | 无函数 | 通过 |
| `internal/provider/claudecli/session.go` | 362 | `(*session).stop`（36 行） | 通过 |
| `internal/provider/claudecli/session_events.go` | 265 | `decodeAssistantEvent`（30 行） | 通过 |
| `internal/provider/claudecli/transport.go` | 220 | `newTransport`（31 行） | 通过 |
| `internal/provider/claudecli/transport_config.go` | 227 | `buildCLIArgs`（28 行） | 通过 |
| `internal/provider/codexapp/driver.go` | 180 | `startRemoteThread`（21 行） | 通过 |
| `internal/provider/codexapp/event_map.go` | 297 | `translateTurnEvent`（37 行） | 通过 |
| `internal/provider/codexapp/history.go` | 40 | `(*rolloutReader).ReadHistory`（21 行） | 通过 |
| `internal/provider/codexapp/history_rollout.go` | 98 | `readLocalRollout`（23 行） | 通过 |
| `internal/provider/codexapp/module.go` | 9 | 无函数 | 通过 |
| `internal/provider/codexapp/recovery.go` | 45 | `(*recoveryManager).Reconnect`（17 行） | 通过 |
| `internal/provider/codexapp/session.go` | 338 | `(*session).ListThreads`（25 行） | 通过 |
| `internal/provider/codexapp/support.go` | 35 | `withTimeout`（11 行） | 通过 |
| `internal/provider/codexapp/transport.go` | 323 | `(*transport).stopProcess`（22 行） | 通过 |
| `internal/provider/codexapp/transport_helpers.go` | 64 | `jsonRPCIDKey` / `websocketOrigin`（各 11 行） | 通过 |

### 接口完整性

- `contract.Driver` 方法集：`Name`、`StartSession`、`ResumeSession`。
- `contract.Session` 方法集：`ThreadID`、`Capabilities`、`StartTurn`、`Interrupt`、`ListThreads`、`ForkThread`、`Configure`、`Close`、`ForceStop`。
- `contract.TurnHandle` 方法集：`TurnID`、`Done`、`Err`。
- `contract.ToolCallResponder` 方法集：`RespondResult`、`RespondError`。
- `claudecli/driver.go` 已完整实现 `Driver`，且有编译期断言。
- `claudecli/session.go` 已完整实现 `Session`；`turnHandle` 已完整实现 `TurnHandle` 方法集。`ListThreads` / `ForkThread` 为不支持能力，现已返回 `dto.NewCapabilityError(...)`，不再是通用 error。
- `codexapp/driver.go` 已完整实现 `Driver`，且有编译期断言。
- `codexapp/session.go` 已完整实现 `Session`；`turnHandle` 已完整实现 `TurnHandle` 方法集。该实现的会话方法均为“支持”，因此未出现 CapabilityError 分支。
- 两个 provider 文件集中未见 `var _ contract.TurnHandle = ...` 的显式编译期断言，但方法集完整且通过编译。
- `ToolCallResponder` 不在 `Session` 合同内；本轮 wave2 provider 文件中未见对应实现。这不构成 `Driver` / `Session` 接口缺失，但说明 tool response bridge 不在本波次 surface 内。

### 事件翻译覆盖度

Claude `event_map.go` 声明的 raw → typed：

- `agent:launched` / `system:init` → `agentdto.AgentLaunched`
- `agent:stopped` → `agentdto.AgentStopped`
- `agent:failed` → `agentdto.AgentFailed`
- `turn:started` → `turndto.TurnStarted`
- `turn:input_received` → `turndto.TurnInputReceived`
- `assistant:message_delta` → `turndto.TurnOutputDelta`
- `turn:interrupted` → `turndto.TurnInterrupted`
- `turn:complete` → `turndto.TurnCompleted`
- `tool:use_begin` → `tooldto.ToolCallBegin`
- `tool:use_end` → `tooldto.ToolCallEnd`

Codex `event_map.go` 声明的 raw → typed：

- `thread/started` / `session.configured` → `agentdto.AgentLaunched`
- `thread/status/changed` → `agentdto.StateChanged`
- `shutdown.complete` / `shutdown_complete` → `agentdto.AgentStopped`
- `recovery.attempt` → `agentdto.AgentRecovering`
- `connection.dead` → `agentdto.AgentFailed`
- `turn/started` / `turn.started` → `turndto.TurnStarted`
- `turn/completed` / `turn.completed` / `turn/aborted` / `turn.aborted` → `turndto.TurnCompleted`
- `turn/interrupted` / `turn.interrupted` → `turndto.TurnInterrupted`
- `item/agentMessage/delta` / `message.delta` / `agent_message_delta` → `turndto.TurnOutputDelta`
- `item/reasoning/summaryTextDelta` / `item/reasoning/textDelta` / `reasoning.delta` → `turndto.TurnOutputDelta`
- `item/commandExecution/outputDelta` / `exec_output_delta` → `turndto.TurnOutputDelta`
- `item/tool/call` / `dynamic_tool_call` / `tool.call.begin` → `tooldto.ToolCallBegin`
- `item/completed` / `tool.call.end` → `tooldto.ToolCallEnd`
- `item/commandExecution/requestApproval` / `tool.approval.requested` → `tooldto.ToolApprovalRequested`
- `approval/resolved` / `tool.approval.resolved` → `tooldto.ToolApprovalResolved`

按 provider 相关 typed event 覆盖度统计：

| 事件 | Claude producer | Codex producer | 无 producer |
|---|---|---|---|
| `AgentLaunched` | `agent:launched`, `system:init` | `thread/started`, `session.configured` | 否 |
| `StateChanged` | 无 | `thread/status/changed` | 否 |
| `AgentStopped` | `agent:stopped` | `shutdown.complete`, `shutdown_complete` | 否 |
| `AgentRecovering` | 无 | `recovery.attempt` | 否 |
| `AgentFailed` | `agent:failed` | `connection.dead` | 否 |
| `TurnStarted` | `turn:started` | `turn/started`, `turn.started` | 否 |
| `TurnCompleted` | `turn:complete` | `turn/completed`, `turn.completed`, `turn/aborted`, `turn.aborted` | 否 |
| `TurnInterrupted` | `turn:interrupted` | `turn/interrupted`, `turn.interrupted` | 否 |
| `TurnStalled` | 无 | 无 | 是 |
| `TurnResumed` | 无 | 无 | 是 |
| `TurnInputReceived` | `turn:input_received` | 无 | 否 |
| `TurnOutputDelta` | `assistant:message_delta` | `item/agentMessage/delta`, `message.delta`, `agent_message_delta`, `item/reasoning/summaryTextDelta`, `item/reasoning/textDelta`, `reasoning.delta`, `item/commandExecution/outputDelta`, `exec_output_delta` | 否 |
| `ToolCallBegin` | `tool:use_begin` | `item/tool/call`, `dynamic_tool_call`, `tool.call.begin` | 否 |
| `ToolCallEnd` | `tool:use_end` | `item/completed`, `tool.call.end` | 否 |
| `ToolApprovalRequested` | 无 | `item/commandExecution/requestApproval`, `tool.approval.requested` | 否 |
| `ToolApprovalResolved` | 无 | `approval/resolved`, `tool.approval.resolved` | 否 |

注：上表是“源码层 producer 覆盖度”。运行时是否真的能经 `EventDispatcher` 发布到 `kelindar/event` 总线，见 “kelindar/event 契约”。

### V2 对照

Claude：

- V2 `SpawnAndConnect` 的 MCP config 生成已在 V3 `claudecli/driver.go` 接入：`StartSession` 调用 `dto.BuildManifest(...)`，`launchCLI` 再通过 `writeManifestConfig` 落成 `--mcp-config`。该点覆盖成立，但 V3 已不再复刻 V2 的 DynamicTools MCP 构建路径。
- V2 `Submit` 的 `prompt/images/files/outputSchema` 在 V3 `StartTurn` 中只实现了部分覆盖：文本输入、`skills` 和 `outputSchema` 已覆盖；显式图片/文件 hint 注入未保留，`buildTurnText` 仅消费 `InputItem.Content`，忽略 `InputItem.Type`。
- V2 `Shutdown` / `Kill` 双路径在 V3 中仍有明确区分：`Close` → `stop(false)` → `transport.Close()`，`ForceStop` → `stop(true)` → `transport.Kill()`。

Codex：

- V2 `SpawnAndConnect` 的 app-server 连接在 V3 `StartSession` 中得到覆盖：`newSession` / `newTransport` 负责本地拉起或连接 app-server，随后 `initializeSession` + `thread/start` 完成会话创建。
- V2 `SubmitWithSkillsAndOverrides` 的 `skills + overrides + outputSchema` 在 V3 `StartTurn` 中得到覆盖：`buildTurnStartParams` 会填充 `SelectedSkills`、`ManualSkillSelection`、`Overrides.Model`、`Overrides.Effort`、`OutputSchema`，并且 `mapTurnInput` 已支持 `text/image/localImage/mention(file)`。
- V2 `connection_dead` 的恢复处理在 V3 仅部分覆盖：`transport.endReadLoop` 会向 handler 发 `connection.dead`，`session.onNotification` 只做 `failTurns`；`recoveryManager` 虽存在，但 LSP 全局搜索未见其被自动调用，因此没有形成 V2 那种自动恢复闭环。

### 工厂模式

- `claudecli/event_map.go` 与 `codexapp/event_map.go` 都通过 `dispatcher.Register(...)` 注册 `EventTranslator`，provider 侧未直接调用 `event.Publish`。
- `claudeCapabilities` / `codexCapabilities` 均为 `dto.CapabilitySet` map，未使用 `iota`。
- transport 构造是集中化的：Claude 由 `launchCLI -> newTransport` 统一构造；Codex 由 `newSession -> newTransport` 统一构造，本地 app-server 进程拉起集中在 `transport.spawnLocal`。
- `exec.Command(...)` 在 Claude provider 仅出现在 `transport.go:newTransport`，在 Codex provider 仅出现在 `transport.go:spawnLocal`，未见散落式建进程。
- 未发现 3 处以上需要立即抽成新工厂的长段重复块；当前重复主要是短小的 `withTimeout + transport.Call` 模式，仍属可接受的 RPC 样板代码。

### DynamicTools

- `DynamicTools`：0 匹配
- `dynamicTools`：0 匹配
- `dynamic_tools`：0 匹配
- `denyList`：0 匹配
- `filterDynamic`：0 匹配

结论：`internal/provider/` 范围内 DynamicTools 残留已清零。

### kelindar/event 契约

- `internal/provider/unified/event_map.go` 的框架正确：`EventTranslator -> EventDispatcher.Dispatch -> event.Publish(d.bus, typedEv)`。
- `internal/provider/claudecli/*.go` 和 `internal/provider/codexapp/*.go` 中，`event.Publish` / `event.Emit` / `event.On` 搜索结果均为 0；driver 层未越权直接发总线事件。
- `event_map.go` 返回的 typed event struct 均实现了 `Type() uint32`：已在 `internal/dto/agent/event.go`、`internal/dto/tool/event.go`、`internal/dto/turn/event.go` 验证。
- 但运行时契约仍未成立：
- `claudecli/driver.go` 在 `start(...)` 里直接创建私有 `unified.NewEventDispatcher(nil, d.logger)`，并非使用 fx 提供的总线 dispatcher；由于 `bus == nil`，`Dispatch` 时不会进入 `event.Publish`。
- `codexapp/module.go` 虽然通过 `fx.Invoke(RegisterTranslators)` 注册了 translator，但 `codexapp` 文件集中没有 `dto.RawProviderEvent` 生产点，也没有 `EventDispatcher.Dispatch(...)` 调用；因此没有运行时链路把 codex raw event 送到 `event.Publish`。
- 结论：源码层 translator 已存在，driver 层也没有直接越权发事件，但真正的 `EventTranslator -> EventDispatcher -> event.Publish(bus, ev)` 运行时链路仍未打通。

### 总结论

仍需修正。

行动项：

- 修正 `internal/provider/codexapp/transport.go` 的白名单外依赖 `golang.org/x/net/websocket`，或同步调整白名单口径。
- 打通运行时事件总线：provider 必须使用注入的 `*unified.EventDispatcher` 和真实 `*event.Dispatcher`，不能在 driver 内部新建 `nil` bus dispatcher；同时 codexapp 需要补上 `dto.RawProviderEvent` 生产与 `Dispatch` 链路。
- 在事件总线接线完成后，重新核验 codex 的 typed event 是否真正可观测；当前 `fx.Invoke(RegisterTranslators)` 只完成了注册，不代表运行时 producer 已生效。
- 补齐剩余 V2 核心差距：Claude 的显式 image/file 输入语义仍未达到 V2 `Submit` 水平；Codex 的 `connection_dead` 自动恢复闭环仍未达到 V2 行为。 

---

## 11. 波次 3 审查（T5 统一层 + T6 Turn 服务）

### 编译与守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过
- 直接测试覆盖缺口：`internal/provider/unified`、`internal/module/turn` 目录下当前没有 `_test.go`

### import 方向

- `internal/provider/unified/*.go` 未见 `claudecli` / `codexapp` / `store` / `internal/module/*` import。
- `internal/module/turn/*.go` 未见 `internal/provider/*` import，也未见 `module/orchestration` import。
- 方向约束基本成立，但“只允许标准库 + dto/* + contract + platform/* + kelindar/event”这一白名单未完全满足：
- `internal/provider/unified/module.go:3-7` 引入了 `go.uber.org/fx`
- `internal/module/turn/module.go:3-7` 引入了 `go.uber.org/fx`
- 若 `module.go` 也在白名单审查范围内，则本项应判定为不通过；若 DI 装配文件豁免，则其余文件通过。

### 行数

| 文件 | 行数 | 最长函数 | 长度 |
| --- | ---: | --- | ---: |
| `internal/provider/unified/client.go` | 68 | `(*Client).open` | 21 |
| `internal/provider/unified/event_map.go` | 67 | `(*EventDispatcher).Dispatch` | 24 |
| `internal/provider/unified/module.go` | 23 | 无 | 0 |
| `internal/provider/unified/registry.go` | 53 | `NewRegistry` / `(*Registry).Names` | 11 |
| `internal/provider/unified/session.go` | 100 | `(*SessionManager).Register` | 17 |
| `internal/module/turn/assembler.go` | 45 | `(*inputAssembler).appendPaths` | 12 |
| `internal/module/turn/contract.go` | 38 | 无 | 0 |
| `internal/module/turn/manifest.go` | 15 | `(*manifestBuilder).Build` | 8 |
| `internal/module/turn/module.go` | 8 | 无 | 0 |
| `internal/module/turn/service.go` | 180 | `(*service).StartTurn` | 30 |
| `internal/module/turn/skills.go` | 56 | `(*skillResolver).Resolve` | 28 |
| `internal/module/turn/tracker.go` | 121 | `(*turnTracker).Complete` | 20 |

- 全部文件 `< 400` 行，全部函数 `< 80` 行。

### Registry

- `internal/provider/unified/module.go:9-13` 通过 `fx.In` + `Drivers []contract.DriverFactory \`group:"drivers"\`` 收集 driver，满足 group 装配要求。
- `internal/provider/unified/registry.go:27-36` 的 `Resolve(provider string)` 会做 `strings.ToLower(strings.TrimSpace(...))` 归一化，再按 `factory.Create()` 选路，逻辑正确。
- `internal/provider/claudecli/module.go:12-18` 输出 `Name: "claude"`；`internal/provider/codexapp/module.go:12-18` 输出 `Name: "codex"`；与 `driver.Name()` 返回值一致。
- 当前 registry 只识别规范名 `claude` / `codex`，不识别包名 `claudecli` / `codexapp`；若上层 API 约定使用规范名，则无问题。

### Client facade

- `internal/provider/unified/client.go:29-67` 仅做三件事：`registry.Resolve(...)`、调用 driver 的 `StartSession/ResumeSession`、成功后交给 `SessionManager.Register(...)`。
- 未见 store 读写、未见 provider-specific 分支、未见额外业务规则判断。
- 结论：facade 约束成立。

### SessionManager

- `internal/provider/unified/session.go` 明确按 `agentID` 管理活跃 session。
- `Register/Get/Remove/CloseAll` 四个方法齐全。
- `Register` 会在替换旧 session 时调用 `ForceStop()`；`CloseAll` 会先 `Close(ctx)`，失败再 `ForceStop()`，生命周期处理完整。

### Turn Service 接口

- `internal/module/turn/contract.go:11-16` 包含 `PrepareTurn` / `StartTurn` / `InterruptTurn` / `TrackTurn`，接口完整。
- `PrepareInput` 已覆盖 V2 关注字段：`Prompt`、`Images`、`Files`、`Skills`、`Model`、`OutputSchema`；另补充了 `Effort`、`AgentID`、`CWD`、`ThreadCaps`、`BinaryDir`。
- `Skills` 使用 `[]dto.SkillRef`，既保留技能名，也能承载 skill prompt。

### Turn 工厂模式

- 已拆出独立组件：`inputAssembler`、`skillResolver`、`manifestBuilder`、`turnTracker`。
- `internal/module/turn/service.go` 主要职责是编排：参数归一化、session/thread 校验、调用 assembler/resolver/manifest/tracker、委托 `session.StartTurn/Interrupt`。
- 未发现超过 3 处应立即抽离的新重复块；重复校验已下沉为 `normalizeContext` / `requireSession` / `resolveThreadID`。

### V2 对照

- `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:146-169` 的 `prepareTurnSubmissionCommon` 会做输入解析、selected skills 合并、auto-matched skill prompt 注入；V3 `internal/module/turn/service.go:35-55` 只做 prompt/images/files/skills/outputSchema/overrides/manifest 组装，不包含 auto-match/force-match 逻辑。结论：部分等价，能力收缩。
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:277-396` 的 `ParseTurnInputs` 支持通用 `TurnInput` 类型解析、去重、guardrail 和多种输入类型；V3 `internal/module/turn/assembler.go:11-44` 只覆盖 `text + image path + file path`，不覆盖 V2 的 `localImage/fileContent/mention` 泛化输入面。结论：非等价，属于简化实现。
- `go-agent-v2/internal/apiserver/commonadapter/skills.go:43-112` 含显式 mention、force/explicit/trigger 分类和技能名标准化；V3 `internal/module/turn/skills.go:11-55` 仅做 trim、按 name 去重、同名 prompt 合并。结论：未覆盖 V2 skill 解析语义。
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go:138-193` / `304-417` 覆盖历史线程恢复、进程拉起、resume candidate、approval/provider launch config；V3 `internal/module/turn/service.go:58-87` 仅在已有 `contract.Session` 上启动 turn。运行时复杂度已下沉到 provider session：`internal/provider/claudecli/session.go:87-147,303-340` 与 `internal/provider/codexapp/session.go:94-129,306-329` 承担了 provider-specific start path。结论：在“session 已就绪”前提下成立，但不等价于 V2 全量 runtime。
- `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go:144-269` 会发送中断、等待 settle、回读状态、必要时通知完成；V3 `internal/module/turn/service.go:89-105` 仅同步调用 `session.Interrupt(...)`，没有等待确认/状态收敛逻辑。结论：部分覆盖，缺少 V2 的 interrupt settle 语义。

### turn ID 相关性

- `internal/module/turn/tracker.go:14-19` 明确同时保存 `localID` 与 `providerID`，`lookupLocked` 允许按两类 ID 查询，区分是成立的。
- 但 `internal/module/turn/tracker.go:94-100` 在 `providerID` 非空时，会把 `TurnStatus.TurnID` 切换成 provider ID。
- 与之相比，orchestration 侧 `internal/sidecar/orch/orchestration/helpers.go:147-153` 和 `internal/sidecar/orch/orchestration/service.go:244-250` 把活跃 turn ID 视为本地/orchestration ID（`ExpectedTurnID` 或派生 ID）。
- 结论：内部存储区分正确，但对外 `TurnStatus.TurnID` 语义与 orchestration 的 `activeTurnID` 不完全对齐；如果调用方把 `TrackTurn(...).TurnID` 当作 orchestration turn ID 回传，会发生语义漂移。更稳妥的接口应固定 `TurnID` 为 local/orchestration ID，并单独暴露 `ProviderID`。

### 事件归属

- `internal/module/turn` 范围内未见 `kelindar/event` import，未见 `event.Publish` 调用，未见事件总线发布行为。
- `internal/provider/claudecli/event_map.go` 与 `internal/provider/codexapp/event_map.go` 都只做 raw event -> typed event 翻译；真正发布发生在 `internal/provider/unified/event_map.go:43-66` 的 `EventDispatcher.Dispatch`。
- 当前 `module/turn` 也未见事件总线消费接线，状态更新完全依赖 `TurnHandle.Done()` / `Err()`，因此“只消费不发布”中的“消费”尚未体现。
- 结论：未见越权发布；若设计要求 turn 模块订阅 turn 级事件，则当前仍未接入。

### DynamicTools

- `internal/**` 范围内搜索 `DynamicTools` / `dynamicTools` / `dynamic_tools`：0 / 0 / 0。
- 旧 provider stub 目录中的相关注释与环境变量残留，已随整包删除一并清理。
- 若口径仅限当前仓库活跃实现，本项通过。

### 结论（通过/需修正 + 行动项）

- 结论：需修正。
- 守卫命令全部通过，结构拆分也基本达标；当前主要问题不在编译，而在契约一致性和迁移完整性。
- 行动项 1：明确 import 白名单是否豁免 `module.go` 装配文件；若不豁免，需要把 `fx` 装配移出被审查目录或调整审查口径。
- 行动项 2：修正 turn ID 对外语义漂移，避免 `TurnStatus.TurnID` 在 provider 绑定后改写成 provider ID。
- 行动项 3：补齐或明确放弃 V2 的 skill 解析能力与 interrupt settle 语义；当前实现仅覆盖简化版。
- 行动项 4：已完成，旧 provider stub 目录已整体移除。

---

## 12. 波次 3 T6 返工后复审

### 编译与守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过

### 返工项复核

| # | 项 | 状态 | 备注 |
| --- | --- | --- | --- |
| R1 | turn ID 对齐 | 通过 | `turn/tracker.go` 已具备 `TurnStatus.LocalID + ProviderID`、`Start(localID, providerID)`、`BindProviderID(...)`、`GetByProviderID(...)`。 |
| R2 | skill 解析增强 | 部分通过 | `Resolve(refs, prompt)` 已落地，且有 `@skill` / `[skill:name]` / 名称包含式 auto-match 与 TODO 注释；但未见明确的 force 分类或显式/force 分层逻辑。 |
| R3 | interrupt settle | 未通过 | `InterruptTurn` 仍是直接调用 `session.Interrupt(...)` 后返回，调用后未见 settle 骨架，也未见 TODO 注释。 |

### 回归检查

- `turn/contract.go` 的 `Service` 接口签名有语义调整：
- `TrackTurn(ctx context.Context, localID string)` 明确把查询键收敛到 local ID；这是合理变更。
- `TurnStatus` 从旧的单一 `TurnID` 改为 `LocalID + ProviderID + State + Error`；方向正确，但属于对外返回结构变更。
- `turn/service.go` 当前 180 行，仍 `<= 400`。
- `turn/tracker.go` 当前 125 行，仍 `<= 200`。
- `internal/module/turn/*.go` 未见 `internal/provider/*` import，方向约束仍成立。

### turn ID 交叉验证

- `module/orchestration/helpers.go` 的 `turnIDFor(...)` 仍返回 `ExpectedTurnID` 或本地派生值 `threadID-turn-N`，这是 local/orchestration turn ID。
- `module/orchestration/service.go` 的 `agentRuntime.activeTurnID` 与 `claimTurnWork(...)` 继续持有该 local turn ID。
- `turn/service.go` 现已把 `TrackTurn` 查询键明确为 `localID`，`turn/tracker.go` 也以 `localID` 为主索引，并把 `providerID` 降为独立字段和独立查询入口。
- 结论：turn tracker 的主标识已与 orchestration 的 local turn ID 语义对齐；provider ID 不再覆盖 local ID。当前检查范围内未见新的语义冲突。

### 结论

- 结论：仍需修正。
- 已完成返工：turn ID 对齐。
- 已部分完成返工：skill 解析增强。
- 未完成返工：interrupt settle 骨架。

---

## 13. 波次 3 T6 修复与自审

### 1. 编译+守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过

### 2. 4 个修复复核

- 修复 1 `InterruptTurn` settle 骨架：已落地。`session.Interrupt(...)` 成功后保留 `TODO(P5)` settle 注释，明确后续通过 tracker 轮询或事件订阅补齐等待逻辑。
- 修复 2 `skills.go` auto-match 可达性：已落地。`Resolve(...)` 改为 explicit/force 分支先收集显式 skills，再无条件进入 `autoMatch(prompt, seen)` 分支；当前 `autoMatch` 返回空切片，但分支已可达。
- 修复 3 watcher goroutine 泄漏：已落地。`watchTurn(ctx, handle, localID)` 现在受 `ctx.Done()` 与 `trackerTTL` timer 双重控制，不再无限阻塞在 `handle.Done()`。
- 修复 4 tracker 清理/TTL：已落地。新增 `trackerTTL`、`Cleanup()`、`isTerminal()`，并在 `StartTurn(...)` 前调用 `s.tracker.Cleanup()`。

### 3. import 方向

- `internal/module/turn/*.go` 未见 `internal/provider/*` import。

### 4. 行数

- 修改文件行数：
- `service.go`：202
- `skills.go`：116
- `tracker.go`：148
- 以上均 `<= 400`。
- 函数长度检查：
- `(*service).watchTurn`：32 行
- `(*skillResolver).Resolve`：37 行
- `(*turnTracker).Cleanup`：11 行
- 当前未见函数超过 80 行。

### 5. 接口完整性

- `contract.go` 中 `Service` 签名与 `service.go` 实现一致：
- `PrepareTurn(ctx, session, input)`
- `StartTurn(ctx, session, req)`
- `InterruptTurn(ctx, session, source)`
- `TrackTurn(ctx, localID)`

### 6. 工厂模式

- `service.go` 仍只做编排：
- `assembler` 负责输入装配
- `skills` 负责技能解析/提示合并/selected 提取
- `manifest` 负责 MCP manifest 构造
- `tracker` 负责 turn 生命周期状态
- 分层未被此次修复破坏。

### 7. 并发安全

- `turnTracker` 的 `Start/BindProviderID/Update/Complete/Cleanup/Get/GetByProviderID` 均受 `sync.RWMutex` 保护。
- watcher goroutine 现在有 3 个退出源：`ctx.Done()`、`trackerTTL` timer、`handle.Done()`。
- `Cleanup()` 与状态写路径共享同一把锁，没有新增无锁共享状态。

### 8. turn ID 一致性

- `TurnStatus` 继续保留 `LocalID + ProviderID` 双标识。
- `service.go` 仍以 `req.LocalID` 作为 tracker 主键，`BindProviderID(...)` 仅补 provider 侧 ID。
- `TrackTurn(...)` 继续按 `localID` 查询，未回退到 provider ID 作为外部主键。

### 9. 事件归属

- `internal/module/turn` 未见 `kelindar/event` import，未见 `event.Publish` 调用。
- 本次修复未向 `turn` 模块引入任何事件发布职责。

### 10. 代码量

- `turn` 包当前总行数约 572：
- `assembler.go` 45
- `contract.go` 38
- `manifest.go` 15
- `module.go` 8
- `service.go` 202
- `skills.go` 116
- `tracker.go` 148
- 结果：低于 715-900 观察带，但不构成编译、守卫、接口或并发层面的新问题。

### 结论

- 4 个指定修复项已全部落地。
- 三项守卫全绿。
- 结构、接口、并发和 turn ID 一致性均保持稳定。
- 残余说明：`skills.autoMatch` 目前仍是 P5 占位实现，只解决了“分支可达性”，尚未实现真实 prompt→skill 匹配。

---

## 14. 波次 4 深度审查（T7 Thread + T8 Contract Tests）

### 守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过

### Findings

1. `module/thread` 仍是空壳，T7 实际未落地。当前接口只有 `List/Get` 两个方法，且实现都直接返回 `nil`；计划要求吸收 `history/archive/listing/command/messages/rollout` 六类能力和 10 个 V2 源文件，现状完全不匹配。见 `internal/module/thread/contract.go:5-13`、`internal/module/thread/service.go:12-22`，对照计划 `docs/plans/迁移/p4-execution-plan.md:352-359`、`docs/plans/迁移/p4-execution-plan.md:60-71`。
2. T8 contract/capability/manifest 测试未落地。`internal/provider`、`internal/module/thread`、`internal/dto/provider` 当前没有任何 `_test.go`；全局也未找到针对 `StartSession/ResumeSession/ToolCallResponder/BuildManifest/CapabilitySet` 的新增 contract suite。计划要求的 “driver contract suite、capability fallback 测试、MCP manifest 测试” 当前缺失。对照计划 `docs/plans/迁移/p4-execution-plan.md:361-367`。
3. history/rollout 读取虽然在 provider 内部有私有实现，但没有接到公共 thread service 面。`contract.Session` 只暴露 `ListThreads/ForkThread/Configure`，没有任何 history/messages 读取接口；`module/thread/service.go` 也没有 session/provider 依赖。与此同时，`claudecli`/`codexapp` 的 `history` 字段和 `ReadHistory(...)` 实现没有任何使用点，当前属于未接线代码。见 `internal/contract/provider.go:23-35`、`internal/module/thread/service.go:8-22`、`internal/provider/claudecli/driver.go:102-118`、`internal/provider/codexapp/session.go:74-85`、`internal/provider/claudecli/history.go:18-47`、`internal/provider/codexapp/history.go:19-39`。
4. Codex rollout 本地读取路径会在命中本地文件时直接短路返回，跳过 RPC `thread/read`，导致 metadata/hydration 语义丢失，无法达到计划要求的 `messages + rollout` 聚合面。`readLocalRollout(...)` 只解析 `role/content/timestamp`，`Message.Metadata` 永远为空；而 `ReadHistory(...)` 只要本地 rollout 有内容就不会再合并 RPC history。见 `internal/provider/codexapp/history.go:19-38`、`internal/provider/codexapp/history_rollout.go:52-73`，对照迁移映射 `docs/plans/迁移/v3-migration-plan.md:1615-1620`。

### 结论

- 波次 4 当前不通过。
- 守卫全绿仅说明代码形状可编译，不说明 T7/T8 功能已实现。
- 优先级顺序：
- 先补齐 `module/thread` 的统一 history surface 和实际实现。
- 再把 provider history/rollout 接到 thread service 的公共路径。
- 最后补齐 T8 contract/capability/manifest 测试，防止后续回归。
