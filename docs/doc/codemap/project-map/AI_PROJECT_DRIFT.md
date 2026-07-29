# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4244
>
> 未细分职责文件：81

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 81 |
| 未细分职责占比 | 1.91% |
| 最大未细分职责占比阈值 | 5.00% |

## 2. 漂移告警

- 无

## 3. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `cmd` | 79 |
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
- `cmd/super-dolphin-gate-executor/main.go`
- `cmd/super-dolphin-gate-executor/main_test.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_container.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_controller.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_controller_protocol_test.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_docker_test.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_execution.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_root.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_runner.go`
- `cmd/super-dolphin-gate/coordinator_bootstrap_test.go`
- `cmd/super-dolphin-gate/coordinator_cli.go`
- `cmd/super-dolphin-gate/coordinator_cli_wait_test.go`
- `cmd/super-dolphin-gate/coordinator_container_exit_test.go`
- `cmd/super-dolphin-gate/coordinator_deferred_test.go`
- `cmd/super-dolphin-gate/coordinator_logs_test.go`
- `cmd/super-dolphin-gate/coordinator_owner.go`
- `cmd/super-dolphin-gate/coordinator_owner_deadline_test.go`
- `cmd/super-dolphin-gate/coordinator_plan_test.go`
- `cmd/super-dolphin-gate/coordinator_production.go`
- `cmd/super-dolphin-gate/coordinator_production_config.go`
- `cmd/super-dolphin-gate/coordinator_production_fixture_test.go`
- `cmd/super-dolphin-gate/coordinator_production_promotion_test.go`
- `cmd/super-dolphin-gate/coordinator_production_test.go`
- `cmd/super-dolphin-gate/coordinator_promotion.go`
- `cmd/super-dolphin-gate/coordinator_provision.go`
- `cmd/super-dolphin-gate/coordinator_provision_docker_test.go`
- `cmd/super-dolphin-gate/coordinator_provision_e2e_optin_test.go`
- `cmd/super-dolphin-gate/coordinator_provision_failure_logs_test.go`
- `cmd/super-dolphin-gate/coordinator_provision_helpers_test.go`
- `cmd/super-dolphin-gate/coordinator_provision_publish_darwin.go`
- `cmd/super-dolphin-gate/coordinator_provision_publish_unsupported.go`
- `cmd/super-dolphin-gate/coordinator_provision_recovery.go`
- `cmd/super-dolphin-gate/coordinator_provision_recovery_test.go`
- `cmd/super-dolphin-gate/coordinator_provision_release_docker_test.go`
- `cmd/super-dolphin-gate/coordinator_provision_test.go`
- `cmd/super-dolphin-gate/coordinator_receipt_fixture_test.go`
- `cmd/super-dolphin-gate/coordinator_recovery.go`
- `cmd/super-dolphin-gate/coordinator_recovery_plan_test.go`
- `cmd/super-dolphin-gate/coordinator_recovery_shards.go`

## 5. 修复方式

优先在 `.ai-project-map.overrides.json` 中补充 `purpose_rules_append`，或用 `--rules` 传入显式规则文件，然后重新运行：

```bash
node scripts/generate_ai_project_map.mjs
```
