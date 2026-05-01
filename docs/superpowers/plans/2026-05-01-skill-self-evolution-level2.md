# Level 2: 反馈驱动 Skill 自进化 — 实现计划

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task.

**Goal:** 让系统自动从用户反复给出的 feedback 中提炼 skill candidate，用户在技能页面审批后生效。

**Architecture:** 三段管道 — (A) 前端审批 UI：侧边栏红点 + SkillsPage 内审批面板；(B) Feedback 频率追踪；(C) Skill 提议管道。

**Tech Stack:** Vue 3、Go 1.22、sqlc、existing dream executor

**Threshold:** 同类 feedback 累计 3 次触发提议。

---

Task 1-3: 前端审批 UI | Task 4-5: 频率追踪 | Task 6-7: 提议管道 | Task 8: 历史加载 | Task 9: 端到端验证

详细步骤见对话中的完整计划。
