# M6 机器评分表

评分规则：0-100 分，越高越好。A/B/C 表示三台评分机器，裁决分为综合裁决。

## 迁移前评分

| 维度 | 权重 | 评分机 A | 评分机 B | 评分机 C | 裁决分 | 主要扣分点 | M6 目标 |
| --- | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| 认知负荷 | 20% | 62 | 60 | 63 | 62 | AI 要记住哪些工具直接返回数组 | 76 |
| 出参统一度 | 25% | 25 | 28 | 25 | 26 | 直接数组无法携带 `hint/total/showing` | 64 |
| 可发现性 | 15% | 48 | 50 | 47 | 48 | schema 没有告诉 AI 可要 envelope | 78 |
| 兼容风险控制 | 20% | 90 | 88 | 86 | 88 | 默认数组很安全，但也限制统一度 | 90 |
| 测试覆盖 | 20% | 58 | 60 | 58 | 59 | 没有 envelope 迁移测试 | 82 |
| 加权总分 | 100% | 56.2 | 57.2 | 56.1 | 56.5 | 最大短板是出参统一度 | 77.0 |

## 工具级裁决表

| 工具 | 当前默认出参 | 破坏性风险 | M6 处理 |
| --- | --- | ---: | --- |
| `list_agents` | `[]AgentSnapshot` | 高 | 默认不变；新增 `envelope=true` 返回 `{agents,data,total,showing,truncated,hint}` |
| `workspace_list_runs` | `[]workspaceRunDTO` | 高 | 默认不变；新增 `envelope=true` 返回 `{runs,data,total,showing,truncated,hint}` |
| `prompt_list` | `[]promptTemplateDTO` | 高 | 默认不变；新增 `envelope=true` 返回 `{prompts,data,total,showing,truncated,hint}` |
| `command_list` | `[]commandCardDTO` | 高 | 默认不变；新增 `envelope=true` 返回 `{commands,data,total,showing,truncated,hint}` |

## 验收线

| 验收项 | 通过标准 |
| --- | --- |
| 默认兼容 | 不传 `envelope` 时仍返回数组 |
| 统一出参 | 传 `envelope=true` 时返回统一 envelope |
| 可发现性 | schema 暴露 `envelope` 参数 |
| hint | 每个 envelope 输出都提供下一步 hint |
| 测试 | tools 包测试通过 |
