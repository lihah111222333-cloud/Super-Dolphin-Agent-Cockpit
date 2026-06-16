# 第 04 轮审查结论

## 审查范围

- `internal/util/safego/safego.go`（与 runtimesafe.SafeGo 对照）
- `internal/platform/config/config.go`（Config 加载、env fallback、.env 解析、project root 解析）
- `internal/platform/config/timeouts.go`（超时常量与 With* helper）
- `internal/platform/toolbridge/proxy.go`（toolbridge HTTP proxy、JSON-RPC dispatch、tools/list、tools/call）
- `internal/platform/toolbridge/diff_fallback.go`（ToolCallEnd 后的 git diff fallback 跟踪器）

> 与第 01-03 轮覆盖的 `cmd/mcp-lsp/`、`mcpserver/common/`、`bootstrap/`、`platform/runner/`、`runtimesafe/`、`runtimeenv/` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `util/safego/safego.go:1-53` 整个包 | 兜底+死代码 | 与 `internal/platform/runtimesafe/safego.go` **代码逐字相同**（包名/函数名不同），调用点混用 | 双实现并存，修复 panic 处理时只改一处会导致行为漂移；增加心智成本 | 删除其一（建议保留 `runtimesafe.SafeGo`，把 `util/safego.Go` 改为 `Deprecated: use runtimesafe.SafeGo`，逐步迁移），核心是统一收口 |
| `util/safego/safego.go:26-28` `Go` | 静默 | `if fn == nil { return }` 直接 noop | 调用方传 nil 是 bug；这里完全无声 | nil fn 直接 panic |
| `util/safego/safego.go:32-50` panic recover | 静默 | recover 后只 log，无 metrics、无回调通知 | 与第 3 轮 runtimesafe 同根问题；goroutine panic 静悄悄消失 | 加 metrics counter + 可选 done channel |
| `config/config.go:26-65` `New` | 兜底 | 几乎所有字段都用 `envOr` / `envBoolOr` / `envPositiveIntOr` 链式 fallback 到默认值 | "未配置"与"配置错"被同一路径吞掉。`envBoolOr` 的 `ParseBool` 失败也走 fallback，用户写错值不会得到任何提示 | 区分"未传"（用 default）与"传了非法值"（return error）；至少 envBoolOr/envPositiveIntOr 解析失败 log+error |
| `config/config.go:38` `RPCAddr` | 兜底 | 默认值 `127.0.0.1:8090` | RPC 地址是关键路径配置，缺失应是装配错误而不是默认到 8090 | 删除默认值，缺失即 error；让 `runtimeenv` 在装配阶段保证它存在 |
| `config/config.go:218-234` `resolveProjectRoot` | 兜底+静默 | `os.Getwd()` 失败时返回 `""` | 进程级失败被当成"用空字符串"，下游 `loadDotEnv` 收到空字符串又静默 return nil | `os.Getwd` 失败必须返回 error；项目 root 解析失败应是 fatal |
| `config/config.go:101-119` `loadDotEnv` | 兜底 | projectRoot=="" 静默 return nil；非 packaged 时 `.env` 读不到也 return nil | 隐藏 .env 配置错误。开发者拼错路径 → 没有 env 也跑起来，行为飘忽 | 至少 log；packaged 已 hard error，dev 也应至少 Warn |
| `config/config.go:121-138` `applyDotEnv` | 静默 | non-strict 路径下 parse 错误 `continue`，不打印行号 | 用户写错 .env 一行就被吞 | non-strict 也要 Warn 一条带行号的日志 |
| `config/config.go:140-146` `parseDotEnvLine` | 兜底 | strict 版本返回 error 后被 wrapper 吞成 bool | 重复实现两份，宽松版本完全无声 | 删除非 strict 版本，调用方直接用 strict |
| `config/config.go:163` `value = strings.Trim(value, ` "'`)` | 弱契约 | 单/双引号混用裸 trim | `'value"` 这种值会被误处理；也无 escape 处理 | 实现真正的 quote-aware 解析，或拒绝混合引号 |
| `config/config.go:252-257` `envOr` | 兜底 | 通用三参数 fallback | 配套 `envOrCompat` 才有 deprecation log；裸 `envOr` 无任何提示 | 在该 helper 内加可选 `onMissing` callback；或全部改成 `envOrCompat` 强制留痕 |
| `config/config.go:259-268` `envOrCompat` | 兜底 | 三层 fallback：canonical → legacy → 字面默认值 | 用户同时设了 canonical 和 legacy 不报错；"两个都设"是 bug | canonical+legacy 同时存在时返回 error 或 fatal log |
| `config/config.go:270-280` `envBoolOr` | 兜底 | `ParseBool` 失败走 fallback | 用户写 `SKILL_PROGRESSIVE_DISCLOSURE=enabled` 拿到的是 false（fallback），无任何提示 | 解析失败必须返回 error/panic |
| `config/config.go:282-292` `envPositiveIntOr` | 兜底 | `Atoi` 失败、值 ≤0 都走 fallback | 用户写 `NOTIFY_TIMEOUT_SECONDS=-1` 拿到 10，业务静悄悄异常 | 同上，解析失败/非法值返回 error |
| `toolbridge/proxy.go:57-72` `ServeProxy` | 静默 | `errors.Is(err, http.ErrServerClosed) \|\| net.ErrClosed` 时返回 nil；其它返回 err | 这里相对正确，但**没有给上层任何"已正常停止"信号**，调用方无法区分 server 是被显式 Stop 的还是 listener 异常关闭 | 返回结构化的 sentinel：`server.ErrStopped` vs 其他 |
| `toolbridge/proxy.go:74-118` `handleProxyRequest` | 静默 | `r.Body.Close()` defer 错误被忽略 | 一般可接受，但项目要求 fail-fast，应至少捕获错误 | `defer func() { if err := r.Body.Close(); err != nil { h.warn(...) } }()` |
| `toolbridge/proxy.go:201-220` `handleProxyOrchToolsList` | 兜底 | peer tools 加载失败时，如果 host tools 非空就只返回 host tools + Warn | 客户端拿到"半套"工具列表，模型调用本应可见的 peer tool 时报"unknown tool"；难以归因 | 返回 jsonRPC error，让客户端重试；不要降级返回部分结果 |
| `toolbridge/proxy.go:257-260` `handleProxyToolCall` | 兜底 | `result == nil` 时构造 `&ToolCallResult{Success: true}` | nil result 是 dispatcher bug，当作"成功无内容"会让客户端拿到误导性的 200 | nil result 应返回 jsonRPC internal error |
| `toolbridge/proxy.go:285-303` `publishProxyToolCallEnd` | 静默 | `h == nil \|\| h.dispatcher == nil` 或 ThreadID 为空时直接 return | 关键事件 publish 失败无 metrics、无 log；遥测路径漏报无感知 | 至少 debug log："proxy tool call end skipped"；ThreadID 为空是约束破坏，应当 panic 或 error |
| `toolbridge/proxy.go:321-333` `proxyToolResultPreview` | 静默 | `json.Marshal` 失败返回 `""` | preview 拿到空字符串与"成功无输出"无法区分 | 返回 marshal 错误的 string 描述，或在 publish 时附 marshal_error 字段 |
| `toolbridge/proxy.go:391-395` `writeProxyJSON` | 静默 | `_ = json.NewEncoder(w).Encode(resp)` 显式忽略 | 与第 02 轮 writeJSONError 同根 | 加 Warn log |
| `toolbridge/proxy.go:368-373` `callIDFromJSONRPCID` | 弱契约 | 用 `fmt.Sprintf("%v", id)` 把任意类型转字符串 | id 类型靠默认 fmt 行为；浮点 id（JSON number）会变成 `1234.0` 形式 | id 必须是 string 或 number，分别处理 |
| `toolbridge/diff_fallback.go:36-42` `MarkSeen` | 静默 | `t == nil` 直接 return | nil receiver 调用是装配 bug | nil receiver 应 panic |
| `toolbridge/diff_fallback.go:46-67` `handleToolCallEnd` | 兜底+静默 | nil tracker、nil emitter、callID 为空、resolver 失败、cwd 空、git diff 错误，**全部静默 return** | diff 跟踪是用户可见特性；这条路径里有 6 处早 return 都不打 log，user-facing diff 丢失时无任何线索 | 每条 early-return 至少 debug log 一句，标明原因；非 nil receiver 校验改成 panic |
| `toolbridge/diff_fallback.go:62-66` emit 失败 | 静默 | emit 失败 Warn 后 `MarkSeen(callID)` 跳过 | 已经 fail 的 callID 被标记成"已处理"；下次 retry 不会再尝试 | emit 失败不应 MarkSeen |
| `toolbridge/diff_fallback.go:77-92` `resolveCWD` | 兜底 | nil resolver 返回 `("", false)`；agentID 空返回同样；resolve error → Warn + 返回 false | 调用方无法区分"无 resolver"、"无 agentID"、"resolver 出错" | 改返回 `(string, error)` |
| `toolbridge/diff_fallback.go:94-106` `currentGitDiff` | 静默 | 非 git 仓库 ErrNotGitRepository 静默；其它错误 Warn 后返回 false | 业务上"非 git 仓"不报是合理的；但 Warn 后吞错让 fallback 路径完全不可观测 | 加 metrics 计数；保持 Warn |
| `config/timeouts.go:1-57` 整个文件 | 弱契约 | 全部是 `ctxutil` 同名常量与函数的 thin wrapper | 双层别名增加调用方迷惑：到底用 config.WithTimeout 还是 ctxutil.WithTimeout？ | 长期用 type alias 收敛或彻底移除一层 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `util/safego/safego.go:26-28` | nil fn 静默 return |
| `util/safego/safego.go:32-50` | panic recover 仅 log |
| `config/config.go:101-119` | dev 模式 .env 读取失败静默 return nil |
| `config/config.go:121-138` | non-strict 解析错误 continue 不打日志 |
| `config/config.go:218-234` `resolveProjectRoot` | `os.Getwd` 失败返回 `""` |
| `config/config.go:270-280` `envBoolOr` | ParseBool 失败 fallback |
| `config/config.go:282-292` `envPositiveIntOr` | Atoi 失败 fallback |
| `toolbridge/proxy.go:81` | `defer r.Body.Close()` 错误忽略 |
| `toolbridge/proxy.go:201-220` | peer tools 失败时部分降级 + Warn |
| `toolbridge/proxy.go:257-258` | nil result 当作成功 |
| `toolbridge/proxy.go:285-303` | publish 早 return 无 log |
| `toolbridge/proxy.go:321-333` | json.Marshal 失败返回 "" |
| `toolbridge/proxy.go:391-395` | Encode 错误显式忽略 |
| `toolbridge/diff_fallback.go:36-42` | nil receiver 静默 |
| `toolbridge/diff_fallback.go:46-67` | 6 处 early-return 无 log |
| `toolbridge/diff_fallback.go:62-66` | emit 失败仍 MarkSeen |
| `toolbridge/diff_fallback.go:77-92` | resolveCWD 多种失败状态合并成 (空, false) |
| `toolbridge/diff_fallback.go:94-106` | 非 git 仓库静默 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `util/safego/safego.go:25` `Go` | fn=nil 静默；ctx=nil 兜底 background；logger=nil 兜底全局；label 无校验；与 runtimesafe 双实现 |
| `config/config.go:26` `New` | 完全无入参，副作用全局（os.Getenv + os.Setenv）；不可单元测试无 fixture |
| `config/config.go:42-55` `Skill/Notify/Agent` | 多嵌套 struct 全字段都靠 envXxxOr 兜底，无 Validate |
| `config/config.go:259-268` `envOrCompat` | canonical 与 legacy 同时存在不冲突拒绝 |
| `config/config.go:148-165` `parseDotEnvLineStrict` | 引号处理是 `strings.Trim(value, ` "'`)`，不区分单双引号；不处理 escape；不允许引号内含等号以外的特殊字符 |
| `config/config.go:174-184` `hasPackagedRuntimeManifest` | 入参 projectRoot 若为空，filepath.Join 出 "runtime-manifest.json"，去当前目录 stat |
| `toolbridge/proxy.go:57-63` `ServeProxy` | h/ln nil 校验在调用入口，但 listener address 等非 nil 字段无校验 |
| `toolbridge/proxy.go:138-156` `decodeProxyToolCallParams` | name 必填校验完整，但 arguments 为 null/空时静默替换为 `{}`（与第 01 轮 normalizeOptionalToolParams 同根） |
| `toolbridge/proxy.go:158-167` `validateProxyToolFamily` | 仅校验 family-vs-tool 对齐；family 来自 URL path 未做白名单校验 |
| `toolbridge/proxy.go:201-220` | 兼容性强：host tools 与 peer tools 都允许部分缺失 |
| `toolbridge/proxy.go:222-269` | callReq 字段全靠 trimspace，没 Validate；threadID 在 family=lsp 时还会兜底成 agentID |
| `toolbridge/proxy.go:346-366` `resolveProxyThreadID` | bindingStore=nil 时 LSP 兜底 agentID 当 threadID；其它 family 直接 return "" |
| `toolbridge/diff_fallback.go:23-32` `newDiffFallbackTracker` | logger=nil 兜底全局；emitter/resolver=nil 不校验 |
| `toolbridge/diff_fallback.go:108-115` `shouldFallbackDiffTool` | 工具白名单写死，新增 edit-类工具时容易漏 |

## 修复优先级

### P0（必须本周修）
1. **统一 safego 双实现**：把 `util/safego` 标 Deprecated，调用点逐步迁到 `runtimesafe.SafeGo`；冷库期间禁止新增对 `util/safego.Go` 的引用（`util/safego/safego.go` 全文）
2. `config.envBoolOr` / `envPositiveIntOr` 解析失败必须 error，禁止静默 fallback（`config.go:270-292`）
3. `config.resolveProjectRoot` 中 `os.Getwd` 失败必须 hard error（`config.go:218-234`）
4. `toolbridge/proxy.go:201-220` peer tools 失败不再降级返回 host tools，改为 jsonRPC error
5. `toolbridge/proxy.go:257-258` nil result 改为 internal error
6. `toolbridge/diff_fallback.go:62-66` emit 失败不能 MarkSeen，否则 retry 永远跳过

### P1（本月）
7. `config.New` 改为接受 `EnvSource interface { Lookup(key string) (string, bool) }` 入参，副作用与全局 env 解耦
8. `config.RPCAddr` 默认值 `127.0.0.1:8090` 删除，缺失即 error
9. `config.envOrCompat` canonical+legacy 同时存在时返回 error
10. `loadDotEnv` dev 模式读 .env 失败至少 Warn（`config.go:101-119`）
11. `applyDotEnv` non-strict 解析错误 Warn 带行号（`config.go:121-138`）
12. `toolbridge/diff_fallback.go:46-67` 6 处 early-return 加 debug log
13. `toolbridge/diff_fallback.go:77-92` `resolveCWD` 改返回 `(string, error)`

### P2（下个 sprint）
14. `util/safego.Go` 增加 metrics counter + done channel（修完 P0 迁移后再加）
15. `config.parseDotEnvLineStrict` 实现 quote-aware 解析
16. `config/timeouts.go` 与 `ctxutil` 二选一收口
17. `toolbridge/proxy.go:368-373` callIDFromJSONRPCID 按类型分发
18. `toolbridge/proxy.go:391-395` writeProxyJSON Encode 失败加 Warn
19. `toolbridge/proxy.go:285-303` publish 早 return 加 debug log
20. `toolbridge/proxy.go:138-156` arguments null→{} 改为 error，与第 01 轮的 normalizeOptionalToolParams 同步收紧

## 边界条件

1. **safego 双实现迁移要分两阶段**：先把 `util/safego` 包内函数体改为 `func Go(...) { runtimesafe.SafeGo(...) }`（行为完全一致，只是去重），再在新一轮里把所有调用点迁过去。一次性删包会破坏大量 import。
2. **`config.envBoolOr` 收紧会破坏现有 dev 体验**：开发同学可能在本地 `.env` 写了 `FOO=enabled` 或 `FOO=on`（虽然 `strconv.ParseBool` 接受后者）。先做调用面盘点，必要时支持 yes/no/on/off。
3. **`config.New` 改入参签名是 breaking change**：fx provider 都通过 `platformconfig.New` 注入。改造时保留无参 wrapper 调用新带参版本，避免一次性重构。
4. **toolbridge proxy peer tools 降级语义**：当前"peer 失败时返回 host tools"是为了让 desktop 在 sidecar 启动慢时仍能继续。改为 hard error 后需要确认前端 UI 有合理重试逻辑，否则启动期闪现错误。
5. **`diff_fallback` 的早 return 静默是有意设计**：很多场景（非 git 仓库、agent 没绑工作目录）确实不该 emit。修复时只把"应该 emit 但失败"的路径加日志，不要把"业务上不需要 emit"也变成 Warn。
6. **`config/timeouts.go` 与 `ctxutil` 收口**：先 grep 调用面分布。如果 `config.WithTimeout` 已被广泛使用，建议保留 alias 但加 `// Deprecated: use ctxutil.WithTimeout`。
7. **`util/safego.Go` 与 `runtimesafe.SafeGo` 行为**：本轮已确认两者代码字节一致（仅包名/函数名差异）。统一时不会引入行为变化，但要同步审视它们各自的测试是否都保留。
8. **proxy `handleProxyRequest` 的 `r.Body.Close()` defer**：HTTP 服务器层一般已调过 Close，这里 defer 是 belt-and-suspenders。改为 log 错误前先确认是否真的可能 double-close 报错。

---

下一轮范围建议：
- `internal/platform/toolbridge/handler.go` + `handler_*.go` 系列（核心调用路由、launch_args、managed_launch、peer_decode、shadow 处理）
- `internal/platform/toolbridge/host_tools.go`、`memory_*_tool.go`（host 侧工具实现）
- `internal/platform/difftracker/`（diff_fallback 上游）
