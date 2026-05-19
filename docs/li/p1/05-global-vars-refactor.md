### Task 5: 核心架构重构（Global Variables）

**Files:**
- Modify: 违反 `global_vars` 的 46 个文件。此任务改动最广，建议按模块拆分给多个 Agent 认领执行。

#### 评选唯一修复方案
- **唯一方案**: **全面依赖注入 (Dependency Injection)**。将可变状态封装进实例 `struct` 中，通过 `fx.Provide` 托管。只读的不变量改为 `const`。
  - *原因*: V3 架构推崇 stateless 原则。任何包级可变变量 (`var Client`, `var Registry`) 都会破坏测试时的并发执行隔离性（Parallel Testing），引发脏数据甚至竞态崩溃。严禁使用 `sync.Once` 单例模式来伪装解决。

#### 边界条件 (Boundary Conditions)
1. **只读变量转换**: 如果全局 `var` 仅仅用来定义固定错误（如 `var ErrNotFound = errors.New(...)`），这是唯一允许的特例豁免（不包含可变状态），但应当尽量迁移到独立的常量包或错误定义栈。
2. **避免循环依赖**: 在解耦互相引用的全局 Registry 时，极易造成包间的循环引用 (`import cycle`)。必须利用接口（`interface`）在消费端定义契约，或者提取纯净的 `dto` 层来斩断死结，禁止强行耦合两个平级模块。
3. **隔离 `fx` 层**: `fx.Provide` 构造函数签名中必须仅依赖抽象或下层 Provider，不得隐式地去反向加载其他包的变量。

- [ ] **Step 1: 定位并分析全局变量**
排查包级别的 `var` 声明。

- [ ] **Step 2: 实施无状态化与 DI 重构**
```go
// 修复后
const RetryLimit = 3

type Registry struct {
    handlers map[string]Handler
}

func NewRegistry() *Registry {
    return &Registry{handlers: make(map[string]Handler)}
}
// 并通过 fx.Provide(NewRegistry) 注入
```

- [ ] **Step 3: 级联调用清理**
将所有原先直接使用全局变量的调用方，改为从结构体接收器（Receiver）或函数参数中获取依赖。

- [ ] **Step 4: 运行验证**
运行: `make test` 和 `make guard`
Expected: 所有测试通过，`global_vars` 清零。注意测试代码中也需要做对应的依赖注入修改。

- [ ] **Step 5: Commit**
```bash
git add .
git commit -m "refactor: remove package level global variables in favor of fx injection"
```
