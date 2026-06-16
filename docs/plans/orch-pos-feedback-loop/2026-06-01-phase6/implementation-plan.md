# M6 实施计划

## 步骤

1. 给 `list_agents` 输入增加 `envelope`。
2. 给 `workspace_list_runs` 输入增加 `envelope`。
3. 给 `prompt_list` 输入增加 `envelope`。
4. 给 `command_list` 输入增加 `envelope`。
5. 为四类工具增加 envelope 输出结构，保留旧语义字段。
6. 在 schema 中暴露 `envelope`。
7. 增加单测：默认路径仍返回数组，`envelope=true` 返回统一对象。
8. 运行 `go test ./internal/sidecar/orch/tools -count=1`。
9. 审查、修复、再评分，并登记剩余分页缺口。

## 非目标

本轮不把 `envelope=true` 设为默认值。默认切换需要确认所有外部调用方都已迁移。
