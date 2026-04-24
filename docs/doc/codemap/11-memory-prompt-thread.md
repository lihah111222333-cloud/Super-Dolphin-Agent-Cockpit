<<<<<<< Updated upstream
# 11 Memory / Prompt / Thread 拆卷索引

> 兼容旧链接：原合卷已拆为 memory 与 prompt-thread 两卷；此文件只保留导航，不再承载正文。

## 新入口

- Memory 主链、retrieval、Claude parity、子包依赖树：[`11-memory.md`](11-memory.md)
- Prompt / Thread / Provider 启停链路：[`11-prompt-thread.md`](11-prompt-thread.md)

## 协作说明

- A11a：本轮只更新 memory 卷。
- A11b：同步维护 prompt / thread 卷。

## 拆卷映射表

| 卷 | 重点章节 | 讲什么 |
|---|---|---|
| [11-memory.md](11-memory.md) | §2、§4、§5、§6、§7、§9 | memory 子包拆分、compat bridge、写侧闭环、turn 检索、Claude parity、provider-neutral snapshot |
| [11-prompt-thread.md](11-prompt-thread.md) | 当前入口 / 说明 | prompt / thread / provider `start/resume/fork` 稳定入口位；A11b 继续补正文 |

## 阅读顺序补充

1. 先读 [11-memory.md](11-memory.md) §2，确认当前源码结论。
2. 再读 [11-memory.md](11-memory.md) §5，顺着写侧 → start 注入 → turn 检索读主链。
3. 只想看 provider-neutral 汇合点，直接跳 [11-memory.md](11-memory.md) §5.5 B。
4. 若问题落到 thread/start、resume、fork 的执行链，联读 [07-module-write.md](07-module-write.md) §2.4 / §3.4，再回 [11-prompt-thread.md](11-prompt-thread.md)。

## 跨卷跳转锚点

- 看 `PROMPT_START_CURRENT_DATE` 先去 [11-prompt-thread.md](11-prompt-thread.md)；只想确认 assembly 汇合点，再看 [11-memory.md](11-memory.md) §5.5 B。
- 看 `internal/module/memory/domain_bridges.go` 去 [11-memory.md](11-memory.md) §3、§4.2、§6。
- 看 `internal/module/memory/retrieval_bridge.go` 去 [11-memory.md](11-memory.md) §3、§4.2、§6。
- 看 thread/start、resume、fork 的 runtime 链，去 [07-module-write.md](07-module-write.md) §2.4，再回 [11-prompt-thread.md](11-prompt-thread.md)。

## 最近一次重大变更摘要

- **2026-04-17**：11 从旧合卷拆成 `11-memory.md` + `11-prompt-thread.md`，本页改为兼容索引。
- **2026-04-20**：memory 卷按子包拆分真值重写，补回 `domain_bridges.go`、`retrieval_bridge.go`、provider-neutral snapshot 口径；prompt-thread 卷继续保留稳定入口位。

## 常见误导

- `11-memory-prompt-thread.md` 只有十几行，**不代表内容少**；正文已经拆进 `11-memory.md` / `11-prompt-thread.md`。
- `11-memory.md` 不只是 retrieval 文档；它还覆盖写侧闭环、prompt invalidation、Claude parity、shared snapshot。
- `11-prompt-thread.md` 目前是稳定入口位，不等于链路不存在；thread 生命周期细节仍要联读 `07-module-write.md`。

## 新增符号入口

| 符号 / 主题 | 去哪看 |
|---|---|
| `PROMPT_START_CURRENT_DATE` | [11-prompt-thread.md](11-prompt-thread.md)；assembly 汇合补看 [11-memory.md](11-memory.md) §5.5 B |
| `internal/module/memory/domain_bridges.go` | [11-memory.md](11-memory.md) §3、§4.2、§6 |
| `internal/module/memory/retrieval_bridge.go` | [11-memory.md](11-memory.md) §3、§4.2、§6 |
| `MemoryRulesProvider` | [11-memory.md](11-memory.md) §5.3 |
| `MemoryContextProvider` | [11-memory.md](11-memory.md) §5.4 |
=======
# 11 Memory / Prompt / Thread 入口索引

> 本卷已拆分，避免单文件继续超过仓库契约的 600 行上限。

## 阅读顺序

1. **[11-memory.md](11-memory.md)**
   - 看 durable memory 根目录、索引、规则、agent memory、prefetch、memory tool。
   - 回答“memory 存哪、何时写、何时读、如何挂到 prompt / turn”。
2. **[11-prompt-thread.md](11-prompt-thread.md)**
   - 看 prompt assembler、thread start/resume/fork、prompt snapshot、provider bridge、blank-thread 首发。
   - 回答“运行时到底怎么接上、snapshot 怎么流、provider 事件怎么回到 thread”。

## 本次拆卷边界

- **11-memory.md**：memory 主链、rules/context/agent/team/tool。
- **11-prompt-thread.md**：
  - `internal/module/prompt/`
  - `internal/module/thread/`
  - `internal/store/thread/` 中 prompt snapshot 相关部分
  - `internal/provider/{unified,codexapp,claudecli}` 的 provider→thread 桥接
  - blank-thread 首发 `resolveLaunchSkillSelectionForStart → startThread → sendMessage`

## 两卷之间的交叉点

### 1. start-time memory 并不单独存 snapshot

- `memory` / `agent_memory` 是 **start-only dynamic section**。
- 它们在 `prompt.AssembleStart()` 时被折叠进：
  - `StartAssembly.BaseInstructions`
  - `PromptAssemblySnapshot.SectionSnapshot`
- 详见：`11-prompt-thread.md` §2 / §3。

### 2. turn-time memory 走 turn 侧，不回写 prompt snapshot

- `memory_context` / relevant-memory attachments / search-past-context 输入，发生在 `prompt.AssembleTurn()` 与 turn service。
- 它们不进入 thread store 的 `PromptSnapshot`。
- 详见：`11-memory.md` §4、`11-prompt-thread.md` §3.4。

### 3. thread 生命周期是 memory hooks 的事件源

- thread 成功 start / resume / stop 后，memory hooks 通过 bus 订阅 thread / turn 事件做索引更新、prefetch 回收、team sync。
- 详见：`11-memory.md` §5、`11-prompt-thread.md` §4。

## 快速结论

- prompt store 与 prompt snapshot 不是一回事：
  - 前者是 `internal/store/prompt` 的用户模板 CRUD；
  - 后者是 `internal/store/thread` 的运行时 `PromptSnapshot`。
- provider bridge 的关键位置不是 UI RPC，而是：
  - provider session 产出 `RawProviderEvent`
  - unified `EventDispatcher` 翻译为 typed event
  - thread `events.go` 订阅 `AgentLaunched` / `AgentFailed`
- blank-thread 首发现在已是：
  - `resolveLaunchSkillSelectionForStart`
  - `startThread`
  - `sendMessage`
  - 再落到后端 `thread/start` + `turn/start`

## 相关卷

- [07-module.md](07-module.md)：模块职责、消费面与入口边界
- [09-provider.md](09-provider.md)：provider 模块结构与驱动职责
- [10-store.md](10-store.md)：thread/prompt/memory 的持久化 contract
- [11-memory.md](11-memory.md)
- [11-prompt-thread.md](11-prompt-thread.md)
>>>>>>> Stashed changes
