# Round 006 - 深入确认 major #10~#12

## Findings 确认

### 10. [major] platform/bus/sink.go:21-25 — NewLogSink 静默空 sink

```go
func NewLogSink(dispatcher *event.Dispatcher, logger *pkglogger.Logger) *LogSink {
    sink := &LogSink{subs: NewSubscription()}
    if dispatcher == nil || logger == nil {
        return sink  // ← 返回空 sink，无任何订阅
    }
```

**影响**：fx 装配期 dispatcher 或 logger 未注入时，LogSink 无订阅。所有 agent/thread/turn/tool/task/UI 事件不会被结构化日志记录。运维在排查问题时发现"日志里没有任何事件"，但系统实际在运行。

**精修方案**：
```go
func NewLogSink(dispatcher *event.Dispatcher, logger *pkglogger.Logger) (*LogSink, error) {
    if dispatcher == nil {
        return nil, errors.New("bus: dispatcher required for log sink")
    }
    if logger == nil {
        return nil, errors.New("bus: logger required for log sink")
    }
```

---

### 11. [major] module/threadprompt/runtime_catalog.go:182-187 — 逻辑反转 bug

```go
func (c *runtimePromptCatalog) storeListKeyword(filter RuntimeListFilter) string {
    if strings.TrimSpace(filter.Keyword) != "" {
        return ""  // ← 非空 keyword 被丢弃！
    }
    return strings.TrimSpace(filter.Keyword)  // ← 空 keyword 返回空（正确但无意义）
}
```

**影响**：`storeListKeyword` 永远返回 `""`。prompt catalog 的关键字过滤永远不生效，用户搜索 prompt 时看到全量列表。

**精修方案**：
```go
func (c *runtimePromptCatalog) storeListKeyword(filter RuntimeListFilter) string {
    return strings.TrimSpace(filter.Keyword)
}
```
或者如果原意是"有 keyword 时不传给 store（由 runtime 层自己过滤）"，则需要在调用方加注释说明意图。但当前无任何注释，判定为 bug。

---

### 12. [major] provider/codexapp/support.go:34-37 — mustJSON 吞 marshal error

```go
func mustJSON(v any) json.RawMessage {
    raw, _ := json.Marshal(v)
    return raw
}
```

**影响**：`mustJSON` 用于构建 Codex RPC params（`buildTurnStartParams`、`buildTurnSteerParams` 等）。如果传入不可序列化的值（含 chan/func 的 struct），`raw == nil`，下游发送 `null` payload 给 Codex app-server → 400 或静默忽略。

**精修方案**：
- 方案 A（推荐）：改名为 `marshalJSON`，签名 `(json.RawMessage, error)`，caller 上抛。
- 方案 B：保留 `must` 语义但 panic：`if err != nil { panic("codexapp: marshal: " + err.Error()) }`。
