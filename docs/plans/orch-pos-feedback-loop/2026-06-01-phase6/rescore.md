# M6 再评分校对

## 实施后评分

| 维度 | 权重 | M6 前裁决分 | M6 后评分 | 变化 | 说明 |
| --- | ---: | ---: | ---: | ---: | --- |
| 认知负荷 | 20% | 62 | 78 | +16 | AI 只需记一个统一开关 `envelope=true` |
| 出参统一度 | 25% | 26 | 66 | +40 | 直接数组工具已有统一 envelope 路径 |
| 可发现性 | 15% | 48 | 82 | +34 | schema 暴露 `envelope` |
| 兼容风险控制 | 20% | 88 | 91 | +3 | 默认旧数组不变 |
| 测试覆盖 | 20% | 59 | 84 | +25 | 覆盖四个工具的默认和 envelope 路径 |
| 加权总分 | 100% | 56.5 | 80.0 | +23.5 | 达到 M6 验收线 |

## 工具级复核

| 工具 | 默认返回 | `envelope=true` 返回 | 结果 |
| --- | --- | --- | --- |
| `list_agents` | 数组 | `{agents,data,total,showing,truncated,hint}` | 通过 |
| `workspace_list_runs` | 数组 | `{runs,data,total,showing,truncated,hint}` | 通过 |
| `prompt_list` | 数组 | `{prompts,data,total,showing,truncated,hint}` | 通过 |
| `command_list` | 数组 | `{commands,data,total,showing,truncated,hint}` | 通过 |

## 裁决

M6 完成直接数组工具的兼容迁移。默认返回没有破坏，新的 AI 流程可以统一要求 `envelope=true`，从而拿到一致的 `data/total/showing/truncated/hint`。
