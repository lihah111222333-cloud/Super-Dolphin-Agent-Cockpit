# 第 36 轮审查结论

## 审查范围

- `cmd/mcp-lsp/protocol/notification.go`（DispatchNotification、decodeNotificationParams、LogMessageType.String）
- `cmd/mcp-lsp/multilsp/transport_compat.go`（dispatchCompatServerRequest、lspCompatEmptyStructMethodSet）
- `cmd/mcp-orch/store/sqlc/types_ext.go`（TimeValue、TimePtr、TimeValuePtr、TextPtr、TextValuePtr、Int8Ptr、Int8ValuePtr）
- `cmd/mcp-lsp/protocol/ext.go`（LocationResult、PrimaryLocation、MarshalJSON、HasFuncRange、各 Capability 类型）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `notification.go:74-83` decodeNotificationParams | 静默 | `if len(raw) == 0 { return params, nil }` 静默返回 T 零值 | PublishDiagnostics 期望非空 URI 但收到空 params 时返回零值 URI；handler 无从区分「缺字段」vs「真零值」 | 改为：method 已知需 params 时 raw==0 返 error |
| `types_ext.go:14-18` TimeValue | 静默 | NULL → 静默返 `time.Time{}` 零值 | 调用方无法区分「DB 是 NULL」vs「DB 是 1970-01-01 00:00:00」 | 仅在确定字段 NOT NULL 时调用；NULL 字段必须用 TimePtr |
| `ext.go:196-198, 209-210` PrimaryLocation/MarshalJSON | 弱契约 | `Range == (Range{})` 比较检测「无 range」 | 0-based LSP 协议中 line=0, col=0 是合法位置（文件第一行行首），被误判为「无 range」回退到 TargetRange | 用显式 `RangeOpt *Range` 或加 `HasRange bool` 字段 |
| `ext.go:202-221` LocationResult.MarshalJSON | 信息丢失 | 自定义序列化只输出 file/line/col/end，丢弃 Canonical/LocationLink 区分信息 | 调用方拿到 JSON 无法知道结果是 declaration 还是 type definition | 序列化加 `source` 字段标识来源 |
| `ext.go:182-200` PrimaryLocation | 弱契约 | `Location > Canonical > LocationLink` 三层 fallback 无文档解释优先级 | 维护者修改优先级时不知道为何这个顺序 | 加注释说明 LSP 协议中各字段语义 |
| `ext.go:223-225` HasFuncRange | 隐式约定 | `FuncStart > 0 && FuncEnd >= FuncStart`——隐式认为行号 1-based，0 = unset | 如果未来切换到 0-based 行号（与 LSP 协议一致），所有 line=0 的 funcRange 失效 | 改为显式 `HasFuncRange bool` 字段，避免哨兵值 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `notification.go:48-72` DispatchNotification | 同步路径：decode → switch → handler.Publish/Log；handler 实现可能阻塞（IO/锁） | 加 duration 监控；handler 慢调用（>10ms）打 Warn |
| `transport_compat.go:87-105` dispatchCompatServerRequest | 同步路径但纯 in-memory 操作，应 < 1µs | 不需特殊监控；compat hit 计数器已在（line 89/97 Info 日志） |
| `types_ext.go` 整体 | 8 个 helper 都是值拷贝；批量转换 1000 行 result 时累积 | 大批量场景考虑 unsafe pointer cast（但牺牲可读性） |
| `ext.go:202-221` MarshalJSON | json.Marshal map[string]any 比 struct 慢；高 QPS LSP 工具（grep/xref）累积 | 改回 struct 序列化 + omitempty |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `notification.go:76-78` | 空 params 静默返零值 |
| `types_ext.go:14-18` TimeValue | NULL 静默映射 zero-time |
| `types_ext.go:36-42` TextPtr | 这是合理的 NULL→nil 映射（非静默） |
| `ext.go:196-198, 209-210` | 零值 Range 被视为「无 range」 |
| `ext.go:202-221` MarshalJSON | Canonical/LocationLink 信息在序列化中丢失 |
| `ext.go:182-200` PrimaryLocation | LocationLink 缺 TargetSelectionRange 时 fallback 到 TargetRange，无日志 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `notification.go:74-83` decodeNotificationParams | 空 params 与零值 params 不可区分 |
| `types_ext.go:14-34` Time 系列 | TimeValue 与 TimePtr 区分 NULL 处理但调用方需自行判断字段是否 NOT NULL |
| `ext.go:124-130` LocationResult | 4 个字段（Location/LocationLink/Canonical/FuncStart/End）互斥语义靠注释 |
| `ext.go:223-225` HasFuncRange | FuncStart > 0 哨兵值约定 |
| `ext.go:182-200` PrimaryLocation | 三层 fallback 优先级靠代码顺序 |

## 修复优先级

### P0（必须本周修）
1. **`ext.go:196-198, 209-210` 零值 Range 误判**——LSP 协议中 line=0 col=0 是文件首位置，是合法值。当前代码把 (0,0,0,0) 视为「无 range」，会让所有指向文件首位置的 LocationLink 错误 fallback 到 TargetRange。改为显式 nil-pointer 或 HasRange bool。这是协议兼容性 bug。
2. **`notification.go:74-83` 空 params 静默零值**——PublishDiagnostics 期望 URI 非空，但收到空 params 时静默返回 `PublishDiagnosticsParams{URI: ""}`。handler 拿到空 URI 不知道是恶意/损坏消息。改为：已知非空 method 时 raw==0 返 error。

### P1（本月）
3. `ext.go:202-221` MarshalJSON 加 source 字段标识来源
4. `ext.go:223-225` HasFuncRange 改显式 bool 字段
5. `types_ext.go:14-18` TimeValue 加注释强调仅用于 NOT NULL 字段
6. `ext.go:182-200` PrimaryLocation 加优先级注释

### P2（下个 sprint）
7. `ext.go:202-221` MarshalJSON 改回 struct 序列化（性能）
8. `notification.go:74-83` 加 method-specific 校验（PublishDiagnostics 必有 URI）

## 边界条件

1. **`transport_compat.go` 整体是项目正面案例**：明确白名单、archtest 钉死方法集合、每次 compat hit 都打结构化日志（带 stable event anchor `gopls.compat_fallback.hit`）让 ops dashboard 可以聚合统计。代码作者明确写了「ErrMethodNotSupported branch is NOT a hit」让监控不混淆。这是 fail-fast + 可观测性的优秀范例，建议作为模板。
2. **`types_ext.go` 的 NULL 处理双 API 设计**：`TimeValue`（NOT NULL 字段，NULL→zero）+ `TimePtr`（nullable 字段，NULL→nil）是合理的。但调用方需知道每个字段的 schema 约束才能正确选择。建议在 `Queries` 类型的注释中说明哪些字段是 NOT NULL，或用 `// nullable` 注释。当前设计依赖 reviewer 经验，是隐式约定。
3. **`ext.go:196-198` 的 Range 零值检测的实际触发场景**：LSP server 返回 `LocationLink` 时，TargetSelectionRange 通常指向 symbol 名称的精确范围（如 `func Foo()` 中 `Foo` 三字符），TargetRange 指向整个 symbol 块（如整个函数体）。SelectionRange 一般非零。但**「文件第一行第一列定义的 symbol」** 完全合法（如 package 声明 `package main`），SelectionRange 可以是 (0,0)-(0,12) 但 Start 是 (0,0) → `Range{}` 比较时 Start 部分零等于零，但 End 部分非零 → 整个 Range 非零，不会误触发。**实际 bug 概率较低**（需要 Start 和 End 都是零），但仍然应该改为显式标记。
4. **`notification.go` 的 fail-fast 大体良好**：handler nil 报错、未知 method 报错、JSON decode 失败包装错误。唯一弱点是 line 76-78 空 params 路径——但需考虑 LSP 协议中部分 notification 确实可以无 params（如 `$/cancelRequest` 之类）。修复时需 method-specific 判断。
5. **`types_ext.go` 的内存复制安全**：line 25 `copy := value.Time` 是必要的——`pgtype.Timestamptz` 是值类型但内部含 pointer field（实际上 time.Time 本身就是 monotonic clock + wall）。返回 `&value.Time` 会让 caller 修改影响 source row。复制是正确选择。
6. **`ext.go:202-221` 的 MarshalJSON 设计取舍**：自定义序列化让 LLM 收到的 JSON 更紧凑（不含 Canonical/LocationLink 嵌套对象）。这是 token-efficiency 优化，符合「输出给 LLM」的场景。但 server-to-server 调用（如果有）会丢失信息。建议加 verbosity 参数（compact vs full），与 `format/compact.go:39-46` NormalizeVerbosity 风格统一。

---

**本轮总结**：发现 2 个 P0 协议兼容性问题：①LocationResult 零值 Range 检测在文件首位置 symbol 上错判；②notification.go 空 params 静默零值。`transport_compat.go` 是 fail-fast + 结构化可观测性的正面案例（明确白名单 + stable event anchor），建议作为项目模板。`types_ext.go` 的双 API 设计合理但调用方需要 schema 知识。`ext.go` MarshalJSON 自定义序列化有 LLM token 优化意图但丢失 source 信息。

**累计进度**：36 轮完成。cron `fd4b4728` 继续推进。
