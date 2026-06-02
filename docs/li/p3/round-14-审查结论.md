# 第 14 轮审查结论

## 审查范围

- `internal/module/turn/manifest.go`（manifestBuilder、discoverPeers、binaryDirFor）
- `internal/module/turn/skills.go`（skillResolver、normalizeSkillRefs、skillDedupKey、autoMatch、mergeSkillRefMetadata）
- `internal/module/turn/prompt_context.go`（configOutputStyle、normalizeOutputStyleConfig、configScratchpadDir、configOptionalBool）
- `internal/module/turn/skill_evaluator.go`（DefaultEvaluator、Evaluate、terminalRejectionReason）
- `internal/module/turn/skill_extractor.go`（DefaultExtractor 构造、Extract pipeline 入口）

> 与第 12-13 轮覆盖的 `tool_result_*`、`service*`、`tracker*`、`interrupt_service` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `manifest.go:17-22` `newManifestBuilder` | 弱契约 | binaryDir 仅 trim；buildFn 不做 nil 校验 | buildFn=nil 时 `Build` 调用 `b.buildFn(...)` 会 nil pointer panic | 构造期 `if buildFn == nil { panic }` |
| `manifest.go:24-37` `Build` | 静默 | `discoverPeers()` 失败时返回 nil maps；Build 继续用 nil addrs/tokens | peer 发现失败被当成"无 peer"；manifest 中不含 HTTP transport 信息，MCP 只走 stdio | 至少 debug log "no HTTP peers discovered" |
| `manifest.go:47-68` `discoverPeers` | 静默 | `discovery.DiscoverPeerHTTPAddrWithToken` 错误 `continue`；所有 family 都失败时返回 `(nil, nil)` | peer 发现错误（如 RPC 超时、token 无效）被当成"peer 不存在" | 区分"peer 不存在"与"发现失败"；后者至少 Warn |
| `manifest.go:39-44` `binaryDirFor` | 兜底 | input.BinaryDir 为空时 fallback 到 builder 的 binaryDir | 与 round-13 resolveBinaryDir 同根：多层 fallback 掩盖配置缺失 | 当前合理（input 级覆盖 builder 级）；但两者都为空时应 Warn |
| `skills.go:14-40` `Resolve` | 兜底 | 所有 ref 都被 dedup 后 `len(resolved) == 0` 返回 nil | 调用方传了 skills 但全部被去重/过滤掉，拿到 nil 无法区分"没传"与"全部无效" | 返回空 slice `[]dto.SkillRef{}` 而非 nil；或加 metrics |
| `skills.go:45-83` `normalizeSkillRefs` | 兜底 | `ref.Name == ""` 时 continue；`key == ""` 时 continue | 空 name 的 SkillRef 是调用方 bug；静默跳过 | 至少 debug log "skipping skill ref with empty name" |
| `skills.go:112-131` `autoMatch` | 兜底 | `prompt == "" \|\| len(refs) == 0` 返回 nil | 合理的 early-exit | OK |
| `skills.go:133-150` `mergeSkillRefMetadata` | 兜底 | 所有字段都是"existing 为空时用 next"的 first-non-empty 模式 | 与 round-04/09 firstNonEmpty 滥用同根；但这里是 metadata merge，语义合理 | 当前合理 |
| `prompt_context.go:10-21` `configOutputStyle` | 兜底 | 多 key 遍历，找不到返回 nil | 合理的 optional 配置查找 | OK |
| `prompt_context.go:23-51` `normalizeOutputStyleConfig` | 兜底 | `default: return nil`（未知类型静默忽略） | 配置中放了非法类型（如 int）被当成"未配置" | 未知类型至少 debug log |
| `prompt_context.go:80-91` `configScratchpadDir` | 兜底 | 类型断言失败 continue；空值 continue | 合理 | OK |
| `prompt_context.go:93-102` `configOptionalBool` | 兜底 | 类型断言失败 continue | 配置中放了 string "true" 被当成"未配置" | 尝试 string → bool 转换；或至少 debug log |
| `skill_evaluator.go:51-65` `Evaluate` | 弱契约 | `e` 为 nil receiver 时 `e.MinToolCalls` 会 panic | 调用方用 `var e *DefaultEvaluator; e.Evaluate(...)` 会崩 | 入口 `if e == nil { return EvaluationVerdict{Eligible: false, Reason: "evaluator_nil"} }` 或 panic |
| `skill_evaluator.go:78-81` `normalizedMinToolCalls` | 兜底 | 负值返回 0 | 负值 MinToolCalls 是构造方 bug | 构造期校验 MinToolCalls >= 0 |
| `skill_extractor.go:86-110` `NewDefaultExtractor` | 兜底 | logger/redactor/evaluator nil 全部兜底默认值；dream nil 是合法 optional | 三重兜底；logger nil 是调用方 bug | logger nil 应 panic；redactor/evaluator nil 兜底合理（有 default 实现） |
| `skill_extractor.go:87` 第二参数 `_ any` | 弱契约 | 忽略的参数保留在签名中 | 调用方传什么都行；签名不清晰 | 删除该参数或标注 deprecated |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `manifest.go:47-68` | discoverPeers 错误 continue |
| `manifest.go:24-37` | Build 用 nil addrs/tokens 继续 |
| `skills.go:45-83` | normalizeSkillRefs 空 name/key 静默 skip |
| `prompt_context.go:23-51` | normalizeOutputStyleConfig 未知类型返回 nil |
| `prompt_context.go:93-102` | configOptionalBool 类型不符 continue |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `manifest.go:17-22` | buildFn nil 不校验 |
| `manifest.go:39-44` | binaryDirFor 双层 fallback |
| `skills.go:14-40` | Resolve 全部去重后返回 nil 而非空 slice |
| `skills.go:45-83` | normalizeSkillRefs 空 name 静默 skip |
| `skill_evaluator.go:51-65` | nil receiver 会 panic |
| `skill_evaluator.go:78-81` | 负值 MinToolCalls 兜底 0 |
| `skill_extractor.go:86-110` | 三重 nil 兜底 |
| `skill_extractor.go:87` | 第二参数 `_ any` 保留 |
| `prompt_context.go:23-51` | 未知类型静默 nil |
| `prompt_context.go:93-102` | string "true" 不转换 |

## 修复优先级

### P0（必须本周修）
1. `manifest.go:17-22` buildFn=nil 构造期 panic
2. `manifest.go:47-68` discoverPeers 错误至少 Warn（peer 发现失败不应静默）
3. `skill_evaluator.go:51-65` nil receiver 入口校验

### P1（本月）
4. `skill_extractor.go:86-110` logger nil 改 panic
5. `skills.go:45-83` normalizeSkillRefs 空 name 加 debug log
6. `prompt_context.go:23-51` normalizeOutputStyleConfig 未知类型 debug log
7. `prompt_context.go:93-102` configOptionalBool 尝试 string→bool 转换
8. `manifest.go:39-44` binaryDirFor 两者都为空时 Warn
9. `skill_evaluator.go:78-81` 构造期校验 MinToolCalls >= 0

### P2（下个 sprint）
10. `skills.go:14-40` Resolve 返回空 slice 而非 nil
11. `skill_extractor.go:87` 删除 `_ any` 参数
12. `manifest.go:24-37` Build 无 peer 时 debug log

## 边界条件

1. **`discoverPeers` 的静默 continue 是有意设计**：peer HTTP 发现是 best-effort——stdio 是主 transport，HTTP 是加速路径。改 Warn 后日志量可能增加（每次 PrepareTurn 都会调用）。建议只在首次失败时 Warn，后续用 debug。
2. **`normalizeSkillRefs` 返回 nil vs 空 slice**：下游 `dto.TurnRequest.Skills` 字段是 `[]dto.SkillRef`，nil 和空 slice 在 JSON 序列化时分别是 `null` 和 `[]`。provider 端可能对 `null` 有特殊处理（如"不传 skills"）。改空 slice 前要确认 provider 行为。
3. **`DefaultEvaluator` nil receiver**：当前通过 `NewDefaultEvaluator()` 构造，不会产生 nil。但接口 `Evaluator` 的调用方可能持有 nil 实现。加 nil 校验是 defensive。
4. **`configOptionalBool` 的 string→bool 转换**：如果配置来自 JSON unmarshal 到 `map[string]any`，bool 字段会正确解析为 Go bool。只有手动构造 map 时才会出现 string "true"。风险低但改进可读性。
5. **`skill_extractor.go` 第二参数 `_ any`**：注释说"preserve stale call sites while the live old candidate writer remains disabled"。删除会破坏所有调用点签名。建议先标 `// Deprecated: will be removed` 再下一轮删。
6. **`manifest.go:24-37` Build 的 TransportMode**：当前硬编码 `dto.ManifestTransportStdioOnly`。即使 discoverPeers 成功找到 HTTP addr，transport mode 仍是 stdio-only。这意味着 HTTP addr 只是 metadata，不影响实际 transport 选择。discoverPeers 的 Warn 优先级因此降低。
7. **`skills.go:95-109` skillDedupKey 的格式**：key 格式是 `name@version` 或 `key:xxx@version` 或 `ref:scope:type:name:path@version`。如果任何字段包含 `@` 或 `:` 会产生歧义。当前 skill name/key 不含这些字符（由 UI 校验），但没有 server-side 校验。长尾风险。

---

下一轮范围建议：
- `internal/module/turn/prompt_assembly.go`（prompt 组装）
- `internal/module/turn/assembler.go`（input 组装）
- `internal/module/turn/redaction.go`（敏感信息脱敏）
- `internal/module/turn/trajectory_collector.go`（轨迹收集）
- 或切换到 `internal/contract/`（核心接口定义、error 类型）
