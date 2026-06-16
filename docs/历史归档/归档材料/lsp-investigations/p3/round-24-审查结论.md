# 第 24 轮审查结论

## 审查范围

- `internal/module/turn/observation/contract.go`（type aliases、terminalPrecedence）
- `internal/module/turn/observation/memory.go`（Memory 实现：MapTurn、AttributeCall、RecordTokens、RecordTerminal、mergeTokens）
- `internal/module/turn/observation/subscribers.go`（Subscribe、onTurnStarted/Completed/Interrupted/Stalled、onToolCallBegin/End、onUITokensUpdated、onRawProviderEvent）
- `internal/module/turn/observation/bus_provider.go`（NewObservationSubscribers）

> 与第 15 轮覆盖的 `trajectory_collector.go`（observation 的消费方）不重复。本轮聚焦 observation 包内部实现。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `subscribers.go:28-31` `Subscribe` | 兜底 | `dispatcher == nil \|\| contract == nil` 返回 noop cancel | nil dispatcher 是装配 bug（与 round-17 ResilientSubscribe 同根）；nil contract 是装配 bug | nil 应 panic |
| `subscribers.go:87-94` `onTurnStarted` | 兜底 | `turnID == ""` 时 return（不记录） | 空 turnID 是 provider 事件 bug；静默丢弃 | 至少 debug log "turn started with empty turn_id" |
| `subscribers.go:97-105` `onTurnCompleted` | 兜底 | 同上 | 同上 | 同上 |
| `subscribers.go:108-120` `onTurnInterrupted` | 兜底 | 同上 | 同上 | 同上 |
| `subscribers.go:122-134` `onTurnStalled` | 兜底 | 同上 | 同上 | 同上 |
| `subscribers.go:136-151` `onToolCallBegin` | 兜底 | `callID == ""` 时不 Dedupe 也不 IncrementToolCalls；`turnID == ""` 时 AttributeCall 内部会 return false | 空 callID 是 provider bug；空 turnID 让 attribution 失败 | 至少 debug log |
| `subscribers.go:153-168` `onToolCallEnd` | 兜底 | 同上 | 同上 | 同上 |
| `subscribers.go:215-235` `onRawProviderEvent` | 兜底 | `rawProviderDedupeKey` 返回零值 DedupeKey 时不调用 Dedupe | 合理（无法构造 key 的事件不参与去重） | OK |
| `subscribers.go:237-252` `rawPayloadMap` | 静默 | `json.Marshal(v)` 失败返回 nil | 未知类型的 data 被静默丢弃 | 至少 debug log |
| `subscribers.go:254-260` `decodeRawPayload` | 静默 | `json.Unmarshal` 失败返回 nil | 损坏的 JSON payload 被静默丢弃 | 至少 debug log |
| `memory.go:38-53` `MapTurn` | 兜底 | `local == "" \|\| provider == ""` 返回 false | 空 ID 是调用方 bug | 至少 debug log |
| `memory.go:69-77` `AttributeCall` | 兜底 | `callID == "" \|\| localTurnID == ""` 返回 false | 同上 | 同上 |
| `memory.go:86-95` `RecordTokens` | 兜底 | `turnID == ""` 时返回 snap 不记录 | 同上 | 同上 |
| `memory.go:127-150` `RecordTerminal` | 兜底 | `turnID == ""` 时返回 t 不记录 | 同上 | 同上 |
| `memory.go:104-125` `mergeTokens` | 兜底 | 零值字段不覆盖（`if next.Input != 0`） | 合理（增量 merge：只覆盖非零字段） | OK |
| `memory.go:138-142` RecordTerminal locked kinds | 兜底 | Interrupted/Aborted 一旦记录就不可覆盖 | 合理（sticky terminal 是 P0b 设计决策） | OK |
| `bus_provider.go:14-36` `NewObservationSubscribers` | 弱契约 | contract/logger 不做 nil 校验；Subscribe 内部判 nil | 当前合理（Subscribe 内部处理） | 但 contract nil 应在装配期 panic |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `subscribers.go:28-31` | nil dispatcher/contract 返回 noop |
| `subscribers.go:87-168` | 所有 onTurn*/onToolCall* 空 turnID/callID 静默 return |
| `subscribers.go:237-260` | rawPayloadMap/decodeRawPayload 失败返回 nil |
| `memory.go:38-95, 127-150` | 所有 Memory 方法空 ID 静默 return false/不记录 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `subscribers.go:28-31` | Subscribe nil 参数兜底 |
| `memory.go:38-150` | 所有方法空 ID 静默 return |
| `bus_provider.go:14-36` | contract/logger 不校验 |

## 修复优先级

### P0（必须本周修）
1. `subscribers.go:28-31` Subscribe nil dispatcher/contract 改 panic——nil 是装配 bug，noop cancel 让整个 observation 链路静悄悄断裂（与 round-17 同根）

### P1（本月）
2. `subscribers.go:87-168` 所有 onTurn*/onToolCall* 空 turnID/callID 加 debug log
3. `subscribers.go:237-260` rawPayloadMap/decodeRawPayload 失败加 debug log
4. `memory.go:38-150` Memory 方法空 ID 加 debug log
5. `bus_provider.go:14-36` contract nil 装配期 panic

### P2（下个 sprint）
6. 无额外 P2

## 边界条件

1. **`Subscribe` nil dispatcher/contract panic 的影响**：与 round-17 `ResilientSubscribe` 同根问题。如果 dispatcher 是 fx optional 依赖，nil 是合法的。修复前 grep 调用面确认。当前 `NewObservationSubscribers` 在 `module.go` 中通过 fx Provide 注入，dispatcher 来自 `bus.Module`——如果 bus 未装配，dispatcher 为 nil。建议：如果 bus 未装配，observation 也不应装配（fx 依赖图保证）。
2. **空 turnID/callID 的来源**：provider 事件（TurnStarted、ToolCallBegin 等）的 turnID 来自 provider 进程。如果 provider 有 bug 发了空 turnID，observation 静默丢弃是合理的 graceful degradation——不应因为一个坏事件让整个 observation 崩溃。改 debug log 而非 error/panic。
3. **`RecordTerminal` 的 sticky 语义**：Interrupted/Aborted 一旦记录就不可被后续 Completed 覆盖。这是 P0b 设计决策——防止 provider 在中断后发送的 "completed" 事件覆盖中断状态。当前实现正确。
4. **`mergeTokens` 的零值不覆盖**：`if next.Input != 0` 意味着如果 provider 发了 `Input: 0`（真的用了 0 个 input token），不会覆盖之前的非零值。这是一个已知的 trade-off——P0b 文档中标注"零值 token 不视为有效观测"。
5. **observation 包整体代码质量较高**：设计清晰（单向数据流：bus → Contract → consumers），并发安全（sync.RWMutex），去重机制完善（DedupeKey）。主要问题是 nil 兜底和空 ID 静默丢弃——这些是 graceful degradation 而非 fail-fast 违规，但在"100% Fail-Fast"审查标准下仍需标注。

---

**本轮总结**：observation 包代码质量较高，设计清晰。唯一的 P0 是 `Subscribe` nil 参数兜底（与 round-17 同根）。其余问题是空 ID 静默丢弃——这是 graceful degradation 设计，加 debug log 即可。

**累计进度**：24 轮完成。cron `da34430c` 继续推进。
