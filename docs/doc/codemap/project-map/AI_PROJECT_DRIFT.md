# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4802
>
> 未细分职责文件：14

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 14 |
| 未细分职责占比 | 0.29% |
| 最大未细分职责占比阈值 | 5.00% |

## 2. 漂移告警

- 无

## 3. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `cmd` | 12 |
| `internal` | 2 |

## 4. 样例文件

- `cmd/codex-worktree-setup/atomic_unix.go`
- `cmd/codex-worktree-setup/atomic_windows.go`
- `cmd/codex-worktree-setup/main.go`
- `cmd/codex-worktree-setup/setup.go`
- `cmd/codex-worktree-setup/setup_config.go`
- `cmd/codex-worktree-setup/setup_paths.go`
- `cmd/codex-worktree-setup/setup_probe.go`
- `cmd/codex-worktree-setup/setup_test.go`
- `cmd/codex-worktree-setup/worktree_integration_test.go`
- `cmd/mcp-schema-compiler-helper/main.go`
- `cmd/super-dolphin-guard/main.go`
- `cmd/super-dolphin-guard/main_test.go`
- `internal/e2e/rpc_runtime/doc_test.go`
- `internal/e2e/rpc_runtime/runtime_e2e_test.go`

## 5. 修复方式

优先在 `.ai-project-map.overrides.json` 中补充 `purpose_rules_append`，或用 `--rules` 传入显式规则文件，然后重新运行：

```bash
node scripts/generate_ai_project_map.mjs
```
