# `oklog/run` 契约（快速参考）

> **当前规范以本文和 `docs/架构/skeleton-rungroup.md` / `docs/架构/fx-rungroup-skeleton.md` 为准；`docs/plans/v3-migration-plan.md` 仅作历史迁移背景。**
> 本文是快速参考卡，覆盖 run.Group 的核心范式和反模式。

## 1. 选型理由

| 方案 | 结论 | 说明 |
| --- | --- | --- |
| `github.com/oklog/run` | 采用 | `execute/interrupt` 二元模型非常适合托管长跑 goroutine，退出语义明确。 |
| `errgroup.Group` | 不作为主编排 | 更适合短生命周期并发任务，对不可感知 `context` 的资源不够直接。 |
| `sync.WaitGroup` | 不采用 | 只有等待，没有统一失败传播和中断协议。 |
| 手写 supervisor | 不采用 | 容易把退出顺序、错误传播和信号处理写散。 |

V3 把 `run.Group` 定义为引擎层：所有长跑部件一起转，一个停全部停。

## 2. Paradigm

### 2.1 每个 actor 都必须有 `execute` 和 `interrupt`

```go
package main

import (
	"net"

	"github.com/oklog/run"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	var g run.Group
	g.Add(
		func() error {
			_, err := ln.Accept()
			return err
		},
		func(error) {
			_ = ln.Close()
		},
	)

	_ = g.Run()
}
```

### 2.2 用统一 `Runner` 桥接业务组件

```go
package main

import (
	"context"
	"time"

	"github.com/oklog/run"
)

type Runner interface {
	Run(ctx context.Context) error
}

type Worker struct{}

func (Worker) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	var g run.Group
	for _, runner := range []Runner{Worker{}, Worker{}} {
		r := runner
		g.Add(func() error { return r.Run(ctx) }, func(error) { cancel() })
	}
	g.Add(func() error { <-ctx.Done(); return nil }, func(error) { cancel() })
	_ = g.Run()
}
```

### 2.3 信号处理也是 actor，不是散落的 goroutine

```go
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/oklog/run"
)

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	var g run.Group
	g.Add(
		func() error {
			sig := <-signals
			return fmt.Errorf("received %s", sig)
		},
		func(error) {
			signal.Stop(signals)
		},
	)

	_ = g.Run()
}
```

## 3. Best Practice

- 每个 actor 都要能被外部中断，不要写“只能自然结束”的循环。
- `interrupt` 只做停止动作，不做复杂业务。
- `run.Group` 只托管长跑组件，不托管一次性初始化任务。
- 所有 actor 共享一个顶层退出语义，一个失败触发全部停止。
- 对 listener、ticker、subscription 这类资源，`interrupt` 必须显式关闭。

## 4. Anti-pattern

- 在 `execute` 里再自己起匿名 goroutine，让 `run.Group` 失去托管权。
- 把 `fx.OnStart` 当成引擎层，直接在里面跑无限循环。
- 用 `run.Group` 包一堆瞬时函数，把它写成 `errgroup` 的替代品。
- `interrupt` 里做阻塞网络调用或数据库清理，拖长停止路径。

## 5. 与其他 5 个框架的集成方式

- 与 `fx`：`fx` 只构造和注入 `Runner`，真正运行在 `run.Group`。
- 与 `jrpc2`：RPC server 作为一个 actor，listener 关闭由 `interrupt` 触发。
- 与 `kelindar/event`：事件 loop 订阅取消动作放在 `interrupt` 或 `ctx.Done` 路径。
- 与 `qmuntal/stateless`：状态机 service 可以是 actor，但状态配置本身不是 actor。
- 与 `sqlc`：数据库 pool 由 `fx` 生命周期管理，不由 `run.Group` 负责创建。
