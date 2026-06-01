# M6 直接数组出参兼容迁移

日期：2026-06-01

本轮目标：处理 M5 回流的直接数组型列表工具，让新流程可以拿到统一 envelope，同时保持默认旧数组输出不变。

## 严格流程记录

| 步骤 | 状态 | 产物 |
| --- | --- | --- |
| 机器评分 | 已执行 | `score-table.md`, `scorecards/array-output-migration.json` |
| 复审 | 已执行 | `adjudication.md` |
| 编写计划 | 已执行 | `implementation-plan.md` |
| 实施 | 已执行 | 代码改动 |
| 机器审查实施 | 已执行 | `review-after-implementation.md` |
| 修复 | 已执行 | 修复测试 helper 名称错误 |
| 再评分校对 | 已执行 | `rescore.md` |
| 剩余缺口回流 | 已执行 | `issues-ledger.json` |

## 本轮范围

- `list_agents`
- `workspace_list_runs`
- `prompt_list`
- `command_list`

## 核心约束

默认返回仍保持旧数组，避免破坏已有调用方。新增统一参数 `envelope=true`，新 AI 流程显式打开后返回对象：

- `data`
- `total`
- `showing`
- `truncated`
- `hint`

并保留各工具自己的旧语义字段，例如 `agents/runs/prompts/commands`。
