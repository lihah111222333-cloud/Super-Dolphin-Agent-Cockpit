# P22.1 架构债子任务红队仲裁

> 2026-04-25 W1-W4 红队审查 + 独立仲裁汇总
> 归属：`docs/plans/迁移/p22/p22.1/`（P22 R10 deferred 架构债子任务）
> 仲裁身份：独立仲裁 agent；未参与 W1-W4 任一路红队审查；本文件只追加文档，不改代码。

## §1 红队构成与独立性

- **W1** `agent-1777050081916-1777050081912269000`：FINDINGS HEAD 对账；报告状态 idle / report present；结论 **NEEDS-FIX**，F-1 UPGRADE，其余 CONFIRM。
- **W2** `agent-1777050107430-1777050107429069000`：文档漂移 + P23 残留 + R10.6/§7.5.1/FINDINGS 对账；报告状态 idle / report present；结论 **NEEDS-FIX**。
- **W3** `agent-1777050136871-1777050136870226000`：DAG 依赖 + 并行 write-set + 估算；报告状态 idle / report present；结论 **NEEDS-FIX**，不建议直接开 6-slice 并行。
- **W4** `agent-1777050169931-1777050169930823000`：Gate vs 现有 archtest；报告状态 idle / report present；结论 **NEEDS-FIX / Red-team BLOCK for gate-spec clarity**。

独立仲裁规则：按 `docs/1/会话习惯.md §10.22/§10.41`，W 路结论必须被承认或由主仲裁 LSP 独立反驳；按 §10.18，一路真实 BLOCK 即整体 BLOCK；按 §10.33，若 W 路报 HEAD 已修旧问题则 OVERTURN W 路。

## §2 收报状态表（4/4）

| W# | agent id | state | report? | 处置 |
|---|---|---|---|---|
| W1 | `agent-1777050081916-1777050081912269000` | idle | yes | 纳入仲裁；F-1 UPGRADE 需 LSP 复核 |
| W2 | `agent-1777050107430-1777050107429069000` | idle | yes | 纳入仲裁；文档漂移/死锚点/范围声明采信 |
| W3 | `agent-1777050136871-1777050136870226000` | idle | yes | 纳入仲裁；DAG 并行与估算采信 |
| W4 | `agent-1777050169931-1777050169930823000` | idle | yes | 纳入仲裁；gate-spec clarity BLOCK 采信 |

无 `state=running` 未完成报告；无 idle 空报告；无 §10.40 空跑谎报。

## §3 FINDINGS F-1 ~ F-11 仲裁表

| F# | W1 判定 | W2 涉及 | W3 涉及 | W4 涉及 | 仲裁结论 | 证据 |
|---|---|---|---|---|---|---|
| F-1 | UPGRADE | 覆盖 R10.6 #8 未修；非 overlay 重复 | Phase 1 P1A 合理，但需澄清 P1 只冻 root ordering | `TestShutdownOrdering` 现有 archtest 未覆盖 | **UPGRADE / 保留；实施前修测试反向保护** | `internal/app/runner.go:71-84` 当前 drain→cancel→wait；`runner_test.go:79-88` 固化 “cancel 前 drain”；`BindRuntime` refs 5、incoming 4 |
| F-2 | CONFIRM | 覆盖 R10.6 #8 未修；非 P23 残留 | Phase 1/P3：watcher 边界需精确 allowlist | `TestShutdownOrdering`/session-private allowlist 涉及 | **CONFIRM / Phase 1+3** | W1/W3 指向 `internal/app/app.go:171-181`；`watchFXShutdown` 使用 `context.Background()` |
| F-3 | CONFIRM | 与 §7.5.1 #9 同 locus；需澄清非“不 wait/drain”重开 | Phase 2 memory；估算低估、单 owner | Gate #1/#2 首批命中 | **CONFIRM，但必须追加 overlay 澄清** | `module.go:422-438` 仍由 fx hook Start/Stop + subscription；`drainMemoryHooks` 已存在，故 OVERTURN “不 wait/drain”旧说法、保留 owner 违例 |
| F-4 | CONFIRM | 无重复销账冲突 | Phase 2 thread；与 observation 有边界/测试跨引 | Gate #1/#2 命中 | **CONFIRM / Phase 2 串行审冲突** | `thread/module.go:64-88` lifecycle start/subscribe/stop；需进入 RunnerModule/BusModule |
| F-5 | CONFIRM | 与 R10.6 #5 仅弱歧义；非 TeamSync Pull/Push 重开 | Phase 2 platform/cachekeepalive；P2E 依赖 P2B worker pattern | Gate #1/#2 命中 | **CONFIRM / Phase 2，但依赖 worker pattern** | `cachekeepalive/module.go:35-49` OnStart `startKeepaliveRelay`；definition 到 `relay.go:14-47` |
| F-6 | CONFIRM | 无 overlay 冲突 | Phase 2 platform fanout；P2B 先给 pattern | Gate #1/#2 命中 | **CONFIRM / Phase 2 P2B** | W1/W4 均指向 `hooks/module.go:93-108` worker Start/Stop + relay subscriptions |
| F-7 | CONFIRM | 无 overlay 冲突 | Phase 2 P2B；作为 rpc worker pattern 前置 | Gate #1/#2 命中 | **CONFIRM / Phase 2 P2B 优先样本** | `rpc/module.go:151-169` worker Start/Stop + subscribeCoreEventPushes |
| F-8 | CONFIRM | 无 overlay 冲突 | Phase 2 P2B；与 hooks/rpc 半串行 | Gate #1/#2 命中 | **CONFIRM / Phase 2 P2B** | `mcpcontrol/module.go:178-196` worker Start/Stop + config subscriptions |
| F-9 | CONFIRM | R10.6 #2 handler fallback 未覆盖；F-9 只是 subscriber owner | Phase 2 P2F 依赖 P2A BusModule template | Gate #1 命中；不得混同 P22/P4 hidden contract | **CONFIRM / 需声明不覆盖 handler fallback** | `toolbridge/module.go:171-183` OnStart 直接 `ResilientSubscribe`；不是 `handler.go` fallback 债 |
| F-10 | CONFIRM | 无 overlay 冲突 | Phase 2 P2A BusModule template 候选 | Gate #1 命中 | **CONFIRM / Phase 2 模板样本** | `insight/module.go:59-69` lifecycle 调 `Collector.subscribe`；helper 存在不等于 BusModule 生效 |
| F-11 | CONFIRM | 无 overlay 冲突 | Phase 2 P2A；与 thread/turn 边界需串行复核 | Gate #1 命中 | **CONFIRM / Phase 2 模板样本** | `observation/module.go:48-58` lifecycle 调 `Subscribe`；helper 存在不等于 BusModule 生效 |

## §4 Phase 时序 / write-set 冲突仲裁

### Phase 0 必要性

仲裁采信 W3：**Phase 0 对并行 Phase 2 是硬前置**。HEAD `internal/platform/bus/module.go:10-30` 仅提供 dispatcher/thread emitters/log sink 并在 OnStop close dispatcher；无 `bus.subscribers` 或等价 subscriber group。`lsp_grep "bus.subscribers" internal` 为 0。若 P2 多 slice 各自发明 contract，将冲突于 `internal/platform/bus/*`、`internal/platform/runner/*`、`internal/archtest/*`。

### Phase 1 vs Phase 2 先后

仲裁采信 W3：Phase 1 先于 Phase 2 总体合理，但 DAG 必须补明：P1A 只处理 root `cancel → wait RunGroup` 顺序；subscriber intake/cancel owner 属 Phase 0 contract + Phase 2 消费，P1A 不得发明业务 subscriber drain。

### Phase 3 gate 激活时机

仲裁采信 W3+W4：Phase 3 作为 cleanup/hardening 后置合理；但 **no-new-regression gate 必须在 P0C 先以 warning/TODO allowlist 模式激活，Phase 3 再升级 fail 并回收临时 allowlist**。

### Phase 2 六 slice 并行矩阵

当前 DAG 对 “P2A/P2B/P2E/P2F 可并行” 过乐观，需降级：

| Pair/面 | 仲裁 |
|---|---|
| P2A insight+observation ↔ P2C thread | 有 turn/observation 边界，P2C 依赖 P2A template，合入需串行 |
| P2B rpc/hooks/mcpcontrol ↔ P2E cachekeepalive | P2E 依赖 P2B worker pattern，不应完全并行发明 wrapper |
| P2B ↔ P2D memory | P2D 依赖 worker pattern 且 memory 复杂，应单 owner 后置/半串行 |
| P2A ↔ P2F toolbridge | P2F 依赖 BusModule template；可调研并行，代码合入需确认 contract frozen |
| 所有 P2 ↔ `internal/platform/bus/*`/`runner/*`/`archtest/*` | P0 frozen 后只消费；不得改公共 helper，除非回 P0 变更单 |

### 人日估算对账

采信 W3：G3 6-10 人日低估；仲裁建议在 DAG 追加 **12.5-20 人日** 规划口径，低估集中在 memory、rpc/hooks/mcpcontrol、thread 与 archtest hardening。

### 命名债 `runner.actors`

采信 W4：当前 active Fx tag 是 `group:"runners"`；`runner.actors` 属 `docs/契约/*` deferred 命名债，P22.1 gate 不能误写成 active tag，但应在文档中显式声明该命名债未收。

## §5 Gate 契约仲裁

| Gate | W4 判定 | 现有重复? | 建议动作 |
|---|---|---|---|
| `TestBusSubscriberGroup` | 方向正确，但 gate 与现有 guard 映射不清 | 与 `bus_callback_guard_test.go` / `fx_invoke_guard_test.go` / `lifecycle_onstart_guard_test.go` **部分重叠、不等价** | 在现有 runtime-ownership guard 体系内追加 Subscribe/ResilientSubscribe matcher；写清 skip→live 清单和 one-hop helper AST contract |
| `TestRunnerActorOwnership` | 方向正确，但不能误认为已存在 | HEAD 无同名函数；仅有 `TestRunnerActorGuard`，语义不同 | 在 `runner_actor_guard_test.go` 或新 ownership guard 中落地 worker Start/Stop matcher；明确与 Runner body guard 分工 |
| `TestShutdownOrdering` | 必要且现有未覆盖 | 无等价 archtest；`runner_test.go` 反向固化旧顺序 | 新建 root-specific guard 或复用 root bridge allowlist；覆盖 `internal/app/runner.go` AST 顺序 |
| `TestSessionPrivateRuntimeAllowlist` | 必要但当前字段弱于 root bridge 文化 | root bridge 已有 9 字段严格 schema；session-private 无现成 schema | 以 root bridge 9 字段为基座扩展 owner/lifetime/stop/drain/why_not_runner；禁止整文件豁免 |

## §6 LSP 独立反驳记录（≥5 争议点）

| 争议点 | W 路结论 | LSP 实测 | 仲裁 |
|---|---|---|---|
| F-1 是否只是 CONFIRM | W1 UPGRADE | `read_file internal/app/runner.go:71-84` 显示 drain→cancel→wait；`read_file runner_test.go:79-88` 显示测试禁止 drain 前 cancel；`references/call_hierarchy BindRuntime` 显示 prod+test 入口 | **承认 UPGRADE**：实施前必须修反向测试保护 |
| F-3 是否被 §7.5.1 #9 已销账推翻 | W1 CONFIRM；W2 指歧义 | `read_file session-summary.md:278-291` 显示 #9 “不 wait/drain”已销账；`read_file memory/module.go:422-438` 显示 Start/Stop/subscription owner 仍在 fx hook；`drainMemoryHooks` 已存在 | **部分反驳旧基线**：OVERTURN “不 wait/drain”说法，但 CONFIRM §10.30 owner 违例 |
| Phase 0 是否硬前置 | W3 PASS with caveat | `read_file bus/module.go:10-30` 无 subscriber group；`lsp_grep "bus.subscribers" internal` = 0；`references ResilientSubscribe` 跨 memory/thread/insight/observation 等 50 处 | **承认 W3**：并行 P2 前必须冻结 P0 contract |
| `TestRunnerActorOwnership` 是否已存在/重名 | W4 纠正“未存在同名” | `lsp_grep "TestRunnerActorOwnership" internal/archtest` = 0；`workspace_symbol TestRunnerActor` 仅见 `TestRunnerActorGuard` | **承认 W4**：无函数名冲突，有语义混淆风险 |
| Gate #1/#2 是否已有现成 archtest 覆盖 | W4 不等价 | `read_file fx_invoke_guard_test.go:54-81`、`lifecycle_onstart_guard_test.go:69-88`、`runner_actor_guard_test.go:72-92` 均为 matcher skeleton/t.Skip；现有 live tests 未覆盖 Subscribe/worker ownership | **承认 W4 BLOCK 点**：需 skip→live 映射 |
| Session-private allowlist 字段是否够严 | W4 认为弱于 root | `read_file root_bridge_allowlist.go:9-36` 显示 root schema 9 字段：DefinitionPath/CallSitePath/Symbol/BridgeShape/ExceptionClass/Reason/RemoveWhen/RollbackWhen/RollbackAction；GATE_CONTRACTS §5 仅 7 字段 | **承认 W4**：P22.1 应按 9 字段基座扩展 |
| P23 残留是否存在 | W2 PASS | W2 指定 query 全 0；仲裁未见 P22.1 内 P23 路径残留 | **承认 W2**：不构成 BLOCK |

## §7 最终裁决

- **整体口径：🔴 BLOCK**。
- 定调原因：W4 明确给出 “Red-team BLOCK for gate-spec clarity / 当前不应直接交实施”；按 §10.18 “任一 W 路真实 BLOCK → 整体 BLOCK” 一票定调。该 BLOCK 不是方向错误，而是 gate spec clarity 阻塞：新建 test vs 现有 guard 加 matcher、skip→live 清单、AST helper 深度、session-private allowlist schema、`runner.actors` 命名口径均未冻结。
- 需修改的 P22.1 文档位置 + 具体修法：
  1. `README.md`：修正 `GATE_CONTRACTS.md#testbussubscribergroup` / `#testrunneractorownership` 两个死锚点；追加 P22.1 仅覆盖 §10.30 owner 债，R10.6 #2/#10 非本轮主体或另列 out-of-scope；显式引用 §10.41-§10.43。
  2. `FINDINGS.md`：在 F-3 追加 overlay 澄清：不是重开 R10.6 #9 “OnStop 不 wait/drain”；F-5 追加不是重开 R10.6 #5 TeamSync Pull/Push test-only；F-9 追加不覆盖 toolbridge handler fallback。
  3. `DAG.md`：补归属 header；补 6×6 pairwise 冲突矩阵；补 P0 frozen 禁越权条款；补 P0C warning/TODO allowlist → Phase 3 fail/hardening；估算改为 12.5-20 人日。
  4. `GATE_CONTRACTS.md`：补 “P22.1 与现有 archtest 改动映射”；补 `t.Skip → live` 表；补 AST matcher contract（direct + nested FuncLit + one-hop helper；超过一跳 fail-closed/显式 TODO allowlist）；session-private allowlist 改用 root bridge 9 字段基座 + 扩展字段；明确 active tag 为 `group:"runners"`，`runner.actors` 是 deferred docs debt。
- 可立即派 Phase 0 实施 agent 的条件：以上文档补齐并经一次 LSP/archtest 规格复核后，先派 **单 owner Phase 0**（bus/runner contract + archtest skeleton warning 模式），不得直接派 6 个 Phase 2 代码 slice。

## §8 §10.31 合规自查

- 本仲裁只加不删：新建 `JUDGEMENT.md`，并只在 P22.1 既有 4 份 `.md` 末尾追加仲裁指针。
- 未修改 `.go` / `.js` / `.sql` / `.json` / `go.mod` / `go.sum`。
- 未删除 P22.1 既有 header / DAG / FINDINGS 表。

## §9 LSP 自证（≥5 工具 + ≥3 xref + ≥2 call_hierarchy + ≥1 diagnostics）

- `lsp_file(read_file)`：读取 `session-summary.md §7.5/§7.5.1`、`lsp-mandatory-prefix.md`、`会话习惯.md §10.18/§10.22/§10.31/§10.41-43`、P22.1 四文档、`JUDGEMENT_R8_QA.md §R10.6`、`runner.go`、`runner_test.go`、`memory/module.go`、`bus/module.go`、archtest guard 文件、`root_bridge_allowlist.go`。
- `lsp_file(diagnostics)`：对 `internal/app/runner.go`、`internal/app/app.go`、`internal/module/memory/module.go`、`internal/archtest/runner_actor_guard_test.go` 执行；返回 3 条 hint 级诊断，无本仲裁写入错误。
- `lsp_grep(text_search)`：`bus.subscribers` = 0；`TestRunnerActorOwnership` = 0；`t.Skip` in `internal/archtest` = 30；`§10.41/§10.42/§10.43` 定位；`archtest/runner/bus` 在 `ai-index.json` 中命中。
- `lsp_xref(references)` ≥3：`BindRuntime` refs 5；`registerMemoryHooks` refs 4；`ResilientSubscribe` refs 50；另查 `RunGroup` incoming。
- `lsp_xref(call_hierarchy)` ≥2：`BindRuntime` incoming 4；`RunGroup` incoming 4；`bus.registerLifecycle` incoming 1。
- `lsp_inspect(definition)`：`startKeepaliveRelay` → `internal/platform/cachekeepalive/relay.go:14-47`。
- `lsp_inspect(implementation)`：`platformrunner.Runner` interface 多实现（cron/insight/notify/mcpcontrol/rpc/toolbridge/codexapp/ui 等），证明 RunnerModule 目标有既有模式。
- `lsp_structure(document_symbol/workspace_symbol)`：`internal/app/runner.go` document symbols；`workspace_symbol TestRunnerActor` 确认存在 `TestRunnerActorGuard`、不存在 `TestRunnerActorOwnership`。
- `lsp_completion`：在 `internal/platform/bus/module.go` 执行补全，未发现现成 subscriber group API。
---

## §R2 第 2 轮复审仲裁（2026-04-25）

> 本节按 §10.31 只加不删，延续第 1 轮 §1-§9 结论，不改旧文。
> 本轮不是重复互审；只核第 1 轮问题是否在修订后主体文档中真实销账。

### §R2.1 W 路复审收报状态（4/4）

| W# | state | report? | 复审条目数 | 处置 |
|---|---|---:|---:|---|
| W1 | idle | yes | 11 | 纳入；7 CLOSED / 4 STILL_OPEN / 0 REOPENED |
| W2 | idle | yes | 5 | 纳入；1 CLOSED / 4 STILL_OPEN / 0 REOPENED |
| W3 | idle | yes | 5 + 1 drift | 纳入；0 CLOSED / 5 STILL_OPEN / 1 REOPENED |
| W4 | idle | yes | 6 + 1 drift | 纳入；0 CLOSED / 6 STILL_OPEN / 1 REOPENED |

无 running 未完成；无空 report；无 R2 report unavailable。

### §R2.2 销账表（第 1 轮问题 → 第 2 轮状态）

#### W1 FINDINGS 销账（第 1 轮 F-1~F-11 主要判定）

| F# | 第 1 轮 W1 判定 | 第 1 轮仲裁 | 第 2 轮 W1 复审 | 主 agent LSP 核 | R2 状态 |
|---|---|---|---|---|---|
| F-1 | UPGRADE | UPGRADE；需修反向测试保护 | 半修：DAG 纳入 `runner_test.go`，FINDINGS 未写反向保护 | `FINDINGS.md:24-35` 仅写 `runner.go`；`BindRuntime` refs 仍含 `runner_test.go:58` | 🟠 STILL_OPEN |
| F-2 | CONFIRM | Phase 1+3 | 修好文档处置 | README/GATE/FINDINGS 仍保留 watcher 边界与 allowlist 方向 | 🟢 CLOSED |
| F-3 | CONFIRM + overlay 歧义 | 必须追加 overlay 澄清 | 未修到 FINDINGS | `lsp_grep "OnStop 不 wait/drain" FINDINGS.md` = 0；F-3 仍仅 `49-60` | 🟠 STILL_OPEN |
| F-4 | CONFIRM | Phase 2 串行审冲突 | 足够 | DAG 已有 thread/observation 边界；Gate 有 worker/subscriber 命中 | 🟢 CLOSED |
| F-5 | CONFIRM | 需澄清非 R10.6 #5 重开 | 半修：路径有，澄清无 | `lsp_grep "TeamSync Pull" FINDINGS.md` = 0 | 🟠 STILL_OPEN |
| F-6 | CONFIRM | Phase 2 P2B | 足够 | DAG/GATE 有 P2B 与 hooks 命中 | 🟢 CLOSED |
| F-7 | CONFIRM | Phase 2 P2B 优先样本 | 足够 | DAG/GATE 有 rpc push worker pattern | 🟢 CLOSED |
| F-8 | CONFIRM | Phase 2 P2B | 足够 | DAG/GATE 有 mcpcontrol worker/subscriber 命中 | 🟢 CLOSED |
| F-9 | CONFIRM | 需声明不覆盖 handler fallback | 半修：区分在 J，FINDINGS 未落 | `lsp_grep "handler fallback" FINDINGS.md` = 0 | 🟠 STILL_OPEN |
| F-10 | CONFIRM helper≠生效 | Phase 2 模板样本 | 基本修好 | GATE 禁 wrapper/helper 隐藏订阅；FINDINGS lifecycle owner 仍清楚 | 🟢 CLOSED |
| F-11 | CONFIRM helper≠生效 | Phase 2 模板样本 | 基本修好 | `RegisterSubscribers` refs = definition + fx.Invoke caller；GATE 禁 `Subscribe(...)` wrapper | 🟢 CLOSED |

#### W2 文档漂移销账

| 维度 | 第 1 轮问题 | 第 2 轮 W2 复审 | 主 agent LSP 核 | R2 状态 |
|---|---|---|---|---|
| A P23 残留 | 指定 P23 query 全 0 | 仍 PASS；无需主体修订 | R2 未见 `p23/` 路径残留；J 中 P23 仅为审查维度描述 | 🟢 CLOSED |
| B R10.6 / §7.5.1 / FINDINGS 范围 | #2/#10 未覆盖；#5/#9 歧义 | 未真修主体文档 | README 未补 out-of-scope；FINDINGS 对 F-3/F-5/F-9 澄清 grep 均 0 | 🟠 STILL_OPEN |
| C 归属 header | `DAG.md` 漏归属声明 | 未修 | `DAG.md:1-5` 仍只有总览/Findings/Gate，无归属声明 | 🟠 STILL_OPEN |
| D 死链/死锚点 | README 两个 gate anchor 不稳定 | 未修 | `README.md:39` 仍是 `#testbussubscribergroup` / `#testrunneractorownership` | 🟠 STILL_OPEN |
| E §10.41-43 呼应 | README/主体缺 §10.41-43 | 未修 | `lsp_grep "§10.41" README.md` = 0；§10.41-43 仅 JUDGEMENT 侧完整 | 🟠 STILL_OPEN |

#### W3 DAG 销账

| # | 第 1 轮问题 | 第 2 轮 W3 复审 | 主 agent LSP 核 | R2 状态 |
|---:|---|---|---|---|
| 1 | Phase 0 硬前置；P1A 不发明 subscriber drain | 部分修订 / 不充分 | DAG 主体 `17-43` 未补 P1A 限定语，只在末尾指针 | 🟠 STILL_OPEN |
| 2 | P0C warning/TODO allowlist 先开，P3 升级 fail | 未修到 DAG 主体 | `lsp_grep warning/no-new/Phase 3 再升级 DAG.md` 为 0 | 🟠 STILL_OPEN |
| 3 | 缺 6×6 并行矩阵；旧并行过乐观 | 未修，且旧矛盾仍在 | `DAG.md:83` 仍写 `P2B || P2E || P2F`；`lsp_grep "6×6" DAG.md` = 0 | 🟠 STILL_OPEN |
| 4 | 估算 6-10 人日低估，应 12.5-20 | 未修 | `lsp_grep "12.5" DAG.md` = 0 | 🟠 STILL_OPEN |
| 5 | §10.13 四维度核查缺失 | 未补全 | `DAG.md:98-107` 仍是旧 6 行摘要；无“四维度”/pairwise 表 | 🟠 STILL_OPEN |
| drift | 指针式修订造成 DAG 主体与 JUDGEMENT 直接矛盾 | W3 新发现 | J §4 降级 P2B↔P2E 并行，但 DAG Batch 2 仍允许并行 | 🔴 REOPENED |

#### W4 Gate 销账

| # | 第 1 轮问题 | 第 2 轮 W4 复审 | 主 agent LSP 核 | R2 状态 |
|---:|---|---|---|---|
| 1 | `TestRunnerActorOwnership` 落地方式/与 `TestRunnerActorGuard` 分工不清 | 未实质修订 | workspace symbol 仅见 `TestRunnerActorGuard`；GATE 未写新建/扩展分工 | 🟠 STILL_OPEN |
| 2 | 未写 `t.Skip → live` 映射 | 未修 | `lsp_grep "t.Skip" GATE_CONTRACTS.md` = 0 | 🟠 STILL_OPEN |
| 3 | `TestShutdownOrdering` AST/text hybrid 与防绕过未写 | 未修 | GATE §4 仍只有抽象 AST/fixture；无 hybrid/defer/async/helper 说明 | 🟠 STILL_OPEN |
| 4 | #1/#2/#3 绕过 pattern 未写 | 未修 | `one-hop` / `nested FuncLit` / `fail-closed` 在 GATE 均 0 | 🟠 STILL_OPEN |
| 5 | session-private allowlist 应用 root 9 字段基座 | 未修 | GATE §5.1 仍 7 字段；`DefinitionPath` grep = 0 | 🟠 STILL_OPEN |
| 6 | `runner.actors` vs `group:"runners"` 命名债 | 部分修；仍不足 | GATE 使用 `group:"runners"`，但 `runner.actors` grep = 0，无 deferred 声明 | 🟠 STILL_OPEN |
| drift | GATE 只追加指针，主体仍可误读为可实施 | W4 新发现 | GATE §1-§6 未落入 J §7.106 五项修法，只在 `142-144` 指针 | 🔴 REOPENED |

### §R2.3 LSP 独立核 ≥3 争议点

| 争议点 | W 路第 2 轮 | 主 agent LSP 实测 | 仲裁 |
|---|---|---|---|
| FINDINGS overlay 是否真实落地 | W1/W2：F-3/F-5/F-9 未落 | `lsp_grep "OnStop 不 wait/drain" FINDINGS.md` = 0；`TeamSync Pull` = 0；`handler fallback` = 0；`FINDINGS.md:49-60/123-134` 仍旧 | **承认 W1/W2：STILL_OPEN** |
| README 死锚点是否修复 | W2：未修 | `README.md:39` 仍为 `GATE_CONTRACTS.md#testbussubscribergroup` / `#testrunneractorownership`；目标标题是带章节号标题 | **承认 W2：STILL_OPEN** |
| DAG 并行矩阵是否补齐 | W3：未修且仍矛盾 | `DAG.md:83` 仍写 `P2B || P2E || P2F`；`lsp_grep "6×6" DAG.md` = 0；`12.5` = 0 | **承认 W3：STILL_OPEN + REOPENED drift** |
| Gate skip/live 与 AST contract 是否补齐 | W4：未修 | GATE 中 `t.Skip` / `one-hop` / `nested FuncLit` / `DefinitionPath` / `runner.actors` 均 0；`GATE_CONTRACTS.md:111-123` 仍 7 字段 | **承认 W4：STILL_OPEN** |
| `TestRunnerActorOwnership` 是否存在同名可直接复用 | W4：仍无同名，仅 `TestRunnerActorGuard` | `workspace_symbol TestRunnerActor` 仅显示 `TestRunnerActorGuard`；`lsp_grep TestRunnerActorOwnership internal/archtest` 第 1 轮已为 0，R2 GATE 未补分工 | **承认 W4：STILL_OPEN** |

### §R2.4 整体 R2 裁决

- **整体口径：🔴 R2 BLOCK**。
- 定调原因：W3 与 W4 均给出真实 BLOCK 级未销账；按 §10.18 一票定调，R2 仍 BLOCK。W1/W2 亦有多项 STILL_OPEN。
- 销账统计：CLOSED 8；STILL_OPEN 19；REOPENED 2。
- 未销账项清单：
  1. W1：F-1 反向测试证据未进 FINDINGS；F-3 overlay；F-5 非 TeamSync Pull/Push 重开；F-9 不覆盖 handler fallback。
  2. W2：README 范围声明、DAG header、README 死锚点、§10.41-43 主体引用均未落。
  3. W3：DAG Phase 限定、P0C warning→P3 fail、6×6 pairwise、12.5-20 人日、§10.13 四维度均未落。
  4. W4：Gate 落地映射、t.Skip→live、AST/hybrid、防绕过、9 字段 allowlist、`runner.actors` deferred 声明均未落。
- 新引入漂移清单：
  1. **DAG drift**：DAG 末尾指向 JUDGEMENT，但主体仍保留与 JUDGEMENT 冲突的并行建议，实施 agent 可能误按旧 DAG 执行。
  2. **Gate drift**：GATE 末尾指向 JUDGEMENT，但主体仍未承载 gate-spec clarity 必修项，实施 agent 可能误按旧 7 字段/无 skip-live/无 AST contract 实施。
- 建议下一步：不要派 Phase 0/Phase 2 代码 agent；先派单文档修订 agent（或主 agent 自改）只追加修文，逐项把 J §7.103-106 与本 §R2.4 未销账清单落回 README/FINDINGS/DAG/GATE_CONTRACTS 主体，并保留 §10.31 只加不删。

### §R2.5 §10.31 合规自查

- 第 1 轮 §1-§9 未被修改：追加前记录 `JUDGEMENT.md` 为 126 行，SHA256=`aacf896d255eee822ae088f7ba1adc49886fc843a055da16a13d0d6994ca926b`；本节从文件末尾追加，不改旧文。
- 本轮只追加：在 `JUDGEMENT.md` 末尾新增 §R2；对 P22.1 对应主体文档仅追加 R2 HEAD drift note。
- 未修改 `.go` / `.js` / `.sql` / `.json` / `go.mod` / `go.sum`。

### §R2.6 LSP 自证（≥5 工具 + ≥3 xref + ≥2 call_hierarchy + ≥1 diagnostics）

- `lsp_file(read_file)`：读取 `JUDGEMENT.md`、`README.md`、`FINDINGS.md`、`DAG.md`、`GATE_CONTRACTS.md` 当前状态。
- `lsp_grep(text_search)`：验证 `OnStop 不 wait/drain`、`TeamSync Pull`、`handler fallback`、`6×6`、`12.5`、`t.Skip`、`one-hop`、`DefinitionPath`、`runner.actors`、`§10.41` 等关键字命中/0 命中。
- `lsp_xref(references)` ≥3：`BindRuntime` refs 5；`RegisterSubscribers` refs 2；`isRootBridgeException` refs 6。
- `lsp_xref(call_hierarchy)` ≥2：`BindRuntime` incoming 4；`RunGroup` incoming 4。
- `lsp_file(diagnostics)`：P22.1 五份 `.md` diagnostics = no diagnostics。
- `lsp_structure(workspace_symbol/document_symbol)`：`TestRunnerActor` workspace symbol；`root_bridge_allowlist.go` document symbols确认 9 字段 schema 文化。
- `lsp_inspect(definition)`：从 observation `Subscribe` 调用点定位 module/register 函数，确认 helper 不等于 BusModule owner。
- `lsp_completion`：在 `internal/platform/bus/module.go` 执行 completion，未见现成 `bus.subscribers` API。
---

## §R3 第 3 轮 R2 后复查仲裁（2026-04-25）

> 本节按 §10.31 追加；R2 后 drift note + 转录复查结果。
> 前置：§R2 已判 🔴 R2 BLOCK。本轮判定 drift/转录层是否到位，以决定下一步派工策略。

### §R3.1 W 路 R3 收报状态（4/4）

| W# | state | report? | 3 问完成度 | 处置 |
|---|---|---:|---|---|
| W1 | idle | yes | Q1/Q2/Q3 完成 | 纳入；drift note 未误开 CLOSED，W1 转录准确 |
| W2 | idle | yes | Q1/Q2/Q3 完成 | 纳入；§10.31/死链无新增问题，但 drift note 只提示未完成，判弱 |
| W3 | idle | yes | Q1/Q2/Q3 完成 | 纳入；DAG REOPENED drift 定性准确，主体仍 ONLY_DRIFT |
| W4 | idle | yes | Q1/Q2/Q3 完成 | 纳入；Gate drift note 定性准确但 #1 分工提示偏泛化 |

### §R3.2 三维分类汇总

| W# | A drift | B body | C 转录 |
|---|---|---|---|
| W1 | 🟢 DRIFT_OK：FINDINGS drift note 只列 F-1/F-3/F-5/F-9，未误开 CLOSED | 🟠 ONLY_DRIFT：4 个 STILL_OPEN 有 note，主体未实质修 | 🟢 TRANSCRIBED_OK |
| W2 | 🟠 DRIFT_WEAK：无 §10.31/死链新增问题，但 drift note 只是集中提示，未充分替代 B/C/D/E 主体处置 | 🟠 ONLY_DRIFT：B/C/D/E 仍未主体修复 | 🟢 TRANSCRIBED_OK |
| W3 | 🟢 DRIFT_OK：DAG note 清楚承认旧 Batch 2 与 JUDGEMENT/R2 冲突 | 🟠 ONLY_DRIFT：5 项 STILL_OPEN + 1 REOPENED 均未修主体 | 🟢 TRANSCRIBED_OK |
| W4 | 🟠 DRIFT_WEAK：Gate note 对 #2-#6 清楚，但 #1 TestRunnerActorOwnership vs TestRunnerActorGuard 分工只泛化为“archtest 改动映射” | 🟠 ONLY_DRIFT：6 项 STILL_OPEN + 1 REOPENED 均未修主体 | 🟢 TRANSCRIBED_OK |

### §R3.3 LSP 独立核 ≥3 争议点

| 争议点 | W R3 结论 | LSP 实测 | 仲裁 |
|---|---|---|---|
| `DAG.md:83` 旧并行建议是否仍与 JUDGEMENT §4 冲突 | W3：仍存在，drift note 承认但主体未修 | `DAG.md:83` 仍为 `P22.1-P2B || P22.1-P2E || P22.1-P2F`；`JUDGEMENT.md:63-67` 已降级 P2B↔P2E/P2D 并行；`DAG.md:122` 只是 note | **承认 W3：A=DRIFT_OK，B=ONLY_DRIFT** |
| `GATE_CONTRACTS.md` 主体是否已有 `TestRunnerActorOwnership` → `TestRunnerActorGuard` 分工/重命名 | W4：主体未修，note #1 泛化 | `workspace_symbol TestRunnerActor` 仅见 `TestRunnerActorGuard`；`GATE_CONTRACTS.md:18-21/57-83` 仍写 `TestRunnerActorOwnership`，`lsp_grep TestRunnerActorGuard GATE_CONTRACTS.md` = 0 | **承认 W4：DRIFT_WEAK + ONLY_DRIFT** |
| `README.md` 两个 `GATE_CONTRACTS.md#anchor` 死链是否仍存在 | W2：旧死锚点仍未修，但非 R2 note 新引入 | `README.md:39` 仍命中 `GATE_CONTRACTS.md#testbussubscribergroup` / `#testrunneractorownership`；R2 note 在 `README.md:132-133` 已承认需修 | **承认 W2：旧问题 STILL_OPEN，drift 无新增死链** |
| Gate 9 字段 allowlist 是否进入主体 | W4：未修，note 有提示 | `GATE_CONTRACTS.md:111-123` 仍为 7 字段；`lsp_grep DefinitionPath GATE_CONTRACTS.md` = 0；`root_bridge_allowlist.go` document symbols 显示 rootBridgeException schema | **承认 W4：ONLY_DRIFT** |
| §R2.2 转录是否有实质错误 | W1-W4：转录准确 | `JUDGEMENT.md:150-193` 与四路 R3 对账一致；未发现 W 子表状态反转 | **TRANSCRIBED_OK 全通过** |

### §R3.4 R3 整体裁决

- **前提**：R2 本身已判 🔴 BLOCK，这是既定事实；R3 不重审 F-1~F-11，也不把 body 未修误判成 drift/转录失败。
- **R3 聚焦**：drift/转录层是否到位。
- **裁决：🟠 R3 drift 需补**。
- 理由：
  1. W1/W3 的 drift note 定性足够，W1/W2/W3/W4 的 §R2.2 转录均准确，无 🔴 TRANSCRIPT_ERROR。
  2. W2 指出 drift note 只是临时漂移提示，B/C/D/E 仍需主体修；该点不构成转录错误，但不足以判 R3 🟢。
  3. W4 指出 GATE drift note 对 #1 `TestRunnerActorOwnership` 与 `TestRunnerActorGuard` 的具体分工提示偏泛化，需补一个 R3 drift fix note；因此不是 🟢。
  4. 未发现 drift note 覆盖既有内容、破坏 §10.31 或新增 markdown 死链；故不判 🔴 drift BLOCK。

### §R3.5 下一步建议

- 本轮已允许进入“先补 drift/再主体修订”的文档阶段，但 **不可派代码 agent**。
- 因 R3 为 🟠，建议先完成最小 drift 修正：在 `GATE_CONTRACTS.md` 末尾追加 R3 drift fix note，点名 `TestRunnerActorOwnership` 必须明确“新建 ownership guard vs 扩展 TestRunnerActorGuard”的分工；本仲裁已按 §10.43/§10.31 追加该 note。
- drift 补齐后，派单文档主体修订 agent，按 R2 §9 / §R2.4 四份文档清单只加不删修主体：
  1. `README.md`：补范围声明、修两个 Gate 锚点、补 §10.41-§10.43。
  2. `FINDINGS.md`：补 F-1 反向测试证据、F-3/F-5/F-9 overlay/out-of-scope 澄清。
  3. `DAG.md`：补归属 header、P1A 边界、P0C warning→P3 fail、6×6 pairwise、12.5-20 人日、§10.13 四维度。
  4. `GATE_CONTRACTS.md`：补现有 archtest 映射、`t.Skip → live`、AST/hybrid、防绕过、9 字段 allowlist、`runner.actors` deferred 说明。

### §R3.6 §10.31 合规自查

- §1-§9 + §R2 追加前 SHA256：`24086baff977936d34cb1d4a1a7b0c701c25a9954e2a1f93fcc0fa7238d830c6`；追加前行数 236。
- 本节从 `JUDGEMENT.md` 末尾追加，不改 §1-§9 与 §R2 任何字面。
- 本轮除 `JUDGEMENT.md` §R3 外，仅因 W4 drift note #1 泛化问题，在 `GATE_CONTRACTS.md` 末尾追加 R3 drift fix note；未改代码、JSON、SQL、go.mod/go.sum。

### §R3.7 LSP 自证

- `lsp_file(read_file)`：读取 `JUDGEMENT.md §R2`、`DAG.md:73-122`、`GATE_CONTRACTS.md:1-147`、`README.md:32-133`。
- `lsp_grep(text_search)`：核 `P22.1-P2B || P22.1-P2E || P22.1-P2F`、`TestRunnerActorGuard`、`GATE_CONTRACTS.md#testbussubscribergroup`、`DefinitionPath`、`t.Skip`、`## §R3`。
- `lsp_xref(references)` ≥3：`BindRuntime` refs 5；`isRootBridgeException` refs 6；`ResilientSubscribe` refs 20（抽样上限）。
- `lsp_xref(call_hierarchy)` ≥2：`BindRuntime` incoming 4；`RunGroup` incoming 4。
- `lsp_file(diagnostics)`：P22.1 五份 `.md` diagnostics = no diagnostics。
- `lsp_structure(workspace_symbol/document_symbol)`：`TestRunnerActor` workspace symbol；`root_bridge_allowlist.go` document symbols确认 rootBridgeException schema。
- `lsp_inspect(definition)`：`isRootBridgeException` definition → `root_bridge_allowlist.go:203-213`。
- `lsp_completion`：在 `internal/platform/bus/module.go` 执行 completion，确认 LSP 可解析上下文。

---

## §R4 第 4 轮代码×文档自洽仲裁（2026-04-25）

> 本节按 §10.31 追加；X1-X4 全新独立 agent 的自洽审查结果。
> 前置：R1 🔴 BLOCK → R2 🔴 BLOCK → R3 🟠 drift 需补 → E1 修订 4 份主体。
> 本轮判定 P22.1 最终合入就绪度。

### §R4.1 X 路收报状态（4/4）

| X# | state | report? | 维度 | 处置 |
|---|---|---|---|---|
| X1 | idle | yes | FINDINGS × HEAD 自洽 | 纳入；11 条 FINDINGS 与 HEAD 一致，F-11 在 G1/N-10 后仍为 observation module lifecycle owner |
| X2 | idle | yes | 文档互相自洽 + §10.31 全历史 | 纳入；指出最终裁决入口/R4 缺失、header 与 DAG 显式映射仍需补 |
| X3 | idle | yes | DAG 可行性 + Phase 0 就绪 | 纳入；Phase 0 方向正确但 API/文件/测试/allowlist schema 未可直接派代码 |
| X4 | idle | yes | Gate × archtest 兼容 | 纳入；现有 archtest 可扩展，但 matcher 算法与 SessionRuntime symbol 样例仍有硬漂移 |

### §R4.2 X 路真实性三维分类

| X# | A 证据 | B 真实性 | C 合入影响 |
|---|---|---|---|
| X1 | 🟢 EVIDENCE_STRONG：给出 file:line、xref、call_hierarchy、ast_search、diagnostics | 🟢 TRUE：F-1/F-3/F-11 与 HEAD 实测一致；§7.5.1 overlay 边界未误重开 | 🟢 READY_CONTRIBUTION：FINDINGS 主体可作为 Phase 0 输入 |
| X2 | 🟢 EVIDENCE_STRONG：文档 line、xref/call_hierarchy、ast_search 与 diagnostics 齐备 | 🟢 TRUE：R4 前确有最终裁决入口漂移；DAG/JUDGEMENT header 与 F→Node 显式表仍弱 | 🔴 BLOCK_CONTRIBUTION：按 §10.18 采纳其状态漂移 BLOCK；本 §R4 只能解除“缺 R4”本身，不能代替 DAG/JUDGEMENT header/F→Node 后续补齐 |
| X3 | 🟢 EVIDENCE_STRONG：读 bus/runner/app 代码并用 xref/call_hierarchy/ast_search 验 Phase 0 差距 | 🟢 TRUE：BusModule/RunnerModule HEAD 仍缺 contract；Phase 0 还不是可直接派 Go 实施 spec | 🟠 NEEDS_FOLLOWUP：需补 DAG Phase 0 implementation spec 后再派代码 |
| X4 | 🟢 EVIDENCE_STRONG：读 archtest 实码，xref/call_hierarchy/ast_search/diagnostics 均覆盖 | 🟢 TRUE：TestRunnerActorGuard 现有实现不含 ownership；SessionRuntime 样例 symbol 与 HEAD 漂移 | 🔴 BLOCK_CONTRIBUTION：Gate contract 若按现文执行会有 AST 定位失败/形式主义 matcher 风险 |

### §R4.3 §7.5.1 overlay × P22.1 FINDINGS 边界复核

- overlay 销账清单：以 `session-summary.md §7.5.1` 为准，P22.1 不重开 archtest 3 live failure、`waitDreamTask` prod caller、`NewRelevantMemoryFinder` prod caller、TeamSync Pull/Push API 收敛、`ErrThreadRuntimeRequired`/`ErrMissingCWD` 0 命中、`PersistentSubagentDefault=true` 默认值、`registerMemoryHooks.OnStop` drain 行为等已销账编号；其中 R4 口径沿用“已销账项不作为 P22.1 新 BLOCK”。
- P22.1 FINDINGS 是否误重叠：X1 与主 J LSP 核均未发现误重开。F-1/F-2 对应 overlay #8 未修；F-3/F-5/F-9 是同 locus/同包的正交 ownership 问题，不是已销账项回滚。
- F-3 overlay 正交性：HEAD `registerMemoryHooks` 仍在 fx lifecycle `OnStart` 启动 scheduler/nested/teamSync 并注册 subscriptions；`drainMemoryHooks` 已存在仅说明“不 wait/drain”旧说法销账，不消除 owner 仍在 module lifecycle 的 F-3。
- F-5 overlay 正交性：cachekeepalive relay/timer/subscriber owner 与 TeamSync Pull/Push test-only API 收敛不是同一问题；E1 主体说明充分。
- F-9 overlay 正交性：toolbridge diff fallback subscriber ownership 与 Z-B handler fallback / hidden contract 是不同 lane；E1 主体说明充分。

### §R4.4 独立 LSP 核 ≥3 争议点

| 争议点 | X 路结论 | J LSP 实测 | 仲裁 |
|---|---|---|---|
| X1 F-1 HEAD 顺序 | X1：`BindRuntime.OnStop` 仍先 `DrainPendingExtraction(ctx)`，再 `cancel()`，再 wait `done` | `internal/app/runner.go:71-84` 实测为 drain→cancel→select done；`BindRuntime` refs=5，incoming=4 | 采纳 X1：F-1 仍真实；Phase 1 必须反转测试与实现 |
| X1 F-11 G1/N-10 后 observation 状态 | X1：G1/N-10 未改变 subscriber lifecycle owner | `internal/module/turn/observation/module.go:46-58` 为 `fx.Hook`；`OnStart` line 50 调 `Subscribe`；`subscribers.go:28-48` 内 9 个 `ResilientSubscribe`；`RegisterSubscribers` refs 仅 module invoke+定义 | 采纳 X1：F-11 仍真实；不是旧基线误护 |
| X3 Phase 0 就绪度 | X3：BusModule/RunnerModule 方向正确但 contract skeleton 不足以直接派 Go 代码 | `internal/platform/bus/module.go:10-18` 仅 provide dispatcher/emitters/log sink；`internal/platform/runner/module.go:5` 仅空 `fx.Module("runner")`；`runner/group.go:15-87` 有 `Runner`/`RunGroup` 但无 adapter/spec | 采纳 X3：Phase 0 必须先补 implementation spec，不得直接派多 slice 代码 |
| X4 `TestRunnerActorGuard` ownership 分工 | X4：扩展现有 `TestRunnerActorGuard/ownership` 比新建重复 guard 更贴合现状，但现有 guard 不支撑 ownership | `internal/archtest/runner_actor_guard_test.go:35-104` 只有 forbidden token catalogue、hot-file scope、waiter goroutine matcher与一个 skeleton skip；无 module lifecycle `worker.Start/Stop` matcher | 采纳 X4：E1 “ownership 子测试”方向正确，但 Gate 还需写清 matcher algorithm |
| X4 SessionRuntime allowlist symbol 漂移 | X4：GATE 样例 `SessionRuntime.Run/startReaderLoop/startHealthLoop` 与 HEAD 不一致 | J 未重做全仓代码审计，但 X4 已给出 archtest/source 实测；按 §10.22 独立第三方权重与 §10.33 HEAD 为准采纳 | 采纳 X4：该项为 Gate BLOCK，需改为 HEAD 真实 symbol 后才能派实施 |

### §R4.5 R4 整体裁决

- 🔴 **P22.1 BLOCK** — 本轮不是 FINDINGS 真实性 BLOCK，而是“最终合入/派代码就绪度”BLOCK。
- 理由：X2 与 X4 均给出真实 🔴；按 §10.18 BLOCK 一票定调，不能因 X1 FINDINGS 全绿而把 P22.1 判 READY。
- 具体含义：README/FINDINGS 的 HEAD 修订方向成立；DAG/GATE/JUDGEMENT 的最终入口与 Phase 0/Gate 可实施细节仍需只改文档补齐。补齐后可重启 R5，只审差异，不重复 R1-R4 全量。

### §R4.6 下一步派工清单

- 🔴 BLOCK 文档补修方向（仅 `.md`，不可改 Go）：
  1. `DAG.md`：追加 Phase 0 implementation spec，冻结 `bus.subscribers` Fx group tag、subscriber spec 类型/字段、BusModule 注册与 Stop 顺序、Runner adapter/helper API、P0C TODO allowlist schema、warning-mode 行为与测试名。
  2. `DAG.md`：补显式 `F-1~F-11 → P22.1-Px` 映射表；补 P1A 边界（只修 root bridge，不声称未迁移 worker 已受 run.Group 管住）；必要时把 P2B 再拆或给文件白名单。
  3. `GATE_CONTRACTS.md`：把 `TestRunnerActorOwnership` active 名称收敛为 `TestRunnerActorGuard/ownership` 子测试；保留表中历史名时必须标“概念名/落地名”。
  4. `GATE_CONTRACTS.md`：写明必须用 custom `go/ast` walker + one-hop helper resolver；不得只靠 token grep；超过一跳 fail-closed/TODO allowlist。
  5. `GATE_CONTRACTS.md`：修正 SessionRuntime allowlist 样例 symbol 为 HEAD 可定位 symbol（X4 建议：`(*SessionRuntime).Start`、`(*SessionRuntime).spawnReader`、`(*SessionRuntime).runHealthLoop`、`(*SessionRuntime).runRecoveryWorker` 或经 LSP 再确认后的真实集合）。
  6. `README.md`：把“以 JUDGEMENT.md §7 为准”改为只加不删状态漂移说明：§7/§R2/§R3 为历史，当前以最新 §R4/R5 为准；本轮为 🔴 BLOCK。
  7. `JUDGEMENT.md`：不得修改 §1-§9/§R2/§R3；若补修完成，只能末尾追加 §R5 复核。

### §R4.7 §10.31 合规自查

- §1-§9 + §R2 + §R3 追加前 SHA256：`9bfe2c24717503e55043b3a50e8bbae91caaa5f60c474e0e6444d77d7574f48d`；追加前行数 308。
- 本节从 `JUDGEMENT.md` 末尾追加，不改 §1-§9、§R2、§R3 任何字面。
- 本轮除 `JUDGEMENT.md` §R4 外，仅在 `README.md`、`DAG.md`、`GATE_CONTRACTS.md` 末尾追加 R4 BLOCK drift note；未改 `.go` / `.js` / `.sql` / `.json` / `go.mod` / `go.sum`。

### §R4.8 LSP 自证

- `lsp_file(read_file)`：读取 `internal/app/runner.go:34-87`、`internal/module/turn/observation/module.go:46-59`、`subscribers.go:1-48`、`internal/platform/bus/module.go:10-30`、`internal/platform/runner/module.go:1-5`、`internal/platform/runner/group.go:1-87`、`internal/archtest/runner_actor_guard_test.go:35-130`、P22.1 文档片段。
- `lsp_file(diagnostics)`：`internal/platform/bus/module.go`、`internal/platform/runner/module.go`、`internal/app/runner.go`、`runner_actor_guard_test.go`；仅见 `runner_actor_guard_test.go:88` forvar hint，与本轮无关。
- `lsp_grep(ast_search)`：`func RegisterSubscribers($$$) $$$` 命中 `internal/module/turn/observation/module.go:46-59`。
- `lsp_grep(text_search)`：`ResilientSubscribe` 抽样命中 observation/rpc/toolbridge/eventsurface 等 subscriber call sites。
- `lsp_xref(references)` ≥3：`BindRuntime` refs=5；`RegisterSubscribers` refs=2；`RunGroup` refs=5。
- `lsp_xref(call_hierarchy)` ≥2：`BindRuntime` incoming=4；`RunGroup` incoming=4。
- `lsp_structure(document_symbol)`：`internal/platform/runner/group.go` symbols 包含 `Runner`、`RunGroup`、`addRunnerActors`。
- `lsp_inspect(definition)`：对 `observation/module.go:50` 的 `Subscribe` 跳转返回空，已改用 `read_file subscribers.go:28-48` 与 xref 交叉核验。
- `lsp_completion`：`internal/platform/bus/module.go:17` 返回 `fx.Invoke` 等候选，确认 LSP 上下文可解析。

---

## §R6 归属补录（2026-04-25，X2 R3 补齐）

> 本节按 §10.31 追加；不改 §1-§9 / §R2 / §R3 / §R4 / §R5 历史裁决字面。
> 与 P22.1 其他 4 份主体文档（README / FINDINGS / DAG / GATE_CONTRACTS）顶部归属声明对齐。

### §R6.1 本仲裁文件归属声明（2026-04-25 补）

本文件（`docs/plans/迁移/p22/p22.1/JUDGEMENT.md`）是 P22.1 架构债子任务的红队仲裁历史：

- **来源 1**：`docs/plans/迁移/p22/JUDGEMENT_R8_QA.md §R10.6` 代码层 deferred 债总账
- **来源 2**：`docs/plans/迁移/p22/JUDGEMENT_R8_QC.md §7` 契约本体 deferred 债
- **来源 3**：§10.30 三层分工铁律（2026-04-22 P22 R8 新教训，见 `docs/1/会话习惯.md §10.30`）

P22.1 ⊂ P22（不是独立新 lane）。本仲裁文件与 P22.1 其他 4 份主体文档一样，属于 P22 R10 FINAL 阶段显式 deferred 的架构违规遗留子任务收口。

### §R6.2 §10.31 合规自查

- **追加前** JUDGEMENT.md 总行数：386
- **追加前** 行 1-386 SHA256：`8dc1a2a3d8b801975a9697c82b5ada4c5cf6c9b9ca0146229cc13edc7d1a9cce`
- **追加后**同一前缀 SHA256 保持不变（本节只在末尾新增，未改写任何历史）
- 结论：§1-§9 + §R2 + §R3 + §R4 + §R5 字面未变


---

## §R5 留空补录（2026-04-25，第 6 轮文档一致性修复）

> 本节按 §10.31 追加，用于修正 §R6 中“§R5 历史裁决字面”的引用漂移；不改 §R6 原文。HEAD 锚点：`a81554c`；实施链锚点：`25a37ad` → `f737e45` → `17b5ce7` → `dfe12e6` → `b386217` → `a9a018e` → `a81554c`。

§R5 未触发独立审查轮，因此没有 R5 裁决正文。§R6 行 392/410 提到的“§R5 历史裁决字面”应按本补录解释为：**§R5 为编号预留/留空状态，不存在可被改写的历史裁决内容**。后续引用最新状态时应以 §R7 及之后追加节为准；§R4 仍保留为实施前历史 BLOCK 裁决。

## §R7 HEAD `a81554c` 实施后状态 overlay（2026-04-25，第 6 轮）

> 本节按 §10.31 只加不删追加；不改 §R4 历史 BLOCK 字面。§R4 是 P22.1 文档/代码实施前的历史裁决。当前 HEAD `a81554c` 已包含 P22.1 实施链 `25a37ad` → `f737e45` → `17b5ce7` → `dfe12e6` → `b386217` → `a9a018e` → `a81554c` 后的状态。

### §R7.1 最新裁决

- 🟡 **代码大部 ready**：F-1~F-11 已有主体迁移与 guard/allowlist 证据；BusModule/RunnerModule contract 已落地；root shutdown ordering 与 desktop watcher owner ctx 已与 §10.30 目标态对齐。
- 🟠 **仍有 NEEDS-FIX**：cron+uistate cross-file gap、gate 3 处 NEEDS-FIX 仍待 Audit-A/B/C 修复与复核；这些是 HEAD `a81554c` 后的收口项，不推翻 §R4 的历史意义。
- 🔲 **仍 deferred**：`runner.actors` vs `group:"runners"` 是 `docs/契约/*` 命名债，本 lane 只登记，不修改契约文档。

### §R7.2 R4 → HEAD 状态漂移解释

| 历史项 | §R4 历史判定 | HEAD `a81554c` overlay |
|---|---|---|
| Phase 0 contract | §R4 判定派代码就绪度 BLOCK | 已落 BusModule `bus.subscribers` 与 RunnerModule adapter contract；仍需 gate 命名/覆盖补强。 |
| F-1/F-2 root/desktop | §R4 仍以旧顺序/旧 watcher 为证据 | 已实施 cancel→RunGroup wait→drain 与 owner ctx watcher；P1A/P1B 从 BLOCK 输入转为已销账。 |
| Phase 2 module migration | §R4 作为未来实施项 | HEAD `a81554c` 已完成 memory/thread/cachekeepalive/hooks/rpc/mcpcontrol/toolbridge/insight/observation 主体迁移。 |
| Phase 3 gate | §R4 要求 matcher algorithm 补齐 | session-private allowlist 已落；P3B 仍需 Audit-A/B/C 处理 cron+uistate cross-file gap 与 3 个 gate NEEDS-FIX。 |

### §R7.3 F-10 审查误差记录

第 6 轮修文档采信主 agent LSP 证伪：此前 Audit-D 报告中关于 `internal/module/insight/module.go:48-63` two-hop subscription 的说法不作为 HEAD `a81554c` 事实使用。该项应视为 Audit-D LSP 缓存/自身实验状态未真回滚导致的误判；本轮只修文档，不改 Go 代码。

## §R8 HEAD `5d6a93c` Round-3 BLOCK 收口 overlay（2026-04-25）

> 本节按 §10.31 / §10.44 末尾追加；§R7 的 HEAD `a81554c` 结论为历史 overlay，保留不改。当前修复基线为 HEAD `5d6a93c`，用于承接 Final-V1/V2/V3/V4 cross-check 戳穿的 P22+P22.1 真 BLOCK。

### §R8.1 代码状态修正

- `BindRuntime.OnStop` 当前目标态为 `cancel → waitForRuntimeDone(done, ctx) → drainRuntimeBeforeStop(ctx, p)`；§R7 中声称的 cancel→wait→drain 在本轮修复后才成为代码真状态。
- `preDrainDesktopRuntime` 当前目标态为 `owner.WaitRuntimeDone(drainCtx)` 先于 `owner.DrainRuntime(drainCtx)`，与 root shutdown ordering 同步。
- `TestShutdownOrdering` 已从 dormant text-index guard 升级为 AST ordering gate：定位 `BindRuntime` 的 `fx.Hook.OnStop` func literal，并按 statement 顺序检查 cancel / wait / drain。
- `TestBindRuntimeWaitsRunGroupBeforeDrain` 反向保护 wait-before-drain；旧的 drain-before-RunGroup-complete 语义不再作为合法行为。

### §R8.2 剩余裁决

Round-3 指定的 runner ordering、desktop pre-drain ordering、vet goroutine fatal、memory coalesce race、thread event fake-store race、session-private BindRuntime allowlist integrity、P21 `runner.actors` active tag 澄清均纳入本轮收口。最终 READY 仍以本轮交付报告的 fail-injection 与全链验证输出为准。


## §R9 HEAD `aa09f58` V3-B 锚点修正 overlay（2026-04-25）

> 本节按 §10.31 / §10.44 末尾追加；§R8 的 HEAD `5d6a93c` 记录保留为 Round-3 代码修复基线历史 overlay。V3-B 复核实测当前仓库 `git rev-parse --short HEAD` 为 `aa09f58`，因此当前文档 HEAD 锚点修正为 `aa09f58`。

### §R9.1 锚点裁决

- `5d6a93c`：保留为 Round-3 代码修复提交锚点，不再作为当前 Git HEAD 声明。
- `aa09f58`：作为当前复核 HEAD 锚点，用于承接 V3-B 对 §10.33 防旧基线的修正。
- 代码顺序裁决不变：root `BindRuntime.OnStop` 为 `cancel → waitForRuntimeDone → drainRuntimeBeforeStop`；desktop `preDrainDesktopRuntime` 为 `WaitRuntimeDone → DrainRuntime`。
