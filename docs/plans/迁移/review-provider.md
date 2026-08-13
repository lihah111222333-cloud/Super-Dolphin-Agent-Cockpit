# Provider 统一层后端审查

审查时间：2026-03-21
审查方式：只读；以 LSP 符号/引用/调用层级为主，辅以文件清单与定向 `go test`。
审查范围：

- `internal/provider/unified/`
- `internal/provider/claudecli/`
- `internal/provider/codexapp/`

## 结论摘要

总体上，这一层已经形成了可工作的 V3 统一抽象：

- `claudecli` 与 `codexapp` 都完整实现了 `contract.Driver` / `contract.Session`。
- `unified` 已承担 registry、session manager、event dispatcher、session resolver、fx wiring。
- `codexapp` 的 approval 闭环已经能从 provider 通知进入，再经内部审批器等待，最后回写 provider。

但本轮审查确认了 6 个需要优先处理的问题：

1. `claudecli` 的 `Configure` 目前只改本地字段，不会把 `/model`、`/personality`、`/approvals` 真正应用到现有 CLI 会话。`thread/model/set` 等 RPC 可能返回成功，但运行态不变。证据：`internal/provider/claudecli/session.go:237`、`internal/provider/claudecli/session.go:303`、`internal/module/thread/command.go:21`。
2. `claudecli` 声明了 `context_compact` capability，但 thread 命令通道并不支持 `/compact`，`thread/compact/start` 通过 capability gate 后仍会落到 `unsupported command`。证据：`internal/provider/claudecli/driver.go:13`、`internal/module/thread/rpc.go:63`、`internal/module/thread/command.go:20`。
3. `claudecli` 的 session 代码实际支持 turn-level `Model/Effort` override，但 capability 集未声明 `turn_override`，上游 `module/turn` 会直接把 override 剪掉，导致能力被“实现了但不可达”。证据：`internal/provider/claudecli/session.go:333`、`internal/provider/claudecli/driver.go:13`、`internal/module/turn/service.go:242`。
4. `SessionResolver` 并没有成为唯一的 `threadID -> session` 入口。`turn/rpc` 与 capability gate 使用它，但 `module/thread` 仍保留了自己的一套 binding + session provider 解析逻辑。证据：`internal/provider/unified/session_resolver.go:23`、`internal/module/turn/rpc.go:20`、`internal/platform/rpc/handler.go:20`、`internal/module/thread/service.go:213`。
5. `ReadHistory` 在两个 driver 上都已存在，但与 V2 相比，历史 metadata 保真度退化。`claudecli` 已不再提取附件 metadata；`codexapp` 的本地 rollout 路径也会丢 metadata。证据：`internal/provider/claudecli/session_history.go:35`、`internal/provider/claudecli/history.go:84`、`internal/provider/codexapp/history_rollout.go:52`、`go-agent-v2/legacy-agentsdk/claude/history_backend.go:180`、`go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216`。
6. `codexapp/recovery.go` 目前只是孤立实现，session 构造时挂上了 `recoveryManager`，但审查范围内没有任何调用点，和 V2 appserver client 的 health/reconnect 能力相比尚未真正迁入。证据：`internal/provider/codexapp/session.go:86`、`internal/provider/codexapp/recovery.go:18`、`internal/provider/codexapp/recovery.go:28`。

## 1. 文件清单与行数

### unified

| 文件 | 行数 |
| --- | ---: |
| `internal/provider/unified/module.go` | 42 |
| `internal/provider/unified/session_adapter.go` | 34 |
| `internal/provider/unified/client_test.go` | 56 |
| `internal/provider/unified/contract_test.go` | 181 |
| `internal/provider/unified/event_map.go` | 66 |
| `internal/provider/unified/registry_test.go` | 48 |
| `internal/provider/unified/registry.go` | 56 |
| `internal/provider/unified/client.go` | 67 |
| `internal/provider/unified/session.go` | 117 |
| `internal/provider/unified/session_resolver.go` | 46 |
| `internal/provider/unified/manifest_test.go` | 41 |
| 小计 | 754 |

### claudecli

| 文件 | 行数 |
| --- | ---: |
| `internal/provider/claudecli/module.go` | 26 |
| `internal/provider/claudecli/driver.go` | 133 |
| `internal/provider/claudecli/history.go` | 134 |
| `internal/provider/claudecli/skill_prompt.go` | 46 |
| `internal/provider/claudecli/transport_config.go` | 226 |
| `internal/provider/claudecli/event_map.go` | 130 |
| `internal/provider/claudecli/history_model.go` | 25 |
| `internal/provider/claudecli/session.go` | 393 |
| `internal/provider/claudecli/session_events.go` | 264 |
| `internal/provider/claudecli/session_history.go` | 57 |
| `internal/provider/claudecli/config.go` | 93 |
| `internal/provider/claudecli/transport.go` | 219 |
| 小计 | 1746 |

### codexapp

| 文件 | 行数 |
| --- | ---: |
| `internal/provider/codexapp/module.go` | 31 |
| `internal/provider/codexapp/driver.go` | 189 |
| `internal/provider/codexapp/history_rollout.go` | 97 |
| `internal/provider/codexapp/history.go` | 39 |
| `internal/provider/codexapp/skill_prompt.go` | 33 |
| `internal/provider/codexapp/event_map.go` | 296 |
| `internal/provider/codexapp/transport_helpers.go` | 63 |
| `internal/provider/codexapp/input_map.go` | 69 |
| `internal/provider/codexapp/support.go` | 34 |
| `internal/provider/codexapp/session.go` | 356 |
| `internal/provider/codexapp/recovery.go` | 44 |
| `internal/provider/codexapp/session_history.go` | 62 |
| `internal/provider/codexapp/transport.go` | 322 |
| `internal/provider/codexapp/session_approval.go` | 83 |
| 小计 | 1718 |

总计：4218 行。

## 2. Driver 接口实现

结论：`claudecli` 和 `codexapp` 都完整实现了 `contract.Driver`。

- `contract.Driver` 定义在 `internal/contract/provider.go:10`，只要求 `Name`、`StartSession`、`ResumeSession`。
- `claudecli` 在 `internal/provider/claudecli/driver.go:51`、`:53`、`:75` 实现全部方法，并有 `var _ contract.Driver = (*driver)(nil)`，见 `internal/provider/claudecli/driver.go:133`。
- `codexapp` 在 `internal/provider/codexapp/driver.go:76`、`:78`、`:96` 实现全部方法，并有 `var _ contract.Driver = (*driver)(nil)`，见 `internal/provider/codexapp/driver.go:25`。

补充：

- 对外 provider 名称是 `"claude"` / `"codex"`，不是包名 `"claudecli"` / `"codexapp"`。
- `unified.Registry` 只按 `DriverFactory.Name` 精确匹配，因此传 `"claudecli"` 或 `"codexapp"` 不会命中。

## 3. Session 接口实现

结论：两个 driver 的 `session` 都完整实现了 `contract.Session`。

- `contract.Session` 定义在 `internal/contract/provider.go:23`。
- `claudecli.session` 已实现 `ThreadID`、`Capabilities`、`StartTurn`、`Interrupt`、`ListThreads`、`ForkThread`、`ReadHistory`、`Configure`、`Close`、`ForceStop`，见 `internal/provider/claudecli/session.go` 与 `internal/provider/claudecli/session_history.go:12`。编译期断言在 `internal/provider/claudecli/session.go:393`。
- `codexapp.session` 已实现同一组方法，见 `internal/provider/codexapp/session.go` 与 `internal/provider/codexapp/session_history.go:13`。编译期断言在 `internal/provider/codexapp/session.go:34`。

边界说明：

- `contract.ToolCallResponder` 仍定义在 `internal/contract/provider.go:48`，但审查范围内没有任何实现体；这不影响 `contract.Session` 的完整性，但说明 V3 provider contract 里仍残留一块未落地面。

## 4. SessionManager lifecycle

结论：`Get` / `Remove` / `CloseAll` 生命周期已闭环；“Create”不是独立方法，而是 `Client.open -> Register`。

- 创建入口：`unified.Client.StartSession/ResumeSession` 在 `internal/provider/unified/client.go:29`、`:38` 调到 `open`，`open` 成功后执行 `c.sessions.Register(agentID, session)`，见 `internal/provider/unified/client.go:63`。
- `SessionManager.Register` 在 `internal/provider/unified/session.go:30`。若 agent 已有旧 session，会 `ForceStop` 旧 session，而不是 `Close`，见 `internal/provider/unified/session.go:41`。
- `Get` 在 `internal/provider/unified/session.go:48`，按标准化后的 `agentID` 查找。
- `Remove` 在 `internal/provider/unified/session.go:59`，先从 map 删除，再调用 `session.Close(context.Background())`，失败才 `ForceStop`，见 `internal/provider/unified/session.go:76`。
- `CloseAll` 在 `internal/provider/unified/session.go:84`，会 drain 全部 session，再逐个 `Close`，失败时 `ForceStop`，见 `internal/provider/unified/session.go:91`。
- fx `OnStop` hook 已接到 `CloseAll`，见 `internal/provider/unified/module.go:32`。

审查判断：

- 生命周期主路径是完整的。
- 但替换已有 session 时只走 `ForceStop` 不走 `Close`，属于更激进的置换策略，和 `Remove/CloseAll` 的优雅关闭策略不一致。

## 5. SessionResolver

结论：`SessionResolver` 已实现并被 turn RPC / capability gate 使用，但并没有覆盖所有需要 `threadID -> session` 的模块。

- 实现：`internal/provider/unified/session_resolver.go:23`，逻辑是 `threadStore.GetByThreadID(threadID) -> ref.AgentID -> SessionManager.Get(agentID)`。
- 直接消费者：
  - `internal/module/turn/rpc.go:20` 的 `withSession`
  - `internal/platform/rpc/handler.go:20` 的 `NewCapabilityResolver`
- 未统一到 resolver 的路径：
  - `internal/module/thread/service.go:213` 仍有自己的 `resolveSession`
  - 它走的是 `resolveBinding -> SessionProvider.GetSession(binding.AgentID)`，不是 `contract.SessionResolver`

审查判断：

- 如果标准是“只要输入是 threadID，就统一走 `SessionResolver`”，当前答案是否定的。
- 当前状态更像“两套路径并存”：一套在 unified resolver，一套在 thread service 内部。

## 6. Registry

结论：fx group 收集已完成；driver 选择逻辑是“精确命中、无默认回退”。

- `group:"drivers"` 收集定义在 `internal/provider/unified/module.go:16`。
- `claudecli` 与 `codexapp` 都通过 `fx.Annotate(NewDriverFactory, fx.ResultTags(\`group:"drivers"\`))` 注册，见 `internal/provider/claudecli/module.go:23`、`internal/provider/codexapp/module.go:28`。
- `Registry` 在 `internal/provider/unified/registry.go:15` 建 map，key 为 `normalizeProviderName(factory.Name)`。
- `Resolve` 在 `internal/provider/unified/registry.go:27` 做精确查找；找不到直接报 `unknown provider`。

和 V2 对照：

- V2 `provider_adapter_registry` 有默认 provider 与 fallback 逻辑，见 `go-agent-v2/internal/apiserver/provider_adapter_registry.go:23`、`:77`。
- 当前 V3 `unified.Registry` 不再提供默认 provider，也不会把未知 provider 回退到默认值。

审查判断：

- 作为严格 registry 没问题。
- 作为 V2 行为对照，存在一个明确语义变化：默认 provider fallback 被移除。

## 7. EventDispatcher

结论：统一 dispatcher 设计是干净的；codexapp 的 AgentID 回填保证存在，但回填发生在 `session.dispatch`，不是 `event_map.go` 本身。

- `unified.EventDispatcher` 只负责维护 translator 列表并 fan-out，见 `internal/provider/unified/event_map.go:13`、`:43`。
- `claudecli` translator 注册在 `internal/provider/claudecli/event_map.go:14`。
- `codexapp` translator 注册在 `internal/provider/codexapp/event_map.go:17`。
- `codexapp.session.dispatch` 会先把 `raw.Data` 解成 map，如果 payload 里没有 `agentId/agent_id`，就补入当前 session 的 `s.agentID`，见 `internal/provider/codexapp/session.go:242`、`:247`。
- `codexapp/event_map.go` 的 header helper 只读取 payload 字段：
  - thread fallback：`threadId/thread_id -> nested thread.id`，见 `internal/provider/codexapp/event_map.go:150`
  - session fallback：`sessionId/session_id -> threadID`，见 `internal/provider/codexapp/event_map.go:158`
  - turn fallback：`turnId/turn_id -> nested turn.id`，见 `internal/provider/codexapp/event_map.go:162`

审查判断：

- “AgentID 回填保证”是成立的。
- 但这个保证依赖 `session.dispatch` 预处理；如果 translator 被绕过或未来有别的 raw event 入口，这个约束不会自动存在于 `event_map.go` 自身。

## 8. approval 集成

结论：`codexapp/session_approval.go` 的链路是完整的。

完整链路如下：

1. provider 通知进入 `session.onNotification`，见 `internal/provider/codexapp/session.go:230`。
2. 方法名命中 `item/commandExecution/requestApproval` 或 `tool.approval.requested`，转入 `handleApprovalRequest`，见 `internal/provider/codexapp/session.go:233`。
3. `handleApprovalRequest` 异步调用 `requestToolApproval`，见 `internal/provider/codexapp/session_approval.go:14`。
4. `buildApprovalRequest` 从 provider payload 归一化出 `CallID` / `ApprovalID` / `ToolName` / `ThreadID` / `TurnID` / `RequestID`，见 `internal/provider/codexapp/session_approval.go:38`。
5. 之后调用 `ApprovalManager.RequestApproval(s.ctx, nil, nil, req)` 进入内部审批等待，见 `internal/provider/codexapp/session_approval.go:31`。
6. 前端或上层通过内部 RPC `approval/respond` 调到 `module/turn` 的 approval handler，再落到 `ApprovalManager.Respond`，见 `internal/module/turn/rpc.go:79`、`internal/platform/rpc/approval.go:105`。
7. `requestToolApproval` 拿到审批结论后，调用 `sendApprovalDecision` 回写 provider transport 的 `"approval/respond"`，见 `internal/provider/codexapp/session_approval.go:66`。

边界说明：

- 此链路下 `ApprovalManager` 被以 `bridge=nil, server=nil` 调用，因此它不会主动发起 callback dispatch，见 `internal/platform/rpc/approval.go:149`。
- 也就是说，approval 的 requested/resolved 事件主要依赖 provider 自己的 raw event 翻译，而不是 `ApprovalManager.publishRequested/publishResolved`。

## 9. ReadHistory

结论：两个 driver 都实现了 `ReadHistory`；`codexapp` 比 `claudecli` 多一层 RPC fallback；但两边都比 V2 少了 metadata 保真。

### claudecli

- session 方法在 `internal/provider/claudecli/session_history.go:12`。
- 后端只读本地 history/session 文件：`historyBackend.ReadHistory`，见 `internal/provider/claudecli/history.go:18`。
- 转 provider DTO 时只保留 `Role` / `Content` / `Timestamp`，见 `internal/provider/claudecli/session_history.go:35`。
- 当前实现不会把附件信息保留到 `dto.Message.Metadata`。

### codexapp

- session 方法在 `internal/provider/codexapp/session_history.go:13`。
- `rolloutReader.ReadHistory` 先读本地 rollout，失败或为空时 fallback 到 `thread/read` RPC，见 `internal/provider/codexapp/history.go:19`。
- 本地 rollout parser `parseRolloutLine` 只返回 `Role` / `Content` / `Timestamp`，见 `internal/provider/codexapp/history_rollout.go:52`。
- DTO 转换层 `session_history.go:28` 支持 metadata，但只有 RPC `thread/read` 返回的 `Message.Metadata` 才会保留下来。

和 V2 对照：

- V2 Claude history 会从注入的 `[image:...]` / `[file:...]` hint 中恢复 metadata，见 `go-agent-v2/legacy-agentsdk/claude/history_backend.go:164`、`:180`。
- V2 Codex rollout reader 会提取用户图片 metadata，见 `go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216`。

审查判断：

- “有 ReadHistory”这一点成立。
- “与 V2 同等保真”这一点不成立。

## 10. CapabilityError

结论：类型化错误已经存在，但只在 `claudecli` 的不支持能力上使用；生产路径里几乎没有对该类型做 `errors.As` 分支。

- 类型定义在 `internal/dto/provider/capability.go:17`。
- `claudecli` 对 `ListThreads` / `ForkThread` 返回 `dto.NewCapabilityError(...)`，见 `internal/provider/claudecli/session.go:229`、`:233`。
- `codexapp` 支持 thread list / fork，因此没有相应 capability error。
- 生产代码中更常见的是前置 capability gate：`internal/platform/rpc/handler.go:71`。
- 当前 repo 内对 `CapabilityError` 的显式 `errors.As` 校验只出现在 unified contract test，见 `internal/provider/unified/contract_test.go:146`、`:154`。

审查判断：

- 类型化错误本身是到位的。
- 但它主要是 provider-side unsupported error，不是系统级统一错误分流的核心机制。

## 11. InputItem

结论：统一类型来源已经完成；driver 侧的映射策略并不一致，`codexapp` 保结构，`claudecli` 偏文本化。

- 单一源类型是 `internal/dto/shared/input.go:3` 的 `shared.InputItem`。
- `dto/provider.TurnRequest` 使用类型别名，见 `internal/dto/provider/turn.go:24`。
- `module/turn` 也使用同一别名，见 `internal/module/turn/contract.go:21`。

driver 侧：

- `codexapp` 在 `internal/provider/codexapp/input_map.go:10` 明确映射：
  - `text`
  - `image`
  - `localImage`
  - `mention`
  - fallback
- `claudecli` 在 `internal/provider/claudecli/session.go:162` 把输入拼成文本；非文本附件会变成提示语句和 attachment hint，见 `internal/provider/claudecli/session.go:183`。

审查判断：

- 类型统一已经做到。
- 语义统一还没做到：同一个 `InputItem` 进入两个 driver 后，结构保真度不同。

## 12. claudecli vs codexapp 能力面对照

| 能力 | claudecli 声明 | claudecli 实际 | codexapp 声明 | codexapp 实际 |
| --- | --- | --- | --- | --- |
| `message_send` | 是 | 是 | 是 | 是 |
| `thread_list` | 否 | 返回 `CapabilityError` | 是 | 已实现 `thread/list` |
| `thread_fork` | 否 | 返回 `CapabilityError` | 是 | 已实现 `thread/fork` |
| `turn_override` | 否 | session 代码可处理，但上游不会下发 | 是 | 已实现并可达 |
| `model_switch` | 是 | `Configure` 有接口，但当前不会真正应用到现有 CLI 会话 | 是 | 已实现远端 `thread/config/set` |
| `context_compact` | 是 | capability 声明与命令实现不一致，实际不可用 | 否 | 不声明 |
| `approval` | 无独立 capability | 无 provider-side approval 流 | 无独立 capability | 已有 provider-side approval 闭环 |
| `ReadHistory` | 是 | 本地文件读取 | 是 | 本地 rollout + RPC fallback |

审查判断：

- `codexapp` 的能力面更接近“完整 provider”。
- `claudecli` 现在的主要问题不是缺功能，而是“声明、上游 gate、实际行为”三者不一致。

## 13. V2 provider 对照

和 V2 代码对照后，可以明确确认以下差异：

### 已迁入

- Codex appserver transport 的基础 JSON-RPC 会话控制已迁入到 `internal/provider/codexapp/transport.go` / `driver.go`。
- Claude CLI 的基础启动、事件监听、history 读取已迁入到 `internal/provider/claudecli/*`。
- provider registry 已从 V2 `apiserver/provider_adapter_registry.go` 转为 V3 `unified.Registry`。

### 明确退化或未迁完

- 默认 provider fallback：V2 有，V3 `unified.Registry` 没有。证据：`go-agent-v2/internal/apiserver/provider_adapter_registry.go:77` 对比 `internal/provider/unified/registry.go:27`。
- Claude history metadata：V2 有附件 metadata 恢复，V3 没有。证据：`go-agent-v2/legacy-agentsdk/claude/history_backend.go:180` 对比 `internal/provider/claudecli/history.go:113`。
- Codex rollout metadata：V2 有 `extractRolloutMetadata`，V3 本地 rollout parser 没有 metadata。证据：`go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216` 对比 `internal/provider/codexapp/history_rollout.go:52`。
- Codex recovery/health：V2 appserver client 有成体系的 health/reconnect 文件族，V3 仅保留了一个未接线的 `recovery.go`。证据：`go-agent-v2/legacy-agentsdk/codex/client_appserver_health.go` 对比 `internal/provider/codexapp/recovery.go:18`。
- Claude 动态 tool result responder：V2 CLI client 有 `SendDynamicToolResult` / `RespondError`，见 `go-agent-v2/legacy-agentsdk/claude/client.go:363`；V3 `contract.ToolCallResponder` 仍在，但 provider 包里没有实现。

审查判断：

- V3 已完成“统一层骨架迁移”。
- 但离“对 V2 provider 行为完全对齐”还有明显距离，尤其在 registry fallback、history metadata、recovery、tool response 四块。

## 14. import 方向

结论：如果目标是“unified 只依赖 contract + dto，driver 不能 import unified”，当前并未达到。

### unified 的依赖

- 依赖 `contract`：有。
- 依赖 `dto/provider`：有。
- 还依赖：
  - `internal/module/thread`，见 `internal/provider/unified/module.go:10`
  - `cmd/mcp-orch/orchestration`，见 `internal/provider/unified/module.go:9`
  - `internal/store/thread`，见 `internal/provider/unified/session_resolver.go:9`
  - `github.com/kelindar/event`，见 `internal/provider/unified/event_map.go:7`

### driver 对 unified 的依赖

- `claudecli` 直接 import `internal/provider/unified`，见：
  - `internal/provider/claudecli/driver.go:10`
  - `internal/provider/claudecli/event_map.go:11`
  - `internal/provider/claudecli/module.go:9`
  - `internal/provider/claudecli/session.go:16`
- `codexapp` 同样直接 import `internal/provider/unified`，见：
  - `internal/provider/codexapp/driver.go:15`
  - `internal/provider/codexapp/event_map.go:14`
  - `internal/provider/codexapp/module.go:10`
  - `internal/provider/codexapp/session.go:15`

审查判断：

- 当前是“driver 依赖 unified.EventDispatcher，unified 依赖 module/store 适配层”的结构。
- 好处是没有直接包循环。
- 但如果目标是更严格的分层边界，这一版还没有收口到位。

## 15. fx 注册

结论：三个子包的 fx 注册都能工作，职责边界也比较清楚。

### unified/module.go

- 提供：
  - `NewEventDispatcher`
  - `NewRegistry`
  - `NewClient` 作为 `thread.SessionStarter`
  - `NewSessionManager`
  - `NewSessionProvider` 作为 `thread.SessionProvider`
  - `NewSessionCleaner` 作为 `orchestration.SessionCleaner`
  - `NewSessionResolver`
- 通过 `fx.Invoke(registerSessionShutdown)` 在 `OnStop` 执行 `CloseAll`。

### claudecli/module.go

- 只注册一个 grouped driver factory：`NewDriverFactory`
- 通过 `fx.Invoke(RegisterTranslators)` 把 Claude translator 接到 unified dispatcher

### codexapp/module.go

- 同样只注册 grouped driver factory：`NewDriverFactory`
- 通过 `fx.Invoke(RegisterTranslators)` 接入 Codex translator

审查判断：

- fx wiring 是成立的。
- `unified` 明确承担了“把 provider 统一抽象适配给 thread/orchestration”的角色。

## 附：定向测试

已执行：

```bash
go test ./internal/provider/... ./internal/module/thread ./internal/module/turn ./internal/platform/rpc/...
```

结果：

- `internal/provider/unified` 通过
- `internal/module/turn` 通过
- `internal/provider/claudecli` 无测试文件
- `internal/provider/codexapp` 无测试文件
- `internal/module/thread` 无测试文件
- `internal/platform/rpc` 无测试文件

测试结论：

- 统一层 contract / wiring 的基础测试在 `unified` 和 `turn` 上是存在的。
- 本轮发现的关键问题主要落在没有专门单测覆盖的区域：`claudecli` 配置生效、capability 对齐、history metadata、codex recovery wiring。

## 建议优先级

P0：

- 修正 `claudecli.Configure` 的生效链路，让 `/model`、`/personality`、`/approvals` 真正作用于运行态。
- 移除或补齐 `claudecli` 的 `context_compact` capability。

P1：

- 决定 `claudecli` 是否正式支持 `turn_override`；如果支持，就把 capability 声明和 `module/turn` gate 对齐。
- 明确 `SessionResolver` 是否要成为唯一 thread-to-session 入口；若是，则收敛 `module/thread` 的重复解析逻辑。

P2：

- 回补 `ReadHistory` metadata 保真，至少对齐 V2 的图片/附件 metadata。
- 确认 `codexapp/recovery.go` 是要接线还是删除，避免“看起来有能力、实际上没路径”的假象。
- 决定是否恢复 V2 的 default provider fallback，或在上层明确要求 provider 必填。
