# Round 008 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:38:29 KST
- 结束：2026-05-17 05:39:38 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 team sync watcher 的事件 debounce、flush、suppression、root drift fail-closed、自写抑制，以及 session start/stop/shutdown 对 watcher 的处理。

- `internal/module/memory/team/team_sync_watcher.go`
- `internal/module/memory/team/team_sync_watcher_test.go`
- `internal/module/memory/team/team_sync.go`
- `internal/module/memory/team/team_sync_pull.go`
- `internal/module/memory/team/team_sync_test.go`

## Findings

1. **[major] watcher push 失败后会清掉 dirty 标志，没有自动重试或重新检测**
   - 证据：事件命中后 `handleWatcherLoopEvent()` 将 `dirty=true` 并重置 debounce timer（`internal/module/memory/team/team_sync_watcher.go:169-181`）。timer 触发后 `flushWatcherLoopPush()` 先把 `*dirty=false`，再调用 `pushLocalChanges()`；如果 push 返回错误，只记录 warning，不恢复 dirty，也不安排下一次 timer（`internal/module/memory/team/team_sync_watcher.go:185-199`）。
   - 风险：网络错误、OAuth 暂不可用、远端 5xx、冲突处理失败等一次性错误会让本地变更停留在磁盘但 watcher 认为本轮已处理。除非后续又发生新的 fsnotify 事件、用户手动 push、或 stop/shutdown flush，团队记忆可能长期不上传。
   - 建议：push 失败时保留 dirty 并按退避策略重试；或者调用 `detectDirty()` 重新确认后恢复 timer。失败计数应可观测，避免 watcher 静默卡住。

2. **[major] remote pull 后按路径 suppression 1 秒，可能吞掉同路径真实本地编辑事件**
   - 证据：remote apply 后 `suppressWatcherWrites(paths)` 调用 watcher `Suppress()`（`internal/module/memory/team/team_sync_pull.go:126-131`）。`Suppress()` 对每个路径设置固定 `teamSyncWatcherSuppressFor = 1s` 过期时间（`internal/module/memory/team/team_sync_watcher.go:19-20`、`internal/module/memory/team/team_sync_watcher.go:107-118`）。后续事件只要 path 仍在 suppression map 中，`handleEvent()` 就直接返回 unchanged（`internal/module/memory/team/team_sync_watcher.go:218-225`）。测试只锁定 remote pull 后短时间不触发 self-push（`internal/module/memory/team/team_sync_test.go:146-160`）。
   - 风险：如果用户或工具在 remote pull 后 1 秒内编辑同一个 team memory 文件，fsnotify 事件会被 suppression 吃掉，且 watcher 不会执行内容 diff。该本地编辑不会自动 push，直到下一次事件或关闭 flush 才可能被发现。
   - 建议：suppression 不应只按 path+时间判断；可记录 remote 写入后的 checksum，收到 suppressed path 事件时重新扫描该 path，如果 checksum 已不同于 remote-applied 版本，则取消 suppression 并触发 dirty。

3. **[moderate] watcher fail-closed 后不会自恢复，session 仍保留但实时同步停止**
   - 证据：`handleWatcherLoopError()` 遇到 watcher error 直接返回 true 结束 loop（`internal/module/memory/team/team_sync_watcher.go:162-167`）；`handleWatcherLoopEvent()` 遇到 root drift、symlink、addRecursive 等错误也 warning 后返回 true（`internal/module/memory/team/team_sync_watcher.go:169-177`）。loop 结束只 close fsnotify watcher（`internal/module/memory/team/team_sync_watcher.go:128-137`、`internal/module/memory/team/team_sync_watcher.go:330-333`），没有通知 `TeamSyncService` 清空或重建 `s.watcher`。`StartSession()` 只有 runtime 不可复用时才替换 watcher；同 root/repo 下会直接复用现有 `s.watcher` 指针（`internal/module/memory/team/team_sync.go:114-126`）。
   - 风险：一次 root drift 或临时 fsnotify 错误会让 watcher goroutine 退出，但 service 仍持有 watcher 指针，后续同一项目新 session 可能“复用”一个已退出的 watcher。实时同步停止，用户只能靠手动 push 或 shutdown flush。
   - 建议：watcher loop 退出时回调 service 标记 watcher dead；下一次 session 或健康检查应重建 watcher。对可恢复错误可以重新 add watch，而不是永久停掉。

## 误报与已覆盖项

- root symlink drift 的 fail-closed 语义已有测试覆盖（`internal/module/memory/team/team_sync_watcher_test.go:9-33`），本轮不报告 drift 未检测。
- `detectDirty()` 会忽略非 `.md` 和 root 外文件，只对 team markdown 变化报 dirty（`internal/module/memory/team/team_sync_watcher_test.go:35-71`），本轮不报告 dirty 范围过宽。
- `Close(ctx, flush=true)` 会在 loop 结束后用 `context.WithoutCancel` 派生的上下文执行 push（`internal/module/memory/team/team_sync_watcher.go:81-104`、`internal/module/memory/team/team_sync_watcher.go:380-390`），本轮不报告关闭 flush 缺失。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/memory/team -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/memory/team` 均通过。

## 下一轮建议

- Round 009 审查 prompt assembly 的动态 section 排序、token budget 和 attachment 渲染，重点看评分/预算结果是否在最终 prompt 中被真实执行。
