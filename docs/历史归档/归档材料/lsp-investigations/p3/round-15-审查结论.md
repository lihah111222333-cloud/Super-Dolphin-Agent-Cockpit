# 第 15 轮审查结论

## 审查范围

- `internal/module/turn/redaction.go`（DefaultRedactor、Redact、RepoFingerprint）
- `internal/module/turn/trajectory_collector.go`（Collector、NewTrajectoryCollector、Snapshot、Drain、partialTrajectory）
- `internal/module/turn/prompt_assembly.go`（prepareTurnAssembly）
- `internal/module/turn/assembler.go`（inputAssembler、Assemble、PromptText、normalize*、clampString）

> 与第 12-14 轮覆盖的 `tool_result_*`、`service*`、`tracker*`、`manifest`、`skills`、`skill_evaluator`、`skill_extractor`、`prompt_context` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `redaction.go:83-85` `Redact` | 兜底 | `r == nil \|\| len(r.patterns) == 0` 返回原文 + nil hits + nil error | nil receiver 是调用方 bug；返回原文等于**跳过脱敏**，敏感信息直接进入 LLM distillation | nil receiver 应 panic；patterns 为空应 error（构造异常） |
| `redaction.go:103-105` `RepoFingerprint` | 兜底 | 调用 `repofingerprint.MustCompute(cwd)`；如果 cwd 为空/whitespace 返回 "" | 空 cwd 是调用方 bug；返回 "" 让 skill 的 repo scope 为空，可能导致跨 repo 误匹配 | 空 cwd 应 error |
| `trajectory_collector.go:111-121` `NewTrajectoryCollector` | 兜底 | logger nil 兜底全局；contract nil 是合法 optional | logger nil 是调用方 bug | logger nil 应 panic |
| `trajectory_collector.go:126-138` `Snapshot` | 兜底 | turnID 为空返回 `(zero, false)` | 空 turnID 是调用方 bug | panic 或 error |
| `trajectory_collector.go:143-150` `Drain` | 兜底 | `len(c.completed) == 0` 返回 nil | 合理（无完成的 trajectory） | OK |
| `prompt_assembly.go:13-16` `prepareTurnAssembly` | 兜底 | `s == nil \|\| s.promptAssembly == nil` 返回零值 `dto.TurnAssembly{}` + nil error | nil service 是 bug；promptAssembly nil 是合法 optional（NewService 不传）；但返回零值 assembly 让 TurnRequest.TurnAssembly 为空，provider 可能误判"无 prompt" | nil service panic；promptAssembly nil 返回零值是合理的（调用方 PrepareTurn 已经有 Inputs） |
| `assembler.go:48-71` `Assemble` | 兜底 | `len(raw) > maxTurnInputItems` 时截断到 256；normalize 失败的 item 静默 skip；dedup 后为空返回 nil | 截断无日志/metrics；normalize 失败无日志；空结果返回 nil 而非空 slice | 截断时至少 Warn "input items truncated from %d to %d"；normalize 失败 debug log |
| `assembler.go:48-50` 截断 | 静默 | `raw = raw[:maxTurnInputItems]` 无任何提示 | 用户传了 300 个 input items，后 44 个被静默丢弃 | Warn log |
| `assembler.go:56-59` normalize 失败 | 静默 | `if !ok { continue }` | 非法 input item 被静默丢弃；用户不知道哪些 item 没进入 turn | debug log |
| `assembler.go:104-119` `normalize` switch | 兜底 | `default: return normalizeFallbackItem(item)` | 未知 type 走 fallback（先尝试 filecontent，再尝试 mention）；不报错 | 未知 type 至少 debug log |
| `assembler.go:185-190` `normalizeFallbackItem` | 兜底 | content 非空走 filecontent；否则走 mention | 未知 type 的 item 被"猜测"成 filecontent 或 mention；语义可能错误 | 未知 type 应 return (zero, false) + Warn |
| `assembler.go:192-207` `normalizeInputType` | 兜底 | 空字符串当 "text"；未知类型 lowercase 透传 | 空 type 是调用方 bug；未知类型透传后走 default fallback | 空 type 应 error 或至少 Warn |
| `assembler.go:260-269` `clampString` | 兜底 | `limit <= 0` 时返回原字符串 | limit<=0 是调用方 bug | limit<=0 应 panic |
| `assembler.go:251-258` `inputKey` | 弱契约 | 用 `\|` 拼接 type/content/path/url 做 dedup key | 如果 content 或 path 包含 `\|`，会产生 key 碰撞 | 用 hash 或 struct key |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `redaction.go:83-85` | nil receiver 跳过脱敏 |
| `trajectory_collector.go:126-138` | Snapshot turnID="" 返回 false |
| `prompt_assembly.go:13-16` | nil service/promptAssembly 返回零值 |
| `assembler.go:48-50` | 截断 256 无日志 |
| `assembler.go:56-59` | normalize 失败静默 skip |
| `assembler.go:104-119` | 未知 type 走 fallback 无日志 |
| `assembler.go:185-190` | normalizeFallbackItem 猜测类型 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `redaction.go:83-85` | nil receiver 当 noop |
| `redaction.go:103-105` | 空 cwd 返回 "" |
| `trajectory_collector.go:111-121` | logger nil 兜底 |
| `trajectory_collector.go:126-138` | turnID="" 返回 false |
| `prompt_assembly.go:13-16` | nil service/promptAssembly 返回零值 |
| `assembler.go:48-71` | 截断/skip/nil 返回 |
| `assembler.go:192-207` | 空 type 当 "text"；未知类型透传 |
| `assembler.go:260-269` | limit<=0 返回原字符串 |
| `assembler.go:251-258` | inputKey 用 `\|` 拼接有碰撞风险 |
| `assembler.go:209-216` `normalizeInputTarget` | 多值 fallback（first non-empty） |

## 修复优先级

### P0（必须本周修）
1. **`redaction.go:83-85` nil receiver 跳过脱敏**——这是安全问题：nil Redactor 让敏感信息（API key、token）直接进入 LLM distillation pipeline。必须 panic。
2. `assembler.go:48-50` 截断 256 个 input items 无日志——用户数据被静默丢弃

### P1（本月）
3. `redaction.go:103-105` RepoFingerprint 空 cwd 返回 "" 改 error
4. `trajectory_collector.go:111-121` logger nil 改 panic
5. `assembler.go:56-59` normalize 失败加 debug log
6. `assembler.go:185-190` normalizeFallbackItem 改为 return (zero, false) + Warn
7. `assembler.go:192-207` 空 type 改 Warn；未知类型改 Warn + return false
8. `prompt_assembly.go:13-16` nil service panic

### P2（下个 sprint）
9. `assembler.go:260-269` clampString limit<=0 panic
10. `assembler.go:251-258` inputKey 改用 struct key 或 hash
11. `trajectory_collector.go:126-138` Snapshot turnID="" 改 error
12. `assembler.go:104-119` normalize switch default 加 debug log

## 边界条件

1. **`DefaultRedactor.Redact` nil receiver 是文档化的 noop**：注释写 "nil receiver is a documented no-op so callers can plug a zero value for tests / partial wiring"。这是有意设计——测试中不想跑脱敏时传 nil。但生产路径里 nil Redactor 是安全事故。修复方向：保留 nil noop 但在 `DefaultExtractor.Extract` 入口显式校验 `e.redactor != nil`，不让 nil 走到 Redact 调用。
2. **`assembler.go` 截断 256 的设计**：`maxTurnInputItems = 256` 是为了防止模型 context 爆炸。截断是合理的，但用户应该知道。加 Warn 后日志量取决于用户行为——正常使用不会超过 256。
3. **`normalizeInputType` 空字符串当 "text"**：这是为了兼容旧 API（早期 InputItem 没有 Type 字段，默认是 text）。改 error 会破坏旧客户端。建议保留兼容但加 debug log。
4. **`inputKey` 的 `|` 碰撞**：实际 content 是用户输入文本，可能包含 `|`。但 dedup 的目的是"完全相同的 item 不重复发送"，碰撞只会导致"不同 item 被误判为相同"从而少发一个。风险低但存在。
5. **`normalizeFallbackItem` 的猜测逻辑**：当前是为了处理"type 字段拼错"的情况（如 `"typ": "flie"`）。完全拒绝会让用户体验变差。建议保留 fallback 但加 Warn。
6. **`RepoFingerprint` 的 `MustCompute`**：如果 `repofingerprint.MustCompute` 内部对空 cwd panic，那当前已经是 fail-fast。需要确认 MustCompute 的实现——如果它对空 cwd 返回 "" 而非 panic，则本轮的 P1 修复有效。
7. **`Collector.Snapshot` 的 turnID="" 场景**：调用方是 observation 层的 bus callback，turnID 来自事件 payload。如果 provider 发了空 turnID 的事件，Snapshot 返回 false 是合理的（不是 collector 的 bug）。改 panic 可能过于激进——建议改为 debug log + return false。
8. **`prompt_assembly.go` 的零值 TurnAssembly**：当 promptAssembly 未注入时（NewService 路径），TurnRequest.TurnAssembly 为空。provider 端需要处理空 assembly 的情况（用 Inputs 直接构造 prompt）。这是已有的合法路径。

---

下一轮范围建议：
- `internal/contract/` 核心接口（ToolRegistry、Session、TurnHandle、AgentMemory、HookManager 等）
- 或 `internal/module/turn/observation/`（bus provider、memory、subscribers）
- 或 `internal/sidecar/lsp/tools/tool_xref.go` + `tool_grep.go`（具体工具实现的入参校验）
