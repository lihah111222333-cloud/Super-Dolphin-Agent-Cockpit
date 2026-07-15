# Go 测试规范与常见陷阱 (V3 规范)

> **加载条件**: 编写测试、测试 fx 模块、调试状态机或并发问题时加载。

---

## V3 架构专属测试范式

### 1. fx 依赖注入与架构测试

V3 强制模块间隔离，测试时需验证 `fx.Module` 的依赖方向和启动正常，避免隐式依赖和循环依赖。

```go
func TestModule_DependencyDirection(t *testing.T) {
    // 验证模块构建是否可以独立运行（或带最小 Mock 依赖）
    err := fx.ValidateApp(
        workspace.Module,
        fx.Provide(func() Store { return &mockStore{} }), // 注入 Mock
    )
    if err != nil {
        t.Fatalf("module validation failed: %v", err)
    }
}
```

> [!WARNING]
> 测试中不要手动 `newService(a, b)` 组装复杂的依赖树。如果依赖过多，应该使用 `fxtest.New(t, ...)` 构建局部应用。

### 2. 状态机全矩阵测试 (State Matrix Test)

复杂实体的状态机禁止只测 Happy Path。必须覆盖所有的 `(状态 x 触发器)` 矩阵，确保不可预期的事件被拦截。

```go
func TestStateMachine_Matrix(t *testing.T) {
    cases := []struct {
        from    AgentState
        trigger AgentTrigger
        wantCan bool
    }{
        {from: StateIdle, trigger: TriggerUserPrompt, wantCan: true},
        {from: StateIdle, trigger: TriggerToolFinished, wantCan: false},
        {from: StateRunning, trigger: TriggerToolFinished, wantCan: true},
    }

    for _, tc := range cases {
        t.Run(fmt.Sprintf("%s_on_%s", tc.from, tc.trigger), func(t *testing.T) {
            sm := buildTestMachine(tc.from)
            can, _ := sm.CanFire(tc.trigger)
            if can != tc.wantCan {
                t.Errorf("from=%s trigger=%s can=%v want=%v", tc.from, tc.trigger, can, tc.wantCan)
            }
        })
    }
}
```

---

## 标准 Go 测试规范

### 按语义选择测试形态

当多个 case 共享同一执行逻辑、断言结构和失败语义时使用表驱动测试；单一生命周期序列、并发协调或强时序场景可以使用命名清晰的独立测试，不机械改写成表格。

```go
func TestValidateEmail(t *testing.T) {
    tests := []struct {
        name    string
        email   string
        wantErr bool
    }{
        {"valid email", "user@example.com", false},
        {"missing @", "userexample.com", true},
        {"empty string", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateEmail(tt.email)
            if (err != nil) != tt.wantErr {
                t.Errorf("validateEmail(%q) error = %v, wantErr %v",
                    tt.email, err, tt.wantErr)
            }
        })
    }
}
```

### 测试辅助函数

```go
// MUST 使用 t.Helper() 标记辅助函数
func assertNoError(t *testing.T, err error) {
    t.Helper()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}

// MUST 使用 t.Cleanup() 清理资源
func setupTestDB(t *testing.T) *sql.DB {
    db, err := sql.Open("sqlite", ":memory:")
    if err != nil {
        t.Fatalf("open test sqlite db: %v", err)
    }
    t.Cleanup(func() {
        if err := db.Close(); err != nil {
            t.Errorf("close test sqlite db: %v", err)
        }
    })
    return db
}
```

### 仓库测试入口

- 聚焦包：`./scripts/test_with_guard.sh <affected-packages> -count=1`
- 架构边界：`./scripts/test_with_guard.sh ./internal/archtest -count=1`，必要时追加`make guard`
- 全仓回归：`make test`，由 Makefile 维护显式源码包根和 deferred E2E 串行策略
- 不把裸`go test ./...`或空的`[no test files]`输出作为本仓库完成证据
- 修复守卫时先注入一个真实违规取得 RED，再恢复并取得 GREEN；不得只证明正常输入通过

---

## 常见陷阱与防坑指南

### 竞态条件

并发相关改动必须覆盖仓库登记的并发面。聚焦验证可通过`./scripts/test_with_guard.sh <packages> -race -count=1`运行；提交/推送范围的 race 计划以当前 AI maintenance gate 和`.githooks/README.md`为准，不手写一个会扫描生成物或绕过 deferred E2E 策略的全仓命令。

```go
// ❌ 竞态
var service map[string]net.Addr
func RegisterService(name string, addr net.Addr) {
    service[name] = addr  // 多 goroutine 并发写!
}

// ✅ 加锁
var (
    service   map[string]net.Addr
    serviceMu sync.Mutex
)
func RegisterService(name string, addr net.Addr) {
    serviceMu.Lock()
    defer serviceMu.Unlock()
    service[name] = addr
}
```

### Goroutine 泄漏

```go
// ❌ 无接收者时永久阻塞
func process() {
    ch := make(chan int)
    go func() {
        ch <- expensive()  // 没有接收者，永久阻塞，内存泄露
    }()
}

// ✅ 缓冲 channel 或 select + context 取消
func process() {
    ch := make(chan int, 1)
    go func() {
        ch <- expensive()
    }()
}
```

### 闭包变量捕获

```go
// ❌ 所有 goroutine 共享同一个 v (Go 1.22 之前)
for _, v := range values {
    go func() {
        fmt.Println(v)
    }()
}

// ✅ 传参给闭包
for _, v := range values {
    go func(val string) {
        fmt.Println(val)
    }(v)
}
```

### 必须避免的错误速查

| 陷阱 | 正确做法 |
|------|---------|
| 不检查错误 | ALWAYS 检查并处理 |
| 竞态条件 | mutex 或 channel 同步 |
| Goroutine 泄漏 | context 取消，保障所有路径可退出 |
| 遗忘关闭资源 | `defer file.Close()`，`t.Cleanup()` |
| 测试互相干扰 | 避免修改包级全局变量，或者必须 `t.Cleanup` 还原 |
| nil 接口陷阱 | 明确理解接口零值（类型与值同时 nil 才是 nil） |
