-- 0064_skill_candidates.sql — P0b Step 1: durable buffer for self-learned
-- skill candidates that the extractor (Step 4) writes and the review gate
-- (Step 5) reads / approves / rejects / promotes.
--
-- Lifetime / status machine (enforced by the queries in
-- sql/queries/skill_candidate.sql; SQL layer guards listed below):
--   * pending_review  initial state stamped by the extractor.
--   * approved        reviewer accepted; SKILL.md await CreateSkill landing.
--   * rejected        reviewer declined (terminal).
--   * promoted        CreateSkill landed (terminal).
--   * superseded      replaced by a newer candidate row (terminal).
--
-- Why a unique key on (scope, slug, content_hash, repo_fingerprint):
-- this is the approval-cache hit key. The extractor consults
-- LookupSkillCandidateApproval before re-running the LLM evaluator so
-- the same candidate hash within the same project (or system scope)
-- short-circuits to the prior decision. Dropping repo_fingerprint
-- from the key would cross-pollinate approvals between projects, which
-- the P0 plan section "approval cache" explicitly forbids.
--
-- Field notes:
--   * skill_md is reserved for Step 5: ApproveCandidate calls into the
--     P0a CreateSkill flow which needs the full SKILL.md text, so the
--     extractor (Step 4) must persist it on Insert. Default '' keeps
--     legacy / backfill rows valid.
--   * repo_fingerprint default '' is purely a schema concession; the
--     business layer for project-scope writes MUST populate a
--     non-empty fingerprint. Step 4 / Step 5 enforce this.
--   * approved_at / approved_by / reason are stamped by the Approve
--     query and stay empty for non-approved rows.
--
-- Migrations are forward-only (no down script). Idempotent up via
-- IF NOT EXISTS, mirroring 0046_session_insights.sql and
-- 0060_turn_dedupe_registry.sql.

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
