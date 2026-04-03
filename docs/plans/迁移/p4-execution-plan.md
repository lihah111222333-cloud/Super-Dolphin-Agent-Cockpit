# P4 执行方案（修正版）

## 1. 文档目的

本文件用于替换旧版 P4 拆分摘要，先修正 `docs/plans/迁移/p4-plan-review.md` 中指出的 4 个 Blocker 和 5 个 Improvement，再进入实现排期。

修订后的方案必须同时满足：

- 对齐 `docs/plans/迁移/v3-migration-plan.md` §3 与 §4.5 的 Provider 统一方向。
- 对齐 `docs/plans/迁移/v3-module-migration-details.md` 的包级目标行数和依赖边界。
- 把 P4 真实交付面从“窄口径 provider/unified 方案”修正为“provider + turn + thread + orchestration 补口”的完整执行口径。

## 2. 执行范围与统计口径

### 2.1 三种口径与本方案选用值

| 口径 | 文件数 | 行数 | 用途 |
|---|---:|---:|---|
| `v3-migration-plan.md` §4.5 高层 P4 source：`agentcore/*` + `claude/*` + `codex/*` + `codexadapter/*` + `provider_registry.go` | 43 | 11,018 | 上位批次摘要口径 |
| `provider/unified` 窄口径：在 43 文件基础上再加 `commonadapter/*` + `service/lifecycle/*` + `service/runtime/*` | 49 | 13,040 | 只适合估算统一层，不适合作为整个 P4 执行面 |
| P4 完整执行口径：再加 `service/archive/command/history/listing/messages/rollout` 与 T6 turn/common 补充源 | 59 | 15,803 | 本方案唯一执行口径 |

结论：

- 旧版 `49 文件 / 13,231 行` 不再作为 P4 总范围口径使用。
- 本方案统一采用 `59 文件 / 15,803 行` 作为 P4 执行范围基线。
- `49 文件` 只保留为 `provider/unified` 窄来源说明，不再出现在任务总览、Agent 分配和代码量汇总中。

### 2.2 T6：Turn 服务显式来源

T6 必须显式覆盖以下 19 个 V2 生产文件，不能再只写“turn prepare/runtime”摘要：

- `go-agent-v2/internal/apiserver/commonadapter/common.go`
- `go-agent-v2/internal/apiserver/commonadapter/skills.go`
- `go-agent-v2/legacy-agentsdk/service/common/turn_common_paths.go`
- `go-agent-v2/legacy-agentsdk/service/prompt/turn_prompt_core.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/stream_timeout.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_adapters.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_operations.go`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_steer_alignment.go`
- `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go`
- `go-agent-v2/legacy-agentsdk/service/support/interrupt_state.go`
- `go-agent-v2/legacy-agentsdk/service/support/prompt_match.go`
- `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_core.go`
- `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_lifecycle_core.go`
- `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_rules_core.go`
- `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_stall_core.go`
- `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_state_core.go`
- `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_summary_core.go`

补充说明：

- 其中 `service/common + prompt + interrupt + support + tracker` 是旧摘要遗漏的 turn/common 补入口径，共 11 个生产文件、2,250 行。
- `commonadapter/*` 与 `service/runtime/*` 已包含在 49 文件窄口径中，但在任务拆分里必须显式点名，否则 T6 仍会被低估。

### 2.3 T7：Thread 服务显式来源

T7 的 thread/history 面统一按以下 10 个 V2 生产文件执行：

- `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_core.go`
- `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_io.go`
- `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_ops.go`
- `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_utils.go`
- `go-agent-v2/legacy-agentsdk/service/command/slash_command_logic.go`
- `go-agent-v2/legacy-agentsdk/service/history/thread_history_core.go`
- `go-agent-v2/legacy-agentsdk/service/listing/thread_listing_core.go`
- `go-agent-v2/legacy-agentsdk/service/messages/thread_messages_logic.go`
- `go-agent-v2/legacy-agentsdk/service/rollout/thread_messages_hydration_core.go`
- `go-agent-v2/legacy-agentsdk/service/rollout/thread_messages_rollout_core.go`

补充说明：

- `messages + rollout` 为本次显式补入项，共 3 个生产文件、670 行。
- T7 完整执行面合计 10 个生产文件、2,763 行；旧摘要若只写 `history/archive/listing/command`，则 thread history 迁移不完整。

## 3. Blocker 修正

### 3.1 B1：接口缺口修正

`go-agent-v2/legacy-agentsdk/agentcore/client.go` 暴露的方法面不能直接照搬到 V3，但也不能丢失语义。修正后的归宿如下：

| V2 方法 | V3 归宿 | 方案要求 |
|---|---|---|
| `SetEventHandler` | Session 构造期注入 `*event.Dispatcher`；Session 额外暴露 `Events() <-chan event.Event` 作为只读观察面 | 统一事件主链走 dispatcher，测试与适配层可通过 `Events()` 订阅 |
| `SendDynamicToolResult` | `ToolCallResponder.RespondResult(ctx, callID, output)` | 作为统一 tool-call 回包 contract，不能再丢给字符串命令通道 |
| `RespondError` | `ToolCallResponder.RespondError(ctx, callID, code, msg)` | 与成功回包同属一组 tool-call responder 接口 |
| `Running` | `orchestration.AgentSnapshot.Running bool` | 归 runtime/orchestration metadata；当前骨架未见该字段，纳入 P4 扩充 |
| `Kill` | `Session.Close(ctx)` 只表示 graceful stop；hard-stop 走 `SessionControl.ForceStop(ctx)` 或 orchestration `ForceStop` | 不把硬停语义并入 `Close` |
| `GetPort` | `orchestration.AgentSnapshot.Port int` | 归 runtime metadata；当前骨架未见该字段，纳入 P4 扩充 |
| `SendCommand` | `/interrupt` 归 `Session.Interrupt`；`/compact`、`/model`、`/personality`、`/approvals` 归 `Session.Configure(ctx, ThreadConfigPatch)` | 禁止继续保留字符串命令透传主路径 |

对应的 contract 形态调整为：

```go
type Driver interface {
    Name() string
    StartSession(ctx context.Context, req providerdto.StartSessionRequest) (Session, error)
    ResumeSession(ctx context.Context, req providerdto.ResumeSessionRequest) (Session, error)
}

type Session interface {
    ThreadID() string
    Capabilities() providerdto.CapabilitySet
    Events() <-chan event.Event
    StartTurn(ctx context.Context, req providerdto.TurnRequest) (providerdto.TurnHandle, error)
    Interrupt(ctx context.Context, req providerdto.InterruptRequest) error
    Configure(ctx context.Context, patch providerdto.ThreadConfigPatch) error
    Close(ctx context.Context) error
}

type ToolCallResponder interface {
    RespondResult(ctx context.Context, callID, output string) error
    RespondError(ctx context.Context, callID string, code int, msg string) error
}

type SessionControl interface {
    ForceStop(ctx context.Context) error
}
```

补充约束：

- `StartSessionRequest` 与 `ResumeSessionRequest` 只承载会话启动/恢复所需元数据，不再包含 prompt。
- `SpawnAndConnect` 原先携带的 prompt 语义统一下沉到 turn 层，由 `module/turn` 生成 `TurnRequest`。
- `ListThreads`、`ForkThread`、realtime、session-control 一律视为 capability-gated optional surface，不再假定为双 provider 的必备 happy path。

### 3.2 B2：统计口径修正

- P4 总源面修正为 `59 文件 / 15,803 行`。
- T6 显式点名 `commonadapter + service/common + prompt + runtime + interrupt + support + tracker` 的全部生产文件。
- T7 显式补入 `messages + rollout`，并把 3 个生产文件、670 行纳入 thread/history 子任务。
- `49 文件 / 13,231 行` 从本方案中彻底退役，只保留 `49 文件 / 13,040 行` 的窄口径背景说明。

### 3.3 B3：依赖方向修正

以下 provider-neutral 类型从 `internal/provider/unified` 上提到新目录 `internal/dto/provider/`：

- `TurnRequest`
- `TurnOverrides`
- `StartSessionRequest`
- `ResumeSessionRequest`
- `InterruptRequest`
- `ForkRequest`
- `ForkResult`
- `CapabilitySet`
- `MCPManifest`
- `MCPBinary`
- `ToolFamily`
- `ThreadConfigPatch`
- `InputItem`
- `SkillRef`
- `TurnHandle`

新的依赖方向固定为：

```text
internal/dto/provider
    ^
    |
internal/contract/provider.go
    ^                ^
    |                |
internal/module/turn | internal/provider/unified
    ^                ^
    |                |
internal/module/thread  internal/provider/claudecli / internal/provider/codexapp
```

执行规则：

- `internal/contract/provider.go` 的 `Driver`、`Session`、`ToolCallResponder`、`SessionControl` 统一引用 `dto/provider` 中的类型。
- `module/turn` 和 `provider/unified` 共同依赖 `dto/provider`，不再互相依赖。
- `v3-module-migration-details.md` 中“复用 `provider/unified` 的统一 `TurnRequest`”这一旧描述，在实现时按 `dto/provider.TurnRequest` 落地。

### 3.4 B4：代码量口径修正

P4 需要同时保留两套不冲突的代码量口径。

第一套是与模块迁移文档对齐的“全包归宿口径”，用于判断 P4 是否低估整体替代成本：

| V3 包 | 目标行数 |
|---|---:|
| `provider/unified` | 1,400 - 2,000 |
| `provider/claudecli` | 800 - 1,100 |
| `provider/codexapp` | 1,400 - 2,000 |
| `module/turn` | 900 - 1,300 |
| `module/thread` | 700 - 1,000 |
| `module/orchestration` | 2,200 - 3,200 |
| 合计 | 7,400 - 10,600 |

第二套是本执行计划的“直接改动口径”，用于切分任务和分配 Agent 容量：

| V3 包 | 直接改动目标行数 |
|---|---:|
| `provider/unified` | 1,400 - 2,000 |
| `provider/claudecli` | 800 - 1,100 |
| `provider/codexapp` | 1,400 - 2,000 |
| `module/turn`（新建） | 900 - 1,300 |
| `module/thread`（扩充） | 700 - 1,000 |
| `dto/provider`（新建） | 200 - 300 |
| orchestration 扩充 | 500 - 800 |
| contract 扩充 | 100 - 200 |
| 合计 | 6,000 - 8,700 |

口径解释：

- `7,400 - 10,600` 是与 `v3-module-migration-details.md` 对齐的包级总归宿，必须作为 P4 是否低估的判断基线。
- `6,000 - 8,700` 是本批实际编码面，其中 orchestration 只做 contract、snapshot、force-stop、recovery 对接扩充，不在本文件内重复申明整个 `module/orchestration` 全包重写量。
- 旧版 `4,000 - 5,500` 目标值作废。

## 4. Improvement 纳入方式

### 4.1 I1：EventMapper 改为异构发射器

统一层不再假设“一个 raw event 映射成一个固定泛型输出”。改为 driver 注册一组异构发射器：

```go
type EventTranslator func(raw RawProviderEvent, publish func(ev event.Event))
```

执行要求：

- translator 可以对一个 raw event 发射 0 个、1 个或多个 typed event。
- `provider/unified/event_map.go` 只负责调度 translator，不理解具体 provider 原始事件类型。
- driver 侧各自维护 raw event 解析与 translator 组装。

### 4.2 I2：RegisterDriver 改为 fx 组装配

driver 注册不采用包级全局 `RegisterDriver`，统一改为 `fx.Out`/`fx.In` 组装：

```go
type DriverRegistration struct {
    fx.Out
    Driver DriverFactory `group:"drivers"`
}

type RegistryParams struct {
    fx.In
    Drivers []DriverFactory `group:"drivers"`
}
```

执行要求：

- `provider/claudecli` 与 `provider/codexapp` 各自输出 `DriverFactory`。
- `provider/unified` 在构造期收集 `group:"drivers"` 并生成只读 registry。
- 测试注入通过 `fx.Replace` 或测试模块完成，不引入全局可变状态。

### 4.3 I3：BuildManifest 改为接收 ManifestContext

统一 MCP manifest builder 改为接收线程级上下文：

```go
type ManifestContext struct {
    AgentID    string
    CWD        string
    ThreadCaps CapabilitySet
    BinaryDir  string
    Env        map[string]string
}

func BuildManifest(ctx ManifestContext) MCPManifest { ... }
```

执行要求：

- `cmd/mcp-lsp` 与 `cmd/mcp-orch` 默认挂载。
- `cmd/mcp-ida` 仅在 `ThreadCaps` 启用对应能力时挂载。
- driver 只消费 manifest 结果，不自行生成私有工具 schema。

### 4.4 I4：T4 Codex driver 显式拆三段

| 子任务 | V2 来源 | V3 目标 |
|---|---|---|
| T4a transport | `client_appserver.go` + `client_appserver_transport.go` + `client_appserver_protocol.go` + `client_appserver_helpers.go` + `client_appserver_runtime.go` | `provider/codexapp/transport.go` |
| T4b recovery | `client_appserver_health.go` | `provider/codexapp/recovery.go` |
| T4c history | `rollout_reader.go` | `provider/codexapp/history.go` |

补充说明：

- `event_map.go` 继续保留为独立文件，不并入 transport。
- `driver.go` 与 `module.go` 负责 driver factory、session 启停和 `fx` 装配。

### 4.5 I5：T7 显式补入 messages + rollout

| V2 文件 | 行数 | V3 归宿 |
|---|---:|---|
| `service/messages/thread_messages_logic.go` | 157 | `module/thread/service.go` |
| `service/rollout/thread_messages_hydration_core.go` | 281 | `module/thread/service.go` |
| `service/rollout/thread_messages_rollout_core.go` | 232 | `module/thread/service.go` |

执行要求：

- `module/thread/service.go` 统一拥有 history read、archive、listing、command、messages、rollout 聚合逻辑。
- 不再保留第二条“runtime messages”和“history hydration”并行实现链。

## 5. 修正后的 8 个子任务

### T1：接口层 + DTO 类型上提

- 波次：1
- 依赖：无
- 目标：新建 `internal/dto/provider/`，上提所有 provider-neutral DTO；扩充 `internal/contract/provider.go`，定义 `Driver`、`Session`、`ToolCallResponder`、`SessionControl`。
- 关键内容：补齐 `SetEventHandler`、`SendDynamicToolResult`、`RespondError`、`SendCommand`、`Kill` 的结构化归宿；把 `TurnRequest` 与 `ThreadConfigPatch` 从 unified 中剥离。
- 目标行数：≤ 500

### T2：事件翻译工厂

- 波次：1
- 依赖：无
- 目标：在 `provider/unified/event_map.go` 中建立 `EventTranslator` 工厂、注册表和调度器。
- 关键内容：统一层只处理 translator 调度，不直接依赖 Claude/Codex 原始事件结构。
- 目标行数：≤ 200

### T3：Claude CLI driver

- 波次：2
- 依赖：T1
- 目标：实现 `provider/claudecli/` 5 个文件：`module.go`、`driver.go`、`transport.go`、`event_map.go`、`history.go`。
- 关键内容：CLI spawn、NDJSON 读取、history backend、typed event 翻译。
- 目标行数：≤ 1,100

### T4：Codex app-server driver

- 波次：2
- 依赖：T1
- 目标：实现 `provider/codexapp/` 6 个文件：`module.go`、`driver.go`、`transport.go`、`event_map.go`、`recovery.go`、`history.go`。
- 子段落：T4a transport、T4b recovery、T4c history 三段分开实现与验收。
- 关键内容：删除 `DynamicTools` 路径、保留 app-server transport/recovery 专属复杂度、通过统一 contract 暴露 history。
- 目标行数：≤ 2,000

### T5：统一层核心

- 波次：3
- 依赖：T1 + T3 + T4
- 目标：实现 `provider/unified/` 的 session、client、registry、capability fallback、manifest builder。
- 关键内容：driver registry 通过 `fx` value group 装配；manifest builder 改为 `BuildManifest(ManifestContext)`；session 事件出口固定为 dispatcher + `Events()`。
- 目标行数：≤ 1,500

### T6：Turn 服务

- 波次：3
- 依赖：T1
- 目标：新建 `module/turn/`，覆盖 `prepare`、`runtime`、`review`、`tracker` 责任。
- 显式来源：覆盖 §2.2 列出的全部 19 个 V2 生产文件。
- 关键内容：turn prepare、prompt compose、interrupt、tracker 统一进入 turn 模块；只依赖 `contract` + `dto/provider`。
  - **review 功能推迟到 P5 RPC 层**：`module/turn/review.go` 不在 P4 范围内。原因：`contract.Session` 无 review 专用接口，review 本质是 slash-command，与 RPC handler 层更紧密。
- 目标行数：≤ 1,300

### T7：Thread 服务扩充

- 波次：4
- 依赖：T5
- 目标：扩充 `module/thread/service.go`，吸收 history、archive、listing、command、messages、rollout。
- 显式来源：覆盖 §2.3 列出的全部 10 个 V2 生产文件。
- 关键内容：thread messages/history 的运行时与持久化聚合读取统一到一个 service 面，补齐 `messages + rollout`。
- 目标行数：≤ 1,000

### T8：Contract 测试

- 波次：4
- 依赖：T3 + T4 + T5
- 目标：补齐 driver contract suite、capability fallback 测试、MCP manifest 测试。
- 关键内容：锁定 Claude/Codex 在 start/resume/turn/interrupt/configure/tool-call responder/manifest 维度的统一 contract 行为。
- 目标行数：≤ 400

## 6. 修正后的 Agent 分配

| 波次 | Agent 数 | 任务 | 行数 |
|---|---:|---|---:|
| 1 | 2 | T1（接口+DTO） + T2（事件工厂） | ~700 |
| 2 | 2 并行 | T3（Claude） + T4（Codex） | ~3,100 |
| 3 | 2 并行 | T5（统一层） + T6（Turn） | ~2,800 |
| 4 | 2 并行 | T7（Thread） + T8（测试） | ~1,400 |
| 合计 | 8 | 8 个子任务 | ~8,000 |

排期说明：

- 波次 1 先解 contract 与事件工厂，确保后续 driver 与 unified 不返工。
- 波次 2 把 Claude/Codex driver 并行落地，但都只面向 T1 contract。
- 波次 3 再收口到 unified 与 turn，避免 turn 继续直接依赖 provider 实现。
- 波次 4 最后补 thread history 聚合与 contract tests。

## 7. 守卫预检清单

守卫清单共 13 条；1-10 为执行基线，11-13 为本次新增依赖边界守卫。

1. `StartSessionRequest`、`ResumeSessionRequest` 只承载会话启动/恢复元数据，不再包含 prompt 或 `DynamicTools`。
2. `TurnRequest` 是唯一统一 turn 提交 DTO；skills、overrides、MCP manifest 只能从这里进入 provider。
3. `Session.Interrupt` 只负责中断语义；`model`、`personality`、`approvals`、`compact` 等配置变更统一走 `Session.Configure(ctx, ThreadConfigPatch)`。
4. `ToolCallResponder` 是 tool-call 成功/失败回包的唯一 contract，禁止再经由字符串命令通道或 provider 私有辅助函数回包。
5. provider 事件出口必须是构造期 dispatcher 注入或 `Session.Events()`，不再保留公共 `SetEventHandler` 风格回调 setter。
6. `ListThreads`、`ForkThread`、realtime、session-control 一律 capability-gated；thread/turn 服务不得假设双 provider 必然支持。
7. `BuildManifest(ManifestContext)` 是唯一 MCP manifest 构造路径；driver 不得自行拼工具 schema 或绕过 family binaries。
8. driver registry 只能通过 `fx` value group 组装并在构造期冻结，不得引入包级可变 `RegisterDriver`。
9. provider 差异只能通过 `CapabilitySet` 和 typed capability error 暴露，`module/turn`、`module/thread`、`platform/rpc` 不允许按 provider 名称分支业务语义。
10. `module/thread` 的 history surface 必须统一吸收 `archive + command + history + listing + messages + rollout`，不允许保留第二条并行 messages/hydration 逻辑链。
11. `dto/provider` 不 import `provider/*` 或 `module/*`。
12. `provider/unified` 不 import `claudecli` 或 `codexapp`，具体 driver 一律通过 `fx` 注入。
13. `module/turn` 不 import `provider/*`，只依赖 `contract` + `dto/provider`。

## 8. Done 标准

1. `SubmitWithSkillsAndOverrides` 不再出现在 V3 公共接口、DTO 或模块命名中。
2. `DynamicTools` 不再穿过 runtime/service/provider 主链路；所有工具接入统一走 MCP family manifest。
3. Claude 与 Codex 共用同一套 turn prepare、skill resolve、prompt compose、manifest builder 入口。
4. V2 `SendCommand`、`Kill`、`Running`、`GetPort` 的语义在 V3 已有结构化等价物：`Session.Interrupt`、`Session.Configure`、`SessionControl.ForceStop`、`orchestration.AgentSnapshot`。
5. `TurnRequest`、`CapabilitySet`、`ThreadConfigPatch` 等 provider-neutral 类型已迁到 `dto/provider`，`module/turn` 与 `provider/unified` 不再互相依赖。
6. driver registry 通过 `fx` group 装配，`provider/unified` 不直接 import `claudecli` 或 `codexapp`。
7. `SendDynamicToolResult` / `RespondError` 在 V3 有 `ToolCallResponder` 等价接口。
8. `SetEventHandler` 在 V3 有 `*event.Dispatcher` 注入或 `Session.Events()` 等价出口。
