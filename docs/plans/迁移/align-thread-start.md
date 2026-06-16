# V2↔V3 1:1 对齐：thread/start

## 范围

- V2：
  - `go-agent-v2/internal/apiserver/methods_thread.go`
  - `go-agent-v2/internal/apiserver/methods.go`
  - `go-agent-v2/internal/apiserver/provider_adapter_registry.go`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go`
  - `go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go`
  - `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go`
- V3：
  - `internal/module/thread/rpc.go`
  - `internal/module/thread/rpc_types.go`
  - `internal/module/thread/contract.go`
  - `internal/module/thread/lifecycle.go`
  - `internal/sidecar/orch/orchestration/service.go`
  - `internal/sidecar/orch/orchestration/events.go`
  - `internal/sidecar/orch/orchestration/helpers.go`
  - `internal/provider/unified/client.go`
  - `internal/provider/unified/session.go`
  - `internal/provider/unified/registry.go`
  - `internal/provider/codexapp/driver.go`
  - `internal/provider/codexapp/session.go`
  - `internal/provider/claudecli/driver.go`
  - `internal/store/thread/store.go`
  - `internal/store/binding/store.go`
  - `internal/store/sqlc/query_agent_thread.go`
  - `internal/store/sqlc/query_agent_binding.go`

## 总结

| 比对项 | 结论 | 结论说明 |
| --- | --- | --- |
| 参数字段 | ❌ | V2 暴露 `model/modelProvider/cwd/approvalPolicy/baseInstructions/developerInstructions/sandbox/summary/effort/personality`；V3 暴露 `provider/cwd/model/prompt/approvalPolicy/instructions/effort/personality`。字段面和语义都不兼容。 |
| 返回值结构 | ❌ | V2 返回 `{thread:{id,status},model,modelProvider,cwd,approvalPolicy}`；V3 只返回 `{threadId,agentId}`。 |
| session 创建 | ⚠️ | 两边都会在 `thread/start` 里完成 agent+session 建立，但 V2 是 `threadID=agentID` 的 launch+binding 一体化链路，V3 是先 `LaunchAgent` 再 `StartSession`，并把 session 注册到 `SessionManager`。 |
| store 写入 | ⚠️ | V2 只持久化并校验 binding；V3 会先写 `agent_threads`，再写 `agent_provider_binding`。若 binding 冲突，V3 可能留下已写入的 thread 行。 |
| 事件发布 | ⚠️ | V2 固定做 `uiRuntime.ReplaceThreadsWithSource("thread_start", ...)`；V3 改成 orchestration 事件总线 + provider raw event，且 codex/claude 启动事件行为不一致。 |
| 错误处理 | ⚠️ | 两边都会在后续步骤失败时停掉已拉起进程，但 V2 有 fresh-id 重试、偏好解析、统一 wrapped error；V3 缺少 threadID 新鲜度预检，且持久化不是原子链。 |
| 边界条件 | ❌ | `空 provider`、`空 cwd`、`重复 start` 的处理都已改变，不是 1:1 对齐。 |

## 1. 参数字段

### V2

- `methods_thread.go:16-27` 的 `threadStartParams` 暴露：
  - `model`
  - `modelProvider`
  - `cwd`
  - `approvalPolicy`
  - `baseInstructions`
  - `developerInstructions`
  - `sandbox`
  - `summary`
  - `effort`
  - `personality`
- `methods_thread.go:45-63` 会先走 `resolveThreadStartConfig(...)`，再把字段传给 `providerAdapter.ThreadStart(...)`。
- `methods_thread.go:107-139,173-218` 额外承担：
  - active provider 解析
  - model 默认值
  - approvalPolicy 默认值
  - `danger-full-access => approvalPolicy=never`
  - `sandbox/summary/effort/personality` 的偏好读取与兜底

### V3

- `rpc_types.go:7-16` 的 `startParams` 只有：
  - `provider`
  - `cwd`
  - `model`
  - `prompt`
  - `approvalPolicy`
  - `instructions`
  - `effort`
  - `personality`
- `rpc.go:21-31` 直接映射到 `StartRequest`。
- `contract.go:28-38` 的 `StartRequest` 也只有这 8 个字段。
- `lifecycle.go:208-219` 下发给 provider 的 `StartSessionRequest.Config` 只保留：
  - `approvalPolicy`
  - `effort`
  - `personality`
- `internal/provider/codexapp/driver.go:41-52,124-136` 说明底层 codex driver 其实还能接收：
  - `modelProvider`
  - `baseInstructions`
  - `developerInstructions`
  - `summary`
  - `sandbox`
  但 thread 模块没有把这些字段暴露/透传出来。

### 对齐判断

- ❌ 不对齐。
- 差异不是“少几个可选字段”，而是 contract 级改型：
  - V3 新增必填 `provider`
  - V3 新增 `prompt`
  - V3 缺失 `modelProvider`
  - V3 缺失 `developerInstructions`
  - V3 缺失 `sandbox`
  - V3 缺失 `summary`
  - V3 把 V2 的 `baseInstructions` 近似折叠成 `instructions`
- `lifecycle.go:213` 的 `firstNonEmpty(req.Instructions, req.Prompt)` 还把 `prompt` 混入 provider 启动指令，这在 V2 `thread/start` 里没有对应语义。

## 2. 返回值结构

### V2

- `methods_thread.go:69-75` 返回：
  - `thread.id`
  - `thread.status`
  - `model`
  - `modelProvider`
  - `cwd`
  - `approvalPolicy`
- `thread_lifecycle_logic.go:58-69` 的 `newThreadStartResult(...)` 明确给出：
  - `Status: "running"`
  - `Cwd` 为空时强制转 `"."`

### V3

- `contract.go:40-43` 的 `StartResult` 只有：
  - `threadId`
  - `agentId`
- `rpc.go:21-31` 直接把 `svc.Start(...)` 的结果返回，没有再包装 `status/model/provider/cwd/approvalPolicy`。

### 对齐判断

- ❌ 不对齐。
- V3 少了 V2 响应里最关键的启动后确认信息：
  - thread status
  - effective model
  - model provider
  - effective cwd
  - effective approvalPolicy
- 反过来，V3 新增了 `agentId`，这说明它的运行时身份模型已经不是 V2 的 `threadID=agentID`。

## 3. session 创建

### V2

- `methods_thread.go:45-63` 先分配 fresh threadID，再调用 `providerAdapter.ThreadStart(...)`。
- `adapter_lifecycle.go:40-80` 里：
  1. 组装 `LaunchConfig`
  2. 调 `RunThreadStart(...)`
  3. 在 launch 后通过进程对象注册 binding
- `thread_lifecycle_logic.go:33-55` 的 `RunThreadStart(...)` 顺序是：
  1. 校验 threadID
  2. 解析 start instructions
  3. `launchThread(...)`
  4. `registerStartedThreadBinding(...)`
  5. `syncStartedThreadRuntime(...)`
- 这里 session/thread 上下文是 launch 链内隐式建立的；没有独立的 session registry。

### V3

- `rpc.go:21-31` 的 `thread/start` 调 `svc.Start(...)`。
- `lifecycle.go:44-76` 的 `Start(...)` 顺序是：
  1. `normalizeStartRequest(...)`
  2. `launchAgent(...)`
  3. `startSession(...)`
  4. `lookupSession(agentID)`
  5. 取 `session.ThreadID()`
  6. `persistThreadState(...)`
- `lifecycle.go:171-188` 里如果 `AgentID` 为空，会生成新的 `agent-*`。
- `unified/client.go:29-67` 会：
  1. 按 `provider` 解析 driver
  2. 调 driver `StartSession(...)`
  3. 成功后 `sessions.Register(agentID, session)`
- `unified/session.go:31-47` 表明 session 是按 `agentID` 存在内存 map 里的。

### 对齐判断

- ⚠️ 有同类能力，但不是 1:1 语义。
- 主要差异：
  - V2 用预先分配的 `threadID` 作为 launch 标识
  - V3 用独立 `agentID` 启动，再从 session 反查 `threadID`
  - V2 session 是 launch 内隐式结果
  - V3 session 是显式 `StartSession + Register`
- 这意味着 V3 的 thread/start 已经不是 V2 那种“单一 thread identity 驱动全链路”的模型。

## 4. store 写入

### V2

- `adapter_thread_listing.go:270-289` 的 `registerThreadBinding(...)` 顺序是：
  1. 规范化输入
  2. 预查已有 binding
  3. 判断是否允许持久化
  4. 持久化 binding
  5. 反查 verify
- `adapter_thread_listing.go:192-209` 对已有 binding 的规则是：
  - 同 agent + 同 providerThreadID：幂等成功
  - 同 binding 但 cwd 之前缺失：允许补写
  - 不同 providerThreadID：直接拒绝
- `adapter_thread_listing.go:229-239` 只写 binding，不写 thread 表。

### V3

- `lifecycle.go:62-74,238-271` 的 `persistThreadState(...)` 顺序是：
  1. `rememberThreadAgent(threadID, agentID)` 写内存映射
  2. `threadStore.Upsert(...)`
  3. `bindingStore.Upsert(...)`
- `lifecycle.go:246-255` 写入 `agent_threads`：
  - `thread_id`
  - `prompt`
  - `model`
  - `cwd`
  - `status: created`
  - `created_at/updated_at`
  - `owner_thread_id`
- `lifecycle.go:262-270` 写入 `agent_provider_binding`：
  - `agent_id`
  - `provider`
  - `provider_thread_id`
  - `codex_thread_id`
  - `cwd`
  - `created_at/updated_at`
- `query_agent_thread.go:12` 说明 thread 行是 `ON CONFLICT (thread_id) DO UPDATE`。
- `query_agent_binding.go:7` 说明 binding 行是 `ON CONFLICT (agent_id) DO UPDATE`。
- `binding/store.go:44-58` 又补了一层 provider-thread 唯一冲突判定：
  - 如果同 `provider+providerThreadID` 已经属于别的 agent，会报错
  - 但这一步发生在 thread row 已写之后

### 对齐判断

- ⚠️ 降级。
- V3 不是“少写一张表”，而是写入顺序和一致性模型变了：
  - V2 是 binding-first 且带 verify
  - V3 是 thread-first，再写 binding
- 直接后果：
  - 如果 binding 冲突，`Start(...)` 会失败
  - 但 `agent_threads` 的 upsert 已经发生，可能留下无 binding 的 thread 行
- 这是 V2 `thread/start` 没有的部分持久化风险。

## 5. 事件发布

### V2

- `adapter_lifecycle.go:64-68` 在成功路径上固定调用：
  - `uiRuntime.ReplaceThreadsWithSource("thread_start", ...)`
- 也就是 `thread/start` 成功后会立即做一次线程列表快照刷新。

### V3

- `orchestration/service.go:255-263` 启动 agent 成功后调用：
  - `publishAgentLaunched(...)`
- `orchestration/service.go:281-288` 在状态机跳转时调用：
  - `publishStateChanged(...)`
- `orchestration/events.go:13-33` 实际发布的是：
  - `agentdto.StateChanged`
  - `agentdto.AgentLaunched`
- provider 侧还有 raw event 分发：
  - `unified/event_map.go:42-66` 会把 driver raw event 翻译后重新发布到 event bus
  - `claudecli/driver.go:130-141` 在 session ready 后显式 dispatch `agent:launched`
  - `codexapp/session.go:237-263` 只转发 transport notification，本身没有与 `claude` 对称的显式 launched event

### 对齐判断

- ⚠️ 降级。
- V3 不是没有事件，而是事件面改了：
  - V2：thread/start 成功后立刻刷新 thread runtime snapshot
  - V3：发布 agent lifecycle event；thread 视图是否收敛依赖 event translator 和消费者
  - provider 行为还不完全一致，`claude` 有显式 launched raw event，`codexapp` 启动路径没有同级显式事件

## 6. 错误处理

### V2

- `methods_thread.go:77-90` 在 start 前先做 fresh threadID 分配，最多重试 8 次。
- `methods_thread.go:92-105` 同时检查：
  - manager 里是否已有运行中的同 ID
  - history/binding 里是否已有同 ID
- `methods_thread.go:173-180` 对坏掉的 sandbox 偏好只告警并忽略，不直接失败。
- `methods_thread.go:117-120` 对 `danger-full-access` 自动改写 `approvalPolicy=never`。
- `methods_thread.go:64-67` 启动失败会记录 warning。
- `adapter_lifecycle.go:69-78` 在 launch 后续步骤失败时会 stop 已拉起进程。
- `thread_lifecycle_logic.go:49,96` 统一用 `Server.threadStart` 包装错误来源。

### V3

- `lifecycle.go:171-188` 只硬校验：
  - `provider` 不能为空
  - `agentID` 为空时自动生成
- `lifecycle.go:50-74` 在以下任一阶段失败都会 `stopAgent(...)`：
  - `launchAgent`
  - `startSession`
  - `lookupSession`
  - `persistThreadState`
- `unified/registry.go:27-39` 空/未知 provider 会直接报 `unknown provider`
- `orchestration/helpers.go:293-300` 启动 agent 只校验：
  - `agentID`
  - `command`
- thread 模块自身没有 V2 那样的：
  - threadID freshness 检查
  - 偏好解析/兜底
  - start 配置日志
  - 统一 wrapped error 前缀

### 对齐判断

- ⚠️ 降级。
- V3 的“失败回滚”是有的，但缺少 V2 的“失败前防呆”。
- 尤其是：
  - 没有 provider fallback
  - 没有 threadID freshness preflight
  - 没有原子化 store 写入

## 7. 边界条件

### 空 provider

#### V2

- RPC 入参没有 `provider` 字段。
- `methods.go:56-74` 的 `activeProvider(...)` 会从偏好里取当前 provider。
- `provider_adapter_registry.go:9,48-53` 默认 provider 是 `codex`。

#### V3

- `rpc_types.go:8` 暴露了顶层 `provider`。
- `lifecycle.go:181-183` 明确要求 `provider` 非空，否则返回 `provider is required`。

#### 判断

- ❌ 不对齐。
- V2 是“省略 provider 走 active/default provider”。
- V3 是“provider 不给就失败”。

### 空 cwd

#### V2

- `methods_thread.go:51-53` 把 `strings.TrimSpace(p.Cwd)` 直接传给 `ThreadStart(...)`。
- `thread_lifecycle_logic.go:58-69` 只会把 result/launch 用的 `cwd` 兜底成 `"."`。
- 但 `adapter_lifecycle.go:59-63` 注册 binding 时仍把外层原始 `cwd` 传进去。
- `adapter_thread_listing.go:186-188` 明确拒绝空 `cwd`。

#### V3

- `orchestration/helpers.go:293-300` 不校验 `cwd`。
- `lifecycle.go:298-336` 的 launch request 允许空 `cwd`。
- `lifecycle.go:246-250,267` 允许把空 `cwd` 写入 thread/binding store。

#### 判断

- ❌ 不对齐。
- 从 V2 控制流可直接推导：如果上层没预填 `cwd`，start 很可能在 binding 注册阶段失败。
- V3 则把空 `cwd` 视为合法输入并继续执行，最终是否可运行取决于具体 provider/子进程。

### 重复 start

#### V2

- `methods_thread.go:46-49,77-105` 每次 start 都先分配 fresh threadID，并对 runtime/history 做冲突检查。
- `adapter_thread_listing.go:196-202` 如果已有 binding 指向别的 provider thread，会直接报：
  - `new thread creation must use a fresh thread id`

#### V3

- 公开 RPC 不暴露 `agentID`，`lifecycle.go:184-186` 在 service 层会自动生成新 agentID，因此重复调用通常会启动新 agent。
- 但 V3 没有任何“provider 返回的 threadID 必须是 fresh”的预检。
- `lifecycle.go:246-270` 会先 upsert thread，再 upsert binding。
- `binding/store.go:44-58` 如果 provider thread 已属于别的 agent，会在 binding 阶段报错。

#### 判断

- ❌ 不对齐。
- V2 的重复 start 语义是“先拿 fresh threadID，再启动”。
- V3 的重复 start 语义更接近“先启动，再相信 provider 返回的 threadID，冲突时晚失败”。
- 这不只是顺序不同，而是失败窗口不同：V3 会多出部分持久化状态。

## 结论

- 结论：❌ 缺失。
- 原因不是单点缺字段，而是 `thread/start` 的 wire contract、identity 模型、store 写入顺序、事件面、边界语义都变了。
- 若按 V2 `thread/start` 的 1:1 语义判定，V3 当前至少缺以下关键能力：
  - V2 参数面完整透传：`modelProvider/developerInstructions/sandbox/summary`
  - V2 返回面：`thread.status/model/modelProvider/cwd/approvalPolicy`
  - V2 的 active/default provider fallback
  - V2 的 fresh threadID 预分配与重复 start 保护
  - V2 的 binding-first + verify 持久化语义
