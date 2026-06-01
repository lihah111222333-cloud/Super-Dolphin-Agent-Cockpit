# M5 再评分校对

## 实施后评分

| 维度 | 权重 | M5 前裁决分 | M5 后评分 | 变化 | 说明 |
| --- | ---: | ---: | ---: | ---: | --- |
| 认知负荷 | 20% | 82 | 86 | +4 | AI 可以统一读取 `data`，不用先猜列表字段 |
| 出参统一度 | 25% | 42 | 70 | +28 | 对象型列表工具已统一；数组型工具进入下一轮 |
| hint 质量 | 15% | 55 | 74 | +19 | 新增下一步 hint，指向 `pos` 读详情或后续用法 |
| 兼容风险控制 | 20% | 78 | 86 | +8 | 旧字段保留，没有强改数组型工具 |
| 测试覆盖 | 20% | 75 | 84 | +9 | 新增四类 envelope 断言 |
| 加权总分 | 100% | 66.0 | 79.8 | +13.8 | 达到 M5 验收线 |

## 工具级复核

| 工具 | 旧字段 | 新统一字段 | hint | 结果 |
| --- | --- | --- | --- | --- |
| `task_list_dags` | `dags` | `data/total/showing/truncated` | 有 | 通过 |
| `task_list_runs` | `runs` | `data/total/showing/truncated` | 有 | 通过 |
| `shared_file_list` | `files` | `data/total/showing/truncated` | 有 | 通过 |
| `list_models` | `providers` | `data/total/showing/truncated` | 有 | 通过 |
| `list_agents` | 直接数组 | 未改 | 无 | 回流下一轮 |
| `workspace_list_runs` | 直接数组 | 未改 | 无 | 回流下一轮 |
| `prompt_list` | 直接数组 | 未改 | 无 | 回流下一轮 |
| `command_list` | 直接数组 | 未改 | 无 | 回流下一轮 |

## 裁决

M5 达成“安全范围内的出参统一”。直接数组型工具不在本轮强改，原因是兼容风险高，已进入剩余缺口表，下一轮继续按同一反馈闭环处理。
