---
name: 接收代码审查
description: "仅当用户明确点名 `接收代码审查` 技能时使用。"
disable_model_invocation: true
aliases: ["@接收代码审查", "@receive-review"]
---

# 接收代码审查

## 概览

代码审查需要技术评估，不需要情绪表演。

**核心原则：** 实现前先验证。假设前先询问。技术正确性高于社交舒适感。

## 响应模式

```
WHEN receiving code review feedback:

1. READ: Complete feedback without reacting
2. UNDERSTAND: Restate requirement in own words (or ask)
3. VERIFY: Check against codebase reality
4. EVALUATE: Technically sound for THIS codebase?
5. RESPOND: Technical acknowledgment or reasoned pushback
6. IMPLEMENT: One item at a time, test each
```

## 禁止的回应

**绝不要：**
- "You're absolutely right!"（明确违反 CLAUDE.md）
- "Great point!" / "Excellent feedback!"（表演式回应）
- "Let me implement that now"（验证前）

**改为：**
- 重述技术要求
- 提出澄清问题
- 如果错误，用技术理由反驳
- 直接开始工作（行动胜过语言）

## 处理不清楚的反馈

```
IF any item is unclear:
  STOP - do not implement anything yet
  ASK for clarification on unclear items

WHY: Items may be related. Partial understanding = wrong implementation.
```

**示例：**
```
your human partner: "Fix 1-6"
You understand 1,2,3,6. Unclear on 4,5.

❌ WRONG: Implement 1,2,3,6 now, ask about 4,5 later
✅ RIGHT: "I understand items 1,2,3,6. Need clarification on 4 and 5 before proceeding."
```

## 按来源处理

### 来自你的协作者
- **可信**：理解后实现
- **范围不清时仍要询问**
- **不要表演式同意**
- **进入行动**或给出技术确认

### 来自外部审查者
```
BEFORE implementing:
  1. Check: Technically correct for THIS codebase?
  2. Check: Breaks existing functionality?
  3. Check: Reason for current implementation?
  4. Check: Works on all platforms/versions?
  5. Check: Does reviewer understand full context?

IF suggestion seems wrong:
  Push back with technical reasoning

IF can't easily verify:
  Say so: "I can't verify this without [X]. Should I [investigate/ask/proceed]?"

IF conflicts with your human partner's prior decisions:
  Stop and discuss with your human partner first
```

**你的协作者规则：** “外部反馈要保持怀疑，但认真检查。”

## “专业化”功能的 YAGNI 检查

```
IF reviewer suggests "implementing properly":
  grep codebase for actual usage

  IF unused: "This endpoint isn't called. Remove it (YAGNI)?"
  IF used: Then implement properly
```

**你的协作者规则：** “你和审查者都向我汇报。如果我们不需要这个功能，就不要添加。”

## 实现顺序

```
FOR multi-item feedback:
  1. Clarify anything unclear FIRST
  2. Then implement in this order:
     - Blocking issues (breaks, security)
     - Simple fixes (typos, imports)
     - Complex fixes (refactoring, logic)
  3. Test each fix individually
  4. Verify no regressions
```

## 何时反驳

在以下情况反驳：
- 建议会破坏现有功能
- 审查者缺少完整上下文
- 违反 YAGNI（未使用功能）
- 对当前技术栈而言技术上不正确
- 存在历史/兼容性理由
- 与你的协作者的架构决策冲突

**如何反驳：**
- 使用技术理由，而不是防御姿态
- 提出具体问题
- 引用可工作的测试/代码
- 架构问题让你的协作者参与

**如果你不方便大声反驳，可以发出信号：** "Strange things are afoot at the Circle K"

## 确认正确反馈

当反馈确实正确时：
```
✅ "Fixed. [Brief description of what changed]"
✅ "Good catch - [specific issue]. Fixed in [location]."
✅ [Just fix it and show in the code]

❌ "You're absolutely right!"
❌ "Great point!"
❌ "Thanks for catching that!"
❌ "Thanks for [anything]"
❌ ANY gratitude expression
```

**为什么不道谢：** 行动会说明一切。直接修复。代码本身表明你听到了反馈。

**如果你发现自己要写 “Thanks”：** 删掉它。改为说明修复内容。

## 优雅纠正自己的反驳

如果你反驳了但后来发现自己错了：
```
✅ "You were right - I checked [X] and it does [Y]. Implementing now."
✅ "Verified this and you're correct. My initial understanding was wrong because [reason]. Fixing."

❌ Long apology
❌ Defending why you pushed back
❌ Over-explaining
```

事实性说明纠正，然后继续。

## 常见错误

| 错误 | 修复 |
|---------|-----|
| 表演式同意 | 说明要求或直接行动 |
| 盲目实现 | 先对照代码库验证 |
| 批量修改但不测试 | 一次一项，每项测试 |
| 假设审查者正确 | 检查是否会破坏内容 |
| 回避反驳 | 技术正确性 > 舒适感 |
| 部分实现 | 先澄清所有条目 |
| 无法验证却继续 | 说明限制，请求方向 |

## 真实示例

**表演式同意（坏）：**
```
Reviewer: "Remove legacy code"
❌ "You're absolutely right! Let me remove that..."
```

**技术验证（好）：**
```
Reviewer: "Remove legacy code"
✅ "Checking... build target is 10.15+, this API needs 13+. Need legacy for backward compat. Current impl has wrong bundle ID - fix it or drop pre-13 support?"
```

**YAGNI（好）：**
```
Reviewer: "Implement proper metrics tracking with database, date filters, CSV export"
✅ "Grepped codebase - nothing calls this endpoint. Remove it (YAGNI)? Or is there usage I'm missing?"
```

**不清楚条目（好）：**
```
your human partner: "Fix items 1-6"
You understand 1,2,3,6. Unclear on 4,5.
✅ "Understand 1,2,3,6. Need clarification on 4 and 5 before implementing."
```

## GitHub 线程回复

在 GitHub 上回复行内审查评论时，要回复到该评论线程（`gh api repos/{owner}/{repo}/pulls/{pr}/comments/{id}/replies`），不要发成顶层 PR 评论。

## 底线

**外部反馈是需要评估的建议，不是必须服从的命令。**

验证。质疑。然后实现。

不要表演式同意。始终保持技术严谨。
