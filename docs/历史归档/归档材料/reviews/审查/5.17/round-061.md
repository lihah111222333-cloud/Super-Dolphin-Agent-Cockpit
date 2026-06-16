# Round 061 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:56:34 KST
- 结束：2026-05-17 09:04:18 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 skill extractor/evaluator 与 skill candidate review。重点看量化任务完成后的经验提取、候选去重、审批缓存、redaction 和 promotion 是否会形成可靠反馈。

- `internal/module/turn/skill_evaluator.go`
- `internal/module/turn/skill_extractor.go`
- `internal/module/turn/redaction.go`
- `internal/module/turn/module.go`
- `internal/store/skillcandidate/contract.go`
- `internal/store/skillcandidate/store.go`
- `sql/queries/skill_candidate.sql`
- `migrations/0064_skill_candidates.sql`
- `internal/module/skill/candidate_review.go`
- `internal/module/turn/skill_extractor_test.go`

## Findings

1. **[major] candidate slug 从 turn id 派生，候选去重与审批缓存几乎按每轮分裂**
   - 证据：`newExtractedSkill()` 使用 `slugFromTrajectory(t)` 生成 slug（`internal/module/turn/skill_extractor.go:231-241`）。`slugFromTrajectory()` 优先取 `t.TurnID`，其次 `t.LocalTurnID`，没有解析 LLM 输出的 frontmatter `name`（`internal/module/turn/skill_extractor.go:329-350`）。candidate 唯一键是 `(scope, slug, content_hash, repo_fingerprint)`（`migrations/0064_skill_candidates.sql:53-55`），migration 注释称该四元组也是 approval-cache key（`migrations/0064_skill_candidates.sql:13-19`）。
   - 风险：同一种量化操作经验在不同 turn 里会产生不同 slug，即使内容相同也不会命中同一个审批/去重通道。候选列表会按 turn 膨胀，reviewer 的批准无法复用到后续相似技能，反馈闭环变成低效人工队列。
   - 建议：解析并校验 SKILL.md frontmatter name/slug，或用 canonicalized skill name + content hash 做去重；turn id 应作为 sample/source metadata，而不是 slug。

2. **[major] extractor 没有先查 approval cache，已批准/拒绝的同候选仍会重复跑 LLM**
   - 证据：store 暴露 `LookupApproval()`（`internal/store/skillcandidate/contract.go:73-87`），SQL 按四元组查任意状态候选（`sql/queries/skill_candidate.sql:54-60`）。但 `DefaultExtractor.Extract()` 流程是 evaluate -> prompt -> Dream -> redact -> insert，未调用 `LookupApproval()`（`internal/module/turn/skill_extractor.go:121-151`）。migration 注释明确“extractor consults LookupSkillCandidateApproval before re-running the LLM evaluator”（`migrations/0064_skill_candidates.sql:13-17`）。
   - 风险：对重复量化模式，系统仍会消耗 dream executor 成本，并再次生成/插入候选；之前的 rejection 也不会抑制同类内容，review 噪声持续产生。
   - 建议：在 Dream 前用稳定 slug/content fingerprint 或 prompt fingerprint 查 approval cache；至少在 Dream 后 insert 前查同 content_hash 的批准/拒绝状态并跳过重复 pending。

3. **[major] `MarkSuperseded` 紧跟 insert 执行，可能把同 slug 的旧 pending 全部静默下架**
   - 证据：insert 成功后立即调用 `store.MarkSuperseded(scope, slug, repoFingerprint, created.ID)`（`internal/module/turn/skill_extractor.go:251-267`）。SQL 会把同 scope/slug/repo 且非当前 id 的所有 `pending_review` 改为 `superseded`，不比较 content_hash、质量分或创建原因（`sql/queries/skill_candidate.sql:45-52`）。
   - 风险：一旦 slug 生成从 turn id 改成语义 slug，新的低质量候选会直接隐藏旧的高质量 pending；当前 turn-id slug 则几乎不会 supersede，说明行为两端都不理想。量化反馈候选要么堆积，要么被后来样本覆盖。
   - 建议：supersede 应基于明确版本策略，例如只 supersede 相同 semantic slug 且 reviewer 未处理、评分更低或内容 hash 等价的候选；记录 superseded_by 便于审计。

4. **[moderate] nil candidate store 跳过不增加指标，部署缺表/缺 wiring 不可观测**
   - 证据：`readyToExtract()` 在 `e.store == nil` 时只 warn 并返回 false，没有任何 metrics counter（`internal/module/turn/skill_extractor.go:154-168`）。相比之下 dream nil 会增加 `DreamNotConfigured`（`internal/module/turn/skill_extractor.go:160-164`）。
   - 风险：如果量化反馈在某部署中缺少 skillcandidate store，所有成功 trajectory 都会被跳过，但监控只看到日志；长期看不到技能候选却无法从 extractor metrics 定位原因。
   - 建议：增加 `StoreNotConfigured` 或 `CandidateStoreMissing` counter，并在健康检查中暴露。

5. **[moderate] promotion 使用 candidate slug 作为最终技能名，继承 turn-id 命名问题**
   - 证据：审批通过后 `promoteCandidateToSkill()` 调 `CreateSkill`，`Name: c.Slug`，内容为 `c.SkillMD`（`internal/module/skill/candidate_review.go:176-185`）。candidate response 对外不会返回 SkillMD，reviewer 主要看到 slug/content hash/sample（`internal/module/skill/candidate_review.go:197-230`）。
   - 风险：被批准的自动技能最终文件名可能是 `t-eligible` 或 provider turn id，而不是“risk-review-cron-dedupe”这类可读语义名。技能库质量下降，后续 skill selection 也更难按名称命中。
   - 建议：审批前从 SkillMD frontmatter 提取并校验 name；CreateSkill 使用该 canonical name，candidate slug 与展示名保持一致。

## 误报与已覆盖项

- extractor 对 prompt 和 LLM 输出都做 redaction，并在二次扫描仍有命中时拒绝插入；已有测试覆盖 bearer/openai/jwt 等常见泄漏（`internal/module/turn/skill_extractor_test.go:105-128`；`internal/module/turn/redaction.go:40-98`）。
- evaluator 会拒绝非 completed、显式失败、工具调用过少、全部工具失败的 trajectory（`internal/module/turn/skill_evaluator.go:51-65`）。
- candidate approval/reject/promote 有状态机 guard，非 pending 或非 approved 的状态转换不会静默成功（`sql/queries/skill_candidate.sql:23-43`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/turn ./internal/store/skillcandidate ./internal/module/skill -count=1
```

结果：通过。

## 下一轮建议

- Round 062 审查 skill candidate review/RPC 与 CreateSkill 写入路径：确认审批权限、cwd/repo fingerprint、最终文件路径和用户可见信息是否一致。
