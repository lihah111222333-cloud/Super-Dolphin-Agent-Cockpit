# 第 12 轮审查结论

## 审查范围

- `internal/module/turn/tool_result_storage.go`（CaptureToolResult、persistToolResult、toolResultStorageDir、文件名生成）
- `internal/module/turn/tool_result_budget.go`（turn 级 budget registry、takeToolResultPreview、truncateToolResultChars、repairTruncatedJSON、JSON 修复状态机）
- `internal/module/turn/tool_result_lifecycle.go`（lifecycle registry、Register/Cleanup/Reset、文件删除、目录修剪）

> 与第 01 轮覆盖的 `internal/mcpserver/common/tool_result.go` 不重复（本轮聚焦 module/turn 层的存储与 budget 逻辑）。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `tool_result_storage.go:28-51` `CaptureToolResult` | 兜底 | `originalSize == 0` 时返回零值 `ToolResultRecord{}`；不区分"空字符串"与"全空白" | `toolResultCharCount` 对全空白字符串返回 0（因为 `strings.TrimSpace(raw) == ""`）；调用方传 `"   "` 拿到零值 record，后续 UI 显示空白 | 空白字符串应 return error 或至少保留原始空白（不 trim） |
| `tool_result_storage.go:53-63` `persistToolResult` | 静默 | `toolResultStorageDir()` 失败返回 `""`；`os.WriteFile` 失败也返回 `""` | 持久化失败完全无日志、无 metrics；调用方拿到 `PersistedPath == ""` 无法区分"不需要持久化"与"持久化失败" | 至少 Warn log + metrics；返回 (string, error) |
| `tool_result_storage.go:65-74` `toolResultStorageDir` | 静默 | `toolresults.CacheDir()` 返回 "" 时返回 error；`os.MkdirAll` 失败返回 error | 这里本身返回 error 是正确的，但调用方 `persistToolResult` 把 error 吞掉了 | 修 persistToolResult 即可 |
| `tool_result_storage.go:76-88` `toolResultFileName` | 兜底 | `meta.Timestamp.IsZero()` 时 fallback 到 `time.Now()` | 零值 Timestamp 是调用方 bug（CaptureToolResult 的调用方应设置 Timestamp）；fallback 掩盖 | 零值 Timestamp 应 panic 或 error |
| `tool_result_budget.go:33-35` `ResetToolResultScope` | 兜底 | 直接调用 `defaultToolResultBudgetRegistry.Reset`；threadID/turnID 为空时 scope 为空，Reset 内部 `scope == ""` 直接 return | 调用方传空 threadID/turnID 是 bug；静默 noop | 空参数 panic 或 error |
| `tool_result_budget.go:37-45` `Reset` | 兜底 | `r == nil \|\| scope == ""` 直接 return | nil receiver 是 bug | nil receiver panic |
| `tool_result_budget.go:51-76` `Take` | 兜底 | `r == nil` 或 `scope == ""` 时不走 budget 管理，直接用全局 `toolResultPreviewBudgetChars` 截断 | 无 scope 时每次调用都用满额 budget，不做累计限制；turn 级 budget 形同虚设 | scope 为空应 error；nil receiver panic |
| `tool_result_budget.go:78-85` `toolResultScope` | 兜底 | threadID/turnID 任一为空返回 "" | 与上一条配合：scope 为空 → budget 不生效 | 空参数应 error |
| `tool_result_budget.go:87-92` `toolResultCharCount` | 兜底 | `strings.TrimSpace(raw) == ""` 返回 0 | 全空白字符串被当成"空"；与 CaptureToolResult 的零值 return 配合，空白内容被静默丢弃 | 不 trim；空字符串返回 0，非空（含空白）返回实际 rune count |
| `tool_result_budget.go:94-103` `truncateToolResultChars` | 兜底 | `limit <= 0 \|\| raw == ""` 返回 "" | limit<=0 是调用方 bug | limit<=0 panic |
| `tool_result_budget.go:105-122` `repairTruncatedJSON` | 兜底 | 修复失败返回原 truncated | 这是 best-effort 设计，合理。但 `json.Valid([]byte(repaired))` 失败时无日志 | 至少 debug log "JSON repair failed" |
| `tool_result_budget.go:29-31` 全局 registry | 弱契约 | `var defaultToolResultBudgetRegistry = &toolResultBudgetRegistry{...}` 全局单例 | 不可测试（测试间共享状态）；并发 turn 共享同一 map | 改为 per-turn 注入；或至少在测试中 Reset |
| `tool_result_lifecycle.go:32-34` `registerToolResultLifecycle` | 兜底 | 全局 registry；`r == nil \|\| threadID == "" \|\| record.OriginalSize == 0` 静默 return | threadID 为空是调用方 bug；OriginalSize==0 是 CaptureToolResult 的零值 record 路径 | threadID 为空 panic |
| `tool_result_lifecycle.go:44-52` `Register` | 兜底 | nil receiver / threadID 为空 / OriginalSize==0 全部静默 return | 同上 | nil receiver panic；threadID 为空 error |
| `tool_result_lifecycle.go:54-86` `Cleanup` | 兜底 | `r == nil \|\| threadID == "" \|\| !cfg.EnabledForModel(model)` 返回零值 result | nil receiver 是 bug；threadID 为空是 bug；cfg 未启用是合法 | nil receiver panic；threadID 为空 error |
| `tool_result_lifecycle.go:56` `cfg.EnabledForModel(model)` | 弱契约 | cfg 为 nil 时 `cfg.EnabledForModel` 会 panic（nil pointer dereference） | 调用方 `cleanupToolResultLifecycle` 传入的 cfg 来自 `*contract.FRCConfig`，可能为 nil | 入口校验 `cfg == nil` 返回零值（合法：未配置 FRC） |
| `tool_result_lifecycle.go:88-100` `Reset` | 静默 | 遍历 entries 调用 `deleteToolResultFile`；删除失败静默 | 文件删除失败（权限、占用）无日志 | 至少 Warn |
| `tool_result_lifecycle.go:102-112` `deleteToolResultFile` | 静默 | `os.Remove` 失败（非 NotExist）返回 false | 权限错误、IO 错误被当成"文件不存在" | 区分 NotExist（正常）与其它错误（Warn） |
| `tool_result_lifecycle.go:114-125` `pruneToolResultDir` | 静默 | `os.Remove(current)` 失败直接 return（不继续向上） | 合理（目录非空时 Remove 失败是正常的）；但如果是权限错误也被当成"非空" | 区分 `os.IsNotExist` / `os.IsPermission` / 其它 |
| `tool_result_lifecycle.go:127-134` `toolResultCleanupRoots` | 兜底 | `os.UserCacheDir()` 失败时跳过；始终 append TempDir 路径 | UserCacheDir 失败是环境异常；当前静默跳过 | 至少 Warn |
| `tool_result_lifecycle.go:114-125` `pruneToolResultDir` 循环 | 弱契约 | 外层 `for _, stop := range toolResultCleanupRoots()` 内层 `for current := ...` | 如果 `toolResultCleanupRoots()` 返回多个 root，外层循环会对同一个 dir 路径重复尝试 Remove | 逻辑应是"找到匹配的 root 后只在该 root 下修剪"；当前实现可能误删其它 root 下的目录 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `tool_result_storage.go:53-63` | persistToolResult 两处错误返回 "" |
| `tool_result_storage.go:76-88` | Timestamp 零值 fallback time.Now() |
| `tool_result_budget.go:37-45` | Reset nil/empty 静默 |
| `tool_result_budget.go:51-76` | Take nil/empty scope 不走 budget |
| `tool_result_budget.go:87-92` | toolResultCharCount 全空白返回 0 |
| `tool_result_budget.go:105-122` | repairTruncatedJSON 修复失败无日志 |
| `tool_result_lifecycle.go:44-52` | Register nil/empty 静默 |
| `tool_result_lifecycle.go:88-100` | Reset 删除失败静默 |
| `tool_result_lifecycle.go:102-112` | deleteToolResultFile 非 NotExist 错误返回 false |
| `tool_result_lifecycle.go:114-125` | pruneToolResultDir 权限错误当成"非空" |
| `tool_result_lifecycle.go:127-134` | UserCacheDir 失败静默跳过 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `tool_result_storage.go:28-51` `CaptureToolResult` | meta 字段无校验（ThreadID/TurnID/CallID/ToolName 都可为空） |
| `tool_result_storage.go:13-19` `ToolResultMeta` | 全字段 string，无 Validate 方法 |
| `tool_result_budget.go:29-31` | 全局单例 registry，不可测试 |
| `tool_result_budget.go:78-85` | toolResultScope 空参数返回 "" |
| `tool_result_budget.go:87-92` | toolResultCharCount 用 TrimSpace 判空 |
| `tool_result_budget.go:94-103` | truncateToolResultChars limit<=0 返回 "" |
| `tool_result_lifecycle.go:28-30` | 全局单例 lifecycle registry |
| `tool_result_lifecycle.go:54-86` | Cleanup cfg 可能 nil panic |
| `tool_result_lifecycle.go:114-125` | pruneToolResultDir 双层循环逻辑可能误删 |
| `tool_result_lifecycle.go:127-134` | toolResultCleanupRoots 依赖 os.UserCacheDir + os.TempDir |

## 修复优先级

### P0（必须本周修）
1. `tool_result_storage.go:53-63` persistToolResult 错误必须 log + metrics，不能静默返回 ""
2. `tool_result_budget.go:51-76` Take 在 scope 为空时不走 budget 管理，turn 级限制形同虚设——scope 为空应 error
3. `tool_result_lifecycle.go:56` Cleanup 中 `cfg.EnabledForModel(model)` 在 cfg==nil 时会 panic——入口加 nil 校验
4. `tool_result_lifecycle.go:114-125` pruneToolResultDir 双层循环逻辑审查：确认不会误删非 tool-result 目录

### P1（本月）
5. `tool_result_storage.go:76-88` Timestamp 零值改 error/panic
6. `tool_result_budget.go:87-92` toolResultCharCount 不应 TrimSpace 判空（空白内容也是内容）
7. `tool_result_budget.go:33-35` ResetToolResultScope 空参数 error
8. `tool_result_lifecycle.go:88-100` Reset 删除失败加 Warn
9. `tool_result_lifecycle.go:102-112` deleteToolResultFile 区分 NotExist 与其它错误
10. `tool_result_budget.go:29-31` 全局 registry 改为 per-turn 注入（长期重构）
11. `tool_result_lifecycle.go:28-30` 全局 lifecycle registry 同上

### P2（下个 sprint）
12. `tool_result_storage.go:28-51` CaptureToolResult 入口加 meta.Validate()
13. `tool_result_budget.go:105-122` repairTruncatedJSON 修复失败加 debug log
14. `tool_result_budget.go:94-103` truncateToolResultChars limit<=0 panic
15. `tool_result_lifecycle.go:127-134` UserCacheDir 失败加 Warn
16. `tool_result_lifecycle.go:44-52` Register nil receiver panic

## 边界条件

1. **`toolResultCharCount` 的 TrimSpace 判空是有意设计**：避免把"全空白"的 tool result 当成有效内容占用 budget。但这与 CaptureToolResult 的零值 return 配合后，空白内容被完全丢弃。修复时要确认：是否真的有工具返回全空白结果？如果有，应该保留还是丢弃？
2. **`persistToolResult` 返回 "" 的影响**：调用方 `CaptureToolResult` 用 `record.PersistedPath` 判断是否需要持久化。返回 "" 时 record 的 `PersistedPath` 为空，后续 lifecycle cleanup 不会尝试删除文件——这是正确的（文件没写成功就不需要删）。但 UI 侧可能需要知道"持久化失败"以显示警告。
3. **`pruneToolResultDir` 的双层循环**：外层遍历 `toolResultCleanupRoots()`（通常 2 个：UserCacheDir/super-agent-v3 和 TempDir/super-agent-v3），内层从 `dir` 向上 Remove 直到碰到 stop root。如果 `dir` 不在任何 root 下，内层循环会一直向上直到 `/` 或 `.`——这是一个**潜在的误删风险**。修复时应先检查 `dir` 是否在某个 root 下，不在就直接 return。
4. **`cfg.EnabledForModel(model)` nil panic**：`cleanupToolResultLifecycle` 的调用方在 `service_helpers.go` 或 `frc_config.go` 中。需要 trace 调用链确认 cfg 是否可能为 nil。如果 FRC 未配置时 cfg 就是 nil，那 nil 校验是必须的。
5. **全局 registry 的并发安全**：`defaultToolResultBudgetRegistry` 和 `defaultToolResultLifecycleRegistry` 都用 `sync.Mutex` 保护。并发安全没问题，但全局状态让测试间互相污染。改为 per-turn 注入是长期目标，短期可以在测试 setup 中 Reset。
6. **`repairTruncatedJSON` 的 best-effort 设计**：修复失败返回原 truncated 是合理的——broken JSON 总比 panic 好。但 `json.Valid([]byte(repaired))` 的校验是 O(n)，对大 JSON 有性能影响。当前 truncated 最大 50K runes，可接受。
7. **`deleteToolResultFile` 返回 bool 而非 error**：调用方用 bool 计数 `DeletedFiles`。改为返回 error 会改变 Cleanup 的签名和逻辑。建议保留 bool 返回但内部加 Warn log。
8. **`toolResultScope` 的 "threadID:turnID" 格式**：如果 threadID 或 turnID 包含 ":"，scope 会产生歧义。当前 sanitize 只在文件名生成时做，scope key 不做。低风险（ID 通常是 UUID），但可以加 assert。

---

下一轮范围建议：
- `internal/module/turn/service.go` + `service_helpers.go`（turn 服务主逻辑、FRC 调用）
- `internal/module/turn/tracker.go` + `tracker_states.go`（turn 状态机）
- `internal/module/turn/interrupt_service.go`（中断处理）
- 或 `internal/contract/`（核心接口、error 类型）
