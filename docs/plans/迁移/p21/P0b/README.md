# P0b：自学习 Skill 闭环（提炼/审批端）

## 总览

P21 唯一未完成的子项是 **P0b**——自学习 Skill 闭环的提炼与审批端。P0a（host-side `skills/create` 入口）已交付，落点在 `internal/module/skill/contract.go:51`、`internal/module/skill/service.go`、`internal/module/skill/rpc.go`，实现是 `WriteLocal(..., scope=project)` 的薄封装。

P0b 在 P0a 之上，把 turn 生命周期事件转化为可被审批的 `skill_candidate`，并在审批通过后复用 P0a 的 `CreateSkill` 把 SKILL.md 落到 `.agent/skills/<slug>/`。

P0b 的事实底座是已就绪的 `internal/module/turn/observation/`（`bus_provider.go`、`contract.go`、`memory.go`、`module.go`、`subscribers.go`），P0b 只读不反向依赖。

## 端到端数据流

```
turn 生命周期事件（TurnStarted/TurnCompleted/ToolCallBegin/ToolCallEnd/ToolDiffUpdated/UITokensUpdated/...）
        |
        v  (bus.subscribers; 已就绪)
internal/module/turn/observation （Canonical Turn Observation Contract）
        |
        v  单向 push
trajectory_collector  ----[Trajectory]---->  skill_evaluator (verdict)
                                                  |
                                                  v eligible
                                          runner.actors worker queue
                                                  |
                                                  v
                                          skill_extractor
                                            |  DreamExecutor.ExecuteDream
                                            |  二次 Redactor.Redact
                                            v
                                          skill_candidates 表 (status=pending_review)
                                                  |
                                                  v  host UI / JSON-RPC
                                          candidate_review (approve)
                                                  |
                                                  v
                                          Service.CreateSkill (P0a)
                                            |  WriteLocal(scope=project)
                                            v
                                          SkillsChanged 事件 (含 Scope+Cwd)
```

## 6 步索引

| 步骤 | 标题 | 主要落点 | 依赖 |
|---|---|---|---|
| [Step 1](step-1-skill-candidate-schema.md) | skill_candidate 表 + 查询 + store | `migrations/0064_skill_candidates.sql`、`sql/queries/skill_candidate.sql`、`internal/store/skillcandidate/` | — |
| [Step 2](step-2-trajectory-collector.md) | trajectory_collector | `internal/module/turn/trajectory_collector.go` | observation |
| [Step 3](step-3-skill-evaluator.md) | skill_evaluator | `internal/module/turn/skill_evaluator.go` | Step 2 |
| [Step 4](step-4-skill-extractor.md) | skill_extractor + 二次 redaction | `internal/module/turn/skill_extractor.go`、`redaction.go` | Step 1 / Step 2 / Step 3 |
| [Step 5](step-5-review-gate.md) | review gate（审批链路） | `internal/module/skill/candidate_review.go` 等 | Step 1 / Step 4 / P0a |
| [Step 6](step-6-skills-changed-payload.md) | SkillsChanged 扩展 Scope/Cwd | `internal/dto/ui/event.go`、`internal/module/skill/service.go` | Step 5 |

## 步骤依赖图

```
        Step 1 (schema, 含 skill_md 字段)
         ^   ^
         |   | (Step 5 反馈：approve 时需读 SKILL.md 全文)
         |   |
Step 2 --+   |
  |          |
  v          |
Step 3       |
  |          |
  v          |
Step 4 ------+   (写 candidate)
  |
  v
Step 5 (approve -> P0a CreateSkill)
  |
  v
Step 6 (SkillsChanged 携 Scope/Cwd)
```

字段反馈：Step 5 的 approve 流程需要拿到 SKILL.md 全文，所以 Step 1 的 schema **必须**包含 `skill_md TEXT NOT NULL DEFAULT ''` 字段，由 Step 4 extractor 写入时填充，Step 5 read 出后再交给 P0a 的 `CreateSkill`。

## 关键约束摘要

下列 6 条是 P0b 的硬约束（详见 `docs/plans/迁移/p21/P0_SelfLearningSkill.md` §"关键实现约束"）：

1. project-scope 自学习只允许走 `CreateSkill` / `WriteLocal(..., scope=project)`，禁止从 extractor / review gate 另起写盘路径。
2. bus callback 内只做事实合并、采样和入队，**LLM 提炼必须在 `runner.actors` worker 中跑**，不在 callback 内执行。
3. LLM 提炼输出必须经过二次 redaction，redaction 命中已知 secret 且无法脱敏 → **直接丢弃 candidate**，不允许 fallback 到未脱敏入库。
4. 自动提炼默认不直写技能目录，extractor 只产 `skill_candidates` 行（`status=pending_review`），需人工/自动审批通过后才允许调 `CreateSkill`。
5. approval cache 命中键必须是 `(scope, slug, content_hash, repo_fingerprint)`，**不允许**降级成 `(name, hash)`，避免同名同 hash skill 在不同 repo 间复用旧批准。
6. observation 与 collector / extractor 之间是**单向 push**；`turnTracker` 不得 import observation 或 trajectory_collector，避免循环依赖。

## 端到端 golden 测试

Step 5 完成后追加一个 `TestP0b_EndToEnd_FromTurnToSkill`：

- 输入：构造一段含 bearer + JWT + `OPENAI_API_KEY` 的假 trajectory（覆盖 `TurnStarted/ToolCallBegin/ToolCallEnd/ToolDiffUpdated/TurnCompleted`）；
- 经过 collector → evaluator（eligible）→ extractor（DreamExecutor 用 fake 实现回填 SKILL.md）→ Redactor（剥离秘密）→ store.Insert；
- review gate 调 `ApproveCandidate(approved_by="reviewer", reason="ok")`；
- 断言：candidate.status=promoted、`.agent/skills/<slug>/SKILL.md` 落盘、`SkillsChanged` 事件含 `Scope=project` + `Cwd` 非空、auditlog 含 `approved_by/approved_at/repo_fingerprint`。

## 不在本期范围内（延后到 P22）

- signed skill 的密码学验签：P21 阶段 `trust: signed` 仅表示"声明为 signed、待 P22 verifier 兑现"，**一律按未验签处理**，不得因 frontmatter 写了 signed 就跳过审批 / 脱敏 / system-scope review。
- agent-visible skill create via MCP：本期不修 `cmd/mcp-orch`，`skills/create` 仅暴露给 host / UI JSON-RPC。
- runtime auto-match：本期不闭环"下一轮自动加载已批准 skill"，仅保证 skill catalog 下次扫描可见。
- system scope 自动提炼：extractor 默认只产 project scope candidate；system scope 仍需人工 review，无人工审核就不能写。