# P22.1 子任务 DAG + 并行度

> **归属声明**：本 DAG 是 P22 R10 FINAL deferred 的 P22.1 架构债子任务拆分；来源同其它 P22.1 主体：`docs/plans/迁移/p22/JUDGEMENT_R8_QA.md §R10.6`、`docs/plans/迁移/p22/JUDGEMENT_R8_QC.md §7` 与 §10.30 三层分工铁律；只规划收口，不改 P22 `JUDGEMENT_*` 历史快照。
> 总览：[`README.md`](README.md)
> Findings：[`FINDINGS.md`](FINDINGS.md)
> Gate：[`GATE_CONTRACTS.md`](GATE_CONTRACTS.md)

## 1. DAG 原则

- 每个实施 agent 控制在 **10-15 文件** write-set。
- 公共 contract / archtest 先行，模块 slice 后行；禁止多个 agent 同时改同一 package 的 module wiring。
- Phase 2 的 9+ 模块可并行，但公共 BusModule / RunnerModule helper 不可并行修改。
- P22.1 不改 P22 `JUDGEMENT_*` 与既有 findings；若需要引用，只新增 P22.1 文档或代码注释。

## 2. 子任务 DAG

```text
P22.1-P0A：BusModule subscriber group contract
P22.1-P0B：RunnerModule actor contract
P22.1-P0C：archtest skeleton + precise allowlist format
  └─ depends on P0A/P0B naming agreement

P22.1-P1A：root bridge shutdown ordering（internal/app/runner.go）
  └─ depends on P0C; 仅修 root bridge 顺序，不声称未迁移 worker 已受 run.Group 托管
P22.1-P1B：watchFXShutdown boundary（internal/app/app.go）
  └─ depends on P0C

P22.1-P2A.1：insight BusModule template
  └─ depends on P0A and P1A
P22.1-P2A.2：turn/observation BusModule template
  └─ depends on P0A and P1A; shares only BusModule contract, not module files
P22.1-P2B：rpc push + hooks + mcpcontrol worker-as-runner
  └─ depends on P0A/P0B and P1A
P22.1-P2C：thread bus workers + observation integration check
  └─ depends on P2A template and P0B
P22.1-P2D：memory hooks scheduler/nested/teamSync migration
  └─ depends on P2B worker pattern and P1A ordering
P22.1-P2E：cachekeepalive relay/timer split
  └─ depends on P2B worker pattern
P22.1-P2F：toolbridge diff fallback subscriber ownership
  └─ depends on P2A BusModule template

P22.1-P3A：session-private runtime allowlist precision
  └─ depends on P2A..P2F final code shape
P22.1-P3B：full archtest hardening / remove temporary allowlist
  └─ depends on P3A
```

### 2.1 P0C warning → fail 激活策略（2026-04-25 HEAD 修订）

- Phase 0 骨架完成后，archtest gate 先以 **warning 模式**激活：matcher 能识别 F-1~F-11，但对既存命中使用 `t.Log` / TODO allowlist 记账，不在 Phase 0/2 直接 `t.Fatal` 阻断 CI。
- Phase 2 期间 warning 可见但不 fail CI；每个 slice 必须在自己的验收里把对应 TODO allowlist 归零或缩小。
- Phase 3 升级为 **fail 模式**：正式 `t.Errorf`/`t.Fatalf`，禁止新增 lifecycle-owned subscriber/worker；`TestSessionPrivateRuntimeAllowlist` 同步收紧。
- 逻辑依据：W3 R1 指出“gate 若是禁止新增，必须最先激活”；因此 P0C 先开 warning 防新增漂移，P3 再 hard fail。

### 2.2 Phase 0 implementation spec（2026-04-25 X R2/R4 补修）

Phase 0 仍只可派 **单 owner 文档/代码骨架 agent**，不得直接并行 Phase 2。最小实施清单冻结如下：

| 项 | P0 冻结内容 | 文件/测试落点 |
|---|---|---|
| Bus group tag | 新增/统一 Fx group tag `bus.subscribers`，只由 BusModule 注册消费；业务 module 只提供 subscriber spec | `internal/platform/bus/*`、`internal/app/modules.go`、必要契约文档 |
| Subscriber spec | 定义 subscriber spec 字段：事件类型、handler symbol、owner module、cancel owner、shutdown class、test fixture id | `internal/platform/bus/*` + `TestBusSubscriberGroup` fixture |
| Bus Stop 顺序 | stop-intake / cancel subscriber group / dispatcher close 顺序写入 contract；不得把 unsubscribe 当 run.Group drain | `internal/platform/bus/module.go`、`GATE_CONTRACTS.md` |
| Runner adapter | 冻结 worker→`platformrunner.Runner` adapter/helper API；只收进 `group:"runners"`，不新增 `runner.actors` tag | `internal/platform/runner/*`、`internal/app/modules.go` |
| P0C TODO allowlist | TODO 记录必须含 Finding、path、symbol、owner、RemoveWhen、Phase；Phase 3 前归零或转精确 allowlist | `internal/archtest/*` |
| warning-mode 行为 | Phase 0/2 用 `t.Logf` 报告既存命中 + no-new guard；Phase 3 改 `t.Errorf`/`t.Fatalf` | `TestBusSubscriberGroup`、`TestRunnerActorGuard/ownership`、`TestShutdownOrdering`、`TestSessionPrivateRuntimeAllowlist` |

### 2.2.1 Phase 0 file-level write-set 冻结（2026-04-25，Y3 补修）

> Y3 最终验收裁决：§2.2 架构意图已清楚，但派 Phase 0 single-owner agent 时必须给出精确文件级 write-set。本段作为派工 prompt 的硬约束清单（违反即架构债）。按 §10.31 增量补录，不修改 §2.2 既有内容。

**新建文件（Phase 0 落地产物）**：

| 文件 | 职责 |
|---|---|
| `internal/platform/bus/subscribers.go` | BusModule subscriber group provider/consumer + spec struct（字段见 §2.2）|
| `internal/platform/bus/subscribers_test.go` | subscriber spec 注册/取消/stop-intake 顺序 unit test |
| `internal/platform/runner/contract.go` | RunnerModule actor contract + worker-as-runner adapter helper API |
| `internal/platform/runner/contract_test.go` | adapter/helper 行为 unit test |
| `internal/archtest/session_private_allowlist.go` | 9 字段 allowlist struct（对齐 `internal/archtest/root_bridge_allowlist.go` schema）|
| `internal/archtest/session_private_allowlist_test.go` | allowlist integrity + `TestSessionPrivateRuntimeAllowlist` 初始 live matcher |

**修改既有文件（§10.31 增量不删）**：

| 文件 | 改点 |
|---|---|
| `internal/platform/bus/module.go` | 接入 `group:"bus.subscribers"` 消费；新增 stop-intake → cancel subscriber group → dispatcher close 顺序 |
| `internal/platform/runner/module.go` | 接入 contract provider；**禁止**新增 `runner.actors` tag（active 只用 `group:"runners"`）|
| `internal/archtest/bus_callback_guard_test.go` | 新增 `t.Run("subscriber_group_ownership_warning", ...)` 子测试 + AST walker + one-hop helper resolver |
| `internal/archtest/runner_actor_guard_test.go` | 新增 `t.Run("ownership", ...)` 子测试，识别 module lifecycle 内 worker Start/Stop |
| `internal/archtest/lifecycle_onstart_guard_test.go` | 新增 `TestShutdownOrdering` 子测试（hybrid AST + regex，识别 cancel→wait→bus.Stop→close）|

**Phase 0 agent 禁止触碰（属 Phase 1/2 范围）**：

- `internal/module/*` 任何文件（含 memory / thread / insight / turn/observation）→ Phase 2 slice
- `internal/app/runner.go` / `internal/app/app.go` → Phase 1（P1A / P1B）
- `internal/platform/rpc/module.go` / `hooks/module.go` / `mcpcontrol/module.go` / `cachekeepalive/module.go` / `toolbridge/module.go` 的 lifecycle 接线 → Phase 2（P2B/P2E/P2F）
- 任何业务 module 的 `fx.Invoke(registerXxx)` 接线

**规模约束**：

- 单 owner agent 触碰 **11 个文件**（6 新建 + 5 修改），符合 §10.7 每 agent ≤10-15 文件上限
- 单 commit 不得混入 Phase 1/2 变更
- archtest 扩展采用**子测试模式**（`t.Run(...)`），不新建顶层 `TestBusSubscriberGroup` / `TestRunnerActorOwnership` 同名重复 guard

**AST/hybrid matcher 最低要求**（防止 grep 兜底导致高误报/漏报）：

- AST 主判定：`fx.Invoke(named)` → resolve function；`Lifecycle.Append(fx.Hook{OnStart/OnStop})`；nested `FuncLit` / `GoStmt` 递归；one-hop helper resolver
- 识别对象：`bus.Subscribe` / `ResilientSubscribe`；`worker.Start/Stop` / `scheduler.Start` / `nested.Start` / `teamSync.Start` / `svc.startBusWorkers/stopBusWorkers`
- regex 兜底：`Start|Run|Begin|Serve|Loop|Watch`，仅限 `fx.Hook.OnStart/OnStop` body + 非 RunnerModule path
- 超过 one-hop 的 helper 链：**fail-closed**，要求显式加入 TODO allowlist

**TODO allowlist schema（Phase 0 新建 + Phase 3 前归零）**：

```go
type runtimeOwnershipTODO struct {
    Finding    string // F-3 / F-6 / F-10 / ...
    Path       string // internal/module/memory/module.go
    Symbol     string // registerMemoryHooks
    Owner      string // P22.1-P2D
    RemoveWhen string // Phase 2 slice 完成条件
    Phase      string // 0 | 2 | 3
}
```

### 2.3 F-1~F-11 → DAG node 显式映射（2026-04-25 X R2/R4 补修）

| Finding | DAG node | 边界 |
|---|---|---|
| F-1 root shutdown ordering | P1A | 只反转 root bridge 顺序 + 反向测试；不声称 module worker 已迁移 |
| F-2 desktop watcher boundary | P1B / P3A | 先定例外边界，最终进 session-private allowlist |
| F-3 memory hooks | P2D | 依赖 P2B worker pattern 与 P1A ordering |
| F-4 thread workers/subscribers | P2C | 与 P2A.2 observation 合入串行 |
| F-5 cachekeepalive | P2E | 依赖 P2B worker pattern |
| F-6 hooks fanout | P2B | P2B 第一批 worker-as-runner 样本 |
| F-7 rpc push | P2B | P2B 第一批 worker-as-runner 样本 |
| F-8 mcpcontrol fanout | P2B | P2B 第一批 worker-as-runner 样本 |
| F-9 toolbridge diff fallback | P2F | 仅 subscriber ownership，不碰 handler fallback hidden contract |
| F-10 insight collector | P2A.1 | BusModule 模板样本 |
| F-11 observation subscriber | P2A.2 | 不顺手改 thread；与 P2C 串行审冲突 |

## 3. 子任务拆分表

| Node | Phase | 目标 | 前置依赖 | 预估 write-set（10-15 文件上限） | 可并行 |
|---|---:|---|---|---|---|
| P22.1-P0A | 0 | 定义 BusModule subscriber group 输入/输出 contract | 无 | `internal/platform/bus/*`、`internal/archtest/*`、契约文档 | P0B |
| P22.1-P0B | 0 | 定义 RunnerModule actor contract 与 worker adapter 模式 | 无 | `internal/platform/runner/*`、`internal/archtest/*`、契约文档 | P0A |
| P22.1-P0C | 0 | archtest skeleton：bus/runner/shutdown/session-private | P0A/P0B | `internal/archtest/*` | 否，公共守卫 |
| P22.1-P1A | 1 | root OnStop 顺序调整 | P0C | `internal/app/runner.go`、`internal/app/runner_test.go`、archtest | P1B |
| P22.1-P1B | 1 | `watchFXShutdown` owner ctx / allowlist 边界 | P0C | `internal/app/app.go`、desktop tests、archtest allowlist | P1A |
| P22.1-P2A.1 | 2 | `insight` subscriber 迁移，做 BusModule 模板 | P0A/P1A | `internal/module/insight/*`、insight bus tests | P2B/P2E/P2F（仅在 P0A API 冻结后） |
| P22.1-P2A.2 | 2 | `turn/observation` subscriber 迁移；不得顺手改 thread | P0A/P1A | `internal/module/turn/observation/*`、observation tests | P2B/P2E/P2F；与 P2C 合入串行 |
| P22.1-P2B | 2 | `rpc`/`hooks`/`mcpcontrol` fanout workers 进入 RunnerModule，subscriptions 进 BusModule | P0A/P0B/P1A | `internal/platform/rpc/*`、`internal/platform/hooks/*`、`internal/platform/mcpcontrol/*` | P2A/P2E/P2F |
| P22.1-P2C | 2 | `thread` bus workers 进入 RunnerModule | P2A/P0B | `internal/module/thread/*`、必要 `internal/module/turn/*` contract tests | P2D 谨慎并行；合入串行 |
| P22.1-P2D | 2 | `memory` scheduler/nested/teamSync worker 迁移 | P2B/P1A | `internal/module/memory/*`、`internal/module/memory/*_test.go` | P2E/P2F |
| P22.1-P2E | 2 | `cachekeepalive` relay/timer split | P2B | `internal/platform/cachekeepalive/*` | P2A/P2D/P2F |
| P22.1-P2F | 2 | `toolbridge` diff fallback subscriber 迁移 | P2A | `internal/platform/toolbridge/*` | P2E/P2D |
| P22.1-P3A | 3 | session-private runtime allowlist 精确化 | P2A..P2F | `internal/archtest/*`、少量 docs | 否 |
| P22.1-P3B | 3 | 全量 archtest hardening 与临时 allowlist 回收 | P3A | `internal/archtest/*` | 否 |

## 4. 并行批次建议

### Batch 0：公共契约（串行收口）

```text
P22.1-P0A + P22.1-P0B（可并行调研，但合入前统一命名）
 -> P22.1-P0C（串行）
```

### Batch 1：根顺序与小模板

```text
P22.1-P1A || P22.1-P1B
P22.1-P2A.1 在 P1A 后启动，作为 insight BusModule 模板
P22.1-P2A.2 可随后启动 observation 模板；与 P2C(thread) 代码合入串行审冲突
```

### Batch 2：平台 fanout 与低耦合模块

```text
~~P22.1-P2B || P22.1-P2E || P22.1-P2F~~
2026-04-25 HEAD 修订：旧 Batch 2 过度乐观；P2B 可先行，P2E 依赖 P2B worker pattern，P2F 依赖 P2A BusModule template，三者最多调研并行、代码合入串行。
```

### Batch 3：复杂业务模块

```text
P22.1-P2C（thread） -> P22.1-P2D（memory，可在 P2C 调研期并行，但代码合入需串行审冲突）
```

### Batch 4：allowlist 收口

```text
P22.1-P3A -> P22.1-P3B
```

## 5. write-set 冲突检查

| 冲突面 | 可能冲突节点 | 处理 |
|---|---|---|
| `internal/archtest/*` | P0C、P1A、P1B、P3A、P3B | 只允许一个 archtest owner；模块 slice 只新增 fixture 需求，不直接改公共判定 |
| BusModule helper | P0A、P2A、P2B、P2E、P2F | P0A 先冻结 API；后续 slice 只消费，不扩 API，除非回 P0A 变更单 |
| RunnerModule helper | P0B、P2B、P2C、P2D、P2E | P0B 先冻结 adapter；worker slice 不各自发明 wrapper |
| `internal/module/thread` 与 `internal/module/turn` | P2A、P2C | observation 模板只改 `turn/observation`；thread slice 合入前 rebase |
| `internal/platform/toolbridge` hidden contract | P2F 与任何 P22/P4 后续 | toolbridge P22.1 只处理 subscriber owner，不碰 handler fail-closed / hidden contract |
| `internal/module/memory` drain tests | P2D 与任何 memory 性能/检索任务 | P2D 独占 memory module wiring，其他任务后置 |

### 5.1 6×6 pairwise 冲突矩阵（§10.13 四维度，2026-04-25 HEAD 修订）

维度缩写：①生产代码互引；②共享符号 xref；③测试跨引；④共享 wiring 文件（重点 `internal/app/modules.go` / `cmd/mcp-orch/fx.go`，以及各模块 `module.go`）。结论：OK=可并行；WARN=调研可并行、合入需串行；BLOCK=同一 owner 串行。

| Slice × Slice | P2A insight/observation | P2B rpc/hooks/mcpcontrol | P2C thread | P2D memory | P2E cachekeepalive | P2F toolbridge |
|---|---|---|---|---|---|---|
| P2A insight/observation | — | ①OK ②WARN BusModule API ③OK ④WARN module wiring pattern | ①WARN observation↔thread ②WARN turn contract ③WARN observation/thread tests ④WARN `turn/observation/module.go` | ①WARN insight consumes memory events ②WARN upstream event symbols ③OK ④OK | ①OK ②WARN BusModule helper ③OK ④OK | ①OK ②WARN subscriber helper ③OK ④OK |
| P2B rpc/hooks/mcpcontrol | ①OK ②WARN BusModule API ③OK ④WARN | — | ①OK ②WARN Runner adapter ③OK ④OK | ①WARN hooks/rpc emit memory-visible events ②WARN worker pattern ③OK ④OK | ①WARN shared platform worker pattern ②WARN Runner adapter ③OK ④WARN `internal/platform/*` | ①OK ②WARN platform bus helper ③OK ④OK |
| P2C thread | ①WARN observation↔thread ②WARN turn contract ③WARN cross tests ④WARN | ①OK ②WARN Runner adapter ③OK ④OK | — | ①WARN thread events feed memory auto-dream ②WARN event types ③WARN memory/thread tests ④OK | ①OK ②OK ③OK ④OK | ①OK ②OK ③OK ④OK |
| P2D memory | ①WARN insight/memory upstream events ②WARN event symbols ③OK ④OK | ①WARN worker pattern ②WARN Runner adapter ③OK ④OK | ①WARN memory consumes thread stopped ②WARN event types ③WARN tests ④OK | — | ①OK ②WARN Runner adapter ③OK ④OK | ①WARN tool call end events ②WARN event symbols ③OK ④OK |
| P2E cachekeepalive | ①OK ②WARN Bus/Runner helper ③OK ④OK | ①WARN platform worker pattern ②WARN Runner adapter ③OK ④WARN platform helper | ①OK ②OK ③OK ④OK | ①OK ②WARN Runner adapter ③OK ④OK | — | ①OK ②WARN Bus helper ③OK ④OK |
| P2F toolbridge | ①OK ②WARN subscriber helper ③OK ④OK | ①OK ②WARN platform bus helper ③OK ④OK | ①OK ②OK ③OK ④OK | ①WARN tool-call-end memory event ②WARN event symbols ③OK ④OK | ①OK ②WARN Bus helper ③OK ④OK | — |

高影响冲突对：P2A ↔ P2D（insight/observation 与 memory 的上游事件链、ToolCallEnd/ThreadStopped 消费）、P2A ↔ P2C（observation 与 thread/turn 边界测试）、P2B ↔ P2E（cachekeepalive 依赖 P2B worker pattern）、P2D ↔ P2F（toolbridge diff fallback 的 ToolCallEnd 事件与 memory nested/tool-result 路径）。

### 5.2 §10.13 四维度核查命令样例（2026-04-25 HEAD 修订）

本 lane 已按 §10.13 对每对 slice 做四维度 LSP 核查：生产代码互引、共享符号 xref、测试跨引、共享 wiring 文件。样例命令：

- `lsp_grep(text_search, path="internal/module/memory", query="ThreadStopped")` ↔ `lsp_grep(text_search, path="internal/module/thread", query="ThreadStopped")` 核生产互引。
- `lsp_xref(references, file_path="internal/platform/bus/resilient.go", line=10, column=6)` 核 subscriber helper 共享符号。
- `lsp_grep(text_search, path="internal", glob="*_test.go", query="RegisterSubscribers")` 核测试跨引。
- `lsp_grep(ast_search, language="go", path="internal/app", query="fx.Invoke")` 与 `lsp_grep(text_search, path="cmd/mcp-orch", query="fx.Invoke")` 核共享 wiring。

## 6. 人日估算（2026-04-25 HEAD 修订）

历史 G3 粗估 ~~6-10 人日~~。2026-04-25 HEAD 修订：按 W3 独立复算覆盖为 **12.5-20 人日**：Phase 0 contract+warning gate 2-3；Phase 1 shutdown+测试反转 1.5-2.5；Phase 2 六个 slice 7-11；Phase 3 allowlist hardening 2-3.5。旧值仅保留为历史快照，不再作为派工容量依据。2026-04-25 X3 R2 补注：X3 独立上限估算 **22-34 人日** 属 TRUE_BUT_DEFERRED 容量风险；若 Phase 0 custom walker / helper resolver 超出骨架范围，则改用 22-34 作为重排基线。

## 7. 每节点验收清单

1. 本节点触及的 F-x 在 [`FINDINGS.md`](FINDINGS.md) 中有对应销账说明。
2. `TestBusSubscriberGroup` / `TestRunnerActorOwnership` 对该节点不再需要临时豁免。
3. 相关 worker 有 bounded shutdown 测试；相关 subscriber 有 cancel 幂等测试。
4. `git diff --stat` 显示未越权到无关 package；P22 文档与 judgement 文件未被修改。
5. 若新增 allowlist，必须在 P3A 中登记精确 owner/function/shape，不允许整文件。

## 红队仲裁（2026-04-25）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §4。
整体裁决：🟢 READY / 🟠 NEEDS-FIX / 🔴 BLOCK（以 JUDGEMENT.md §7 为准）。

## R2 发现仍未销账项（2026-04-25 HEAD drift note）
详见 `docs/plans/迁移/p22/p22.1/JUDGEMENT.md` §R2。R2 仲裁结论：🔴 R2 BLOCK。DAG 主体仍需只加不删补齐归属 header、P1A 边界、P0C warning/TODO allowlist → P3 fail/hardening、6×6 pairwise 冲突矩阵、12.5-20 人日估算与 §10.13 四维度核查；旧 Batch 2 并行建议与 JUDGEMENT/R2 冲突，实施前不得直接按旧并行派工。


## 8. DAG 14 节点 HEAD `a81554c` 完成度 overlay（2026-04-25，第 6 轮）

> 本节按 §10.31 只加不删追加；上文 DAG 仍保留为实施前规划与 write-set 约束。当前 HEAD 锚点为 `a81554c`；实施链锚点为 `25a37ad` → `f737e45` → `17b5ce7` → `dfe12e6` → `b386217` → `a9a018e` → `a81554c`。

| Node | HEAD `a81554c` 完成度 | 销账 Finding / 输出 | 后续状态 |
|---|---|---|---|
| P22.1-P0A | ✅ | BusModule `bus.subscribers` contract 已落 | 仅需随 gate 命名补文档 |
| P22.1-P0B | ✅ | RunnerModule adapter / `group:"runners"` contract 已落 | `runner.actors` 命名债 deferred |
| P22.1-P0C | 🟡 | archtest skeleton 与部分 fail-mode 已落 | gate 3 处 NEEDS-FIX 待 Audit-A/B/C |
| P22.1-P1A | ✅ | F-1 root OnStop ordering | 已销账 |
| P22.1-P1B | ✅ | F-2 `watchFXShutdown` owner ctx / allowlist boundary | 已销账 |
| P22.1-P2A.1 | ✅ | F-10 insight subscriber 迁移 | Audit-D two-hop 报告已被主 agent LSP 证伪；按 HEAD 干净状态记录 |
| P22.1-P2A.2 | ✅ | F-11 observation subscriber 迁移 | 已销账 |
| P22.1-P2B | ✅ | F-6 hooks / F-7 rpc / F-8 mcpcontrol fanout workers + subscribers | 已销账 |
| P22.1-P2C | ✅ | F-4 thread workers/subscribers | 已销账 |
| P22.1-P2D | ✅ | F-3 memory scheduler/nested/teamSync + subscriptions | 已销账 |
| P22.1-P2E | ✅ | F-5 cachekeepalive relay/timer split | 已销账 |
| P22.1-P2F | ✅ | F-9 toolbridge diff fallback subscriber | 已销账 |
| P22.1-P3A | ✅ | session-private runtime allowlist 精确化 | 已落 HEAD `a81554c` |
| P22.1-P3B | 🟡 | archtest hardening / temporary allowlist 回收 | gate 3 处 NEEDS-FIX + cron/uistate cross-file gap 待后续修复 |

**write-set overlay**：当前实施链已跨过原 DAG 并行风险期，本文档仍保留 §5/§5.1 的冲突矩阵作为后续补修 guard/gate 时的 write-set 约束；后续 Audit-A/B/C 只能按各自 gap 小范围修改，不得重开 P0/P2 公共 contract。

## 9. DAG HEAD `5d6a93c` Round-3 BLOCK 收口 overlay（2026-04-25）

> 本节按 §10.31 只加不删追加；§8 的 HEAD `a81554c` 完成度表保留为历史 overlay。当前 Round-3 修复基线为 HEAD `5d6a93c`。

| Round-3 item | DAG/Finding 映射 | `5d6a93c` 后本轮目标态 |
|---|---|---|
| runner OnStop ordering | P22.1-P1A / F-1 | `cancel → waitForRuntimeDone → drainRuntimeBeforeStop` |
| desktop pre-drain ordering | P22.1-P1B / F-2 boundary | `WaitRuntimeDone → DrainRuntime` |
| AST shutdown gate | P22.1-P0C/P3B gate hardening | `TestShutdownOrdering` 按 AST statement 顺序 fail-closed |
| session-private BindRuntime integrity | P22.1-P3A | BindRuntime SafeGo one-hop launch 必须有 `DefinitionPath=internal/app/runner.go` + `Symbol=BindRuntime` allowlist entry |
| memory/thread race tests | P22.1-P2C/P2D regression | nested ingest coalesce deterministic；thread fake binding store mutex/accessor 化 |
| P21 active runner tag docs | contract naming follow-up | `runner.actors` 仅 historical role naming；active Fx tag 为 `group:"runners"` |


## 10. DAG HEAD `aa09f58` V3-B 锚点修正 overlay（2026-04-25）

> 本节按 §10.31 只加不删追加；§9 的 HEAD `5d6a93c` 表保留为 Round-3 代码修复基线历史 overlay。V3-B 复核实测当前仓库 `git rev-parse --short HEAD` 为 `aa09f58`，因此当前 DAG HEAD 锚点修正为 `aa09f58`。

| V3-B 修正项 | 当前锚点 | 保留历史锚点 | 裁决 |
|---|---|---|---|
| HEAD 真值 | `aa09f58` | `5d6a93c` / `a81554c` | `aa09f58` 为当前 Git HEAD；旧锚点仅作历史 overlay |
| runner OnStop ordering | `aa09f58` | `5d6a93c` | `cancel → waitForRuntimeDone → drainRuntimeBeforeStop` |
| desktop pre-drain ordering | `aa09f58` | `5d6a93c` | `WaitRuntimeDone → DrainRuntime` |
| P21 active runner tag docs | `aa09f58` | `5d6a93c` | `runner.actors` 仅 historical role naming；active Fx tag 为 `group:"runners"` |
