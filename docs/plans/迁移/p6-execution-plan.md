# P6 执行计划 — Wails v3 入口层集成

> 生成时间：2026-03-21
> 前置：P5 RPC 层已收官，80+ handler 已注册且全量验证通过

---

## 1. 目标

将 V3 的 headless `app.Run()` 升级为 Wails v3 桌面应用，前端通过 Wails 绑定调用后端 jrpc2 handler.Map。

**核心原则**：V3 已有完整 fx + run.Group + jrpc2 + event bus 骨架，P6 只做**薄装配层**，不引入新的业务逻辑。

---

## 2. V2 架构分析

V2 `cmd/agent-terminal/` 共 ~1500 行，结构如下：

| 文件 | 行数 | 职责 |
|---|---|---|
| main.go | 327 | 入口：信号处理 + DB + apiserver + 调用 runMainDesktopApplication |
| main_setup.go | 490 | setupDatabase / setupAppServer / runMainDesktopApplication / Wails application.New |
| app.go | 301 | App struct（Wails 绑定）：CallAPI / LaunchAgent / ListAgents / StopAgent / ... |
| app_handlers.go | ~200 | bridge notification → Wails event 转发 |
| app_helpers.go | ~200 | UI 工具函数 |

**V2 的 Wails 集成模式**：
1. `main()` 手工创建 apiserver + AgentManager
2. `App` struct 持有 `*apiserver.Server` + `*runner.AgentManager`
3. 前端 `window.go.main.App.CallAPI(method, params)` → `apiserver.InvokeMethod()`
4. 后端 notification → `App.handleBridgeNotification()` → `wailsApp.Event.Emit()`

---

## 3. V3 架构优势

V3 已经有完整的 fx DI + jrpc2 handler.Map + event bus → push bridge，P6 只需：

| V2 手工做的 | V3 已有 | P6 需要做的 |
|---|---|---|
| apiserver 手工创建 | fx.Provide + handler.Map | 只需把 handler.Map 暴露给 Wails binding |
| AgentManager 手工创建 | orchestration.Service via fx | 不需要直接持有 |
| notification hook 手工设 | bus → push bridge 已接通 | 把 push bridge 改为也推 Wails events |
| 信号处理手工做 | fx.Lifecycle + run.Group | 只需桥接 Wails lifecycle → fx lifecycle |
| DB 手工创建 | platform/db via fx | 不需要额外做 |

---

## 4. 三个执行批次

### 批次 A：Wails 薄绑定层（核心）

**新建 `internal/ui/wails/` 目录**，包含：

| 文件 | 职责 | 预估行数 |
|---|---|---|
| module.go | fx.Module：提供 WailsApp + Binding | ~20 |
| binding.go | App struct（Wails 绑定）：CallAPI / GetBuildInfo / GetGroup | ~80 |
| bridge.go | bus event → Wails event 转发（复用现有 push bridge 模式）| ~60 |
| lifecycle.go | Wails lifecycle → fx lifecycle 桥接（ShouldQuit / OnShutdown）| ~80 |
| window.go | 主窗口创建 + 配置 | ~40 |

**关键设计**：

```go
// binding.go — Wails 绑定，前端通过 window.go.main.App.XXX() 调用
type App struct {
    dispatch func(ctx context.Context, method string, params json.RawMessage) (any, error)
    emitter  func(event string, data any)  // wails event emit
}

// CallAPI 通用 JSON-RPC 桥 — 覆盖全部后端功能
func (a *App) CallAPI(method string, paramsJSON string) (any, error) {
    return a.dispatch(context.Background(), method, json.RawMessage(paramsJSON))
}
```

`dispatch` 由 fx 注入，直接调用 `rpc.Server` 的 handler.Map 分发。
前端不需要知道后端是 jrpc2 还是其他 — 只管 `CallAPI(method, params)`。

### 批次 B：cmd/agent-terminal 改造

**修改 `cmd/agent-terminal/main.go`**：

从当前的：
```go
func main() {
    app.Run()  // headless fx app
}
```

改为：
```go
func main() {
    app.RunDesktop()  // Wails desktop app
}
```

**修改 `internal/app/`**：

| 文件 | 改动 |
|---|---|
| app.go | 新增 `RunDesktop()`：在 `NewApp()` 的 fx.Options 中加入 `wails.Module` |
| modules.go | 条件引入 wails module（desktop 模式）或 headless 模式（MCP server 等） |
| runner.go | 不变（Wails runner 通过 group:"runners" 自动加入 run.Group）|

### 批次 C：前端资产 + Shutdown + 测试

| 项 | 内容 |
|---|---|
| embed 前端 | `//go:embed all:frontend` 嵌入 Vue 前端资产 |
| 窗口配置 | 1440x900 / 800x600 min / 暗色背景 / 文件拖放 |
| Shutdown | ShouldQuit → 活跃 agent 检查 → quit overlay → fx.Shutdowner |
| Signal | SIGINT/SIGTERM → fx graceful shutdown |
| Smoke test | desktop boot → CallAPI("thread/start") → verify response |

---

## 5. 并行策略

```
批次 A（ui/wails/ 新建）────┐
批次 B（app/ 改造）──────────┼── A 先行，B 依赖 A 的 module
批次 C（assets + shutdown）──┘   C 可与 A 并行
```

建议：**2 Agent 串行**（A→B）+ **1 Agent 并行**（C）

或者 A+C 一个 Agent（都是 ui/wails 新建），B 单独一个 Agent。

---

## 6. V2 参考文件

| V2 文件 | V3 对应 | 用途 |
|---|---|---|
| cmd/agent-terminal/main.go | cmd/agent-terminal/main.go | 入口对照 |
| cmd/agent-terminal/main_setup.go:372-490 | internal/ui/wails/lifecycle.go | Wails app 创建 + lifecycle |
| cmd/agent-terminal/app.go | internal/ui/wails/binding.go | Wails 绑定方法 |
| cmd/agent-terminal/app_handlers.go | internal/ui/wails/bridge.go | event → Wails 转发 |

---

## 7. 代码守卫

- 每文件 ≤ 400 行，每函数 ≤ 80 行，CC ≤ 10
- ui/wails/ 总预估 ~280 行
- app/ 改动 ~30 行
- cmd/ 改动 ~5 行

---

## 8. Done 标准

1. Desktop app 能完整启动 V3 后端
2. `CallAPI("thread/start", ...)` 返回正确响应
3. bus events 推送到 Wails frontend
4. 启停链走 fx lifecycle，不依赖 sync.WaitGroup
5. go build + go vet + archtest 全通过

---

## 9. 依赖

- `github.com/wailsapp/wails/v3` — 需要确认是否已在 go.mod
- 前端 Vue 资产 — 从 V2 复制或新建骨架
