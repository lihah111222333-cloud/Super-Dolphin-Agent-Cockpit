# P18 Phase 6：记忆检索（Relevant Memories）

> 预计：2 天 | 依赖：Phase 1

## 目标
实现基于相关性的异步记忆召回，不阻塞主 turn。

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

> **来源**：`restored-src/src/query.ts:301-304` (启动)
> **来源**：`restored-src/src/query.ts:1599-1613` (消费)

## Manifest 构建

```text
memory_write / memory_forget / migrate
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
10. 若用户 `@` 提及带 memory 的 agent，切到该 agent 的 memory dir 检索（而非 AutoMem 根目录）
11. 多目录检索使用 **bounded worker pool**；`continue/继续/expand` 类 turn 优先复用上轮结果

> **来源**：`restored-src/src/memdir/findRelevantMemories.ts:39-141`

## Selector 基础设施

- `memory/selector.go` 提供 provider-neutral `RelevantMemorySelector` 接口，统一封装 side-query
- selector 只接收 manifest + 最后一条 user 输入 + recent successful tools 摘要，不直接依赖 provider DTO
- 默认超时 **3s**、单轮只发一次 side-query；**这是 V3 provider-neutral/runtime safety 增强，不是 Claude 原样锚点**
- side-query 运行在更小权限边界：只读 / 无工具 / 无网络 / 不可写 memory
- 增加 query cache：`normalizedUserText + manifestVersion + provider + recentToolSignature`
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

> **来源**：`attachments.ts:2231-2234, 2520-2541`

## 正文截断

- **200 行** / **4096 bytes**
- 超限时保留前缀内容，不直接丢弃整条 memory
- 截断后附提示：让模型用 Read 工具查看完整文件

## Session 阈值

- 已 surfaced 正文累计达 **60KB** → 停止预取
- 统计口径来自历史 `relevant_memories` attachment 中 `mem.content.length` 的累计值，而不是文件原始大小
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
- consume 必须幂等：同一批 relevant memories 最多注入一次，不因多轮工具循环重复附加
- consume 通过 CAS `ready → injected` 保证只注入一次
- selector / prefetch / consume 共享去重状态时，只允许在同一 turn generation 内读写，避免跨 provider/跨轮次串味

## Nested Memory（暂不实现，排期 P19）

> nested_memory 是另一套机制（按目标文件路径补充 CLAUDE.md/rules），
> 复杂度高，与 relevant_memories 独立，P18 不纳入。

## 任务清单
- [ ] `memory/retrieval.go`：StartRelevantMemoryPrefetch / ConsumeIfReady / cancel + CAS consume
- [ ] `memory/scan.go`：RepairScanMemoryFiles / BuildOrLoadManifest
- [ ] `memory/selector.go`：SelectRelevantMemories（cheap prefilter + provider-neutral side-query + structured JSON parse）
- [ ] `memory/manifest.go`：sidecar manifest 更新与版本管理
- [ ] 集成到 turn 执行链路
- [ ] 去重 + 截断 + fail-soft + query cache + cooldown

## 验收
- 异步预取不阻塞 turn
- manifest 正确排除 MEMORY.md
- selector 输出结构化 JSON，并经过白名单过滤
- **三段式去重测试**
- prefetch cancel / singleflight / CAS consume 测试
- 60KB 阈值测试
