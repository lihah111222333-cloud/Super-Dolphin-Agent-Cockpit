# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4581
>
> 未细分职责文件：12

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 12 |
| 未细分职责占比 | 0.26% |
| 最大未细分职责占比阈值 | 5.00% |

## 2. 漂移告警

- 无

## 3. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `cmd` | 9 |
| `internal` | 2 |
| `sql` | 1 |

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
- `internal/e2e/rpc_runtime/doc_test.go`
- `internal/e2e/rpc_runtime/runtime_e2e_test.go`
- `sql/schema/prompt_intent_drafts.sql`

## 5. 修复方式

优先在 `.ai-project-map.overrides.json` 中补充 `purpose_rules_append`，或用 `--rules` 传入显式规则文件，然后重新运行：

```bash
node scripts/generate_ai_project_map.mjs
```
