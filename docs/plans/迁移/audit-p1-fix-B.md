# P1 修复审查 — Agent B

## 1. Orchestration 修复（B17/B18/B16/B19/B20 逐项验证+行号）

### B17：AgentSnapshot json tag

- `OK`：`AgentSnapshot` 全字段均为 snake_case json tag，和 V2 `AgentInfo` 一致。证据：`internal/sidecar/orch/orchestration/contract.go:40-50`，`go-agent-v2/internal/runner/manager.go:147-157`。
- `Blocker`：`snapshotLocked` 未同步完整字段集。当前仅写入 `ID/Name/ParentID/Port/ThreadID/Cwd/State`，其中 `Port` 被硬编码为 `0`，`Provider/LastReport` 完全未赋值；同时 `agentRuntime` 结构本身也没有 `provider/lastReport` 字段，导致 `agent.list` / `agent.snapshot` 永远无法返回与 V2 同等信息。证据：`internal/sidecar/orch/orchestration/service.go:34-54`，`internal/sidecar/orch/orchestration/service.go:193-202`，`internal/sidecar/orch/orchestration/service.go:198`，`go-agent-v2/internal/runner/manager.go:289-299`。

### B18：agent.list 排序

- `OK`：`ListAgents` 返回前执行了 `sort.SliceStable`，排序键为 `ID -> Name -> Port`，与 V2 `List()` 一致；`sort` import 已存在。证据：`internal/sidecar/orch/orchestration/service.go:10`，`internal/sidecar/orch/orchestration/service.go:162-178`，`go-agent-v2/internal/runner/manager.go:278-313`。

### B16：launchParams 补齐 ParentID/Env

- `OK`：`launchParams` 已包含 `parentId` 与 `env`；handler 已映射到 `LaunchRequest.ParentID` 和 `LaunchRequest.Env`；`envList` 先对 key 排序再构造 `KEY=VALUE`，顺序稳定；launch 数据后续已落到 runtime 并进入 `cmd.Env`。证据：`internal/sidecar/orch/orchestration/rpc_types.go:7-13`，`internal/sidecar/orch/orchestration/rpc.go:58-66`，`internal/sidecar/orch/orchestration/rpc.go:101-114`，`internal/sidecar/orch/orchestration/helpers.go:33-43`，`internal/sidecar/orch/orchestration/service.go:205-208`。

### B19：agent.submit / agent.submitPrompt 接线

- `OK`：两条 RPC 均已接到 `svc.SubmitTurn`。`agent.submit` 通过 `submissionFromInput` + `decodeInputItems` 转为 `TurnSubmission`；`agent.submitPrompt` 直接构造 `InputItem{Type:"text", Content: p.Prompt}`，字段名与 shared DTO 一致。证据：`internal/sidecar/orch/orchestration/rpc.go:20-39`，`internal/sidecar/orch/orchestration/rpc.go:69-90`，`internal/dto/shared/input.go:3-9`，`internal/sidecar/orch/orchestration/service.go:141-160`。
- `Blocker`：参数契约仍与 V2 不兼容。V3 `submitParams` 只接受 `agentId + input`，`submitPromptParams` 只接受 `agentId + prompt`；V2 则把 `agent.submit` 与 `agent.submitPrompt` 都绑定到同一个 `agentSubmitParams`，并且契约测试明确要求二者参数键都为 `agent_id / prompt / images / files`。迁移阶段若仍有 V2 客户端或脚本调用，这里会出现解码失败或字段丢失。证据：`internal/sidecar/orch/orchestration/rpc_types.go:48-55`，`go-agent-v2/internal/apiserver/methods_orchestration.go:16-17`，`go-agent-v2/internal/apiserver/methods_orchestration.go:73-78`，`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:224-225`。

### B20：task/dag/list 参数

- `OK`：`listDAGsParams` 已包含 `status/keyword/limit`，字段语义与 `ListDAGsFilter` 对齐。证据：`internal/sidecar/orch/orchestration/rpc_types.go:58-61`，`internal/store/taskdag/contract.go:41-45`。
- `Warning`：`task/dag/list` 仍是 `newNotImplementedHandler`，尚未把参数真正映射到 store；另外 RPC 层 `Limit` 是 `int`，store 层是 `int32`，后续实现时仍需显式收敛类型。证据：`internal/sidecar/orch/orchestration/rpc.go:52`，`internal/sidecar/orch/orchestration/rpc_types.go:61`，`internal/store/taskdag/contract.go:44`。

## 2. 跨模块一致性（fx/handler.Map/编译守卫）

- `OK`：skill module 移除 `prompt.Store` 后，`Module` 仅提供 `NewService` 与 `NewSkillHandlers`；`service` 结构和构造函数只依赖 `commandcardstore.Store`，fx 图在当前仓库下可通过构建守卫。证据：`internal/module/skill/module.go:5-8`，`internal/module/skill/service.go:17-30`。
- `OK`：orchestration 新增的 `agent.submit` / `agent.submitPrompt` key 已进入 `handler.Map`；`HandlerMapResult` 通过 `group:"rpc_handlers"` 聚合，`registerAllHandlers` 会统一注册到 RPC server。证据：`internal/sidecar/orch/orchestration/rpc.go:16-55`，`internal/platform/rpc/module.go:31-46`。
- `OK`：workspace handler 拆分后，`NewWorkspaceHandlers` 仍返回 `rpc.HandlerMapResult`，`workspace/module.go` 继续提供该构造，聚合路径没有断。证据：`internal/module/workspace/rpc.go:13-24`，`internal/module/workspace/module.go:5-8`，`internal/platform/rpc/module.go:31-46`。
- `OK`：三处目标模块的 `fx.Provide` 都位于各自 `module.go`，结构完整，没有出现脱离 module 的 `fx` 注入。证据：`internal/sidecar/orch/orchestration/module.go:5-12`，`internal/module/skill/module.go:5-8`，`internal/module/workspace/module.go:5-8`。
- `OK`：按各模块 `handler.Map` 收集公开 RPC key，`orchestration / skill / thread / turn / workspace` 五个模块之间未发现重复键。证据：`internal/sidecar/orch/orchestration/rpc.go:17-54`，`internal/module/skill/rpc.go:20-63`，`internal/module/thread/rpc.go:20-80`，`internal/module/turn/rpc.go:33-91`，`internal/module/workspace/rpc.go:15-22`。

### 编译守卫

- `go build ./...`：通过。
- `go vet ./...`：通过。
- `go test ./internal/archtest/... -count=1`：通过，输出为 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 4.005s`。相关 size guard 阈值定义见 `internal/archtest/guardlib.go:18-24` 与 `internal/archtest/guardlib.go:253-258`。

## 3. Skill + Workspace 快扫

- `OK`：skill 的 shell 执行入口仍为 unexported helper。公开方法是 `ExecCommand`，shell 路径仅通过 `execShell` 被 `RunCard` 内部调用，没有额外公开 surface。证据：`internal/module/skill/exec.go:27-33`，`internal/module/skill/cards.go:93-106`。
- `OK`：`service.go` 中未见 `prompt.Store` 残留；`service` 当前仅持有 `cards/root/http` 三个字段。证据：`internal/module/skill/service.go:17-30`。
- `OK`：`workspace/rpc.go` 拆分后的函数尺寸仍在 archtest 守卫内。当前文件总行数停在 `internal/module/workspace/rpc.go:142`，而守卫上限为 `MaxFileLines=400`、`MaxFuncLines=80`。证据：`internal/module/workspace/rpc.go:13-142`，`internal/archtest/guardlib.go:18-19`。

## 结论（Blocker / Warning / OK）

- `Blocker`：`AgentSnapshot` 的 tag 对齐已完成，但数据构造未闭环，`Port` 恒为 `0`，`Provider/LastReport` 不可达，`agent.list` / `agent.snapshot` 仍与 V2 信息面不一致。证据：`internal/sidecar/orch/orchestration/service.go:34-54`，`internal/sidecar/orch/orchestration/service.go:193-202`，`go-agent-v2/internal/runner/manager.go:289-299`。
- `Blocker`：`agent.submit` / `agent.submitPrompt` 的 handler 已接线，但 RPC 参数契约仍偏离 V2；若迁移目标包含 V2 调用兼容，这一项未完成。证据：`internal/sidecar/orch/orchestration/rpc_types.go:48-55`，`go-agent-v2/internal/apiserver/methods_orchestration.go:16-17`，`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:224-225`。
- `Warning`：`task/dag/list` 参数层已补齐，但方法仍未实现，且 `limit` 类型存在 `int -> int32` 收敛点。证据：`internal/sidecar/orch/orchestration/rpc.go:52`，`internal/sidecar/orch/orchestration/rpc_types.go:58-61`，`internal/store/taskdag/contract.go:41-45`。
- `OK`：B18 排序、B16 `ParentID/Env` 传递、skill/workspace/module wiring、编译守卫均已通过本轮审查。证据：`internal/sidecar/orch/orchestration/service.go:162-178`，`internal/sidecar/orch/orchestration/rpc.go:58-66`，`internal/sidecar/orch/orchestration/rpc.go:101-114`，`internal/module/skill/module.go:5-8`，`internal/module/workspace/rpc.go:13-24`，`internal/archtest/guardlib.go:18-24`。
