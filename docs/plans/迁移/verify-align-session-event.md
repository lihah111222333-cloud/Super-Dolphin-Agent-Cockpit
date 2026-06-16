# 验证：session lifecycle + event flow 对齐修复

## 范围
- 对照 `docs/plans/迁移/align-session-lifecycle.md` 与 `docs/plans/迁移/align-event-flow.md` 复核当前代码；代码读取仅使用 LSP。
- 备注：`align-event-flow.md` 里“RPC/Wails 只推 3 个 method”的旧结论已过时。当前 `internal/platform/rpc/push.go:67-74` 与 `internal/ui/wails/bridge.go:33-44` 都改为复用 `internal/platform/eventsurface/bind.go:33-41`。

## 逐项结论

### 1. SessionManager 代际保护是否已修
- 结论：✅
- 证据：`internal/provider/unified/session.go:37-55` 在 `Register` 返回 generation；`internal/provider/unified/session.go:78-90` 的 `Remove` 要求传入 generation；`internal/provider/unified/session.go:139-152` 的 `removeEntry` 只有 generation 匹配才删除；`internal/module/thread/session_generation.go:13-25` 会把当前 generation 绑定进 orchestration；`internal/sidecar/orch/orchestration/session_generation.go:13-40` 在 stop/exit 清理时优先走 `RemoveSessionGeneration(agentID, generation)`；`internal/provider/unified/session_generation_test.go:39-58` 覆盖了“旧 generation 不能删掉新 session”。
- 说明：就 `SessionManager` 并发 create/remove 的代际问题看，当前代码已修。

### 2. `codexapp` `threadID` data race 是否已修
- 结论：✅
- 证据：`internal/provider/codexapp/session.go:19-35` 把 `threadID` 改成 `atomic.Value`；`internal/provider/codexapp/thread_id.go:5-22` 统一通过 `Load`/`Store` 读写；`internal/provider/codexapp/driver.go:79-93` 与 `internal/provider/codexapp/driver.go:97-111` 在 start/resume 成功后只经 `setThreadID(...)` 写入。
- 说明：当前实现里已经看不到旧的无锁读写 `threadID` 路径。

### 3. `Remove` 是否包含 `Close` + `context/timeout`
- 结论：✅
- 证据：`internal/provider/unified/session.go:78-90` 的 `Remove` 在删 map 后会调用 `closeRemovedSession(...)`；`internal/provider/unified/session.go:154-163` 在关闭前包了一层 `platformconfig.WithSessionCloseTimeout(context.Background())`；`internal/platform/config/timeouts.go:13-27` 定义 `SessionCloseTimeout = 5 * time.Second` 与 helper；`internal/provider/unified/session.go:173-187` 的 `closeSession` 会等待 `session.Close(ctx)` 或 `ctx.Done()`；`internal/provider/unified/session.go:157-160` 在失败时回退 `ForceStop()`。
- 说明：`Remove` 这一条路径已经具备 `Close + timeout + ForceStop fallback`。但线程归档/删除仍未统一收口到 `Remove`：`internal/module/thread/archive.go:5-16`、`internal/module/thread/service.go:123-143`、`internal/module/thread/service.go:253-266` 仍是直接 `session.Close(ctx)`。

### 4. `CloseAll` 是否挂到 fx `OnStop`
- 结论：✅
- 证据：`internal/provider/unified/module.go:19-31` 注册了 `registerSessionShutdown`；`internal/provider/unified/module.go:33-42` 在 fx lifecycle `OnStop` 中调用 `sessions.CloseAll(ctx)`；`internal/provider/unified/session.go:108-125` 实现了 `CloseAll(ctx)`。
- 说明：provider shutdown 已经接到 fx 停机生命周期。

### 5. event 推送面是否已从 3 个扩展；method name 是否 V2 兼容
- 结论：⚠️
- 证据：`internal/platform/rpc/push.go:67-74` 不再手写 3 个订阅，而是调用 `eventsurface.Bind(...)`；`internal/platform/eventsurface/bind.go:18-29` 当前定义了 11 个对外 method：`ui/state/changed`、`turn/started`、`turn/completed`、`thread/started`、`thread/stopped`、`thread/messages/page`、`workspace/run/created`、`workspace/run/merged`、`workspace/run/aborted`、`agent/launched`、`agent/stopped`；`internal/platform/eventsurface/bind.go:44-95` 把 core/thread/workspace/agent 四组 typed event 都接到对外推送面；`internal/platform/eventsurface/bind_test.go:20-77` 也显式校验了 expanded surface。线程/工作区/agent 发布端当前都存在：`internal/module/thread/service.go:41-43`、`internal/module/thread/service.go:76-78`、`internal/module/thread/service.go:320-358`；`internal/module/workspace/service.go:42-58`、`internal/module/workspace/service_helpers.go:260-289`；`internal/sidecar/orch/orchestration/events.go:25-43`。
- 说明：从“只推 3 个”这个问题看，当前代码已经扩面，子结论可判 `✅`。但 V2 method name 兼容仍只能判部分对齐：保留下来的兼容名主要是 `ui/state/changed`、`turn/started`、`turn/completed`、`thread/started`（见 `internal/platform/eventsurface/bind.go:18-23`）；按当前 method 常量表推断，仍未见 V2 的 `thread/tokenUsage/updated`、`thread/compacted`、`turn/diff/updated`、`turn/plan/updated`、`item/*` 等更宽方法族，所以整体仍是 `⚠️`，不是 1:1。

### 6. Wails bridge 是否同步扩展
- 结论：✅
- 证据：`internal/ui/wails/bridge.go:33-44` 与 `internal/platform/rpc/push.go:67-74` 共享同一个 `eventsurface.Bind(...)`；`internal/ui/wails/bridge.go:61-68` 仍把事件封装成 `{type, payload}`；`internal/ui/wails/lifecycle.go:11-15` 保持频道名 `bridge-event`；`internal/ui/wails/module.go:120-133` 在应用生命周期内启动/停止 bridge。
- 说明：后端 Wails bridge 已经和 RPC push 同步扩面。补充说明：当前仍只看到单路 `bridge-event`，没有看到 V2 风格 `agent-event` 双通道恢复。

## 收口
- session lifecycle 侧，本次核对的 4 项里：`SessionManager` 代际保护、`codexapp` `threadID` data race、`Remove` timeout close、`CloseAll` fx `OnStop` 都已修。
- event flow 侧，旧的“只推 3 个 event”结论已经不再成立；当前已经扩到共享 `eventsurface`。但 method name 与 V2 仍不是 1:1 全兼容，因此事件面对齐最多只能判到 `⚠️`。
