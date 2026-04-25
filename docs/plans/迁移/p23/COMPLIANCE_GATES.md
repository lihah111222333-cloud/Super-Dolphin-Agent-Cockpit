# P23 合规保障 Gate 体系

> 创建时间：2026-04-25 | 状态：**未实施（gate 设计文档，需 P0 启动前先合 L1+L3+L4 骨架）**
> authoritative：本文件 + [`README.md`](README.md) §"守卫与 archtest" + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 9
> 设计输入：2026-04-25 `compliance-gate-design` agent 报告 + `contract-compliance-master` 报告

## 现有 gate 盘点

| 层 | 现状 | 关键 file |
|---|---|---|
| archtest | 已有 runtime ownership / 依赖方向 / sqlc 边界等多档守卫 | `internal/archtest/fx_invoke_guard_test.go:25-82`、`lifecycle_onstart_guard_test.go:13-33,93-169`、`bus_callback_guard_test.go:15-40,48-58` |
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
| L4 | CI 门禁 | 新建 `.github/workflows/p23-gates.yml`：`make guard` + archtest + `make ci-l1` + integration | 1 工程日 | merge 前硬拦 | ⭐ 必做 |
| L5 | merge gate | P22 H/O/M 三签机制扩展到 P23；分类 hard / soft 见下表 | 0.5 工程日/周 | 拦设计漂移 | 推荐 |
| L6 | runtime alert | 增 DAG metrics + promhttp exporter + 统一日志告警 sink | 2 工程日 | 拦运行态违规 | P7 前 hard；P0 可 fallback |
| L7 | scheduled audit | daily / weekly / monthly cron 跑 archtest 趋势 + migration 编号 + 契约漂移 | 1 工程日 | 拦慢性漂移 | 可选 |

**实施顺序建议**：L1 + L3 + L4 必须在 P0 合入前就位（合规底座）；L2 + L5 在 P0 期间补；L6 + L7 可在后段（P7+）开工前补。

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

本表是 P23 archtest 唯一权威清单；README 只摘要展示。每行包含 archtest key 与具体 Go 测试函数名，函数名变更必须同步本表。

| # | archtest key | 测试函数 | 落点 | 守的违规 |
|---|---|---|---|---|
| 1 | `dag_watcher_no_lifecycle_loop` | `TestDAGNoLifecycleLoop` | `internal/archtest/dag_runtime_test.go` | DAG 长循环不在 lifecycle / bus callback |
| 2 | `dag_runner_actors_present` | `TestDAGRunnerActorsPresent` | `internal/archtest/dag_runtime_test.go` | P0 4 actor + P7/P8/P11/P12/P13 后续 actor 都进 `group:"runners"` |
| 3 | `dag_actor_no_fire_and_forget` | `TestDAGActorNoFireAndForget` | `internal/archtest/dag_runtime_test.go` | actor `Run(ctx)` 禁裸 goroutine / ticker |
| 4 | `dag_status_cas_only` | `TestDAGStatusCASOnly` | `internal/archtest/dag_state_test.go` | status 写必带 expected prev status CAS |
| 5 | `dag_trigger_enum_only` | `TestDAGTriggerEnumFailFast` | `internal/archtest/dag_schema_test.go` | trigger 字段 schema 必须是 enum |
| 6 | `dag_external_rpc_guard` | `TestDAGExternalIdentityAdaptersPresent` + `TestDAGServiceAuthZEnforced` | `internal/archtest/dag_external_rpc_test.go` | 三入口注入 caller identity，service 层执行 AuthN/AuthZ/tenant/rate/quota/audit/idempotency guard |
| 7 | `dag_launcher_shared_path_only` | `TestDAGLauncherSharedPathOnly` + `TestDAGVerifyJobsDurable` + `TestDAGVerifyJobClaimRetryDeadLetter` | `internal/archtest/dag_launcher_test.go` / `internal/archtest/dag_verify_test.go` | dispatcher / verifier launch 都经共享 launcher；P8 verify job 必须 durable claim/retry/dead-letter |
| 8 | `dag_hook_tap_enqueue_only` | `TestDAGHookTapEnqueueOnly` | `internal/archtest/dag_hook_test.go` | hook callback 禁重 DB / launch / LLM；P13 只 bounded parse + enqueue |
| 9 | `dag_llm_light_boundary` | `TestDAGLLMLightBoundary` | `internal/archtest/dag_llm_boundary_test.go` | P8/P12 调 LLM 必须经 light 层，禁裸 provider |
| 10 | `dag_growth_spawn_only` | `TestDAGSpawnChildOnlyViaService` | `internal/archtest/dag_growth_test.go` | 禁绕过 `SpawnChildNodes` 直接 INSERT node |
| 11 | `dag_swarm_quota_only` | `TestDAGSwarmUsesTokenBucket` | `internal/archtest/dag_swarm_test.go` | P12 swarm 共用 P9 token bucket |
| 12 | `dag_output_validate_before_verify` | `TestDAGOutputValidationBeforeVerify` | `internal/archtest/dag_output_validation_test.go` | P13 schema validate 早于 P8 verify |
| 13 | `dag_template_no_cmd_import` | `TestDAGTemplateNoCmdImport` | `internal/archtest/dag_template_boundary_test.go` | UI/template 不反向 import cmd concrete |
| 14 | `dag_migration_sequence_guard` | `TestDAGMigrationNumbersNoConflict` | `internal/archtest/dag_migration_test.go` | 0063–0072 编号、0064 unused、no-tx concurrent index 分拆 |
| 15 | `cron_dag_bridge_no_concrete_orch_import` | `TestDAGCronBridgeNoOrchImport` | `internal/archtest/dag_cron_bridge_test.go` | cron 模块与 mcp-orch 不得 concrete import |
| 16 | `dag_audit_append_only` | `TestDAGAuditAppendOnly` | `internal/archtest/dag_audit_append_only_test.go` | audit/arbiter/swarm/output_validation append-only + hash-chain |
| 17 | `dag_terminal_turn_fence` | `TestDAGTerminalTurnFence` + `TestDAGVerifierTerminalUsesVerifyTurnFence` | `internal/archtest/dag_turn_fence_test.go` / `internal/archtest/dag_verify_test.go` | terminal 写必须校验 active_turn_id；verifier terminal 必须校验 verify_turn_id |
| 18 | `dag_no_status_naked_write` | `TestDAGNoStatusNakedWrite` | `internal/archtest/dag_state_test.go` | 禁裸 status UPDATE |
| 19 | `dag_tenant_filter_required` | `TestDAGTenantFilterRequired` | `internal/archtest/dag_tenant_filter_test.go` | DAG/template/audit 查询 RPC 必带 tenant filter |
| 20 | `dag_pii_redaction_present` | `TestDAGPIIRedactionPresent` | `internal/archtest/dag_pii_redaction_test.go` | repair/error/arbiter/validation 落库前统一 redactor |
| 21 | `dag_mcp_orch_dependency_allowlist_tight` | `TestMCPOrchDependencyDirection` | `internal/archtest/dependency_direction_mcp_orch_test.go` | cmd/mcp-orch allowlist 不得宽泛放行未登记 internal 包 |

## CI workflow 设计（L4）

新建 `.github/workflows/p23-gates.yml`：

```yaml
name: P23 Gates
on: [pull_request]
jobs:
  archtest:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: make guard
      - run: go test ./internal/archtest/... -count=1 -race
      - run: make ci-l1
      - run: make sqlc-verify
      - run: go test ./cmd/mcp-orch/... -tags integration
```

> 当前仓库无 `.github/workflows/`，本期需要先建。如不能立即建 CI，退路只能是 **manual hard fallback**：PR 模板强制贴 `make guard`、`go test ./internal/archtest/... -count=1`、`make sqlc-verify` 输出，指定 reviewer 签收；缺任一项禁止 merge。CI 仍必须在 P7 之前补齐。

## runtime alert 触发条件（L6）

| metric | 阈值 | 路径 | 子任务 |
|---|---|---|---|
| `dag_spawn_budget_usage_ratio` | > 80% warn / >= 100% hard | log + alert | P11 |
| `archtest_failure_rate` | scheduled audit 连续 2 次失败 | 自动 issue 创建 | 全 |
| `spawn_bypass_attempt_total` | > 0（任何绕过 SpawnChildNodes 的尝试） | audit log + alert | P11 |
| `audit_log_write_fail_total` | > 0 | log + alert（P6 要求） | P6 |
| `dag_observe_lost_total` | 持续上升趋势 | log + alert | P0 / P2 |
| `dag_verdict_lost_total` | 持续上升 | log + alert | P8 |
| `dag_output_validation_fail_rate` | > 30% | log + warn | P13 |
| `dag_hook_consumer_lag_seconds` | p99 over 10min > 5s | promhttp histogram（a6） | P2 / P9 |
| `dag_launcher_queue_lag_seconds` + `dag_launcher_queue_depth` | depth > quota×3；lag p99 > 30s | promhttp gauge + histogram（a6） | P9 |
| `dag_node_transition_total{from,to,result}` | result=unexpected 任意计数 > 0 | promhttp counter（a6） | P0 / P2 |
| `dag_observe_lost_total` / `dag_verdict_lost_total` | 连续上升趋势 | promhttp counter（a6） | P0 / P2 / P8 |
| `dag_actor_last_heartbeat_timestamp_seconds` | actor 连续 2 轮无 heartbeat | promhttp gauge + 结构化 log event `dag_actor_heartbeat`（a6） | 全 actor |

> 当前仓库 `internal/platform/metrics/metrics.go` 只有 counter 声明，没有 promhttp exporter。P0 合入前必须明确 promhttp PR 或本地 scheduled-audit fallback；P7 开工前 L6 必须具备 exporter，否则 P7–P13 中依赖 runtime alert 的 hard gate 降级为 merge blocker。

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
| P0 前 | `dag_status_cas_only` / `dag_no_status_naked_write` / `dag_terminal_turn_fence` / `dag_hook_tap_enqueue_only` / `dag_migration_sequence_guard` / `dag_mcp_orch_dependency_allowlist_tight` | 先拦最容易污染状态机和契约的实现缺口。 |
| P6 前 | 上述 + `dag_external_rpc_guard` / `dag_tenant_filter_required` / `dag_audit_append_only` / `dag_pii_redaction_present` | 外部 RPC 和多租户开放前必须安全闭环。 |
| P9 前 | 全部 21 项 | 大规模、LLM、UI 后段开工前全部转 hard；0064 是唯一允许缺号。 |


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

P0 前必须冻结指标 manifest：每个 `dag_*` 指标写明 type、unit、labels、source actor、cardinality、PromQL 示例。若 promhttp/exporter 未落地，scheduled-audit fallback 必须是可执行脚本/命令，声明输入源（DB/structured log）、输出 artifact、运行频率、失败退出码与 owner；P7 前没有 `/metrics` 且无可执行 fallback，则 P7–P13 freeze。

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
2. `go test ./internal/archtest/... -count=1` PASS
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

1. **先落 P23 archtest 骨架 + migration sequence guard**（L3）—— 不让 P0 之后再补规则
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

每个 `0063–0072` migration owner 必须在 PR 描述或同目录 stub 中填写：preflight SQL、forward migration、是否 forward-only、rollback/roll-forward 修复路径、backfill/lock 评估、是否含 `CREATE INDEX CONCURRENTLY` no-transaction 文件、`cmd/mcp-orch/sqlc.yaml` schema entry、`make sqlc-verify` 输出、tenant/audit/redaction 影响、postcheck SQL。缺任一项不得 merge 对应 migration PR。

### 阶段 0 / P0 拆分 checklist（需求补全仲裁）

P0 runtime PR 之前必须先合最小阶段 0 checklist：migration sequence guard、0064 hard-fail 规则、P22 allowlist tighten、CAS / terminal fence archtest skeleton、CI 或 manual hard fallback 冻结、metrics exporter/fallback 决策、P0 DDL exact schema、StartDAG 最小入口归属。阶段 0 不实现完整 watcher；P0 才落 runtime skeleton。
