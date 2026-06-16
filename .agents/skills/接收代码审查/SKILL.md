---
name: 接收代码审查
description: 收到代码审查反馈、准备实现建议前使用，尤其是反馈不清楚或技术上可疑时；要求技术严谨和验证，而不是表演式同意或盲目实现
---

# 接收代码审查

## 节索引（按需读，勿全文加载）

- 概览 — 代码审查需要技术评估，不需要情绪表演。
  详见 references/01-概览.md
- 响应模式 — ``` WHEN receiving code review feedback:。
  详见 references/02-响应模式.md
- 禁止的回应 — 
  详见 references/03-禁止的回应.md
- 处理不清楚的反馈 — ``` IF any item is unclear: STOP - do not implement anything yet ASK for clarif…
  详见 references/04-处理不清楚的反馈.md
- 按来源处理 — ``` BEFORE implementing: 1.
  详见 references/05-按来源处理.md
- “专业化”功能的 YAGNI 检查 — ``` IF reviewer suggests "implementing properly": grep codebase for actual usag…
  详见 references/06-“专业化”功能的 YAGNI 检查.md
- 实现顺序 — ``` FOR multi-item feedback: 1.
  详见 references/07-实现顺序.md
- 何时反驳 — 在以下情况反驳：。
  详见 references/08-何时反驳.md
- 确认正确反馈 — 当反馈确实正确时： ``` ✅ "Fixed.
  详见 references/09-确认正确反馈.md
- 优雅纠正自己的反驳 — 如果你反驳了但后来发现自己错了： ``` ✅ "You were right - I checked [X] and it does [Y].
  详见 references/10-优雅纠正自己的反驳.md
- 常见错误 — | 错误 | 修复 | |---------|-----| | 表演式同意 | 说明要求或直接行动 | | 盲目实现 | 先对照代码库验证 | | 批量修改但…
  详见 references/11-常见错误.md
- 真实示例 — ``` Reviewer: "Remove legacy code" ❌ "You're absolutely right!
  详见 references/12-真实示例.md
- GitHub 线程回复 — 在 GitHub 上回复行内审查评论时，要回复到该评论线程（`gh api repos/{owner}/{repo}/pulls/{pr}/comments/…
  详见 references/13-GitHub 线程回复.md
- 底线 — 验证。
  详见 references/14-底线.md

> 需要某节内容时，使用 Read 工具读取对应 references/ 文件，不要整文加载。
