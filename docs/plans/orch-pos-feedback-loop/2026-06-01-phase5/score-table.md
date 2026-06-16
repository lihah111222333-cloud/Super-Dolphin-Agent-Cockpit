# M5 机器评分表

评分规则：0-100 分，分数越高越好。A/B/C 表示三台评分机器，裁决分取综合判断，不简单平均。

## 总体评分

| 维度 | 权重 | 评分机 A | 评分机 B | 评分机 C | 裁决分 | 主要扣分点 | M5 目标 |
| --- | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| 认知负荷 | 20% | 84 | 82 | 80 | 82 | 入参已明显简化，但列表出参仍需要 AI 猜字段 | 86 |
| 出参统一度 | 25% | 40 | 45 | 42 | 42 | 有的返回数组，有的返回对象，缺少统一 `data/total/showing/hint` | 70 |
| hint 质量 | 15% | 54 | 58 | 55 | 55 | 多数列表没有告诉 AI 下一步该用哪个工具 | 72 |
| 兼容风险控制 | 20% | 80 | 76 | 78 | 78 | 全量统一会破坏数组型旧调用方 | 85 |
| 测试覆盖 | 20% | 74 | 76 | 75 | 75 | 缺少列表 envelope 断言 | 84 |
| 加权总分 | 100% | 65.6 | 66.9 | 65.7 | 66.0 | 最大短板是出参统一度 | 79.4 |

## 工具级评分

| 工具 | 当前出参 | 认知负荷 | 出参统一度 | 兼容风险 | M5 裁决 |
| --- | --- | ---: | ---: | ---: | --- |
| `task_list_dags` | 对象：`{dags}` | 72 | 55 | 低 | 本轮兼容新增 `data/total/showing/truncated/hint` |
| `task_list_runs` | 对象：`{runs}` | 70 | 55 | 低 | 本轮兼容新增 `data/total/showing/truncated/hint` |
| `shared_file_list` | 对象：`{files, allowed_prefixes}` | 68 | 58 | 低 | 本轮兼容新增 `data/total/showing/truncated/hint` |
| `list_models` | 对象：`{providers}` | 70 | 58 | 低 | 本轮兼容新增 `data/total/showing/truncated/hint` |
| `list_agents` | 直接数组 | 62 | 25 | 高 | 暂缓，下一轮做兼容迁移方案 |
| `workspace_list_runs` | 直接数组 | 62 | 25 | 高 | 暂缓，下一轮做兼容迁移方案 |
| `prompt_list` | 直接数组 | 60 | 25 | 高 | 暂缓，下一轮做兼容迁移方案 |
| `command_list` | 直接数组 | 60 | 25 | 高 | 暂缓，下一轮做兼容迁移方案 |

## 本轮验收线

| 验收项 | 通过标准 |
| --- | --- |
| 兼容性 | 原字段 `dags/runs/files/providers` 保留 |
| 统一性 | 新增统一字段 `data/total/showing/truncated/hint` |
| AI 易用性 | `hint` 明确下一步工具，例如用 `pos` 读取详情 |
| 测试 | `go test ./internal/sidecar/orch/tools -count=1` 通过 |
| 缺口回流 | 数组型工具进入 `issues-ledger.json` |
