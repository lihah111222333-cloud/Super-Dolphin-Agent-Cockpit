# 第 23 轮审查结论

## 审查范围

- `internal/platform/shared/validation.go`（FirstNonEmpty、FirstTrimmed、ClampLimit、FirstPayloadString）
- `internal/platform/shared/retry.go`（Retry、RetryWithPolicy、normalizeRetryPolicy、exponentialDelay、applyJitter、waitRetry）
- `internal/platform/shared/safe_go.go`（SafeGo deprecated wrapper）
- `internal/platform/shared/pathscope.go`（AppManagedDataRoots、ContainsPath、cleanAppManagedDataRoot、isAllowedConfiguredSuperDolphinHome）
- `internal/platform/shared/jsonutil.go`（DecodeInput、CloneSelector/HookPayload/Strings/RawMessage、FilterKeys、NormalizeAbsolutePath）
- `internal/platform/shared/idgen.go`（NewID delegate）
- `internal/platform/shared/hookutil.go`（NormalizeSelectorScope）

> 与第 03 轮覆盖的 `platform/shared/log_error.go` 不重复。本轮覆盖 shared 包其余文件。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `jsonutil.go:13-19` `DecodeInput` | 兜底 | 空/null input 被替换为 `{}` 后 unmarshal | 与 round-01 `normalizeOptionalToolParams` 完全相同的模式——空 body 被当成合法空对象 | 这是全项目最广泛使用的 decode helper；改动影响面极大。建议保留但在调用方层面做必填校验 |
| `retry.go:24-27` `RetryWithPolicy` | 兜底 | `ctx == nil` 兜底 Background | nil ctx 是调用方 bug | panic |
| `retry.go:54-71` `normalizeRetryPolicy` | 兜底 | 所有负值字段兜底为 0；Jitter > 1 兜底为 1 | 负值是调用方 bug | 负值应 error/panic |
| `safe_go.go:15-26` `SafeGo` | 静默+deprecated | `logger == nil` 时 recover 后不 log（`r != nil && logger != nil`）；fn=nil 时直接 panic（无 nil 校验） | 已标 Deprecated 且无调用点；但如果有人误用，fn=nil 会 panic 在 goroutine 内 | 删除该函数（已无调用点） |
| `safe_go.go:18` recover 条件 | 静默 | `if r := recover(); r != nil && logger != nil` | logger=nil 时 panic 被完全吞掉 | 已 deprecated，建议直接删除 |
| `pathscope.go:22-68` `AppManagedDataRoots` | 兜底 | `os.UserHomeDir()` 失败返回 error（正确）；`SUPER_DOLPHIN_HOME` 为空时 fallback 到 `~/.super-dolphin` | 合理的 fallback | OK |
| `pathscope.go:114-132` `cleanAppManagedDataRoot` | 兜底 | `os.ExpandEnv(root)` 后 trim；`~` 展开为 userHome | `os.ExpandEnv` 对未设置的 env var 返回空字符串——如果 root 是 `$UNDEFINED_VAR/data`，展开后变成 `/data`，可能指向非法路径 | 展开后校验路径是否在 userHome 下（已有 `isAllowedConfiguredSuperDolphinHome` 做此校验） |
| `pathscope.go:70-83` `isAllowedConfiguredSuperDolphinHome` | 兜底 | `cleanAppManagedDataRoot(userHome, userHome)` 失败时返回 false | 合理（defensive） | OK |
| `validation.go:18-27` `FirstPayloadString` | 兜底 | 所有 key 都找不到时返回 "" | 合理（optional 查找） | OK |
| `validation.go:29-38` `payloadString` | 兜底 | 未知类型返回 ""；map 类型递归查找 text/summary/message/output/result | 递归深度无限制；恶意构造的嵌套 map 可能 stack overflow | 加最大递归深度限制（如 3 层） |
| `hookutil.go:9-19` `NormalizeSelectorScope` | 兜底 | nil scope 返回零值 SelectorScope | 合理（nil = 无 scope） | OK |
| `jsonutil.go:42-57` `FilterKeys` | 兜底 | `len(keys) == 0` 时返回原 payload（不过滤） | 合理（空 keys = 不过滤） | OK |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `retry.go:24-27` | RetryWithPolicy ctx==nil 兜底 Background |
| `retry.go:54-71` | normalizeRetryPolicy 负值兜底 0 |
| `safe_go.go:18` | logger==nil 时 panic 被吞 |
| `jsonutil.go:13-19` | DecodeInput 空/null 替换为 {} |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `retry.go:24-27` | ctx nil 兜底 |
| `retry.go:54-71` | 负值字段兜底 |
| `safe_go.go:15-26` | deprecated 但仍存在；fn nil 无校验 |
| `validation.go:29-38` | payloadString 递归无深度限制 |
| `jsonutil.go:13-19` | DecodeInput 空/null → {} |
| `pathscope.go:114-132` | os.ExpandEnv 对未定义 var 返回空 |

## 修复优先级

### P0（必须本周修）
1. `safe_go.go` 整个文件删除——已标 Deprecated 且无调用点，但 logger=nil 时 panic 被完全吞掉是安全隐患

### P1（本月）
2. `retry.go:24-27` RetryWithPolicy ctx==nil 改 panic
3. `validation.go:29-38` payloadString 加最大递归深度限制（防 stack overflow）
4. `retry.go:54-71` normalizeRetryPolicy 负值改 error/panic

### P2（下个 sprint）
5. `jsonutil.go:13-19` DecodeInput 的 null→{} 行为文档化（不改行为，但在 godoc 中标注）
6. `pathscope.go:114-132` cleanAppManagedDataRoot 展开后加 debug log

## 边界条件

1. **`DecodeInput` 的 null→{} 是全项目最广泛使用的 decode helper**：grep 调用面会发现 50+ 处使用。改动行为会破坏所有 MCP tool call 的参数解析。当前行为是"空 body 当成空对象"——这是 MCP 协议的隐式约定（tools/call 的 arguments 可以为 null）。不应改动，但应在 godoc 中标注。
2. **`safe_go.go` 删除的影响**：注释说"no in-tree call sites remain"且有 archtest 阻止新增调用。直接删除不会破坏编译。但如果有外部 fork 使用了这个函数，删除会破坏他们。建议先确认 `go doc` 是否标注了 Deprecated。
3. **`payloadString` 递归**：当前只在 `FirstPayloadString` 中使用，payload 来自 MCP config change 事件。正常 payload 嵌套不超过 2 层。但如果恶意 peer 发送深度嵌套的 payload，会 stack overflow。加 `depth int` 参数限制到 3 层即可。
4. **`normalizeRetryPolicy` 负值兜底**：当前所有调用点都传正值（`Retry(ctx, 3, time.Second, fn)`）。负值不会在生产路径出现。改 panic 是 defensive。
5. **`AppManagedDataRoots` 的安全校验**：`isAllowedConfiguredSuperDolphinHome` 确保 `SUPER_DOLPHIN_HOME` 只能指向 userHome 下的特定路径（`.super-dolphin`、`Library/Application Support/Super Dolphin`、`AppData/Roaming/Super Dolphin`）。这是安全关键路径——防止用户配置 `SUPER_DOLPHIN_HOME=/` 导致整个文件系统被当成 app-managed。当前实现正确。
6. **shared 包整体是 thin wrapper 层**：大部分函数委托到 `internal/util/` 或 `internal/util/pathutil/`。本轮发现的问题集中在少数有实际逻辑的函数（retry、safe_go、payloadString）。

---

**本轮总结**：shared 包大部分是 thin wrapper（delegate 到 util 层），代码量小。唯一的 P0 是已 deprecated 的 `safe_go.go` 应该直接删除。其余问题是 retry 的 nil ctx 兜底和 payloadString 的递归无限制。

**累计进度**：23 轮完成。cron `da34430c` 继续推进。
