# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4819
>
> 未细分职责文件：77

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 77 |
| 未细分职责占比 | 1.60% |
| 最大未细分职责占比阈值 | 5.00% |

## 2. 漂移告警

- 无

## 3. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `cmd` | 75 |
| `internal` | 2 |

## 4. 样例文件

- `cmd/acp-node/main.go`
- `cmd/acp-node/main_test.go`
- `cmd/build-trusted-gate-launcher/main.go`
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
- `cmd/super-dolphin-gate/capcontract_cli.go`
- `cmd/super-dolphin-gate/capcontract_cli_test.go`
- `cmd/super-dolphin-gate/cli_common.go`
- `cmd/super-dolphin-gate/closure_cli.go`
- `cmd/super-dolphin-gate/closure_cli_test.go`
- `cmd/super-dolphin-gate/codemap_cli.go`
- `cmd/super-dolphin-gate/frontend_code_size_cli.go`
- `cmd/super-dolphin-gate/frontend_code_size_cli_test.go`
- `cmd/super-dolphin-gate/launcher_cli.go`
- `cmd/super-dolphin-gate/launcher_cli_test.go`
- `cmd/super-dolphin-gate/main.go`
- `cmd/super-dolphin-gate/main_remote_baseline_source_gate.go`
- `cmd/super-dolphin-gate/main_test.go`
- `cmd/super-dolphin-gate/project_map_cli.go`
- `cmd/super-dolphin-gate/project_map_cli_test.go`
- `cmd/super-dolphin-gate/project_map_cli_test_helpers_test.go`
- `cmd/super-dolphin-gate/remote_agent_token.go`
- `cmd/super-dolphin-gate/remote_agent_token_test.go`
- `cmd/super-dolphin-gate/remote_baseline_source.go`
- `cmd/super-dolphin-gate/remote_baseline_state_store.go`
- `cmd/super-dolphin-gate/remote_hook_test.go`
- `cmd/super-dolphin-gate/remote_hook_test_helpers_test.go`
- `cmd/super-dolphin-gate/remote_ledger_init.go`
- `cmd/super-dolphin-gate/remote_ledger_init_test.go`
- `cmd/super-dolphin-gate/remote_manifest_install.go`
- `cmd/super-dolphin-gate/remote_manifest_install_dispatch_test.go`
- `cmd/super-dolphin-gate/remote_manifest_install_test.go`
- `cmd/super-dolphin-gate/remote_materialize.go`
- `cmd/super-dolphin-gate/remote_materialize_test.go`
- `cmd/super-dolphin-gate/remote_oci_project_cache_test.go`
- `cmd/super-dolphin-gate/remote_progress_test.go`
- `cmd/super-dolphin-gate/remote_provision_generation_one.go`
- `cmd/super-dolphin-gate/remote_provision_generation_one_test.go`
- `cmd/super-dolphin-gate/remote_registry_credential.go`
- `cmd/super-dolphin-gate/remote_registry_credential_test.go`
- `cmd/super-dolphin-gate/remote_run.go`
- `cmd/super-dolphin-gate/remote_run_automation_test.go`

## 5. 修复方式

优先在 `.ai-project-map.overrides.json` 中补充 `purpose_rules_append`，或用 `--rules` 传入显式规则文件，然后重新运行：

```bash
node scripts/generate_ai_project_map.mjs
```
