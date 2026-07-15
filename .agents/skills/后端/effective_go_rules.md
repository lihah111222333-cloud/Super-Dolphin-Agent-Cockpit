# Go 惯用写法的仓库增量

> **加载条件**：需要快速确认通用 Go 写法时加载。这里只记录会影响本仓库实现和门禁的增量，不复制一份可能漂移的语言教程。

## 事实源

- Go 语言版本、toolchain 和依赖要求始终读取根`go.mod`；技能不得硬编码未来版本。
- 格式和注释门禁以`gofmt`、`make guard`及当前 archtest/guard 实现为准。
- 依赖方向以 backend boundary registry、`internal/archtest`和`docs/doc/codemap/13-archtest-boundaries.md`导航结果为准。
- 测试入口以`scripts/test_with_guard.sh`和 Makefile 为准。

## 格式与命名

- Go 文件必须通过`gofmt`；导入整理可使用`goimports`，但不要对全仓无差别重写。
- 标识符使用 MixedCaps；包名短小、小写且不使用下划线。
- 不遮蔽`new`、`len`、`make`、`copy`、`error`等内置标识符。
- 仓库没有独立的 120 字符硬门禁；在语义边界断行，以可读性和现有格式化/复杂度守卫为准。

## 错误

- 每个返回 error 的调用都要处理；有意忽略的 close/shutdown 错误必须走仓库明确的观测 helper，不能裸`_ = err`。
- 同一抽象内直接返回，跨 owner 且需要定位上下文时使用`fmt.Errorf("context: %w", err)`包装一次。
- 判断错误使用`errors.Is` / `errors.As`，不要比较完整字符串。
- service 返回 transport-neutral 错误；协议 code 在 adapter/middleware 映射。

## 接口与构造

- 接口优先由消费方定义，并保持满足真实消费者所需的最小方法集。
- 构造函数是否返回接口或具体类型取决于调用方，不把“永远返回接口/具体类型”写成机械规则。
- 运行时依赖、配置和可变状态显式注入并验证；缺失时返回错误，不提供静默默认值。

## 控制流与并发

- 错误分支尽早返回，让成功路径向下流动。
- 长生命周期工作实现`platformrunner.Runner`并接入`group:"runners"`；短生命周期 goroutine 也必须有 owner、上限、取消和等待路径。
- channel 与 mutex 按真实所有权选择；禁止持锁调用网络、RPC 或未知 callback。

## 零值

`bytes.Buffer`、slice 等基础类型可以设计为零值可用。配置、身份、生命周期组件、持久化端口和外部资源不得把零值解释成默认配置或延迟注入；这些类型必须通过显式构造和 Fail-Fast 校验。
