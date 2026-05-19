### Task 3: 错误处理合规（Panic Count）

**Files:**
- Modify: 违反 `panic_count` 的 15 个文件。

#### 评选唯一修复方案
- **唯一方案**: 对于所有业务逻辑中的显式 `panic`，**唯一正确方案是向上冒泡返回强类型的 `error`**。对于处于程序极早期入口（如 `main.go` 或顶级 runner）的严重配置错误，允许使用顶层分发，但不允许在 `internal` 库层使用。
  - *原因*: Panic 会破坏系统的可用性和错误溯源。严禁使用 `recover()` 进行鸵鸟式掩盖处理。

#### 边界条件 (Boundary Conditions)
1. **签名穿透**: 若将 `panic` 替换为 `return err`，必须保证级联修改所有上游调用者的签名，直到最顶层的 HTTP Handler / CLI Entrypoint 能够接住并安全打印错误日志。
2. **测试断言修正**: 测试代码中原来的 `assert.Panics` 必须同步修改为 `err := Foo(); assert.Error(t, err)`，决不允许因此直接屏蔽报错断言。
3. **不可抗力豁免**: 若必须在非常底层的第三方回调约束（且不支持返回 err）内发生异常，必须提供附带极详尽栈信息的自定义 panic 结构，并立刻在上一级包裹 recover（极其罕见，需慎重）。

- [ ] **Step 1: 定位所有显式 Panic**
排查这 15 个文件中的 `panic()` 调用点。

- [ ] **Step 2: 实施 Error 冒泡**
```go
// 修复后
func ReadConfig() (string, error) {
    data, err := os.ReadFile("config.json")
    if err != nil {
        return "", fmt.Errorf("read config failed: %w", err)
    }
    return string(data), nil
}
```

- [ ] **Step 3: 运行验证**
运行: `make test` 和 `make guard`
Expected: `panic_count` 归零，基线收缩。

- [ ] **Step 4: Commit**
```bash
git add .
git commit -m "refactor: remove explicit panic and enforce error propagation"
```
