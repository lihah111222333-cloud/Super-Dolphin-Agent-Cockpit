# 验证：thread 族 1:1 对齐修复（start/stop/resume/config）

审查时间：2026-03-21  
审查方式：只用 LSP；先读 `align-thread-start.md` / `align-thread-stop.md` / `align-thread-resume.md` / `align-thread-config.md`，再核对当前代码。  
口径：这里只复核用户点名的 4 项，不重审 4 份报告中的全部结论。

## 结论摘要

| 项目 | 当前裁定 | 结论 |
| --- | --- | --- |
| `thread/start` 参数面是否已扩展 | `✅已修` | 旧报告中缺失的 `modelProvider/baseInstructions/developerInstructions/sandbox/summary` 已回到 public params/contract，并继续透传到 provider config。 |
| `thread/stop` 级联清理是否已补 | `✅已修` | 现在已有裸 `thread/stop` 路由；停止前会 interrupt active turn，停止后会按 `thread/provider/codex/agent` 多个 ID 清理 turn tracker。 |
| `thread/resume` RPC 形状是否已对齐 V2 | `✅已修` | public request 已接受 `threadId/path/cwd/model`，response 已恢复成 `{"thread":{"id","status"},"model":...}`。 |
| `thread/config` 3 个高频命令是否有真实实现 | `⚠️部分修` | `thread/config/get`、`thread/model/set`、`thread/compact/start` 都已不是纯壳；但 `model/set`、`compact/start` 仍受 provider 能力约束，不能算完全收敛。 |

## 1. `thread/start` 参数面

裁定：`✅已修`

- `startParams` 现在公开了此前报告中缺失的字段：`modelProvider`、`baseInstructions`、`developerInstructions`、`sandbox`、`summary`，并保留 `approvalPolicy`、`effort`、`personality`：`internal/module/thread/rpc_types.go:22-35`
- 自定义反序列化继续兼容旧字段别名与旧入口：`modelProvider/model_provider`、`approvalPolicy/approval_policy`、`baseInstructions/base_instructions`、`developerInstructions/developer_instructions`、`instructions/prompt`：`internal/module/thread/rpc_types.go:37-83`
- RPC handler 已把这些字段全部映射进 `StartRequest`：`internal/module/thread/rpc.go:89-105`
- `StartRequest` 契约本身也已经扩展到相同字段集：`internal/module/thread/contract.go:33-47`
- provider 启动配置里已经实际透传 `approvalPolicy`、`modelProvider`、`developerInstructions`、`summary`、`effort`、`personality`、`sandbox`：`internal/module/thread/start_session.go:133-149`

备注：

- 这只能证明“参数面已扩展”这个问题已经修掉。
- `provider` 仍是 V3 额外字段，`BaseInstructions` 仍会折叠进 `Prompt/Instructions`：`internal/module/thread/start_session.go:17-35`，`internal/module/thread/start_session.go:38-49`
- 因此 `thread/start` 整体仍不等于 V2 1:1，但本次核对的问题本身已修。

## 2. `thread/stop` 级联清理

裁定：`✅已修`

- 当前已经存在裸 `thread/stop` 路由，不再只是旧报告里的 `thread/realtime/stop`：`internal/module/thread/rpc.go:21-23`
- `Stop(...)` 主链路会先解析 binding，再 `interruptStoppingThread(...)`，随后 `stopManagedAgent(...)`，最后 `cleanupThreadTurns(...)`：`internal/module/thread/stop.go:10-24`
- 停止前会尝试中断活跃 turn，并等待 settle：`internal/module/thread/stop.go:26-37`，`internal/module/turn/thread_cleanup.go:11-35`
- 停止后会对 `threadID/providerThreadID/codexThreadID/agentID` 做去重并调用 `turns.CleanupThread(...)`：`internal/module/thread/stop.go:50-85`
- `CleanupThread(...)` 会直接 `AbortThread(threadID, reason)`；tracker 层已有对应实现：`internal/module/turn/thread_cleanup.go:37-39`，`internal/module/turn/tracker.go:190-198`
- 走 orchestration 时，`StopAgent(...)` 还会 remove session 并发布 stopped event：`internal/sidecar/orch/orchestration/service.go:122-135`

备注：

- 这里验证的是“turn 级联清理是否已补”。
- store/history 删除闭环是不是也补齐，不在这条核对范围内。

## 3. `thread/resume` RPC 形状

裁定：`✅已修`

- `resumeParams` 当前已接收 `thread_id`、`path`、`cwd`、`model`，并兼容旧的 `threadId`；`provider` 只是额外可选字段：`internal/module/thread/rpc_types.go:86-101`
- handler 返回值已经恢复成 V2 形状：`{"thread":{"id","status"},"model":...}`：`internal/module/thread/rpc.go:175-191`
- service 返回结构也已显式持有 `ThreadID/Status/Model`：`internal/module/thread/contract.go:54-67`
- `Resume(...)` 仍会把 `provider/agentID` 从持久化状态里补全，但这是 service 内部恢复策略，不改变外层 RPC 形状：`internal/module/thread/start_session.go:80-99`，`internal/module/thread/lifecycle.go:81-120`
- codexapp driver 继续把 service 结果下沉到真实 remote `thread/resume`：`internal/provider/codexapp/driver.go:193-204`

备注：

- 从“RPC 形状”看，这一项已经对齐。
- 从“恢复语义/依赖持久化状态”看，V3 仍不等于 V2；那是另一类问题，不是本次问的形状问题。

## 4. `thread/config` 3 个高频命令

裁定：`⚠️部分修`

本条按当前 `rpc.go` 里已经拆出的 3 个高频入口核对：`thread/config/get`、`thread/model/set`、`thread/compact/start`。

- `thread/config/get`
  - 已不再是 TODO 壳；handler 直接调 `svc.GetConfig(...)`：`internal/module/thread/rpc.go:49`，`internal/module/thread/rpc.go:128-132`
  - service 会读 session config reader，并做标准化：`internal/module/thread/command.go:135-145`，`internal/module/thread/command.go:166-188`
  - codexapp 是真实 remote `thread/config/get`：`internal/provider/codexapp/session_history.go:64-76`
  - claudecli 也有真实 `ReadConfig(...)`，只是返回本地 session snapshot：`internal/provider/claudecli/session_config.go:69-84`

- `thread/model/set`
  - 已不再是 `SendCommand` 空壳；handler 先做参数规整，再进 `svc.SetModel(...)`：`internal/module/thread/rpc.go:52`，`internal/module/thread/rpc.go:145-173`
  - service 会校验 model、校验 allowed models、调用 `session.Configure(...)`，并回写 thread model：`internal/module/thread/command.go:147-164`，`internal/module/thread/command.go:190-206`，`internal/module/thread/command.go:225-236`
  - codexapp 的 `Configure(Model)` 会真实下沉到 remote `thread/config/set`：`internal/provider/codexapp/session.go:207-223`
  - 但 claudecli active session `Configure(...)` 仍明确返回 unsupported/capability error：`internal/provider/claudecli/session_config.go:15-29`

- `thread/compact/start`
  - 已不再是 report 里的占位结果；handler 直接调 `svc.Compact(...)`：`internal/module/thread/rpc.go:57`，`internal/module/thread/rpc.go:155-158`
  - service 会解析 `compactSession`、估算前后 token、调用 provider `CompactThread(...)`：`internal/module/thread/history.go:100-129`
  - codexapp 已有真实 remote `thread/compact/start`：`internal/provider/codexapp/session_history.go:78-90`
  - 但它仍取决于 active provider 是否实现 `compactSession`；当前不是所有 provider 都支持：`internal/module/thread/history.go:105-115`

附注：

- 如果把 runtime config setter 也算进“高频命令”，`thread/personality/set` / `thread/approvals/set` 现在也不再是纯 TODO 壳，而是落到 `session.Configure(...)`：`internal/module/thread/command.go:27-45`，`internal/module/thread/command.go:91-109`
- 但这两项同样受 provider 能力约束；codexapp 会真实下沉到 remote `thread/personality/set` / `thread/approvals/set`，claudecli active session 仍不支持：`internal/provider/codexapp/session.go:220-249`，`internal/provider/claudecli/session_config.go:15-29`

## 最终结论

- `thread/start` 参数面扩展：`✅已修`
- `thread/stop` 级联清理：`✅已修`
- `thread/resume` RPC 形状：`✅已修`
- `thread/config` 3 个高频命令真实实现：`⚠️部分修`
