# P18 Phase 2：记忆行为规则注入

> 预计：1 天 | 依赖：Phase 0

## 目标
生成记忆系统的行为规则 prompt（taxonomy + save/access/trust），注入到 system prompt dynamic section。

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

`BuildMemoryPrompt()` 输出内容：

### 1. 四种记忆类型 Taxonomy（Individual 版本）

| type | 存什么 | 不讨论 scope |
|------|--------|-------------|
| user | 角色/目标/职责/知识背景/协作偏好 | 避免负面判断、避免无关人物评价 |
| feedback | 纠正 + 被确认有效的非显然做法 | 正文结构：`rule + Why: + How to apply:` |
| project | 项目背景/决策动机/截止日期/谁在做什么/目标/事故 | 相对日期转绝对日期；正文：`fact + Why: + How to apply:` |
| reference | 外部系统哪里找什么信息及用途（不是快照） | 保存入口指针 |

> **审查修订**（Agent 2）：V3 单用户，用 Individual 版本（不含 scope）。
> Combined 版本留待 Team Memory 实现时再加。

### 2. 保存规则
- 显式 `remember` → 立即保存
- 显式 `forget` → 查找并删除
- 写 topic file → 更新 MEMORY.md 索引（skipIndex 时仅写文件）
- 优先更新已有 memory，不制造重复
- memory 目录视为已存在，禁止先 `mkdir` / 探测
- frontmatter `name/description/type` 要持续保持与内容一致
- 按**语义主题**组织，不按时间堆积
- 错误/过时 memory 要**更新或删除**

### 3. 读取规则
- memory 看起来相关时读取
- 用户提到先前对话时读取
- 用户显式要求 recall/check/remember 时 **必须** 读取
- 用户说 ignore/not use memory → 当空处理

### 4. 信任规则
- memory 是"过去某时为真"，不是当前真相
- 与当前观测冲突 → 以当前为准 → 更新/删除 stale memory
- 引用 memory 中的文件/函数/**flag** 前先验证还在不在
- 用户准备据此采取行动时，**必须先验证再建议**
- 问“近期/当前 repo 状态”时，优先 `git log` / 读代码，不信 memory snapshot

### 5. 不能存什么（排除列表完整版）
- 代码模式、**约定(conventions)**、架构、文件路径、项目结构
- Git history、最近改动、谁改了什么
- 调试方案、修复 recipe
- 已写在 CLAUDE.md 的信息
- 当前会话临时任务细节、短期状态、**当前对话上下文**
- 即使用户要求，也不机械保存 PR list / activity summary
- 只保存其中 **surprising / non-obvious** 的部分

> **来源**：`restored-src/src/memdir/memoryTypes.ts:183-195` (WHAT_NOT_TO_SAVE)

### 6. Memory 与其他持久化机制的区分（来自 memdir.ts:254-257，非 WHAT_NOT_TO_SAVE 原文）
- Plan 和 tasks 有专门机制，不存进 memory
- 非 trivial 实现方案对齐应进 Plan
- 当前会话的步骤拆解和进度跟踪应进 tasks

> **来源**：`restored-src/src/memdir/memdir.ts:254-257`

## 任务清单
- [ ] `memory/prompt_builder.go`：BuildMemoryPrompt(mode) string
- [ ] 输出覆盖：taxonomy + save + access + trust + exclusions
- [ ] 测试：prompt 包含所有必要关键字

## 验收
- prompt 输出包含四种类型定义
- prompt 包含完整排除列表
- prompt 包含 save/access/trust 规则
