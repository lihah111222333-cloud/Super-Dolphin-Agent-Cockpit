# M5 机器审查实施

## 审查结论

| 审查项 | 结果 | 说明 |
| --- | --- | --- |
| 旧字段兼容 | 通过 | `dags/runs/files/providers` 均保留 |
| 统一字段新增 | 通过 | 已新增 `data/total/showing/truncated/hint` |
| hint 可用性 | 通过 | hint 均指向下一步工具或下一步用法 |
| 测试覆盖 | 通过 | 覆盖 `task_list_dags`, `task_list_runs`, `shared_file_list`, `list_models` |
| 范围控制 | 通过 | 未强改直接数组型工具 |

## 审查发现与修复

| 编号 | 发现 | 处理 |
| --- | --- | --- |
| M5-FIX-001 | 新增测试桩命名与既有 `stubSharedFileStore` 冲突 | 已改名为 `stubSharedFileListStore` |
| M5-FIX-002 | 首次测试因测试桩冲突构建失败 | 修复后 `go test ./internal/sidecar/orch/tools -count=1` 通过 |

## 验证结果

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/sidecar/orch/tools -count=1` | 通过 |
| `go test ./cmd/mcp-orch/... -count=1` | 未全绿，仍失败于既有非 tools 用例 |

全量失败项：

- `TestMcpOrchSidecarRuntimeConsumesPackagedParentContract`
- `TestMcpOrchSidecarRuntimeDevParentIgnoresResidualPackagedEnv`
- `TestAutomationExecutor_Happy`

这些失败项在 M5 修改范围之外，且 tools 包已单独通过。
