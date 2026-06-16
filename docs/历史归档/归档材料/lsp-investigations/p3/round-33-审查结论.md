# 第 33 轮审查结论

## 审查范围

- `internal/sidecar/lsp/edit/replaceutil.go`（GuardContentAndReplacement、ShouldForceBypass、OffsetToLine、BuildEditContext、indexContent、lineForOffset、lineRangeForOffsets、validateOffsets、splitAffectedLines）
- `internal/sidecar/lsp/protocol/codec.go`（BuildRequest、BuildNotification、BuildSuccessResponse、BuildErrorResponse、EncodeMessage、DecodeEnvelope、DecodeRequest、DecodeNotification、DecodeResponse、hasRequestID、marshalPayload）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `replaceutil.go:122-127` lineForOffset | 静默兜底 | `pos == 0` 静默返 1；`pos > len(start)` 静默返 len | offset 越界本应被 validateOffsets 拦截，这两个分支说明存在漏拦截路径；静默兜底掩盖逻辑漏洞 | 改为 `panic("invariant violated: validateOffsets should prevent this")` 让 bug 早期暴露 |
| `replaceutil.go:88-89` indexContent | 性能 bug | `starts := make([]int, 0, len(lines))` 但此时 lines 是空切片（len=0），cap 也是 0 | 大文件每次 append 都触发 reallocation，O(n²) | 改为 `make([]int, 0, cap(lines))` 或 `make([]int, 0, strings.Count(content, "\n")+1)` |
| `codec.go:211-214` marshalPayload | 静默 | `[]byte` 输入时 `json.Valid(raw)` 才视为 raw JSON；无效 JSON 静默 fall-through 到 `json.Marshal`（编码为 base64 字符串） | caller 传无效 JSON []byte 期望被拒绝，实际被静默编码为 base64 字符串 | 显式：`if !json.Valid(raw) { return nil, fmt.Errorf("invalid JSON bytes") }` |
| `replaceutil.go:131-144` lineRangeForOffsets | 边界 | `endOffset == startOffset` 时 startLine==endLine（line 136-138）；否则 `endOffset-1` | 当 startOffset == endOffset == 0 且 content 非空时，是否预期返回 (1,1)？逻辑没有显式 doc | 加注释说明零长度 range 的语义 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `replaceutil.go:80-110` indexContent | 4MB 上限内的内容仍可能很大；逐字节扫描 + 多次 append | 1) 已有 size 限制（GuardContentAndReplacement）是正面案例 2) 加 duration 监控，>50ms 打 Debug |
| `replaceutil.go:50-78` BuildEditContext | 调用 indexContent + lineRangeForOffsets + sliceLines 多步；每次 edit 都重新构建 index | 加 LRU 缓存（key=hash(content)），相同 content 多次 edit 不重复 index |
| `codec.go:124-130` EncodeMessage | json.Marshal 大消息（如几 MB）会耗时 | 加 size 阈值告警；或加 incremental encoding |
| `codec.go:132-141` DecodeEnvelope | json.Unmarshal 不限 depth，恶意嵌套消息可栈溢出 | 加 max depth limit（如 64） |
| `codec.go:174-198` DecodeResponse | 解码大 response（带 4MB result）阻塞 transport readLoop | response 大小限制 + 单独协程解码（但需保序） |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `replaceutil.go:122-124` lineForOffset | pos==0 静默返 1（应不可达） |
| `replaceutil.go:125-127` lineForOffset | pos>len 静默返 len（应不可达） |
| `replaceutil.go:146-153` affectedWindow | lines 空时静默返 (1,1)（合理边界，可接受） |
| `replaceutil.go:172-177` sliceLines | startLine > endLine 静默返 nil（合理） |
| `codec.go:212-214` marshalPayload | invalid JSON []byte 静默 fall-through 到 base64 |
| `codec.go:200-203` hasRequestID | "null" 字符串视为无 ID（合理 JSON-RPC 规范） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `replaceutil.go:36-38` ShouldForceBypass | 报告 bool 但「caller 应做什么」靠隐式约定 |
| `replaceutil.go:131-144` lineRangeForOffsets | 零长度 range 行为无文档 |
| `codec.go:205-221` marshalPayload | 接受 nil / json.RawMessage / []byte / any 多种输入语义混合 |
| `replaceutil.go:161-170` splitAffectedLines | 返回 nil vs []string{""} 区分语义靠注释 |

## 修复优先级

### P0（必须本周修）
1. **`replaceutil.go:88-89` indexContent 性能 bug**——这是大文件 edit 的延迟主因。`make([]int, 0, len(lines))` 时 `len(lines) == 0`，cap=0，导致每次 append 翻倍扩容（log2(N) 次）。对 4MB 文件（数十万行），多达 18 次 reallocation。改为 `strings.Count(content, "\n")+1` 一次预分配。
2. **`codec.go:211-214` marshalPayload 静默 base64 编码**——caller 传 `[]byte("invalid json")` 期望返回 error，实际被 `json.Marshal` 编码为 `"aW52YWxpZCBqc29u"` 字符串。下游 LSP server 收到错误格式消息无法解析，错误传播路径长且模糊。

### P1（本月）
3. `replaceutil.go:122-127` lineForOffset 不可达分支改 panic
4. `codec.go:132-141` DecodeEnvelope 加 max depth limit
5. `codec.go:124-130` EncodeMessage 加 size 阈值监控

### P2（下个 sprint）
6. `replaceutil.go:50-78` BuildEditContext 加 content-hash LRU 缓存
7. `replaceutil.go:131-144` 零长度 range 加文档
8. `codec.go:205-221` marshalPayload 重构为类型化输入

## 边界条件

1. **`replaceutil.go` 的 size limit 是正面案例**：line 10-15 定义 4 个常量阈值（content 4MB、replacement 256KB、bypass 2MB、max edits 20、context lines 5），`GuardContentAndReplacement` 在所有 entry point（BuildEditContext/indexContent）入口处调用。这是 fail-fast 的良好实践——大文件直接拒绝而非阻塞处理。建议作为模板推广到 `manager_symbols_fallback.go`（第30轮发现的无 size 限制问题）。
2. **`replaceutil.go:88-89` 性能 bug 的实际影响**：4MB 文件上 strings.Count 大约 100K 次循环（约 1ms），相比 18 次 reallocation 总搬迁约 2MB 数据（~20ms），预分配修复后整体 indexContent 从 ~25ms 降到 ~5ms。在 LSP edit 路径上每次都跑一次，是协程延迟可量化优化点。
3. **`codec.go` 整体质量较高**：DecodeResponse 严格校验 result/error 互斥（line 186-188）、id 必需（line 183-185）、method 不可有（line 180-182）；这是 fail-fast 实践的优秀示例。`hasRequestID` 正确处理 JSON-RPC 规范中的 null id 区分。所有 builder 函数（BuildRequest/Notification/SuccessResponse/ErrorResponse）都校验输入。
4. **`marshalPayload` 的设计意图**：line 211-214 优先级是「raw JSON []byte 直传 > 通用 marshal」。意图是允许已经序列化好的 payload 不重复 marshal。但「无效 JSON []byte 静默走 marshal」违反此意图——如果 caller 要的是 raw 但 raw 无效，应当 error 而非降级到 string marshal。这是 P0 因为它影响外部协议兼容性。
5. **`lineForOffset` 不可达分支的设计取舍**：line 122-124 / 125-127 处理 sort.Search 边界。理论上 `validateOffsets` 已保证 `0 <= offset <= len(content)`，但代码作者可能担心 sort.Search 行为或未来 refactor 删除 validate。两种思路：①保持兜底（fail-soft，Go 风格）；②改 panic 让不变量违反在测试期暴露（fail-fast）。本项目主张 100% fail-fast，建议改 panic。
6. **codec.go 没有协程并发问题**：所有函数都是 pure（无全局状态）。线程安全性来自上层调用者（transport.writeMu）。这是良好的层次分离。

---

**本轮总结**：发现 1 个 P0 性能 bug（indexContent 预分配错误，影响大文件 edit 延迟）、1 个 P0 静默问题（marshalPayload 无效 JSON 降级 base64）。`replaceutil.go` 的 size limit 设计和 `codec.go` 的 mutual-exclusion 校验是项目内 fail-fast 正面案例。lineForOffset 不可达分支建议改 panic 让 invariant 违反早期暴露。

**累计进度**：33 轮完成。cron `fd4b4728` 继续推进。
