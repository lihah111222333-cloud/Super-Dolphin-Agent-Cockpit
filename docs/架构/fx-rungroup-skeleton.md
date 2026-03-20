# V3 骨架：`fx` + `run.Group`

## 1. 核心定位

```text
fx        = 工厂（造出零件，接好线路）
oklog/run = 引擎（所有零件一起转，一个停全部停）
```

V3 对两者的职责边界只有一条解释：

- `fx` 负责 Build + Start：构造对象、注入依赖、初始化资源。
- `run.Group` 负责 Run：并行托管所有长跑 goroutine。
- `fx` 负责 Stop：按依赖关系释放资源。

## 2. 三阶段生命周期

### Phase 1: `fx Build + Start`

- 解析配置。
- 创建 `Bus`、`Store`、`StateMachine Service`、`RPC Server`。
- 执行 `fx.Lifecycle.OnStart`，只做资源初始化，不启动无限循环。

### Phase 2: `run.Group Run`

- 收集所有实现 `Run(ctx) error` 的组件。
- 为每个组件注册 `execute/interrupt`。
- 任何一个 runner 返回，触发统一停止。

### Phase 3: `fx Stop`

- 关闭数据库连接池。
- 释放外部连接与缓存。
- 刷新日志和其余清理动作。

## 3. Runner 契约

```go
package app

import (
	"context"
	"log/slog"

	"go.uber.org/fx"
)

type Runner interface {
	Run(ctx context.Context) error
}

type RunnerResult struct {
	fx.Out
	Runner Runner `group:"runners"`
}

type Runtime struct {
	fx.In
	Logger  *slog.Logger
	Runners []Runner `group:"runners"`
}
```

说明：

- 统一接口只允许一个入口：`Run(ctx) error`。
- `fx` 通过 `group:"runners"` 自动收集。
- `run.Group` 不知道业务类型，只托管 `Runner`。

## 4. `main.go` 骨架

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
)

func main() {
	baseLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	baseCtx := context.Background()

	var runtime app.Runtime
	fxApp := fx.New(
		app.Module,
		fx.Populate(&runtime),
	)

	if err := fxApp.Start(baseCtx); err != nil {
		baseLogger.Error("fx start failed", "error", err)
		os.Exit(1)
	}

	if err := app.RunGroup(baseCtx, runtime.Runners); err != nil {
		runtime.Logger.Info("run group exited", "reason", err.Error())
	}

	stopCtx, cancel := context.WithTimeout(baseCtx, 10*time.Second)
	defer cancel()
	if err := fxApp.Stop(stopCtx); err != nil {
		runtime.Logger.Error("fx stop failed", "error", err)
		os.Exit(1)
	}
}
```

## 5. `fx.Module` 分工示例

### `StoreModule`

- 提供 `StoreConfig`
- 提供 `*pgxpool.Pool`
- 提供 `*store.Queries`
- 注册 pool 的 `OnStop`

### `BusModule`

- 提供 `*bus.Bus`
- 统一 typed event 发布/订阅入口

### `RunnerModule`

- 提供 `*runner.Service`
- 内部使用 `stateless` 配置状态机
- 作为 `Runner` 由 app 层适配进 `group:"runners"`

### `RPCModule`

- 提供 typed `handler.Map`
- 提供 `*rpc.Server`
- `rpc.Server` 实现 `Run(ctx) error`

## 6. 六条红线

1. 禁止在 `fx.Provide` constructor 里启动 goroutine、监听端口或永久阻塞。
2. 禁止在 `fx.OnStart` 里跑无限循环；`OnStart` 只做初始化。
3. 禁止绕过 `Runner` 接口手工维护“启动列表”。
4. 禁止在 RPC handler 里直接写 SQL、直接改状态字段或直接起后台任务。
5. 禁止在 `run.Group` 里托管一次性初始化任务或短生命周期函数。
6. 禁止在业务代码中直接混用全局单例和依赖注入对象。

## 7. V2 → V3 对比

| 维度 | V2 | V3 |
| --- | --- | --- |
| 组件创建 | 手写 wiring 较多 | `fx` 模块化装配 |
| goroutine 管理 | 各处自起 goroutine | `run.Group` 统一引擎层 |
| 状态机 | `switch/case` + 分散判断 | `stateless` 声明式迁移表 |
| 事件总线 | 字符串事件 + 动态 payload | `kelindar/event` typed event |
| RPC | 入口与业务耦合偏重 | `handler.Map` + 薄 handler |
| SQL | 手写查询层 | `sqlc` 生成 Queries |

## 8. 集成结论

- `fx` 解决“零件从哪里来、如何接线”。
- `run.Group` 解决“零件如何一起跑、如何一起停”。
- 两者必须串成固定顺序：`fx Build+Start -> run.Group Run -> fx Stop`。
