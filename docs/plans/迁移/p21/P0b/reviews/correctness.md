# P0b Correctness Review (2026-04-25)

范围说明：`2dc1b21..HEAD` 当前实际包含 20 个 commit；以下只按你给的精确路径审 P0b 相关改动。

## 正确性

1. **[critical] `approved` 变成不可恢复中间态，和注释/状态机语义冲突**
`internal/module/skill/candidate_review.go:125` / `141` / `150`
现在流程是 `pending_review -> approved -> CreateSkill -> MarkPromoted`。一旦 `CreateSkill` 失败，row 已经停在 `approved`；下一次再调 `ApproveCandidate` 会在 `raw.Status != pending_review` 直接返回 `ErrCandidateNotPending`。`MarkPromoted` 失败也是同样，文件可能已落盘但 row 卡在 `approved`，没有公开恢复路径。
**期望写法**：把 `approved` 明确定义成可恢复态，并允许 `ApproveCandidate` 对 `status=approved` 走 resume 分支：跳过 `Store.Approve`，继续 `CreateSkill/MarkPromoted`；或新增 `promotion_failed` / retry RPC / `MarkPromoted` 管理路径。至少要在状态变更前做 `cwd`、slug、content 预校验，避免明显失败也先落 `approved`。

2. **[critical] project candidate 没有绑定调用方 CWD，可能把 repo A 的候选写进 repo B 并标记 repo A promoted**
`internal/module/skill/candidate_review.go:118` / `141`，`sql/queries/skill_candidate.sql:16`
`ApproveCandidate` 只检查 `raw.RepoFingerprint` 非空，没有校验它等于当前 `cwd` 的 fingerprint；`ListPendingSkillCandidates` 也不按 repo 过滤。reviewer 如果在 repo B 的 cwd approve 了 repo A 的 candidate，`CreateSkill` 会用 repo B 的 cwd 写文件，但 `MarkPromoted` 标记的是 repo A candidate。
**期望写法**：approve 前先 `requireCWD(ctx)`，计算同一套 `RepoFingerprint(cwd)`，要求 `raw.RepoFingerprint == requestFingerprint`；pending list 也应接受 cwd/repo fingerprint 参数并只列当前 repo，或返回时强制 UI/RPC 带 fingerprint 过滤。

3. **[major] extractor 生产的真实 project candidate 大概率 `repo_fingerprint=''`，后续永远无法 approve**
`internal/module/turn/trajectory_collector.go:27`，`internal/module/turn/skill_extractor.go:198` / `217`，`internal/module/skill/candidate_review.go:118`
collector 明确不填 `Trajectory.Cwd`，但 extractor 只做 `RepoFingerprint(t.Cwd)`，没有按注释“via ThreadID backfill”。空 cwd 会插入 project-scope candidate，Step 5 又拒绝空 fingerprint，闭环断掉。
**期望写法**：extract 前必须从 turn/thread/session 状态回填 cwd；无法回填时不要插入 project candidate，并计数/日志；最好在 DB 或 store 层加 `CHECK (scope <> 'project' OR repo_fingerprint <> '')` 防止坏状态入库。

4. **[major] `superseded` 是声明状态但没有任何合法转移路径**
`migrations/0064_skill_candidates.sql:51`，`sql/queries/skill_candidate.sql:22`-`42`
五状态里包含 `superseded`，注释说“replaced by a newer candidate row”，但 SQL/store 只有 approve/reject/promote，没有 supersede query，也没有 insert 新版本时 supersede 旧版本的逻辑。旧 pending 会继续出现在 review list。
**期望写法**：补 `MarkSkillCandidateSuperseded`，在同 repo/scope/slug 新 hash 入库时把旧 pending/approved 按规则置为 `superseded`；否则先移除该状态和注释，避免状态机虚假完整。

5. **[major] store 把 `pgx.ErrNoRows` 全映射为 `ErrConflict`，无法区分“不存在”和“状态不匹配”**
`internal/store/skillcandidate/store.go:154` / `168` / `179`
`UPDATE ... WHERE id=$1 AND status=... RETURNING *` 的 no rows 可能是 id 不存在，也可能是状态不匹配。当前 approve/reject/promote 全部返回 conflict，RPC 层也无法给出正确 404 / not pending。并发 approve 的双写被 SQL guard 挡住，但第二个 reviewer 得到的是模糊 store conflict。
**期望写法**：用 CTE 或二次 `SELECT status FROM skill_candidates WHERE id=$1`：不存在映射 `ErrNotFound`，存在但状态不符映射 `ErrConflict` / domain `ErrCandidateNotPending`。

6. **[major] `SkillsChanged` cross-scope override 会真实丢事件**
`internal/module/skill/events.go:121`-`129`
debounce buffer 只有一个 `skillsChangedNext`。project 事件尚未 flush 时来了 system 事件，`mergeSkillsChanged` 直接 `return next`，前一个 project 事件不会被 emit。订阅者会漏掉一次 scope/cwd 的 invalidation。
**期望写法**：不要 override 单 buffer；改成按 `(scope,cwd)` 分桶 debounce，或在 cross-scope/cross-cwd 时先把 current 入队，flush 时 emit 队列中的所有事件。

## 安全性

1. **[major] `long_hex` 在 `long_base64` 前会造成部分 secret 残留，residual scan 抓不到**
`internal/module/turn/redaction.go:52`-`56`，`internal/module/turn/skill_extractor.go:173`-`181`
例：一个 base64-like token 以 32 个 hex 字符开头再接 `+/xx`，第一轮会先把 hex 前缀替换成 `[REDACTED:long_hex]`，短后缀残留；第二轮后缀长度不足 32，不会触发 residual scan。base64url 的 `_` / `-` 也没有被 generic base64 覆盖。
**期望写法**：不要顺序替换互相重叠的 regex；应先在原文上收集所有 match span，按最长/优先级合并后一次替换。至少补 `base64url` pattern，并新增 hex-prefix base64 / url-safe token 的回归测试。

## 可读性

1. **[minor] 注释描述了“可重试/已 enforce”，实现却相反**
`internal/module/skill/candidate_review.go:93`-`98`，`migrations/0064_skill_candidates.sql:26`-`28`
注释说 CreateSkill 失败后 operator 可 retry，实际 `approved` 会被 `ErrCandidateNotPending` 拦住；migration 说 Step 4 enforce project fingerprint，实际 extractor 可插入空 fingerprint。
**期望写法**：先修状态机/指纹校验；若暂不修，注释必须改成当前真实行为，避免后续维护者按错误契约写调用方。

## 性能

未发现本次范围内明确的性能 blocking issue。

## 测试覆盖盲区

- **[major]** `internal/module/skill/candidate_review.go:150` 的 `MarkPromoted` 失败分支未测；需要测文件已落盘、返回 `SkillPath`、row 处于可恢复状态。
- **[major]** `internal/module/skill/candidate_review.go:125` 未测 `status=approved` 的 retry/resume；当前测试只断言 CreateSkill 失败后不 promote，但没有断言下一次能恢复。
- **[major]** `internal/module/skill/candidate_review.go:118` 未测 candidate fingerprint 与调用方 cwd fingerprint 不一致。
- **[major]** `internal/module/turn/skill_extractor.go:198` 未测 `Trajectory.Cwd == ""` 的真实 collector 输入路径。
- **[major]** `internal/module/turn/redaction.go:55`-`56` 未测 hex-prefix base64 / base64url fall-through。
- **[major]** `internal/module/turn/trajectory_collector.go:323` 未测 terminal/drain 后 late diff；也未测 callID 复用导致 attribution 覆盖。
- **[major]** `internal/module/skill/events.go:127` 的测试当前断言“override 后没有 extra event”，等于把事件丢失固化成预期；应改测 cross-scope 两条事件都能收到。
- **[minor]** `internal/store/skillcandidate/store_test.go` 全是 stub querier，未覆盖真实 SQL guard 下“不存在 vs 状态不匹配”的区分。

验证：`go test ./internal/store/skillcandidate ./internal/module/turn` 通过；`go test ./internal/module/skill` 在 Windows 环境下有既有 `printenv/pwd` 和 RPC JSON 相关失败；`go test ./internal/module/skill -run TestReview` 与 `-run Test.*SkillsChanged` 通过。

**verdict：7 个 blocking issues（critical/major）。**
