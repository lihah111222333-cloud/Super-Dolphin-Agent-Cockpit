# Wails Desktop 绑定面对齐

## 范围

- V2 基线文件：`go-agent-v2/cmd/agent-terminal/app.go`
- 因你点名的方法不全在 `app.go`，补读了同包两个直接相关文件：
  - `go-agent-v2/cmd/agent-terminal/app_handlers.go`：`CallAPI`
  - `go-agent-v2/cmd/agent-terminal/app_dialogs.go`：`SelectProjectDir`、`SelectFiles`
- V3 基线文件：`internal/ui/wails/binding.go`、`internal/ui/wails/binding_native.go`
- `走 CallAPI` 的可达性依据：
  - `internal/ui/wails/module.go:30-35` 把 Wails `CallAPI` 直接接到 `rpc.Server.Dispatch`
  - `internal/platform/rpc/module.go:50-52` 统一注册各模块 `rpc_handlers`

## 绑定清单

### V2

`app.go` 里公开的 App 绑定方法有：

- `LaunchAgent`
- `LaunchBatch`
- `SubmitInput`
- `SubmitWithFiles`
- `SendCommand`
- `StopAgent`
- `ListAgents`
- `GetGroup`
- `GetBuildInfo`
- `GetLSPDiagnostics`
- `GetLSPStatus`
- `SaveClipboardImage`
- `OpenNewWindow`

补充：

- `CallAPI` 在 `app_handlers.go:15`
- `SelectProjectDir` 在 `app_dialogs.go:59`
- `SelectFiles` 在 `app_dialogs.go:124`

### V3

`binding.go` 只公开：

- `CallAPI`
- `GetBuildInfo`
- `GetGroup`

`binding_native.go` 只公开：

- `SaveClipboardImage`
- `SelectProjectDir`
- `SelectFiles`

结论：V3 的 Wails 公开绑定面已经明显收窄，Agent/Window/LSP 相关入口大多不再同名直出。

## 逐项对比

| 方法 | V2 | V3 | 结论 | 说明 |
| --- | --- | --- | --- | --- |
| `CallAPI` | 有，`app_handlers.go:15` | 有，`binding.go:22` | ⚠️ | 同名保留，但 V2 是 `params any`，V3 改成 `paramsJSON string`，前端调用契约变了。 |
| `LaunchAgent` | 有，`app.go:78` | 走 `CallAPI("thread/start")`，有首条 prompt 时再走 `CallAPI("turn/start")`；见 `internal/module/thread/rpc.go:21`、`internal/module/turn/rpc.go:33` | ⚠️ | V3 没有同名便捷绑定。按 V2 实现本身，它实际就是 `thread/start` + 可选 `turn/start`。 |
| `StopAgent` | 有，`app.go:173` | 走 `CallAPI("agent.stop")`；见 `internal/sidecar/orch/orchestration/rpc.go:40` | ⚠️ | 同名绑定删除，功能收敛到 RPC。 |
| `ListAgents` | 有，`app.go:188` | 走 `CallAPI("agent.list")`；见 `internal/sidecar/orch/orchestration/rpc.go:43` | ⚠️ | 同名绑定删除，功能收敛到 RPC。 |
| `SubmitInput` | 有，`app.go:149` | 走 `CallAPI("agent.submitPrompt")` 或 `CallAPI("agent.submit")`；见 `internal/sidecar/orch/orchestration/rpc.go:20`、`30` | ⚠️ | 同名绑定删除，功能收敛到 RPC。 |
| `SendCommand` | 有，`app.go:165` | 缺失 | ❌ | V3 没有泛化 `SendCommand` 绑定，也没有通用 RPC key。`internal/module/thread/rpc.go:59-95` 只是把少数命令拆成若干 `thread/*` 路由，不能 1:1 替代任意 `cmd,args`。 |
| `GetBuildInfo` | 有，`app.go:197` | 有，`binding.go:41` | ✅ | 仍是直接绑定，零参数，归宿清晰。 |
| `GetGroup` | 有，`app.go:193` | 有，`binding.go:45` | ⚠️ | V2 返回运行时 `a.group`；V3 直接返回 `defaultGroup` 常量 `""`，语义退化。 |
| `SaveClipboardImage` | 有，`app.go:241` | 有，`binding_native.go:15` | ⚠️ | 同名保留，但 V2 入参是 base64 图片数据；V3 入参是目标文件名，并从系统剪贴板抓图，契约已变。 |
| `SelectProjectDir` | 有，`app_dialogs.go:59` | 有，`binding_native.go:26` | ⚠️ | 同名保留，但 V2 有 `defaultPath` 入参且只返回 `string`；V3 改成无参、返回 `(string, error)`。 |
| `SelectFiles` | 有，`app_dialogs.go:124` | 有，`binding_native.go:47` | ⚠️ | 同名保留，但 V3 额外暴露 `error` 返回，前端错误处理面变化。 |
| `OpenNewWindow` | 有，`app.go:272` | 缺失 | ❌ | V3 `binding*.go` 无同名方法；全仓 Go 代码也未见 `ui/openNewWindow` 等等价路由。 |
| `GetLSPDiagnostics` | 有，`app.go:211` | 缺失 | ❌ | V3 `binding*.go` 无同名方法；全仓 Go 代码未见 `lsp_diagnostics_query` 或其他等价 RPC handler。 |

## 总结

总评：`⚠️ 未做到 1:1 对齐`

按这 13 个方法看：

- `✅ 直接对齐`：`GetBuildInfo`
- `⚠️ 收敛到 CallAPI 或同名但契约漂移`：`CallAPI`、`LaunchAgent`、`StopAgent`、`ListAgents`、`SubmitInput`、`GetGroup`、`SaveClipboardImage`、`SelectProjectDir`、`SelectFiles`
- `❌ 缺失`：`SendCommand`、`OpenNewWindow`、`GetLSPDiagnostics`

如果目标是“前端继续保留 V2 的 `window.go.main.App.XXX()` 调用面”，至少还要补三类缺口：

1. 补回真正缺失的绑定：`SendCommand`、`OpenNewWindow`、`GetLSPDiagnostics`
2. 决定是否恢复旧契约：`CallAPI`、`GetGroup`、`SaveClipboardImage`、`SelectProjectDir`、`SelectFiles`
3. 决定是否保留便捷壳：`LaunchAgent`、`StopAgent`、`ListAgents`、`SubmitInput` 是否继续作为 `CallAPI` 的桌面侧薄封装暴露
