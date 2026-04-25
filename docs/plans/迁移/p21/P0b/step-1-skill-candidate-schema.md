# P0b Step 1：skill_candidate 表 + 查询 + store

## 目标

为自学习闭环建立 candidate 持久化层：一张 `skill_candidates` 表 + 一组 sqlc 查询 + 一个 Go store。这是 P0b 全部下游步骤（extractor 写入、review gate 读出 / 状态推进）的事实底座。

## 前置依赖

- 仓库已用到 `migrations/0063_agent_thread_name.sql`，下一个可用编号是 `0064`（**不要写 0047**，原 P0 文档的旧编号已过时）。
- sqlc 配置位置：`sql/queries/*.sql`（每表一文件），生成代码落点 `internal/store/sqlc/`。
- 现有 `internal/store/` 目录命名风格：裸名小写（`insight`、`turndedupe`、`prompt` 等）；本步新建 **`internal/store/skillcandidate/`**（不要叫 `skill`，避免和 `internal/module/skill/` 视觉混淆）。

## 文件清单

### 新建

| 路径 | 说明 |
|---|---|
| `migrations/0064_skill_candidates.sql` | 建表 + 索引；单向 up，用 `IF NOT EXISTS` + `IF NOT EXISTS` 索引让 up 幂等（与 0046 / 0060 对齐）。 |
| `sql/queries/skill_candidate.sql` | sqlc 查询（每条注释一行 `-- name: Xxx :one|:many|:exec`）。 |
| `internal/store/skillcandidate/store.go` | Go 接口 + sqlc 适配。 |
| `internal/store/skillcandidate/store_test.go` | 唯一约束 / 状态机 / 分页 / 跨 fingerprint 测试。 |

### 修改

| 路径 | 说明 |
|---|---|
| `internal/store/sqlc/` | sqlc 生成产物（由 `make sqlc-generate` 产出）。 |
| `internal/store/module.go` | 注册新 store 的 fx provider。 |

## 契约

### DB schema（migrations/0064_skill_candidates.sql）

```sql
CREATE TABLE IF NOT EXISTS public.skill_candidates (
    id                BIGSERIAL    PRIMARY KEY,
    scope             TEXT         NOT NULL,
    slug              TEXT         NOT NULL,
    content_hash      TEXT         NOT NULL,
    repo_fingerprint  TEXT         NOT NULL DEFAULT '',
    status            TEXT         NOT NULL DEFAULT 'pending_review',
    skill_md          TEXT         NOT NULL DEFAULT '',
    approved_by       TEXT         NOT NULL DEFAULT '',
    approved_at       TIMESTAMPTZ,
    reason            TEXT         NOT NULL DEFAULT '',
    redacted_sample   TEXT         NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT chk_skill_candidates_scope
        CHECK (scope IN ('project','system')),
    CONSTRAINT chk_skill_candidates_status
        CHECK (status IN ('pending_review','approved','rejected','promoted','superseded')),
    CONSTRAINT uq_skill_candidates_dedupe
        UNIQUE (scope, slug, content_hash, repo_fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_skill_candidates_status_created
    ON public.skill_candidates (status, created_at);

CREATE INDEX IF NOT EXISTS idx_skill_candidates_repo_status
    ON public.skill_candidates (repo_fingerprint, status);
```

**status 枚举说明**：

- `pending_review`：extractor 写入后的初始状态。
- `approved`：reviewer 已批准，但 `CreateSkill` 落盘尚未完成；用于落盘失败时的可观测中间态。
- `rejected`：reviewer 拒绝（终态）。
- `promoted`：`CreateSkill` 落盘成功（终态）。
- `superseded`：被新版 candidate 取代（终态）。

> `promoted` 与 `superseded` 是 P21 原文未列、本步基线扩展的两个状态：落盘后转 `promoted`，新版替代旧版用 `superseded`。

> **字段反馈**：`skill_md TEXT NOT NULL DEFAULT ''` 是为 Step 5 approve 流程预留的。Step 5 的 `ApproveCandidate` 在调用 P0a `CreateSkill` 时需要 SKILL.md 全文，由 Step 4 extractor 在 `Insert` 时写入此字段；详见 `step-5-review-gate.md`。

### sqlc 查询清单（sql/queries/skill_candidate.sql）

| 名称 | 类型 | 用途 |
|---|---|---|
| `Insert` | `:one` | extractor 写入新 candidate；命中唯一约束时由调用方决定如何处理。 |
| `GetByID` | `:one` | review gate 读取单条 candidate。 |
| `ListPending` | `:many` | 列出 `status='pending_review'`，按 `created_at` 升序，支持 limit / offset。 |
| `Approve` | `:exec` | `pending_review` → `approved`；写 `approved_by/reason/approved_at`。 |
| `Reject` | `:exec` | `pending_review` → `rejected`；写 `reason`。 |
| `MarkPromoted` | `:exec` | `approved` → `promoted`。落盘成功后调用。 |
| `LookupApproval` | `:one` | 按 `(scope, slug, content_hash, repo_fingerprint)` 查询历史审批结果（用于 approval cache 命中）。 |

### Go store 接口（internal/store/skillcandidate/store.go）

```go
package skillcandidate

import (
    "context"
    "time"
)

type Candidate struct {
    ID              int64
    Scope           string
    Slug            string
    ContentHash     string
    RepoFingerprint string
    Status          string
    SkillMD         string
    ApprovedBy      string
    ApprovedAt      *time.Time
    Reason          string
    RedactedSample  string
    CreatedAt       time.Time
}

type InsertParams struct {
    Scope           string
    Slug            string
    ContentHash     string
    RepoFingerprint string
    SkillMD         string
    RedactedSample  string
    CreatedAt       time.Time
}

type Store interface {
    Insert(ctx context.Context, p InsertParams) (Candidate, error)
    GetByID(ctx context.Context, id int64) (Candidate, error)
    ListPending(ctx context.Context, limit, offset int32) ([]Candidate, error)
    Approve(ctx context.Context, id int64, approvedBy, reason string, approvedAt time.Time) (Candidate, error)
    Reject(ctx context.Context, id int64, reason string) (Candidate, error)
    MarkPromoted(ctx context.Context, id int64) (Candidate, error)
    LookupApproval(ctx context.Context, scope, slug, contentHash, repoFingerprint string) (*Candidate, error)
}
```

## 实施约束

- 唯一约束 `(scope, slug, content_hash, repo_fingerprint)` 是审批缓存的命中键；**禁止**降级成 `(name, hash)` 缓存（P0 §"关键实现约束"：approval cache 必须含 `repo fingerprint(project_root/cwd)`）。
- 状态转移合法性由 store 层强制：
    - `pending_review` → `approved` / `rejected`
    - `approved` → `promoted` / `superseded`
    - `rejected` / `promoted` / `superseded` 终态，禁止再次推进
    - 非法转移返回明确 error，不静默忽略
- `Approve` 与 `Reject` 必须只在 `status='pending_review'` 时生效；通过 SQL `WHERE status='pending_review'` 防御并发推进，受影响行数为 0 时返回 sentinel error。
- `MarkPromoted` 必须只在 `status='approved'` 时生效，理由同上。
- `LookupApproval` 入参 `repo_fingerprint` 为空字符串时也要按字面值匹配（不要把空当通配），避免跨 repo 命中。
- 所有时间戳用 `TIMESTAMPTZ`（与 `migrations/0046_session_insights.sql`、`migrations/0060_turn_dedupe_registry.sql` 对齐；sqlc 在 nullable 列上生成 `pgtype.Timestamptz`，store 层负责转换为 `*time.Time`）。

## 验收标准

### 必测项

- `TestStore_Insert_Roundtrip`：Insert 返回的 Candidate 字段与入参一致。
- `TestStore_UniqueConstraint_RejectsDuplicate`：相同 `(scope, slug, content_hash, repo_fingerprint)` 第二次 Insert 必须 error（PG unique violation）。
- `TestStore_LookupApproval_ScopedByRepoFingerprint`：相同 `slug + content_hash` 但不同 `repo_fingerprint` 不互相命中。
- `TestStore_Approve_RejectsTerminalTransition`：从 `rejected` / `promoted` / `superseded` 调 `Approve` 必须 error。
- `TestStore_MarkPromoted_RequiresApprovedStatus`：未经 `Approve` 直接 `MarkPromoted` 必须 error。
- `TestStore_ListPending_Pagination`：插入 N 条 pending，分页拉取顺序按 `created_at` 升序、跨页无重复无遗漏。
- `TestStore_Reject_DoesNotAffectOtherRows`：拒绝一条不影响其他 pending 行。

### 命令

```bash
make sqlc-generate
go test ./internal/store/skillcandidate/...
```

### 集成验证

- migration 单向 up；用 `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS` 让 up 幂等（与 `migrations/0046_session_insights.sql`、`migrations/0060_turn_dedupe_registry.sql` 对齐）。应用 migration 后 `make sqlc-generate` 必须 pass，生成产物含 `SkillCandidate` 类型与 7 个查询方法（`InsertSkillCandidate` / `GetSkillCandidateByID` / `ListPendingSkillCandidates` / `ApproveSkillCandidate` / `RejectSkillCandidate` / `MarkSkillCandidatePromoted` / `LookupSkillCandidateApproval`）。
- `internal/store/module.go` fx wiring 启动后能注入 `Store`。

## 已知风险 / 反模式

- **错把 `repo_fingerprint` 当可选字段**：默认值 `''` 只是 schema 兼容；project scope 写入时业务层必须填充非空（约束在 Step 4 / Step 5 强制）。
- **降级成 `(name, hash)` 缓存**：会导致同名同 hash skill 在不同项目间复用旧批准，违反 P0 §"关键实现约束"。
- **状态机在应用层而非 SQL 层强制**：并发 approve / reject 同一行时容易竞态；必须用 `WHERE status='...'` 在 SQL 层过滤。
- **migration 编号回退**：当前已用到 `0063`，必须用 `0064`，原 P0 文档的 `0047` 已过时。
