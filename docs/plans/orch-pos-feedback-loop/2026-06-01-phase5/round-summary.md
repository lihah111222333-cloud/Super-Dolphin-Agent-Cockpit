# M5 输出统一与评分表补齐

日期：2026-06-01

本轮目标：补齐领导要求中“机器评分、复审、计划、实施、审查、修复、再评分”的可见证据，并先统一低风险列表工具的出参结构。

## 严格流程记录

| 步骤 | 状态 | 产物 |
| --- | --- | --- |
| 机器评分 | 已执行 | `score-table.md`, `scorecards/output-uniformity.json` |
| 复审 | 已执行 | `adjudication.md` |
| 编写计划 | 已执行 | `implementation-plan.md` |
| 实施 | 已执行 | 代码改动 |
| 机器审查实施 | 已执行 | `review-after-implementation.md` |
| 修复 | 已执行 | 修复测试桩命名冲突 |
| 再评分校对 | 已执行 | `rescore.md` |
| 剩余缺口回流 | 已执行 | `issues-ledger.json` |

## 本轮边界

M5 只做兼容式新增，不强行破坏已有返回类型。

先处理当前已经是对象输出的工具：

- `task_list_dags`
- `task_list_runs`
- `shared_file_list`
- `list_models`

暂缓直接数组输出的工具：

- `list_agents`
- `workspace_list_runs`
- `prompt_list`
- `command_list`

暂缓原因：这些工具现在直接返回数组，改成对象会破坏现有调用方和测试。它们进入下一轮迁移缺口，需要先设计 `compat_mode` 或新版本工具名。
