# P25 Skill 优化：从 eager 注入到 progressive-disclosure

> 创建时间：2026-04-25 | 最近核对：2026-04-27（Phase 2 same-binary MCP child + host-direct observability 状态复核）
> 状态：🟡 codexapp 普通 name-only/selected skill 路径已可走 Summary + host-direct；claudecli same-binary MCP child 最小实现与 host-direct 基础 observability 已落地；仍非 harness 级 PR-ready（真实 Claude CLI 已认证 E2E、resume/recovery、redaction/default discovery、Phase 3 policy 与 rollout 导出/告警待完成）
> 关联文档：`docs/plans/迁移/p20/p20.18-host-direct-skill-tool-exposure.md`、
>           `docs/plans/迁移/p20/p20.11-mcp-skill-tools.md`（已废弃）、
>           `docs/plans/迁移/p20/p20.5-skill-catalog-provider.md`、
>           `docs/plans/迁移/p20/p20.10-skill-rpc-expand-list.md`、
>           `docs/plans/迁移/p20/p20.14-frontend-launch-skill-ui.md`、
>           `docs/plans/迁移/p20.1-skill-progressive-disclosure-hardening.md`

## 0. 概览

### 0.1 目的

把项目原本 **eager 注入完整 SKILL.md body** 的模式，改造成 Claude 原生的
**progressive-disclosure**——system prompt 只放 skill metadata（name + summary），
模型按需调 `skill_expand_body` / `skill_read_resource` 工具拉取详情。

### 0.2 当前状态（一句话）

> 当前 worktree 中，turn / cron 的普通 name-only skill 构造不再提前物化 `Mode=Full`，
> `applyHydration` 也保留 `Mode=Unspecified` 作为 provider-aware default marker。
> codexapp provider 入口会把该 marker 转为 Summary，因此 codexapp 普通 selected skill /
> name-only hydrate 路径已能得到 summary + `skill_expand_body` pointer。
> 但这仍不是 harness 级默认 progressive-disclosure 完成：显式 Full 仍 eager，
> claudecli 仍通过 `SkillMode.Effective()` 回到 Full，且缺模型视角 E2E / 可观测性闭环。

### 0.3 进度标记

图例：✅ 完成 / 🟡 进行中 / ⏸ 显式延期到后续 PR / ⚠️ 已知缺口（详见 §10）

| 阶段 | 状态 | 备注 |
|---|---|---|
| **C0：provider-aware default marker 准备** | ✅ 完成 | 普通 selected/name-only/cron 路径保留 `Mode=Unspecified`；turn 测试锁定 marker 不被 hydrate 物化 |
| **Phase 0：pkg/skilltool 共用 schema** | ✅ 完成 | 4 测试锁 tool name / required / no-cwd / property type / additionalProperties / marshal |
| **Phase 1：toolbridge host-direct 分支** | ✅ 完成 | 10 测试覆盖 SkillHostTools / dedup |
| **Phase 1.5：codexapp Mode override** | ✅ 完成 | 6 helper 测试 + 2 个入口/真实路径测试；仅处理 `Mode=Unspecified`，见 p20.18 §11 |
| **Phase 1 关键 bug 修复：agentId 注入** | ✅ 完成 | session.go enrichment + 6 测试（trust 姿态见 §10.1） |
| **可观测性 / 安全加固** | 🟡 基础已补 | host-direct counters / INFO/WARN / structured result 已落地；exporter / alerting / 30 天 rollout observation 待做，见 §3.4 / §10 |
| **Phase 2：claudecli stdio MCP server** | 🟡 最小实现已落地 | B3 same-binary child、`--mcp-config` skill server、stdio smoke、真实二进制 lifecycle 与 latency budget 已覆盖；真实 Claude CLI 已认证 tool-call E2E / 放量观测仍是 red gate（见 §5.1 / §5.3） |
| **Phase 3：正式化 provider default policy + override 删除** | ⏸ Phase 2 / E2E 后再做 | 不再是单纯改 `DefaultSkillMode()`；见 §5.2 |

### 0.4 执行摘要 / 当前推荐 PR 队列

当前最短业务闭环不是继续扩新架构，而是把已有 `skill.Service`、`ApprovalManager`、
`toolbridge`、`codexapp DynamicTools` 与 prompt/turn 设施编排成可验收链路。推荐按下表
顺序派单，planned test names / 验收矩阵见 §5.5；前置 PR 未完成前，不建议把
`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 默认打开。

> 2026-04-26 PR-1/PR-2/PR-3 更新：Approval 主闭环、DynamicTools partial degradation、
> 模型视角 E2E 已落地；当前推荐下一步从 PR-4 selected metadata / redaction 决策开始。

| 优先级 | 推荐 PR | 目标 | 关键文件 / 模块 | 必过验收 |
|---:|---|---|---|---|
| P0 ✅ | PR-1：Approval 主闭环 + metadata 传递（已落地） | project skill 首次展开触发 UI approval，approved 后可继续读取 | `internal/platform/toolbridge/{handler,host_tools}.go`、`internal/module/skill/{skills_expand,service,approval}.go` | 已锁：`agentId/threadId/turnId/callId/cwd/toolName` 进入 approval payload；approved/denied/timeout fail closed；`ApproveArtifact` 按 body/resource/path/repo 隔离 |
| P0 ✅ | PR-2：DynamicTools partial degradation（已落地） | host skill tools 不被 orch/lsp peer readiness 拖死，同时尽量保留 peer tools | `internal/platform/toolbridge/handler.go`、`internal/platform/toolbridge/host_tools_test.go` | 已锁：host tools 先收集；orch/lsp 并发等待；成功 peer 合并；双 peer 失败但 host 存在仍返回 host；无 host 且 peer 全失败才 error |
| P0 ✅ | PR-3：模型视角 E2E（已落地） | 证明模型能看到工具、调用 `skill_expand_body`、拿结果继续回答 | `internal/provider/codexapp/dynamic_skill_tools_e2e_test.go`、fake / controlled app-server | 已锁：`thread/start dynamicTools -> model tool call(skill_expand_body) -> tool result -> final answer`；approved/denied 模型视角结构化结果均覆盖 |
| P1 | PR-4：selected metadata / redaction 决策 | 收敛 untrusted selected summary 可见性策略 | `internal/module/turn/skills.go`、`internal/module/prompt/skill_catalog_provider.go` | 若允许 summary，只限真实 `ManualSkillSelection=true && source=manual`；legacy `Source=Unspecified` 不得误授权 |
| P1 | PR-5：resume / recovery skill tools | 证明或修复 resume/recovery 后 skill tools 可用 | `internal/provider/codexapp/{driver,recovery}.go` | 先验证 app-server 是否保留 / 接受 resume dynamicTools；不允许只靠 params 加字段验收 |
| P2 | PR-6：默认 discovery / observability | 为默认 progressive-disclosure 放量做准备 | `internal/module/prompt/config.go`、`internal/platform/toolbridge`、`pkg/skillmetrics` | 默认开启前必须有 approval / DynamicTools / E2E / observability gates；补 degraded、host-call、approval、artifact cache 日志 / 指标 |

---

## 1. 背景

用户最初问"如何把项目 skill 改造对齐 Claude 注入方式"。完整改造包含 6 步（C0-C6），
但实际 PR 落地范围窄于此，原因：

1. 原计划走 **P20.11**（在 mcp-orch 注册 skill 工具），recon 发现该任务 2026-04-19
   已被官方废弃（`docs/plans/迁移/p20/p20.11-mcp-skill-tools.md`），架构错位。
2. 替代路径是 **P20.18**（toolbridge 宿主直跑 + 独立 stdio MCP server），
   工作量约 700 行。
3. 最终决定：Phase 1 + 1.5 先落 codexapp host-direct 工具链 + `Mode=Unspecified` 临时 override；当前 Phase 2 same-binary stdio MCP child 最小实现也已落地并补了 stdio smoke 单测。codexapp / claudecli 都具备代码级工具链，但 harness 级默认 progressive-disclosure 仍未达到业务 PR-ready。

---

## 1.1 Harness 外壳当前能力盘点

本项目是桌面 / RPC / Provider / MCP / Skill / Prompt / Turn 编排 harness，不是单一模型运行时。Skill progressive-disclosure 横跨多个已有能力域：

| 能力域 | 当前已有实现 | 与 skill progressive-disclosure 的关系 |
|---|---|---|
| Desktop / RPC 外壳 | `internal/app`、`internal/platform/rpc`、Wails runtime | 承载 host RPC、UI approval surface 与桌面生命周期 |
| Provider bridge | `provider/unified`、`provider/claudecli`、`provider/codexapp` | provider-specific skill render / tool exposure；codexapp 走 DynamicTools，claudecli 仍待 Phase 2 |
| Thread / Turn 编排 | `module/thread`、`module/turn` | 把用户输入和 selected skill 变成 `dto.TurnRequest`，并执行 hydrate / manifest / memory context |
| Prompt / Memory | `module/prompt`、`module/memory` | `SkillCatalogProvider` 生成 L1 manifest；memory / prompt snapshot 进入 provider start |
| Tooling | `platform/toolbridge`、`platform/mcpcontrol` | codexapp tool list/call 进 host-direct 或 MCP peer；skill tools 当前由 host-direct 承接 |
| Skill service | `module/skill` | roots / scan / metadata / expand / resource / approval / legacy RPC 的真值源 |
| UI read model | `module/dashboard`、`module/uistate` | skill list / thread / timeline / diagnostics 的前端消费面 |
| Ops | `module/cron`、`module/notify`、`module/insight` | 复用 turn/session 能力，但不是 skill progressive-disclosure 的主闭环 |

因此，P25 的验收不能只看 `module/skill` 或 `provider/codexapp` 单点；必须看 `skill.Service -> prompt catalog -> turn PrepareTurn -> provider render -> DynamicTools/tool call -> ExpandBody/ReadResource -> 模型继续推理` 的完整 harness 链路。

## 1.2 Harness 架构 / 已有设施二次复核（2026-04-26）

二次复核结论：**当前项目已有 harness 足够支撑 P25，P25 的主要缺口不是缺基础设施，
而是已有设施尚未被完整编排成业务闭环**。

| 层级 / 设施 | 已有能力 | 对 P25 的判断 |
|---|---|---|
| 根 Fx 图 | `internal/app/modules.go` 已装配 config/db/bus/rpc/hooks、memory/prompt/skill/thread/turn/uistate、provider、toolbridge、mcpcontrol、cron/notify/insight | 架构底座完整；不需要为 P25 新增一条并行 runtime |
| Contract / Provider 边界 | `contract.Session`、`TurnHandle`、`SessionResolver`、`ApprovalRequester/Responder`、`PromptAssemblyService`、`SkillInjectionPort`、`group:"drivers"` | provider 扩展点已存在，P25 应在 adapter / policy 层闭环，不应反向耦合 turn/thread |
| Thread / Turn | thread 管 start/resume/fork/recover/archive 与 prompt snapshot；turn 管 PrepareTurn、hydrate、manifest、memory context、tracker | selected/name-only skill 进入 `dto.TurnRequest` 的链路已具备；缺的是后续 provider/tool/approval 闭环 |
| Prompt assembly | static + dynamic sections、snapshot、cache policy、`skill_catalog` slot | `SkillCatalogProvider` 能承载全量 discovery，但默认灰度关闭，不能把 selected path 写成全量 catalog 已上线 |
| Skill service | cwd-scoped roots、scan/list、ExpandBody/ReadResource、trust/content hash、artifact approval cache、SkillsChanged event | skill 层能力完整；project skill 首次展开已接 `ApprovalRequester` 主闭环，fallback approval-required 也会结构化返回 |
| RPC / Approval | `ApprovalManager` 支持 pending 去重、恢复、request_user_input、bus 事件 | approval 基础设施已存在；P25 不应重造审批，只需把 host-direct skill error 接入现有 approval bridge |
| MCP / Toolbridge | mcpcontrol registry/heartbeat/fanout；toolbridge list/call/proxy/diff；host-direct SkillHostTools | tool 暴露/调用主干成立；host skill tools 已具备 partial degradation、structured result/error、基础 metrics 与 INFO/WARN 日志 |
| Codex provider | shared app-server、WS transport、dynamicTools thread/start、approval bridge、recovery/pending replay | codexapp 是当前可落地 provider；缺 resume/recovery 后 DynamicTools 可用性证明 |
| Claude provider | manifest / MCP config / native skill injection port / eager skill render / same-binary skill MCP child 最小链路 | Phase 2 最小实现已落地；仍缺真实 Claude CLI E2E、stdio smoke / lifecycle 与放量观测证明 |
| UI / Observability / Ops | uistate、dashboard、turnobservation、cachekeepalive、pidregistry、cron/notify/insight | 发布支撑设施已有；P25 已补 host-direct 基础 metrics / 日志 / structured approval result，仍缺 exporter/alerting、catalog refresh 策略与默认放量观察 |

复核后的策略约束：

1. **不要扩一套新架构**：优先复用 `ApprovalManager`、`SkillCatalogProvider`、`toolbridge`、
   `turnobservation`、provider recovery 等现有设施。
2. **业务闭环优先级高于代码链路存在**：selected/name-only Summary render 已打通，
   但 project-scope approval、全量 discovery、resume/recovery、observability 未闭环前，
   不得标记为 harness 级 PR-ready。
3. **保持文档地图卫生再继续大规模派单**：`docs/doc/codemap` 历史残留 conflict markers
   已清理，当前不再是 P25 Phase 2 / Phase 3 前置 blocker；后续若重新生成 codemap，仍必须把
   行首 `^<<<<<<<` / `^=======$` / `^>>>>>>>` 检查纳入 smoke；详见 §10.9。

---

## 2. 已完成内容（详细文件清单）

### 2.1 Phase 0：共用 schema 包

| 文件 | 状态 | 说明 |
|---|---|---|
| `pkg/skilltool/schema.go` ✨ | 已落地 | `ToolName{ExpandBody,ReadResource}` 常量 + `{ExpandBody,ReadResource}InputSchema()` JSON Schema |
| `pkg/skilltool/schema_test.go` ✨ | 已落地 | 4 测试：tool 命名 / required / cwd 不暴露 / property type / additionalProperties / JSON 序列化 |

设计要点：
- 纯数据，零 internal 依赖（设计约束：`pkg/skilltool` 不得 import `internal/`；当前代码事实成立，但还需补 archtest guard，见 §10.8）
- cwd 字段**不暴露给模型**——由 host runtime 注入

### 2.2 Phase 1：toolbridge host-direct 分支

| 文件 | 状态 | 变动 |
|---|---|---|
| `internal/platform/toolbridge/host_tools.go` ✨ | 已落地 | `HostToolRegistry` 接口 + `SkillHostTools` 实现 + cwd 强制覆盖 |
| `internal/platform/toolbridge/host_tools_test.go` ✨ | 已落地 | 10 测试 |
| `internal/platform/toolbridge/handler.go` | 已改造 | `Handler.hostTools` 字段 + `routeToolCall` host-direct 分支 + `ListToolsForCodex` 聚合 + `dedupToolsByName` + `callHostTool` / `resolveAgentCWD` |
| `internal/platform/toolbridge/module.go` | 已改造 | `provideHostToolRegistry` fx provider + `handlerIn.HostTools` 可选字段 |

设计要点：
- **dedup 优先级**：hostTools > peer。同名工具 hostTools 命中即返回，不再查 peer。
  当前**未对被 shadow 的 peer 同名工具发出告警**——见 §10.4。
- **cwd 强制覆盖**：`CallHostTool` 强行用 host 解析的 cwd 覆盖模型可能填的 CWD 字段，
  防御模型恶意/误填。schema 也未暴露 cwd（见 §6.5 决策日志）。
- **nil-safe**：mcp-orch / mcp-lsp standalone 进程未加载 skill module 时，
  hostTools=nil 不报错，全部走 peer 路径。
- **不变量**：`Handler.hostTools` 在 `NewHandler` 内一次性赋值，构造后**不可变**——
  当前安全依赖此假设，未加锁。若未来引入动态 (re-)注册，需改 `atomic.Pointer`。

### 2.3 Phase 1.5：codexapp Mode override（临时折中）

| 文件 | 状态 | 变动 |
|---|---|---|
| `internal/provider/codexapp/skill_mode_override.go` ✨ | 已落地 | `overrideSkillsToSummary` 仅把 `Mode=Unspecified` 转换为 Summary，显式 Full/Summary/None 不动 |
| `internal/provider/codexapp/skill_mode_override_test.go` ✨ | 已落地 | 8 测试：6 个 helper 单测 + `buildTurnStartParams` 入口断言 + `PrepareTurn→skill.Service hydrate→buildTurnStartParams` 普通路径断言 |
| `internal/provider/codexapp/session_turn.go` | 已改造 | `buildTurnStartParams` / `buildTurnSteerParams` 入口调 override |

为什么需要：单纯全局切 Summary 曾会让 claudecli 项目级 skill body 在 Phase 2 前完全消失。
当前 claudecli same-binary skill MCP child 已有最小实现，但真实 Claude CLI E2E、stdio lifecycle、provider-aware default policy 与放量观测尚未闭环；因此本阶段仍只在 codexapp 入口保留临时 override，
并且 **只覆盖 `Mode=Unspecified`**；显式 Full / Summary / None 仍保留调用方意图。当前普通 selected skill / name-only hydrate 路径已不再提前物化 Full，因此会以 Unspecified marker 进入 codexapp 并转 Summary。claudecli 在 Phase 3 前仍通过 `SkillMode.Effective()` 保持 Full/eager 兼容，同时额外具备 Phase 2 skill MCP tool 链路供验证。

**预期删除时机**：Phase 2 / provider-aware default policy / 模型视角 E2E 全部闭环后，
PR-B 删除 override 文件和 caller，再校验 Go 源码中零命中。代码 agent 应使用 `lsp_grep(text_search)` 检索 `overrideSkillsToSummary`；人类本地复核可用等价 shell 检索。

### 2.4 Phase 1 关键 bug 修复：agentId 注入

| 文件 | 状态 | 变动 |
|---|---|---|
| `internal/provider/codexapp/session_enrich.go` ✨ | 已落地 | `enrichToolCallParams` helper |
| `internal/provider/codexapp/session_enrich_test.go` ✨ | 已落地 | 6 测试 |
| `internal/provider/codexapp/session.go` | 已改造 | onInboundMessage 调 toolHandler 前 enrich |

bug 详情：
- Codex app-server 发的 `item/tool/call` params **只含 name + arguments**，无 agentId
- 旧的 peer-routed 工具不需 agentId（peer 自管 cwd），所以从未暴露
- 但 host-direct 路径**强依赖** agentId 解析 cwd
- 不修：模型每次调 `skill_expand_body` 都会拿到 "cwd is required" 错误，
  Phase 1 链路 100% 失败

修复：onInboundMessage 在调 toolHandler 前调用 `enrichToolCallParams`，以 `s.agentID`
覆盖写入 canonical `agentId`，并删除 `agent_id` alias；外部 params 中已有的
`agentId` / `agent_id` 一律不可信。

> 安全 review 结论：旧“保留外部 agentId”策略在 host-direct 上下文下是反向 trust 姿态，
> 本轮已按 §10.1 改为 always-overwrite；旧 fixture 改为断言外部 agentId 被覆盖。

### 2.5 C0：provider-aware default marker（前置工作）

| 文件 | 变动 |
|---|---|
| `internal/module/turn/skills.go` | 保留 `DefaultSkillMode()` 作为 legacy effective default 说明；`applyHydration` 不再把 Mode 物化为 Full |
| `internal/module/turn/factory.go` | `normalizeSkillNames` 保留 `Mode=Unspecified`，作为 provider-aware default marker |
| `internal/module/cron/turn_adapter.go` | cron skill 构造同样保留 `Mode=Unspecified`，由 provider adapter 决定默认注入模式 |
| `internal/module/turn/skills_test.go` | 4 测试锁定 Unspecified marker / hydration 不覆盖显式 Mode |

设计要点：普通 selected skill / name-only hydrate / cron skill 不再提前写 `Mode=Full`。
`Mode=Unspecified` 的语义变成 provider-aware default marker：codexapp adapter 转 Summary，legacy/claudecli render 仍通过 `SkillMode.Effective()` 解释为 Full。Phase 3 需要正式化 default policy 的落点，不再是“只改 `DefaultSkillMode()` 一行”。

---

## 3. 当前分支行为 / 合入后行为

### 3.1 codexapp 用户

🟡 **codexapp 普通路径代码级 Summary 已打通，但还不是模型视角 / harness 级 PR-ready**：

```text
已落地能力：
1. thread/start 可携带 DynamicTools：skill_expand_body / skill_read_resource
2. codex app-server → onInboundMessage（注入 session agentId）→ toolHandler →
   toolbridge.routeToolCall → SkillHostTools.CallHostTool → skill.Service.ExpandBody / ReadResource
3. 普通 selected skill / name-only hydrate / cron skill 保留 Mode=Unspecified marker
4. codexapp 入口把 Unspecified 转 Summary，buildSkillPromptInput 渲染摘要 + 工具指针，
   不嵌完整 SKILL.md body
5. `TestPrepareTurnToBuildTurnStartParams_NameOnlySkillUsesSummary` 已覆盖
   cwd/.agent/skills/demo/SKILL.md → skill.Service hydrate → turn.PrepareTurn → codexapp input

当前限制：
1. 显式 Full 仍 eager 注入，这是兼容设计，不被 override 覆盖
2. codexapp 模型视角 E2E 已落地：fake app-server 证明 DynamicTools → skill_expand_body → tool result → final answer
3. host-direct 基础 metrics / INFO/WARN 日志 / structured approval-or-error result 已上线，仍缺 exporter/alerting 与 30 天放量观察
4. claudecli same-binary MCP child 最小 parity 已落地，仍缺已认证真实 Claude CLI tool-call E2E / 托管 child orphan 观测
```

因此，准确口径是：codexapp 普通路径已具备 progressive-disclosure 的代码级能力，
但不能写成“整个 harness 默认 progressive-disclosure 已业务交付”。

### 3.2 claudecli 用户

✅ **行为完全不变**（向后兼容）：

```
1. claudecli 走 buildSkillPromptText / composeTurnText（不经 buildTurnStartParams）
2. SkillRef.Mode 可保持 Unspecified，但 claudecli render 通过 SkillMode.Effective()
   把 Unspecified 解释为 Full
3. system prompt eager 注入完整 SKILL.md body（与改造前一致）
4. claudecli 不挂 toolbridge host-direct 分支（独立进程），已通过 Phase 2 same-binary stdio MCP child 暴露 `skill_expand_body` / `skill_read_resource` 最小链路；Phase 3 默认策略切换前仍保留 Full/eager 兼容
```

### 3.3 mcp-orch standalone 进程

✅ **不受影响**：

- 当前安全点：`cmd/mcp-orch/fx.go` 不加载 `toolbridge.Module`，standalone 进程不会装配 host skill tools
- 运行时分支 nil-safe：`Handler.hostTools=nil` 时，所有工具调用走原 peer 路径
- 注意：`provideHostToolRegistry(svc skillpkg.Service)` 当前不是 Fx optional input；如果未来 standalone 也加载 `toolbridge.Module`，需要先把 `skill.Service` 输入改成 optional Fx 参数，或提供 noop registry

### 3.4 可观测性现状（🟡 基础已补，导出 / 告警待做）

当前实现已补上 host-direct 最小观测闭环：

- `pkg/skillmetrics` 新增 `host_tool_calls_total{outcome}` 对应的 Go counters：
  `ok` / `cwd_missing` / `approval_required` / `error`。
- `pkg/skillmetrics` 新增 `enrich_failures_total` counter；`enrichToolCallParams`
  JSON decode / marshal fail-soft 时会计数，避免协议漂移静默 100% 失败。
- `handler.callHostTool` 每次 host-direct 命中打一行 INFO 日志，包含 tool / agentId /
  threadId / callId / outcome / duration_ms。
- `resolveAgentCWD` 失败导致 cwd 为空时，会在真正调用 host tool 前打一行 WARN，包含
  tool / agentId / threadId / callId，并将 outcome 归类为 `cwd_missing`。
- DynamicTools dedup 时若 peer 工具被 host 同名工具 shadow，会打一行 WARN（含 peer 来源）。
- approval-required / denied 已返回结构化 result，不再只有普通 `err.Error()` 文本。

仍未完成的是**导出与告警层**：这些 Go counters 目前仍是 in-process snapshot 模式；
Prometheus / OTel exporter、error-rate > 5% 持续 5min 告警、30 天放量观察仍属于 Phase 3 前
rollout gate。

详见 §10.5。

### 3.5 业务能力主链缺口（2026-04-26 复核新增）

以下缺口不是单纯治理 / 文档卫生问题，而是决定 P25 能否从“代码级链路打通”升级为
“业务能力 PR-ready”的主链路判断：

1. **project-scope skill 首次展开没有 approval 闭环**：项目级 skill 默认 `TrustProject`，
   `skill_expand_body` / `skill_read_resource` 命中 `SkillApprovalRequiredError` 时，
   `toolbridge.callHostTool` 目前只把错误包装成普通 `success=false + inputText`，
   不触发 UI approval，也不返回机器可识别的 `kind="approval_required"`。
   因此当前 host-direct 展开能力对 trusted / preapproved skill 可顺利工作，
   但对普通 `.agent/skills` 首次展开仍会卡在非结构化失败文本。
2. **selected skill Summary 与 catalog redaction 策略不一致**：`SkillCatalogProvider`
   对 untrusted project skill 会隐藏作者原始 description / summary；但 selected/name-only
   路径会经 `hydrateSkillRefs -> applyHydration` 把 `SkillInfo.Summary` 填入 `SkillRef`，
   再由 codexapp Summary render 写给模型。需产品决策：手选 project skill 是否等价于
   允许暴露 summary；否则应补 metadata approval / redaction gate。
3. **全量 skill discovery 默认未启用**：`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 当前默认
   `false`，`SkillCatalogProvider` 默认不注册。因此已打通的是 selected/name-only skill
   的 Summary path，不是“模型默认看到全量 skill catalog 并自主发现”的业务能力。
4. **host skill tools 暴露被 orch/lsp peer readiness 绑定**：`ListToolsForCodex` 先等待
   orch / lsp peer，再合并 host tools。任一 peer list 失败都会导致
   `skill_expand_body` / `skill_read_resource` 也无法暴露。业务上需要 host-only partial
   degradation，或用跨层测试证明启动顺序稳定。
5. **resume / recovery 后 DynamicTools 可用性未证明**：`thread/start` 携带 dynamicTools，
   但 `thread/resume` params 没有 dynamicTools 字段。需要 E2E 证明 app-server 会保留工具
   schema，或在 resume/recovery 后重新注册 / 恢复 skill tools。
6. **resource 能力限定为文本资源**：`skill_read_resource` 当前返回 string，适合
   `references/*.md`、脚本等文本；二进制 asset（图片、模板、压缩包等）仍需 base64
   或独立 `skill_read_asset` 方案。
7. **长会话中的 catalog 更新不是完整闭环**：`SkillsChanged` 与 provider cache revision
   已存在，但 codexapp 的 baseInstructions/catalog 主要在 `thread/start` 注入；运行中新增
   / 修改 skill 后，模型侧是否可自动发现还缺 refresh / delta / `skill/list` 策略。

### 3.6 实现拆解二次审核修订（2026-04-26）

后续实现不得按“只把错误包装成文本 / 只证明 schema 存在”的弱口径推进；本节把
2026-04-26 审核后的硬约束写成派单前置条件：

1. **approval 主路径必须在 skill service 内部闭环**：`SkillApprovalRequiredError`
   只能作为 `approvalRequester == nil` / 降级环境的结构化 fallback；正常 desktop / RPC
   图必须由 `ExpandBody` / `ReadResource` 在 artifact cache miss 后调用
   `ApprovalRequester.RequestApproval(...)`，approved 后继续读取并返回正文 / 资源。
2. **host-direct 必须把调用元数据传入 approval payload**：`ToolCallRequest` 已有
   `agentId/threadId/callId`，但当前 `callHostTool` 只把 cwd 传给 `SkillHostTools`。
   PR-1 必须扩 host tool 调用上下文，把 `agentId/threadId/callId/cwd/toolName` 传到
   `skill.Service` 的 approval request；否则 UI approval 即使弹出也缺少定位上下文。
3. **approval cache 必须按 artifact 维度写入**：approval 通过后写
   `ApproveArtifact`，不能混用旧的 full-skill `Approve`。body、body anchor、resource
   path、不同 repo fingerprint 必须互相隔离；approve body 不得自动 approve resource。
4. **DynamicTools partial degradation 不能秒回 host-only**：`ListToolsForCodex` 应先拿
   host tools，再并发等待 orch/lsp peer，最长只等一个 `peerReadyTimeout`；peer 失败只能
   降级合并成功的工具并记录日志 / 指标，不能让 host skill tools 被无关 peer 拖死，也
   不能因为过早返回 host-only 导致本次 session 永久缺少 lsp/orch tools。
5. **selected metadata 策略必须区分真实手选与 legacy unspecified**：不能简单写成
   “手选即授权 summary”。`applyHydration` 当前会把 `Source=Unspecified` 补成
   `manual`，因此 untrusted project skill 的 summary 可见性必须同时参考
   `ManualSkillSelection` 与真实来源；否则旧 payload 会被误当作用户显式授权。
6. **resume/recovery 先验真 app-server 行为再改 wire params**：`thread/resume` 当前不带
   `dynamicTools`；是否补字段必须先用 E2E / fake app-server 证明 resume 后工具是否保留、
   以及 app-server 是否接受 resume dynamicTools。不得只靠“params 加字段”作为验收。

---

## 4. 测试现状

### 4.1 新增测试（共 32 个）

| 包 / 区域 | 测试数 | 覆盖范围 |
|---|---:|---|
| `pkg/skilltool` | 4 | tool name / required / no-cwd / property type / additionalProperties / marshal |
| `internal/platform/toolbridge` | 10 | SkillHostTools / dedup |
| `internal/provider/codexapp` Mode override | 8 | 6 个 helper 单测 + `TestBuildTurnStartParams_AppliesSummaryOverride` + `TestPrepareTurnToBuildTurnStartParams_NameOnlySkillUsesSummary` |
| `internal/provider/codexapp` enrich | 6 | agentId 注入 / 覆盖外部 `agentId` 与 `agent_id` / fail-soft |
| `internal/module/turn` | 4 | Unspecified marker / hydration preserves Mode / explicit Mode 不被覆盖 |
| **合计** | **32** | 30 个 leaf/unit + 2 个 codexapp 入口/真实路径集成断言 |

### 4.2 全套测试状态

> 注：本轮验证中 `go build ./...` 可通过；若某些 worktree 因 VCS stamping 报
> `error obtaining VCS status: exit status 128`，可使用 `go build -buildvcs=false ./...`
> 作为 fallback。以下代码块保持可直接复制执行，不在命令行尾部混入状态符号。

```bash
go build ./...
go build -buildvcs=false ./...
go test ./pkg/skilltool/ -count=1
go test ./internal/platform/toolbridge/ -count=1
go test ./internal/provider/codexapp/ -run '^(TestEnrichToolCallParams_.*|TestOverrideSkillsToSummary_.*|TestBuildTurnStartParams_AppliesSummaryOverride|TestPrepareTurnToBuildTurnStartParams_NameOnlySkillUsesSummary)$' -count=1
go test ./internal/provider/claudecli/ -run Skill -count=1
go test ./internal/module/turn/ -count=1
go test ./internal/module/skill/ -count=1
go test ./internal/module/cron/ -count=1
go test ./internal/dto/provider/ -count=1
```

### 4.3 已知测试覆盖盲点（建议后续补）

| 缺口 | 当前状态 | 建议 |
|---|---|---|
| codexapp 普通 name-only selected skill 路径 Summary | 已有 `PrepareTurn→skill.Service hydrate→buildTurnStartParams` 小集成验证 | 保留为已覆盖，不再列为业务 blocker；后续用模型 E2E 验真 |
| codexapp `ListToolsForCodex` 真实包含 `skill_expand_body` / `skill_read_resource` | 缺跨层断言 | 补 toolbridge / codexapp 小集成 |
| `routeToolCall → callHostTool → SkillHostTools.CallHostTool` 串联 | 只有叶子节点单测 | 补 `TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD` |
| `onInboundMessage` 实际把 `s.agentID` 覆盖写入 canonical `agentId`、删除 `agent_id` alias，并把 enriched params 交给 toolHandler | 目前主要测 helper | 补 `TestOnInboundMessage_EnrichesToolCallParams_OverridesAgentID` |
| `buildTurnSteerParams` override | 有代码路径，缺默认 Summary 断言 | 补 `TestBuildTurnSteerParams_AppliesSummaryOverride` |
| claudecli 负面断言 | 只跑 `-run Skill`，未证明 eager Full 不受 override / provider-aware marker 影响 | 补 claudecli 保持 Full/eager 的 contract test |
| DynamicTools 真实 skill tools | 现有测试偏 fake tool / leaf | 补真实 skill tools dynamicTools 测试，断言 `ListToolsForCodex` 真含 `skill_expand_body` / `skill_read_resource` |
| DynamicTools host-only 降级 | host skill tools 仍被 orch/lsp peer readiness 绑定 | 补并发 peer list partial-degradation 测试：orch fail/lsp ok、lsp fail/orch ok、双 peer fail+host ok、无 host+双 peer fail 才 error；禁止串行 10s+10s 或秒回 host-only |
| 模型视角 E2E | 缺 | 真 app-server + fake/controlled model tool call：DynamicTools → skill_expand_body → approval/cache/read → result 回模型继续推理；只断言 schema 存在不算闭环 |
| project skill approval 闭环 | `ExpandBody` / `ReadResource` 能返回 `SkillApprovalRequiredError`，但 host-direct 只转成普通失败文本，且未把 `agentId/threadId/callId` 传入 skill approval payload | 补 service 内部 `ApprovalRequester` 主路径、host-direct metadata 传递、`ApproveArtifact` cache 写入、approved/denied/timeout E2E；`SkillApprovalRequiredError` 仅作 no-requester fallback |
| selected path untrusted metadata 策略 | selected/name-only Summary 可能暴露 project skill summary，未复用 catalog redaction；`applyHydration` 会把 `Source=Unspecified` 补成 `manual` | 补产品决策 + contract test：若允许 summary，只能限真实 `ManualSkillSelection=true && source=manual`；legacy unspecified / trigger / force 默认不得当作 metadata 授权 |
| resume/recovery 后 skill tools | `thread/start` 有 dynamicTools，`thread/resume` 未携带 dynamicTools | 先补 app-server 行为证明：resume/recovery 后工具是否保留、resume 是否接受 dynamicTools；若不保留再实现重新注册，不能只以 params 加字段作为验收 |
| schema description 文案漂移 | 已锁 tool name / required / no-cwd / property type / additionalProperties / marshal，未锁完整 description 文案 | 如需对外契约冻结，可补 description golden |
| v1 writerFormat Summary mode | 未验证空 body 渲染是否误导模型 | 补 `RenderSkillBlockV1` Summary 集成 |
| `ReadResource` traversal | 依赖 `internal/module/skill` 防护，host-direct 无独立断言 | 补跨层 contract test；同时确认 symlink / EvalSymlinks 失败路径不逃逸 |
| resource binary asset | 当前 `skill_read_resource` 返回 string，主要覆盖文本资源 | 若业务需要图片/模板/压缩包，补 base64 或 `skill_read_asset` 工具 |

---

## 5. 待办（Phase 2 + Phase 3）

### 5.1 Phase 2：claudecli stdio MCP server（独立 PR）

详见 `docs/plans/迁移/p20/p20.18-host-direct-skill-tool-exposure.md` §4.2 / §4.3 / §11。

**当前实现状态（2026-04-27）**：B3 最小实现已落地：`agent-terminal --mcp-skill-mode` same-binary stdio MCP child、manifest `launch_kind="same-binary-skill"`、Claude `--mcp-config` skill server、静态 `tools/list`、lazy host RPC、runtime cwd/agentID/threadID 注入、per-turn turnID 不进 manifest/env、approval-required 结构化 envelope、stdio `initialize -> tools/list -> tools/call -> EOF` smoke 均已有单测覆盖。真实 Claude CLI E2E / orphan child 进程观测 / 放量指标仍按 §5.3 red gates 执行，未完成前不得删除 codexapp 临时 override。

**推荐方案 B3**（same-binary stdio MCP 子进程 + host RPC client）：
- 复用 agent-terminal 二进制 + `--mcp-skill-mode` 子命令。
- 子进程跑 stdio MCP server，**通过 host RPC（`skills/expandBody` / `skills/readResource`
  等）回调父进程的 `skill.Service`**；子进程不得 own 一套独立 `skill.Service`。
- 原因：`skill.Service` 持有 `sessionApprovals`（in-memory）/ `candidateStore` /
  `auditStore` / `skillsChangedSeq` 等状态，双实例会导致审批不同步、审计双写、
  UI 事件丢失（进程模型见 p20.18 §4.2，命名/调用路径见 §4.3）。
- claudecli 在 manifest 中注册该 stdio server 到 `--mcp-config`。

**Provider-specific transport / shared semantic contract（必须保持）：**
- `codexapp` 继续走 `thread/start DynamicTools + parent host-direct`：`ListToolsForCodex()` 把
  `skill_expand_body` / `skill_read_resource` 作为 host tools 下发，模型 tool call 回到父进程同进程
  `SkillHostTools -> skill.Service`。
- `claudecli` 不能复用 Codex DynamicTools；Phase 2 只通过 `--mcp-config` 挂
  `agent-terminal --mcp-skill-mode` stdio MCP child。child 只做代理：Claude CLI ⇄ MCP child ⇄ 父进程
  host RPC ⇄ 父进程 `skill.Service`。
- 两个 provider 必须共享模型可见契约：工具名、schema、结构化结果、approval 语义、cwd/agentID/threadID
  runtime 注入规则一致；差异只允许存在于 transport 层。

**Phase 2 实施 runbook（必须在开工前拆成任务）：**
1. 在 agent-terminal 启动早期增加 `--mcp-skill-mode` 短路入口，必须早于完整 Fx / Wails app 启动。
2. manifestbuilder 增加 same-binary skill server 表达，并让 `transport_config` 放行
   `agent-terminal --mcp-skill-mode`。
3. 父进程提供 host RPC：`skills/expandBody` / `skills/readResource` / approval wait wrapper。
4. 子进程只持有 RPC client，不 import / new 一套独立 `internal/module/skill.Service`。
5. **启动期轻量边界（首轮延迟红线）**：MCP `initialize` / `tools/list` 只能返回静态 schema
   `skill_expand_body` / `skill_read_resource`；不得扫描 skill roots、不得读取任何 `SKILL.md`、不得调用
   `skills/expandBody` / `skills/readResource`、不得触发 approval / audit / candidateStore / `skill.Service`、
   不得阻塞父进程 RPC。
6. host RPC client 应 lazy 使用：child 启动和 `tools/list` 阶段不连接 / 不探活父进程；第一次
   `tools/call` 才按需调用父进程 RPC。普通首轮对话若模型未调用 skill 工具，不得发生 expand/read RPC。
7. cwd / agentID / threadID 只允许来自父进程 spawn 时注入的 env / argv / RPC 上下文，per-turn turnID 不得进入稳定 MCP manifest/env，
   禁止子进程重新猜测或从模型参数信任读取。
8. 审批流：子进程命中 `SkillApprovalRequiredError` 时，经 RPC 阻塞等待父进程 UI 决议
   （建议 timeout 30s），并返回结构化 `kind="approval_required"` / `approved|denied|timeout`。
9. 补 E2E：trusted/preapproved happy path、approval_required、cwd 注入、manifest 启动、
   静态 `tools/list`、lazy host RPC、首轮无 skill tool-call 不触发 expand/read、子进程生命周期清理、
   claudecli 负面兼容。

**Blocking decisions（2026-04-27 已收敛为实现约束）：**
- 子进程不 import / new `internal/module/skill.Service`；只持有轻量 host RPC client。
- approval-required 使用 host RPC `-31002` + error data 透传为 `kind="approval_required"` envelope；approved 走正常 result，denied/timeout fail closed 并保持结构化错误 envelope。
- host RPC 复用现有 blocking `skills/expandBody` / `skills/readResource` 调用，不新增 bootstrap 全栈。
- manifestbuilder 使用 `MCPLaunchKind` / `LaunchKindSameBinarySkill`，claudecli `transport_config` 仅对该 marker 放行 same-binary command。
- same-binary 子进程入口为 `cmd/agent-terminal` 早期 `--mcp-skill-mode` 短路，命中后不得进入完整 app/Fx/Wails。
- cwd / agentID / threadID 只来自父进程注入的 canonical env / manifest runtime；per-turn turnID 不进入 MCP manifest/env；不信任模型参数。

**预估工作量（修订）**：~600 行 ± 150（含测试 + manifestbuilder 改动）。
参照锚点：mcp-orch stdio runtime 脚手架 ≥150 行、tool adapter + RPC client +
approval 等待 ≈200 行、manifestbuilder Kind 扩展 ≈60-80 行、生命周期 / 审批 /
cwd 注入测试 ≈150 行。

### 5.2 Phase 3：正式化 provider default policy 与 override 删除（Phase 2 / E2E 后）

当前 Phase 3 不再定义为“简单修改 `DefaultSkillMode()` 返回 Summary”。代码事实已经把普通路径的 `Mode=Unspecified` 保留下来，让 provider adapter 决定默认注入模式。因此 Phase 3 应拆成三步，避免默认策略散落在 caller 约定里：

- **PR-A：正式化 provider-aware default policy**
  - codexapp default = Summary；claudecli default = Full，直到 Phase 2 完成；
  - 显式 Full / Summary / None 保持不变；
  - `overrideSkillsToSummary` 可先改成 assertion/noop wrapper，断言进入 codexapp 的普通路径已符合 policy；
  - 补 `buildTurnSteerParams` / 新入口防绕过测试。
- **PR-B：claudecli Phase 2 完成后评估是否全局 Summary**
  - 若选择全局 Summary，需要同步调整 `SkillMode.Effective()` 或 provider render default 的兼容语义；
  - 若继续 provider-aware，则把 policy 提升为显式 helper / config，不再依赖零值隐式约定。
- **PR-C：删除临时 override 文件和 caller**
  - 删除 `internal/provider/codexapp/skill_mode_override.go`；
  - 删除 / 改写 `internal/provider/codexapp/skill_mode_override_test.go`（当前 8 测试）；
  - 删除 `internal/provider/codexapp/session_turn.go` 中两处 `overrideSkillsToSummary` 调用；
  - 校验：代码 agent 用 `lsp_grep(text_search)` 分别检索 `overrideSkillsToSummary` 与 `Phase 1.5`；期望前者零命中，后者只允许迁移注释 / 删除说明残留。人类本地可用等价 `rg` / `grep` 命令复核。

### 5.3 Phase 2 触发条件与 Phase 3 red gates

启动 Phase 2 的任一**可量化**条件：
- 实测 system prompt skill 段 token 占用 > 8k 持续 ≥ 7 天，或 skill 总数 ≥ 20，
  或单 skill body 平均 > 2KB。
- codexapp host-direct 路径有 metrics/logging，且 `skill_expand_body` 成功率 ≥ 99%
  持续 30 天。
- 外部集成方要求 `skill_expand_body` 作为模型可调工具。
- 产品明确要求 progressive-disclosure 默认启用（provider-aware 或全局 Summary policy）。

Phase 3 硬性 red gates（任一不满足不得正式化 Summary default policy / 删除 override）：
- Phase 2 claudecli stdio MCP server 真实 Claude CLI E2E 通过；单测已覆盖静态 `tools/list`、lazy host RPC、首轮无 skill tool-call 不触发 expand/read、`--mcp-skill-mode` 不启动完整 Fx/Wails、startup latency、内存 stdio smoke 与真实 agent-terminal 二进制 `Content-Length initialize -> tools/list -> tools/call -> EOF` smoke；上线前仍需 Claude CLI 真实托管 child 场景 / orphan PID 观测。
- host-direct metrics / INFO 日志 / error-rate 告警已上线，不能再凭人工猜测 99% 成功率。
- approval E2E 已闭环，含 approved / denied / timeout。
- rollback / degrade playbook 已演练：能在一个小 PR 内回到 Full/eager；若 Phase 2 已落地，还必须覆盖 claudecli same-binary child 禁用、退出和临时 mcp-config 清理。
- claudecli eager Full 负面断言通过，证明不受 codexapp override / provider-aware default 误伤。
- §9 owner / RACI 已指派，且有人监控触发条件与风险。

### 5.4 审核后推荐派单顺序（2026-04-26）

1. **PR-1：Approval 主闭环 + metadata 传递**（✅ 2026-04-26 已落地）
   - `toolbridge` host-direct 调用上下文已扩为 `HostToolCall`，把
     `agentId/threadId/turnId/callId/cwd/toolName` 传给 `SkillHostTools` / skill service。
   - `ExpandBody` / `ReadResource` cache miss 时已由 service 调用
     `ApprovalRequester.RequestApproval`；approved 后写 `ApproveArtifact`，denied / timeout
     fail closed；无 requester 时才返回 `SkillApprovalRequiredError` fallback。
   - toolbridge fallback / denied 已返回结构化 tool result（`kind=approval_required` /
     `kind=approval_denied`），不再把 `err.Error()` 当普通文本结果。
   - 验收已覆盖：body/resource/path/repo fingerprint approval cache 隔离；approved / denied /
     timeout；host-direct metadata 与 resolved cwd；host-direct 命中不访问 peer。
2. **PR-2：DynamicTools partial degradation**（✅ 2026-04-26 已落地）
   - `ListToolsForCodex` 已改为 host tools 先收集、orch/lsp 并发等待、最长一个
     `peerReadyTimeout`，成功项合并，peer error 只降级记录。
   - 验收已覆盖：host+单 peer 成功、host+双 peer 失败、无 host+双 peer 失败、dedup
     host 优先，以及 `TestListToolsForCodex_PeerWaitIsConcurrent` 锁定不串行等待。
3. **PR-3：模型视角 E2E**（✅ 2026-04-26 已落地）
   - fake app-server 已证明模型看到 `dynamicTools`，发 `skill_expand_body`，tool result 回到
     app-server / 模型路径，approved 后继续产出 final answer delta。
   - 验收已覆盖：`ExpandBodyResultReturnsToModel`、`ApprovalApprovedContinuesFinalAnswer`、
     `ApprovalDeniedReturnsStructuredToolResult`；不再只是断言 schema 存在。
4. **PR-4：selected metadata / redaction 决策**
   - 明确 untrusted selected summary 策略。若允许暴露，必须限定真实手选；legacy
     `Source=Unspecified` 不得因 hydration 默认补 `manual` 而被误授权。
5. **PR-5：resume / recovery skill tools**
   - 先证明 app-server 是否保留 start-time dynamic tools；若不保留，再设计 resume/recovery
     重新注册或扩 app-server schema。
6. **PR-6：默认 discovery / rollout observability**
   - PR-1 / PR-2 / PR-3 与 host-direct 基础 observability 已落地；PR-4 / PR-5 之前仍保持
     `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false`。
   - 默认放量前还需补 exporter / alerting / rollout observation：至少能从外部系统观察
     host tool success/error、cwd_missing、approval_required、enrich failure 与 artifact approval/cache 信号。

### 5.5 Planned test names / 验收矩阵（补充）

> 本节是后续实现 PR 的 **planned test names**。2026-04-26 起，PR-1 / PR-2 / PR-3 已由
> planned 提升为实际回归测试；PR-4 以后仍是派单测试名建议。若实现时包名 / helper 名称调整，可改测试名，
> 但不得降低验收语义：approval 必须真实闭环，DynamicTools 必须可降级但不丢 peer，模型视角 E2E
> 必须证明 tool result 回到模型路径。

| PR | 建议测试入口 | Planned test names | 必须锁住的验收语义 |
|---|---|---|---|
| PR-1 Approval 主闭环 | `go test ./internal/module/skill -run 'Test(ExpandBody|ReadResource|ArtifactApproval)' -count=1` | `TestExpandBody_UntrustedProjectSkill_ApprovalRequesterApproved`、`TestExpandBody_UntrustedProjectSkill_ApprovalRequesterDenied`、`TestExpandBody_UntrustedProjectSkill_ApprovalRequesterTimeout`、`TestExpandBody_NoApprovalRequester_ReturnsApprovalRequiredFallback`、`TestReadResource_UntrustedProjectSkill_ApprovalRequesterApproved`、`TestArtifactApprovalCache_BodyDoesNotApproveResource`、`TestArtifactApprovalCache_ResourcePathIsolation`、`TestArtifactApprovalCache_RepoFingerprintIsolation` | `ExpandBody` / `ReadResource` cache miss 时由 `skill.Service` 调 `ApprovalRequester`；approved 后写 `ApproveArtifact`；denied / timeout fail closed；body、anchor、resource path、repo fingerprint 互相隔离 |
| PR-1 host-direct metadata / structured result | `go test ./internal/platform/toolbridge -run 'Test(CallHostTool|RouteToolCall)' -count=1` | `TestCallHostTool_PassesApprovalMetadata`、`TestCallHostTool_ApprovalDeniedReturnsStructuredResult`、`TestCallHostTool_ApprovalRequiredFallbackReturnsStructuredResult`、`TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD` | `agentId/threadId/callId/cwd/toolName` 进入 approval payload；no-requester fallback 是结构化 `kind=approval_required`，不是普通 `err.Error()` 文本；host-direct 命中不访问 peer |
| PR-2 DynamicTools partial degradation | `go test ./internal/platform/toolbridge -run TestListToolsForCodex -count=1` | `TestListToolsForCodex_HostToolsSurviveOrchFailure_LSPReady`、`TestListToolsForCodex_HostToolsSurviveLSPFailure_OrchReady`、`TestListToolsForCodex_HostOnlyWhenBothPeersFail`、`TestListToolsForCodex_ReturnsErrorWhenNoHostAndPeersFail`、`TestListToolsForCodex_DedupKeepsHostBeforePeer`、`TestListToolsForCodex_PeerWaitIsConcurrent` | host tools 先收集；orch/lsp 并发等待且最长一个 `peerReadyTimeout`；单 peer 成功必须被保留；双 peer 失败但 host 存在仍返回 host；只有无 host 且 peer 全失败才 error |
| PR-3 模型视角 E2E | `go test ./internal/provider/codexapp -run TestDynamicSkillTools_ModelE2E -count=1` | `TestDynamicSkillTools_ModelE2E_ExpandBodyResultReturnsToModel`、`TestDynamicSkillTools_ModelE2E_ApprovalApprovedContinuesFinalAnswer`、`TestDynamicSkillTools_ModelE2E_ApprovalDeniedReturnsStructuredToolResult` | fake / controlled app-server 证明 `thread/start dynamicTools -> model dynamic_tool_call(skill_expand_body) -> approval/cache/read -> tool result -> final answer`；只断言 schema 存在不得通过 |
| PR-4 selected metadata / redaction | `go test ./internal/module/turn -run TestApplyHydration_UntrustedSummary -count=1`；`go test ./internal/module/prompt -run TestSkillCatalogProvider -count=1` | `TestApplyHydration_UntrustedSummary_RedactedWhenSourceUnspecified`、`TestApplyHydration_UntrustedSummary_AllowsOnlyRealManualSelection`、`TestApplyHydration_UntrustedSummary_RedactedForTriggerAndForce`、`TestSkillCatalogProvider_UntrustedProjectSkillRedacted` | 若允许 untrusted selected summary，只能限真实 `ManualSkillSelection=true && source=manual`；legacy `Source=Unspecified` 经 hydration 补成的 manual 不得当授权；catalog redaction 继续成立 |
| PR-5 resume / recovery skill tools | `go test ./internal/provider/codexapp -run 'Test(Resume|Recovery).*DynamicSkillTools' -count=1` | `TestResumeSession_DynamicSkillToolsStillCallable`、`TestRecoveryResume_DynamicSkillToolsStillCallable`、`TestThreadResume_AppServerRetainsStartDynamicTools`、`TestThreadResume_DynamicToolsWireCompatibilityIsExplicit` | 先证明 app-server 是否保留 start-time tools；若需要扩 `thread/resume`，必须证明 app-server 接受 / 显式处理该字段；验收以 resume/recovery 后模型仍能调用 skill tools 为准 |
| PR-6 discovery / observability | `go test ./internal/module/prompt -run 'Test(SkillProgressiveDisclosure|SkillCatalogProvider)' -count=1`；`go test ./internal/platform/toolbridge -run 'Test.*Observability|Test.*Metrics' -count=1` | `TestSkillProgressiveDisclosure_DefaultDisabled`、`TestSkillProgressiveDisclosure_EnableFlagRendersCatalog`、`TestSkillCatalogProvider_GroupsNativeTrustedRedacted`、`TestListToolsForCodex_LogsDegradedPeer`、`TestHostSkillToolCall_EmitsApprovalAndCacheMetrics` | 默认仍为 disabled；enable=true 时 catalog 分组与 untrusted redaction 正确；放量前能观察 degraded peer、host tool success/error、approval requested/approved/denied/timeout、artifact cache hit/miss |
| Claude Phase 2 MCP child | `go test ./internal/provider/claudecli -run 'TestSkillMCP(Server|Mode)_(ToolsListStaticNoHostRPC|ExpandBodyLazyCallsHostRPC|ReadResourceLazyCallsHostRPC|RejectsModelRuntimeFields|FirstTurnWithoutSkillDoesNotCallExpandRPC|ApprovalRequiredReturnsStructuredEnvelope|ObservabilityCounters|StartupLatencyBudget|StdioSmokeInitializeListCallAndEOF)|TestSkillHostRPCClient_ValidatesHostResponse|TestTransportConfig_SameBinarySkillServerMCPConfig' -count=1`；`go test ./cmd/agent-terminal -run '^TestMcpSkillMode_(DoesNotStartFullApp|RealBinaryFramedStdioSmokeAndEOF|RealBinaryLatencyBudget|ClaudeLikeParentLifecycleEOFCancelAndNoOrphan|ClaudeCLIManagedSameBinarySkillE2E)$' -count=1` | `TestSkillMCPServer_ToolsListStaticNoHostRPC`、`TestSkillMCPServer_ExpandBodyLazyCallsHostRPC`、`TestSkillMCPServer_ReadResourceLazyCallsHostRPC`、`TestSkillMCPServer_RejectsModelRuntimeFields`、`TestSkillMCPServer_FirstTurnWithoutSkillDoesNotCallExpandRPC`、`TestSkillMCPServer_ApprovalRequiredReturnsStructuredEnvelope`、`TestSkillMCPServer_ObservabilityCounters`、`TestSkillMCPMode_StdioSmokeInitializeListCallAndEOF`、`TestSkillHostRPCClient_ValidatesHostResponse`、`TestMcpSkillMode_DoesNotStartFullApp`、`TestMcpSkillMode_RealBinaryFramedStdioSmokeAndEOF`、`TestMcpSkillMode_RealBinaryLatencyBudget`、`TestMcpSkillMode_ClaudeLikeParentLifecycleEOFCancelAndNoOrphan`、`TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E`、`TestTransportConfig_SameBinarySkillServerMCPConfig`、`TestSkillMCPServer_StartupLatencyBudget` | `initialize/tools/list` 只返回静态 `skill_expand_body` / `skill_read_resource` schema，不扫 skill、不读 `SKILL.md`、不调 host RPC、不触发 approval；第一次 `tools/call` 才 lazy 调父进程；stdio smoke 覆盖内存 server 与真实 agent-terminal 二进制 `Content-Length initialize -> tools/list -> tools/call -> EOF`；普通首轮不使用 skill 时不得发生 expand/read RPC；runtime env 不继承伪造 `GO_AGENT_SKILL_MCP_*` / per-turn turnID；`--mcp-skill-mode` 不进入完整 Fx/Wails，startup / 真实二进制 latency smoke 有明确耗时预算；skill MCP child emits success/error/approval_required counters，真实 Claude CLI 未认证环境按 opt-in 测试 skip |

提交或合并前的最小验证命令建议：

```bash
go test ./internal/module/skill -run 'Test(ExpandBody|ReadResource|ArtifactApproval)' -count=1
go test ./internal/platform/toolbridge -run 'Test(ListToolsForCodex|CallHostTool|RouteToolCall)' -count=1
go test ./internal/provider/codexapp -run 'Test(DynamicSkillTools|Resume|Recovery)' -count=1
go test ./internal/module/turn -run TestApplyHydration_UntrustedSummary -count=1
go test ./internal/module/prompt -run 'Test(SkillProgressiveDisclosure|SkillCatalogProvider)' -count=1
go test ./pkg/skillmetrics ./internal/provider/claudecli -run 'TestCountersIncrementAndSnapshot|TestResetForTestingZeroes|TestSkillMCP(Server|Mode)_(ToolsListStaticNoHostRPC|ExpandBodyLazyCallsHostRPC|ReadResourceLazyCallsHostRPC|RejectsModelRuntimeFields|FirstTurnWithoutSkillDoesNotCallExpandRPC|ApprovalRequiredReturnsStructuredEnvelope|ObservabilityCounters|StartupLatencyBudget|StdioSmokeInitializeListCallAndEOF)|TestSkillHostRPCClient_ValidatesHostResponse|TestTransportConfig_SameBinarySkillServerMCPConfig' -count=1
go test ./cmd/agent-terminal -run '^TestMcpSkillMode_(DoesNotStartFullApp|RealBinaryFramedStdioSmokeAndEOF|RealBinaryLatencyBudget|ClaudeLikeParentLifecycleEOFCancelAndNoOrphan|ClaudeCLIManagedSameBinarySkillE2E)$' -count=1
git diff --check
```

---

## 6. 决策日志

### 6.1 路径决策（不走 mcp-orch）

源于 P20.11 废弃文档：mcp-orch 是独立进程，注册 skill 工具会陷入"重复实现"
或"bootstrap 反向 IPC"两个不可接受选择。正确路径是 codexapp 宿主进程内 host-direct + provider 暴露；claudecli B3 仍允许受控的 same-binary child → host RPC client 回调，但 child 不得复制 / 持有 `skill.Service`。

### 6.2 Phase 2 推迟（不在本 PR 完成）

reviewer 3 验证：claudecli 子进程的 B1/B2/B3 是真三岔路，选错回滚成本高。
本 PR 不打算扩范围做 Phase 2，留给独立 PR + 单独评审。

### 6.3 Phase 1.5 引入（codexapp Unspecified override）

不引入则 Phase 1 只完成工具链能力，缺少让 `Mode=Unspecified` 请求走 Summary 的入口。
该 override **只转换 Unspecified**；显式 Full / Summary / None 一律尊重。随后代码层已把普通 selected skill / name-only hydrate / cron 路径收敛为保留 Unspecified marker，因此 codexapp 普通路径现在会走 Summary。
但它仍是 codexapp-only 临时兼容层，不是“整个 harness 默认 progressive-disclosure 已业务交付”的证明：claudecli 仍通过 `SkillMode.Effective()` 保持 Full/eager，模型视角 E2E 与可观测性也未闭环。代码注释明确标记“Phase 1.5 临时”，Phase 3 删除或替换为正式 provider default policy。

### 6.4 agentId enrichment（修复方式）

源于审查发现的 bug：codex 协议不含 agentId，但 host-direct 路径强依赖。
修复点选在 `codexapp/session.go:onInboundMessage`，理由：
- 离 bug 最近
- 不改 toolbridge 接口
- 收紧 host-direct trust boundary：session agentID 是唯一真值，外部 `agentId` / `agent_id` 不可信
- 旧测试 fixture 已改为断言外部 agentId 被覆盖

> "已存在不覆盖" 是历史风险口径；本轮已按 §10.1 改为 always-overwrite。

### 6.5 cwd 强制覆盖（schema + runtime 双层）

- `pkg/skilltool` 的 ExpandBody / ReadResource schema **不暴露 cwd 字段**，
  `additionalProperties: false`，模型无法通过 schema 透传 cwd
- `host_tools.go:CallHostTool` 在 `decodeArgs` 之后无条件 `p.CWD = cwd`，
  即使协议未来变更允许多余字段，也会被覆盖
- ctx 也通过 `skillpkg.WithCWD(ctx, cwd)` 双轨注入，下游 service 可二次校验

防御充分；但 cwd 真值仍依赖 `resolveAgentCWD(req.AgentID)` 的 agentId 可信度，
所以 §6.4 / §10.1 是真正的 trust 边界。

### 6.6 enrich fail-soft（吞所有 error）

`enrichToolCallParams` 在 JSON 解码 / 写回失败时**返回原始 msg 不报错**。理由：
- 非 tool/call 消息走原路径，不影响主链路
- enrichment 只是补字段，失败回退到 "模型看见 cwd is required" 是可接受降级

当前已补 `enrich_failures_total` counter：若未来 codex 协议变更导致
`msg.Params` 解码恒挂，至少能从 in-process snapshot / 后续 exporter 发现异常；函数本身仍保持 fail-soft，不把坏 payload 升级成主链路错误。

### 6.7 dedup 优先级 hostTools > peer（静默）

选择 "hostTools first-seen wins" 而非 "name conflict → fail" 的理由：
- mcp-orch / mcp-lsp 都是远端进程，命名冲突时 host 内嵌实现性能 / 安全更优
- fail-fast 会让任何一个第三方 server 注册同名工具就阻塞 thread/start

当前已补 shadow WARN：dedup 时若 peer 工具被 host 同名工具 shadow，会记录 tool / source /
shadowed_by，避免同名冲突静默消失。仍保留的长期选项是为 host 工具加强制前缀
（如 `host__skill_expand_body`），但代价是改 schema + 系统 prompt 元指令。

---

## 7. 文件改动总览

```
新建 / 新增文档（10 个）：
  pkg/skilltool/schema.go
  pkg/skilltool/schema_test.go
  internal/platform/toolbridge/host_tools.go
  internal/platform/toolbridge/host_tools_test.go
  internal/provider/codexapp/skill_mode_override.go
  internal/provider/codexapp/skill_mode_override_test.go
  internal/provider/codexapp/session_enrich.go
  internal/provider/codexapp/session_enrich_test.go
  docs/plans/迁移/p25skill优化/p25skill优化.md（本文档）
  docs/plans/迁移/p20/p20.18-host-direct-skill-tool-exposure.md（plan）

修改（9 个）：
  internal/module/turn/skills.go
  internal/module/turn/factory.go
  internal/module/turn/skills_test.go
  internal/module/cron/turn_adapter.go
  internal/platform/toolbridge/handler.go
  internal/platform/toolbridge/module.go
  internal/provider/codexapp/session.go
  internal/provider/codexapp/session_turn.go
  docs/plans/迁移/p20/README.md（p20.18 / P25 反链与状态同步）
```

说明：本节只列文件，不再维护精确行数；行数已在多轮 refactor / review 后漂移，提交前如需审计请用脚本重新生成。

---

## 8. 接手者快速 onboarding

如果你是接手 Phase 2 / Phase 3 的同学：

1. 先读 `p20.18` plan §4.2 (B1/B2/B3) + §11（Phase 1.5）+ §12（owner / 触发条件）
2. 跑 `git log --oneline --grep="p20.18\|p25\|skill_mode_override\|host_tools"`
   看本 PR 及后续 refactor 提交历史
3. 看本文档 §5 的 Phase 2 描述，对照 `p20.18` §4.2 B3 + p20.18 §4.3 状态共享设计
4. **必读 §10 已知风险**——agentId trust 姿态、可观测性、dedup 静默、入口约定
   都是 Phase 2 / 3 路上会再次踩到的雷
5. 验证 Phase 1 在你环境下正常工作：
   ```bash
   go build -buildvcs=false ./...
   go test ./internal/provider/codexapp/ -run '^(TestToolBridge_StartSession_UsesDynamicTools|TestEnrichToolCallParams_.*|TestOverrideSkillsToSummary_.*|TestBuildTurnStartParams_AppliesSummaryOverride|TestPrepareTurnToBuildTurnStartParams_NameOnlySkillUsesSummary)$' -count=1
   go test ./internal/platform/toolbridge/ -count=1
   go test ./pkg/skilltool/ -count=1
   go test ./internal/module/turn/ -count=1
   ```
6. 端到端验证 host-direct / Summary override 能力；这一步仍不是 harness 级业务 PR-ready 证明：
   ```text
   1. 启动 codexapp，确认 thread/start 的 DynamicTools 含 skill_expand_body / skill_read_resource
   2. 构造普通 name-only / selected skill 请求，确认 PrepareTurn 后 Mode 仍是 Unspecified marker
   3. codexapp buildTurnStartParams 后确认 system prompt 只有 name + summary + 工具指针
   4. 触发模型调 skill_expand_body，确认返回的 inputText 是 SKILL.md body
   5. 构造显式 Full 请求，确认仍 eager 注入完整 body
   6. 检查 claudecli 走老路径：Unspecified 经 Effective() 回到 Full，system prompt 仍 eager 注入完整 body
   ```
7. Phase 2 实施前确认 §10.1 的 always-overwrite 修复与
   `TestEnrichToolCallParams_OverridesExisting` 已合入并通过。

---

## 9. Owner / DoD / Rollback / Non-goals

### 9.1 Owner / RACI

> **当前 owner**：（待认领）。这是 release 管理问题，不改变 §9.2 的业务能力判断。
> 后续进入 Phase 3 默认切换 / exporter-alerting 放量 PR 前，再补 Responsible / Accountable / Consulted / Informed 即可；本文不把 owner 缺失作为当前能力主 blocker。
>
> **最后更新**：2026-04-27 / Phase 2 same-binary MCP child 与 host-direct observability 复核；codexapp selected/name-only Summary、project approval 闭环、模型视角 E2E、partial degradation、基础 observability 已关闭；全量 discovery、resume/recovery、真实 Claude CLI 已认证 E2E、Phase 3 policy 与 rollout 导出/告警仍待闭环。

### 9.2 业务能力 Definition of Done

当前文档若仅作为“交接草案 / 风险清单”交付，已满足：

- [x] 不再声称整个 harness 默认 progressive-disclosure 已生产生效。
- [x] 明确 codexapp 普通 name-only / selected skill 路径已有代码级 Summary 验证。
- [x] 明确 Phase 1.5 只覆盖 codexapp `Mode=Unspecified` 临时路径，不改变显式 Full / Summary / None。
- [x] 明确 claudecli 仍通过 `SkillMode.Effective()` 保持 Full/eager，Phase 2 same-binary MCP child 只是工具链最小链路，不等于默认策略已切换。
- [x] 历史测试数量口径已说明；后续实际测试集合已扩展到 PR-1/2/3、Phase 2 MCP child 与 observability 回归。
- [x] 已知测试盲点已重新拆分：模型视角 E2E / approval / partial degradation / observability 基础已关闭；真实 Claude CLI、resume/recovery、default policy 与 rollout 导出/告警仍保留。

若要升级为**业务能力 PR-ready**，当前状态拆成“已关闭缺口”和“仍保留 red gates”：

已关闭 / 已有回归覆盖：

- [x] **BUSINESS CLOSED**：模型视角 E2E 已落地。fake / controlled app-server 已证明真实 DynamicTools 包含 skill tools、模型发 `skill_expand_body`、approval/cache/read 结果回到模型并继续产出 final answer；不再只是断言 schema 存在。
- [x] **BUSINESS CLOSED**：project-scope skill 首次展开 approval 主闭环已落地。`skill.Service` cache miss 会调 `ApprovalRequester`；approved 后写 `ApproveArtifact`；denied / timeout fail closed；host-direct metadata 会进入 approval payload；no-requester fallback 返回结构化 `kind="approval_required"`。
- [x] **BUSINESS CLOSED**：DynamicTools partial degradation 已落地。host skill tools 先收集，orch/lsp peer 并发等待；单 peer 成功保留、双 peer 失败但 host 存在仍返回 host；同名 peer 被 host shadow 时有 WARN。
- [x] **BUSINESS CLOSED**：host-direct 基础观测已落地。已有 `host_tool_calls_total{outcome}` 对应 Go counters、`enrich_failures_total` counter、host-direct INFO 日志、cwd_missing WARN、approval/error structured result。仍未包含 Prometheus / OTel exporter、error-rate 告警与 30 天放量观察。

仍保留 red gates / 决策项：

- [ ] **BUSINESS BLOCKED**：selected/name-only Summary 与 `SkillCatalogProvider` 的 untrusted metadata redaction 策略不一致；需明确真实手选是否允许 summary，且不能把 legacy `Source=Unspecified` 经 hydration 补成的 `manual` 当作授权。
- [ ] **BUSINESS BLOCKED**：全量 skill discovery 默认未启用；`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false` 时模型只能看到 selected/name-only skill，不会默认获得完整 catalog。默认开启需等待 discovery / redaction / observability / rollout gates。
- [ ] **BUSINESS BLOCKED**：resume / recovery 后 `skill_expand_body` / `skill_read_resource` 是否仍可用未验证；`thread/resume` 不携带 dynamicTools 字段，但扩字段前必须先证明 app-server 是否保留 / 是否接受 resume dynamicTools。
- [ ] **BUSINESS BLOCKED**：显式 Full 仍 eager 注入完整 body；这是兼容设计，但产品/迁移策略需确认是否允许长期存在。
- [ ] **VALIDATION REMAINS**：claudecli Phase 2 same-binary stdio MCP child 最小实现已落地，两个 provider 的模型可见 skill tool 语义已有代码级 parity；内存 stdio、真实 agent-terminal 二进制 `Content-Length initialize -> tools/list -> tools/call -> EOF`、Claude-like parent stdin EOF / context cancel / no-orphan lifecycle、真实二进制 initialize / tools/list / tools/call / stdin EOF latency budget 已有单测 smoke。真实 Claude CLI `--mcp-config` E2E 已有 opt-in 测试，未认证环境会跳过；业务 PR-ready 仍必须补已认证环境下的真实 Claude CLI tool-call E2E 与放量观测。
- [ ] **ROLL-OUT BLOCKED**：metrics exporter / alerting / rollout observation 未完成。当前只是 in-process counters + 日志，Phase 3 默认策略前仍需 Prometheus / OTel 或等价导出、error-rate > 5% 持续 5min 告警、30 天成功率观察。
- [ ] **BUSINESS BLOCKED**：Phase 3 provider default policy / override 删除还没有可量化验收证据（CI job / test command / smoke checklist / rollout observation）。

Release governance / 风险 gate 只作为发布管理补充，不盖过上述业务能力判断：

- [ ] **RELEASE FOLLOW-UP**：§9 owner / RACI、默认放量监控人和 rollout 值守机制仍需在默认切换 PR 中指派；不把 owner / 签核缺失写成当前能力主 blocker。

### 9.3 Rollback / degrade playbook

若 codexapp Summary / host-direct 路径线上异常：

1. **当前可用触发条件（exporter / 告警未上线前）**：人工 smoke 失败、用户报告 skill body 不可展开、可复现 tool response 错误、host-direct INFO/WARN 日志里 `cwd_missing` / `approval_required` / `error` outcome 异常集中、或 in-process snapshot 中 `host_tool_calls_total{outcome!="ok"}` / `enrich_failures_total` 异常增长。
2. **自动化触发条件（P25-HIGH-02 导出与告警落地后才可用）**：host-direct error-rate > 5% 持续 5min、approval_required 无法闭环告警、`enrich_failures_total` 异常增长。在 Prometheus / OTel 或等价导出与告警上线前，不得宣称自动化 rollback gate 可执行。
3. **Phase 1.5-only 最小回滚 PR**：在 `internal/provider/codexapp/session_turn.go` 临时移除 / noop `overrideSkillsToSummary` 调用，只覆盖 `Mode=Unspecified → Summary` 这条临时路径，使 Unspecified 恢复 eager Full；保留 host tools 代码。
4. **显式 Summary / 工具异常降级**：若已有显式 `Mode=Summary` 流量或 host tool 本身异常，临时在 `internal/platform/toolbridge/host_tools.go` / `ListToolsForCodex` 暴露层移除 `skill_expand_body` / `skill_read_resource`，或在入口把该流量强制转 Full，避免模型反复调用坏工具。
5. **Phase 2 claudecli child 降级（若已落地）**：禁用 manifest 中 `skill` server / `LaunchKindSameBinarySkill` 生成，重启 Claude session 让新 `--mcp-config` 生效；确认 child 收到 stdin EOF 后退出，清理临时 `--mcp-config` 文件，并检查是否存在 orphan child / PID 泄漏。
6. **Phase 3 回滚点**：若已正式化 provider default policy，关闭 codexapp Summary 默认或恢复对应 policy helper；若未来调整过 `SkillMode.Effective()` / provider render default，也必须同步回滚到 Unspecified→Full 兼容语义。
7. **不动 claudecli 当前 eager Full 基线**：Phase 2 same-binary child 已落地后，claudecli eager Full 仍是 provider default policy 切换前的降级参考；只允许按上一条禁用 skill MCP server，不在同一 PR 中同时改变 claudecli 默认行为与 child lifecycle。
8. **验证命令**：回滚前后复跑 §4.2 的 codexapp / claudecli / toolbridge / turn 最小测试，并手工 smoke：Mode=Unspecified、显式 Full、显式 Summary 三类 system prompt 均符合预期；Phase 2 已落地时还要附 child 退出 / 临时配置清理证据。

### 9.4 Non-goals（当前 PR 不做）

- 不正式切换 claudecli 默认 skill render 策略；Phase 2 stdio MCP server 仅作为最小工具链落地，真实 Claude CLI E2E / lifecycle 观测仍是上线前置。
- 不正式化全局 Summary default policy，也不调整 `SkillMode.Effective()` 的 Unspecified→Full 兼容语义。
- 不恢复 p20.11 mcp-orch 注册 skill tools 路线。
- 不改变 claudecli eager Full 行为。
- 不承诺解决 §10 全部风险；§10 仅保留影响后续实现 / 发布观察的风险提示。
- 不以当前 Phase 1.5 override 作为“默认 progressive-disclosure 已上线”的验收依据。

---

## 10. 已知风险与未做事项（review 沉淀）

本节集中收口 2026-04-26 多 agent review 发现的问题；业务能力 blockers 以 §9.2 为准。
本节只保留会影响后续实现、默认切换或发布观察的风险提示，避免让治理事项压过业务能力判断。
其中 §10.1 已在本轮闭包修复，其余大多仍是当前 PR 范围外风险。

### 10.1 agentId trust 姿态反向（HIGH，安全，已修）

- 位置：`internal/provider/codexapp/session_enrich.go:14-39`
- 原风险：enrich 曾在 `msg.Params` 已有非空 `agentId` / `agent_id` 时**保留**外部值；未来 codex 协议透传 / 中间件改写 / fixture 复用都可能让模型间接控制 agentId，导致下游 `resolveAgentCWD(req.AgentID)` 解析到**别的 agent 的 cwd**，跨 session 越权读 SKILL.md / 资源。
- 本轮修复：`enrichToolCallParams` 现在 always overwrite canonical `agentId = s.agentID`，并删除 `agent_id` alias；测试 fixture 改为断言外部 agentId 被覆盖。
- 验收：`TestEnrichToolCallParams_OverridesExisting` 锁定恶意 / 旧 fixture agentId 不能胜过 session agentID。
- Release note：当前 worktree 已包含修复；若后续拆 PR 导致修复未随当前 PR 合入，不得默认切换 Summary。
- 验收：`go test ./internal/provider/codexapp -run Enrich -count=1` 通过；review 确认 codexapp `onInboundMessage` enriched path 覆盖 canonical `agentId` 并 strip `agent_id` 后再进入 toolHandler。注意：legacy / 非 codexapp-enriched 调用仍可能被 `decodeToolCallRequest` alias 兼容读取 `agent_id`，不得把本 gate 扩大解释为全仓 alias 移除。

### 10.2 ReadResource 路径 traversal 未在 host-direct 层独立断言（MED，安全）

- 位置：依赖 `internal/module/skill/skills_expand.go` 的 `resolveResourceTarget`
- 风险：`EvalSymlinks` 失败时回退到字面路径，`os.ReadFile` 仍跟符号链接读出
  skillDir 之外的文件；trust=user/signed skill 不需审批，攻击面来自被植入
  恶意 symlink 的 skill 包
- 建议：`EvalSymlinks` 错误直接报错 + `os.Lstat` 二次确认 / `securejoin.SecureJoin`；
  并在 host-direct 层加跨层 contract test

### 10.3 host-direct 错误体未结构化（MED）

- 位置：`internal/platform/toolbridge/handler.go:719-742` `callHostTool`
- 现状：把 `ExpandBodyResult` 整体 `json.Marshal` 塞进 `inputText`，含绝对 `Path`，
  泄露宿主文件系统结构；审批失败的错误体只有 `err.Error()` 文本，缺
  `kind="approval_required"` 标识
- 建议：与 peer 路径一致使用 MCP content item；序列化前 strip / cwd-relative 化
  `Path`；专门识别 `SkillApprovalRequiredError` 返回结构化结果

### 10.4 dedup 静默吞 peer / host 工具命名前缀（MED）

详见 §6.7。落地选项二选一即可。

### 10.5 基础可观测性已补（HIGH，运维：导出 / 告警待做）

详见 §3.4。Phase 1 原先“生产静默坏掉无信号”的问题已收敛为：

- 已补：`host_tool_calls_total{outcome}` 对应 Go counters、`enrich_failures_total` counter、host-direct INFO 日志、cwd_missing WARN、peer shadow WARN。
- 后续：Phase 3 正式化 Summary default policy / 删除 override 前，仍需补 Prometheus / OTel 或等价导出、error-rate 告警与 30 天 rollout observation，才能把 99% 成功率从人工判断升级为可执行 gate。
- 定位：这是 rollout support，不是当前 codexapp 普通路径代码级能力的 blocker；当前业务主 blocker 仍是真实 Claude CLI 已认证 E2E、resume/recovery、redaction/default discovery 与 Phase 3 provider policy。

### 10.6 Mode override 在 caller 而非 sink（MED，可维护性）

- 位置：`internal/provider/codexapp/session_turn.go:42, 57`
- 风险：`overrideSkillsToSummary` 仅在 `buildTurnStartParams` / `buildTurnSteerParams`
  调用。当前普通路径已有 `PrepareTurn -> buildTurnStartParams` 测试覆盖；但未来若新 RPC / 入口直接调 `turnInputsFromRequest` 或 `buildSkillPromptInput` 忘了先 override，skill 会以 Full 重新泄漏。当前仍是"约定优于强制"。
- 建议：Phase 3 把 provider default policy 下沉为显式 helper / sink-level policy，或把 override 下沉到 `turnInputsFromRequest` / `buildSkillPromptInput` 内部，关闭新入口绕过窗口

### 10.7 SkillRef 默认策略静态检查缺失（MED）

- 现状：当前恰恰需要允许普通路径构造 `dto.SkillRef{Name: x}`，让 `Mode=Unspecified` 作为 provider-aware default marker 流到 provider adapter；因此不能再简单禁止所有字面量构造。
- 风险：未来新代码可能把“默认 Full”重新显式写回 `dto.SkillRef{Mode: SkillModeFull}`，绕过 codexapp Summary 默认；也可能在 provider 边界外误用 Unspecified，造成语义不清。
- 建议：补 archtest / lint 规则时按语义检查：禁止把 `SkillModeFull` 当默认值硬编码；显式 Full 必须有注释或 helper；新增 provider default policy helper 后，禁止绕过该 helper。

### 10.8 跨文档遗留

- `docs/plans/迁移/p20/README.md` 已增 `p20.18` 反链，指向 `p20.18-host-direct-skill-tool-exposure.md` 作为 Mode=Summary 端到端能力硬前置。
- `docs/plans/迁移/p20/README.md` 已同步 p20.18 部分完成状态，并在必读文档中加入本文档反链。后续只需在 p20.18 Phase 2 / Phase 3 状态变化时同步 README 状态 / 关闭提交。
- `pkg/skilltool` 不 import `internal/`、`cmd/mcp-orch` 不 import `pkg/skilltool` 当前只是设计约束 + 代码事实；未发现对应 archtest guard。若要把它作为 CI gate，需补 archtest 后再在文档中标“已强制”。

### 10.9 codemap 冲突标记清理记录（已完成，文档地图可靠性）

二次复核曾发现 `docs/doc/codemap` 多个文档残留 merge conflict markers，包括
`<<<<<<< Updated upstream`、`=======`、`>>>>>>> Stashed changes`。该问题已在本轮清理，
当前用 `lsp_grep(text_search, regex=true)` 检查行首冲突标记：

- `^<<<<<<<`：零命中
- `^=======$`：零命中
- `^>>>>>>>`：零命中

清理覆盖的历史命中文件包括：

- `docs/doc/codemap/01-terminal-ui.md`
- `docs/doc/codemap/02-mcp-orch.md`
- `docs/doc/codemap/03-mcp-lsp-ida.md`
- `docs/doc/codemap/04-app-contract.md`
- `docs/doc/codemap/06-mcpserver.md`
- `docs/doc/codemap/08-platform.md`
- `docs/doc/codemap/10-store.md`
- `docs/doc/codemap/11-memory-prompt-thread.md`
- `docs/doc/codemap/README.md`

结论：该项不再作为 P25 Phase 2 / Phase 3 派单前置 blocker；后续若重新生成 codemap，
仍应把 `^<<<<<<<` / `^=======$` / `^>>>>>>>` 行首检查纳入文档地图卫生 smoke。
