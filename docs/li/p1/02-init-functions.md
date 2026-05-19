### Task 2: 初始化与状态机合规（Init Functions）

**Files:**
- Modify: 违反 `has_init` 的 5 个文件及其对应的 DI 注入层。

#### 评选唯一修复方案
- **唯一方案**: 将所有的 `func init()` 改造为**显式构造函数**（如 `NewModule()`），并交由全局 `fx` DI 容器托管生命周期。
  - *原因*: `init()` 执行顺序依赖文件引入顺序，极易引发死锁、隐蔽的空指针异常（依赖未就绪）或破坏测试沙箱隔离性。禁止用 `sync.Once` 来包庇原来的静态注册模式。

#### 边界条件 (Boundary Conditions)
1. **语义守恒**: 提取出的逻辑绝对不允许出现功能丢失（比如原先注册了3个插件，提取后少注册了1个）。
2. **容器封闭性**: `fx` 组装层必须与原包保持同等的模块化隔离，避免引发大面积循环依赖编译失败。
3. **延迟初始化原则**: 如果原来的 `init` 中包含了重度的 IO 阻塞操作（连接 DB 等），重构时必须将其挂载到 `fx.Lifecycle.Append(OnStart)` 钩子中，严禁在构造函数内部直接阻塞。

- [ ] **Step 1: 定位 `init()`**
搜索基线中报告 `has_init` 的文件，找到所有的 `func init()` 函数。

- [ ] **Step 2: 改造为显式构造函数**
```go
// 修复后
func NewMyPlugin() *Plugin {
    return plugin
}
// 配合 fx.Invoke 或者 fx.Hook 来执行注册
```

- [ ] **Step 3: 运行验证**
运行: `make test` 和 `make guard`
Expected: 单元测试正常通过，无初始化缺失报错，`has_init` 违规消失。

- [ ] **Step 4: Commit**
```bash
git add .
git commit -m "refactor: remove init functions and migrate to explicit di"
```
