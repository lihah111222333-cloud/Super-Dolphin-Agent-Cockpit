# P0b Step 5：review gate（审批链路）

## 目标

为 `skill_candidates` 表暴露审批 RPC：列 pending、approve、reject。approve 通过后**复用 P0a 的 `Service.CreateSkill`** 把 SKILL.md 落到 `.agent/skills/<slug>/`，并写 auditlog。失败状态不前进到 `promoted`。

## 前置依赖

- Step 1：`internal/store/skillcandidate/Store`（含 `skill_md` 字段、状态机方法）。
- Step 4：extractor 已写入 candidate（`status='pending_review'` + `skill_md` 已脱敏）。
- P0a：`Service.CreateSkill` 已交付（见 `internal/module/skill/contract.go:51` 与 `internal/module/skill/service.go`）；本步**禁止**另起写盘路径。
- `internal/store/auditlog/` 已存在（见 `internal/store/auditlog/store.go:1`），review gate 复用。

## 文件清单

### 新建

| 路径 | 说明 |
|---|---|
| `internal/module/skill/candidate_review.go` | `ApproveCandidate` / `RejectCandidate` / `ListPendingCandidates` / `LookupApproval` 实现。 |
| `internal/module/skill/candidate_review_test.go` | 必测项见下文。 |
| `internal/module/skill/rpc_candidate_types.go` | `ApproveCandidateParams` / `RejectCandidateParams` / `CandidateRef` JSON-RPC 类型。 |
| `internal/module/skill/candidate_audit.go` | 写 `auditlog` 的 helper（统一 record schema）。 |

### 修改

| 路径 | 说明 |
|---|---|
| `internal/module/skill/contract.go` | `Service` 接口加 4 个方法（见下）。 |
| `internal/module/skill/service.go` | 实现新方法；approve 时调 `s.CreateSkill(...)`；落盘成功后 `MarkPromoted` + audit。 |
| `internal/module/skill/rpc.go` | 注册 3 个 RPC（`skills/candidate/list/pending` / `approve` / `reject`）。 |

## 契约

### Service 接口扩展（叠加在 `internal/module/skill/contract.go:46` 现有 `Service` 接口上）

```go
type Service interface {
    // ... 现有方法保持不变 ...

    ApproveCandidate(ctx context.Context, p ApproveCandidateParams) (ApproveResult, error)
    RejectCandidate(ctx context.Context, id int64, reason string) error
    ListPendingCandidates(ctx context.Context, limit, offset int32) ([]Candidate, error)
    LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*Candidate, error)
}

type ApproveCandidateParams struct {
    CandidateID int64  // 必填
    ApprovedBy  string // 必填，非空白
    Reason      string // 可空（推荐填写）
}

type ApproveResult struct {
    OK        bool   `json:"ok"`
    SkillPath string `json:"skill_path"` // 落盘后的相对路径，如 ".agent/skills/<slug>/SKILL.md"
}

// Candidate 是 store.Candidate 的对外投影；按需裁剪敏感字段（不要外泄 skill_md 全文给 list 接口，仅 GetByID / approve 内部使用）。
type Candidate struct {
    ID              int64
    Scope           string
    Slug            string
    ContentHash     string
    RepoFingerprint string
    Status          string
    ApprovedBy      string
    ApprovedAt      *int64
    Reason          string
    RedactedSample  string
    CreatedAt       int64
}
```

### RPC 方法（snake_case，与现有 `skills/*` 风格一致）

| Method | Params | Result |
|---|---|---|
| `skills/candidate/list/pending` | `{limit, offset}` | `{candidates: []CandidateRef}` |
| `skills/candidate/approve` | `{candidate_id, approved_by, reason}` | `{ok, skill_path}` |
| `skills/candidate/reject` | `{candidate_id, reason}` | `{ok}` |

`CandidateRef` 是面向 UI 的精简投影：仅含 `id/scope/slug/content_hash/repo_fingerprint/status/redacted_sample/created_at`，**不包含** `skill_md` 全文。

## 实施流程（approve 路径）

1. 校验 `approved_by` 非空白，否则返回 `InvalidParams`（RPC 层映射）。
2. `store.GetByID(candidate_id)` 读 candidate；不存在 → `NotFound`。
3. 若 `candidate.Scope == "project"` 且 `candidate.RepoFingerprint == ""` → 拒绝，返回 sentinel error。
4. 若 `candidate.Status != "pending_review"` → 返回 sentinel error（已 approved/rejected/promoted/superseded 的 candidate 不能再走 approve 流程）。
5. `store.Approve(candidate_id, approved_by, reason, now)`：`pending_review` → `approved`。受影响行数 0 → 并发竞态 error。
6. 调 `s.CreateSkill(WithCWD(ctx, derivedCwd), createSkillParams{Name: candidate.Slug, Content: candidate.SkillMD, Scope: candidate.Scope, CWD: derivedCwd})`。
    - `derivedCwd` 来自 caller ctx（review gate RPC 必须由 host UI 在 ctx 上挂 cwd），**不**从 `repo_fingerprint` 反解（fingerprint 是单向 hash）。
    - caller ctx 缺 cwd → `CreateSkill` 自身会返回 `ErrMissingCWD`（见 `internal/module/skill/contract.go:11`），review gate 直接透传该错误。
7. `CreateSkill` 成功 → `store.MarkPromoted(candidate_id)` + 写 auditlog。
8. `CreateSkill` 失败 → **不**调 `MarkPromoted`；状态停在 `approved`；error 透传给 caller。auditlog 仍写一条 `event="approve_promote_failed"` 记录失败原因。

## 实施约束

- 缺 `approved_by` 或 `repo_fingerprint`（project scope）→ 拒绝，sentinel error 明确（P0 §"关键实现约束"：未获批不得写 system scope；project scope 也走同链路只是 policy 阈值不同）。
- system scope 必须人工 review；本期 RPC 不区分 scope 的审批策略，**但** auditlog 必须记录 `scope`，以便后续 P22 加 policy 阈值。
- approval cache 命中键：`(scope, slug, content_hash, repo_fingerprint)`，不允许降级（P0 §"关键实现约束"）。`LookupApproval` 必须按四元组查；任何字段为空都按字面值匹配，**不**做通配。
- approve 后**必须**调 `CreateSkill`（不能另起写盘路径，违反 P0 §"关键实现约束"）。
- 全流程写 `auditlog`，记录 `scope/slug/content_hash/repo_fingerprint/approved_by/approved_at/reason`；`event` 字段枚举：`approve_succeeded` / `approve_promote_failed` / `reject` / `lookup`。
- `trust: signed` 仍走完整审批，不跳过（P0 §"关键实现约束"：P21 阶段一律按未验签处理）。
- `Reject` 同样写 auditlog（`event="reject"`）；rejected candidate 不能被再次 approve（store 层强制）。
- review gate 不直接读 `candidate.SkillMD` 给 RPC 出参；落盘只在 service 内部完成，避免 SKILL.md 全文经 RPC 外泄。

## 验收标准

### 必测项（建议测试名）

- `TestReview_PendingCannotPromote`：未经 `Approve` 直接调 `MarkPromoted` 应被 store 层拒绝（这条由 Step 1 必测项保障，Review 层叠加：approve 流程缺步骤不进 promoted）。
- `TestReview_RequiresApprovedBy`：`approved_by=""` 或仅含空白 → `InvalidParams`，candidate 状态不变。
- `TestReview_ProjectScopeRequiresRepoFingerprint`：candidate `scope="project"` 且 `repo_fingerprint=""` → 拒绝，状态不变。
- `TestReview_ApprovalCacheScopedByFingerprint`：`LookupApproval` 在 `(scope=project, slug=X, hash=H, repo=A)` 上有 approved 记录；查询 `(...repo=B)` **不**命中。
- `TestReview_ApproveTriggersCreateSkillAndAudit`：成功路径下 `CreateSkill` 被调一次（用 fake `Service` 注入观察）；auditlog 写入 `event="approve_succeeded"` 含完整字段。
- `TestReview_SignedSkillStillRequiresApproval`：candidate `skill_md` frontmatter 含 `trust: signed` → 仍需 `ApproveCandidate` 才能 promote（不跳过审批）。
- `TestReview_CreateSkillFailureDoesNotMarkPromoted`：fake `CreateSkill` 返回 error → candidate 状态停在 `approved`、未到 `promoted`；auditlog 写 `event="approve_promote_failed"`。
- `TestReview_RejectWritesAudit`：`RejectCandidate` 后 status=`rejected`、auditlog `event="reject"`。
- `TestReview_RpcMissingCwdMapsToInvalidParams`：RPC handler 在 caller ctx 没有 cwd 时返回 `InvalidParams`（来自 `ErrMissingCWD`）。

### 命令

```bash
go test ./internal/module/skill/ -run Review
go test ./internal/module/skill/ -run Candidate
```

### 集成验证

- 启动整套 fx app；插入一条 pending candidate；通过 RPC `skills/candidate/approve` 审批；DB 中 candidate.status=`promoted`、`.agent/skills/<slug>/SKILL.md` 存在、`auditlog` 含两条记录（approve + create）。

## 已知风险 / 反模式

- **从 `repo_fingerprint` 反解 cwd**：fingerprint 是单向 hash，不可逆；cwd 必须由 caller 在 ctx 上提供。
- **绕过 `CreateSkill` 直接 `os.WriteFile`**：违反 P0 §"关键实现约束"（保证一条写入路径口径）。
- **approve 与 `CreateSkill` 不在同一事务**：本设计**故意**不放在同一事务（DB 与文件系统跨边界），用 `approved → promoted` 两阶段状态弥补；落盘失败时不回滚到 pending（避免重复 approve），停在 `approved` 由人工 / 后台重试。
- **list 接口外泄 `skill_md` 全文**：`CandidateRef` 投影必须裁剪。
- **审批 cache 用 `(slug, hash)` 而非四元组**：会跨 repo 复用旧批准。
- **RPC 错误码用通用 internal error**：违规校验必须明确映射 `InvalidParams` / `NotFound` / `Conflict`，否则 UI 无法区分。