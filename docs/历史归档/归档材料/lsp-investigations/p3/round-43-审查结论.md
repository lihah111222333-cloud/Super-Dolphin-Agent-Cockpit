# 第 43 轮审查结论

## 审查范围

- `internal/platform/shared/timeparse.go`（ParseRFC3339Loose、DecodeHistoryMetadata、CloneTime、CloneInt64、WithEventTime、ResolveEventTime、FirstEventTime、EventTimeFromPayload、ParseEventTime、eventTimeFromContext）
- `internal/platform/shared/hookutil.go`（NormalizeSelectorScope）
- `internal/platform/shared/turnutil.go`（IsRemoteTurnInput 委托）
- `internal/platform/shared/search.go`（LineMatcher、NewLineMatcher、Find）
- `internal/platform/shared/project_key.go`（4 个 pathutil 委托）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `timeparse.go:12-22` ParseRFC3339Loose | 静默 | 解析失败时静默返 `time.Time{}` 零值（line 20-21 错误丢弃） | caller 拿到 zero time 不知是「输入空」「解析失败」还是「真实 1970-01-01」 | 改为 `(time.Time, error)` 让 caller 区分 |
| `timeparse.go:17-21` 解析两次 | 性能/逻辑 | 先尝试 RFC3339Nano，失败再 RFC3339；第二次用 `_` 丢错 | 即使 RFC3339 失败也返零值；double parse 在热路径有性能成本 | 用 `time.Parse` 一次（RFC3339Nano 兼容 RFC3339）+ 错误返回 |
| `timeparse.go:24-33` DecodeHistoryMetadata | 静默 | json.Unmarshal 失败 / 空 payload 都返 nil | caller 无法区分「无 metadata」「JSON 损坏」「空 metadata」 | 返 `(map, error)` |
| `timeparse.go:48-56` WithEventTime | 静默 | `ctx == nil` fallback Background；`timestamp.IsZero()` 静默不注入 | 与第28/37/41轮 ctx-nil 同问题；零值 timestamp 是 caller bug | nil ctx 改 panic；零值 timestamp Warn 日志 |
| `timeparse.go:68-75` FirstEventTime | 静默 fallback | 所有 fallback 零值时 `return time.Now()` | 调用方期望「拿到上游时间戳」但可能拿到当前墙钟时间——会让事件顺序错乱 | 全部零值时 return error 让 caller 决定 |
| `timeparse.go:77-90` EventTimeFromPayload | 弱契约 | 6 个 fallback key 硬编码顺序（timestamp/ts/createdAt/...） | 与第42轮 FirstPayloadString 同问题；caller 不知优先级 | 改为 caller 显式传 keys 或 schema 常量 |
| `hookutil.go:9-19` NormalizeSelectorScope | 静默 | `scope == nil` 时返空 SelectorScope；不报错 | nil selector scope 是 caller 信息丢失，但被掩盖；后续 dispatch 用空 scope 路由错误 | nil scope 返 (SelectorScope, bool) 或 panic（开发期） |
| `search.go:16-35` NewLineMatcher | 弱契约 | regexMode + caseSensitive 4 种组合，不同 needle 处理（line 21-24 加 (?i) 前缀；line 32-34 lower needle） | 4 种组合靠条件分支处理，组合性差；caller 难以预测 needle 在 LineMatcher 内的最终形态 | 拆为 4 个具名构造器（NewLiteralMatcher / NewRegexMatcher / NewCaseInsensitive...） |
| `search.go:21-23` regex case-insensitive | 静默 | 直接拼接 `(?i)` 到 needle 前——caller 已加 `(?i)` 时变成 `(?i)(?i)` | 双前缀虽然 valid 但反映 needle 操作的隐式行为 | 检查 needle 是否已有 (?i) 前缀；或文档化 |
| `project_key.go` 整体 | 弱契约 | 4 个 1-line 委托（与第42轮 idgen.go 同模式） | 委托层无业务逻辑，增加导航成本 | 与 idgen.go 一并合并到 paths.go |
| `turnutil.go:1-7` 整文件 | 弱契约 | 1 行委托——是否值得单独成文件？ | 同上 | 合并 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `timeparse.go:17-21` ParseRFC3339Loose | 双 time.Parse 调用——time.Parse 内部有正则状态机 | 高 QPS 场景下累积；加 LRU cache by needle string |
| `search.go:25-28` regexp.Compile | 每次 NewLineMatcher 都 compile 一次正则 | LRU cache by query 字符串 + 设置（regexMode/caseSensitive） |
| `search.go:37-55` Find | utf8.RuneCountInString 在长 line（>10K 字符）上 O(N) | 长 line 时改简单 ASCII 检测短路 |
| `timeparse.go:58-66` ResolveEventTime | 三层 fallback：ctx → payload → fallbacks → time.Now()；每层都同步操作 | 已是 hot path，但操作均纯 in-memory；无延迟问题 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `timeparse.go:14-15` ParseRFC3339Loose | 空字符串静默返 zero |
| `timeparse.go:17-21` | 双 Parse 失败静默返 zero |
| `timeparse.go:25-26` DecodeHistoryMetadata | null/空 raw 静默返 nil |
| `timeparse.go:29-31` | Unmarshal 失败 / 空 payload 静默返 nil |
| `timeparse.go:49-51` WithEventTime | nil ctx fallback Background |
| `timeparse.go:52-54` | 零值 timestamp 静默不注入 ctx |
| `timeparse.go:74` FirstEventTime | 全空 fallback 静默 time.Now() |
| `hookutil.go:10-12` NormalizeSelectorScope | nil scope 静默返空 |
| `search.go:21-23` | needle 已含 (?i) 时双前缀 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `timeparse.go:12-22` ParseRFC3339Loose | 名字含 "Loose" 暗示宽松解析，但只接受 RFC3339Nano + RFC3339 两种格式 |
| `timeparse.go:77-90` EventTimeFromPayload | 6 个 fallback key 硬编码 |
| `search.go:16-35` NewLineMatcher | 4 种组合靠 bool 参数 |
| `search.go:37-55` Find | 返 `(int, bool)` —— int 是 rune count（不是 byte index），靠文档约定 |
| `project_key.go` | 4 个委托函数无差异化文档 |

## 修复优先级

### P0（必须本周修）
1. **`timeparse.go:12-22` ParseRFC3339Loose 静默返 zero**——这是事件时间戳解析的核心 helper。LLM/外部输入解析失败时返 zero time，下游`FirstEventTime` line 74 fallback `time.Now()`——错误的时间戳被静默替换为当前墙钟时间。结果：事件按错误顺序排序、log 时序错乱、retry/timeout 决策基于错时间。改为 (time.Time, error)。
2. **`timeparse.go:68-75` FirstEventTime 全空 fallback time.Now()**——这是事件溯源核心的反模式。在分布式系统中事件时间戳应该来自上游（不来自当前节点墙钟）；上游全空时应 fail 让 caller 决定，而非静默替换。结果：本应被拒绝的「无时间戳事件」被赋当前时间持久化，事后无法分辨真伪。

### P1（本月）
3. `timeparse.go:48-56` WithEventTime nil ctx 改 panic
4. `timeparse.go:24-33` DecodeHistoryMetadata 改 (map, error)
5. `hookutil.go:9-19` NormalizeSelectorScope nil scope 改 (SelectorScope, bool)
6. `search.go:16-35` NewLineMatcher 拆 4 个具名构造器
7. `timeparse.go:77-90` EventTimeFromPayload 改为 caller 显式传 keys

### P2（下个 sprint）
8. `project_key.go`、`turnutil.go`、`idgen.go`、`idgen_agent.go` 合并到 paths.go / ids.go
9. `search.go:21-23` regex (?i) 双前缀检测
10. `timeparse.go` 加 RFC3339 cache LRU

## 边界条件

1. **`timeparse.go` 与第36轮 notification.go 静默零值的对称性**：两处都把「输入缺失/损坏」静默映射为零值。但事件时间戳尤其敏感——分布式系统的因果序、retry 退避、SLA 计算全依赖时间戳准确性。当前 `ParseRFC3339Loose` + `FirstEventTime time.Now()` 的组合让「时间戳错误」无声无息地被「当前时间」替换。**P0 因为它影响系统正确性而非可观测性**。
2. **`timeparse.go:48-56` WithEventTime 的零值守卫是合理的**：`timestamp.IsZero()` 时不注入 ctx——这个分支本身合理（避免污染 ctx），但 caller 调用此函数本意是注入；零值不注入相当于 no-op。建议：①caller 责任：调用前自行判断；②或加 Debug 日志「WithEventTime called with zero timestamp, no-op」。
3. **`search.go` LineMatcher 整体设计正面**：rune count 而非 byte index 是 UTF-8 正确性的细致处理（line 43、54）。NewLineMatcher 拒绝空查询（line 18-20）是 fail-fast 实践。但 4 种 bool 组合（regex/case）让 API 难用。建议改 builder pattern 或多构造器。
4. **`hookutil.go` 仅 1 个函数 19 行**：与 `turnutil.go` 6 行同模式——单函数文件。Go 风格通常按业务领域聚合而非拆分。建议合并到一个 `hooks.go` 文件。
5. **`project_key.go` 的「双键」设计**：`ProjectKeyFromCwd` 和 `MemoryProjectKeyFromCwd` 是不同 key 派生规则——建议在文档说明用例区分（普通项目 key vs memory 子系统 key）。`SanitizeSkillProjectKey` 和 `SanitizeMemoryProjectKey` 同理。当前 4 个委托无任何区分文档。
6. **整个 shared 包的「委托文件」反模式**：本轮（timeparse 部分非委托、project_key 全委托、turnutil 全委托）+ 第42轮（idgen 全委托、idgen_agent 全委托、log_error 全委托、validation 部分委托）累计 6+ 个文件以委托为主。这个模式有合理性（提供稳定 API 边界），但当前实现过度——很多委托文件少于 10 行。建议项目内重新评估 shared/util 的边界，要么大幅合并，要么明确每个文件的领域职责。

---

**本轮总结**：发现 2 个 P0 问题集中在事件时间戳处理：①ParseRFC3339Loose 解析失败静默返 zero；②FirstEventTime 全空时 fallback time.Now() 用墙钟代替上游时间戳。这两者组合导致「时间戳错误」被无声替换。`search.go` LineMatcher rune count 处理是 UTF-8 正确性正面案例。整个 shared 包的「委托文件」模式过度——多个文件少于 10 行，建议重新评估包边界。

**累计进度**：43 轮完成。cron `fd4b4728` 继续推进。
