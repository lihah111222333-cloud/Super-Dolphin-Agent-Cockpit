# Round 003 - 深入确认 blocker #1~#3

## Findings 确认

### 1. [blocker] platform/db/module.go:306 — rows.Scan 错误静默跳过

```go
for rows.Next() {
    var f string
    if err := rows.Scan(&f); err == nil {  // ← Scan 失败时跳过，不返回 error
        applied[f] = true
    }
}
return applied, nil  // ← 永远返回 nil error
```

**影响**：`getAppliedMigrations` 返回不完整的 applied 集合 → `applyPendingMigrations` 可能重复执行已应用的迁移 → 数据库损坏。

**精修方案**：
```go
if err := rows.Scan(&f); err != nil {
    return nil, fmt.Errorf("scan migration filename: %w", err)
}
applied[f] = true
```

---

### 2. [blocker] module/skill/skills_meta.go:169 — 越界索引

```go
for i := 0; i < len(lines); i++ {
    key, value, ok := parseMetaLine(lines[i])
    if !ok { continue }
    i += applyMetaLine(&info, key, value, lines[i+1:])  // ← i==len(lines)-1 时 lines[i+1:] 为空
}
```

当 `applyMetaLine` 返回 advance > 0 且 `i` 已在末尾时，`i += advance` 使 `i > len(lines)`，下一次循环 `lines[i]` 越界 panic。

**精修方案**：
```go
advance := applyMetaLine(&info, key, value, lines[i+1:])
i += advance
```
外层循环条件已经是 `i < len(lines)`，所以 `lines[i+1:]` 传空切片本身安全；问题在 `applyMetaLine` 返回值可能让 `i` 跳过循环终止条件。加守卫：
```go
if i+1+advance > len(lines) {
    break
}
```

---

### 3. [blocker] module/prompt/user_context_builder.go:257-262 — 无 bounds check

```go
func truncateAtRuneBoundary(content string, limit int) string {
    cut := limit
    for cut > 0 && !utf8.RuneStart(content[cut]) {  // ← limit >= len(content) 时 panic
        cut--
    }
    return content[:cut]
}
```

注释说"调用者保证 limit < len(content)"，但函数本身无守卫。

**精修方案**：
```go
func truncateAtRuneBoundary(content string, limit int) string {
    if limit >= len(content) {
        return content
    }
    cut := limit
    for cut > 0 && !utf8.RuneStart(content[cut]) {
        cut--
    }
    return content[:cut]
}
```
