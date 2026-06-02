# 第 05 轮审查结论

## 审查范围

- `internal/platform/toolbridge/handler.go`（Handler 主路由、HandleToolCall、routeToolCall、bindings 解析、persistent subagent 兜底）
- `internal/platform/toolbridge/handler_managed_launch.go`（orchestration_launch_agent 上下文注入、provider/model/effort 解析）
- `internal/platform/toolbridge/handler_peer_decode.go`（codex tool surface 准备、MCP peer 启动、surface 调用、tools 解析）
- `internal/platform/toolbridge/host_tools.go`（HostToolRegistry 接口、HostToolCall 类型）

> 与第 01-04 轮已覆盖的 `proxy.go`、`diff_fallback.go`、`safego/`、`config/`、`mcpserver/common/` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `handler.go:66-87` `NewHandler` | 弱契约 | 全部依赖 `in.Logger`/`in.Registry`/`in.Emitter`/`in.Resolver`/`in.BindingStore`/`in.ThreadStore`/`in.Preferences`/`in.Config`/`in.Dispatcher`/`in.HostTools` 都不做 nil 校验 | 任一关键依赖缺失（如 registry/cfg）后续都成了 nil-safe early return，掩盖装配错误 | 至少 registry、cfg、dispatcher、bindingStore 几个核心依赖必须非 nil；HostTools 显式 optional |
| `handler.go:113-116` `routeToolCall` | 兜底 | `if h == nil \|\| h.registry == nil { return nil, ErrNoPeerAvailable }` | 把"装配未注入 registry"伪装成"没有可用 peer"，导致用户拿到错误的 retry 提示 | 装配错误应是独立 error；`ErrNoPeerAvailable` 仅用于 peer 启动中/全失败 |
| `handler.go:138-143` `routeHostOnlyToolCall` | 兜底 | host 工具未配置时不报错，构造 `agent_memory` 错误 result + `nil` error 返回 | 调用方只判 err 不判 result.Success 时会以为成功；client 看到的是"工具返回错误"而不是"工具不存在" | 改为 return `(nil, hostToolNotConfiguredErr)`；让上层统一 envelope |
| `handler.go:200-202` `callPeerTool` | 兜底 | `peer.Callback` 失败 → `return toolCallErrorResult(err.Error()), nil` | 同上，error 被降级成 result；transport-level 错误被埋没 | peer callback 错误必须 return error，让 routeToolCall 决定包装 |
| `handler.go:212-220` `toolCallTextResult` | 弱契约 | `Type: "inputText"`（非 MCP 标准 `"text"`），由 `toMCPContent` 在 proxy 边界改名 | 双标准并存，编码 hack；新调用点容易漏改名 | 在构造时就用 `"text"`；proxy 不再做转换 |
| `handler.go:228-252` `resolveCurrentToolCallBinding` | 兜底 | 多次 `lookupToolCallBindingByAgent`/`ByProviderThread` 的返回错误全部转成 false 静默 | binding store IO 错误（数据库 down、网络）被当成"没有 binding"，managed launch 注入静悄悄失败 | 区分"未找到"和"读取失败"；后者必须 return error |
| `handler.go:254-260` `lookupToolCallBindingByAgent` | 静默 | `if err != nil { return toolCallBinding{}, false }` | 同上 | 同上 |
| `handler.go:262-268` `lookupToolCallBindingByProviderThread` | 静默 | 同上 | 同上 | 同上 |
| `handler.go:284-306` `persistentSubagentRequired` | 兜底 | `!present` 且 `!allowDefaultPersistentSubagentFallback()` 才报错；env=1 时 fallback 到 `cfg.Agent.PersistentSubagentDefault` | 兼容期 env 控制太隐晦；线上"未配置 flag" 与"显式配置 false"行为不同 | 把 env switch 改为短期 deprecation；同时记录 metrics 告警 |
| `handler.go:300-305` `runtimeHasTool` 路径 | 兜底 | tool 列表存在但不含目标工具时返回 `(false, true)`，调用方用 `(false, nil)` 短路；否则 `(true, true)` 返回 `true` | 行为依赖第二个 bool；`runtimeHasTool` 返回 `(false, false)` 时 `persistentSubagentRequired` 直接 `return true, nil` —— 把"未声明"当"应启用"，对全局影响大 | 拆成两次明确的状态返回；缺失 enabledTools 应显式 error |
| `handler.go:316-322` `toolCallRuntimeConfig` | 兜底 | `requireToolCallRuntimeConfig` 失败时返回 `(nil, false)` | wrapper 把所有错误吃掉；调用方看不到"线程不存在 vs 配置读失败 vs JSON 损坏" | 改为 `(map, error)`，让上层判断；`bool` 形式只用于"明确未配置" |
| `handler.go:340-345` `resolveToolCallThreadID` | 兜底 | 多源解析全部失败时返回 `("", false)`；`requireToolCallThreadID` 转换为 `ErrThreadRuntimeRequired` | error 没有保留任何上下文（哪个 store 失败、agent_id 是什么、是否 binding 异常） | error 链 wrap：`fmt.Errorf("resolve thread id for agent %q: %w", agent, err)` |
| `handler.go:355-372` `resolveToolCallThreadIDFromAgent` | 静默 | binding store 错误被转成 false | 同 round-04 处理：binding store 是关键依赖 | 加 Warn；error 必须传播 |
| `handler.go:374-383` `readToolCallRuntime` | 静默 | `GetConfigOverride` 错误转 `(nil, false)`；`len(raw) == 0` 也转 false | DB 报错与"无 override"不可区分 | DB error 必须返回 |
| `handler.go:385-394` `decodeToolArguments` | 静默 | `json.Unmarshal` 失败返回 nil | 调用方拿到 nil map 会以为"参数为空"而非"参数损坏" | 返回 (map, error) |
| `handler.go:431-452` `persistentSubagentFlagFromRuntime` | 静默 | flag 不是 bool 时静默继续；遍历到 string `"true"` 也不识别 | 用户写 YAML/JSON 时常把 bool 误存成字符串；这里直接当成"未设置"，触发 fallback 默认值 | 类型不符返回 error |
| `handler.go:454-479` `runtimeHasTool` | 静默 | 找不到 enabledTools 时 `(false, false)`；类型不符 default | 同上 | 类型不符报 error，不要静默忽略 |
| `handler_managed_launch.go:13-52` `injectManagedLaunchContext` | 兜底+静默 | binding 找不到/参数空/marshal 失败都返回原 req 不修改；marshal 失败仅 Warn | managed launch 上下文注入失败用户无感知；后续 launch_agent 用错误 provider/model 启动 | marshal 失败必须 return error；binding 缺失改为 Warn 但不静默 |
| `handler_managed_launch.go:31-37` JSON marshal 失败 | 静默 | `if err != nil { h.warn(...); return req }` | 同上 | 必须 return error |
| `handler_managed_launch.go:63-68` `resolveManagedLaunchDefaults` | 兜底 | "首个非空"风格连环；任一源缺失都 fallback 到下一源 | 与 round-04 firstNonEmpty 滥用同根；用户期望"明确指定"被偏好覆盖 | 优先级文档化；显式日志记录"用了哪一源" |
| `handler_managed_launch.go:102-114` `readMergedUIPreferences` | 静默 | preferences 读失败仅 Warn 后返回 (nil, false) | UI 偏好读不到等于走 default；与"用户期望偏好生效"语义冲突 | 至少 metrics |
| `handler_managed_launch.go:131-141` `normalizeProviderPreferenceScope` | 兜底 | 空字符串/`"openai"`/`"codex"` 都视为 codex；其它 lowercase 透传 | 鼓励"什么都填得进来"；用户写 `cox` 不会有任何提示 | 未识别 provider 返回 error |
| `handler_managed_launch.go:150-161` `compatibleManagedLaunchModelEffort` | 兜底 | 不兼容 model → 返回 `("", "")` 把模型和 effort 都清掉 | 用户配了不兼容 model 不会被告知，effort 也连带丢失；继续 fallback 到 default | 不兼容时返回 error 让上层显式拒绝 |
| `handler_managed_launch.go:154-159` 不兼容 effort | 兜底 | 不兼容 effort 仅清空 effort，model 保留 | 半合法状态被默许 | 不兼容时直接报错 |
| `handler_managed_launch.go:198-218` `warnManagedLaunchConfigTrace` | 静默 | 全部 best-effort：threadID 解析失败、stored 读失败都没在 trace 中暴露 | 排查问题时拿到的 trace 信息不全 | trace 应包含 `thread_resolve_err`、`stored_read_err` 字段 |
| `handler_managed_launch.go:220-233` `readStoredThreadRuntime` | 静默 | DB 错误、JSON 解析错误都转 `(empty, false)` | 与 handler.go:374 同 | 同上 |
| `handler_managed_launch.go:235-241` `decodeStoredThreadRuntime` | 静默 | `json.Unmarshal` 错误吞掉 | runtime override JSON 损坏静悄悄回退 default | 返回 error |
| `handler_peer_decode.go:50-65` `PrepareCodexToolSurface` | 兜底 | `storeCodexToolSurface` 失败时 `removeCodexToolSurface` + `surface.Close()`，关闭错误用 `fmt.Errorf("...; additionally close: %v", closeErr)` | 关闭错误降级成 string 嵌入而非 wrap，调用方无法用 `errors.Is/As` 判断 | 改用 `errors.Join` |
| `handler_peer_decode.go:178-184` `closeMCPClients` | 静默 | 遍历所有 client `_ = result.client.Close()` | 单个 close 错误被吞，多 binary 启动时其中一个 close 失败无人知道 | 用 `errors.Join` 聚合返回 |
| `handler_peer_decode.go:117-176` `prepareMCPSurfaceBinaries` | 兜底 | 用 sync.WaitGroup + recordErr 聚合首个错误，但**只保留 firstErr**；其余错误丢弃 | 多 binary 同时失败定位困难 | 用 `errors.Join` 聚合所有错误 |
| `handler_peer_decode.go:147-168` safego.Go worker | 静默 | safego.Go 内 panic 被 recover 后 `wg.Done` **不调用**（defer wg.Done 在 fn 外层，panic 后是否执行依赖 safego 实现） | 如果 safego.Go 内的 panic 触发，wg.Wait 永远不退出，PrepareCodexToolSurface 卡死 | 改为显式 `defer wg.Done()` 在 worker 入口最早；recordErr 内同时 done |
| `handler_peer_decode.go:404-420` `listPeerTools` | 兜底 | h 或 registry 为 nil 时返回 `ErrNoPeerAvailable` | 装配错误伪装成 peer 不可用（与 routeToolCall 同根） | 同 routeToolCall 修法 |
| `handler_peer_decode.go:520-563` `decodeToolCallRequest` | 兜底 | `len(req.Arguments) == 0` 时静默赋值 `{}`（与 round-04 normalizeOptionalToolParams 同根） | 模型可能恶意传空 arguments，被默认替换为空对象后调用工具仍可执行 | 必填工具参数在工具自身校验；这里至少 log 一条 debug |
| `host_tools.go:25-34` `HostToolRegistry` 接口 | 弱契约 | `CallHostTool` 返回 `(any, error)` | `any` 让所有实现都能返回任何类型，下游 `callHostTool` 无法做强类型校验 | 改为返回 `*ToolCallResult` |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `handler.go:200-202` | peer callback err → toolCallErrorResult + nil err |
| `handler.go:228-268` | binding lookup err → false |
| `handler.go:316-322` | runtimeConfig err → (nil, false) |
| `handler.go:340-372` | thread id 解析多源全部 err → false |
| `handler.go:374-383` | readToolCallRuntime DB err → false |
| `handler.go:385-394` | decodeToolArguments json err → nil |
| `handler.go:431-479` | persistentSubagent flag/tool 解析类型不符忽略 |
| `handler_managed_launch.go:31-37` | marshal 失败仅 Warn |
| `handler_managed_launch.go:108-112` | readMergedUIPreferences err → false |
| `handler_managed_launch.go:220-233` | readStoredThreadRuntime DB/JSON err → false |
| `handler_managed_launch.go:235-241` | decodeStoredThreadRuntime err → false |
| `handler_peer_decode.go:60-63` | close err 用 string 嵌入 |
| `handler_peer_decode.go:178-184` | closeMCPClients 错误全吞 |
| `handler_peer_decode.go:117-176` | prepareMCPSurfaceBinaries 仅保留 firstErr |
| `handler_peer_decode.go:362-373` | publish 早 return 无 log |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `handler.go:66-87` `NewHandler` | 10 个依赖字段无 nil 校验 |
| `handler.go:89-102` `HandleToolCall` | msg 不做 Validate；surfaceReq.CallID 兜底 |
| `handler.go:104-136` `routeToolCall` | name 用 `strings.TrimSpace` 后 switch；空字符串走默认（peer 路径），未单独处理 |
| `handler.go:212-224` `toolCallTextResult/ErrorResult` | content type 用非标准 `"inputText"` |
| `handler.go:316-322` | bool+error 混合返回（与 round-04 LoadLSPBundleFromEnv 同根） |
| `handler_managed_launch.go:54-61` `isManagedLaunchToolName` | 写死字符串 list；新增 launch alias 易漏 |
| `handler_managed_launch.go:131-141` `normalizeProviderPreferenceScope` | 默认把空/openai/codex 当 codex；其它 lowercase 透传 |
| `handler_managed_launch.go:163-174` `managedLaunchModelCompatible` | claude 用 enum 列举 + claude- 前缀；其它走 gpt- 前缀；新模型加入需改两处 |
| `handler_managed_launch.go:176-187` `managedLaunchEffortCompatible` | 仅识别 6 个枚举；其它一律 false 但无 error 链路 |
| `handler_peer_decode.go:208-225` `addSurfaceTool` | tool.Name 仅 trim；duplicate 检测用 map exists；schema 字段无校验 |
| `handler_peer_decode.go:242-253` `addSurfaceAlias` | alias=canonical 时 noop；map 冲突报错；不限制 alias 长度/字符集 |
| `handler_peer_decode.go:280-292` `legacyCodexToolAliases` | 双家族写死；新 family 加入需改这里 |
| `handler_peer_decode.go:520-563` `decodeToolCallRequest` | 多源 firstString fallback；nestedString 双层兜底；arguments 空 → `{}` 兜底 |
| `host_tools.go:25-34` `HostToolRegistry` | CallHostTool 返回 `any`；HostToolCall 字段全可选 |

## 修复优先级

### P0（必须本周修）
1. `handler.go:200-202` peer callback 失败必须 return error，不能降级成 result
2. `handler.go:138-143` host 工具未配置必须 return error
3. `handler.go:228-394` binding/thread/runtime 多源 lookup 全部统一为 `(value, error)`，区分"未找到"与"读取失败"
4. `handler_managed_launch.go:31-37` marshal 失败必须 return error
5. `handler_peer_decode.go:147-168` safego.Go worker 的 wg.Done 移到入口 defer，避免 panic 时死锁
6. `handler_peer_decode.go:117-176` prepareMCPSurfaceBinaries 用 errors.Join 聚合所有错误

### P1（本月）
7. `handler.go:66-87` NewHandler 增加核心依赖 nil 校验，构造期 panic
8. `handler.go:113-116`、`handler_peer_decode.go:404-407` 装配 nil 错误改用专用 error，不与 ErrNoPeerAvailable 混
9. `handler.go:431-479` persistentSubagentFlag 类型不符 → error；runtimeHasTool `(false, false)` 路径不要默认认为应启用
10. `handler_managed_launch.go:131-187` provider/model/effort 不兼容时返回 error，让上层显式拒绝
11. `handler.go:212-224` 用 MCP 标准 `"text"`，删除 proxy 的转换
12. `handler_peer_decode.go:178-184` closeMCPClients 用 errors.Join

### P2（下个 sprint）
13. `handler_managed_launch.go:63-68` provider/model/effort 解析优先级文档化 + 决策日志
14. `handler_managed_launch.go:198-218` warnManagedLaunchConfigTrace 增加错误字段
15. `handler.go:284-306` allowDefaultPersistentSubagent env 加 deprecation 计划
16. `host_tools.go:25-34` CallHostTool 返回类型从 `any` 改为 `*ToolCallResult`
17. `handler_peer_decode.go:520-563` decodeToolCallRequest 多源兜底改为优先级单源（pos-style）

## 边界条件

1. **`handler.go` 多源兜底是历史包袱**：codex/claude provider 入口 payload 字段命名不一致（agentId/agent_id/_agentId 同义），`firstString` 是为了兼容。修复时不要一刀切；应保留 alias 但统一在 dto 层 normalize。
2. **`runtimeHasTool` 默认启用语义**：`return true, nil` 在 `enabledTools` 缺失时被使用，原本是为了"老线程没显式声明也走 managed"。修复需要先盘点旧线程数据是否还有这种 `null enabledTools` 的；如果有要先迁移。
3. **`safego.Go` 与 `wg.Done` 的死锁**：本轮把 P0 列了，但实际行为取决于 `internal/util/safego.Go` 的 panic recover 是否会让 fn 的 deferred 执行。Go runtime 标准行为：goroutine 内部 panic + recover 后正常返回，deferred 会执行。所以理论上 wg.Done 会被调用。但代码可读性上仍建议把 defer wg.Done() 放到入口最早（fn 第一行）。
4. **`toolCallTextResult` 的 `"inputText"` type**：这是 Codex 的内部约定（见 proxy.go:404），改为 `"text"` 后必须确认 Codex 客户端没有针对 `"inputText"` 的特殊处理。
5. **bind store 错误传播会导致 UI 退化**：当前所有 bind/runtime 读失败都被吞掉，所以 UI 一直能"勉强工作"。改成 fail-fast 后，store 故障会直接让工具调用失败。需要先确认 binding store 的 SLO，避免一刀切。
6. **`allowDefaultPersistentSubagentFallback` env**：`TOOLBRIDGE_ALLOW_DEFAULT_PERSISTENT_SUBAGENT=1` 是兼容 switch。`persistentSubagentDefaultFallbackTotal` 已经有 metrics counter；可以基于实际 fallback 数量决定何时 hard cutover。
7. **provider/model/effort 不兼容报错的影响范围**：当前 silently 清空让"用户随便填"也能跑。改 error 后会让一部分历史 thread runtime override 报错。建议先加 `[deprecated]` warning 一周，再升级为 error。

---

下一轮范围建议：
- `internal/platform/toolbridge/handler_host_tools.go`、`memory_read_tool.go`、`memory_write_tool.go`、`subscribers.go`、`stdio_mcp_client.go`
- `internal/platform/difftracker/`（diff_fallback 上游）
- `internal/platform/mcpcontrol/`（peer registry）
