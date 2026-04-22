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
  - 前：`fx-convention.md §2 + §4.4`
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
  - `README.md`：旧错误锚点 `§2 + §4.4` 已 `lsp_grep` 0 命中。
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
5. `README.md:99` 仍保留 `fx-convention.md §4.4` 这类死章节号的否定式提示；章节号修正按本轮分工继续归 `Q-D`（`A1` / `X17`）。
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
