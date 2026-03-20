# 审查：module/turn

## 方法与口径

- 审查范围：`internal/module/turn/` 全部 11 个文件。
- 代码读取使用 LSP：`text_search`、`workspace_symbol`、`document_symbol`、`references(compact)`、`call_hierarchy`、`read_file`。
- 文件清单与行数通过只读命令补充：`rg --files`、`wc -l`；未使用 `grep/find/cat/sed/awk`。
- 对照基线：
  - V3 `turn` 模块：`internal/module/turn/*.go`
  - V2 turn/review：`go-agent-v2/internal/apiserver/methods_thread_turn.go`、`go-agent-v2/internal/apiserver/methods_turn.go`
  - V2 approval：`go-agent-v2/internal/apiserver/server_approval.go`
  - V3 approval lifecycle：`internal/platform/rpc/approval*.go`

## 总结

- 结论：`internal/module/turn` 当前更像“turn service + 半成品 RPC 适配层”，不是 V2 等价迁移。
- `rpc.go` 的 6 个 handler key 都在，但只有 5 个有行为，其中 `review/start` 仍是明确的 `ErrNotImplemented`。
- `PrepareInput`、assembler、skills、manifest 这些 service 能力在实现上并不空，但 RPC 入口没有把大部分字段喂进去，导致很多能力“代码存在、handler 走不到”。
- 最关键的迁移阻塞有 4 个：
  1. `turn/start`、`turn/steer` 的参数面和返回面都与 V2 不兼容。
  2. `review/start` 还未实现，而且参数类型已经先天不对。
  3. manifest 在 RPC 路径上拿不到 `BinaryDir`，会生成 `/go-agent-mcp-*` 根路径命令。
  4. `approval/respond` 虽然接上了 `ApprovalResponder`，但 wire contract 与 V2 不同，且 `decision` 只写入 raw detail，不做归一化。

## 1. 文件清单与行数

| 文件 | 行数 | 备注 |
| --- | ---: | --- |
| `internal/module/turn/assembler.go` | 291 | 输入组装与归一化 |
| `internal/module/turn/contract.go` | 44 | `Service` 与输入/状态类型 |
| `internal/module/turn/manifest.go` | 14 | MCP manifest 包装器 |
| `internal/module/turn/module.go` | 15 | fx 注册 |
| `internal/module/turn/rpc.go` | 93 | 6 个 RPC handler |
| `internal/module/turn/rpc_helpers.go` | 14 | RPC -> `PrepareInput` 适配 |
| `internal/module/turn/rpc_types.go` | 37 | RPC 参数/结果类型 |
| `internal/module/turn/service.go` | 294 | service 主实现 |
| `internal/module/turn/service_test.go` | 265 | service 级测试 |
| `internal/module/turn/skills.go` | 93 | skill 归并与自动匹配 |
| `internal/module/turn/tracker.go` | 234 | in-memory turn tracker |

总计：1394 行。

## 2. handler 完整性

证据：`internal/module/turn/rpc.go:32-91`

| key | 状态 | 结论 |
| --- | --- | --- |
| `turn/start` | 已注册 | 能调用 `PrepareTurn + StartTurn`，但请求/返回不兼容 V2 |
| `turn/steer` | 已注册 | 能调用 `SteerTurn`，但语义退化为“按 prompt 新开 turn” |
| `turn/interrupt` | 已注册 | 有行为，参数面基本对齐 |
| `turn/forceComplete` | 已注册 | 有行为，但只是 `Interrupt(source=force_complete)` 包装 |
| `review/start` | 已注册 | 直接返回 `rpc.ErrNotImplemented("review/start is not yet implemented")` |
| `approval/respond` | 已注册 | 走 `ApprovalResponder.Respond(...)`，但不是 V2 同构返回 |

结论：

- “6 个 key 齐全”这件事成立。
- “6 个 key 功能完整”不成立；真正闭环的只有 `turn/interrupt`，其余都存在不同程度缺口。

## 3. V2 方法对照

### 3.1 `turn/start`

- V2 参数面：`threadId`、`input[]`、`selectedSkills`、`manualSkillSelection`、`cwd`、`approvalPolicy`、`model`、`outputSchema`，见 `go-agent-v2/internal/apiserver/methods_turn.go:30-37` 与 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:458-464`。
- V3 参数面：只有 `threadId`、`prompt`、`images`、`files`、`model`、`effort`，见 `internal/module/turn/rpc_types.go:5-12`。
- V2 返回面：`{"turn":{"id","status"}}`。
- V3 返回面：`{"turnId": "<local-id>"}`，见 `internal/module/turn/rpc.go:41-45`、`internal/module/turn/rpc_types.go:35-37`。

结论：不兼容。V3 现在暴露的是精简、旧式 prompt/images/files 面，不是 V2 的结构化 input 面。

### 3.2 `turn/steer`

- V2 参数面：`threadId`、`expectedTurnId`、`input[]`、`selectedSkills`、`manualSkillSelection`，见 `go-agent-v2/internal/apiserver/methods_turn.go:68-82` 与 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:466-470`。
- V3 参数面：只有 `threadId`、`prompt`，见 `internal/module/turn/rpc_types.go:14-17`。
- V2 语义：对齐当前 active turn，至少保留 `turnId` 对齐结果，见 `go-agent-v2/pkg/agentsdk/service/runtime/turn_prepare_core.go:90-106`。
- V3 语义：`SteerTurn` 直接 `PrepareTurn(...Prompt...)` 后 `StartTurn(...)`，测试名也明确写成 `StartsPromptAsNewTurn`，见 `internal/module/turn/service.go:94-100`、`internal/module/turn/service_test.go:119-142`。

结论：不兼容，而且语义偏差比 `turn/start` 更大。V2 是“steer active turn”，V3 是“再发一个新 turn”。

### 3.3 `turn/interrupt`

- V2 参数面：`threadId`、`source`，见 `go-agent-v2/internal/apiserver/methods_turn.go:84-87` 与 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:304`。
- V3 参数面：相同，见 `internal/module/turn/rpc_types.go:19-22`。
- V2 返回面：schema 期望 `{"ok": true}`。
- V3 返回面：handler 返回 `nil`，不是 ack map，见 `internal/module/turn/rpc.go:60-65`。
- V3 额外行为：如果 tracker 中存在活跃 turn，会等待 settle，再把状态推进到 terminal，见 `internal/module/turn/service.go:114-125`。

结论：参数面基本对齐，返回面不对齐。

### 3.4 `turn/forceComplete`

- V2 参数面：`threadId`，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:305`。
- V2 行为：调用 provider 的专门 `TurnForceComplete(threadID)`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:67-71`。
- V3 行为：没有专门 contract，只是 `session.Interrupt(..., Source: "force_complete")`，见 `internal/module/turn/service.go:128-144`。
- V3 返回面同样是 `nil`，不是 `{"ok": true}`。

结论：部分兼容。名字保住了，但 contract 和语义都降级了。

### 3.5 `review/start`

- V2 参数面：`threadId`、`target`、`delivery`，并有 `target.type` / `instructions` / `branch` / `sha` 校验，见 `go-agent-v2/internal/apiserver/methods_turn.go:116-186`。
- V3 参数面：handler 只接 `threadIDOnlyParams`，见 `internal/module/turn/rpc.go:74-77`、`internal/module/turn/rpc_types.go:24-26`。
- V3 当前实现：直接 `ErrNotImplemented`。

结论：缺失，而且不是“只差实现”，而是 RPC 参数类型本身就还没迁过来。

### 3.6 `approval/respond`

- V2 参数面：`requestId int64`、`approved *bool`、`decision any`，见 `go-agent-v2/internal/apiserver/server_approval.go:456-460`。
- V2 返回面：`{"ok": true/false, "status": ...}`，见 `go-agent-v2/internal/apiserver/server_approval.go:462-483` 与 `go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:227`。
- V3 参数面：`callId string`、`requestId *int64`、`approved *bool`、`decision json.RawMessage`，见 `internal/module/turn/rpc_types.go:28-33`。
- V3 返回面：handler 成功时返回 `nil`，失败时直接抛 error，见 `internal/module/turn/rpc.go:79-91`。

结论：内部 approval manager 对接上了，但对外 RPC 契约不是 V2 的 shape。

## 4. Service 接口

证据：`internal/module/turn/contract.go:12-19`、`internal/module/turn/rpc.go:33-90`

| `Service` 方法 | RPC 调用方 | 结论 |
| --- | --- | --- |
| `PrepareTurn` | `turn/start` 直接调用；`turn/steer` 经 `SteerTurn -> PrepareTurn` 间接调用 | 已接线 |
| `StartTurn` | `turn/start` 直接调用；`turn/steer` 经 `SteerTurn -> StartTurn` 间接调用 | 已接线 |
| `SteerTurn` | `turn/steer` | 已接线，但语义与 V2 不同 |
| `InterruptTurn` | `turn/interrupt` | 已接线 |
| `ForceCompleteTurn` | `turn/forceComplete` | 已接线，但 contract 降级 |
| `TrackTurn` | 无 RPC 调用方 | 孤立 service 能力 |

补充：

- `review/start` 不在 `Service` 里，说明 turn service 本身并没有 review 能力抽象。
- `approval/respond` 也不走 `Service`，而是直接依赖 `contract.ApprovalResponder`，见 `internal/module/turn/rpc.go:79-91`。

结论：`Service` 不是 turn 模块完整 RPC facade，只覆盖 turn lifecycle 的一部分。

## 5. assembler

证据：`internal/module/turn/assembler.go:47-291`

已有能力：

- 支持 `text`、`filecontent`、`image`、`local_image`、`mention` 五类输入归一化。
- 做了去重、数量上限、UTF-8 截断、可执行文件扩展名拒绝、图片/data-url/远程 URL 处理。
- `PromptText` 能把 `Prompt + text/filecontent` 拼成 skill auto-match 的输入文本。

真实缺口：

- RPC 入口没有 `input []InputItem` 字段；`buildPrepareInput` 只填 `Prompt/Images/Files/Model/Effort/ThreadCaps`，见 `internal/module/turn/rpc_helpers.go:5-14`。
- 这意味着 assembler 里对 `filecontent`、`local_image`、显式 `name/url/path`、fallback 归一化等能力，在 RPC 路径下都走不到。
- `Files []string` 被硬映射成 `mention`，`Images []string` 被硬映射成 `image`，仍然是旧式二元输入面，见 `internal/module/turn/assembler.go:88-100`。

结论：assembler 自身实现并不空，但 RPC 装配不完整，导致 rich input 能力基本不可达。

## 6. tracker

证据：`internal/module/turn/tracker.go:13-234`、`internal/module/turn/service.go:75-90,102-125,158-205`

已有机制：

- in-memory `map[localID]*trackedTurn`，状态机至少覆盖 `preparing/running/interrupting/completed/interrupted/failed/stalled`。
- `StartTurn` 会依次 `Cleanup -> Start -> AttachHandle -> BindProviderID -> Update("running") -> watchTurn`。
- `InterruptTurn` 会按 thread 找 active turn，标记 `interrupting`，再等待 handle / tracker settle。
- `watchTurn` 有 30 分钟 TTL；超时会 `Stall(localID, "turn watch timed out")`。

缺口：

- 没有持久化；进程重启后状态全部丢失。
- 没有 RPC handler 暴露 tracker 状态；`TrackTurn` 只在 service/test 中可见，见 `internal/module/turn/service.go:146-156`。
- `GetByProviderID` 完全没有调用方，见 `internal/module/turn/tracker.go:204-217`。
- `ForceCompleteTurn` 不会直接改 tracker，只能等 watcher 根据 handle 结果收尾，测试也明确是这个设计，见 `internal/module/turn/service_test.go:144-185`。

结论：tracker 够支撑“本进程内 turn 启停追踪”，但还不是对外可用的 turn state 子系统。

## 7. skills 集成

证据：`internal/module/turn/skills.go:11-93`、`internal/module/turn/service.go:49-55`

已有能力：

- 显式 skills 会按名字归一化、去重、prompt 合并，见 `mergePromptText`。
- candidate skills 会基于 prompt 文本自动匹配，规则是 `[skill:name]`、`@name`、或普通子串包含。

缺口：

- RPC 路径没有 `selectedSkills`、`candidateSkills`、`manualSkillSelection` 这些字段。
- `skillResolver` 只处理传入的 `dto.SkillRef` 切片，不做真实 skill registry 查询或合法性校验。
- auto-match 规则比较粗糙，是纯字符串匹配，没有边界控制，容易误命中。
- 当前覆盖这块的测试只在 service 层直构 `PrepareInput`，并不保护 RPC shape，见 `internal/module/turn/service_test.go:16-55`。

结论：skill resolution 和 prompt merging 在 service 里是存在的，但还没有形成 V2 那种可从 RPC 结构化输入驱动的完整链路。

## 8. manifest

证据：`internal/module/turn/manifest.go:7-14`、`internal/dto/provider/manifest.go:28-42`、`internal/module/turn/rpc_helpers.go:5-14`

现状：

- `manifestBuilder.Build` 只是把 `PrepareInput` 的 `AgentID/CWD/ThreadCaps/BinaryDir` 透传给 `dto.BuildManifest(...)`。
- `dto.BuildManifest(...)` 默认总是生成 `go-agent-mcp-lsp` 和 `go-agent-mcp-orch`，如果 thread capability 有 `ida` 再追加 `go-agent-mcp-ida`。

关键问题：

- RPC 路径只填了 `ThreadCaps`，没有填 `BinaryDir`。
- `dto.BuildManifest(...)` 拼命令时直接做 `ctx.BinaryDir + "/go-agent-mcp-" + family`，所以当 `BinaryDir == ""` 时，产物会变成：
  - `/go-agent-mcp-lsp`
  - `/go-agent-mcp-orch`
- 这不是 PATH 查找，而是根目录绝对路径，明显是高风险坏值。

补充：

- `AgentID`、`CWD` 目前在 `dto.BuildManifest` 里还没被使用，但 turn RPC 也没有把它们传入。

结论：manifest builder 本身很薄，真正的问题是 RPC 装配缺字段，导致 manifest 在默认路径下就可能不可执行。

## 9. approval/respond

证据：

- `internal/module/turn/rpc_types.go:28-33`
- `internal/module/turn/rpc.go:79-91`
- `internal/contract/approval.go:7-16`
- `internal/platform/rpc/approval.go:105-116`
- `internal/platform/rpc/approval_support.go:133-142,200-210`
- `internal/platform/rpc/approval_events.go:26-34`

对接情况：

- turn 模块的 `approval/respond` 最终调用 `ApprovalResponder.Respond(callID, requestID, ApprovalDecision{Approved, Detail})`。
- `ApprovalManager.Respond(...)` 会先用 `approvalCallID(callID, requestID)` 归一化 key，所以 V3 这条链既能用 `callId`，也能退回到 `requestId`。
- 从“pending approval 生命周期能不能被 resolve”这个角度看，链路是通的。

与 V2 的差异：

- V2 没有 `callId` 这个对外参数，`requestId` 也是必填正整数；V3 把 `requestId` 改成了可选指针。
- V2 成功/失败都返回结构化 ack；V3 成功返回 `nil`，失败抛 error。

更细的生命周期风险：

- turn 模块对 `decision` 只做了 raw copy：`Detail: append(json.RawMessage(nil), p.Decision...)`。
- 它没有像 `decodeApprovalDecision(...)` 那样把 `"accept"`、`"deny"`、`{"decision":"accept"}` 这类 payload 归一化到 `Approved/Reason`。
- 后续 `publishResolved(...)` 的 `Approved` 字段来自 `decisionApproved(decision)`，它只看 `decision.Approved` 指针。
- 结果是：如果调用方只传 `decision: "accept"` 而不传 `approved: true`，pending 虽然会被 resolve，但 resolved event 的 `Approved` 仍可能是 `false`。

结论：参数类型表面上已经升级到 `decision: RawMessage`、`approved: *bool`，但 lifecycle 语义还没完全对齐 V2 的“decision-only payload 也能被正确归一化”。

## 10. import 方向

检查结果：

- 在 `internal/module/turn/` 下未发现 `internal/provider/...` 方向的 import。
- 当前依赖主要是：
  - `internal/contract`
  - `internal/dto/provider`
  - `internal/dto/shared`
  - `internal/platform/rpc`
  - `internal/platform/config`
  - `go.uber.org/fx`

结论：

- “禁止反向 import provider/ 实现层”这一条满足。
- `internal/dto/provider` 只是 DTO/能力集合，不属于反向依赖 provider driver 实现。

## 11. fx 注册

证据：`internal/module/turn/module.go:7-15`

现状：

- `turn.Module` 只提供两个东西：
  - `NewService`
  - `NewTurnHandlers`（带 `fx.Annotate`）

需要注意的点：

- `SessionResolver` 被标成了 `optional:"true"`，但 `rpc.go` 里它其实是运行时硬依赖；没有它，所有依赖 session 的 handler 都会报 `session resolver is not configured`，见 `internal/module/turn/rpc.go:20-29`。
- `CapabilityResolver` 也被标成了 optional，但 `CapabilityGate` 在 resolver 为 nil 时会把 capability 当成不支持；于是 `turn/start` / `turn/steer` 会稳定失败，而不是降级可用，见 `internal/platform/rpc/handler.go:71-85,94-95` 与 `internal/dto/provider/capability.go:30-35`。
- `ApprovalResponder` 反而没有被标 optional，但 handler 里又写了 nil 防御，DI 口径和运行时口径不一致，见 `internal/module/turn/module.go:10-13`、`internal/module/turn/rpc.go:79-83`。

结论：fx 注册能把模块装起来，但 optional 标注不严谨，会把“装配错误”延迟成“运行时 handler 错误”。

## 12. 函数复杂度

口径：按 `document_symbol` 的 `start/end` 行号做 inclusive line count。

### 12.1 全范围 top 3（含测试）

| 排名 | 函数 | 行数 |
| --- | --- | ---: |
| 1 | `TestInterruptTurnWaitsForSettle` (`internal/module/turn/service_test.go:76-117`) | 42 |
| 2 | `TestForceCompleteTurnLeavesFinalStateToWatcher` (`internal/module/turn/service_test.go:144-185`) | 42 |
| 3 | `TestPrepareTurnKeepsSkillPromptsAndNormalizesInputs` (`internal/module/turn/service_test.go:16-55`) | 40 |

### 12.2 生产代码 top 3

| 排名 | 函数 | 行数 |
| --- | --- | ---: |
| 1 | `(*service).StartTurn` (`internal/module/turn/service.go:61-92`) | 32 |
| 2 | `(*service).watchTurn` (`internal/module/turn/service.go:158-188`) | 31 |
| 3 | `(*skillResolver).Resolve` (`internal/module/turn/skills.go:11-37`) | 27 |

结论：

- 生产代码里真正偏长的是 `StartTurn` 与 `watchTurn`，说明 turn 启动和 watcher 收口逻辑是当前复杂度中心。
- tests 反而比实现更长，说明现在更多是在 service 层做行为样例，而不是在 RPC/模块装配层做契约保护。

## 最终判断

- `module/turn` 目前具备一个可运行的最小 turn service，但还不能视为 V2 `turn + review + approval/respond` 的完整迁移。
- 如果目标是 V2 对齐，优先级应该是：
  1. 先把 `rpc_types.go` / `rpc_helpers.go` 对齐到 V2 input/return shape。
  2. 把 `PrepareInput` 的 `Inputs/Skills/CandidateSkills/OutputSchema/BinaryDir/...` 从 RPC 路径真正接通。
  3. 实现 `review/start`，而不是继续保留 stub。
  4. 给 `forceComplete` 一个独立 contract，而不是复用 interrupt source。
  5. 在 `approval/respond` 里把 decision-only payload 归一化到 `ApprovalDecision`，并补回 V2 风格 ack。
