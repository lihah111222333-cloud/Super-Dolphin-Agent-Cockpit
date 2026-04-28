# P25 Skill 优化：从 eager 注入到 progressive-disclosure

> 创建时间：2026-04-25 | 最近核对：2026-04-28（PR-6 rollout observability review-ready + 执行落地边界复核）
> 状态：🟡 codexapp 普通 name-only/selected skill 路径已可走 Summary + host-direct；claudecli same-binary MCP child 最小实现与 host-direct 基础 observability 已落地；PR-6 observability / evidence / rollout guard 已可评审；默认上线仍未完成（真实 production smoke、30 天 observation、authenticated Claude CLI E2E 与 Phase 3 policy 待完成）
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
> claudecli 默认仍通过 `SkillMode.Effective()` 回到 Full；真实 Claude CLI 已认证 E2E、默认 discovery 放量观测与 Phase 3 policy 仍未闭环。

### 0.3 进度标记

图例：✅ 完成 / 🟡 进行中 / ⏸ 显式延期到后续 PR / ⚠️ 已知缺口（详见 §10）

| 阶段 | 状态 | 备注 |
|---|---|---|
| **C0：provider-aware default marker 准备** | ✅ 完成 | 普通 selected/name-only/cron 路径保留 `Mode=Unspecified`；turn 测试锁定 marker 不被 hydrate 物化 |
| **Phase 0：pkg/skilltool 共用 schema** | ✅ 完成 | 4 测试锁 tool name / required / no-cwd / property type / additionalProperties / marshal |
| **Phase 1：toolbridge host-direct 分支** | ✅ 完成 | 10 测试覆盖 SkillHostTools / dedup |
| **Phase 1.5：codexapp Mode override** | ✅ 完成 | 6 helper 测试 + 2 个入口/真实路径测试；仅处理 `Mode=Unspecified`，见 p20.18 §11 |
| **Phase 1 关键 bug 修复：agentId 注入** | ✅ 完成 | session.go enrichment + 6 测试（trust 姿态见 §10.1） |
| **可观测性 / 安全加固** | 🟡 基础已补 | host-direct counters / INFO/WARN / structured result、Prometheus collector、local `/metrics` endpoint、alert/scrape config artifact、production smoke script、evidence bundle collector、PR-6 verification wrapper 与 rollout observation 模板已落地；生产 smoke 通过与 30 天真实观察待做，见 §3.4 / §10 |
| **Phase 2：claudecli stdio MCP server** | 🟡 最小实现已落地 | B3 same-binary child、`--mcp-config` skill server、stdio smoke、真实二进制 lifecycle 与 latency budget 已覆盖；真实 Claude CLI 已认证 tool-call E2E / 放量观测仍是 red gate（见 §5.1 / §5.3） |
| **Phase 3：正式化 provider default policy + override 删除** | ⏸ Phase 2 / E2E 后再做 | 不再是单纯改 `DefaultSkillMode()`；见 §5.2 |

### 0.4 执行摘要 / 当前推荐 PR 队列

当前最短业务闭环不是继续扩新架构，而是把已有 `skill.Service`、`ApprovalManager`、
`toolbridge`、`codexapp DynamicTools` 与 prompt/turn 设施编排成可验收链路。推荐按下表
顺序派单，planned test names / 验收矩阵见 §5.6；前置 PR 未完成前，不建议把
`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 默认打开。

> 2026-04-27 PR-4/PR-5 更新：selected metadata / redaction 决策、resume / recovery skill tools
> 已落地并有回归覆盖；当前推荐下一步从 PR-6 默认 discovery / rollout observability 开始。
>
> 2026-04-28 PR-6 更新：rollout observability / evidence / guard 已落地并可评审；继续推进时应执行
> 生产 smoke、30 天 observation 与 authenticated Claude CLI E2E evidence，不是合并分支、默认开启或删除 override。

| 优先级 | 推荐 PR | 目标 | 关键文件 / 模块 | 必过验收 |
|---:|---|---|---|---|
| P0 ✅ | PR-1：Approval 主闭环 + metadata 传递（已落地） | project skill 首次展开触发 UI approval，approved 后可继续读取 | `internal/platform/toolbridge/{handler,host_tools}.go`、`internal/module/skill/{skills_expand,service,approval}.go` | 已锁：`agentId/threadId/turnId/callId/cwd/toolName` 进入 approval payload；approved/denied/timeout fail closed；`ApproveArtifact` 按 body/resource/path/repo 隔离 |
| P0 ✅ | PR-2：DynamicTools partial degradation（已落地） | host skill tools 不被 orch/lsp peer readiness 拖死，同时尽量保留 peer tools | `internal/platform/toolbridge/handler.go`、`internal/platform/toolbridge/host_tools_test.go` | 已锁：host tools 先收集；orch/lsp 并发等待；成功 peer 合并；双 peer 失败但 host 存在仍返回 host；无 host 且 peer 全失败才 error |
| P0 ✅ | PR-3：模型视角 E2E（已落地） | 证明模型能看到工具、调用 `skill_expand_body`、拿结果继续回答 | `internal/provider/codexapp/dynamic_skill_tools_e2e_test.go`、fake / controlled app-server | 已锁：`thread/start dynamicTools -> model tool call(skill_expand_body) -> tool result -> final answer`；approved/denied 模型视角结构化结果均覆盖 |
| P0 ✅ | PR-4：selected metadata / redaction 决策（已落地） | 收敛 untrusted selected summary 可见性策略 | `internal/module/turn/skills.go`、`internal/module/prompt/skill_catalog_provider.go` | 已锁：untrusted summary 只限真实 `ManualSkillSelection=true && source=manual`；legacy `Source=Unspecified` / trigger / force 不授权；catalog redaction 不泄露作者 metadata |
| P0 ✅ | PR-5：resume / recovery skill tools（已落地） | 证明 resume/recovery 后 skill tools 仍可用 | `internal/provider/codexapp/{driver,recovery}.go` | 已锁：app-server 保留 start-time dynamicTools；`thread/resume` 不携带 dynamicTools；resume/recovery 后模型仍能调用 `skill_expand_body` |
| P1 ✅ | PR-6：默认 discovery / rollout observability（observability guard 已落地 / 可评审） | 为默认 progressive-disclosure 放量做准备，但不切默认行为 | `internal/module/prompt/config.go`、`internal/platform/metrics`、`internal/ui/wails/http_server.go`、`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-*` | 已补 exporter、local `/metrics`、alert/scrape config、smoke/report/gate/preflight/evidence bundle/collector/default-switch guard/PR-6 verifier；仍需真实 production smoke、30 天 observation 与 authenticated Claude CLI E2E 作为 Phase 3 gate |

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
| Codex provider | shared app-server、WS transport、dynamicTools thread/start、approval bridge、recovery/pending replay | codexapp 是当前可落地 provider；resume/recovery 后 DynamicTools 可用性已有回归证明，`thread/resume` 暂不扩 dynamicTools 字段 |
| Claude provider | manifest / MCP config / native skill injection port / eager skill render / same-binary skill MCP child 最小链路 | Phase 2 最小实现已落地；仍缺真实 Claude CLI E2E、stdio smoke / lifecycle 与放量观测证明 |
| UI / Observability / Ops | uistate、dashboard、turnobservation、cachekeepalive、pidregistry、cron/notify/insight | 发布支撑设施已有；P25 已补 host-direct metrics / 日志 / structured approval result、Prometheus collector、local `/metrics` endpoint、alert/scrape config artifact、production smoke script 与 rollout observation 模板，仍缺生产 smoke 结果、catalog refresh 策略与 30 天默认放量观察 |

复核后的策略约束：

1. **不要扩一套新架构**：优先复用 `ApprovalManager`、`SkillCatalogProvider`、`toolbridge`、
   `turnobservation`、provider recovery 等现有设施。
2. **业务闭环优先级高于代码链路存在**：selected/name-only Summary render、project-scope approval、
   untrusted metadata redaction、resume/recovery 与 host-direct 基础 observability 已关闭；全量 discovery、
   生产 Prometheus/Alertmanager smoke 通过、30 天真实 rollout observation 与 Phase 3 policy 未闭环前，不得标记为默认放量 PR-ready。
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
当前 claudecli same-binary skill MCP child 已有最小实现，内存 stdio / 真实 agent-terminal 二进制 smoke / lifecycle / latency 均已覆盖；但真实 Claude CLI 已认证 E2E、provider-aware default policy 与放量观测尚未闭环。因此本阶段仍只在 codexapp 入口保留临时 override，
并且 **只覆盖 `Mode=Unspecified`**；显式 Full / Summary / None 仍保留调用方意图。当前普通 selected skill / name-only hydrate 路径已不再提前物化 Full，因此会以 Unspecified marker 进入 codexapp 并转 Summary。claudecli 在 Phase 3 前仍通过 `SkillMode.Effective()` 保持 Full/eager 兼容，同时额外具备 Phase 2 skill MCP tool 链路供验证。

**预期删除时机**：真实 Claude CLI 已认证 E2E / provider-aware default policy / rollout observability 全部闭环后，
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

### 3.4 可观测性现状（🟡 基础已补，真实 evidence 待跑）

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

PR-6 第一段已补 **Prometheus collector 声明层**：`internal/platform/metrics/skill.go` 把
`pkg/skillmetrics` 的 snapshot 包装成 `prometheus.CounterFunc`，其中 `host_tool_calls_total{outcome}`
保留 `ok|cwd_missing|approval_required|error` 维度，`enrich_failures_total` 也可由默认 gatherer 读取。
PR-6 第二段已补 **promhttp 本地暴露端点**：`internal/platform/metrics/http.go` 提供统一
`/metrics` handler，`internal/ui/wails/http_server.go` 在本地 HTTP asset server（默认
`127.0.0.1:4511`）挂载 `/metrics`，可作为 Prometheus scrape target 的本机入口。
PR-6 第三段已补 **Prometheus scrape/rule-loading config artifact**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-prometheus.yml` 同时声明
`rule_files`、`scrape_configs`、本机 `127.0.0.1:4511/metrics` target 和 Alertmanager 占位。
PR-6 第四段已补 **production apply smoke script**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-smoke.sh` 会检查
`/metrics`、Prometheus target UP、4 条 alert rule 已加载、Alertmanager ready。
PR-6 第五段已补 **daily observation report generator**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh` 会查询
Prometheus `/api/v1/query` 生成 observation row，支持可选串行运行 production smoke，并自动应用
no-sample rule 与 `artifact_approval_miss` 备注。
PR-6 第六段已补 **daily observation append helper**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh` 会把 report row 安全追加到 observation 文件，重复日期、`TODO(...)`、no-sample 却 continue、continue 但 smoke / rollback 非 PASS 均 fail closed，避免 30 天记录靠手工复制错列。
PR-6 第七段已补 **rollout status / next-step helper**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh` 会读取 observation 文件并输出 sample days、no-sample days、non-ok rate、blockers 与 `Next phase actions`，让下一阶段执行项随 evidence 状态一起输出。
PR-6 第八段已补 **one-command daily rollout helper**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh` 会串起 report → append → status，保留 report / append / status artifact，作为每日 observation 执行入口。
PR-6 第九段已补 **30-day rollout gate verifier**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh` 会解析 observation markdown row，
在样本天数不足、manual / production smoke 非 PASS、rollback drill 缺失、non-ok rate 超阈值、或 no-sample 试图闭 gate 时 fail closed。
PR-6 第十段已补 **Phase 3 default-policy preflight gate**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh` 会串起 rollout gate、production smoke evidence、authenticated Claude CLI E2E evidence，作为默认策略正式化 / override 删除前的 fail-closed 检查。
PR-6 第十一段已补 **preflight evidence templates**：production smoke evidence 必须声明 `Evidence type: production-smoke` 且 `Total host tool calls` 为正数；Claude CLI E2E evidence 必须声明 authenticated 且不得含 `SKIP`。
PR-6 第十二段已补 **Phase 3 evidence bundle verifier**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh` 要求 production smoke evidence、Claude E2E evidence、rollout observation、rollout gate output 与 preflight output 收束在同一 bundle 目录并统一校验，避免人工贴错 evidence。
PR-6 第十三段已补 **default-switch static guard**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-default-switch-guard.sh` 会在当前 PR 中 fail closed，确认 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 默认仍为 false、`overrideSkillsToSummary` 仍存在且两处 caller 未被删、Phase 3 gates 仍在。
PR-6 第十四段已补 **Phase 3 evidence bundle collector**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh` 会把已填写 evidence 复制成标准 bundle 文件名，串行运行 rollout gate、Phase 3 preflight 与 bundle verifier，并默认生成 manifest，减少人工组包错误；它仍不启用默认开关、不删除 override。
PR-6 第十五段已补 **one-command PR-6 verification wrapper**：
`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh` 串起脚本语法 / 可执行位检查、default-switch guard、focused PR-6 tests 与 `git diff --check`，作为提交前防漏跑入口；它同样不启用默认开关、不删除 override。
仍未完成的是**生产执行 / 放量层**：生产 Prometheus/Alertmanager 仍需运行 smoke 并把结果附到 observation 行，
30 天真实放量观察仍属于 Phase 3 前 rollout gate。

详见 §10.5。

### 3.5 业务能力主链缺口（2026-04-26 复核新增）

以下缺口不是单纯治理 / 文档卫生问题，而是决定 P25 能否从“代码级链路打通”升级为
“业务能力 PR-ready”的主链路判断：

1. ✅ **project-scope skill 首次展开 approval 闭环已关闭**：`skill_expand_body` / `skill_read_resource`
   cache miss 会进入 `ApprovalRequester` 主路径；approved 后写 `ApproveArtifact`，denied / timeout fail closed；
   no-requester fallback 返回结构化 `kind="approval_required"`，不再把错误当普通文本。
2. ✅ **selected skill Summary 与 catalog redaction 策略已收敛**：untrusted project skill 的作者 metadata
   只在真实手选 `ManualSkillSelection=true && source=manual` 时暴露；legacy `Source=Unspecified`、trigger、force
   均不得因为 hydration 误授权。`SkillCatalogProvider` 仍对 unknown/invalid trust 做 redacted placeholder。
3. **全量 skill discovery 默认未启用**：`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 当前默认
   `false`，`SkillCatalogProvider` 默认不注册。因此已打通的是 selected/name-only skill
   的 Summary path，不是“模型默认看到全量 skill catalog 并自主发现”的业务能力。
4. ✅ **host skill tools host-only partial degradation 已关闭**：`ListToolsForCodex` 先收集 host tools，
   orch / lsp peer 并发等待；单 peer 失败只降级记录，双 peer 失败但 host 存在仍返回 host；无 host 且
   双 peer 失败才返回错误，同名工具由 host 优先并打 shadow WARN。
5. ✅ **resume / recovery 后 DynamicTools 可用性已证明**：`thread/start` 继续携带 dynamicTools，
   `thread/resume` 明确不携带 dynamicTools；回归测试证明 app-server 会保留 start-time dynamicTools，
   resume/recovery 后模型仍能调用 `skill_expand_body`。
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

### 4.1 核心回归测试矩阵（截至 2026-04-27）

> 测试数随 PR-1~PR-5 持续增长，本节不再维护易过期的总数，只记录业务 gate 对应的回归入口。

| 包 / 区域 | 代表测试 | 覆盖范围 |
|---|---|---|
| `pkg/skilltool` | `schema_test.go` | tool name / required / no-cwd / property type / additionalProperties / marshal |
| `internal/platform/toolbridge` | `TestCallHostTool_*`、`TestListToolsForCodex_*`、`TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD` | SkillHostTools、approval structured result、host metrics/logs、peer partial degradation、host shadow dedup |
| `internal/provider/codexapp` Mode override / enrich | `TestOverrideSkillsToSummary_*`、`TestEnrichToolCallParams_*` | `Mode=Unspecified` → Summary 临时 override、agentId 注入 / 覆盖外部 alias / fail-soft |
| `internal/provider/codexapp` 模型视角 / resume | `dynamic_skill_tools_e2e_test.go`、`TestResumeSession_DynamicSkillToolsStillCallable`、`TestRecoveryResume_DynamicSkillToolsStillCallable` | DynamicTools → model tool call → result 回模型；resume/recovery 后 skill tools 仍可用 |
| `internal/module/turn` | `TestApplyHydration_UntrustedSummary_*` | Unspecified marker、hydration preserves Mode、untrusted selected summary 只限真实手选 |
| `internal/module/prompt` | `TestSkillCatalogProvider_Untrusted*`、`TestGroupSkillsForManifest_UntrustedGoesToRedactedNotManualOnly` | catalog redaction、untrusted + disable-model-invocation 不泄露 metadata / 不落 Manual-only |
| `internal/provider/claudecli` / `cmd/agent-terminal` | `TestSkillMCPServer_*`、`TestMcpSkillMode_*`、`TestTransportConfig_SameBinarySkillServerMCPConfig` | same-binary stdio MCP child、static tools/list、lazy host RPC、真实二进制 smoke / lifecycle / latency |

### 4.2 全套测试状态

> 注：本轮验证中 `go build ./...` 可通过；若某些 worktree 因 VCS stamping 报
> `error obtaining VCS status: exit status 128`，可使用 `go build -buildvcs=false ./...`
> 作为 fallback。以下代码块保持可直接复制执行，不在命令行尾部混入状态符号。

```bash
go test ./internal/archtest -run TestCodeSizeGuard -count=1
go test ./internal/module/skill ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/module/turn ./internal/module/prompt ./pkg/skillmetrics ./internal/provider/claudecli ./cmd/agent-terminal ./internal/provider/unified ./internal/dto/provider ./internal/provider/manifestbuilder -count=1
git diff --check
# optional one-command equivalent / final local pre-submit wrapper:
docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh
```

### 4.3 已知测试覆盖盲点（建议后续补）

| 缺口 | 当前状态 | 建议 |
|---|---|---|
| schema description 文案漂移 | 已锁 tool name / required / no-cwd / property type / additionalProperties / marshal，未锁完整 description 文案 | 如需对外契约冻结，可补 description golden |
| v1 writerFormat Summary mode | 未验证空 body 渲染是否误导模型 | 补 `RenderSkillBlockV1` Summary 集成 |
| `ReadResource` traversal | 已补 `internal/module/skill` symlink / EvalSymlinks 失败防护与 host-direct 跨层断言 | `go test ./internal/module/skill -run 'TestReadResource_(SymlinkEscapeRejected|BrokenSymlinkRejectedBeforeRead)$' -count=1`；`go test ./internal/platform/toolbridge -run '^TestSkillHostTools_CallReadResource_RejectsSymlinkEscape$' -count=1` |
| resource binary asset | 当前 `skill_read_resource` 返回 string，主要覆盖文本资源 | 若业务需要图片/模板/压缩包，补 base64 或 `skill_read_asset` 工具 |
| 真实 Claude CLI 已认证 tool-call E2E | opt-in 测试已存在；未认证环境会 skip | 在有 Claude CLI 认证的环境跑 `TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E` 并记录延迟 / orphan child 观测 |

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
  - 删除前必须先运行 `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh`，并附 production smoke evidence、30-day rollout gate output 与 authenticated Claude CLI E2E evidence；
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
4. **PR-4：selected metadata / redaction 决策**（✅ 2026-04-27 已落地）
   - untrusted selected summary 只允许真实手选 `ManualSkillSelection=true && source=manual`；legacy
     `Source=Unspecified` / trigger / force 不得因 hydration 误授权；catalog redaction 继续不泄露作者 metadata。
5. **PR-5：resume / recovery skill tools**（✅ 2026-04-27 已落地）
   - 已证明 app-server 保留 start-time dynamicTools；`thread/resume` 不携带 dynamicTools 字段；
     resume/recovery 后模型仍能调用 `skill_expand_body`。
6. **PR-6：默认 discovery / rollout observability**（✅ 2026-04-28 observability guard 已落地 / 可评审）
   - PR-1 / PR-2 / PR-3 / PR-4 / PR-5 与 host-direct 基础 observability 已落地；默认开启前仍保持
     `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false`。
   - 已补 production smoke / report / 30-day gate / Phase 3 preflight / evidence bundle collector / default-switch guard / PR-6 verifier。
   - 继续推进的执行项是按 §5.5.5 运行真实 production smoke、累计 30 天 observation，并收集 authenticated Claude CLI E2E evidence；不得把该执行项替换成分支合并、默认开启或删除 override。

### 5.5 PR-6 执行 runbook：默认 discovery / rollout observability

> 目标：把“默认 discovery / observability”从一句 backlog 拆成可派单执行项。PR-6 仍**不**直接打开
> `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 默认值；它只补齐默认切换前必须有的外部可观测、告警、放量和回滚证据。

#### 5.5.1 范围边界

| 类别 | 内容 |
|---|---|
| In scope | exporter / snapshot API、error-rate 告警规则、rollout observation 记录模板、默认 discovery 灰度开关 smoke、catalog redaction 回归保持绿 |
| Out of scope | 删除 `overrideSkillsToSummary`、修改 `SkillMode.Effective()`、默认打开 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE`、改变 claudecli eager Full 基线 |
| 进入条件 | PR-1~PR-5 回归绿；host-direct in-process counters 与 INFO/WARN 日志已存在；真实 Claude CLI E2E 若未认证，只能标 red gate 未关闭 |
| 退出条件 | 外部系统能观察 host skill tool success/error/cwd_missing/approval_required/enrich_failure；有可执行 rollback trigger；30 天观察起点可登记 |

#### 5.5.2 推荐执行顺序

1. ✅ **P25-HIGH-02a：导出当前 counters**（2026-04-27 已落地）
   - 输入：`pkg/skillmetrics.Read()` 中的 `HostToolCall*`、`EnrichFailuresTotal`、artifact approval / MCP child counters。
   - 输出：`internal/platform/metrics/skill.go` 注册 Prometheus `CounterFunc`；`host_tool_calls_total{outcome=ok|cwd_missing|approval_required|error}` 保留 outcome 维度，`enrich_failures_total`、`skill_artifact_approval_miss_total`、`skill_mcp_*` 等同步可 gather。
   - 验收：`TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes` 已证明 Prometheus 读取值与 `pkg/skillmetrics` snapshot 一致。
2. ✅ **P25-HIGH-02b：告警规则与降级触发**（2026-04-27 已落地 rule artifact）
   - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-alerts.yml`。
   - error-rate：`SkillHostToolHighErrorRate` 规则锁定 `host_tool_calls_total{outcome!="ok"} / host_tool_calls_total` > 5% 持续 5min。
   - cwd_missing：`SkillHostToolCWDMissing` 规则锁定 5min 窗口非零即告警，因为它通常表示 agentId/cwd binding 漂移。
   - approval_required：`SkillHostToolApprovalRequiredStuck` 规则锁定 approval_required 增长但 ok 不增长的 10min 窗口，避免 UI approval 链断开后模型反复调用。
   - enrich_failure：`SkillToolEnrichFailures` 规则锁定 `enrich_failures_total` 非零告警，因为它代表 tool call params 协议漂移。
   - 验收：`TestSkillProgressiveDisclosureAlertRulesArtifact` 固定 alert 名称、关键 PromQL 与 rollback runbook 引用。
3. ✅ **P25-HIGH-02c：默认 discovery smoke**（2026-04-27 已落地）
   - `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false`：默认不注册全量 catalog，只保留 selected/name-only path。
   - `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=true`：`SkillCatalogProvider` 渲染 Core / Native / Manual-only / Untrusted 分组；untrusted metadata redaction 回归必须保持绿。
   - 验收：`TestSkillProgressiveDisclosure_DefaultDisabled`、`TestSkillProgressiveDisclosure_EnableFlagRendersCatalog`、`TestSkillCatalogProvider_GroupsNativeTrustedRedacted` 已覆盖。
4. ✅ **P25-HIGH-02d：rollout observation 记录模板**（2026-04-27 已落地）
   - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md`。
   - 记录字段：日期、版本/commit、开关状态、总 calls、ok/error/cwd_missing/approval_required、enrich_failure、人工 smoke 结论、回滚演练结果、rollback trigger 与 decision。
   - 观察窗口：默认策略切换前至少 30 天；若无真实流量，必须标记为“无样本 / no samples”，不能把 0 error 当 99% 成功率。
   - 验收：`TestSkillProgressiveDisclosureRolloutObservationTemplateArtifact` 锁定必填字段、30 天窗口与 no-sample 规则。
5. ✅ **P25-HIGH-02e：promhttp `/metrics` 本地暴露端点**（2026-04-27 已落地）
   - `internal/platform/metrics/http.go` 统一提供 `PrometheusMetricsPath=/metrics` 与 promhttp handler。
   - `internal/ui/wails/http_server.go` 在本地 HTTP asset server 挂载 `/metrics`；默认地址为 `127.0.0.1:4511/metrics`。
   - 验收：`TestMetricsHandlerServesSkillHostToolCounters`、`TestRegisterHTTPHandlersMountsMetricsPath`、`TestHTTPAssetRoutesExposePrometheusMetricsEndpoint` 锁定 handler 输出与 Wails route。
6. ✅ **P25-HIGH-02f：Prometheus scrape / rule-loading config artifact**（2026-04-27 已落地）
   - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-prometheus.yml`。
   - config 同时声明 `rule_files: skill-progressive-disclosure-alerts.yml`、`scrape_configs`、`metrics_path: /metrics`、`127.0.0.1:4511` target 与 Alertmanager 占位。
   - 验收：`TestSkillProgressiveDisclosurePrometheusConfigArtifact` 锁定 scrape target、rule_files、Prometheus Targets / Rules / Alertmanager checklist 与 30-day observation 起点条件。
7. ✅ **P25-HIGH-02g：production apply smoke script**（2026-04-27 已落地）
   - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-smoke.sh`。
   - 默认检查 `http://127.0.0.1:4511/metrics`、Prometheus `api/v1/targets`、Prometheus `api/v1/rules` 与 Alertmanager readiness；生产可通过 `SUPER_DOLPHIN_METRICS_URL`、`PROMETHEUS_URL`、`ALERTMANAGER_URL`、`SKILL_PD_PROMETHEUS_JOB` 覆盖。
   - 验收：`TestSkillProgressiveDisclosureRolloutSmokeScriptArtifact` 锁定 endpoint、target UP、4 条 alert、Alertmanager ready 与 30-day observation 结果附着要求；`bash -n` 校验脚本语法。
8. ✅ **P25-HIGH-02h：daily observation report generator**（2026-04-27 已落地）
   - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh`。
   - 功能：查询 Prometheus `api/v1/query` 生成 copy/paste observation row，默认附带 `artifact_approval_miss` 备注；支持可选执行 `skill-progressive-disclosure-rollout-smoke.sh` 并把结果一并带入输出。
   - 验收：`TestSkillProgressiveDisclosureRolloutReportScriptArtifact` 锁定 query API、关键 metric/token 与 smoke integration 开关；`TestSkillProgressiveDisclosureRolloutReportScriptNoSampleRule` 实跑脚本锁定 no-sample rule、默认 hold decision 与 smoke 输出拼接；`bash -n` 校验脚本语法。
9. ✅ **P25-HIGH-02i：30-day rollout gate verifier**（2026-04-27 已落地）
   - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh`。
   - 功能：解析 rollout observation markdown row，要求默认 30 个真实 sample day、manual / production smoke 为 PASS、至少一次 rollback drill PASS、no rollback trigger drift、non-ok rate 不超过阈值；无样本行不计入 gate。
   - 验收：`TestSkillProgressiveDisclosureRolloutGateScriptArtifact` 锁定关键 env / threshold / gate token；`TestSkillProgressiveDisclosureRolloutGateScriptPassAndNoSampleFail` 实跑脚本锁定 pass path 与 no-sample fail-closed path；`bash -n` 校验脚本语法。
10. ✅ **P25-HIGH-02j：Phase 3 default-policy preflight gate**（2026-04-27 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh`。
    - 功能：在默认策略正式化 / override 删除 PR 前，要求 production smoke evidence、30-day rollout gate output 与 authenticated Claude CLI E2E evidence 同时满足；脚本本身不启用 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE`，也不删除 `overrideSkillsToSummary`。
    - 验收：`TestSkillProgressiveDisclosurePhase3PreflightScriptArtifact` 锁定 evidence env / fail-closed token；`TestSkillProgressiveDisclosurePhase3PreflightScriptPassAndMissingEvidenceFail` 实跑脚本锁定 evidence pass path、缺 Claude E2E evidence fail-closed path 与 zero-traffic evidence fail-closed path；`bash -n` 校验脚本语法。
11. ✅ **P25-HIGH-02k：Phase 3 preflight evidence templates**（2026-04-27 已落地）
    - artifacts：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence.md`、`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence.md`。
    - 功能：把 production smoke / authenticated Claude CLI E2E evidence 格式标准化；preflight 会校验 evidence type、positive `Total host tool calls`、authenticated environment、PASS 与 non-SKIP。
    - 验收：`TestSkillProgressiveDisclosurePhase3EvidenceTemplates` 锁定模板字段与 fail-closed 前置 token。
12. ✅ **P25-HIGH-02l：Phase 3 evidence bundle verifier**（2026-04-27 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh`。
    - 功能：校验同一 evidence bundle 目录中的 `production-smoke-evidence.md`、`claudecli-e2e-evidence.md`、`rollout-observation.md`、`rollout-gate-output.txt` 与 `phase3-preflight-output.txt`，并可生成 `manifest.md`；脚本不启用默认开关、不删除 override。
    - 验收：`TestSkillProgressiveDisclosurePhase3EvidenceBundleScriptArtifact` 锁定 bundle token；`TestSkillProgressiveDisclosurePhase3EvidenceBundleScriptPassAndMissingFileFail` 实跑脚本锁定 pass / manifest path 与缺文件 fail-closed path；`bash -n` 校验脚本语法。
13. ✅ **P25-HIGH-02m：default-switch static guard**（2026-04-27 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-default-switch-guard.sh`。
    - 功能：PR-6 收口前静态确认 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 默认仍为 false、`TestSkillProgressiveDisclosure_DefaultDisabled` 存在、`overrideSkillsToSummary` 及两处 `session_turn.go` caller 未被删除，并确认 Phase 3 preflight / evidence bundle gate 与 evidence collector helper 仍在。
    - 验收：`TestSkillProgressiveDisclosureDefaultSwitchGuardScriptArtifact` 锁定 guard token；`TestSkillProgressiveDisclosureDefaultSwitchGuardPassAndDefaultTrueFail` 实跑脚本锁定当前 repo pass path 与 default=true fail-closed path；`bash -n` 校验脚本语法。
14. ✅ **P25-HIGH-02n：Phase 3 evidence bundle collector**（2026-04-27 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh`。
    - 功能：把 production smoke evidence、authenticated Claude CLI E2E evidence 与 rollout observation 复制成标准 bundle 文件名，先清理旧 `rollout-gate-output.txt` / `phase3-preflight-output.txt` / `evidence-bundle-output.txt` / `manifest.md`，再依次重新生成 gate / preflight / bundle verifier output 与 manifest，避免复用目录时残留旧 green evidence，减少 Phase 3 手工组包错误。
    - 验收：`TestSkillProgressiveDisclosurePhase3EvidenceCollectScriptArtifact` 锁定 collector token；`TestSkillProgressiveDisclosurePhase3EvidenceCollectPassAndMissingEvidenceFail` 实跑脚本锁定 pass / manifest path、缺 evidence fail-closed path、no-sample rollout gate fail-closed 与 stale output cleanup；`bash -n` 校验脚本语法。
15. ✅ **P25-HIGH-02o：one-command PR-6 verification wrapper**（2026-04-27 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh`。
    - 功能：提交前一键运行 PR-6 脚本 `bash -n` / 可执行位检查、default-switch guard、focused regression tests 与 `git diff --check`；支持 `SKILL_PD_PR6_VERIFY_SKIP_GO_TESTS=true` 与 `SKILL_PD_PR6_VERIFY_SKIP_GIT_DIFF_CHECK=true` 仅用于测试 / 本地分段排查。
    - 验收：`TestSkillProgressiveDisclosurePR6VerifyScriptArtifact` 锁定 verifier token；`TestSkillProgressiveDisclosurePR6VerifyScriptSkipGoTestsSmoke` 实跑脚本锁定语法检查、default-switch guard 与 skip 输出；完整 PR-6 收尾可直接运行该 wrapper。
16. ✅ **P25-HIGH-02p：daily observation append helper**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh`。
    - 功能：把 `skill-progressive-disclosure-rollout-report.sh` 生成的 daily row 安全追加到 rollout observation 文件，避免手工复制错列；重复日期、`TODO(...)`、no-sample 却 decision=continue、continue 但 manual/prometheus/rollback 非 PASS 都会 fail closed。
    - 验收：`TestSkillProgressiveDisclosureRolloutAppendScriptArtifact` 锁定 helper token；`TestSkillProgressiveDisclosureRolloutAppendScriptPassDuplicateAndNoSampleFail` 实跑脚本锁定 append pass、重复日期 fail 与 `SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE=true` no-sample fail；PR-6 verifier 会检查脚本可执行与 `bash -n`。
17. ✅ **P25-HIGH-02q：rollout status / next-step helper**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh`。
    - 功能：读取 observation 文件，输出 sample_days / remaining_sample_days / no_sample_days / non_ok_rate / rollback_drill_pass / blockers，并在 `Next phase actions` 中明确下一步是继续收样本、运行 rollout gate，还是收集 Phase 3 evidence bundle。
    - 验收：`TestSkillProgressiveDisclosureRolloutStatusScriptArtifact` 锁定 status token；`TestSkillProgressiveDisclosureRolloutStatusScriptNextActions` 实跑脚本锁定样本不足与样本齐备两种 next-step 输出；PR-6 verifier 会检查脚本可执行与 `bash -n`。
18. ✅ **P25-HIGH-02r：one-command daily rollout helper**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh`。
    - 功能：每日执行入口，按顺序运行 report → append → status，保留 report / append / status 输出到 `SKILL_PD_DAILY_OUTPUT_DIR`，append 失败时保留 report artifact 便于审计。
    - 验收：`TestSkillProgressiveDisclosureRolloutDailyScriptArtifact` 锁定 daily token；`TestSkillProgressiveDisclosureRolloutDailyScriptRunsReportAppendStatus` 实跑 fake report，锁定 append/status 串联、artifact 落盘与重复日期 fail-closed；PR-6 verifier 会检查脚本可执行与 `bash -n`。
19. ✅ **P25-HIGH-02s：production smoke evidence generator**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh`。
    - 功能：从 rollout report artifact 生成 Phase 3 `production-smoke` evidence；要求 positive `Total host tool calls`、production smoke `PASS`、raw smoke output 含 `P25-HIGH-02g rollout smoke passed.`，并要求 operator / metrics / Alertmanager URL 明确填写。
    - 验收：`TestSkillProgressiveDisclosureProductionSmokeEvidenceGenerateScriptArtifact` 锁定 generator token；`TestSkillProgressiveDisclosureProductionSmokeEvidenceGeneratePassAndFailClosed` 实跑 pass path、zero-traffic fail 与 smoke-pass token 缺失 fail；PR-6 verifier 会检查脚本可执行与 `bash -n`。
20. ✅ **P25-HIGH-02t：authenticated Claude CLI E2E evidence generator**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh`。
    - 功能：从真实 authenticated Claude CLI E2E 输出生成 Phase 3 `authenticated-claudecli-e2e` evidence；要求 `SKILL_PD_AUTHENTICATED_ENVIRONMENT=true`、明确 version / operator、原始输出包含 `TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E` 的 `--- PASS:` 与独立 `PASS` 行，且拒绝 skip / unauthenticated / FAIL 标记。
    - 验收：`TestSkillProgressiveDisclosureClaudeCLIE2EEvidenceGenerateScriptArtifact` 锁定 generator token；`TestSkillProgressiveDisclosureClaudeCLIE2EEvidenceGeneratePassAndFailClosed` 实跑 pass path、skip fail 与 unauthenticated fail；PR-6 verifier 会检查脚本可执行与 `bash -n`。
21. ✅ **P25-HIGH-02u：Phase 3 evidence readiness status helper**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-status.sh`。
    - 功能：只读汇总 rollout gate readiness、production-smoke evidence、authenticated Claude CLI E2E evidence 与 Phase 3 collector readiness，输出 `blocker_count` 和下一步动作；支持 `SKILL_PD_PHASE3_STATUS_FAIL_ON_BLOCKERS=true` 在自动化中 fail closed。
    - 验收：`TestSkillProgressiveDisclosurePhase3EvidenceStatusScriptArtifact` 锁定 status token；`TestSkillProgressiveDisclosurePhase3EvidenceStatusReadyAndMissing` 实跑 ready path、缺 Claude evidence fail-on-blockers path 与 next action 输出；PR-6 verifier 会检查脚本可执行与 `bash -n`。
22. ✅ **P25-HIGH-02v：one-command Phase 3 evidence ready collect helper**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-ready-collect.sh`。
    - 功能：先以 fail-closed 模式运行 Phase 3 evidence status，确认 `phase3_collect_ready=true` 与 `blocker_count=0` 后才调用 canonical evidence collector；同时保存 status / collect transcript 到 bundle 目录，防止人工跳过 readiness gate。
    - 验收：`TestSkillProgressiveDisclosurePhase3EvidenceReadyCollectScriptArtifact` 锁定 helper token；`TestSkillProgressiveDisclosurePhase3EvidenceReadyCollectPassAndStatusFail` 实跑 ready collect pass、缺 Claude evidence status fail 与 collector 未运行路径；PR-6 verifier 会检查脚本可执行与 `bash -n`。
23. ✅ **P25-HIGH-02w：Phase 3 evidence handoff report helper**（2026-04-28 已落地）
    - artifact：`docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-handoff-report.sh`。
    - 功能：校验 ready-collect 产出的 canonical bundle、status / collect transcript 与 manifest，生成 `phase3-handoff-report.md`；报告包含 readiness summary、required evidence file SHA256、gate outputs verified 与下一步 owner action，便于 Phase 3 PR / release review 附件审计。
    - 验收：`TestSkillProgressiveDisclosurePhase3HandoffReportScriptArtifact` 锁定 report token；`TestSkillProgressiveDisclosurePhase3HandoffReportPassAndMissingFileFail` 实跑 report pass、缺 bundle 文件 fail-closed 与 SHA256 表输出；PR-6 verifier 会检查脚本可执行与 `bash -n`。

#### 5.5.3 PR-6 必过命令

```bash
go test ./pkg/skillmetrics ./internal/platform/metrics -count=1
go test ./internal/platform/metrics -run 'Test(SkillProgressiveDisclosure(AlertRulesArtifact|RolloutObservationTemplateArtifact|PrometheusConfigArtifact|RolloutSmokeScriptArtifact|RolloutReportScriptArtifact|RolloutReportScriptNoSampleRule|RolloutAppendScriptArtifact|RolloutAppendScriptPassDuplicateAndNoSampleFail|RolloutStatusScriptArtifact|RolloutStatusScriptNextActions|RolloutDailyScriptArtifact|RolloutDailyScriptRunsReportAppendStatus|ProductionSmokeEvidenceGenerateScriptArtifact|ProductionSmokeEvidenceGeneratePassAndFailClosed|ClaudeCLIE2EEvidenceGenerateScriptArtifact|ClaudeCLIE2EEvidenceGeneratePassAndFailClosed|Phase3EvidenceStatusScriptArtifact|Phase3EvidenceStatusReadyAndMissing|Phase3EvidenceReadyCollectScriptArtifact|Phase3EvidenceReadyCollectPassAndStatusFail|Phase3HandoffReportScriptArtifact|Phase3HandoffReportPassAndMissingFileFail|RolloutGateScriptArtifact|RolloutGateScriptPassAndNoSampleFail|Phase3PreflightScriptArtifact|Phase3PreflightScriptPassAndMissingEvidenceFail|Phase3EvidenceTemplates|Phase3EvidenceBundleScriptArtifact|Phase3EvidenceBundleScriptPassAndMissingFileFail|Phase3EvidenceCollectScriptArtifact|Phase3EvidenceCollectPassAndMissingEvidenceFail|PR6VerifyScriptArtifact|PR6VerifyScriptSkipGoTestsSmoke|DefaultSwitchGuardScriptArtifact|DefaultSwitchGuardPassAndDefaultTrueFail)|MetricsHandlerServesSkillHostToolCounters|RegisterHTTPHandlersMountsMetricsPath)' -count=1
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-smoke.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-status.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-ready-collect.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-handoff-report.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh
bash -n docs/plans/迁移/p25skill优化/skill-progressive-disclosure-default-switch-guard.sh
go test ./internal/platform/toolbridge -run 'Test.*(Observability|Metrics|ListToolsForCodex|CallHostTool)' -count=1
go test ./internal/module/prompt -run 'Test(SkillProgressiveDisclosure|SkillCatalogProvider)' -count=1
go test ./internal/module/turn -run TestApplyHydration_UntrustedSummary -count=1
go test ./internal/ui/wails -run TestHTTPAssetRoutesExposePrometheusMetricsEndpoint -count=1
git diff --check
```

#### 5.5.4 不得合入的 red flags

- 只新增日志、不新增可被 Prometheus default gatherer 或等价外部系统读取的指标 / snapshot。
- 只把 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` 默认打开，却没有 30 天观察起点和 rollback trigger。
- exporter 只统计 total，不保留 `outcome` 维度，导致 cwd_missing / approval_required / error 无法区分。
- 把真实 Claude CLI opt-in skip 当作已认证 E2E 通过。
- PR-6 混入 Phase 3 default policy 删除 override；这会把观测 PR 变成行为切换 PR，回滚面过大。

#### 5.5.5 PR-6 后真实 rollout evidence 落地清单（执行文档）

> 本节是 PR-6 代码 / artifact 落地后的执行清单。它只收集真实 evidence，**不**自动合并分支、
> **不**默认开启 `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE`，也**不**删除 `overrideSkillsToSummary`。
> 任何 `git merge` / `git rebase` / `gh pr merge` / main 同步动作都必须由 owner 明确确认后再做。

1. **应用观测 artifact 到目标环境**
   - 部署包含 `/metrics` route 的构建，确认本地 HTTP asset server 暴露 `127.0.0.1:4511/metrics` 或等价生产 URL。
   - 将 `skill-progressive-disclosure-alerts.yml` 与 `skill-progressive-disclosure-prometheus.yml` 的 `rule_files` / `scrape_configs` 合入目标 Prometheus 配置。
   - Prometheus reload 后，确认 target job 名称为 `super-dolphin-skill-progressive-disclosure`（或通过 `SKILL_PD_PROMETHEUS_JOB` 覆盖）。
2. **运行 production smoke，并保存原始输出**

   ```bash
   SUPER_DOLPHIN_METRICS_URL="http://127.0.0.1:4511/metrics" \
   PROMETHEUS_URL="http://127.0.0.1:9090" \
   ALERTMANAGER_URL="http://127.0.0.1:9093" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-smoke.sh \
     | tee "/tmp/p25-skill-rollout-smoke-$(date +%F).txt"
   ```

   - 只有脚本输出 `P25-HIGH-02g rollout smoke passed.` 才能把 production smoke 写为 `PASS`。
   - 如果 `/metrics` 或 Prometheus target 未接入，记录 `FAIL` / `SKIP(not applied)`，decision 必须 `hold`。
3. **生成并追加 daily observation row**

   ```bash
   SKILL_PD_RUN_ROLLOUT_SMOKE=true \
   SKILL_PD_MANUAL_SMOKE_RESULT="PASS" \
   SKILL_PD_ROLLBACK_DRILL_RESULT="PASS" \
   SKILL_PD_DECISION="continue" \
   PROMETHEUS_URL="http://127.0.0.1:9090" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh \
     | tee "/tmp/p25-skill-rollout-report-$(date +%F).md"

   SKILL_PD_ROLLOUT_REPORT_FILE="/tmp/p25-skill-rollout-report-$(date +%F).md" \
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh
   ```

   或使用 one-command daily helper 串起 report → append → status：

   ```bash
   SKILL_PD_RUN_ROLLOUT_SMOKE=true \
   SKILL_PD_MANUAL_SMOKE_RESULT="PASS" \
   SKILL_PD_ROLLBACK_DRILL_RESULT="PASS" \
   SKILL_PD_DECISION="continue" \
   PROMETHEUS_URL="http://127.0.0.1:9090" \
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh
   ```

   - `SKILL_PD_MANUAL_SMOKE_RESULT=PASS` 与 `SKILL_PD_ROLLBACK_DRILL_RESULT=PASS` 只能在人工 smoke / rollback drill 实际完成后填写。
   - `SKILL_PD_RUN_ROLLOUT_SMOKE=true` 时不要手填 `SKILL_PD_PROMETHEUS_SMOKE_RESULT=PASS`；让脚本按 production smoke 真实退出码写 PASS / FAIL。
   - `skill-progressive-disclosure-rollout-append.sh` 会拒绝重复日期、`TODO(...)`、no-sample 却 decision=continue、continue 但 manual/prometheus/rollback 非 PASS 的行。
   - `Total host tool calls=0` 时必须保留 `SKIP(no samples)` / `hold`，不能把 no-sample 当成功率；若当天必须只收真实样本，可设置 `SKILL_PD_APPEND_REQUIRE_REAL_SAMPLE=true`。
   - 追加后的 observation 文件可进入 status / 30-day gate；原始 report / smoke 输出仍应作为 evidence 附件保存。
4. **输出 rollout status 与下阶段动作**

   ```bash
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   SKILL_PD_REQUIRED_SAMPLE_DAYS=30 \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh
   ```

   - status 输出必须包含 `sample_days`、`remaining_sample_days`、`blocker_count` 与 `Next phase actions`。
   - 若样本不足，下一阶段是继续 production smoke / report / append；若样本满足，下一阶段是 rollout gate、production evidence、authenticated Claude CLI E2E evidence 与 evidence bundle collect。
   - status helper 只读 observation，不合并分支、不默认开启、不删除 override。
5. **30 天样本满足后运行 rollout gate**

   ```bash
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   SKILL_PD_REQUIRED_SAMPLE_DAYS=30 \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh
   ```

   - gate 输出必须包含 `P25-HIGH-02i rollout gate passed`、`sample_days=30`（或更多）、`rollback_drill_pass=true`。
   - no-sample row 不计入 sample day；cwd_missing / approval_required / enrich_failure 没有 accepted incident / fix note 时 fail closed。
6. **生成 production smoke evidence（默认策略 PR 前置，不在 PR-6 内完成）**

   ```bash
   SKILL_PD_ROLLOUT_REPORT_FILE="/path/to/rollout-report-YYYY-MM-DD.md" \
   SKILL_PD_PRODUCTION_SMOKE_EVIDENCE_OUT="/path/to/production-smoke-evidence.md" \
   SKILL_PD_OPERATOR="release-owner" \
   SUPER_DOLPHIN_METRICS_URL="https://example.invalid/metrics" \
   ALERTMANAGER_URL="https://alertmanager.example.invalid" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh
   ```

   - generator 会拒绝 `Total host tool calls=0`、production smoke 非 PASS、或 raw smoke output 缺少 `P25-HIGH-02g rollout smoke passed.` 的 report。
7. **生成 authenticated Claude CLI E2E evidence（默认策略 PR 前置，不在 PR-6 内完成）**

   ```bash
   go test ./cmd/agent-terminal -run '^TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E$' -count=1 \
     | tee "/tmp/p25-skill-claudecli-e2e-$(date +%F).txt"

   SKILL_PD_CLAUDECLI_E2E_OUTPUT_FILE="/tmp/p25-skill-claudecli-e2e-$(date +%F).txt" \
   SKILL_PD_CLAUDECLI_E2E_EVIDENCE_OUT="/path/to/claudecli-e2e-evidence.md" \
   SKILL_PD_VERSION_COMMIT="$(git rev-parse --short HEAD)" \
   SKILL_PD_OPERATOR="release-owner" \
   SKILL_PD_AUTHENTICATED_ENVIRONMENT=true \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh
   ```

   - generator 会拒绝未显式认证、原始输出包含 skip / unauthenticated / FAIL 标记、缺少 `--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E` 或缺少独立 `PASS` 行的 output；不能把 opt-in skip 当已认证通过。
8. **输出 Phase 3 evidence readiness status（默认策略 PR 前置，不在 PR-6 内完成）**

   ```bash
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="/path/to/production-smoke-evidence.md" \
   SKILL_PD_CLAUDECLI_E2E_EVIDENCE="/path/to/claudecli-e2e-evidence.md" \
   SKILL_PD_REQUIRED_SAMPLE_DAYS=30 \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-status.sh
   ```

   - status helper 是只读检查；`phase3_collect_ready=true` 且 `blocker_count=0` 后再进入 collector。自动化里可设置 `SKILL_PD_PHASE3_STATUS_FAIL_ON_BLOCKERS=true` fail closed。
9. **一条命令执行 status → collect（默认策略 PR 前置，不在 PR-6 内完成）**

   ```bash
   SKILL_PD_BUNDLE_OUT_DIR="/tmp/p25-skill-phase3-evidence-$(date +%F)" \
   SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="/path/to/production-smoke-evidence.md" \
   SKILL_PD_CLAUDECLI_E2E_EVIDENCE="/path/to/claudecli-e2e-evidence.md" \
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   SKILL_PD_REQUIRED_SAMPLE_DAYS=30 \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-ready-collect.sh
   ```

   - ready-collect helper 会先 fail-closed 运行 status，只有 `phase3_collect_ready=true` / `blocker_count=0` 才调用 collector，并在 bundle 目录保存 `phase3-evidence-status-output.txt` 与 `phase3-evidence-collect-output.txt`。
10. **收集 Phase 3 evidence bundle（默认策略 PR 前置，不在 PR-6 内完成）**

   ```bash
   SKILL_PD_BUNDLE_OUT_DIR="/tmp/p25-skill-phase3-evidence-$(date +%F)" \
   SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="/path/to/production-smoke-evidence.md" \
   SKILL_PD_CLAUDECLI_E2E_EVIDENCE="/path/to/claudecli-e2e-evidence.md" \
   SKILL_PD_OBSERVATION_FILE="docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh
   ```

   - production evidence 必须声明 `Evidence type: production-smoke`、`P25-HIGH-02g smoke passed.`、`real traffic is non-zero`，且 `Total host tool calls` 为正数。
   - Claude evidence 必须声明 `Evidence type: authenticated-claudecli-e2e`、`Authenticated environment: true`，包含 `TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E` 与 `PASS`，且不得包含 `SKIP`。
   - collector 通过只代表 Phase 3 前置 evidence 齐备；默认开启 / override 删除仍必须另起 Phase 3 PR。
11. **生成 Phase 3 handoff report（默认策略 PR review 附件）**

   ```bash
   SKILL_PD_EVIDENCE_BUNDLE_DIR="/tmp/p25-skill-phase3-evidence-$(date +%F)" \
   docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-handoff-report.sh
   ```

   - handoff report 会校验 ready-collect transcripts、bundle manifest、gate / preflight / collector 输出，并记录 required evidence 文件 SHA256；报告只用于 review / release handoff，不代表默认策略已上线。

### 5.6 Planned test names / 验收矩阵（补充）

> 本节原是后续实现 PR 的 **planned test names**。截至 2026-04-28，PR-1 / PR-2 / PR-3 / PR-4 / PR-5 / PR-6
> 已由 planned 提升为实际回归测试；后续 Phase 3 默认策略 PR 与 authenticated Claude CLI E2E 仍是派单测试名建议。
> 若实现时包名 / helper 名称调整，可改测试名，但不得降低验收语义：approval 必须真实闭环，DynamicTools
> 必须可降级但不丢 peer，模型视角 E2E 必须证明 tool result 回到模型路径，resume/recovery 后 skill tools 必须仍可调用。

| PR | 建议测试入口 | Planned test names | 必须锁住的验收语义 |
|---|---|---|---|
| PR-1 Approval 主闭环 | `go test ./internal/module/skill -run 'Test(ExpandBody|ReadResource|ArtifactApproval)' -count=1` | `TestExpandBody_UntrustedProjectSkill_ApprovalRequesterApproved`、`TestExpandBody_UntrustedProjectSkill_ApprovalRequesterDenied`、`TestExpandBody_UntrustedProjectSkill_ApprovalRequesterTimeout`、`TestExpandBody_NoApprovalRequester_ReturnsApprovalRequiredFallback`、`TestReadResource_UntrustedProjectSkill_ApprovalRequesterApproved`、`TestArtifactApprovalCache_BodyDoesNotApproveResource`、`TestArtifactApprovalCache_ResourcePathIsolation`、`TestArtifactApprovalCache_RepoFingerprintIsolation` | `ExpandBody` / `ReadResource` cache miss 时由 `skill.Service` 调 `ApprovalRequester`；approved 后写 `ApproveArtifact`；denied / timeout fail closed；body、anchor、resource path、repo fingerprint 互相隔离 |
| PR-1 host-direct metadata / structured result | `go test ./internal/platform/toolbridge -run 'Test(CallHostTool|RouteToolCall)' -count=1` | `TestCallHostTool_PassesApprovalMetadata`、`TestCallHostTool_ApprovalDeniedReturnsStructuredResult`、`TestCallHostTool_ApprovalRequiredFallbackReturnsStructuredResult`、`TestRouteToolCall_HostToolBypassesPeer_UsesResolvedCWD` | `agentId/threadId/callId/cwd/toolName` 进入 approval payload；no-requester fallback 是结构化 `kind=approval_required`，不是普通 `err.Error()` 文本；host-direct 命中不访问 peer |
| PR-2 DynamicTools partial degradation | `go test ./internal/platform/toolbridge -run TestListToolsForCodex -count=1` | `TestListToolsForCodex_HostToolsSurviveOrchFailure_LSPReady`、`TestListToolsForCodex_HostToolsSurviveLSPFailure_OrchReady`、`TestListToolsForCodex_HostOnlyWhenBothPeersFail`、`TestListToolsForCodex_ReturnsErrorWhenNoHostAndPeersFail`、`TestListToolsForCodex_DedupKeepsHostBeforePeer`、`TestListToolsForCodex_PeerWaitIsConcurrent` | host tools 先收集；orch/lsp 并发等待且最长一个 `peerReadyTimeout`；单 peer 成功必须被保留；双 peer 失败但 host 存在仍返回 host；只有无 host 且 peer 全失败才 error |
| PR-3 模型视角 E2E | `go test ./internal/provider/codexapp -run TestDynamicSkillTools_ModelE2E -count=1` | `TestDynamicSkillTools_ModelE2E_ExpandBodyResultReturnsToModel`、`TestDynamicSkillTools_ModelE2E_ApprovalApprovedContinuesFinalAnswer`、`TestDynamicSkillTools_ModelE2E_ApprovalDeniedReturnsStructuredToolResult` | fake / controlled app-server 证明 `thread/start dynamicTools -> model dynamic_tool_call(skill_expand_body) -> approval/cache/read -> tool result -> final answer`；只断言 schema 存在不得通过 |
| PR-4 selected metadata / redaction | `go test ./internal/module/turn -run TestApplyHydration_UntrustedSummary -count=1`；`go test ./internal/module/prompt -run 'Test(SkillCatalogProvider_Untrusted|GroupSkillsForManifest_Untrusted|IsUntrustedScope)' -count=1` | `TestApplyHydration_UntrustedSummary_RedactedWhenSourceUnspecified`、`TestApplyHydration_UntrustedSummary_AllowsOnlyRealManualSelection`、`TestApplyHydration_UntrustedSummary_RedactedForTriggerAndForce`、`TestSkillCatalogProvider_UntrustedRenderedAsRedactedPlaceholder`、`TestSkillCatalogProvider_UntrustedDisableModelInvocation_NoLeak`、`TestGroupSkillsForManifest_UntrustedGoesToRedactedNotManualOnly`、`TestIsUntrustedScope` | 若允许 untrusted selected summary，只能限真实 `ManualSkillSelection=true && source=manual`；legacy `Source=Unspecified` / trigger / force 不得当授权；catalog redaction 继续成立 |
| PR-5 resume / recovery skill tools | `go test ./internal/provider/codexapp -run 'Test(Resume|Recovery).*DynamicSkillTools' -count=1` | `TestResumeSession_DynamicSkillToolsStillCallable`、`TestRecoveryResume_DynamicSkillToolsStillCallable`、`TestThreadResume_AppServerRetainsStartDynamicTools`、`TestThreadResume_DynamicToolsWireCompatibilityIsExplicit` | 先证明 app-server 是否保留 start-time tools；若需要扩 `thread/resume`，必须证明 app-server 接受 / 显式处理该字段；验收以 resume/recovery 后模型仍能调用 skill tools 为准 |
| PR-6 discovery / observability | `go test ./pkg/skillmetrics ./internal/platform/metrics -count=1`；`go test ./internal/module/prompt -run 'Test(SkillProgressiveDisclosure|SkillCatalogProvider)' -count=1`；`go test ./internal/platform/toolbridge -run 'Test.*Observability|Test.*Metrics' -count=1` | `TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes`、`TestSkillProgressiveDisclosureAlertRulesArtifact`、`TestSkillProgressiveDisclosureRolloutObservationTemplateArtifact`、`TestSkillProgressiveDisclosurePrometheusConfigArtifact`、`TestSkillProgressiveDisclosureRolloutSmokeScriptArtifact`、`TestSkillProgressiveDisclosureRolloutReportScriptArtifact`、`TestSkillProgressiveDisclosureRolloutReportScriptNoSampleRule`、`TestSkillProgressiveDisclosureRolloutAppendScriptArtifact`、`TestSkillProgressiveDisclosureRolloutAppendScriptPassDuplicateAndNoSampleFail`、`TestSkillProgressiveDisclosureRolloutStatusScriptArtifact`、`TestSkillProgressiveDisclosureRolloutStatusScriptNextActions`、`TestSkillProgressiveDisclosureRolloutDailyScriptArtifact`、`TestSkillProgressiveDisclosureRolloutDailyScriptRunsReportAppendStatus`、`TestSkillProgressiveDisclosureClaudeCLIE2EEvidenceGenerateScriptArtifact`、`TestSkillProgressiveDisclosureClaudeCLIE2EEvidenceGeneratePassAndFailClosed`、`TestSkillProgressiveDisclosurePhase3EvidenceStatusScriptArtifact`、`TestSkillProgressiveDisclosurePhase3EvidenceStatusReadyAndMissing`、`TestSkillProgressiveDisclosurePhase3EvidenceReadyCollectScriptArtifact`、`TestSkillProgressiveDisclosurePhase3EvidenceReadyCollectPassAndStatusFail`、`TestSkillProgressiveDisclosurePhase3HandoffReportScriptArtifact`、`TestSkillProgressiveDisclosurePhase3HandoffReportPassAndMissingFileFail`、`TestSkillProgressiveDisclosureRolloutGateScriptArtifact`、`TestSkillProgressiveDisclosureRolloutGateScriptPassAndNoSampleFail`、`TestSkillProgressiveDisclosurePhase3PreflightScriptArtifact`、`TestSkillProgressiveDisclosurePhase3PreflightScriptPassAndMissingEvidenceFail`、`TestSkillProgressiveDisclosurePhase3EvidenceTemplates`、`TestSkillProgressiveDisclosurePhase3EvidenceBundleScriptArtifact`、`TestSkillProgressiveDisclosurePhase3EvidenceBundleScriptPassAndMissingFileFail`、`TestSkillProgressiveDisclosurePhase3EvidenceCollectScriptArtifact`、`TestSkillProgressiveDisclosurePhase3EvidenceCollectPassAndMissingEvidenceFail`、`TestSkillProgressiveDisclosurePR6VerifyScriptArtifact`、`TestSkillProgressiveDisclosurePR6VerifyScriptSkipGoTestsSmoke`、`TestSkillProgressiveDisclosureDefaultSwitchGuardScriptArtifact`、`TestSkillProgressiveDisclosureDefaultSwitchGuardPassAndDefaultTrueFail`、`TestSkillProgressiveDisclosure_DefaultDisabled`、`TestSkillProgressiveDisclosure_EnableFlagRendersCatalog`、`TestSkillCatalogProvider_GroupsNativeTrustedRedacted`、`TestListToolsForCodex_LogsDegradedPeer`、`TestHostSkillToolCall_EmitsApprovalAndCacheMetrics` | 默认仍为 disabled；enable=true 时 catalog 分组与 untrusted redaction 正确；放量前能通过 Prometheus default gatherer 或等价出口观察 degraded peer、host tool success/error、approval requested/approved/denied/timeout、artifact cache hit/miss，并有 Prometheus scrape/rule-loading config artifact、production smoke script、daily observation report script、daily observation append helper、rollout status / next-step helper、one-command daily rollout helper、production smoke evidence generator、authenticated Claude CLI E2E evidence generator、Phase 3 evidence readiness status helper、one-command Phase 3 evidence ready collect helper、Phase 3 handoff report helper、30-day rollout gate verifier、Phase 3 preflight gate、evidence templates、evidence bundle verifier、evidence bundle collector、one-command PR-6 verification wrapper、default-switch static guard 与 30 天 observation 模板锁住 no-sample 规则 |
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

### 6.2 Phase 2 分离执行（已落地最小链路，仍保留真实 E2E gate）

reviewer 3 验证：claudecli 子进程的 B1/B2/B3 是真三岔路，选错回滚成本高。
因此 Phase 2 被拆成独立实现：B3 same-binary stdio MCP child 已落地并有 smoke / lifecycle / latency 单测；真实 Claude CLI 已认证 tool-call E2E、托管 child orphan 观测与放量指标仍作为 Phase 3 前 red gate。

### 6.3 Phase 1.5 引入（codexapp Unspecified override）

不引入则 Phase 1 只完成工具链能力，缺少让 `Mode=Unspecified` 请求走 Summary 的入口。
该 override **只转换 Unspecified**；显式 Full / Summary / None 一律尊重。随后代码层已把普通 selected skill / name-only hydrate / cron 路径收敛为保留 Unspecified marker，因此 codexapp 普通路径现在会走 Summary。
但它仍是 codexapp-only 临时兼容层，不是“整个 harness 默认 progressive-disclosure 已业务交付”的证明：claudecli 默认仍通过 `SkillMode.Effective()` 保持 Full/eager；真实 Claude CLI 已认证 E2E、外部可观测 / 告警 / 30 天 rollout 与 Phase 3 policy 仍未闭环。代码注释明确标记“Phase 1.5 临时”，Phase 3 删除或替换为正式 provider default policy。

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

PR-6 observability 后续追加 artifact / guard / endpoint：
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-alerts.yml`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-prometheus.yml`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-smoke.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence.md`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence.md`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-status.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-ready-collect.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-handoff-report.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh`
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-default-switch-guard.sh`
- `internal/platform/metrics/http.go`
- `internal/platform/metrics/http_test.go`
- `internal/platform/metrics/skill_alert_rules_test.go`
- `internal/platform/metrics/skill_rollout_observation_test.go`
- `internal/platform/metrics/skill_prometheus_config_test.go`
- `internal/platform/metrics/skill_rollout_smoke_test.go`
- `internal/platform/metrics/skill_rollout_report_test.go`
- `internal/platform/metrics/skill_rollout_append_test.go`
- `internal/platform/metrics/skill_rollout_status_test.go`
- `internal/platform/metrics/skill_rollout_daily_test.go`
- `internal/platform/metrics/skill_rollout_gate_test.go`
- `internal/platform/metrics/skill_phase3_preflight_test.go`
- `internal/platform/metrics/skill_production_smoke_evidence_generate_test.go`
- `internal/platform/metrics/skill_claudecli_e2e_evidence_generate_test.go`
- `internal/platform/metrics/skill_phase3_evidence_bundle_test.go`
- `internal/platform/metrics/skill_phase3_evidence_collect_test.go`
- `internal/platform/metrics/skill_phase3_evidence_status_test.go`
- `internal/platform/metrics/skill_phase3_evidence_ready_collect_test.go`
- `internal/platform/metrics/skill_phase3_handoff_report_test.go`
- `internal/platform/metrics/skill_pr6_verify_test.go`
- `internal/platform/metrics/skill_default_switch_guard_test.go`
- `internal/ui/wails/http_server.go`
- `internal/ui/wails/http_server_test.go`

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
> **最后更新**：2026-04-27 / Phase 2 same-binary MCP child、selected metadata redaction、resume/recovery 与 host-direct observability 复核；codexapp selected/name-only Summary、project approval 闭环、模型视角 E2E、partial degradation、resume/recovery、基础 observability 已关闭；全量 discovery、真实 Claude CLI 已认证 E2E、Phase 3 policy 与 rollout 导出/告警仍待闭环。

### 9.2 业务能力 Definition of Done

当前文档若仅作为“交接草案 / 风险清单”交付，已满足：

- [x] 不再声称整个 harness 默认 progressive-disclosure 已生产生效。
- [x] 明确 codexapp 普通 name-only / selected skill 路径已有代码级 Summary 验证。
- [x] 明确 Phase 1.5 只覆盖 codexapp `Mode=Unspecified` 临时路径，不改变显式 Full / Summary / None。
- [x] 明确 claudecli 仍通过 `SkillMode.Effective()` 保持 Full/eager，Phase 2 same-binary MCP child 只是工具链最小链路，不等于默认策略已切换。
- [x] 历史测试数量口径已说明；后续实际测试集合已扩展到 PR-1/2/3、Phase 2 MCP child 与 observability 回归。
- [x] 已知测试盲点已重新拆分：模型视角 E2E / approval / partial degradation / resume-recovery / observability 基础已关闭；真实 Claude CLI 已认证 E2E、default policy 与 rollout 导出/告警仍保留。

若要升级为**业务能力 PR-ready**，当前状态拆成“已关闭缺口”和“仍保留 red gates”：

已关闭 / 已有回归覆盖：

- [x] **BUSINESS CLOSED**：模型视角 E2E 已落地。fake / controlled app-server 已证明真实 DynamicTools 包含 skill tools、模型发 `skill_expand_body`、approval/cache/read 结果回到模型并继续产出 final answer；不再只是断言 schema 存在。
- [x] **BUSINESS CLOSED**：project-scope skill 首次展开 approval 主闭环已落地。`skill.Service` cache miss 会调 `ApprovalRequester`；approved 后写 `ApproveArtifact`；denied / timeout fail closed；host-direct metadata 会进入 approval payload；no-requester fallback 返回结构化 `kind="approval_required"`。
- [x] **BUSINESS CLOSED**：DynamicTools partial degradation 已落地。host skill tools 先收集，orch/lsp peer 并发等待；单 peer 成功保留、双 peer 失败但 host 存在仍返回 host；同名 peer 被 host shadow 时有 WARN。
- [x] **BUSINESS CLOSED**：selected/name-only Summary 与 catalog redaction 策略已收敛。untrusted selected summary 只限真实手选；legacy unspecified / trigger / force 不授权；catalog 对 unknown/invalid trust 继续 redacted。
- [x] **BUSINESS CLOSED**：resume / recovery 后 skill tools 可用性已证明。app-server 保留 start-time dynamicTools，`thread/resume` 不携带 dynamicTools 字段；resume/recovery 后模型仍能调用 `skill_expand_body`。
- [x] **BUSINESS CLOSED**：host-direct 基础观测已落地。已有 `host_tool_calls_total{outcome}` 对应 Go counters、`enrich_failures_total` counter、host-direct INFO 日志、cwd_missing WARN、approval/error structured result、Prometheus collector、local `/metrics` endpoint、alert rules artifact、scrape/rule-loading config artifact、production smoke script、daily observation report generator、30-day rollout gate verifier、Phase 3 preflight gate、evidence templates、evidence bundle verifier、evidence bundle collector、one-command PR-6 verification wrapper 与 default-switch static guard。仍未完成生产 smoke 结果与 30 天放量观察。

仍保留 red gates / 决策项：

- [ ] **BUSINESS BLOCKED**：全量 skill discovery 默认未启用；`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false` 时模型只能看到 selected/name-only skill，不会默认获得完整 catalog。默认开启的 discovery smoke 已补，仍需 observability / rollout gates。
- [ ] **BUSINESS BLOCKED**：显式 Full 仍 eager 注入完整 body；这是兼容设计，但产品/迁移策略需确认是否允许长期存在。
- [ ] **VALIDATION REMAINS**：claudecli Phase 2 same-binary stdio MCP child 最小实现已落地，两个 provider 的模型可见 skill tool 语义已有代码级 parity；内存 stdio、真实 agent-terminal 二进制 `Content-Length initialize -> tools/list -> tools/call -> EOF`、Claude-like parent stdin EOF / context cancel / no-orphan lifecycle、真实二进制 initialize / tools/list / tools/call / stdin EOF latency budget 已有单测 smoke。真实 Claude CLI `--mcp-config` E2E 已有 opt-in 测试，未认证环境会跳过；业务 PR-ready 仍必须补已认证环境下的真实 Claude CLI tool-call E2E 与放量观测。
- [ ] **ROLL-OUT BLOCKED**：Prometheus collector 声明、local promhttp `/metrics` 暴露端点、alert rules artifact、scrape/rule-loading config artifact、production smoke script、daily observation report generator、30-day rollout gate verifier、Phase 3 preflight gate、evidence templates、evidence bundle verifier、evidence bundle collector、one-command PR-6 verification wrapper、default-switch static guard 与 rollout observation 模板已落地，但生产 smoke 结果尚未附到 observation、30 天真实 observation 未完成。Phase 3 默认策略前仍需 30 天成功率观察。
- [ ] **BUSINESS BLOCKED**：Phase 3 provider default policy / override 删除已有 fail-closed preflight artifact、evidence 模板、bundle verifier、bundle collector、PR-6 verification wrapper 与 default-switch static guard，但还没有真实 production smoke evidence、30-day rollout gate output、authenticated Claude CLI E2E evidence 与 phase3 evidence bundle，因此仍不得正式化默认策略或删除 override。

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
其中 §10.1 / §10.2 / §10.3 已闭包修复，其余大多仍是当前 PR 范围外风险。

### 10.1 agentId trust 姿态反向（HIGH，安全，已修）

- 位置：`internal/provider/codexapp/session_enrich.go:14-39`
- 原风险：enrich 曾在 `msg.Params` 已有非空 `agentId` / `agent_id` 时**保留**外部值；未来 codex 协议透传 / 中间件改写 / fixture 复用都可能让模型间接控制 agentId，导致下游 `resolveAgentCWD(req.AgentID)` 解析到**别的 agent 的 cwd**，跨 session 越权读 SKILL.md / 资源。
- 本轮修复：`enrichToolCallParams` 现在 always overwrite canonical `agentId = s.agentID`，并删除 `agent_id` alias；测试 fixture 改为断言外部 agentId 被覆盖。
- 验收：`TestEnrichToolCallParams_OverridesExisting` 锁定恶意 / 旧 fixture agentId 不能胜过 session agentID。
- Release note：当前 worktree 已包含修复；若后续拆 PR 导致修复未随当前 PR 合入，不得默认切换 Summary。
- 验收：`go test ./internal/provider/codexapp -run Enrich -count=1` 通过；review 确认 codexapp `onInboundMessage` enriched path 覆盖 canonical `agentId` 并 strip `agent_id` 后再进入 toolHandler。注意：legacy / 非 codexapp-enriched 调用仍可能被 `decodeToolCallRequest` alias 兼容读取 `agent_id`，不得把本 gate 扩大解释为全仓 alias 移除。

### 10.2 ReadResource 路径 traversal 防护补强（MED，安全，已修）

- 位置：`internal/module/skill/skills_expand.go` 的 `resolveResourceTarget`，以及 host-direct `SkillHostTools.CallHostTool` → `skill.Service.ReadResource` 跨层路径。
- 原风险：`EvalSymlinks` 失败时回退到字面路径，`os.ReadFile` 仍可能跟随符号链接读出 skillDir 之外的文件；trust=user/signed skill 不需审批，攻击面来自被植入恶意 symlink 的 skill 包。
- 本轮修复：`resolveResourceTarget` 现在对 skillDir 与 resource target 的 `EvalSymlinks` 失败直接报错，不再 fallback 到字面路径；成功解析后继续用 `ContainsPath(skillDir, target)` 拦截逃逸。
- 验收：`TestReadResource_SymlinkEscapeRejected`、`TestReadResource_BrokenSymlinkRejectedBeforeRead`、`TestSkillHostTools_CallReadResource_RejectsSymlinkEscape` 锁定 service 层与 host-direct 跨层路径。

### 10.3 host-direct result 路径脱敏与结构化错误（MED，已修）

- 位置：`internal/platform/toolbridge/handler_host_tools.go` 的 `callHostTool` / `hostToolErrorResult`。
- 原风险：成功结果直接把 `ExpandBodyResult` / `ReadResourceResult` 整体 `json.Marshal` 塞进 `inputText`，其中 `Path` / `SkillDir` 可能泄露宿主绝对路径；审批失败若退化成普通 `err.Error()` 文本，模型侧也难以识别 `approval_required`。
- 本轮修复：成功结果在序列化前经 `sanitizeHostToolResult` 脱敏，`ExpandBodyResult.Path` 与 `ReadResourceResult.SkillDir` 均转为 cwd-relative；若不在 cwd 下则只保留 basename。审批 required / denied 继续返回结构化 envelope（`kind="approval_required"` / `kind="approval_denied"`）。
- 验收：`TestCallHostTool_SanitizesSuccessfulSkillPaths`、`TestCallHostTool_SanitizesSuccessfulResourceSkillDir`、`TestCallHostTool_ApprovalRequiredFallbackReturnsStructuredResult`、`TestCallHostTool_ApprovalDeniedReturnsStructuredResult`。

### 10.4 dedup 静默吞 peer / host 工具命名前缀（MED）

详见 §6.7。落地选项二选一即可。

### 10.5 基础可观测性已补（HIGH，运维：真实 evidence 待跑）

详见 §3.4。Phase 1 原先“生产静默坏掉无信号”的问题已收敛为：

- 已补：`host_tool_calls_total{outcome}` 对应 Go counters + Prometheus `CounterFunc` collector、`enrich_failures_total` collector、local `/metrics` endpoint、scrape/rule-loading config artifact、production smoke script、daily observation report generator、30-day rollout gate verifier、Phase 3 preflight gate、evidence templates、evidence bundle verifier、evidence bundle collector、one-command PR-6 verification wrapper、default-switch static guard、host-direct INFO 日志、cwd_missing WARN、peer shadow WARN。
- 后续：Phase 3 正式化 Summary default policy / 删除 override 前，仍需在生产运行 smoke 并把结果附到 observation 行，再基于已落地的 observation 模板 / report script / append script / status script / daily script / production smoke evidence generator / gate script / preflight script / evidence templates / evidence bundle script / evidence collect script 完成 30 天真实 rollout observation 与 authenticated Claude CLI E2E evidence，才能把 99% 成功率从人工判断升级为可执行 gate。
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
