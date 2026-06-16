# P23 合规保障 Gate 体系

> 创建时间：2026-04-25 | 状态：**未实施（gate 设计文档，需 P0 启动前先合 L1+L3+L4 骨架）**
> authoritative：**本文件是 P23 gate / archtest / CI / hard-soft 阶段唯一权威**；[`README.md`](README.md) 只引用本文件，不维护第二清单；stub 只引用 gate key
> 设计输入：2026-04-25 `compliance-gate-design` agent 报告 + `contract-compliance-master` 报告

## 现有 gate 盘点

| 层 | 现状 | 关键 file |
|---|---|---|
| archtest | 已有 runtime ownership / 依赖方向 / sqlc 边界等多档守卫；**当前 `go test ./internal/archtest/... -count=1` build failed**，失败点为 `internal/module/uistate/timeline/projector_parity.go:12:2` unused import `pkglogger`，Phase0 gate PR 必须先恢复 archtest 可编译 | `internal/archtest/fx_invoke_guard_test.go:25-82`、`lifecycle_onstart_guard_test.go:13-33,93-169`、`bus_callback_guard_test.go:15-40,48-58` |
| 本地 gate | `make test` 走 `scripts/test_with_guard.sh` 跑全仓 + code size + `TestCodeSizeGuard` | `Makefile:81-84`、`scripts/test_with_guard.sh:42-49,85-90` |
| 本地 guard | `make guard` 只跑轻量 guard（**不**等于全 archtest） | `Makefile:120-124` |
| CI | **无** `.github/workflows/*` | — |
| pre-commit / husky | **无** | — |
| merge gate | 文档化 H/O/M 三签（P22）；**无**自动 GitHub branch protection | `docs/plans/迁移/p22/README.md:240-246` |
| metrics / alert | counter 声明已在；**无** promhttp / exporter / alert 链路 | `internal/platform/metrics/metrics.go:1-7,15-51` |

## 七层 gate 体系（P23 设计）

| 层 | 名称 | P23 设计 | 实施成本 | 防御能力 | 优先级 |
|---|---|---|---|---|---|
| L1 | schema / IDE | trigger enum、launch 必填、sqlc 生成；缺参 fail-fast（README §默认值安全原则） | 1 工程日 | 拦字段类违规 | ⭐ 必做 |
| L2 | pre-commit | git hooks 跑 `make guard` + grep spawn / 裸 INSERT / 直接 LLM 调用 | 0.5 工程日 | 拦低级绕过 | 推荐 |
| L3 | archtest（本地） | `go test ./internal/archtest/...` 必须 PASS；P23 21 项 archtest 分阶段落地 | 2-3 工程日 | 拦结构违规 | ⭐ 必做 |
| L4 | CI 门禁 | 仓库当前无 `.github/workflows/` 且仓库内尚无 PR template；目标是新建 `p23-gates.yml` 跑 `make guard` + archtest + `make ci-l1` + integration；Phase0 gate PR 必须新增 PR template 或等价可核验机制 | 1 工程日 | merge 前硬拦 | ⭐ 必做 |
| L5 | merge gate | P22 H/O/M 三签机制扩展到 P23；分类 hard / soft 见下表 | 0.5 工程日/周 | 拦设计漂移 | 推荐 |
| L6 | runtime alert | 当前只有 metrics counter 声明、无 promhttp/exporter，且无 executable scheduled-audit artifact；P7 前必须创建 promhttp `/metrics` exporter 或脚本/命令卡 artifact + 统一日志告警 sink | 2 工程日 | 拦运行态违规 | P7 前 hard；P0 可 fallback |
| L7 | scheduled audit | daily / weekly / monthly cron 跑 archtest 趋势 + migration 编号 + 契约漂移 | 1 工程日 | 拦慢性漂移 | 可选 |

**实施顺序建议**：L1 + L3 + L4 必须在 P0 合入前就位（合规底座）；L2 + L5 在 P0 期间补；L6 + L7 可在后段（P7+）开工前补。

### Phase0 gate PR 最小可执行闭环（D gate skeleton）

Phase0 gate PR 是 P23 的 D gate 可执行闭环，不实现完整 DAG runtime，但必须让 gate 自身可运行、可失败、可复核。最小顺序如下：

1. **先恢复 archtest 可编译**：当前 `go test ./internal/archtest/... -count=1` 因 `internal/module/uistate/timeline/projector_parity.go:12:2` unused import `pkglogger` build failed；这是 Phase0 hard blocker。本文只记录事实，不在本任务修代码。
2. **收紧 mcp-orch allowlist**：当前 `internal/archtest/dependency_direction_mcp_orch_test.go` 仍宽泛放行 `internal/store` / `internal/module`；Phase0 PR 必须真实修改该测试到 P22 modularity 名录口径，否则 P0 runtime PR blocked。
3. **落 P23 archtest skeleton**：先提交 21 个测试函数的最小 skeleton / TODO-fail 或 fixture-fail 版本，落点按下表 `落点` 列；每个 skeleton 必须能被 `go test ./internal/archtest/... -count=1` 发现，不能只写文档。
4. **落 manual fallback 载体**：仓库内尚无 PR template；Phase0 gate PR 必须新增 PR template 或等价可核验机制，明确 commit SHA、命令输出、reviewer 签收字段。未新增前不得声称 manual fallback 已闭环。
5. **落 migration / metrics artifact 决策**：migration sequence guard 至少能检查已占编号；L6 若不建 promhttp，则必须创建 executable scheduled-audit 脚本或命令卡 artifact，写明命令、输入源、输出 artifact、退出码、owner。

`hard_from=Phase0 gate PR` 表示 Phase0 PR 本身必须 hard fail；`soft_until=pre-Phase0 only` 只允许本文档修订阶段记录风险，不允许 P0 runtime PR 继续 soft。P0 runtime PR 在 Phase0 全部 green 前 blocked。

## P23 14 子任务 × 7 层 gate 矩阵

H = hard fail / S = soft warn / R = record only / `H/O` = hard 且需 O 工位安全审

| 子任务 | L1 | L2 | L3 | L4 | L5 | L6 | L7 |
|---|---|---|---|---|---|---|---|
| P0 runtime skeleton | H | H | H | H | H | S | H |
| P1 dispatcher / launcher | H | H | H | H | H | H | S |
| P2 reconcile hook | H | H | H | H | H | H | S |
| P3 start / owner | H | S | H | H | H | S | H |
| P4 host trigger | H | S | S | H | S | S | R |
| P5 cron trigger | H | S | H | H | H | H | H |
| P6 external RPC | H | H | H | H | H/O | H | H |
| P7 liveness | H | S | H | H | H | H | H |
| P8 verify gate | H | H | H | H | H/O | H | H |
| P9 scale | H | S | H | H | H | H | H |
| P10 template / UI | H | S | S | H | M | S | R |
| P11 dynamic growth | H | H | H | H | H/O | H | H |
| P12 swarm | H | H | H | H | H/O | H | H |
| P13 strict JSON | H | S | H | H | H/O | H | H |

`H/O` 强制 O 工位（安全 / 权限 / 信任域）签收的子任务：P6（外部 RPC AuthN）/ P8（LLM 调用）/ P11（spawn budget）/ P12（swarm + LLM）/ P13（金融场景输出）。

## 21 项 archtest 单一 authoritative 表

本表是 P23 archtest 唯一权威清单，也是 hard / soft 阶段唯一执行口径；README 只允许摘要引用 gate key，stub 只允许引用 gate key，不维护第二清单。当前事实：下列 P23 专属 archtest 均**当前未实施**，必须按 `hard_from` 阶段逐步落地；已存在的 P22 runtime ownership 类 guard 只能作为基础，不等同于本表闭环。

| # | archtest key | 测试函数 | 落点 | existing/planned | owner_subtask | introduced_by | hard_from | soft_until / pre-hard 行为 | 守的违规 |
|---|---|---|---|---|---|---|---|---|---|
| 1 | `dag_watcher_no_lifecycle_loop` | `TestDAGNoLifecycleLoop` | `internal/archtest/dag_runtime_test.go` | planned（当前未实施） | P0 | phase0/P22 ownership 扩展 | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard | DAG 长循环不在 lifecycle / bus callback |
| 2 | `dag_runner_actors_present` | `TestDAGRunnerActorsPresent` | `internal/archtest/dag_runtime_test.go` | planned（当前未实施） | P0 | phase0/P22 ownership 扩展 | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard；缺 actor inventory 不得合 P0 | P0 4 actor + P7/P8/P11/P12/P13 后续 actor 都进 `group:"runners"` |
| 3 | `dag_actor_no_fire_and_forget` | `TestDAGActorNoFireAndForget` | `internal/archtest/dag_runtime_test.go` | planned（当前未实施） | P0 | phase0/P22 ownership 扩展 | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard；发现裸 goroutine 必须改设计 | actor `Run(ctx)` 禁 fire-and-forget goroutine / 不受 ctx 管控 ticker / 未 Stop ticker；允许受 ctx 管控且 stop/drain 明确的 ticker |
| 4 | `dag_status_cas_only` | `TestDAGStatusCASOnly` | `internal/archtest/dag_state_test.go` | planned（当前未实施） | P0 | phase0/state-machine | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard；裸写必须列入 blocker | status 写必带 expected prev status CAS |
| 5 | `dag_trigger_enum_only` | `TestDAGTriggerEnumFailFast` | `internal/archtest/dag_schema_test.go` | planned（当前未实施） | P3 | phase0/trigger enum | P3 前 | P0-P2 soft；P3 PR hard | trigger 字段 schema 必须是 enum |
| 6 | `dag_external_rpc_guard` | `TestDAGExternalIdentityAdaptersPresent` + `TestDAGServiceAuthZEnforced` | `internal/archtest/dag_external_rpc_test.go` | planned（当前未实施） | P6 | security/a4 | P6 前 | P0-P5 soft；P6 PR hard + O 签 | 三入口注入 caller identity，service 层执行 AuthN/AuthZ/tenant/rate/quota/audit/idempotency guard |
| 7 | `dag_launcher_shared_path_only` | `TestDAGLauncherSharedPathOnly` + `TestDAGVerifyJobsDurable` + `TestDAGVerifyJobClaimRetryDeadLetter` | `internal/archtest/dag_launcher_test.go` / `internal/archtest/dag_verify_test.go` | planned（当前未实施） | P1 / P8 | phase0 + verify/a2 | P1 前（shared path）；P8 前（verify durable） | 未到 P8 时 verify 子项 record-only；shared launcher P1 hard | dispatcher / verifier launch 都经共享 launcher；P8 verify job 必须 durable claim/retry/dead-letter |
| 8 | `dag_hook_tap_enqueue_only` | `TestDAGHookTapEnqueueOnly` | `internal/archtest/dag_hook_test.go` | planned（当前未实施） | P2 / P13 | phase0 + output/a5 | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard；P2/P13 代码触碰 hook 时继续 hard | hook callback 禁重 DB / launch / LLM；P13 只 bounded parse + enqueue |
| 9 | `dag_llm_light_boundary` | `TestDAGLLMLightBoundary` | `internal/archtest/dag_llm_boundary_test.go` | planned（当前未实施） | P8 | impl-quality | P8 前 | P0-P7 record-only；P8 PR hard | P8/P12 调 LLM 必须经 light 层，禁裸 provider |
| 10 | `dag_growth_spawn_only` | `TestDAGSpawnChildOnlyViaService` | `internal/archtest/dag_growth_test.go` | planned（当前未实施） | P11 | growth/a3 | P11 前 | P0-P10 soft scan；P11 PR hard + O 签 | 禁绕过 `SpawnChildNodes` 直接 INSERT node |
| 11 | `dag_swarm_quota_only` | `TestDAGSwarmUsesTokenBucket` | `internal/archtest/dag_swarm_test.go` | planned（当前未实施） | P12 | swarm/cost | P12 前 | P0-P11 record-only；P12 PR hard + O 签 | P12 swarm 共用 P9 token bucket |
| 12 | `dag_output_validate_before_verify` | `TestDAGOutputValidationBeforeVerify` | `internal/archtest/dag_output_validation_test.go` | planned（当前未实施） | P13 | output/a5 | P13 前 | P0-P12 record-only；P13 PR hard + O 签 | P13 schema validate 早于 P8 verify |
| 13 | `dag_template_no_cmd_import` | `TestDAGTemplateNoCmdImport` | `internal/archtest/dag_template_boundary_test.go` | planned（当前未实施） | P10 | template boundary | P10 前 | P0-P9 soft; P10 PR hard | UI/template 不反向 import cmd concrete |
| 14 | `dag_migration_sequence_guard` | `TestDAGMigrationNumbersNoConflict` | `internal/archtest/dag_migration_test.go` | planned（当前未实施） | phase0 / all migration owners | migration/a5 | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard；each migration PR must paste HEAD recalibration | 当前 HEAD 下一个可用编号起排；禁止占用既有 0063/0064；no-tx concurrent index 分拆 |
| 15 | `cron_dag_bridge_no_concrete_orch_import` | `TestDAGCronBridgeNoOrchImport` | `internal/archtest/dag_cron_bridge_test.go` | planned（当前未实施） | P5 | cron boundary | P5 前 | P0-P4 soft；P5 PR hard | cron 模块与 mcp-orch 不得 concrete import |
| 16 | `dag_audit_append_only` | `TestDAGAuditAppendOnly` | `internal/archtest/dag_audit_append_only_test.go` | planned（当前未实施） | P6 / P8 / P12 / P13 | security/a4 | P6 前（audit base）；P8+ touched tables hard | P0-P5 soft；new audit table PR hard | audit/arbiter/swarm/output_validation append-only + hash-chain |
| 17 | `dag_terminal_turn_fence` | `TestDAGTerminalTurnFence` + `TestDAGVerifierTerminalUsesVerifyTurnFence` | `internal/archtest/dag_turn_fence_test.go` / `internal/archtest/dag_verify_test.go` | planned（当前未实施） | P2 / P8 | data/a5 | Phase0 gate PR for active_turn; P8 前 for verify_turn | active_turn skeleton Phase0 hard；verify_turn 子项 record-only until P8 | terminal 写必须校验 active_turn_id；verifier terminal 必须校验 verify_turn_id |
| 18 | `dag_no_status_naked_write` | `TestDAGNoStatusNakedWrite` | `internal/archtest/dag_state_test.go` | planned（当前未实施） | P0 | data/a5 | Phase0 gate PR | pre-Phase0 only；Phase0 PR hard；任何新增裸写阻塞 | 禁裸 status UPDATE |
| 19 | `dag_tenant_filter_required` | `TestDAGTenantFilterRequired` | `internal/archtest/dag_tenant_filter_test.go` | planned（当前未实施） | P3 / P6 / P10 | security/a4 | P6 前 | P0-P5 soft；P6/P10 PR hard | DAG/template/audit 查询 RPC 必带 tenant filter |
| 20 | `dag_pii_redaction_present` | `TestDAGPIIRedactionPresent` | `internal/archtest/dag_pii_redaction_test.go` | planned（当前未实施） | P8 / P13 | security/a4 | P8 前 | P0-P7 record-only；P8/P13 PR hard | repair/error/arbiter/validation 落库前统一 redactor |
| 21 | `dag_mcp_orch_dependency_allowlist_tight` | `TestMCPOrchDependencyDirection` | `internal/archtest/dependency_direction_mcp_orch_test.go` | planned（当前未实施；代码当前仍宽泛放行 `internal/store` / `internal/module`） | phase0 / P0 | contract/a1 | Phase0 gate PR | pre-Phase0 only；Phase0 PR 必须真实修改测试，P0 runtime PR blocked until green | cmd/mcp-orch allowlist 不得宽泛放行未登记 internal 包 |

## CI workflow 设计（L4）

目标 CI（当前仓库无 `.github/workflows/`，以下为待新建示例）`.github/workflows/p23-gates.yml`：

```yaml
name: P23 Gates
on: [pull_request]
jobs:
  archtest:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: make guard
      - run: go test ./internal/archtest/... -count=1 -race
      - run: make ci-l1
      - run: make sqlc-verify
      - run: go test ./cmd/mcp-orch/... -tags integration
```

> 当前仓库无 `.github/workflows/`，且仓库内尚无 PR template；本期需要先建 CI，Go 版本必须使用 `go-version-file: go.mod`（或与 `go.mod` 完全一致），不得在示例里硬写固定版本。若 Phase0 不能立即建 CI，Phase0 gate PR 必须新增 PR template 或等价可核验机制：固定字段逐项粘贴 `git rev-parse HEAD`、`make guard`、`go test ./internal/archtest/... -count=1`、`make sqlc-verify`、必要 integration 命令的完整输出；reviewer 必须在同一 PR 留下 `P23-manual-gate: verified` 并核对命令时间戳/commit SHA。缺载体、缺输出、SHA 不匹配、或 reviewer 未签收均禁止 merge。CI 仍必须在 P7 之前补齐。

## runtime alert 触发条件（L6）

| metric | 阈值 | 路径 | 子任务 |
|---|---|---|---|
| `dag_spawn_budget_usage_ratio` | > 80% warn / >= 100% hard | promhttp gauge 或 scheduled-audit artifact | P11 |
| `archtest_failure_rate` | scheduled audit 连续 2 次失败 | scheduled-audit artifact；Phase0 前无 artifact 则不能声称 L7 已闭环 | 全 |
| `spawn_bypass_attempt_total` | > 0（任何绕过 SpawnChildNodes 的尝试） | audit log + alert | P11 |
| `audit_log_write_fail_total` | > 0 | strict/financial fail-closed；dev/read 可 durable spool + alert | P6 / P8 / P12 / P13 |
| `dag_observe_lost_total` | 连续两个采样窗口上升且非 planned maintenance | promhttp counter 或 scheduled-audit artifact；首动作查 launcher/agent_status/lease | P0 / P2 |
| `dag_verdict_lost_total` | 连续两个采样窗口上升，或单 DAG 超过 verify budget | promhttp counter 或 scheduled-audit artifact；首动作查 arbiter/LLM/token bucket | P8 / P12 |
| `dag_output_validation_fail_rate` | > 30% | log + warn；financial preset 可升 hard | P13 |
| `dag_hook_consumer_lag_seconds` | p99 over 10min > 5s | promhttp histogram 或 scheduled-audit artifact | P2 / P9 |
| `dag_launcher_queue_lag_seconds` + `dag_launcher_queue_depth` | depth > quota×3；lag p99 > 30s | promhttp gauge + histogram 或 scheduled-audit artifact | P9 |
| `dag_node_transition_total{from,to,result}` | `result=unexpected` 任意计数 > 0 | promhttp counter 或 structured audit row | P0 / P2 |
| `dag_actor_last_heartbeat_timestamp_seconds` | actor 连续 2 轮无 heartbeat | promhttp gauge + lifecycle log event `dag_actor_heartbeat` | 全 actor |

> 当前仓库 `internal/platform/metrics/metrics.go` 只有 counter 声明，没有 promhttp exporter / `/metrics` 暴露面，也没有 executable scheduled-audit artifact。P0 合入前必须明确 promhttp PR 或本地 scheduled-audit fallback；fallback 必须以脚本或命令卡 artifact 落仓，声明固定命令、输入源（DB/structured log）、输出 artifact、运行频率、失败退出码、owner。P7 开工前 L6 必须具备 promhttp exporter 或上述可执行 fallback，否则 P7–P13 freeze。

### actor heartbeat / lifecycle 全局契约（L6）

覆盖 actor：P0 `dagWatcherActor` / `dagDispatcherActor` / `dagLeaseActor` / `dagReconcileActor`，P7 `dagActivityActor`，P8 `dagArbiterActor`，P11 `dagConvergenceActor`，P12 `dagSwarmArbiterActor`，P13 `outputValidationActor`。

- **labels**：所有 metrics 至少带 `actor`、`dag_key`（无 DAG 时为 `none`）、`tenant_id`（未知为 `unknown`）、`owner_subtask`、`result`；禁止高基数 `prompt` / `error_detail` / raw agent output label。
- **heartbeat fields**：`actor`、`instance_id`、`lease_owner`、`last_heartbeat_at`、`iteration_seq`、`inflight_count`、`queue_depth`、`last_error_class`、`drain_state`、`build_sha`。
- **cadence**：active actor 每 30s±20% jitter 写 heartbeat；idle actor 至少每 60s 写 heartbeat；shutdown drain deadline 内每 10s 写 drain heartbeat；连续 2 个 cadence miss 触发 L6 alert。
- **lifecycle events**：每个 actor 必须结构化记录 `actor_start`、`actor_ready`、`actor_iteration_begin`、`actor_iteration_end`、`actor_error`、`actor_backoff`、`actor_drain_start`、`actor_drain_done`、`actor_stop`；事件带 `trace_id` / `run_id` / `dag_key` / `node_key`（不适用填 `none`）。
- **Phase0 skeleton**：Phase0 先落 metric/event name 常量与 archtest skeleton，P0 runtime PR 才接真实 actor；P0 PR blocked until heartbeat/lifecycle skeleton green。

## archtest 补充项（历史记录，已并入上方 21 项 authoritative 表）

以下只保留来源追溯，不再作为第二张执行清单；执行以“21 项 archtest 单一 authoritative 表”为准。

| archtest | 守的内容 | 点名者 |
|---|---|---|
| `dag_audit_append_only` | `dag_audit_log` / `dag_arbiter_calls` / `dag_swarm_consensus` / `dag_output_validations` 禁 UPDATE/DELETE；append-only + hash chain | a4 + a5 + a6 |
| `dag_terminal_turn_fence` | `CompleteTaskDagNode` 必校 active_turn_id；late completed 不可覆 aborted | a5 + a2 |
| `dag_no_status_naked_write` | 禁裸 status 写；所有 status 写带 `WHERE current_status = $expected` | a5 |
| `dag_tenant_filter_required` | DAG/template/audit 查询 RPC 必带 `tenant_id` filter | a4 critical |
| `dag_pii_redaction_present` | `repair_prompt` / `error_detail` / arbiter input/output 走统一 redactor | a4 + P13 |
| `dag_mcp_orch_dependency_allowlist_tight` | 收紧 `dependency_direction_mcp_orch_test.go` 的 cmd/mcp-orch allowlist；不得用宽泛 `internal/store` / `internal/module` 绕过 P22 modularity 名录 | a1 ❌ |

### archtest 分阶段 hard 策略（交叉验证裁决 12）

| 阶段 | hard fail 集合 | 说明 |
|---|---|---|
| Phase0 gate PR | `dag_status_cas_only` / `dag_no_status_naked_write` / `dag_terminal_turn_fence` / `dag_hook_tap_enqueue_only` / `dag_migration_sequence_guard` / `dag_mcp_orch_dependency_allowlist_tight` / `dag_actor_no_fire_and_forget` / `dag_runner_actors_present` 最小 skeleton | Phase0 PR hard；P0 runtime PR blocked until green。 |
| P6 前 | 上述 + `dag_external_rpc_guard` / `dag_tenant_filter_required` / `dag_audit_append_only` / `dag_pii_redaction_present` | 外部 RPC 和多租户开放前必须安全闭环。 |
| P9 前 | 全部 21 项 | 大规模、LLM、UI 后段开工前全部转 hard；不再存在中间缓冲号/允许缺号口径，migration guard 以 PR 当时 HEAD 重新校准为准。 |


## on-call runbook 五类（a6 调研 → 本表）

| 场景 | 信号 | 首动作 | 升级路径 |
|---|---|---|---|
| hook 滞后 | `dag_hook_consumer_lag_seconds` p99>5s | 查 worker pool 额度 / DB latency / 考虑临时增容 | dag-runtime owner |
| launcher 积压 | `dag_launcher_queue_lag_seconds` p99>30s 或 `dag_launcher_queue_depth`>quota×3 | 查 token bucket / verifier+swarm 占比 / 必要时调低 P9 quota；若 token bucket 自身故障，立刻降级固定并发 | dag-runtime owner + cost owner |
| observe_lost 上升 | `dag_observe_lost_total` 趋势 | 查 launcher 失败路径、agent_status lookup、lease | dag-runtime owner |
| spawn budget 触顶 | `dag_spawn_budget_usage_ratio`>=1 或 `spawn_bypass_attempt_total`>0 | 临时冻 P11；核 audit log；查 archtest fail | growth owner + sec owner |
| audit alert 失败 | `audit_log_write_fail_total`>0 | strict env hard fail；非 strict 可临时 log-only 但必须开阻塞修复项，不能静默放行 merge | sec owner + sre |

> a6 明示：4 个 P0 actor + hook pool + cron bridge + LLM in-flight 的 graceful shutdown 顺序仍未定；owner 启动前需冻。

### metrics manifest / exporter fallback（交叉验证仲裁）

P0 前必须冻结指标 manifest：每个 `dag_*` 指标写明 type、unit、labels、source actor、cardinality、PromQL 示例。当前无 promhttp/exporter，也无 executable scheduled-audit artifact；若 promhttp/exporter 未落地，scheduled-audit fallback 必须以脚本或命令卡 artifact 落仓，声明输入源（DB/structured log）、输出 artifact、运行频率、失败退出码与 owner；P7 前没有 `/metrics` 且无可执行 fallback artifact，则 P7–P13 freeze。

### graceful shutdown / drain 顺序（交叉验证仲裁）

P0 前冻结 drain protocol：停止接新 start/spawn → 停 watcher claim → hook terminal 继续 durable insert → drain dispatcher/reconcile/outputValidation/arbiter/swarm 到 deadline → 持久化 in-flight launch/LLM/audit job → 释放或缩短 lease。每个 actor 必测 SIGTERM injection，并暴露 `dag_actor_shutdown_inflight_total`、`dag_actor_drain_duration_seconds`、`dag_actor_drain_dropped_total`。

## cost approval gate（第五轮仲裁）

P9/P10/P11/P12/P13 共用中央成本阻断口径：若 `dag/cost_preview` 判定超过 tenant/subscription/月度阈值、provider capacity verdict 为 `不足/阻断`、LLM spend budget exhausted、或高风险 preset（金融 swarm / 大规模 growth）未填写 ROI/approval 元数据，则 Start / Spawn / Swarm / Strict JSON financial preset 必须 hard block。UI 二次确认不能替代 service 层 approval guard；所有 override 必须写 audit。

## scheduled audit（L7）

### Daily

- 跑全套 archtest（`go test ./internal/archtest/...`）
- 扫 migration 编号是否冲突 / 倒序
- 扫直接 INSERT / spawn 绕过尝试
- dag_hook_consumer_lag_seconds / dag_launcher_queue_lag_seconds 历史趋势检查

### Weekly

- archtest 失败率趋势
- p99 lag 趋势
- spawn budget 命中率
- H/O/M 待签 PR 清单
- P7-P13 状态列是否漂移（`verify_phase` / `growth_phase` / `last_activity_at` 是否仍正确独立）

### Monthly

- 契约漂移审计：`modularity-convention.md` / `fx-convention.md` / `rungroup-convention.md` 文件版本 vs 代码符合度
- 重点：`fx.Invoke` 仍只工厂；`run.Group` actor 仍无 fire-and-forget；模块归属未漂移

## P23 owner 每日 / 每周自查清单

### Daily（PR 提交前 ≤ 5 项）

1. `make guard` PASS
2. `go test ./internal/archtest/... -count=1` PASS（当前因 `projector_parity.go` unused import build failed；Phase0 hard blocker）
3. 确认无 callback 长跑（`hook_consumer.go` / bus subscriber 内）
4. 确认 schema fail-fast（缺参不静默 default）
5. 贴 migration / SQLC 校验结果（`make sqlc-verify`）

### Weekly（合规审查会议输入）

- 哪些 archtest 趋势变差
- 哪些 metric 异常
- 哪些跨子任务依赖未对齐
- H/O/M 待签 PR
- migration 编号是否需要重排（特别是后段 P7-P13 编号陆续合入时）

### Monthly（契约漂移审计）

- contract 文件版本 vs 代码符合度
- archtest allowlist 演化（如新增包是否合规进入）

## 整体合规风险评级：**高**

依据 `contract-compliance-master` 报告 + `compliance-gate-design` 报告。**三条最关键守门动作**（按优先级）：

1. **先恢复 archtest build + 落 P23 archtest skeleton + migration sequence guard**（L3）—— `go test ./internal/archtest/...` 当前 build failed，Phase0 必须先变绿，不让 P0 之后再补规则
2. **修正 P3 / P5 / P8 落点漂移**（已落 README + stub 修正）—— `StartDAG` 留 mcp-orch / cron 走 bridge / `llm/light` 不放顶层 `internal/`
3. **P8 / P9 / P12 / P13 共用 quota + sanitize + output-before-verify 三联门**（L4 merge hard gate）

## 触发 stop / 重构条件

应叫停或重构 P23 的观测信号：

1. **archtest**：P23 hard archtest 任一主干红；或同一 PR 引入 > 2 个 ownership / 依赖方向违规
2. **hook**：`dag_hook_consumer_lag_seconds` p99 持续 > 5s，或 bounded queue drop 非 0 且无显式 backpressure
3. **launcher**：queue depth 持续 > 全局 quota × 3，且 verifier / swarm 占比 > 50%
4. **DB**：`dag_wakeup_age_seconds` p99 或 pending → running p99 连续超 SLO；CAS conflict rate 异常上升
5. **merge gate**：后段 PR 被 status enum / 直接 INSERT / no authn gate 拒绝率 > 30%，说明计划切片边界错误

任一信号触发，进入「P23 stop & restructure review」流程。

### MIGRATION_OWNER_CHECKLIST（需求补全仲裁）

每个 P23 migration owner 必须按 PR 当时 HEAD 的下一个可用编号重新校准，并在 PR 描述或同目录 stub 中填写：preflight SQL、forward migration、是否 forward-only、rollback/roll-forward 修复路径、backfill/lock 评估、是否含 `CREATE INDEX CONCURRENTLY` no-transaction 文件、`internal/sidecar/orch/sqlc.yaml` schema entry、`make sqlc-verify` 输出、tenant/audit/redaction 影响、postcheck SQL。缺任一项不得 merge 对应 migration PR。

### 阶段 0 / P0 拆分 checklist（需求补全仲裁）

P0 runtime PR 之前必须先合最小 Phase0 gate PR：修复当前 archtest build failed、migration sequence guard（禁止复用已占用编号并要求 PR 前重新校准）、真实收紧 `dependency_direction_mcp_orch_test.go` 中对 `internal/store` / `internal/module` 的宽泛放行、CAS / terminal fence / 21 项 P23 archtest skeleton、CI 或 manual hard fallback 载体（仓库内尚无 PR template，必须新增模板或等价可核验机制）、metrics exporter 或 executable scheduled-audit artifact 决策、P0 DDL exact schema、StartDAG 最小入口归属。Phase0 不实现完整 watcher；P0 才落 runtime skeleton，且 P0 runtime PR blocked until Phase0 green。
