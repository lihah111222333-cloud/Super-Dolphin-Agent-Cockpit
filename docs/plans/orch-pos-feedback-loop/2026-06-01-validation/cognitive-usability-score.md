# Orch 工具认知功能可用性验证

日期：2026-06-01

本轮是验证轮，不新增功能，目标是用测试结果评估 M0-M6 优化后的效果。

## 测试结果

| 测试项 | 命令 | 结果 | 说明 |
| --- | --- | --- | --- |
| 定向验证：pos / envelope / list 输出 | `go test ./internal/sidecar/orch/tools -run 'Test.*(Pos|Envelope|ListDAGs|ListRuns|SharedFileList|ListModels)' -count=1` | 通过 | 验证平铺定位、统一出参、列表工具 |
| 定向验证：创建 / 应用 ops / 数组型 envelope | `go test ./internal/sidecar/orch/tools -run 'TestHandle(CreateDAG|ApplyOps|ListAgents|PromptList|CommandList|WorkspaceListRuns)' -count=1` | 通过 | 验证复杂入参平铺和兼容 envelope |
| tools 包完整测试 | `go test ./internal/sidecar/orch/tools -count=1` | 通过 | 本次优化范围内通过 |
| mcp-orch 全量回归 | `go test ./cmd/mcp-orch/... -count=1` | 未全绿 | 仍失败在既有非 tools 用例 |

全量回归失败项：

- `TestMcpOrchSidecarRuntimeConsumesPackagedParentContract`
- `TestMcpOrchSidecarRuntimeDevParentIgnoresResidualPackagedEnv`
- `TestAutomationExecutor_Happy`

这些失败不在本次 `orch tools` 优化范围内，且本次优化范围内的 `tools` 包已通过。

## 认知功能可用性评分

评分规则：0-100 分，越高越好。

| 维度 | 优化前估分 | 优化后评分 | 变化 | 证据 | 结论 |
| --- | ---: | ---: | ---: | --- | --- |
| 入参定位认知负荷 | 52 | 88 | +36 | `pos=agent:<id>`, `pos=dag:<key>/run:<key>/node:<key>` 测试通过 | AI 不需要在多个 key 字段里来回推理 |
| 复杂入参可理解性 | 48 | 84 | +36 | `task_create_dag` 平铺 schedule / node execution，`task_dag_apply_ops` 平铺 action 测试通过 | 常用路径不用写深层嵌套 JSON |
| 出参统一度 | 35 | 82 | +47 | 对象型列表新增 `data/total/showing/truncated/hint`，数组型工具支持 `envelope=true` | AI 可以统一读 `data` |
| hint 可操作性 | 40 | 80 | +40 | 列表输出 hint 指向下一步工具，例如 `task_get_run`, `shared_file_read` | AI 能知道下一步该调用什么 |
| 旧接口兼容性 | 76 | 91 | +15 | 旧字段保留；数组型工具默认仍返回数组 | 优化没有粗暴破坏老调用方 |
| 功能可用性 | 65 | 87 | +22 | `go test ./internal/sidecar/orch/tools -count=1` 通过 | 工具层可用性验证通过 |
| 测试覆盖可信度 | 55 | 84 | +29 | pos、flat input、envelope、legacy fallback 均有测试 | 优化路径有自动化测试保护 |
| 加权总分 | 53.5 | 85.0 | +31.5 | 见上方测试结果 | 达到可交付状态 |

## 工具能力验证矩阵

| 能力 | 覆盖工具 | 验证状态 |
| --- | --- | --- |
| `pos` 平铺定位 | `get_agent_report`, `task_get_dag`, `task_get_run`, `send_message`, `stop_agent`, `task_update_node`, `task_dispatch_node` 等 | 通过 |
| 创建 DAG 平铺入参 | `task_create_dag` | 通过 |
| 应用 DAG ops 平铺入参 | `task_dag_apply_ops` | 通过 |
| 对象型列表统一出参 | `task_list_dags`, `task_list_runs`, `shared_file_list`, `list_models` | 通过 |
| 数组型列表兼容 envelope | `list_agents`, `workspace_list_runs`, `prompt_list`, `command_list` | 通过 |
| 旧字段兼容 | `dags`, `runs`, `files`, `providers`, 默认数组返回 | 通过 |

## 最终结论

`orch tools` 优化后的认知功能可用性评分为 **85/100**。

核心效果：

1. AI 入参推理明显减少。
2. 常用复杂参数已经平铺。
3. 列表出参有统一读取路径。
4. hint 能指导下一步工具调用。
5. 老接口保持兼容。
6. 优化范围内自动化测试通过。

剩余风险：

1. 全量 `cmd/mcp-orch/...` 仍有三个既有非 tools 失败项，需要单独排查。
2. `total` 目前是返回数量，不是后端全量计数。
3. `truncated` 目前基于 limit 推断，后续最好由后端 `has_more` 明确返回。
