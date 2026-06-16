# 第 02 轮审查结论

## 审查范围

- `internal/mcpserver/common/server.go`（stdio 服务器主循环、JSON-RPC 分发、tools/list、tools/call）
- `internal/mcpserver/common/http_transport.go`（HTTP 传输的 MCP server）
- `internal/mcpserver/common/stdio.go`（stdio framing/raw 传输）
- `internal/mcpserver/common/scope.go`（ToolScope、上下文键、normalize）
- `internal/mcpserver/common/bootstrap/client.go`（control-plane jrpc2 客户端、注册、callback drain）
- `internal/mcpserver/common/bootstrap/hooks.go`（钩子订阅/分发/replay）

> 与第 01 轮已覆盖的 `tool_result.go`、`structured_content.go`、`tool_error_envelope.go` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `server.go:171-217` `Server.Run` | 兜底 | `<-ctx.Done()` 路径执行 `_ = s.transport.Close(); return nil` | ctx 取消是"被动停止"信号，但 `Close` 错误被吞，且最外层返回 `nil` 让 fx 误判优雅退出 | 返回 `ctx.Err()`；`Close` 错误用 `errors.Join` 一并返回 |
| `server.go:188-192` 读通道关闭分支 | 静默 | `if !ok { return nil }` 直接静默 | read goroutine 异常关闭通道（如 panic 后 close）等于 transport 死亡，应当返回 error 让 fx 重启或上报 | 返回 `errors.New("server: read channel closed unexpectedly")` |
| `server.go:219-229` `startReadLoop` | 静默 | `defer recover()` 后只记录 error log，不通过 `results` 通知主循环、不退出 | panic 被吞后 read goroutine 终止，主循环阻塞在 `<-results` 永不退出 | recover 后必须 `results <- readResult{err: panicErr}` 再 `close(results)`；最好直接 `os.Exit(2)` 或上报 fatal |
| `server.go:354-359` `listTools` | 兜底 | `s.tools == nil` 时返回 `(nil, nil)` | tools 为 nil 是装配 bug；返回空列表掩盖问题 | `if s.tools == nil { return nil, errors.New("tool provider not configured") }` |
| `server.go:396-407` `callToolSafely` | 静默 | `defer recover()` 后将 panic 转 `NewPanicToolError`，无 stack、无 metrics、无 fatal log | 与 round-01 fx.go:266 同根；panic 会被反复转 envelope 而无人发现 | recover 后 `pkglogger.Error("...", "stack", debug.Stack())`；可选 re-panic 或 metrics 计数 |
| `server.go:285-288` `handleInitialize` | 兜底 | `protocolVersion` 缺失时 fallback 到 `"2024-11-05"` | 协议版本应是必填；客户端不传应拒绝 | 空版本号直接 `errorResponse(req.ID, codeInvalidParams, "protocolVersion is required")` |
| `http_transport.go:105-106` `handleMCP` 鉴权失败 | 静默 | `w.WriteHeader(http.StatusUnauthorized)` 后无 audit log | 401 不留痕，攻击者扫描无感知；与公司级安全审计要求冲突 | `pkglogger.Warn("mcp http: unauthorized", "remote", r.RemoteAddr, "ua", r.UserAgent())` |
| `http_transport.go:131-132` 写响应错误 | 静默 | `if err := json.NewEncoder(w).Encode(resp); err != nil { pkglogger.Warn(...) }` 仅 log | 客户端可能拿到部分写入的响应；无 metrics、无重试 | 至少 `w.Header().Set("Connection", "close")` 强制断连；记录到 prometheus counter |
| `http_transport.go:194-203` `handleToolsList` | 兜底 | `h.tools == nil` 时返回 `{"tools": []}` | 同 server.go:354 | 与 stdio 路径同步改为返回 500 |
| `http_transport.go:91-97` `Stop` | 弱契约 | `if h.server == nil { return nil }` | server 未启动就调 Stop 是装配 bug；返回 nil 掩盖调用顺序错误 | `errors.New("http server not started")` |
| `http_transport.go:244` `writeJSONError` | 静默 | `_ = json.NewEncoder(w).Encode(resp)` 显式忽略错误 | 错误响应都写不下去时连日志都没有 | 至少 `pkglogger.Warn` 一行 |
| `stdio.go:42-49` `Close` | 兜底 | `if t == nil || t.closer == nil { return nil }` | `t == nil` 是调用方 bug；这里掩盖了 nil receiver 调用 | `t == nil` 应该 panic；只允许 `closer == nil`（stdin 不可关）走 nil 路径 |
| `stdio.go:51-68` `ReadMessage` | 静默 | `ensureMode` 失败仅 `Warn`，但仍把 err 透传上层 | `pkglogger.Warn` + 透传 err 让上层认为可恢复，实际可能是 EOF/格式破损不可恢复 | 区分 EOF/IO 错误与协议错误；后者应升级为 fatal |
| `stdio.go:70-95` `WriteMessage` | 静默 | marshal/write 各分支都是 `Warn` + return err | 与上一条一样的 log+raise 模式重复 | 把 log 改为只在最外层做（避免冗余日志），错误本身要原样返回 |
| `scope.go:78-91` `WithToolScope` | 兜底 | `scope.isEmpty()` 时返回原 ctx，调用方收不到任何错误 | 无 scope 的 tools/call 直接进入 handler，后续用 `WorkspaceRootFromContextStrict` 才报错；调用栈不可追溯 | 不要在这里降级；让上层（handleToolsCall）显式校验 scope 必备字段 |
| `scope.go:67-76` `NormalizeToolScope` | 弱契约 | 全字段 `TrimSpace`、家族字符串别名、CWD 失败时置空 | "失败时置空"等于把"非法 CWD"当合法处理 | 非法 CWD（绝对路径校验失败）应返回 error，由调用方决定是否兜底 |
| `scope.go:129-141` `normalizeScopeCWD` | 静默 | 非绝对路径直接返回 `""` | 调用方无法分辨"未传"与"传了非法值" | 同上，应返回 (string, error) |
| `scope.go:171-186` `normalizeWorkspaceRoot` | 静默 | 非法相对路径返回 `""` | 调用方无感知；过滤行为隐藏配置错误 | 同上 |
| `bootstrap/client.go:108-117` `Start` | 兜底+静默 | RPCAddr 缺失先打 `Warn` 再返回 error | 警告 + error 双信号反而模糊；`pkglogger.Warn` 后再返回 errors.New 不应同时存在 | 删 `Warn`；让 caller 决定是否 log；error 文本足够定位 |
| `bootstrap/client.go:135-137` Start 失败的 Close | 静默 | `_ = c.Close()` 显式忽略 close 错误 | Close 失败可能意味着 callback 泄漏；这里完全静默 | `closeErr := c.Close(); if closeErr != nil { return errors.Join(err, closeErr) }` |
| `bootstrap/client.go:255-304` `Close` | 静默 | `flushQueuedReportsWithConn` 内错误未 return；`finalReport` 失败仅 Warn；callback drain 超时仅 Warn 后照常 close | 关键路径错误全部降级到日志；服务关闭异常不会被 fx/上层感知 | 用 `errors.Join` 合并多源错误，原样返回 |
| `bootstrap/client.go:311-324` `drainCallbacks` | 静默 | goroutine 内 `defer func() { _ = recover() }()` 完全空 recover | 用户在 OnShutdown/OnConfigChanged 里写 panic 会被吞 | recover 后必须打 fatal log + stack；否则等于鼓励 callback 不关心 panic |
| `bootstrap/hooks.go:135-147` `handleHookBefore` | 兜底 | `handler == nil` 时返回 `Decision: HookDecisionDeny` 并返回 `(_, true, nil)` | 钩子未注册被当成 "拒绝" 决策成功返回；上游无法区分 "未实现" 与 "明确拒绝" | 返回 `(nil, true, errors.New("hook OnBefore not configured"))`；让 control-plane 判断 |
| `bootstrap/hooks.go:149-161` `handleHookCheck` | 兜底 | `handler == nil` → `HookDecisionContinue`（默认放行） | 钩子检查未实现却返回 "继续" 是默认放行，违反 fail-closed | 返回 error；或至少返回 `HookDecisionDeny` |
| `bootstrap/hooks.go:163-175` `handleHookAfter` | 兜底 | `handler == nil` → `HookDecisionReject`（默认拒绝） | 与 OnCheck 默认放行不一致；语义混乱 | 三个 hook 必须统一策略；建议都为返回 error |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `server.go:186` | `_ = s.transport.Close()` ctx 取消路径 |
| `server.go:222-226` | readLoop panic 仅 log，不通知主循环 |
| `server.go:396-402` | callToolSafely panic recover 无 stack |
| `http_transport.go:75-83` | Serve goroutine panic 仅 log，不上报 |
| `http_transport.go:81-83` | `serve error` 非 ErrServerClosed 仅 Warn，没有上报或退出 |
| `http_transport.go:108` | `defer r.Body.Close()` 未保留 close 错误 |
| `http_transport.go:131` | json.NewEncoder.Encode 错误仅 Warn |
| `http_transport.go:244` | `_ = json.NewEncoder(w).Encode(resp)` 写错误响应忽略 |
| `stdio.go:53,64,73,81,85,91` | 多处 `Warn` + `return err` 双重写日志 |
| `bootstrap/client.go:135` | `_ = c.Close()` Start 失败路径 |
| `bootstrap/client.go:280-287` | finalReport 失败仅 Warn |
| `bootstrap/client.go:294-298` | callback drain 超时仅 Warn |
| `bootstrap/client.go:314` | drainCallbacks 内 `_ = recover()` 空 recover |
| `bootstrap/hooks.go:78-88` `markReplayFailure` | 错误对象只保留 `.Error()` 文本 |
| `bootstrap/hooks.go:330-337` | replay 重试仅 Warn |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `server.go:155-163` `NewServer` | name/version 为空时塞默认值；transport/tools 未做 nil 校验 |
| `server.go:171-174` `Run` | `ctx == nil` 自动用 `context.Background()`，掩盖调用方传 nil 的 bug |
| `http_transport.go:38-52` `NewHTTPServer` | 同上 name/version；`tools == nil` 不检查 |
| `http_transport.go:46-50` 选项遍历 | `if opt != nil` 跳过，应该禁止 nil option |
| `http_transport.go:56-87` `Start` | listenAddr 空字符串 fallback 到 `127.0.0.1:0` |
| `http_transport.go:135-147` `authorized` | `bearerToken == ""` 直接放行；正式部署不应允许空 token |
| `stdio.go:33-42` `NewStdioTransport` | stdin/stdout 为 nil 时直接构造，调用 ReadMessage 才崩 |
| `stdio.go:97-120` `ensureMode` | 仅根据首字节是 `{`/`[` 区分模式；非法首字节直接默认走 framed，没有显式 error |
| `scope.go:45-51` `DecodeToolCallParams` | 失败时返回零值 + err，但调用方多处只判 err；零值 ToolCallParams 进入下游会被 normalize 出空 scope |
| `scope.go:53-65` `Scope` | 不校验 `Family` 是否在白名单 |
| `scope.go:115-127` `normalizeScopeFamily` | switch 默认返回原值 trim，等于宽容陌生 family |
| `bootstrap/client.go:71-93` `Config` | 30+ 字段无任何 builder/validator；`New` 内的 `normalizeConfig` 只塞默认值 |
| `bootstrap/client.go:103` `New` | `cfg.ReportQueueLimit == 0` 用 `ClampLimit` 兜底为 `defaultReportQueueLimit` |
| `bootstrap/client.go:118-120` `Start` | ctx nil 兜底为 Background |
| `bootstrap/hooks.go:140-141` payload Unmarshal | 失败返回 `(nil, true, err)` 但 `handled=true` 暗示已被钩子处理 |
| `bootstrap/hooks.go:255-259` `PendingHooks` | 仅校验 agentID；topics、scope 等其它入口都不强校验 |

## 修复优先级

### P0（必须本周修）
1. `Server.Run` ctx 取消路径返回 `ctx.Err()` 而非 nil；`transport.Close()` 错误 join（`server.go:171-217`）
2. `startReadLoop` panic 必须通知主循环退出，否则 server 死锁（`server.go:219-229`）
3. `callToolSafely` panic recover 增加 stack + metrics（`server.go:396-407`，与第 01 轮 fx.go:266 配套修复）
4. `bootstrap.Hook*` handler == nil 三个分支语义统一（`bootstrap/hooks.go:135/149/163`），当前 OnBefore=Deny / OnCheck=Continue / OnAfter=Reject 三种结果是事故温床
5. `drainCallbacks` 的 `_ = recover()` 空 recover 改为 fatal log（`bootstrap/client.go:314`）
6. `http_transport.go:104-107` 401 鉴权失败必须 audit log

### P1（本月）
7. `Server.listTools` 与 `HTTPServer.handleToolsList` 在 tools=nil 时改报错（`server.go:354`、`http_transport.go:194`）
8. `Server.handleInitialize` protocolVersion 缺失改报错（`server.go:285`）
9. `bootstrap.Client.Close` 多源错误 `errors.Join` 后整体返回（`bootstrap/client.go:255-304`）
10. `scope.normalizeScopeCWD` / `normalizeWorkspaceRoot` 非法路径返回 error（`scope.go:129-186`）
11. `Server.Run`、`HTTPServer.Start` 的 ctx==nil 兜底删除，调用方必传
12. `WithToolScope` empty 直接拒绝（`scope.go:78-91`）
13. `stdio.ensureMode` 非法首字节显式 error（`stdio.go:97-120`）

### P2（下个 sprint）
14. `NewServer` / `NewHTTPServer` name/version 默认值删除，构造期 panic
15. `bootstrap.Config` 加 Validate()，所有必填字段构造期校验
16. 删除 `stdio.go` 中重复的 Warn + return err 模式
17. `http_transport.authorized` 空 token 时直接拒绝
18. `bootstrap.hooks.markReplayFailure` 持久化原始 error，不要只保留字符串

## 边界条件

1. **MCP 协议版本兜底**：`handleInitialize` 现在的 `2024-11-05` fallback 是为了兼容老客户端。修复时要先盘点线上客户端是否真的会不传版本；如果是，改为 P1 级别警告返回，不要直接 hard error。
2. **rpc 鉴权审计**：401 加 audit log 时注意脱敏 Authorization 头；只能记 prefix。
3. **fx 生命周期顺序**：`Server.Run` 改返回 ctx.Err 后，fx 的 OnStop 可能把 cancel 当作错误上报；需同步检查 `bindRuntime` 对 `RunGroup` 的错误判断（已在 fx.go:413 用 `errors.Is(err, context.Canceled)` 过滤，要保证一致）。
4. **bootstrap hook 默认行为变化是 wire-protocol 改动**：当前 `OnBefore=Deny`、`OnCheck=Continue`、`OnAfter=Reject` 是已部署在 control-plane 的契约。任一改动需走 mcp/dto 版本协商，不能单边修。建议改成"全部返回 error 由 control-plane 决策"前先发 RFC。
5. **callback drain 的 recover**：把 `_ = recover()` 改成 fatal log 后，注意 `drainCallbacks` 自身的 wait goroutine 要在 panic 时也能 close(done)，否则 select 永远走 timeout。修法是 defer 内先 close 再 log。
6. **测试同步**：`server_test.go`、`http_transport_auth_test.go`、`server_scope_test.go`、`bootstrap/*_test.go` 均会受 P0/P1 改动影响。`callback_drain_test.go` 尤其要重写预期。
7. **Windows 路径**：`scope.go` 中 `isSlashRootedPOSIXPath` 与 `isWindowsDriveAlias` 是为 Windows MCP 客户端把 `D:\foo` 写成 `/D:/foo` 准备的兼容层。修 normalize 报错路径时不要破坏这个分支。

---

下一轮范围建议：`internal/platform/runner/`（runner 注册、生命周期、信号处理）+ `internal/platform/runtimesafe/`（SafeGo、recover 工具）+ `internal/platform/runtimeenv/`（LSP bundle 装载）。
