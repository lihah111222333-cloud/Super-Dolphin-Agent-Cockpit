# V2↔V3 1:1 对齐：`thread/config/*` + `thread/model/*` + `thread/compact/*`

## 读取基线

本次只基于代码实现做对比，未采信既有迁移文档结论。

- V2
  - `go-agent-v2/internal/apiserver/methods_thread.go`
  - `go-agent-v2/internal/apiserver/methods_thread_turn.go`
  - `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go`
  - `go-agent-v2/internal/apiserver/methods_inline_routes_slash_guardrail_test.go`
  - `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go`
  - `go-agent-v2/cmd/agent-terminal/app.go`
  - `go-agent-v2/internal/runner/manager_submission.go`
  - `go-agent-v2/pkg/agentsdk/agentcore/client.go`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_launch_config.go`
- V3
  - `internal/module/thread/rpc.go`
  - `internal/module/thread/rpc_types.go`
  - `internal/module/thread/command.go`
  - `internal/module/thread/contract.go`
  - `internal/platform/rpc/handler.go`
  - `internal/platform/rpc/strict.go`
  - `internal/provider/codexapp/session.go`
  - `internal/provider/claudecli/session_config.go`
  - `internal/contract/provider.go`
  - `internal/dto/provider/thread_config.go`

## 先说结论

`thread/config/*`、`thread/model/*`、`thread/compact/*` 目前没有任何一项达到 V2↔V3 的 1:1 对齐。

- `thread/config/get`: `⚠️`
- `thread/config/set`: `❌`
- `thread/model/set`: `⚠️`
- `thread/compact/start`: `❌`

关键原因不是一类问题，而是三类问题叠加：

- 参数形状不一致。V2 的 typed 字段在 V3 被降成了 `args` 壳，且 `StrictHandler` 会按目标 struct 严格解码。
- 运行时生效路径不一致。V2 的 `/model`、`/compact` 是直接打到活跃 CLI 会话；V3 统一走 `thread.Service.SendCommand(...)`，其中一部分只是本地 skeleton。
- 语义不一致。V2 的 `thread/config/set` 是“持久化 thread launch override”，不是 live slash command；V3 既没有这个 typed surface，也没有同等 provider-neutral patch 面。

## 逐项对比

| RPC | V2 入口 | V3 入口 | 参数 | 运行时生效性 | 语义 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| `thread/config/get` | typed `threadConfigGetTyped -> ThreadConfigGet` | `newThreadConfigGetHandler -> SendCommand("config/get", "")` | `✅` 都要求 `threadId` | `⚠️` V3 不走 provider 真实查询，只走本地拼装 | `⚠️` V3 只可靠返回 `model`，`effort` 实际未解析 | `⚠️` |
| `thread/config/set` | typed `threadConfigSetTyped -> ThreadConfigSet` | `newThreadCommandHandler("config/set")` | `❌` V2 是 `threadId + model + effort`，V3 只认 `threadId + args` | `❌` V2 持久化 launch override；V3 会落到 unsupported command | `❌` 完全不是同一能力面 | `❌` |
| `thread/model/set` | capability guard + live `/model` slash | capability guard + `SendCommand("/model", args)` | `⚠️` 字段名基本保留，但 V2 `threadId` 可缺省解析，V3 强制要求 `threadId` | `⚠️` V2 直接改活跃 CLI；V3 改成 `session.Configure(Model)` | `⚠️` codexapp 实际转成远端 `thread/config/set`，claudecli active session 直接不支持 | `⚠️` |
| `thread/compact/start` | capability guard + live `/compact` slash | capability guard + `SendCommand("/compact", args)` | `✅` 两边都要求 `threadId`，且都能接 `args` 壳 | `❌` V3 根本不下发 provider，只返回本地占位结果 | `❌` V2 真正触发 compact；V3 明确 `not_implemented` | `❌` |

## 细项说明

### 1. `thread/config/get`

V2 是真实 typed 方法：

- 注册在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:49-50`
- 参数定义在 `go-agent-v2/internal/apiserver/methods_thread.go:343-355`
- 真实实现是 `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:47-59`
- 返回结构在 `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:15-29`
- schema 也明确要求 `override/effective.{model,effort}`，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:59-71` 和 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:437-448`

V3 虽然也保留了 `thread/config/get` 路由，但不是同等实现：

- 路由在 `internal/module/thread/rpc.go:59`
- handler 只是 `SendCommand("config/get", "")`，见 `internal/module/thread/command.go:119-122`
- `SendCommand` 对 `/config/get` 的处理是本地 `configGetResult(...)`，见 `internal/module/thread/command.go:32-35`
- `configGetResult` 只从 thread store 取 `model`，然后同时塞进 `override/effective`；`effort` 字段虽然还在 struct 里，但没有填充逻辑，见 `internal/module/thread/command.go:74-85` 和 `internal/module/thread/command.go:100-109`
- `supportsThreadOverride` 也不是 V2 的真实 resolve 结果，而是 `model_switch || turn_override` 能力推导，见 `internal/module/thread/command.go:106`

结论：

- 路由名和 `threadId` 参数保住了。
- 返回 payload 只做到“形似”，没有做到“值等价”。
- 尤其 `effort` 已经退化成空壳，因此只能给 `⚠️`。

### 2. `thread/config/set`

这项是当前最不对齐的一项。

V2：

- 注册在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:49-50`
- typed 参数是 `threadId + model + effort`，见 `go-agent-v2/internal/apiserver/methods_thread.go:347-359`
- 会校验 `effort` 枚举值，见 `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:75-83`
- 会检查线程是否 busy，不允许边跑边改 launch config，见 `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:61-73`
- 真正做的是持久化 thread-level override，并返回新的 `effective/override` 结构，见 `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:102-140`
- V2 的有效 `effort` 枚举是 `none|minimal|low|medium|high|xhigh`，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_launch_config.go:23-25`

这里要特别强调：V2 的 `thread/config/set` 不是 live `/model` 命令，它是“改 thread launch config，影响后续 turn/start 或下次运行态解析”。

V3：

- 路由在 `internal/module/thread/rpc.go:61`
- 绑定的是 `newThreadCommandHandler(svc, "config/set")`，也就是通用命令壳，不是 typed config setter
- 通用壳的参数是 `commandParams{threadId,args}`，见 `internal/module/thread/rpc_types.go:34-37`
- `rpc.ThreadHandler` 包装的是 `StrictHandler`，见 `internal/platform/rpc/handler.go:122-125` 和 `internal/platform/rpc/strict.go:11-17`
- 这意味着 V2 形状的 `{threadId, model, effort}` 在 V3 这里已经不是同一签名
- 即便传成 `{threadId,args}`，`SendCommand` 也没有 `/config/set` 分支；默认直接 `unsupported command`，见 `internal/module/thread/command.go:31-45`

结论：

- 参数不兼容。
- 行为不兼容。
- 运行时也不可用。
- 这项必须给 `❌`。

### 3. `thread/model/set`

V2 这是 live slash command：

- 注册在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:98-100`
- 绑定关系在 `go-agent-v2/internal/apiserver/methods_inline_routes_slash_guardrail_test.go:34`
- schema 参数是 `threadId + model + args`，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:330-337`
- helper 是 `SendSlashCommandWithArgs(...)`，内部走 `sendSlashCommandWithParams(...)`，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:420-436`
- 最终命令链会落到 `client.SendCommand(cmd, args)`，见：
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:56-58`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:212-214`
  - `go-agent-v2/cmd/agent-terminal/app.go:165-169`
  - `go-agent-v2/internal/runner/manager_submission.go:528-536`
  - `go-agent-v2/pkg/agentsdk/agentcore/client.go:7-18`

因此 V2 的 `/model` 是直接送进活跃 CLI 会话的。

V3 表面上保留了 `model` / `args` 双字段，但语义已经换了：

- 路由在 `internal/module/thread/rpc.go:62`
- 参数定义在 `internal/module/thread/rpc_types.go:43-47`
- `newModelSetHandler` 会把 `model` / `args` 规整成一个字符串，然后调用 `svc.SendCommand(..., "/model", args)`，见 `internal/module/thread/rpc.go:136-162`
- `SendCommand` 收到 `/model` 后并不直接走 provider slash channel，而是进 `sendModelSet(...)`，再调用 `session.Configure(dto.ThreadConfigPatch{Model: ...})`，见 `internal/module/thread/command.go:35-36` 和 `internal/module/thread/command.go:112-127`
- provider patch 结构只有 `Model / Personality / Approvals`，没有 `Effort`，见 `internal/dto/provider/thread_config.go:3-7`

更关键的是 provider 端行为：

- codexapp 的 `Configure(Model)` 会远端调用 `thread/config/set`，不是 `/model`，见 `internal/provider/codexapp/session.go:192-204`
- 这会把 V2 `thread/model/set` 的 live slash 语义，降级成 V2 `thread/config/set` 的 launch-config override 语义
- claudecli 的 active session `Configure(...)` 直接返回 unsupported，见 `internal/provider/claudecli/session_config.go:13-27`

另外，V2 的 `SendSlashCommandWithArgs` 不是 `RequireThreadID` 版本；V3 的 `rpc.ThreadHandler` 则强制要求 `threadId`，见 `internal/platform/rpc/handler.go:52-75`

结论：

- 参数字段名大体还在，但 thread scope 要求已经更严格。
- 最关键的运行时生效路径已经变了。
- 对 codexapp 是“改后续 launch config”，对 claudecli 则根本不支持 active-session configure。
- 所以只能给 `⚠️`，不能算 1:1。

### 4. `thread/compact/start`

V2：

- 注册在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:33-35`
- 绑定关系在 `go-agent-v2/internal/apiserver/methods_inline_routes_slash_guardrail_test.go:31`
- 走的是 `SendSlashCommandFromRawParamsRequireThreadID(..., "/compact")`
- `sendSlashCommandWithParams(...)` 会把原始 params 解析成 thread scope + args 壳，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:420-433`
- 最终同样落到 live `client.SendCommand("/compact", args)`，命令链同上

V3：

- 路由在 `internal/module/thread/rpc.go:67`
- 参数定义在 `internal/module/thread/rpc_types.go:49-52`
- handler 只是 `svc.SendCommand(..., "/compact", p.Args)`，见 `internal/module/thread/rpc.go:146-149`
- `SendCommand` 对 `/compact` 的实现不是 provider 调用，而是本地 `sendCompactCommand(...)`，见 `internal/module/thread/command.go:41-42`
- `sendCompactCommand(...)` 明确返回占位结果：
  - 有 `args` 时：`accepted=false`, `status="rejected"`
  - 无 `args` 时：`accepted=false`, `status="not_implemented"`
  - 见 `internal/module/thread/command.go:170-186`

结论：

- 参数壳大致对齐。
- 真实 compact 语义完全没有下沉到 provider/session。
- 这项是明确的 `❌`。

## `personality` / `effort` / `approvalPolicy`

这三项要单独看，因为它们正好暴露了 V3 现在的“config 面不完整”。

| 项 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| `personality` | 运行时走 `thread/personality/set`，参数是 `threadId + personality + args`，并直接发 `/personality` 到活跃 CLI；见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:101-103`、`go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:331-337` | 路由还在，但只是 `newThreadCommandHandler(svc, "/personality")`；严格参数只认 `threadId + args`，再映射到 `session.Configure(Personality)`；见 `internal/module/thread/rpc.go:63-64`、`internal/module/thread/rpc_types.go:34-37`、`internal/module/thread/command.go:37-38,130-147` | `⚠️` |
| `effort` | 是 `thread/config/get|set` 的一等字段，并有枚举校验；见 `go-agent-v2/internal/apiserver/methods_thread.go:347-359`、`go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:75-83` | V3 `threadConfigResult` 仍保留 `effort` 字段，但 `configGetResult()` 不填；`ThreadConfigPatch` 根本没有 `Effort`；`thread/config/set` 也不是 typed setter；见 `internal/module/thread/command.go:74-85,100-109`、`internal/dto/provider/thread_config.go:3-7` | `❌` |
| `approvalPolicy` | 启动参数叫 `approvalPolicy`，运行时 setter 则是 `thread/approvals/set`，字段名是 `policy`；见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:330-337,361-370` | V3 `thread/start` 仍叫 `approvalPolicy`，但运行时只剩 `thread/approvals/set(args)` 这层壳；provider patch 字段名又叫 `Approvals`；见 `internal/module/thread/rpc_types.go:7-16,34-37`、`internal/module/thread/rpc.go:65-66`、`internal/dto/provider/thread_config.go:3-7` | `⚠️` |

补充：

- `thread/approvals/set` 在 V2 也是 live `/approvals` slash，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:104-106`
- V3 对 `approvals` 的运行时更新同样依赖 `session.Configure(...)`，codexapp 会远端回落到 V2 `thread/approvals/set`，claudecli active session 不支持

## 最终判断

如果目标是“V2↔V3 1:1 对齐”，当前状态是：

- `thread/config/get`: `⚠️`，只有路由名和部分返回形状对上，真实值语义没有对上
- `thread/config/set`: `❌`，typed config setter 已丢失，现状不可用
- `thread/model/set`: `⚠️`，表面可调，但 live `/model` 语义已变成 provider-dependent `Configure`
- `thread/compact/start`: `❌`，只有壳，没有真正 compact
- `personality`: `⚠️`
- `effort`: `❌`
- `approvalPolicy`: `⚠️`

一句话总结：

- V2 的 `config` 面是“typed thread launch config + 若干 live slash command”混合体。
- V3 现在把它们大多压到了 `thread.Service.SendCommand(...)`，但这个面只完整覆盖了少量命令，导致 `config/set`、`compact/start` 和 `effort` 明显失配，`model/personality/approvals` 也只剩部分兼容。
