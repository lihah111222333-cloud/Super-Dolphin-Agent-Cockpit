# 后端无状态架构契约 (Stateless Convention)

**版本**: V3 Migration
**范围**: 所有后端 Go 代码 (包含 `cmd/*`, `internal/*`, `pkg/*`)

## 一、 核心目标

在 `super-agent-v3` 中，后端的“无状态”（Stateless）架构是一条不可逾越的红线。它的核心目的是：
1. **水平扩展性 (Scale-out)**: 允许在 Load Balancer 后部署多个平行的 Agent 实例。
2. **容灾与恢复 (Resilience)**: 进程崩溃或重启后，不会丢失任何连贯的业务状态。
3. **确定性 (Determinism)**: 任何两次相同的输入应当得到一致的处理结果，而不依赖特定的进程内“隐藏上下文”。

---

## 二、 绝对红线 (Redlines)

### 1. 绝对禁止包级可变全局变量
严禁在包级别声明具有运行时变动性质的全局状态变量，这会破坏实例间的数据一致性。

* ❌ **禁止模式**：
  ```go
  var GlobalCache = make(map[string]interface{})
  var DiskLocks sync.Map
  var DefaultService *MyService
  ```
* ✅ **合法场景**：
  仅允许声明只读常量、错误定义或接口校验断言。
  ```go
  var ErrNotFound = errors.New("not found")
  var _ MyInterface = (*myImpl)(nil)
  ```

### 2. 严禁 Singleton 内部的业务状态污染
通过 `go.uber.org/fx` 注入的 Service 或 Manager 实例虽然在进程内是单例（Singleton），但其**内部不能挂载任何长生命周期的业务数据 Map 或 Slice**。

* ❌ **禁止模式**：
  ```go
  type service struct {
      // 错误：在内存中追踪 Agent 会话状态
      threadAgents map[string]string
      agentReservations map[string]struct{}
  }
  ```
* ✅ **正确模式**：
  任何关联关系、映射表、会话锁定，必须下推到 `store` 层（基于数据库唯一索引或 Redis 等外部组件防碰撞）。

### 3. 禁止野生协程捕获闭包状态
所有生命周期管理的后台任务，禁止使用野生的 `go func()` 逃逸管理。野生协程容易产生内存死角，导致平滑关闭时状态未能及时落库。
* 必须统一使用 `github.com/oklog/run` 的 `Execute/Interrupt` 契约。
* 状态必须显式流转在 `context.Context` 中，或通过函数参数传递。

---

## 三、 状态外置原则 (State Externalization)

对于必须有“状态”的组件（如 Dispatcher、Scheduler），需遵循**基础设施空壳化**原则：

1. **状态机与流转落库**：
   复杂实体的生命周期必须使用 `qmuntal/stateless` 进行全矩阵映射推演。推演产生的状态必须**即刻落库**，不得将“In-flight”的中间态缓存在长跑协程中。
2. **调度器的内存模型**：
   调度器在内存中维护的 `map` 或 `channel` 必须是**“无状态的投递引用”**。它不能作为事实真相（Source of Truth）。
3. **Shutdown Drain 销账机制**：
   当节点收到 Stop 信号触发 `fx.Lifecycle.OnStop` 时，所有的 Dispatcher 必须拥有明确的排水（Drain）期，在宽限期内把尚未处理完的任务交还给数据库，确保零状态丢失。
4. **业务与灰度开关的唯一真相源 (Single Source of Truth for Toggles)**：
   业务开关（Feature Flags）、灰度发布配置以及任何动态配置状态，**必须统一收敛到设置中心（Settings Center）的 DB 配置表中**，作为外部唯一的真相源。**严禁**在进程内存中私自长驻缓存这些开关（除非配套了实时的事件总线/Redis PubSub 失效刷新机制），以防止横向扩展时多节点发生“薛定谔的灰度”割裂。

---

## 四、 审查与验证标准

开发新功能或提交合并请求 (Merge Request) 时，需按以下标准自检：
1. **静态扫描**：执行正则 `^var\s+[a-zA-Z0-9_]+\s*=\s*(make\(map|&sync\.)`，确保没有违规变量。
2. **混沌测试 (Chaos/Red Testing)**：启动该服务的 3 个实例（连同一个 DB），向其并发发送 MCP Tool 调用。如果出现资源抢占失败、读写覆写等脏数据，则说明系统内存中存留了隐式状态，未遵守本契约。
