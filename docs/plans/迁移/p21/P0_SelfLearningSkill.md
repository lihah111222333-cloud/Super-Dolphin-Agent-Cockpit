# P0: 自学习 Skill 闭环

## 目标

Agent 自动将成功经验（trajectory）提炼为可复用 Skill，并以 scope-safe 方式落入本地技能目录，供后续 turn 发现与消费。

## 现状校准

- 当前仓库已经具备 Skill 写入基础能力：`skills/local/write`、`skills/local/importDir`、`WriteSkillContent`、`WriteSummary`，以及 `SkillsChanged` 事件广播。
- `skills/local/write` 是 `cwd` + scope 感知的；但 `WriteSkillContent` / `WriteSummary` 当前直接写全局 `s.root`，不适合承接 project-scope 自学习沉淀。
- 当前 `skill/rpc.go` 注册的是宿主 / UI JSON-RPC，不是 agent 直接可见的 MCP / dynamic tool；若未来希望模型运行中主动创建 skill，应另起 `internal/sidecar/orch/tools` 方案，不在 P21 默认范围。
- `internal/module/turn/tracker.go` 目前只维护 turn 的本地状态与句柄，不记录完整 `tool_calls/results` 轨迹；不应把它扩成事实流水仓库。
- 自动提炼更适合复用现有 bus 订阅模式，但 callback 只能做 state merge / enqueue；LLM 提炼、落盘与重试都必须交给 runner worker。
- signed skill 验签**延后到 P22**；P21 只先定义写入路径、observation 契约和自动提炼闭环。
- `internal/contract/dream.go` / 各 provider DreamExecutor 当前仍可能返回 `ErrDreamExecutorNotConfigured`；P0 提炼器不能把“可用 DreamExecutor”当默认前置条件。

## Phase 1：Host 侧创建入口（P0a）

| 模块 | 文件落点 | 说明 |
|---|---|---|
| Host 创建封装 | `internal/module/skill/{contract.go,service.go}` | `CreateSkill(ctx, params)` **仅作为 `WriteLocal(..., scope=project)` 的薄封装**：只负责 slug 规则、`name -> .agent/skills/<slug>/SKILL.md` 路径映射、`cwd/scope` 校验与 sentinel error；**禁止**另起第二条写盘实现，所有真实写盘都回落到 `WriteLocal` |
| 类型定义 | `internal/module/skill/rpc_skill_types.go` | 新增 `createSkillParams{Name, Content, Scope, CWD}`，并把 `cwd` 视为 project scope 的必填字段 |
| Host RPC 包装 | `internal/module/skill/rpc.go` | 暴露宿主侧 slash 风格方法 `skills/create`；继续沿用 host/UI JSON-RPC，不扩到 MCP tool |
| 写入复用 | `internal/module/skill/service.go` | project scope 只允许走 `CreateSkill` 或 `WriteLocal(..., scope=project)`，不复用 `WriteSkillContent` / `WriteSummary` |

> `skills/create` 只用于宿主 / UI JSON-RPC。本期不默认要求 agent-visible skill create，也不要求修改 `cmd/mcp-orch`。
>
> project-scope 自学习只允许 `CreateSkill` / `WriteLocal(..., scope=project)`；`WriteSkillContent` / `WriteSummary` 由于直写 `s.root`，明确禁止承接 project-scope 自学习。
>
> `skills/create` 缺 `cwd` 必须硬报错；service 层直接返回 `ErrMissingCWD`，RPC 层映射 `InvalidParams`，不允许把 project-scope 写入兜底到 system root。

## Phase 2：自动提炼闭环（P0b owner）

### Canonical Turn Observation Contract

Canonical Turn Observation Contract：共享 observation 层统一产出 local turn id ↔ provider turn id、`call_id -> turn_id`、`skills_selected`、token snapshot、terminal precedence 与 raw/typed 去重事实；P0b 是 owner，P3 只消费这层输出。

- 必须维护 `local turn id ↔ provider turn id` 映射表，供 turn 终态、tool 事件与 provider raw event 对齐。
- 必须维护 `call_id -> turn_id` 映射；`internal/dto/tool/event.go:46-55` 的 `ToolDiffUpdated` 只有 `ThreadID/AgentID/CallID`，**没有 `turn_id`**，归因不能跳过这张表。
- `skills_selected` 只表示 resolver 在 `PrepareTurn` 选中并准备注入的 skill 集合，**不等于模型实际使用**。
- token snapshot 要做归一：保留旧的非零 token 计数，不被 zero-event 覆盖；Claude path 的 `UITokensUpdated` 经 `internal/provider/unified/ui_tokens.go:58-75` 固定 `Projection="thread"`，且可能不带 `turn_id`，不能直接当 per-turn 权威值。
- terminal precedence 必须固定：`interrupted/aborted` 一旦成立，不能被 late `completed` 覆盖；`internal/dto/turn/event.go:11-21` 的 `TurnCompleted.Success` 是非指针 `bool`，缺字段时有默认 true 陷阱。
- `dto.BusRawProviderEvent` 与 typed event 必须在 observation 层统一去重，只允许按 `call_id`、raw event id 或等价 key 合并一次；collector / trajectory 不得 raw + typed 双算。
- observation 层为 P0b 前置交付；P3 作为 consumer 依赖这层事实，不再自建第二套 turn 归因逻辑。

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 共享 observation 层 | `internal/module/turn/observation.go` [NEW] | 统一产出 turn / tool / token / terminal 事实，并作为 P0b 的 owner 交付物；**必须以独立 `fx.Invoke(RegisterObservationSubscribers)` 注入 `bus.subscribers`，只向下游 push 只读事实**；`turnTracker` / service 不得反向持有 observation，避免循环依赖 |
| 轨迹收集器 | `internal/module/turn/trajectory_collector.go` [NEW] | 只消费 observation 输出，负责启发式判断所需的采样、去重与入队 |
| 启发评估器 | `internal/module/turn/skill_evaluator.go` [NEW] | 判定是否值得提炼，例如成功、tool call 次数、diff / 结果质量、无人工拒批 |
| LLM 提炼器 | `internal/module/turn/skill_extractor.go` [NEW] | 在 runner worker 中把轨迹归纳为标准 `SKILL.md`；失败只记日志，不影响主 turn |
| 生命周期接线 | `internal/module/turn/module.go` | `fx.Provide` 只构造 collector / queue / extractor；`fx.Invoke(RegisterSubscribers)` 注入 `bus.subscribers`；提炼 worker 进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） |
| Candidate 表 | `migrations/0064_skill_candidates.sql`、`sql/queries/skill_candidate.sql`、`internal/store/skill/candidate_store.go` [NEW] | 提炼器输出先落 `skill_candidates(id, scope, slug, content_hash, repo_fingerprint, status, approved_by, approved_at, reason, redacted_sample, created_at)`；v1 默认状态为 `pending_review`，**不直接写盘**，由 host UI / API 流程审批后再 promote |
| 落盘 | `internal/module/skill/service.go` | 审批通过后统一通过 `CreateSkill` 或 `WriteLocal(..., scope=project)` 写入，并复用 `SkillsChanged` 事件 |
| 二次 redaction | `internal/module/turn/skill_extractor.go` | LLM 提炼返回后**必须再跑一遍脱敏规则**（覆盖 secret / bearer / cookie / JWT / 常见 env 名），并把 `content_hash + redacted_sample` 落 candidate audit；脱敏失败直接丢弃该 candidate 并记指标 |

## 发现与加载语义

- 写入 `SKILL.md` 后，下次 turn 最多保证可被 Skill catalog 扫描到。
- `skills_selected` 与 catalog 命中只表示准备注入 / 可发现，不承诺模型一定实际使用该 skill。
- 当前没有 runtime auto-match 的完整闭环；“自动加载”不能表述成下一轮一定自动注入模型。
- signed skill 验签延后到 P22，本期不把已落盘 skill 扩写成已验签可执行 artifact。

## 关键实现约束

- project-scope 自学习只允许 `CreateSkill` / `WriteLocal(..., scope=project)`；**显式禁止** `WriteSkillContent` / `WriteSummary` 承接 project-scope 自学习。
- `skills/create` 缺 `cwd` 必须硬报错；不能把 project-scope 自动降级到 system scope。
- 自动提炼默认只写 project scope；**system scope 必须人工 review gate**，且 review request / audit record 至少要携带 `scope`、`skill slug`、`content hash`、`repo fingerprint(project_root/cwd)`、`approved_by`、`approved_at`、`reason`；未获批不得写 system scope。
- bus callback 内只做 observation 事实合并、采样和入队；**LLM 提炼不在 bus callback 内执行**，而是在 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） worker 中跑。
- `fx.Module` 只负责构造 collector / extractor / queue 等对象；长跑 goroutine、批量 flush、重试策略都交给 `Runner.Run(ctx)`。
- 自动提炼前必须做内容净化：裁剪大工具结果、剥离 secret / 凭据 / 客户数据、抑制 prompt injection 文本。
- `SkillsChanged` 事件当前不携带完整 scope / cwd 语义，不能把它当成 project-vs-system 的权威事实，除非同步扩展 payload。
- 自动提炼必须 feature-gate；提炼失败不能反向影响主 turn 成功与否。
- P21 阶段的 `trust: signed` 只表示“声明为 signed、待 P22 verifier 兑现”，**一律按未验签 / 不可信处理**；不得因 frontmatter 写了 `signed` 就跳过审批、脱敏或 system-scope review。
- 审批缓存 / review decision 不能只按 `name + hash` 命中；必须至少带 `repo fingerprint(project_root/cwd)`，避免同名同 hash skill 在不同项目间复用旧批准。
- 提炼器若命中 `ErrDreamExecutorNotConfigured`，只能记日志 / 指标并跳过本次提炼；不得在 bus callback 内补救重试，更不得让主 turn 失败。
- `CreateSkill` **禁止**新起独立写盘路径；实现必须是 `WriteLocal(..., scope=project)` 的薄封装，只额外处理 slug / cwd 校验 / sentinel error，保证"一条写入路径"口径。
- LLM 提炼输出**必须**经过二次 redaction；redaction 失败（命中已知 secret pattern 且无法脱敏）时 candidate 直接丢弃，不允许 fallback 到"未脱敏入库"。
- 自动提炼默认**不直写技能目录**：extractor 产物先入 `skill_candidates` 表，status=`pending_review`；需要人工/自动审批事件（携带 `scope + slug + content_hash + repo_fingerprint + approved_by + reason`）推进到 `approved` 才允许调 `CreateSkill` 落盘。project scope 与 system scope 都走这条审批链，只是 policy 阈值不同。
- observation 层与 collector / extractor 之间必须是**单向 push**（observation → queue → consumer）；`turnTracker` 不得 import observation 包，以防循环依赖与重复归因。

## 必测项

- `skills/create` wrapper 必须保住 `cwd/scope` 语义，不能把 project 写到 system root。
- 缺 `cwd` 时必须命中硬报错路径，而不是落回全局目录。
- trajectory 聚合必须验证 raw / typed 去重、`ToolDiffUpdated` 归属、token 归一与 terminal precedence。
- system scope review gate 必须验证无人工审核就不能写，且审批记录必须落全 `approved_by/approved_at/reason/repo fingerprint`。
- approval cache 必须验证同名同 hash skill 在不同 repo fingerprint 下不会共享批准结果。
- `trust: signed` 在 verifier 落地前必须验证仍走 untrusted/redacted/review 路径，不能跳过审批。
- callback / runner 边界必须验证：回调内无 LLM 提炼、无同步磁盘写、无长时阻塞。
- `ErrDreamExecutorNotConfigured` 必须验证只导致 skip + log/metric，不影响主 turn 成功路径。
- `CreateSkill` 单测必须断言调用链最终落到 `WriteLocal(scope=project)`；若未来有人另起写盘路径，测试应失败。
- LLM 提炼 golden 测试：构造含 bearer/JWT/OPENAI_API_KEY 的假 trajectory，断言提炼结果被二次 redaction 抹除且 candidate.redacted_sample 里无原文。
- Candidate 审批流测试：未审批的 candidate 不能落盘；审批 payload 缺 `approved_by` / `repo_fingerprint` 必须拒绝 promote。
- observation 层独立性：断言 `turnTracker` import 图里**不**出现 `internal/module/turn/observation`。
