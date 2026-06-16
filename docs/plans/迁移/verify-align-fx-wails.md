# 验证：fx/shutdown + Wails binding 对齐修复

## 读取基线

- `docs/plans/迁移/align-fx-shutdown.md`
- `docs/plans/迁移/align-wails-binding.md`

## 验证结论

| 项目 | 结论 | 证据 | 备注 |
| --- | --- | --- | --- |
| RunDesktop 单一 fx Wails app（不再双套） | ✅ | `internal/app/app.go:29-50`, `internal/app/app.go:68-75`, `internal/ui/wails/module.go:17-25`, `internal/ui/wails/module.go:72-98` | `RunDesktop()` 通过 `newDesktopFXApp(fx.Populate(&wailsApp, &lifecycle))` 取出同一个 FX 图里的 `*application.App`；全仓 `application.New(` 仅见 `internal/ui/wails/module.go:74`。 |
| WailsLifecycle fx.Provide 闭合 | ✅ | `internal/ui/wails/module.go:17-28`, `internal/ui/wails/module.go:62-70`, `internal/ui/wails/module.go:111-118`, `internal/app/app.go:34`, `internal/app/app.go:68-75` | `uiwails.Module` 已 `fx.Provide(NewWailsLifecycle)`，`NewWailsApplication()` 显式依赖 `*WailsLifecycle`，`bindWailsLifecycle()` 再把 `fx.Shutdowner` 接进去；桌面图通过 `newDesktopFXApp()` 固定包含该 module。 |
| 双向 shutdown（Wails→fx + runner→Quit） | ✅ | `internal/ui/wails/lifecycle.go:82-103`, `internal/ui/wails/module.go:111-118`, `internal/app/runner.go:37-53`, `internal/app/app.go:78-88`, `internal/ui/wails/lifecycle.go:105-112`, `internal/ui/wails/lifecycle.go:166-173` | 正向：`ShouldQuit()` / `OnShutdown()` 都走 `requestBackendShutdown()`，再由 `shutdowner.Shutdown()` 落到 FX。反向：runner 结束后统一 `Shutdown()`，desktop 侧由 `watchFXShutdown()` 把 `app.Done()` 翻成 `NotifyBackendFailed()`，最终调用 `Quit()`。 |
| runner nil 返回无条件 Shutdown | ✅ | `internal/app/runner.go:37-53` | `RunGroup()` 返回后，无论 `err` 是否为 `nil`，都会执行 `_ = p.Shutdowner.Shutdown()`。`align-fx-shutdown.md:302-323` 这一点仍然和当前代码一致。 |
| Wails 绑定面：CallAPI + 原生方法 + 便捷方法 | ✅ | `internal/ui/wails/binding.go:23-89`, `internal/ui/wails/binding_native.go:15-65` | 三层都在：`CallAPI`；原生方法 `SaveClipboardImage` / `SelectProjectDir` / `SelectFiles`；便捷方法 `LaunchAgent` / `StopAgent` / `ListAgents` / `GetBuildInfo` / `GetGroup`。但便捷层不是完整 V2 parity，`OpenNewWindow` / `GetLSP*` 只是 deferred 壳。 |
| OpenNewWindow de-scope 标注 | ✅ | `internal/ui/wails/binding.go:73-77`, `internal/ui/wails/binding_test.go:90-127` | 代码已明确 `TODO(P9): Defer multi-window desktop support...`，运行时返回 `deferredBindingError(...)`；测试也把它归入 deferred/not implemented。 |
| JSON wire format 全量治理进度 | ⚠️ | `internal/sidecar/orch/orchestration/rpc_types.go:25-205`, `internal/sidecar/orch/orchestration/rpc_types.go:285-376`, `internal/module/thread/rpc_types.go:37-84`, `internal/module/thread/rpc_types.go:94-212`, `internal/module/turn/rpc_types.go:24-149`, `internal/module/workspace/rpc_types.go:17-178`, `internal/module/workspace/contract.go:66-115`, `internal/module/thread/rpc_types.go:23-33`, `internal/module/turn/rpc_types.go:156-159` | 输入兼容层已经明显扩展到 orchestration DAG、thread、turn、workspace，不再是“只补 6 个类型”。但对外 tag 仍未全量统一，例子包括 thread start 仍保留 `modelProvider` / `approvalPolicy` / `baseInstructions`，`turnForceCompleteResult` 仍是 `forceCompleted`。结论应是“治理推进明显，但未完成”。 |
| 文档更新准确 | ❌ | `docs/plans/迁移/align-wails-binding.md:42-54`, `docs/plans/迁移/align-wails-binding.md:61-72`, `docs/plans/迁移/cap-wails-desktop.md:255`, `docs/plans/迁移/arch-json-wire.md:8`, `docs/plans/迁移/arch-json-wire.md:121`, `docs/plans/迁移/arch-json-wire.md:124-126`, `internal/ui/wails/binding.go:45-89`, `internal/sidecar/orch/orchestration/rpc_types.go:80-205`, `internal/module/workspace/rpc_types.go:17-178`, `internal/module/workspace/contract.go:66-115` | 现有文档有明显过期项：`align-wails-binding.md` 仍写成 “`binding.go` 只公开 `CallAPI/GetBuildInfo/GetGroup`” 且把 `OpenNewWindow` 判成缺失；`cap-wails-desktop.md:255` 仍写 “V3 source 下无对应绑定”；`arch-json-wire.md` 仍写 thread/workspace/DAG 大量只接受 camelCase。代码已经不是这个状态。 |

## 文档对齐备注

- `docs/plans/迁移/align-fx-shutdown.md` 的 runner 返回统一 shutdown 判断目前仍准确，见 `docs/plans/迁移/align-fx-shutdown.md:302-323` 对照 `internal/app/runner.go:37-53`。
- `docs/plans/迁移/align-wails-binding.md` 已明显落后于当前 binding 实现；至少 `LaunchAgent` / `StopAgent` / `ListAgents` / `OpenNewWindow` / `GetLSPDiagnostics` / `GetLSPStatus` 都已在 `internal/ui/wails/binding.go:45-89` 出现，其中后 3 个是 deferred，而不是“无同名方法”。
- `docs/plans/迁移/arch-json-wire.md` 需要重写“兼容层覆盖面”部分；当前代码已把 snake_case 主格式 + camelCase 兼容扩展到多个模块，但仍未做到输出形状全量统一。

## 总结

- 本轮 fx/shutdown 对齐主链已落地：单一 FX Wails app、`WailsLifecycle` 装配闭合、双向 shutdown、runner nil return 统一 shutdown，均可在当前代码中直接验证为已修。
- Wails binding 已不是“只有 `CallAPI` + 少量 native”；当前实际是 `CallAPI + native + convenience + deferred placeholders`。
- 剩余两处没有收尾干净：一是 JSON wire format 仍在治理中，二是迁移文档没有同步到当前实现，尤其 `align-wails-binding.md` 与 `arch-json-wire.md` 已明显失真。
