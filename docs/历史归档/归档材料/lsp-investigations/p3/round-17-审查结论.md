# 第 17 轮审查结论

## 审查范围

- `internal/contract/frc.go`（FRCConfig、Normalize、EnabledForModel、KeepRecentCount）
- `internal/contract/provider.go`（Driver/Session/TurnHandle 接口、CodexNativeToolPolicy、NativeToolDescriptor、CapabilityError）
- `internal/contract/bus.go`（ResilientSubscribe、recoverCall、NewEmitter）
- `internal/contract/orchestration.go`（ValidateLaunchCWD、OrchestrationService/DAGRuntime 接口）

> 与第 16 轮覆盖的 `contract/{errors,memory,session,hooks,config,manifest}` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `frc.go:47-58` `EnabledForModel` | 兜底 | `c.Normalize()` 在 nil receiver 时返回 nil → `normalized == nil` 返回 false | nil FRCConfig 被当成"FRC 未启用"；与 round-12 `Cleanup` 中 `cfg.EnabledForModel(model)` 的 nil panic 配合——如果调用方先判 nil 再调用就不会 panic，但如果不判就崩 | 当前行为合理（nil = 未配置 = 不启用）；但应在接口文档标注 |
| `frc.go:17-45` `Normalize` | 兜底 | nil receiver 返回 nil；`KeepRecent <= 0` 兜底为 `defaultFRCKeepRecent` | nil receiver 合理（optional config）；KeepRecent 负值是配置 bug | 负值 KeepRecent 应 error 或 panic |
| `frc.go:60-66` `KeepRecentCount` | 兜底 | nil receiver → Normalize → nil → 返回 `defaultFRCKeepRecent` | nil config 用默认值是合理的 | OK |
| `bus.go:29-42` `ResilientSubscribe` | 兜底+静默 | `dispatcher == nil \|\| fn == nil` 返回 noop cancel；panic 后仅 log | nil dispatcher 是装配 bug；nil fn 是调用方 bug；panic 后仅 log 无 metrics | nil dispatcher/fn 应 panic；panic 后加 metrics |
| `bus.go:44-48` `recoverCall` | 静默 | recover 后返回 recovered value；调用方（ResilientSubscribe）仅 log | 与 round-03/04 safego 同根：panic 被吞 | 加 metrics counter |
| `bus.go:61-68` `NewEmitter` | 兜底 | `dispatcher == nil` 时返回的 emitter 内部 `if dispatcher == nil { return }` 静默丢弃事件 | nil dispatcher 是装配 bug；事件静悄悄丢失 | nil dispatcher 应 panic（构造期） |
| `provider.go:156-169` `NewCodexNativeToolPolicy` | 弱契约 | disabled 列表中非法 tool ID（`!IsKnownCodexNativeTool`）被静默跳过 | 用户配了不存在的 tool ID 不会得到任何提示 | 非法 ID 至少 Warn |
| `provider.go:265-268` `has` | 弱契约 | 方法名 `has` 实际含义是"is disabled"——语义反转 | 代码可读性差；新开发者容易误用 | 重命名为 `isDisabled` |
| `provider.go:337-353` `Session` 接口 | 弱契约 | `StartTurn` 返回 `(TurnHandle, error)`；TurnHandle 可能为 nil + nil error | 与 round-13 StartTurn 中 `handle == nil` 检查配合；但接口层无约束 | 接口文档标注"TurnHandle must not be nil when error is nil" |
| `provider.go:355-361` `TurnHandle` 接口 | 弱契约 | `Done()` 返回 `<-chan struct{}`；`Err()` 返回 error | Done channel 可能为 nil（实现 bug）；调用方 `<-handle.Done()` 会永远阻塞 | 接口文档标注"Done must return a non-nil channel" |
| `provider.go:363-370` `CapabilityError` | 弱契约 | `Capability` 和 `Driver` 字段可为空 | Error() 输出 `capability "" is not supported by  driver` | 构造函数 `NewCapabilityError` 应校验非空 |
| `provider.go:372-381` `NewCapabilityError` | 弱契约 | cap/driver 不做 trim/校验 | 同上 | 入口 trim + 空值 panic |
| `provider.go:376-381` `HasCapability` / `HasAllCapabilities` | 兜底 | `caps == nil` 返回 false | 合理（nil caps = 无能力） | OK |
| `orchestration.go:22-41` `ValidateLaunchCWD` | 弱契约 | 多条件分支校验 cwd；`cwd != "" && trimmedCWD == ""` 检测 whitespace-only | 校验逻辑正确且 fail-fast | OK（这是本轮中少见的正面案例） |
| `orchestration.go:64-80` `OrchestrationService` | 弱契约 | 20+ 方法的大接口；无分组/拆分 | 接口过大让 mock 困难；但这是设计问题不是 fail-fast 问题 | 长期拆分为 AgentService + DAGService + ReportService |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `bus.go:29-32` | ResilientSubscribe nil dispatcher/fn 返回 noop |
| `bus.go:38-40` | panic 后仅 log |
| `bus.go:63-66` | NewEmitter nil dispatcher 返回静默丢弃 emitter |
| `frc.go:26` | Normalize KeepRecent<=0 兜底 |
| `provider.go:156-169` | NewCodexNativeToolPolicy 非法 tool ID 静默跳过 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `frc.go:17-45` | Normalize nil receiver 返回 nil |
| `frc.go:26` | KeepRecent<=0 兜底为 2 |
| `bus.go:29-42` | ResilientSubscribe nil 参数兜底 |
| `bus.go:61-68` | NewEmitter nil dispatcher 兜底 |
| `provider.go:156-169` | NewCodexNativeToolPolicy 非法 ID 静默 |
| `provider.go:265-268` | `has` 方法名语义反转 |
| `provider.go:337-353` | Session.StartTurn nil handle 无约束 |
| `provider.go:355-361` | TurnHandle.Done nil channel 无约束 |
| `provider.go:363-381` | CapabilityError/NewCapabilityError 空字段 |
| `orchestration.go:64-80` | OrchestrationService 接口过大 |

## 修复优先级

### P0（必须本周修）
1. `bus.go:29-32` ResilientSubscribe nil dispatcher/fn 改 panic——nil 是装配 bug，noop cancel 让整个事件链路静悄悄断裂
2. `bus.go:61-68` NewEmitter nil dispatcher 改 panic——事件静悄悄丢失是不可接受的

### P1（本月）
3. `bus.go:38-40` ResilientSubscribe panic 后加 metrics counter
4. `provider.go:156-169` NewCodexNativeToolPolicy 非法 tool ID 加 Warn
5. `provider.go:372-381` NewCapabilityError 入参 trim + 空值 panic
6. `frc.go:26` Normalize KeepRecent 负值 error/panic
7. `provider.go:265-268` `has` 重命名为 `isDisabled`

### P2（下个 sprint）
8. `provider.go:337-361` Session/TurnHandle 接口文档标注 nil 约束
9. `orchestration.go:64-80` OrchestrationService 拆分（长期重构）
10. `bus.go:44-48` recoverCall 加 metrics

## 边界条件

1. **`ResilientSubscribe` nil dispatcher panic 的影响**：当前有多处调用 `ResilientSubscribe(dispatcher, fn, logger)` 其中 dispatcher 来自 fx 注入。如果 fx 图中 dispatcher 是 optional 依赖（`optional:"true"`），nil 是合法的。修复前要 grep 所有调用点确认 dispatcher 是否真的 optional。如果是，保留 noop 但加 Warn。
2. **`NewEmitter` nil dispatcher 的影响**：同上。`NewEmitter` 在 `bus/emitters.go` 中被多处调用，dispatcher 来自 fx 注入。如果 dispatcher 是 optional，nil emitter 是合法的"不发事件"语义。修复方向：如果 dispatcher 是 required，panic；如果 optional，保留 noop 但加 debug log。
3. **`has` 方法重命名**：这是 unexported 方法，只在 `provider.go` 内部使用。重命名不影响外部 API。但要同步更新所有调用点（约 10 处）。
4. **`FRCConfig.Normalize` 的 nil receiver**：这是 Go 的 nil receiver 惯用法——让调用方不需要先判 nil 再调用。当前 `EnabledForModel` 和 `KeepRecentCount` 都依赖这个行为。改 panic 会破坏所有 `cfg == nil` 的合法路径。保持当前行为。
5. **`NewCodexNativeToolPolicy` 非法 ID 静默跳过**：当前设计是"只禁用已知工具"。如果用户配了 `"my_custom_tool"`，它不在 `knownCodexNativeToolIDs` 中，会被跳过。这是合理的——自定义工具不走 native policy。但如果用户拼错了已知工具名（如 `"shel"` 而非 `"shell"`），会被静默忽略。Warn 可以帮助定位。
6. **`ValidateLaunchCWD` 是本轮中少见的正面案例**：它做了完整的 fail-fast 校验（空值、whitespace、相对路径、"." 路径），每种情况都有明确的 error message。可以作为其它校验函数的参考模板。
7. **`OrchestrationService` 接口过大**：20+ 方法让 mock 困难，但拆分是大型重构。当前不影响 fail-fast 审查目标。列为 P2 长期改进。

---

下一轮范围建议：
- `internal/contract/toolbridge.go` + `mcp_control.go`（toolbridge/mcpcontrol 接口定义）
- `internal/contract/skill.go`（skill 接口）
- `internal/contract/prompt.go` + `prompt_attachment.go`（prompt 组装接口）
- 或切换到 `internal/sidecar/lsp/tools/tool_xref.go` + `tool_grep.go`（具体工具实现）
- 或 `internal/platform/shared/`（共享工具函数）
