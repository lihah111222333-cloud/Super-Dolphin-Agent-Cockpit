# P4: 依赖方向与隐藏契约收口

## 目标

把继续审查中确认的“不是纯 runtime ownership，而是模块边界 / import direction / consumer-local hidden contract”的遗留违规，单独从 `P22` 里收口成第二条支线，避免和 `P0-P3` 的 `fx / bus / run.Group` 主题混题。`P4` 不重写 `fx.Module / BusModule / RunnerModule` 的运行时分工，只处理这些分工之外的边界与隐藏契约；`toolbridge` runtime owner 继续归 `P2`，`orchestration` waiter / exit owner 继续归 `P3`。

## 覆盖问题

- `ui/wails -> module/uistate` 的活跃直连，以及 `NewActiveAgentCounter` 这类跨 `ui/wails ↔ orchestration/app` 的 hidden contract
- `provider/claudecli -> module/* / module/prompt` 的反向依赖
- `native-skill` 与 `signed-skill` / trust / review contract 不再混成一个 `claudecli` 子域标签：前者归 provider 行为契约，后者归 `module/skill` / verifier / approval trust lane
- `thread/turn` 的 consumer-local side-channel contract
- `platform/toolbridge -> provider/* / store/*` 的平台层依赖越界
- `internal/sidecar/orch/orchestration` 里的 `Module` / `handler.Map` / consumer-local 扩展接口
- `MCP-LSP/bootstrap` 的 compatibility / hidden contract

## 现状校准

### `ui/wails`

- [internal/ui/wails/rpc.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/ui/wails/rpc.go) 仍直接注入 `uistate.Service`
- [internal/ui/wails/scope_catalog.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/ui/wails/scope_catalog.go) 直接依赖 `uistate` 做 scope 解析
- `NewActiveAgentCounter` 当前已有 prod caller 链（`NewActiveAgentCounter -> NewWailsLifecycle -> ShouldQuit -> ListAgents/isActiveAgentState`）；它是 live hidden contract，不是可忽略的空壳 helper

这违反了 Wails 侧应尽量经 `rpc.Server.Dispatch` / contract 边界交互的约束。

### `provider/claudecli`

- [internal/provider/claudecli/event_map.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/provider/claudecli/event_map.go) 直接调用 `module/turn` 状态副作用
- [internal/provider/claudecli/session_turn.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/provider/claudecli/session_turn.go) 仍依赖 `module/skill` renderer
- [internal/provider/claudecli/history_trim.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/provider/claudecli/history_trim.go) 继续依赖 `module/skill` helper
- [internal/provider/claudecli/module.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/provider/claudecli/module.go) 为 `SkillInjectionPortGroupTag` 反向 import `module/prompt`
- `Mode=None` 的 native-skill name-list 可见性在设计文档与现有代码/测试之间仍冲突
- native-scan 根路径语义在文档写成 `gitRoot > cwd`，但当前 seam 只有 `cwd`
- `HEAD 2026-04-23` 的 `skill_inject.go:61-69` 仍把空 `cwd` 直接返回 `nil`；`skill_injection_test.go:71-79` 也仍按该旧语义写死，因此 `ErrMissingCWD` 目前还是目标 contract，不是现状事实

这说明 provider concrete package 仍未从 `module/*` 反向解耦。

### `thread / turn`

- `thread` 仍以 `turn.PendingLaunchSpawner` 形式把自己暴露给 `turn`
- 该接口定义在 `turn` 消费者包里，实际实现却依赖 `thread` 的 `SpawnIfNeeded(...)` 语义

这属于典型的 thread/turn 边界隐藏契约，不是单纯的 runtime ownership 问题。

### `platform/toolbridge`

- [internal/platform/toolbridge/handler.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/platform/toolbridge/handler.go) 直接依赖 `provider/codexapp` 与 `store/{binding,thread}`
- 同时还硬编码依赖 `thread.ConfigOverride.runtime` 的 JSON schema / key alias / behavior contract
- proxy / handler 还承担一套未文档化的 protocol compatibility 与 peer-discovery 语义
- `HEAD 2026-04-23` 的 `persistentSubagentRequired()` 在 runtime 解析失败时仍直接回退 `cfg.Agent.PersistentSubagentDefault`（`handler.go:136-153`）；`handler_runtime_test.go:111-143` 仍把 “missing thread row returns false” 当旧语义钉死，因此 H-3 fail-closed 还不是代码真值

这不是单纯 owner 问题，而是平台层本身已经吸入了 provider/store 语义。

### `internal/sidecar/orch/orchestration`

- [internal/sidecar/orch/orchestration/service.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/service.go) 仍导出 `orchestration.Module`
- [internal/sidecar/orch/orchestration/rpc.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go) 仍以 `handler.Map` 暴露协议壳
- [internal/sidecar/orch/orchestration/hook_consumer.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/hook_consumer.go) 仍直接导出 bootstrap/hook 协议入口
- [internal/sidecar/orch/orchestration/helpers.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/helpers.go) 仍通过 `sessionReadyWaiter` 这类消费者本地扩展接口读取附加能力
- [internal/sidecar/orch/orchestration/process_lifecycle.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/process_lifecycle.go) 还依赖 `generationAwareSessionCleaner` 这种未入 `internal/contract` 的本地私扩接口
- [internal/sidecar/orch/orchestration/launcher.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/launcher.go) 仍以 `*agentRuntime` 作为共享可变 carrier，并在子包里硬编码 outbound RPC 方法名 / 请求参数 / 响应 alias 兼容
- [internal/sidecar/orch/orchestration/factory.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/factory.go) 的 `lookupAgentByIDLocked(...)` 仍把本地 `agentID`、`remoteAgentID`、`remoteThreadID` 静默视为等价 lookup key，却不校验 `SessionID/launchSeq`
- [internal/sidecar/orch/orchestration/rpc.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/rpc.go) / [report.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/sidecar/orch/orchestration/report.go) 仍保留 `agent.reportEvent` / `agent.rememberReportRequest` 这组 stringly hidden protocol shell

这批问题属于 `cmd/mcp-*` 模块化契约与隐藏 contract 违规。

## 目标架构

### 1. `ui/wails`

- 只依赖 `rpc.Server.Dispatch`、公共 contract 或专用 facade
- 不再直接 import `module/uistate`
- `NewActiveAgentCounter` 这类通过 `ListAgents + isActiveAgentState` 负面枚举重算 active 语义的 hidden contract，要么升格为显式 facade/contract，要么退回根装配层，不再藏在 UI 子包里

### 2. `provider/*`

- concrete provider 只依赖 `internal/contract`、`dto/*`、provider 自己的 translator carrier
- 不再直接 import `module/turn`、`module/skill`、`module/prompt`
- provider-specific 行为契约，如 native-skill `Mode=None` 语义、native-scan 根路径优先级，也必须有单一 authoritative 文档与守卫测试
- native-scan 的 authoritative 根路径只接受显式 `cwd`；缺 `cwd` 时直接 `ErrMissingCWD`，`gitRoot > cwd` 只能作为显式 legacy opt-in，不能继续挂在默认解析链
- `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1` 继续只允许作为 legacy default-home 的显式 opt-in；P22 不把它写回默认解析链

### 2.5 `thread / turn`

- owner contract 放在拥有者模块或 `internal/contract`
- 不再由消费者包定义 `PendingLaunchSpawner`、`sessionReadyWaiter` 这类 side-channel interface

### 3. `platform/*`

- 平台层不直接 import provider concrete package 或业务 store
- 如确需适配，迁到 app/provider adapter 邻域，或抽象成 contract/facade

如果 `toolbridge` 短期无法迁包，至少要把下面这些 contract 正式冻结：

- `thread.ConfigOverride.runtime` 的最小 schema 与 alias 规则
- `persistent_subagent_default`、`enabledTools` 与 `spawn_agent` 阻断条件
- `req.threadId/thread.id -> bindingStore.GetThreadByAgent(agentId) -> runtime` 这条 thread 归属解析顺序；缺 thread/runtime/identity 时返回 `ErrThreadRuntimeRequired` / `ErrPersistentSubagentRuntimeRequired` 一类 sentinel，不再静默回退 `cfg.Agent.PersistentSubagentDefault`
- `PersistentSubagentDefault` 只有在 thread 已成功解析且 runtime 明确无本地配置位时才允许读取；thread 解析失败本身就是 fail-closed，不得借“无配置”名义偷回全局默认
- `_agentId`、`_threadId`、`_callId` 这组注入到 peer `tools/call` 的私有元数据 contract
- proxy 的 `/mcp/{family}/{agentID}` 路径与 family/tool 校验
- `HandleToolCall` 接受的 alias / nested request shape
- proxy 当前固定支持的方法集合、`initialize` 固定返回的 `protocolVersion/serverInfo`、以及错误码映射
- peer tool result 目前只保留 `content[].text` 的折叠/归一规则
- “tool call 歧义报错 / tool list 轮询后取首个 peer”的现行行为
- `ida` family 是否是真正受支持的 proxy family，还是应从 manifest/proxy 中删除

### 4. `cmd/mcp-*`

- 不再由子包导出 `Module`
- 不再通过 `rpc.go -> handler.Map` 作为标准协议壳出口
- 不再让消费者本地定义“附加接口”去偷读 owner 能力

对于 `orchestration`，本页的最小目标是：

- `Module` 退回 `cmd/mcp-orch` 根入口组装
- `NewOrchestrationHandlers` / `handler.Map` 退回根入口或被 facade 替代
- `HookConsumer` 退回根入口组装或被 facade 取代，不再作为子包直接导出的 protocol shell
- `generation-aware remove` 与 `WaitForSessionReady` 要么升格进 owner contract，要么删掉 fallback/side-channel
- `AgentLauncher` / `remoteLauncher` 的 request/response/method alias 合同要么升格为显式 facade/DTO，要么退出子包实现细节
- 入站 hook/event 的 identity contract 收敛为单一 authoritative key，并把 `SessionID/launchSeq` fence 纳入校验
- `agent.reportEvent` / `agent.rememberReportRequest` 这组 payload key / event type / requester-drain 规则要么升格为显式协议定义，要么退回根入口 facade

## 收口口径

- 本页同样继承 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4`；`P4` 不再另写一套 `fx.Module / BusModule / RunnerModule` 语义，只在此前三页已经冻结的 runtime contract 之外处理依赖方向与 hidden contract。
- `P4` 只签收 dependency direction / hidden contract，不替代 `P2/P3` 的 runtime owner 验收；`fx.Module / BusModule / RunnerModule` 的职责线继续以前三页为准。
- 首波守卫按包域收窄推进，不做“全仓 `provider/* -> module/*` / `platform/* -> provider/*` 一把梭”式大网；先锁本页点名子域。
- `P4` umbrella 同时承接 `P21` 递延的两条 contract lane：`provider/claudecli` 子域只直接承接 native-skill / native-scan / default-home 这条 provider lane；signed-skill / verifier / approval trust 继续在 `module/skill` 侧 lane 收口，不再合写成一个 claudecli 子域。
- `toolbridge` 在本页只处理 provider/store 依赖越界与协议 contract；proxy serve / stop / drain 仍由 `P2` 先收口。
- `orchestration` 在本页只处理 `Module` / `handler.Map` / side-channel interface / identity-report contract；waiter / exit owner 仍由 `P3` 先收口。
- `gopls/bootstrap` 在本页只处理 compatibility / hidden contract；constructor-owned loop、async callback owner 仍由 `P2` 先收口。
- `stop-intake/退订 ≠ drain`、双树同构与 runner-only sidecar 的 runtime 口径继续以 README/P0/P2/P3 为准；`P4` 只记账这些 contract 的消费者边界，不把它们改写成新的 hidden fallback。

## fallback / 缺失硬报错口径

- `toolbridge`：`thread/runtime/identity` 缺失时一律硬报错；`PersistentSubagentDefault` 只能作为显式 compatibility flag / env opt-in 的 fallback，且默认关闭
- `provider/claudecli`：native-scan 缺 `cwd` 直接 `ErrMissingCWD`；repo-root / `gitRoot > cwd` 只能作为 legacy opt-in，不得继续挂在默认链
- `bootstrap`：`PendingHooks()` / subscribe / report-event 缺 authoritative `agent_id` 时直接报错；`Config.AgentID` 与 `boot.AgentID` 不再允许 split-brain fallback
- `orchestration`：`WaitForSessionReady`、generation-aware remove、multi-id reverse lookup 若仍保留，必须升格为显式 contract；不接受“公共 contract + 私有 fallback”双轨
- 上面 4 条是 **目标 contract**，不是 HEAD 已销账事实：`toolbridge/handler.go:136-153` 仍在 thread/runtime 解析失败时回退 `cfg.Agent.PersistentSubagentDefault`，`handler_runtime_test.go:111-143` 仍钉住 “missing thread row returns false”；`provider/claudecli/skill_inject.go:61-69` 仍把空 `cwd` 视为 `nil`，`skill_injection_test.go:71-79` 也仍按该旧语义断言。因此代码真值未改前，本页只能把 H-3 记为目标态，不能据此宣称已闭环。

## 实施方式

- `P4` 按 phase-B 方式推进：先冻结 authoritative contract，再抽 facade / contract carrier，最后删旧 shell / fallback。
- `arch-import-direction.md` 继续只作为历史扫描与 debt banner 承载页；每次 `P4` 子域收口后，都要同步更新它的 debt banner/权威指向，避免历史扫描页与当前权威页重新漂移。
- 第一批优先做守卫清晰、共享写集较小的子域：`ui/wails`、`provider/claudecli(native-skill/provider lane)`；这是 `P4` 内部唯二可直接作为首波 implementation lane 拆开的子域。signed-skill / trust lane 继续留在 `P4` umbrella 内，但不与 claudecli 首波实现混写。
- `NewActiveAgentCounter` 不按“纯 UI 内部重命名”处理；它属于 `ui/wails ↔ orchestration/app` 的 hidden contract，同步改判时要连同 facade / debt banner / 口径说明一起收口。
- `thread/turn` 虽属 `P4` 的 hidden contract，但与 `P2` 的 `thread event / resume / task-handoff` 切片共享写集与 owner contract；文档澄清可先行，代码合入按 `P2(thread slice) -> P4(thread/turn side-channel)` 串行，且 thread+turn 视为同一 lane，不再拆成两个互不感知的 agent。
- `toolbridge`、`orchestration`、`gopls/bootstrap` 都只允许先做文档/contract 澄清；代码实现分别要等 `P2(toolbridge runtime)`、`P3(waiter/exit owner)`、`P2(gopls/bootstrap runtime)` 前置完成后串行落地。
- 守卫优先级以窄规则为主：`ui/wails -> module/*`、`provider/claudecli -> module/{turn,skill,prompt}`、`platform/toolbridge -> provider/* / store/*`、`internal/sidecar/orch/orchestration` 不再导出 `Module/handler.Map` 与本地 side-channel shell，以及 `ui/wails` 不再保留 `NewActiveAgentCounter` 这类 state-negative-enum hidden contract。
- 不保留“新 facade 已接上，但旧 import / old shell 仍能继续用”的软删除状态。
- `thread/turn`、`toolbridge`、`orchestration` 这三类共享写集子域，默认先做文档/contract 澄清，再等前置 runtime slice 合入后串行删除旧 shell；不把“文档已说明”误当成“代码已可并行改”。
- `native-skill` 与 `signed-skill` 分两条 contract lane 处理：前者跟随 `provider/claudecli` 的注入/渲染/Mode=None 语义，后者跟随 `module/skill` / verifier / approval trust 边界；不再用“claudecli 子域”一把兜住两者。
- `NewActiveAgentCounter` 的 live chain 按 `NewActiveAgentCounter -> NewWailsLifecycle -> ShouldQuit -> ListAgents/isActiveAgentState` 冻结；它不是单纯 UI helper rename，而是 `ui/wails ↔ orchestration/app` 的 behavior/protocol 守卫对象。

### 守卫分类 -> 子域 映射

| 守卫类 | 本页主承接子域 | 典型对象 |
|---|---|---|
| import-direction | `ui/wails`、`provider/claudecli`、`platform/toolbridge` | 包 import 越界 |
| symbol / export | `internal/sidecar/orch/orchestration`、`thread/turn` | `Module` / `handler.Map` / `HookConsumer` / side-channel interface |
| behavior / protocol | `ui/wails`、`provider/claudecli`、`toolbridge`、`orchestration`、`gopls/bootstrap` | `NewActiveAgentCounter`、native-skill、signed-skill trust lane、identity/report、protocol fallback |

## 依赖图（文本）

```text
P0 -> P4(ui/wails + claudecli(native-skill/provider lane))
P0 + P1c -> P2(thread / cachekeepalive / session users) -> P4(thread / turn side-channel contract)
P2(toolbridge runtime) -> P4(toolbridge dependency / protocol contract)
P3(waiter / exit owner) -> P4(orchestration shell / identity / report contract)
P2(gopls/bootstrap runtime) -> P4(gopls/bootstrap compatibility / hidden contract)
```

## 落地顺序建议

1. 先做 `ui/wails + claudecli(native-skill/provider lane)`：守卫边界最清晰，也能把 `P21` 递延的 provider-side contract 债正式接住；signed-skill / verifier / approval trust lane 继续在 `P4` umbrella 内按独立 lane 记账。
2. `thread / turn` 先做文档与 contract 澄清，再等 `P2` 的 `thread event / resume / task-handoff` 切片冻结 owner 之后串行合入，避免两边同时改同一组 wiring。
3. `toolbridge` 放在 `P2` runtime owner 收口之后，避免同一批同时改 serve owner 与 protocol shell。
4. `orchestration` 放在 `P3` waiter / exit owner 收口之后，再处理 `Module` / `handler.Map` / identity-report contract。
5. `gopls/bootstrap` 最后做，把 runtime owner 与 compatibility contract 分轨合并。

## 内部并行关系（叙事）

| 组合 | 口径 |
|---|---|
| `ui/wails` ↔ `claudecli` | 可并行做首波 implementation；两边共享的只是入口级 debt banner / 文档同步，不共享热写集 |
| `ui/wails` ↔ `thread/turn` | 不要求同批；若 `NewActiveAgentCounter` 语义要纳入 thread-owned active 判定，再单独追加 contract 同步 |
| `ui/wails` ↔ `orchestration` | `NewActiveAgentCounter` 属共享 hidden contract，但优先走 facade/projection 收口，不要求把 `orchestration` 整域提前实现 |
| `claudecli` ↔ `thread/turn` | 不并拆成 provider lane + thread lane 双改同批；先 provider 去反向依赖，再让 `thread/turn` 在 `P2(thread slice)` 后串行删 side-channel |
| `thread` ↔ `turn` | 固定同一 lane；不拆成两个 agent，也不写成两个独立 merge 单元 |
| `toolbridge` ↔ `thread/turn` | 只做 contract 澄清时可并行读写文档；代码实现要等 `P2(toolbridge runtime)` 与 `P2(thread slice)` 各自前置完成 |
| `toolbridge` ↔ `orchestration` | 共享的是 protocol / launcher 语义，不是首波 implementation 写集；两者都不在第一批并行实现里 |
| `orchestration` ↔ `gopls/bootstrap` | 都依赖前置 runtime slice，最多并行做 contract 澄清；真正代码落地仍分属 `P3` 后与 `P2(sidecar)` 后 |
| `gopls` ↔ `bootstrap` | 固定同一 sidecar lane；compatibility 与 hidden contract 可以分两拍，但不拆成两个互不感知的 implementation lanes |

> 本页的“可并行”统一解释为**可并发开工**；凡共享 `internal/app/modules.go`、`cmd/mcp-lsp/fx.go`、`internal/sidecar/orch/orchestration/*`、`internal/platform/toolbridge/*` 等 wiring/hot files 的组合，仍需单点 closer 串行合码。

> 简化成一句话：`P4` 只有 `ui/wails + claudecli(native-skill/provider lane)` 是首波可直接实施的双 lane；signed-skill / trust lane 仍属 `P4`，但不与 claudecli 首波实现混写。其余组合最多先做文档/contract 澄清，真正实现必须跟随 `P2/P3` 前置门串行落地。

## phase-B / 子域回滚卡（R2）

| subdomain | gate carrier | rollback trigger | state rewind | disable steps | red-green |
|---|---|---|---|---|---|
| `ui/wails` | facade gate / feature flag（default-off） | active-count 语义漂移、退出 overlay 行为回归 | 回退 facade/projection，但不恢复 `module/uistate` 直连 | 停用新 facade gate，清理旧 hidden contract bridge | import + behavior guard 同 PR red-green |
| `provider/claudecli` | explicit legacy opt-in | `Mode=None` / native-scan / default-home contract 漂移 | 回退到前一版 translator carrier，但保持 `ErrMissingCWD` / `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1` default-off | 关闭新 contract gate，停用新增 renderer bridge | import + protocol + 文档一致性守卫同 PR red-green |
| `thread/turn` | contract gate | `PendingLaunchSpawner` / `WaitForSessionReady` side-channel 回归 | 恢复前一版 owner contract 适配，但不恢复消费者本地私扩 fallback | 停用新 contract gate，删除半接线 interface | side-channel 守卫同 PR red-green |
| `toolbridge` | compatibility gate（default-off） | thread/runtime fail-closed 被破坏、peer selection / schema contract 漂移 | 回退 facade/schema carrier，但不恢复 silent fallback | 关闭 compatibility gate，清理 fallback path 与 wiring smoke | protocol + wiring smoke 同 PR red-green |
| `orchestration` | shell removal gate | `Module/handler.Map` / identity-report contract 回归 | 回退根入口 facade 接线，但保留 `SessionID/launchSeq` fence | 停用新 shell-removal gate，清理临时 facade | export + protocol guard 同 PR red-green |
| `gopls/bootstrap` | sidecar contract gate | compatibility response / hook identity / report queue 语义漂移 | 回退上一版 contract carrier，但保持 fail-closed 身份判定 | 关闭 sidecar contract gate，等待 queue / reconnect drain | sidecar protocol guard 同 PR red-green |

## 实施步骤

### Step 1：冻结 authoritative 规则

- 统一在 `P4` 里把四类违规的单一口径写死
- 与 `arch-import-direction.md`、相关 codemap、P18/P20 followups 对齐，消除“文档 A 禁止、文档 B 记录为正常路径”的冲突

### Step 2：抽 facade / contract carrier

- `ui/wails` 需要 façade 或 RPC-only 入口
- `claudecli` 需要把 skill / turn / prompt 依赖改成 contract/dto carrier
- `claudecli` 还需要把 `Mode=None`、native-scan 根路径、history-trim marker 这类实现细节提升为显式 contract 或修正文档
- `orchestration` 需要把 `WaitForSessionReady`、generation-aware remove 这类能力升格进 owner contract 或删除本地私扩 fallback
- `toolbridge` 需要把 runtime schema / request shape / peer selection 这些隐藏 contract 从实现细节提升为可测试、可守卫的显式协议定义
- `toolbridge` 还需要把私有 metadata 注入、runtime parse chain、JSON-RPC handshake、response shape 降级规则、family 支持矩阵提升为显式协议定义
- `orchestration` 需要把 hook consumer、launcher、remote RPC method alias 这组 hidden protocol contract 提升为显式 façade/DTO/contract
- `orchestration` 还需要把 multi-id reverse lookup、session fence、report-event protocol、live-pointer launcher mutation 一并提升为显式 contract 或删掉
- `thread/turn` 需要把 `PendingLaunchSpawner`、`WaitForSessionReady` 这类跨模块 side-channel 提升为正式 contract 或删掉

补充要求：

- 不能保留“公共 contract + 私有扩展接口 fallback”双轨
- 不能保留“`Module` 虽然不再被推荐使用，但仍可 import”这种软删除状态
- 不能保留 “hook consumer / launcher 虽然是子包协议壳，但默认仍从根入口直接拿来用” 的软删除状态
- 不能保留“发出去带 `SessionID/launchSeq`，收回来却不校验”的软 fence 状态
- 不能保留 launcher/network 调用直接读写 live `*agentRuntime` 的跨边界 mutation contract

### Step 3：删除隐藏 contract 与 protocol shell

- 删 `Module` 级整包装配出口
- 删 `handler.Map` 型旧协议壳
- 删消费者本地私扩接口和 fallback 路径

## 非目标

- 不把 runtime ownership 再混回 `P4`；proxy serve、waiter owner、constructor-owned loop 仍分别归 `P2/P3`。
- 不在第一批就打开全仓级 import 大网；`P4` 先做包域明确、可验证的窄守卫。
- 不与 `P2/P3` 在同一批同时改共享文件；共享写集按串行收口。

## TDD 与旧实现清理

- `P4` 守卫固定分三类写：**import-direction**、**symbol/export**、**behavior/protocol**；不要把 `ui/wails` / `claudecli` / `toolbridge` / `orchestration` / `gopls-bootstrap` 的所有问题都塞进同一份 `dependency_direction` 测试壳。
- 先补失败的依赖方向守卫：只对本页点名子域落包域窄守卫——`provider/claudecli -> module/{turn,skill,prompt}`、`ui/wails -> module/*`、`platform/toolbridge -> provider/*/store/*`、`internal/sidecar/orch/orchestration` 子包 `Module/handler.Map` 出口
- 先补失败的 claudecli-specific 行为守卫：`provider/claudecli -> module/{turn,skill,prompt}`、`Mode=None` name-list 语义、native-scan 根路径优先级
- 先补失败的 hidden-contract 守卫：`toolbridge` runtime schema / request shape / peer selection 行为，以及 `orchestration` 的 generation/session-ready side-channel contract
- 测试名固定到可派单级别：`TestClaudecliNativeScanRequiresCWD`、`TestClaudecliModeNoneContract`、`TestToolbridgePersistentSubagentRejectsMissingRuntime`、`TestBootstrapPendingHooksRequiresAgentID`、`TestThreadTurnPendingLaunchSpawnerContractGuard`、`TestOrchestrationNoModuleExport`、`TestToolbridgeCompatibilityFallbackRemoved`
- 验证命令固定写法：`go test ./internal/provider/claudecli/... -run 'Test(ClaudecliNativeScanRequiresCWD|ClaudecliModeNoneContract)' -count=1 -v`、`go test ./internal/platform/toolbridge/... -run 'Test(ToolbridgePersistentSubagentRejectsMissingRuntime|ToolbridgeCompatibilityFallbackRemoved)' -count=1 -v`、`go test ./internal/mcpserver/common/bootstrap/... -run 'TestBootstrapPendingHooksRequiresAgentID' -count=1 -v`、`go test ./internal/sidecar/orch/orchestration -run 'TestOrchestrationNoModuleExport' -count=1 -v`
- 对 `native-scan` 根路径判定、`PendingHooks()` 身份判定、`persistent_subagent_default` 阻断逻辑补运行时 PoC；不能只靠 mock 单测证明“缺参会硬报错”
- 补上 `hook_consumer` / `AgentLauncher` / `remoteLauncher` 这组 protocol-shell / hidden-RPC-contract 的失败测试与守卫
- 补上 `thread/turn` side-channel interface 的失败测试与守卫
- 补上 orchestration-specific hidden contract 守卫：single identity key + session fence、report-event protocol、live-pointer launcher mutation
- 修复完成后必须删掉旧 helper / tag / fallback，不接受“新 facade 接上，但旧 import 路径还能继续用”的双轨
- 若必须阶段性兼容，必须在文档和测试里写明删除时点；没有删除时点的 compatibility shim 视为垃圾代码
- 对 `orchestration` 尤其要补两类失败测试：`cmd/mcp-*` 子包不再导出 `Module/handler.Map`；消费者不再能通过本地私扩接口读取 owner 附加能力
- 对 `claudecli` 还要补一类文档一致性清理：`P20` followup 中过期锚点、过期 writer 路径、以及与 live 测试相反的 `Mode=None` 设计说明必须同步修正

## 验收标准

- `ui/wails` 不再直接 import `module/uistate`
- `provider/claudecli` 不再直接 import `module/turn`、`module/skill`、`module/prompt`
- `claudecli` 的 `Mode=None` 与 native-scan 根路径 contract 有单一 authoritative 口径，且代码/测试与文档一致
- `platform/toolbridge` 不再直接 import provider concrete package 与业务 store，或文档中有明确、受守卫保护的临时例外
- `toolbridge` 的 runtime schema / protocol compatibility / peer selection 规则要么被显式协议化并有测试守卫，要么随迁包/解耦一起删除
- `toolbridge` 的 thread runtime 解析链、私有 metadata 注入、handshake 固定值、response shape 降级、peer selection 规则与 family 支持矩阵都有单一 authoritative 文档和测试守卫
- 缺 `cwd` / `thread` / `runtime` / `agent_id` 时统一走 `ErrXxxRequired` / fail-closed；不再通过 silent fallback 放宽 trust-domain
- `internal/sidecar/orch/orchestration` 不再导出 `Module` / `handler.Map` 协议壳
- `HookConsumer` 不再作为子包直接导出的 bootstrap/hook 协议入口
- `generationAwareSessionCleaner`、`sessionReadyWaiter` 这类本地私扩接口被升格进 owner contract 或删掉
- `AgentLauncher` / `remoteLauncher` 的 hidden protocol contract 被 facade/DTO/contract 替代，不再靠 `*agentRuntime` 与子包内 RPC alias 实现传递
- `thread.PendingLaunchSpawner` 不再作为 thread→turn 的消费者侧隐藏契约存在
- `lookupAgentByIDLocked` 不再以多套别名 id 静默 reverse-lookup 当前 runtime，且入站 hook/event 会校验 `SessionID/launchSeq`
- `agent.reportEvent` / `agent.rememberReportRequest` 的协议壳和 payload 规则已显式化，不再散落在子包里
- `NewActiveAgentCounter` 仍按 live hidden contract 对待：要么升格为 facade / projection，要么退回根装配层；不能以“没有 prod caller”为由误判成死代码
- 至少补以下测试/守卫：
  - import direction archtest
  - codemap / 文档口径一致性核对
  - 删除 fallback 后的 wiring smoke test
  - `cmd/mcp-*` 子包出口守卫
  - hidden contract fallback 移除守卫
  - `toolbridge` 协议兼容面与 runtime schema 守卫
  - `hook_consumer` / `launcher` / `remoteLauncher` 协议壳与 hidden-RPC-contract 守卫
  - `thread/turn` side-channel interface 守卫
  - `claudecli` 行为契约与文档一致性守卫
  - orchestration identity fence / report-event protocol / live-pointer launcher 守卫
  - `toolbridge` 私有 metadata / handshake / response shape / peer selection / family matrix 守卫

## 与 P22 其他子计划的关系

- `P0-P3` 解决 runtime ownership
- `P4` 解决 dependency direction / hidden contract
- `toolbridge` 同时命中两类问题：proxy serve / setter wiring 归 `P2`；平台层依赖 provider/store 归 `P4`
- `orchestration` 同时命中两类问题：waiter/exit owner 归 `P3`；`Module` / `handler.Map` / 本地私扩接口 归 `P4`

## 追加范围：MCP-LSP / Bootstrap Hidden Contracts

### `cmd/mcp-lsp/gopls`

- gopls transport 固定 `initialize` capability/workspaceFolders
- 对 `workspace/configuration`、`client/registerCapability`、`client/unregisterCapability`、`window/workDoneProgress/create`、`workspace/*/refresh` 等 server request 返回默认响应
- 这些 compatibility fallback 需要显式 protocol contract 与守卫测试，不能继续散落在 transport/client 实现中

### `internal/mcpserver/common/bootstrap`

- callback 协议当前对未知 method 默认成功 ACK；本页统一改判为 **fail-closed**，任何 compatibility ACK 只能挂显式 gate 且默认关闭
- `Config.AgentID` 当前是 split-brain：注册路径发送空 `AgentID`，而 Context/PendingHooks 又把它当本地身份 hint 使用；本页口径改为“缺 authoritative `agent_id` 直接报错，不再 fallback 取另一路”
- hook subscribe desired-state、report queue、heartbeat、reconnect、final report 都属于 bootstrap owner 语义，需要显式化

### `bootstrap / gopls` 最低 observability contract

- `log`：`bootstrap.hook_replay.begin/end`、`bootstrap.report_queue.drain`、`gopls.compat_fallback.hit`
- `metric`：`heartbeat_failures_total`、`report_queue_dropped_total`、`reconnect_attempts_total`
- `trace`：`bootstrap.hook_replay`、`bootstrap.report_queue`、`gopls.transport.compat`
- sidecar compatibility / hidden-contract 回滚时，必须一并保留这些 log / metric / trace 名称，避免运维面板断裂

### 文档权威口径

- `arch-import-direction.md` 目前只可作为旧扫描结果参考，不能作为 `P4` 的完整 authoritative baseline
- `codemap` 中把 `ui/wails`、`toolbridge`、`orchestration` 等 live debt 写成稳定职责的段落，需要在后续文档同步中加 debt banner 或更新为 P22/P4 口径
