# M6 复审裁决

## 多机意见汇总

| 议题 | 机器意见 | 裁决 |
| --- | --- | --- |
| 是否把默认返回从数组改成对象 | A 认为统一度最高，B/C 认为破坏性太大 | 不改默认返回 |
| 是否新增 `envelope=true` | 三台机器都支持 | 新增统一开关 |
| 参数名用什么 | A 建议 `format=envelope`，B/C 建议布尔值 | 采用 `envelope`，降低入参认知负荷 |
| 是否每个工具保留旧语义字段 | 三台机器都支持 | 保留 `agents/runs/prompts/commands` |
| 是否继续记录分页缺口 | 三台机器都支持 | 继续回流 true total / has_more |

## 最终裁决

M6 采用兼容迁移：

1. 不传 `envelope`：保持旧数组返回。
2. 传 `envelope=true`：返回统一对象。
3. schema 暴露 `envelope`，让 AI 可以发现这个能力。
4. 不引入复杂 `format` 枚举，避免增加入参认知负荷。
