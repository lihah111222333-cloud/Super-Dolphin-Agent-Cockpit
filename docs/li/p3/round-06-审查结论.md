# 第 06 轮审查结论

## 审查范围

- `internal/platform/toolbridge/memory_read_tool.go`（memory_read host-direct 工具、scope/type 解析、CompositeRegistry）
- `internal/platform/toolbridge/memory_write_tool.go`（memory_write host-direct 工具、target 字段拒绝、scope/type 校验、Composite registry）
- `internal/platform/toolbridge/stdio_mcp_client.go`（stdio MCP 子进程客户端、initialize、request、Close 进程清理）
- `internal/platform/toolbridge/handler_host_tools.go`（host-direct 工具调用、影子工具检测、cwd 解析、错误信封）
- `internal/platform/toolbridge/subscribers.go`（diff fallback 订阅装配）

> 与第 01-05 轮已覆盖的 `proxy.go`、`diff_fallback.go`、`handler.go`、`handler_managed_launch.go`、`handler_peer_decode.go`、`host_tools.go` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `memory_read_tool.go:33-38` `NewMemoryReadHostToolRegistry` | 兜底 | `if reader == nil { return nil }` 然后所有方法都靠 `r == nil` early return | nil registry 在装配/调用任何阶段都不报错；功能整体静默缺失 | reader=nil 应 panic（装配 bug），或返回 error 显式拒绝 |
| `memory_read_tool.go:40-46` `ListHostTools` | 静默 | `json.Marshal` 错误用 `_` 忽略 | schema marshal 错误（理论不会发生但 lint 视角有风险）会让客户端拿到空 schema | 改为 `_, err := ...; if err != nil { panic("memory_read schema marshal: " + err.Error()) }`（init 一次） |
| `memory_read_tool.go:40-43` 多重短路 | 静默 | `r==nil \|\| r.reader==nil \|\| !opts.Enabled \|\| !opts.ToolsEnabled` 都返回 nil 工具列表 | 配置错误（如 enabled=false 但 toolsEnabled=true）静悄悄使工具不可见，Codex 模型不知该工具应当存在 | 至少 debug log 一行，标明哪个 guard 触发；NewRegistry 拒绝构造 |
| `memory_read_tool.go:48-50` `HasTool` | 弱契约 | `return r != nil && name == ToolNameMemoryRead` | name 不做 trim，调用方传 `"memory_read "` 会被判 false；与 ListHostTools 不一致 | `strings.TrimSpace(name) == ToolNameMemoryRead` |
| `memory_write_tool.go:40-45` `NewMemoryWriteHostToolRegistry` | 兜底 | 同上，writer=nil 静默 | 同上 | 同上 |
| `memory_write_tool.go:47-53` `ListHostTools` | 静默 | 同 memory_read | 同上 | 同上 |
| `memory_write_tool.go:104-109` `rejectMemoryWriteTargetFields` | 弱契约 | 5 个字段任一非 nil 就报错 | 入参字段类型是 `any`，模型传 `null` 也会被识别为 non-nil（json.RawMessage 的"null"是 nil）实际不会，但若类型变更易回归 | 字段类型用 `*string` / 显式 sentinel，或在 schema 层用 additionalProperties=false 拒绝 |
| `memory_write_tool.go:119-131` `parseMemoryWriteScope` | 兜底 | scope 留空时 fallback 到 `defaultScopeForMemoryWriteType`；不匹配时返回 error | 关键约束（feedback→user / project→project）由调用方留空才正确；用户填写错误 scope 拿到 `invalid_input`，但留空反而"自动正确" | 必填化 scope，且 schema 显式声明 required；fallback 路径加日志 |
| `memory_write_tool.go:127` `scope != defaultScopeForType` | 兜底 | scope 传了，但与 type 不匹配 → `invalid_input` | 没问题，但报错信息只说 "scope does not match type"，不告诉用户 type 期望什么 scope | hint 中带 `expected scope=%s for type=%s` |
| `memory_write_tool.go:209-218` `CompositeHostToolRegistry.CallHostTool` | 兜底 | `r != nil` guard 后遍历；找不到工具时返回 `fmt.Errorf("unknown tool %q")`，**不带 contract.AgentMemoryError code** | 上层 `hostToolErrorOutcome` 通过 `errors.Is`/`errors.As` 分类失败原因；普通 fmt.Errorf 会落入 default `outcome=error`，丢失 metrics 标签 | 用 `contract.NewAgentMemoryError("unknown_tool", ...)` |
| `memory_write_tool.go:158-173` `NewCompositeHostToolRegistry` | 兜底 | 收集所有非 nil registry；为空时返回 nil | 装配阶段没注入任何 host tool 时返回 nil；上层 `h.hostTools != nil` 判 false 走 peer 路径 | 装配为空应是错误：要么显式禁用 host tools，要么至少 log；当前完全无声 |
| `stdio_mcp_client.go:81-86` `newStdioMCPClient` initialize | 静默 | `_ = client.transport.WriteMessage(map[string]any{... "notifications/initialized"})` 显式忽略写入错误 | initialized 通知失败时子进程可能未进入 ready 状态，后续 tools/list 失败原因模糊 | 至少 log 失败；真正可靠的做法是检查写错误 + close client |
| `stdio_mcp_client.go:109-127` `CallTool` | 兜底 | request 失败时返回 `(toolCallTextResult(false, err.Error()), err)` —— **同时返回 result 和 error** | 调用方若只判 err 会跳过 result；若只判 result.Success=false 会丢失 error 类型；与第 5 轮 callPeerTool 相反方向但同样割裂语义 | 失败时只返回 (nil, err) 一种 |
| `stdio_mcp_client.go:138-143` `request` ctx select | 静默 | ctx done 时 `_ = c.Close()` 忽略关闭错误 | 进程清理失败导致僵尸进程；与 stdio_process_unix/windows 的 cleanup 路径同根 | 用 `errors.Join(ctx.Err(), c.Close())` |
| `stdio_mcp_client.go:152-154` `if resp.ID == 0 { continue }` | 静默 | 收到 id==0 的响应（jsonrpc 通知）静默忽略 | id 为 0 也可能是合法响应（jsonrpc spec 允许 id=0）；这里把它当成"通知"丢弃 | 区分 raw 是否包含 id 字段（用 `*int64` 或 `json.RawMessage`） |
| `stdio_mcp_client.go:158-160` `resp.Error.Message` | 弱契约 | 用 `fmt.Errorf("%s", resp.Error.Message)` 把远端错误转字符串 | 丢失 error code、retryable 标志等结构化字段 | 用 `common.NewCodedToolError`，保留 code/data |
| `stdio_mcp_client.go:165-183` `readMessage` | 静默 | ctx done 时 close client 并返 ctx.Err；read goroutine 的写入 `readDone <- ...` 在 ctx done 后没人接收，channel 是 buffered=1 不会泄漏，但 read goroutine 还在阻塞 transport.ReadMessage | transport.ReadMessage 是阻塞 IO；ctx 取消后 goroutine 仍在跑，直到子进程退出才返回 | 在 close 路径里强制让 transport.ReadMessage 立即返回（关闭 stdout pipe），否则 goroutine 可能泄漏 |
| `stdio_mcp_client.go:185-198` `Close` | 兜底 | `c == nil` / `c.closed == nil` 都走兜底路径 | nil receiver 调用是 bug；`closed == nil` 是构造未完成 bug | nil receiver panic |
| `stdio_mcp_client.go:200-222` `close` | 静默 | `_ = c.stdin.Close()` / `_ = c.transport.Close()` 显式忽略 | 关闭子进程 stdin/transport 失败可能让 wait 卡住；这两个错误现在完全不可见 | 收集所有错误用 errors.Join 返回 |
| `stdio_mcp_client.go:48-56` `defaultStdioClientFactory` | 弱契约 | 仅校验 type/url/command；未校验 binary.Name 是否为空、Env 中 key 是否合法 | 装配时 manifest 损坏（空 name）会 propagate 到 `binary.Name` 各处日志/错误用空字符串 | 增加 binary.Validate() |
| `stdio_mcp_client.go:89-95` `manifestEnv` | 静默 | env 中 key=="" 或 value 包含 "=" 不做校验 | 注入子进程 env 可能产生格式异常；exec.Command 内部对此宽容但行为不可预测 | 入口校验 key 非空且不含 `=` |
| `handler_host_tools.go:25-67` `listPeerToolsForCodex` | 静默 | 直接 `go func() { ... }()`（**没用 safego.Go**）；自己写了 recover panic | 与项目 SafeGo 约定不一致；recover 后 panic 转 error 写入 outcome.err，但 panic 后的 stack 没保留 | 改用 `safego.Go`，并把 panic err 也走结构化日志 |
| `handler_host_tools.go:39-49` recover 内 | 静默 | `defer func() { if recovered := recover(); ...; ch <- ... }()` | recover 后构造 error 文本时丢 stack | recover 路径用 `debug.Stack()` 注入 |
| `handler_host_tools.go:79-102` `ListToolsForCodex` | 兜底 | merged 为空时返回 `ErrNoPeerAvailable` | host tool 也为空 + peer 也为空确实是 ErrNoPeerAvailable；但若 host 因配置 disabled 不在列表里、peer 又失败，error 路径会先报 peer error，反而盖过 host disabled 信息 | 顺序调整：先 join peer error，再判 host empty；至少日志区分 |
| `handler_host_tools.go:104-143` `appendDynamicToolsWithShadowWarning` / `appendMCPToolsWithShadowWarning` | 兜底 | 重名工具仅 Warn 后跳过；reserved host-only 工具被 peer 暴露仅 Warn 后跳过 | shadow 是潜在的安全问题——peer 注册了和 host 同名工具试图覆盖 host-direct（如 memory_write 走 peer 绕过 audit）；当前仅 log，无 metrics、无告警 | shadow 应是 fatal-level 事件；至少 metrics counter + 周期审计报警 |
| `handler_host_tools.go:124-130` shadow 检测 | 静默 | `if previousSource, ok := seen[name]; ok { warn; continue }` | shadow 数据全藏在日志里；上线后无人盯就漏了 | 同上 |
| `handler_host_tools.go:163-165` `removedSkillToolResult` | 兜底 | 已废弃 skill 工具被调用时返回 `Success=false` text result + `nil` error | error 被吞，调用方只能看到失败文本；与 round-05 routeHostOnlyToolCall 同根问题 | 返回 `(nil, errors.New("tool removed: " + name))` |
| `handler_host_tools.go:200-217` `callHostTool` 异常路径 | 兜底 | `cwdErr` 与 `CallHostTool` err 都被 `hostToolErrorResult` 包成 result + 返回 `nil` error | 同上 | error 必须 return |
| `handler_host_tools.go:218-222` `json.Marshal` payload | 静默 | marshal 失败 `return nil, mErr` 但 `outcome` 仍为 error（defer 会上报 metrics）——这里行为正确，但有 220 行空白行（疑似漏 outcome 设置） | marshal 失败前 outcome 被设为 ok 之前；当前实际 outcome 是初始 "error"，正确 | 删除空白行；保留逻辑 |
| `handler_host_tools.go:226-230` text content | 弱契约 | `Type: "inputText"` 与 round-05 同根 | 同上 | 同上修法 |
| `handler_host_tools.go:299-317` `resolveRequiredHostToolCWD` | 兜底 | cwd 为空 → resolveAgentCWD，仍空 → 报错；resolveAgentCWD 失败 → wrap ErrSkillMissingCWD | resolveAgentCWD 错误被 wrap 成 ErrSkillMissingCWD，掩盖了原因（DB 错？路径不存在？）；调用方用 `errors.Is` 判断 cwd 缺失，所有故障都被归因为"cwd missing" | 区分 ErrSkillMissingCWD（合法缺失）与 ErrResolverFailure（IO 错误） |
| `handler_host_tools.go:319-328` `resolveAgentCWD` | 兜底 | resolver=nil 时 wrap 为 ErrSkillMissingCWD | resolver=nil 是装配 bug，不应是 ErrSkillMissingCWD（误导：以为 agent 没绑工作目录） | 用专用 ErrResolverNotConfigured |
| `handler_host_tools.go:171-182` `validateHostToolGuards` | 弱契约 | 三段判断：enabled、toolsEnabled、name match；name 不做 trim | 若 callName 含空格会判 invalid_input；与 ListHostTools 一致性问题 | trim 后比较 |
| `subscribers.go:13-37` `NewToolbridgeDiffFallbackSubscribers` | 弱契约 | tracker 入参不做 nil 校验；ResilientSubscribe 的 cancel 可能为 nil（已用 once 包裹） | tracker=nil 时 register handler 触发 nil pointer | tracker=nil 装配期 panic |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `memory_read_tool.go:44` | `json.Marshal(memoryReadInputSchema())` 错误用 `_` 忽略 |
| `memory_read_tool.go:40-43` | enabled/toolsEnabled guard 触发时静默返回 nil |
| `memory_write_tool.go:51` | 同 read |
| `memory_write_tool.go:48-50` | 同 read |
| `stdio_mcp_client.go:85` | initialized 通知写入错误忽略 |
| `stdio_mcp_client.go:140` | ctx done 时 close 错误忽略 |
| `stdio_mcp_client.go:152-154` | id==0 响应静默 continue |
| `stdio_mcp_client.go:180` | readMessage ctx done 时 close 错误忽略 |
| `stdio_mcp_client.go:202-205` | stdin/transport close 错误忽略 |
| `handler_host_tools.go:38-49` | listPeerToolsForCodex 用裸 go + 自管 recover |
| `handler_host_tools.go:124-130` | shadow tool 仅 Warn |
| `handler_host_tools.go:132-138` | reserved host-only 工具被 peer 暴露仅 Warn |
| `handler_host_tools.go:163-165` | removedSkillToolResult 返回 false 但 nil err |
| `handler_host_tools.go:200-217` | callHostTool 错误降级成 result |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `memory_read_tool.go:33-38` | reader=nil 静默返回 nil registry |
| `memory_read_tool.go:48-50` | HasTool 不 trim |
| `memory_read_tool.go:91-97` | parseMemoryReadScope：scope 必填，但调用方传空字符串走 ParseMemoryScope 默认行为 |
| `memory_write_tool.go:40-45` | writer=nil 静默 |
| `memory_write_tool.go:55-57` | HasTool 不 trim |
| `memory_write_tool.go:119-131` | scope 留空 fallback 到 default by type |
| `memory_write_tool.go:209-218` | CompositeRegistry.CallHostTool 用 fmt.Errorf 而非 AgentMemoryError |
| `memory_write_tool.go:158-173` | NewCompositeHostToolRegistry 全 nil 时返 nil |
| `stdio_mcp_client.go:48-56` | binary.Name/Env 字段未校验 |
| `stdio_mcp_client.go:89-95` | manifestEnv 不校验 key |
| `stdio_mcp_client.go:109-127` | CallTool 失败同时返回 result 和 error |
| `stdio_mcp_client.go:158-160` | resp.Error 转 string，丢失 code |
| `stdio_mcp_client.go:185-198` | nil receiver 走兜底 |
| `handler_host_tools.go:171-182` | callName 不 trim |
| `handler_host_tools.go:226-230` | content type "inputText" |
| `handler_host_tools.go:299-328` | ErrSkillMissingCWD 混合多种失败原因 |
| `subscribers.go:13-37` | tracker 不做 nil 校验 |

## 修复优先级

### P0（必须本周修）
1. **shadow 工具检测升级**：`handler_host_tools.go:124-138` 的 `seen[name]` 命中和 reserved host-only 被 peer 注册都应升级为 metrics + 告警；shadow 是潜在安全风险
2. `stdio_mcp_client.go:109-127` CallTool 失败同时返回 result 和 error，必须只返回 (nil, err)
3. `stdio_mcp_client.go:165-183` readMessage 取消后强制关闭 stdout pipe，避免 goroutine 泄漏
4. `handler_host_tools.go:200-217` callHostTool 错误必须 return error，不能降级成 result
5. `handler_host_tools.go:163-165` removedSkillToolResult 必须 return error
6. `handler_host_tools.go:299-328` cwd 解析错误分类，区分"未配置"、"resolver 故障"、"路径不存在"

### P1（本月）
7. `memory_read_tool.go:33`、`memory_write_tool.go:40` reader/writer=nil 装配期 panic
8. `memory_write_tool.go:209-218` CompositeRegistry.CallHostTool 用 contract.NewAgentMemoryError 包装
9. `memory_write_tool.go:158-173` NewCompositeHostToolRegistry 全空时记录 Warn 或 error
10. `stdio_mcp_client.go:158-160` 远端 error 用 CodedToolError 保留 code
11. `stdio_mcp_client.go:48-56` binary 入参 Validate
12. `handler_host_tools.go:25-67` listPeerToolsForCodex 改用 safego.Go
13. `handler_host_tools.go:171-182` validateHostToolGuards 入参 trim
14. `subscribers.go` tracker=nil 装配期 panic

### P2（下个 sprint）
15. memory_read/write `ListHostTools` schema marshal 错误用 panic + init-once
16. `memory_read_tool.go:48`、`memory_write_tool.go:55` HasTool trim
17. `stdio_mcp_client.go:152` id==0 响应区分通知 vs id=0 响应
18. `stdio_mcp_client.go:200` close 用 errors.Join 收集所有错误
19. `stdio_mcp_client.go:85` initialized 通知失败 log + close
20. `handler_host_tools.go:226` 改 "text" 类型（与 round-05 协同）

## 边界条件

1. **shadow 工具升级影响 mcp peer 兼容性**：peer 端可能因为历史原因一直在 ListTools 中暴露 `memory_read/memory_write`（只是被 host-only reservation 屏蔽）。改成 fatal 后会让旧 peer 启动失败。先做调用面盘点：peer 是否真的暴露这俩名字？
2. **`stdio_mcp_client.readMessage` goroutine 泄漏**：改为强制关闭 pipe 时要小心 Windows 下 NamedPipe 的 close 行为可能阻塞。`stdio_process_windows.go` 已有 process tree cleanup 路径，建议复用而非新实现。
3. **CallTool 同时返回 (result, err) 的修法**：上游 `entry.client.CallTool(...)` 在 `handler_peer_decode.go:380` 处只用 err 判断，已经会丢 result。统一为 `(nil, err)` 不会破坏现有行为。
4. **`removedSkillToolResult` 改为 error 的影响**：当前返回 false-result 是为了让 Codex 看到"工具已下线"的友好文本而不是 transport-level error。改为 error 后需要在更外层统一包装这条 hint，避免回退体验。
5. **`hostToolErrorResult` 包成 result+nil 的设计**：这是因为 host tool 错误（如 cwd 缺失）通常需要让 Codex"看到"错误内容然后调整重试参数。如果改为 error 透传，Codex 可能直接终止 turn。先确认 Codex 对 transport error 的重试策略再改。
6. **`memory_write` scope 留空 fallback**：当前 fallback 是为了"模型偷懒可以不填 scope"。改为必填会让所有 codex tool call 失败一波，需要 schema required 同步更新 + 模型 prompt 调整。
7. **`memory_read_tool.go` 与 `memory_write_tool.go` 几乎对称的 nil 兜底/schema marshal/HasTool trim**：所有修复点都要在两个文件配对修，否则一边 trim 一边不 trim 会引入更微妙的不一致。
8. **`stdio_mcp_client` 与 `mcpserver/common.StdioTransport` 的 close 顺序**：本轮发现 stdin Close 先于 transport Close。如果 transport 内部还有未发完的 buffered write，会丢失最后一条消息。修复 errors.Join 时确认顺序：先 transport 再 stdin。

---

下一轮范围建议：
- `internal/platform/difftracker/`（git diff emit、agent cwd resolve）
- `internal/platform/mcpcontrol/`（peer registry、tool instance、scope）
- `internal/platform/bus/`（event dispatcher、subscriber resilience、SubscriberSpec）
