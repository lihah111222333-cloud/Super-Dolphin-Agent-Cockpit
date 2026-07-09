---
name: 请求代码审查
description: 完成任务、实现重要功能，或合并前需要验证工作是否满足要求时使用
aliases: ["@请求代码审查", "@request-review"]
---

# 请求代码审查

派发 superpowers:code-reviewer 子代理，在问题扩散前捕获它们。审查者会获得精确构造的评估上下文，而不是你的会话历史。这能让审查者聚焦于工作产物，而不是你的思考过程，也能保留你自己的上下文继续工作。

**核心原则：** 尽早审查，经常审查。

## 何时请求审查

**强制：**
- 子代理驱动开发中每个任务完成后
- 重要功能完成后
- 合并到 main 前

**可选但有价值：**
- 卡住时（获得新视角）
- 重构前（基线检查）
- 修复复杂 bug 后

## 如何请求

**1. 获取 git SHA：**
```bash
BASE_SHA=$(git rev-parse HEAD~1)  # or origin/main
HEAD_SHA=$(git rev-parse HEAD)
```

**2. 派发 code-reviewer 子代理：**

在 super-agent-v3 中，优先使用当前平台可用的子代理能力直接派发审查者。若本轮审查需要持久 DAG、重试、租约或结构化交接记录，可选创建审查 DAG/node：

1. `task_create_dag`：创建本轮审查 DAG，或在现有实现 DAG 中新增 reviewer node。
2. `task_start_dag`：在需要立即执行审查时启动 run。
3. `task_dispatch_node`：将 ready 的 reviewer node 指派给 `superpowers:code-reviewer` 对应执行者。
4. `task_update_node`：审查完成后写入 `done`、`failed` 或 `blocked`，并保存 findings 摘要。

没有 mcp-orch `task_*` 工具不是阻断条件；直接派发子代理或改为本会话单代理审查，并说明观测限制。

**占位符：**
- `{WHAT_WAS_IMPLEMENTED}`：你刚构建的内容
- `{PLAN_OR_REQUIREMENTS}`：它应该做什么
- `{BASE_SHA}`：起始提交
- `{HEAD_SHA}`：结束提交
- `{DESCRIPTION}`：简短总结

**3. 处理反馈：**
- 立即修复 Critical 问题
- 继续前修复 Important 问题
- 记录 Minor 问题供之后处理
- 如果审查者错了，基于理由反驳

## 示例

```
[Just completed Task 2: Add verification function]

You: Let me request code review before proceeding.

BASE_SHA=$(git log --oneline | grep "Task 1" | head -1 | awk '{print $1}')
HEAD_SHA=$(git rev-parse HEAD)

[Dispatch superpowers:code-reviewer subagent]
  WHAT_WAS_IMPLEMENTED: Verification and repair functions for conversation index
  PLAN_OR_REQUIREMENTS: Task 2 from docs/superpowers/plans/deployment-plan.md
  BASE_SHA: a7981ec
  HEAD_SHA: 3df7661
  DESCRIPTION: Added verifyIndex() and repairIndex() with 4 issue types

[Subagent returns]:
  Strengths: Clean architecture, real tests
  Issues:
    Important: Missing progress indicators
    Minor: Magic number (100) for reporting interval
  Assessment: Ready to proceed

You: [Fix progress indicators]
[Continue to Task 3]
```

## 与工作流集成

**子代理驱动开发：**
- 每个任务后审查
- 在问题叠加前捕获
- 修好后再进入下一个任务

**执行计划：**
- 每个批次（3 个任务）后审查
- 获取反馈、应用反馈、继续

**临时开发：**
- 合并前审查
- 卡住时审查

## 红旗

**绝不要：**
- 因为“很简单”而跳过审查
- 忽略 Critical 问题
- 带着未修复的 Important 问题继续
- 与有效技术反馈争辩

**如果审查者错了：**
- 用技术理由反驳
- 展示能证明其工作的代码/测试
- 请求澄清

见模板：请求代码审查/code-reviewer.md
