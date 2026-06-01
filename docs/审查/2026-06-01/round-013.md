# Round 013 - 第二梯队：app/ fx 装配层兜底

## 来源

Round-002 扫雷 agent 报告：app/ 5 条。

## Findings

### 1. [major] app/thread_orchestration_adapter.go:23 — 静默 noop facade（已在 round-005 #9 确认）

### 2. [major] app/modules.go:68 — 注释声明 optional deps 自动降级为 noop 无编译守卫

**证据**：fx 模块声明中 optional 依赖降级为 noop adapter，但无编译期断言确保 noop 实现完整。
**影响**：新增接口方法时 noop adapter 不会编译报错，运行期静默返回零值。
**精修**：每个 noop adapter 加 `var _ Interface = noopXxx{}`。

### 3. [moderate] app/runtime_reporter_adapter.go:22 — 静默 noop 隐藏缺失 orchestration

**证据**：与 #1 同模式，RuntimeReporter 为 nil 时返回 noop。
**精修**：同 #1 方案。

### 4. [moderate] app/toolbridge_adapters.go:87 — 返回 nil AgentThreadLookup

**证据**：store 为 nil 时返回 nil lookup，下游需要 nil check。
**精修**：返回 error 或 noop-with-error。

### 5. [moderate] app/runner.go:231 — RootCtxProvider optional:true fallback context.Background

**证据**：fx optional 依赖未注入时用 `context.Background()`。
**影响**：丢失 trace/cancel 传播链。
**精修**：如果 RootCtxProvider 是 optional，至少 log.Warn 告知使用了 fallback context。
