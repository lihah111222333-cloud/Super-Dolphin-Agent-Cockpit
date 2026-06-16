# V2↔V3 1:1 对齐：agent.launch + agent.stop

## 范围

- V2：
  - `go-agent-v2/internal/apiserver/methods_orchestration.go`
  - `go-agent-v2/internal/runner/manager_launch.go`
  - `go-agent-v2/internal/runner/manager_lifecycle.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go`
  - `go-agent-v2/legacy-agentsdk/claude/client.go`
  - `go-agent-v2/legacy-agentsdk/agentcore/types.go`
- V3：
  - `internal/sidecar/orch/orchestration/rpc.go`
  - `internal/sidecar/orch/orchestration/rpc_types.go`
  - `internal/sidecar/orch/orchestration/contract.go`
  - `internal/sidecar/orch/orchestration/service.go`
  - `internal/sidecar/orch/orchestration/helpers.go`
  - `internal/sidecar/orch/orchestration/runner_actor.go`
  - `internal/module/thread/lifecycle.go`（仅用于核对 session 创建职责边界）

## 总结

| 比对项 | 结论 | 结论说明 |
| --- | --- | --- |
| `agent.launch` 请求参数 | ❌ | V2 是 `id/prompt/instructions/dynamic_tools/config`，V3 是 `agentId/name/cwd/command/parentId/env`，线面和 JSON key 都不兼容。 |
| `agent.launch` 返回值 | ❌ | V2 返回 `{agent_id,name,status}`；V3 handler 返回 `nil`。 |
| 进程启动链 | ❌ | V2 是 provider-aware launch（factory/port/fallback/resume）；V3 只是 `exec.Command + Start`。 |
| session 创建 | ❌ | V2 launch 链直接建/续 provider session；V3 `agent.launch` 只拉起进程，session 创建在 `thread.Start/Resume`。 |
| port/provider 解析 | ❌ | V2 由 provider registry + free port 分配给出真实值；V3 只从 `env/argv` 猜测。 |
| `agent.stop` 请求参数 | ✅ | V3 `agentIDParams.UnmarshalJSON` 同时接受 `agentId` 和 legacy `agent_id`。 |
| `agent.stop` 返回值 | ❌ | V2 返回 `{success:true}`；V3 handler 返回 `nil`。 |
| `agent.stop` 清理链 | ⚠️ | 都会停进程并清状态，但 V3 是硬 kill + 提前发 stopped 事件，链路顺序与 V2 不同。 |

## 1. `agent.launch` 参数面

### V2

- `methods_orchestration.go:29-37` 定义 RPC 入参：
  - 顶层字段：`id`、`name`、`prompt`、`cwd`、`instructions`、`dynamic_tools`、`config`
- `methods_orchestration.go:39-71` 直接把这些字段传给 launcher。
- `agentcore/types.go:84-104` 里的 `LaunchConfig` 还承载：
  - `approvalPolicy`
  - `sandbox`
  - `summary`
  - `effort`
  - `personality`
  - `model`
  - `developerInstructions`
  - `provider`
  - `resumeSessionID`
  - `parentID`

### V3

- `rpc_types.go:8-17` 的 `launchParams` 只有：
  - `agentId`
  - `name`
  - `cwd`
  - `command`
  - `parentId`
  - `env`
- `rpc.go:79-87` 只是把这 6 个字段映射到 `LaunchRequest`。
- `contract.go:40-47` 的 `LaunchRequest` 也只有这 6 个字段。
- `rpc_types.go:8-9` 已经直接写了 TODO：V2 `agent.launch` 还带 `prompt/instructions/dynamic_tools/config`，当前 V3 contract 没暴露。

### 对齐判断

- ❌ 不对齐。
- 这是 wire contract 级别的不兼容，不是“部分字段缺失”这么简单：
  - V2 用 `id`，V3 要 `agentId`
  - V2 没有 `command`，V3 `validateLaunchRequest` 强制要求 `command`，见 `helpers.go:293-301`
  - V3 完全没有 `prompt/instructions/dynamic_tools/config`
  - V2 的 `parentID` 在 `config.parentID` 里；V3 的 `parentId` 在顶层

### 补充观察

- `methods_orchestration.go:66-70` 的 V2 `agent.launch` 返回 `{agent_id,name,status:"running"}`。
- `rpc.go:17-19` 的 V3 `agent.launch` 只返回 `svc.LaunchAgent(...)` 的 error，成功时结果体是 `nil`。
- V2 虽然有 `prompt` 字段，但当前 provider 启动实现并没有把它作为首条 turn 发送：
  - Codex `SpawnAndConnect` 只在日志里记 `prompt_len`，实际调用的是 `ThreadStart(cwd, model, instructions, dynamicTools)`，见 `client_appserver_runtime.go:129-156`、`client_appserver_protocol.go:102-149`
  - Claude `SpawnAndConnect` 直接进 `spawnWithResume`，内部只保存 `model/cwd/instructions/dynamicTools`，见 `claude/client.go:163-165,176-225`

## 2. 进程启动链

### V2

- `manager_launch.go:268-333` 的 `AgentManager.Launch` 是完整启动链。
- 它先做 provider 解析：
  - `manager_launch.go:74-94` 通过 `ResolveProviderFactory(...)` 选 factory
  - 显式 provider 不存在时直接报错，不接受 runtime fallback
- 再做 runtime 准备：
  - `manager_launch.go:96-117` 分配 free port
  - `manager_launch.go:156-190` 创建 client、应用 launch config、写入 `m.agents`
  - `manager_launch.go:294-296` 绑定 event handler
- 真正启动时：
  - `manager_launch.go:192-207` 走 `SpawnAndConnect` 或 `SpawnWithResumeSession`
  - `manager_launch.go:209-251` app-server 失败时还能尝试 REST fallback
  - `manager_launch.go:253-266` 启动失败会 rollback 并从 agents map 删除

### V3

- `service.go:110-125` 的 `LaunchAgent` 只有 4 步：
  - 校验
  - `agentForLaunchLocked`
  - `prepareLaunchStateLocked`
  - `startProcessLocked`
- `helpers.go:34-50` 的 `agentForLaunchLocked` 只是把 request 拷到 runtime：
  - `name/parentID/cwd/command/env`
  - 再顺手算一个 `port/provider`
- `service.go:234-263` 的 `startProcessLocked` 是标准库 `exec.Command(...).Start()`：
  - `cmd.Dir = agent.cwd`
  - `cmd.Env = append(os.Environ(), agent.env...)`
  - 成功后记 `cmd/launchSeq/startedAt`
  - 触发 `LaunchSucceeded`
- 没有：
  - provider registry
  - app/rest 双 factory
  - free port 分配
  - resume-session 启动分支
  - transport fallback
  - 启动失败回滚删除 runtime

### 对齐判断

- ❌ 不对齐。
- V3 当前是“原始进程管理器”；V2 是“provider-aware runtime launcher”。

## 3. session 创建职责

### V2

- session/thread 创建直接发生在 launch 链里。
- Codex：
  - `client_appserver_runtime.go:129-156` `SpawnAndConnect`
  - `client_appserver_protocol.go:102-149` 调 `thread/start`，拿到 thread ID 并写回 client
- Claude：
  - `claude/client.go:163-165,176-225` 启动 CLI
  - `claude/client.go:617-625` 在 `EventSessionConfigured` 时记录 thread/session ID
- `manager.go:278-313` 的 `List()` 直接从 `proc.Client.GetThreadID()` 读 thread ID，说明 launch 之后 client 已经持有 provider session/thread 上下文。

### V3

- `service.go:110-125` 的 `LaunchAgent` 只启动进程，不碰 session starter。
- `helpers.go:52-60`、`service.go:249-251` 还会把 `threadID` 清空。
- session 创建在 thread 模块：
  - `thread/lifecycle.go:44-76` `Start()` 先 `launchAgent`，再 `startSession`
  - `thread/lifecycle.go:204-220` `startSession()` 调 `starter.StartSession(...)`
  - `thread/lifecycle.go:77-107,221-229` `Resume()` 单独走 `ResumeSession(...)`
- 也就是说：
  - 直接调用 orchestration RPC `agent.launch` 不会创建 session
  - 只有 `thread.Start/Resume` 这层组合调用才会补上 session 注册与 thread/binding 持久化

### 对齐判断

- ❌ 不对齐。
- 如果目标是 V2 `agent.launch` 的 1:1 语义，V3 现在少了最关键的一段：launch 内联 session 创建。

## 4. port/provider 解析

### V2

- provider 是显式 runtime 决策：
  - `manager_launch.go:74-94`
  - `provider_registry.go:111-133`
- port 是 manager 分配的真实监听端口：
  - `manager_launch.go:96-117`
  - `manager_launch.go:162-168` 用该 port 创建 client
- 对外暴露时读的是 client 真值：
  - `manager.go:289-299` 里的 `Port: proc.Client.GetPort()`
  - `Provider: proc.Provider`

### V3

- `helpers.go:233-253` 的 `launchPort/launchProvider` 只是从 request 里猜：
  - `PORT`
  - `--port` / `-p`
  - `AGENT_PROVIDER` / `CODEX_PROVIDER` / `PROVIDER`
  - `--provider`
- 没有 registry 解析，没有默认 provider 继承，也没有真实 port 分配。
- `thread/lifecycle.go:326-336` 的默认 launch request 甚至只塞了：
  - `AgentID`
  - `Name`
  - `Cwd`
  - `Command: []string{os.Executable()}`
- 这意味着 thread 默认启动路径下，V3 的 `provider` 往往是空，`port` 往往是 0，除非外部显式把它们编码进 `env/argv`。

### 对齐判断

- ❌ 不对齐。
- V2 是 authoritative resolution；V3 是 heuristic extraction。

## 5. `agent.stop` 清理链

### V2

- RPC 层：
  - `methods_orchestration.go:93-107`
  - 入参是 `agent_id`
  - 成功返回 `{success:true}`
- service/runtime 层：
  - `manager_lifecycle.go:19-92`
- 停止链顺序：
  1. 从 `m.agents` 查 `proc`
  2. 先 `proc.Client.Shutdown()`
  3. Shutdown 失败再 fallback `proc.Client.Kill()`
  4. 停止动作完成后才 `delete(m.agents, id)`
  5. 清 `activeSubmission`
  6. 置 `StateStopped`
  7. 如果中途有活跃提交，补发 synthetic `turn_aborted`

### V3

- RPC 层：
  - `rpc.go:40-42`
  - `rpc_types.go:19-40` 的 `agentIDParams.UnmarshalJSON` 同时接受 `agentId` 和 legacy `agent_id`
  - 成功返回 `nil`
- service/runtime 层：
  - `service.go:127-141`
  - `service.go:155-164`
  - `helpers.go:303-308`
  - `runner_actor.go:26-59`
  - `service.go:355-394`
- 停止链顺序：
  1. `fireOrForceLocked(..., TriggerStopRequested)`
  2. `stopRequested = true`
  3. `queue.Clear()`
  4. `activeTurnID = ""`
  5. `threadID = ""`
  6. `cmd.Process.Kill()`
  7. 立刻 `removeSession(agent.id)`
  8. 立刻 `publishAgentStopped(..., "user_requested")`
  9. 等后台 waiter 收到 `cmd.Wait()` 退出
  10. `handleProcessExit()` 再次 `removeSession(agent.id)`，再触发 `ProcessExited/LaunchFailed`

### 对齐判断

- ⚠️ 部分对齐。
- 相同点：
  - 都会终止进程
  - 都会清理活跃执行上下文
- 关键差异：
  - V2 先 graceful shutdown，再 kill fallback；V3 只有 `Process.Kill()`
  - V2 停止完成后会从 `m.agents` 删除；V3 不删除 runtime，只在真实退出前先发 `AgentStopped`，后续再把 `cmd` 置空并迁状态
  - V2 有 synthetic `turn_aborted` 补偿；V3 没有对应补偿逻辑
  - V3 `removeSession()` 在 `StopAgent` 和 `handleProcessExit()` 各调一次，是否幂等取决于 `SessionCleaner` 实现

## 结论

- `agent.launch`：❌ 当前不是 V2 的 1:1 对齐，差异同时存在于 RPC 参数面、返回值、启动链、session 创建职责、port/provider 解析。
- `agent.stop`：⚠️ 请求参数兼容性基本够用，但返回值与清理时序仍不对齐，尤其是“提前发布 stopped”和“只有硬 kill”这两点。
- 如果要做真正的 V2↔V3 对齐，`agent.launch` 至少还要补：
  - V2 legacy payload 兼容解析
  - `prompt/instructions/dynamic_tools/config` contract
  - provider-aware 启动与 resume/fallback
  - launch 内联 session 创建或等价封装
