# P2: bus/runtime 解耦

## 目标

把 memory 相关 bus 回调中的 runtime ownership 抽离出来，恢复“bus 只接线，worker/runner 才拥有 watcher/scheduler”的分层。

## 对应 findings

- Finding 5: `internal/module/memory/module.go:456-466`
- Finding 6: `internal/module/memory/team/team_sync_watcher.go:72-78`
- Finding 7: `internal/module/memory/auto_dream_task.go:160-177`

## 现状校准

### TeamSync

- `registerTeamSyncSubscriptions(...)` 在 `thread.started/stopped` 回调里直接 `StartSessionFromThreadEvent` / `StopSessionFromThreadEvent`
- `TeamSyncService.StartSession(...)` 会创建/替换 watcher，并最终由 `teamSyncWatcher.Start()` 用 `SafeGo` 拉起主循环

这等于 bus 回调直接拥有了 watcher/session runtime。

### Auto-dream

- `registerAutoDreamSubscriptions(...)` 的回调里直接调用 `onThreadStopped(...)`
- `onThreadStopped(...)` 里再 `go` 调度 `maybeScheduleAutoDream(...)`
- `launchAutoDreamTask(...)` 再启动 consolidation 后台任务

这让订阅回调链直接承担了 scheduler/worker 的 ownership。

## 目标架构

推荐把 memory 的慢路径拆成两个显式 owner：

- `TeamSyncCoordinator`
- `AutoDreamScheduler`

bus 回调只负责 enqueue command，不再直接启动 watcher/任务。

### TeamSync 目标流向

```text
thread.started/stopped event
  -> bus callback
     -> non-blocking enqueue TeamSyncCommand
        -> TeamSyncCoordinator / Runner
           -> StartSession / StopSession
              -> watcher lifecycle
```

### Auto-dream 目标流向

```text
thread.stopped event
  -> bus callback
     -> enqueue AutoDreamJob
        -> AutoDreamScheduler / Runner
           -> eligibility check
           -> launch consolidation
```

## 实施步骤

### Step 1：bus 回调改 enqueue

`registerTeamSyncSubscriptions` / `registerAutoDreamSubscriptions` 的回调只允许做：

- 构造 command/job
- 写入 bounded channel
- channel 满时记录 drop / merge / coalesce

### Step 2：显式 worker owner

新增一个或两个 owner：

- 若 TeamSync 与 Auto-dream 都实现为 `Runner`，则直接加入 `group:"runners"`
- 若其中一个更适合 service-owned queue worker，也必须有显式 `Start/Stop/Drain`，不能再从回调里 `go`

### Step 3：watcher ownership 下沉

`teamSyncWatcher.Start()` 不再由 bus 路径间接触发；它应只被 `TeamSyncCoordinator` 调用。

### Step 4：Auto-dream 调度合流

把 `onThreadStopped -> go maybeScheduleAutoDream -> launchAutoDreamTask` 改为：

- 回调只 enqueue threadID
- scheduler 负责节流、去重、单飞、启动和 stop/drain

## 同步约束

- 本单不改变 TeamSync / Auto-dream 的功能语义，只改变 ownership 位置。
- 允许保留现有 eligibility / gating / stamp / dedupe 逻辑；不要在本单顺手改业务策略。
- 如果实现时碰到 `thread.onAgentFailed` 等相同模式，可按同样模板追加，但不作为本单必做项。

## 验收标准

- memory bus 回调中不再直接 `StartSession/StopSession`
- `teamSyncWatcher.Start()` 不再通过 bus 路径直达
- `auto_dream_task.go` 的事件回调中不再直接 `go`
- 至少补以下测试：
  - 回调只 enqueue，不做慢路径
  - repeated events 会正确 coalesce / dedupe
  - cancel/shutdown 时 watcher 和 auto-dream 任务能停止
  - event storm 下 publish path 不被慢操作反压
