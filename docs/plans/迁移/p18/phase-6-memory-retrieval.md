# P18 Phase 6：记忆检索（Relevant Memories）

> 预计：2 天 | 依赖：Phase 1、Phase 4.5（turn adapter 注入点）、Phase 5（`@agent` scope/ACL）

## 目标
实现基于相关性的异步记忆召回，不阻塞主 turn。

## Claude 源码核对补充（2026-04-14）

- `query.ts` 在每个 user turn 的 `queryLoop` 进入 `while` 前只启动一次 `using pendingMemoryPrefetch = startRelevantMemoryPrefetch(...)`；consume block 位于工具循环末尾、skill discovery 注入前，条件是 `settledAt != nil && consumedOnIteration == -1`，未 ready 时直接跳过、不等待。
- Claude 当前 prefetch handle 只有 `promise / settledAt / consumedOnIteration / [Symbol.dispose]()`；计划中的 `generation / turnID / singleflight / CAS state` 属于 **V3 并发安全增强**，不是 Claude 现状。
- `startRelevantMemoryPrefetch()` 的真实 gate 是：`isAutoMemoryEnabled()`、GrowthBook `tengu_moth_copse`、存在最后一条非 meta user message、输入不是单词级 prompt、`collectSurfacedMemories(messages).totalBytes < 60 * 1024`。它通过 `createChildAbortController(toolUseContext.abortController)` 绑定 turn 生命周期。
- `getRelevantMemoryAttachments()` 若命中 `@agent` 且对应 agent 定义带 `memory` scope，则**仅**检索这些 agent 目录；否则退回 auto-memory 目录。多目录当前实现是 `Promise.all(dirs.map(...))`，随后 `.flat().filter(!readFileState.has && !alreadySurfaced.has).slice(0, 5)`，**没有** bounded worker pool。
- Claude 当前没有 `manifest.jsonl` sidecar / 本地 cheap prefilter / “低置信度才 side-query” / query cache / cooldown。`findRelevantMemories()` 每次都 `scanMemoryFiles()` + `selectRelevantMemories()`：递归 `readdir`，读取前 30 行 frontmatter，按 `mtimeMs` 倒序截前 200 个 header，然后直接发一次 Sonnet sideQuery 做 JSON 选择。
- 去重的 Claude 原始三段式是：`alreadySurfaced` 在 selector 前过滤、`getRelevantMemoryAttachments()` 合并后再次用 `readFileState + alreadySurfaced` 过滤、consume 时 `filterDuplicateMemoryAttachments()` 先过滤再写回 `readFileState`。这一点与本计划一致，应保持不变。
- 当前硬阈值来自源码：每文件 `200 lines / 4096 bytes`、全局选择 `slice(0, 5)`、session surfaced 正文累计 `60 * 1024`。其中 `60KB` 是 parity 阈值；文档里出现的 `manifest sidecar`、`Top20 prefilter`、`worker pool` 等均应视为 **V3 设计增强**，不是 Claude 原样行为。

## 异步预取机制（审查修订 Agent 4/6/22）

### 触发
- **每个 user turn 启动一次** `StartRelevantMemoryPrefetch()`
- 触发 gate：
  - Auto Memory enabled
  - 存在最后一条非 meta user message
  - 输入不是单词级短 prompt
  - 已 surfaced 正文累计未超过 **60KB**
- handle 至少包含：`promise / settledAt / consumedOnIteration / generation / turnID`
- 同一 `threadID + generation + turnID` 最多允许一个 in-flight prefetch；新 turn / compact / provider switch / resume restore 必须取消旧 handle

### 消费
- 在 turn 的**每轮工具循环**中零等待检查一次
- 仅当 `settledAt != nil && consumedOnIteration == -1` 时允许消费
- ready → 通过 CAS 标记后注入
- 没 ready → 跳过本轮，下一轮再试
- **不阻塞主 turn**

### V3 接线点
- provider-neutral consume hook 放在 **thread/turn 层、且紧贴 provider 调用前的共享边界**，而不是散落到各 provider read loop 中各自实现一套
- thread/turn 层负责在最终 `dto.TurnRequest` 进入 provider 前调用 `ConsumeIfReady()`；provider 只读取已经组装完成的 relevant memory 产物
- relevant memories **不进入** `StartAssembly` / `TurnAssembly.UserContextText`；应由 thread/turn adapter 以 attachment/hint 形式并列装配，再交给 provider 消费
- 一旦进入 provider-specific `turnInputsFromRequest()` / `buildTurnText()` / transport payload 组装阶段，consume window 就视为关闭；禁止在 provider-specific 组装之后再回头 mutation `req.Inputs`
- Codex / Claude 若因 transport 差异需要适配，也只能在 adapter 层做薄映射，不得各自重写一套去重/consume 状态机
- 审查补充：当前 V3 `internal/module/turn/service.go:77-101` 把 `dto.TurnRequest` 交给 provider session，而 `internal/provider/codexapp/session_turn.go:37-85` / `internal/provider/claudecli/session_turn.go:167-288` 会立刻把 `req.Inputs` 转成 provider payload；因此 consume hook 必须落在 **provider 调用前的共享边界**，不能落到 provider-specific 组装之后

### 调用时序

```text
turn start
  → prefetch ready?
  → ConsumeIfReady()
  → append relevant-memory attachments / hints to dto.TurnRequest
  → provider call
```

- 若 prefetch 尚未 ready，则跳过 consume，直接继续本轮 provider call；不额外等待

> **来源**：`restored-src/src/query.ts:301-304` (启动)
> **来源**：`restored-src/src/query.ts:1599-1613` (消费)

## Manifest 构建

```text
turn/end hook 写盘 / `/forget` / migrate
  → 维护 retrieval sidecar（**默认 `manifest.jsonl`**；sqlite / boltdb 留作后续优化选项）
turn prefetch
  → 优先读取 sidecar header
  → sidecar 缺失/损坏时才 repair scan .md 文件
  → repair scan 有独立 budget（建议 500ms~1s）
  → 读取 header 时顺手拿 mtimeMs，避免双 stat / 二次 IO
  → repair 后回写 sidecar
  → 每个 header 只保留 type / description / lang / aliases / search_keys / mtimeMs / bytes / priority
  → description 进入 manifest 前先过敏感信息脱敏；高风险时回退为 redacted 占位
  → 按 mtimeMs 倒序
  → 保留最新 200 个 header（MAX_MEMORY_FILES）
  → 格式化: "- [type] relative/path (ISO时间戳): description"
     注：每行前缀 `- `，type/description 可选
```

分层 fail-soft：
- 单文件 header 读取失败：跳过该文件，不影响同目录其他文件
- 单目录 repair scan 失败：该目录返回空 manifest，不影响其他目录
- sidecar 缺失/损坏：回退 repair scan，但不拖垮整轮 turn

> **来源**：`restored-src/src/memdir/memoryScan.ts:21-93`

## 选择逻辑

1. `alreadySurfaced` **selector 前**先过滤，避免浪费名额
2. 先做本地 cheap prefilter（basename/title/description/aliases/search_keys 的关键词 / trigram / BM25）缩到 Top 20
3. 仅在低置信度时再发 Sonnet `sideQuery()`；高置信度命中可直接用本地排序结果
4. side-query 输出必须是结构化 JSON：`{ "selected_memories": ["..."] }`
5. 只解析第一个 text block，再做 manifest 白名单过滤，丢弃 hallucinated filename
6. 每个目录由 prompt 建议“up to 5”（软约束），合并后 **全局 `.slice(0, 5)`**（硬约束）
7. 只返回 **filename**，再由 `byFilename` 映射成绝对路径 + mtimeMs
8. 没有明显相关 → 允许返回空数组
9. `recent successful tools` 的统计窗口是：从当前最后一条真实 user message 往前回扫，直到上一个 human turn 边界；只保留成功且未报错的工具名
10. 若用户 `@` 提及带 memory 的 agent，仅允许切到**当前线程可见且已授权**的 agent memory dir；目录解析必须复用 Phase 5 的 `GetAgentMemoryDir(scope, agentType)` + sanitize / ACL 规则，未命中或未授权则直接不检索
11. 多目录检索使用 **bounded worker pool**；`continue/继续/expand` 类 turn 优先复用上轮结果

> **来源**：`restored-src/src/memdir/findRelevantMemories.ts:39-141`

## Selector 基础设施

- `memory/selector.go` 提供 provider-neutral `RelevantMemorySelector` 接口，统一封装 side-query
- selector 只接收 manifest + 最后一条 user 输入 + recent successful tools 摘要，不直接依赖 provider DTO
- 默认超时 **3s**、单轮只发一次 side-query；**这是 V3 provider-neutral/runtime safety 增强，不是 Claude 原样锚点**
- side-query 运行在更小权限边界：只读 / 无工具 / 无网络 / 不可写 memory
- 增加 query cache：`normalizedUserText + manifestVersion + provider + recentToolSignature + memoryRoot + scope + agentType + generation`
- side-query 失败要分类：`timeout / budget_exhausted / provider_unavailable / invalid_selector_output / whitelist_filtered_to_empty`；短期 cooldown 后再重试，必要时退回 cheap prefilter 结果
- codex / claude 共用同一 selector 服务，provider 差异只体现在底层 query client 适配

## 三段式去重（交叉审查修订 Agent 5）

| 阶段 | 机制 | 职责 |
|------|------|------|
| 1. selector 前 | `alreadySurfaced` 预过滤 | 避免浪费 5 个名额 |
| 2. 多目录合并后 | `!readFileState.has && !alreadySurfaced.has` | belt-and-suspenders guard |
| 3. consume 时 | `filterDuplicateMemoryAttachments()` | 最终去重 + **先过滤再写入** `readFileState` |

硬规则：
- prefetch/read 阶段**禁止**提前把 relevant memories 写入 `readFileState`
- 只有 consume 阶段幸存的 attachments 才能写入 `readFileState`，避免 prefetch 把自己提前过滤掉
- 若 surfacing 最终复用普通 `dto.InputItem` 承载，`internal/module/turn/assembler.go:48-71,251-258` 现有 `inputKey()` 去重只能算**传输层兜底**，不能替代这里的三段式去重，也不能反向驱动 `alreadySurfaced/readFileState` 语义

> **来源**：`attachments.ts:2231-2234, 2520-2541`

## 正文截断

- **200 行** / **4096 bytes**
- 超限时保留前缀内容，不直接丢弃整条 memory
- 截断后附提示：让模型用 Read 工具查看完整文件

## 注入载体与历史回放

- relevant memories 的统一交付形态是 **provider-neutral attachment records + 对应 hint/context block**，不是普通用户文本输入，也不是 `TurnAssembly.UserContextText`
- 去重主键以 `logical memory id + canonical path` 为准；`readFileState` / history / replay 使用同一标识，不允许三处各自发明一套 key
- history/replay/UI 恢复时以同一 attachment 协议重建，不把 relevant memories 伪装成普通 user text

## Session 阈值

- 已 surfaced 正文累计达 **60KB** → 停止预取
- 统计口径来自历史 `relevant_memories` attachment 中 `mem.content.length` 的累计值，而不是文件原始大小
- **60KB 是 Claude parity 阈值，不要偷换成当前 Go `inputAssembler` 的 `64 * 1024` 文本上限**（`internal/module/turn/assembler.go:12-15`）；预算判断必须发生在最终 `InputItem` 装配前，并把 surfacing wrapper / truncation warning / hint 文本一起计入 headroom
- compact 后旧 attachment 消失，计数自然归零，允许重新 surfacing

## Fail-soft

- 单文件 header 读取失败：跳过该文件
- 单目录召回失败：该目录返回空数组，不影响其他目录
- surfacing 读取失败：跳过该 memory，不中断整批结果
- side-query 失败：按分类记录原因、进入 cooldown，并可退回 cheap prefilter；只有最终无幸存结果时才整体返回空
- 不抛硬错误，不阻塞主 turn

## 并发安全

- prefetch 状态按 `threadID + generation + turnID` 隔离；provider 切换或 compact invalidate 后旧 generation 结果不得再注入
- 每个 `threadID + generation + turnID` 使用 singleflight 抑制重复 prefetch
- `StartRelevantMemoryPrefetch()` 必须继承 **turn-scoped context**；禁止 `context.Background()` 或 detached `SafeGo` 风格 fire-and-forget，避免 goroutine 穿越 turn / generation 生命周期
- 取消旧 handle 时必须连同 selector / worker pool 子 goroutine 一并收敛（`errgroup` / `WaitGroup` drain）；不得只替换句柄而放任旧 worker 晚到写回 `ready`
- consume 必须幂等：同一批 relevant memories 最多注入一次，不因多轮工具循环重复附加
- consume 通过 CAS `ready → injected` 保证只注入一次；被 cancel / stale-generation 标记的 handle 即使晚到 ready 也只能进入 discarded，不得再转 injected
- selector / prefetch / consume 共享去重状态时，只允许在同一 turn generation 内读写，避免跨 provider/跨轮次串味

## Nested Memory（P18 未实现，详见 p18-unimplemented.md）

> nested_memory 是另一套机制（按目标文件路径补充 CLAUDE.md/rules），
> 复杂度高，与 relevant_memories 独立，当前转记入 [p18-unimplemented.md](p18-unimplemented.md)。

## 任务清单
- [ ] `memory/retrieval.go`：StartRelevantMemoryPrefetch / ConsumeIfReady / cancel + CAS consume + turn-scoped ctx / worker drain
- [ ] `memory/scan.go`：RepairScanMemoryFiles / BuildOrLoadManifest
- [ ] `memory/selector.go`：SelectRelevantMemories（cheap prefilter + provider-neutral side-query + structured JSON parse）
- [ ] `memory/manifest.go`：默认 `manifest.jsonl` sidecar + 版本管理
- [ ] 集成到 turn 执行链路（consume hook 固定在 provider call 前的共享边界；relevant memories 不进入 `TurnAssembly.UserContextText`，也不允许在 provider-specific 组装后再 mutation）
- [ ] 去重 + 截断 + fail-soft + query cache + cooldown

## 验收
- 异步预取不阻塞 turn
- consume 时序可验证：`turn start → prefetch ready → consume → provider call`；未 ready 时跳过 consume 直接继续 provider call，不阻塞
- manifest 正确排除 MEMORY.md
- sidecar 默认格式固定为 `manifest.jsonl`，repair 逻辑与 query cache key 可验证
- selector 输出结构化 JSON，并经过白名单过滤
- **三段式去重测试**
- prefetch cancel / singleflight / CAS consume / stale-ready-discard 测试
- relevant memory attachment 的 history/replay roundtrip 测试
- 60KB parity 阈值 vs 64KiB transport clamp 测试
