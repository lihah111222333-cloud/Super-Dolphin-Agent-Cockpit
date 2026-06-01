# Round 027 - 模式归纳：`safeList` / noop-adapter 静默降级反模式

## 系统性问题

多个模块使用"依赖为 nil 时返回空值"的模式，把"系统配置缺失"伪装成"业务数据为空"。

## 受影响位置

| 模式 | 文件 | 行为 |
|------|------|------|
| safeList | dashboard/factory.go:59 | nil store → 空数组 |
| noop facade | app/thread_orchestration_adapter.go:23 | nil svc → 空操作 |
| noop facade | app/runtime_reporter_adapter.go:22 | nil svc → 空操作 |
| nil lookup | app/toolbridge_adapters.go:87 | nil store → nil |
| fallback ctx | app/runner.go:231 | nil provider → Background() |
| empty sink | bus/sink.go:23 | nil dispatcher → 空 sink |
| nil inventory | dashboard/service.go:124 | type assert fail → nil |

## 统一精修方案

### 方案 A：fx 装配期强制（推荐）

```go
// 在 fx module 层
func ProvideXxx(dep Dep) (Xxx, error) {
    if dep == nil {
        return nil, errors.New("xxx: dep required")
    }
    return NewXxx(dep), nil
}
```

### 方案 B：noop-with-error

```go
type noopOrchFacade struct{}
func (noopOrchFacade) LaunchAgent(...) error {
    return errors.New("orchestration: not configured")
}
```

### 方案 C：archtest 守卫

在 `internal/archtest` 加规则：扫描 `if xxx == nil { return` 模式在非 _test.go 文件中的出现，标记为 limitZero。

## 预期影响

- ~7 个 adapter/factory 文件需要修改。
- fx module 声明需要从 `optional:"true"` 改为 required。
- 部分集成测试需要提供 mock 而非 nil。
