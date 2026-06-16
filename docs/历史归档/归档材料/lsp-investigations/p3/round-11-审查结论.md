# 第 11 轮审查结论

## 审查范围

- `internal/sidecar/lsp/middleware/budget.go`（output budget 限制、overflow envelope 构造）
- `internal/sidecar/lsp/middleware/budget_hints.go`（per-tool overflow hint 配置、summary 提取）
- `internal/sidecar/lsp/middleware/recovery.go`（panic recovery middleware）
- `internal/sidecar/lsp/middleware/timeout.go`（tool timeout middleware、callWithRecover、ClampTimeout）
- `internal/sidecar/lsp/middleware/logging.go`（Handler/Middleware 类型定义、Chain、Logging middleware）

> 与第 01 轮覆盖的 `cmd/mcp-lsp/fx.go`、`tools/factory.go` 不重复（本轮聚焦 middleware 包内部实现）。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `budget.go:33-35` `WithOutputBudget` | 兜底 | `if next == nil { return nil }` | nil handler 是装配 bug；返回 nil handler 后 Chain 调用时 nil 会 panic | nil next 应 panic |
| `budget.go:37-44` limit 兜底 | 兜底 | `budget.MaxBytes <= 0` 时 fallback 到 per-tool default 或 64KB | 调用方传 0 或负值是 bug；当作"用默认"掩盖配置错误 | 0 用默认可接受；负值应 panic |
| `budget.go:50-53` fitsBudget | 静默 | `json.Marshal(value)` 失败时 `err == nil` 为 false → 返回 false → 走 overflow 路径 | marshal 失败被当成"超预算"；overflow envelope 内部再次 marshal 也会失败，最终返回一个 `actual_bytes: 0` 的 envelope | marshal 失败应直接 return error，不走 overflow |
| `budget.go:57-60` `fitsBudget` 双重 marshal | 兜底 | 先 marshal 判大小，再在 overflow 里再 marshal 一次 | 性能浪费（大对象 marshal 两次）；且两次 marshal 之间 value 理论不变但如果是 pointer 可能被并发修改 | 改为 marshal 一次，把 raw 传给 overflow |
| `budget.go:62-72` `overflowEnvelope` | 静默 | marshal 失败 → `structuredOverflow(toolName, nil, 0, maxBytes)`；unmarshal 到 map 失败 → `structuredOverflow(toolName, nil, len(raw), maxBytes)` | 两种失败都静默降级成"无 payload 的 overflow"；调用方拿到的 envelope 没有 summary/original_success 等字段 | marshal 失败应 return error；unmarshal 失败至少保留 raw 长度 + 错误信息 |
| `recovery.go:14-37` `Recovery` | 静默 | panic 后转 error 返回；**无 metrics、无 re-panic 选项** | 与 round-01 fx.go:266、round-02 server.go:396 同根问题；panic 被静默转 error | 增加 metrics counter；可选 re-panic 模式（dev build） |
| `recovery.go:14-16` logger nil 兜底 | 兜底 | `if logger == nil { logger = pkglogger.Get() }` | 调用方传 nil 是 bug | 构造期 panic 或强制非 nil |
| `timeout.go:20-22` `Timeout` limit<=0 | 兜底 | `if limit <= 0 { limit = TierNormal }` | 负值 timeout 是调用方 bug；当作 30s 掩盖 | 负值 panic；0 用 default 可接受 |
| `timeout.go:25-43` Timeout handler | 静默 | goroutine 内 `callWithRecover` 的 panic 被 recover 转 error；ctx 取消后 goroutine 仍在跑 | 与 round-06 stdio_mcp_client.readMessage 同根：ctx 取消后 goroutine 泄漏直到 handler 自然返回 | 无法强制杀 goroutine（Go 限制）；但应在 timeout 路径加 metrics 记录"orphaned handler goroutine" |
| `timeout.go:27-29` 提前 ctx.Err 检查 | 兜底 | `if err := timeoutCtx.Err(); err != nil { return nil, err }` | 在 `withToolTimeout` 之后立刻检查——如果 parent ctx 已经 done，直接返回。这是合理的 early-exit，但 error 没有 wrap 成 CodedToolError | 改为 `newToolTimeoutError(limit, err)` 保持一致 |
| `timeout.go:59-66` `callWithRecover` | 静默 | panic 转 `common.NewPanicToolError`；与 Recovery middleware 重复 recover | 双层 recover：Chain 顺序是 `Recovery(Logging(Timeout(handler)))`，Timeout 内部又有 recover。如果 handler panic，Timeout 的 callWithRecover 先捕获，Recovery 永远看不到 | 删除 callWithRecover 的 recover，让 Recovery 统一处理；或反过来删 Recovery |
| `timeout.go:83-98` `ClampTimeout` | 兜底 | fallback<=0 用 TierNormal；ceiling<=0 用 fallback；requestedSeconds<=0 用 fallback | 三层兜底；负值全部静默替换 | 负值应 error/panic |
| `logging.go:17-26` `Chain` | 兜底 | `if middlewares[idx] == nil { continue }` 跳过 nil middleware | nil middleware 是装配 bug | panic |
| `logging.go:28-30` `Logging` logger nil | 兜底 | 同 Recovery | 同上 | 同上 |
| `logging.go:63-69` `compactAny` | 静默 | marshal 失败返回 `"<unmarshalable>"` | 日志中出现 `<unmarshalable>` 时无法定位是哪个字段/类型导致 | 至少包含 `reflect.TypeOf(value).String()` |
| `budget_hints.go:56-61` `lookupHint` | 兜底 | 未知 tool 返回 generic hint | 新增工具忘记加 hint 时用户拿到 generic 提示；不算 bug 但可改进 | 至少 debug log "no specific hint for tool %s" |
| `budget_hints.go:63-98` `extractSummary` | 兜底 | payload==nil 返回空 map；未知 tool 返回空 map | 合理的 fallback；但 grep 路径中 `payload["files"].(map[string]any)` 类型断言失败时静默跳过 | 类型断言失败至少 debug log |
| `budget_hints.go:171-176` `numericField` | 兜底 | key 不存在返回 `0` | 调用方拿到 0 无法区分"字段值为 0"与"字段不存在" | 返回 `any`（当前已是 any），但 key 不存在时返回 nil 而非 0 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `budget.go:50-53` | fitsBudget marshal 失败当成"超预算" |
| `budget.go:62-72` | overflowEnvelope marshal/unmarshal 失败静默降级 |
| `recovery.go:24-33` | panic 转 error 无 metrics |
| `timeout.go:30-43` | ctx 取消后 handler goroutine 泄漏无 metrics |
| `timeout.go:59-66` | callWithRecover 与 Recovery 双重 recover |
| `logging.go:63-69` | compactAny marshal 失败返回 placeholder |
| `budget_hints.go:73-79` | grep files 类型断言失败静默 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `budget.go:33-35` | next=nil 返回 nil handler |
| `budget.go:37-44` | MaxBytes<=0 兜底 |
| `recovery.go:14-16` | logger nil 兜底 |
| `timeout.go:20-22` | limit<=0 兜底 |
| `timeout.go:83-98` | ClampTimeout 三层兜底 |
| `logging.go:17-26` | Chain nil middleware skip |
| `logging.go:28-30` | Logging logger nil 兜底 |
| `budget_hints.go:8-54` | toolOverflowHints 写死 map；新工具需手动加 |
| `budget_hints.go:171-176` | numericField 不存在返回 0 |
| `logging.go:13` `Handler` 类型 | 无 Validate 方法；任何 func 签名匹配就能当 handler |

## 修复优先级

### P0（必须本周修）
1. `budget.go:50-53` fitsBudget marshal 失败必须 return error，不能走 overflow 路径
2. `timeout.go:59-66` 删除 callWithRecover 的 recover，让 Recovery middleware 统一处理 panic（双重 recover 导致 Recovery 永远看不到 panic）
3. `recovery.go:24-33` panic recover 增加 metrics counter（与 round-01/02 同根，本轮确认 middleware 层也有同样问题）

### P1（本月）
4. `budget.go:33-35` next=nil 改 panic
5. `budget.go:57-60` fitsBudget 改为 marshal 一次，raw 传给 overflow（性能 + 一致性）
6. `timeout.go:27-29` 提前 ctx.Err 检查改用 newToolTimeoutError 保持一致
7. `timeout.go:30-43` ctx 取消后 orphaned goroutine 加 metrics
8. `logging.go:17-26` Chain nil middleware 改 panic
9. `recovery.go:14-16` / `logging.go:28-30` logger nil 改 panic

### P2（下个 sprint）
10. `budget.go:62-72` overflowEnvelope marshal 失败路径加错误信息到 envelope
11. `timeout.go:83-98` ClampTimeout 负值 panic
12. `logging.go:63-69` compactAny 失败时包含类型信息
13. `budget_hints.go:171-176` numericField 不存在返回 nil 而非 0
14. `budget_hints.go:56-61` lookupHint 未知 tool debug log
15. `budget_hints.go:73-79` grep files 类型断言失败 debug log

## 边界条件

1. **双重 recover 的修复方向**：当前 Chain 顺序是 `Recovery(Logging(Timeout(handler)))`（在 `factory.go:526-534` 中 `middleware.Chain(handler, Recovery, Logging, Timeout)`，Chain 从后往前 wrap，所以执行顺序是 Recovery → Logging → Timeout → handler）。Timeout 内部的 `callWithRecover` 先于 Recovery 捕获 panic。删除 callWithRecover 的 recover 后，panic 会冒泡到 Timeout 的 goroutine 内部——但 goroutine 内 panic 不会传播到 select 的 caller！所以正确修法是：保留 callWithRecover 的 recover（goroutine 内必须 recover），但把 panic error 写入 resultC 让外层拿到。Recovery 层的 recover 仍保留作为 belt-and-suspenders。
2. **fitsBudget 双重 marshal 的性能影响**：大对象（如 grep 返回 47KB）会被 marshal 两次。改为 marshal 一次后，overflow 路径需要接受 `[]byte` 而非 `any`。这会改变 `overflowEnvelope` 的签名。
3. **Timeout goroutine 泄漏是 Go 的固有限制**：无法强制杀 goroutine。当前设计是"ctx 取消后 handler 自然退出"——如果 handler 内部 honor ctx（如 LSP RPC 调用），goroutine 会很快退出。但如果 handler 阻塞在 IO（如 `os.ReadFile`），goroutine 会一直存活直到 IO 完成。加 metrics 是为了可观测，不是为了修复泄漏。
4. **`numericField` 返回 0 vs nil**：当前 `extractSummary` 把 `numericField` 的结果放入 map[string]any，最终 marshal 成 JSON。返回 0 会输出 `"total": 0`；返回 nil 会输出 `"total": null`。对 LLM 来说 0 比 null 更有意义（"找到 0 个结果"），所以当前行为可能是有意的。修改前确认语义。
5. **`budget_hints.go` 的 toolOverflowHints 写死**：新增工具（如 `format_preview`）不在 map 里会走 generic hint。这不是 bug 但容易遗忘。建议在 `tools.go` 的 manifest 注册时同步注册 hint，或在 CI 中检查所有 tool 都有对应 hint。
6. **Recovery middleware 的 logger nil 兜底**：`wrapToolHandler` 在 `factory.go:526` 调用 `middleware.Recovery(log, toolName)` 时 `log` 来自 `pkglogger.Get()`，理论不会为 nil。改 panic 是 defensive，影响面小。
7. **`overflowEnvelope` 中 `editOverflowEnvelope` 的 payload 字段访问**：直接用 `payload["action"]`、`payload["error"]` 等——如果 payload 是 nil（marshal 失败路径），这些都是 nil。当前 `structuredOverflow` 在 marshal 失败时传 nil payload，`editOverflowEnvelope` 会被调用但 payload 为 nil，所有字段都是 nil。JSON 输出 `"action": null` 等。不会 panic 但信息量为零。

---

下一轮范围建议：
- `internal/module/turn/tool_result_storage.go` + `tool_result_budget.go`（turn 级 tool result 截断与存储）
- 或 `internal/sidecar/lsp/tools/tool_xref.go` + `tool_grep.go`（具体工具实现的入参校验）
- 或 `internal/contract/`（核心接口定义、error 类型、memory 契约）
