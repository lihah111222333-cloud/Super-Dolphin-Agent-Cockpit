# P18 Phase 2：记忆行为规则注入

> 预计：1 天 | builder 依赖：Phase 0 | **实际注入接线依赖：Phase 3 + Phase 4.5 + Phase 4**

## 目标
生成记忆系统的行为规则 prompt（taxonomy + save/access/trust），注入到 system prompt **dynamic memory slot**。

> 范围澄清：本 Phase 只对齐 `loadMemoryPrompt()` 产出的 **behavioral rules**，**不包含** `buildMemoryPrompt()` 那条把 `MEMORY.md` 正文/entrypoint 内容直接注入 prompt 的路径。
>
> **现状审查结论（2026-04-14）**：当前 `internal/module/memory/` 只有类型/根目录骨架；`internal/module/prompt/` 的 `AssembleStart()` 仅透传 `BaseInstructions/DeveloperInstructions`、`AssembleTurn()` 仍返回空 `TurnAssembly`；`internal/` 内也不存在可复用的 `OnTurnStart` memory hook。故本 Phase 交付的是 **behavior builder 契约 + 规则文案**，不是已经接入运行链路的能力。

## 运行模式分派（优先级严格）

```
1. KAIROS && autoEnabled && kairosActive? → buildAssistantDailyLogPrompt(skipIndex)
2. TEAMMEM && isTeamMemoryEnabled()?    → buildCombinedMemoryPrompt(extra, skipIndex)
3. autoEnabled?                          → buildMemoryLines(...)
4. 否则                                   → return null（记忆系统禁用）
```

> **交叉审查修订**（Agent 9）：补上 `autoEnabled=false → null` 禁用分支。
> **来源**：`memdir.ts:427-507`

## V3 实现：Standard 模式（Phase 2 范围）

> Go 侧先只落 `MemoryModeStandard` 可运行实现；KAIROS / TEAMMEM 只保留为源码对照与未来扩展位，不在 P18 Phase 2 交付。

为避免把 Claude 的“调度层”和“规则构造层”混成一个函数，V3 文档显式拆成两层语义：
- `LoadMemoryPrompt(mode, autoEnabled, skipIndex, extraGuidelines)`：负责模式分派、禁用分支、runtime ensure 等外层调度
- `BuildMemoryLines(skipIndex, extraGuidelines)`：负责 Standard memory 规则正文
- 源码里的 `skipIndex` 来自 feature flag、`extraGuidelines` 来自 env/source config；**V3 对外实现**可以把它们提升为显式 API 参数
- 若保留 `BuildMemoryPrompt(...)` 作为 façade，只能把它视为 V3 统一入口名，**不能**暗示内部只有一个 builder

### 1. 四种记忆类型 Taxonomy（Individual 版本）

| type | 存什么 | 不讨论 scope |
|------|--------|-------------|
| user | 角色/目标/职责/知识背景/协作偏好 | 避免负面判断、避免无关人物评价 |
| feedback | 纠正 + 被确认有效的非显然做法；**不要只记失败，也要记已被验证有效的成功做法** | 正文结构：`rule + Why: + How to apply:` |
| project | 项目背景/决策动机/截止日期/谁在做什么/目标/事故（**只保存不能从代码或 git history 推导的信息**） | 相对日期转绝对日期；正文：`fact + Why: + How to apply:` |
| reference | 外部系统哪里找什么信息及用途（不是快照） | 保存入口指针 |

> **审查结论**：这 4 个 type **完整覆盖 Claude Individual taxonomy**；`TEAMMEM/Combined` 是**模式**不是第五种 type，Phase 5 的 `user/project/local` 是**scope/ACL 维度**也不是 type，文档与实现都不得混淆。

### 2. 保存规则（Save）
- 显式 `remember` → 立即保存
- 显式 `forget` → 查找并删除
- 写 topic file → 更新 MEMORY.md 索引（`skipIndex` 时仅写文件）
- `skipIndex`、`extraGuidelines` 必须作为显式 API/验收项存在，不只在正文里一句带过
- 优先更新已有 memory，不制造重复
- **保存/删除动作只允许发生在 runtime `sanitize + resolve + authorize` 成功之后**；提示词层不会授予额外文件系统权限，`deny`/`not_visible`/`local_unavailable` 视为停止条件，不得绕过 authorizer
- **提示词约束**：模型不要先 `mkdir` / 探测 memory 目录；**runtime 可以自行 ensure 目录存在**，两者不要混淆
- frontmatter `name/description/type` 要持续保持与内容一致
- `MEMORY.md` 索引必须是一行一条、约 150 字以内、只放 pointer/hook、无 frontmatter、**不能把 memory 正文直接写进索引**
- 按**语义主题**组织，不按时间堆积
- 错误/过时 memory 要**更新或删除**

### 3. 读取规则（Access）
- memory 看起来相关时读取
- 用户提到先前对话时读取
- 用户显式要求 recall/check/remember 时 **必须** 读取
- 用户说 ignore/not use memory → **视同 `MEMORY.md` 为空；不要应用、引用、比较、提及任何已记住内容**
- **主线程 behavioral rules 只允许消费 runtime 已 surfaced 的 memory 正文或 tool 成功返回的结果**；不得自行拼路径、脑补其它 scope、或把 `@agent`/`name/path` 猜解成已授权访问
- **可见性/授权边界由 runtime 的 `sanitize + resolve + authorize` 决定**；仅仅知道 `name/path/@agent`，**不等于** 已获访问权
- Phase 5/7 scope 语义在此预留：`user` 仅 root agent / 当前线程授权链可访问；`project` 仅同 project / 同 workspace 授权链可访问；`local` 仅当前机器 + 当前 project 授权链可访问
- runtime / tool 返回 `deny`、`not_visible`、`local_unavailable` 或等价结果时，必须把该 memory 视为 unavailable；不得偷偷 fallback 到其它 memory root / scope 重试

### 4. 信任规则 / Before recommending from memory（Trust）
- memory 是“过去某时为真”，不是当前真相
- 与当前观测冲突 → 以当前为准 → 更新/删除 stale memory
- 引用 memory 中的文件路径 → 先检查文件是否存在
- 引用 memory 中的函数/**flag** → 先搜索验证仍然存在；若 V3 想把“类型名”也纳入，需在实现与测试里明确标注为 V3 补强
- 用户准备据此采取行动时，**必须先验证再建议**
- 问“近期/当前 repo 状态”时，优先 `git log` / 读代码，不信 memory snapshot

### 5. 不能存什么（排除列表完整版）
- **V3 安全强化**：API keys、tokens、credentials、密码等不得写入任何 memory（包括 user/feedback/project/reference 所有类型）；Claude 原始 Individual 路径未把这条写成全局规则，V3 主动上移为统一约束
- 代码模式、**约定(conventions)**、架构、文件路径、项目结构
- Git history、最近改动、谁改了什么
- 调试方案、修复 recipe
- 已写在 CLAUDE.md 的信息
- 当前会话临时任务细节、短期状态、**当前对话上下文**
- PR list / activity summary / 当前进度跟踪 / tasks / plans
- **即使用户显式要求保存，上述排除项也仍然不保存**
- 只保存其中 **surprising / non-obvious** 的部分

> **来源**：`restored-src/src/memdir/memoryTypes.ts:183-195` (WHAT_NOT_TO_SAVE)

### 6. Memory 与其他持久化机制的区分（来自 memdir.ts:254-257，非 WHAT_NOT_TO_SAVE 原文）
- Plan 和 tasks 有专门机制，不存进 memory
- 非 trivial 实现方案对齐应进 Plan
- 当前会话的步骤拆解和进度跟踪应进 tasks

> **来源**：`restored-src/src/memdir/memdir.ts:254-257`

### 7. Standard 模式输出合同（`BuildMemoryLines`）
固定骨架按以下顺序输出，避免测试与运行时漂移：
1. 持久记忆系统简述（可短，不和 entrypoint 注入混写）
2. taxonomy（仅 4 种 type）
3. save rules
4. access rules
5. trust rules
6. exclusions
7. memory vs plan/tasks
8. `extraGuidelines`（如有，**追加在末尾**，不得覆盖核心 save/access/trust 规则）

补充约束：
- 该顺序必须稳定，测试按固定 section 顺序断言
- `skipIndex` 只影响“保存动作如何写索引”的文案，不改变 taxonomy/access/trust/exclusions 语义
- `extraGuidelines` 是扩展位，不是 override 位；不得削弱核心安全边界、授权边界、ignore-memory 语义

### 8. Searching past context（本 Phase 决策）
- Claude `buildMemoryLines()` 末尾存在一个 **feature-gated** 的 `Searching past context` 段
- **P18 Phase 2 的主线程 behavioral rules 选择显式 deferred**：本期不把 Claude 原段落直接迁入 Standard memory rules
- 该 omission 必须在文档与测试中被视为**有意延后**，而不是默默漏掉
- Phase 6 的 memory retrieval 负责检索链；若 Phase 5 的 agent-memory builder 需要更高 Claude parity，须在各自任务与测试中单列恢复，不能偷偷回灌到本 Phase

### 9. 注入落点 / lifecycle hook（审查修订）
- **不使用** `OnTurnStart`、MCP `HookManager` / `HookLifecycle`、或其它 control-plane hook；这些路径与 memory behavioral rules 无关，且当前 `internal/` 也不存在可复用的 `OnTurnStart` memory hook
- 正确落点是 Phase 3 的动态 prompt section：`PromptSection{Name: "memory", Region: PromptRegionDynamic}`
- 该 section 由 `PromptAssemblyService.AssembleStart()` 在 `thread/start` 阶段计算，产物进入 `StartAssembly.BaseInstructions`
- Provider 只消费 assembly 产物：codex 走结构化 `BaseInstructions + DeveloperInstructions`，claude 走 launch system prompt 拼接；**都不应在 provider 内二次重建 memory rules**
- 主线程 `MEMORY.md` / AutoMem entrypoint、`claudeMd`、relevant memories 分别属于 Phase 4/5/6 的其它注入链，**不得**与本 Phase 的 behavioral rules builder 混成同一个 hook

## 任务清单
- [ ] `memory/prompt_builder.go`：`LoadMemoryPrompt(mode, autoEnabled, skipIndex, extraGuidelines)` + `BuildMemoryLines(skipIndex, extraGuidelines)`（或等价 façade + 内部拆层）
- [ ] 输出覆盖：taxonomy + save + access + trust + exclusions + memory-vs-plan/tasks
- [ ] 明确 Phase 2 对 `Searching past context` 的 deferred 决策
- [ ] 在 Phase 3/4 文档中复用本契约：`memory` dynamic section → `PromptAssemblyService.AssembleStart()` → `StartAssembly.BaseInstructions`
- [ ] 测试：prompt 包含所有必要关键字，并校验 ignore-memory / deny-means-unavailable / action-before-trust / deterministic-order / extraGuidelines-append-only 文案强度

## 验收
- prompt 输出包含四种类型定义，且明确“scope/mode ≠ fifth type”
- prompt 包含完整排除列表
- prompt 包含 save/access/trust 规则，且明确 `sanitize + resolve + authorize` 边界
- 文档明确 lifecycle 落点：`memory` dynamic section → `PromptAssemblyService.AssembleStart()` → `StartAssembly.BaseInstructions`，而**不是** turn hook / MCP hook
- `skipIndex` / `extraGuidelines` 有显式 API 与测试覆盖
