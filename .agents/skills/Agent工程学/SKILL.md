---
name: "Agent工程学"
display_name: "Agent工程学"
description: "仅当用户明确点名 `Agent工程学` 技能时使用。"
disable_model_invocation: true
---

# Agent 工程学 (Agentic Engineering)

## Overview
使用此技能来规范 AI Agent 驱动的工程工作流。在这种模式下，AI Agent 执行大部分的实现工作，而人类、当前平台原生多代理能力或可选 `mcp-orch` 编排面负责质量、风险控制与进度管理。

## When to Use
- **症状与用例**:
  - 规划或执行包含多步骤的实现计划时。
  - 调试复杂系统，遇到任务过于庞大导致 Agent 丢失上下文时。
  - 需要管理多模型协同开发流程，不知如何分配任务时。
- **何时不要使用 (When NOT to use)**:
  - 只是进行单次轻量级的代码修复或简单的咨询任务时。

## Core Pattern: Eval-First Loop & Decomposition
**测试驱动开发 (TDD) 的 Agent 视角**：
1. **基准测试**: 运行并捕获失败特征（如契约测试报错）。
2. **分解**: 将修复任务拆分为 Agent 能在一次会话（15分钟单元规则）内验证的步骤。
3. **实现与比较**: Agent 执行实现后，重新运行评估。

## Quick Reference
| 场景 | 推荐操作 / 策略 |
| :--- | :--- |
| **会话延续** | 紧密耦合的 15 分钟开发单元，避免频繁切换丢失近期记忆。 |
| **开启新会话** | 完成重大里程碑或阶段过渡时，防止 Token 爆炸。 |
| **上下文紧凑化** | 阶段完成后手动清理上下文；**禁止**在活跃调试时折叠，以免丢失线索。 |
| **模型路由** | 基础级处理样板代码；中级处理 LSP 补全与重构；高级处理跨文件架构。 |

## Implementation (AI 生成代码审查)
接收 Agent 提交的代码审查时，人类与上游系统的聚焦点应当是：
- 变式与边界情况 (Invariants and edge cases)
- 错误边界 (Error boundaries)（严查高风险吞错点）
- 安全和鉴权假设 (Security and auth assumptions)
- 隐式耦合和回滚风险 (Hidden coupling and rollout risk)

## Common Mistakes
- **错误**: 在一个巨大的 Prompt 中塞入所有任务。
  **修复**: 强制应用 15 分钟单元规则，每个子单元只包含一种核心风险。
- **错误**: 因纯代码风格规范而与 Agent 产生长对话。
  **修复**: 把规范编写到 Linter 配置中，让工具链强制规范。
- **错误**: 模型遇到错误后直接要求人类提供答案。
  **修复**: 升级前，确信低级模型或当前工具链已经尝试尽其所能仍有推理缺口，才升级模型/干预。
