# Round 032 - archtest 守卫规则提案

## 目的

为 round-025~028 归纳的 4 类反模式设计 archtest 守卫规则，确保精修后不再回退。

## 规则 1：禁止 json.Marshal/Unmarshal 错误丢弃

```go
// internal/archtest/json_error_guard_test.go
func TestNoDroppedJSONErrors(t *testing.T) {
    pattern := regexp.MustCompile(`(?:_\s*(?:,\s*_)?\s*[:=]\s*json\.(?:Marshal|Unmarshal))`)
    violations := scanFiles(t, "internal/", pattern, excludeTests)
    assertZeroViolations(t, "json_error_dropped", violations)
}
```

## 规则 2：禁止 unchecked type assertion

```go
// internal/archtest/type_assert_guard_test.go
func TestNoUncheckedTypeAssert(t *testing.T) {
    // 匹配 `x.(Type)` 不在 `v, ok :=` 模式中的情况
    pattern := regexp.MustCompile(`[^,]\s*:?=\s*\w+\.\([A-Z]`)
    violations := scanFiles(t, "internal/", pattern, excludeTests)
    assertZeroViolations(t, "unchecked_type_assert", violations)
}
```

## 规则 3：禁止 nil-receiver guard

```go
// internal/archtest/nil_receiver_guard_test.go
func TestNoNilReceiverGuard(t *testing.T) {
    pattern := regexp.MustCompile(`if\s+\w+\s*==\s*nil\s*\{\s*return`)
    // 只扫描 method receiver 函数内的第一行
    violations := scanMethodFirstLine(t, "internal/", pattern, excludeTests)
    assertZeroViolations(t, "nil_receiver_guard", violations)
}
```

## 规则 4：禁止 safeList / noop-when-nil 模式

```go
// internal/archtest/noop_fallback_guard_test.go
func TestNoNoopFallbackOnNilDep(t *testing.T) {
    // 扫描 fx Provide 函数中 `if xxx == nil { return noop/empty }`
    pattern := regexp.MustCompile(`if\s+\w+\s*==\s*nil\s*\{\s*return\s+(?:noop|&?\w*\{\})`)
    violations := scanFiles(t, "internal/app/", pattern, excludeTests)
    assertZeroViolations(t, "noop_fallback", violations)
}
```

## 落地步骤

1. 精修完成后，先加规则到 `internal/archtest/`。
2. 运行 `make guard` 确认 baseline 为 0。
3. 后续任何新增违例会被 CI 拦截。
