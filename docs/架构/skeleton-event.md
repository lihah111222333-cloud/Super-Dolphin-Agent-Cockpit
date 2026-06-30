# skeleton-event.md — bus + eventsurface 类型事件骨架

> **当前库**: `github.com/kelindar/event`
> **当前平台封装**: `internal/platform/bus`、`internal/platform/eventsurface`
> **定位**: 进程内 typed event 分发，以及后端事件到前端 wire 方法的整形层。

---

## 0. 一句话定位

`bus` 负责进程内强类型事件分发，`eventsurface` 负责把内部 DTO 事件转成 UI/RPC 可消费的 wire method + payload。业务模块不直接用字符串 topic 或 `map[string]any` 做事件总线。

---

## 1. 当前事件类型来源

事件 DTO 不在旧的 `internal/events` 包。当前按领域放在：

| 包 | 用途 |
|---|---|
| `internal/dto/agent` | agent 生命周期、runtime report、warning/error |
| `internal/dto/thread` | thread started/stopped/messages/compacted/updated/launched |
| `internal/dto/turn` | turn started/completed/interrupted/stalled/resumed/input/output |
| `internal/dto/tool` | tool call、approval、diff |
| `internal/dto/task` | DAG/node/wakeup |
| `internal/dto/ui` | UI projection、tokens、skills、preferences、thread patch |
| `internal/dto/cron` | cron run state |

所有可路由事件都实现稳定的 `Type() uint32`，事件编号定义在 `internal/dto/shared/event.go`。

---

## 2. bus 平台层

`internal/platform/bus` 是 kelindar/event 的薄封装：

- `Bus` 提供 `*event.Dispatcher`。
- `NewEmitter[T]` / `ThreadEmitters` 提供 typed publish 入口。
- `Router` / `Subscription` 统一托管 cancel func。
- `ResilientSubscribe` 在订阅回调边界 recover，防止单个 handler 拉垮 dispatcher。
- `LogSink` 订阅已知 DTO 事件并写结构化日志。

示例：

```go
dispatcher := bus.NewDispatcher()
emitter := bus.NewEmitter[turn.TurnCompleted](dispatcher)
emitter.Emit(turn.TurnCompleted{Success: true})

cancel := bus.ResilientSubscribe(dispatcher, func(ev turn.TurnCompleted) {
    // typed payload
}, logger)
defer cancel()
```

---

## 3. eventsurface 推送层

`internal/platform/eventsurface.Bind(dispatcher, logger, publish)` 订阅内部 DTO，并发布 wire 通知：

```text
internal DTO event
  -> bus dispatcher
  -> eventsurface.Bind
  -> eventsurface.ExpandNotifications
  -> rpc.PushBridge / push worker
  -> rpc.Server.NotifyAll
```

关键映射：

- `turn.TurnOutputDelta.Stream=message` -> `item/agentMessage/delta`
- `turn.TurnOutputDelta.Stream=reasoning` -> `item/reasoning/textDelta`
- `turn.TurnOutputDelta.Stream=stdout` -> `item/commandExecution/outputDelta`
- `tool.ToolApprovalRequested.Kind=file|skill|command` -> 对应审批 wire method
- `ui.UIThreadPatch` -> `ui/thread/patch`
- `task.TaskNodeStatusChanged` -> `task/node/statusChanged`

`eventsurface.AllTypedWireMethods()` 是后端拥有强类型事件面的完整方法清单；`RawWireAllowlistSpec()` 只允许少量 provider raw 事件直通。

---

## 4. payload 字段守卫

wire DTO 到 eventsurface mapper 是容易漂移的边界。当前 guard 要求：

- selected `internal/dto` 事件 struct 的 JSON 字段必须登记为 mapper 消费或写明豁免原因。
- selected `internal/contract` provider mirror report/input struct 的 JSON 字段必须登记。
- 新字段未登记会在 `internal/archtest` 中失败。

对应测试：`internal/archtest/wire_dto_field_registry_test.go`。

---

## 5. 前端清单一致性

`internal/platform/eventsurface/methods_test.go` 校验 `AllTypedWireMethods()` 与前端订阅列表一致。新增 wire method 时必须同步：

1. 后端 `eventsurface` 方法常量和映射。
2. 前端订阅/处理列表。
3. DTO 字段 registry 或豁免原因。

---

## 6. 验证

```bash
./scripts/test_with_guard.sh ./internal/platform/bus ./internal/platform/eventsurface -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

涉及前端订阅时追加：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

---

## 7. 禁止行为

| 规则 | 原因 |
|---|---|
| 不新增字符串 topic + `map[string]any` 事件总线 | 编译期无法守住字段漂移 |
| 不绕过 `eventsurface` 直接向 UI 推送业务 DTO | wire method/payload 会失去统一守卫 |
| 不让 provider raw 事件随意直通 | 只能进入 `RawWireAllowlistSpec` |
| 不在事件回调里做阻塞 I/O | 会阻塞 dispatcher 调用链 |
| 不新增 DTO 字段后跳过 registry/豁免 | 会导致 mapper 静默漏字段 |
