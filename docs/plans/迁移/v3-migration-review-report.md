# V3 迁移子文档审查报告

> 审查日期：2026-03-20
> 审查范围：v3-module-migration-details.md / v3-two-zone-dry-enrichment.md / v3-framework-usage-guide.md

## 1. 总体评价

需要大改。

三份子文档的方向大体正确，但当前不能作为实施基线。LSP 抽查已经确认存在 5 类硬偏差：V2 来源路径写错、契约路径口径冲突、框架约束漏项、模块边界过度拆分、代码量口径不可加总。

## 2. 维度 1：现状符合性

### 2.1 符合项（抽查通过）

- `thread/turn` 的 V2 入口面真实存在，而且高度耦合在一个注册函数内。`go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113` 实际同时注册 `thread/*` 与 `turn/*`。
- `turn` 的 typed 参数与 review 派生流真实存在。`go-agent-v2/internal/apiserver/methods_turn.go:48-186` 包含 `turnStartTyped`、`turnSteerTyped`、`reviewStartTyped`。
- `skill` 的双入口现状真实存在。`go-agent-v2/internal/service/skills_core.go:59-493` 定义 `SkillService`，`go-agent-v2/internal/skills/manager.go:60-88` 另有 `Manager`。
- `orchestration` 的复杂度判断成立。`go-agent-v2/internal/runner/manager.go` 为 521 行，`go-agent-v2/internal/runner/manager_event.go` 为 530 行，`go-agent-v2/internal/store/task_dag_phase1.go` 为 555 行，`go-agent-v2/internal/apiserver/dagwatcher/` 实际有 11 个源文件。
- `workspace` 的复杂度判断成立。`go-agent-v2/internal/service/workspace.go:176-727` 与 `go-agent-v2/internal/service/workspace_file_ops.go:12-153` 构成真实核心；`MergeRun` 还被 `workspace_methods.go`、`tool_provider_adapters.go`、`resource_adapters.go` 多点调用。
- `uistate` 的高扇入判断成立。`go-agent-v2/internal/uistate/runtime_timeline.go` 为 546 行，`go-agent-v2/internal/apiserver/server_event_handler.go` 为 558 行，`go-agent-v2/internal/dashboard/state_service.go` 为 366 行。
- 子文档对“当前 V3 只有前置骨架”的描述基本准确。LSP 已确认 `internal/rpc/module.go`、`internal/bus/event_bus.go`、`internal/store/module.go`、`internal/app/module.go`、`internal/runner/state_machine.go` 存在；`internal/module/thread/module.go`、`internal/platform/rpc/module.go`、`internal/provider/unified/module.go`、`internal/tool/lsp/module.go`、`internal/mcpserver/lsp/module.go` 当前均不存在。
- `v3-framework-usage-guide.md` 对当前骨架的两条矫正是准确的：`internal/bus/event_bus.go` 仍在使用包级 `event.Emit` / `event.On`；`internal/store/module.go` 确实把 DB 与 store 装配混在一个文件。

### 2.2 偏差项（需修正）

- `provider/unified` 的 V2 来源写错。文档写的是 `go-agent-v2/pkg/agentsdk/provider/*`，但该目录在实际仓库中不存在。LSP 已确认真实来源应落在 `go-agent-v2/pkg/agentsdk/claude/*`、`go-agent-v2/pkg/agentsdk/codex/*`、`go-agent-v2/pkg/agentsdk/agentcore/*` 以及 `internal/apiserver/codexadapter/*`。
- `module/thread` 的 RPC 面严重漏项。实际 `registerThreadTurnMethods()` 还包含 `thread/name/set`、`thread/resolve`、`thread/loaded/list`、`thread/realtime/*`、`thread/rollback`、`thread/undo`、`thread/model/set`、`thread/personality/set`、`thread/approvals/set`、`thread/mcp/list`、`thread/skills/list`、`thread/backgroundTerminals/clean`、`thread/debugMemory`；子文档没有给出迁移归宿，也没有声明废弃。
- 代码量估算在部分模块上偏乐观。`workspace` 核心服务仅 `workspace.go + workspace_file_ops.go` 就已达 881 行，还未计 RPC、tool adapter、store；文档给出的 V3 500-700 行缺乏可信压缩依据。
- `taskdagphase1` 被单列为 V3 store 包，但 V2 代码真实形态仍是同一个 `TaskDAGStore` 的扩展方法集，拆成独立包会制造人为边界。

### 2.3 缺失项（文档未覆盖）

- 缺少“现有 V2 方法处置表”。尤其是 `thread/*` 中非核心但已在线上的方法，当前没有 `保留 / 下沉 / 合并 / 删除` 的明确判定。
- 缺少 `uistate` 事件补丁语义的显式迁移单元。`runtime_timeline.go` 中的 duplicate suppression、approval patch、requestId/callId 关联、thinking/assistant merge 不是可忽略细节。
- 缺少 `task_dag_phase1` 的 lease / wakeup / turn binding 事务边界说明。V2 实码显示这是 `orchestration` 的核心一致性面，不是附属功能。

## 3. 维度 2：契约符合性

### 3.1 符合项

- `fx` 约束总体正确。子文档普遍把 `fx` 限定在 `module.go` 级装配；当前骨架 `internal/app/runner.go` 也已使用 `fx.Out` + `group:"runners"`，符合 `fx-convention.md`。
- `run.Group` 角色定义正确。`v3-framework-usage-guide.md` 明确了 execute/interrupt 模型，与 `rungroup-convention.md` 一致。
- `stateless` 的主约束正确。文档明确要求 `FiringQueued`，并反对 `proc.State + effectiveState()` 双真相，这与 `statemachine-event-convention.md` 一致。
- `kelindar/event` 的主约束正确。子文档已经明确指出当前 `event.Default` 只适合 demo，正式实现必须注入 `*event.Dispatcher`。
- 子文档主动纠正了主文档中不存在的 `stateless-convention.md` / `event-convention.md` 链接，改为指向 `statemachine-event-convention.md`，这一步是正确的。

### 3.2 违规项（需修正）

- `sqlc` 输出路径口径冲突。`sqlc-convention.md` 已固定要求 `internal/store/sqlc/`。
- 受影响文档 1：`v3-framework-usage-guide.md` 仍写 `internal/store/sqlcgen` 或 `internal/store/sqlc/`。
- 受影响文档 2：`v3-module-migration-details.md` 仍写 `internal/store/sqlcgen/*`。
- 受影响文档 3：`v3-two-zone-dry-enrichment.md` 仍写 `sqlcgen`。
- 这不是文字差异，而是生成代码路径、import 规则、archtest、CI 流程的全面冲突。
- `jrpc2` 指南未落实严格绑定契约。`jrpc2-convention.md` 已明确公共方法应使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`；`v3-framework-usage-guide.md` 的示例与迁移结论仍停留在 `handler.New` / `NewValidated(...)`，未覆盖 object-only 与 unknown-field 拒绝。
- `Two-Zone` 与模块细化文档的共享 helper 命名不一致。`v3-two-zone-dry-enrichment.md` 把分页能力定义为 `platform/shared/cursor.go`，但 `v3-module-migration-details.md` 仍在 `uistate`、`dashboard` 中引用 `platform/shared/pagination`。这会直接制造错误 import。
- `Two-Zone` 文档要求 `module/thread`、`module/skill`、`module/workspace`、`module/ida` 额外落 `helpers.go`，但 `v3-module-migration-details.md` 的目标文件结构没有这些文件。模块形状定义不一致。

## 4. 维度 3：更优方案建议

### 4.1 可合并模块

- `store/taskdagphase1` 应并回 `store/taskdag`。证据不是主观偏好，而是 V2 代码本身：`task_dag.go` 与 `task_dag_phase1.go` 都挂在同一个 `TaskDAGStore` 上，处理的是同一聚合根与同一事务边界。

### 4.2 过度拆分模块

- `module/coderun` 与 `tool/code` 当前有明显双计数与双边界风险。两者都直接声明源自 `go-agent-v2/internal/executor/code_runner.go` 与 `go-agent-v2/pkg/toolsdk/tools/code_run.go`。如果两边都承接策略，必然重复。
- `module/ida` 与 `tool/ida` 同样重叠。两者都把 `go-agent-v2/pkg/idamcp/*` 与 `go-agent-v2/internal/mcp/runtime_ida.go` 计入来源；需要明确 `module/ida` 只拥有 lifecycle/gateway，`tool/ida` 只拥有 tool schema / envelope。
- `module/orchestration` 与 `tool/orchestration` 的边界也偏虚。两者都把 `go-agent-v2/pkg/toolsdk/tools/orchestration.go` 作为来源。tool 侧必须退化为 facade，否则会复制一套业务校验。
- `module/turn` 对外提供 `PrepareService`、`RuntimeService`、`Tracker`、`ReviewService` 四类对象，FX 图面过宽。建议对外只暴露一个 facade，内部协作留在 package 内部。

### 4.3 接口简化建议

- `thread` 需要一个单独的“线程控制面处置表”，把 `config`、`realtime`、`slash-command wrapper`、`read/list/messages` 明确拆成可审计的子面，而不是只列核心方法。
- `turn` 需要把 review 明确降为 turn 派生流，不要在模块接口层形成第二个一级服务。
- `tool/*` 包统一限定为 schema + request/response envelope + facade 适配，不持有策略、不持有事务、不持有状态机。

### 4.4 遗漏逻辑

- `module/thread` 遗漏了 V2 现有 thread 控制面的大量方法，缺少显式处置结论。
- `module/uistate` 没有把 backfill / duplicate suppression / approval patch / requestId 关联列成明确迁移对象，但实际复杂度已经在 `runtime_timeline.go` 中展开。
- `module/orchestration` 写到了 phase1 watcher，却没有把 wakeup claim、worker lease、running-node turn binding 这些 `task_dag_phase1.go` 中的强事务语义拆成明确子任务。
- `provider/unified` 没有对应到真实 V2 来源树，导致后续 provider-neutral contract 的归纳基础本身就是错的。

## 5. 维度 4：可维护性评估

### 5.1 优势点

- 模块化方向正确。V2 实码已经证明 `apiserver.Server`、`runner.AgentManager`、`server_event_handler.go`、`tool_provider_adapters.go` 都存在巨型横切问题。
- `Two-Zone DRY` 的主思想正确：平台层只收纯技术复用，业务重复留在模块内。
- `internal/archtest/` 的测试清单设计是对的，尤其是依赖方向、FX 图、`shared` 预算、`sqlc`/`jrpc2` import 限制。

### 5.2 风险点

- 规则未落盘为守护测试。当前仓库中 `internal/archtest/dependency_direction_test.go`、`shared_budget_test.go`、`runner_group_registration_test.go` 均不存在，说明所有“预算”和“禁止事项”仍停留在纸面。
- `platform/shared` 规则执行性不足。文档一边定义 `cursor.go`，一边在模块文档里引用 `pagination`；说明共享层命名和晋升门槛还没冻结。
- 当前 V3 骨架仍明显是 demo 级。`internal/bus/event_bus.go` 仍用 `event.Default`，`internal/store/module.go` 仍把 pool/query/module 混在一处，`internal/rpc/module.go` 仍把 server/config/module 放在一个文件。这意味着子文档不能假定平台契约已经落地。
- 模块来源重叠导致未来 ownership 不清晰。代码归属一旦不清晰，测试、回归和代码量预算都会失真。

### 5.3 改进建议

- 把 `internal/archtest/*` 前置到 Phase 0，先把 import 方向、FX 使用面、`sqlc` 目录、`shared` 预算钉死。
- 先冻结 3 个基础口径，再继续拆模块：
  - `sqlc` 生成目录唯一口径
  - `cursor`/`pagination` 唯一命名
  - `tool/*` 与 `module/*` 的 wrapper-only 边界
- 为每个 V2 RPC 方法、tool 方法、store 能力建立迁移台账，字段至少包含 `来源`、`去向`、`兼容策略`、`删除条件`。

## 6. 维度 5：代码体量验证

### 6.1 各模块预估加总

- 按 `v3-module-migration-details.md` 的 25 个模块直接相加：
  - V3 手写代码预估合计：`19,900 - 29,400` 行
  - V2 对应代码预估合计：`51,400 - 68,200` 行
- 该结果与主文档“V3 手写核心 30,000 - 40,000 行”的口径不一致。

### 6.2 过于乐观的预估

- `module/thread`：`450-650` 行偏低。仅现有 `thread/messages`、archive/binding、config 控制面就不是薄服务。
- `module/turn`：`600-900` 行偏低。deferred turn、interrupt/forceComplete、review 派生流、runtime tracking 没有足够压缩依据。
- `module/workspace`：`500-700` 行偏低。实际 V2 核心服务 881 行，且安全校验与状态漂移处理不能简单蒸发。
- `module/orchestration`：`1800-2600` 行偏低。`manager.go`、`manager_event.go`、`task_dag_phase1.go`、`dagwatcher` 的复杂度决定它仍是重模块。
- `module/uistate`：`1000-1500` 行偏低。V2 的 runtime patch / duplicate suppression / approval patch 不是框架样板，是真实业务复杂度。

### 6.3 过于保守的预估

- `platform/db`：如果严格执行 `sqlc + thin tx helper`，`350-500` 行偏保守。当前 V3 骨架只有 68 行，最终规模大概率仍然很薄。
- `platform/runner`：`200-350` 行偏保守。当前 `RunGroup` 宿主只有 72 行，若不回流业务逻辑，最终不会太厚。
- `tool/registry`：`500-800` 行偏保守。前提是它真的只做 schema/availability/compose，不做业务 facade。

### 6.4 总体可达性判断

- “30K-40K 可达”这个结论本身没有被否定。
- 但当前三份子文档给出的加总口径不可直接审计，原因有二：
  - 多个模块重复统计同一批 V2 来源文件
  - 25 个模块的 V3 预估总和低于主文档目标区间
- 结论：现口径不能支撑人天、里程碑和代码量承诺。必须先去重，再定义哪些代码被计入“手写核心”。

## 7. 逐文档审查明细

### 7.1 v3-module-migration-details.md

- `module/thread`：V2 来源真实，但 RPC 面遗漏现有 thread 控制方法，不能直接通过。
- `module/turn`：来源真实，deferred turn 风险判断准确，但对外服务面过宽，估算偏乐观。
- `module/skill`：正确识别出 `SkillService` 与 `skills.Manager` 双入口问题，这一合并方向成立。
- `module/orchestration`：复杂度判断真实；`taskdagphase1` 建议不要拆成独立 store 包。
- `module/workspace`：安全边界识别准确，代码量压缩过于乐观。
- `module/uistate`：高扇入判断准确，但应把 patch/backfill/order 作为显式迁移对象。
- `module/coderun`：与 `tool/code` 来源重叠，边界需要收紧。
- `module/ida`：与 `tool/ida` 来源重叠，边界需要收紧。
- `module/dashboard`：把 code-open 视为 UI 辅助是对的；线程别名逻辑当前仍只在 dashboard 本地出现，不应提前上提。
- `platform/rpc`：目标角色合理；但严格 binder 规则必须补回。
- `platform/db`：方向正确；需统一 `sqlc` 目录口径。
- `platform/bus`：方向正确；正式实现必须摆脱当前 demo 级全局 dispatcher。
- `platform/statemachine`：拆出技术骨架合理，禁止把业务状态枚举带进平台层。
- `platform/runner`：薄宿主方向正确，不应再吸回业务 supervisor。
- `platform/config`：方向正确，timeout 收敛必须前置。
- `provider/unified`：V2 来源路径错误，必须先改。
- `provider/claudecli`：方向基本合理，CLI-specific history/resume 仍应封在 driver 内。
- `provider/codexapp`：方向基本合理，`connection dead` 恢复复杂度被正确识别。
- `store/*`：总体方向正确，但 `taskdagphase1` 包化建议不佳。
- `tool/lsp`：复杂度判断真实，保留真实复杂度的结论成立。
- `tool/code`：应退化为 wrapper-only，避免与 `module/coderun` 双持逻辑。
- `tool/orchestration`：应退化为 wrapper-only，避免第二套 DAG/Workspace 校验链。
- `tool/ida`：应退化为 wrapper-only，生命周期与 gateway 归 `module/ida`。
- `tool/registry`：方向正确，但必须严防再次长成神对象。
- `mcpserver/*`：三二进制拆分方向正确；当前仓库尚未落地，`common` 不能回吸 family-specific 依赖。

### 7.2 v3-two-zone-dry-enrichment.md

- Zone A / Zone B 的大方向正确，尤其是“平台共享只收纯技术 helper”的约束是必要的。
- `cursor.go` 与模块细化文档中的 `pagination` 命名不一致，必须统一。
- `helpers.go` / `patterns.go` 的模块形状定义比模块细化文档更完整，应以本文件为准回填另两份文档。
- `pkg/factory` 去向表存在轻微时态问题：当前仓库没有 `pkg/factory/fsm.go`，不应写成现成迁移对象。
- `internal/archtest/*` 列表设计是正确的，但当前仓库没有对应文件，说明执行条件尚未建立。
- `platform/shared` 预算本身合理，但只有在预算测试落地后才有约束力。

### 7.3 v3-framework-usage-guide.md

- `fx`、`run.Group`、`stateless`、`kelindar/event` 的核心职责划分基本符合契约。
- 对当前 V3 骨架的两条矫正是准确的：bus 仍用 `event.Default`，store 仍混 DB 装配。
- `jrpc2` 指南缺少严格绑定模式。公共方法不应只停在 `handler.New`；必须把 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()` 写成默认范式。
- `sqlc` 指南使用了过时目录 `internal/store/sqlcgen`，与正式契约冲突。
- 示例代码对“公共方法保持斜杠命名”的强调不够，容易弱化公共 RPC 命名规范。

## 8. 结论与行动项

### 必须修正项（Blocker）

- 修正 `provider/unified` 的 V2 来源路径，删除不存在的 `go-agent-v2/pkg/agentsdk/provider/*`。
- 统一 `sqlc` 生成目录口径。主文档、三份子文档、契约文档必须只保留一个路径。
- 为 `module/thread` 补齐现有 V2 thread 方法的迁移去向表，禁止无声丢失。
- 统一 `cursor` / `pagination` 命名，消除错误 import 风险。
- 重新计算代码量预算，去除重复统计，并明确“手写核心”是否包含 app/ui/cmd/test/read-model。

### 建议修正项（Improvement）

- 将 `store/taskdagphase1` 并回 `store/taskdag`。
- 将 `tool/code`、`tool/orchestration`、`tool/ida` 明确收缩为 wrapper-only。
- 将 `turn` 对外图面收敛为单 facade，减少 FX 图复杂度。
- 将 `internal/archtest/*` 前置到 Phase 0，而不是等模块落地后补。

### 可选优化项（Nice-to-have）

- 在模块细化文档中补回 `helpers.go` / `patterns.go` 的文件形状说明。
- 为每个模块增加一列“当前 V3 是否已有前置骨架 / 是否 path not found”状态，降低实施时的信息跳跃。
- 为 `uistate` 增加专门的事件归一化与 patch 语义迁移小节，不要只写风险提示。

---

## 9. 修复记录

> 修复日期：2026-03-20

| # | Blocker | 修复状态 | 修复内容 |
|---|---|---|---|
| 1 | provider/unified V2 来源路径 | ✅ 已修正 | `v3-module-migration-details.md` 已将不存在的 `go-agent-v2/pkg/agentsdk/provider/*` 改为 `claude/*`、`codex/*`、`agentcore/*`、`codexadapter/*`、`commonadapter/*`。 |
| 2 | sqlc 目录口径统一 | ✅ 已修正 | `v3-module-migration-details.md`、`v3-two-zone-dry-enrichment.md`、`v3-framework-usage-guide.md`、`v3-migration-plan.md` 中的 `sqlcgen` 已统一改为 `sqlc`，`no_sqlcgen_outside_store_test.go` 已改为 `no_sqlc_outside_store_test.go`。 |
| 3 | module/thread 方法处置表 | ✅ 已补齐 | `v3-module-migration-details.md` 的 `module/thread` 小节已新增完整 V2 RPC 方法迁移处置表，覆盖保留、下沉、合并、删除四类去向。 |
| 4 | cursor/pagination 命名统一 | ✅ 已修正 | `v3-module-migration-details.md` 中的 `platform/shared/pagination` 已统一改为 `platform/shared/cursor`，与 `v3-two-zone-dry-enrichment.md` 保持一致。 |
| 5 | 代码量预估重算 | ✅ 已修正 | `v3-module-migration-details.md` 已上调 `thread`、`turn`、`workspace`、`orchestration`、`uistate` 等重模块估算，明确 `tool/code`、`tool/orchestration`、`tool/ida` 为 wrapper-only，合并 `store/taskdagphase1` 到 `store/taskdag`，并新增代码量汇总表使总量回到 `30,000-40,000`。 |
