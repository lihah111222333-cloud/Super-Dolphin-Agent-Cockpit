# P22.1 E1 R2 裁决与修复落盘（2026-04-25）

> 本文件按 §10.31 新增；不修改 `JUDGEMENT.md` 历史 §1-§9 / §R2 / §R3 / §R4。
> 任务范围：收 J R4 + X1-X4 R2 报告，独立 LSP 裁决真实性；只对 TRUE_ACTIONABLE 文档项落盘补修。

## §R5.1 收报状态（J R4 + X1-X4 R2 共 5 份）

| Agent | state | report? | 纳入? | 备注 |
|---|---|---:|---:|---|
| J R4 `agent-1777050952162-1777050952161337000` | unavailable/空 | no | 部分纳入 | `orchestration_get_agent_report` 首次与 60s 后重试均空；但 `JUDGEMENT.md §R4` 已落盘，作为历史文档输入读取，不盲信 agent report |
| X1 R2 | idle | yes | yes | FINDINGS 自洽全绿；无新 ACTIONABLE |
| X2 R2 | idle | yes | yes | DAG header 统一引用不足为 TRUE_ACTIONABLE |
| X3 R2 | idle | yes | yes | Phase 0 implementation spec / P1A 边界为 TRUE_ACTIONABLE；22-34 人日为 TRUE_BUT_DEFERRED 容量风险 |
| X4 R2 | idle | yes | yes | Gate AST 改名绕过与 SessionRuntime path/symbol 为 TRUE_ACTIONABLE |

## §R5.2 裁决分类表

| 来源 | 新报项 | 分类 | E1 裁决 |
|---|---|---|---|
| X1 | F-1 反向测试证据是否充分 | DUPLICATE | R1 已修，LSP 读 `runner_test.go:50-99` 证明确实锁定旧错序，无需再修 |
| X1 | F-3/F-5/F-9 overlay 正交性 | DUPLICATE | R1 已修；X1 判充分，无新项 |
| X2 | README 死锚点 / §10.41-43 | DUPLICATE | 已修，grep 旧死锚点为 0，README line 24 命中三节 |
| X2 | DAG header 未显式列 R8_QA §R10.6 / R8_QC §7 / §10.30 | TRUE_ACTIONABLE | 已补 DAG 顶部归属声明 |
| X2 | §10.31 全历史保留 | DUPLICATE | 未发现整段删除，无需修 |
| X3 | Phase 0 不可直接派 Go 实施，缺 implementation spec | TRUE_ACTIONABLE | 已补 `DAG.md §2.2 Phase 0 implementation spec` |
| X3 | P1A 边界声明不足 | TRUE_ACTIONABLE | 已在 DAG node 与 F→Node 表声明：只修 root bridge，不声称未迁移 worker 已受 run.Group 托管 |
| X3 | 6×6 / §10.13 | DUPLICATE | 已修，X3 判 CLOSED |
| X3 | X3 22-34 人日估算未解释 | TRUE_BUT_DEFERRED | 已在 DAG 估算段记录为容量风险；不覆盖 W3 12.5-20 主基线，若 custom walker 超范围再改用 22-34 |
| X4 | `TestRunnerActorGuard/ownership` 可落地性 | DUPLICATE | X4 判方向可落地；仅需补算法细节 |
| X4 | AST 改名绕过不足 / 不得只靠 token grep | TRUE_ACTIONABLE | 已补 custom go/ast walker + one-hop resolver + Start/Run/Begin/Serve/Loop/Watch regex 兜底 |
| X4 | SessionRuntime allowlist path/symbol 漂移 | TRUE_ACTIONABLE | 已改为 `internal/provider/codexapp/session_runtime.go` 与 `(*SessionRuntime).Start/spawnReader/runHealthLoop/runRecoveryWorker` |
| X4 | `runner.actors` deferred | DUPLICATE | 已修，X4 判通过 |
| J §R4 | README R4 状态漂移 | DUPLICATE | `README.md` 已有 R4 BLOCK drift note |
| J §R4 | DAG Phase 0 spec / F→Node / P1A | TRUE_ACTIONABLE | 与 X3 合并处理 |
| J §R4 | GATE active name / custom walker / SessionRuntime symbol | TRUE_ACTIONABLE | 与 X4 合并处理 |
| J report | agent report 空 | FALSE_OVERTURN | 不能以空 report 代表无 R4；HEAD `JUDGEMENT.md §R4` 已存在，故只标 report unavailable，不推翻落盘 R4 |

统计：TRUE_ACTIONABLE 6 组；TRUE_BUT_DEFERRED 1；FALSE_OVERTURN 1；DUPLICATE 8。

## §R5.3 独立 LSP 核 ≥3 争议点

| 争议点 | X/J 结论 | LSP 实测 | 仲裁 |
|---|---|---|---|
| F-1 测试是否反向锁错 | X1：`TestBindRuntimeDrainsExtractionBeforeCancel` 锁定先 drain 后 cancel | `internal/app/runner_test.go:79-88` 先等 `drainer.started`，若 `runner.canceled` 先发生则 fatal；`BindRuntime` refs=5、incoming=4 | 采纳 X1；R1 FINDINGS 已充分，DUPLICATE |
| `TestRunnerActorGuard/ownership` 是否可落地 | X4：现有 `TestRunnerActorGuard` 有子测试结构但无 ownership matcher | `runner_actor_guard_test.go:35-104` 有多个 `t.Run` 与 skeleton skip，无 module lifecycle Start/Stop matcher | 采纳 X4；方向可落地，算法细节 TRUE_ACTIONABLE |
| Phase 0 是否可直接派 Go 实施 | X3/J：BusModule/RunnerModule HEAD 仅骨架，需 implementation spec | `internal/platform/bus/module.go:10-30` 只 provide dispatcher/emitters/log sink；`internal/platform/runner/module.go:5` 为空 `fx.Module("runner")` | 采纳 X3/J；补 DAG §2.2 后仍建议先单 owner Phase 0 |
| SessionRuntime 样例是否 HEAD 可定位 | X4：旧 path/symbol 漂移 | `lsp_grep func (r *SessionRuntime)` 命中 `internal/provider/codexapp/session_runtime.go` 的 `Start/runHealthLoop/runRecoveryWorker/spawnReader`；旧 `internal/codexapp/session_runtime.go` 在 `internal` 0 命中 | 采纳 X4；已修 GATE 样例 |

## §R5.4 修订清单（TRUE_ACTIONABLE file:line + before/after）

| # | 文档 | file:line | before | after |
|---:|---|---|---|---|
| 1 | DAG | `DAG.md:3` | header 仅泛称 P22 R10 FINAL deferred / §10.30 | 明确来源同其它 P22.1 主体：R8_QA §R10.6、R8_QC §7、§10.30 |
| 2 | DAG | `DAG.md:23-24` | P1A 只写 root bridge shutdown ordering | 增：仅修 root bridge 顺序，不声称未迁移 worker 已受 run.Group 托管 |
| 3 | DAG | `DAG.md:56-67` | Phase 0 只有 P0A/P0B/P0C 名称与 warning→fail | 新增 Phase 0 implementation spec：`bus.subscribers`、Subscriber spec、Bus Stop、Runner adapter、P0C TODO allowlist、warning-mode 行为 |
| 4 | DAG | `DAG.md:69-83` | 无 F-1~F-11 → node 显式映射 | 新增 11 行映射表，含边界说明 |
| 5 | DAG | `DAG.md:177` | 只保留 W3 12.5-20 | 增 X3 22-34 为 TRUE_BUT_DEFERRED 容量风险与触发条件 |
| 6 | GATE | `GATE_CONTRACTS.md:19` | `TestRunnerActorOwnership` 看似 active 顶层名 | 标注“概念名；落地名 `TestRunnerActorGuard/ownership`” |
| 7 | GATE | `GATE_CONTRACTS.md:46` | regex 兜底只覆盖 `.Start(` | 增 custom walker + one-hop resolver；兜底覆盖 Start/Run/Begin/Serve/Loop/Watch |
| 8 | GATE | `GATE_CONTRACTS.md:162-165` | `internal/codexapp/session_runtime.go` + `SessionRuntime.Run/startReaderLoop/startHealthLoop` | `internal/provider/codexapp/session_runtime.go` + `(*SessionRuntime).Start/spawnReader/runHealthLoop/runRecoveryWorker` |

## §R5.5 §10.31 合规自查（历史不变 SHA256 证明）

- 新增本文件 `R2_CORRECTIONS.md`，不改 `JUDGEMENT.md`。
- `JUDGEMENT.md` §1-§9 + §R2 + §R3 + §R4 当前行 1-386 SHA256：`8dc1a2a3d8b801975a9697c82b5ada4c5cf6c9b9ca0146229cc13edc7d1a9cce`。
- 本轮只修改 `.md`：`DAG.md`、`GATE_CONTRACTS.md`、新增 `R2_CORRECTIONS.md`；未改代码/JSON/SQL/go.mod/go.sum。
- 历史估算、旧 R4 drift note、旧 finding 均保留；新内容以 HEAD 修订/补修表追加。

## §R5.6 LSP 自证

- `lsp_file(read_file)`：读取 X/J 相关文档、`runner_test.go`、`runner_actor_guard_test.go`、`bus/module.go`、`runner/module.go`、P22.1 主体。
- `lsp_grep(text_search)`：核旧 SessionRuntime path/symbol 为 0、新 Phase 0 spec/F→Node 命中、`§R4` 存在、`t.Skip`/`DefinitionPath` 等。
- `lsp_xref(references)` ≥3：`BindRuntime` refs=5；`SessionRuntime.Start` refs=14；`ResilientSubscribe` refs 抽样=20。
- `lsp_xref(call_hierarchy)` ≥2：`BindRuntime` incoming=4；`RunGroup` incoming=4。
- `lsp_structure`：用于确认 guard/test 文档与 root allowlist schema（见执行报告）。
- `lsp_inspect` / `lsp_completion`：辅助定位定义与 LSP 上下文。
- `lsp_file(diagnostics)`：最终对 P22.1 文档跑 diagnostics，结果见执行报告。
