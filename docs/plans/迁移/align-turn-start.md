# V2↔V3 1:1 对齐：`turn/start` + `turn/steer`

## 对比范围

### V2
- `go-agent-v2/internal/apiserver/methods_turn.go:24-82`
- `go-agent-v2/legacy-agentsdk/agentcore/types.go:20-39`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go:39-124`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:218-270`
- `go-agent-v2/legacy-agentsdk/claude/client.go:283-309`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:42-169,243-291`
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_operations.go:72-160`
- `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:165-171`
- `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:69-92,152-179,382-418`
- `go-agent-v2/internal/apiserver/codexadapter/adapter_stall.go:24-50`

### V3
- `internal/module/turn/rpc_types.go:8-20`
- `internal/module/turn/rpc.go:32-58`
- `internal/module/turn/rpc_helpers.go:5-14`
- `internal/module/turn/contract.go:27-42`
- `internal/module/turn/service.go:26-115,257-269`
- `internal/module/turn/assembler.go:47-189`
- `internal/module/turn/skills.go:11-37`
- `internal/module/turn/manifest.go:13-31`
- `internal/module/turn/tracker.go:34-118,155-234`
- `internal/dto/provider/turn.go:9-31`
- `internal/dto/provider/manifest.go:22-45`
- `internal/provider/codexapp/session.go:110-131,334-357`
- `internal/provider/codexapp/input_map.go:10-69`
- `internal/provider/codexapp/skill_prompt.go:9-25`
- `internal/provider/claudecli/session.go:90-184`
- `internal/provider/claudecli/skill_prompt.go:9-46`

## 总结

- `turn/start`：⚠️ 部分对齐。V3 已有 `PrepareTurn -> StartTurn -> session.StartTurn` 主链路，但 direct RPC 输入面比 V2 窄，`skills` 也没有 1:1 暴露。
- `turn/steer`：❌ 未对齐。V2 是“对 active turn 追加输入并校验 `expectedTurnId`”，V3 现在是“拿 `prompt` 再开一个新 turn”。

## 逐项结论

| 项目 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| `turn/start` 输入参数（`prompt/images/files/skills`） | typed RPC 入口是 `input[] + selectedSkills + manualSkillSelection + cwd + outputSchema`；客户端再把 `prompt/images/files/skills` 组装进 `input[]` | direct RPC 只有 `prompt/images/files/model/effort`；`PrepareInput`/`TurnRequest` 虽支持 `Skills`，但 `buildPrepareInput(...)` 没有把它们暴露出来 | ⚠️ |
| `turn/steer` 输入参数（`prompt/images/files/skills`） | `input[] + expectedTurnId + selectedSkills + manualSkillSelection`，可携带图片/文件/skill 输入 | direct RPC 只有 `prompt`；没有 `expectedTurnId`，也没有 `images/files/skills` | ❌ |
| assembler 组装 | `ParseTurnInputs(...)` 把 `text/image/localImage/filecontent/mention/skill` 拆成 `prompt/images/files`，再把 selected/auto-matched skill prompt 注入 `submitPrompt` | `inputAssembler` 也会规整 `Prompt + Inputs + Images + Files`，并由 `skillResolver` 合并显式/自动匹配 skills；但 direct RPC 仅喂入 `Prompt/Images/Files`，没有 V2 的 `input[]/skill` 面 | ⚠️ |
| manifest 构建（`BinaryDir`） | V2 `turn/start` / `turn/steer` 路径里没有 manifest / `BinaryDir` 概念 | `PrepareTurn(...)` 总会构建 `MCP`；`NewService(...)` 先用 `resolveBinaryDir()` 取 `os.Executable()` 目录，`input.BinaryDir` 可 override，`BuildManifest(...)` 再 `filepath.Join(BinaryDir, name)` | ⚠️ |
| provider `session.StartTurn` | V2 `turn/start` 最终走 `SubmitWithSkills(...)`；`turn/steer` 走 `RunTurnSteer(...)`，本质也是往现有 provider 进程 submit | V3 `StartTurn(...)` 已稳定落到 `session.StartTurn(...)`，`codexapp` 与 `claudecli` 都有实现 | ✅ |
| tracker 初始化 | V2 在 submit 成功后，解析 active turn id，再 `beginTrackedTurn(threadID, resolvedTurnID)` | V3 在 `session.StartTurn(...)` 之前先 `tracker.Start(localID,"",threadID)`，成功后再 attach handle/providerID、置 `running` 并起 watcher | ⚠️ |
| `steer` 追加输入 | V2 `TurnSteerFromInputAligned(...)` 先校验 `expectedTurnId == activeTurnId`，再把 `submitPrompt/images/files/skills` 追加到当前 turn；缺失 `turnId` 时还会回填 active turn id | V3 `SteerTurn(...)` 直接 `PrepareTurn(...Prompt...)` 后 `StartTurn(...)`；没有 active-turn 校验，也不是追加输入 | ❌ |

## 细节

### 1. 输入参数面

V2 的 typed RPC 入口本身不是 `prompt/images/files/skills` 四个平铺字段，而是统一收 `input[]`。但它的 appserver client 会在下游把 `prompt/images/files/skills` 组装成 `input[]`:

- `buildTurnStartInputs(...)` 会把 `prompt` 变成 `text`，`images` 变成 `image/localImage`，`files` 变成 `mention`，`skills` 变成 `skill`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go:39-124`。
- `turnStartTyped(...)` / `turnSteerTyped(...)` 再把 `input[]` 和 `selectedSkills/manualSkillSelection` 交给 provider adapter，见 `go-agent-v2/internal/apiserver/methods_turn.go:48-82`。

V3 direct RPC 则明显收窄：

- `turn/start` 只暴露 `threadId/prompt/images/files/model/effort`，见 `internal/module/turn/rpc_types.go:8-15`。
- `turn/steer` 只暴露 `threadId/prompt`，见 `internal/module/turn/rpc_types.go:17-20`。
- 虽然 `PrepareInput` 与 `dto.TurnRequest` 仍支持 `Inputs/Skills/ManualSkillSelection/OutputSchema/BinaryDir`，见 `internal/module/turn/contract.go:27-42`、`internal/dto/provider/turn.go:9-18`，但 `buildPrepareInput(...)` 实际只填写了 `Prompt/Images/Files/Model/Effort/ThreadCaps`，见 `internal/module/turn/rpc_helpers.go:5-14`。

结论：

- `turn/start` 对 `prompt/images/files` 只是部分保留，对 `skills` 不是 1:1。
- `turn/steer` 则连 `expectedTurnId` 和附件输入一起丢了。

### 2. Assembler 组装

V2 的核心在 runtime prepare 层：

- `ParseTurnInputs(...)` 会把 `text/image/localImage/filecontent/mention/skill` 规整成 `Prompt/Images/Files/TimelineAttachments`，见 `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:277-291,303-319`。
- `prepareTurnSubmissionCommon(...)` 会把 `selectedSkills` 和 `input[]` 里的 `skill` 名称并起来，再注入 selected-skill prompt 与 auto-matched skill prompt，产出最终 `submitPrompt`，见 `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:146-169,243-257`。
- `manualSkillSelection` 在这段 prepare 代码里其实没有生效，代码直接 `_ = manualSkillSelection`，见 `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:154`。

V3 的 service prepare 层也有 assembler，但边界不同：

- `inputAssembler.collect(...)` 会把 `Prompt + Inputs + Images + Files` 统一成 `[]InputItem`，见 `internal/module/turn/assembler.go:88-100`。
- `Assemble(...)` 会做 normalize、去重、限长/限量，支持 `text/filecontent/image/local_image/mention`，见 `internal/module/turn/assembler.go:47-69,103-189`。
- skill 解析不再把 skill prompt 直接塞回 submitPrompt，而是由 `skillResolver.Resolve(...)` 生成 `[]dto.SkillRef`，后续交 provider 处理，见 `internal/module/turn/service.go:59-73`、`internal/module/turn/skills.go:11-37`。

结论：

- 从 service 内部能力看，V3 assembler 不弱，甚至比 direct RPC 面更宽。
- 但从 `turn/start` / `turn/steer` direct RPC 的 1:1 对齐看，V3 没把 V2 的 `input[]`/`skill` 入口完整接出来，所以只能算 ⚠️。

### 3. Manifest 与 `BinaryDir`

V2 对应路径里没有 manifest builder，也没有 `BinaryDir` 参数。

V3 则在 `PrepareTurn(...)` 固定构建 manifest：

- `NewService(...)` 初始化 `manifest: newManifestBuilder(resolveBinaryDir())`，见 `internal/module/turn/service.go:26-36`。
- `resolveBinaryDir()` 用 `os.Executable()` 的目录作为默认值，见 `internal/module/turn/service.go:39-45`。
- `manifest.Build(...)` 会优先用 `input.BinaryDir`，否则回退到 service 默认值，见 `internal/module/turn/manifest.go:17-31`。
- `dto.BuildManifest(...)` 最终把 `BinaryDir` 与 `go-agent-mcp-*` 拼成二进制命令，见 `internal/dto/provider/manifest.go:30-45`。

这里有两个判断：

- `BinaryDir` 默认值现在是对的，不再是根路径问题。
- 但 direct `turn/start` / `turn/steer` 并没有任何入口把 `BinaryDir` 传进 `PrepareInput`，所以只能走 service 默认值，不是完整的 1:1 可控面。

### 4. Provider `session.StartTurn`

V2 的 start / steer 最终都走 provider submit：

- `TurnStart(...)` 在 runtime 层 prepare 完后，调用 `StartTurnSubmissionAndTrack(...)`，再经 `SubmitWithSkills(...)` 进入 provider 进程，见 `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_operations.go:72-160`。
- codex appserver client 会把数据编码回 `turn/start`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:218-270`。
- claude client 则把附件提示和 skill hints 合并回 prompt，见 `go-agent-v2/legacy-agentsdk/claude/client.go:283-309`。

V3 已有清晰的 session 抽象并落地：

- `service.StartTurn(...)` 明确调用 `session.StartTurn(ctx, req)`，见 `internal/module/turn/service.go:76-106`。
- `codexapp.session.StartTurn(...)` 会把 `dto.TurnRequest` 映射回 appserver `turn/start`，包括 `SelectedSkills/ManualSkillSelection/Model/Effort/OutputSchema`，见 `internal/provider/codexapp/session.go:110-131,334-357`。
- `claudecli.session.StartTurn(...)` 会把 inputs/skills/outputSchema 压成文本和附件 hint 后发给 transport，见 `internal/provider/claudecli/session.go:90-184`、`internal/provider/claudecli/skill_prompt.go:9-46`。

结论：

- “是否已经落到 provider session.StartTurn” 这一点是 ✅。
- 但 provider 侧是否仍保持 V2 那种结构化输入 fidelity，要分 provider 看；`claudecli` 明显是提示词折叠，不是结构化直传。

### 5. Tracker 初始化

V2 tracker 的起点更靠后：

- `SubmitTurnStartWithSkills(...)` 在无 active turn 时走 `submitWithSkillsImmediate(..., trackTurn=true)`，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:69-92`。
- `dispatchImmediateSubmit(...)` 只有在 submit 成功后，才通过 client 解析 active turn id，并 `beginTrackedTurn(proc.ID, resolvedTurnID)`，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:152-165`。
- `beginTrackedTurn(...)` 真正进 tracker core，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_stall.go:44-50`。

V3 tracker 的起点更靠前：

- `StartTurn(...)` 在调用 `session.StartTurn(...)` 前先 `Cleanup()`，然后 `tracker.Start(localID, "", threadID)`，见 `internal/module/turn/service.go:84-92`。
- 返回 handle 后再 `AttachHandle(...)`、`BindProviderID(...)`、`Update(...,"running")`，并启动 `watchTurn(...)`，见 `internal/module/turn/service.go:97-106,173-203`。
- tracker 状态机本体在 `internal/module/turn/tracker.go:34-118,155-234`。

结论：

- V3 并非缺 tracker；相反，它比 V2 更早开始跟踪。
- 但“初始化时机/ID 语义”不是 1:1：V2 更偏 provider turn id，V3 先本地 local id，再补 provider id。

### 6. `turn/steer` 的真实语义

这是这次对齐里最大的断点。

V2:

- `turnSteerTyped(...)` 走的是 `TurnSteerFromInputAligned(...)`，见 `go-agent-v2/internal/apiserver/methods_turn.go:74-82`。
- `ResolveTurnSteerAlignment(...)` 会强制要求 `expectedTurnId` 非空，且必须等于当前 active tracked turn id，见 `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:70-88`。
- 通过校验后，`TurnSteerFromInput(...)` 会 prepare 出 `submitPrompt/images/files/skills`，再调用 `a.TurnSteer(...)`，见 `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go:90-106`、`go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_operations.go:110-125`。
- `RunTurnSteer(...)` 本质是对当前 provider 进程再做一次 submit，而不是新建 turn，见 `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:165-171`。

V3:

- RPC `turn/steer` 直接 `svc.SteerTurn(ctx, session, p.Prompt)`，见 `internal/module/turn/rpc.go:49-58`。
- `SteerTurn(...)` 自身只是 `PrepareTurn(...Prompt...)` 后 `StartTurn(...)`，见 `internal/module/turn/service.go:109-115`。
- `Service` 接口签名也只有 `prompt string`，没有 `expectedTurnId`、附件或 skills，见 `internal/module/turn/contract.go:12-18`。

结论：

- 这不是“轻微偏差”，而是语义切换。
- V2 `turn/steer` = 对当前 active turn 追加输入。
- V3 `turn/steer` = 用 prompt 再启动一个新 turn。
