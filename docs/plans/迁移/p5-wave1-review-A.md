# P5 波次 1 审查 A

## 1. 方法覆盖

`go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113` 实际注册了 35 个方法。逐项对照波次 1 计划如下。

| 方法 | 波次 1 归宿 | 结论 |
| --- | --- | --- |
| `thread/start` | R1 `module/thread/rpc.go` | 有归宿；但不能挂 `ThreadScope`，且当前 `thread.Service` 无 `Start` 能力。 |
| `thread/resume` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `Resume` 能力。 |
| `thread/recover` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `Recover/ResolveRecoverable` 能力。 |
| `thread/fork` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `Fork` 能力。 |
| `thread/archive` | R1 `module/thread/rpc.go` | 有归宿；`thread.Service.Archive` 已存在。 |
| `thread/unarchive` | R1 `module/thread/rpc.go` | 有归宿；`thread.Service.Unarchive` 已存在。 |
| `thread/delete` | R1 `module/thread/rpc.go` | 有归宿；`thread.Service.Delete` 已存在。 |
| `thread/name/set` | R1 `module/thread/rpc.go` | 有归宿；`thread.Service.SetName` 已存在。 |
| `thread/compact/start` | R1 `module/thread/rpc.go` | 有归宿；必须加 `CapabilityGate("context_compact")`，当前 `thread.Service` 无能力。 |
| `thread/rollback` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `Rollback` 能力。 |
| `thread/list` | R1 `module/thread/rpc.go` | 有归宿；`thread.Service.List` 存在，但当前返回类型过窄。 |
| `thread/loaded/list` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `LoadedList` 能力。 |
| `thread/read` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service.Get` 仅返回 `Ref`，不足以对齐 V2。 |
| `thread/resolve` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `Resolve` 能力。 |
| `thread/config/get` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `ConfigGet` 能力。 |
| `thread/config/set` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无 `ConfigSet` 能力。 |
| `thread/messages` | R1 `module/thread/rpc.go` | 有归宿；`thread.Service.ReadMessages` 已存在。 |
| `thread/backgroundTerminals/clean` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service.SendCommand` 不支持 `/clean`。 |
| `turn/start` | R2 `module/turn/rpc.go` | 有归宿；必须加 `CapabilityGate("message_send")`，且当前 `turn.Service` 还要求调用方先拿到 `contract.Session`。 |
| `turn/steer` | R2 `module/turn/rpc.go` | 有归宿；必须加 `CapabilityGate("message_send")`，当前 `turn.Service` 无 `Steer` 能力。 |
| `turn/interrupt` | R2 `module/turn/rpc.go` | 有归宿；当前 `turn.Service.InterruptTurn` 存在，但仍要求调用方先拿到 `contract.Session`。 |
| `turn/forceComplete` | R2 `module/turn/rpc.go` | 有归宿；当前 `turn.Service` 无 `ForceComplete` 能力。 |
| `thread/realtime/start` | R1 `module/thread/rpc.go` | 有归宿；必须加 `CapabilityGate("realtime")`，当前 `thread.Service` 无能力。 |
| `thread/realtime/appendAudio` | R1 `module/thread/rpc.go` | 有归宿；必须加 `CapabilityGate("realtime")`，当前 `thread.Service` 无能力。 |
| `thread/realtime/appendText` | R1 `module/thread/rpc.go` | 有归宿；必须加 `CapabilityGate("realtime")`，当前 `thread.Service` 无能力。 |
| `thread/realtime/stop` | R1 `module/thread/rpc.go` | 有归宿；必须加 `CapabilityGate("realtime")`，当前 `thread.Service` 无能力。 |
| `review/start` | R2 `module/turn/rpc.go` | 有归宿；当前只能是骨架，`turn.Service` 内无 review 能力。 |
| `thread/undo` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service.SendCommand` 不支持 `/undo`。 |
| `thread/model/set` | R1 `module/thread/rpc.go` | 有归宿；必须加 `CapabilityGate("model_switch")`，当前只能通过 `SendCommand("/model")` 包装。 |
| `thread/personality/set` | R1 `module/thread/rpc.go` | 有归宿；可由 `SendCommand("/personality")` 包装。 |
| `thread/approvals/set` | R1 `module/thread/rpc.go` | 有归宿；可由 `SendCommand("/approvals")` 包装。 |
| `thread/mcp/list` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service.SendCommand` 不支持 `/mcp`。 |
| `thread/skills/list` | R1 `module/thread/rpc.go` | 有归宿；当前 `thread.Service` 无能力。 |
| `thread/debugMemory` | 无明确归宿 | 波次 1 计划写成“thread/* 全部方法”会把它带入 R1，但 `docs/plans/迁移/p5-execution-plan.md:120-122` 已把它归到 `platform/rpc/debug.go`。该方法不应进 `module/thread/rpc.go`。 |
| `mock/experimentalMethod` | 无归宿 | 该方法是 compat noop；`docs/plans/迁移/p5-execution-plan.md:120-122` 已把它归到 `platform/rpc/debug.go`。波次 1 不应在 R1/R2 重复占位。 |

补充：

- `approval/respond` 不在 `methods_thread_turn.go`；它来自 `go-agent-v2/internal/apiserver/methods.go:157-163`，波次 1 计划把它放到 R2 是对的。
- R1 的“~25 个方法”低估了真实规模。仅 `thread/*` 在该文件里就有 29 个；若按执行计划把 `thread/debugMemory` 排除，仍有 28 个。

## 2. 依赖方向

### `module/thread/rpc.go` 只注入 `thread.Service`

`internal/module/thread/contract.go:9-21` 当前只暴露 11 个能力：

- `List`
- `Get`
- `ReadHistory`
- `ReadMessages`
- `Archive`
- `Unarchive`
- `ListByStatus`
- `ListByCWD`
- `SendCommand`
- `SetName`
- `Delete`

这不足以覆盖 R1。缺口至少包括：

- `thread/start`
- `thread/resume`
- `thread/recover`
- `thread/fork`
- `thread/rollback`
- `thread/loaded/list`
- `thread/resolve`
- `thread/config/get`
- `thread/config/set`
- `thread/compact/start`
- `thread/realtime/*`
- `thread/skills/list`

`internal/module/thread/command.go:12-41` 还显示 `SendCommand` 目前只支持：

- `/model`
- `/personality`
- `/approvals`
- `/interrupt`

因此这些 compat 路由现在也无法通过 `thread.Service` 落地：

- `thread/backgroundTerminals/clean`
- `thread/compact/start`
- `thread/undo`
- `thread/mcp/list`

另外，`internal/module/thread/contract.go:23-26` 的 `Ref` 只有 `ID/Name`，对 `thread/read`、`thread/list`、`thread/loaded/list` 来说响应面也不够。

结论：`module/thread/rpc.go` 若只注入当前 `thread.Service`，R1 不可执行。至少需要先扩展 contract，或把部分路由明确划到其他 facade。

### `module/turn/rpc.go` 注入 `turn.Service` + `ApprovalManager`

`internal/module/turn/contract.go:12-17` 当前只有：

- `PrepareTurn`
- `StartTurn`
- `InterruptTurn`
- `TrackTurn`

直接缺少：

- `turn/steer`
- `turn/forceComplete`
- `review/start`
- `approval/respond`

更关键的是，`PrepareTurn`、`StartTurn`、`InterruptTurn` 都要求调用方先提供 `contract.Session`。但波次 1 计划只写“handler 注入 `turn.Service` + `ApprovalManager`”，没有任何 session resolver。仅凭当前注入集合，连 `turn/start` 和 `turn/interrupt` 都无法调用。

再往下看，`internal/contract/provider.go:23-37` 的 `Session` 只有：

- `StartTurn`
- `Interrupt`
- `ListThreads`
- `ForkThread`
- `ReadHistory`
- `Configure`
- `Close`
- `ForceStop`

它也没有：

- `Steer`
- `Realtime`
- `ReviewStart`
- `ThreadConfigGet`

结论：R2 的问题不是 `ApprovalManager` 接不进来，而是 `turn.Service` 乃至底层 `Session` 契约都还没补齐。

## 3. 中间件

`internal/platform/rpc/handler.go` 与 `internal/platform/rpc/strict.go` 的结论如下。

- `ThreadScope` 已支持多 field。`internal/platform/rpc/handler.go:30-54` 通过可变参数支持多个字段名，默认支持 `threadId/threadID/thread_id`。
- `CapabilityGate` 已就绪。`internal/platform/rpc/handler.go:57-72` 会在能力缺失时返回 `CodeCapabilityGate` 错误。
- `StrictHandler` 已就绪。`internal/platform/rpc/strict.go:11-17` 使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`。
- `Validate` 不是替代品。`internal/platform/rpc/handler.go:96-100` 目前是 no-op。

与 V2 对照：

- `typedHandler` 可以由 `StrictHandler` 覆盖。
- `withRequiredThreadID` 可以由 `ThreadScope` 覆盖，但只适用于“顶层 params 里直接有 threadId 字段”的路由。
- `capabilityGuard` 可以由 `CapabilityGate` 覆盖，但响应语义已改变。V2 `capabilityGuard` 返回 compat result，当前 `CapabilityGate` 返回 jrpc2 error。

限制：

- `ThreadScope` 不能全量套到所有 `thread/*`。`thread/start`、`thread/list`、`thread/loaded/list`、`thread/skills/list`、`thread/debugMemory` 都没有 `threadId`；若字面执行“thread/* 全挂 `ThreadScope`”，这些路由会直接报 `threadId is required`。
- `CapabilityGate` 也不能按文件级统一挂载，只能逐方法挂。波次 1 至少需要区分：
  - `thread/compact/start` -> `context_compact`
  - `thread/realtime/*` -> `realtime`
  - `thread/model/set` -> `model_switch`
  - `turn/start`、`turn/steer` -> `message_send`
- 当前树里只有 `CapabilityResolver` 类型定义，没有现成 resolver 实现或使用点。若 handler 只拿 `thread.Service`/`turn.Service`，也没有地方取活跃 provider capability。
- `ThreadScope` 目前只支持“按字段名取字符串”，并不覆盖执行计划里写的 `agent_id` / `cwd` / request context 推导。

结论：中间件基件可用，但“全路由统一套一层”不可行，必须按方法粒度装配。

## 4. approval 接线

注入方向本身可行：

- `internal/platform/rpc/module.go:12-18` 已通过 Fx 提供 `NewApprovalManager`。
- `internal/archtest/dependency_direction_test.go:90-95` 只禁止 `platform -> module`，没有禁止 `module -> platform`。
- 当前仓库里 `module` 也已经直接 import `platform/*`，例如 `internal/module/turn/service.go:13` import `internal/platform/config`。

因此，`module/turn/rpc.go` 若直接 import `internal/platform/rpc` 并注入 `*rpc.ApprovalManager`，不违反当前代码树的硬性规则。

真正的阻断在公开契约：

- V2 `approval/respond` 参数是 `requestId/approved/decision`，见 `go-agent-v2/internal/apiserver/server_approval.go:456-483`。
- 当前 `ApprovalManager.Respond` 只接受 `callID string`，见 `internal/platform/rpc/approval.go:106-117`。
- `ApprovalManager` 的 pending map 也按 `callID` 建索引，见 `internal/platform/rpc/approval.go:17-20,126-147`。
- 虽然回调发给客户端时同时带了 `requestId` 和 `callId`，见 `internal/platform/rpc/approval_events.go:40-60`，但当前 `ApprovalManager` 没有“按 requestId 查 pending”的公开 API。

结论：

- 如果新 RPC 面允许 `approval/respond` 直接吃 `callId`，当前接线可以成立。
- 如果必须保持 V2 的 `requestId` 对外契约，当前 `ApprovalManager` 还缺一层 `requestId -> pending/callId` 解析。

## 5. review 骨架

先建 `review/start` RPC 入口是合理的，原因只有两个：

- 路由归属需要尽早冻结到 `module/turn/rpc.go`。
- `rpc_handlers`、中间件链、错误映射可以先接好，避免之后再改公开路由面。

但骨架的返回不能是空 map：

- 空 map 会把“未实现”伪装成“成功”，后续很难再收紧响应契约。
- 当前仓库没有 `ErrNotImplemented`，`internal/platform/rpc/errors.go:5-31` 只有 `NotFound/InvalidState/Conflict/CapabilityGate/ApprovalTimeout`。

建议：

- 最低限度返回显式 jrpc2 error。
- 更干净的做法是在 `platform/rpc/errors.go` 增加 `CodeNotImplemented` / `ErrNotImplemented`。
- 若不接受错误面，就不要在波次 1 先注册 `review/start`。

## 6. handler.Map 输出

R0b 的收集机制已经够用：

- `internal/platform/rpc/module.go:28-44` 定义了 `group:"rpc_handlers"` 的 value-group 收集口。
- `registerAllHandlers` 会把所有 `handler.Map` 统一注册到 RPC server。

因此 R1/R2 只需要产出 `handler.Map` 到该 group。兼容做法如下。

```go
var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewRPCHandlers,
			fx.ResultTags(`group:"rpc_handlers"`),
		),
	),
)
```

约束：

- `rpc.go` 本身不应 import `fx`。`internal/archtest/dependency_direction_test.go:44-58` 明确要求 `module.go` 之外的 `internal/module/*` 文件不能 import `go.uber.org/fx`。
- 也可以返回 `internal/platform/rpc.HandlerMapResult`，但这会把 value-group 类型耦合到 `platform/rpc`。若只为打 group tag，`module.go + fx.ResultTags` 更轻。

兼容性风险：

- `internal/platform/rpc/server.go:29-35` 的 `Register` 对重复 method 名称没有冲突检测，只是后写覆盖前写。
- 因此 `thread/debugMemory`、`mock/experimentalMethod` 这类已在执行计划里归到 `platform/rpc/debug.go` 的方法，不能再被 R1/R2 重复生产，否则最终 owner 取决于注册顺序。

结论：输出模式与 R0b 兼容，但前提是每个方法只有唯一 owner，且 group tag 放在 `module.go`。

## 7. 结论（Blocker / Improvement）

### Blocker

- `module/thread/rpc.go` 只注入当前 `thread.Service` 不可行；contract 和返回类型都覆盖不全。
- `module/turn/rpc.go` 只注入当前 `turn.Service` + `ApprovalManager` 也不可行；`turn.Service` 既缺方法，也缺 session resolver。
- `approval/respond` 若保持 V2 `requestId` 公开契约，当前 `ApprovalManager` 无法直接接线。
- “thread/* 全挂 `ThreadScope + CapabilityGate`”不可行；必须按方法粒度装配，否则会误伤无 `threadId` 路由。
- `thread/debugMemory` 与 `mock/experimentalMethod` 不应进入波次 1 的 R1/R2；否则会和 `platform/rpc/debug.go` 发生 owner 冲突。

### Improvement

- 先补 `thread.Service` / `turn.Service` facade，再写 R1/R2；否则 `rpc.go` 只能退化成 transport 直连 provider/platform。
- 明确 capability source；当前只有 `CapabilityGate` 抽象，没有 resolver 接线方案。
- 为 `review/start` 增加显式 not-implemented error，而不是返回空成功结果。
- 在各模块 `module.go` 中用 `fx.ResultTags('group:"rpc_handlers"')` 输出 `handler.Map`，不要在 `rpc.go` 中引入 `fx`。
