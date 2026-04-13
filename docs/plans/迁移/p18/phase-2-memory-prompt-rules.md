# P18 Phase 2：记忆行为规则注入

> 预计：1 天 | 依赖：Phase 0

## 目标
生成记忆系统的行为规则 prompt（taxonomy + save/access/trust），注入到 system prompt **dynamic memory slot**。

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
- 若保留 `BuildMemoryPrompt(...)` 作为 façade，只能把它视为 V3 统一入口名，**不能**暗示内部只有一个 builder

### 1. 四种记忆类型 Taxonomy（Individual 版本）

| type | 存什么 | 不讨论 scope |
|------|--------|-------------|
| user | 角色/目标/职责/知识背景/协作偏好 | 避免负面判断、避免无关人物评价 |
| feedback | 纠正 + 被确认有效的非显然做法 | 正文结构：`rule + Why: + How to apply:` |
| project | 项目背景/决策动机/截止日期/谁在做什么/目标/事故（**只保存不能从代码或 git history 推导的信息**） | 相对日期转绝对日期；正文：`fact + Why: + How to apply:` |
| reference | 外部系统哪里找什么信息及用途（不是快照） | 保存入口指针 |

> **审查修订**（Agent 2）：V3 单用户，只迁移 Claude 源码的 Individual 路径；Claude 原版存在 Combined 版本，但该分支依赖 Team Memory，本轮仅保留为源码对照，不作为 P18 交付范围。

### 2. 保存规则
- 显式 `remember` → 立即保存
- 显式 `forget` → 查找并删除
- 写 topic file → 更新 MEMORY.md 索引（`skipIndex` 时仅写文件）
- `skipIndex`、`extraGuidelines` 必须作为显式 API/验收项存在，不只在正文里一句带过
- 优先更新已有 memory，不制造重复
- **提示词约束**：模型不要先 `mkdir` / 探测 memory 目录；**runtime 可以自行 ensure 目录存在**，两者不要混淆
- frontmatter `name/description/type` 要持续保持与内容一致
- `MEMORY.md` 索引必须是一行一条、约 150 字以内、只放 pointer/hook、无 frontmatter、**不能把 memory 正文直接写进索引**
- 按**语义主题**组织，不按时间堆积
- 错误/过时 memory 要**更新或删除**

### 3. 读取规则
- memory 看起来相关时读取
- 用户提到先前对话时读取
- 用户显式要求 recall/check/remember 时 **必须** 读取
- 用户说 ignore/not use memory → **视同 `MEMORY.md` 为空；不要应用、引用、比较、提及任何已记住内容**

### 4. Before recommending from memory（行动前校验）
- memory 是“过去某时为真”，不是当前真相
- 与当前观测冲突 → 以当前为准 → 更新/删除 stale memory
- 引用 memory 中的文件路径 → 先检查文件是否存在
- 引用 memory 中的函数/类型/**flag** → 先搜索验证仍然存在
- 用户准备据此采取行动时，**必须先验证再建议**
- 问“近期/当前 repo 状态”时，优先 `git log` / 读代码，不信 memory snapshot

### 5. 不能存什么（排除列表完整版）
- **禁止保存敏感信息**：API keys、tokens、credentials、密码等不得写入任何 memory（包括 user/feedback/project/reference 所有类型）
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

### 7. Searching past context（本 Phase 范围声明）
- Claude `buildMemoryLines()` 末尾还会追加 `Searching past context` 段
- P18 Phase 2 若**不迁移**该段，必须显式标记为 deferred，而不是默默省略
- 若迁移，则需单列任务与测试，不与 taxonomy/save/access/trust 混写

## 任务清单
- [ ] `memory/prompt_builder.go`：`LoadMemoryPrompt(mode, autoEnabled, skipIndex, extraGuidelines)` + `BuildMemoryLines(skipIndex, extraGuidelines)`（或等价 façade + 内部拆层）
- [ ] 输出覆盖：taxonomy + save + access + trust + exclusions
- [ ] 明确 `Searching past context`：实现或显式 deferred
- [ ] 测试：prompt 包含所有必要关键字，并校验 ignore-memory / action-before-trust 文案强度

## 验收
- prompt 输出包含四种类型定义
- prompt 包含完整排除列表
- prompt 包含 save/access/trust 规则
- `skipIndex` / `extraGuidelines` 有显式 API 与测试覆盖
