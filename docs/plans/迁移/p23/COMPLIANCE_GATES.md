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
| L3 | archtest（本地） | `go test ./internal/archtest/...` 必须 PASS；P23 14 项 archtest 完整落地 | 2-3 工程日 | 拦结构违规 | ⭐ 必做 |
| L4 | CI 门禁 | 新建 `.github/workflows/p23-gates.yml`：`make guard` + archtest + `make ci-l1` + integration | 1 工程日 | merge 前硬拦 | ⭐ 必做 |
| L5 | merge gate | P22 H/O/M 三签机制扩展到 P23；分类 hard / soft 见下表 | 0.5 工程日/周 | 拦设计漂移 | 推荐 |
| L6 | runtime alert | 增 DAG metrics + promhttp exporter + 统一日志告警 sink | 2 工程日 | 拦运行态违规 | 可选 |
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

## 14 项 archtest 落点详情

详见 [`README.md`](README.md) §"守卫与 archtest"。每项 archtest 应包含：

- **测试函数名**：`TestDAG<X>`
- **测试什么**：明确的违规模式（grep / AST / SQL pattern）
- **失败信号**：人类可读的 error message
- **生命周期**：永久 / P23 完成后可降级 / 临时

### 必加 archtest 的具体函数名建议

| 测试函数 | 落点 | 守的违规 |
|---|---|---|
| `TestDAGRunnerActorsPresent` | `internal/archtest/dag_runtime_test.go` | 编译期 assert `runner.actors` 切片包含 P0 4 actor + P7+P8+P12 后续 actor |
| `TestDAGNoLifecycleLoop` | `internal/archtest/dag_runtime_test.go` | grep 禁 `OnStart -> go` / `OnStart -> ticker` 形态出现在 DAG 相关包 |
| `TestDAGHookTapEnqueueOnly` | `internal/archtest/dag_hook_test.go` | grep `hook_consumer.go` 的 callback 内禁 `lsp.LaunchAgent` / `db.Query` / `light.Complete` 等重 token；P13 schema validate 例外白名单 |
| `TestDAGStatusCASOnly` | `internal/archtest/dag_state_test.go` | 扫 `cmd/mcp-orch/sql/queries/task_dag_node_*.sql`，所有 `UPDATE task_dag_node ... SET status` 必须带 `WHERE current_status = $expected` |
| `TestDAGTriggerEnumFailFast` | `internal/archtest/dag_schema_test.go` | trigger 字段 schema 必须是 enum 校验，缺失返 `ErrInvalidTrigger` |
| `TestDAGMigrationNumbersNoConflict` | `internal/archtest/dag_migration_test.go` | 扫 `migrations/0063_*.sql` 到 `0071_*.sql`，验证编号无缺号、无依赖倒序（参考 README §阶段 0 ①） |
| `TestDAGLauncherSharedPathOnly` | `internal/archtest/dag_launcher_test.go` | dispatcher / verifier launch 都必须经 `service_launcher_bridge.go:54-64`，禁绕路 |
| `TestDAGExternalRPCAuthnMiddleware` | `internal/archtest/dag_rpc_test.go` | `cmd/mcp-orch/orchestration/rpc.go` 的 `task/*` method 必须经 `WithCallerIdentity` middleware |
| `TestDAGSpawnChildOnlyViaService` | `internal/archtest/dag_growth_test.go` | grep 全仓 `INSERT INTO task_dag_node`，只允许出现在 `CreateDAG` / `SpawnChildNodes` 对应 SQL；其它位置 hard fail |
| `TestDAGVerifierUsesSharedQuota` | `internal/archtest/dag_verify_test.go` | P8 verifier launch 必须占用同一 launcher quota（`maxConcurrentLaunches`），不允许独立 quota |
| `TestDAGSwarmUsesTokenBucket` | `internal/archtest/dag_swarm_test.go` | P12 swarm 必须经 P9 全局 token bucket，禁裸 `go llmCall(...)` |
| `TestDAGOutputValidationBeforeVerify` | `internal/archtest/dag_output_validation_test.go` | P13 schema validate 必须早于 P8 verify_phase 推进；扫 hook tap 调用顺序 |
| `TestDAGTemplateNoCmdImport` | `internal/archtest/dag_template_boundary_test.go` | UI / template 包不允许 import `cmd/mcp-orch/` concrete |
| `TestDAGCronBridgeNoOrchImport` | `internal/archtest/dag_cron_bridge_test.go` | `internal/module/cron/*.go` 禁 import `cmd/mcp-orch/`（已有 `dependency_direction_mcp_orch_test.go:49-53` 防护，本测试在 P5 引入 `TriggerSink` 后专项加强） |
| `TestDAGLLMLightBoundary` | `internal/archtest/dag_llm_boundary_test.go` | P8 / P12 调 LLM 必须经 `cmd/mcp-orch/orchestration/llm/light/*`，禁裸 import provider |

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

> 当前仓库无 `.github/workflows/`，本期需要先建。如不能立即建 CI，退路：PR 模板强制贴本地 `make guard` + archtest 输出，merge gate 走人工 review；但这是软约束，必须在 P7 之前补 CI。

## runtime alert 触发条件（L6）

| metric | 阈值 | 路径 | 子任务 |
|---|---|---|---|
| `hook_consumer_lag_p99` | > 5s 持续 10 分钟 | Prometheus histogram + log alert | P2 / P9 |
| `launcher_queue_lag_p99` | > 30s 或持续上升 | 同上 | P9 |
| `spawn_budget_usage` | > 80% warn / >= 100% hard | log + alert | P11 |
| `archtest_failure_rate` | scheduled audit 连续 2 次失败 | 自动 issue 创建 | 全 |
| `spawn_bypass_attempt_total` | > 0（任何绕过 SpawnChildNodes 的尝试） | audit log + alert | P11 |
| `audit_log_write_fail_total` | > 0 | log + alert（P6 要求） | P6 |
| `dag_observe_lost_total` | 持续上升趋势 | log + alert | P0 / P2 |
| `dag_verdict_lost_total` | 持续上升 | log + alert | P8 |
| `dag_output_validation_fail_rate` | > 30% | log + warn | P13 |
| `dag_hook_consumer_lag_seconds` | p99 §10min > 5s | promhttp histogram（a6） | P2 / P9 |
| `dag_launcher_queue_lag_seconds` + `_depth` | depth > quota×3；lag p99 > 30s | promhttp gauge + histogram（a6） | P9 |
| `dag_node_transition_total{from,to,result}` | result=unexpected 任意计数 > 0 | promhttp counter（a6） | P0 / P2 |
| `dag_observe_lost_total` / `dag_verdict_lost_total` | 连续上升趋势 | promhttp counter（a6） | P0 / P2 / P8 |
| `dag_actor_heartbeat` | actor 连续 2 轮无 lifecycle log | 结构化 log（a6） | 全 actor |

> 当前仓库 `internal/platform/metrics/metrics.go` 只有 counter 声明，没有 promhttp exporter。L6 实施前必须先补 promhttp 路径或这些 metric 暂降级为 log only + scheduled audit 检查。

## archtest 补充项（8 路 → 10 路调研合并后）

README §"守卫与 archtest" 表中的 14 项上补五项为 19（来源：a4 安全 / a5 数据一致性 / a7 UX 间接 / a8 依赖 / a9 rollout / a10 成本）：

| archtest | 守的内容 | 点名者 |
|---|---|---|
| `dag_audit_append_only` | `dag_audit_log` / `dag_arbiter_calls` / `dag_output_validations` 禁 UPDATE/DELETE；append-only + hash chain | a4 + a5 + a6 |
| `dag_terminal_turn_fence` | `CompleteTaskDagNode` 必校 active_turn_id；late completed 不可覆 aborted | a5 + a2 |
| `dag_no_status_naked_write` | 禁裸 status 写；所有 status 写带 `WHERE current_status = $expected` | a5 |
| `dag_tenant_filter_required` | DAG/template/audit 查询 RPC 必带 `tenant_id` filter | a4 critical |
| `dag_pii_redaction_present` | `repair_prompt` / `error_detail` / arbiter input/output 走统一 redactor | a4 + P13 |

## on-call runbook 五类（a6 调研 → 本表）

| 场景 | 信号 | 首动作 | 升级路径 |
|---|---|---|---|
| hook 滞后 | `dag_hook_consumer_lag_seconds` p99>5s | 查 worker pool 额外 / DB latency / 考虑临时增容 | dag-runtime owner |
| launcher 积压 | `dag_launcher_queue_lag_seconds` p99>30s 或 depth>quota×3 | 查 token bucket / verifier+swarm 占比 / 可能 说 P9 quota 调低 | dag-runtime owner + cost owner |
| observe_lost 上升 | `dag_observe_lost_total` 趋势 | 查 launcher 失败路径、agent_status lookup、lease | dag-runtime owner |
| spawn budget 触顶 | `spawn_budget_usage`>=100% 或 `spawn_bypass_attempt_total`>0 | 临时冻 P11；核 audit log；查 archtest fail | growth owner + sec owner |
| audit alert 失败 | `audit_log_write_fail_total`>0 | 临时以 log only fallback；不能推迟 merge；仅 strict env 为 hard fail | sec owner + sre |

> a6 明示：4 个 P0 actor + hook pool + cron bridge + LLM in-flight 的 graceful shutdown 顺序仍未定；owner 启动前需冻。

## scheduled audit（L7）

### Daily

- 跑全套 archtest（`go test ./internal/archtest/...`）
- 扫 migration 编号是否冲突 / 倒序
- 扫直接 INSERT / spawn 绕过尝试
- hook_consumer_lag / launcher_queue_lag 历史趋势检查

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
2. **hook**：`hook_consumer_lag` p99 持续 > 5s，或 bounded queue drop 非 0 且无显式 backpressure
3. **launcher**：queue depth 持续 > 全局 quota × 3，且 verifier / swarm 占比 > 50%
4. **DB**：`wakeup_age_p99` 或 pending → running p99 连续超 SLO；CAS conflict rate 异常上升
5. **merge gate**：后段 PR 被 status enum / 直接 INSERT / no authn gate 拒绝率 > 30%，说明计划切片边界错误

任一信号触发，进入「P23 stop & restructure review」流程。
