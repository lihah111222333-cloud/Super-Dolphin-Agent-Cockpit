# P2: bus/runtime 解耦

## 目标

把 `P2` 明确成 bus/runtime ownership 的 umbrella plan，而不是单张 mega PR。主线是把 callback 链上的 runtime owner 拆出去：`BusModule` 只接线，显式 owner / worker / `RunnerModule` 才持有 watcher / scheduler / timer / queue / drain。`memory/team + auto-dream + memory hooks` 这一主切片默认同时包含 `NestedRuntime` tool-read ingest（Finding 10）；每个执行切片在进入实现前都必须先写出自己的 overflow freeze 表，而不是边写 owner 边临时决定 drop/backpressure 语义。

## 对应 findings

- Finding 5: `internal/module/memory/module.go:456-467`
- Finding 6: `internal/module/memory/team/team_sync_watcher.go:72-79`
- Finding 7: `internal/module/memory/auto_dream_task.go:156-178`
- Finding 9: `internal/platform/toolbridge/module.go:130-159`
- Finding 10: `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339`
- 补充纳入：`internal/module/thread` 中的事件回调 runtime ownership、post-construction mutation、以及 `backgroundResumeIfNeeded(...)` 一类 service-owned background resume
- 补充纳入：`internal/platform/hooks/event_relay.go` 中的 bus callback 直 fanout / `DispatchAfter` / shutdown 不 drain
- 补充纳入：`internal/platform/toolbridge/module.go:54-60` 的 late setter wiring；其依赖方向问题另行注明，不在本页误写成纯 runtime 问题
- 补充纳入：`internal/platform/mcpcontrol/config_change.go` 中的 bus callback 直 fanout config notify，以及 `internal/platform/cachekeepalive/*` 中 bus 回调直持 keepalive timer/session runtime
- 补充纳入：`internal/platform/rpc/push.go` / `eventsurface` 中 bus callback 直做 RPC notify/network I/O

## 现状校准

### TeamSync

- `internal/module/memory/module.go:456-467` 的 `registerTeamSyncSubscriptions(...)` 在 `thread.started/stopped` 回调里直接 `StartSessionFromThreadEvent` / `StopSessionFromThreadEvent`
- `internal/module/memory/team/thread_metadata.go:63-84` 的 event helper 仍以 `context.Background()` + store 查询 + `svc.StartSession/StopSession(...)` 直达慢路径
- `TeamSyncService.StartSession(...)` 会创建/替换 watcher，并最终由 `internal/module/memory/team/team_sync_watcher.go:72-79` 的 `teamSyncWatcher.Start()` 用 `SafeGo` 拉起主循环
- `StartSession(...)` 现有顺序是 `resolveRuntime -> initial pull -> refreshLocalChecksum -> create/start watcher`
- `StopSession(last)` 与 `Shutdown` 当前都等价于 `watcher.Close(ctx, true)`：等待 loop 退出并做最终 flush
- `TeamSyncService` 仍公开 `Start/Stop/Pull/Push/Shutdown` 这组 slow-path API，允许普通调用方绕过 queue/runner 直接持有 lifecycle/remote I/O
- `TeamSyncService.Pull(...)` / `Push(...)` 当前仍只有 `_test.go` caller；若 `TeamSyncCoordinator` 落地后它们不进入真实 owner 链，就必须降为 internal-only 或直接删除，不能继续以公开 API 伪装成“已接线”

这等于 bus 回调直接拥有了 watcher/session runtime。

### Auto-dream

- `internal/module/memory/auto_dream_task.go:156-163` 的 `registerAutoDreamSubscriptions(...)` 回调里直接调用 `onThreadStopped(...)`
- `internal/module/memory/auto_dream_task.go:165-178` 的 `onThreadStopped(...)` 里再 `go` 调度 `maybeScheduleAutoDream(...)`
- `internal/module/memory/auto_dream_task.go:285-299` 的 `launchAutoDreamTask(...)` 再启动 consolidation 后台任务
- auto-dream 现有语义还带 gating、双节流和单飞：不是“收到 stopped 就一定跑”

这让订阅回调链直接承担了 scheduler/worker 的 ownership。

### Memory Hooks / Extraction

- `TurnInputReceived` / `TurnCompleted` 订阅目前仍直接进入 `MemoryLifecycleHooks` 慢路径，实际会做 memory write、transcript/manifest 读取，并启动 background extraction goroutine
- `registerMemoryHooks.OnStop` 目前只 `killDreamTask()` + 退订，不等待 `waitDreamTask()`，也不 drain background extraction
- `memory.team` 的 `OnStop` 仍直接绑到 `TeamSyncService.Shutdown(ctx)`，而不是显式 owner/coordinator
- `waitDreamTask()` 当前仍只有 `_test.go` caller；若 `MemoryHookWorker` 负责 stop/drain，就必须把它接进生产 shutdown path，否则直接删除
- `internal/module/memory/retrieval_bridge.go:44` 的 `memory.NewRelevantMemoryFinder(...)` 目前也只有 `_test.go` caller；bridge ctor 不能继续以“兼容壳”形式留在文档里冒充 live owner

这意味着 memory 自己除了 TeamSync/auto-dream 外，还存在 hook-owned write/extraction worker 和 stop 不 drain 的残留问题。

### Thread

- `thread` 目前仍通过 `fx.Invoke(registerSubscriptions)` 做 `bindDispatcher` / `bindPromptStore` 后置注入
- `onAgentLaunched` / `onAgentFailed` / `onTurnCompleted` 等回调里还同步做 binding 更新、prompt invalidation、session recovery、task handoff refresh
- `backgroundResumeIfNeeded(...)` 以 `context.Background()` 裸起 `Resume` goroutine，且不只被事件路径复用

这意味着 `thread` 也存在典型的 bus/runtime 混层：回调链直接持有恢复、写库、共享文件、history 读取等慢路径 ownership。

### Hooks Relay

- `internal/platform/hooks/event_relay.go:39-83` 目前在 bus callback 路径里直接进入 `manager.DispatchAfter(...)`，并可能走 peer callback fanout 与 `resolver.Escalate(...)`
- `internal/platform/hooks/event_relay.go:94-112` 的 `dispatchObservedAfter(...)` 还存在 fire-and-forget `go`，而 `module.go` 的 `OnStop` 只退订不 drain
- hooks owner 还承担 pending review 的 startup recovery：当前模块启动时会主动 `RecoverOnStartup(...)`
- hooks 当前还有 lost-subscriber cleanup 规则：连续失败达到阈值后会 `Unsubscribe`、清除 failure tracking，并按 lease 清理 pending review

这意味着 `hooks` 也不是轻量 relay，而是 callback 直持 fanout / 持久化 / shutdown race 的 runtime ownership。

### Toolbridge Proxy

- `internal/platform/toolbridge/module.go:54-60` 仍有 `fx.Invoke` 下的 late setter 注入，`internal/platform/toolbridge/module.go:130-159` 还在模块级 `OnStart` 里直接 `go ServeProxy(...)`
- 同时它还夹带更根的依赖方向问题：`platform/*` 直接依赖 `provider/*` 与 `store/*`

这意味着 `toolbridge` 至少有两层整改：runtime owner 收口纳入 P22，本包落点/依赖方向是否迁移另行明确。

### Config Fanout

- `internal/platform/mcpcontrol/config_change.go:33-82` 当前在 bus 回调里直接执行 `publishConfigChanged(...)`
- 这条链路会做 JSON 编码、推进 config version，并通过 `NotifyConfigChanged -> NotifyBySelector` 直接 fanout 到 peer RPC
- 当前 fanout 还把通知上下文硬编码成 `context.Background()`，因此已经完全脱离 publish/shutdown cancel
- `advanceConfigVersion()` 还会同步推进活跃实例的 `ConfigVersion`，迁移时需要冻结“版本推进与 fanout 的先后顺序、notify 失败是否仍递增版本”

这意味着它不是“轻量 subscriber wiring”，而是 bus callback 直持 fanout/编码/通知 slow path。

### Cache Keepalive

- `internal/platform/cachekeepalive/relay.go:22-38` 当前在 launch/idle/turn/stop 事件回调里直接查询 binding/thread store，并直接 reset/stop keepalive timer
- 也就是说 keepalive 的 timer/session runtime 仍由 bus 回调链直接持有
- 现存 slow-path 不止事件回调：`internal/platform/cachekeepalive/manager.go:275-277` 的 `time.AfterFunc(...)` timer callback 也会直接做 `bindingStore.GetByAgentID`、`ResolveSession`、`SendKeepalive`
- 当前 timer callback 同样运行在 `context.Background()` 上，已触发的 ping 不受 shutdown cancel 约束

这意味着 `cachekeepalive` 也属于典型的 callback slow-path + runtime ownership 混层。

### RPC Push

- `internal/platform/rpc/push.go` 当前在 bus 回调里直接 `broadcastNotifications(...)`
- 该链路同步走 `NotifyAll/NotifyClient` 做 RPC client 推送，属于 callback 直做 transport I/O
- typed event push 与 raw provider push 两条路径都用 `context.Background()` 做广播，现状问题不仅是 slow-path，还包括上下文已与 stop/drain 脱钩

### Eventsurface Legacy Expansion

- `eventsurface/legacy.go` 会把单个事件扩成多条 legacy refresh，例如 `ui/thread/changed`、`ui/sidebar/changed`
- `workspace/run/*` 这类事件还会无条件追加 sidebar refresh

这意味着 push queue 的容量、coalesce/dedupe 和验收都必须把 legacy 派生通知倍增器算进去。

这意味着 `rpc push` 也应从 callback 路径移出，改成显式 queue/worker/owner。

## 目标架构

推荐把 memory 的慢路径拆成**两条主 owner 线 + 一个配套 hook owner**：

- `TeamSyncCoordinator`
- `AutoDreamScheduler`
- `MemoryHookWorker`（负责显式写盘 / extraction / auto-dream 退出等待等 hook-owned drain，不与前两者争抢 session/watcher owner）

同时把 `thread` 的慢路径收口为显式 owner，例如：

- `ThreadEventCoordinator`
- `ThreadResumeWorker`
- `HookRelayWorker`
- `ToolbridgeProxyOwner`
- `ConfigFanoutWorker`
- `KeepaliveCoordinator`
- `PushNotificationWorker`

bus 回调只负责 enqueue command，不再直接启动 watcher/任务。

## 安全 / SSRF / payload 递延硬规则（P21 只加不删）

- authoritative 指针仍是 `P21/P2_MultiPlatformNotifications.md`；本页只做 runtime ownership 收口时，**不得**删掉那边已冻结的 webhook 安全 contract。
- 本页虽聚焦 runtime ownership，但通知 / webhook 的 `SSRF` 正文继续保留：默认拒绝 loopback、link-local、ULA、multicast 与 private CIDR；必须做 `DNS rebinding` 防御，且 redirect 每跳重新校验；禁环境代理与隐式代理透传。
- `secret` / 签名 / webhook URL 一律做 redact；钉钉 `sign` query、飞书签名 body、Slack webhook URL 都不能进日志、指标标签或错误正文。
- 内容洁化继续保留 `Markdown escape`、`markdownEscape`、mention 抑制、长度截断与“禁发 raw provider payload / transcript / tool result”规则；用户输入不得借模板逃逸为 `@all` / `@channel` / `@here` / `<@U123>` / 飞书 mention / 钉钉 `@手机号`。
- 三平台 payload 示例继续作为 smoke baseline：
  - 钉钉 Markdown：`{"msgtype":"markdown","markdown":{"title":"{{.Title | markdownEscape}}","text":"{{.Body | markdownEscape}}"}}`
  - 飞书 Rich Text：`{"msg_type":"interactive","card":{"elements":[{"tag":"markdown","content":"{{.Body | markdownEscape}}"}]}}`
  - Slack Block：`{"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"{{.Body | markdownEscape}}"}}]}`

### TeamSync 目标流向

```text
thread.started/stopped event
  -> bus callback
     -> non-blocking enqueue TeamSyncCommand
        -> TeamSyncCoordinator / Runner
           -> StartSession / StopSession
              -> watcher lifecycle
```

`TeamSyncCoordinator` 必须保留“同一 `(root, repoSlug)` 只跑一个 watcher”的共享模型：多 thread 只增减 session，只有最后一个 session 退出才真正 close watcher。

### Auto-dream 目标流向

```text
thread.stopped event
  -> bus callback
     -> enqueue AutoDreamJob
        -> AutoDreamScheduler / Runner
           -> eligibility check
           -> launch consolidation
```

`AutoDreamScheduler` 必须显式冻结 busy 行为：当前语义是“同一时刻只允许一个 dream task；busy 时新触发直接放弃”，不是排队补跑。

### Thread 目标流向

```text
agent/thread/turn event
  -> bus callback
     -> non-blocking enqueue ThreadCommand
        -> ThreadEventCoordinator / Worker
           -> binding update / prompt invalidation / task-handoff refresh
           -> session recovery / resume
```

`ThreadEventCoordinator` 只负责串行消费命令，不允许 bus 回调再直接做 store I/O、shared file 写入或 delayed resume。

### Hooks 目标流向

```text
thread/turn/item/agent event
  -> bus callback
     -> non-blocking enqueue HookCommand
        -> HookRelayWorker / Runner
           -> dispatch / callback fanout / escalate / drain
```

`HookRelayWorker` 必须拥有显式 shutdown drain 语义；不允许在 callback 路径里再 fire-and-forget `go DispatchAfter(...)`。

除了 runtime owner，本页还要冻结 hooks 的行为契约：

- startup recovery 仍由 owner 接管，不能在迁移时丢掉 pending review 恢复
- lost-subscriber cleanup 规则仍保留，且 cleanup 动作包含 unsubscribe、failure tracking 清理和 pending review cancel-by-lease
- hook merge/fail-closed 语义保持：`before` partial failure -> deny；`after` partial failure -> 保留成功决策；`allowedTools` 取交集；`deniedTools` 取并集；decision 优先级顺序不漂移
- hook recursion guard 保持：dispatch 深度递增，默认最大深度 3，超限直接回默认决策

### Toolbridge 目标流向

```text
fx wiring
  -> explicit constructor params / groups
proxy start
  -> ToolbridgeProxyOwner / Runner
     -> serve / stop / drain
```

`ToolbridgeProxyOwner` 只解决 proxy serve 生命周期；包落点/依赖方向越界需要在计划中单独标注为“运行时外的附带整改”。

### Config Fanout 目标流向

```text
agent/thread/runtime event
  -> bus callback
     -> non-blocking enqueue ConfigChangeCommand
        -> ConfigFanoutWorker
           -> version advance / encode / notify / drain
```

`ConfigFanoutWorker` 必须把 fanout 和编码从 publish path 上挪走，并具备 shutdown drain。

### Cache Keepalive 目标流向

```text
agent/thread/turn event
  -> bus callback
     -> non-blocking enqueue KeepaliveCommand
        -> KeepaliveCoordinator
           -> binding lookup / timer reset / timer stop / keepalive session ownership
```

`KeepaliveCoordinator` 必须统一接管 timer 生命周期；bus 回调不再直接 reset/stop 计时器或触发 store 读路径。

`KeepaliveCoordinator` 还必须定义并实现“已触发 keepalive ping 的 stop/drain 语义”；`Timer.Stop()` 本身不等于 drain。

### RPC Push 目标流向

```text
provider/thread/tool/ui event
  -> bus callback
     -> non-blocking enqueue PushNotificationCommand
        -> PushNotificationWorker
           -> notify / fanout / drain
```

`PushNotificationWorker` 必须把 `NotifyAll/NotifyClient` 这类 transport I/O 从 publish path 上挪走，并具备 shutdown drain。

同时要把 `ExpandNotifications()` 的 legacy 派生通知纳入 queue/coalesce/dedupe 设计，而不是按单条原始事件估算容量。

### Memory Hook 目标流向

```text
turn input/completed event
  -> bus callback
     -> non-blocking enqueue MemoryHookCommand
        -> MemoryHookWorker
           -> explicit memory write / transcript+manifest read / extraction / drain
```

`MemoryHookWorker` 必须把显式写盘、background extraction、auto-dream 退出等待统一收口，并在 shutdown 前完成 drain。

## 收口口径

- 本页关于 callback/owner/drain 的分类，以 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4` 为准：`BusModule` 只做 wiring / subscriber registration，长跑 loop 进 `RunnerModule`，本地短生命周期并发也必须落到显式 owner 的 `Start/Stop/Drain`。
- `P2` 只收 runtime ownership；`toolbridge` 的 dependency direction / protocol contract 留给 `P4`，`gopls/bootstrap` 的 compatibility / hidden contract 也只在 `P4` 继续收口。
- bus callback 只允许轻量状态更新或 non-blocking enqueue。退订只代表 stop intake，不等于 drain；drain 由 owner / worker / runner 负责。
- overflow policy 必须分档写明：TeamSync / config version / hook escalate 这类不能 silent drop；对这类 inlet，不再停留在抽象口号或模糊备选框，而是要在切片文档里明确冻结成 `lossless pending-set / wake-signal`、`bounded queue + fail-close/backpressure`、或 `desired-state persistence + retry/replay` 之一，不能把队列满当成隐式丢弃。auto-dream / keepalive reset / rpc push 可以 coalesce 或丢弃，但也必须显式文档化。
- 若某子域暂时不直接进 `run.Group`，也必须有单一 owner 的 `Start/Stop/Drain` contract；不接受 callback 里直跑、service 里再裸 `go` 的折中方案。
- `memory/team`、`thread`、`hooks`、`config fanout`、`cachekeepalive`、`rpc push`、`toolbridge runtime`、`gopls/bootstrap runtime` 都按这一口径处理，不再把“只是 relay / just notify / util callback”当豁免理由。
- callback 是否违规按完整触发链判定，而不是只看闭包本体有没有字面 `go`：`callback -> helper -> goroutine/manager/store/notify` 仍算 callback slow-path，没有因为绕一层 helper 就豁免。
- `bootstrap` 的 stdio EOF / peer 生命周期 owner 需要结合上层 peer runtime 接入点统一裁决；`P2` 在本页只先冻结 common/bootstrap 这一侧的 async owner、desired-state 与 drain 语义，不把“谁拥有进程寿命”误写成单包内已定事实。

## 实施方式

- `P2` 按共享写集拆成多批实施，不再假定“一份文档 = 一次代码合入”。
- 建议的执行切片：
  1. `memory/team + auto-dream + memory hooks + NestedRuntime tool-read ingest`
  2. `thread event / resume / task-handoff + cachekeepalive(session users)`
  3. `hooks + config fanout + rpc push + eventsurface`
  4. `toolbridge runtime owner`
  5. `cmd/mcp-lsp/gopls + internal/mcpserver/common/bootstrap` sidecar runtime
- 同一切片内部可以共享 owner 模板；不同切片之间只在 contract 冻结后串行合入，避免 `memory/thread/toolbridge` 这类高耦合包并行写冲突。
- `cachekeepalive` 归属 thread/session-user lane，不再和 platform event lane 混派；`gopls` 与 `bootstrap` 也始终视为同一 sidecar lane，只能按“分组 ready”推进，不拆成两个互不感知的独立 merge 单元。
- `P2` 的目标是把 callback slow-path 迁出 publish path，并把 stop/drain 语义收敛到单一 owner；不是把所有子域都强行改写成同一种 queue 实现。
- 任何 live wiring scope 都不接受“旧 subscriber wiring 保留 + 只接新 runner”双轨过渡；新 owner 接线与旧回调直达路径必须成对删除或显式 gate。
- 每个执行切片在开工前都要先冻结自己的 overflow 机制：哪些 command 可 drop/coalesce，哪些 command 必须 backpressure 或 durable replay；没有这张表就不进入实现。

### 执行切片 overflow / owner freeze 表（进入实现前最少冻结到此粒度）

| slice | non-silent-drop inlet 与冻结机制 | 允许 coalesce / drop 的 inlet | 进入实现前必须写死的 owner 事实 |
|---|---|---|---|
| `memory/team + auto-dream + memory hooks + NestedRuntime` | TeamSync session lifecycle、memory hook write/extraction、NestedRuntime tool-read ingest 一律走 `lossless pending-set / wake-signal`，不接受 silent drop | auto-dream trigger 允许按现状 busy-drop / cancel-coalesce | `TeamSyncCoordinator + AutoDreamScheduler + MemoryHookWorker` 三者同时落位 |
| `thread + cachekeepalive(session users)` | session recovery / resume / task-handoff refresh、已触发 keepalive ping 的 drain 一律走 `lossless pending-set / wake-signal` | keepalive reset / idle refresh 可 coalesce | 复用 `P1c` 的 session stop/drain contract，不再发明第二套 session owner |
| `hooks + config fanout + rpc push + eventsurface` | hook escalate、config version advance、in-flight dispatch drain 统一走 `bounded queue + fail-close/backpressure`，不做 durable replay 幻觉 | rpc push / legacy refresh 可 queue+dedupe / coalesce | callback 只 enqueue；single owner 负责 notify / fanout / drain |
| `toolbridge runtime owner` | proxy start / stop / peer lifecycle / serve-drain 统一走 `bounded queue + explicit backpressure` | 无 silent-drop 类入口 | 必须在 `P1a + P1c` 冻结 peer/session contract 后进入实现 |
| `gopls + bootstrap sidecar runtime` | bootstrap required-topic subscribe / desired-state / final report 统一走 `desired-state persistence + retry/replay` | cache recycle / responder 可 batch，但不能留下 orphan goroutine | 两者固定同一 sidecar lane；不得拆成两个互不感知 merge 单元 |

- sidecar 第 5 切片在补齐自己的测试名 / 验证命令前，只算“owner / rollback 口径已冻结”，不算 implementation-ready；本页当前不允许把空的 sidecar red-green 入口当成可签收。

## 依赖图（文本）

```text
P0 -> P2(memory/team/auto-dream/memory hooks/NestedRuntime)
P0 + P1c -> P2(thread / cachekeepalive / session users)
P0 + P1b -> P2(hooks / config fanout / rpc push / eventsurface)
P0 + P1a + P1c -> P2(toolbridge runtime) -> P4(toolbridge dependency / protocol contract)
P0 -> P2(gopls/bootstrap runtime) -> P4(gopls/bootstrap compatibility contract)
```

## 落地顺序建议

1. 先做 `memory/team + auto-dream + memory hooks + NestedRuntime tool-read ingest`：这是 README Finding 5/6/7/10 的主战场，也是 callback slow-path 最集中的模板段。
2. `thread` 与 `cachekeepalive` 建议在 `P1c` 把 session stop/drain contract 冻结后再接入，避免反向发明第二套 session owner。
3. `hooks + config fanout + rpc push / eventsurface` 作为平台事件切片推进；先承接 `P1b` 已冻结的 loop/restore wiring，再统一落“callback 只 enqueue、owner 负责 drain”的模板。
4. `toolbridge runtime owner` 需要等 `P1a/P1c` 冻结 codexapp peer/session contract 后再推进；与 `P4` 的依赖方向整改串行合入，不在同一批同时改 runtime 和 protocol shell。
5. `gopls/bootstrap` 作为 sidecar runtime 子批后置；先把 constructor-owned loop / async callback owner 收口，再把 compatibility / hidden contract 交给 `P4`；其中 bootstrap 的 stdio EOF owner 要与上层 peer runtime 一并复核，且这两块只按同一 sidecar lane 派工，不再按两个独立 scope 机械平推。

## 实施步骤

### Step 1：bus 回调改 enqueue

`registerTeamSyncSubscriptions` / `registerAutoDreamSubscriptions` 的回调只允许做：

- 构造 command/job
- 写入 bounded channel，或写入 lossless pending-set / wake-signal inlet
- 满载时按该切片预先冻结的 overflow policy 处理；不能默认静默丢弃

### Step 2：显式 worker owner

新增一个或两个 owner：

- 若 TeamSync 与 Auto-dream 都实现为 `Runner`，则直接加入 `group:"runners"`
- 若其中一个更适合 service-owned queue worker，也必须有显式 `Start/Stop/Drain`，不能再从回调里 `go`

补充要求：

- TeamSync 的 `Start/Stop/Pull/Push/Shutdown` 必须走同一个串行 owner/临界区；P2 不能只写 enqueue，还要写清 command ordering 和互斥边界
- Auto-dream 的 scheduler 必须拥有显式 `cancel/drain/status` 语义，替掉回调链里的裸 `go`
- Memory hooks 的 explicit memory write / transcript read / manifest read / extraction 也要走单一 owner；bus callback 不再直接做写盘或启动 extraction goroutine
- NestedRuntime 的 tool-read ingest 也要走单一 owner；`ToolCallEnd` callback 不再直达 `AddToolReadResult(...)` 再触发同步 `os.ReadFile(...)`
- Thread 的 binding 更新、task-handoff refresh、session recovery、background resume 也要走同一个显式 owner/worker 模型；不能只把事件回调改成 enqueue，却保留 service 方法里的裸 `Resume` goroutine
- Hooks 的 relay/fanout/escalate 也要走单一 owner；模块级 `OnStop` 不能只退订，但 in-flight dispatch 的 drain 也不能再写成 `OnStop` 自己承担。正确口径是：由该 owner 的显式 `Stop/Drain` contract 完成有界收口与 join，避免跨 shutdown 泄漏 after-hook 副作用
- Hooks owner 迁移后还必须保住 startup recovery、lost-subscriber cleanup、merge/fail-closed 和 recursion guard 这四类隐藏契约
- Toolbridge 的 proxy serve 也要走单一 owner；`fx.Invoke` setter 注入必须改成 constructor 参数或 group wiring，不能留 late mutation
- Config change fanout 也要走单一 owner；不能继续在 bus 回调里直接推进 version、编码 payload、通知 peers
- Cache keepalive 的 binding/thread store 查询和 timer reset/stop 也要走单一 owner；bus 回调不再直接持有 timer runtime
- RPC push 的 notify/fanout 也要走单一 owner；bus 回调不再直接做 `NotifyAll/NotifyClient`
- Config fanout / keepalive / rpc push 迁移后都必须恢复可取消、可 drain 的上下文边界，不再默认为 `context.Background()`

### Step 3：watcher ownership 下沉

`teamSyncWatcher.Start()` 不再由 bus 路径间接触发；它应只被 `TeamSyncCoordinator` 调用。

runtime 切换时，必须先把旧 watcher `detach + close + final flush` 完成，再覆写 `root/repoSlug/state/stateStore` 并安装新 watcher；否则旧 watcher 的最终 flush 会打到新 runtime。

同时要收紧 API 面：

- `thread_metadata.go` 这类 event helper 不能继续保留 `context.Background()` + store lookup + `StartSession/StopSession` 直达慢路径
- `TeamSyncService.Start/Stop/Pull/Push/Shutdown` 若继续存在，必须降为 owner 内部原语或明确标注为非 event-facing/internal-only；不能继续作为任意调用方可直达的公开 lifecycle 入口

### Step 4：Auto-dream 调度合流

把 `onThreadStopped -> go maybeScheduleAutoDream -> launchAutoDreamTask` 改为：

- 回调只 enqueue threadID
- scheduler 负责节流、去重、单飞、启动和 stop/drain

还需保留现有语义：

- eligibility gate：只接受 auto-memory root thread，且不能带 agent memory scope；`ResolveMemoryGate(...)` 必须 `AutoEnabled=true` 且 `KairosActive=false`
- scan throttle：同一 store root 10 分钟内最多 scan 一次，且 scan stamp 先写入再做后续判断
- success throttle：距上次成功 consolidation 未满 24 小时，不启动 auto-dream
- session 阈值：自上次成功以来至少累计 5 个 eligible session，且计数排除当前 thread 自身
- project 归属：默认按 canonical project key 收敛；只有显式 `autoMemPathOverride` 且对应 feature flag / env opt-in 已开启时，才允许退化成全局 eligible thread 视角；这条 override 是**非默认**兼容入口，不得被实现者误读成主路径。缺 project scope / root / repoSlug 时返回 `ErrMemoryProjectScopeRequired` 一类 sentinel，不能默默扩大范围
- task 状态：保持 `starting/updating` phase，且 stop 是显式 cancel 当前 task，不跟 bus callback context 绑定

## 同步约束

- 本单不改变 TeamSync / Auto-dream 的功能语义，只改变 ownership 位置。
- 允许保留现有 eligibility / gating / stamp / dedupe 逻辑；不要在本单顺手改业务策略。
- 如果实现时碰到 `thread.onAgentFailed` 等相同模式，可按同样模板追加，但不作为本单必做项。
- `resolveRuntime(...)` 只有在 authoritative 解析后明确判定“不满足 auto-memory eligibility”时才能 no-op；若缺 `gate/root/repoSlug/OAuth` 这类必需输入，必须返回 `ErrMemoryRuntimeRequired` / `ErrMemoryScopeRequired` 一类 sentinel 并计数，不得静默跳过、更不得回退到全局默认视角。
- benign no-op 只允许用于版本/日历/流控/已去重命中的无害条件；scope / 信任域 / root 解析未决绝不属于 benign no-op。
- `thread` 当前文件数已高，修复优先采用边删边改、边内联边收口，不鼓励再横向拆出一串新 helper 文件制造新的包内碎片。
- 本轮继续不把整个 `skill` 包纳入 scope；若某次修复只触发 `skill` 侧 UI debounce，而不涉及 ownership/stop-drain 退化，不应混入 P22。
- `toolbridge` 的依赖方向问题不是纯 runtime ownership；若本轮不一并修，文档必须明确这是仍然存在的仓库契约违规，避免只修 runner 后误判为闭环。
- `memory/team` 的 event helper / public service API 也属于整改面；若只改 `module.go` 回调而不删 helper/API 旁路，视为半迁移未完成。
- `memory hook` shutdown 必须阻塞到 auto-dream 与 background extraction worker 退出；只 cancel 不 wait 不算闭环。
- `config_change` 虽不需要像 sweeper 一样 Runner 化，但其 bus callback slow-path 仍属 `P2` 硬范围，不能因为 `registerConfigChangeLifecycle` 是 wiring 就被排除。
- `rpc push` 虽然依赖现有 `rpc.Server`/`PushBridge`，但 callback 直做 transport I/O 仍属 `P2` 硬范围，不能因为它看起来像“只是通知”就被排除。
- `eventsurface` 的 legacy 派生通知规则也属于 `P2` 必须冻结的行为契约；否则搬运到 queue/worker 后极易 silently 改变刷新语义。

## 切片级回滚卡（R2）

| slice | gate carrier | rollback trigger | state rewind | disable steps | red-green |
|---|---|---|---|---|---|
| `memory/team + auto-dream + memory hooks + NestedRuntime tool-read ingest` | feature flag / env opt-in（default-off） | watcher final flush 丢失、project-scope 判定漂移、busy 由 drop 变补跑、tool-read ingest 串回 callback/helper slow-path | 恢复旧 watcher/runtime 绑定与 tool-read ingest 接线，撤回 queue owner wiring，保留 canonical project key + enqueue-only 语义 | 关闭新 coordinator/scheduler/hook-worker gate，停用新 queue/worker，删除半接线入口 | owner 守卫 + NestedRuntime ingest + 行为测试同 PR red-green |
| `thread event / resume / task-handoff + cachekeepalive(session users)` | feature flag / env opt-in（default-off） | callback slow-path 仍写库/做重 I/O、resume/drain 失效、keepalive ping 无法有界收口 | 恢复旧 thread/session-user 路径，但不得恢复 `context.Background()` resume/ping 旁路 | 停用新 thread owner / keepalive gate，回退前等待 in-flight resume/ping drain | hot-file guard + queue/worker 测试同 PR red-green |
| `hooks + config fanout + rpc push + eventsurface` | feature flag / env opt-in（default-off） | hook escalate / config version / legacy refresh 语义漂移，或 shutdown 后仍有 in-flight fanout 越界 | 恢复原有 hook/config/push 顺序与 legacy expansion 规则，但不得恢复 silent fallback / fire-and-forget callback | 关闭新 relay/fanout/push worker gate，回退前等待队列 drain | worker 行为测试 + shutdown PoC 同 PR red-green |
| `toolbridge runtime owner` | explicit compatibility gate | proxy serve / stop / drain 回归，或 peer/session contract 前置未满足却误入实现 | 只回退 serve owner 接线，不回退 `thread/runtime/identity` 的硬报错口径 | 关闭新 proxy owner gate，恢复旧 serve wiring 前先清空残留 goroutine | runtime owner 与 protocol smoke 同 PR red-green |
| `gopls + bootstrap runtime` | sidecar lane gate | heartbeat/report queue/reconnect/drain 回归，或 required-topic subscribe / desired-state contract 无法维持 | 恢复上一个 sidecar owner 接线，保留 protocol / identity fail-closed contract | 停用新 sidecar owner，等待 reconnect/report queue drain 完毕 | sidecar runtime 测试 + protocol smoke 同 PR red-green |

## 非目标

- `P2` 不签收 dependency direction / hidden contract；这些继续由 `P4` 负责。
- `P2` 不把所有子域塞进一次 mega PR；文档是一张 umbrella map，不是合入批次承诺。
- `P2` 不顺手改业务策略，例如 TeamSync gate、auto-dream 阈值、hook merge 规则、config version 语义；只要求这些规则从 callback 路径迁到单一 owner。
- `P2` 不把“退订”写成 drain，也不把 `context.Background()` 旁路保留成默认兜底。

## TDD 与旧实现清理

- 先补失败测试：bus 回调只 enqueue、不直拉 watcher/task；TeamSync final flush 不丢；runtime 切换不串写；auto-dream busy 仍 drop；scan/success throttle 不漂移。
- 对扩围项逐项补失败测试：thread event slow-path、hooks relay drain、toolbridge proxy owner、config fanout、cachekeepalive timer drain、rpc push queue、memory hook extraction drain、NestedRuntime tool-read ingest enqueue-only。
- 测试名固定到可派单级别：`TestTeamSyncCallbackEnqueueOnly`、`TestTeamSyncRuntimeSwapFinalFlush`、`TestAutoDreamBusyDropsWithoutReplay`、`TestAutoDreamRequiresExplicitProjectScope`、`TestMemoryHookWorkerDrainsOnStop`、`TestNestedToolReadIngestEnqueueOnly`、`TestHookRelayDrainAfterShutdown`、`TestConfigFanoutWorkerUsesCancelableContext`、`TestCacheKeepaliveDrainCancelsPendingPing`、`TestRPCPushQueuePreservesLegacyExpansion`
- 验证命令固定写法：`go test ./internal/module/memory/... -run 'Test(TeamSyncCallbackEnqueueOnly|TeamSyncRuntimeSwapFinalFlush|AutoDreamBusyDropsWithoutReplay|AutoDreamRequiresExplicitProjectScope|MemoryHookWorkerDrainsOnStop|NestedToolReadIngestEnqueueOnly)' -count=1 -v`、`go test ./internal/platform/hooks/... -run 'TestHookRelayDrainAfterShutdown' -count=1 -v`、`go test ./internal/platform/toolbridge/... -run 'TestToolbridgeProxyOwner' -count=1 -v`、`go test ./internal/platform/mcpcontrol/... ./internal/platform/cachekeepalive/... ./internal/platform/rpc/... -run 'Test(ConfigFanoutWorkerUsesCancelableContext|CacheKeepaliveDrainCancelsPendingPing|RPCPushQueuePreservesLegacyExpansion)' -count=1 -v`
- 对 auto-memory `project/root/repoSlug` 资格判定、hook relay shutdown drain、config fanout cancelability 补运行时 PoC；不能只靠 callback 级 mock 证明“没有 silent fallback”
- 修复完成后必须删除回调链里的直接 `StartSession/StopSession`、`go maybeScheduleAutoDream(...)`、`launchAutoDreamTask(...)` 旁路启动点；不能留下“新 coordinator/scheduler + 旧回调直达”双轨。
- 若 `TeamSyncCoordinator` / `AutoDreamScheduler` 吸收了旧 helper，旧的 service-owned 启动入口要同步删掉或降成纯内部原语，避免语义重复。
- 本单明确不接受“回调里先尝试直跑，失败再 enqueue”的折中方案；这类 fallback 视为垃圾代码。
- 新增 owner 后必须删除或降级旧入口：event helper、late setter、callback direct fanout、`context.Background()` 旁路、un-drained timer/goroutine 都不能保留双轨。

## 验收标准

- memory bus 回调中不再直接 `StartSession/StopSession`
- `teamSyncWatcher.Start()` 不再通过 bus 路径直达
- `auto_dream_task.go` 的事件回调中不再直接 `go`
- `internal/module/thread/module.go` 不再通过 `fx.Invoke` 做 setter 型后置注入
- `thread` 事件回调不再直接做 binding store / prompt invalidation / task-handoff 重 I/O / delayed resume
- `backgroundResumeIfNeeded(...)` 有明确 owner、可 drain、可测试，不再是裸 `context.Background()` goroutine
- `platform/hooks/event_relay.go` 不再在 callback 路径里 `go` fanout / `DispatchAfter(...)`，并且 shutdown 有明确 drain
- `hooks` 的 startup recovery、lost-subscriber cleanup、merge/fail-closed 与 recursion guard 语义未漂移
- `internal/platform/toolbridge/module.go` 不再通过 `fx.Invoke` 做 setter 型 late wiring，proxy serve 有明确 owner/stop/drain
- `internal/platform/toolbridge/module.go:130-159` 不再在 `OnStart` 里 `go ServeProxy(...)`
- `internal/platform/mcpcontrol/config_change.go` 不再在 bus callback 路径里直接 fanout config notify
- `internal/platform/cachekeepalive/relay.go` 不再由 bus callback 直接持有 timer/session runtime
- `internal/platform/rpc/push.go` 不再在 bus callback 路径里直接做 RPC notify/network I/O
- `config_change`、`cachekeepalive`、`rpc push` 不再以 `context.Background()` 旁路 publish/shutdown cancel
- `eventsurface` 的 legacy 派生通知规则在迁移后保持显式、可测试、不回归
- `memory/team/thread_metadata.go` 不再保留 event helper 直达 `StartSession/StopSession` 的慢路径旁路
- `TurnInputReceived` / `TurnCompleted` 不再在 bus callback 路径里直接执行 memory write / transcript+manifest read / extraction
- `ToolCallEnd -> AddToolReadResult(...) -> os.ReadFile(...)` 不再停留在 callback/helper slow-path；NestedRuntime tool-read ingest 进入单一 owner，且沿本页冻结的 `lossless pending-set / wake-signal` 口径处理
- `registerMemoryHooks` 的 shutdown path 返回前必须能证明 auto-dream 与 background extraction 已完成有界收口；但 drain 主体是 `MemoryHookWorker` / 显式 owner，不把模块级 `OnStop` 写成 worker drain 主体
- `memory.team` 的 lifecycle stop 绑到显式 owner/coordinator，而不是直接绑 `TeamSyncService.Shutdown`
- `TeamSyncService` 不再对外暴露可绕过 owner 的 lifecycle/remote-I/O 公开入口，或这些入口被明确降级为 owner 内部原语
- `waitDreamTask()`、`TeamSyncService.Pull/Push(...)`、`memory.NewRelevantMemoryFinder(...)` 这类当前仅 test caller 的 helper / API，不再以 public live entry 形态残留；要么接入真实 owner，要么删除 / 降级为 internal-only
- 至少补以下测试：
  - 回调只 enqueue，不做慢路径
  - repeated events 会正确 coalesce / dedupe
  - cancel/shutdown 时 watcher 和 auto-dream 任务能停止
  - event storm 下 publish path 不被慢操作反压
  - TeamSync 切换 runtime 时旧 watcher final flush 不会误打到新 runtime
  - `StopSession(last)` / `Shutdown` 仍保留最终 flush 语义
  - auto-dream 保持 10 分钟 scan throttle、24h success throttle、5-session 阈值
  - busy 状态下 auto-dream 触发保持 drop 而不是补跑
  - `thread` event storm 下不再直接触发重 I/O slow path
  - `backgroundResumeIfNeeded(...)` 在 shutdown/drain 时不会遗留未托管的 resume goroutine
  - hooks relay 在 shutdown 后无残留 in-flight dispatch 越过 stop
  - hook fanout / escalate 不再直接跑在 publish path 上
  - hook startup recovery 仍能恢复 pending review
  - hook lost-subscriber cleanup 仍按失败阈值触发
  - hook merge/fail-closed 与 recursion guard 语义不回归
  - toolbridge proxy stop 后无残留 serve goroutine
  - 去掉 setter 注入后 toolbridge wiring 仍完整可用
  - config-change fanout 不再直接跑在 publish path 上，且 shutdown 时可 drain
  - cache keepalive 的 timer reset/stop 不再直接由 bus callback 驱动
  - rpc push notify/fanout 不再直接跑在 bus callback 路径上，且 shutdown 时可 drain
  - `memory/team` 不再存在可从 event/helper 直达 lifecycle/remote I/O 的公开 API 旁路
  - config-change / rpc push / keepalive 的背景上下文旁路被移除
  - keepalive 已触发 ping 的 drain 语义有测试
  - `eventsurface` legacy 派生通知倍增规则仍受测试保护
  - memory hook slow-path 不再直接跑在 bus callback 上
  - auto-dream 与 background extraction 在 stop 前完成 drain
  - `thread_metadata` 的 `BuildCtx` 恢复优先级在迁移后仍有测试守卫

## 追加范围：MCP-LSP / Bootstrap Runtime

本轮继续审查确认 `cmd/mcp-lsp/gopls` 与 `internal/mcpserver/common/bootstrap` 也存在 runtime ownership 类残留，归入 `P2` 的运行时收口清单。

### MCP-LSP Gopls Runtime

- `NewManagerPool()` 构造时启动 recycler loop，定时做 RSS 扫描、client shutdown/restore/recreate 等慢路径
- LSP cache store 构造时启动 cleanup loop，维护 TTL/persist
- transport 对 server request 使用 fire-and-forget responder goroutine，且没有明确 join/drain
- diagnostics / grouping helper 使用 `context.Background()` 可能触发 install/spawn/recreate 慢路径

目标：

- `recycler` / `cache cleanup` 这类长期 loop 归 `RunnerModule`；`transport responder` 这类不适合直接进 `run.Group` 的并发，归显式 local owner 的 `Start/Stop/Drain`（旧稿合写为“owner 或 Runner”；本轮拆分为准）
- constructor 不再偷跑 goroutine
- sidecar 仍只有一棵 runner root；local owner 不等于再发明第二棵 root bridge 或 bus 树。
- Close/Shutdown 必须 join/drain
- diagnostics/wait 这类只读路径不得触发 auto-install 或 client recreate

### MCP Bootstrap Runtime

- `stdio` EOF 当前能隐式接管 peer 进程寿命；peer mode 同时暴露 stdio 与 HTTP/discovery 时，必须明确谁拥有进程寿命
- `SubscribeHooks` 现在只在 `conn=nil/degraded` 或订阅成功时保存 desired state；若 live `CallResult(...)` 首次失败，当前不会落盘 desired state，因此这条失败路径仍不能靠 retry/replay 自愈
- `OnShutdown`、`OnConfigChanged` 等 inbound callback 目前可异步 fire-and-forget，`Close()` 不 join/drain

目标：

- peer 生命周期 owner 不再由 inherited stdio EOF 隐式决定，除非文档显式冻结这种模式
- hook subscribe 对 required topics 必须显式冻结一种 failure policy：要么 fatal gate，要么保存 desired state 并持续 retry/replay
- inbound callback 的 async 执行必须有 owner、cancel、join/drain 语义
