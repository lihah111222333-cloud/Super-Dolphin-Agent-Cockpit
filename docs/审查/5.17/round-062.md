# Round 062 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 09:04:31 KST
- 结束：2026-05-17 09:12:07 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 skill candidate review/RPC 与 `CreateSkill` 写入路径。重点看候选可见性、repo fingerprint 隔离、审批落盘、审计与最终技能文件是否一致。

- `internal/module/skill/contract.go`
- `internal/module/skill/rpc_candidate_types.go`
- `internal/module/skill/rpc.go`
- `internal/module/skill/candidate_review.go`
- `internal/module/skill/candidate_audit.go`
- `internal/module/skill/skills_fs.go`
- `internal/module/skill/events.go`
- `internal/module/skill/candidate_review_test.go`
- `internal/util/repofingerprint/fingerprint.go`

## Findings

1. **[major] `skills/candidate/get` 没有 cwd/repo fingerprint 过滤，可跨项目查看候选元数据与 redacted sample**
   - 证据：RPC `getCandidateRPCParams` 只有 `candidate_id`，没有 `cwd`（`internal/module/skill/rpc_candidate_types.go:29-31`）。`skillCandidateGetHandler()` 直接调用 `svc.GetCandidateByID(ctx, id)`，不计算 caller repo fingerprint（`internal/module/skill/rpc.go:422-429`）。而 list/approve/reject 都要求 cwd 并计算 fingerprint（`internal/module/skill/rpc.go:383-447`）。`GetCandidateByID()` 也只是投影 store row，不调用 `validateCandidateCaller()`（`internal/module/skill/candidate_review.go:61-70`）。
   - 风险：知道或猜到 candidate id 的调用方可以读取其他项目候选的 slug/content_hash/repo_fingerprint/status/redacted_sample。即便 SkillMD 被隐藏，redacted sample 和 repo fingerprint 仍可能暴露项目行为、工具调用摘要和审查队列状态。
   - 建议：get RPC 加 `cwd` 参数并按 caller fingerprint 校验；或只允许 list 返回的 id 在同请求上下文内读取。

2. **[major] 审批人看不到完整 `SkillMD`，但 approve 会把完整内容写入磁盘**
   - 证据：`Candidate` 明确省略 `SkillMD`，注释称完整 SKILL.md “must never appear in list / lookup responses”（`internal/module/skill/contract.go:67-82`）。`candidateFromStore()` 对 get 也只返回 `RedactedSample`，不返回 `SkillMD`（`internal/module/skill/candidate_review.go:197-215`）。但 approve 时 `promoteCandidateToSkill()` 用 `c.SkillMD` 作为 `CreateSkill` content 写入项目（`internal/module/skill/candidate_review.go:176-185`）。
   - 风险：reviewer 只能基于 slug/hash/sample 审批，无法看到完整 frontmatter、工具步骤、边界条件或残留敏感内容。量化反馈可能把未充分审查的自动生成 SKILL.md 直接写入项目技能库。
   - 建议：get/detail 应返回完整但已 redacted 的 SKILL.md，或提供 diff/preview 审查接口；禁止仅凭 sample approve。

3. **[major] approve 先把 DB 状态改为 approved，再写文件；CreateSkill 失败会留下不可重试的 approved 非 promoted 行**
   - 证据：`ApproveCandidate()` 先 `approveCandidateForPromotion()`，内部调用 `candidateStore.Approve()` 把 pending 改为 approved（`internal/module/skill/candidate_review.go:102-135`）；随后 `promoteCandidateToSkill()` 失败时直接返回错误，注释明确 row stays approved（`internal/module/skill/candidate_review.go:96-115`）。后续再次 approve 会被 `validateCandidateApproval()` 的 pending guard 拒绝（`internal/module/skill/candidate_review.go:138-145`）。
   - 风险：临时文件系统错误、cwd 错误或 slug 验证失败后，候选处于 approved 但未落盘状态，普通 approve 入口无法重试。量化反馈候选会卡在半审批状态，需要人工 DB 修复或新增专用重试路径。
   - 建议：把 approve+create+promote 拆成可恢复状态机，例如 `approval_granted` 后提供 `promote` retry；或先 dry-run CreateSkill 验证 slug/path/content，再提交 approved。

4. **[moderate] reject RPC 不把 cwd 注入 ctx，审计和后续上下文不含项目作用域**
   - 证据：approve handler 用 `scopedSkillContext(ctx, p.CWD)` 注入 cwd 后调用 service（`internal/module/skill/rpc.go:383-398`）。reject handler 只 `requireRequestCWD` 和 `repofingerprint.Compute`，随后把原始 `ctx` 传给 `svc.RejectCandidate()`（`internal/module/skill/rpc.go:406-418`）。
   - 风险：当前 RejectCandidate 主要依赖显式 fingerprint，短期可工作；但任何后续审计、事件或策略如果从 ctx 取 cwd，会在 reject 路径缺失项目上下文。approve/reject 审计口径不一致。
   - 建议：reject handler 也用 `scopedSkillContext`，保持所有 candidate review 操作上下文一致。

5. **[moderate] fingerprint 只基于 canonical path，项目移动会导致旧候选不可见/不可审批**
   - 证据：`repofingerprint.Compute()` 对 cwd 做 Abs/Clean/EvalSymlinks 后取路径 SHA-256 前 128 bit（`internal/util/repofingerprint/fingerprint.go:13-28`）。list/approve/reject 都用当前 cwd 计算 fingerprint（`internal/module/skill/rpc.go:383-447`）。
   - 风险：同一 repo 移动目录、换 worktree 或路径挂载变化后，旧候选的 repo_fingerprint 与当前项目不匹配，pending 列表消失，已生成的量化反馈无法审查或审批。
   - 建议：优先使用 git remote/root identity 或仓库内部稳定 id；路径 fingerprint 只作为 fallback。

## 误报与已覆盖项

- approve/reject/list 对项目 scope 都有 caller fingerprint 校验，能阻止正常路径跨项目审批（`internal/module/skill/candidate_review.go:148-159`）。
- `CreateSkill` 通过 `WriteLocal(..., project)` 落到项目 `.agent/skills`，不会走 system scope（`internal/module/skill/skills_fs.go:261-278`）。
- system-scope `trust: signed` 不会绕过候选审批；测试覆盖 signed skill 仍需完整 approve/promote（`internal/module/skill/candidate_review_test.go:514-560`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/store/skillcandidate -count=1
```

结果：通过。

## 下一轮建议

- Round 063 审查 skill 文件写入/读取与路径安全：`skills_fs.go`、`skills_meta.go`、import/delete/read/write，确认自动生成技能是否能覆盖或逃逸项目技能根。
