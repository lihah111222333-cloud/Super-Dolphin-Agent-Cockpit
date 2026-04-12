# 09 Provider 集成层代码地图

## 0. 扫描结论

- 扫描范围：`internal/provider/claudecli/`、`internal/provider/codexapp/`、`internal/provider/e2e/`、`internal/provider/shared/`、`internal/provider/toolfilter/`、`internal/provider/unified/`。
- 额外对照：`internal/contract/provider.go`、`internal/contract/runtime_reporter.go`、`internal/dto/provider/*`、`internal/dto/{agent,tool,turn,ui}/`。
- 本次核对后确认的关键结论：
  1. **Claude session 启动并不总是等待真实 `system:init` 才 ready**：`thread_identity.go` 允许在已有 public thread id 时提前 ready；之后 `system:init` 若解析出不同的真实 provider id，会回填并补发 `agent:launched`。
  2. **Claude/Codex 的事件表此前缺项**：实际 translator 还覆盖了 `agent:stopped` / `agent:failed`、`turn:started` / `turn:input_received`、Codex 的 `turn/aborted`、`session.configured`、`shutdown.complete` 等别名。
  3. **Codex 恢复链路是 reconnect + `thread/resume` + pending turn replay**，不是单纯重连 WebSocket。
  4. **Codex 的进程管理分两层**：共享 `ServerManager` 管 app-server / peer / orphan cleanup；单个 session 只拥有自己的 WS，必要时才自起本地 `codex app-server`。
  5. **`UITokensUpdated` 不是 `unified/event_map.go` 的 raw-type switch 产物**，而是 Claude/Codex translator 先调用 `ui_tokens.go` 从 payload / usage 中抽取。
- 2026-04-12 本地验证：
  - `go test ./internal/provider/codexapp ./internal/provider/unified ./internal/provider/shared ./internal/provider/toolfilter` ✅
  - `go test ./internal/provider/claudecli` ❌：当前 `claudecli` 包本身与测试都存在编译问题，至少包含 `takeActiveToolInterruptEventsLocked` / `trackToolEvent` 缺失、`session_events.go` 未使用 `sort`、以及 `pidregistry.RegistryFilesMatchingKind` 调用签名不匹配等错误。
  - `go test ./internal/provider/e2e` ❌：`internal/provider/e2e/codex_mcp_test.go` 通过 `linkname` 引用的 `internal/provider/codexapp.writeCodexMCPConfig` 当前源码中不存在，构建阶段直接失败。

---

## 1. 模块概述：Provider 层的统一抽象

Provider 层把不同运行时折叠成统一的 `Driver / Session / TurnHandle / RawProviderEvent` 模型：

```text
上层模块(thread / turn / orchestration)
  -> unified.Client
  -> unified.Registry.Resolve(provider)
  -> contract.Driver
  -> contract.Session
  -> provider transport
  -> dto.RawProviderEvent
  -> unified.EventDispatcher
  -> typed bus events / UI events
```

统一点分两层：

1. **控制面**：`contract.Driver` / `contract.Session` 统一启动、恢复、发 turn、中断、读历史、配置、关闭等能力。  
2. **事件面**：provider 先产出 `dto.RawProviderEvent`，再由 `unified.EventDispatcher` 做公共翻译 + provider 专属翻译。

注意两种 provider 都存在“**public thread identity**”与“**provider thread identity**”分离：

- **Claude**：`thread_id` 面向外部/UI，`session_id` / `ThreadID()` 保存 provider 侧真实 resume id。  
- **Codex**：`session.ThreadID()` 保存 provider thread id，但 `session.dispatch()` 会把下发 payload 的 `threadId` 重写成 public `agentID`，避免 UI 看到 provider 内部 thread uuid。

---

## 2. 统一入口：`internal/provider/unified/`

### 2.1 Registry + Client

- `registry.go`
  - 把 provider 名称标准化为小写。
  - 当前标准名称：`claude`、`codex`。
- `client.go`
  - `StartSession` / `ResumeSession` 只负责选 driver、执行、成功后注册到 `SessionManager`。
- `module.go`
  - Fx 提供 `Registry`、`Client`、`SessionManager`、`SessionResolver`、thread/turn 适配器，并在 `OnStop` 时 `CloseAll()`。
- `session_adapter.go`
  - 把 `SessionManager` 适配成 thread / turn / orchestration 侧需要的窄接口，删除 session 时最终仍走 `RemoveCurrent()` / `Remove()`。

### 2.2 SessionManager

`session.go` 是进程内“在线 session 真相源”：

- `Register(agentID, session)`：返回 generation；同 agent 再注册时会 `ForceStop()` 旧 session。  
- `Remove(agentID, generation)`：按 generation 删除，防止并发误删新 session；删除成功后会带关闭超时调用 `Close()`，失败再 `ForceStop()`。  
- `RemoveCurrent(agentID)`：无 generation 条件删除当前 session，并执行同样的 close / force-stop 收尾。  
- `CloseAll()`：应用退出时 drain 整个 map，逐个 `Close()`，失败再 `ForceStop()`。

### 2.3 SessionResolver

`session_resolver.go` 的真实流程比旧文档更细：

1. 先把传入 `threadID` 当成 `agentID`，直接查 `SessionManager`。  
2. 若 miss，查 `threadStore.GetByThreadID(threadID)`：
   - 拿到 `agentID` 后再次查内存；
   - 若仍 miss，再查 `bindingStore.GetByAgentID(agentID)`，然后自动 `driver.ResumeSession()`。  
3. 若还没找到，再把传入值当成 **provider thread id**，按 `registry.Names()` 遍历 provider，查 `bindingStore.GetByProviderThread(provider, threadID)`，再自动恢复。  
4. `autoResumeSession()` 会把 `binding.ProviderThreadID` 放到 `ResumeSessionRequest.ProviderThreadID`，把 `binding.AgentID` 继续作为 public `ThreadID` / `AgentID`。

### 2.4 EventDispatcher

`event_map.go` 的职责有三件：

1. 把 raw 事件发到 bus（`dto.BusRawProviderEvent`）；  
2. 先跑公共 translator（warning / error / plan / item）；  
3. 再跑 provider translator（Claude / Codex）。

### 2.5 UI token 提取

`ui_tokens.go` 不是 raw-type 路由器，而是 **从 payload 的 `usage` / token 字段里抽取数值**：

- `inputTokens` / `promptTokens`
- `outputTokens` / `completionTokens`
- `totalTokens`
- `contextWindowTokens`

Claude / Codex translator 都会先调用 `PublishUITokensUpdated()`。

---

## 3. Claude CLI Provider 详述

### 3.1 文件地图

| 文件 | 作用 |
|---|---|
| `config.go` | 启动配置解析、cwd/binary/thread fallback 工具函数 |
| `driver.go` | Claude driver 入口，负责 start / resume / runtime report |
| `event_map.go` | Claude raw event -> typed event translator |
| `factory.go` | stream block 解码、attachment hint 编解码、辅助工具 |
| `history.go` / `history_model.go` | 读取 `~/.claude` JSONL 历史 |
| `history_trim.go` | 清理 system noise、skill block、LSP hint |
| `module.go` | Fx 注册 driver factory + translator |
| `peer_discovery.go` | 探测共享 peer HTTP MCP 地址 |
| `session.go` | session 状态机、restart、stop、turn handle |
| `session_config.go` | `Configure` / `AllowedModels` / `ReadConfig` / `ForceComplete` |
| `session_events.go` | stdio read loop、raw event 应用、turn 终态收口 |
| `session_history.go` | provider history 适配层 |
| `session_interrupt_cleanup.go` | `Interrupt()` 后对旧 transport 的 SIGINT / SIGKILL 清理 |
| `session_turn.go` | turn payload / steer / skill prompt / attachment hint |
| `thread_identity.go` | public thread id / provider thread id / ready 协调 |
| `transport.go` | Claude CLI 子进程 transport |
| `transport_config.go` | CLI 参数构造、manifest 文件写出 |

### 3.2 Session 生命周期

#### StartSession / ResumeSession

入口都在 `driver.go`：

- `StartSession()`：
  - `dto.BuildManifest(...)` 会带上 `AgentID`、`CWD`、capabilities、`ResolveBinaryDir()`、`env`、`auto_approve`、`discoverPeerAddrs()`。
  - 历史目录支持 `history_dir` / `claude_home` 配置项。
- `ResumeSession()`：
  - 也会 `BuildManifest(...)`，但恢复时主要依赖 `ProviderThreadID` / `ThreadID` 构造 resume id。
  - 恢复路径当前只用 `AgentID` / `CWD` / capabilities / `ResolveBinaryDir(req.CWD, nil)` / peer HTTP 地址重建 manifest；不像 start 路径那样从 `req.Config` 带入 `env` / `auto_approve`。
  - `publicThread` 用的是 `req.ThreadID`，即外部 thread / agent 标识。

#### `start()` 的真实流程

1. `launchCLI(...)` 拉起 Claude CLI。  
2. 用 placeholder 构造 session：
   - `threadID = fallbackThreadID(agentID, spec.threadID)`
   - `publicThreadID = spec.publicThread || agentID || initialThreadID`
   - `sessionID = initialThreadID`
3. `thread_identity.go` 判断是否可以 **提前 ready**：
   - `spec.threadID` 不是 placeholder，或者
   - `publicThreadID` 不是 placeholder，
   就会立即 `markThreadReady()`。  
4. 启动 `startReadLoop()`。  
5. `awaitResolvedThreadID(ctx)`：如果前一步已 ready，会立刻返回；否则才等待 `system:init`。  
6. 注册 Claude CLI PID 到 `pidregistry`。  
7. 主动补发：
   - `agent:launched`
   - `agent:state_changed(new_state=idle)`
8. 后续如果 `system:init` 带来了新的真实 id，`applyRaw()` 会再次补发一次 `agent:launched`，用于纠正 binding / UI 上的 provider thread id。

#### 线程标识的真实语义

源码当前不是严格区分三个独立字段，而是：

- `EventThreadID()` / raw payload `thread_id`：对外 public thread id。  
- `ThreadID()`：session 内部保存的 resolved provider id。  
- `sessionID`：当前实现里会被 `setResolvedThreadIDForTransport()` 同步成同一个 resolved id。  

换句话说，**Claude 当前实现把 provider 侧 resume id 基本折叠为一个 resolved session/thread id**；对外仍通过 public thread id 发事件。

### 3.3 Turn 生命周期

Claude session 只有一个 `activeTurn`。

#### StartTurn

`session.go` + `session_turn.go`：

```text
StartTurn
  -> prepareTurn
     -> buildTurnText
     -> restartIfNeededLocked(必要时)
     -> ensure no active turn
     -> newTurnHandle(localID, providerID=localID)
     -> marshalTurnPayload
  -> transport.Send(payload)
  -> dispatch turn:started
  -> dispatch turn:input_received
  -> readLoop 持续产出 assistant/tool/result 事件
  -> turn:complete / turn:interrupted
  -> finish handle
```

要点：

- 输入最终被包装为 Claude CLI 的 `user.message.content=[{type:text,text:...}]`。  
- 附件不会直传给 Claude；会注入 `The user has attached...` 文本提示，引导模型用 `Read` 工具读取。  
- skills 会以文本块插入 turn 文本中。  
- `OutputSchema` 会作为 `output_schema:` 文本段追加。

#### Steer / Interrupt / ForceComplete

- `Steer()`：不是新 turn，而是向当前 active turn 继续发送 payload；会额外发 `turn:input_received`。  
- `Interrupt()`：本地收走 `activeTurn` 并记录 suppressed turn id，补发 `turn:interrupted`，handle 以 `context.Canceled` 结束；随后把当前 transport 从 session 上摘掉，`cleanupInterruptedTransport()` 对旧进程先发 `SIGINT`、最多等 2 秒，不退出则 `SIGKILL`，所以下一个 turn 会通过 `restartIfNeededLocked()` 重新拉 CLI。  
- `ForceComplete()`：同样先发 `SIGINT`，但会：
  - 记录 suppressed turn id；
  - 本地伪造 `turn:complete{success=true,status=completed,reason=force_complete}`；
  - finish handle；
  - 后续真实到来的 terminal 事件会被 suppress 掉。

### 3.4 Restart / 恢复机制

Claude 没有网络层“重连”，恢复本质是 **重启 CLI + `--resume`**。

`restartIfNeededLocked()` 的触发条件有两个：

1. `transport.readyForSend()` 为 false；  
2. 本 turn 改变了运行设置：
   - `Overrides.Model`
   - `Overrides.Effort`
   - `req.MCP`（manifest 非空且发生变化）

恢复流程：

1. 保留旧 `sessionID/threadID` 作为 `restartResumeIDLocked()` 的 resume id；  
2. 拉起新 transport；  
3. `resetThreadReadyLocked()`；  
4. 清空 `activeTurn` 与 `suppressedTurns`；  
5. 启动新的 read loop；  
6. 旧 transport 异步 `releaseTransport()`；  
7. 等待新 transport ready。  

注意：旧 transport 的事件会被 `isCurrentTransport()` 判定后直接丢弃，不会串到新会话上。

### 3.5 历史 / 配置 / 能力

#### 历史

- 根目录：
  - `history_dir` / `claude_home` 配置；
  - 否则 `CLAUDE_HOME`；
  - 再否则 `~/.claude`。  
- 文件定位：`projects/*/<threadID>.jsonl`。  
- `ReadHistory()` 的 fallback：如果传入 thread id 没查到，会再试一次 session 当前 resolved `ThreadID()`。  
- `history_trim.go` 会剥离：
  - `# AGENTS.md`
  - `<environment_context>` / `<instructions>` / `<permissions instructions>`
  - 注入的 skill block
  - 注入的 LSP hint
- 附件 hint 可从用户消息文本中恢复成 metadata。

#### 配置 / 能力

- `Configure()`：运行中不支持，直接返回 capability error。  
- `AllowedModels()`：内置 `sonnet` / `haiku` / `opus` / `opus[1m]`，若当前 model 不在内置表中，会把当前值附加进去。  
- `ReadConfig()`：可返回当前 `model` / `effort` / `approvals`。  
- 能力声明只包含：
  - `message_send`
  - `model_switch`
  - `turn_override`

### 3.6 Transport / CLI / 进程管理

#### 启动参数

`transport_config.go` 的实际行为：

- 固定参数：
  - binary 默认为 `claude`（可由 `CLAUDE_CLI_BIN` 覆盖）
  - `-p`
  - `--input-format stream-json`
  - `--output-format stream-json`
  - `--verbose`
- **当前不会传 `--model`**；model 只存在于 session 状态里用于展示/记录。  
- `--system-prompt`：仅在拼接结果非空时传；内容来自：
  - base instructions
  - developer instructions
  - `approval_policy` / `sandbox` / `summary` / `effort` / `personality` 元数据（`key=value`，按行拼接）
- `--permission-mode`：根据 `sandbox` / `approvalPolicy` 归一化：
  - `sandbox=danger-full-access` / JSON `{"type":"danger-full-access"}` -> `bypassPermissions`
  - `sandbox=workspace-write` -> `acceptEdits`
  - `sandbox=read-only` -> `default`
  - approval policy 空、`never`、`on-request`、`always`、`auto` -> `bypassPermissions`
  - `on-failure`、`untrusted` -> `default`
- `--effort`：仅在 `normalizeEffort()` 非空时传；`minimal` / `low` -> `low`，`medium` -> `medium`，`high` / `xhigh` -> `high`，其他非空值原样传。
- `--resume <resumeID>`：`launchCLIWithManifest()` 在 `buildCLIArgs()` 之后追加；resume id 为空时不传。

#### Manifest 写出

- `writeManifestConfig()` 会把 managed server 写成临时 JSON。  
- 仅接受：
  - `type=http,url=...` 的共享 peer 端点；
  - basename 以 `mcp-` 开头的 managed stdio binary。  
- 只要 `writeManifestConfig()` 返回非空配置路径：
  - 加 `--mcp-config <tempfile>`
  - 加 `--disallowedTools Read,Write,Edit,MultiEdit,Bash,Grep,Glob,LS`
  - 若参数列表里尚无 `--permission-mode`，再补 `--permission-mode bypassPermissions`

#### 子进程管理

`transport.go`：

- `Setpgid=true`，后续 signal 会打到整个 process group。  
- `stderr` 只保留最近 8 KiB。  
- `stdout` scanner 单行上限 20 MiB。  
- `Close()`：`SIGTERM` -> 等 3 秒 -> 仍存活则 `SIGKILL`。  
- `Kill()`：直接 `SIGKILL`。  
- `driver.start()` 成功后会把 PID 注册到 `pidregistry`；`stop()` 时注销。

---

## 4. Codex App Provider 详述

### 4.1 文件地图

| 文件 | 作用 |
|---|---|
| `driver.go` | driver 工厂、start / resume 入口 |
| `event_map.go` | Codex raw event -> typed event translator |
| `factory.go` | JSON-RPC / payload helper / shutdown helper |
| `history_rollout.go` | 读取本地 rollout 历史并去噪 |
| `module.go` | Fx module、`ServerManager`、共享 app-server 生命周期 |
| `orphan_sweeper.go` | 清理孤儿 `codex app-server --listen ...` 及其残留后代 |
| `peer_discovery_cleanup.go` | 清理 peer HTTP discovery 文件 |
| `peer_spawn.go` | 启动/守护 `mcp-orch`、`mcp-lsp` peer 进程 |
| `recovery.go` | health check、自动恢复、pending turn replay |
| `session.go` | session 主状态机、dispatch、turn 表 |
| `session_approval.go` | approval / request_user_input 桥接 |
| `session_history.go` | history 读取、compact thread |
| `session_turn.go` | turn input 映射 |
| `support.go` | runtime config、dynamic tools 注入、thread/start helper |
| `transport.go` | WebSocket transport 主循环 |
| `transport_helpers.go` | initialize、JSON-RPC 收发、pending call 管理 |
| `transport_process.go` | 本地 `codex app-server` 子进程管理 |

### 4.2 运行模型：共享 app-server + 独立 session WS

Codex 的隔离模型是：

- **共享层**：`ServerManager` 最多持有一个共享 `codex app-server` 本地进程。  
- **会话层**：每个 session 都有自己的独立 WebSocket 连接。  
- **兜底层**：如果没有共享 URL，也没有运行中的 manager，则该 session 的 transport 会自己 `spawnLocal()` 一个本地 app-server。

因此：

| 维度 | Codex 实现 |
|---|---|
| provider session | 独立 WS 连接 |
| provider process | 可共享，也可单 session 自起 |
| tool call 执行 | 若 `ServerManager.toolHandler` 已配置，则通过它回桥到 toolbridge |
| public thread id | 事件下发时重写为 `agentID` |
| provider thread id | `session.ThreadID()` / binding store 持久化 |

事件进入 `onNotification()` 后还有一道 thread 归属过滤：payload 如果带 `threadId` 且它与当前 `session.ThreadID()` 不一致，会被视为 alien thread event 直接丢弃；通过过滤后，`session.dispatch()` 再把 provider 内部 `threadId` 改成 public `agentID`。

### 4.3 Session 启动与恢复

#### `newSession()`

无论 start 还是 resume，都先走 `newSession()`：

1. 选 server URL：先用传入的显式 URL 初始化；若 `ServerManager` 正在运行，则无条件改用共享 `ServerURL()`。  
2. `newTransport(ctx, url)`：
   - 有 URL：直接连；
   - 无 URL：`spawnLocal()` 然后连接。  
3. `establish()`：`connect()` + `initialize(experimentalApi=true)`。  
4. 创建 session。  
5. 立即 `startReadLoop()` + `startHealthLoop()`。

#### StartSession

`driver.StartSession()` 的真实流程：

1. `newSession()` 建立 transport / read loop / health loop。  
2. `setRuntimeConfig(req.Config)`。  
3. `setApprovalPolicy(resolveApprovalPolicy(req.Config))`；默认值是 `never`。  
4. `startDynamicSession()`：
   - 要求 `DriverFactory.listTools` 已配置；
   - 拉取 dynamic tool schema；
   - `thread/start` 时把 schema 放进 `DynamicTools`；
   - 同时把工具目录文本注入 `DeveloperInstructions`，因为这些工具不会以 MCP tool 形式直接暴露给模型。  
5. `finishStartedSession()`：先把 resolved `threadID` 写回 session，再把 `model` / `cwd` / `port` 放进 runtime config snapshot，最后 `reportRuntime()`。

这里没有额外本地伪造 `agent:launched`；通常依赖 provider 自身通知 `thread/started` / `session.configured`。

#### ResumeSession

`driver.ResumeSession()`：

1. `newSession()`。  
2. 调 `thread/resume`。  
3. `setThreadID(resolved thread id)`。  
4. `restoreApprovalPolicy()`：尝试 `thread/config/get` 读取 `effective.approvals`；拿不到则退回本地状态。  
5. `reportRuntime()`。

### 4.4 Turn / 配置 / approval / toolbridge

#### Turn 相关 RPC

| Session 方法 | 底层 RPC / 行为 |
|---|---|
| `StartTurn()` | `turn/start`，返回 provider turn id，登记 `turns` / `activeTurnID` / `pendingTurn` |
| `Steer()` | `turn/steer`，强制要求 `ExpectedTurnID` |
| `Interrupt()` | `turn/interrupt` |
| `ForceComplete()` | `turn/forceComplete` + 本地补发 `turn/completed{reason=force_complete}` |
| `ListThreads()` | `thread/list` |
| `ForkThread()` | `thread/fork` |
| `Configure()` | `thread/config/set` + `thread/personality/set` + `thread/approvals/set` |
| `CompactThread()` | `thread/compact/start` |
| `AllowedModels()` | `model/list` |
| `RolloutPath()` | 定位本地 rollout 文件 |

Codex `CapabilitySet` 的实际声明只包含：

- `message_send`
- `thread_list`
- `thread_fork`
- `context_compact`
- `turn_override`
- `model_switch`

当前没有声明 `realtime`，也没有单独的 `thread_configure` capability 常量。

#### 输入映射

`session_turn.go` 会把输入转换成 Codex 输入项：

- `text` -> `text`  
- `image` -> `image` / `localImage`  
- `local_image` -> `localImage`  
- `file` / `mention` -> `mention`  
- skills：
  - `selectedSkills` 单独传；
  - skill prompt 本身会被拼成一条额外 text input。

#### Toolbridge 回桥

`session.onInboundMessage()` 有一个关键分支：

- 如果收到 **带 JSON-RPC `id` 的 tool call request**，且 `ServerManager` 配置了 `toolHandler`，并且方法名属于：
  - `item/tool/call`
  - `dynamic_tool_call`
  - `tool.call.begin`
- 就会异步调用 `toolHandler(ctx, msg)`，然后 `RespondWithID()` 把结果写回 provider。

这样做的目的是：**不阻塞 read loop**。

#### Approval 桥接

`session_approval.go` 处理 approval；method 集合在 `factory.go` 的 `approvalBridgeMethods` / `requestUserInputMethods` 中定义，实际包括：

- `rpc.DefaultApprovalCallbackMethod`（当前值：`approval/request`）
- `tool/approval/request`
- `item/commandExecution/requestApproval`
- `item/fileChange/requestApproval`
- `skill/requestApproval`
- `tool.approval.requested`
- `request_user_input`
- `codex/event/request_user_input`
- `item/commandExecution/requestUserInput`
- `item/commandExecution/request_user_input`
- `item/tool/requestUserInput`
- `item/tool/request_user_input`
- `mcpServer/elicitation/request`

流程：

```text
provider notification
  -> onNotification()
  -> approval method ?
     -> buildApprovalRequest()
     -> processedApprovals 去重(callID + requestID)
     -> RequestApproval / RequestUserInput
     -> approval/respond
```

要点：

- `processedApprovals` 上限 1000；重复请求会等待首个处理结果。  
- `request_user_input` 走 `ApprovalManager.RequestUserInput()`。  
- 默认 approval policy 为 `never`，避免前端审批流未接通时工具调用整体卡死。  
- **重要细节**：当 `ApprovalManager` 存在时，approval bridge raw event 默认不会继续 `dispatch(raw)`；也就是说 `event_map.go` 虽然支持把它们翻成 `ToolApprovalRequested`，但正常在线路径常常是“直接桥接处理，不走 bus 展示”。

### 4.5 恢复机制

`recovery.go` 是 Codex provider 的核心韧性模块。

#### 触发入口

1. `callTransport()`：底层 `transport.Call()` 报错，且 `shouldReconnect(err)` 为 true。  
2. `connection.dead`：read loop 结束时 transport 主动注入的 synthetic raw event。  
3. `health loop`：15 秒 tick；若 30 秒内没有读活动，就 `app/list` 探活。

`shouldReconnect(err)` 的排除项：

- `context.Canceled` / `context.DeadlineExceeded`  
- `transport unavailable`  
- `rpc error ...`（说明服务端活着，只是协议返回错误）

`transport closed` 当前不在排除项里，会走 reconnect；health check 遇到 `rpc error` / `invalid request` / `method not found` 也只刷新读活动时间，不触发恢复。

#### 恢复流程

```text
attemptRecovery(reason)
  -> recoveryCount++，超过 3 次直接 failTurns
  -> dispatch recovery.attempt
  -> transport.reconnect()
       -> 清 closed 标记
       -> close old socket
       -> 若是 local transport 且本地进程已死，则重新 spawnLocal
       -> connect + initialize(experimentalApi)
  -> waitReadLoopStopped()
  -> startReadLoop()
  -> 清空 suppressed map
  -> thread/resume
  -> replayPendingTurn()
       -> 重新 turn/start
       -> 更新 handle.providerID / turns map / activeTurnID
  -> recoveryCount 清零
  -> noteReadActivity()
```

#### 需要特别记住的语义

- `pendingTurn` 会保留最近未完成 turn 的启动参数；恢复后重新 `turn/start`。  
- `TurnHandle` 不会换对象，但它的 `providerID` 会被更新成 replay 后的新 turn id。  
- 如果恢复失败，`failRecovery()` 会 `failTurns()`，活跃 turn 全部以错误结束。  
- `connection.dead` 在恢复前就会先发到 translator，所以 UI / bus 可能先看到 `AgentFailed`，随后再看到 `AgentRecovering`。

### 4.6 Transport / 进程管理

#### WebSocket JSON-RPC transport

`transport.go` + `transport_helpers.go`：

- `connect()`：指数退避重试连接。  
- `initialize()`：发送 `initialize`，声明 `experimentalApi=true`。  
- `Call()`：pending map + request id + 阻塞等待 result。  
- `Notify()`：单向通知。  
- `ReadLoop()`：分发 response / notification；socket 断开时会发 synthetic `connection.dead`。  
- `RespondWithID()`：把 toolbridge 结果写回 provider。

#### 本地 app-server 进程

`transport_process.go`：

- 命令：`codex app-server --listen ws://127.0.0.1:<port>`  
- `Stdout = io.Discard`  
- `Setpgid=true`  
- `stderr` 仅保留最近 8 KiB  
- `watchLocalProcess()` 监控异常退出：
  - 记录退出错误；
  - 关闭 socket；
  - fail 掉 pending RPC。

#### 关闭语义

`shutdownTransport(graceful)` 很关键：

- 只有 **local transport** 才会在 graceful close 时先发 JSON-RPC `shutdown`。  
- 连接共享 app-server 的非 local transport **不会** 杀共享进程，只会关自己的 socket。

### 4.7 ServerManager / peer / orphan cleanup

`module.go` + `peer_spawn.go` + `orphan_sweeper.go` + `peer_discovery_cleanup.go`：

#### `ServerManager.start()`

1. `pidregistry.CleanupStale()`：按 PID registry 清理旧实例遗留进程。  
2. `cleanOrphanedMCPProcesses(nil)`：兜底清理旧版遗留 `mcp-orch` / `mcp-lsp`。  
3. `cleanOrphanedAppServers()`：清理孤儿 `codex app-server --listen ...` 及其残留后代。  
4. `transport.spawnLocal()`：起共享 app-server。  
5. 注册 app-server PID 到 `pidregistry`。  
6. 用 manager 自己持有的 `transport` 建立 WS 并 `establish()`（`connect + initialize`）验证 app-server 可用；这个 transport 之后仍归 `ServerManager` 生命周期管理，并不是各 session 复用的 WS。  
7. 暴露 `ServerURL()` 给各 session，让每个 session 再各自新建独立 WS。

#### `spawnToolbridgePeers()`

- 额外拉起：`mcp-orch`、`mcp-lsp`  
- 二进制路径只从当前 `os.Executable()` 所在目录拼出，不走 PATH 搜索。  
- 环境变量：`GO_AGENT_PEER_MODE=1`  
- 使用 `os.Pipe()` 保持 stdin 打开，避免 peer 因 EOF 退出  
- `watchAndRestartPeer()` 负责异常退出自动拉起新 peer  
- peer PID 也会注册进 `pidregistry`

#### `ServerManager.stop()`

- 标记 `ready=false`  
- 关闭 peer stdin pipe 并发 `SIGTERM`  
- 清 discovery 文件  
- `pidregistry.Close()`  
- 优雅关闭共享 app-server  
- 等 `mcpOrphanCleanupGracePeriod`（500ms）让父进程向 sidecar 传播信号  
- 再执行 residual orphan cleanup

#### 孤儿清理细节

- `cleanOrphanedMCPProcesses()`：扫描 `ps -eo pid,ppid,comm`，找出不在当前进程树中的 `mcp-orch` / `mcp-lsp`。  
- `cleanOrphanedAppServers()`：扫描 `ps -eo pid,ppid,args`，找出不在当前进程树中的 `codex app-server --listen ...`。  
- `killMCPProcess(pid)`：优先杀 process group，再回退单进程 kill。  
- app-server 清理会继续杀残留 descendant（例如 sidecar / tool 进程）。

### 4.8 历史读取

- 历史文件来自：`~/.codex/sessions/*/*/*/rollout-*-<threadID>.jsonl`。  
- 只解析：
  - `type == response_item`
  - `payload.type == message`
- 对用户消息：
  - 去掉 system noise / 注入 skill block / LSP hint；
  - 抽取 `input_image` 等 `input_*` 条目恢复 metadata。  
- 若本地 rollout 不可用：
  - 记录 warning；
  - 远端 history API 当前未接，返回空历史而不是报错。

---

## 5. 事件映射总览（与 `event_map.go` 对齐）

### 5.1 Claude translator：`internal/provider/claudecli/event_map.go`

| Raw event | Typed event | 备注 |
|---|---|---|
| `agent:launched`、`system:init` | `agentdto.AgentLaunched` | `system:init` 也被视为 launched |
| `agent:state_changed` | `agentdto.StateChanged` | |
| `agent:stopped` | `agentdto.AgentStopped` | |
| `agent:failed` | `agentdto.AgentFailed` | |
| `turn:started` | `turndto.TurnStarted` | |
| `turn:input_received` | `turndto.TurnInputReceived` | |
| `assistant:message_delta` | `turndto.TurnOutputDelta` | `stream=message/reasoning` |
| `turn:interrupted` | `turndto.TurnInterrupted` | |
| `turn:complete` | `turndto.TurnCompleted` | 会带 `success/error/status/reason/result/summary/message/stop_reason` |
| `tool:use_begin` | `tooldto.ToolCallBegin` | |
| `tool:use_end` | `tooldto.ToolCallEnd` | |

Claude raw event 的生成源：

- `decodeClaudeLine()`：
  - `system:<subtype>`
  - `assistant:message_delta`
  - `tool:use_begin`
  - `tool:use_end`
  - `turn:complete`
- session 本地主动补发：
  - `agent:launched`
  - `agent:state_changed`
  - `agent:stopped`
  - `agent:failed`
  - `turn:started`
  - `turn:input_received`
  - `turn:interrupted`
  - synthetic `turn:complete(force_complete)`
  - `turn:complete(error)`（如发送失败、read loop 以 EOF/错误收口时）

### 5.2 Codex translator：`internal/provider/codexapp/event_map.go`

| Raw event / alias | Typed event | 备注 |
|---|---|---|
| `thread/started`、`session.configured` | `agentdto.AgentLaunched` | |
| `thread/status/changed` | `agentdto.StateChanged` | `active -> turn_running`，`idle -> idle` |
| `shutdown.complete`、`shutdown_complete` | `agentdto.AgentStopped` | |
| `recovery.attempt` | `agentdto.AgentRecovering` | |
| `connection.dead` | `agentdto.AgentFailed` | `Recoverable` 取 `recoverable/willRetry` |
| `turn/completed`、`turn.completed`、`turn/aborted`、`turn.aborted` | `turndto.TurnCompleted` | **注意不是 `TurnInterrupted`** |
| `turn/started`、`turn.started` | `turndto.TurnStarted` | |
| `turn/interrupted`、`turn.interrupted` | `turndto.TurnInterrupted` | |
| `item/agentMessage/delta`、`message.delta`、`agent_message_delta` | `turndto.TurnOutputDelta` | `stream=message` |
| `item/reasoning/summaryTextDelta`、`item/reasoning/textDelta`、`reasoning.delta` | `turndto.TurnOutputDelta` | `stream=reasoning` |
| `item/commandExecution/outputDelta`、`exec_output_delta` | `turndto.TurnOutputDelta` | `stream=stdout` |
| approval bridge methods | `tooldto.ToolApprovalRequested` | translator 支持，但运行时常被 approval bridge 直接消费 |
| `item/tool/call`、`dynamic_tool_call`、`tool.call.begin` | `tooldto.ToolCallBegin` | |
| `item/completed`、`tool.call.end` | `tooldto.ToolCallEnd` | 仅当 payload 看起来像 tool call |
| `approval/resolved`、`tool.approval.resolved` | `tooldto.ToolApprovalResolved` | |

注意：`EventDispatcher` 会先跑公共 translator 再跑 Codex translator，所以同一个 raw event 可能产出多个 typed event。例如 `item/completed` 命中公共表时会产 `turndto.ItemCompleted`，若 payload 又像 tool call，Codex translator 还会产 `tooldto.ToolCallEnd`。

Codex translator 中明确 **仅记录日志、不产 typed event** 的事件：

- `mcpServer/startupStatus/update`
- `mcpServer/startupStatus/updated`

另外，`shouldWarnUnknownRawEvent()` 还把若干 plan/item 别名列入“无需 unknown warning”的白名单。当前其中 `item_plan_delta`、`agent/event/item_plan_delta`、`item/plan/updated`、`item_plan_updated`、`agent/event/item_plan_updated` 在公共 translator 表里也没有对应分支，因此实际是“不产 typed event，也不告警”。

### 5.3 公共 translator：`internal/provider/unified/event_map.go`

| Raw event / alias | Typed event |
|---|---|
| `warning`、`configWarning`、`windows/worldWritableWarning`、`deprecationNotice` | `agentdto.AgentWarning` |
| `error`、`stream_error` | `agentdto.AgentError` |
| `item/plan/delta`、`plan_delta`、`agent/event/plan_delta` | `turndto.PlanDelta` |
| `turn/plan/updated`、`plan_update`、`turn_plan` | `turndto.PlanUpdated` |
| `item/started`、`item_started`、`agent/event/item_started` | `turndto.ItemStarted` |
| `item/completed`、`item_completed`、`agent/event/item_completed`、`rawResponseItem/completed` | `turndto.ItemCompleted` |

### 5.4 Token 事件补充

`UITokensUpdated` 的触发路径不是上表这些 raw type，而是：

- `claudecli.translateClaudeEvent()` -> `unified.PublishUITokensUpdated(raw.Data, publish)`
- `codexapp.translateCodexEvent()` -> `unified.PublishUITokensUpdated(payload, publish)`

---

## 6. `shared` / `toolfilter` / `e2e`

### 6.1 `internal/provider/shared/`

`config_helpers.go` 提供 provider 共用配置工具：

- `ResolveBinaryDir()`：
  1. 优先显式 `binary_dir` / `binaryDir`  
  2. 否则收集候选目录：当前 executable 所在目录、`cwd`、`LookPath(mcp-lsp|mcp-orch)` 所在目录  
  3. 先返回第一个实际包含 `mcp-lsp` 或 `mcp-orch` 文件的候选目录  
  4. 若所有候选目录都没有 managed binary，再返回第一个非空候选目录
- `ConfigString()` / `ConfigStringSlice()` / `StringMap()`：统一读取 provider config。

当前直接使用它来拼 manifest 的是 **Claude provider**；Codex 当前主链路更偏向 dynamic tools + shared app-server，而不是从这里写 MCP config 文件。

### 6.2 `internal/provider/toolfilter/`

`presets.go` 提供三套预设：

- `ReviewerDecision()`：
  - 允许：`lsp_file`、`lsp_grep`、`lsp_inspect`、`lsp_xref`、`lsp_structure`、`lsp_completion`、`shared_file_read`
  - 禁止：`lsp_edit`、`code_run`、`code_run_test`、`orchestration_launch_agent`、`orchestration_stop_agent`
- `WorkerDecision()`：禁止 `orchestration_launch_agent`、`orchestration_send_message`、`orchestration_stop_agent`、`orchestration_list_agents`、`orchestration_get_agent_report`
- `FullAccessDecision()`：不做限制

从注释和当前接线看，这仍是 **预设策略库**，尚未深度接入 provider 主链路。

### 6.3 `internal/provider/e2e/`

- `claude_mcp_test.go`：验证 Claude manifest JSON 写出逻辑；其 `linkname` 目标 `claudecli.writeManifestConfig` 当前仍存在。  
- `codex_mcp_test.go`：仍假设存在 `writeCodexMCPConfig(path, manifest, cwd)`，并通过 `linkname` 直连 `codexapp` 包。  
- `doc.go`：包级说明仍描述“Codex: MCPManifest -> config.toml -> reload -> poll ready”。

**当前现状**：`codexapp` 包内已找不到 `writeCodexMCPConfig`，因此 `internal/provider/e2e` 包对 Codex 的测试在构建阶段就失败。  
**推断**：旧的 Codex MCP `config.toml` 注入链路已经被移除，或者尚未迁回当前 provider 主实现；当前主链路明显是 dynamic tools + shared app-server。

---

## 7. Claude / Codex 的关键差异总结

| 维度 | Claude CLI | Codex App |
|---|---|---|
| 通信方式 | stdio stream-json | WebSocket JSON-RPC |
| 进程模型 | 1 session = 1 Claude CLI 进程 | 1 session = 1 WS；app-server 可共享 |
| provider id 暴露 | raw `thread_id` 用 public id，真实 provider id 主要放 `session_id` / `ThreadID()` | raw `threadId` 被重写为 public `agentID`，真实 provider thread id 留在 session / binding |
| 启动 ready 语义 | 可提前 ready，之后 `system:init` 回填真实 id | `newSession()` 先建好 transport，再 `thread/start` / `thread/resume` |
| 恢复方式 | 本地 restart + `--resume` | reconnect + `thread/resume` + replay pending turn |
| 历史来源 | `~/.claude/projects/*/*.jsonl` | `~/.codex/sessions/**/rollout-*.jsonl` |
| 配置变更 | 运行中 `Configure()` 不支持；部分 turn override 会触发 restart | 支持 `thread/config/set` 与 slash-config |
| approval | 无内建桥接层 | approval/request_user_input bridge 是主链路之一 |
| 进程治理 | 管 Claude CLI 自身进程，复用外部 peer HTTP endpoint | 管 shared app-server、peer 进程、PID registry、orphan cleanup |

---

## 8. 结论

Provider 集成层的真实结构不是“两个 provider 并列”，而是三层：

1. **统一抽象层**：`contract` + `unified`  
2. **provider 适配层**：`claudecli` / `codexapp`  
3. **运行时辅助层**：`shared` / `toolfilter` / `e2e`

其中：

- **ClaudeCLI** 的重点在：CLI 启停、stdio 流解析、public/provider thread id 协调、CLI restart 恢复、manifest 注入。  
- **CodexApp** 的重点在：shared app-server、WS JSON-RPC、toolbridge、approval bridge、自动恢复、peer / orphan 治理。  
- **Unified** 的重点在：driver 选择、session 管理、auto-resume、公共事件翻译、typed event 输出。

如果继续扩展第三种 provider，最应该复用的骨架仍然是：

- `contract.Driver / Session`
- `dto.RawProviderEvent`
- `unified.EventDispatcher`
- `SessionManager / SessionResolver`

provider 自己实现的最小闭环则是：

- transport
- session 状态机
- raw event translator
- runtime-specific 的恢复 / 历史 / 进程管理

## 审查补遗

- 已修正文档中关于 **Claude session 启动一定等待 `system:init`** 的描述；源码实际支持 public thread 已知时提前 ready。  
- 已修正文档中 **事件映射表不完整** 的问题，补上：
  - Claude：`agent:stopped`、`agent:failed`、`turn:started`、`turn:input_received`
  - Codex：`session.configured`、`shutdown.complete` / `shutdown_complete`、`turn/aborted` / `turn.aborted`
- 已修正文档中 **`UITokensUpdated` 来源** 的描述；它来自 `ui_tokens.go` 抽取，而不是 `unified/event_map.go` 的 raw-type switch。  
- 已补充 **Codex raw `threadId` 会在 dispatch 时被重写为 public `agentID`**，避免把 provider 内部 thread uuid 直接暴露给 UI。  
- 已补充 **Codex approval raw event 的 bus 可见性限制**：translator 虽支持 `ToolApprovalRequested`，但在 `ApprovalManager` 存在时，运行时通常会直接桥接处理，不再把 raw event 广播出去。  
- 已补充 **Claude restart 条件**：不仅是 transport 不可用，`model` / `effort` / `MCP manifest` 变化也会触发 restart。  
- 已补充 **Codex 进程管理层次**：单 session WS 与共享 `ServerManager`、peer spawn、PID registry、orphan sweeper 的分工边界。  
- 已明确标注一处基于源码状态的推断：**Codex 旧版 `config.toml` MCP 注入链路大概率已被移除或尚未迁回**；证据是主链路已转向 dynamic tools，而 e2e 仍依赖缺失的 `writeCodexMCPConfig`。
