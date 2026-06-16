# P12: Sub-Agent Turn Runtime — Launcher 接口注入（v8）

> **给 Claude:** 必须使用 @执行计划 逐任务实现此计划。

> **修订历史:**
> - v1-v3: 探索 config/*、config.toml、remote bridge 路线
> - v4-v5: 确定纯 RPC proxy + parent 装 orchestration.Module，发现自递归问题
> - v6: Launcher 接口注入解决自递归，3/4 CONDITIONAL PASS
> - **v7: 整合 8 个审查通过条件
> - **v8: 修正 5 个审查反馈** — ① AgentLauncher 参数改 *agentRuntime（local threadID 为空问题）；② jrpc2 IsClosed→IsStopped；③ remote SubmitTurn 绕过 queue；④ remote 读面明确；⑤ 补 threadAgents 清理测试，形成可执行闭环**

## 决策（已定）
1. parent agent-terminal 装 orchestration.Module ✅
2. 直改现有 orchestration.service ✅
3. mcp-orch 不起子进程，纯 RPC proxy ✅
4. 不破坏 archtest，不依赖 internal/provider/* ✅

## 核心设计：AgentLauncher 接口

```go
// internal/sidecar/orch/orchestration/contract.go
type AgentLauncher interface {
    Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error)
    Stop(ctx context.Context, agent *agentRuntime) error
    SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error)
    IsRunning(ctx context.Context, agent *agentRuntime) bool
}

// 接口以 *agentRuntime 为参数，不是 threadID。原因：
// - local 模式下 launch 后 threadID 为空（只有 turn claim 后才写回）
// - local 模式用 agent.cmd != nil 判断运行态
// - remote 模式用 agent.remoteThreadID 判断运行态
// - 由 launcher 实现自己决定看哪个字段

type LaunchResult struct {
    ThreadID      string
    RemoteAgentID string // parent 侧生成的 agentID（用于反查）
}
```

### parent 注入 localLauncher
- Launch(agent) → exec.Command（从现有 startProcessLocked 提取），操作 agent.cmd
- Stop(agent) → stopProcess(agent.cmd)
- SubmitTurn(agent) → 本地 turnStarter.StartTurn（现有逻辑）
- IsRunning(agent) → agent.cmd != nil
- **字段 cmd/launchSeq/monitoredSeq 留在 agentRuntime，localLauncher 通过 agent 指针操作**

### mcp-orch 注入 remoteLauncher
- Launch → RPC thread/start → 返回 threadID + remoteAgentID
- Stop → RPC thread/stop(thread_id)
- SubmitTurn → RPC turn/start(thread_id, prompt/input)
- IsRunning → 缓存状态（由 hooks 回流更新，或 Launch 成功即 true，Stop 后 false）

## 架构

```
parent agent-terminal                     mcp-orch
  orchestration.Module                     orchestration.Module
  + localLauncher                          + remoteLauncher
  │                                         │
  service.LaunchAgent                       service.LaunchAgent
  └→ localLauncher.Launch                   └→ remoteLauncher.Launch
     └→ exec.Command                           └→ RPC→parent thread/start
                                                   └→ thread.Start
                                                      └→ launchAgent→localLauncher ✅ 不递归
                                                      └→ startSession
                                                      └→ bindGeneration
```

## v6 审查 8 个通过条件的逐条落实

### 条件 1: localLauncher 只抽进程动作，字段留 agentRuntime
- `agent.cmd` / `launchSeq` / `monitoredSeq` 仍是 agentRuntime 的字段
- localLauncher 方法签名接收 `*agentRuntime`，直接操作这些字段
- 不做私有化迁移，现有 runner/recover/snapshot 代码改动最小

### 条件 2: SubmitTurn + submitAgentReadyState + claimTurnWork 全改运行态探针
- 抽象方法 `s.launcher.IsRunning(ctx, agent)` 替代 `agent.cmd != nil`
- 接口参数是 `*agentRuntime`，不是 `threadID`（local 下 threadID 为空）
- 影响 3 处：
  - `service.go:295-297` SubmitTurn
  - `helpers.go:218-233` submitAgentReadyState
  - `process_lifecycle.go:52-85` claimTurnWork
- local 模式：IsRunning(agent) = agent.cmd != nil
- remote 模式：IsRunning(agent) = agent.remoteThreadID != ""

### 条件 3: RunnerActor remote 模式不注册
- fx 注入时判断 mode：
  - local → `fx.Annotate(NewRunnerActor, group:"runners")`（现有逻辑）
  - remote → 不注册 RunnerActor（remote 不需要本地进程监控）
- remote 模式的 turn 提交直接走 remoteLauncher.SubmitTurn RPC

### 条件 4: RPC 有 deadline，不在 mu 持锁区间发网络请求
- remoteLauncher 的每个 RPC 调用包 `platformconfig.WithRPCRequestTimeout(ctx)`
- LaunchAgent 改成两阶段：
  1. 锁内：预留 agent 状态 + fencing（launchSeq++）
  2. 锁外：发 RPC
  3. 锁内：按 launchSeq 提交结果（防并发覆盖）
- StopAgent/SubmitTurn 同理

### 条件 5: jrpc2 client 有重连策略
- 方案：懒连接 + 每次请求前检查连接健康
  ```go
  type remoteLauncher struct {
      addr   string
      mu     sync.Mutex
      client *jrpc2.Client
  }
  func (r *remoteLauncher) ensureClient(ctx context.Context) (*jrpc2.Client, error) {
      r.mu.Lock()
      defer r.mu.Unlock()
      if r.client != nil && !r.client.IsStopped() {
          return r.client, nil
      }
      // dial + jrpc2.NewClient
  }
  ```
- fx OnStop 关闭连接
- 不复用 bootstrap.Client（它有 lease/heartbeat 语义，bridge 不需要）

### 条件 6: parent fx 冲突解决（唯一方案）
- **RuntimeReporter 冲突**：orchestration.Module 删除 RuntimeReporter provider（parent 已有，不需要重复提供）
- **taskdag.Store**：改为 `fx.In optional:"true"`
  - 有 store → DAG RPC 正常
  - 无 store → DAG RPC 返回 "dag store not configured"
  - parent 不需要 DAG 功能，optional 降级可接受

### 条件 7: identity 闭环
- LaunchResult 返回 `ThreadID + RemoteAgentID`
- service 存两个映射：
  - `agent.threadID = result.ThreadID`（用于后续 turn/start、thread/stop）
  - `agent.remoteAgentID = result.RemoteAgentID`（用于 remote 状态查询）
- thread/start RPC 返回的 `agentId` 字段就是 remoteAgentID

### 条件 8: LOC 上调
- 生产代码：~350 LOC
- 测试：~180 LOC

---

## 任务拆分

### 任务 0: 定义 AgentLauncher 接口 + LaunchResult
文件: `internal/sidecar/orch/orchestration/contract.go`
- AgentLauncher{Launch, Stop, SubmitTurn, IsRunning}
- LaunchResult{ThreadID, RemoteAgentID}
- service 构造函数加 AgentLauncher 参数
- ~15 LOC

### 任务 1: 提取 localLauncher
文件: `internal/sidecar/orch/orchestration/launcher_local.go`（新建）
- 从 startProcessLocked 提取 Launch 方法（操作 agentRuntime 字段）
- 从 stopProcess 提取 Stop 方法
- SubmitTurn 委托现有 turnStarter
- IsRunning = agent.cmd != nil
- **字段留在 agentRuntime，localLauncher 只操作不拥有**
- ~80 LOC

### 任务 2: 实现 remoteLauncher
文件: `internal/sidecar/orch/orchestration/launcher_remote.go`（新建）
- 懒连接 jrpc2.Client + ensureClient + IsStopped 检查
- Launch: RPC→thread/start → 解析返回 threadID + agentId
- Stop: RPC→thread/stop
- SubmitTurn: RPC→turn/start(thread_id, prompt/input)
- IsRunning: 本地状态缓存
- 每个 RPC 包 deadline
- fx OnStop 关闭
- ~80 LOC

### 任务 3: 改 service 核心路径
文件: `internal/sidecar/orch/orchestration/service.go` + `helpers.go` + `process_lifecycle.go`
- LaunchAgent: 两阶段（锁内预留 → 锁外发 launcher.Launch(agent) → 锁内提交）
- StopAgent: 委托 launcher.Stop(agent)
- SubmitTurn: agent.cmd!=nil → launcher.IsRunning(agent)
- submitAgentReadyState: 同上
- claimTurnWork: 同上
- **remote 模式下 SubmitTurn 绕过本地 queue：**
  - 如果 launcher 是 remote，直接调 launcher.SubmitTurn(agent, submission)
  - 不入本地 queue，不走 RunnerActor
  - turn 结果通过 RPC turn/start 的返回值获取
  - 本地状态机通过 launcher 返回值推进（成功→TurnStarting，失败→回退）
- ~80 LOC 改动

### 任务 4: parent 装 orchestration.Module
文件: `internal/app/modules.go` + `internal/sidecar/orch/orchestration/service.go`
- taskdag.Store 改 optional
- 解决 RuntimeReporter 冲突
- parent 注入 localLauncher
- 验证 thread/start→launchAgent→localLauncher 链路
- ~30 LOC

### 任务 5: mcp-orch fx 注入 remoteLauncher
文件: `cmd/mcp-orch/fx.go`
- GO_AGENT_CTL_RPC_ADDR 有值 → remoteLauncher + 不注册 RunnerActor
- 无值 → localLauncher + 注册 RunnerActor
- ~20 LOC

### 任务 6: identity + 状态回流 + remote 读面
文件: `internal/sidecar/orch/orchestration/service.go` + `runtime.go`
- agent.remoteThreadID 存 LaunchResult.ThreadID
- agent.remoteAgentID 存 LaunchResult.RemoteAgentID
- remote 模式：Launch 成功 → remoteThreadID 有值，Stop → 清空
- **remote 读面**：ListAgents/Snapshot/GetState/GetReport
  - 本地 agent map 仍作为 source-of-truth（存 agentID→状态映射）
  - remote 状态通过 launcher 返回值更新本地 mirror
  - 如需权威状态，GetState 可选调 parent agent.getState RPC
- ~35 LOC

### 任务 7: 测试
- TestLocalLauncher_LaunchStop
- TestRemoteLauncher_LaunchStop（mock RPC server）
- TestRemoteLauncher_SubmitTurn
- TestRemoteLauncher_ReconnectOnStopped（用 IsStopped 检测断连后重建）
- TestRemoteLauncher_RPCTimeout
- TestService_LaunchWithLocal
- TestService_LaunchWithRemote
- TestService_SubmitTurnRemoteMode（验证绕过本地 queue）
- TestService_SubmitTurnLocalMode（验证走本地 queue/RunnerActor）
- TestThreadAgents_CleanupOnStop（验证 thread/stop 后 threadAgents 映射清除）
- TestParentFxStartup（parent 装 orchestration.Module 后 fx.Start 正常）
- ~200 LOC

### 任务 8: E2E 验证
- parent 装 orchestration.Module 后 fx 启动正常
- launch agent → thread/start 成功
- send_message → turn/start 成功
- stop agent → thread/stop 成功
- 现有功能不受影响

---

## 不改的部分
- internal/module/thread/lifecycle.go — 不动
- internal/provider/* — 不触碰
- archtest — 不修改
- RunnerActor 本身 — local 模式完全保留

## 架构边界
```
cmd/mcp-orch → internal/contract ✅
cmd/mcp-orch → internal/module/* ✅
cmd/mcp-orch → jrpc2 ✅
cmd/mcp-orch ✘ internal/provider/*
```

## 预估
- 生产代码：~350 LOC（接口20 + local80 + remote80 + service80 + parent30 + fx20 + identity35）
- 测试：~200 LOC
- **总计：~550 LOC**
