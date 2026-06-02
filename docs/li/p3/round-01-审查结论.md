# 第 01 轮审查结论

## 审查范围

- `cmd/mcp-lsp/fx.go`（MCP LSP 主入口、tools/list、tools/call、bootstrap）
- `cmd/mcp-lsp/tools/factory.go`（工具入参解码、参数校验、handler wrapper）
- `internal/mcpserver/common/tool_result.go`（统一结果包装）
- `internal/mcpserver/common/structured_content.go`（structured content 兜底逻辑）
- `internal/mcpserver/common/tool_error_envelope.go`（错误信封、错误分类）
- `internal/platform/shared/log_error.go`（统一的"已忽略错误"入口）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `cmd/mcp-lsp/fx.go:80-83` | 兜底 | `OnLSPReleaseScope` 在 `runtimeManager == nil` 时返回 `(LSPReleaseScopeResult{}, nil)` | runtime 未注入是配置/装配 bug，静默返回空结果让上游误以为释放成功；后续会泄漏 LSP scope | runtime 未注入应直接 `panic`（fx 装配阶段）或返回 `errors.New("runtimeManager not initialized")`；scope 释放属关键路径，禁止零值兜底 |
| `cmd/mcp-lsp/fx.go:85-87` | 静默 | `OnShutdown` 用 `LogIgnoredError` 包裹 `shutdowner.Shutdown()` 错误 | shutdown 失败会被静默到日志；进程可能泄漏 goroutine/资源 | shutdown 是关键生命周期，错误必须传播到上游或退出码非零；至少应触发 fatal log 并 `os.Exit` |
| `cmd/mcp-lsp/fx.go:264-284` `handleScopedToolsCall` | 兜底 | `tp.CallTool` 失败时把 error 包成 envelope **不再返回** error，而是把 envelope 当 result 返回 | 上层 MCP server 依赖 `err != nil` 区分 transport vs tool error；这里把所有错误降级成 result 后 transport 层无法做重试/熔断 | 改为同时返回 `(envelope, originalErr)`；或在 transport 层显式约定"tool 错误信封不视为 transport 成功" |
| `cmd/mcp-lsp/fx.go:266-270` panic recovery | 静默 | `defer recover()` 后将 panic 转成 envelope 返回，没有 re-throw、没有进程级 metrics | panic 被吞，开发期暴露不出来；只在结果文本里看到 `internal_panic`，难定位 | recover 后必须：①写 fatal-level 结构化日志包含 stack；②触发告警 metrics；③高敏配置下 re-panic |
| `cmd/mcp-lsp/fx.go:307-315` `marshalInputSchema` | 弱契约 | `len(schema)==0` 时返回 `"{}"`；调用方可传 nil/空 map | 工具 schema 为空说明 manifest 装配错误，应暴露问题而不是降级成无 schema 工具 | manifest 阶段就拒绝空 schema；这里改为 `errors.New("tool schema is empty")` |
| `cmd/mcp-lsp/fx.go:220-238` `withRuntimeWorkspaceScopeFallback` | 兜底 | 没有 scope 也没有 runtime roots 时返回 `(ctx, nil)` 让后续工具自行处理 | 工具调用必须有 workspace scope，否则路径校验、URI 转换都会沦为相对路径；返回 nil error 等于鼓励下游用零值 | 强约束：scope 缺失 + roots 缺失时 `errors.New("workspace scope is required")` 直接拒绝调用 |
| `cmd/mcp-lsp/tools/factory.go:133-139` `normalizeOptionalToolParams` | 兜底 | 入参为 `null`/空时被替换成 `{}`，让 unmarshal 走默认值路径 | 任何"必填字段"都会因为零值通过 schema → tool 收到 `Action == ""`，再走到 `unsupportedActionError` 才报错；错误堆栈被掩盖 | 在 decoder 入口区分"未传"和"零值"。空 body 应直接报错，不应静默替换为 `{}`。strict 模式应是默认 |
| `cmd/mcp-lsp/tools/factory.go:285-289` `missingDependencyHandler` | 静默 | manager 未注入时返回一个永远报错的 handler，但工具仍在 ListTools 中暴露 | 客户端调用才发现工具不可用；MCP listing 阶段未过滤 | 装配阶段 `panic` 或在 `ListTools` 内过滤掉无 handler 的工具 |
| `cmd/mcp-lsp/tools/factory.go:511-513` `executeSandbox` | 弱契约 | 仅校验 `sandbox.Run` 返回值，未校验 `request` 必填字段 | `lspexec.Request` 零值（command="" 等）下游会跑 `exec.LookPath("")` 等异常 | 入口先 `request.Validate()`；同时 `executeSandbox` 应拒绝 `mode==""`、`language==""` |
| `internal/mcpserver/common/tool_result.go:47-73` `ResolveToolResultText` | 兜底 | value=nil 时返回字符串 `"null"`，**且 err=nil** | 工具 handler 不应返回 nil；这里把 bug 当"成功"渲染给 LLM | 改为 `errors.New("tool returned nil result")`，让上层走 envelope 错误路径 |
| `internal/mcpserver/common/structured_content.go:16-29` `StructuredContentFromRaw` | 兜底 | 空 raw / `null` 时返回 `{}` | 与 tool_result.go:47 同根问题——nil 结果被当合法 | 与上一条配套修复，删除该 fallback |
| `internal/mcpserver/common/tool_error_envelope.go:129-151` `ToolResultIsError` | 兜底 | json marshal 失败时返回 `true`（视为错误）但不传 err；后续仍按"成功"模板渲染 | marshal 失败说明 result 类型有问题，应是 fatal | 调用链应整体返回 `(value, error)`，marshal 失败一路向上抛 |
| `internal/mcpserver/common/tool_error_envelope.go:177-182` `NewCodedToolError` | 弱契约 | `err == nil` 时用 code 文本造一个 fake error | 屏蔽了"错误码无对应错误"这种调用方 bug | nil err 直接 `panic("code without error")` 或返回 `nil`，禁止造假 error |
| `internal/mcpserver/common/tool_error_envelope.go:223-239` `ClassifyToolError` | 弱契约 | `err == nil` 时返回 `("unknown", false, ..., nil)` 而不是 panic | 调用方传 nil 表示分类逻辑被错误使用；当作 unknown 错误返回会污染 envelope | nil error 作为不可达分支，应 panic 或 `return "", false, "", nil` 让上层判空 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `cmd/mcp-lsp/fx.go:86` `OnShutdown` | `LogIgnoredError` 包裹 shutdown error |
| `cmd/mcp-lsp/fx.go:392` `bindRuntime requestShutdown` | 同上 |
| `cmd/mcp-lsp/fx.go:266-270` `handleScopedToolsCall` defer | recover 转 envelope 不 re-panic |
| `cmd/mcp-lsp/fx.go:413-417` `RunGroup` 分支 | `if err != nil && !errors.Is(err, context.Canceled)` 仅打 log，未传 shutdown 失败原因 |
| `internal/platform/shared/log_error.go` 整体 | 该函数本身就是"静默化"的语义；公司级 helper 反向鼓励静默 |
| `internal/mcpserver/common/tool_error_envelope.go:138` | json marshal 错误转 `true` 后吞掉 |
| `internal/mcpserver/common/tool_error_envelope.go:147` | unmarshal 错误转 `true` 后吞掉 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `cmd/mcp-lsp/fx.go:120-129` `newServer` | `mcpStdout == nil` fallback 到 `os.Stdout`，应在装配阶段就要求显式传入 |
| `cmd/mcp-lsp/fx.go:143-169` `ListTools` | 未校验 `def.Manifest.Name == ""`、`Description == ""` 等必填 |
| `cmd/mcp-lsp/fx.go:307-315` `marshalInputSchema` | 空 schema 静默通过 |
| `cmd/mcp-lsp/tools/factory.go:63-80` `newManagerTool` | `registry == nil` 走 `missingManagerHandler` 而非装配期 panic |
| `cmd/mcp-lsp/tools/factory.go:82-93` `newSandboxTool` | sandbox=nil 延迟到调用时才报；应该构造时 panic |
| `cmd/mcp-lsp/tools/factory.go:117-118` `decodeLenientToolParams` | "lenient" 本身就是反 fail-fast，建议下沉只在历史兼容字段使用 |
| `cmd/mcp-lsp/tools/factory.go:559-561` `funcRangeEnricher.Symbols` | 接收者 `p == nil` 时返回 error 而不是 panic；nil receiver 是调用方 bug |
| `cmd/mcp-lsp/tools/factory.go:295-300` `managerForFile` | 同样的 nil registry 软失败 |
| `internal/mcpserver/common/tool_result.go:82-100` `BuildToolCallResult` | 入参 `value` 没有 nil 校验，依赖下游 ResolveToolResultText 的 fallback |
| `internal/mcpserver/common/tool_error_envelope.go:193-221` `NewToolErrorEnvelopeWithMeta` | `toolName` / `err` 均无强校验；`toolName == ""` 会渲染出 `"Tool error in \"\""` |

## 修复优先级

### P0（必须本周修）
1. `handleScopedToolsCall` panic recovery → 必须 fatal log + metrics 上报，可选 re-panic（`fx.go:266-270`）
2. `OnLSPReleaseScope` 返回零值 → 改为 hard error（`fx.go:80-83`）
3. `OnShutdown` / `bindRuntime` 的 `LogIgnoredError` → shutdown 错误必须传播（`fx.go:86, 392`）
4. `ResolveToolResultText` / `StructuredContentFromRaw` 的 nil 兜底 → 直接报错（`tool_result.go:47`、`structured_content.go:16`）

### P1（本月）
5. `withRuntimeWorkspaceScopeFallback` scope 缺失 → 拒绝调用而非降级（`fx.go:220`）
6. `decodeLenientToolParams` → 限定使用范围；新工具一律 strict（`factory.go:117`）
7. `missingDependencyHandler` 模式 → 装配阶段 panic + ListTools 过滤（`factory.go:285`）
8. `NewCodedToolError(nil err)` 造假 error → 移除（`tool_error_envelope.go:177`）
9. `marshalInputSchema` 空 schema 兜底 → 改为 error（`fx.go:307`）

### P2（下个 sprint）
10. `ToolResultIsError` json 错误处理路径整改（`tool_error_envelope.go:129`）
11. `NewToolErrorEnvelopeWithMeta` 入参校验
12. `LogIgnoredError` 全局收紧 —— 列入"使用一处审一处"清单，不允许新增调用点
13. `funcRangeEnricher.Symbols` nil receiver → panic 语义改造

## 边界条件

1. **不要破坏 MCP 错误信封语义**：MCP spec 要求 `tools/call` 即使工具内部失败也返回 200 + `isError=true`。修复 `handleScopedToolsCall` 时不能让 transport 层直接返 JSON-RPC error；正确做法是工具错误进 envelope，transport/装配错误再升级。
2. **fx 生命周期**：`bindRuntime`、`OnStart/OnStop` 内部错误升级为 fatal 必须确保 OnStop 仍能被调用，否则 sidecar 不能优雅退出。
3. **测试包同步**：`tools_call_plain_text_test.go`、`tools_call_budget_test.go`、`tools_test.go` 中可能依赖当前 nil → "null" 的兜底行为，修 P0 时必须同步更新断言。
4. **`LogIgnoredError` 的合法剩余场景**：仅限 close defer（如 `defer file.Close()` 在已经处理过主路径错误的情况下）。其余调用都应纳入 P2 整改。
5. **lenient decode 历史兼容**：legacy `{file_path, line, column}` 用户已迁移到 `pos`，`decodeLenient` 现存价值很小；但如果有外部 MCP 客户端尚未升级，需先做调用面盘点再砍。
6. **panic re-throw 与 sidecar**：mcp-lsp 是 sidecar，单次 panic 不应拖死宿主；可走"日志 + metrics + 标记 tool 不可用"，不一定 re-panic。本轮"必须 re-panic"建议指开发期/测试构建。

---

下一轮范围建议：`internal/mcpserver/common/server.go` + `http_transport.go` + `bootstrap/`，以及 `internal/platform/runner/`（runner 注册和 sidecar 生命周期）。
