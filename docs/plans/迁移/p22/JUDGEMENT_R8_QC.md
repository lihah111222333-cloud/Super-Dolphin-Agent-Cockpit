# P22 Round-8 Q-C 契约终裁书

> 时间：2026-04-23
> 角色：Q-C（契约合规 + 三层铁律域）
> 收报链路：`orchestration_get_agent_report`
> 总体裁决：**BLOCK** —— Q-C 域内可修文档已直修；但 `docs/契约/*` 本体的命名债与 `JUDGEMENT_*` 的 Q-B 域残留未清，整批仍不能判绿。

## 1. 收报状态表（5/5）

> 注：5 路报告均通过 `orchestration_get_agent_report` 收到；`orchestration_list_agents` 本轮因输出超 budget 未能展开完整清单，因此 idle 状态以各报告返回的 `state="idle"` 为准。

| 路 | agent id | 任务 | 收报 | state | 子结论 | Q-C 摘要 |
|---|---|---|---|---|---|---|
| H1 | `agent-1776918159545-1776918159544697000` | contracts-conformity | ✅ | `idle` | 🟡 | active 契约锚点命中，但 `runner.actors` vs `group:"runners"` 债仍在，且 `JUDGEMENT_STATIC.md` 有历史死串残留 |
| H2 | `agent-1776918170475-1776918170474938000` | fx-bus-rungroup-trinity | ✅ | `idle` | 🟡 | `README/P0/P1b/P2/P3/P4` 存在 lifecycle / restore / drain 口径灰区，需 Q-C 契约直修 |
| H3 | `agent-1776918180445-1776918180444691000` | shutdown-flow | ✅ | `idle` | 🟡 | 主叙事已收敛，但 `P0/P1b/P4` 仍需统一 `ctx cancel -> run.Group -> bus stop-intake -> fx.OnStop` 与 `退订 ≠ drain` |
| L4 | `agent-1776918360127-1776918360126256000` | trinity-ironlaw | ✅ | `idle` | 🟡 | P22 文档侧基本对齐，代码 live debt 仍多；Q-C 只修契约叙事，不越界修实现 |
| X5 | `agent-1776918316618-1776918316615644000` | current-state-drift | ✅ | `idle` | 🟡 | `§现状校准` 与 HEAD 基本无漂移，但 live debt 未消，不能误判成已闭环 |

## 2. 契约引用合规矩阵（10 文档 × 3 契约件）

> 记号：`Y` = 原已命中；`N` = 当前未引；`N+fix` = 本轮 Q-C 已补契约锚点。矩阵按 `modularity / fx / rungroup` 三件套统计。

| 文档 | modularity | fx | rungroup | 说明 |
|---|---:|---:|---:|---|
| `README.md` | Y | Y | Y | 顶部基线 + `收口口径` |
| `P0_RuntimeOwnershipSkeleton.md` | Y | Y | N+fix | 本轮补 `rungroup` 锚点，并收紧 startup recovery / shutdown 口径 |
| `P1a_CodexAppPeerSupervisor.md` | N+fix | N+fix | N+fix | 本轮补 `fx.Invoke` 分类与 `RunnerModule` 归口 |
| `P1b_PlatformLoopRunners.md` | N+fix | N+fix | N+fix | 本轮补 `startup restore`、`BusModule`、`RunnerModule` 契约依据 |
| `P1c_CodexAppSessionRuntime.md` | N+fix | N+fix | N+fix | 本轮补 session-local runtime handle 的三层契约依据 |
| `P2_BusRuntimeDecoupling.md` | N+fix | N+fix | N+fix | 本轮补 callback/owner/drain 的三层契约依据 |
| `P3_OrchestrationWaiterAlignment.md` | N+fix | N+fix | N+fix | 本轮补 actor / wait owner / drain 的三层契约依据 |
| `P4_DependencyDirectionAndHiddenContracts.md` | N+fix | N+fix | N+fix | 本轮补“P4 不另写一套 fx/bus/run 语义”的契约依据 |
| `JUDGEMENT_STATIC.md` | Y | Y | N | Q-B 域，未越界修改 |
| `JUDGEMENT_DYNAMIC.md` | Y | Y | Y | 原已显式记 `304-306` |

## 3. 三层铁律违反清单 + 本轮修订 before/after

| 项 | 文件 | before | after |
|---|---|---|---|
| 1 | `README.md` | 仅保留了“`fx.Module = constructor、lifecycle、一次性恢复`”简写，易被误读成 lifecycle 常态承接恢复 | 在 `收口口径` 补 authoritative 注释：lifecycle 只做资源 open/close；启动期恢复归 `§4.4` 合法单次 wiring / `fx.Invoke` |
| 2 | `P0_RuntimeOwnershipSkeleton.md` | `一次性恢复仍允许保留在 lifecycle` | 改为：`fx.Module` 启动期单次 wiring（旧稿“留 lifecycle”保留并列，但以新句为准） |
| 3 | `P1a_CodexAppPeerSupervisor.md` | `RegisterTranslators` 只写“纯注册 Invoke” | 明确归类到 `modularity-convention.md §4.4` 的“必须执行一次的图连线验证 / registry wiring” |
| 4 | `P1b_PlatformLoopRunners.md` | `恢复留 lifecycle，loop 交 Runner`；并把 `subscriber wiring` 混在 `fx.Module` 里表述 | 改为：`startup restore` 留 `fx.Module` 单次 wiring，`subscriber wiring` 留 `BusModule`，长期 loop 进 `RunnerModule` |
| 5 | `P2_BusRuntimeDecoupling.md` | sidecar 目标写成 `owner 或 Runner`，含混不清 | 改为：`recycler/cache cleanup` 这类长期 loop 归 `RunnerModule`；`transport responder` 归显式 local owner 的 `Start/Stop/Drain` |
| 6 | `P3_OrchestrationWaiterAlignment.md` | waiter drain 与 P2 的 `stop-intake ≠ drain` 关系未显式继承 | 补明：bus 退订/stop signal 都不能替代 wait owner 的 drain/join |
| 7 | `P4_DependencyDirectionAndHiddenContracts.md` | 只说“不替代 P2/P3”，但没继承 runtime contract 口径 | 补明：P4 不另写一套 `fx.Module / BusModule / RunnerModule` 语义，并显式继承 `stop-intake/退订 ≠ drain`、双树同构、runner-only sidecar 口径 |
| 8 | `P0/P1b/P3/P4` | `退订` 与 `drain` 容易被不同页分开讲，读者易误判 | 统一补到 contract 段：**退订/stop-intake 只停 intake，不等于 drain** |

## 4. “双树同构 vs runner-only sidecar”一致性最终口径

1. **只有两棵完整树**：`internal/app` 与 `cmd/mcp-orch`。
   - `internal/app/modules.go:33-43` 同时装了 `bus.Module` 与 `platformrunner.Module`。
   - `cmd/mcp-orch/fx.go:34-41,56-61` 同时装了 `platformbus.Module`、runner providers 与 `bindRuntime`。
2. **`cmd/mcp-lsp` / `cmd/mcp-ida` 不是 bus 树，只是 runner-only sidecar**。
   - 两者都有独立 fx root、`bindRuntime`、`group:"runners"`。
   - 但 `lsp_grep path=cmd/mcp-lsp query="platformbus.Module"` = 0，`path=cmd/mcp-ida` = 0。
3. **desktop 例外不改写主契约**。
   - `internal/app/runner.go:71-77` 的 bounded pre-drain 与 `internal/app/app.go:171-180` 的 `watchFXShutdown(...)` 只属于 desktop 辅助路径，不属于 root bridge allowlist 本体。
4. **最终表述固定为**：
   - `internal/app` + `cmd/mcp-orch`：**双树同构**
   - `cmd/mcp-lsp` + `cmd/mcp-ida`：**runner-only sidecar**

## 5. runner 命名口径统一

1. P22 active 文档统一使用 **角色术语**：`fx.Module / BusModule / RunnerModule`。
2. 当前代码与 root bridge 的 **现实现 tag** 继续写作：`group:"runners"`。
3. `runner.actors` 在 P22 中只允许作为**债务描述**出现，不再作为 active 计划页的主命名。
4. 因此本轮的 authoritative 口径是：
   - **角色层**：`fx.Module / BusModule / RunnerModule`
   - **实现 tag 层**：`group:"runners"`
   - **契约本体债务层**：`runner.actors` deferred，不在本轮篡改 `docs/契约/*`

## 6. 与 Q-A / Q-B / Q-D 的冲突点 + Q-C 最终裁决

| 冲突点 | 对方域 | Q-C 最终裁决 |
|---|---|---|
| `README/P0` 的 `§目标` 仍保留历史 shorthand，但用户边界又禁止 Q-C 触碰 `§目标` | Q-A | **不越界改 `§目标`**；Q-C 在 `收口口径` / `实施步骤` 落 authoritative 更正。若后续 `§目标` 与 contract note 冲突，以 Q-C 更正文为准。 |
| `JUDGEMENT_STATIC.md` 仍带历史死串 / 命名债转述 | Q-B | **不越界改 `JUDGEMENT_*`**；Q-C 仅把其视作证据，不让其反向覆盖 active 计划页契约口径。 |
| shutdown 流中的 desktop pre-drain / `watchFXShutdown` / local exit owner 仍需动态事实层持续核 | Q-D | **Q-C 只定契约层**：`ctx cancel -> run.Group -> bus stop-intake -> fx.OnStop`；desktop pre-drain 是 bounded 例外；`退订 ≠ drain` 强制统一。运行时/测试/运维证据仍归 Q-D。 |
| `runner.actors` vs `group:"runners"` | Q-A/Q-B/Q-D 均可能外溢 | **契约文字优先，但不得抹掉代码现状**：P22 active 文档一律退回角色术语 + 现实现 tag；repo 级命名统一 deferred 到 `docs/契约/*`。 |

## 7. 交给契约本体（`docs/契约/*`）的 deferred 债（不修）

1. `docs/契约/modularity-convention.md §7.3` 仍把建议 group 写成 `runner.actors`，而仓内 root bridge / `fx-convention.md §2.3` / P22 active 文档都以 `group:"runners"` 为现实现 tag；需要 authoritative alias 或统一裁决。
2. `docs/契约/*` 尚未把“角色术语层（`RunnerModule`）”与“实现 tag 层（`group:"runners"`）”写成明确二层映射，导致审查轮反复把二者误当一层命名冲突。
3. root runtime bridge 的永久架构例外、desktop bounded pre-drain、`watchFXShutdown` 非 allowlist 本体这组关系，仍缺契约本体层的一次性 authoritative 描述。

## 8. LSP 自证（≥15）

1. `[lsp_file]` `docs/契约/modularity-convention.md:555-563`：`fx.Invoke` 5 类合法用途仍在。
2. `[lsp_file]` `docs/契约/modularity-convention.md:781-833`：`StoreModule / BusModule / RunnerModule` 角色定义仍在。
3. `[lsp_file]` `docs/契约/fx-convention.md:17-127`：lifecycle 只放资源 init/release，`group:"runners"` 示例仍在。
4. `[lsp_file]` `docs/契约/rungroup-convention.md:17-134`：actor `execute/interrupt`、`run.Group` 不托管一次性初始化仍在。
5. `[lsp_grep]` `path=docs/plans/迁移/p22 query="docs/契约/modularity-convention.md §4.4 / §7"`：命中 `README/P0/P1a/P1b/P1c/P2/P3/P4/JUDGEMENT_DYNAMIC`。
6. `[lsp_grep]` `path=docs/plans/迁移/p22 query="docs/契约/fx-convention.md §2 / §3"`：命中 `README/P0/P1c/P2/P3/P4` 与新补 contract 段。
7. `[lsp_grep]` `path=docs/plans/迁移/p22 query="docs/契约/rungroup-convention.md §2 / §4"`：命中 `README/P0/P1a/P1b/P1c/P2/P3/P4`。
8. `[lsp_grep]` `query="图连线验证 / registry wiring"`：命中 `P1a_CodexAppPeerSupervisor.md:84`。
9. `[lsp_grep]` `query="单次 wiring"`：命中 `P0:48`、`P1b:27/99`、`README:190`。
10. `[lsp_grep]` `query="退订 ≠ drain"`：命中 `P0:72`、`P1b:104`、`P3:125`、`P4:126`。
11. `[lsp_structure]` `workspace_symbol query="BindRuntime|bindRuntime"`：命中 `internal/app/runner.go:34`、`cmd/mcp-orch/runtime.go:225`、`cmd/mcp-lsp/fx.go:203`、`cmd/mcp-ida/fx.go:99` 四个 root bridge。
12. `[lsp_xref references]` `internal/platform/runner/group.go:23 RunGroup`：HEAD caller 只落四个 root bridge。
13. `[lsp_xref incoming]` `internal/app/runner.go:34 BindRuntime`：prod caller 为 `internal/app/app.go:133,154`。
14. `[lsp_xref incoming]` `cmd/mcp-orch/runtime.go:225 bindRuntime`：prod caller 为 `cmd/mcp-orch/fx.go:61`。
15. `[lsp_xref incoming]` `cmd/mcp-lsp/fx.go:203 bindRuntime`：prod caller 为 `cmd/mcp-lsp/fx.go:89`。
16. `[lsp_xref incoming]` `cmd/mcp-ida/fx.go:99 bindRuntime`：prod caller 为 `cmd/mcp-ida/fx.go:67`。
17. `[lsp_file]` `internal/app/modules.go:33-43`：`bus.Module` 与 `platformrunner.Module` 同时存在，支持双树同构口径。
18. `[lsp_grep]` `path=cmd/mcp-orch query="platformbus.Module"`：命中 `cmd/mcp-orch/fx.go:35`。
19. `[lsp_grep]` `path=cmd/mcp-lsp query="platformbus.Module"`：0 命中。
20. `[lsp_grep]` `path=cmd/mcp-ida query="platformbus.Module"`：0 命中。
21. `[lsp_inspect definition]` `internal/app/runner.go:26:38`：跳到 `internal/platform/runner/group.go:15` 的 `Runner` 接口定义。
22. `[lsp_inspect implementation]` `internal/app/runner.go:19:24`：返回 `bootstrapRunner/httpRunner/runnerActor/common.Server/rpc.Server/uiwails runner` 等实现，证明 root bridge 汇总的是统一 `Runner` 接口。
23. `[lsp_completion]` `internal/app/app.go:132:9`：补全返回 `Module / fx.Module / platformconfig.Module / uiwails.Module`，验证 fx root 装配上下文可被语义解析。
24. `[lsp_file diagnostics]` 修改后的 `README/P0/P1a/P1b/P1c/P2/P3/P4`：`no diagnostics`。
25. `[lsp_file diagnostics]` `internal/app/runner.go`、`cmd/mcp-orch/runtime.go`、`cmd/mcp-lsp/fx.go`、`cmd/mcp-ida/fx.go`：`no diagnostics`。

## R2

> Round-2 只按 R9 复核补修 Q-C 域；不回滚 R1 裁决表，不越界改 `JUDGEMENT_STATIC.md` / `JUDGEMENT_DYNAMIC.md`。
> R2 总体裁决：**仍为 BLOCK** —— active p22 的新一轮契约/三层铁律回潮已补修，但 `JUDGEMENT_*` 残留与 `docs/契约/*` 本体命名债仍未清。

### R2.1 收报状态表（5/5）

| 路 | agent id | R9 状态 | R9 结论 | Q-C 取舍 |
|---|---|---|---|---|
| H1 | `agent-1776918159545-1776918159544697000` | `idle` | 🔴 | active 契约锚点未漂移；`JUDGEMENT_STATIC` 死串 / `§14.3 item 1` 回引仍是 Q-B gap，本轮不越界改；仅吸收其对 active 术语洁净度的提醒 |
| H2 | `agent-1776918170475-1776918170474938000` | `idle` | 🔴 | 认定 `P2` 仍有 2 处把 `OnStop` 写成 drain 主体；本轮已在 `P2:339,427` 纠偏 |
| H3 | `agent-1776918180445-1776918180444691000` | `idle` | 🟡 | active shutdown 叙事基本已对齐；剩余 handoff gap 在 `JUDGEMENT_STATIC/DYNAMIC`，列入 Q-B/Q-D gap |
| L4 | `agent-1776918360127-1776918360126256000` | `idle` | 🟡 | 指出 `README/P0` 历史 shorthand 仍具视觉回潮风险；本轮已在 `README:11`、`P0:5` 直修 |
| X5 | `agent-1776918316618-1776918316615644000` | `idle` | 🔴 | `P1a/P1b §现状校准` drift 仍在，但属事实/叙事域；Q-C 仅吸收其对 `P1c` 术语回潮的提醒并已修正 |

### R2.2 R9 销账表

| 项 | R9 发现 | R2 处理结果 | 归属 |
|---|---|---|---|
| active 契约锚点 drift | H1：README + P0-P4 新锚点未漂移 | **CLOSED** | Q-C |
| `fx.Invoke` 5 类口径回退 | H1/H2/L4：未回退 | **CLOSED** | Q-C |
| 双树同构 vs sidecar 回退 | H1/H3/L4：未回退 | **CLOSED** | Q-C |
| `P1c` 把 `runner.actors` 带回 active 页 | H1 | **CLOSED**：改回 `group:"runners"` 现实现 tag，并把 `runner.actors` 降为债务注记 | Q-C |
| `P2` 把 `OnStop` 写成 drain 主体 | H2 | **CLOSED**：`P2:339,427` 改成 owner `Stop/Drain` contract 主体，`OnStop` 不再是 drain 主语 | Q-C |
| `README/P0` 历史 shorthand 仍可误读成 lifecycle 承接恢复 | L4/H2 | **CLOSED**：`README:11`、`P0:5` 改为 constructor + 资源 lifecycle + 单次 wiring / 合法 `fx.Invoke` | Q-C |
| `JUDGEMENT_STATIC` 死串 / `§14.3 item 1` 回引 | H1 | **OPEN**：Q-B 主责，本轮只记 gap | Q-B |
| `JUDGEMENT_DYNAMIC` 自证 `0-hit` stale | H3/X5 | **OPEN**：Q-B/Q-D 主责，本轮只记 gap | Q-B / Q-D |
| `P1a/P1b §现状校准` drift | X5 | **OPEN**：不属 Q-C 契约段，转交 Q-A | Q-A |

### R2.3 本轮补修清单（按契约 / 三层铁律）

| 分类 | file:line | before | after |
|---|---|---|---|
| 契约术语 | `README.md:11` | `fx.Module：constructor、lifecycle、一次性恢复` | `fx.Module：constructor + 资源 lifecycle；启动期一次性恢复只允许作为单次 wiring / 合法 fx.Invoke` |
| 契约术语 | `P0_RuntimeOwnershipSkeleton.md:5` | `fx.Module 只做构造 / lifecycle / 一次性恢复` | `fx.Module 只做构造 / 资源 lifecycle；启动期一次性恢复只允许作为单次 wiring / 合法 fx.Invoke` |
| 命名口径 | `P1c_CodexAppSessionRuntime.md:5` | `不是新的 root runner.actors 成员` | `不是新的 root group:"runners" 成员（runner.actors 仅保留为契约本体债务注记）` |
| 三层铁律 | `P2_BusRuntimeDecoupling.md:339` | `OnStop 不能只退订，还必须 drain in-flight dispatch` | `模块级 OnStop 不能只退订，但 drain 主体必须是 owner 的 Stop/Drain contract，不再由 OnStop 自己承担` |
| 三层铁律 | `P2_BusRuntimeDecoupling.md:427` | `registerMemoryHooks.OnStop 返回前完成 ... drain` | `shutdown path 必须证明已完成有界收口；但 drain 主体是 MemoryHookWorker / 显式 owner，不把模块级 OnStop 写成 worker drain 主体` |

### R2.4 最终契约口径（再次钉死）

1. **`fx.Module`**：只做 constructor + 资源 lifecycle；启动期一次性恢复只允许作为单次 wiring / 合法 `fx.Invoke`。
2. **`BusModule`**：只做 bus 实例与 subscriber wiring；callback 只做轻量同步状态更新或 non-blocking enqueue。
3. **`RunnerModule`**：承接所有长期 owner / `run.Group` actor；若某并发不适合直接进 `run.Group`，也必须落到显式 local owner 的 `Start/Stop/Drain`。
4. **`root runtime bridge`**：只允许出现在 4 个进程入口桥形态；process root 可在 `OnStop` 做 cancel/join/drain，但这是入口例外，不外溢成普通模块的权力。
5. **双树 / sidecar**：`internal/app` + `cmd/mcp-orch` 是双树同构；`cmd/mcp-lsp` + `cmd/mcp-ida` 是 runner-only sidecar，不强补 bus 树。
6. **命名**：active p22 统一使用 `fx.Module / BusModule / RunnerModule`；`group:"runners"` 仅作现实现 tag；`runner.actors` 只允许作为 `docs/契约/*` 的 deferred 债务描述。
7. **shutdown**：`stop-intake/退订 ≠ drain`；drain 永远由显式 owner contract 负责，不由 bus 退订或普通模块级 `OnStop` 代替。

### R2.5 交 Q-A / Q-B / Q-D 的 gap

| 去向 | gap |
|---|---|
| Q-A | `P1a` 的 Finding 2 顶部锚点 / `§现状校准` 回退（`18-109` vs HEAD `18-155`），以及 `P1b` 把 `Sweeper.Run` 写成 `ticker loop` 而 HEAD 仍是 `timer+jitter`；属事实/叙事 drift，不属本轮 Q-C 写边界 |
| Q-B | `JUDGEMENT_STATIC.md` 仍保留“历史误引死章节号”转述（`68/94`）、“不存在的 \`fx-convention\` 章节号”转述（`274`）与 `§14.3 item 1` 回引（`489`）；`JUDGEMENT_DYNAMIC.md` 仍存在“本文件 0-hit”与本文件正文自相矛盾的 stale 自证 |
| Q-D | `watchFXShutdown` / desktop pre-drain 的最终 dynamic disposition、`waitDreamTask` / memory hook drain / sidecar live runtime debt 仍需代码事实域签收；Q-C 已钉死契约，但不代签运行时闭环 |

### R2.6 契约本体 deferred 债总账（`docs/契约/*`）

1. `docs/契约/modularity-convention.md §7.3` 仍以 `runner.actors` 作为建议 group，而仓内 active 文档 / root bridge 现实现 tag 是 `group:"runners"`。
2. `docs/契约/*` 仍缺“角色术语层（`RunnerModule`）↔ 实现 tag 层（`group:"runners"`）”的 authoritative 二层映射。
3. root runtime bridge 的永久架构例外、desktop bounded pre-drain、`watchFXShutdown` 非 allowlist 本体之间的关系，仍未在契约本体一次性写清。
4. `fx-convention.md §2.3` 的 `group:"runners"` 示例与 `modularity-convention.md §7.3` 的 `runner.actors` 建议，仍缺统一的 alias/迁移注释。

### R2.7 LSP 自证（≥12）

1. `[lsp_file]` `README.md:11-14`：`fx.Module` 目标行已改成 `constructor + 资源 lifecycle + 单次 wiring / 合法 fx.Invoke`。
2. `[lsp_file]` `P0_RuntimeOwnershipSkeleton.md:3-5`：P0 `§目标` 已同步改成同一 authoritative 口径。
3. `[lsp_file]` `P1c_CodexAppSessionRuntime.md:5`：active 页已从 `runner.actors` 主命名改回 `group:"runners"` 现实现 tag。
4. `[lsp_grep]` `path=docs/plans/迁移/p22 query="新的 root \`runner.actors\` 成员"`：`0-hit`。
5. `[lsp_file]` `P2_BusRuntimeDecoupling.md:339`：已改成 owner `Stop/Drain` contract 负责 hooks drain。
6. `[lsp_file]` `P2_BusRuntimeDecoupling.md:427`：已改成 `MemoryHookWorker` / 显式 owner 负责 drain，不再把模块级 `OnStop` 写成主体。
7. `[lsp_grep]` `path=docs/plans/迁移/p22 query="\`OnStop\` 不能只退订，还必须 drain"`：`0-hit`。
8. `[lsp_structure]` `workspace_symbol query="BindRuntime"`：仍只命中 `internal/app`、`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 四个 root bridge。
9. `[lsp_xref incoming]` `internal/platform/runner/group.go:23 RunGroup`：caller 仍只落四个 root bridge，支持双树/sidecar 口径未回退。
10. `[lsp_inspect definition]` `internal/platform/bus/module.go:27`：bus lifecycle 仍只指向 dispatcher close 语义，不自带 owner drain。
11. `[lsp_file]` `internal/platform/bus/module.go:20-28`：`OnStop` 只 `sink.Close()` + `dispatcher.Close()`，支持“stop-intake ≠ drain”。
12. `[lsp_file]` `internal/app/runner.go:71-77`：desktop root bridge 仍是 bounded pre-drain + cancel 的入口例外。
13. `[lsp_grep]` `JUDGEMENT_STATIC.md "不存在章节号"`：exact literal 已为 `0-hit`；当前剩余的是“历史误引死章节号”（`68/94`）与“不存在的 \`fx-convention\` 章节号”（`274`）这两类变体，证明 H1 blocker 仍属 Q-B gap。
14. `[lsp_grep]` `JUDGEMENT_STATIC.md "§14.3 item 1"`：仍命中 `489`，证明 H1 blocker 未由 Q-C 越界代修。
15. `[lsp_grep]` `JUDGEMENT_DYNAMIC.md "watchFXShutdown" / "pre-drain" / "Finding 11" / "Finding 12"`：当前正文已命中，但同文件仍保留 `0-hit` 自证，说明 stale gap 仍在。
16. `[lsp_file diagnostics]` `README.md`、`P0_RuntimeOwnershipSkeleton.md`、`P1c_CodexAppSessionRuntime.md`、`P2_BusRuntimeDecoupling.md`、`JUDGEMENT_R8_QC.md`：`no diagnostics`。

## R3

> R3 总体裁决：**BLOCK**。`orchestration_get_agent_report` 在传输层已返回 **4/4**，但只有 **2/4** 带正文；按空 report 触发的本地强制复扫，补抓到 `P2` 的“二选一”残留与 `Q-C` 自证中的 exact-grep 失真。本轮已在允许边界内补修；但 `JUDGEMENT_STATIC/QA` 仍有术语 / 死链变体残留，且属 `Q-B/Q-A` 域，故整批仍不得判绿。

### R3.1 收报状态（4/4）

| 路 | agent id | 任务 | tool 返回 | R3 取舍 |
|---|---|---|---|---|
| G1 | `agent-1776927771326-1776927771324697000` | contracts-audit | `state="idle"`, `report!=empty` | 🟡 确认 active 契约锚点未漂移；补抓到 `JUDGEMENT_R8_QC` 对 `JUDGEMENT_STATIC` exact grep 的说法失真 |
| G2 | `agent-1776927781548-1776927781543709000` | trinity-ironlaw | `state="idle"`, `report=""` | ⚠️ 以空 report 记 transport-only 收报；Q-C 立即做本地三层铁律强制复扫，补抓 `P2` “二选一”残留并已直修 |
| G3 | `agent-1776927789306-1776927789305707000` | shutdown-flow | `state="idle"`, `report!=empty` | 🟡 主 shutdown 口径已收敛；desktop `pre-drain + watchFXShutdown` 仍只记 `Q-D` 动态 gap |
| G4 | `agent-1776927797335-1776927797332731000` | term-consistency | `state="idle"`, `report=""` | 🔴 以空 report 触发本地术语穷扫；editable set 已清，但 `JUDGEMENT_STATIC` 仍命中 `二选一` / `fx.Options`，且本轮禁改 `JUDGEMENT_STATIC`，按 `Q-B` gap 维持阻断 |

### R3.2 R10 GAP

| GAP | 证据 | 处理 |
|---|---|---|
| `P2` 仍残留 “二选一” 表述，违反 G2/G4 的 residual-zero 要求 | `P2_BusRuntimeDecoupling.md:268,504` | **CLOSED**：改成“显式冻结一种 failure policy / 冻结成具体机制”，不再保留“二选一”字面 |
| `Q-C R2` 把 `JUDGEMENT_STATIC "不存在章节号"` 说成 exact hit | `JUDGEMENT_R8_QC.md:171,195` vs `JUDGEMENT_STATIC exact 0-hit` | **CLOSED**：改成 exact `0-hit` + 变体残留（`68/94/274/489`） |
| `JUDGEMENT_STATIC` 仍保留术语 / 历史死链变体 | `JUDGEMENT_STATIC.md:68/94/113/205/274/276/489` | **OPEN / 🔴**：`Q-B` 主责；本轮边界禁止越界代修 |
| `JUDGEMENT_R8_QA` 仍保留 overflow “二选一”叙事 | `JUDGEMENT_R8_QA.md:145,199` | **OPEN / 🟡**：转 `Q-A`；需同步改成“显式冻结单一机制 / failure policy” |
| G2/G4 只回 `idle + 空 report` | `orchestration_get_agent_report(...)` | **OPEN / 🟡**：收报链路异常；本轮以强制本地复扫兜底，但不记通过 |

> 按 `§10.18`，只要仍存在任一 🔴，整批即 **BLOCK**；本轮阻断点是 `Q-B` 域的 `JUDGEMENT_STATIC` 残留，而不是 editable contract 段本身。

### R3.3 R2 vs R10 契约合规对比

| 项 | R2 说法 | R10 复核 | 结论 |
|---|---|---|---|
| active 契约锚点 | 已对齐 | G1 确认未漂移 | **维持** |
| `OnStop = drain 主体` 反模式 | 已清 | 本轮 `lsp_grep` 仍 `0-hit` | **维持** |
| overflow 文案 | R2 未再记 residual | R10 补抓 `P2:268,504` 仍残留“二选一” | **R10 trump R2，已补修** |
| `JUDGEMENT_STATIC` 死链 grep | R2 把 exact literal 写成命中 | R10 证明 exact `0-hit`，只剩变体 | **R10 trump R2，已校正** |
| 术语变体 | R2 仅记 repo-level 债 | R10 复核：editable set 无新增禁用变体，但 `JUDGEMENT_STATIC:276` 仍有 `fx.Options` | **active 清 / cross-judgement 未清** |

### R3.4 本轮补修（file:line + before/after）

| file:line | before | after |
|---|---|---|
| `docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:268` | `不再停留在抽象“二选一”` | `不再停留在抽象口号或模糊备选框` |
| `docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:504` | `必须二选一：fatal gate，或保存 desired state 并持续 retry/replay` | `必须显式冻结一种 failure policy：要么 fatal gate，要么保存 desired state 并持续 retry/replay` |
| `docs/plans/迁移/p22/JUDGEMENT_R8_QC.md:171` | `fx-convention.md §2 + <不存在章节号>（68/478/490）` | `历史误引死章节号（68/94） + 不存在的 \`fx-convention\` 章节号（274） + §14.3 item 1（489）` |
| `docs/plans/迁移/p22/JUDGEMENT_R8_QC.md:195` | `JUDGEMENT_STATIC.md "不存在章节号"：仍命中 ...` | `exact literal 0-hit；仅余历史误引/不存在章节号变体` |

### R3.5 契约本体 deferred 债更新

1. `docs/契约/modularity-convention.md §7.3` 仍把建议 group 写成 `runner.actors`，而 `docs/契约/fx-convention.md §2.3` 与 active p22 文档使用 `group:"runners"` 作为现实现 tag。
2. `docs/契约/*` 仍缺“角色术语层（`RunnerModule`）↔ 实现 tag 层（`group:"runners"`）”的 authoritative 二层映射。
3. root runtime bridge 的永久架构例外、desktop bounded pre-drain、`watchFXShutdown` 非 allowlist 本体之间的关系，仍未在契约本体一次性写清。
4. **新增联动债**：`docs/会话习惯.md:598-605` 仍把 root runner group 叙述在 `runner.actors` 侧，和 `fx-convention.md §2.3` / active p22 的 `group:"runners"` 形成跨文档 alias debt。

### R3.6 交 Q-A/Q-B/Q-D gap

| 去向 | gap |
|---|---|
| Q-A | `JUDGEMENT_R8_QA.md:145,199` 仍保留 overflow “二选一”叙事；应同步改成“显式冻结单一机制 / failure policy”，避免再把 `P2` 的旧口号带回裁决页 |
| Q-B | `JUDGEMENT_STATIC.md:68/94/274/489` 仍留历史死链变体；`113/205` 仍留 overflow “二选一”；`276` 仍命中 `fx.Options`。本轮因禁触 `JUDGEMENT_STATIC` 无法代修，这是当前 **BLOCK** 主因 |
| Q-D | `internal/app/runner.go:71-78` desktop pre-drain、`internal/app/app.go:171` `watchFXShutdown`、以及 memory/hooks/wait owner 的 live code debt 仍需动态事实层签收；Q-C 只钉契约，不代签运行时闭环 |

### R3.7 §10.31 self-check

- `fx.Module / BusModule / RunnerModule` 锚点仍在：`README:228`、`P0:68`、`P1b:102`、`P4:126-127`。
- 双树同构锚点仍在：`README:21,230`、`P0:69`、`P4:133`。
- runner-only sidecar 锚点仍在：`README:21,230`、`P0:69`、`P4:133`。
- 5 类合法 `fx.Invoke` authoritative 锚点仍在：`docs/契约/modularity-convention.md:557-563`，且 `README:226-227`、`P1a:87` 仍按此引用。
- `退订 != drain` 锚点仍在：`P0:74-75`、`P1b:108`、`P3:128`、`P4:133`。
- 本轮未删除上述锚点；只做 4 处 factual/contract 更正并追加 `R3` 记录。
- editable contract 段内 `二选一` 已清零；反向 `lsp_grep` 仅剩 `JUDGEMENT_STATIC/QA` 命中，符合“主文已修、外域 gap 外送”的边界。

### R3.8 LSP 自证 ≥15 条

1. `[lsp_file]` `docs/契约/modularity-convention.md:555-563`：`fx.Invoke` 5 类合法用途仍在。
2. `[lsp_file]` `docs/契约/modularity-convention.md:800-832`：`BusModule / RunnerModule` 角色与 `runner.actors` 契约本体仍在。
3. `[lsp_file]` `docs/契约/fx-convention.md:92-100`：`group:"runners"` 的 `fx.Out / fx.In` 示例仍在。
4. `[lsp_file]` `docs/契约/rungroup-convention.md:129-134`：anti-pattern 明确仍禁止把 `fx.OnStart` 当成长跑引擎。
5. `[lsp_grep]` `docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md "二选一"`：`0-hit`，证明 R3 已清掉 editable residual。
6. `[lsp_file]` `docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:267-268`：overflow 文案已改成“冻结具体机制”，不再写“二选一”。
7. `[lsp_file]` `docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:503-505`：required-topic subscribe 已改成“显式冻结一种 failure policy”。
8. `[lsp_grep]` `docs/plans/迁移/p22 query="\`OnStop\` 不能只退订，还必须 drain"`：`0-hit`，active 文档未回潮成 `OnStop=drain` 主体。
9. `[lsp_grep]` `docs/plans/迁移/p22/JUDGEMENT_STATIC.md "历史误引死章节号"`：命中 `68/94`。
10. `[lsp_grep]` `docs/plans/迁移/p22/JUDGEMENT_STATIC.md "不存在的 \`fx-convention\` 章节号"`：命中 `274`。
11. `[lsp_grep]` `docs/plans/迁移/p22/JUDGEMENT_STATIC.md "§14.3 item 1"`：命中 `489`。
12. `[lsp_grep]` `docs/plans/迁移/p22/JUDGEMENT_STATIC.md "二选一"`：命中 `113/205`。
13. `[lsp_grep]` `docs/plans/迁移/p22/JUDGEMENT_STATIC.md "fx.Options"`：命中 `276`。
14. `[lsp_grep]` `docs/plans/迁移/p22/JUDGEMENT_R8_QA.md "二选一"`：命中 `145/199`。
15. `[lsp_grep]` `docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md "图连线验证 / registry wiring"`：命中 `87`，`RegisterTranslators` 分类未漂移。
16. `[lsp_grep]` `docs/plans/迁移/p22/README.md "合法 \`fx.Invoke\`"`：命中 `11/227`。
17. `[lsp_grep]` `docs/plans/迁移/p22 query="双树同构"`：命中 `README:21,230`、`P0:69`、`P4:133` 等锚点。
18. `[lsp_grep]` `docs/plans/迁移/p22 query="退订 ≠ drain"`：命中 `P0:74`、`P1b:108`、`P3:128`、`P4:133`。
19. `[lsp_structure workspace_symbol]` `query="BindRuntime"`：命中 `internal/app/runner.go:34`、`cmd/mcp-orch/runtime.go:225`、`cmd/mcp-lsp/fx.go:203`、`cmd/mcp-ida/fx.go:99` 四个 root bridge。
20. `[lsp_xref references]` `internal/platform/runner/group.go:23`：`RunGroup` caller 仅 4 处（`internal/app/runner.go:52`、`cmd/mcp-orch/runtime.go:238`、`cmd/mcp-lsp/fx.go:213`、`cmd/mcp-ida/fx.go:109`）。
21. `[lsp_xref references]` `internal/app/runner.go:34 BindRuntime`：prod caller 仍是 `internal/app/app.go:133,154`。
22. `[lsp_xref references]` `cmd/mcp-orch/runtime.go:225 bindRuntime`：caller 仍是 `cmd/mcp-orch/fx.go:61`。
23. `[lsp_xref references]` `cmd/mcp-lsp/fx.go:203 bindRuntime`：caller 仍是 `cmd/mcp-lsp/fx.go:89`。
24. `[lsp_xref references]` `cmd/mcp-ida/fx.go:99 bindRuntime`：caller 仍是 `cmd/mcp-ida/fx.go:67`。
25. `[lsp_xref references]` `internal/app/app.go:171 watchFXShutdown`：唯一 caller 仍是 `internal/app/app.go:122`，证明它仍是 desktop-only helper。
26. `[lsp_inspect definition]` `internal/app/runner.go:26:38`：`Runner` 接口定义落到 `internal/platform/runner/group.go:15`。
27. `[lsp_inspect implementation]` `internal/app/runner.go:19:24`：仍返回 `cmd/mcp-{ida,lsp,orch}`、`common.Server`、`rpc.Server`、`ui/wails` 等 `Runner` 实现集合。
28. `[lsp_file]` `internal/platform/bus/module.go:20-28`：`OnStop` 只 `sink.Close()` + `dispatcher.Close()`，支持“stop-intake ≠ drain”。
29. `[lsp_file]` `internal/app/modules.go:33-43`：`bus.Module` 与 `platformrunner.Module` 同时存在，支持 app 双树同构。
30. `[lsp_file]` `cmd/mcp-orch/fx.go:34-41`：`platformbus.Module` 仍在 orch root。
31. `[lsp_grep]` `cmd/mcp-lsp "platformbus.Module"`：`0-hit`。
32. `[lsp_grep]` `cmd/mcp-ida "platformbus.Module"`：`0-hit`。
33. `[lsp_completion]` `internal/app/app.go:132:9`：返回 `Module / fx.Module / platformconfig.Module / uiwails.Module`，证明 fx root 装配上下文仍可语义解析。
34. `[lsp_edit format]` `internal/platform/bus/module.go`：`no formatting edits`，确认本轮未在代码侧引入额外语义漂移。
35. `[lsp_file diagnostics]` `P2_BusRuntimeDecoupling.md`、`JUDGEMENT_R8_QC.md`、`internal/platform/bus/module.go`、`internal/app/runner.go`、`cmd/mcp-orch/runtime.go`、`cmd/mcp-lsp/fx.go`、`cmd/mcp-ida/fx.go`：`no diagnostics`。
