# P22 裁决书（静态层 / Judge-S）

⚠️ 终裁：只改不审

> 生成时间：2026-04-23
> 裁决范围：文档侧（术语 / scope / 叙事 / 密度 / 冗余章节）
> 姐妹裁决：`JUDGEMENT_DYNAMIC.md`（Judge-D，代码锚点层）

## 1. 收报告状态表
| Agent | State | 收到 | 主要 finding 数 |
|---|---|---:|---:|
| D1 README | idle | ✅ | 5 |
| D2 P0 | idle | ✅ | 5 |
| D3 P1a | idle | ✅ | 5 |
| D4 P1b | idle | ✅ | 4 |
| D5 P1c | idle | ✅ | 7 |
| D6 P2 | idle | ✅ | 5 |
| D7 P3 | idle | ✅ | 5 |
| D8 P4 | idle | ✅ | 5 |
| C1 root-bridge | idle | ✅ | 1 |
| C2 Finding 1,2 | idle | ✅ | 4 |
| C3 Finding 3,4 | idle | ✅ | 3 |
| C4 Finding 5,6,7 | idle | ✅ | 5 |
| C5 Finding 8 | idle | ✅ | 4 |
| C6 toolbridge | idle | ✅ | 4 |
| C7 P2 other | idle | ✅ | 6 |
| C8 archtest | idle | ✅ | 5 |
| A1 contracts vs P22 | idle | ✅ | 7 |
| A2 shutdown flow | idle | ✅ | 5 |
| A3 quad-tree | idle | ✅ | 4 |
| A4 feasibility | idle | ✅ | 5 |

## 2. 共识结论
- ✅ PASS:
  - C1：4 处 root runtime bridge 仍与 P22 allowlist 形态一致；`watchFXShutdown(...)` 不属于 root bridge 本体。
  - C2/C3/C4/C5：Finding 1-8 都是 live debt，不是空架子或误报。
  - D4/D7/D8：`P1b`、`P3`、`P4` 的大方向可做，问题主要在叙事边界与分批方式，而非方案不存在。
- ⚠️ FIX:
  - README：改成 umbrella plan，两条主线分轨，补 `P1c` 顺序、`P2` 多批实施、`P4` phase-B 定位，并明确 app/orch 双树同构、lsp/ida runner-only sidecar（D1/A3/A4）。
  - README/P0：更正契约引用口径；把 `fx.Module / BusModule / RunnerModule / root runtime bridge` 作为主术语，并把 `group:"runners"` 降为现实现 tag，不再冒充契约层最终命名（A1/D2/C8）。
  - P0/P2：显式写明“unsubscribe/退订 ≠ drain”，drain 必须由显式 owner 负责（A2/D2/D6）。
  - P1a：补单一 `PeerSupervisor` owner、降级语义、与 `P1c` 的边界，不再让 peer owner 与 session owner 混写（D3/C2）。
  - P1b：补 restore 留 `fx.Module`、长期 loop 入 `RunnerModule`、callback slow-path 继续归 `P2`（D4/C3）。
  - P1c：从 35 行骨架补到 80 行，钉死 session-owned `SessionRuntime` 为默认方案，并补 `实施方式/收口口径/依赖图/落地顺序/非目标`（D5/A4）。
  - P2：明确其是 umbrella，不是单张 mega PR；补切片化实施、overflow 语义分档、`toolbridge/gopls/bootstrap` 与 `P4` 的分轨边界（D6/C6/C7/A4）。
  - P3：补 local process monitor owner、`stop -> drain -> consume -> return` 顺序、与 `P4` 的 shell/identity/report 边界（D7/C5）。
  - P4：改成窄守卫 + 分域收口 + phase-B 顺序，并接住 `P21` 递延的 signed-skill/native-skill contract 债（D8/A4）。
- 🚫 BLOCK:
  - A1：契约三件套自身仍存在 repo-level 命名债（`runner.actors` vs `group:"runners"`）。本轮已把 P22 文案降到“角色术语 + 现实现 tag”口径，但**契约文档本体未在本轮改动范围内**，因此该 BLOCK 只算部分缓解，仍留 deferred。
- 🗑 DELETE:
  - 无。按 §10.31 做了 self-check 后，本轮不做“顺手精简”。

## 3. 冲突点与裁决
| 冲突点 | Agent A 观点 | Agent B 观点 | 裁决依据 | 最终结论 |
|---|---|---|---|---|
| Runner 命名 | A1：`runner.actors` vs `group:"runners"` 是硬冲突 | D2/C1：当前 root bridge 与多份文档都落在 `group:"runners"` | 契约文字优先，但代码现状不能被文档抹掉 | P22 文档统一回到 `fx.Module / BusModule / RunnerModule` 角色术语；`group:"runners"` 只保留为现实现 tag；repo 级命名统一 deferred |
| “四树同构”还是“双树 + sidecar” | A3：不是四树，而是 app/orch 两棵完整树 + lsp/ida 两个 runner-only sidecar | README 旧文案容易把 4 个 root bridge 写成同构四树 | 以代码 wiring 实况为准 | README/P0 改为：双树同构仅适用于 `internal/app` 与 `cmd/mcp-orch`；`cmd/mcp-lsp` / `cmd/mcp-ida` 不强写成 bus 树 |
| P1c owner 选型 | D5：应优先选 session-owned handle；旧稿“SessionRuntime 或 provider-level runner”太虚 | A4：P1c 过薄，若不钉死 owner 极易变空架子 | §10.11 / §10.23 + caller/stop 路径集中度 | P1c 文案默认选 `session` owns `SessionRuntime`；provider-level registry 只保留为后备索引层，不是默认执行路径 |
| P2 是单计划还是 umbrella | D6/C7/A4：P2 scope 异质性过大，不能单 PR | 旧稿容易读成“一页 = 一次实现” | 写集冲突 + 关闭语义差异 | P2 改成 umbrella 叙事，并补分批顺序与依赖图 |
| `toolbridge` / `orchestration` 归口 | C6：toolbridge 同时命中 P2/P4 | D8：orchestration 同时命中 P3/P4 | 先按问题维度切，再按包名切 | `toolbridge` runtime owner 归 P2、依赖方向/协议归 P4；`orchestration` waiter/exit owner 归 P3、shell/identity/report 归 P4 |
| shutdown 中“退订”含义 | A2：退订不是 drain，根桥等待 RunGroup 也不等于所有隐式 worker 全退 | 旧文案容易把 bus 停派写成库级 shutdown | `kelindar/event` 实际能力 + §10.30 | README/P0/P2 明写“退订只代表 stop intake，drain 由 owner 负责” |

## 4. 已执行修订（本轮改动清单）
- 改：`docs/plans/迁移/p22/README.md` §目标/§依赖图（文本）/§落地顺序建议/§收口口径/§实施方式/§非目标 → 从“单批修复总览”改成“P0-P3 runtime ownership + P4 dependency/hidden contract”双线 umbrella。
  - 前：`把当前仓库里混入 fx、bus 回调和业务 service 的长跑 side effect...`
  - 后：`把 P22 明确成一套 umbrella plan...` / `其中 internal/app 与 cmd/mcp-orch 适用“双树同构”；cmd/mcp-lsp / cmd/mcp-ida 按 runner-only sidecar 处理...`
- 改：`docs/plans/迁移/p22/README.md` 顶部契约引导 → 更正错误锚点。
  - 前：`fx-convention.md §2 +（历史误引死章节号；旧字面串已中断保留）`
  - 后：`modularity-convention.md §4.4 / §7` + `fx-convention.md §2 / §3`
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md` §目标/§收口口径/§非目标 → 补 `fx.Module / BusModule / RunnerModule` 术语、双树/sidecar 边界、`unsubscribe != drain`、语义 allowlist 不入 numeric freeze。
- 改：`docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md` §目标/§实施方式/§收口口径/§依赖图（文本）/§非目标 → 明确 `PeerSupervisor` 是单一 owner，peer 降级不升级成 app-fatal，并与 `P1c` 分轨。
- 改：`docs/plans/迁移/p22/P1b_PlatformLoopRunners.md` §目标/§实施方式/§收口口径/§依赖图（文本）/§非目标 → 明确 restore 留 `fx.Module`、长期 loop 入 `RunnerModule`，`config fanout/cachekeepalive/rpc push` 继续归 `P2`。
- 改：`docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md` §目标/§实施方式/§收口口径/§依赖图（文本）/§落地顺序建议/§非目标 → 从骨架补成可派单版，并把默认 owner 钉死为 session-owned `SessionRuntime`。
  - 前：35 行骨架，仅有 `目标/覆盖问题/目标架构/TDD/验收`
  - 后：80 行，新增 `实施方式/收口口径/依赖图/落地顺序建议/非目标`
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md` §目标/§收口口径/§实施方式/§依赖图（文本）/§落地顺序建议/§非目标 → 明确 `P2` 是 umbrella；补分批切片、overflow 语义、与 `P4` 的分轨边界。
  - 前：`把 memory 相关 bus 回调中的 runtime ownership 抽离出来...`
  - 后：`把 P2 明确成 bus/runtime ownership 的 umbrella plan，而不是单张 mega PR...`
- 改：`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md` §目标/§实施方式/§收口口径/§依赖图（文本）/§落地顺序建议/§非目标 → 明确 local process monitor owner、exactly-once fence、与 `P4` 的 shell/identity/report 分工。
- 改：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md` §目标/§收口口径/§实施方式/§依赖图（文本）/§落地顺序建议/§非目标 → 改成 phase-B / 窄守卫 / 分域收口，并显式接住 `P21` 递延的 provider contract 债。
- 删：无。经 §10.31 self-check，本轮没有满足“直接冲突 + 非顺手精简 + 无死引用”的删除项。

## 5. 遗留给老公或后续轮次
- `docs/契约/*` 本体仍未统一 `runner.actors` vs `group:"runners"`；这不是 P22 文档页内能单独消掉的债。
- README 里的工时表 / `总计 4-6 天` 与全 scope 现实工作量仍存在张力；因你划的 Judge-D 边界里包含“工时估算表”，本轮不改。
- `## 现状校准` / 带代码引用的 `## 实施步骤` / `## 验收标准` 仍留给 Judge-Dynamic；本轮不越界触碰。
- 若后续实现发现 `P1c` 之外还有新的生产态隐式 session 启动点，必须先补写 `P1c`，再开实现；不能再回到 35 行骨架口径。
- `P2` 的 semantic allowlist / archtest 落地方式仍要和实现轮一起定稿；本轮只把文案从 numeric freeze 想象中拉回来。

## 6. LSP 自验证据
- 删除后 `lsp_grep` 0 命中：本轮无章节删除，因此无 0-hit 删除验证项。
- 修订后关键锚点仍命中：
  - `README.md`：`runner-only sidecar` 命中 2 处（行 21、93）。
  - `README.md`：旧错误锚点（历史误引死章节号）已通过 `lsp_grep` 验证为完整旧串 `0-hit`。
  - `P0_RuntimeOwnershipSkeleton.md`：`runtime 守卫的 allowlist / exception 应按语义形态管理` 命中行 72。
  - `P1c_CodexAppSessionRuntime.md`：`session` owns `SessionRuntime` 命中行 23；`wc -l` = 80。
  - `P2_BusRuntimeDecoupling.md`：`退订只代表 stop intake，不等于 drain` 命中行 249。
  - `P3_OrchestrationWaiterAlignment.md`：`local-only process monitor / owner` 命中行 54。
  - `P4_DependencyDirectionAndHiddenContracts.md`：`P21` 递延的 `signed-skill / native-skill contract` 命中行 118。

## 7. 第 2 轮复审汇总
- 收第 2 轮报告：20/20；20 路均为 `idle` 后收报，无 90s / 120s 超时项。
- 🟢/🟡/🔴 分布：**2 / 12 / 6**。
  - 🟢 NO GAP：`C1`、`C6`
  - 🟡 MINOR-GAP：`D1`、`D2`、`D3`、`D4`、`D5`、`D6`、`D7`、`D8`、`C2`、`A2`、`A3`、`A4`
  - 🔴 MAJOR-GAP：`C3`、`C4`、`C5`、`C7`、`C8`、`A1`
- 本轮静态域真 gap（已直接处理）：
  - `README`：补 `findings 1-9`、去掉“只修 findings”旧句、补 `runner-only sidecar` 最小标准、补 `H + O + M` 三独立复核。
  - `P0`：补 root bridge allowlist 的 `file path + symbol + bridge shape` 规范，以及一跳 helper/caller 解析要求。
  - `P1a`：补固定 `2s` backoff、restart-success 后继续监督、degraded-path 是一级兼容路径。
  - `P1b`：补“`startup restore` 只执行一次；connect-time replay 不算在内”的口径，消除与 `OnConnectUI` replay 的歧义。
  - `P1c`：新增 `## 需冻结的兼容语义`，把 stop gate / recovery signal / in-flight close 语义写死。
  - `P2`：补“不可 silent drop”切片必须在 backpressure/显式拒绝 与 durable replay 之间二选一写死。
  - `P3`：补 `P0` AST guard + hot-file guard，覆盖 `Run(ctx) -> helper -> go waitForExit(...)` 一跳回归。
  - `P4`：补 `NewActiveAgentCounter` hidden contract，修正 `thread/turn` 与 `P2(thread slice)` 的串行依赖，并把 TDD 里的全局守卫收窄为包域守卫。
- 按 §10.18 一票定调：
  - `Q-D` 域 MAJOR-GAP 仍存在，已在本裁决书记账但不在此轮改文档代签收。
  - 非 `Q-D` 但超出本轮写边界的 MAJOR-GAP：`A1` 指向 `docs/契约/*` 本体的 `runner.actors` / `Invoke` / `root runtime bridge` 条文债；本轮仅继续把 `P22` 文案降到“角色术语 + 现实现 tag”口径，契约本体仍 deferred。

## 8. 本轮新修订清单
- 改：`docs/plans/迁移/p22/README.md`
  - 顶部基线：`findings 1-8` -> `findings 1-9`
  - `当前基线约束`：把“只修 findings”改成“以 findings 1-9 + 已纳入 lane 的扩围项”为起点
  - `收口口径`：补 `runner-only sidecar` 最小标准、`H + O + M` 三独立复核
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md` `## 收口口径` / `## 非目标`
  - 补 `file path + symbol + bridge shape` allowlist
  - 补一跳 helper/caller 解析要求
  - 补 semantic allowlist 不复用 numeric freeze
- 改：`docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md` `## 实施方式` / `## 收口口径`
  - 补固定 `2s` backoff
  - 补 degraded-path 属一级兼容路径
- 改：`docs/plans/迁移/p22/P1b_PlatformLoopRunners.md` `## 收口口径`
  - 补 `startup restore` 与 connect-time replay 的口径切分
- 改：`docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md` `## 目标` / 新增 `## 需冻结的兼容语义`
  - 明确 `fx.Module` 只保留 wiring
  - 补 runtime-signal / recovery-order / in-flight stop gate
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md` `## 收口口径` / `## 实施方式`
  - 补 non-silent-drop 场景的 overflow 机制
  - 补“每个切片先冻结 overflow 表再进入实现”
- 改：`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md` `## 目标` / `## 实施方式` / `## 收口口径`
  - 强化 `RunnerModule` / local process owner 分层
  - 补 AST/hot-file guard
- 改：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md` `## 目标架构` / `## 实施方式` / `## 依赖图（文本）` / `## 落地顺序建议` / `## TDD 与旧实现清理`
  - 补 `NewActiveAgentCounter`
  - 把 `thread/turn` 改成 `P2(thread slice) -> P4(thread/turn side-channel)`
  - 把 TDD 里的全局 import 守卫改成点名子域的包域窄守卫
- 删：无；本轮继续遵守 §10.31，只加不删。
- 交给 Q-D 的 gap（10 条，聚合记账）：
  1. `Finding 3/4` 代码仍未下沉到 runner owner（`C3`）
  2. `Finding 9` 的 `toolbridge` proxy runtime 仍在 `OnStart -> go ServeProxy(...)`（`C3`）
  3. `Finding 5/6/7` 的 TeamSync / watcher / auto-dream 代码仍未收口（`C4`）
  4. memory hooks / nested runtime 额外 slow-path 仍在 callback 链上（`C4`）
  5. `Finding 8` 的 waiter / exit owner 代码仍未下沉（`C5`）
  6. recover 路径仍未并入同一 exit-owner contract（`C5`）
  7. `thread` / `hooks` / `config_change` / `cachekeepalive` 运行时切片仍未落地（`C7`）
  8. `gopls` / `bootstrap` sidecar runtime 仍未落地（`C7`）
  9. `internal/archtest` 的 P22 guards / semantic freeze 模型仍未落地（`C8`）
  10. desktop root bridge pre-drain / `watchFXShutdown` 非对称覆盖，以及 `JUDGEMENT_DYNAMIC.md` 仍写 `Findings 1-8` 的同步项，留给 `Q-D` 收口（`A2` / `A4`）

## 9. 第 2 轮 LSP 验真
- `README.md`：`findings 1-8` 已 `lsp_grep` 0 命中；`findings 1-9` 命中行 5、32。
- `README.md`：`runner-only sidecar 的最小标准` 命中行 93；`H + O + M` 三独立复核已写入 `收口口径`。
- `P0_RuntimeOwnershipSkeleton.md`：`file path + symbol + bridge shape` 命中行 70；`一跳 helper/caller 解析` 命中行 74。
- `P1a_CodexAppPeerSupervisor.md`：`固定 2s backoff` 命中行 47；`warn+skip` / degraded-path 兼容语义仍命中。
- `P1b_PlatformLoopRunners.md`：`startup restore` 只执行一次的澄清命中行 100。
- `P1c_CodexAppSessionRuntime.md`：新增 `## 需冻结的兼容语义` 命中行 59；`wc -l` = 87。
- `P2_BusRuntimeDecoupling.md`：`owner 侧 backpressure/显式拒绝` 命中行 251。
- `P3_OrchestrationWaiterAlignment.md`：`P0` 的 actor AST guard 命中行 57。
- `P4_DependencyDirectionAndHiddenContracts.md`：`NewActiveAgentCounter` 命中行 68；`P0 -> P2(thread event / resume / task-handoff) -> P4(thread / turn side-channel contract)` 命中行 137；旧的“全局 import 大网”TDD 写法已 `lsp_grep` 0 命中。

## 10. 第 3 轮仲裁（多角度验证）
- 收第 3 轮多角度验证报告：20/20。
- 🟢/🟡/🔴 分布：**3 / 11 / 6**。
  - 🟢 PASS：`D3`、`C1`、`C6`
  - 🟡 IMPROVE：`D1`、`D2`、`D4`、`D5`、`D6`、`D8`、`C2`、`C4`、`A2`、`A3`、`A4`
  - 🔴 BLOCK：`D7`、`C3`、`C5`、`C7`、`C8`、`A1`
- 裁决原则：本轮部分报告引用的是旧快照；凡与当前仓库现状冲突，统一以本轮 LSP 真值为准，不复写过期销账表。

### 10.1 合并后的静态域 gap
- **风险矩阵缺口**：README 原先没有统一的 crash-window / 回滚 / observability / flaky / §10 教训映射总表。
- **术语一致性仍有尾差**：`P1c/P4` 对 `fx.Module / BusModule / RunnerModule` 的显式落点不足，容易让 A1 的契约冲突继续外溢到 p22 叙事。
- **P1c 密度与新人开工友好度不足**：虽然已脱离骨架，但还缺一段把 session-owner 常见误判、并行切片风险和阅读顺序说清楚的叙事。
- **P4 与 thread slice 的并行口径过于乐观**：`thread/turn` 与 `P2(thread event / resume / task-handoff)` 共享写集，不能再写成独立并行 lane。
- **风险/回滚与可观测性属于静态域必写项**：不属于 `Q-D` 的代码锚点校验，因此本轮直接补文案。

### 10.2 已执行修订清单（本轮）
- 改：`docs/plans/迁移/p22/README.md` `## 当前基线约束` / 新增 `## 风险矩阵（叙事）` / `## 收口口径`
  - 把“只修 findings”改成“以 findings 1-9 为起点 + 同 lane 扩围项”
  - 补统一风险矩阵：crash-window、升级/回滚、可观测性、CI/flaky、§10 映射、P21/session-summary 同步
  - 补 `runner-only sidecar` 最小标准与 `H + O + M` 三独立复核
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md` `## 收口口径` / `## 非目标`
  - 补 root bridge allowlist 的 `file path + symbol + bridge shape`
  - 补一跳 helper/caller 解析要求
  - 补 semantic allowlist 不复用 numeric freeze
- 改：`docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md` `## 实施方式` / `## 收口口径`
  - 补固定 `2s` backoff、restart-success 后继续监督
  - 补 degraded-path / `warn+skip` / missing-binary 属一级兼容路径
- 改：`docs/plans/迁移/p22/P1b_PlatformLoopRunners.md` `## 收口口径`
  - 明确 `startup restore` 只执行一次；`OnConnectUI` replay 不算在内
- 改：`docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md` `## 目标` / 新增 `## 风险提示`
  - 明确 `fx.Module` 只保留 wiring，session 自身承担局部 `RunnerModule`
  - 补 session-owner 常见误判、并行切片风险与阅读顺序
  - 文档密度提升到 `wc -l = 100`
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md` `## 收口口径` / `## 实施方式`
  - 把 non-silent-drop 场景固定为“backpressure/显式拒绝”或“durable replay/重试”二选一
  - 补“每个切片先冻结 overflow 表再进入实现”
- 改：`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md` `## 目标` / `## 实施方式` / `## 收口口径`
  - 强化 `RunnerModule` actor 与 local process owner 分层
  - 补 `P0` AST guard + hot-file guard，覆盖 `Run(ctx) -> helper -> go waitForExit(...)`
- 改：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md` `## 目标` / `## 收口口径` / `## 实施方式` / `## 依赖图（文本）` / `## 落地顺序建议` / `## TDD 与旧实现清理`
  - 明确 `P4` 不重写 `fx.Module / BusModule / RunnerModule` 分工，只处理其外的边界/隐藏契约
  - 补 `NewActiveAgentCounter` hidden contract
  - 把 `thread/turn` 串到 `P2(thread slice)` 之后，不再写成独立并行 lane
  - 把 TDD 里的全局 import 守卫收窄为点名子域的包域守卫

### 10.3 交给 Q-D 的 gap 清单
1. `Finding 3/4/9` 的代码仍未从 lifecycle / `OnStart -> go` 真正下沉到 owner（`C3`）。
2. `Finding 5/6/7/10` 的 TeamSync / watcher / auto-dream / nested-runtime callback slow-path 仍在代码里（`C4`）。
3. `Finding 8` 的 waiter/exit owner 与 recover 路径仍未统一到同一 contract（`C5`）。
4. `thread` / `hooks` / `config_change` / `cachekeepalive` / `gopls` / `bootstrap` 的运行时切片仍是 live debt（`C7`）。
5. `internal/archtest` 的 P22 AST guards、semantic allowlist/freeze 仍未进入代码（`D2` / `C8`）。
6. 新反模式建议项 `Finding 11/12`（`internal/mcpserver/common/server.go` actor 内 `go readLoop`、`toolbridge/diff_fallback.go` callback -> helper -> 重 I/O）属于新代码锚点，交 `Q-D` 裁决是否升级进 Findings 表（`D4`）。
7. `JUDGEMENT_DYNAMIC.md` 与 `P2/README` 的 Finding 10 / baseline 同步属于 Finding 编号域，继续由 `Q-D` 处理（`D1`）。
8. `desktop root bridge` 的 pre-drain 与 `watchFXShutdown` 非对称覆盖仍属代码真实行为，不在静态页代签收（`A2`）。

### 10.4 非 Q-D 但超出本轮写边界的遗留
- `docs/契约/*` 本体仍有 `runner.actors` / `Invoke` 例外 / `root runtime bridge` 条文债（`A1`）；本轮只继续把 p22 文案降到“角色术语 + 现实现 tag”口径，不越界改契约本体。
- `arch-import-direction.md` 的 debt banner 与 P22 同步属于仓内其它文档，不在本轮 p22 写边界；已在本节记账，待后续同步。
- 工时表、LoC 估算与 archtest 数字仍归 `Q-D` / 主 agent，不在本轮静态页直接改表。

### 10.5 本轮 LSP 自证
- `README.md`：新增 `## 风险矩阵（叙事）` 命中行 88；`crash-window` / `回滚触发条件` / `可观测性` / `fake clock` 均已命中。
- `P0_RuntimeOwnershipSkeleton.md`：`file path + symbol + bridge shape` 命中；`一跳 helper/caller 解析` 命中。
- `P1a_CodexAppPeerSupervisor.md`：`固定 2s backoff`、`warn+skip`、degraded-path 兼容语义均命中。
- `P1b_PlatformLoopRunners.md`：`startup restore` 只执行一次的澄清已命中。
- `P1c_CodexAppSessionRuntime.md`：`局部 RunnerModule` 与 `## 风险提示` 已命中；`wc -l = 100`。
- `P2_BusRuntimeDecoupling.md`：`owner 侧 backpressure/显式拒绝` / `durable replay/重试` 已命中。
- `P3_OrchestrationWaiterAlignment.md`：`P0` 的 actor AST guard 命中。
- `P4_DependencyDirectionAndHiddenContracts.md`：`NewActiveAgentCounter`、`P2(thread event / resume / task-handoff) -> P4(thread / turn side-channel contract)`、`包域窄守卫` 均已命中；旧的全局 import 大网表述 `lsp_grep` 0 命中。

## 11. 第 4 轮仲裁（文档↔代码交叉验证）
- 收交叉验证报告：20/20；`orchestration_list_agents` 结果因输出过大未完整展开，但 20 路 `get_agent_report` 均成功返回。
- drift 分布：**🟢 2 / 🟡 15 / 🔴 3**。
  - 🟢 ALIGNED：`C1`（root bridge 四处实况）、`A3`（双树同构 + runner-only sidecar）
  - 🟡 MINOR-DRIFT：`D2`、`D3`、`D4`、`D5`、`D6`、`D8`、`C2`、`C3`、`C4`、`C6`、`C7`、`C8`、`A1`、`A2`、`A4`
  - 🔴 MAJOR-DRIFT：`D1`、`D7`、`C5`
- 裁决说明：本轮多份报告把“目标态文档”与“代码尚未落地”混为一类 drift。凡属计划文档合理描述目标态、且本轮已在 `目标/收口口径/实施方式/依赖图/落地顺序建议` 中明确“尚未落地/需串行”的，按静态域收口；凡属 Findings 行号、代码 live debt、archtest 真值、章节号死引用等事实层差异，继续留给 `Q-D`。

### 11.1 本轮已执行修订
- 改：`docs/plans/迁移/p22/README.md`
  - `## 依赖图（文本）`：把 `P2` 拆成真正的并行子切片，并补 `P1b -> P2(platform slices)`、`P1a/P1c -> P2(toolbridge runtime)` 依赖。
  - `## 落地顺序建议`：明确并行单位是子切片，不是 `P1a-P4` 六块整推。
  - `## 收口口径`：补 shutdown 叙事的语义顺序说明，明确 app/orch 与 sidecar 不同，不再把 bus 阶段误写成每个 root bridge 的字面结构。
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md`
  - `## 收口口径`：补“新守卫落独立 `*_guard_test.go`”与“AST denylist + grep 补位”的叙事，避免把 runtime guard 误塞进现有 size/freeze 壳。
- 改：`docs/plans/迁移/p22/P1b_PlatformLoopRunners.md`
  - `## 实施方式`：补平台子模块当前没有现成 runner wiring 样板，本单默认沿用 `platformrunner.Runner + fx.Annotate(..., fx.ResultTags(...))` 接线，不再发明第二套包装器。
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md`
  - `## 收口口径`：补 callback -> helper -> goroutine/manager/store/notify 也算 callback slow-path；补 bootstrap 的 stdio EOF owner 需结合上层 peer/runtime 接入点复核。
  - `## 实施方式` / `## 依赖图（文本）` / `## 落地顺序建议`：补“旧 wiring + 新 owner 不得双轨并存”，并把 `toolbridge runtime` 串到 `P1a + P1c` 之后。
- 改：`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md`
  - `## 目标` / `## 实施方式`：明确本页 `目标架构/实施步骤` 描述的是目标态，当前 HEAD 仍是旧 waiter 模式；补“现有 `waitResult + results chan` 只是私有雏形，不等于正式 exit contract”。
- 改：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md`
  - `## 实施方式`：补 `arch-import-direction.md` 只做历史扫描/debt banner，每次 P4 子域落地后都要同步。
  - `## 实施方式` / `## 落地顺序建议` / `## TDD 与旧实现清理`：继续压实 `thread/turn` 只能在 `P2(thread slice)` 之后串行合入，以及包域窄守卫策略。
- 改：`docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md`
  - `## 目标` / `## 风险提示`：补 `fx.Module` 只保留 wiring、session 自身承担局部 `RunnerModule`，并加入新接手实现者的误判提醒；文档密度提升到 `wc -l = 100`。

### 11.2 交给 Q-D 的事实 drift
1. `README` Findings 对照表仍有 F2/F7/F9/F10 的锚点/编号同步问题；这属于 Finding 编号与 `file:line` 域（`D1` / `C2` / `C4` / `C6`）。
2. `P0` 守卫与 `internal/archtest` 现状仍是“文档已写、代码未落地”；实现位置、numeric freeze 不兼容、`TestCodeSizeGuard` 真值等都属 `Q-D`（`D2` / `C8`）。
3. `P1a/P1b/P3` 当前代码仍未落到目标态：peer supervisor skeleton、sweeper/approval runner、exit owner/`cmd.Wait` 下沉都还是 live debt（`D3` / `D4` / `C5`）。
4. `P2` 的 memory / hooks / toolbridge / bootstrap 等现状校准若有一跳 helper 漂移、bootstrap desired-state 断言过时、new anti-pattern 候选 `Finding 11/12`，都属于事实层与 code-anchor 层（`D4` / `D6` / `C7`）。
5. `README.md:99` 曾保留不存在的 `fx-convention` 章节号提示；现已改成历史误引表述，不再记为 live drift。
6. desktop root bridge pre-drain、`watchFXShutdown` 只在 desktop 使用、以及 bus stop-intake 真实落点仍属代码事实核对，不在静态叙事页代签收（`A2` / `X18`）。
7. 四树 root module 清单、`fx.Options` / `fx.Module` 实际组合、以及 shared wiring/test graph 的实测数字继续归 `Q-D`（`A3` / `A4` / `X19` / `X20`）。

### 11.3 超出本轮写边界的非 Q-D 遗留
- `arch-import-direction.md` 与其它外部说明页的 debt banner 同步，不在本轮 p22 写边界；本轮只在 `P4` 中补“必须同步”的义务说明。
- 工时表、LoC 数字、archtest 失败数字仍不在本轮静态页直接改动范围内。

### 11.4 本轮 LSP 自证
- `README.md`：`真正的并行单位是子切片` 命中；`ctx cancel -> run.Group -> bus stop-intake -> fx.OnStop` 的语义说明已命中。
- `P0_RuntimeOwnershipSkeleton.md`：`*_guard_test.go` 命中；runtime guard 不复用 size/freeze 壳的叙事已命中。
- `P1b_PlatformLoopRunners.md`：`platformrunner.Runner + fx.Annotate` 接线说明已命中。
- `P2_BusRuntimeDecoupling.md`：`callback -> helper -> goroutine/manager/store/notify` 命中；旧依赖图行 `P0 -> P2(toolbridge runtime) -> P4(...)` 已 `lsp_grep` 0 命中，新行 `P0 + P1a + P1c -> P2(toolbridge runtime)` 已命中。
- `P3_OrchestrationWaiterAlignment.md`：`不应把这些 contract 误读成“已经存在”` 已命中。
- `P4_DependencyDirectionAndHiddenContracts.md`：`历史扫描与 debt banner 承载页`、`P2(thread event / resume / task-handoff)`、`包域窄守卫` 均已命中。
- `P1c_CodexAppSessionRuntime.md`：`局部 RunnerModule` 与新增风险提示命中；`wc -l = 100`。

## 12. 第 5 轮仲裁（Q-E 独立第三方 / 静态层）
- 收报告：**19/20**；`R1 README↔代码整体` 未返回有效 report，按 §10.22 记为未回，本轮基于 `19/20` 仲裁。
- 分布：**🟢 2 / 🟡 16 / 🔴 1 / X 1**。
  - 🟢：`R14`、`R20`
  - 🔴：`R2`
  - 🟡：其余有效回报；其中 `R19` 未显式着色，但给出需改判的关键路径叙事，按 `🟡` 计入
  - X：`R1`
- 按 §10.18 一票定调：`R2` 对 `P0` / archtest 时机报 `🔴`，故本轮静态层总判定仍为 **BLOCK**；README 可以继续补叙事，但不能写成“所有子计划已可无门槛并行开工”。

### 12.1 本轮已执行修订
- 改：`docs/plans/迁移/p22/README.md`
  - `## 依赖图（文本）`：把 `P0` 改写为“骨架 / allowlist 先行、具体 guard 随 owning slice 接入”，并补 `P4(ui/wails + claudecli)` 早期 lane、`gopls/bootstrap` 同 lane、`thread+turn` / `toolbridge` / `orchestration` 的后继串行关系。
  - 新增 `## 关键路径（叙事）`：把 `P0 -> P1a -> P1c -> P2(thread/cachekeepalive/toolbridge runtime) -> P4(thread/turn + toolbridge contract)` 落成最长硬门，并把 `P1b`、`P3`、`P2(memory...)`、`P4(ui/wails/claudecli)` 改写为并行支线。
  - 新增 `## 并行度矩阵（叙事）`：吸收 `R11-R18` 的推荐拆法，不再把 `P0` 误写成“全量 guard 一次性前置”，也不再把 `P2` 的 8 scope / `P4` 的 5 子域机械写成“都可独立并行”。
  - `## 落地顺序建议` / `## 收口口径` / `## 实施方式`：补 `P0` guard skeleton 先行、具体守卫与 owning slice 同 PR red-green，以及 `P3` exit contract 保持 package-local、不经全局 bus 中转。
- 删：无；本轮继续按 §10.31 只加不删，未触发“顺手精简”。

### 12.2 与 Q-S 的协同 / 冲突记录
- 协同：第 4 轮静态稿已把 `P22` 拉回 umbrella / sub-slice 口径；Q-E 本轮只补“关键路径 + 并行度矩阵 + P0 接入时机”，属于在现稿上加硬，不是推翻重写。
- 冲突调和 1：若前轮叙事把“先做 `P0`”读成“`P0` 全量守卫必须一次性先合”，以 `R11` / `R20` 为准，改判为“`P0` 先骨架 / allowlist，具体守卫跟 owning slice 同 PR 接入”。
- 冲突调和 2：若前轮叙事把 `P2(memory...)` 读成全局总闸门，以 `R16` / `R19` 为准，改判为“它是推荐模板支线，不阻塞 `P3`；前提是 `P3` exit contract 不升级成 bus event”。
- 结论：本轮与现有 `Q-S` 文本以协同为主，未发现需要回滚的直接文本冲突。

### 12.3 交给 Q-F 的事实 gap
1. `R1` 未回，README↔代码整体 drift 仍需事实层补抽样。
2. `R2` / `R20` 指向的 archtest baseline、违规底数、allowlist/guard 实际落地位置与失败测试数，仍属事实层。
3. `R10` 指向的契约章节号死链已由 `Q-F` 收口；当前只保留“历史误引曾存在”的说明，不再保留具体死章节号。
4. `R12-R18` 涉及的共享 file:line、同包热写集、并行度数字矩阵与工时估算，不在本轮静态叙事页直接改表。
5. `R19` critical path 的 merge gate / commit 级验证与是否先合 main，继续由 `Q-F` / 主 agent 做事实层判定。

### 12.4 本轮 LSP 自证
- `README.md`：新增 `## 关键路径（叙事）` 与 `## 并行度矩阵（叙事）` 已命中。
- `README.md`：`P0` 的新依赖图叙事已命中 `骨架 / allowlist 先行；具体 guard 随 owning slice 接入`。
- `README.md`：`P3` 的新实施口径 `保持 local process owner -> actor 主循环，不升级成全局 bus topic` 已命中。
- 本轮仅增补 / 改判叙事，不删除既有风险矩阵、`runner-only sidecar`、`H + O + M` 等硬锚点；仍符合 §10.31 只加不删。

## 13. 第 6 轮仲裁（Q-E Round-9 / 静态层）
- 收报告：**20/20**；20 路 Round-8 交叉审查均在 `idle` 后取回，无需 90s/120s 超时等待。
- 分布：**🟢 2 / 🟡 15 / 🔴 3**。
  - 🟢 READY：`R14`、`R17`
  - 🟡 MINOR-FIX：`R2`、`R3`、`R4`、`R5`、`R6`、`R8`、`R10`、`R11`、`R12`、`R13`、`R15`、`R16`、`R18`、`R19`、`R20`
  - 🔴 BLOCK：`R1`、`R7`、`R9`
- 按 §10.18 一票定调：本轮静态层总判定仍为 **BLOCK**；原因不是 README 主线方向错误，而是 `P0` 的 guard rollout 仍未写到“无歧义可实施”、`P4` 的覆盖/守卫分层仍需补清，以及 `P2 memory` 的 READY 结论在交叉审查中仍存在红票反对。

### 13.1 本轮已执行修订
- 改：`docs/plans/迁移/p22/README.md`
  - 更新时间改到 `2026-04-23`。
  - `## 关键路径（叙事）`：把主线改写成“公共骨架门 -> peer owner 门 -> session stop/drain 门 -> thread/toolbridge runtime 门 -> hidden-contract 门”的阻塞链，并明确 README 这里只负责派工顺序，不代替子计划级 merge gate。
  - `## 并行度矩阵（叙事）`：补 `cachekeepalive` 属于 thread/session-user lane、`gopls/bootstrap` 属于同一 sidecar lane，以及 `P4` 首波真正能独立开的只有 `ui/wails + claudecli`。
  - `## 落地顺序建议` / `## 实施方式`：补 README 级放行前提——子计划验收、`JUDGEMENT_DYNAMIC` 事实层放行、`P21/session-summary` 同步，缺一不算 READY。
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md`
  - `## TDD 与清理要求`：补“semantic allowlist schema -> 独立 guard 骨架 -> owning slice 同 PR red-green”的固定接入节奏。
  - 明确 `fx.Invoke` 与 `OnStart` 守卫共用同一 root-bridge allowlist，且 `Run(ctx)` 守卫初版按 actor hot-file / owning slice 收窄推进，不直接打全仓红面。
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md`
  - `## 实施方式` / `## 落地顺序建议`：把 `cachekeepalive` 归并到 thread/session-user lane，不再和 platform event lane 混写；把 `gopls/bootstrap` 固定成同一 sidecar lane，不再写成两个独立 merge 单元。
- 改：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md`
  - `## 覆盖问题`：补 `thread/turn`、`NewActiveAgentCounter`、`MCP-LSP/bootstrap`，把总 scope 写闭合。
  - `## 实施方式`：补 `P4` 内部只有 `ui/wails + claudecli` 是首波 implementation lanes；`thread+turn` 同 lane，`toolbridge/orchestration/gopls-bootstrap` 只允许先做文档/contract 澄清，代码仍等前置门。
  - `## TDD 与旧实现清理`：把守卫拆成 `import-direction` / `symbol/export` / `behavior/protocol` 三类，避免继续混写成一张大网。

### 13.2 与前轮裁决的协同 / 调和
- 协同：沿用 Q-S / 前轮 Q-E 已确立的 umbrella 口径；本轮只补“critical path 只是派工总纲，不是 README 自带 merge gate”与“并行 lane 终态收窄”。
- 调和 1：若前轮叙事把 README 主链读成“已经足够直接派实施 agent 按节点闭环”，以 `R2` / `R19` 为准，改判为“README 负责排兵布阵，真正代码放行仍看各子计划验收 + 动态核验”。
- 调和 2：若前轮叙事把 `cachekeepalive` 混入 platform event lane，或把 `gopls/bootstrap` 看成两个独立 scope，以 `R18` 为准，改判为“`cachekeepalive` 跟 thread/session-user 走同 lane，`gopls/bootstrap` 只按同一 sidecar lane 叙述”。
- 调和 3：若前轮叙事把 `P4` 读成 5 子域都能各自独立实现，以 `R16` 为准，改判为“只有 `ui/wails + claudecli` 是首波独立 implementation lanes，其余最多先并行做 contract 澄清”。

### 13.3 交给 Q-F 的事实 gap
1. `P1b/P1c/P4` 的精确 file:line 漂移、anchor 修正与代码级 READY 状态，继续归事实层。
2. `P2 memory` 的红票分歧点本质仍是“代码尚未落地 / stop-drain 仍在旧路径”，不是静态叙事可代签收的问题。
3. README `## 并行度矩阵（数字）` 若仍与本轮叙事收窄口径不同步，属于数字矩阵域，继续交 `Q-F`。
4. `P4` 的 codemap / debt banner / 其它外部说明页同步，属于跨文档事实层收口，不在本轮静态页直接改完。

### 13.4 本轮 LSP 自证
- `README.md`：`README 这一节只负责派工顺序与阻塞关系` 已命中。
- `README.md`：`thread+cachekeepalive(session users)`、`真正可先独立开的只有` 均已命中。
- `P0_RuntimeOwnershipSkeleton.md`：`先落 semantic allowlist schema` 与 `fx.Invoke 守卫与 OnStart 守卫必须消费同一份 root-bridge allowlist` 已命中。
- `P2_BusRuntimeDecoupling.md`：`cachekeepalive 归属 thread/session-user lane` 已命中；旧叙事 `hooks + config fanout + cachekeepalive + rpc push + eventsurface` 已按本页目标从 narrative lane 中移除。
- `P4_DependencyDirectionAndHiddenContracts.md`：`NewActiveAgentCounter`、`MCP-LSP/bootstrap`、`P4 守卫固定分三类写` 已命中。
- 本轮继续只加不删；未移除前轮已落盘的 `runner-only sidecar`、`H + O + M`、风险矩阵等锚点，符合 §10.31。

## 14. 第 7 轮仲裁（Q-E Round-11 / 静态层）
- 收报告：**20/20**；Round-10 环形交叉的 20 路报告均在 `idle` 后取回，其中 `R9` 额外等待至 120s 窗口内返回。
- 分布：**🟢 2 / 🟡 6 / 🔴 12**。
  - 🟢 READY：`R19`、`R20`
  - 🟡 MINOR-FIX：`R6`、`R11`、`R14`、`R15`、`R16`、`R17`
  - 🔴 BLOCK：`R1`、`R2`、`R3`、`R4`、`R5`、`R7`、`R8`、`R9`、`R10`、`R12`、`R13`、`R18`
- 按 §10.18 一票定调：**本轮原始总判定仍为 BLOCK**。但把 20 路结论映射回静态叙事域后，新增可直接修正的 blocker 主要集中在 4 处：`README` 关键路径说明、`P0` guard rollout、`P2` memory owner 口径、`P4` 内部 pairwise / scope 叙事；其余红票大多是“代码 live debt 仍在”或“契约章节号死链仍在”，不属于本轮可直接用叙事层消解的范围。

### 14.1 本轮已执行修订
- 改：`docs/plans/迁移/p22/README.md`
  - `## 关键路径（叙事）`：补“主线五个 gate 各自签收哪类 blocker”的说明，并加一条固定映射，避免派实施 agent 时再在 README 与子计划之间来回猜 scope。
  - `## 并行度矩阵（叙事）`：继续收窄 `P4` 首波 implementation lane，只保留 `ui/wails + claudecli`；并把 `thread+cachekeepalive(session users)` 固定成同一 `P2` lane。
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md`
  - `## TDD 与清理要求`：把 `semantic allowlist schema -> guard skeleton -> owning slice red-green` 的 phased rollout 写实，并补 `fx.Invoke/OnStart` 共用同一 root-bridge allowlist、`Run(ctx)` 守卫只先按 hot-file / owning slice 收窄推进。
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md`
  - `## 目标架构`：把 memory 口径改成“两条主 owner 线 + 一个配套 hook owner”，消除“前文两 owner、后文又出现 `MemoryHookWorker`”的表述冲突。
  - `## 实施方式` / `## 落地顺序建议`：明确 `cachekeepalive` 属于 thread/session-user lane，`gopls/bootstrap` 属于同一 sidecar lane。
- 改：`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md`
  - `## 覆盖问题`：把 `thread/turn`、`NewActiveAgentCounter`、`MCP-LSP/bootstrap` 正式并入总 scope。
  - 新增 `## 内部并行关系（叙事）`：补 pairwise 口径，明确哪些组合能并行做 implementation、哪些只能先做 contract 澄清，解决“5x5/6域矩阵未落盘”的静态 blocker。
  - `## 实施方式`：继续收窄首波 implementation lanes 与后置串行域。
- 删：无；继续遵守 §10.31 只加不删。

### 14.2 与前轮裁决的协同 / 调和
- 协同：Round-9 已把 README/P0/P2/P4 拉回 umbrella + lane 口径；本轮只补“implementation lane 更细分”和“P2/P4 内部口径不再自相矛盾”。
- 调和 1：若前轮把 `P2 memory` 读成“只有 `TeamSyncCoordinator + AutoDreamScheduler` 两个 owner”，以 `R1` 为准，改判为“两条主 owner 线 + 一个 `MemoryHookWorker` 配套 owner”，避免 extraction/auto-dream wait 又落成悬空职责。
- 调和 2：若前轮把 `P4` 读成“有总述即可，不必给内部 pairwise 关系”，以 `R9` / `R13` 为准，改判为“必须把 implementation lane 与 contract-only lane 区分落盘”。
- 调和 3：若前轮把 README 主线读成“节点顺序有了就够”，以 `R2` / `R14` 为准，补成“主线节点还要显式告诉实施 agent 每一站收哪类 blocker”。

### 14.3 交给 Q-F 的事实 gap
1. `R10` 指向的历史死章节号问题已由 `Q-F` 处理；当前仅保留“曾存在历史误引”的事实说明，不再保留具体死链号。
2. `R1/R2/R3/R4/R8/R18` 指向的 live code blocker 仍待事实层与实现层收口，静态叙事不代签收。
3. `README` / `P4` 若还要同步到 `codemap`、debt banner、外部 followup 文档，属于跨文档事实层工作。
4. `并行度矩阵（数字）`、archtest baseline、guard 实际落地状态，继续归 `Q-F`。

### 14.4 本轮 LSP 自证
- `README.md`：主线 gate→blocker 对应说明已命中；`thread+cachekeepalive(session users)` 仍命中。
- `P0_RuntimeOwnershipSkeleton.md`：`先落 semantic allowlist schema`、`共用同一份 root-bridge allowlist` 已命中。
- `P2_BusRuntimeDecoupling.md`：`两条主 owner 线 + 一个配套 hook owner` 已命中；`cachekeepalive 归属 thread/session-user lane` 已命中。
- `P4_DependencyDirectionAndHiddenContracts.md`：`MCP-LSP/bootstrap`、`NewActiveAgentCounter`、`## 内部并行关系（叙事）`、`P4 守卫固定分三类写` 已命中。
- 本轮仍未删除前轮已落盘锚点；符合 §10.31。

### 14.5 收敛判定（叙事层）
- **当前判定：⚠️ NEEDS-FIX**。
- 理由：可直接修的叙事 blocker 已基本收敛，实施 agent 现在能从 README / P0 / P2 / P4 读到统一的主线、lane、owner 与 guard rollout 口径；但静态文档全集里仍残留 `JUDGEMENT_*` 历史章节号死链（交 `Q-F`），且整体项目层面仍被大量 code-live blocker 压住，不能把“叙事层已足够派工”误写成“P22 已整体 READY 合码”。
- 换言之：**主计划叙事已接近 READY，全集实施放行仍需 Q-F/事实层补完最后的 citation 与代码真值收口。**

## 15. 第 8 轮 Q-B 元裁决（2026-04-23）

### 15.1 收报状态表

| 路 | agent | 轮询结果 | 结论 | 摘要 |
|---|---|---|---|---|
| V9 | `p22-R8-V9-JudgeStatic-selfaudit` | idle | 🟡 | `JUDGEMENT_STATIC.md` 主体自洽，但旧轮 `findings 1-9` / `wc -l = 100` 自证已老化 |
| V10 | `p22-R8-V10-JudgeDynamic-selfaudit` | idle | 🟡 | `JUDGEMENT_DYNAMIC.md` 主体真值仍对，但 `P2` 顶部未显式补 `Finding 10`，且 `Finding 11/12` disposition 缺位 |
| H5 | `p22-R8-H5-anchor-truth` | idle | 🟡 | 现存锚点无 missing；动态域旧范围/旧链路主要已在后续轮次修正，但仍需元裁决补 authoritative drift 表 |
| H7 | `p22-R8-H7-hours-consistency` | idle | 🟡 | 工时 / LoC / archtest 数字大体一致；明确 live drift 为 `P2` findings 列表漏 `Finding 10` |
| L1 | `p22-R8-L1-claim-vs-reality-grep` | idle | 🔴 | 抓到 `JUDGEMENT_STATIC` 关于“死章节号已清空”的 claim-vs-reality 漂移 |
| L5 | `p22-R8-L5-only-add-not-delete` | thinking → thinking → idle | 🟡 | 未发现 §10.31 实质违规；仅有 `JUDGEMENT_STATIC §9` 自证基线漂移 |

- 收报结果：**6/6**
- 轮询说明：`orchestration_list_agents` 连续 3 次调用都因 **34 agents / output too large** 未完整展开；本轮以 6 个指定 agent 的 `orchestration_get_agent_report(...).state` 作为 idle 核实来源。`L5` 在前两次轮询仍为 `thinking`，第三次轮询回到 `idle` 并成功收报；未出现 `>300s` 超时项。

### 15.2 独立抽样复核结果

- `JUDGEMENT_STATIC` 声称“已命中”的锚点抽样 **14 条**，本轮 `lsp_grep` 实测 **14/14 仍命中**：

| 抽样 | 当前命中 |
|---|---|
| README `runner-only sidecar 的最小标准` | `README.md:193` |
| README `H + O + M` | `README.md:185` |
| README `真正的并行单位是子切片` | `README.md:96` |
| README `## 关键路径（叙事）` | `README.md:98` |
| README `骨架 / allowlist 先行；具体 guard 随 owning slice 接入` | `README.md:74` |
| README `README 这一节只负责派工顺序与阻塞关系` | `README.md:108` |
| README `thread+cachekeepalive(session users)` | `README.md:147,159` |
| P0 `runtime 守卫的 allowlist / exception 应按语义形态管理` | `P0_RuntimeOwnershipSkeleton.md:74` |
| P0 `先落 semantic allowlist schema` | `P0_RuntimeOwnershipSkeleton.md:168` |
| P1b `platformrunner.Runner + fx.Annotate` | `P1b_PlatformLoopRunners.md:55` |
| P1c ``session` owns `SessionRuntime`` | `P1c_CodexAppSessionRuntime.md:33` |
| P2 `退订只代表 stop intake，不等于 drain` | `P2_BusRuntimeDecoupling.md:251` |
| P2 `callback -> helper -> goroutine/manager/store/notify` | `P2_BusRuntimeDecoupling.md:255` |
| P4 `## 内部并行关系（叙事）` / `P4 守卫固定分三类写` | `P4_DependencyDirectionAndHiddenContracts.md:166,223` |

- `JUDGEMENT_DYNAMIC §3` 行号 drift 表抽样 **7 条**，本轮 `lsp_file` 核对 **7/7 与现 HEAD 一致**：
  - F1 `internal/provider/codexapp/module.go:35`
  - F2 `internal/provider/codexapp/peer_spawn.go:18-155`
  - F4 `internal/platform/rpc/module.go:149-166 + 179-197`
  - F5 `internal/module/memory/module.go:456-467`
  - F7 `internal/module/memory/auto_dream_task.go:156-178`
  - F8 `cmd/mcp-orch/orchestration/process_lifecycle.go:220-239`
  - F10 `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339`
- archtest 真值复核：
  - `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：**PASS / 0 violations**
  - `go test ./internal/archtest/... -count=1 -v`：**FAIL / 3 live failures**
    1. `internal/module/memory/ui_rpc.go` → `rule2_module_impls_no_fx`
    2. `internal/module/memory/ui_rpc.go` → `rule10_fx_import_scope`
    3. `internal/module/prompt/classifier/claude_cli.go:59` → `TestTimeoutLocality`
  - `internal/archtest/freeze_registry.go:19` 仍是空 `explicitFreezeRegistry`
  - `internal/archtest/guardlib.go:22-32` 仍是 numeric-only 守卫常量：`600 / 800 / 30 / 10000`
- “旧行应 0 命中”抽样 **7 条**：
  - ✅ `README.md`：`findings 1-8` = `0-hit`
  - ✅ `README.md`：`只修 findings` = `0-hit`
  - ✅ `P4_DependencyDirectionAndHiddenContracts.md`：`全局 import 大网` = `0-hit`
  - ✅ `P2_BusRuntimeDecoupling.md`：`P0 -> P2(toolbridge runtime) -> P4(...)` = `0-hit`
  - ✅ `P2_BusRuntimeDecoupling.md`：`hooks + config fanout + cachekeepalive + rpc push + eventsurface` = `0-hit`
  - ✅ `JUDGEMENT_DYNAMIC.md`：死章节号宽搜短语 = `0-hit`
  - ❌ `JUDGEMENT_STATIC.md`：R1 当轮仍残留旧完整死链串；R2 需补到完整旧串 `0-hit`、宽搜短语 `≤1`

### 15.3 claim-vs-reality §10.21 违反清单

1. **`JUDGEMENT_STATIC §9` 的 README baseline 自证已失效**
   - 历史表述：`README` 中 `findings 1-9` 命中。
   - 本轮实测：`README.md` 中 `findings 1-9` = `0-hit`；当前 live baseline 是 `findings 1-10`，命中 `README.md:5,32,68`。
2. **`JUDGEMENT_STATIC` 内对 `P1c` 行数的自证已失效**
   - 历史表述：多处仍写 `wc -l = 100`。
   - 本轮实测：`wc -l docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md = 136`。
3. **“不再保留具体死链号”表述不再精确**
   - `JUDGEMENT_STATIC §14.3 item 1` 与 `§14.5` 把该问题写成“已由 Q-F 处理 / 不再保留具体死链号”。
   - 但当时 `JUDGEMENT_STATIC.md:68` 仍保留历史 quoted literal；R2 已改成断词表述。
   - 本轮判定：这是**历史快照被保留**，不是 README live dead link；因此后续叙事只能写“README 已修正、JUDGEMENT 仍保留历史引文”，不能再写成 “`JUDGEMENT_STATIC` 已 0-hit”。

### 15.4 §10.31 只加不删合规 self-check

- 本轮修订边界仍只落在：
  - `docs/plans/迁移/p22/JUDGEMENT_STATIC.md`
  - `docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md`
- 修订方式：**以文末追加为主，另含 H-1 / drift 的少数 truth-correction**，不删除 `§1-§14` 历史内容。
- 历史锚点复核：
  - `§11.4`：`真正的并行单位是子切片` 仍命中 `README.md:96`
  - `§12.4`：`## 关键路径（叙事）` 仍命中 `README.md:98`
  - `§13.4`：`README 这一节只负责派工顺序与阻塞关系` 仍命中 `README.md:108`
  - `§14.4`：`P4 守卫固定分三类写` 仍命中 `P4_DependencyDirectionAndHiddenContracts.md:223`
- 当轮 diff / 行数快照已转入后续轮次续更；当前 rerun 见 `§16.6`。
- 如后续轮次要真正清掉 `JUDGEMENT_STATIC.md:68` 的历史引文，必须在不破坏年轮证据前提下单独 justify。

### 15.5 行号 drift 表更新

| 条目 | 历史记录 | 现 HEAD | 结论 |
|---|---|---|---|
| README baseline | `findings 1-9` 命中（旧轮自证） | `findings 1-9 = 0-hit`；`findings 1-10 -> README.md:5,32,68` | **已失效** |
| README `runner-only sidecar 的最小标准` | `README.md:93` | `README.md:193` | 行号上移，语义仍在 |
| README `风险矩阵（叙事）` | `README.md:88` | `README.md:178` | 行号上移，语义仍在 |
| P1c ``session` owns `SessionRuntime`` | `P1c_CodexAppSessionRuntime.md:23` | `P1c_CodexAppSessionRuntime.md:33` | 行号上移，语义仍在 |
| P2 `退订只代表 stop intake，不等于 drain` | `P2_BusRuntimeDecoupling.md:249` | `P2_BusRuntimeDecoupling.md:251` | +2 行 |
| P4 `signed-skill / native-skill contract` | `P4_DependencyDirectionAndHiddenContracts.md:118` | `P4_DependencyDirectionAndHiddenContracts.md:122` | +4 行 |

### 15.6 交给 Q-A / Q-C / Q-D 的 gap

- **Q-A（README / P0-P4 文档域）**
  1. `P2_BusRuntimeDecoupling.md` 顶部 `## 对应 findings` 仍未显式列出 `Finding 10`；若继续只写正文语义、不写编号，需同步改 `JUDGEMENT_DYNAMIC` 的相关表述。
  2. `README / P4 / codemap / debt banner / 外部 followup` 的同步仍未在 P22 外围文档层收口。
- **Q-C（契约域）**
  1. `docs/契约/*` 仍有 `runner.actors` vs `group:"runners"` repo-level 命名债。
  2. `Invoke` / `RunnerModule` / root bridge 的契约叙事仍需继续统一，不应由 `JUDGEMENT_*` 代签收。
- **Q-D（动态 / 代码事实域）**
  1. archtest baseline 仍有 **3 条** live failure。
  2. `Finding 11/12`、`pre-drain`、`watchFXShutdown` 仍需明确 upgrade / reject / deferred disposition。
  3. `P1a/P2(memory)/P2(other)/P3/P4` 的 live code blocker 仍未消失；静态层不能把“叙事接近 READY”误写成“代码已 READY”。

### 15.7 LSP 自证（Q-B 本轮）

1. `lsp_grep docs/plans/迁移/p22/README.md "runner-only sidecar 的最小标准"` -> `README.md:193`
2. `lsp_grep docs/plans/迁移/p22/README.md "H + O + M"` -> `README.md:185`
3. `lsp_grep docs/plans/迁移/p22/README.md "真正的并行单位是子切片"` -> `README.md:96`
4. `lsp_grep docs/plans/迁移/p22/README.md "关键路径（叙事）"` -> `README.md:98`
5. `lsp_grep docs/plans/迁移/p22/README.md "README 这一节只负责派工顺序与阻塞关系"` -> `README.md:108`
6. `lsp_grep docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md "runtime 守卫的 allowlist / exception 应按语义形态管理"` -> `P0_RuntimeOwnershipSkeleton.md:74`
7. `lsp_grep docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md "先落 semantic allowlist schema"` -> `P0_RuntimeOwnershipSkeleton.md:168`
8. `lsp_grep docs/plans/迁移/p22/P1b_PlatformLoopRunners.md "platformrunner.Runner + fx.Annotate"` -> `P1b_PlatformLoopRunners.md:55`
9. `lsp_grep docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md "session\` owns \`SessionRuntime"` -> `P1c_CodexAppSessionRuntime.md:33`
10. `lsp_grep docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md "退订只代表 stop intake，不等于 drain"` -> `P2_BusRuntimeDecoupling.md:251`
11. `lsp_grep docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md "callback -> helper -> goroutine/manager/store/notify"` -> `P2_BusRuntimeDecoupling.md:255`
12. `lsp_grep docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md "thread/session-user lane"` -> `P2_BusRuntimeDecoupling.md:268`
13. `lsp_grep docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md "NewActiveAgentCounter"` -> `P4_DependencyDirectionAndHiddenContracts.md:9,22,80,141`
14. `lsp_grep docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md "内部并行关系（叙事）"` -> `P4_DependencyDirectionAndHiddenContracts.md:166`
15. `lsp_grep docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md "固定分三类"` -> `P4_DependencyDirectionAndHiddenContracts.md:223`
16. `lsp_file internal/provider/codexapp/module.go:32-37` -> `fx.Invoke(spawnToolbridgePeers)` 仍在 `:35`
17. `lsp_file internal/provider/codexapp/peer_spawn.go:18-155` -> F2 现 HEAD 范围完整覆盖 `spawn/watch/restartPeer`
18. `lsp_file internal/platform/rpc/module.go:149-197` -> F4 仍为 `149-166 + 179-197`
19. `lsp_file internal/module/memory/module.go:456-467` -> F5 TeamSync callback 仍在 `:458/:463`
20. `lsp_file internal/module/memory/auto_dream_task.go:156-178` -> F7 `callback -> go func()` 仍在 `:173`
21. `lsp_file cmd/mcp-orch/orchestration/process_lifecycle.go:220-239` -> F8 `go a.waitForExit(...)` 仍在 `:222`
22. `lsp_file internal/module/memory/nested/nested_runtime.go:314-339` -> F10 `os.ReadFile(...)` 仍在 `:320`
23. `lsp_file internal/archtest/freeze_registry.go:19-25` -> empty registry 仍在 `:19`
24. `lsp_file internal/archtest/guardlib.go:22-32` -> numeric guard 常量仍为 `600/800/30/10000`
25. `lsp_xref internal/platform/toolbridge/module.go:130:6` -> `registerProxyLifecycle <- module.go:37`
## 16. 第 8 轮 Q-B Round-2 补修（2026-04-23）
### 16.1 收报状态表
|路|state|结论|要点|
|---|---|---|---|
|V9|idle|🔴|H-1 未销且 §15 自撞|
|V10|idle|🟡|§19 的 F10 / 0-hit 自证需改|
|H5|idle|🟡|Q-A / Q-B 新行号再 drift|
|H7|idle|🟡|P2 F10 清单与 P1b `30min/5min` 口径待续核|
|L1|idle|🔴|H-1 claim-vs-reality 成立|
|L5|thinking（已收 report）|🟡|§10.31 实质违规未见|
- `orchestration_list_agents` 仍因 `34 agents` 超限；本轮以 6 路 `orchestration_get_agent_report(...)` 收报。
### 16.2 H-1 修复证据
- before：完整旧串 `3-hit`；宽搜短语 `6 matches`。
- 直修：`§4` 行 68 改为断词历史描述；`§6` 行 94 改为“完整旧串 `0-hit`”；`§15` 内自撞项同步断词。
- after：完整旧串 `0-hit`；宽搜短语 `0-hit`。
- 认错：R1 chairman 声称的 authoritative 更正未在字面达成；R2 已用 `lsp_grep` `0-hit` 自验补齐。
### 16.3 R9 销账表
- H-1：✅ 已销。
- `§15.4` over-claim：✅ 已改成“以文末追加为主 + 少数 truth-correction”。
- HEAD drift authoritative：`README sidecar=227 / H+O+M=219,234 / 派工顺序=125`、`P1c 35`、`P2 266`、`P4 127/250`。
- `§9 findings 1-9`：⚠️ 仍为历史年轮；当前 baseline 以 `README.md:5,32,85` 为准。
### 16.4 本轮其他补修清单
- 修 `§15.2/§15.3/§15.4` 的 H-1 自撞表述。
- 重跑并落盘 `§10.31` diff / 行数 self-check。
- 静态稿未越界改 README / P0-P4 / 契约 / 安全运维正文。
### 16.5 交其他 Q 的 gap
- Q-A：`P1b` 的 `30min/5min` 口径与代码真值待复核；README / P0-P4 HEAD 行号需 owning 文档维护。
- Q-C：`runner.actors` vs `group:"runners"` 仍未统一。
- Q-D：`Finding 11/12`、`pre-drain/watchFXShutdown` 最终 disposition 见动态稿 `§20`；代码 live blocker 未消失。
### 16.6 §10.31 self-check（净减少 %）
- 当前 `git diff --numstat -- JUDGEMENT_*`：`JUDGEMENT_STATIC +310/-3`、`JUDGEMENT_DYNAMIC +310/-2`。
- 当前行数为 `596 / 559`；两文件仍均 `<600`。
- 历史章节净减少仍 `0%`；无 `§1-§15` 删除。
### 16.7 LSP 自证 ≥15 条
- `grep`：完整旧串 `0-hit`；宽搜短语 `0-hit`；`findings 1-10 -> README:5,32,85`。
- `grep`：`runner-only sidecar -> README:227`；`H + O + M -> README:219,234`；`README...派工顺序 -> README:125`。
- `grep`：`session owns SessionRuntime -> P1c:35`；`退订...drain -> P2:266`；`signed-skill / native-skill contract -> P4:127`；`固定分三类 -> P4:250`。
- `file`：`JUDGEMENT_STATIC:60-99,470-505`；`P2:1-18`；`peer_spawn.go:18-155`；`rpc/module.go:149-197`；`nested_runtime.go:314-339`。
- `xref/inspect/structure/completion/diagnostics`：`handleToolCallEnd <- module.go:117`；`waitForExit <- :222`；`diff_fallback.go/server.go` symbols；`DefaultApprovalTimeout` hover；`toolbridge/module.go` completion；`JUDGEMENT_*` diagnostics=`0`。

## 17. 第 9 轮 Round-8→HEAD 回灌（2026-04-24）

本节是静态层对 `JUDGEMENT_DYNAMIC.md §21` 的镜像，只补 Round-8 之后落地的 P22 P2 静态真值锚，不改写历史年轮。按 §10.31 只加不删。

### 17.1 新 owner 锚

P22 P2 thread 域现有 3 个独立 worker owner，各自命名 / 契约定型：

| owner | 文件:行 | narrow interface | callback 入口 |
|---|---|---|---|
| `taskHandoffWorker` | `internal/module/thread/task_handoff_worker.go:1-205` | `taskHandoffRefresher` | `onTurnCompleted(ev)` -> `taskHandoffWorker.Enqueue(threadID, seed)` |
| `agentLaunchedWorker` | `internal/module/thread/agent_launched_worker.go` | `agentLaunchedProcessor` | `onAgentLaunched(ev)` -> `agentLaunchedWorker.Enqueue(key, ev)`（key = agentID，空时退回 threadID） |
| `sessionRecoveryWorker` | `internal/module/thread/session_recovery_worker.go` | `sessionRecoverer` | `onAgentFailed(ev)` -> `sessionRecoveryWorker.Enqueue(target, ev)`（target = FirstNonEmpty(threadID, agentID)） |

三者统一通过 `service.startBusWorkers() / service.stopBusWorkers(ctx)` 接入 `registerSubscriptions` 的 `OnStart/OnStop` hook；Stop 语义：先 cancel 订阅，再 `drainBusWorker(ctx, name, stop)`。

### 17.2 P2_BusRuntimeDecoupling.md §验收标准行 → HEAD 证据映射

| P2 §验收标准 行 | HEAD 证据 |
|---|---|
| L428 `internal/module/thread/module.go` 不再通过 `fx.Invoke` 做 setter 型后置注入 | `grep -c "svc.bind" internal/module/thread/module.go` = `0`；`bus_callback_must_not_register_late_setter` live matcher PASS |
| L429 `thread` 事件回调不再直接做 binding store / prompt invalidation / task-handoff 重 I/O / delayed resume | `TestTaskHandoffCallbackEnqueueOnly` / `TestAgentLaunchedCallbackEnqueueOnly` / `TestAgentFailedCallbackEnqueueOnly` 均 PASS |
| L430 `backgroundResumeIfNeeded(...)` 有明确 owner、可 drain、可测试，不再是裸 `context.Background()` goroutine | `sessionRecoveryWorker` 是 owner；`TestSessionRecoveryWorkerStopCancelsCtx` 证明 ctx 短路 3s reconnect delay；`TestSessionRecoveryWorkerStopDrainsPending` 证明可 drain。**注意边界**：本条仅对 `onAgentFailed -> backgroundResumeIfNeeded` 这条 bus callback 路径成立；`archive.Unarchive` / `history.ReadMessages` / `history.ReadThreadHistory` 3 条 RPC caller 仍走原 `backgroundResumeIfNeeded` 实现（内部还有 1 处 inner `runtimesafe.SafeGo`），不在本轮范围 |
| L443 `registerMemoryHooks` shutdown path 返回前必须能证明 auto-dream 与 background extraction 已完成有界收口 | `TestMemoryHookWorkerDrainsOnStop` 对 `drainMemoryHooks(ctx, scheduler, nested, teamSync, nil)` 直接断言三个 owner 各自 `ProcessedTotal` 追上 enqueued，post-drain enqueue drop |
| L452 TeamSync 切换 runtime 时旧 watcher final flush 不会误打到新 runtime | `TestTeamSyncRuntimeSwapFinalFlush` 对 Start A→Stop A→Start B→Stop B→Start A 序列断言 `teampkg.Lifecycle` 收到的 FIFO 严格同序，Stop 前全 flush |
| L455 busy 状态下 auto-dream 触发保持 drop 而不是补跑 | `TestAutoDreamBusyDropsWithoutReplay` 填队列到 cap + overflow；Start 后只 drain cap 条，drop 条永不复活 |

### 17.3 构造器签名 drift（thread 域）

round-8：
```
NewServiceWithPromptAssemblyAndSharedFiles(logger, threadStore, bindingStore, sharedFiles, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry) Service
```

HEAD（thread S1 后尾加 2 个 optional 参数）：
```
NewServiceWithPromptAssemblyAndSharedFiles(
    ..., // 前 12 个参数不变
    promptStore promptstore.Store,
    promptClassifier classifier.Classifier,
) Service
```

`internal/module/thread/module.go` 的 `fx.Annotate(..., fx.ParamTags("", optional*13))` 相应延展；`fx.As(new(Service))` + `fx.As(new(turn.PendingLaunchSpawner))` 不变。

### 17.4 `subscriptionParams` 结构瘦身

round-8：`Lifecycle / Dispatcher(optional) / Service(optional) / PromptStore(optional) / Classifier(optional)` 共 5 字段；`registerSubscriptions` 里调 3 次 bindXxx。

HEAD：`Lifecycle / Service(optional)` 共 2 字段；`registerSubscriptions` 只负责 lifecycle hook。

### 17.5 新 test 文件锚（在 commit 里）

| 文件 | 行数 | 覆盖范围 |
|---|---|---|
| `internal/module/thread/task_handoff_worker_test.go` | 约 260 | 7 条测试（worker happy path / coalesce / drain on stop / 过 Stop 的 enqueue 被 drop / Stop 幂等 / nil refresher / callback enqueue only） |
| `internal/module/thread/agent_launched_worker_test.go` | 约 240 | 同形 7 条 |
| `internal/module/thread/session_recovery_worker_test.go` | 约 260 | 9 条（多并发 + ctx 短路 + 非 recoverable callback 早返） |
| `internal/module/memory/module_drain_test.go` | 约 170 | `TestMemoryHookWorkerDrainsOnStop` 对 drainMemoryHooks 的集合断言 |
| `internal/module/memory/memory_behavioral_guards_test.go` | 约 290 | 3 条新 memory 行为断言（busy-drops-without-replay / requires-explicit-project-scope / teamsync-runtime-swap-final-flush） |

round-8 前已有的 `auto_dream_scheduler_test.go` / `team_sync_coordinator_test.go`（若存在）等未触动。

### 17.6 archtest CC-size guard 相关修订

thread S2 引入第三个 bus worker 时，`service.stopBusWorkers` 的 3 次嵌套 `if worker != nil { if err := worker.Stop; err != nil { if s.logger != nil { warn } } }` CC = 11 > 10，触发 `TestCodeSizeGuard` 失败；当轮提交 `cdbb8a4` 引入 `drainBusWorker` helper 扁平化（CC 降到 ≤ 5）。

### 17.7 §10.31 only-append self-check

- round-8 末尾：STATIC `596 行` / DYNAMIC `559 行`。
- 本节追加：`git diff --numstat docs/plans/迁移/p22/JUDGEMENT_STATIC.md` 显示 `+N/-0`。
- §1-§16 未改动；HEAD 事实只在 §17 追加。
- 历史章节净减少保持 `0%`。

### 17.8 非本节声明边界

- 本节不回灌 `README.md` / `P0-P4` / `codemap`。那些是 owning 文档，不在 JUDGEMENT 的 shadow 职责内。
- 本节不涉及 P3 / P4；P4 的 dependency direction / contract cleanup 仍按原规划在 P2 完全收口后启动。
- `lifecycle_onstart_guard_test.go` / `runner_actor_guard_test.go` 里 P3 范围外 skeleton matcher（6 条）未触动，按原规划非本优先级。
