# M5 实施计划

## 目标

降低 AI 读取列表工具结果时的认知负荷，让常用对象型列表工具具备统一出参：

- `data`
- `total`
- `showing`
- `truncated`
- `hint`

同时保留旧字段：

- `dags`
- `runs`
- `files`
- `providers`

## 步骤

1. 在工具层增加轻量 list envelope 构造逻辑。
2. 修改 `task_list_dags`，保留 `dags`，新增统一字段。
3. 修改 `task_list_runs`，保留 `runs`，新增统一字段。
4. 修改 `shared_file_list`，保留 `files/allowed_prefixes`，新增统一字段。
5. 修改 `list_models`，保留 `providers`，新增统一字段。
6. 增加或扩展单测，断言旧字段和新字段同时存在。
7. 运行 `go test ./internal/sidecar/orch/tools -count=1`。
8. 按审查结果修复，再评分并登记剩余缺口。

## 非目标

本轮不直接修改以下工具的返回类型：

- `list_agents`
- `workspace_list_runs`
- `prompt_list`
- `command_list`

原因：它们当前直接返回数组，需要单独兼容迁移设计。
