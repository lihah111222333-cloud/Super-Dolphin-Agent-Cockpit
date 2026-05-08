# 上下文优化：Per-Tool Budget + 结构化截断 — 实施计划 v2

## 背景

对比 go-agent-v2 发现，Super-Dolphin 的工具结果预算过于宽松（64KB 统一上限 vs go-agent-v2 的 8-16KB per-tool），且超预算时只返回空壳，不给 LLM 引导信息。

## 现状

| 工具 | 当前 budget | 当前 overflow 处理 |
|---|---|---|
| lsp_grep | 64KB | grepOverflowEnvelope（保留 total/hint） |
| lsp_file | 64KB | defaultOverflowEnvelope（空 data + message） |
| lsp_inspect | 无 | — |
| lsp_xref | 无 | — |
| lsp_structure | 无 | — |
| lsp_edit | 无 | — |
| lsp_completion | 无 | — |
| code_run | 无 | — |
| code_run_test | 无 | — |

---

## 改动 4 项

### 1. Per-tool byte budget + 全工具覆盖

**文件**: middleware/budget.go

新增 per-tool 默认 budget 表（初始值，可根据实际 overflow 频率调优）：

```go
var defaultToolBudgets = map[string]int{
    "lsp_grep":       24 * 1024,  // 24KB（从 64KB 下调；配合渐进裁剪）
    "lsp_file":       16 * 1024,  // 16KB（从 64KB 下调）
    "lsp_inspect":     8 * 1024,  //  8KB（新增）
    "lsp_xref":       16 * 1024,  // 16KB（新增）
    "lsp_structure":  16 * 1024,  // 16KB（新增）
    "lsp_edit":       16 * 1024,  // 16KB（新增）
    "lsp_completion": 16 * 1024,  // 16KB（新增）
    "code_run":       32 * 1024,  // 32KB（新增，shell 输出较重要）
    "code_run_test":  32 * 1024,  // 32KB（新增）
}
```

修改 WithOutputBudget 签名，增加 toolName string 参数：
- 如果 Budget.MaxBytes > 0，优先使用（显式覆盖）
- 否则查 defaultToolBudgets[toolName]
- 兜底用 defaultOutputBudget (64KB)

**文件**: tools/factory.go

在 wrapToolHandler 中统一加 WithOutputBudget（Chain 外层单独调用，不放入 Chain 内——Chain 的中间件签名是柯里化 func(Handler) Handler，而 WithOutputBudget 是直接包装签名）：

```go
func wrapToolHandler(toolName string, tier time.Duration, handler Handler) Handler {
    log := pkglogger.Get()
    chained := middleware.Chain(
        handler,
        middleware.Recovery(log, toolName),
        middleware.Logging(log, toolName),
        middleware.Timeout(tier),
    )
    return middleware.WithOutputBudget(toolName, chained, middleware.Budget{})
}
```

lsp_grep 和 lsp_file 的 NewXxxHandler 中需移除各自的 WithOutputBudget 调用（避免双层嵌套）。

**影响的文件**:
- middleware/budget.go — 新增 budget 表 + 修改 WithOutputBudget 签名
- tools/factory.go — wrapToolHandler 加 WithOutputBudget
- tools/tool_grep.go:51-61 — 移除 WithOutputBudget 包装
- tools/tool_file.go:91-99 — 移除 WithOutputBudget 包装

**风险**: 中。Budget 下调会触发更多 overflow，必须配合第 2 项（结构化信封）一起上

---

### 2. 结构化截断信封（hint + next_action + summary）

**文件**: middleware/budget.go

重构 overflowEnvelope，按 toolName 路由（不按 payload shape 猜测）：

```go
func overflowEnvelope(toolName string, value any, maxBytes int) map[string]any {
    raw, err := json.Marshal(value)
    if err != nil {
        return structuredOverflow(toolName, nil, 0, maxBytes)
    }
    var payload map[string]any
    if err := json.Unmarshal(raw, &payload); err != nil {
        return structuredOverflow(toolName, nil, len(raw), maxBytes)
    }
    return structuredOverflow(toolName, payload, len(raw), maxBytes)
}

func structuredOverflow(toolName string, payload map[string]any, actualBytes, budgetBytes int) map[string]any {
    hint := lookupHint(toolName)
    envelope := map[string]any{
        "error_code":   "result_too_large",
        "tool":         toolName,
        "actual_bytes": actualBytes,
        "budget_bytes": budgetBytes,
        "summary":      extractSummary(toolName, payload),
        "hint":         hint.Hint,
    }
    if hint.NextAction != nil {
        envelope["next_action"] = hint.NextAction
    }
    return envelope
}
```

**新增文件**: middleware/budget_hints.go — per-tool hint/next_action 模板 + summary 提取

所有 9 个工具的 hint + generic fallback：

```go
type toolOverflowHint struct {
    Hint       string
    NextAction map[string]any
}

var toolOverflowHints = map[string]toolOverflowHint{
    "lsp_grep": {
        Hint: "Narrow search: add path/glob filter, or reduce max_results",
        NextAction: map[string]any{
            "tool": "lsp_grep",
            "suggest_args": map[string]any{"max_results": 10},
            "tip":  "Scope search to a subdirectory or single file",
        },
    },
    "lsp_file": {
        Hint: "Use offset/limit pagination to read file in chunks",
        NextAction: map[string]any{
            "tool": "lsp_file",
            "suggest_args": map[string]any{"limit": 100},
            "tip":  "Read a specific range with offset and limit",
        },
    },
    "lsp_inspect": {
        Hint: "Hover result is large; try a more specific location",
    },
    "lsp_xref": {
        Hint: "Use compact verbosity or reduce max_results",
        NextAction: map[string]any{
            "tool": "lsp_xref",
            "suggest_args": map[string]any{"verbosity": "compact", "max_results": 10},
        },
    },
    "lsp_structure": {
        Hint: "Use compact verbosity or limit to document_symbol action",
    },
    "lsp_edit": {
        Hint: "Edit result truncated; check success/applied fields for status",
    },
    "lsp_completion": {
        Hint: "Too many completions; use a more specific prefix",
        NextAction: map[string]any{
            "tool": "lsp_completion",
            "suggest_args": map[string]any{"max_results": 10},
        },
    },
    "code_run": {
        Hint: "Command output too large; pipe through head/tail or redirect to file",
    },
    "code_run_test": {
        Hint: "Test output too large; run a single test function or check -v flag",
    },
}

func lookupHint(toolName string) toolOverflowHint {
    if h, ok := toolOverflowHints[toolName]; ok {
        return h
    }
    return toolOverflowHint{Hint: "Result too large; try narrowing the query"}
}
```

**summary 提取逻辑** — extractSummary 按 toolName switch：

```go
func extractSummary(toolName string, payload map[string]any) map[string]any {
    if payload == nil { return map[string]any{} }
    switch toolName {
    case "lsp_grep":
        s := map[string]any{"total": numericField(payload, "total"), "showing": numericField(payload, "showing")}
        if files, ok := payload["files"].(map[string]any); ok {
            names := make([]string, 0, 5)
            for k := range files {
                names = append(names, k)
                if len(names) >= 5 { break }
            }
            s["top_files"] = names
        }
        return s
    case "lsp_xref":
        return map[string]any{"total": numericField(payload, "total"), "showing": numericField(payload, "showing")}
    case "lsp_edit":
        return map[string]any{"success": payload["success"], "applied": payload["applied"], "action": payload["action"]}
    default:
        return map[string]any{}
    }
}
```

**风险**: 低。新增函数和数据表

---

### 3. 渐进式负载裁剪（grep 专用）

**文件**: tools/tool_grep.go

在 buildGrepResponse 返回后加入 byte-size 裁剪循环：

```go
func capGrepResponseBytes(resp *grepResponse, maxBytes int) {
    for {
        raw, err := json.Marshal(resp)
        if err != nil || len(raw) <= maxBytes {
            return
        }
        if !dropLastGrepRow(resp) {
            return
        }
        resp.Truncated = true
    }
}

func dropLastGrepRow(resp *grepResponse) bool {
    // 找 rows 最多的文件，删除最后一条
    // 如果所有文件只剩 1 条，删除整个文件
    // 更新 resp.Showing
    // 返回 false 表示无法再删
}
```

**调用位置**: handleGrep 中 buildGrepResponse 返回后、return 之前。maxBytes 从 defaultToolBudgets["lsp_grep"] 获取。

**为什么不在 FilterAndCapSearchMatches 中做**: count cap 和 byte cap 是不同层次。count cap 在去重排序后做，byte cap 在序列化后做。职责分离。

**渐进裁剪 vs budget middleware 的关系**: 渐进裁剪在 handler 内部做，尽量保留最多数据；budget middleware 在外层做最终兜底。两者互补：
- 渐进裁剪成功 → 结果在 budget 内 → middleware 直接放行
- 渐进裁剪后仍超（极端情况）→ middleware 触发 overflow 信封

**风险**: 低。纯新增逻辑

---

### 4. lsp_edit 智能截断

**文件**: middleware/budget.go（或 budget_hints.go）

新增 editOverflowEnvelope。当 lsp_edit 结果超预算时，保留关键状态 + 居中 ~2KB edit_context：

```go
func editOverflowEnvelope(toolName string, payload map[string]any, actualBytes, budgetBytes int) map[string]any {
    hint := lookupHint(toolName)
    envelope := map[string]any{
        "error_code":   "result_too_large",
        "tool":         toolName,
        "actual_bytes": actualBytes,
        "budget_bytes": budgetBytes,
        "hint":         hint.Hint,
        // 保留关键状态
        "success":              payload["success"],
        "action":               payload["action"],
        "status":               payload["status"],
        "applied":              payload["applied"],
        "applied_count":        payload["applied_count"],
        "persisted":            payload["persisted"],
        "diagnostic_generation": payload["diagnostic_generation"],
    }
    // 从 edit_context 中截取编辑位置附近 ~2KB
    if ctx, ok := payload["edit_context"].(string); ok && len(ctx) > 2048 {
        mid := len(ctx) / 2
        start := max(0, mid-1024)
        end := min(len(ctx), mid+1024)
        envelope["edit_context"] = ctx[start:end]
    } else if ok {
        envelope["edit_context"] = ctx
    }
    // 保留行号元数据（不占空间）
    for _, key := range []string{"func_start", "func_end", "affected_start_line", "affected_end_line"} {
        if v, ok := payload[key]; ok {
            envelope[key] = v
        }
    }
    // 丢弃大字段: replaced, replacement, func_body, workspace_edit
    return envelope
}
```

**overflowEnvelope 中按 toolName 路由**:

```go
func structuredOverflow(toolName string, payload map[string]any, actualBytes, budgetBytes int) map[string]any {
    switch toolName {
    case "lsp_edit":
        return editOverflowEnvelope(toolName, payload, actualBytes, budgetBytes)
    default:
        // 通用结构化信封（含 hint + summary）
        ...
    }
}
```

**风险**: 低。只在 lsp_edit 超预算时触发

---

## 兼容性处理

### structuredContent 适配

overflow 信封本身是合法 JSON，server.go/http_transport.go 中 json.RawMessage(raw) 直接放入 structuredContent，不需要特殊处理。

### 前端摘要适配

format-utils.js 中 knownToolSummary 需要兼容 overflow 信封：

```js
// 通用: 在 knownToolSummary 顶部检测 overflow 信封
if (result?.error_code === 'result_too_large') {
    const hint = result?.hint || '请缩小范围';
    return `结果过大（${hint}）`;
}

// lsp_grep: 兼容 overflow 信封中的 summary.total
const total = Number(result?.total ?? result?.summary?.total ?? result?.count);
```

---

## 不做的

| 项目 | 原因 |
|---|---|
| ML 工具调用拦截器 | 复杂度高，需单独排期 |
| 非 grep 工具的渐进裁剪 | lsp_xref/lsp_structure 有 count cap（50），结果通常很小 |
| 修改下游 CaptureToolResult | 保留为安全网 |

---

## 实施顺序

```
Phase 1 — 基础设施（必须先做）:
  1a  middleware/budget.go 新增 per-tool budget 表 + WithOutputBudget 加 toolName 参数
  1b  middleware/budget_hints.go 新文件：hint 模板（9 个工具 + generic fallback）+ extractSummary
  1c  budget.go overflowEnvelope 重构为按 toolName 路由的 structuredOverflow
  1d  budget.go editOverflowEnvelope 智能截断

Phase 2 — 全工具覆盖（步骤必须原子执行）:
  2a  factory.go wrapToolHandler 统一加 WithOutputBudget（Chain 外层）
  2b  tool_grep.go 移除 NewGrepHandler 中的 WithOutputBudget 包装
  2c  tool_file.go 移除 NewFileHandler 中的 WithOutputBudget 包装

Phase 3 — 渐进裁剪:
  3   tool_grep.go 新增 capGrepResponseBytes + dropLastGrepRow

Phase 4 — 前端兼容:
  4   format-utils.js knownToolSummary 兼容 overflow 信封

Phase 5 — 验证:
  go test ./cmd/mcp-lsp/...
  go vet ./cmd/mcp-lsp/...
```

## 受影响的文件

| 文件 | 改动 |
|---|---|
| middleware/budget.go | per-tool budget 表 + WithOutputBudget 加 toolName + structuredOverflow + editOverflowEnvelope |
| middleware/budget_hints.go | 新文件：hint 模板 + lookupHint + extractSummary |
| tools/factory.go | wrapToolHandler Chain 外层加 WithOutputBudget |
| tools/tool_grep.go | 移除 WithOutputBudget + 新增 capGrepResponseBytes |
| tools/tool_file.go | 移除 WithOutputBudget |
| format-utils.js | knownToolSummary 顶部加 overflow 检测 |

## 受影响的测试

| 文件 | 状态 | 需要的动作 |
|---|---|---|
| middleware/budget_test.go | 不存在 | 新建：测试 structuredOverflow、editOverflowEnvelope、per-tool budget 查找 |
| tools/tool_grep_test.go | 不存在 | 新建：测试 capGrepResponseBytes 渐进裁剪 |
| tools/tool_middleware_test.go | 已存在 | 检查是否受 WithOutputBudget 签名变更影响 |

## 回滚策略

- per-tool budget 表值全设为 64KB → 回退到现有行为
- structuredOverflow 信封 fallback 到 defaultOverflowEnvelope → 与现有行为一致
- 渐进裁剪只在超 budget 时触发，不影响正常流程
- budget 值标注为"初始值，可根据实际 overflow 频率调优"
