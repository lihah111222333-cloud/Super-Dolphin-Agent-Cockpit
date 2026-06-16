# 第 32 轮审查结论

## 审查范围

- `internal/sidecar/lsp/tools/tool_edit_lock.go`（lockEditFile、lockEditFiles）
- `internal/sidecar/lsp/tools/tool_error_envelope.go`（newToolErrorEnvelope re-export）
- `internal/sidecar/lsp/format/compact.go`（NormalizeVerbosity、ResolveResultLimit、CompactCompletionItems、CompactWorkspaceSymbols、LocationFromAny、locationFromMap、intFromAny、GroupLocationsByFile）
- `internal/sidecar/lsp/format/funcrange.go`（FindEnclosingFunction、EnrichLocationResultsWithFuncRange、ResolveEnclosingFunctionRange、AbsolutePathFromURI）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `tool_edit_lock.go:5` | 内存泄漏 | `var editFileLocks sync.Map` 全局存储，LoadOrStore 创建后从不删除 | 长期运行进程 lock map 无限增长。每个曾被编辑的文件路径占一个 `*sync.Mutex` | 加 LRU 淘汰，或编辑后短时间内删除（需保证无并发持有者） |
| `tool_edit_lock.go:17-26` | 死锁风险 | 按调用方传入顺序 Lock，但调用方未必排序 | 两个 caller 传 `[a, b]` 和 `[b, a]` 可能互锁等待 | 函数内部强制 `sort.Strings(paths)`，确保全局加锁顺序一致 |
| `tool_edit_lock.go:18` | 静默/隐患 | `path == last` 仅跳过**相邻**重复 | 输入 `[a, b, a]` 会对 `a` 二次 Lock → sync.Mutex 非递归，自死锁 | 用 `seen map[string]struct{}` 替代 `last` 字符串 |
| `tool_edit_lock.go:27-31` | panic 风险 | 返回 release 闭包，无 panic-safe 包装 | caller 在 Lock-Unlock 之间 panic，锁永久不释放 | release 闭包改为 `defer recover()` 保证总能 unlock |
| `format/compact.go:73-82` NewCompactList | 静默 | `if total < len(items) { total = len(items) }` 静默修正 | 上游 total 计算错误（少计）时被静默掩盖，bug 永远不暴露 | total 与 len 不一致时 panic（开发期）或 Warn（生产） |
| `format/compact.go:127-143` LocationFromAny | 静默 | type-switch `default: return ..., false` | 协议升级新增 location 类型时静默丢弃 | 加 Warn 日志带原 type 名 |
| `format/compact.go:168-175` locationFromMap | 静默 | `value["uri"].(string)` 类型断言失败丢弃 | URI 字段类型变更（如变为对象）静默吞掉 | 类型断言失败 Warn |
| `format/compact.go:223-242` intFromAny | 静默 | 数字类型 switch `default: return 0` | 字符串数字（"5"）被映射为 0；JSON 数字反序列化为 json.Number 也走 default | 加 json.Number 分支 + 字符串分支 +  default Warn |
| `format/funcrange.go:46-69` ResolveEnclosingFunctionRange | 静默 | URI 解析失败、Symbols 调用失败、未找到都返 `(0,0,false,false)` | 无法区分「LSP 故障」「输入错」「无 enclosing function」 | 改为 `(start, end, status enum)` 区分三态 |
| `format/funcrange.go:54-56` Symbols 调用失败 | 静默 | err != nil 直接 return false | LSP 故障在 enrichment 路径被吞掉，结果集合无 funcRange 但调用方不知 LSP 故障 | 至少 Debug 日志（频率高时改 RateLimit Warn） |
| `format/funcrange.go:28-32` EnrichLocationResultsWithFuncRange | 弱契约 | provider == nil 时静默 return | nil provider 是注入 bug | 改 panic 或在构造期校验 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `tool_edit_lock.go:11-32` lockEditFiles | 锁竞争是协程延迟主因；高并发编辑同文件时排队 | 加 `time.Now()` 测 Lock 等待耗时；P99 > 1s 打 Warn 带 path |
| `format/funcrange.go:54` provider.Symbols | LSP documentSymbol 调用是同步阻塞，每个 location 一次 LSP roundtrip | 已有 `lastRange` 缓存（line 32, 64-67）避免重复调用——这是正面案例。但缓存只在单次 enrichment 内生效；建议加跨调用 LRU |
| `format/compact.go:189-221` GroupLocationsByFile | 线性扫描 items + map 写入；items 多时 cache miss | items < 1000 时 OK；大集合时改 sort + group |
| `tool_edit_lock.go:5` editFileLocks 全局 map | sync.Map 在写多读少场景退化（每次 LoadOrStore 都竞争） | 替换为分片 map（按 path hash 分 16 shard） |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `tool_edit_lock.go:13-14` | `len(paths) == 0` 时返回空 release（合理边界） |
| `tool_edit_lock.go:18-21` | path 空 / 相邻重复静默跳过 |
| `format/compact.go:39-46` NormalizeVerbosity | 未知 verbosity 静默降级到 compact |
| `format/compact.go:73-77` NewCompactList | total < len 静默修正 |
| `format/compact.go:84-94` CompactCompletionItems | 无 nil-check items 元素（依赖 LSP server 不返 nil） |
| `format/compact.go:127-143` LocationFromAny | 未知类型静默 false |
| `format/compact.go:168-175` locationFromMap | uri 字段类型断言失败静默 |
| `format/compact.go:178-187` mapStartPosition | range/start map 缺失静默返 (0, 0) |
| `format/compact.go:200-203` GroupLocationsByFile | PrimaryLocation 为 nil 静默 continue |
| `format/compact.go:209-217` | funcRange 重复时静默不附加（这是正面去重，非 bug） |
| `format/funcrange.go:48-49, 51-52, 55-56` | nil provider / URI 解析失败 / Symbols 失败均静默返 false |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `tool_edit_lock.go:11` lockEditFiles | 调用方需保证 paths 已排序去重，否则有死锁 / 自死锁风险 |
| `tool_edit_lock.go:7-8` lockEditFile | 单文件版本调用 lockEditFiles 是 OK，但接口分裂 |
| `format/compact.go:48-59` ResolveResultLimit | requested>0 / verbosity / compactDefault 三段式优先级隐式 |
| `format/funcrange.go:46` ResolveEnclosingFunctionRange | 4-tuple 返回值含 isNew/ok 双 bool，语义混乱 |
| `format/funcrange.go:71-94` AbsolutePathFromURI | 接受 path 也接受 file:// URI（line 76 `filepath.IsAbs`），双重接受让契约不清 |

## 修复优先级

### P0（必须本周修）
1. **`tool_edit_lock.go:17-26` 缺排序保证 + 自死锁**——这是 deadlock 直接风险点。函数内强制 `sort.Strings(paths)` + 用 `seen map` 替换相邻去重逻辑。锁竞争路径上的 bug 导致整个进程挂死。
2. **`tool_edit_lock.go:5` 全局 lock map 内存泄漏**——长期运行的服务会积累所有曾编辑过的路径锁。每条 LSP edit 都增量泄漏。改为 LRU 或定期清理无引用 mutex。
3. **`tool_edit_lock.go:27-31` panic 安全**——caller 在 Lock 和 release 之间 panic，锁永久持有。所有后续编辑该文件的请求挂死。release 闭包改为 panic-safe。

### P1（本月）
4. `format/compact.go:73-82` total < len 改为 panic 或 Warn
5. `format/funcrange.go:46-69` 三态化返回值
6. `format/compact.go:127-242` 各 type-switch default 加 Warn 日志
7. `tool_edit_lock.go` 加 lock-wait duration 监控

### P2（下个 sprint）
8. `format/funcrange.go:71-94` AbsolutePathFromURI 改为只接受 URI（path 走另一函数）
9. `tool_edit_lock.go` 全局 sync.Map 改分片
10. `format/compact.go:48-59` ResolveResultLimit 重构清晰优先级

## 边界条件

1. **`tool_edit_lock.go` 的死锁场景具体化**：caller A 调 `lockEditFiles(["foo.go", "bar.go"])`，caller B 调 `lockEditFiles(["bar.go", "foo.go"])`。A 锁住 foo.go，等 bar.go；B 锁住 bar.go，等 foo.go。经典 AB-BA 死锁。修复必须在函数内部 `sort.Strings`，让所有 caller 都按字典序加锁。**这是 P0 的核心理由**。
2. **`tool_edit_lock.go` 的自死锁场景**：input `["foo.go", "bar.go", "foo.go"]`（caller 误传重复）。`last` 变量逻辑：第一个 foo 锁，last="foo.go"；bar 锁，last="bar.go"；第三个 foo 不等于 last="bar.go"，所以会**再次**对 foo 调 Lock。`sync.Mutex` 非递归，goroutine 自死锁。修复用 `seen map[string]struct{}` 而非 `last string`。
3. **format/funcrange.go ResolveEnclosingFunctionRange 的 lastRange 缓存设计**：line 62-67 用 `lastRange` map 去重相邻 location 的 funcRange——避免给每个 location 都重新调用 Symbols。这是项目内为数不多的「明确为协程延迟优化」的代码。**正面案例**。但缓存只在单次 enrichment 内有效，跨 LSP 调用每次都从 0 开始。生产环境若 enrichment 频繁，建议加全局 LRU。
4. **format/compact.go intFromAny 缺 json.Number 分支**：当 LSP 响应被反序列化为 `map[string]any`（如 locationFromMap 路径），数字字段会变成 `float64`（go json 默认行为）—— line 235-238 已处理。但若上游用 `json.Decoder.UseNumber()`，数字变成 `json.Number`（string 底层），会落到 default 返回 0。建议补 `case json.Number: ... v.Int64()`。
5. **`tool_error_envelope.go` 的 re-export 设计**：仅 13 行，把 `common.ToolErrorEnvelope` re-export 到 tools 包。`newToolErrorEnvelope` 的 meta 参数永远传 nil—— 这意味着工具层无法附带 metadata。如果未来需要（如附带 LSP server 名称、scope key 等），需修改签名。当前是简化设计，可接受。
6. **format 层整体可观测性差**：4 个文件覆盖 LSP 结果格式化关键路径（compact、enrich funcRange），但**完全没有日志**。如果 LSP 返回结构变化导致 type-switch 走 default，运维无任何感知。建议至少为 `LocationFromAny:default`、`intFromAny:default`、`Symbols err` 加 Debug 日志（带 RateLimiter 防爆）。

---

**本轮总结**：发现 3 个 P0 问题集中在 `tool_edit_lock.go`：①缺排序保证导致 AB-BA 死锁；②`last` 字符串去重缺陷导致自死锁；③panic 不释放锁导致进程挂死。这是协程延迟和 hung 进程的高危反模式，应优先修复。format 层 type-switch 普遍 silent default，建议加可观测性。`format/funcrange.go` 的 lastRange 缓存是延迟优化正面案例。

**累计进度**：32 轮完成。cron `fd4b4728` 继续推进。
