# Round 007 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:36:37 KST
- 结束：2026-05-17 05:37:33 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 team memory sync 的本地/远端 checksum 差异计算、secret guard 过滤、批量 push、冲突重试、状态持久化，以及 memory health 相似对 ignored set 的读取策略。

- `internal/module/memory/team/team_sync_push.go`
- `internal/module/memory/team/team_sync_fs.go`
- `internal/module/memory/team/team_sync_pull.go`
- `internal/module/memory/team/team_sync_remote.go`
- `internal/module/memory/team/team_sync_state.go`
- `internal/module/memory/team/team_guard.go`
- `internal/module/memory/team/team_sync_test.go`
- `internal/module/memory/ui_rpc.go`
- `internal/module/memory/similarity/similarity.go`

## Findings

1. **[major] push 拆成多批后，每批仍使用当前全量 ServerChecksums 作为 BaseChecksums，批间基线可能过期**
   - 证据：`buildTeamSyncBatches()` 在超过 server limit 时把 uploads/deletes 拆成多个 batch（`internal/module/memory/team/team_sync_push.go:46-76`）。每批发送时 `pushBatchLocked()` 都使用 `BaseChecksums: cloneChecksumMap(s.state.ServerChecksums)`（`internal/module/memory/team/team_sync_push.go:102-109`）。批响应成功后 `applyPushResponse()` 会更新 `s.state.ServerChecksums`（`internal/module/memory/team/team_sync_push.go:131-133`、`internal/module/memory/team/team_sync_push.go:230-249`），所以下一批的 BaseChecksums 已包含前一批应用结果，而本轮 diff/批次是在旧 plan 上一次性生成的（`internal/module/memory/team/team_sync_push.go:21-43`、`internal/module/memory/team/team_sync_push.go:79-99`）。
   - 风险：如果远端把 `baseChecksums` 当作“本次变更基于哪个远端快照”的完整前置条件，多批 push 的第二批开始就可能使用与该 batch 原始 diff 不一致的基线。轻则制造不必要的 conflict/retry；重则在远端实现较宽松时，用更新后的基线掩盖计划生成时的并发变化。
   - 建议：为整个 push plan 固定一个 `baseChecksums` snapshot 和 ETag，并在所有 batch 中使用同一个基线；或者每批成功后重新扫描本地和重新计算剩余 diff，而不是继续使用旧 plan。

2. **[major] secret guard 只过滤 upload，不阻止同路径 delete，可能把远端安全副本删除掉**
   - 证据：`diffServerChecksums()` 对本地有文件但 checksum 不同的 path 生成 upload，对 server 有而 local 没有的 path 生成 delete（`internal/module/memory/team/team_sync_fs.go:167-183`）。`preparePushLocked()` 仅对 uploads 执行 `s.guard.FilterPushFiles(uploads)`，deletes 原样进入 batch（`internal/module/memory/team/team_sync_push.go:92-99`）。`FilterPushFiles()` 遇到 secret 只把该 upload 放进 `Skipped`，不会返回任何“该 path 禁止删除”的信息（`internal/module/memory/team/team_guard.go:84-102`）。batch 请求结构允许同时包含 `Uploads` 和 `Deletes`（`internal/module/memory/team/team_sync_remote.go:46-52`）。
   - 风险：典型场景是本地某个 team memory 文件被改成含 secret，guard 会跳过 upload；如果本地同时把另一个曾在 server 上存在的安全文件删除，delete 会继续推送。更危险的是异常状态下 local 扫描漏掉某个含 secret 或被忽略的 path，而 state.ServerChecksums 里仍有该 path，则系统会推 delete。guard 只防止泄露 secret，不防止因为 skip/扫描差异造成远端数据丢失。
   - 建议：当任何 upload 被 secret guard skip 时，谨慎停止本轮所有 deletes，或至少阻止与 skipped path 同目录/同 canonical name 相关的 delete。失败结果中应明确提示“push blocked deletes due to secret skip”。

3. **[moderate] ignored set 损坏时 UI 相似对会重新出现，可能诱发重复整合**
   - 证据：`similarity.LoadIgnored()` 对 corrupt JSON 返回错误（`internal/module/memory/similarity/similarity.go:111-130`）。但 memory health 生成时直接 `ignored, _ := similarity.LoadIgnored(privateRoot)` 丢弃错误，并按空 set 继续展示所有 `FindSimilarPairs()` 结果（`internal/module/memory/ui_rpc.go:147-170`）。
   - 风险：`.similarity-ignored.json` 一旦损坏，用户之前显式 ignore 或 LLM 判定不应合并的 pair 会重新出现在 health banner 中。用户可能再次触发 consolidate-all，让已拒绝的相似对重新交给 LLM 判断，增加误合并概率。
   - 建议：读取 ignored set 失败时应在 health 中显示降级/错误状态，或至少不自动展示曾可能被忽略的 pair；同时提供修复/重建 ignored 文件的入口。

## 误报与已覆盖项

- 413/max entries 学习路径已有测试覆盖：第一次 oversize push 学到 `ServerMaxEntries`，第二次按 limit 拆批（`internal/module/memory/team/team_sync_test.go:226-320`），本轮不报告该路径缺失。
- push conflict 会 pull 后重试一次，二次 conflict 则停止并合并失败信息（`internal/module/memory/team/team_sync_push.go:155-175`），本轮不报告无限重试。
- 本地扫描拒绝 symlink 和非 markdown/staging 文件（`internal/module/memory/team/team_sync_fs.go:54-111`），本轮不报告路径扫描越界。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/memory/team ./internal/module/memory -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/memory/team`、`internal/module/memory` 均通过。

## 下一轮建议

- Round 008 审查 team sync watcher 的 debounce、root drift、suppression 和 fail-closed 行为，重点看高频变更下是否丢事件或持续停止同步。
