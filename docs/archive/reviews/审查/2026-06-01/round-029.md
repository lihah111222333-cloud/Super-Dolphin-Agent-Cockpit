# Round 029 - 模式归纳：error-log-then-drop 反模式

## 系统性问题

多处代码在 `if err != nil` 后只 `log.Warn/log.Error` 然后 `return`（不返回 error），让调用方以为操作成功。

## 受影响位置

| 文件 | 行 | 操作 | 严重度 |
|------|-----|------|--------|
| turn/service.go | 442 | dedupe upsert | moderate |
| turn/service.go | 465 | dedupe provider ID | moderate |
| turn/service.go | 483 | dedupe terminal | moderate |
| memory/service.go | 369 | merge write | major |
| memory/auto_dream_task.go | 298 | consolidation | moderate |
| feedback/service.go | 50 | insert | moderate |
| memory/service.go | 348 | dedup check | moderate |

## 统一精修方案

### 判定标准

- **数据写入操作**（DB insert/update、文件写入）：error 必须上抛，让调用方知道持久化失败。
- **观测性操作**（metrics emit、audit log）：可以 log + continue，但级别必须是 Error。
- **幂等性操作**（dedupe mark）：error 应上抛，因为幂等保证断裂。

### 修法

```go
// Before
if err != nil {
    s.logger.Warn("dedupe upsert failed", "error", err)
    return
}

// After
if err != nil {
    return fmt.Errorf("turn dedupe upsert: %w", err)
}
```

## 预期影响

- ~7 个函数签名需要加 error 返回值。
- 调用方需要处理新增的 error 路径。
