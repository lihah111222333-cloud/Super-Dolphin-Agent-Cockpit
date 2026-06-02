# Round 025 - 模式归纳：nil-receiver guard 反模式

## 系统性问题

全代码库存在大量 `if s == nil { return zero }` 的 nil-receiver guard 模式。这不是 Go 惯用法的"optional method"，而是掩盖 fx 装配失败的兜底。

## 受影响模块

| 模块 | 文件 | 行为 |
|------|------|------|
| prompt | service.go:104 | 返回零 Config |
| prompt | assembler.go:367 | 吞 invalidation |
| prompt | assembler_support.go:34 | 返回空 attachments |
| skill | service.go:97-119 | 返回 false/0 |
| feedback | service.go:32 | 返回 soft-disabled |
| bus/sink | LogSink.Close():36 | 静默 return |

## 统一精修方案

1. **删除所有 nil-receiver guard**：Go 的 nil pointer dereference 本身就是 fail-fast。
2. **fx 装配期强制非空**：在 `fx.Provide` / `fx.Invoke` 层加 `if svc == nil { return nil, errors.New(...) }`。
3. **archtest 守卫**：在 `internal/archtest` 加规则扫描 `if s == nil` 模式，新增时 CI 报错。

## 预期影响

- 约 15 个文件需要删除 nil-receiver guard。
- fx module 层需要加 ~10 个非空校验。
- 测试中用 nil service 的 case 需要改为 mock。
