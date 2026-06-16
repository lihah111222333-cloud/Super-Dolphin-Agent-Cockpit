# M6 机器审查实施

## 审查结论

| 审查项 | 结果 | 说明 |
| --- | --- | --- |
| 默认兼容 | 通过 | 不传 `envelope` 时仍返回旧数组 |
| envelope 输出 | 通过 | 四个直接数组工具均支持 `envelope=true` |
| schema 可发现性 | 通过 | `list_agents`, `workspace_list_runs`, `prompt_list`, `command_list` 均暴露 `envelope` |
| hint 可用性 | 通过 | 每个 envelope 输出都给出下一步工具 |
| 测试覆盖 | 通过 | 默认数组路径和 envelope 路径均有测试 |

## 审查发现与修复

| 编号 | 发现 | 处理 |
| --- | --- | --- |
| M6-FIX-001 | 新增 prompt 测试使用了不存在的 `mustRawJSON` helper | 改为直接使用 `json.RawMessage` |
| M6-FIX-002 | 首次测试因测试 helper 名称错误构建失败 | 修复后 `go test ./internal/sidecar/orch/tools -count=1` 通过 |

## 验证结果

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/sidecar/orch/tools -count=1` | 通过 |
| `go test ./cmd/mcp-orch/... -count=1` | 未全绿，仍失败于既有非 tools 用例 |

全量失败项仍为：

- `TestMcpOrchSidecarRuntimeConsumesPackagedParentContract`
- `TestMcpOrchSidecarRuntimeDevParentIgnoresResidualPackagedEnv`
- `TestAutomationExecutor_Happy`

失败范围未扩大，M6 修改范围内的 tools 包通过。
