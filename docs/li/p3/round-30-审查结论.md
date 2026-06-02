# 第 30 轮审查结论

## 审查范围

- `cmd/mcp-lsp/multilsp/manager_retry.go`（canAutoRetryDeadClientRequest、nonReplayableDeadClientError、rebuildClientAfterNonReplayableFailure）
- `cmd/mcp-lsp/middleware/recovery.go`（Recovery middleware）
- `cmd/mcp-lsp/middleware/logging.go`（Chain、Logging、compactAny、compactValue）
- `cmd/mcp-lsp/multilsp/manager_symbols_fallback.go`（fallbackDocumentSymbols、parseMarkdownSymbols、parseJSONSymbols、parseYAMLSymbols、buildLevelSymbols）
- `cmd/mcp-lsp/middleware/budget_hints.go`（lookupHint、extractSummary）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `manager_symbols_fallback.go:39-42` | 协程延迟/兜底 | `os.ReadFile` 同步加载整个文件到内存，无大小上限 | 大文件（>100MB JSON/markdown）OOM；解析阶段持续占用协程，主循环延迟 | 加 `MaxFileSize`（如 10MB）阈值；超阈值返回 fallback declined |
| `recovery.go:26` | 弱契约 | `err = fmt.Errorf("panic recovered: %v", recovered)` | %v 格式化丢失原 panic 类型信息；如果 panic 是 error 类型应保留可识别性 | 改为：先 type-assert error，再 fallback 到 %v |
| `logging.go:17-26` Chain | 静默 | nil middleware 静默 `continue` | 中间件本应注入但 nil（fx 注入 bug），调用链静默缺一环 | 至少 Warn 日志 `slog.Warn("middleware: nil at index N, skipped")` |
| `logging.go:39-43` | 安全风险 | params 原文（截断到 2048）写入 Debug 日志 | 如果 params 包含敏感数据（token / password），写入日志泄露 | 加敏感字段脱敏（参考 OWASP 日志规范）或 only-log-on-error |
| `manager_retry.go:47-49` | 静默 | `replacement == nil` 时返回 `ErrClientClosed` 无附加上下文 | rebuildClientAfterFailure 返回 nil 应该是 bug 或边界 case，但被静默映射为通用错误 | 至少 Warn 日志 + 返回更具体错误（如 `errors.New("rebuild returned nil without error")`） |
| `manager_retry.go:10-33` canAutoRetryDeadClientRequest | 弱契约 | 白名单写死，无文档说明「为何这些方法幂等可重试」 | 未来新增 LSP 方法时容易误加入白名单导致重复执行副作用 | 加注释说明「这些方法是只读查询，dead-client 重试不会引发副作用」 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `manager_symbols_fallback.go:39-50` | 同步 `os.ReadFile` + 正则解析大文件 | 1) 加 `time.Now()` 计时，>1s 打 Warn；2) 加文件 size 限制 + bufio 流式解析 |
| `logging.go:39-58` Logging middleware | 已有 duration_ms 指标——这是正面案例 | 维持现状；建议补 P99 摘要日志（每分钟输出最慢 5 个 tool 调用） |
| `recovery.go:24-32` Recovery middleware | 已有日志，但无 panic 计数器 | 加 `panicCounter atomic.Int64`，每分钟输出 panic 频率，超阈值告警 |
| `manager_retry.go:42-51` rebuildClientAfterNonReplayableFailure | 重建 LSP 客户端可能耗时（启动 LSP 进程） | 加 timeout context；记录重建耗时日志 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `logging.go:20-22` Chain | 中间件 nil 静默 continue |
| `logging.go:64-67` compactAny | json.Marshal 失败返回 `"<unmarshalable>"` 字符串（至少是可见的，但失败原因丢失） |
| `manager_retry.go:47-49` | replacement nil 静默映射为 ErrClientClosed |
| `manager_symbols_fallback.go:33-38` | 语言不支持时 `(nil, false, nil)` —— 这是合理的 fallback declined，非真静默 |
| `budget_hints.go:64-66` extractSummary | payload nil 时返回空 map（合理：hint 是 UX 特性，best-effort） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `recovery.go:14-21` Recovery | 接受可变参数 `toolName ...string` 实际只用第一个；签名不清晰 |
| `logging.go:28-35` Logging | 同上 `toolName ...string` 模式 |
| `manager_retry.go:10-33` | 重试白名单无注释说明幂等性约定 |
| `manager_symbols_fallback.go:39-42` | 无文件 size 限制契约 |

## 修复优先级

### P0（必须本周修）
1. **`manager_symbols_fallback.go:39-42` 无 size 限制**——这是协程延迟和 OOM 的直接来源。LSP fallback 在大文件上同步执行，会阻塞 LSP 主循环。加 `MaxFallbackFileSize = 10*1024*1024` 阈值，超阈值返回 `(nil, true, fmt.Errorf("file too large: %d bytes", size))`。
2. **`logging.go:39-43` params 原文记录**——安全风险。如果 LSP 工具被用于处理含 token 的请求（如认证 LSP 扩展），日志会泄露。改为仅在错误时记录 params，或加脱敏字段列表。

### P1（本月）
3. `recovery.go:26` panic 类型保留：先 `if e, ok := recovered.(error); ok { err = fmt.Errorf("panic recovered: %w", e) }`
4. `logging.go:20-22` Chain nil-middleware 加 Warn 日志
5. `manager_retry.go:47-49` rebuild nil-result 加 Warn 日志 + 具体错误
6. `recovery.go` 加 panicCounter + 每分钟 metrics

### P2（下个 sprint）
7. `manager_retry.go:10-33` 白名单加幂等性注释
8. `recovery.go` / `logging.go` 把 `toolName ...string` 改为明确的 string 参数（破坏性变更，需评估调用方）

## 边界条件

1. **Recovery middleware 的 panic-to-error 转换**：line 26 的 `fmt.Errorf("panic recovered: %v", recovered)` 是合理的兜底，但如果 panic 值是 `error` 类型（很常见，如 sql.ErrNoRows panic 等），应该用 `%w` 保留 errors.Is 链。这是一个细节问题但影响调用方错误处理。
2. **Logging middleware 的 duration 指标是正面案例**：line 38（`start := time.Now()`）+ line 48 / line 55 的 `duration_ms` 是项目内为数不多的「内置耗时可观测性」实践。建议作为模板推广到其他子系统（notify、wakeup_reclaim 等）。
3. **manager_retry 白名单 vs 黑名单**：选择白名单是正确的（防止意外重试有副作用的方法）。但项目快速演进时容易遗漏新增的只读方法（如未来加 `MethodLinkedEditingRange`）。建议结合 LSP spec 的 `Request` vs `Notification` 标记自动派生白名单（虽然这要求 protocol 层暴露 idempotency tag）。
4. **manager_symbols_fallback 的语言专属性**：fallback 仅对 markdown/json/yaml 启用是合理的——其他语言由真实 LSP 处理。但「fallback bool」语义（line 33 第二个返回值）含义不直观：`true` 表示「fallback 已尝试」，`false` 表示「fallback 不适用」。建议改为 `enum FallbackResult { NotApplicable, Succeeded, Failed }` 让契约清晰。
5. **buildLevelSymbols 缩进解析的鲁棒性**：line 142 的 `item.level <= stack[len(stack)-1].level` 用整数比较缩进层级。混合 tab/space 时（YAML 不允许，但实际配置常违反）`indentWidth` 计算可能不一致——但这是 fallback parser 的最佳努力，真正解析应由专属 LSP 完成，可接受。
6. **budget_hints 的设计哲学**：这是个工具辅助层（result too large 时给 LLM 提示如何缩小查询），不是核心 fail-fast 路径，所以静默兜底（line 56-61 unknown tool fallback）是合理的。审查中将其归为正面案例。

---

**本轮总结**：发现 1 个 P0 协程延迟问题（manager_symbols_fallback 无 size 限制 → 大文件 OOM 阻塞主循环）、1 个 P0 安全问题（logging middleware 记录 params 原文）。Logging middleware 的 duration_ms 是项目少数正面可观测性实践，建议推广。Recovery middleware 缺 panic 计数器，无法监控 panic 频率。

**累计进度**：30 轮完成。cron `fd4b4728` 继续推进。
