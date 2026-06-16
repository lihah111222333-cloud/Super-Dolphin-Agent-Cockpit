# DAG 节点查看对话跳转失败根因与修复计划

日期: 2026-06-06
原始范围: 仅做源码追溯与修复计划落盘，不改实现代码。
结论级别: P0 用户路径缺陷。DAG 节点已经有 child thread id 记录，但 UI 打开链路依赖当前 cwd 的 chat thread 列表，导致跨 worktree/cwd 的子代理对话无法可靠跳转。

执行更新: 已按本文的前端最小修复路径落地。DAG 节点“查看对话”现在通过显式 thread id 打开动作解析 `spawning_thread_id`，成功打开后才切到 Chat；失败时不再误跳到当前聊天页。普通 sidebar/chat 选择路径的 `agent_*` 保护逻辑保持不变。后端 UIState 物化加固暂未实施，当前验证中前端 upsert + `preserveActiveThreadId` 已能覆盖跨 worktree child thread 跳转。

## 背景与边界

主 agent 先在一个 worktree 中落盘文档，再通过 DAG 拉起多个子代理评审或执行任务。子代理的 `cwd` 被设置为该 worktree，因此它们不出现在主 agent 当前 `cwd` 的 agent 列表里。这个行为本身可以接受，暂时不需要把 agent 列表做成跨 cwd 全局视图。

严重问题不在 agent 列表，而在 DAG 节点提供了“查看对话”入口后，用户无法跳到子代理实际执行步骤的会话。这个入口不应该依赖主 agent 当前 `cwd` 的 agent/thread 列表；它应该以 DAG 节点已经记录的 `spawning_thread_id` 为准，直接打开对应 thread。

## 现有持久链路是成立的

1. `cmd/mcp-orch` 的代码地图明确说明，F1.5 起 `task_get_dag` DTO 透出 `spawning_thread_id`，并且 `nodeexec/executor_agent.go` 在 spawn 成功后写回该字段。
   - `docs/doc/codemap/02-mcp-orch.md:96`
   - `docs/doc/codemap/02-mcp-orch.md:102`

2. 远端 launcher 通过主控 RPC 启动子代理 thread。`remoteLauncher.Launch` 调 `thread/start`，从响应的 `thread.id/threadId/thread_id` 解析出 `ThreadID`，然后写回 agent runtime 的 `threadID/remoteThreadID`。
   - `internal/sidecar/orch/orchestration/launcher.go:180`
   - `internal/sidecar/orch/orchestration/launcher.go:211`
   - `internal/sidecar/orch/orchestration/launcher.go:222`
   - `internal/sidecar/orch/orchestration/launcher.go:233`

3. `thread/start` 在未传 `AgentID` 时会生成 `agent_*` 风格的 agent id；新建 thread 的公开 id 在 start 场景下优先使用 `PublicThreadID`，否则使用 `AgentID`。因此 `agent_*` 既可能是 runtime agent id，也可能是合法的公开 thread id。
   - `internal/module/thread/start_session.go:21`
   - `internal/module/thread/start_session.go:27`
   - `internal/module/thread/factory.go:50`
   - `internal/module/thread/factory.go:52`

4. `thread/start` 持久化并返回公开 thread id。响应同时包含 `thread.id`、`threadId`、`thread_id`、`agentId`、`agent_id`。
   - `internal/module/thread/lifecycle.go:268`
   - `internal/module/thread/lifecycle.go:270`
   - `internal/module/thread/lifecycle.go:288`
   - `internal/module/thread/rpc.go:240`
   - `internal/module/thread/rpc_types.go:462`

5. DAG agent executor 拿到 launcher 返回的 thread id 后，调用 `RecordNodeSpawn` 写回 DAG 节点。
   - `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:221`
   - `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:251`
   - `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:434`
   - `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:446`

6. store 层 fail-fast 要求 `thread_id` 非空，并通过 SQL 更新 `task_dag_nodes.spawning_thread_id`。
   - `internal/sidecar/orch/store/taskdag/store_node_spawn.go:45`
   - `internal/sidecar/orch/store/taskdag/store_node_spawn.go:52`
   - `internal/sidecar/orch/store/taskdag/store_node_spawn.go:78`
   - `internal/sidecar/orch/sql/queries/task_dag_node_spawning_thread.sql:17`
   - `internal/sidecar/orch/sql/queries/task_dag_node_spawning_thread.sql:26`

7. DAG DTO 把 `SpawningThreadID` 作为 `spawning_thread_id` 暴露给 `task_get_dag`。契约注释也明确说 UI 用它拼“节点行到子 agent thread”的跳转链接。
   - `internal/sidecar/orch/orchestration/dag.go:455`
   - `internal/sidecar/orch/orchestration/dag.go:475`
   - `internal/contract/orchestration.go:540`
   - `internal/contract/orchestration.go:543`

8. 前端 DAG 页面已经把 `raw.spawning_thread_id` 归一化为 `node.threadId`，节点行只在该字段存在时展示“查看对话”按钮。
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:146`
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:158`
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:1876`
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:1881`

结论: DAG 侧不是没有记录子会话。当前数据已经从 `thread/start` 经过 `spawning_thread_id` 到达前端节点行。

## 失败链路

1. “查看对话”按钮只调用通用 chat 选择器。
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:1886`
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:1887`
   - `frontend-app/src/pages/workflows/WorkflowPage.jsx:1888`

2. 通用 `setActiveThread` 第一行先用 `backendThreadIdForState(...)` 从当前 store 状态解析 id。
   - `frontend-app/src/entities/client/model/useClientStore.js:4541`
   - `frontend-app/src/entities/client/model/useClientStore.js:4544`

3. `backendThreadIdForState` 只信任当前 `state.threads` 中已出现的 id 或别名；如果没有命中，并且 id 匹配 `isAgentRuntimeId`，直接返回空字符串。
   - `frontend-app/src/entities/client/model/useClientStore.js:402`
   - `frontend-app/src/entities/client/model/useClientStore.js:865`
   - `frontend-app/src/entities/client/model/useClientStore.js:868`
   - `frontend-app/src/entities/client/model/useClientStore.js:870`

4. `isAgentRuntimeId` 的判定是 `/^agent[_-]/i`。这和 `thread/start` 默认公开 thread id 可能使用 `AgentID` 形成冲突。一个合法的 `agent_*` child thread id，只要还没有出现在主 cwd 当前 `state.threads` 中，就会被当成 runtime agent id 丢弃。
   - `frontend-app/src/entities/client/model/useClientStore.js:402`
   - `internal/module/thread/factory.go:52`

5. 即使 thread id 不是 `agent_*`，只要它不在当前 `ui/state/get` 快照的 `threads` 中，后续 `syncThreadState` 也会受到当前 cwd 快照限制。`syncThreadState` 再次用 `backendThreadIdForState` 解析 id，然后调用 `ui/state/get` 和 `thread/messages`。
   - `frontend-app/src/entities/client/model/useClientStore.js:4104`
   - `frontend-app/src/entities/client/model/useClientStore.js:4106`
   - `frontend-app/src/entities/client/model/useClientStore.js:4114`
   - `frontend-app/src/entities/client/model/useClientStore.js:4115`

6. `ui/state/get` 虽然要求传 `threadId`，但 handler 只把该 id 放入 diff request context，再调用 `svc.GetState(ctx)`。
   - `frontend-app/src/shared/api/backendApi.js:782`
   - `frontend-app/src/shared/api/backendApi.js:784`
   - `internal/module/uistate/rpc.go:18`
   - `internal/module/uistate/rpc.go:47`
   - `internal/module/uistate/rpc.go:49`

7. `uistate.GetState` 返回的是 service 当前 UIState 快照。初始快照来自 `ThreadLister.List(ctx)` 和 `OrchestrationService.ListAgents(ctx)`，没有按请求的 `threadId` 去读取或物化目标 thread。
   - `internal/module/uistate/service.go:128`
   - `internal/module/uistate/service.go:130`
   - `internal/module/uistate/service.go:137`
   - `internal/module/uistate/service.go:193`
   - `internal/module/uistate/service.go:198`

8. active thread preference 也只在目标 id 已存在于快照 `Threads` 时保留。前端 `snapshotActiveThreadId` 对显式 preferred id 也通过 `backendThreadIdFromThreads(preferredActiveThreadId, nextThreads, ...)` 查 `nextThreads`，不存在时会走自动选择或清空。
   - `internal/module/uistate/preferences.go:148`
   - `internal/module/uistate/preferences.go:152`
   - `internal/module/uistate/preferences.go:178`
   - `internal/module/uistate/preferences.go:183`
   - `frontend-app/src/entities/client/model/useClientStore.js:1818`
   - `frontend-app/src/entities/client/model/useClientStore.js:1841`

根因判断: DAG 节点跳转复用了“当前 cwd 下的普通会话选择”路径。这个路径的安全假设是“可选 thread 必须已在当前 chat/sidebar 快照中”，并且未知 `agent_*` 是 agent runtime id 而不是 thread id。DAG 节点链接的来源则不同，它是后端 DAG 持久化的 `spawning_thread_id`，应按可信 thread id 打开，不应被当前 cwd 的 agent/thread 列表过滤。

## 最小修复计划

### 1. 先加失败用例

前端回归用例:

- 在 `WorkflowPage` 或 `App` 层构造一个 DAG detail，其中节点包含 `spawning_thread_id: "agent_child_1"`。
- 当前 store 的 `threads` 列表不包含 `agent_child_1`，模拟主 agent cwd 下看不到子 worktree agent/thread。
- 点击“查看对话”。
- 期望不是调用普通 `setActiveThread` 后静默失败，而是通过新的显式打开动作解析并打开 `agent_child_1`，进入 chat 页面，并触发 `thread/resolve`、`ui/state/get` 或 `thread/messages` 中约定的加载路径。

store 单元用例:

- `openThreadById("agent_child_1", { source: "dag-node" })` 在 `threads` 缺失该 id 时仍应调用 `resolveThreadIdentity({ threadId: "agent_child_1" })`。
- `resolveThreadIdentity` 返回合法 thread ref 后，store 必须 upsert 一个 thread summary，设置 `activeThreadId`，并加载 messages/state。
- `resolveThreadIdentity` 返回空对象、id 不匹配或 RPC 报错时，必须 fail-fast 通知错误，不允许回退到随机当前 thread。

### 2. 前端增加可信 thread id 打开动作

在 `frontend-app/src/entities/client/model/useClientStore.js` 增加一个专用动作，建议命名为 `openThreadById` 或 `openExplicitThread`。它只服务于 DAG 节点、可观测性 trace drilldown 等后端已给出 thread id 的入口，不替换普通 sidebar/chat 选择。

行为要求:

- 输入 id 先做 `normalizeBackendThreadId`，但在调用方标记 `source: "dag-node"` 时不要走未知 `agent_*` 拦截。
- 调用已有 `thread/resolve` 能力确认 thread 存在。前端现有 API 是 `resolveThreadIdentity`，后端 handler 是 `thread/resolve`，最终调用 `svc.Get(ctx, id)`。
  - `frontend-app/src/shared/api/backendApi.js:1040`
  - `frontend-app/src/shared/api/backendApi.js:1041`
  - `internal/module/thread/rpc.go:42`
  - `internal/module/thread/rpc.go:46`
  - `internal/module/thread/service.go:140`
- 用 resolve 返回的 `Ref` upsert 到 `state.threads`，字段至少保留 `id/name/agentId/status/provider/cwd/createdAt/updatedAt`。`Ref` 当前已经包含这些身份字段。
  - `internal/module/thread/contract.go:222`
  - `internal/module/thread/contract.go:235`
- 设置 `activeThreadId` 后调用 `syncThreadState(id, { includeArchived: true, includeDiff: false, preserveActiveThreadId: true })`，同时让 `thread/messages` 加载历史。
- `WorkflowNodeRow.openWorkflowNodeThread` 改为调用该专用动作。若动作不存在，应显式报错或显示通知，不要静默 fallback 到当前失败的普通选择器。

这个修复保持普通 `setActiveThread` 的保护语义不变。未知 `agent_*` 仍然不能从普通 sidebar 选择路径被当作后端 thread 乱打开；只有 DAG 节点这种后端持久链接可以绕过 `isAgentRuntimeId` 的误判。

### 3. 后端 UIState 物化加固

为了避免前端刚 upsert 的 thread 被下一次 `ui/state/get` 快照清掉，建议同步加一个小的后端加固:

- 给 `uistate` 注入一个窄接口，例如 `contract.ThreadResolver`，提供 `Get(ctx, threadID)`，或扩展现有 `ThreadLister`。
- `ui/state/get` 收到显式 `threadId` 时，在 `GetState` 或快照构建后通过 `ThreadResolver.Get(ctx, threadId)` 读取目标 thread，并 upsert 到 `snapshot.Threads`。
- 如果目标不存在，返回语义化 not found，不要静默回退到当前 active thread。
- 只对请求显式带 `threadId` 的路径生效，普通 sidebar 初始快照仍保持当前 cwd 范围。

如果希望更收敛，也可以新增 `ui/thread/open` RPC，把 resolve、summary 物化、state 快照和必要的 message bootstrap 放在一个后端语义里。但从当前代码看，复用 `thread/resolve` 加 `ui/state/get` 物化的改动更小。

### 4. 验收标准

- 子代理 `cwd` 是 worktree，且该子代理不出现在主 agent 当前 cwd 的 agent 列表时，DAG 节点“查看对话”仍能打开对应 child thread。
- `spawning_thread_id` 为 `agent_*` 时可以打开，因为它来自 DAG 持久字段，不再被误判为未知 runtime agent id。
- `spawning_thread_id` 缺失时按钮不展示或展示不可用状态；id 无效时显示明确错误。
- 普通 sidebar/chat thread 选择逻辑不放宽，未知 runtime agent id 不应被普通选择路径打开。
- 当前 cwd 的 agent 列表行为不变，不把跨 worktree 子代理强行混入主 cwd 列表。

建议验证命令:

```bash
cd frontend-app
npm test -- WorkflowPage
npm test -- useClientStore
npm run lint
```

若修改后端 UIState 物化:

```bash
./scripts/test_with_guard.sh internal/module/uistate/rpc.go
./scripts/test_with_guard.sh ./internal/module/uistate -count=1
```

若修改 DAG DTO 或 orchestrator 写回链路:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/store/taskdag -count=1
```

## 不建议的修复

- 不建议把主 cwd agent 列表改成跨 cwd 全局列表来掩盖跳转问题。这样会扩大范围，并且和用户当前可接受的 cwd 隔离行为冲突。
- 不建议简单删除 `isAgentRuntimeId` 判断。该判断可能保护普通 UI 路径不把 runtime agent id 当 thread id；正确做法是给 DAG 持久链接增加显式可信打开路径。
- 不建议让 `openWorkflowNodeThread` 只在失败后跳转 chat 页面。当前问题需要可验证地打开目标 child thread，而不是只切页面。
