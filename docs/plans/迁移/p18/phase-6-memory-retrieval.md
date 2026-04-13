# P18 Phase 6：记忆检索（Relevant Memories）

> 预计：2 天 | 依赖：Phase 1

## 目标
实现基于相关性的异步记忆召回，不阻塞主 turn。

## 异步预取机制（审查修订 Agent 4）

### 触发
- **每个 user turn 启动一次** `StartRelevantMemoryPrefetch()`
- 触发 gate：
  - Auto Memory enabled
  - 存在最后一条非 meta user message
  - 输入不是单词级短 prompt
  - 已 surfaced 正文累计未超过 **60KB**

### 消费
- 在 turn 的**每轮工具循环**中零等待检查一次
- ready → 注入
- 没 ready → 跳过本轮，下一轮再试
- **不阻塞主 turn**

> **来源**：`restored-src/src/query.ts:301-304` (启动)
> **来源**：`restored-src/src/query.ts:1599-1613` (消费)

## Manifest 构建

```
scanMemoryFiles(memoryDir)
  → 递归扫描 .md 文件
  → 排除 MEMORY.md
  → 每个文件只读前 30 行（FRONTMATTER_MAX_LINES）
  → 解析 frontmatter: type / description
  → 按 mtimeMs 倒序
  → 保留最新 200 个 header（MAX_MEMORY_FILES）
  → 格式化: "- [type] relative/path (ISO时间戳): description"
     注：每行前缀 `- `，type/description 可选
```

> **来源**：`restored-src/src/memdir/memoryScan.ts:21-93`

## 选择逻辑

1. `alreadySurfaced` **selector 前**先过滤，避免浪费名额
2. 用 Sonnet `sideQuery()` + manifest 选择相关文件
3. 每个目录由 prompt 建议"up to 5"（软约束），合并后 **全局 `.slice(0, 5)`**（硬约束）
4. 只返回 **filename**，再由 `byFilename` 映射成绝对路径 + mtimeMs
5. 没有明显相关 → 允许返回空数组
6. Recent successful tools 会压低工具 reference/API docs 误召回
7. 模型输出后再做 manifest 白名单过滤
8. 若用户 `@` 提及带 memory 的 agent，切到该 agent 的 memory dir 检索（而非 AutoMem 根目录）
9. 支持**多目录并行召回** `Promise.all(dirs.map(...))`

> **来源**：`restored-src/src/memdir/findRelevantMemories.ts:39-141`

## Selector 基础设施

- `memory/selector.go` 提供 provider-neutral `RelevantMemorySelector` 接口，统一封装 side-query
- selector 只接收 manifest + 最后一条 user 输入 + recent successful tools 摘要，不直接依赖 provider DTO
- 默认超时 **3s**、单轮只发一次 side-query；超时/预算不足直接 fail-soft 返回空
- codex / claude 共用同一 selector 服务，provider 差异只体现在底层 query client 适配

## 三段式去重（交叉审查修订 Agent 5）

| 阶段 | 机制 | 职责 |
|------|------|------|
| 1. selector 前 | `alreadySurfaced` 预过滤 | 避免浪费 5 个名额 |
| 2. 多目录合并后 | `!readFileState.has && !alreadySurfaced.has` | belt-and-suspenders guard |
| 3. consume 时 | `filterDuplicateMemoryAttachments()` | 最终去重 + 写入 readFileState |

> **来源**：`attachments.ts:2231-2234, 2520-2541`

## 正文截断

- **200 行** / **4096 bytes**
- 截断后附提示：让模型用 Read 工具查看完整文件

## Session 阈值

- 已 surfaced 正文累计达 **60KB** → 停止预取

## Fail-soft

- 扫描异常 / selector 异常 / 读取失败 → 返回空结果，不抛硬错误

## 并发安全

- prefetch 状态按 `threadID + generation` 隔离；provider 切换或 compact invalidate 后旧 generation 结果不得再注入
- consume 必须幂等：同一批 relevant memories 最多注入一次，不因多轮工具循环重复附加
- selector / prefetch / consume 共享去重状态时，只允许在同一 turn generation 内读写，避免跨 provider/跨轮次串味

## Nested Memory（暂不实现，排期 P19）

> nested_memory 是另一套机制（按目标文件路径补充 CLAUDE.md/rules），
> 复杂度高，与 relevant_memories 独立，P18 不纳入。

## 任务清单
- [ ] `memory/retrieval.go`：StartRelevantMemoryPrefetch / ConsumeIfReady
- [ ] `memory/scan.go`：ScanMemoryFiles / FormatManifest
- [ ] `memory/selector.go`：SelectRelevantMemories（provider-neutral side-query）
- [ ] 集成到 turn 执行链路
- [ ] 去重 + 截断 + fail-soft

## 验收
- 异步预取不阻塞 turn
- manifest 正确排除 MEMORY.md
- **三段式去重测试**
- 60KB 阈值测试
